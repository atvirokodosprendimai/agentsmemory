package palace

import (
	"context"
	"testing"
)

// TestAnchorsOnAnEndedDrawerAreNeverHandedToAVerifier closes a loop that could
// not be closed from the client side.
//
// ListAnchors already joins drawers so that an anchor whose drawer was DELETED
// never reaches a verifier — its own comment gives the reason: "it reports drift
// on a memory that is gone". An ENDED drawer is gone in every sense that matters
// to a reader, and the join did not cover it.
//
// The consequence was a permanent false alarm, reported from another project on
// 2026-09-03. A superseded record's anchors are drifted ALMOST BY CONSTRUCTION,
// because a record is usually superseded precisely when the code it pinned
// changed. So the sweep reports drift, and the one call that could clear it —
// am_update_drawer with code_anchors — refuses an ended record with "correct the
// record that replaced it, not the one it replaced". That refusal is right: ADR-038
// ends records instead of overwriting them, and anchors are stored per drawer, so
// there is no way to fix the row and no way to stop it being asked about. Every
// session in that repository would have re-reported it forever.
//
// Excluding them at the source is chosen over the two alternatives the report
// offered. A separate "not actionable" bucket still spends a verifier's file reads
// and a reader's attention on a record nobody can act on; allowing anchor-only
// writes to an ended record reopens exactly the history rewriting ADR-038 exists
// to prevent. Skipping needs no new tool surface and no new field — the successor
// carries its own anchors, and it is the record a session should be reading.
func TestAnchorsOnAnEndedDrawerAreNeverHandedToAVerifier(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	const team = "team-anchors-ended"

	live, err := svc.Add(ctx, team, AddInput{
		Wing: "wing_acme", Room: "decisions", Content: "a memory that stays current",
	})
	if err != nil {
		t.Fatalf("add live: %v", err)
	}
	doomed, err := svc.Add(ctx, team, AddInput{
		Wing: "wing_acme", Room: "decisions", Content: "a memory that gets superseded",
	})
	if err != nil {
		t.Fatalf("add doomed: %v", err)
	}

	for _, d := range []struct{ id, snippet string }{
		{live.Drawers[0].ID, "func Live() error {"},
		{doomed.Drawers[0].ID, "func Doomed() error {"},
	} {
		if _, err := svc.AddAnchors(ctx, team, d.id, []AnchorInput{
			{Path: "internal/x/y.go", Snippet: d.snippet, Repo: "agentsmemory"},
		}); err != nil {
			t.Fatalf("add anchor on %s: %v", d.id, err)
		}
	}

	// Both are handed out while both records are current.
	before, err := svc.ListAnchors(ctx, team, AnchorFilter{})
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("before retraction: got %d anchors, want 2 — the fixture cannot show the change", len(before))
	}

	if err := svc.InvalidateDrawer(ctx, team, doomed.Drawers[0].ID, "superseded by the test"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	after, err := svc.ListAnchors(ctx, team, AnchorFilter{})
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("after retraction: got %d anchors, want 1 — an ended drawer's anchor is still being handed to verifiers, and nothing can ever clear it", len(after))
	}
	if after[0].DrawerID != live.Drawers[0].ID {
		t.Errorf("the surviving anchor belongs to %s, want the still-current drawer %s", after[0].DrawerID, live.Drawers[0].ID)
	}

	// The row is EXCLUDED, not deleted. An ended record keeps its text, so its pin
	// is still true of that text and an auditor asking for the whole corpus must
	// still see it — that storage guarantee predates this change and survives it.
	// Without this half the fix would be indistinguishable from quietly dropping
	// anchors on supersession, which is a different and worse behaviour.
	audited, err := svc.ListAnchors(ctx, team, AnchorFilter{IncludeEnded: true})
	if err != nil {
		t.Fatalf("list including ended: %v", err)
	}
	if len(audited) != 2 {
		t.Errorf("IncludeEnded returned %d anchors, want 2 — the ended record's pin must still be readable, only not handed to a verifier", len(audited))
	}
}
