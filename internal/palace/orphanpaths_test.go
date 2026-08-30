package palace

import (
	"context"
	"testing"
)

// TestEveryWritePathAttachesTheDerivedEdge is the class audit for ADR-036 T6.
//
// T6 attached a containment edge in Service.Add and proved it there. That proved
// ONE path. The repository's own characteristic defect is a capability that is
// finished and unreachable, and the shape it takes here is a capability reachable
// on the path somebody tested and no other — so the question is not "does the
// edge attach" but "which write paths attach it".
//
// Three write paths exist and two of them did not, both found by cross-checking
// rather than by reading T6:
//
//   - Service.Add's DEFERRED-EMBEDDING branch returns early, before the
//     attachment. A memory filed while the embedder is down is exactly the one a
//     later session most needs to find, and it was a permanent orphan.
//   - AbsorbDrawers (ADR-035's import path, merged 2026-08-26) writes through
//     SaveUnembedded directly and never goes through Add at all. A whole imported
//     dataset would have been unreachable by traversal.
//
// This test enumerates the paths rather than testing one, so a fourth write path
// added later fails here instead of quietly filing orphans.
func TestEveryWritePathAttachesTheDerivedEdge(t *testing.T) {
	const team = "t-orphan"

	for _, tc := range []struct {
		name string
		file func(t *testing.T, ctx context.Context, svc *Service) string
	}{
		{
			"Service.Add with an embedder",
			func(t *testing.T, ctx context.Context, svc *Service) string {
				res, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the normal filing path"})
				if err != nil {
					t.Fatalf("add: %v", err)
				}
				return res.Drawers[0].ID
			},
		},
		{
			"Service.Add with the embedder down (deferred)",
			func(t *testing.T, ctx context.Context, svc *Service) string {
				// The vector index is deferred; the TEXT is still filed, and the
				// text is the memory. It must still be reachable.
				res, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "filed while the embedder was unreachable"})
				if err != nil {
					t.Fatalf("add deferred: %v", err)
				}
				if !res.PendingEmbedding {
					t.Fatal("this case is meant to exercise the deferred path and did not; the test would prove nothing")
				}
				return res.Drawers[0].ID
			},
		},
		{
			"AbsorbDrawers (the import path)",
			func(t *testing.T, ctx context.Context, svc *Service) string {
				n, err := svc.AbsorbDrawers(ctx, team, []ImportDrawer{{
					Wing: "wing_acme", Room: "decisions", Content: "a row that arrived through import",
				}})
				if err != nil {
					t.Fatalf("absorb: %v", err)
				}
				if n != 1 {
					t.Fatalf("absorbed %d, want 1", n)
				}
				// The import path derives its own ids, so the id is recovered the
				// way any reader would: by listing what is in the room.
				ids, err := svc.repo.IDsBySource(ctx, team, "wing_acme", "decisions", "")
				if err != nil || len(ids) == 0 {
					t.Fatalf("could not find the imported drawer: %v (%d ids)", err, len(ids))
				}
				return ids[len(ids)-1]
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc := newTestService(t)
			if tc.name == "Service.Add with the embedder down (deferred)" {
				svc = newTestServiceWith(t, brokenEmbedder{})
			}

			id := tc.file(t, ctx, svc)

			// Reachability, not the presence of a row: walk out from the room node
			// and require the drawer to be found.
			q, err := svc.KGQuery(ctx, team, KGQueryInput{
				Entity: DerivedEdgeSubject("wing_acme", "decisions"), Direction: "outgoing",
			})
			if err != nil {
				t.Fatalf("traverse from the room node: %v", err)
			}
			for _, f := range q.Facts {
				if f.Object == id {
					return
				}
			}
			t.Errorf("a drawer filed through this path is not reachable from its room node; %d edges out, none naming it — it is an orphan", len(q.Facts))
		})
	}
}

// TestTheFactArmActuallyScores is the rung-4 check T1 was missing.
//
// T1 proved the arm is DECLARED and REGISTERED, and four separate gates agreed.
// None of them asked whether the eval ever scores it. It did not: with no branch
// in evalCase's dispatch, the arm fell to `default`, where serviceForArm returns
// nil and the case is bypassed with ReasonOff. So the arm appeared in every
// table, passed every registration gate, and was structurally incapable of
// producing a number — the repository's characteristic defect, in the task whose
// whole purpose is to be the instrument.
//
// The lesson generalises past this arm: "is it registered" and "does it run" are
// different questions, and a registration gate answers only the first.
func TestTheFactArmActuallyScores(t *testing.T) {
	ctx := context.Background()
	const team = "t-armscores"
	svc := newTestService(t)

	filed, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the ledger service owns invoice numbering"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.KGAdd(ctx, team, "ledger service", "owns", "invoice numbering", "", "", "", "", filed.Drawers[0].ID); err != nil {
		t.Fatalf("kgadd: %v", err)
	}
	if _, err := svc.BackfillEntityLabels(ctx, team); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	gold := CanonicalFact("ledger service", "owns", "invoice numbering")
	rep, err := svc.EvaluateWith(ctx, team, []EvalCase{{
		Query: "who owns invoice numbering", Wing: "wing_acme",
		Category: CatFact, ExpectTriple: gold,
	}}, 10, EvalOptions{Arms: []string{string(ArmFactRetrieval)}}, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	var m *EvalMetrics
	for i := range rep.Arms {
		if rep.Arms[i].Arm == ArmFactRetrieval {
			m = &rep.Arms[i]
		}
	}
	if m == nil {
		t.Fatal("the fact arm produced no row at all; it is registered and never scored")
	}
	if len(m.Ranks) == 0 {
		t.Fatal("the fact arm produced no ranks; every case was bypassed, so the arm can never report a number")
	}
	if m.Ranks[0] == 0 {
		t.Errorf("the gold fact was not found by the arm that exists to find it; ranks=%v", m.Ranks)
	}

	// And the rate it reports agrees with those ranks, so the two numbers the
	// instrument publishes cannot drift apart.
	if got := FactAnswerRateFrom(m.Ranks); got.Answered != 1 || got.Cases != 1 {
		t.Errorf("answerable rate %s disagrees with ranks %v", got, m.Ranks)
	}
}

// TestATripleIDIsNotAStableGold pins WHY the eval corpus is keyed on the
// canonical subject|predicate|object rather than on a triple id.
func TestATripleIDIsNotAStableGold(t *testing.T) {
	// The id hashes validFrom and recordedAt, so the same fact recorded at two
	// different moments has two different ids. A corpus keyed on ids decays
	// silently: cases simply begin to miss, which reads as retrieval getting
	// worse rather than as the gold going stale.
	a := tripleID("s", "p", "o", "2024-01-01", "2026-08-26T10:00:00Z")
	b := tripleID("s", "p", "o", "2024-01-01", "2026-08-26T10:00:01Z")
	if a == b {
		t.Fatal("triple ids are stable after all — if this is now true, the canonical-fact gold could be simplified")
	}
	if CanonicalFact("s", "p", "o") != CanonicalFact("s", "p", "o") {
		t.Error("the canonical fact is not stable, which is the only property it exists for")
	}
}

// TestDeletingADrawerTakesItsDerivedEdge covers every deletion path, because a
// derived edge left behind is worse than an orphan drawer: it stays CURRENT and
// asserts a record exists at an id that no longer resolves.
//
// Re-filing CHANGED content is the case that accumulates them — a changed drawer
// gets a new id and a new edge, beside the stale one — so it is tested rather
// than reasoned about.
func TestDeletingADrawerTakesItsDerivedEdge(t *testing.T) {
	ctx := context.Background()
	const team = "t-purge"

	edgesNaming := func(t *testing.T, svc *Service, id string) int {
		t.Helper()
		q, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: id, Direction: "incoming", Status: KGStatusAll})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		return len(q.Facts)
	}

	t.Run("re-filing changed content leaves its edge pointing at a row that still resolves", func(t *testing.T) {
		svc := newTestService(t)
		first, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes.md", Content: "the original answer"})
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		old := first.Drawers[0].ID
		if edgesNaming(t, svc, old) == 0 {
			t.Fatal("the first filing got no edge, so this test cannot observe one being stranded")
		}
		if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes.md", Content: "a DIFFERENT answer entirely"}); err != nil {
			t.Fatalf("re-add: %v", err)
		}
		// The premise changed with ADR-038 T3 and the test changed with it. A re-file
		// no longer PURGES the chunk it replaced — it ENDS it, so the row is still
		// there and an edge naming it is not an orphan. What had to be true before
		// (no edge naming a deleted row) is now true a better way: nothing was
		// deleted, so nothing dangles.
		if n := edgesNaming(t, svc, old); n == 0 {
			t.Errorf("the edge naming %s is gone; a re-file must END the old chunk, not destroy it", old)
		}
		// GetAnyVersion: the question is whether the ROW is still there, and an ended
		// record is still a record. The default route hides it (T5) and that is not
		// what makes an edge dangle — a deleted row is.
		if _, err := svc.GetAnyVersion(ctx, team, old); err != nil {
			t.Errorf("the edge names %s but the row no longer resolves (%v) — THAT is the orphan "+
				"this test exists to catch", old, err)
		}
	})

	t.Run("deleting a drawer takes its edge", func(t *testing.T) {
		svc := newTestService(t)
		filed, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "a memory that will be deleted"})
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		id := filed.Drawers[0].ID
		if edgesNaming(t, svc, id) == 0 {
			t.Fatal("no edge to begin with")
		}
		if _, err := svc.Delete(ctx, team, id); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if n := edgesNaming(t, svc, id); n != 0 {
			t.Errorf("%d edge(s) survived the drawer they name", n)
		}
	})

	t.Run("an AUTHORED edge survives, deliberately", func(t *testing.T) {
		svc := newTestService(t)
		filed, _ := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "a memory somebody pointed at by hand"})
		id := filed.Drawers[0].ID
		if _, err := svc.KGAdd(ctx, team, "Release Notes", "documents", id, "", "", "", "", ""); err != nil {
			t.Fatalf("author: %v", err)
		}
		if _, err := svc.Delete(ctx, team, id); err != nil {
			t.Fatalf("delete: %v", err)
		}
		// A writer's placement outliving its drawer is a fact a human should
		// resolve, not something a purge erases silently.
		if n := edgesNaming(t, svc, id); n == 0 {
			t.Error("the authored edge was deleted too; only server-derived edges may be purged")
		}
	})
}
