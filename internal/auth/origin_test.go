package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestTheBridgeLiftsTheOriginHeader is ADR-054 T1's bridge half: the origin a
// client declares travels the route the wing already does — one header, lifted
// into the context at the one place per request where HTTP is still visible.
// Header only: the wing's query-parameter form exists for a registration channel
// (Cursor's URL) that carries no origin by construction.
func TestTheBridgeLiftsTheOriginHeader(t *testing.T) {
	with := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	with.Header.Set(mcpprotocol.OriginHeader, "hook:agentsmemory-task-recall-hook.sh")
	if got := OriginFrom(Bridge(context.Background(), with)); got != "hook:agentsmemory-task-recall-hook.sh" {
		t.Errorf("origin = %q, want the header's value", got)
	}

	without := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if got := OriginFrom(Bridge(context.Background(), without)); got != "" {
		t.Errorf("a request with no origin header yielded %q, want ''", got)
	}

	query := httptest.NewRequest(http.MethodPost, "/mcp?origin=hook:x", nil)
	if got := OriginFrom(Bridge(context.Background(), query)); got != "" {
		t.Errorf("a query parameter set the origin (%q); only the header may", got)
	}
}

// ---- ADR-049 T1s three rebind tests, restored 2026-09-05. They were deleted by
// 71455db (ADR-054 T1), which rewrote this file without saying so; the guard
// stayed and had no behaviour test for a week. Found by a quality-harness
// read-only gate: the task table and its fence named tests that existed nowhere.

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
		// wantHeader is the header the refusal must NAME. Without it a mutation
		// that blames "Origin" for a bad Host still refuses, and the operator is
		// sent to look at the wrong header — a refusal that misidentifies its
		// cause is most of the cost of this class of bug.
		wantHeader string
	}{
		{name: "loopback ip with port", host: "127.0.0.1:8080", wantBad: false},
		{name: "localhost with port", host: "localhost:8080", wantBad: false},
		{name: "bare localhost", host: "localhost", wantBad: false},
		{name: "ipv6 loopback bracketed", host: "[::1]:8080", wantBad: false},
		{name: "any loopback in 127/8", host: "127.9.9.9:8080", wantBad: false},
		{name: "ephemeral port still local", host: "127.0.0.1:54321", wantBad: false},
		{name: "loopback host and loopback origin", host: "127.0.0.1:8080", origin: "http://localhost:8080", wantBad: false},

		// The rebind itself: the name resolved to 127.0.0.1, so the packet
		// A --socket client. The dial has no network host, so the proxy mints
		// "http://unix/mcp" and the guard sees an authority that is neither
		// localhost nor an IP. The first version of this guard refused it.
		// The Unix-socket proxy mints this authority; it is a loopback name so it
		// needs no exemption. TestTheSocketPlaceholderIsAcceptedByTheGuard pins it.
		{name: "the socket proxy placeholder", host: "localhost", wantBad: false},
		// "unix" is no longer special: the placeholder is a loopback name now, so
		// a single-label authority is refused like any other off-machine name.
		{name: "the retired unix authority", host: "unix", wantBad: true, wantHeader: "Host"},
		// A trailing dot is the fully-qualified spelling of the same name.
		{name: "fully-qualified localhost", host: "localhost.:8080", wantBad: false},
		// An absent Host is refused rather than skipped: a check whose job is to
		// refuse what it cannot vouch for must not treat "nothing" as "fine".
		{name: "an absent Host", host: "", wantBad: true, wantHeader: "Host"},
		// arrived here, but both headers still say where the browser thought it
		// was going. Either one alone is enough to refuse.
		{name: "rebound name in both headers", host: "evil.example.com:8080", origin: "http://evil.example.com:8080", wantBad: true},
		{name: "rebound name in host only", host: "evil.example.com:8080", wantBad: true, wantHeader: "Host"},
		{name: "foreign origin on a local host", host: "127.0.0.1:8080", origin: "https://evil.example.com", wantBad: true, wantHeader: "Origin"},

		// A sandboxed iframe or a file:// page sends the literal "null". It is
		// not this machine, and it must not read as "no Origin header".
		{name: "null origin is not absent", host: "127.0.0.1:8080", origin: "null", wantBad: true, wantHeader: "Origin"},
		{name: "unparseable origin", host: "127.0.0.1:8080", origin: "::::", wantBad: true, wantHeader: "Origin"},

		// A LAN address is off-machine even though it is private. Local mode
		// serves one machine, not one network.
		{name: "private lan address", host: "192.168.1.5:8080", wantBad: true, wantHeader: "Host"},
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
			if tc.wantBad && tc.wantHeader != "" && !strings.HasPrefix(got, tc.wantHeader+" ") {
				t.Errorf("refusal = %q, want it to name %q", got, tc.wantHeader)
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

			// BOTH METHODS, because the middleware guards three endpoints and
			// /stats is a GET. A guard written `if r.Method == http.MethodPost`
			// would pass a POST-only test while leaving every GET unguarded.
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				reached = false
				req := httptest.NewRequest(method, "/mcp", nil)
				req.Host = tc.host
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				if rec.Code != tc.wantCode {
					t.Errorf("%s status = %d, want %d", method, rec.Code, tc.wantCode)
				}
				if reached != tc.wantReach {
					t.Errorf("%s handler reached = %v, want %v", method, reached, tc.wantReach)
				}
			}
		})
	}
}

// TestARebindOutranksAMissingToken pins which refusal WINS when both apply.
//
// ⚠ It was called ...BeforeTheTokenIsRead and a review corrected the name: it
// observes the status code, so it binds PRECEDENCE, not literal read order. An
// implementation that read the bearer first and still answered 403 would pass,
// correctly — the name promised something the assertion cannot see.
//
// The order is a diagnostic decision rather than a security one — a browser on a
// rebound name holds no token either way, so both orders refuse. But answering
// 401 sends the operator hunting for a credential problem that does not exist,
// and the whole cost of this class of bug is the time between seeing the symptom
// and understanding it.
func TestARebindOutranksAMissingToken(t *testing.T) {
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
