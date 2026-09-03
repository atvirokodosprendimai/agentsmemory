// Package auth is the boundary that turns an inbound HTTP credential into a
// resolved tenant on the request context. It is the bridge between the network
// edge (a Bearer token on the MCP HTTP connection) and the rest of the system,
// which only ever sees a *tenant.Tenant — never a raw token.
//
// Phase 1 is bearer tokens (per-agent API keys). The seam is deliberately a
// single context-injection function so OAuth 2.1 can be added later behind the
// same boundary without touching any tool handler.
package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// ctxKey is an unexported context key type so no other package can collide with
// or overwrite the stored tenant.
type ctxKey struct{}

// tenantKey is the single context key under which the resolved tenant is stored.
var tenantKey = ctxKey{}

// wingKeyType / wingKey carry the connection's DEFAULT WING — the project this
// MCP registration belongs to.
//
// A palace holds every project an agent works on, and wings are how they stay
// apart. The server cannot infer which project a caller is in: it speaks HTTP,
// and the working directory is on the other side of the wire. Asking the agent to
// remember a convention works until it forgets, and then one project's decisions
// are filed into another's wing, permanently.
//
// So the wing travels with the CONNECTION instead: whoever registers the MCP for
// a project states it once, and every write from that registration lands in the
// right wing whether or not the agent was paying attention.
type wingKeyType struct{}

var wingKey = wingKeyType{}

// wingQueryParam is the URL alternative to mcpprotocol.WingHeader for clients
// that cannot attach custom headers to an MCP registration.
const wingQueryParam = "wing"

// Resolver resolves a plaintext bearer token to a tenant. *tenant.Repo
// satisfies it; defining the interface here (consumer side) keeps auth
// decoupled from gorm.
type Resolver interface {
	ResolveToken(ctx context.Context, plaintext string) (tenant.Tenant, error)
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header, returning "" when absent or malformed.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// TenantFrom returns the resolved tenant on the context. ok is false when the
// request was unauthenticated or the token did not resolve, so every tool can
// fail closed with a single check.
func TenantFrom(ctx context.Context) (tenant.Tenant, bool) {
	t, ok := ctx.Value(tenantKey).(tenant.Tenant)
	return t, ok
}

// WithTenant returns a context carrying the given tenant. Exists for tests and
// for any in-process caller that has already authenticated out of band.
func WithTenant(ctx context.Context, t tenant.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey, t)
}

// LocalTenant returns middleware that puts a fixed tenant on every request's
// context. It is the self-hosted (--local) counterpart to the OAuth gate: same
// seam, same context key, so Bridge forwards it and every tool reads it through
// TenantFrom without knowing which one ran.
//
// With an empty token it authenticates nothing by design — there is exactly one
// workspace, so there is nothing to tell apart. That makes the listen address
// the only thing standing between the caller and the whole database, which is
// why local mode defaults to binding loopback (config.LocalAddr).
//
// With a non-empty token it additionally requires "Authorization: Bearer
// <token>" and answers 401 otherwise. That is what makes a routable bind — a
// home network rather than one machine — defensible: the shared secret replaces
// the network boundary loopback was providing. It still identifies nobody; it
// only decides who gets in.
//
// The two behaviours are one function rather than two middlewares because they
// are the same seam under different exposure, and splitting them would let a
// caller mount the credential-free one by mistake. machineBounded joins them for
// the same reason: the DNS-rebinding guard protects the assumption this
// function's own comment states, so a caller must not be able to mount the
// endpoint without it. Pass it true whenever the operator's boundary is this
// machine — a loopback bind, a unix socket, or a container publishing to the
// host's loopback. Pass false when they have deliberately bound a routable
// address, because they were warned and a hard refusal would break them.
func LocalTenant(t tenant.Tenant, token string, machineBounded bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The rebinding guard runs BEFORE the token check so a refusal names
			// the addressing rather than the credential: a browser reaching a
			// rebound name has no token to present either, and "unauthorized"
			// would send an operator hunting for the wrong problem.
			if machineBounded {
				if bad := OffMachineAddressing(r); bad != "" {
					http.Error(w, "forbidden: "+bad+" does not address this machine", http.StatusForbidden)
					return
				}
			}
			// Only reject when a token is configured. Without one, a stray bearer
			// left in an agent config is ignored rather than rejected, so pointing
			// a previously-hosted agent at a local server keeps working.
			if token != "" && !tokenMatches(token, bearerToken(r)) {
				// Name the scheme so a client knows what to present; the body stays
				// terse because the only reader is an agent, not a browser.
				w.Header().Set("WWW-Authenticate", `Bearer realm="agentsmemory"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithTenant(r.Context(), t)))
		})
	}
}

// tokenMatches reports whether a presented bearer equals the configured local
// token, compared in constant time.
//
// subtle.ConstantTimeCompare is not superstition here: unlike the hosted path —
// which looks up a SHA-256 hash and so leaks nothing about the plaintext — this
// compares the secret itself, and the whole point of the token is to face a
// network where an attacker can time many attempts. It returns 0 for
// different-length inputs, so an empty or absent header fails on the same path
// as a wrong one.
func tokenMatches(want, got string) bool {
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// Bridge is an mcp-go HTTPContextFunc that carries a tenant already resolved by
// an upstream middleware (the OAuth gate) on the HTTP request into the context
// the MCP tools receive. When the gate fronts /mcp, resolution happens once
// there; this just forwards the result so the tools can read it via TenantFrom.
//
// It also lifts the connection's default wing off the request, for the same
// reason it lifts the tenant: this is the one place per request where the HTTP
// layer is still visible, and everything downstream should see decisions, not
// headers.
func Bridge(ctx context.Context, r *http.Request) context.Context {
	if t, ok := TenantFrom(r.Context()); ok {
		ctx = WithTenant(ctx, t)
	}
	if wing := requestWing(r); wing != "" {
		ctx = WithDefaultWing(ctx, wing)
	}
	return ctx
}

// requestWing reads the default wing a registration declared, header first and
// query parameter second. Empty means the registration named no project, which
// is the pre-existing behaviour: the caller passes a wing per call or the tool
// says it needs one.
func requestWing(r *http.Request) string {
	if w := strings.TrimSpace(r.Header.Get(mcpprotocol.WingHeader)); w != "" {
		return w
	}
	return strings.TrimSpace(r.URL.Query().Get(wingQueryParam))
}

// WithDefaultWing returns a context carrying the connection's default wing.
func WithDefaultWing(ctx context.Context, wing string) context.Context {
	return context.WithValue(ctx, wingKey, wing)
}

// DefaultWingFrom returns the wing this connection was registered for, or "" if
// it was registered without one. The value is UNVALIDATED — it arrived over the
// wire — so callers sanitize it exactly as they would a wing passed per call.
func DefaultWingFrom(ctx context.Context) string {
	w, _ := ctx.Value(wingKey).(string)
	return w
}
