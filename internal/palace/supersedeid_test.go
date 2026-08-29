package palace

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// longNoteWithTail is the commonest real correction: a long note whose OPENING is
// unchanged and whose conclusion is fixed.
func longNoteWithTail(tail string) string {
	var b strings.Builder
	b.WriteString("A LONG NOTE whose opening does not change. ")
	for line := 0; b.Len() < ChunkSize+200; line++ {
		fmt.Fprintf(&b, "opening line %d with distinct wording so overlap removal finds the seam. ", line)
	}
	b.WriteString(" CONCLUSION: " + tail)
	return b.String()
}

// TestACorrectionMayKeepItsOpeningUnchanged is a REGRESSION test for a break this
// repository shipped on 2026-08-29 in ADR-044 T7.
//
// T7 made a correction atomic by ending the predecessor's chunks BEFORE writing
// the successor's rows, inside one transaction. But `prepareWrite` resolves ids
// OUTSIDE that transaction, and it reuses the id of any CURRENT row already
// holding a chunk's content key — deliberately, so re-filing unchanged text keeps
// anchors pinned to it. When the correction leaves chunk 0 byte-identical, that
// lookup returns the PREDECESSOR's own chunk-0 id; the swap then ends that row;
// and the insert collides with it:
//
//	save drawers: constraint failed: UNIQUE constraint failed: drawers.team_id, drawers.id
//
// Bisected: 0a3beb6 (before T7) succeeded, a7f2898 (T7) failed.
//
// ⚠ IT IS THE COMMONEST CORRECTION THERE IS — fix the conclusion of a long note,
// leave the opening alone — and T7's own tests missed it because every fixture
// changed the whole body, so chunk 0 always differed and a fresh id was always
// minted.
//
// ⚠ AND THE PRE-T7 BEHAVIOUR WAS NOT CORRECT EITHER, which is why this is fixed
// rather than reverted. Before T7 the successor was written FIRST, so the reused
// id meant `Save` UPSERTED the predecessor's still-current chunk 0 — overwriting
// the very row ADR-038 exists to preserve ("end instead of overwrite"), and only
// then ending it. Silent destruction where this is a loud failure. The correct
// behaviour is neither: a correction MINTS, because ADR-038's contract is that the
// id changes.
func TestACorrectionMayKeepItsOpeningUnchanged(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct{ name, source string }{
		{"sourceless", ""},
		{"under a named source", "a-note.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			const team, wing = "team-keepopening", "wing_alpha"
			first, err := svc.Add(ctx, team, AddInput{
				Wing: wing, Room: "decisions", SourceFile: tc.source,
				Content: longNoteWithTail("the original conclusion"),
			})
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			if len(first.Drawers) < 2 {
				t.Fatalf("fixture stored %d chunk(s); this needs a multi-chunk memory so chunk 0 "+
					"can stay identical while a later chunk changes", len(first.Drawers))
			}
			oldRoot := first.Drawers[0].ID

			res, err := svc.Supersede(ctx, team, oldRoot,
				longNoteWithTail("the CORRECTED conclusion"), "the conclusion was wrong")
			if err != nil {
				t.Fatalf("correcting only the tail failed: %v\n  This is the commonest correction "+
					"there is, and it is the one T7's fixtures could not produce.", err)
			}

			// ADR-038's contract: the id CHANGES, and the old text stays readable by
			// its own id. A reused id would mean the predecessor was overwritten.
			if res.ID == oldRoot {
				t.Errorf("the successor reuses the predecessor's id %s — a correction that keeps "+
					"the id has overwritten the record it was supposed to end, which is what "+
					"ADR-038 forbids", short12(oldRoot))
			}

			// The predecessor must still be readable, ended, and linked.
			pred, err := svc.repo.MemoryChunks(ctx, team, oldRoot)
			if err != nil {
				t.Fatalf("read predecessor: %v", err)
			}
			for _, c := range pred {
				if c.ValidTo == "" {
					t.Errorf("chunk %d of the corrected memory is still current", c.ChunkIndex)
				}
				if c.SupersededBy != res.ID {
					t.Errorf("chunk %d links to %q, not to the successor", c.ChunkIndex, c.SupersededBy)
				}
			}
			if !strings.Contains(pred[0].Content, "A LONG NOTE whose opening") {
				t.Errorf("the predecessor's chunk 0 no longer holds its own text — it was "+
					"overwritten rather than ended:\n  %.80q", pred[0].Content)
			}

			// And the successor is whole and current.
			succ, err := svc.repo.MemoryChunks(ctx, team, res.ID)
			if err != nil {
				t.Fatalf("read successor: %v", err)
			}
			var body strings.Builder
			for _, c := range succ {
				if c.ValidTo != "" {
					t.Errorf("the successor's chunk %d is already ended", c.ChunkIndex)
				}
				body.WriteString(c.Content)
			}
			if !strings.Contains(body.String(), "the CORRECTED conclusion") {
				t.Error("the successor does not carry the corrected conclusion")
			}
		})
	}
}
