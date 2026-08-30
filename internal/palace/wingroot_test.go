package palace

import (
	"context"
	"testing"
)

// TestFilingIntoTheEntryRoomMintsTheWingRoot pins the address a session can guess.
//
// ⚠ NOTHING IN THE CODEBASE CREATED A `.root` NODE BEFORE THIS. Grepped
// 2026-08-30 across non-test source: zero hits. Every `<wing>.root` in every
// palace was hand-authored, which is why three sessions in three repositories
// each got `unknown_term` from the first call their entry protocol prescribes —
// on a graph holding 839 entities and 545 current facts. The tier was not
// missing; the door had no name.
func TestFilingIntoTheEntryRoomMintsTheWingRoot(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root", "wing_alpha"

	// A drawer in an ordinary room must NOT mint a root: one root per wing, not
	// one per room, or the fan-out goes back onto a single node.
	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: "an ordinary memory"}); err != nil {
		t.Fatalf("seed ordinary drawer: %v", err)
	}
	if q, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: WingRootSubject(wing), Direction: "outgoing", Status: KGStatusCurrent,
	}); err != nil {
		t.Fatalf("query root: %v", err)
	} else if q.Resolution != KGResolutionUnknownTerm {
		t.Errorf("a drawer in room %q minted a wing root; resolution=%q, want unknown_term — "+
			"every room would otherwise put its fan-out back on one node", "decisions", q.Resolution)
	}

	// The entry room does.
	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION?"}); err != nil {
		t.Fatalf("seed entry drawer: %v", err)
	}
	q, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: WingRootSubject(wing), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("query root: %v", err)
	}
	if q.Resolution != KGResolutionMatched {
		t.Fatalf("%s does not resolve after a drawer was filed into %q: resolution=%q",
			WingRootSubject(wing), EntryRoom, q.Resolution)
	}
	// It must point at the node am_entry_point already reads, not at the drawer —
	// otherwise the by-name address and the mechanised one are two front doors.
	want := DerivedEdgeSubject(wing, EntryRoom)
	var found bool
	for _, f := range q.Facts {
		if f.Object == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the wing root does not point at %q; it must resolve to the node "+
			"am_entry_point reads, or the two addresses are different doors:\n%+v", want, q.Facts)
	}

	// And it is idempotent: a second entry-room drawer adds no second root edge.
	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom, Content: "a second standing rule"}); err != nil {
		t.Fatalf("seed second entry drawer: %v", err)
	}
	q2, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: WingRootSubject(wing), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("re-query root: %v", err)
	}
	if len(q2.Facts) != len(q.Facts) {
		t.Errorf("a second entry-room drawer grew the root from %d to %d edges; the root is one "+
			"hop to the room, not a list of its contents", len(q.Facts), len(q2.Facts))
	}
}
