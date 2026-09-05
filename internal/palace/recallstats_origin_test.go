package palace

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
)

// TestASearchRecordsTheOriginItsContextCarries is ADR-054 T1: a search made on a
// connection that declared an origin writes it into its search_events row, and
// one made without writes ”. The origin is the CALLER's, carried in the context
// the way the default wing is — never a query argument, so no agent can forget
// it or set it.
func TestASearchRecordsTheOriginItsContextCarries(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	const team = "t-origin"
	if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the ledger service owns invoice numbering"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Each row is read back by its own search id: two searches in one second
	// share a created_at, and "the newest" is then whichever id sorts last.
	originOf := func(searchID string) string {
		t.Helper()
		var row searchEventRow
		if err := svc.repo.db.Model(&searchEventRow{}).Where("id = ?", searchID).First(&row).Error; err != nil {
			t.Fatalf("read search event %s: %v", searchID, err)
		}
		return row.Origin
	}

	with, err := svc.SearchPage(auth.WithOrigin(ctx, "hook:agentsmemory-recall-hook.sh"), team,
		SearchQuery{Wing: "wing_acme", Query: "who owns invoice numbering", Limit: 5})
	if err != nil {
		t.Fatalf("search with origin: %v", err)
	}
	if got := originOf(with.SearchID); got != "hook:agentsmemory-recall-hook.sh" {
		t.Fatalf("search_events.origin = %q, want the context's origin", got)
	}

	without, err := svc.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Query: "who owns invoice numbering", Limit: 5})
	if err != nil {
		t.Fatalf("search without origin: %v", err)
	}
	if got := originOf(without.SearchID); got != "" {
		t.Fatalf("a search on a context with no origin recorded %q; a person's search must read as ''", got)
	}
}
