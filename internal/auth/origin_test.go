package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestOffMachineAddressingNamesTheHeaderThatBetraysARebind covers the classifier
// alone, because the middleware test below can only show that SOME refusal
// happened — it cannot show that a legitimate agent still gets through on every
// spelling of "this machine". The population that must pass is wider than it
// looks: a unix-socket client sends "localhost", Docker's published port arrives
// as "127.0.0.1:8080", an IPv6 loopback arrives bracketed, and httptest picks an
// ephemeral port every run.
func TestOffMachineAddressingNamesTheHeaderThatBetraysARebind(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		origin  string
		wantBad bool
	}{
		{name: "loopback ip with port", host: "127.0.0.1:8080", wantBad: false},
		{name: "localhost with port", host: "localhost:8080", wantBad: false},
		{name: "bare localhost", host: "localhost", wantBad: false},
		{name: "ipv6 loopback bracketed", host: "[::1]:8080", wantBad: false},
		{name: "any loopback in 127/8", host: "127.9.9.9:8080", wantBad: false},
		{name: "ephemeral port still local", host: "127.0.0.1:54321", wantBad: false},
		{name: "loopback host and loopback origin", host: "127.0.0.1:8080", origin: "http://localhost:8080", wantBad: false},

		// The rebind itself: the name resolved to 127.0.0.1, so the packet
		// arrived here, but both headers still say where the browser thought it
		// was going. Either one alone is enough to refuse.
		{name: "rebound name in both headers", host: "evil.example.com:8080", origin: "http://evil.example.com:8080", wantBad: true},
		{name: "rebound name in host only", host: "evil.example.com:8080", wantBad: true},
		{name: "foreign origin on a local host", host: "127.0.0.1:8080", origin: "https://evil.example.com", wantBad: true},

		// A sandboxed iframe or a file:// page sends the literal "null". It is
		// not this machine, and it must not read as "no Origin header".
		{name: "null origin is not absent", host: "127.0.0.1:8080", origin: "null", wantBad: true},
		{name: "unparseable origin", host: "127.0.0.1:8080", origin: "::::", wantBad: true},

		// A LAN address is off-machine even though it is private. Local mode
		// serves one machine, not one network.
		{name: "private lan address", host: "192.168.1.5:8080", wantBad: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			got := OffMachineAddressing(r)
			if (got != "") != tc.wantBad {
				t.Fatalf("OffMachineAddressing() = %q, want bad=%v", got, tc.wantBad)
			}
			if tc.wantBad && got == "" {
				t.Fatal("a refusal must name the header it read")
			}
		})
	}
}

// TestLocalTenantRefusesARebindAndOnlyWhenBounded is the behaviour half: the
// guard has to fire through the real middleware, and it has to stay off where
// the operator has deliberately bound a routable address.
//
// The second case is not a formality. Somebody running --local on a LAN address
// with no token was already warned at boot and is presumably reaching it by that
// address; turning that into a 403 would be a silent breaking change delivered
// as a security fix, which is how a guard gets reverted wholesale instead of
// tuned.
func TestLocalTenantRefusesARebindAndOnlyWhenBounded(t *testing.T) {
	want := tenant.Tenant{TeamID: "team-local", UserID: "user-local", Role: tenant.RoleAdmin}

	tests := []struct {
		name      string
		bounded   bool
		host      string
		wantCode  int
		wantReach bool
	}{
		{name: "bounded refuses a rebound host", bounded: true, host: "evil.example.com:8080", wantCode: http.StatusForbidden, wantReach: false},
		{name: "bounded admits this machine", bounded: true, host: "127.0.0.1:8080", wantCode: http.StatusOK, wantReach: true},
		{name: "unbounded admits anything", bounded: false, host: "evil.example.com:8080", wantCode: http.StatusOK, wantReach: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			h := LocalTenant(want, "", tc.bounded)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if reached != tc.wantReach {
				t.Errorf("handler reached = %v, want %v", reached, tc.wantReach)
			}
		})
	}
}

// TestARebindIsRefusedBeforeTheTokenIsRead pins the ORDER, which is the part a
// later edit is most likely to get wrong by moving the cheap check after the
// expensive one.
//
// The order is a diagnostic decision rather than a security one — a browser on a
// rebound name holds no token either way, so both orders refuse. But answering
// 401 sends the operator hunting for a credential problem that does not exist,
// and the whole cost of this class of bug is the time between seeing the symptom
// and understanding it.
func TestARebindIsRefusedBeforeTheTokenIsRead(t *testing.T) {
	want := tenant.Tenant{TeamID: "team-local", UserID: "user-local", Role: tenant.RoleAdmin}

	h := LocalTenant(want, "the-configured-token", true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Host = "evil.example.com:8080"
	// No Authorization header at all, so a token-first order would answer 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d — the addressing must be judged before the credential", rec.Code, http.StatusForbidden)
	}
}
