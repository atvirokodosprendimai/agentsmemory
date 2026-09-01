package palace

import (
	"context"
	"testing"
)

// liveWingRoot reports whether `<wing>.root --holds--> room:<wing>/llm_init` is
// current, which is what a session's first call resolves.
//
// It asks the graph the way the entry protocol does rather than counting rows,
// because the failure being tested is precisely a root that still RESOLVES over a
// room holding nothing — a row count cannot tell that from a healthy front door.
func liveWingRoot(t *testing.T, svc *Service, teamID, wing string) bool {
	t.Helper()
	got, err := svc.KGQuery(context.Background(), teamID, KGQueryInput{
		Entity: WingRootSubject(wing), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("query the wing root: %v", err)
	}
	return len(got.Facts) > 0
}

// TestAMoveOutOfTheEntryRoomEndsTheWingRoot covers the move-OUT half that ADR-045
// shipped without.
//
// EnsureWingRoot mints a root when a record lands in the entry room; nothing ended
// it when the last one left. endDerivedEdgesFor cannot, because it filters
// `object IN drawerIDs` and the root edge's object is the ROOM node — so the move
// took the record's own holds edge and left the root current over an empty room.
//
// ⚠ THE FAILURE IS A CONFIDENT EMPTY ANSWER, NOT AN ERROR. `<wing>.root` goes on
// resolving `matched`, and the hop a session makes next returns zero edges. That is
// the state BackfillWingRoots refuses to create on the boot path, arriving through
// the move path instead. Reported by review on PR #147.
func TestAMoveOutOfTheEntryRoomEndsTheWingRoot(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-move-out", "wing_alpha"

	added, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The craft tier, then this project's root.",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !liveWingRoot(t, svc, team, wing) {
		t.Fatal("filing into the entry room did not mint a wing root, so this test cannot " +
			"observe one being released — the fixture, not the fix, is wrong")
	}

	elsewhere := "decisions"
	if _, err := svc.Update(ctx, team, added.Drawers[0].ID, DrawerPatch{Room: &elsewhere}); err != nil {
		t.Fatalf("move out of the entry room: %v", err)
	}

	if liveWingRoot(t, svc, team, wing) {
		t.Error("the wing root still resolves after the last record left the entry room. A " +
			"session's first call answers `matched`, and the hop it makes next holds nothing — " +
			"a front door onto an empty room, which reads as an answer rather than an absence")
	}
}

// TestAMoveOutOfTheEntryRoomKeepsARootTheRoomStillEarns is the other half, and it is
// what stops the fix above from being "end the root on any move out".
//
// A wing whose entry room still holds a live record must keep its root. Without this
// the release would fire on the first of two entry records to move and take the front
// door with it, which is a worse failure than the one being fixed — the room is
// readable and the name is gone.
func TestAMoveOutOfTheEntryRoomKeepsARootTheRoomStillEarns(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-move-keep", "wing_alpha"

	first, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root-a",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The craft tier first.",
	})
	if err != nil {
		t.Fatalf("add the first entry record: %v", err)
	}
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root-b",
		Content: "WHAT IS STILL OPEN HERE? The second entry record, which stays put.",
	}); err != nil {
		t.Fatalf("add the second entry record: %v", err)
	}

	elsewhere := "decisions"
	if _, err := svc.Update(ctx, team, first.Drawers[0].ID, DrawerPatch{Room: &elsewhere}); err != nil {
		t.Fatalf("move the first record out: %v", err)
	}

	if !liveWingRoot(t, svc, team, wing) {
		t.Error("the wing root was released while the entry room still holds a live record, so " +
			"a readable front door lost its name — the release fired on the move rather than on " +
			"the room being empty")
	}
}

// TestAMoveBetweenWingsEntryRoomsMovesTheRoot pins the case the room check alone
// would miss: the record stays in AN entry room, just not in this wing's.
func TestAMoveBetweenWingsEntryRoomsMovesTheRoot(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, from, to = "team-root-move-wing", "wing_alpha", "wing_beta"

	added, err := svc.Add(ctx, team, AddInput{
		Wing: from, Room: EntryRoom, SourceFile: "root",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? This wing's entry record.",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	dest := to
	if _, err := svc.Update(ctx, team, added.Drawers[0].ID, DrawerPatch{Wing: &dest}); err != nil {
		t.Fatalf("move to the other wing's entry room: %v", err)
	}

	if liveWingRoot(t, svc, team, from) {
		t.Error("the source wing kept its root after its only entry record moved to another " +
			"wing. The record is still in an entry room, which is why a room-only check misses " +
			"this — but it is not in THIS wing's, and that is the one the root names")
	}
	if !liveWingRoot(t, svc, team, to) {
		t.Error("the destination wing has no root after an entry record moved into it, so the " +
			"move-IN half did not fire and the memory is unreachable by the one address a " +
			"session can guess")
	}
}

// TestTheWingRootIsMintedOnceHoweverOftenItIsEnsured pins the idempotence that
// BackfillWingRoots takes for granted on every boot.
//
// ⚠ FOUND BY MUTATION. Deleting EnsureWingRoot's check-then-return — the branch
// that finds the existing triple and stops — left the whole package green. The
// backfill's own comment two hundred lines below says "EnsureWingRoot is
// idempotent either way", and that sentence was resting on a branch nothing
// asserted.
//
// What it would cost is unbounded rather than cosmetic: BackfillWingRoots runs on
// every prepared boot and calls this for EVERY wing, so a lost early return adds
// one `<wing>.root --holds--> room:<wing>/llm_init` edge per wing per restart. A
// session's first call then resolves to a pile of identical edges that grows with
// uptime, and nothing in the response says which of them is the front door —
// because they all are.
//
// The id is derived from the time of writing, so duplicates do not collide on the
// primary key: two calls a second apart produce two rows, which is why the
// existence check is the only thing preventing this.
func TestTheWingRootIsMintedOnceHoweverOftenItIsEnsured(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-idem", "wing_alpha"

	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom,
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The entry record that mints the root.",
	}); err != nil {
		t.Fatalf("add the entry record: %v", err)
	}

	// Three more ensures, standing in for three restarts of a server whose
	// backfill visits this wing every time.
	for i := 0; i < 3; i++ {
		if err := svc.repo.EnsureWingRoot(ctx, team, wing); err != nil {
			t.Fatalf("ensure %d: %v", i, err)
		}
	}

	got, err := svc.KGQuery(ctx, team, KGQueryInput{
		Entity: WingRootSubject(wing), Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("query the wing root: %v", err)
	}
	if len(got.Facts) != 1 {
		t.Errorf("%s resolves to %d current edges after four ensures, want exactly 1. A backfill "+
			"runs on every boot, so a root minted per restart grows without bound and a session's "+
			"first call cannot tell which edge is the front door",
			WingRootSubject(wing), len(got.Facts))
	}
}
