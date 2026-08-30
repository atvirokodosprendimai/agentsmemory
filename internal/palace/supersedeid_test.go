package palace

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// longNote builds a note long enough to chunk, with an opening and a conclusion
// that vary independently.
//
// ⚠ CALLERS MUST PASS HEADS OF EQUAL LENGTH, and the fixtures below do. The body
// is generated until the note passes ChunkSize, so a head of a different length
// shifts every chunk boundary after it — and a test meaning to change ONE chunk
// would then change all of them, quietly becoming the "everything changed"
// fixture whose absence let this defect ship.
func longNote(head, tail string) string {
	var b strings.Builder
	b.WriteString("OPENING: " + head + " ")
	for line := 0; b.Len() < ChunkSize+200; line++ {
		fmt.Fprintf(&b, "body line %d with distinct wording so overlap removal finds the seam. ", line)
	}
	b.WriteString(" CONCLUSION: " + tail)
	return b.String()
}

// The four halves of the fixture. The two heads are equal-length by construction
// (see longNote); the tails need not be, because nothing is generated after them.
const (
	headA = "opening variant AAA"
	headB = "opening variant BBB"
	tailA = "conclusion variant AAA"
	tailB = "conclusion variant BBB"
)

// TestACorrectionMintsWhicheverEndStaysTheSame is a REGRESSION test for a break
// this repository shipped on 2026-08-29 in ADR-044 T7.
//
// T7 made a correction atomic by ending the predecessor's chunks BEFORE writing
// the successor's rows, inside one transaction. But `prepareWrite` resolves ids
// OUTSIDE that transaction, and it reuses the id of any CURRENT row already
// holding a chunk's content key — deliberately, so re-filing unchanged text keeps
// anchors pinned to it. When a correction leaves a chunk byte-identical, that
// lookup returns the PREDECESSOR's own id for it; the swap then ends that row;
// and the insert collides with it:
//
//	save drawers: constraint failed: UNIQUE constraint failed: drawers.team_id, drawers.id
//
// Bisected: 0a3beb6 (before T7) succeeded, a7f2898 (T7) failed.
//
// ⚠ THE FIXTURE TABLE IS THE POINT. T7's own tests missed this because every one
// of them replaced the WHOLE body, so no chunk was ever byte-identical and a
// fresh id was always minted. Three mutants were graded against that suite and
// none reached this path — a mutant proves a test NOTICES a change, never that
// its inputs can REACH the branch that breaks. So this table enumerates which END
// of the note stays the same, because that decides which chunk carries a
// colliding id:
//
//   - only the tail changes — the opening is byte-identical, so CHUNK 0 collides.
//     The commonest correction there is: fix the conclusion of a long note.
//   - only the opening changes — the tail is byte-identical, so a LATER CHUNK
//     collides. Contributed by a peer session that re-tested the fix on
//     2026-08-30 and pointed out that the first shape says nothing about this one.
//
// ⚠ AND THE PRE-T7 BEHAVIOUR WAS NOT CORRECT EITHER, which is why this was fixed
// rather than reverted. Before T7 the successor was written FIRST, so the reused
// id meant `Save` UPSERTED the predecessor's still-current chunk — overwriting the
// very row ADR-038 exists to preserve ("end instead of overwrite"), and only then
// ending it. Silent destruction where this is a loud failure. The correct
// behaviour is neither: a correction MINTS, because ADR-038's contract is that the
// id changes.
func TestACorrectionMintsWhicheverEndStaysTheSame(t *testing.T) {
	ctx := context.Background()

	shapes := []struct {
		name string
		// before and after are the two versions of the note.
		before, after string
		// survivor is text the PREDECESSOR must still hold, and the successor must
		// NOT. It is deliberately drawn from the end that CHANGED: the unchanged end
		// is byte-identical in both versions, so an assertion over it holds whether
		// the row was ended or overwritten, and therefore asserts nothing.
		survivor string
	}{
		{
			name:     "only the tail changes so chunk 0 is byte-identical",
			before:   longNote(headA, tailA),
			after:    longNote(headA, tailB),
			survivor: tailA,
		},
		{
			name:     "only the opening changes so the last chunk is byte-identical",
			before:   longNote(headA, tailA),
			after:    longNote(headB, tailA),
			survivor: headA,
		},
	}
	sources := []struct{ name, source string }{
		{"sourceless", ""},
		{"under a named source", "a-note.md"},
	}

	for _, shape := range shapes {
		for _, src := range sources {
			t.Run(shape.name+"/"+src.name, func(t *testing.T) {
				svc := newTestService(t)
				const team, wing = "team-keepopening", "wing_alpha"

				first, err := svc.Add(ctx, team, AddInput{
					Wing: wing, Room: "decisions", SourceFile: src.source,
					Content: shape.before,
				})
				if err != nil {
					t.Fatalf("seed: %v", err)
				}
				if len(first.Drawers) < 2 {
					t.Fatalf("fixture stored %d chunk(s); this needs a multi-chunk memory so one "+
						"chunk can stay identical while another changes", len(first.Drawers))
				}
				oldRoot := first.Drawers[0].ID
				oldIDs := map[string]int{}
				for _, d := range first.Drawers {
					oldIDs[d.ID] = d.ChunkIndex
				}

				res, err := svc.Supersede(ctx, team, oldRoot, shape.after, "the note was wrong")
				if err != nil {
					t.Fatalf("correcting this shape failed: %v\n  A chunk that did not change hands "+
						"prepareWrite the predecessor's own id, and the insert then collides with "+
						"the row the swap just ended.", err)
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

				// ⚠ EVERY chunk, compared byte-for-byte against what was seeded.
				if len(pred) != len(first.Drawers) {
					t.Fatalf("the predecessor now has %d chunk(s), seeded with %d",
						len(pred), len(first.Drawers))
				}
				var predBody strings.Builder
				for i, c := range pred {
					if c.Content != first.Drawers[i].Content {
						t.Errorf("the predecessor's chunk %d no longer holds its own text — it was "+
							"overwritten rather than ended:\n  want %.60q\n  got  %.60q",
							i, first.Drawers[i].Content, c.Content)
					}
					predBody.WriteString(c.Content)
				}
				if !strings.Contains(predBody.String(), shape.survivor) {
					t.Errorf("the predecessor lost %q, the text that distinguishes it from its "+
						"successor — it was overwritten rather than ended", shape.survivor)
				}

				// And the successor is whole, current, and minted THROUGHOUT.
				succ, err := svc.repo.MemoryChunks(ctx, team, res.ID)
				if err != nil {
					t.Fatalf("read successor: %v", err)
				}
				var body strings.Builder
				for _, c := range succ {
					if c.ValidTo != "" {
						t.Errorf("the successor's chunk %d is already ended", c.ChunkIndex)
					}
					// ⚠ EVERY chunk, not just the root. The re-mint covers any prepared row
					// whose id belongs to the record being ended, and a defect confined to a
					// NON-ZERO chunk would pass a root-only assertion while colliding exactly
					// as chunk 0 used to. Raised by a peer session re-testing this fix on
					// 2026-08-30, which read the successor whole and checked its chunk ids.
					if oldChunk, reused := oldIDs[c.ID]; reused {
						t.Errorf("the successor's chunk %d reuses id %s, which belongs to the "+
							"predecessor's chunk %d — that row was just ended, so this insert "+
							"collides with it", c.ChunkIndex, short12(c.ID), oldChunk)
					}
					if c.ChunkIndex > 0 && c.ParentID != res.ID {
						t.Errorf("the successor's chunk %d has parent %s, not the successor's root "+
							"%s — a reminted root must be followed through its children or they "+
							"point at the record being ended",
							c.ChunkIndex, short12(c.ParentID), short12(res.ID))
					}
					body.WriteString(c.Content)
				}
				if strings.Contains(body.String(), shape.survivor) {
					t.Errorf("the successor still carries %q, which belongs to the predecessor — "+
						"the correction did not take", shape.survivor)
				}
			})
		}
	}
}
