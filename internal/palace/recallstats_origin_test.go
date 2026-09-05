package palace

import (
	"context"
	"testing"
	"time"

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

// seedOriginSearches files three unanswered hook recalls and two unanswered
// origin-less searches into one wing, all inside the report's window.
func seedOriginSearches(t *testing.T, svc *Service, team string) {
	t.Helper()
	ctx := context.Background()
	// The two person's questions are dissimilar on purpose: groupSuggestions
	// collapses paraphrases, and two near-identical fixtures would fold into one
	// suggestion and read as a hook leaking in.
	for _, ev := range []struct{ origin, query string }{
		{"hook:recall", "task/adr-054 internal/palace/recallstats.go cmd/server/mcp.go"},
		{"hook:recall", "task/adr-054 internal/palace/recallstats.go cmd/server/mcp.go"},
		{"hook:task-recall", "so proceed"},
		{"", "how does the ledger service number invoices"},
		{"", "where is the nightly scheduler cron defined"},
	} {
		svc.repo.recordSearch(ctx, searchEventRow{
			TeamID: team, Wing: "wing_acme", Query: ev.query, Origin: ev.origin, Hits: 0, Candidates: 0,
		})
	}
}

// TestSuggestionsHoldNoHookRecalls is ADR-054 T3: a hook's automatic recall
// that found nothing is a fact about the palace worth counting; it is not a
// memory anyone should go and write. The to-write list — Unanswered and the
// Suggestions built over it — is built from the searches nobody's hook made.
func TestSuggestionsHoldNoHookRecalls(t *testing.T) {
	svc := newTestService(t)
	const team = "t-origin-stats"
	seedOriginSearches(t, svc, team)

	stats, err := svc.RecallStats(context.Background(), team, "wing_acme", time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	if len(stats.Unanswered) != 2 {
		t.Fatalf("unanswered = %v, want the two origin-less searches and neither hook recall", stats.Unanswered)
	}
	for _, s := range stats.Suggestions {
		if s.Times != 1 {
			t.Errorf("suggestion %q counted %d asks; hook recalls must not be folded into a person's", s.Query, s.Times)
		}
	}
	if n := len(stats.Suggestions); n != 2 {
		t.Errorf("suggestions = %d entries, want 2 (one per origin-less query)", n)
	}
}

// TestHookSearchesAreCountedPerWing is the other half: the per-wing counts keep
// EVERY row — ADR-001 calibrates on all of them — and hook_searches says how
// many were a hook's, so `hook_searches: 0` beside a polluted list is the tell
// for a kit that has not learned to declare itself.
func TestHookSearchesAreCountedPerWing(t *testing.T) {
	svc := newTestService(t)
	const team = "t-origin-stats-2"
	seedOriginSearches(t, svc, team)

	stats, err := svc.RecallStats(context.Background(), team, "", time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	var acme *WingRecall
	for i := range stats.Wings {
		if stats.Wings[i].Wing == "wing_acme" {
			acme = &stats.Wings[i]
		}
	}
	if acme == nil {
		t.Fatalf("wing_acme missing from %+v", stats.Wings)
	}
	if acme.Searches != 5 || acme.HookSearches != 3 {
		t.Fatalf("wing_acme searches=%d hook_searches=%d, want 5 and 3", acme.Searches, acme.HookSearches)
	}
}
