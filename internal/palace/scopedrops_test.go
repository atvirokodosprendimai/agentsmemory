package palace

import (
	"context"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// searchSpanAttrs runs one Search and returns the am.search parent's attributes.
func searchSpanAttrs(t *testing.T, svc *Service, team string, q SearchQuery) map[string]string {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := telemetry.WithProvider(context.Background(), tp)

	if _, err := svc.Search(ctx, team, q); err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, s := range sr.Ended() {
		if s.Name() != telemetry.StageSearch {
			continue
		}
		got := map[string]string{}
		for _, a := range s.Attributes() {
			got[string(a.Key)] = a.Value.Emit()
		}
		return got
	}
	t.Fatalf("no %s span was recorded", telemetry.StageSearch)
	return nil
}

// TestRequestAndServedValuesAreBothOnTheSpan: what the caller asked for is
// recoverable from the trace, not only what ran.
//
// SearchPage clamps limit to MaxSearchLimit and truncates the query at 250 runes,
// and searchAttrs was handed the ALREADY-CLAMPED value — so a caller asking for
// 5000 and one asking for 100 emitted an identical am.limit=100, and a query cut
// mid-sentence left no evidence anywhere that the text reaching the embedder, the
// lexical channel and the cross-encoder differed from the text that was sent.
//
// Both cases are asserted with their controls. Recording only a `truncated` flag
// would pass an implementation that always set it, and asserting only the clamped
// case would pass one that never clamped.
func TestRequestAndServedValuesAreBothOnTheSpan(t *testing.T) {
	svc := newTestService(t)
	const team = "team-request"
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a memory about request shaping"})

	long := make([]rune, 400)
	for i := range long {
		long[i] = 'a'
	}
	copy(long, []rune("request shaping "))

	over := searchSpanAttrs(t, svc, team, SearchQuery{Query: string(long), Limit: 5000, MaxDistance: 1.25})
	if over["am.limit"] == over["am.limit_requested"] {
		t.Errorf("am.limit and am.limit_requested are both %q for a 5000-limit request; the clamp is "+
			"invisible and an operator cannot tell 100-asked from 5000-asked", over["am.limit"])
	}
	if over["am.limit_requested"] != "5000" {
		t.Errorf("am.limit_requested = %q, want 5000", over["am.limit_requested"])
	}
	if over["am.query_truncated"] != "true" {
		t.Errorf("a 400-rune query recorded am.query_truncated=%q", over["am.query_truncated"])
	}
	if over["am.query_runes"] != "400" {
		t.Errorf("am.query_runes = %q, want the PRE-truncation length 400", over["am.query_runes"])
	}
	// am.max_distance is the one retrieval boundary the knob set omitted, while
	// retrieveStop could already end the widening loop with reason=max_distance —
	// so the trace named the stop without ever naming the threshold.
	if over["am.max_distance"] == "" {
		t.Error("am.max_distance is absent; the trace can say the widening loop stopped on the " +
			"distance boundary but not what that boundary was")
	}

	// Control: a normal request must NOT claim it was altered.
	normal := searchSpanAttrs(t, svc, team, SearchQuery{Query: "request shaping", Limit: 3})
	if normal["am.query_truncated"] != "false" {
		t.Errorf("a short query recorded am.query_truncated=%q — the flag is not reporting, it is "+
			"just on", normal["am.query_truncated"])
	}
	if normal["am.limit_requested"] != "3" {
		t.Errorf("am.limit_requested = %q, want the caller's 3", normal["am.limit_requested"])
	}
}

// TestScopeDropsCountEachCauseSeparately drives the predicate directly, because
// a healthy system cannot produce two of the three causes.
//
// That is the point rather than an inconvenience. The wing/room comparison is
// documented as redundant when the index honoured the filter, and kept solely so
// a stale index cannot surface another wing's memory — so OutOfScope is ZERO on a
// healthy palace and a non-zero value means the vector index and the durable rows
// have DIVERGED. It is an alarm, not a metric. The same holds for Orphan, which
// is the same divergence seen from the other side.
//
// An end-to-end test therefore cannot exercise them without corrupting the index
// on purpose. survivorsFrom is a pure function over (hits, rows, query), so the
// divergence is expressible directly: a hit whose row is missing, and a row whose
// wing disagrees with the query.
func TestScopeDropsCountEachCauseSeparately(t *testing.T) {
	rows := map[string]Drawer{
		"in":        {ID: "in", Wing: "wing_beta", Room: "r", Content: "kept"},
		"elsewhere": {ID: "elsewhere", Wing: "wing_alpha", Room: "r", Content: "another wing"},
		"far":       {ID: "far", Wing: "wing_beta", Room: "r", Content: "too distant"},
	}
	// Score maps to distance via distanceFromScore; "far" is deliberately beyond
	// the boundary and "in" deliberately inside it.
	hits := []store.Hit{
		{ID: "in", Score: 0.95},
		{ID: "elsewhere", Score: 0.94},
		{ID: "orphaned", Score: 0.93}, // index row whose drawer is gone
		{ID: "far", Score: 0.10},
	}

	survivors, distinct, drops := survivorsFrom(hits, rows, SearchQuery{Wing: "wing_beta", MaxDistance: 0.5}, false)

	if drops.Orphan != 1 {
		t.Errorf("Orphan = %d, want 1 — an index row with no durable row is a divergence and must "+
			"be counted, not silently skipped", drops.Orphan)
	}
	if drops.OutOfScope != 1 {
		t.Errorf("OutOfScope = %d, want 1. This is the alarm: the guard only fires when the index "+
			"handed back a wing the caller did not ask for", drops.OutOfScope)
	}
	if drops.OverDistance != 1 {
		t.Errorf("OverDistance = %d, want 1 — the caller's own boundary, and the only one of the "+
			"three that is ordinary", drops.OverDistance)
	}
	if len(survivors) != 1 || distinct != 1 {
		t.Errorf("survivors = %d (distinct %d), want exactly the in-scope, in-range row — the "+
			"counts above are worthless if the predicate also changed what it keeps",
			len(survivors), distinct)
	}
	if !drops.Any() {
		t.Error("Any() reports no drops while three were counted")
	}

	// The healthy case: nothing dropped, so nothing is annotated and the alarm is
	// silent. A test that only ever saw non-zero counts could not tell an alarm
	// from a number that is always on.
	_, _, clean := survivorsFrom([]store.Hit{{ID: "in", Score: 0.95}}, rows, SearchQuery{Wing: "wing_beta"}, false)
	if clean.Any() {
		t.Errorf("a healthy prefix reported drops %+v; the alarm must be silent when the index and "+
			"the rows agree", clean)
	}
}

// TestKnobsThatDecideThePageAreAllOnTheParentSpan is a completeness gate, not a
// spot check.
//
// It exists because a variable map of the served search path, taken 2026-08-26,
// found 34 LIVE variables and 6 that any eval arm varies — and several that
// decide a page were on no span at all. Two were embarrassing in different ways:
// am.candidate_k is the actual fetch width, computed on every recall and
// recorded nowhere, which is why a paired run could show production reaching 15
// candidates while an arm ranking the same query saw 100 and nothing in the
// trace said so. And am.rerank_norm shipped one commit earlier — the change that
// added the knob did not make it observable, which is rung 3 missed inside the
// very commit that introduced it.
//
// The list is deliberately EXPLICIT rather than derived from searchAttrs. A gate
// that reads the same function it guards passes whatever that function happens
// to do, which is how a stage list became both the subject and the authority of
// its own check earlier in this corpus.
func TestKnobsThatDecideThePageAreAllOnTheParentSpan(t *testing.T) {
	svc := newTestService(t).WithReranker(&fakeReranker{budget: 10 * time.Second}, 50).WithRerankWeight(0.5)
	const team = "team-knobs"
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a memory about knob coverage"})

	got := searchSpanAttrs(t, svc, team, SearchQuery{Query: "knob coverage", Limit: 3, MaxDistance: 1.5})

	// Each entry is a knob whose value CHANGES WHICH MEMORIES REACH THE PAGE.
	// Adding one here without emitting it is meant to fail.
	for _, want := range []struct{ key, why string }{
		{"am.candidate_k", "the fetch width actually asked of the index — limit*3, floored by rerank_pool, raised by retrieve-k; not derivable from the others"},
		{"am.limit", "the page size served"},
		{"am.limit_requested", "what the caller asked for before clamping"},
		{"am.max_distance", "the boundary that drops candidates before ranking"},
		{"am.fusion", "which fusion combined the arms"},
		{"am.rerank_configured", "whether a cross-encoder ran at all"},
		{"am.rerank_weight", "how much of the order it decided"},
		{"am.rerank_norm", "HOW its score was scaled — min-max discards magnitude, sigmoid preserves it"},
		{"am.rerank_pool", "how many candidates it actually scored"},
		{"am.rerank_timeout_ms", "the ceiling that decides whether the cross-encoder's order survives at all — measured 2026-08-26, 44 of 60 calls at pool 20 ran past the 10s the deployed stack ships, so on CPU this knob, not the weight, was picking the ranking"},
		{"am.evidence", "which text the cross-encoder was shown"},
		{"am.evidence_chars", "the budget that text shares — it decides WHICH passages are scored, and the order follows from that as surely as from the weight"},
		{"am.evidence_regions_max", "how many places share the budget; the live failure this bounds served sixteen shards too small to carry the reasoning after each match"},
		{"am.vector_backend", "WHICH INDEX answered. A recall served by a behind index is identical to a healthy one in every other attribute on this span"},
		{"am.closet_scale", "the closet prior's weight"},
		{"am.recency_band", "the recency reorder's width"},
		{"am.query_runes", "how long the query was before truncation"},
		{"am.query_truncated", "whether the embedder saw the whole question"},
	} {
		if got[want.key] == "" {
			t.Errorf("%s is absent from the %s span — %s.\n"+
				"A knob that changes the page and appears on no span cannot be reconstructed "+
				"from a trace, so a recall cannot be explained after the fact.",
				want.key, telemetry.StageSearch, want.why)
		}
	}

	// Under rrf the lexical knobs are inert and rrfK is the ONLY fusion parameter
	// that applies, so a reader with am.fusion=rrf and no am.rrf_k cannot
	// reconstruct a single fused score.
	if got["am.fusion"] == "rrf" && got["am.rrf_k"] == "" {
		t.Error("am.fusion=rrf without am.rrf_k: the one constant that defines the fused score is missing")
	}
}
