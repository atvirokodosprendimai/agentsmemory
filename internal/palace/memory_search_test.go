package palace

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// TestSurvivorsFromCarriesStaleIndex: a hit served from the source of truth
// while the index is behind must say so, or the ADR-033 headline ("must serve
// the truth, never an empty answer — and must say so") is half a promise. The
// flag rides on the store.SearchResult into searchCandidates; survivorsFrom is
// where the flag would be dropped if nothing read it, so the stamp lives here.
func TestSurvivorsFromCarriesStaleIndex(t *testing.T) {
	rows := map[string]Drawer{
		"id-1": {ID: "id-1", Wing: "w", Room: "r", Content: "one"},
		"id-2": {ID: "id-2", Wing: "w", Room: "r", Content: "two"},
	}
	hits := []store.Hit{
		{ID: "id-1", Score: 0.5},
		{ID: "id-2", Score: 0.7},
	}

	stale, _, _ := survivorsFrom(hits, rows, SearchQuery{Wing: "w"}, true)
	if len(stale) != 2 {
		t.Fatalf("survivors = %d; want 2", len(stale))
	}
	for _, h := range stale {
		if !h.StaleIndex {
			t.Errorf("hit %s lost the stale flag at the survivor seam", h.MemoryID)
		}
	}

	fresh, _, _ := survivorsFrom(hits, rows, SearchQuery{Wing: "w"}, false)
	for _, h := range fresh {
		if h.StaleIndex {
			t.Errorf("hit %s is flagged stale on a healthy read", h.MemoryID)
		}
	}
}
