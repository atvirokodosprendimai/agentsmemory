package mcptest_test

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestAFetchNamingItsRecallIsRecordedAtTheToolSurface is rung 2 for ADR-028 T3:
// it proves the recorder is SELECTED, not merely present.
//
// The unit tests in internal/palace call Service.RecordFetch directly, which is
// exactly the shape AGENTS.md names as this repository's characteristic defect —
// the component exercised instead of the selection. Delete either call site in
// registerGetDrawer and every one of those tests still passes, while no fetch is
// ever recorded in production. This test drives the real MCP transport and reads
// the count back through a served tool, so it dies when the wiring dies.
//
// It also pins the two properties a ratio built on this table would get wrong:
// a fetch that RESOLVED NOTHING is not a click, and a `whole` fetch of a chunked
// memory is ONE read rather than one per chunk.
func TestAFetchNamingItsRecallIsRecordedAtTheToolSurface(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_acme")

	id := firstDrawerID(t, h, h.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_acme", "room": "decisions",
		"content": "The fetch join is what turns a recall into a relevance signal",
	}))

	page := h.JSON(t, h.MustCall(t, "am_search", map[string]any{
		"query": "what turns a recall into a relevance signal", "wing": "wing_acme",
	}))
	searchID, _ := page["search_id"].(string)
	if searchID == "" {
		t.Fatal("am_search returned no search_id; ADR-028 T1 is the thing that broke, not this")
	}

	if got := fetchCount(t, h); got != 0 {
		t.Fatalf("no fetch has happened yet, want 0 got %d", got)
	}

	// A fetch that names its recall is the signal.
	h.MustCall(t, "am_get_drawer", map[string]any{"id": id, "search_id": searchID})
	if got := fetchCount(t, h); got != 1 {
		t.Fatalf("a fetch naming its recall must be recorded through the served tool, want 1 got %d "+
			"(if this is 0, the call site in registerGetDrawer is gone and the palace unit tests cannot see it)", got)
	}

	// A fetch that names no recall is not a click and records nothing.
	h.MustCall(t, "am_get_drawer", map[string]any{"id": id})
	if got := fetchCount(t, h); got != 1 {
		t.Fatalf("a fetch naming no recall must record nothing, want 1 got %d", got)
	}

	// A fetch that RESOLVED NOTHING is not a click either. Recording it would put
	// misses in the numerator of every ratio derived from this table.
	h.MustRefuse(t, "am_get_drawer", map[string]any{
		"id": "0000000000000000000000000000000000000000000000000000000000000000", "search_id": searchID,
	})
	if got := fetchCount(t, h); got != 1 {
		t.Fatalf("a fetch that resolved nothing must record nothing, want 1 got %d", got)
	}

	// `whole` on a chunked memory is ONE read. Counting chunks would weight long
	// notes higher in every count derived from this.
	long := make([]byte, 4200)
	for i := range long {
		long[i] = byte('a' + i%26)
	}
	chunked := firstDrawerID(t, h, h.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_acme", "room": "decisions", "content": string(long),
	}))
	whole := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{
		"id": chunked, "whole": true, "search_id": searchID,
	}))
	if n, _ := whole["count"].(float64); n < 2 {
		t.Fatalf("fixture must be chunked for this assertion to mean anything, got count=%v", whole["count"])
	}
	if got := fetchCount(t, h); got != 2 {
		t.Fatalf("a whole fetch of a chunked memory is ONE read, want 2 got %d", got)
	}
}

// fetchCount reads the fetch total back through am_recall_stats — the served
// surface, not the store, because a count nothing publishes is unobservable and
// that is the defect this whole task exists to close.
func fetchCount(t *testing.T, h *mcptest.Harness) int {
	t.Helper()
	stats := h.JSON(t, h.MustCall(t, "am_recall_stats", map[string]any{"hours": 24}))
	n, ok := stats["fetches"].(float64)
	if !ok {
		t.Fatalf("am_recall_stats published no `fetches` count: %v", stats)
	}
	return int(n)
}
