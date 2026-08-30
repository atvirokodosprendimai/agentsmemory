package palace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/rerank/tei"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"
)

// searchInto files a memory into wing and runs one real Search against it, so
// the reason travels the whole production path — applyRerankWith to recordSearch
// to the column — rather than being seeded straight into a row. A test that
// INSERTs the value it later reads proves the aggregate and nothing about
// whether anything writes it.
func searchInto(t *testing.T, svc *Service, team, wing string) {
	t.Helper()
	mustAdd(t, svc, team, AddInput{Wing: wing, Room: "r", Content: "a memory about skip reasons"})
	if _, err := svc.Search(context.Background(), team, SearchQuery{
		Query: "skip reasons", Wing: wing, Limit: 3, MaxDistance: 1.5,
	}); err != nil {
		t.Fatalf("search %s: %v", wing, err)
	}
}

// wingRecall returns the per-wing row for wing.
func wingRecall(t *testing.T, svc *Service, team, wing string) WingRecall {
	t.Helper()
	stats, err := svc.RecallStats(context.Background(), team, "", time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	for _, w := range stats.Wings {
		if w.Wing == wing {
			return w
		}
	}
	t.Fatalf("no row for %s", wing)
	return WingRecall{}
}

// TestRecallStatsCountsWhyRerankingWasSkipped drives four real recalls whose
// only difference is why the cross-encoder did or did not order the page.
func TestRecallStatsCountsWhyRerankingWasSkipped(t *testing.T) {
	const team = "team-skips"
	svc := newTestService(t)

	svc.rerank, svc.rerankPool, svc.rerankWeight = &fakeReranker{}, 5, 0.5
	searchInto(t, svc, team, "wing_alpha")

	svc.rerank = nil
	searchInto(t, svc, team, "wing_beta")

	svc.rerank, svc.rerankWeight = &fakeReranker{}, 0
	searchInto(t, svc, team, "wing_gamma")

	sick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(sick.Close)
	svc.rerank, svc.rerankWeight = tei.New(sick.URL, 30*time.Second), 0.5
	searchInto(t, svc, team, "wing_atlas")

	for _, tc := range []struct{ wing, reason string }{
		{"wing_beta", telemetry.ReasonNoReranker},
		{"wing_gamma", telemetry.ReasonWeightZero},
		{"wing_atlas", telemetry.ReasonError},
	} {
		if got := wingRecall(t, svc, team, tc.wing).RerankSkips[tc.reason]; got != 1 {
			t.Errorf("%s: RerankSkips[%q] = %d, want 1 (full: %v)",
				tc.wing, tc.reason, got, wingRecall(t, svc, team, tc.wing).RerankSkips)
		}
	}
	if got := wingRecall(t, svc, team, "wing_alpha").RerankSkips; len(got) != 0 {
		t.Errorf("a recall where reranking RAN was counted as a skip: %v — the healthy path "+
			"must contribute to no bucket, or the column measures nothing", got)
	}
}

// TestADisabledRerankerAndATimingOutOneAreNotTheSameRow is the whole ADR in one
// assertion: today both write reranked = 0 and am_recall_stats cannot tell an
// operator whether their cross-encoder is switched off or failing on every query.
func TestADisabledRerankerAndATimingOutOneAreNotTheSameRow(t *testing.T) {
	const team = "team-tell-apart"
	svc := newTestService(t)

	svc.rerank, svc.rerankPool, svc.rerankWeight = nil, 5, 0.5
	searchInto(t, svc, team, "wing_beta")

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)
	svc.rerank = tei.New(slow.URL, 50*time.Millisecond)
	searchInto(t, svc, team, "wing_atomic")

	off, broken := wingRecall(t, svc, team, "wing_beta"), wingRecall(t, svc, team, "wing_atomic")
	if off.Reranked != broken.Reranked {
		t.Fatalf("precondition gone: these are supposed to be identical on `reranked` (%d vs %d)", off.Reranked, broken.Reranked)
	}
	if off.RerankSkips[telemetry.ReasonNoReranker] != 1 {
		t.Errorf("a disabled reranker recorded %v", off.RerankSkips)
	}
	if broken.RerankSkips[telemetry.ReasonTimeout] != 1 {
		t.Errorf("a timing-out reranker recorded %v", broken.RerankSkips)
	}
	if off.RerankSkips[telemetry.ReasonTimeout] != 0 || broken.RerankSkips[telemetry.ReasonNoReranker] != 0 {
		t.Error("the two reasons are conflated, which is the defect this ADR exists to fix")
	}
}

// TestARowFromBeforeThisColumnIsNotAFalseSkip: NULL means "not recorded yet",
// which is not "nothing was skipped". Rows written by the previous binary must
// land in no bucket, or every historical row reads as a healthy recall.
func TestARowFromBeforeThisColumnIsNotAFalseSkip(t *testing.T) {
	const team = "team-legacy"
	svc := newTestService(t)
	svc.rerank, svc.rerankPool, svc.rerankWeight = &fakeReranker{}, 5, 0.5
	searchInto(t, svc, team, "wing_one")

	// RFC3339, matching what recordSearch writes (recallstats.go:173). The cutoff
	// is compared as a STRING, so datetime('now') — which yields a space where
	// RFC3339 has a T — sorts below any RFC3339 cutoff and the row is filtered out
	// before the aggregate ever sees it. That made this test pass against a mutant
	// that counts NULL rows as skips: the fixture could not exhibit the defect.
	if err := svc.repo.db.WithContext(context.Background()).Exec(
		`INSERT INTO search_events (id, team_id, wing, room, query, candidates, hits, top_score, top_rerank_score, reranked, rerank_skip_reason, created_at)
		 VALUES ('legacy-1', ?, 'wing_one', 'r', 'q', 5, 3, 0.5, 0, 0, NULL, strftime('%Y-%m-%dT%H:%M:%SZ','now'))`, team).Error; err != nil {
		t.Fatalf("seed a pre-column row: %v", err)
	}

	for reason, n := range wingRecall(t, svc, team, "wing_one").RerankSkips {
		t.Errorf("a NULL (pre-column) row counted as a skip for %q (%d) — NULL is 'not recorded', not 'nothing skipped'", reason, n)
	}
}

// TestADR031CalibrationAggregateIsUnchanged: ADR-031 aggregates on
// `hits > 0 AND reranked = 1` (recallstats.go:212). This ADR adds beside it and
// must not move it.
func TestADR031CalibrationAggregateIsUnchanged(t *testing.T) {
	const team = "team-adr031"
	svc := newTestService(t)

	svc.rerank, svc.rerankPool, svc.rerankWeight = &fakeReranker{}, 5, 0.5
	searchInto(t, svc, team, "wing_a")
	searchInto(t, svc, team, "wing_a")

	svc.rerank = nil
	searchInto(t, svc, team, "wing_a")

	got := wingRecall(t, svc, team, "wing_a")
	if got.Reranked != 2 {
		t.Errorf("Reranked = %d, want 2 — only recalls a cross-encoder actually ordered", got.Reranked)
	}
	if got.RerankSkips[telemetry.ReasonNoReranker] != 1 {
		t.Errorf("the skip was not counted: %v", got.RerankSkips)
	}
}
