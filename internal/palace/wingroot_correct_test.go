package palace

import (
	"context"
	"strings"
	"testing"
)

// Issue #155 item 1. `endWingRootIfEntryRoomIsEmpty` had exactly one caller,
// `moveMemory` — and a patch carrying Content never reaches it: Update leaves at
// the supersede branch first, while still able to carry a new wing or room.
//
// So there were three doors out of the entry room and two of them were watched.
// `TestBackfillLeavesAWingWithNoLiveEntryRecordNameless` covers the boot path,
// `TestAMoveOutOfTheEntryRoomEndsTheWingRoot` the move path, and correcting the
// last entry record while relocating it in one call went through neither.
//
// ⚠ THE FAILURE IS A CONFIDENT EMPTY ANSWER RATHER THAN AN ERROR, which is why
// nothing noticed: `<wing>.root` goes on resolving `matched`, and the hop a
// session makes next returns zero edges. A front door onto an empty room reads as
// an answer, not as an absence.

// TestACorrectionThatLeavesTheEntryRoomEndsTheWingRoot is the door that was open.
func TestACorrectionThatLeavesTheEntryRoomEndsTheWingRoot(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-correct-out", "wing_alpha"

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

	// ONE call carrying both: this is the shape that reaches the supersede branch
	// and never reaches moveMemory.
	elsewhere := "decisions"
	corrected := "WHAT MUST I LOAD AT THE START OF A SESSION? This is no longer the entry record."
	if _, err := svc.Update(ctx, team, added.Drawers[0].ID, DrawerPatch{
		Content: &corrected,
		Room:    &elsewhere,
		Reason:  "the entry record moved to decisions and its text changed with it",
	}); err != nil {
		t.Fatalf("correct and relocate in one call: %v", err)
	}

	if liveWingRoot(t, svc, team, wing) {
		t.Error("the wing root still resolves after a correction moved the last record out of " +
			"the entry room. A session's first call answers `matched` and the hop it makes next " +
			"holds nothing, which reads as an answer rather than an absence")
	}
}

// TestACorrectionThatStaysInTheEntryRoomKeepsTheWingRoot is what stops the fix
// from being "end the root on any correction".
//
// It also pins the ORDER: the release runs after the successor is written, so a
// correction that stays put finds the room occupied. Run before persistRows it
// would see the predecessor already ended, call the room empty, and take the
// front door off a wing whose entry record is about to exist — a worse failure
// than the one being fixed, because the room is readable and the name is gone.
func TestACorrectionThatStaysInTheEntryRoomKeepsTheWingRoot(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-correct-stay", "wing_alpha"

	added, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The craft tier first.",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	corrected := "WHAT MUST I LOAD AT THE START OF A SESSION? The craft tier, then the project root."
	if _, err := svc.Update(ctx, team, added.Drawers[0].ID, DrawerPatch{
		Content: &corrected,
		Reason:  "the first version named only half of what a session must load",
	}); err != nil {
		t.Fatalf("correct in place: %v", err)
	}

	if !liveWingRoot(t, svc, team, wing) {
		t.Error("correcting the entry record in place ended the wing root. The room still holds " +
			"a live entry record, so the wing still has a front door — and losing the NAME while " +
			"the room is readable is worse than the dangling root this fix is about")
	}
}

// TestACorrectionOutOfTheEntryRoomKeepsARootTheRoomStillEarns is the move path's
// second half, applied to the third door: a wing whose entry room still holds
// another live record keeps its name.
func TestACorrectionOutOfTheEntryRoomKeepsARootTheRoomStillEarns(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-correct-keep", "wing_alpha"

	first, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root-a",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The craft tier first.",
	})
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root-b",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? And the corrections index after it.",
	}); err != nil {
		t.Fatalf("add second: %v", err)
	}

	elsewhere := "decisions"
	moved := "WHAT MUST I LOAD AT THE START OF A SESSION? Moved, and rewritten on the way."
	if _, err := svc.Update(ctx, team, first.Drawers[0].ID, DrawerPatch{
		Content: &moved,
		Room:    &elsewhere,
		Reason:  "one of two entry records left, and its text changed with it",
	}); err != nil {
		t.Fatalf("correct and relocate: %v", err)
	}

	if !liveWingRoot(t, svc, team, wing) {
		t.Error("the wing root was released while another live entry record remains. The release " +
			"must fire on the LAST record leaving, not the first")
	}
}

// TestRetractingTheLastEntryRecordEndsTheWingRoot is the FOURTH door, found by
// review of PR #325 after that PR closed the third and called it the last.
//
// InvalidateDrawer → EndDrawer ended the row and its derived holds edge and
// called endWingRootIfEntryRoomIsEmpty zero times, so retracting the last live
// entry record left <wing>.root current over a room holding nothing — through a
// tool on the agent surface, not an internal path.
//
// ⚠ THE HELPER'S OWN COMMENT ALREADY CLAIMED THIS POPULATION: "no live edge"
// covers a room whose records are all retracted as well as one whose records have
// left. The condition was written for this case and nothing invoked it here,
// which is §Reachability's defect one layer in from where #325 found it.
func TestRetractingTheLastEntryRecordEndsTheWingRoot(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-retract", "wing_alpha"

	added, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The craft tier, then this project's root.",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !liveWingRoot(t, svc, team, wing) {
		t.Fatal("filing into the entry room did not mint a wing root; the fixture is wrong")
	}

	if err := svc.InvalidateDrawer(ctx, team, added.Drawers[0].ID, "the entry record was withdrawn"); err != nil {
		t.Fatalf("retract: %v", err)
	}

	if liveWingRoot(t, svc, team, wing) {
		t.Error("the wing root still resolves after the last entry record was RETRACTED. " +
			"A session's first call answers `matched` and the hop it makes next holds nothing")
	}
}

// TestRetractingOneOfTwoEntryRecordsKeepsTheWingRoot is the over-firing half. A
// release on any retraction would take the front door off a wing whose entry room
// is still readable, which is worse than the dangling root.
func TestRetractingOneOfTwoEntryRecordsKeepsTheWingRoot(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-retract-keep", "wing_alpha"

	first, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root-a",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The craft tier first.",
	})
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root-b",
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? And the corrections index after it.",
	}); err != nil {
		t.Fatalf("add second: %v", err)
	}

	if err := svc.InvalidateDrawer(ctx, team, first.Drawers[0].ID, "one of two was withdrawn"); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if !liveWingRoot(t, svc, team, wing) {
		t.Error("the wing root was released while another live entry record remains")
	}
}

// TestRetractingAMultiChunkEntryRecordReleasesOnTheLastChunk covers why the
// release sits in EndDrawer rather than InvalidateDrawer: the multi-chunk loop
// then works for free, because each live chunk's own holds edge keeps the count
// above zero until the last one ends.
func TestRetractingAMultiChunkEntryRecordReleasesOnTheLastChunk(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-root-retract-chunks", "wing_alpha"

	long := "WHAT MUST I LOAD AT THE START OF A SESSION? " +
		strings.Repeat("A spine that points at ordinary memories, said at length so this record "+
			"is stored as several chunks sharing one parent. ", 30)
	added, err := svc.Add(ctx, team, AddInput{
		Wing: wing, Room: EntryRoom, SourceFile: "root-long", Content: long,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("the fixture produced %d chunk(s); this test needs a multi-chunk memory to say "+
			"anything about the last one", len(added.Drawers))
	}
	if err := svc.InvalidateDrawer(ctx, team, added.Drawers[0].ID, "the long entry record was withdrawn"); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if liveWingRoot(t, svc, team, wing) {
		t.Error("the wing root survived the retraction of every chunk of the only entry record")
	}
}
