package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
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
