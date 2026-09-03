package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestTheTransportRefusesWhatItCannotServe covers the decision directly, so the
// two rules are pinned without standing up a server.
func TestTheTransportRefusesWhatItCannotServe(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		version string
		want    int
	}{
		// A GET asks for a stream this server cannot open: it is mounted
		// WithStateLess, so there is no session to push down and nothing here
		// sends notifications. mcp-go answers 200 and holds the connection.
		{name: "GET is refused, not held open", method: http.MethodGet, want: http.StatusMethodNotAllowed},

		{name: "POST is served", method: http.MethodPost, want: 0},
		{name: "DELETE is served", method: http.MethodDelete, want: 0},

		// Absent is allowed: the header is only required after initialization,
		// and refusing its absence would break every client that predates it.
		{name: "no version header", method: http.MethodPost, version: "", want: 0},
		{name: "the latest version", method: http.MethodPost, version: mcp.LATEST_PROTOCOL_VERSION, want: 0},
		{name: "a version this library knows", method: http.MethodPost, version: mcp.ValidProtocolVersions[len(mcp.ValidProtocolVersions)-1], want: 0},

		{name: "a version nobody speaks", method: http.MethodPost, version: "1999-01-01", want: http.StatusBadRequest},
		{name: "not a version at all", method: http.MethodPost, version: "banana", want: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/mcp", nil)
			if tc.version != "" {
				r.Header.Set("MCP-Protocol-Version", tc.version)
			}
			got, msg := transportRefusal(r)
			if got != tc.want {
				t.Fatalf("transportRefusal() = %d (%q), want %d", got, msg, tc.want)
			}
			if got != 0 && msg == "" {
				t.Error("a refusal must say why; the client has nothing else to go on")
			}
		})
	}
}

// TestTheAcceptedVersionsAreDerivedNotListed is the half that keeps the check
// from going stale in the direction that breaks callers.
//
// A hardcoded list refuses a client that moved to a version this library already
// speaks, and it fails closed — the caller sees 400 and cannot tell whether the
// server is old or they are wrong. Deriving from mcp.ValidProtocolVersions means
// an mcp-go upgrade widens the check on the same commit.
func TestTheAcceptedVersionsAreDerivedNotListed(t *testing.T) {
	for _, v := range mcp.ValidProtocolVersions {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		r.Header.Set("MCP-Protocol-Version", v)
		if code, msg := transportRefusal(r); code != 0 {
			t.Errorf("version %q is in mcp.ValidProtocolVersions but the transport refuses it: %d %s", v, code, msg)
		}
	}
}

// TestStreamHTTPMountsTheConformanceRules is the reachability half.
//
// transportRefusal can be perfect while StreamHTTP hands back the bare mcp-go
// handler, and every test above would still pass — the component works and
// nothing selects it, which is the defect AGENTS.md §Reachability records. This
// drives the real envelope over HTTP and reads the status a client would get.
func TestStreamHTTPMountsTheConformanceRules(t *testing.T) {
	h := StreamHTTP(New(Deps{Version: "test"}))

	// ⚠ THE GET CARRIES A DEADLINE, AND THAT IS NOT DEFENSIVE PADDING. Without the
	// wrapper this request is exactly the defect under test: mcp-go answers 200 and
	// HOLDS the connection, so the assertion never runs and the test HANGS. Measured
	// while mutation-checking this gate — severing the wrapper produced a 600s
	// timeout rather than a failure, and a gate that hangs instead of failing reads
	// as a broken CI runner rather than a broken server.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx))
	if get.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET through the real envelope = %d, want %d — the wrapper is not mounted", get.Code, http.StatusMethodNotAllowed)
	}
	if allow := get.Header().Get("Allow"); allow == "" {
		t.Error("a 405 must name the methods that are allowed")
	}

	bad := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	bad.Header.Set("MCP-Protocol-Version", "1999-01-01")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bad)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unsupported version through the real envelope = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
