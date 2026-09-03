package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestLocalTenantAdmitsWithoutCredential proves the self-hosted middleware puts
// the fixed workspace exactly where TenantFrom reads it, with no Authorization
// header present — the whole point being that downstream tools cannot tell a
// local request from a token-resolved one.
func TestLocalTenantAdmitsWithoutCredential(t *testing.T) {
	want := tenant.Tenant{TeamID: "team-local", UserID: "user-local", Role: tenant.RoleAdmin}

	var got tenant.Tenant
	var ok bool
	h := LocalTenant(want, "", false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = TenantFrom(r.Context())
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", nil))

	if !ok {
		t.Fatal("no tenant on context; local mode must admit an unauthenticated request")
	}
	if got != want {
		t.Errorf("tenant = %+v, want %+v", got, want)
	}
}

// TestLocalTenantIgnoresInboundCredential confirms a stray bearer token cannot
// steer TOKEN-LESS local mode: the injected workspace wins regardless of what
// the caller sends, so a leftover token in an agent's config resolves to the
// local workspace rather than failing or selecting another one.
func TestLocalTenantIgnoresInboundCredential(t *testing.T) {
	want := tenant.Tenant{TeamID: "team-local", UserID: "user-local", Role: tenant.RoleAdmin}

	var got tenant.Tenant
	h := LocalTenant(want, "", false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = TenantFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer someone-elses-token")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != want {
		t.Errorf("tenant = %+v, want %+v", got, want)
	}
}

// TestLocalTenantToken pins the gate that makes a routable --local bind
// defensible. The cases worth stating out loud: an absent header must be
// rejected exactly like a wrong one (no "no credential means skip the check"
// hole), and the scheme must actually be Bearer — sending the raw secret with no
// scheme, or under Basic, must not pass.
func TestLocalTenantToken(t *testing.T) {
	const secret = "s3cret-token"
	want := tenant.Tenant{TeamID: "team-local", UserID: "user-local", Role: tenant.RoleAdmin}

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"correct token", "Bearer " + secret, http.StatusOK},
		{"lowercase scheme accepted", "bearer " + secret, http.StatusOK},
		{"surrounding space tolerated", "Bearer   " + secret + " ", http.StatusOK},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"no header at all", "", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"secret without scheme", secret, http.StatusUnauthorized},
		{"wrong scheme", "Basic " + secret, http.StatusUnauthorized},
		// A prefix of the secret must fail: constant-time compare rejects
		// different lengths outright rather than matching what it has read.
		{"prefix of the token", "Bearer " + secret[:5], http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			h := LocalTenant(want, secret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				if got, ok := TenantFrom(r.Context()); !ok || got != want {
					t.Errorf("tenant = %+v (ok=%v), want %+v", got, ok, want)
				}
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			// The handler running is the real test of a rejection: a 401 that still
			// reached the tools would have already touched the database.
			if reached != (tc.wantStatus == http.StatusOK) {
				t.Errorf("next handler reached = %v, want %v", reached, tc.wantStatus == http.StatusOK)
			}
			if tc.wantStatus == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 without a WWW-Authenticate header; the client is not told what to present")
			}
		})
	}
}

// TestBridgeLiftsTheRegistrationWing: the wing a project's MCP registration
// declares must reach the tools, by header or by query parameter — not every MCP
// client can attach custom headers, and a URL always can.
func TestBridgeLiftsTheRegistrationWing(t *testing.T) {
	for name, build := range map[string]func() *http.Request{
		"header": func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			r.Header.Set(mcpprotocol.WingHeader, "wing_acme")
			return r
		},
		"query parameter": func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/mcp?wing=wing_acme", nil)
		},
		"header wins over query": func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/mcp?wing=wing_other", nil)
			r.Header.Set(mcpprotocol.WingHeader, "wing_acme")
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := DefaultWingFrom(Bridge(context.Background(), build())); got != "wing_acme" {
				t.Errorf("default wing = %q, want wing_acme", got)
			}
		})
	}
}

// TestBridgeWithoutAWing keeps the old behaviour intact: a registration that
// names no project leaves the wing to the caller, exactly as before.
func TestBridgeWithoutAWing(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if got := DefaultWingFrom(Bridge(context.Background(), r)); got != "" {
		t.Errorf("default wing = %q, want empty", got)
	}
}
