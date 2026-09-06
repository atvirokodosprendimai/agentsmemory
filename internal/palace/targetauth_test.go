package palace

import (
	"context"
	"strings"
	"testing"
)

// TestAuthorizationChecksTheIdentifierItExposes is the class audit for a defect
// that appeared twice, in code whose own comments describe the threat correctly.
//
// A wing check is only worth what it checks. EntryPoint placed each edge by its
// SourceDrawerID and then returned the whole edge — including f.Object, a
// DIFFERENT drawer. CorrectionsFor placed by SourceDrawerID and exposed
// row.Subject. Provenance is optional and independent of the record being named,
// so in both cases a locally-sourced row could disclose a foreign id.
//
// The rule the two share: authorize the identifier you are about to EXPOSE, not
// one that happens to sit beside it.
func TestAuthorizationChecksTheIdentifierItExposes(t *testing.T) {
	ctx := context.Background()
	const team = "t-targetauth"

	t.Run("EntryPoint does not disclose a foreign edge target", func(t *testing.T) {
		svc := newTestService(t)
		local, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: EntryRoom, Content: "the local front door"})
		if err != nil {
			t.Fatalf("add local: %v", err)
		}
		foreign, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", Content: "FOREIGN-TARGET another project's record"})
		if err != nil {
			t.Fatalf("add foreign: %v", err)
		}
		// Provenance LOCAL, target FOREIGN — the shape that separates the two
		// identifiers. Placing by provenance admits this edge.
		if _, err := svc.KGAdd(ctx, team, DerivedEdgeSubject("wing_acme", EntryRoom), DerivedEdgePredicate, foreign.Drawers[0].ID, "", "", "", "", local.Drawers[0].ID); err != nil {
			t.Fatalf("kgadd: %v", err)
		}

		res, err := svc.EntryPoint(ctx, team, "wing_acme")
		if err != nil {
			t.Fatalf("entry point: %v", err)
		}
		for _, e := range res.Edges {
			if e.Object == foreign.Drawers[0].ID {
				t.Errorf("the entry point disclosed a foreign wing's drawer id as an edge target: %s -> %s -> %s", e.Subject, e.Predicate, e.Object)
			}
		}
	})

	t.Run("Bootstrap does not disclose it either, through the entry point it copies", func(t *testing.T) {
		svc := newTestService(t)
		local, _ := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: EntryRoom, Content: "the local front door"})
		foreign, _ := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", Content: "FOREIGN-TARGET another project's record"})
		if _, err := svc.KGAdd(ctx, team, DerivedEdgeSubject("wing_acme", EntryRoom), DerivedEdgePredicate, foreign.Drawers[0].ID, "", "", "", "", local.Drawers[0].ID); err != nil {
			t.Fatalf("kgadd: %v", err)
		}

		res, err := svc.Bootstrap(ctx, team, "wing_acme")
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		// The bootstrap filters its EAGER content correctly and copies the entry
		// point verbatim, so a leak there travels through unexamined.
		for _, e := range res.EntryPoint.Edges {
			if e.Object == foreign.Drawers[0].ID {
				t.Errorf("the bootstrap's entry point disclosed a foreign drawer id")
			}
		}
		for _, p := range res.OnDemand {
			if p.ID == foreign.Drawers[0].ID {
				t.Errorf("a deferred pointer named a foreign drawer id, with the call that fetches it")
			}
		}
		for _, d := range res.Eager {
			if strings.Contains(d.Content, "FOREIGN-TARGET") {
				t.Errorf("foreign content was inlined")
			}
		}
	})

	t.Run("a correction's replacement id is authorized, not its provenance", func(t *testing.T) {
		svc := newTestService(t)
		wrong, _ := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the old answer about scheduling"})
		provenance, _ := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "a local note that happens to be the source"})
		foreignFix, _ := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", Content: "another project's correcting record"})

		// The CORRECTING record is foreign; the provenance is local.
		if _, err := svc.KGAdd(ctx, team, foreignFix.Drawers[0].ID, "supersedes", wrong.Drawers[0].ID, "", "", "", "", provenance.Drawers[0].ID); err != nil {
			t.Fatalf("kgadd: %v", err)
		}

		policy := svc.wingPolicyFor(ctx, team, "wing_acme")
		got, err := svc.CorrectionsFor(ctx, team, []string{wrong.Drawers[0].ID}, policy)
		if err != nil {
			t.Fatalf("corrections: %v", err)
		}
		for _, c := range got[normalizeEntityID(wrong.Drawers[0].ID)] {
			if c.ReplacementID == normalizeEntityID(foreignFix.Drawers[0].ID) || c.ReplacementID == foreignFix.Drawers[0].ID {
				t.Errorf("a foreign correcting record's id was disclosed because the LOCAL provenance was authorized instead")
			}
			// The correction itself must still travel — a reader who is not told
			// acts on something already contradicted.
			if c.Predicate == "" {
				t.Error("the correction was dropped entirely; its existence must travel even when its replacement may not")
			}
		}
	})
}

// TestARequiredWingMustActuallyBeNonEmpty pins the boundary check.
//
// "Required" in a tool schema means the key is PRESENT. An empty string satisfies
// it completely, and WingPolicy reads an empty viewer as "unscoped", treating
// every resolvable record as local — so a missing argument became a cross-wing
// read rather than an error.
func TestARequiredWingMustActuallyBeNonEmpty(t *testing.T) {
	ctx := context.Background()
	const team = "t-emptywing"
	svc := newTestService(t)
	if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: EntryRoom, Content: "another project's front door"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	for _, wing := range []string{"", "   "} {
		if _, err := svc.EntryPoint(ctx, team, wing); err == nil {
			t.Errorf("EntryPoint accepted wing=%q; an empty viewer makes every record read as local", wing)
		}
		if _, err := svc.Bootstrap(ctx, team, wing); err == nil {
			t.Errorf("Bootstrap accepted wing=%q; an empty viewer makes every record read as local", wing)
		}
	}
}

// TestTruncationCountsWhatIsNeitherInlinedNorPointedAt pins the contract with a
// foreign edge on the wing's entry node.
//
// The foreign edge never reaches either tier: EntryPoint authorizes every edge
// on its target with the same WingPolicy, so it is refused at the front door,
// before Bootstrap builds its id list. That is where the loss actually occurs —
// the tier-level refusal branches can fire only on a store mutation between the
// two checks — so the contract this fixture pins is that the ENTRY refusal is
// counted, and that eager + pointers + omitted equals the entry node's full
// out-degree. A lower bound here let the old code (pointers double-counted as
// omissions, the entry drop invisible) pass alongside the fix.
func TestTruncationCountsWhatIsNeitherInlinedNorPointedAt(t *testing.T) {
	ctx := context.Background()
	const team = "t-truncation"
	svc := newTestService(t)

	// Enough local records to fill the eager tier and spill into deferred.
	var local []string
	for i := 0; i < BootstrapEagerLimit+2; i++ {
		res, err := svc.Add(ctx, team, AddInput{
			Wing: "wing_acme", Room: EntryRoom,
			Content: "local start-here record number " + string(rune('a'+i)) + " with real text",
		})
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		local = append(local, res.Drawers[0].ID)
	}
	// One foreign record reachable through an entry edge whose provenance is
	// local — refused by policy in whichever tier it lands in.
	foreign, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", Content: "another project's record"})
	if err != nil {
		t.Fatalf("add foreign: %v", err)
	}
	if _, err := svc.KGAdd(ctx, team, DerivedEdgeSubject("wing_acme", EntryRoom), DerivedEdgePredicate, foreign.Drawers[0].ID, "", "", "", "", local[0]); err != nil {
		t.Fatalf("cross-wing entry edge: %v", err)
	}

	res, err := svc.Bootstrap(ctx, team, "wing_acme")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// The fixture staged exactly one foreign target, so the entry point must
	// report exactly one refusal — this is what proves the edge reached the
	// filter at all, rather than never being written.
	if res.EntryPoint.Refused != 1 {
		t.Errorf("EntryPoint.Refused = %d, want 1: the staged foreign edge was not refused where the filter runs",
			res.EntryPoint.Refused)
	}

	// EQUALITY, not a lower bound. The offer is the entry node's full out-degree:
	// the edges returned plus the ones refused at the door. The pre-fix code
	// accounts 14 for a 13-edge offer (pointers double-counted as omissions, the
	// entry refusal invisible), and a lower bound cleared both readings.
	offered := len(res.EntryPoint.Edges) + res.EntryPoint.Refused
	accounted := len(res.Eager) + len(res.OnDemand) + res.Truncation.Omitted
	if accounted != offered {
		t.Errorf("the entry point offered %d records and the response accounts for %d; the partition leaks",
			offered, accounted)
	}
	for _, p := range res.OnDemand {
		if p.ID == foreign.Drawers[0].ID {
			t.Error("a foreign record was named as a fetchable pointer")
		}
	}
	if res.Truncation.Omitted > 0 && res.Truncation.HowToFetch == "" {
		t.Error("records were omitted with no statement of what can be done about it")
	}
}
