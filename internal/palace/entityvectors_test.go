package palace

import (
	"context"
	"testing"
)

// TestBackfillEmbedsEveryLabelInOneBatch pins the call SHAPE, not a duration.
//
// BackfillEntityLabels used to call IndexEntityLabel per row — one EmbedOne
// round trip and one Upsert each — while `Embed(ctx, []string)` sat on the
// Embedder interface unused, implemented by both backends, with teiembed's own
// version chunking internally against the limit it probes from /info. The batch
// primitive existed and this call site did not select it.
//
// ⚠ ASSERTED AS A COUNT OF CALLS, DELIBERATELY. A timing assertion here would
// measure the machine the test runs on: issue #366 recorded a ~67s recompute as
// though it were a property of the tool, when it was CPU-only Ollama and bge-m3
// at ~59ms per embed. Round trips are the part that is true on every host, so
// round trips are what this pins.
func TestBackfillEmbedsEveryLabelInOneBatch(t *testing.T) {
	ctx := context.Background()
	rec := &recordingEmbedder{}
	svc := newTestServiceWith(t, rec)
	const team = "team-backfill-batch"

	// Three askable entities and one structural one, which must be skipped rather
	// than embedded — it would compete for the five nearest label slots and
	// factsFor discards every derived fact anyway.
	for _, f := range [][3]string{
		{"attachDerivedEdge", "sets", "the drawer id as the edge object"},
		{"BackfillEntityLabels", "embeds", "every entity label"},
		{"entityTunnelsForWing", "pairs", "hallways across wings"},
	} {
		if _, err := svc.KGAdd(ctx, team, f[0], f[1], f[2], "", "", "", "", ""); err != nil {
			t.Fatalf("seed %q: %v", f[0], err)
		}
	}

	before := rec.calls
	n, err := svc.BackfillEntityLabels(ctx, team)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n == 0 {
		t.Fatal("indexed nothing; the fixture seeded no askable entity and this test proves nothing")
	}
	if got := rec.calls - before; got != 1 {
		t.Errorf("the backfill made %d Embed call(s) for %d labels, want exactly 1 — the batch "+
			"primitive is on the interface and both backends implement it, so a per-label loop "+
			"is N round trips buying nothing", got, n)
	}
}

// TestBackfillIsIdempotentAndSkipsStructuralEntities keeps the two properties the
// batching change must not have altered: running twice is a no-op rather than a
// duplicate, and the entities that exist to hold the graph together are not
// indexed as though a question could be about them.
func TestBackfillIsIdempotentAndSkipsStructuralEntities(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-backfill-idempotent"

	if _, err := svc.KGAdd(ctx, team, "computeHallwaysForWing", "derives",
		"one wing's hallways", "", "", "", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	first, err := svc.BackfillEntityLabels(ctx, team)
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	second, err := svc.BackfillEntityLabels(ctx, team)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if first != second {
		t.Errorf("backfill indexed %d then %d; Upsert replaces by id, so the count is stable",
			first, second)
	}
	if first == 0 {
		t.Fatal("indexed nothing, so neither assertion above means anything")
	}
}
