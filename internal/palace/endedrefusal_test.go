package palace

import (
	"context"
	"strings"
	"testing"
)

// TestBothReadPathsRefuseAnEndedDrawerTheSameWay closes a split that lived on the
// one flag the protocol tells readers to use.
//
// am_get_drawer(id) refused an ended drawer with the date, the reason and the
// successor. am_get_drawer(id, whole: true) — the SAME id, the same second —
// answered a bare "drawer not found". Reported 2026-08-29 by a session in another
// repository, with a one-variable proof.
//
// ⚠ THE FLAG IS THE POINT. The shipped protocol says to pass whole:true whenever
// you mean to READ a memory rather than confirm it exists, so the flag we
// recommend for real reading was the one that hid the correction. And the
// degraded answer is a DIFFERENT CLAIM, not a smaller one: "not found" reads as
// never existed rather than was corrected, so a reader chasing a citation
// concludes the pointer was bad and stops.
func TestBothReadPathsRefuseAnEndedDrawerTheSameWay(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-ended", "wing_alpha"

	// Multi-chunk, because the whole:true path is the one that reassembles and a
	// single-chunk memory would not exercise the branch that lost the refusal.
	var b strings.Builder
	b.WriteString("ENDEDMARKER the original claim. ")
	for line := 0; b.Len() < ChunkSize*2; line++ {
		b.WriteString("distinct filler wording so overlap removal finds the real seam here. ")
	}
	first, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: b.String()})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(first.Drawers) < 2 {
		t.Fatalf("fixture stored %d chunk(s); the whole-memory path needs more than one", len(first.Drawers))
	}
	oldID := first.Drawers[0].ID

	res, err := svc.Supersede(ctx, team, oldID, "ENDEDMARKER the corrected claim", "the first reading was wrong")
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}

	_, errOne := svc.Get(ctx, team, oldID)
	_, errWhole := svc.GetMemory(ctx, team, oldID)
	if errOne == nil || errWhole == nil {
		t.Fatalf("an ended drawer was returned: Get=%v GetMemory=%v", errOne, errWhole)
	}

	// Each fact the refusal carries is what makes it navigable rather than a dead
	// end, so each is asserted on BOTH paths rather than comparing the strings —
	// the two messages may legitimately differ in wording later.
	for _, c := range []struct {
		what string
		want string
	}{
		{"the ending is dated", "was ended on"},
		{"the reason is quoted", "the first reading was wrong"},
		{"the successor is named", short12(res.ID)},
		{"history is offered", "include_history"},
	} {
		if !strings.Contains(errWhole.Error(), c.want) {
			t.Errorf("whole:true refusal does not say %s (want %q) — a caller told only "+
				"\"not found\" reads that as NEVER EXISTED, not as corrected, and stops "+
				"chasing the citation.\n  got:  %v\n  and the by-id path says: %v",
				c.what, c.want, errWhole, errOne)
		}
		if !strings.Contains(errOne.Error(), c.want) {
			t.Errorf("by-id refusal does not say %s (want %q): %v", c.what, c.want, errOne)
		}
	}

	t.Run("a retraction names no successor", func(t *testing.T) {
		// "or read , which replaced it" is the shape of a bug: it reads as a lost id
		// rather than as an absence that is meant.
		svc := newTestService(t)
		const team, wing = "team-ended-2", "wing_alpha"
		res, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions",
			Content: "RETRACTMARKER a claim that nothing replaces"})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := svc.InvalidateDrawer(ctx, team, res.Drawers[0].ID, "we are not doing this after all"); err != nil {
			t.Fatalf("retract: %v", err)
		}
		_, err = svc.GetMemory(ctx, team, res.Drawers[0].ID)
		if err == nil {
			t.Fatal("a retracted memory was returned")
		}
		if strings.Contains(err.Error(), "which replaced it") {
			t.Errorf("the refusal offers a successor for a retraction that has none: %v", err)
		}
		if !strings.Contains(err.Error(), "we are not doing this after all") {
			t.Errorf("the refusal drops the reason, which is the only thing the retraction "+
				"recorded that nothing else can recover: %v", err)
		}
	})
}
