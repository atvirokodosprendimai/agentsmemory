package main

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestWingRootBackfillIsRegistered covers the rung palace's own tests cannot see.
//
// ⚠ A BACKFILL THAT NOTHING CALLS IS THE DEFECT THIS REPO KEEPS SHIPPING.
// internal/palace proves BackfillWingRoots mints a root; it cannot prove any
// server ever runs it, and the wing that needed it most is one nobody will write
// to again. This drives the real boot path: seed an entry room, delete the root
// the write-time mint made, reopen through buildServices, and require the door to
// have a name again.
//
// The fixture check between the delete and the reopen is load-bearing: the seed
// MINTS the root, so a delete that missed would leave every assertion below
// passing with the call in main.go removed.
func TestWingRootBackfillIsRegistered(t *testing.T) {
	ctx := context.Background()
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	const wing = "wing_alpha"
	if _, err := svc.drawers.Add(ctx, teamID, palace.AddInput{
		Wing: wing, Room: "llm_init", Content: "WHAT MUST I LOAD AT THE START OF A SESSION?",
	}); err != nil {
		t.Fatalf("seed the entry room: %v", err)
	}

	root := palace.WingRootSubject(wing)
	if err := svc.gdb.Exec("DELETE FROM kg_triples WHERE team_id = ? AND subject = ?", teamID, root).Error; err != nil {
		t.Fatalf("unroot: %v", err)
	}
	if resolves(ctx, t, svc, teamID, root) {
		t.Fatalf("%s still resolves after the delete, so this test would pass with the backfill "+
			"severed — the seed mints the root itself", root)
	}

	// The boot path, which is the thing under test.
	reopened, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !resolves(ctx, t, reopened, teamID, root) {
		t.Errorf("%s does not resolve after a prepared boot. The write-time mint cannot reach a "+
			"wing that has stopped writing, so without BackfillWingRoots in buildServicesWith's "+
			"prepare block that wing answers unknown_term to the first call the entry protocol "+
			"prescribes — forever", root)
	}
}

// TestTheReadOnlyPathMintsNothing keeps the backfill on the writing side.
//
// ⚠ inspectServices EXISTS SO doctor NEVER WRITES: a checker that repaired the
// corpus would be reporting on a palace it had just changed. The backfill sits
// inside `prepare`, and this is what says so — moving it one line out of that
// block is invisible to every other test.
func TestTheReadOnlyPathMintsNothing(t *testing.T) {
	ctx := context.Background()
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	const wing = "wing_alpha"
	if _, err := svc.drawers.Add(ctx, teamID, palace.AddInput{
		Wing: wing, Room: "llm_init", Content: "WHAT MUST I LOAD AT THE START OF A SESSION?",
	}); err != nil {
		t.Fatalf("seed the entry room: %v", err)
	}
	root := palace.WingRootSubject(wing)
	if err := svc.gdb.Exec("DELETE FROM kg_triples WHERE team_id = ? AND subject = ?", teamID, root).Error; err != nil {
		t.Fatalf("unroot: %v", err)
	}
	sqlDB, err := svc.gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	// Closed before the read-only open: query_only is a connection property, and a
	// writable handle left open would let the inspection path write through it.
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close the writing handle: %v", err)
	}

	inspect, err := inspectServices(cfg)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if resolves(ctx, t, inspect, teamID, root) {
		t.Errorf("the read-only path minted %s. A checker must report the palace as it is, not "+
			"repair the evidence before reporting on it", root)
	}
}

// resolves says whether an entity has any current outgoing fact.
func resolves(ctx context.Context, t *testing.T, svc *services, teamID, entity string) bool {
	t.Helper()
	q, err := svc.drawers.KGQuery(ctx, teamID, palace.KGQueryInput{
		Entity: entity, Direction: "outgoing", Status: palace.KGStatusCurrent,
	})
	if err != nil {
		t.Fatalf("query %s: %v", entity, err)
	}
	return len(q.Facts) > 0
}
