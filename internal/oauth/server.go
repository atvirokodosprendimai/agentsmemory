package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// ClientValidator validates OAuth client credentials against the app's own
// api_keys — the "authcounterapi" role, merged in-process. *tenant.Repo
// implements it. ClientByKey checks only the public client_id (used at
// /authorize); ValidateClient also verifies the secret (used at /token).
type ClientValidator interface {
	ClientByKey(ctx context.Context, clientKey string) (tenant.Tenant, error)
	ValidateClient(ctx context.Context, clientKey, secret string) (tenant.Tenant, error)
}

// RawResolver resolves a non-OAuth bearer (a direct project API token), so the
// same /mcp endpoint serves both OAuth clients (claude.ai) and direct callers.
type RawResolver interface {
	ResolveToken(ctx context.Context, plaintext string) (tenant.Tenant, error)
}

// AuthServer is the stateless OAuth 2.1 authorization server + resource gate.
type AuthServer struct {
	issuer  string // public base URL, no trailing slash, no /mcp
	sealer  *Sealer
	clients ClientValidator
	raw     RawResolver // optional; enables direct-token access alongside OAuth

	accessTTL  time.Duration
	refreshTTL time.Duration
	codeTTL    time.Duration
	now        func() int64 // unix seconds; injectable for tests
}

// NewAuthServer builds an AuthServer. issuer must be the public base URL of the
// deployment (e.g. https://mcp.example.com) with no trailing slash and no /mcp.
func NewAuthServer(issuer string, sealer *Sealer, clients ClientValidator, raw RawResolver) *AuthServer {
	return &AuthServer{
		issuer:     strings.TrimRight(issuer, "/"),
		sealer:     sealer,
		clients:    clients,
		raw:        raw,
		accessTTL:  time.Hour,
		refreshTTL: 30 * 24 * time.Hour,
		codeTTL:    5 * time.Minute,
		now:        func() int64 { return time.Now().Unix() },
	}
}

// resourceURL is the protected MCP resource this AS guards.
func (a *AuthServer) resourceURL() string { return a.issuer + "/mcp" }

// metadataURL is where the protected-resource metadata is served.
func (a *AuthServer) metadataURL() string {
	return a.issuer + "/.well-known/oauth-protected-resource"
}

// --- discovery metadata ---

// ProtectedResourceMetadata serves RFC 9728: it tells the client which
// authorization server guards this MCP resource.
func (a *AuthServer) ProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              a.resourceURL(),
		"authorization_servers": []string{a.issuer},
	})
}

// AuthorizationServerMetadata serves RFC 8414: the AS endpoints and the fact
// that only PKCE S256 is accepted.
func (a *AuthServer) AuthorizationServerMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                a.issuer,
		"authorization_endpoint":                a.issuer + "/authorize",
		"token_endpoint":                        a.issuer + "/token",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic", "none"},
	})
}

// --- /authorize ---

// Authorize handles the authorization request. It auto-approves (the credential
// IS the account, so there is no login/consent UI), but only after confirming
// the client_id maps to a real, non-revoked key. The secret is NOT checked here
// — only the public client_id is present — so a PKCE-bound code is issued and
// the secret is verified later at /token.
func (a *AuthServer) Authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	challenge := q.Get("code_challenge")

	// Validate the request shape before trusting the redirect target.
	if q.Get("response_type") != "code" || challenge == "" ||
		q.Get("code_challenge_method") != "S256" || !validRedirect(redirectURI) {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)
		return
	}

	t, err := a.clients.ClientByKey(r.Context(), clientID)
	if err != nil {
		// Unknown/revoked client: cannot safely redirect (redirect_uri is
		// unverified), so fail visibly instead of bouncing to an arbitrary URL.
		http.Error(w, "unknown client", http.StatusBadRequest)
		return
	}

	code, err := a.sealer.sealPayload(payload{
		Kind: kindCode, TeamID: t.TeamID, UserID: t.UserID, Role: string(t.Role),
		ClientKey: clientID, RedirectURI: redirectURI, CodeChallenge: challenge,
		Exp: a.now() + int64(a.codeTTL.Seconds()),
	})
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	dst, _ := url.Parse(redirectURI)
	rq := dst.Query()
	rq.Set("code", code)
	if state != "" {
		rq.Set("state", state)
	}
	dst.RawQuery = rq.Encode()
	http.Redirect(w, r, dst.String(), http.StatusFound)
}

// --- /token ---

// Token handles the authorization_code and refresh_token grants. Errors follow
// RFC 6749 §5.2 (JSON {error}, 400 for grant/request, 401 for client).
func (a *AuthServer) Token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		a.tokenAuthCode(w, r)
	case "refresh_token":
		a.tokenRefresh(w, r)
	default:
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
}

// tokenAuthCode exchanges a PKCE-bound code (+ verified client secret) for a
// fresh access/refresh pair.
func (a *AuthServer) tokenAuthCode(w http.ResponseWriter, r *http.Request) {
	clientID, secret := clientCreds(r)

	code, err := a.sealer.openPayload(r.Form.Get("code"), kindCode, a.now())
	if err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	// Bind the code to this exchange: same redirect_uri, same client, valid PKCE.
	if code.RedirectURI != r.Form.Get("redirect_uri") || code.ClientKey != clientID {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	if !verifyPKCE(r.Form.Get("code_verifier"), code.CodeChallenge) {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	// The secret is proven only now, at the token endpoint.
	t, err := a.clients.ValidateClient(r.Context(), clientID, secret)
	if err != nil {
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	a.issueTokens(w, t, clientID)
}

// tokenRefresh re-issues an access/refresh pair from a valid refresh token,
// re-checking that the client key has not been revoked since (the sealed
// refresh token is itself proof of the original authorization).
func (a *AuthServer) tokenRefresh(w http.ResponseWriter, r *http.Request) {
	rt, err := a.sealer.openPayload(r.Form.Get("refresh_token"), kindRefresh, a.now())
	if err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	t, err := a.clients.ClientByKey(r.Context(), rt.ClientKey)
	if err != nil {
		// Key revoked or gone: the refresh is no longer honoured.
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	a.issueTokens(w, t, rt.ClientKey)
}

// issueTokens mints and writes the sealed access + refresh tokens.
func (a *AuthServer) issueTokens(w http.ResponseWriter, t tenant.Tenant, clientKey string) {
	now := a.now()
	access, err1 := a.sealer.sealPayload(payload{
		Kind: kindAccess, TeamID: t.TeamID, UserID: t.UserID, Role: string(t.Role),
		ClientKey: clientKey, Exp: now + int64(a.accessTTL.Seconds()),
	})
	refresh, err2 := a.sealer.sealPayload(payload{
		Kind: kindRefresh, TeamID: t.TeamID, UserID: t.UserID, Role: string(t.Role),
		ClientKey: clientKey, Exp: now + int64(a.refreshTTL.Seconds()),
	})
	if err1 != nil || err2 != nil {
		tokenError(w, http.StatusInternalServerError, "server_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(a.accessTTL.Seconds()),
		"refresh_token": refresh,
	})
}

// --- resource gate ---

// ResolveBearer opens a sealed access token into its tenant. It is the per-
// request validation: no database hit, integrity guaranteed by the GCM seal.
func (a *AuthServer) ResolveBearer(token string) (tenant.Tenant, error) {
	p, err := a.sealer.openPayload(token, kindAccess, a.now())
	if err != nil {
		return tenant.Tenant{}, err
	}
	return tenant.Tenant{TeamID: p.TeamID, UserID: p.UserID, Role: tenant.Role(p.Role)}, nil
}

// Challenge writes the 401 that makes an MCP client begin OAuth: a
// WWW-Authenticate header pointing at the protected-resource metadata (RFC 9728).
func (a *AuthServer) Challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate",
		`Bearer resource_metadata="`+a.metadataURL()+`"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// resolve turns a bearer token into a tenant, trying a sealed OAuth access token
// first, then a raw project API token. This is the one seam where both credential
// kinds converge.
func (a *AuthServer) resolve(ctx context.Context, token string) (tenant.Tenant, bool) {
	if token == "" {
		return tenant.Tenant{}, false
	}
	if t, err := a.ResolveBearer(token); err == nil {
		return t, true
	}
	if a.raw != nil {
		if t, err := a.raw.ResolveToken(ctx, token); err == nil {
			return t, true
		}
	}
	return tenant.Tenant{}, false
}

// Gate wraps the /mcp handler: it resolves the bearer and, on success, injects
// the tenant into the request context for the tools; on failure it emits the
// OAuth challenge so claude.ai starts the handshake. Fails closed.
func (a *AuthServer) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := a.resolve(r.Context(), bearerToken(r)); ok {
			next.ServeHTTP(w, r.WithContext(auth.WithTenant(r.Context(), t)))
			return
		}
		a.Challenge(w)
	})
}

// --- helpers ---

// bearerToken extracts the token from an Authorization: Bearer header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

// clientCreds reads client_id/client_secret from the POST body
// (client_secret_post) or, failing that, from HTTP Basic auth
// (client_secret_basic).
func clientCreds(r *http.Request) (id, secret string) {
	id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
	if id == "" {
		if bu, bp, ok := r.BasicAuth(); ok {
			id, secret = bu, bp
		}
	}
	return id, secret
}

// validRedirect requires an absolute http/https URL with a host. Redirect URIs
// are not pre-registered (stateless), so the issued code is PKCE- and secret-
// bound and the redirect_uri is re-checked at /token — but we still refuse
// obviously unsafe targets here.
func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http"
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// tokenError writes an RFC 6749 token-endpoint error.
func tokenError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
