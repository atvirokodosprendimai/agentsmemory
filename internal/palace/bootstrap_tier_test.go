package palace

import (
	"context"
	"fmt"
	"testing"
)

// authorTier writes the two-tier entry the shipped protocol prescribes, exactly
// as an agent would: root --must--> root.must --ops--> root.must.ops --<leaf>--> id,
// with source_drawer_id and a hint on every leaf. It returns the leaf ids it
// filed, in the order it filed them.
func authorTier(t *testing.T, svc *Service, team, wing, tier, ns string, leaves map[string]string) {
	t.Helper()
	ctx := context.Background()
	root := WingRootSubject(wing)
	must := func(subject, predicate, object, hint, src string) {
		t.Helper()
		if _, err := svc.KGAdd(ctx, team, subject, predicate, object, "", "", "", hint, src); err != nil {
			t.Fatalf("kg_add %s -%s-> %s: %v", subject, predicate, object, err)
		}
	}
	must(root, tier, root+"."+tier, "", "")
	must(root+"."+tier, ns, root+"."+tier+"."+ns, "what is under here", "")
	for name, id := range leaves {
		must(root+"."+tier+"."+ns, name, id, "hint for "+name, id)
	}
}

// TestBootstrapServesTheTierAuthoredOnTheWingRoot pins the walk from the wing's
// by-name root to its must and ref leaves.
//
// Measured 2026-09-04 (issue #218): a tier authored exactly as the shipped
// protocol prescribes resolved perfectly by am_kg_query("<wing>.root") and
// am_bootstrap returned on_demand: null — EntryPoint resolves the entry ROOM node,
// the tier hangs off the wing ROOT node, and nothing walked from one to the other.
// The protocol's own cost argument depends on the tier being served as POINTERS:
// a session reads the hints and fetches two of forty, and a bootstrap that inlined
// them would be the 99KB protocol it replaced.
func TestBootstrapServesTheTierAuthoredOnTheWingRoot(t *testing.T) {
	ctx := context.Background()
	const team, wing = "t-tier", "wing_delta"
	svc := newTestService(t)

	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom,
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? The spine."}); err != nil {
		t.Fatalf("entry record: %v", err)
	}
	ops, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "gotchas", Content: "the ops leaf: reinstall round trip"})
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}
	eval, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "testing", Content: "the eval leaf: replay blames the embedding"})
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}
	authorTier(t, svc, team, wing, "must", "ops", map[string]string{"reinstall-round-trip": ops.Drawers[0].ID})
	authorTier(t, svc, team, wing, "ref", "eval", map[string]string{"replay-blames-the-embedding": eval.Drawers[0].ID})

	res, err := svc.Bootstrap(ctx, team, wing)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	byID := map[string]BootstrapPointer{}
	for _, p := range res.Tiers {
		byID[p.ID] = p
	}
	want := map[string]struct {
		tier  BootstrapTier
		under string
		hint  string
	}{
		ops.Drawers[0].ID:  {TierMust, wing + ".root.must.ops", "hint for reinstall-round-trip"},
		eval.Drawers[0].ID: {TierRef, wing + ".root.ref.eval", "hint for replay-blames-the-embedding"},
	}
	for id, w := range want {
		p, ok := byID[id]
		if !ok {
			t.Fatalf("leaf %s is not among the tier pointers; got %+v — this is the on_demand: null of #218", id, res.Tiers)
		}
		if p.Tier != w.tier || p.Under != w.under || p.Hint != w.hint || p.Fetch != "am_get_drawer" {
			t.Errorf("pointer for %s = %+v, want tier=%s under=%s hint=%q fetch=am_get_drawer", id, p, w.tier, w.under, w.hint)
		}
	}
	if len(res.Tiers) != 2 {
		t.Errorf("%d tier pointers, want 2: a structural node or the root's holds edge leaked in as a leaf: %+v", len(res.Tiers), res.Tiers)
	}
	// Pointers, never content: the tier's whole reason to exist is that it is
	// NOT carried inline.
	for _, d := range res.Eager {
		if d.ID == ops.Drawers[0].ID || d.ID == eval.Drawers[0].ID {
			t.Errorf("tier leaf %s was inlined into the eager tier", d.ID)
		}
	}
	if res.Truncation.TiersOmitted != 0 {
		t.Errorf("tiers_omitted = %d with nothing cut", res.Truncation.TiersOmitted)
	}

	t.Run("a wing with a root and no tier serves none, and says nothing was cut", func(t *testing.T) {
		if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_epsilon", Room: EntryRoom,
			Content: "WHAT MUST I LOAD AT THE START OF A SESSION? Nothing yet."}); err != nil {
			t.Fatal(err)
		}
		bare, err := svc.Bootstrap(ctx, team, "wing_epsilon")
		if err != nil {
			t.Fatal(err)
		}
		if len(bare.Tiers) != 0 || bare.Truncation.TiersOmitted != 0 {
			t.Errorf("a tierless wing reports tiers=%+v omitted=%d", bare.Tiers, bare.Truncation.TiersOmitted)
		}
	})
}

// TestATierLeafInAnotherWingIsRefusedAndCounted is the tenancy half: an edge
// can name any drawer, and a pointer names an id AND the call that fetches it,
// so an unauthorized pointer is actionable disclosure. Refused ones are counted,
// never listed — the same rule EntryPoint and the eager tier already follow.
func TestATierLeafInAnotherWingIsRefusedAndCounted(t *testing.T) {
	ctx := context.Background()
	const team = "t-tier-foreign"
	svc := newTestService(t)

	if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: EntryRoom,
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? Home."}); err != nil {
		t.Fatal(err)
	}
	foreign, err := svc.Add(ctx, team, AddInput{Wing: "wing_other", Room: "decisions", Content: "another project's decision"})
	if err != nil {
		t.Fatal(err)
	}
	local, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", Content: "this project's decision"})
	if err != nil {
		t.Fatal(err)
	}
	authorTier(t, svc, team, "wing_alpha", "must", "state", map[string]string{
		"foreign": foreign.Drawers[0].ID, "local": local.Drawers[0].ID,
	})

	res, err := svc.Bootstrap(ctx, team, "wing_alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Tiers {
		if p.ID == foreign.Drawers[0].ID {
			t.Fatalf("a leaf naming another wing's drawer was listed as a pointer: %+v", p)
		}
	}
	if len(res.Tiers) != 1 || res.Tiers[0].ID != local.Drawers[0].ID {
		t.Errorf("tiers = %+v, want exactly the local leaf", res.Tiers)
	}
	if res.Truncation.TiersOmitted != 1 {
		t.Errorf("tiers_omitted = %d, want 1: the refused leaf must be counted or the drop is silent", res.Truncation.TiersOmitted)
	}
}

// TestTheTierIsBoundedAndSaysSo pins bootstrapTierLimit and that the cut is
// reported rather than silent — the exact loss the protocol this call replaced
// suffered (74% of a prescribed tier lost to an unreported cap).
func TestTheTierIsBoundedAndSaysSo(t *testing.T) {
	ctx := context.Background()
	const team, wing = "t-tier-bound", "wing_big"
	svc := newTestService(t)

	if _, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: EntryRoom,
		Content: "WHAT MUST I LOAD AT THE START OF A SESSION? Too much."}); err != nil {
		t.Fatal(err)
	}
	leaves := map[string]string{}
	over := bootstrapTierLimit + 3
	for i := 0; i < over; i++ {
		d, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "gotchas",
			Content: fmt.Sprintf("gotcha number %d, distinct enough to be its own memory", i)})
		if err != nil {
			t.Fatalf("leaf %d: %v", i, err)
		}
		leaves[fmt.Sprintf("leaf-%02d", i)] = d.Drawers[0].ID
	}
	authorTier(t, svc, team, wing, "must", "gotchas", leaves)

	res, err := svc.Bootstrap(ctx, team, wing)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tiers) != bootstrapTierLimit {
		t.Errorf("%d tier pointers, want the bound %d", len(res.Tiers), bootstrapTierLimit)
	}
	if res.Truncation.TiersOmitted != over-bootstrapTierLimit {
		t.Errorf("tiers_omitted = %d, want %d", res.Truncation.TiersOmitted, over-bootstrapTierLimit)
	}
	if res.Truncation.HowToFetch == "" {
		t.Error("something was cut and the response does not say how to get it")
	}
}
