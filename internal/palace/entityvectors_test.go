package palace

import (
	"context"
	"fmt"
	"testing"
)

// batchWitnessEmbedder records the SIZE of every Embed call, which is the only
// thing that can tell a bounded batch from an unbounded one.
type batchWitnessEmbedder struct {
	fakeEmbedder // EmbedOne, which the backfill must no longer use
	sizes        []int
}

func (b *batchWitnessEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	b.sizes = append(b.sizes, len(inputs))
	return fakeEmbedder{}.Embed(ctx, inputs)
}

// TestBackfillEmbedsInBoundedBatches pins the call SHAPE and the call SIZE, and
// the size is the half that matters.
//
// BackfillEntityLabels called IndexEntityLabel per row — one EmbedOne round trip
// each — while `Embed(ctx, []string)` sat on the Embedder interface unused. The
// fix is to batch; the trap is to batch without a bound.
//
// ⚠ teiembed CHUNKS INTERNALLY AND OLLAMA DOES NOT. `ollama.Embed` marshals
// every input into one JSON body and issues one HTTP request, so an unbounded
// call makes the batch size equal to however many entities the palace holds.
// `--embed-timeout` records "121s for a batch of 64 on a CPU-only host" against
// a five-minute budget for ONE call, so a few thousand entities is not a slower
// backfill, it is one that cannot finish — and RecomputeGraph logs that error
// and reports EntityLabelsIndexed as 0, so the palaces with the most history
// index nothing and the recompute still succeeds.
//
// ⚠ THE FIXTURE MUST SPAN MORE THAN ONE BATCH OR IT PROVES NOTHING. An earlier
// version of this test seeded three labels and asserted exactly one call; three
// fit inside any bound, so it was green on the safe implementation AND on the
// unbounded one it was written to justify.
//
// Asserted as counts and sizes, never as a duration: a timing assertion would
// measure whichever machine ran it, which is the error issue #366 records.
func TestBackfillEmbedsInBoundedBatches(t *testing.T) {
	ctx := context.Background()
	wit := &batchWitnessEmbedder{}
	svc := newTestServiceWith(t, wit)
	const team = "team-backfill-batch"

	// Deliberately more than entityLabelBatch, so the chunking is exercised
	// rather than merely present.
	const seeded = entityLabelBatch*2 + 5
	for i := range seeded {
		subj := fmt.Sprintf("entity_%03d", i)
		if _, err := svc.KGAdd(ctx, team, subj, "relates_to", "a thing", "", "", "", "", ""); err != nil {
			t.Fatalf("seed %s: %v", subj, err)
		}
	}

	wit.sizes = nil
	n, err := svc.BackfillEntityLabels(ctx, team)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n <= entityLabelBatch {
		t.Fatalf("indexed %d labels, which fits one batch — the fixture no longer spans a "+
			"chunk boundary and this test cannot see an unbounded call", n)
	}
	if len(wit.sizes) == 0 {
		t.Fatal("the backfill made no Embed call at all; it is still on the per-label EmbedOne path")
	}
	want := (n + entityLabelBatch - 1) / entityLabelBatch
	if len(wit.sizes) != want {
		t.Errorf("made %d Embed call(s) for %d labels, want %d — one call per batch of %d",
			len(wit.sizes), n, want, entityLabelBatch)
	}
	for i, size := range wit.sizes {
		if size > entityLabelBatch {
			t.Errorf("Embed call %d carried %d inputs, over the %d bound: on an Ollama-backed "+
				"palace that is one HTTP request of that size against a per-call timeout",
				i, size, entityLabelBatch)
		}
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
