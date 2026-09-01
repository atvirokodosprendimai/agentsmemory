package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// twoChunkContent builds text that ChunkText splits into exactly two chunks, with
// the SECOND chunk's content controlled independently of the first.
//
// The chunker is a fixed-size rune window: chunk k is runes[k*step : k*step+size]
// with step = ChunkSize - ChunkOverlap. So `head` of exactly step runes followed by
// `tail` of exactly ChunkSize runes yields chunk 0 = head + tail's first overlap
// runes, and chunk 1 = tail verbatim. Two memories sharing `tail` and differing in
// `head` therefore collide on chunk 1's content key and on nothing else — which is
// the only shape that can prove a partial failure rolls back.
func twoChunkContent(head, tail rune) string {
	step := ChunkSize - ChunkOverlap
	return strings.Repeat(string(head), step) + strings.Repeat(string(tail), ChunkSize)
}

// TestAMoveRelocatesEveryChunkOfAMemory is ADR-045's central claim.
//
// A memory over ChunkSize is several rows sharing a parent, and Update used to
// resolve them only to REFUSE — the one write path in this package that treated a
// memory as a row while Delete, Supersede and InvalidateDrawer all treated it as a
// memory. The refusal was not protecting an invariant; it was the honest answer of
// a function that could do 1/N of the job, and its message said so ("Moving a whole
// multi-chunk memory is not expressible yet").
//
// The move changes no content, so this asserts the three things that follow from
// that and are the whole reason it is safe: the ids are unchanged (so every
// knowledge-graph fact, anchor and tunnel pointing at them survives), the content is
// unchanged, and BOTH scopes agree — the new wing returns the memory and the old one
// does not, which is the split-scope harm the refusal was written against.
func TestAMoveRelocatesEveryChunkOfAMemory(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-move"

	long := strings.Repeat("The zephyrine retention window is THIRTY days and this forces chunking. ", 40)
	added, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", SourceFile: "policy", Content: long})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture produced %d chunk(s); this test is about a memory of several rows", len(added.Drawers))
	}
	before, err := svc.repo.MemoryChunks(ctx, team, added.Drawers[0].ID)
	if err != nil {
		t.Fatalf("chunks before: %v", err)
	}

	// Addressed through the LAST chunk, not the root: a caller holds whichever id a
	// search handed back, and the memory is the unit either way.
	dest := "wing_beta"
	if _, err := svc.Update(ctx, team, before[len(before)-1].ID, DrawerPatch{Wing: &dest}); err != nil {
		t.Fatalf("move a multi-chunk memory: %v", err)
	}

	after, err := svc.repo.MemoryChunks(ctx, team, added.Drawers[0].ID)
	if err != nil {
		t.Fatalf("chunks after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("the move changed the chunk count from %d to %d; a move re-chunks nothing, and a "+
			"changed count means ids were re-minted and every pointer at them is now dangling",
			len(before), len(after))
	}
	for i := range before {
		if after[i].ID != before[i].ID {
			t.Errorf("chunk %d was re-minted (%s -> %s); ADR-045 rests on ids being stable across a "+
				"move, because nothing repoints a knowledge-graph fact or an anchor",
				i, short12(before[i].ID), short12(after[i].ID))
		}
		if after[i].Content != before[i].Content {
			t.Errorf("chunk %d's content changed during a move", i)
		}
		if after[i].Wing != dest {
			t.Errorf("chunk %d is still in %q; the memory is now split across scopes, which is "+
				"exactly the harm the old refusal existed to prevent", i, after[i].Wing)
		}
		if after[i].ParentID != before[i].ParentID {
			t.Errorf("chunk %d's parentage changed during a move (%q -> %q)", i, before[i].ParentID, after[i].ParentID)
		}
	}

	// The index has to agree, or the memory is unreachable at its new address and
	// still answers at the old one. This is the assertion that covers the vector
	// payload without reaching into the store.
	found, err := svc.Search(ctx, team, SearchQuery{Query: "zephyrine retention window", Wing: dest, Limit: 5, SkipTelemetry: true})
	if err != nil {
		t.Fatalf("search the destination wing: %v", err)
	}
	if len(found) == 0 {
		t.Error("the destination wing returns nothing; the rows moved and the index did not, so the " +
			"memory is unreachable where it now lives")
	}
	stale, err := svc.Search(ctx, team, SearchQuery{Query: "zephyrine retention window", Wing: "wing_alpha", Limit: 5, SkipTelemetry: true})
	if err != nil {
		t.Fatalf("search the origin wing: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("the origin wing still returns %d hit(s) after the move", len(stale))
	}
}

// TestAMoveThatCollidesOnAnyChunkRelocatesNone is ADR-045's falsification.
//
// content_key hashes wing and room, so relocating an N-chunk memory recomputes N
// keys against a partial unique index and any one of them can collide with a memory
// already at the destination. The decision FAILS if a collision on chunk k leaves
// chunks 0..k-1 relocated: that is the split-scope state the refusal prevented,
// reintroduced by a non-atomic fix, and it would ship looking like a working feature
// because the error is returned either way.
//
// The fixture collides on chunk 1 and NOT chunk 0 deliberately — a fixture where
// every chunk collides fails on the first row and has nothing to roll back, so it
// cannot exercise the branch this test exists for.
func TestAMoveThatCollidesOnAnyChunkRelocatesNone(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, src = "team-move-collide", "policy"
	const origin, dest = "wing_alpha", "wing_beta"

	if _, err := svc.Add(ctx, team, AddInput{Wing: dest, Room: "decisions", SourceFile: src, Content: twoChunkContent('c', 'b')}); err != nil {
		t.Fatalf("seed the destination: %v", err)
	}
	moving, err := svc.Add(ctx, team, AddInput{Wing: origin, Room: "decisions", SourceFile: src, Content: twoChunkContent('a', 'b')})
	if err != nil {
		t.Fatalf("seed the mover: %v", err)
	}
	before, err := svc.repo.MemoryChunks(ctx, team, moving.Drawers[0].ID)
	if err != nil {
		t.Fatalf("chunks before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("fixture produced %d chunk(s), want 2; the partial collision this test needs "+
			"depends on the chunk boundaries the helper assumes", len(before))
	}

	target := dest
	_, err = svc.Update(ctx, team, before[0].ID, DrawerPatch{Wing: &target})
	if err == nil {
		t.Fatal("the move was accepted; chunk 1 would then share a content key with a current row " +
			"at the destination, which is the duplicate the partial unique index refuses")
	}
	if !errors.Is(err, ErrContentKeyCollision) {
		t.Errorf("error is %v; without the sentinel a caller cannot tell a collision — a corpus "+
			"fact somebody must look at — from a transient failure worth retrying", err)
	}

	after, err := svc.repo.MemoryChunks(ctx, team, moving.Drawers[0].ID)
	if err != nil {
		t.Fatalf("chunks after: %v", err)
	}
	for i, c := range after {
		if c.Wing != origin {
			t.Errorf("chunk %d moved to %q even though the move was refused; the memory is now "+
				"split across two wings and no single scope returns all of it, which is worse "+
				"than the refusal this ADR removed", i, c.Wing)
		}
	}
}

// reachableFrom reports whether the room node's outgoing edges name this drawer.
// It asks the question the graph is actually walked with — am_kg_query from a room
// node — rather than inspecting rows, so an edge that exists but is ended or
// re-subjected reads as unreachable, which is what a traversing session experiences.
//
// ⚠ Status is passed EXPLICITLY. At the service layer an empty Status means
// KGStatusAll (kg.go:438) and the "current" default lives at the MCP registration
// (kgQueryDefaultStatus), so a test that omits it asks a question no session asks —
// ended edges come back and an ending reads as if it never happened. That is not a
// hypothetical: this helper's first draft omitted it and reported the old room's
// edge still live after endDerivedEdgesFor had correctly ended it.
func reachableFrom(t *testing.T, ctx context.Context, svc *Service, team, wing, room, id string) bool {
	t.Helper()
	q, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: DerivedEdgeSubject(wing, room), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("traverse from %s/%s: %v", wing, room, err)
	}
	for _, f := range q.Facts {
		if f.Object == id {
			return true
		}
	}
	return false
}

// TestAMoveEndsTheOldRoomsEdgeAndAttachesTheNew covers ADR-045 T2.
//
// The move was the only write path in this package that touched no derived edges.
// Add attaches, Supersede ends and re-attaches, Delete drops, InvalidateDrawer ends
// — the move did none, so a relocated memory stayed reachable from the room it had
// LEFT and was reachable from its new room only by accident of some later write.
//
// The single-chunk subtest is the falsifiability half and it is a SUBTEST rather
// than a sibling, so it sits inside the one command the acceptance fence runs: a
// check that proves this can fail must be inside the fence, or a mutation run
// returns "killed" from a gate that never executed it. It is also the older defect
// — single-chunk moves were always allowed, so this has been wrong the whole time,
// not since ADR-045.
func TestAMoveEndsTheOldRoomsEdgeAndAttachesTheNew(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"single chunk — the pre-existing defect", "the deploy runs at 04:00 UTC"},
		{"multi chunk — what ADR-045 newly allows", strings.Repeat("The zephyrine retention window is THIRTY days and this forces chunking. ", 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc := newTestService(t)
			const team, origin, dest = "team-edges", "wing_alpha", "decisions_old"

			added, err := svc.Add(ctx, team, AddInput{Wing: origin, Room: dest, SourceFile: "policy", Content: tc.content})
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			root := added.Drawers[0].ID
			if !reachableFrom(t, ctx, svc, team, origin, dest, root) {
				t.Fatalf("fixture: the drawer is not reachable from its own room before the move")
			}

			newRoom := "decisions_new"
			if _, err := svc.Update(ctx, team, root, DrawerPatch{Room: &newRoom}); err != nil {
				t.Fatalf("move: %v", err)
			}

			if reachableFrom(t, ctx, svc, team, origin, dest, root) {
				t.Error("the OLD room still holds an edge to the drawer after it moved away; a session " +
					"traversing that room is sent to a memory that is no longer there")
			}
			if !reachableFrom(t, ctx, svc, team, origin, newRoom, root) {
				t.Error("the NEW room has no edge to the drawer, so the memory is an orphan: reachable " +
					"by search and invisible to traversal")
			}
		})
	}
}
