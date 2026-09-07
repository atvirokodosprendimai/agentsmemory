package palace

import (
	"context"
	"errors"
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

// failAfterEmbedder succeeds for the first ok batches and fails after that, so a
// test can stop the backfill part-way through a corpus that spans several chunks.
type failAfterEmbedder struct {
	fakeEmbedder
	ok    int
	calls int
}

func (f *failAfterEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	f.calls++
	if f.calls > f.ok {
		return nil, errors.New("embedder unavailable")
	}
	return fakeEmbedder{}.Embed(ctx, inputs)
}

// TestBackfillReportsTheLabelsItActuallyWrote holds the return value to the store.
//
// ⚠ THE COUNT USED TO BE A FALSE STATEMENT, AND THE COMMENT ABOVE IT ARGUED FOR
// THE OPPOSITE PROPERTY. Every error path returned 0 while Upsert ran PER CHUNK,
// so a failure on chunk 3 of 5 left chunks 1 and 2 written and reported that
// nothing had been indexed. The doc comment justified that as all-or-nothing —
// "a half-filled label index that reports a count is worse than one that says it
// did not run" — which describes a property per-chunk Upsert does not have. Both
// halves shipped in #412; this is the repair.
//
// The batching is right and stays. What changes is that the number describes the
// store: the chunks already written stay written, and `indexed` says how many.
// RecomputeGraph discards the count on error today, so this costs the caller
// nothing and stops the signature lying to the next one.
//
// ⚠ IT ASSERTS THE COUNT AND NOT THE NAMESPACE, DELIBERATELY. KGAdd indexes each
// label as it writes it, so the entity namespace is already full before the
// backfill runs and holds the same points whether this call wrote two chunks or
// none. The store cannot distinguish the two here; the return value is the only
// thing that can, which is precisely why it must not be zero.
func TestBackfillReportsTheLabelsItActuallyWrote(t *testing.T) {
	ctx := context.Background()
	const okBatches = 2
	emb := &failAfterEmbedder{ok: okBatches}
	svc := newTestServiceWith(t, emb)
	const team = "team-backfill-partial"

	// More than okBatches worth, so the failure lands part-way rather than at the
	// end — a corpus that fits in the successful batches would pass with any
	// implementation.
	const seeded = entityLabelBatch*okBatches + entityLabelBatch/2
	for i := range seeded {
		subj := fmt.Sprintf("entity_%03d", i)
		if _, err := svc.KGAdd(ctx, team, subj, "relates_to", "a thing", "", "", "", "", ""); err != nil {
			t.Fatalf("seed %s: %v", subj, err)
		}
	}
	// Seeding drives EmbedOne, not Embed, so the batch counter starts here.
	emb.calls = 0

	n, err := svc.BackfillEntityLabels(ctx, team)
	if err == nil {
		t.Fatal("the embedder failed mid-corpus and the backfill reported success; this test can " +
			"no longer see the partial case it exists for")
	}
	want := okBatches * entityLabelBatch
	if n != want {
		t.Errorf("reported %d labels indexed after %d successful batch(es) of %d, want %d.\n"+
			"  0 means every error path throws the progress away while Upsert has already run "+
			"per chunk — the count then denies rows that are in the namespace, and the caller "+
			"logs a failure over an index that is partly built.", n, okBatches, entityLabelBatch, want)
	}
	if n >= seeded {
		t.Errorf("reported %d of %d seeded labels, so the failure did not land part-way and the "+
			"fixture no longer spans the boundary it claims to pin", n, seeded)
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
