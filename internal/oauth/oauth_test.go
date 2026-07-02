package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// fakeClients is an in-memory ClientValidator with one known credential.
type fakeClients struct {
	key, secret string
	tnt         tenant.Tenant
}

func (f fakeClients) ClientByKey(_ context.Context, k string) (tenant.Tenant, error) {
	if k == f.key {
		return f.tnt, nil
	}
	return tenant.Tenant{}, errors.New("unknown client")
}

func (f fakeClients) ValidateClient(_ context.Context, k, s string) (tenant.Tenant, error) {
	if k == f.key && s == f.secret {
		return f.tnt, nil
	}
	return tenant.Tenant{}, errors.New("invalid client")
}

func newTestServer() (*AuthServer, fakeClients) {
	fc := fakeClients{key: "mck_abc", secret: "s3cr3t", tnt: tenant.Tenant{TeamID: "team1", UserID: "u1", Role: tenant.RoleAdmin}}
	s, _ := NewSealer("unit-test-secret")
	return NewAuthServer("https://mcp.test", s, fc, nil), fc
}

// pkcePair returns a verifier and its S256 challenge.
func pkcePair(verifier string) (string, string) {
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorizeAndGetCode runs /authorize and returns the issued code.
func authorizeAndGetCode(t *testing.T, a *AuthServer, clientID, redirect, challenge string) string {
	t.Helper()
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", "xyz")
	req := httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	a.Authorize(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("authorize status=%d body=%s", w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Query().Get("state") != "xyz" {
		t.Fatalf("state not echoed: %s", w.Header().Get("Location"))
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}
	return code
}

// postToken posts a token request and returns the recorder.
func postToken(a *AuthServer, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.Token(w, req)
	return w
}

func TestSealerRoundTripAndTamper(t *testing.T) {
	s, _ := NewSealer("k")
	tok, err := s.sealPayload(payload{Kind: kindAccess, TeamID: "t", Exp: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.openPayload(tok, kindAccess, 0); err != nil {
		t.Fatalf("roundtrip failed: %v", err)
	}
	// Wrong kind is rejected.
	if _, err := s.openPayload(tok, kindCode, 0); err == nil {
		t.Fatal("expected kind mismatch error")
	}
	// Tampering flips integrity.
	if _, err := s.openPayload(tok[:len(tok)-2]+"xy", kindAccess, 0); err == nil {
		t.Fatal("expected tamper rejection")
	}
	// A token from a different key cannot be opened.
	other, _ := NewSealer("different")
	if _, err := other.openPayload(tok, kindAccess, 0); err == nil {
		t.Fatal("foreign-key token must not open")
	}
}

func TestSealerExpiry(t *testing.T) {
	s, _ := NewSealer("k")
	tok, _ := s.sealPayload(payload{Kind: kindAccess, Exp: 1000})
	if _, err := s.openPayload(tok, kindAccess, 999); err != nil {
		t.Fatalf("should be valid before expiry: %v", err)
	}
	if _, err := s.openPayload(tok, kindAccess, 1001); !errors.Is(err, errExpired) {
		t.Fatalf("expected errExpired, got %v", err)
	}
}

func TestVerifyPKCE(t *testing.T) {
	v, c := pkcePair("the-verifier-string-long-enough")
	if !verifyPKCE(v, c) {
		t.Fatal("matching verifier should pass")
	}
	if verifyPKCE("wrong", c) {
		t.Fatal("wrong verifier must fail")
	}
	if verifyPKCE("", c) || verifyPKCE(v, "") {
		t.Fatal("empty inputs must fail")
	}
}

func TestFullAuthCodeFlow(t *testing.T) {
	a, fc := newTestServer()
	redirect := "https://claude.ai/api/mcp/auth_callback"
	verifier, challenge := pkcePair("verifier-abc-123-xyz")

	code := authorizeAndGetCode(t, a, fc.key, redirect, challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirect)
	form.Set("code_verifier", verifier)
	form.Set("client_id", fc.key)
	form.Set("client_secret", fc.secret)
	w := postToken(a, form)
	if w.Code != http.StatusOK {
		t.Fatalf("token status=%d body=%s", w.Code, w.Body.String())
	}
	var tok struct {
		Access  string `json:"access_token"`
		Type    string `json:"token_type"`
		Refresh string `json:"refresh_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tok)
	if tok.Type != "Bearer" || tok.Access == "" || tok.Refresh == "" {
		t.Fatalf("unexpected token response: %s", w.Body.String())
	}

	// The access token resolves to the right tenant.
	tn, err := a.ResolveBearer(context.Background(), tok.Access)
	if err != nil {
		t.Fatalf("ResolveBearer: %v", err)
	}
	if tn.TeamID != "team1" || tn.Role != tenant.RoleAdmin {
		t.Fatalf("wrong tenant: %+v", tn)
	}

	// Refresh yields a new working access token (client must re-authenticate).
	rf := url.Values{}
	rf.Set("grant_type", "refresh_token")
	rf.Set("refresh_token", tok.Refresh)
	rf.Set("client_id", fc.key)
	rf.Set("client_secret", fc.secret)
	w2 := postToken(a, rf)
	if w2.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", w2.Code, w2.Body.String())
	}
	var tok2 struct {
		Access string `json:"access_token"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &tok2)
	if _, err := a.ResolveBearer(context.Background(), tok2.Access); err != nil {
		t.Fatalf("refreshed access invalid: %v", err)
	}
}

// TestResolveBearerRechecksRevocation confirms an access token stops resolving the
// moment its minting key is revoked/removed in the DB — even though the sealed
// token is still cryptographically valid and unexpired. This is the OAuth-path
// half of revoke-on-remove: without the DB re-check, a removed member's live
// token would keep working until it expired.
func TestResolveBearerRechecksRevocation(t *testing.T) {
	// mutableClients lets the test flip a key from valid to revoked between minting
	// and resolving, standing in for RemoveMember revoking the api_key.
	sealer, _ := NewSealer("unit-test-secret")
	mc := &mutableClients{key: "mck_abc", tnt: tenant.Tenant{TeamID: "team1", UserID: "u1", Role: tenant.RoleAdmin}}
	a := NewAuthServer("https://mcp.test", sealer, mc, nil)

	// Mint a valid, unexpired access token for the key (same seal the /token
	// endpoint uses, minus the HTTP plumbing).
	access, err := sealer.sealPayload(payload{
		Kind: kindAccess, TeamID: mc.tnt.TeamID, UserID: mc.tnt.UserID,
		Role: string(mc.tnt.Role), ClientKey: mc.key, Exp: a.now() + 3600,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := a.ResolveBearer(context.Background(), access); err != nil {
		t.Fatalf("precondition: fresh token should resolve, got %v", err)
	}

	// The member is removed → their key is revoked. ClientByKey now rejects it.
	mc.revoked = true
	if _, err := a.ResolveBearer(context.Background(), access); err == nil {
		t.Fatal("revoked key: access token still resolved, want rejection")
	}
}

// mutableClients is a ClientValidator whose key can be flipped to revoked, so a
// test can simulate a key that becomes invalid after a token was minted.
type mutableClients struct {
	key     string
	tnt     tenant.Tenant
	revoked bool
}

func (m *mutableClients) ClientByKey(_ context.Context, k string) (tenant.Tenant, error) {
	if k == m.key && !m.revoked {
		return m.tnt, nil
	}
	return tenant.Tenant{}, errors.New("unknown or revoked client")
}

func (m *mutableClients) ValidateClient(_ context.Context, k, s string) (tenant.Tenant, error) {
	return tenant.Tenant{}, errors.New("not used")
}

func TestAuthCodeIsSingleUse(t *testing.T) {
	a, fc := newTestServer()
	redirect := "https://claude.ai/cb"
	verifier, challenge := pkcePair("verifier-single-use")
	code := authorizeAndGetCode(t, a, fc.key, redirect, challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirect)
	form.Set("code_verifier", verifier)
	form.Set("client_id", fc.key)
	form.Set("client_secret", fc.secret)

	if w := postToken(a, form); w.Code != http.StatusOK {
		t.Fatalf("first redemption should succeed: %d %s", w.Code, w.Body.String())
	}
	// Replaying the exact same code must be refused.
	if w := postToken(a, form); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatalf("replay should be invalid_grant, got %d %s", w.Code, w.Body.String())
	}
}

func TestRefreshRequiresClientSecret(t *testing.T) {
	a, fc := newTestServer()
	redirect := "https://claude.ai/cb"
	verifier, challenge := pkcePair("verifier-refresh")
	code := authorizeAndGetCode(t, a, fc.key, redirect, challenge)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirect)
	form.Set("code_verifier", verifier)
	form.Set("client_id", fc.key)
	form.Set("client_secret", fc.secret)
	var tok struct {
		Refresh string `json:"refresh_token"`
	}
	_ = json.Unmarshal(postToken(a, form).Body.Bytes(), &tok)

	// Refresh without the secret is rejected (a stolen refresh token is useless).
	bad := url.Values{}
	bad.Set("grant_type", "refresh_token")
	bad.Set("refresh_token", tok.Refresh)
	if w := postToken(a, bad); w.Code != http.StatusUnauthorized {
		t.Fatalf("refresh without secret should be 401, got %d %s", w.Code, w.Body.String())
	}
}

func TestTokenRejectsBadPKCE(t *testing.T) {
	a, fc := newTestServer()
	redirect := "https://claude.ai/cb"
	_, challenge := pkcePair("real-verifier")
	code := authorizeAndGetCode(t, a, fc.key, redirect, challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirect)
	form.Set("code_verifier", "WRONG-verifier")
	form.Set("client_id", fc.key)
	form.Set("client_secret", fc.secret)
	w := postToken(a, form)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatalf("expected invalid_grant, got %d %s", w.Code, w.Body.String())
	}
}

func TestTokenRejectsBadSecret(t *testing.T) {
	a, fc := newTestServer()
	redirect := "https://claude.ai/cb"
	verifier, challenge := pkcePair("verifier-2")
	code := authorizeAndGetCode(t, a, fc.key, redirect, challenge)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirect)
	form.Set("code_verifier", verifier)
	form.Set("client_id", fc.key)
	form.Set("client_secret", "WRONG-secret")
	w := postToken(a, form)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid_client") {
		t.Fatalf("expected invalid_client, got %d %s", w.Code, w.Body.String())
	}
}

func TestAuthorizeRejectsUnknownClientAndPlainPKCE(t *testing.T) {
	a, fc := newTestServer()
	_, challenge := pkcePair("v")

	// Unknown client.
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "mck_nope")
	q.Set("redirect_uri", "https://claude.ai/cb")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	w := httptest.NewRecorder()
	a.Authorize(w, httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown client should 400, got %d", w.Code)
	}

	// Known client but non-S256 method is refused.
	q.Set("client_id", fc.key)
	q.Set("code_challenge_method", "plain")
	w2 := httptest.NewRecorder()
	a.Authorize(w2, httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil))
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("plain PKCE should 400, got %d", w2.Code)
	}
}

func TestChallengeHeader(t *testing.T) {
	a, _ := newTestServer()
	w := httptest.NewRecorder()
	a.Challenge(w)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("challenge status=%d", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "oauth-protected-resource") {
		t.Fatalf("WWW-Authenticate missing resource_metadata: %q", got)
	}
}
