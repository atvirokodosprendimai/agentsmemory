package palace

import (
	"context"
	"strings"
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

// TestAnEntryRecordThatChunksIsRefused pins a limit no agent can measure.
//
// ⚠ REPORTED BY A SESSION THAT BROKE IT IN THE SAME TURN IT READ THE RULE. It
// filed a ~1750-rune entry record minutes after reading "keep it under 1600
// runes", and said why: "I cannot count runes and did not try to bound it.
// Nothing warned me; am_add_drawer returned chunks: 2 as a success." The server
// counts instead, and refuses rather than warning — a warning beside a success is
// the shape that was already ignored once.
func TestAnEntryRecordThatChunksIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-entrysize", "wing_alpha"

	long := longNote(headA, tailA) // built to exceed ChunkSize by construction

	_, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom, Content: long})
	if err == nil {
		t.Fatal("a multi-chunk entry record was accepted; the eager tier serves one chunk, so " +
			"the rest would arrive cut with nothing marking it partial")
	}
	// The refusal has to be actionable: name the room and the remedy, or it is a
	// wall rather than an instruction.
	if !strings.Contains(err.Error(), EntryRoom) {
		t.Errorf("the refusal does not name the room it binds: %v", err)
	}
	if !strings.Contains(err.Error(), "ONE chunk") {
		t.Errorf("the refusal does not say what the limit is: %v", err)
	}

	// ⚠ AND IT BINDS ONLY THE ENTRY ROOM. Every other room may chunk freely; this
	// is the one whose read path cannot reassemble.
	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: long}); err != nil {
		t.Errorf("a multi-chunk memory in an ordinary room was refused: %v", err)
	}

	// A short one is fine, and still mints the root.
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION? — short enough",
	}); err != nil {
		t.Fatalf("a single-chunk entry record was refused: %v", err)
	}
}

// TestCorrectingAnEntryRecordEndsItsDerivedEdge pins the front door.
//
// ⚠ THE AUTHOR CANNOT FIX THIS ONE, WHICH IS WHY THE SERVER MUST. A derived edge
// is minted by the server from the room, so when a correction ends the drawer the
// edge points at, no call exists that would let the author repoint it. Reported
// 2026-08-30 by a session that corrected its own entry record and found
// am_entry_point returning both rows, the ENDED one listed first because it is
// older — a front door whose first edge errors on read.
func TestCorrectingAnEntryRecordEndsItsDerivedEdge(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-frontdoor", "wing_alpha"

	first, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION? — v1",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := svc.Supersede(ctx, team, first.Drawers[0].ID,
		"WHAT MUST I LOAD AT THE START OF A SESSION? — v2", "sharpened")
	if err != nil {
		t.Fatalf("correct: %v", err)
	}

	q, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: DerivedEdgeSubject(wing, EntryRoom), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("read the entry room's edges: %v", err)
	}
	for _, f := range q.Facts {
		if f.Object == first.Drawers[0].ID {
			t.Errorf("the entry room still points at the SUPERSEDED record %s — a session "+
				"fetching that edge gets an error at the front door, and the author has no "+
				"call that would end a derived edge:\n%+v", short12(f.Object), q.Facts)
		}
	}
	var pointsAtSuccessor bool
	for _, f := range q.Facts {
		if f.Object == res.ID {
			pointsAtSuccessor = true
		}
	}
	if !pointsAtSuccessor {
		t.Errorf("the entry room does not point at the correction %s; ending the old edge must "+
			"not leave the door pointing at nothing:\n%+v", short12(res.ID), q.Facts)
	}
}

// TestCorrectingADrawerLeavesAuthoredEdgesAlone is the other half, and it was
// UNASSERTED until a mutant found it.
//
// ⚠ REMOVING THE `derived = true` FILTER PASSED THE WHOLE SUITE. The correction
// would then have ended every authored edge pointing at the superseded row too —
// somebody's deliberate `qualifies`, `supersedes` or `must.*` pointer, silently,
// with no call to restore it. The doc comment claimed that was "the opposite
// defect" and nothing held the claim. This does.
//
// The asymmetry is the whole rule: a DERIVED edge is the server's, so the server
// must clean it up; an AUTHORED edge is a person's, so the server must not touch
// it — even when the row it names has been superseded, because the author may
// mean exactly that.
func TestCorrectingADrawerLeavesAuthoredEdgesAlone(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-authored", "wing_alpha"

	target, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", Content: "the record someone pointed at"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := target.Drawers[0].ID

	// An authored edge, the shape start-here prescribes for a correction.
	if _, err := svc.KGAdd(ctx, team, "some-other-record", "qualifies", id, "", "", "", "", id); err != nil {
		t.Fatalf("author an edge: %v", err)
	}

	if _, err := svc.Supersede(ctx, team, id, "the record, corrected", "sharpened"); err != nil {
		t.Fatalf("correct: %v", err)
	}

	q, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: id, Direction: "incoming", Status: KGStatusCurrent})
	if err != nil {
		t.Fatalf("read incoming edges: %v", err)
	}
	var authoredSurvives bool
	for _, f := range q.Facts {
		if f.Predicate == "qualifies" {
			authoredSurvives = true
		}
	}
	if !authoredSurvives {
		t.Errorf("correcting the drawer ended an AUTHORED edge pointing at it — that pointer is "+
			"someone's deliberate act and there is no call that would restore it:\n%+v", q.Facts)
	}
}
