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

// TestAnEntryRecordThatChunksIsServedWholeHoweverItArrives replaces
// TestAnEntryRecordThatChunksIsRefused, which pinned a limit ADR-046 deleted.
//
// ⚠ THE ORIGINAL WAS WRITTEN FROM A REAL REPORT and its evidence still stands: a
// session filed a ~1750-rune entry record minutes after reading "keep it under 1600
// runes", and said why — "I cannot count runes and did not try to bound it. Nothing
// warned me; am_add_drawer returned chunks: 2 as a success." The conclusion drawn then
// was that the server must count and refuse. The conclusion ADR-046 draws is that a
// limit an agent cannot measure should not exist: am_bootstrap now reassembles, so
// there is nothing to protect against.
//
// What is KEPT is the shape that made the original valuable. A review found the first
// version's guard sat in Add and was walked past by two doors, so the test drove all
// three. Those three doors still matter — they are now the three ways a chunked entry
// record can reach the store, and each must be SERVED WHOLE rather than refused. Same
// coverage, opposite expectation.
func TestAnEntryRecordThatChunksIsServedWholeHoweverItArrives(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-entrysize", "wing_alpha"

	long := longNote(headA, tailA) // built to exceed ChunkSize by construction

	// Door 1: a direct write.
	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom, SourceFile: "d1", Content: long}); err != nil {
		t.Fatalf("a multi-chunk entry record was refused: %v", err)
	}
	if !eagerServes(t, ctx, svc, team, wing, long) {
		t.Error("door 1 (Add): the record was accepted and the eager tier did not serve it whole — " +
			"which is the state the deleted refusal existed to prevent, now reached through the " +
			"front door instead of around it")
	}

	// An ordinary room was always allowed to chunk, and still is. Kept so a future
	// change that reinstates a room-keyed limit cannot do it silently.
	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", SourceFile: "d0", Content: long}); err != nil {
		t.Errorf("a multi-chunk memory in an ordinary room was refused: %v", err)
	}

	// Door 2: a CORRECTION. Supersede and content-Update call prepareWrite directly
	// and never touch Add, which is why the original guard had to live there. That
	// remains the interesting path: correcting an entry record is the case that
	// motivated the limit in the first place.
	short, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "d2",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? — v1",
	})
	if err != nil {
		t.Fatalf("seed a short entry record: %v", err)
	}
	grown := longNote(headB, tailB)
	if _, err := svc.Supersede(ctx, team, short.Drawers[0].ID, grown, "the protocol grew"); err != nil {
		t.Fatalf("a CORRECTION that grows an entry record past one chunk was refused: %v", err)
	}
	if !eagerServes(t, ctx, svc, team, wing, grown) {
		t.Error("door 2 (Supersede): a corrected entry record is not served whole; the correction " +
			"is precisely the case the original limit was written for")
	}

	// Door 3: NORMALISATION. prepareWrite trims the room, so " llm_init " lands in the
	// entry room. It must be served like any other entry record.
	padded := longNote(headA, tailB)
	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: " " + EntryRoom + " ", SourceFile: "d3", Content: padded}); err != nil {
		t.Fatalf("a multi-chunk entry record was refused for room %q: %v", " "+EntryRoom+" ", err)
	}
	if !eagerServes(t, ctx, svc, team, wing, padded) {
		t.Errorf("door 3 (padded room name): a record filed as %q is in the entry room after "+
			"normalisation and must be served whole like any other", " "+EntryRoom+" ")
	}
}

// eagerServes reports whether am_bootstrap's eager tier carries this exact text.
//
// Byte equality, because the failure being guarded is the ChunkOverlap seam: a
// reassembly that drops or duplicates 320 runes still produces a plausible length.
func eagerServes(t *testing.T, ctx context.Context, svc *Service, team, wing, want string) bool {
	t.Helper()
	got, err := svc.Bootstrap(ctx, team, wing)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for _, d := range got.Eager {
		if d.Content == want {
			return true
		}
	}
	return false
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

	q, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: id, Direction: "incoming", Status: KGStatusCurrent, IncludeContainment: true})
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

// TestEveryDoorThatEndsARowEndsItsDerivedEdge covers the three paths the first
// fix missed.
//
// ⚠ A REVIEW FOUND THEM AFTER SUPERSEDE WAS ALREADY FIXED. A row stops being
// current through a correction, through a re-file that purges a source, or
// through an outright retraction — and only the first was ending the server's own
// derived edge. The other two left the room's `holds` edge pointing at ended
// content, which an author has no call to remove.
func TestEveryDoorThatEndsARowEndsItsDerivedEdge(t *testing.T) {
	ctx := context.Background()
	const wing = "wing_alpha"

	edgesFor := func(t *testing.T, svc *Service, team, id string) bool {
		t.Helper()
		q, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: id, Direction: "incoming", Status: KGStatusCurrent, IncludeContainment: true})
		if err != nil {
			t.Fatalf("read incoming: %v", err)
		}
		for _, f := range q.Facts {
			if f.Predicate == DerivedEdgePredicate {
				return true
			}
		}
		return false
	}

	t.Run("retraction", func(t *testing.T) {
		svc := newTestService(t)
		const team = "team-retract"
		got, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION? — v1"})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		id := got.Drawers[0].ID
		if !edgesFor(t, svc, team, id) {
			t.Fatal("fixture has no derived edge, so this test would pass whatever the code does")
		}
		if err := svc.EndDrawer(ctx, team, id, "withdrawn"); err != nil {
			t.Fatalf("retract: %v", err)
		}
		if edgesFor(t, svc, team, id) {
			t.Error("a retracted drawer keeps its derived edge — the front door still points at a " +
				"record the same call withdrew, and no author call can end it")
		}
	})

	t.Run("re-file that drops a chunk", func(t *testing.T) {
		svc := newTestService(t)
		const team, src = "team-refile", "notes.md"
		first, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", SourceFile: src, Content: longNote(headA, tailA)})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		dropped := first.Drawers[len(first.Drawers)-1].ID
		if !edgesFor(t, svc, team, first.Drawers[0].ID) {
			t.Fatal("fixture has no derived edge on the root, so this proves nothing")
		}
		// Re-file the same source with a much shorter body: the tail chunk's
		// content key leaves the source and that row is ended.
		if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "decisions", SourceFile: src, Content: "a much shorter note"}); err != nil {
			t.Fatalf("re-file: %v", err)
		}
		if edgesFor(t, svc, team, dropped) {
			t.Error("a chunk dropped by a re-file keeps its derived edge, pointing at ended content")
		}
	})
}

// TestBackfillMintsARootForAnEntryRoomThatPredatesTheMint covers the wings the
// write-time mint can never reach.
//
// ⚠ THE MINT FIRES ON A WRITE, AND A WING THAT STOPPED WRITING KEEPS A NAMELESS
// DOOR. Measured 2026-08-31 on this project's own palace: wing_agentmemories
// filed its entry records at 09:34-09:46 on 2026-08-30 and the binary carrying
// attachWingRootEdge arrived between then and 10:27, when wing_craft filed one.
// Craft, playtrix and quality-harness were rooted; agentmemories answered
// unknown_term to the first call its entry protocol prescribes, and no amount of
// re-reading the protocol would have fixed it.
func TestBackfillMintsARootForAnEntryRoomThatPredatesTheMint(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-backfill", "wing_alpha"

	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION?",
	}); err != nil {
		t.Fatalf("seed entry drawer: %v", err)
	}
	// Reproduce the pre-mint corpus: the entry room and its drawer, no root.
	unroot(t, svc, team, wing)

	minted, err := svc.repo.BackfillWingRoots(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if minted != 1 {
		t.Errorf("backfill minted %d root(s), want 1", minted)
	}
	q, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: WingRootSubject(wing), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("query root: %v", err)
	}
	if q.Resolution != KGResolutionMatched {
		t.Fatalf("%s still does not resolve after the backfill: resolution=%q",
			WingRootSubject(wing), q.Resolution)
	}
	want := DerivedEdgeSubject(wing, EntryRoom)
	var found bool
	for _, f := range q.Facts {
		if f.Object == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the backfilled root does not point at %q — it must be the same door the "+
			"write-time mint builds, not a second one:\n%+v", want, q.Facts)
	}

	// A second run mints nothing: the count reports what CHANGED, so a backfill
	// that reports the whole corpus every boot says nothing when it matters.
	again, err := svc.repo.BackfillWingRoots(ctx)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if again != 0 {
		t.Errorf("a second backfill minted %d root(s) over an already-rooted palace, want 0", again)
	}
}

// TestBackfillLeavesAWingWithNoLiveEntryRecordNameless is the half that keeps the
// backfill from manufacturing a door onto an empty room.
//
// ⚠ A ROOT OVER AN EMPTY ROOM IS WORSE THAN unknown_term, because it reads as an
// answer. am_entry_point drops edges it cannot read and counts them in `refused`,
// so a wing whose every entry record was retracted would resolve `matched` with
// nothing behind it — the shape a session cannot tell from a curated tier.
func TestBackfillLeavesAWingWithNoLiveEntryRecordNameless(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-empty-entry", "wing_alpha"

	got, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION?",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	unroot(t, svc, team, wing)
	if err := svc.EndDrawer(ctx, team, got.Drawers[0].ID, "withdrawn"); err != nil {
		t.Fatalf("retract the only entry record: %v", err)
	}

	minted, err := svc.repo.BackfillWingRoots(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if minted != 0 {
		t.Errorf("backfill minted %d root(s) for a wing whose entry room holds no live record; "+
			"that root resolves matched with nothing behind it", minted)
	}

	// And a wing whose entry record was SUPERSEDED keeps a live successor, so it
	// must be rooted — the two look identical in the drawers table but for valid_to.
	const wing2 = "wing_beta"
	first, err := svc.Add(ctx, team, AddInput{
		Wing: wing2, Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION? — v1",
	})
	if err != nil {
		t.Fatalf("seed beta: %v", err)
	}
	if _, err := svc.Supersede(ctx, team, first.Drawers[0].ID,
		"WHAT MUST I LOAD AT THE START OF A SESSION? — v2", "sharpened"); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	unroot(t, svc, team, wing2)
	if minted, err := svc.repo.BackfillWingRoots(ctx); err != nil {
		t.Fatalf("backfill beta: %v", err)
	} else if minted != 1 {
		t.Errorf("backfill minted %d root(s) for a wing whose entry record was superseded; the "+
			"successor is current, so that door has something behind it", minted)
	}
}

// unroot deletes a wing's root edge, reproducing a palace whose entry room
// predates the mint.
//
// ⚠ IT VERIFIES THE DELETE. A seed that mints the root and a delete that misses
// leave the root in place, and every assertion afterwards passes with the
// backfill severed — the failure wingroot_test.go already guards against
// elsewhere ("fixture has no derived edge, so this test would pass whatever the
// code does").
func unroot(t *testing.T, svc *Service, teamID, wing string) {
	t.Helper()
	subID := normalizeEntityID(WingRootSubject(wing))
	err := svc.repo.db.Exec("DELETE FROM kg_triples WHERE team_id = ? AND subject = ?", teamID, subID).Error
	if err != nil {
		t.Fatalf("delete the root edge: %v", err)
	}
	q, err := svc.KGQuery(context.Background(), teamID, KGQueryInput{
		Entity: WingRootSubject(wing), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("confirm the fixture: %v", err)
	}
	if len(q.Facts) != 0 {
		t.Fatalf("the fixture still has %d root edge(s) for %s, so nothing after this asserts "+
			"anything about the backfill:\n%+v", len(q.Facts), wing, q.Facts)
	}
}

// TestBackfillLeavesAnEntryRoomWithNoLiveEdgeNameless covers the population the
// row-keyed version of this backfill rooted by mistake.
//
// ⚠ REPORTED BY REVIEW 2026-08-31, AND IT IS THE POPULATION THE BACKFILL EXISTS
// FOR. am_entry_point resolves the room node's `holds` edges, which
// attachDerivedEdge mints at FILE time — so a wing whose entry drawers predate
// THAT mechanism has current rows and no edges. Keying the backfill on rows gave
// it a root anyway, producing the shape this file's own comments call worse than
// unknown_term: the root resolves `matched` while the room behind it answers
// known_term_no_facts with zero edges. Measured before the fix, on a fixture
// seeded through Add with the root and the room's holds edges then deleted.
//
// No fixture reproduced it because every fixture seeds through Add, which mints
// the edges too — which is exactly why the review found it and the suite did not.
func TestBackfillLeavesAnEntryRoomWithNoLiveEdgeNameless(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-noedge", "wing_alpha"

	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION?",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The pre-attachDerivedEdge corpus: the drawer row is current, the root and the
	// room's holds edges are not there.
	root := normalizeEntityID(WingRootSubject(wing))
	room := normalizeEntityID(DerivedEdgeSubject(wing, EntryRoom))
	if err := svc.repo.db.Exec(
		"DELETE FROM kg_triples WHERE team_id = ? AND (subject = ? OR subject = ?)", team, root, room,
	).Error; err != nil {
		t.Fatalf("strip the edges: %v", err)
	}
	var live int64
	if err := svc.repo.db.Model(&drawerRow{}).
		Where("team_id = ? AND room = ? AND valid_to = ''", team, EntryRoom).
		Count(&live).Error; err != nil {
		t.Fatalf("count entry drawers: %v", err)
	}
	if live == 0 {
		t.Fatal("the fixture has no live entry drawer, so it does not reproduce the corpus " +
			"this test is about — a row-keyed backfill would skip it for the right reason")
	}

	minted, err := svc.repo.BackfillWingRoots(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if minted != 0 {
		t.Errorf("backfill minted %d root(s) for a wing whose entry room has a live ROW but no "+
			"live EDGE. The root then resolves `matched` while the room behind it answers "+
			"known_term_no_facts with zero edges — a door with a name and nothing behind it, "+
			"which is the answer this backfill's own guard calls worse than unknown_term", minted)
	}
}

// TestBackfillIgnoresARoomTheWildcardLetThrough pins the affix check against SQL
// LIKE's own dialect.
//
// ⚠ `_` IS A SINGLE-CHARACTER WILDCARD IN LIKE, AND EntryRoom IS "llm_init". The
// edge-keyed universe introduced a `subject LIKE 'room:%/llm_init'` prefilter that
// also matches llm-init, llm.init and llm init — and TrimPrefix/TrimSuffix no-op
// silently when the affix is absent, so what came through looked like a wing name
// and was rooted as one. Probed before the fix: a single drawer in a room called
// "llm-init" minted `wing_alpha/llm-init.root`, a root whose name is not a wing,
// pointing at a node that holds nothing — the exact shape this backfill exists to
// prevent, arriving through the query instead of through the guard. Reported by
// review 2026-08-31.
func TestBackfillIgnoresARoomTheWildcardLetThrough(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-wildcard", "wing_alpha"

	// Not the entry room, but it matches llm?init.
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: "llm-init", Content: "an ordinary memory in a room that merely looks like the entry room",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The fixture only proves something if the pattern really does match it.
	var matched []string
	if err := svc.repo.db.Model(&kgTripleRow{}).
		Where("predicate = ? AND valid_to = '' AND subject LIKE ?",
			normalizePredicate(DerivedEdgePredicate), "room:%/"+EntryRoom).
		Distinct().Pluck("subject", &matched).Error; err != nil {
		t.Fatalf("read the prefilter: %v", err)
	}
	if len(matched) == 0 {
		t.Skip("this SQL dialect does not treat _ as a wildcard, so the fixture cannot reproduce " +
			"the defect and would pass for the wrong reason")
	}

	minted, err := svc.repo.BackfillWingRoots(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if minted != 0 {
		t.Errorf("backfill minted %d root(s) from a room named %q, which is not the entry room. "+
			"The LIKE prefilter matched it and the trim no-opped, so the subject was read as a "+
			"wing name — the root that produces is named for a room and points at nothing",
			minted, "llm-init")
	}
	var roots []string
	if err := svc.repo.db.Model(&kgTripleRow{}).
		Where("valid_to = '' AND subject LIKE ?", "%.root").
		Distinct().Pluck("subject", &roots).Error; err != nil {
		t.Fatalf("read roots: %v", err)
	}
	for _, r := range roots {
		if strings.Contains(r, "/") {
			t.Errorf("minted the root %q — a root's name is a WING, and this one carries a room "+
				"path, so nothing will ever resolve it", r)
		}
	}
}
