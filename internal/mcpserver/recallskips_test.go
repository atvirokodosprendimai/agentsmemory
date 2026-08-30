package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestRecallStatsResultCarriesTheSkipBreakdown is the rung-3 half of ADR-034.
//
// WingRecall.RerankSkips being correct is worth nothing if am_recall_stats never
// renders it: an agent reads the tool's OUTPUT, not the struct, so a field
// populated in Go and dropped at the boundary is a capability that ships
// complete and undiscoverable. No test of the aggregate can see that, by
// construction — which is exactly the defect this repository keeps shipping.
//
// It drives the real tool through the real transport, so the assertion covers
// registration, admission, aggregation and rendering together.
func TestRecallStatsResultCarriesTheSkipBreakdown(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_stats")

	if _, isErr, err := h.Call(t, "am_add_drawer", map[string]any{
		"room": "r", "content": "a memory about skip reasons and degraded rankings",
	}); err != nil || isErr {
		t.Fatalf("add drawer: err=%v isErr=%v", err, isErr)
	}
	// This harness configures no reranker, so the recall below is skipped for
	// reason=no_reranker — the case that is indistinguishable from a failing
	// cross-encoder on `reranked` alone.
	if _, isErr, err := h.Call(t, "am_search", map[string]any{"query": "skip reasons"}); err != nil || isErr {
		t.Fatalf("search: err=%v isErr=%v", err, isErr)
	}

	out, isErr, err := h.Call(t, "am_recall_stats", map[string]any{})
	if err != nil || isErr {
		t.Fatalf("recall_stats: err=%v isErr=%v out=%s", err, isErr, out)
	}
	if !strings.Contains(out, "rerank_skips") {
		t.Fatalf("am_recall_stats rendered no rerank_skips key.\n"+
			"An operator reading this tool cannot tell a cross-encoder that is switched off "+
			"from one failing on every query — both report reranked: 0, which is the whole "+
			"reason ADR-034 exists.\ngot:\n%s", out)
	}
	if !strings.Contains(out, "no_reranker") {
		t.Errorf("rerank_skips is present but carries no reason for a recall that was "+
			"definitely skipped; an always-empty key reads as 'measured, and it was fine'.\ngot:\n%s", out)
	}
}
