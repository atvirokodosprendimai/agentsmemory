package palace

import (
	"context"
	"errors"
	"testing"
)

// ADR-038 T4. Correcting a memory writes a NEW record and ends the old one with a
// reason; an agent cannot erase.

func TestCorrectingAMemorySupersedesIt(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-sup"

	first, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "we use Kafka"})
	old := first.Drawers[0].ID

	res, err := svc.Supersede(ctx, team, old, "we use NATS", "rebalancing stalled under load")
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if res.ID == old {
		t.Fatal("a supersede must mint a NEW record, not edit the old one in place")
	}
	if res.Supersedes != old {
		t.Errorf("the new record names %q as its predecessor; want %q", res.Supersedes, old)
	}
	prev, err := svc.GetAnyVersion(ctx, team, old)
	if err != nil {
		t.Fatalf("the superseded row must still be readable: %v", err)
	}
	if prev.ValidTo == "" {
		t.Error("the old record was not ended")
	}
	if prev.EndedReason != "rebalancing stalled under load" {
		t.Errorf("EndedReason = %q; the reason is the only thing worth keeping about an ending", prev.EndedReason)
	}
	if prev.SupersededBy != res.ID {
		t.Errorf("SupersededBy = %q; want the successor %q — the LINK is what a tombstone cannot carry", prev.SupersededBy, res.ID)
	}
}

func TestTheEndedTextIsStillReadableById(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-sup2"

	first, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "the rejected alternative"})
	old := first.Drawers[0].ID
	if _, err := svc.Supersede(ctx, team, old, "the chosen one", "measured slower"); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	got, err := svc.GetAnyVersion(ctx, team, old)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != "the rejected alternative" {
		t.Errorf("content = %q; ending is not deleting, and the rejected alternative is the thing "+
			"that is irrecoverable at any price", got.Content)
	}
}

func TestUpdateWithoutAReasonIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-sup3"
	first, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "x"})
	for _, reason := range []string{"", "  "} {
		if _, err := svc.Supersede(ctx, team, first.Drawers[0].ID, "y", reason); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("supersede with reason %q returned %v; want ErrInvalidInput", reason, err)
		}
	}
}

func TestInvalidateDrawerEndsWithNoSuccessor(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-inv"

	first, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "we are not doing this after all"})
	id := first.Drawers[0].ID
	if err := svc.InvalidateDrawer(ctx, team, id, "the plan was dropped"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	d, err := svc.GetAnyVersion(ctx, team, id)
	if err != nil {
		t.Fatalf("the retracted row must still be readable through the history route: %v", err)
	}
	if d.ValidTo == "" {
		t.Error("the record was not ended")
	}
	if d.SupersededBy != "" {
		t.Errorf("SupersededBy = %q; a retraction that replaces nothing must not invent a successor — "+
			"forcing one would make an agent file a placeholder memory to express an absence", d.SupersededBy)
	}
	if d.EndedReason != "the plan was dropped" {
		t.Errorf("EndedReason = %q", d.EndedReason)
	}
}

func TestASupersedeCarriesAnchorsAsUnchecked(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-anch"

	first, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "explains the parser"})
	old := first.Drawers[0].ID
	if _, err := svc.AddAnchors(ctx, team, old, []AnchorInput{{Path: "internal/p.go", Snippet: "func Parse()"}}); err != nil {
		t.Fatalf("anchor: %v", err)
	}
	res, err := svc.Supersede(ctx, team, old, "explains the parser, corrected", "the old text named the wrong function")
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	carried, err := svc.AnchorsForDrawers(ctx, team, []string{res.ID})
	if err != nil {
		t.Fatalf("anchors: %v", err)
	}
	if len(carried[res.ID]) != 1 {
		t.Fatalf("the successor carries %d anchors; want 1 — anchors are scarce (41 of 2,029 drawers "+
			"carry one) and clearing them on every correction spends what the palace barely has",
			len(carried[res.ID]))
	}
	if got := carried[res.ID][0].Status; got != AnchorUnchecked {
		t.Errorf("carried anchor status = %q; want %q. Verification is CLIENT-side — the server has no "+
			"repository — so a carried anchor must never read as verified until a client says so",
			got, AnchorUnchecked)
	}
	// The predecessor keeps its own: it keeps its text, so its pin is still true of it.
	kept, _ := svc.AnchorsForDrawers(ctx, team, []string{old})
	if len(kept[old]) != 1 {
		t.Errorf("the superseded row lost its anchor (%d remain); it keeps its text, so it keeps its pin", len(kept[old]))
	}
}
