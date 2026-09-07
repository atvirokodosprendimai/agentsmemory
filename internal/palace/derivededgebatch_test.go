package palace

import (
	"context"
	"testing"
)

// TestTwoRootsSharingASourceFileEachGetAnEdge closes the question BACKLOG.md
// bounded rather than answered: whether two roots sharing (wing, room,
// source_file) in ONE batch both become reachable.
//
// They must. attachDerivedEdge sets the edge's OBJECT to the drawer's own id, so
// two memories sharing a source_file are two distinct edges, not one edge written
// twice — and the duplicate that a per-key skip was guarding against cannot occur:
// attachDerivedEdge already returns EdgeAlreadyDerived from CurrentTripleID. The
// skip therefore suppressed a legitimate edge and manufactured an orphan, on the
// import path, where same-key batches are the ordinary case rather than the
// exotic one.
//
// The measurement that made this worth writing: of 1,992 root drawers on the
// local palace, 1,046 carry no derived edge and 555 of those share a key with
// another root. That is a ceiling and not a count — sharing a key across
// different batches is harmless — and this test is what tells the two apart.
func TestTwoRootsSharingASourceFileEachGetAnEdge(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing, room = "team-samekey", "w", "r"

	batch := []Drawer{
		{ID: "drawer-same-key-1", Wing: wing, Room: room, SourceFile: "notes.md", Content: "retention window policy"},
		{ID: "drawer-same-key-2", Wing: wing, Room: room, SourceFile: "notes.md", Content: "cache eviction policy"},
	}
	svc.attachDerivedEdgeTo(ctx, team, batch)

	for _, d := range batch {
		if !d.HasEdge {
			t.Errorf("root %s reports no edge; a second root sharing (wing, room, source_file) "+
				"in one batch was skipped, so the memory is filed and unreachable by traversal", d.ID)
		}
		rows, err := svc.writer.KGTriplesByObject(ctx, team, normalizeEntityID(d.ID), KGStatusCurrent, "", kgPage{})
		if err != nil {
			t.Fatalf("read edges for %s: %v", d.ID, err)
		}
		if len(rows) != 1 {
			t.Errorf("root %s has %d current edge(s), want 1", d.ID, len(rows))
		}
	}
}
