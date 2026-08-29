// Binding for docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md F-3 — the one
// fact that is a WRITE-PATH invariant and belongs beside the write path.
//
// The build tag came off in ADR-044 T7. F-3 was the only binding in this file, so
// its tag left with it.

package palace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// f3Fixture files a MULTI-CHUNK memory under a named source, which is the shape
// that exercises every branch this fact touches: several predecessor chunks to
// end, and a source re-file that would otherwise end them itself.
func f3Fixture(t *testing.T, svc *Service, team, wing string) (id string, chunks int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("F3MARKER the original claim, filed under a named source. ")
	for line := 0; b.Len() < ChunkSize*3; line++ {
		b.WriteString("distinct filler wording so overlap removal finds the real seam here. ")
	}
	res, err := svc.Add(context.Background(), team, AddInput{
		Wing: wing, Room: "decisions", SourceFile: "f3-source.md", Content: b.String(),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(res.Drawers) < 2 {
		t.Fatalf("fixture stored %d chunk(s); F-3 is about ending EVERY chunk, which one chunk "+
			"cannot exercise", len(res.Drawers))
	}
	return res.Drawers[0].ID, len(res.Drawers)
}

// currentUnder counts the CURRENT rows filed under a source — the "how many
// records are standing on this subject" question F-3 is about.
func currentUnder(t *testing.T, svc *Service, team, wing, source string) []Drawer {
	t.Helper()
	rows, err := svc.repo.CurrentBySource(context.Background(), team, wing, "decisions", source)
	if err != nil {
		t.Fatalf("read current rows: %v", err)
	}
	return rows
}

// TestF3ACorrectionLeavesOneCurrentSuccessor is F-3 (UC2-S1, UC2-S2).
//
// What this is NOT: a formally superseded record already disappears from default
// reads (survivorsFrom, memory_search.go:70), so this is not a ranking fact —
// ADR-004 and ADR-038 own history ordering and leave it open. The gap was on the
// WRITE side: supersedeInto wrote the successor, then ended predecessor chunks one
// at a time, with no transaction and no compare-and-swap.
func TestF3ACorrectionLeavesOneCurrentSuccessor(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing, source = "team-f3", "wing_alpha", "f3-source.md"
	oldID, oldChunks := f3Fixture(t, svc, team, wing)

	res, err := svc.Supersede(ctx, team, oldID, "F3MARKER the corrected claim", "the first reading was wrong")
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}

	// EVERY predecessor chunk ends, linked to the successor and carrying the
	// caller's reason. Ending only the head is the binding's second kill-case: the
	// remaining chunks stay current and go on answering with the claim just
	// corrected.
	pred, err := svc.repo.MemoryChunks(ctx, team, oldID)
	if err != nil {
		t.Fatalf("read predecessor chunks: %v", err)
	}
	if len(pred) != oldChunks {
		t.Fatalf("predecessor has %d chunks, seeded %d", len(pred), oldChunks)
	}
	for _, c := range pred {
		if c.ValidTo == "" {
			t.Errorf("chunk %d of the corrected memory is still current; it answers with the "+
				"claim this correction withdrew", c.ChunkIndex)
		}
		if c.SupersededBy != res.ID {
			t.Errorf("chunk %d links to %q, not to the successor %q — an ended record whose "+
				"replacement cannot be found is a dead end, not a correction",
				c.ChunkIndex, c.SupersededBy, res.ID)
		}
		if c.EndedReason != "the first reading was wrong" {
			t.Errorf("chunk %d ended with reason %q, losing the caller's why",
				c.ChunkIndex, c.EndedReason)
		}
	}

	// EXACTLY ONE current record on the subject. This is the kill-case for
	// replacing supersession with a plain Add: that leaves the predecessor current
	// beside the successor, which is the two-competing-records state ADR-044
	// §Decision rejects.
	current := currentUnder(t, svc, team, wing, source)
	roots := map[string]bool{}
	for _, r := range current {
		if r.ParentID == "" {
			roots[r.ID] = true
		}
	}
	if len(roots) != 1 {
		t.Errorf("%d current memories stand on this subject, want exactly 1: %v", len(roots), roots)
	}
	if !roots[res.ID] {
		t.Errorf("the one current memory is not the successor %q", res.ID)
	}

	t.Run("a part way failure leaves no fork", func(t *testing.T) {
		// The abort is driven through the compare-and-swap rather than by injecting
		// a fault: a correction that reaches the swap and is refused takes the same
		// rollback path as one that fails inside it, and it needs no seam that
		// exists only for the test.
		svc := newTestService(t)
		const team, wing = "team-f3-fail", "wing_alpha"
		oldID, _ := f3Fixture(t, svc, team, wing)

		// Somebody else corrects it first. Ours then finds the chunks it observed
		// as open already ended.
		if _, err := svc.Supersede(ctx, team, oldID, "F3MARKER the winner's claim", "won the race"); err != nil {
			t.Fatalf("first correction: %v", err)
		}
		_, err := svc.Supersede(ctx, team, oldID, "F3MARKER the loser's claim", "lost the race")
		if err == nil {
			t.Fatal("a correction of an already-corrected record succeeded; that is the fork this " +
				"fact exists to prevent")
		}
		// Whatever the refusal, NOTHING of the loser may be current.
		for _, r := range currentUnder(t, svc, team, wing, "f3-source.md") {
			if strings.Contains(r.Content, "loser") {
				t.Errorf("the refused correction left a current row behind: %s", r.ID)
			}
		}
	})

	t.Run("a racing correction is refused", func(t *testing.T) {
		// TWO WRITERS, ACTUALLY INTERLEAVED — the task's step 5 requires this to be
		// driven rather than asserted, and to be reported honestly if it cannot be.
		// It can: both goroutines read the predecessor's chunks as open, then race
		// into the swap; exactly one UPDATE finds them current.
		svc := newTestService(t)
		const team, wing = "team-f3-race", "wing_alpha"
		oldID, _ := f3Fixture(t, svc, team, wing)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = svc.Supersede(ctx, team, oldID,
					"F3MARKER correction from writer", "racing writer")
			}()
		}
		close(start)
		wg.Wait()

		won := 0
		for _, err := range errs {
			if err == nil {
				won++
			}
		}
		if won != 1 {
			t.Errorf("%d of 2 racing corrections succeeded, want exactly 1 (errors: %v)", won, errs)
		}
		// The invariant that matters is not which writer won but what is standing
		// afterwards.
		roots := 0
		for _, r := range currentUnder(t, svc, team, wing, "f3-source.md") {
			if r.ParentID == "" {
				roots++
			}
		}
		if roots != 1 {
			t.Errorf("%d current memories stand on the subject after two racing corrections, "+
				"want exactly 1", roots)
		}
		for _, err := range errs {
			if err != nil && !errors.Is(err, ErrConcurrentCorrection) {
				t.Logf("the losing writer was refused by the store rather than by the "+
					"compare-and-swap: %v", err)
			}
		}
	})
}
