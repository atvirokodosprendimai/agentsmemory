package palace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSearchEmitsSemanticStageSpans is the ADR-025 reachability gate: every
// name in telemetry.SearchStages must appear on a real Service.Search. A stage
// documented in the list and missing here is the repository's characteristic
// defect — finished instrumentation that nothing selects.
func TestSearchEmitsSemanticStageSpans(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := telemetry.WithProvider(context.Background(), tp)

	svc := newTestService(t)
	const team = "team-otel"
	mustAdd(t, svc, team, AddInput{
		Wing: "w", Room: "r", Content: "the otel wiring needle is unique here",
	})
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "otel wiring needle", Limit: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("search returned no hits; the fixture must be recallable")
	}

	got := map[string]bool{}
	var searchID string
	for _, s := range sr.Ended() {
		got[s.Name()] = true
		for _, a := range s.Attributes() {
			if string(a.Key) == "am.search_id" && a.Value.AsString() != "" {
				searchID = a.Value.AsString()
			}
		}
	}
	for _, name := range telemetry.SearchStages() {
		if !got[name] {
			t.Errorf("Search did not emit span %q — stage is documented and unreachable", name)
		}
	}
	if searchID == "" {
		t.Error("no span carried am.search_id; SQLite search_events cannot join this trace")
	}

	byID := map[string]string{}
	for _, s := range sr.Ended() {
		byID[s.SpanContext().SpanID().String()] = s.Name()
	}
	under := map[string]map[string]bool{}
	for _, s := range sr.Ended() {
		if !s.Parent().IsValid() {
			continue
		}
		parentName := byID[s.Parent().SpanID().String()]
		if under[parentName] == nil {
			under[parentName] = map[string]bool{}
		}
		under[parentName][s.Name()] = true
	}
	searchKids := []string{
		telemetry.StageEmbed,
		telemetry.StageRetrieve,
		telemetry.StageCollapse,
		telemetry.StageCloset,
		telemetry.StageFusion,
		telemetry.StageRecency,
		telemetry.StageRerank,
		telemetry.StageRecord,
	}
	for _, name := range searchKids {
		if !under[telemetry.StageSearch][name] {
			t.Errorf("%s is not a child of %s — the tree would dump as a forest of roots", name, telemetry.StageSearch)
		}
	}
	if !under[telemetry.StageRetrieve][telemetry.StageHydrate] {
		t.Errorf("%s is not a child of %s", telemetry.StageHydrate, telemetry.StageRetrieve)
	}
}

// failVectors is a VectorStore whose Search fails on demand, to prove the
// failed-closed path of searchCandidates records telemetry.
type failVectors struct {
	store.VectorStore
	err error
}

func (f *failVectors) Search(ctx context.Context, namespace string, vector []float32, k int, filter store.Filter) (store.SearchResult, error) {
	if f.err != nil {
		return store.SearchResult{}, f.err
	}
	return f.VectorStore.Search(ctx, namespace, vector, k, filter)
}

// TestSearchRecordsVectorFailureClosed is the searchCandidates error-exit gate:
// a vector-search failure must end the retrieve and hydrate spans with
// failed_closed. A bare early return skips finish entirely — both spans dangle
// (never End()ed), the failed-closed outcome is never recorded, and the failure
// vanishes from exactly the telemetry a debugging session needs.
func TestSearchRecordsVectorFailureClosed(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := telemetry.WithProvider(context.Background(), tp)

	svc := newTestService(t)
	svc.vectors = &failVectors{VectorStore: svc.vectors, err: errors.New("vector backend down")}
	const team = "team-otel-fail"
	mustAdd(t, svc, team, AddInput{
		Wing: "w", Room: "r", Content: "the failed-closed needle is unique here",
	})

	if _, err := svc.Search(ctx, team, SearchQuery{Query: "failed-closed needle", Limit: 3}); err == nil {
		t.Fatal("search succeeded despite the vector backend erroring")
	}

	retrieve := spansByName(sr)[telemetry.StageRetrieve]
	hydrate := spansByName(sr)[telemetry.StageHydrate]
	if retrieve == nil {
		t.Fatal("retrieve span never ended on a failed vector search — the failure vanishes from telemetry")
	}
	if hydrate == nil {
		t.Fatal("hydrate span never ended on a failed vector search — the failure vanishes from telemetry")
	}
	if got := spanAttrs(retrieve)["am.outcome"]; got != string(telemetry.FailedClosed) {
		t.Errorf("retrieve outcome = %q, want %q", got, telemetry.FailedClosed)
	}
	if got := spanAttrs(hydrate)["am.outcome"]; got != string(telemetry.FailedClosed) {
		t.Errorf("hydrate outcome = %q, want %q", got, telemetry.FailedClosed)
	}
}

func spanAttrs(s sdktrace.ReadOnlySpan) map[string]string {
	got := map[string]string{}
	for _, a := range s.Attributes() {
		got[string(a.Key)] = a.Value.Emit()
	}
	return got
}

func spansByName(sr *tracetest.SpanRecorder) map[string]sdktrace.ReadOnlySpan {
	got := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range sr.Ended() {
		got[s.Name()] = s
	}
	return got
}

// TestSearchDumpJoinsCodeAndKnobs is the debug-tool gate: a dumped tree must
// say which file:line started each stage, and a bypassed stage must name the
// same reason RankingProfile() would predict. If closet_scale=0 and the span
// says ran, the wire is a lie.
func TestSearchDumpJoinsCodeAndKnobs(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := telemetry.WithProvider(context.Background(), tp)

	svc := newTestService(t).WithClosetBoost(0)
	const team = "team-otel-debug"
	mustAdd(t, svc, team, AddInput{
		Wing: "w", Room: "r", Content: "the otel debug needle is unique here",
	})
	if _, err := svc.Search(ctx, team, SearchQuery{
		Query: "otel debug needle", Limit: 3, SkipTelemetry: true,
	}); err != nil {
		t.Fatalf("search: %v", err)
	}

	byName := spansByName(sr)
	search := spanAttrs(byName[telemetry.StageSearch])
	if search["am.closet_scale"] != "0" {
		t.Errorf("search closet_scale = %q, want 0 to match WithClosetBoost", search["am.closet_scale"])
	}
	if search["am.rerank_configured"] != "false" {
		t.Errorf("search rerank_configured = %q", search["am.rerank_configured"])
	}
	if !strings.HasPrefix(search["am.code.file"], "internal/palace/") {
		t.Errorf("search call site = %q, want internal/palace/", search["am.code.file"])
	}

	want := map[string]string{
		telemetry.StageCloset:  telemetry.ReasonScaleZero,
		telemetry.StageRecency: telemetry.ReasonBandZero,
		telemetry.StageRerank:  telemetry.ReasonNoReranker,
		telemetry.StageRecord:  telemetry.ReasonSkipSQLite,
	}
	for name, reason := range want {
		got := spanAttrs(byName[name])
		if got["am.outcome"] != string(telemetry.Bypassed) {
			t.Errorf("%s outcome = %q, want bypassed", name, got["am.outcome"])
		}
		if got["am.reason"] != reason {
			t.Errorf("%s reason = %q, want %s (knob and dump disagree)", name, got["am.reason"], reason)
		}
	}

	retrieve := byName[telemetry.StageRetrieve]
	if retrieve == nil {
		t.Fatal("retrieve span missing")
	}
	if len(retrieve.Events()) == 0 || retrieve.Events()[0].Name != "widen" {
		t.Errorf("retrieve has no widen event; the doubling loop is not visible in a dump")
	}
	if spanAttrs(retrieve)["am.reason"] == "" {
		t.Error("retrieve missing am.reason stop (enough/exhausted/max_distance/widen_ceiling)")
	}
}

func TestEvalCaseNestsArms(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := telemetry.WithProvider(context.Background(), tp)

	svc := newTestService(t)
	const team = "team-otel-eval"
	gold := mustAdd(t, svc, team, AddInput{
		Wing: "w", Room: "r", Content: "the eval nesting needle is unique here",
	})
	if _, err := svc.evalCaseResult(ctx, team, EvalCase{
		Query: "eval nesting needle", Expect: gold[0].ID, Wing: "w",
	}, []EvalArm{ArmVector, ArmProduction}, 10); err != nil {
		t.Fatalf("evalCaseResult: %v", err)
	}

	byID := map[string]string{}
	for _, s := range sr.Ended() {
		byID[s.SpanContext().SpanID().String()] = s.Name()
	}
	under := map[string]map[string]bool{}
	arms := map[string]bool{}
	for _, s := range sr.Ended() {
		if s.Name() == telemetry.StageEvalArm {
			for _, a := range s.Attributes() {
				if string(a.Key) == "am.arm" {
					arms[a.Value.AsString()] = true
				}
			}
		}
		if !s.Parent().IsValid() {
			continue
		}
		parentName := byID[s.Parent().SpanID().String()]
		if under[parentName] == nil {
			under[parentName] = map[string]bool{}
		}
		under[parentName][s.Name()] = true
	}
	if !under[telemetry.StageEvalCase][telemetry.StageEvalArm] {
		t.Errorf("%s has no %s child — eval would dump as a forest", telemetry.StageEvalCase, telemetry.StageEvalArm)
	}
	if !under[telemetry.StageEvalArm][telemetry.StageSearch] {
		t.Errorf("production Search is not a child of %s", telemetry.StageEvalArm)
	}
	if !under[telemetry.StageEvalArm][telemetry.StageCollapse] {
		t.Errorf("ablation rankRetrieved is not a child of %s", telemetry.StageEvalArm)
	}
	if !arms[string(ArmVector)] || !arms[string(ArmProduction)] {
		t.Errorf("am.arm values = %v, want vector and production", arms)
	}
}
