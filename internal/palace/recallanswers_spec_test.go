package palace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"

	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Spec: docs/specs/2026-08-26-a-recall-that-answers.md
//
// These are DELIBERATELY RED stubs bound to @spec facts and scenarios. They
// compile and fail, which is the TDD red state the spec gate requires — a test
// that does not compile is not collectable, and an uncollectable test binds
// nothing. /quality-harness:adr-execute turns them green one task at a time.

func TestAWingScopedRecallNeverReturnsAnotherWingsFact(t *testing.T) {
	svc, team, ctx := factWorld(t)

	page, err := svc.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Query: "tell me about the ledger service", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// The sibling's fact is "ledger service deploys nightly". No part of it may
	// appear anywhere in the returned facts — not the subject, not the
	// predicate, not the object.
	for _, f := range page.Facts.Facts {
		if f.Predicate == "deploys" || f.Object == "nightly" {
			t.Errorf("a foreign wing's fact crossed the boundary: %s -> %s -> %s", f.Subject, f.Predicate, f.Object)
		}
	}
}

func TestARecallNamesTheWingsThatHoldTheAnswer(t *testing.T) {
	svc, team, ctx := factWorld(t)

	page, err := svc.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Query: "tell me about the ledger service", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !slices.Contains(page.Facts.ElsewhereWings, "wing_alpha") {
		t.Errorf("a derivable sibling wing holding a match was not named; got %v", page.Facts.ElsewhereWings)
	}
	// Naming a wing is a POINTER, not a crossing: the wing's name is all that
	// may travel.
	if slices.Contains(page.Facts.ElsewhereWings, "wing_acme") {
		t.Error("the searched wing was named as an elsewhere-wing; that is not somewhere else")
	}
}

func TestACorrectedRecordArrivesCarryingItsCorrection(t *testing.T) {
	// ALL THREE predicates, table-driven. Naming three and asserting one is how
	// `qualifies` was missed on 2026-08-25, when a session that ran only
	// `retracts` shipped a pointer to an ADR that was not on main.
	// The three names are written out HERE rather than ranged over
	// CorrectionPredicates. Iterating the very list under test means shrinking
	// that list just runs fewer subtests and passes — measured: a mutant cutting
	// it to {"retracts"} survived this test completely.
	for _, pred := range []string{"retracts", "supersedes", "qualifies"} {
		t.Run(pred, func(t *testing.T) {
			if !slices.Contains(CorrectionPredicates, pred) {
				t.Fatalf("%q is not swept by CorrectionPredicates; a correction of this kind would never be seen", pred)
			}
			ctx := context.Background()
			team := "t-f3-" + pred
			svc := newTestService(t)

			wrong, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the scheduler runs hourly"})
			if err != nil {
				t.Fatalf("add wrong: %v", err)
			}
			right, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the scheduler runs every five minutes"})
			if err != nil {
				t.Fatalf("add right: %v", err)
			}

			// The correcting record points AT the corrected one. That direction is
			// the whole point: it arrives as an INCOMING edge, which an outgoing
			// walk from the corrected record can never see.
			if _, err := svc.KGAdd(ctx, team, right.Drawers[0].ID, pred, wrong.Drawers[0].ID, "", "", "", "", right.Drawers[0].ID); err != nil {
				t.Fatalf("kgadd %s: %v", pred, err)
			}

			page, err := svc.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Query: "how often does the scheduler run", Limit: 10})
			if err != nil {
				t.Fatalf("search: %v", err)
			}

			var marked *SearchHit
			for i := range page.Hits {
				if page.Hits[i].MemoryID == wrong.Drawers[0].ID {
					marked = &page.Hits[i]
				}
			}
			if marked == nil {
				t.Fatalf("the corrected record is not on the page at all; %d hits", len(page.Hits))
			}
			if len(marked.Corrections) == 0 {
				t.Fatalf("the corrected record arrived with no correction; a reader would act on something already contradicted")
			}
			var sawPred, sawReplacement bool
			for _, c := range marked.Corrections {
				if c.Predicate == pred {
					sawPred = true
				}
				if c.ReplacementID != "" {
					sawReplacement = true
				}
			}
			if !sawPred {
				t.Errorf("the correction does not name its kind; got %+v", marked.Corrections)
			}
			if !sawReplacement {
				t.Errorf("the correction does not name what replaced the record; got %+v", marked.Corrections)
			}

			// The mark lands on the CORRECTED record and never on the correcting
			// one. Without this the sweep can key by the wrong endpoint and still
			// look right whenever both records are on the page — which is the
			// common case, and which let a direction mutant survive.
			for i := range page.Hits {
				if page.Hits[i].MemoryID == right.Drawers[0].ID && len(page.Hits[i].Corrections) > 0 {
					t.Errorf("the correcting record was itself marked as corrected: %+v", page.Hits[i].Corrections)
				}
			}

			// Marked, never hidden and never demoted. A retraction can itself be
			// wrong, so this is a signal for the reader rather than a gate.
			uncorrected, err := svc.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Query: "how often does the scheduler run", Limit: 10})
			if err != nil {
				t.Fatalf("search again: %v", err)
			}
			if len(uncorrected.Hits) != len(page.Hits) {
				t.Errorf("marking changed how many records came back: %d vs %d", len(uncorrected.Hits), len(page.Hits))
			}
		})
	}
}

func TestFactLookupMatchesBothEntityVocabularies(t *testing.T) {
	ctx := context.Background()
	const team = "t-f4"
	svc := newTestService(t)

	// A drawer whose extracted terms include "Ledger" — the frequency vocabulary
	// ADR-016 stamps. Nothing ever joins it to kg_entities.
	filed, err := svc.Add(ctx, team, AddInput{
		Wing: "wing_acme", Room: "decisions",
		Content: "Ledger is the service of record. Ledger issues every invoice number, and Ledger is audited quarterly.",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !slices.Contains(filed.Drawers[0].Entities, "Ledger") {
		t.Fatalf("the extractor did not stamp the term this test is about; entities=%v", filed.Drawers[0].Entities)
	}

	// A fact whose SUBJECT is that same term. It is deliberately never indexed as
	// an authored label: the entity vector index is skipped, so the only way to
	// reach this fact is through the extracted vocabulary.
	if _, err := svc.KGAdd(ctx, team, "Ledger", "audited", "quarterly", "", "", "", "", filed.Drawers[0].ID); err != nil {
		t.Fatalf("kgadd: %v", err)
	}

	bare := *svc
	bare.vectors = noEntityIndex{svc.vectors}
	page, err := bare.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Room: "decisions", Query: "Ledger", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	var found bool
	for _, f := range page.Facts.Facts {
		if f.Predicate == "audited" {
			found = true
		}
	}
	if !found {
		t.Errorf("a fact reachable only through the EXTRACTED vocabulary did not arrive; got %d facts: %+v", len(page.Facts.Facts), page.Facts.Facts)
	}
}

func TestFactAnswerableRateIsMeasured(t *testing.T) {
	// Rung 2, driven rather than parsed: TestEveryDeclaredArmIsRegistered proves
	// the identifier is MENTIONED in evalArms, which a comparison would satisfy.
	// This calls the function and looks in its output, so deleting the append
	// fails here even if the name survives elsewhere in the body.
	t.Run("the arm is registered", func(t *testing.T) {
		for _, rerank := range []bool{false, true} {
			if !slices.Contains(evalArms(EvalOptions{}, rerank), ArmFactRetrieval) {
				t.Errorf("rerank=%v: ArmFactRetrieval is declared but evalArms never returns it, so it appears in no table", rerank)
			}
		}
	})

	// The case set exists, is loadable, and every case has a gold triple. A
	// corpus that loads to zero cases would make every rate 0/0 and satisfy any
	// assertion about the rate vacuously.
	cases, err := LoadFactCases(filepath.Join("testdata", "factcases-synthetic.jsonl"))
	if err != nil {
		t.Fatalf("load fact cases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no fact cases loaded")
	}
	for i, c := range cases {
		if c.ExpectTriple == "" {
			t.Errorf("case %d has no gold triple; its answerable-rate contribution is unfalsifiable", i)
		}
		if c.Category != CatFact {
			t.Errorf("case %d is category %q, not %q — it would be averaged into single-hop and hidden", i, c.Category, CatFact)
		}
	}

	// The rate always carries its denominator. This is the assertion that stops
	// "40%" being quoted a month later over a corpus nobody can reconstruct.
	t.Run("the rate carries its denominator", func(t *testing.T) {
		r := FactAnswerRateFrom([]int{0, 3, 0, 1, 0})
		if r.Answered != 2 || r.Cases != 5 {
			t.Fatalf("got %d/%d, want 2/5", r.Answered, r.Cases)
		}
		if !strings.Contains(r.String(), "2/5") {
			t.Errorf("rate renders as %q, which does not carry its denominator", r.String())
		}
	})

	// The baseline is 0% BY CONSTRUCTION: nothing returns facts yet. Stating it
	// as a test rather than as prose is what makes a later non-zero result mean
	// something — the alternative is zero, so it cannot be noise.
	t.Run("the baseline is zero", func(t *testing.T) {
		ranks := make([]int, len(cases))
		base := FactAnswerRateFrom(ranks)
		if base.Fraction() != 0 {
			t.Fatalf("baseline is %s, want 0 — nothing on the search path returns facts yet", base)
		}
		if base.Cases != len(cases) {
			t.Errorf("baseline denominator is %d, want %d", base.Cases, len(cases))
		}
	})

	// The manifest is the redacted record of the real run. Its absence would mean
	// a rate with no auditable provenance at all.
	m, err := LoadFactCorpusManifest(filepath.Join("testdata", "factcases-manifest-2026-08-26.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.Cases <= 0 || m.Date == "" {
		t.Errorf("manifest carries cases=%d date=%q; a rate without a denominator and a date is unfalsifiable later", m.Cases, m.Date)
	}
}

func TestFactsOnThePageAreScoredByMRR(t *testing.T) {
	// F-6 is a property of the INSTRUMENT, provable before facts reach the page:
	// the fact arm produces the same Ranks slice every other arm produces, so the
	// existing statistics consume it unchanged. If it needed its own MRR path,
	// its numbers would not be comparable with any other row in the table.
	ranks := []int{1, 0, 2, 5, 0, 1}

	iv := BootstrapMRR(ranks)
	if iv.Hi <= 0 {
		t.Fatalf("BootstrapMRR over the fact arm's ranks gave %s; the fact arm is not scored on the shared statistic", iv)
	}
	if iv.Lo > iv.Hi {
		t.Errorf("interval %s is inverted", iv)
	}
	// The observed MRR must sit inside its own interval, which is what makes the
	// interval a statement about this arm rather than a decoration beside it.
	var mrr float64
	for _, r := range ranks {
		mrr += reciprocal(r)
	}
	mrr /= float64(len(ranks))
	if !iv.Contains(mrr) {
		t.Errorf("observed MRR %.3f lies outside its own interval %s", mrr, iv)
	}

	// The paired comparison is what makes a fact-arm change readable against a
	// control rather than against a remembered number.
	other := []int{2, 0, 3, 4, 0, 1}
	d := PairedDelta(ranks, other)
	if d.Lo > d.Hi {
		t.Fatalf("paired delta interval is inverted: [%v,%v]", d.Lo, d.Hi)
	}

	// A miss is rank 0 and must not be scored as a hit at rank 1 — the arithmetic
	// that would quietly turn every miss into a perfect answer.
	allMissed := BootstrapMRR([]int{0, 0, 0})
	if allMissed.Hi != 0 {
		t.Errorf("an all-miss arm scored %s, want an interval pinned at 0", allMissed)
	}

	// And the answerable-rate agrees with the ranks it was derived from, so the
	// two numbers the arm reports cannot drift apart.
	if got := FactAnswerRateFrom(ranks); got.Answered != 4 || got.Cases != 6 {
		t.Errorf("answerable rate %s disagrees with the ranks it came from; want 4/6", got)
	}
}

func TestAnEndedFactIsNeverPresentedAsCurrent(t *testing.T) {
	ctx := context.Background()
	const team = "t-f7"
	svc := newTestService(t)

	filed, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "Ledger runs on the old scheduler"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.KGAdd(ctx, team, "Ledger", "runs on", "old scheduler", "2024-01-01", "2025-01-01", "", "", filed.Drawers[0].ID); err != nil {
		t.Fatalf("kgadd ended: %v", err)
	}
	if _, err := svc.KGAdd(ctx, team, "Ledger", "runs on", "new scheduler", "2025-01-01", "", "", "", filed.Drawers[0].ID); err != nil {
		t.Fatalf("kgadd current: %v", err)
	}
	if _, err := svc.BackfillEntityLabels(ctx, team); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	page, err := svc.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Query: "what does Ledger run on", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	// The block must carry the live answer and not the dead one. 14 facts on the
	// live palace are already ended and unfiltered by search today, so this is a
	// real population, not a hypothetical.
	// Compared CANONICALLY, not on display names: one triple is returned by a
	// two-directional walk and the endpoint the walk did not start from falls
	// back to its entity id when name resolution misses, so a display-name
	// comparison depends on which view happened to survive dedup.
	var sawCurrent, sawEnded bool
	for _, f := range page.Facts.Facts {
		switch CanonicalFact(f.Subject, f.Predicate, f.Object) {
		case CanonicalFact("Ledger", "runs on", "new scheduler"):
			sawCurrent = true
		case CanonicalFact("Ledger", "runs on", "old scheduler"):
			sawEnded = true
		}
		if f.ValidTo != "" {
			t.Errorf("a fact with valid_to=%q was presented in the current block: %s -> %s -> %s", f.ValidTo, f.Subject, f.Predicate, f.Object)
		}
	}
	if sawEnded {
		t.Error("the superseded fact was returned as current")
	}
	if !sawCurrent {
		t.Errorf("the live fact did not arrive at all; got %d facts: %+v", len(page.Facts.Facts), page.Facts.Facts)
	}
}

func TestAFactsWingComesFromItsProvenance(t *testing.T) {
	// Driven at the policy itself, so each of the three states can fail on its
	// own. Routed through a search, an unlocatable fact and a foreign one both
	// produce "not in the block" and one assertion would cover both.
	wings := map[string]string{"d-here": "wing_acme", "d-there": "wing_alpha"}
	p := NewWingPolicy("wing_acme", func(_ context.Context, id string) (string, bool) {
		w, ok := wings[id]
		return w, ok
	})
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		id   string
		want WingPlacement
	}{
		{"provenance in the searched wing", "d-here", PlacementLocal},
		{"provenance in a sibling wing", "d-there", PlacementForeign},
		{"provenance that dangles", "d-gone", PlacementUnlocatable},
		{"no provenance at all", "", PlacementUnlocatable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := p.Place(ctx, tc.id)
			if got != tc.want {
				t.Errorf("Place(%q) = %q, want %q", tc.id, got, tc.want)
			}
			if got != PlacementLocal && p.MayReturnContent(got) {
				t.Errorf("content may be returned for a %q fact; only local content may cross", got)
			}
		})
	}

	// Unresolvable provenance is never LOCAL. That asymmetry is the whole point:
	// defaulting an unplaceable fact into the searched wing returns another
	// project's content under this project's name, and on today's corpus it
	// would do so for the majority of facts.
	if got, _ := p.Place(ctx, "d-gone"); got == PlacementLocal {
		t.Error("a dangling pointer was claimed for the searched wing")
	}
}

func TestReturningFactsDoesNotChangeDrawerRanking(t *testing.T) {
	svc, team, ctx := factWorld(t)
	q := SearchQuery{Wing: "wing_acme", Query: "tell me about the ledger service", Limit: 5}

	withFacts, err := svc.SearchPage(ctx, team, q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if withFacts.Facts.Empty() {
		t.Fatal("no fact block at all — this test would then be comparing two identical runs and proving nothing")
	}

	// The comparison that matters: the same query with the graph unreachable must
	// select and order the same drawers. F-9 is what keeps this decision
	// separable from a retrieval change — without it, any later measurement is of
	// two changes at once.
	bare := *svc
	bare.vectors = noEntityIndex{svc.vectors}
	withoutFacts, err := bare.SearchPage(ctx, team, q)
	if err != nil {
		t.Fatalf("search without facts: %v", err)
	}
	if !withoutFacts.Facts.Empty() {
		t.Fatal("the control still produced facts; it is not a control")
	}

	if len(withFacts.Hits) != len(withoutFacts.Hits) {
		t.Fatalf("drawer count changed: %d with facts, %d without", len(withFacts.Hits), len(withoutFacts.Hits))
	}
	for i := range withFacts.Hits {
		if withFacts.Hits[i].Drawer.ID != withoutFacts.Hits[i].Drawer.ID {
			t.Errorf("drawer order changed at %d: %s vs %s", i, withFacts.Hits[i].Drawer.ID, withoutFacts.Hits[i].Drawer.ID)
		}
		if withFacts.Hits[i].Score != withoutFacts.Hits[i].Score {
			t.Errorf("drawer score changed at %d: %v vs %v", i, withFacts.Hits[i].Score, withoutFacts.Hits[i].Score)
		}
	}
}

// noEntityIndex makes the entity namespace look empty, so a recall runs with the
// graph unreachable and everything else identical. That is the control F-9 needs;
// comparing against a differently-configured service would compare two things.
type noEntityIndex struct{ store.VectorStore }

func (n noEntityIndex) Search(ctx context.Context, namespace string, vector []float32, k int, filter store.Filter) (store.SearchResult, error) {
	if strings.HasSuffix(namespace, "::kg_entities") {
		return store.SearchResult{}, nil
	}
	return n.VectorStore.Search(ctx, namespace, vector, k, filter)
}

func TestAWingReportsItsOwnEntryPoint(t *testing.T) {
	ctx := context.Background()
	const team = "t-f10"
	svc := newTestService(t)

	// A drawer filed into the entry room gets its containment edge from T6, which
	// is what gives the wing a front door at all.
	root, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION?"})
	if err != nil {
		t.Fatalf("add root: %v", err)
	}

	t.Run("a wing names its own entry point", func(t *testing.T) {
		res, err := svc.EntryPoint(ctx, team, "wing_acme")
		if err != nil {
			t.Fatalf("entry point: %v", err)
		}
		if res.Node == "" {
			t.Fatal("the wing reports no entry node; a session would still need an id from a skill file")
		}
		if len(res.Edges) == 0 {
			t.Fatal("the entry node points at nothing; it is a door onto a wall")
		}
		var reaches bool
		for _, e := range res.Edges {
			if e.Object == root.Drawers[0].ID {
				reaches = true
			}
		}
		if !reaches {
			t.Errorf("the entry point does not reach the record filed in its own room; %d edges", len(res.Edges))
		}
		if res.Resolution != KGResolutionMatched {
			t.Errorf("resolution = %q, want %q", res.Resolution, KGResolutionMatched)
		}
	})

	t.Run("a wing with no entry point says so", func(t *testing.T) {
		res, err := svc.EntryPoint(ctx, team, "wing_alpha")
		if err != nil {
			t.Fatalf("a wing without an entry point returned an ERROR; having no front door is a fact about the wing, not a failure: %v", err)
		}
		if res.Node != "" {
			t.Errorf("a wing with no entry room reported node %q", res.Node)
		}
		if res.Resolution != KGResolutionUnknownTerm {
			t.Errorf("resolution = %q, want %q — absence must be distinguishable from an entry point that is merely empty", res.Resolution, KGResolutionUnknownTerm)
		}
	})

	// The absence vocabulary is T2's, not a second one invented here. Two ways to
	// say "nothing" is how a caller ends up handling one and not the other.
	t.Run("absence reuses the lookup vocabulary", func(t *testing.T) {
		res, _ := svc.EntryPoint(ctx, team, "wing_alpha")
		if !slices.Contains([]KGResolution{KGResolutionMatched, KGResolutionKnownTermNoFact, KGResolutionUnknownTerm}, res.Resolution) {
			t.Errorf("resolution %q is outside T2's vocabulary", res.Resolution)
		}
	})
}

func TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked(t *testing.T) {
	ctx := context.Background()
	const team = "t-f11"
	svc := newTestService(t)

	filed, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "we chose the boring option because it fails loudly"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(filed.Drawers) == 0 {
		t.Fatal("nothing filed")
	}
	root := filed.Drawers[0]

	// REACHABILITY, not the presence of a row. A marked self-loop satisfies "the
	// drawer has an edge" completely while making nothing findable — which is the
	// state the corpus is already in: measured 2026-08-26, 57 of 1,985 drawers
	// carried an edge and 0 were reachable as a triple OBJECT.
	t.Run("the drawer is reachable from its room node", func(t *testing.T) {
		q, err := svc.KGQuery(ctx, team, KGQueryInput{
			Entity:    DerivedEdgeSubject("wing_acme", "decisions"),
			Direction: "outgoing",
		})
		if err != nil {
			t.Fatalf("traverse from the room node: %v", err)
		}
		var found bool
		for _, f := range q.Facts {
			if f.Object == root.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("walking out from the room node did not reach the drawer; %d facts, none naming it as object", len(q.Facts))
		}
	})

	// And it must be marked, or derived noise and authored intent become one
	// population that can be neither counted nor removed.
	t.Run("the edge says it was derived", func(t *testing.T) {
		if !root.HasEdge {
			t.Error("the filing reports no edge")
		}
		if !root.EdgeDerived {
			t.Error("the edge is not marked derived; a server guess is indistinguishable from a writer's decision")
		}
	})

	// An authored edge always wins. Driven at the rule itself rather than through
	// a re-file: a filed drawer ALREADY carries a derived edge, so a re-file
	// scenario cannot distinguish "deferred to the author" from "deferred to the
	// edge that was already there". Measured — inverting the deference survived
	// that version of this test completely.
	t.Run("a derived edge never overwrites an authored one", func(t *testing.T) {
		const placed = "drawer-placed-by-hand"
		if _, err := svc.KGAdd(ctx, team, "Release Notes", "documents", placed, "", "", "", "", ""); err != nil {
			t.Fatalf("author an edge: %v", err)
		}

		if _, err := svc.attachDerivedEdge(ctx, team, Drawer{ID: placed, Wing: "wing_acme", Room: "decisions"}); err != nil {
			t.Fatalf("attach: %v", err)
		}

		q, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: placed, Direction: "incoming"})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		for _, f := range q.Facts {
			if f.Predicate == DerivedEdgePredicate {
				t.Errorf("a derived %q edge was attached to a drawer a writer had already placed", DerivedEdgePredicate)
			}
		}
		if len(q.Facts) != 1 {
			t.Errorf("drawer carries %d edges, want only the authored one", len(q.Facts))
		}
	})
}
func TestAFactLookupDistinguishesAbsenceFromFailure(t *testing.T) {
	ctx := context.Background()
	const team = "t-f12"
	svc := newTestService(t)

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The three renderable states are exhaustive and mutually exclusive. Measured
	// 2026-08-26 the last two were indistinguishable: a nonexistent entity and a
	// nonexistent predicate both returned count:0 with no error, exactly like a
	// real empty answer.
	for _, tc := range []struct {
		name       string
		in         KGQueryInput
		want       KGResolution
		unresolved string
	}{
		{"a known entity with facts", KGQueryInput{Entity: "Alice", Direction: "both"}, KGResolutionMatched, ""},
		{"a known entity with no facts this direction", KGQueryInput{Entity: "Acme", Direction: "outgoing"}, KGResolutionKnownTermNoFact, ""},
		{"an entity the graph never heard of", KGQueryInput{Entity: "Nobody", Direction: "both"}, KGResolutionUnknownTerm, "entity"},
		{"a predicate the graph never heard of", KGQueryInput{Predicate: "never_used"}, KGResolutionUnknownTerm, "predicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.KGQuery(ctx, team, tc.in)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if res.Resolution != tc.want {
				t.Errorf("resolution = %q, want %q (facts=%d)", res.Resolution, tc.want, len(res.Facts))
			}
			if res.Unresolved != tc.unresolved {
				t.Errorf("unresolved = %q, want %q", res.Unresolved, tc.unresolved)
			}
		})
	}

	// The states must be DISTINCT, not merely present: three assertions that all
	// pass because every state carries the same value would satisfy the table.
	t.Run("the states are distinct", func(t *testing.T) {
		seen := map[KGResolution]bool{}
		for _, in := range []KGQueryInput{
			{Entity: "Alice", Direction: "both"},
			{Entity: "Acme", Direction: "outgoing"},
			{Entity: "Nobody", Direction: "both"},
		} {
			res, err := svc.KGQuery(ctx, team, in)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			seen[res.Resolution] = true
		}
		if len(seen) != 3 {
			t.Errorf("three different lookups produced %d distinct states, want 3: %v", len(seen), seen)
		}
	})

	// A backend failure must not FAIL OPEN into any of the three. It is returned
	// out of band as an error — a lookup that could not run has no result to
	// carry a state on — and the danger is precisely that it arrives looking like
	// a confident empty answer. Injected rather than assumed.
	t.Run("an injected backend failure is not one of the three", func(t *testing.T) {
		broken, kill := brokenBackendService(t)
		kill()
		res, err := broken.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Direction: "both"})
		if err == nil {
			t.Fatal("a dead backend returned no error; absence and failure are indistinguishable again")
		}
		if res.Resolution != "" {
			t.Errorf("a failed lookup carried resolution %q; failure must not present as one of the three states", res.Resolution)
		}
	})
}

// brokenBackendService builds a migrated service and hands back the closer that
// kills its store, so a test can INJECT a backend failure rather than assume
// errors propagate. Assuming is how a fail-open survives: the code that would
// have caught it is the code being tested.
func brokenBackendService(t *testing.T) (*Service, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broken.db")
	gdb, err := gorm.Open(glebarez.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewService(NewRepo(gdb), fakeEmbedder{}, sqlitevec.New(gdb), fakeDim), func() { _ = sqlDB.Close() }
}

// UC-6 — the bootstrap. One call replaces a client-side protocol that currently
// costs ~25k tokens of instructions plus a hardcoded root id.

func TestOneCallBootstrapsAWing(t *testing.T) {
	ctx := context.Background()
	const team = "t-f13"
	svc := newTestService(t)

	root, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: EntryRoom, Content: "WHAT MUST I LOAD AT THE START OF A SESSION?"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// ONE call. No second round trip and no id carried in from a skill file —
	// the wing name is the only thing the caller has to know.
	res, err := svc.Bootstrap(ctx, team, "wing_acme")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if res.Wing != "wing_acme" {
		t.Errorf("wing = %q, want the one asked for", res.Wing)
	}
	if res.EntryPoint.Node == "" {
		t.Error("no entry point; a session would still need an id from somewhere else")
	}
	var inlined bool
	for _, d := range res.Eager {
		if d.ID == root.Drawers[0].ID {
			inlined = true
		}
	}
	if !inlined {
		t.Errorf("the eager tier did not inline what the entry point points at; %d eager", len(res.Eager))
	}
	// The truncation report is ALWAYS present, so a caller never has to infer
	// completeness by counting.
	if res.Truncation.Omitted != 0 {
		t.Errorf("omitted %d with nothing to omit", res.Truncation.Omitted)
	}

	// UC6-S3: a wing with no entry point STILL bootstraps. It returns a usable
	// answer that says there is no front door, rather than failing.
	t.Run("a wing with no entry point still bootstraps", func(t *testing.T) {
		empty, err := svc.Bootstrap(ctx, team, "wing_alpha")
		if err != nil {
			t.Fatalf("a wing without an entry point failed to bootstrap: %v", err)
		}
		if empty.Wing != "wing_alpha" {
			t.Errorf("wing = %q", empty.Wing)
		}
		if empty.EntryPoint.Resolution != KGResolutionUnknownTerm {
			t.Errorf("resolution = %q, want %q", empty.EntryPoint.Resolution, KGResolutionUnknownTerm)
		}
	})
}

func TestATruncatedBootstrapSaysWhatItDropped(t *testing.T) {
	ctx := context.Background()
	const team = "t-f14"
	svc := newTestService(t)

	// More records than the eager tier can hold, so the response must truncate.
	total := bootstrapEagerLimit + 4
	for i := 0; i < total; i++ {
		if _, err := svc.Add(ctx, team, AddInput{
			Wing: "wing_acme", Room: EntryRoom,
			Content: "start-here entry number " + string(rune('a'+i)) + " with enough text to be a real memory",
		}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}

	res, err := svc.Bootstrap(ctx, team, "wing_acme")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// The eager tier is BOUNDED, which is what makes this a truncated response.
	if len(res.Eager) > bootstrapEagerLimit {
		t.Errorf("eager tier holds %d, over its bound of %d", len(res.Eager), bootstrapEagerLimit)
	}
	if len(res.OnDemand) == 0 {
		t.Fatal("more records than the bound and nothing deferred; the response is not truncated and this test proves nothing")
	}

	// Every offered record is ACCOUNTED FOR: inlined, named as a pointer, or
	// counted as an omission. Those three partition the offer.
	//
	// This used to assert len(OnDemand) == Omitted, which holds only on an
	// all-local wing — where nothing is ever refused — so the fixture pinned an
	// accident. Omitted counts what is neither inlined nor nameable; a pointer is
	// named, so it is not a loss.
	offered := len(res.EntryPoint.Edges) + res.EntryPoint.Refused
	accounted := len(res.Eager) + len(res.OnDemand) + res.Truncation.Omitted
	if accounted != offered {
		t.Errorf("the entry point offered %d records and the response accounts for %d; the partition leaks",
			offered, accounted)
	}

	// Reporting a count is not enough. The protocol this replaces lost 74% of a
	// prescribed tier to an unreported cap; saying "4 omitted" without saying how
	// to get them repeats that in a politer form.
	if res.Truncation.Omitted > 0 && res.Truncation.HowToFetch == "" {
		t.Error("the truncation report does not say how to fetch what it dropped")
	}
	for _, p := range res.OnDemand {
		if p.Fetch == "" {
			t.Errorf("pointer %s carries no fetch call; a pointer without the call that resolves it is a riddle", p.ID)
		}
	}
}

func TestCorrectionsAreSweptServerSideAcrossAllThreePredicates(t *testing.T) {
	// The three names are written out rather than ranged over
	// CorrectionPredicates: iterating the list under test means shrinking it just
	// runs fewer subtests and passes, which is how the same mutant survived T5's
	// first version.
	for _, pred := range []string{"retracts", "supersedes", "qualifies"} {
		t.Run(pred, func(t *testing.T) {
			ctx := context.Background()
			team := "t-f15-" + pred
			svc := newTestService(t)

			wrong, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: EntryRoom, Content: "load the old checklist first"})
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			right, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: EntryRoom, Content: "load the new checklist first"})
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			// INCOMING: the correcting record points AT the corrected one.
			if _, err := svc.KGAdd(ctx, team, right.Drawers[0].ID, pred, wrong.Drawers[0].ID, "", "", "", "", right.Drawers[0].ID); err != nil {
				t.Fatalf("kgadd: %v", err)
			}

			res, err := svc.Bootstrap(ctx, team, "wing_acme")
			if err != nil {
				t.Fatalf("bootstrap: %v", err)
			}
			got := res.Corrections[normalizeEntityID(wrong.Drawers[0].ID)]
			if len(got) == 0 {
				t.Fatalf("a bootstrapped record arrived with no correction; a session that bootstraps perfectly would read what the tier got wrong and believe it")
			}
			var sawPred bool
			for _, c := range got {
				if c.Predicate == pred {
					sawPred = true
				}
			}
			if !sawPred {
				t.Errorf("the %s correction was not swept; got %+v", pred, got)
			}
		})
	}
}

func TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces(t *testing.T) {
	ctx := context.Background()
	const team = "t-f16"
	svc := newTestService(t)

	// A fixture with all four shapes — eager, deferred, corrected and truncated —
	// because a parity gate exercised only on a one-record wing cannot observe an
	// assembly path being removed. Measured: with a single entry, deleting eager,
	// pointer or correction assembly still passed "semantic parity".
	var ids []string
	for i := 0; i < bootstrapEagerLimit+3; i++ {
		res, err := svc.Add(ctx, team, AddInput{
			Wing: "wing_acme", Room: EntryRoom,
			Content: fmt.Sprintf("start-here entry %d, with enough text to be a real memory", i),
		})
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		ids = append(ids, res.Drawers[0].ID)
	}
	// One of them is corrected by another, so the sweep has something to find.
	if _, err := svc.KGAdd(ctx, team, ids[1], "supersedes", ids[0], "", "", "", "", ids[1]); err != nil {
		t.Fatalf("correct: %v", err)
	}
	offer := BootstrapOffer{Records: len(ids), Corrections: 1}

	baseline, err := LoadBootstrapBaseline(filepath.Join("testdata", "bootstrap-baseline-manifest-2026-08-26.json"))
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}

	res, err := svc.Bootstrap(ctx, team, "wing_acme")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// PARITY FIRST. Without it the cheapest conformant bootstrap is one that
	// returns nothing, and this gate would reward exactly that.
	missing := res.MissingParityParts(offer)
	if len(missing) > 0 {
		t.Fatalf("the bootstrap does not carry the same logical payload as the %d calls it replaces; missing: %v", baseline.Calls, missing)
	}

	// The parity check must be able to FAIL, driven directly rather than only
	// through a happy-path fixture. Measured: a mutant that hollowed out the
	// truncation branch survived, because this test's fixture omits nothing and
	// so never reached it. A parity gate that is vacuous on the case it is
	// applied to is a token comparison wearing a parity gate's name.
	t.Run("parity can fail", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			res  BootstrapResult
			want string
		}{
			{"no entry point resolution", BootstrapResult{Wing: "wing_acme"}, "entry point"},
			{"no resolved wing", BootstrapResult{EntryPoint: EntryPointResult{Resolution: KGResolutionMatched}}, "resolved wing"},
			{"omissions with no way to fetch them",
				BootstrapResult{Wing: "wing_acme", EntryPoint: EntryPointResult{Resolution: KGResolutionMatched},
					Truncation: BootstrapTruncation{Omitted: 3}}, "truncation report"},
			{"a wing with records that delivered none",
				BootstrapResult{Wing: "wing_acme", EntryPoint: EntryPointResult{Resolution: KGResolutionMatched}}, "eager content"},
			{"records dropped without being pointed at",
				BootstrapResult{Wing: "wing_acme", EntryPoint: EntryPointResult{Resolution: KGResolutionMatched},
					Eager: make([]Drawer, 2)}, "on-demand pointers"},
			{"corrections the graph holds and the response dropped",
				BootstrapResult{Wing: "wing_acme", EntryPoint: EntryPointResult{Resolution: KGResolutionMatched},
					Eager: make([]Drawer, 5)}, "corrections"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := tc.res.MissingParityParts(BootstrapOffer{Records: 5, Corrections: 1})
				if !slices.Contains(got, tc.want) {
					t.Errorf("a response missing %q passed parity; got %v", tc.want, got)
				}
			})
		}
	})

	// Only then the cost comparison, and only against a DATED baseline naming
	// its own tokenizer.
	if baseline.Calls <= 1 {
		t.Fatalf("baseline claims %d calls; there is nothing to replace", baseline.Calls)
	}
	if baseline.Tokenizer == "" || baseline.Date == "" {
		t.Fatal("the baseline names no tokenizer or no date; an undated cost comparison is unfalsifiable a month later")
	}
	if got := res.OutputTokens(); got >= baseline.OutputTokens {
		t.Errorf("bootstrap costs %d output tokens against a baseline of %d under %s; a bootstrap that returns more than it saves has reproduced the problem inside one call",
			got, baseline.OutputTokens, baseline.Tokenizer)
	}
}

func TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk(t *testing.T) {
	// F-17 asserted against the BOOTSTRAP, not only against EntryPoint. T7 can
	// prove EntryPoint resolves directly and still leave this surface free to be
	// built on a walk whose max_hops is inert, with every T7 test green.
	//
	// A source check, because "which function did you call" is not observable
	// from a return value.
	src, err := os.ReadFile("bootstrap.go")
	if err != nil {
		t.Fatalf("read bootstrap.go: %v", err)
	}
	body := string(src)
	if strings.Contains(body, "s.Traverse(") {
		t.Error("the bootstrap calls Traverse; max_hops is provably inert (via is an intersection carried forward), so it would silently return only hop 1 while looking correct")
	}
	if !strings.Contains(body, "s.EntryPoint(") {
		t.Error("the bootstrap does not resolve through EntryPoint, which is the direct resolution F-17 requires")
	}
}

// TestAQuestionReachesTheFactThatAnswersIt binds UC1-S1, the happy path of
// reaching a fact by question. It exists because the scenario was bound to
// TestAWingScopedRecallNeverReturnsAnotherWingsFact, whose assertion is
// satisfied by returning no facts at all — an unfalsifiable gate on the one
// path that has to prove a question ARRIVES somewhere.
func TestAQuestionReachesTheFactThatAnswersIt(t *testing.T) {
	svc, team, ctx := factWorld(t)

	// The POSITIVE assertion. UC1-S1 was originally bound to a test asserting
	// that no FOREIGN fact appears — which returning nothing satisfies
	// completely. This one requires the fact to actually arrive.
	page, err := svc.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Query: "who owns invoice numbering", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Facts.Facts) == 0 {
		t.Fatal("a question that does not name the entity returned no facts at all; the graph is still off the read path")
	}
	var found bool
	for _, f := range page.Facts.Facts {
		if f.Predicate == "owns" {
			found = true
		}
	}
	if !found {
		t.Errorf("the in-wing fact did not reach the page; got %d facts: %+v", len(page.Facts.Facts), page.Facts.Facts)
	}

	// A derived containment edge is plumbing, not an answer. T6 attaches one to
	// every filed drawer, so without this the block answers "who owns invoice
	// numbering" with "a room contains a drawer" — and it would do so for every
	// drawer in the wing.
	for _, f := range page.Facts.Facts {
		if f.Derived || f.Predicate == DerivedEdgePredicate {
			t.Errorf("a server-derived containment edge was returned as an answer: %s -> %s -> %s", f.Subject, f.Predicate, f.Object)
		}
	}
}

// TestAnUnlocatableFactIsCountedNotDropped binds F-18. Of 196 triples measured
// 2026-08-26 only 90 resolve to a drawer, so a fact that matches but cannot be
// placed in any wing is the majority case. Dropping it silently would recreate
// the exact failure this spec removes: silence that reads as "nothing is filed".
func TestAnUnlocatableFactIsCountedNotDropped(t *testing.T) {
	svc, team, ctx := factWorld(t)

	page, err := svc.SearchPage(ctx, team, SearchQuery{Wing: "wing_acme", Query: "tell me about the ledger service", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if page.Facts.Unlocatable == 0 {
		t.Fatal("a fact with no provenance was dropped silently; silence is indistinguishable from \"nothing is filed\", which is the failure this exists to remove")
	}
	// Counted, never attributed. It must not appear as content and must not put
	// a wing name in the pointer.
	for _, f := range page.Facts.Facts {
		if f.Predicate == "written_in" {
			t.Error("an unlocatable fact was returned as in-wing content")
		}
	}
	if !page.Facts.Empty() && len(page.Facts.ElsewhereWings) > 0 {
		for _, w := range page.Facts.ElsewhereWings {
			if w == "" {
				t.Error("an unlocatable fact contributed an empty wing name to the pointer")
			}
		}
	}
}

// TestOneWingRuleGovernsEveryNewResponsePath binds F-19. The fact block, the
// sibling pointer, the entry point's edges and the bootstrap's inline content
// are four ways out of the server, and a rule re-implemented four times is a
// rule that will disagree with itself on the path nobody tested.
func TestOneWingRuleGovernsEveryNewResponsePath(t *testing.T) {
	// F-19 asserted STRUCTURALLY. A behavioural test over fixtures is satisfied
	// by four duplicated filters that happen to agree today — which is precisely
	// the state F-19 exists to forbid, because the one that later diverges is a
	// tenancy leak rather than a formatting bug.
	//
	// So this asks a question no fixture can answer: does each path CALL the
	// policy?
	for _, path := range []struct{ file, why string }{
		{"factsfor.go", "the fact block and the sibling pointer"},
		{"memory_search.go", "the correction mark on a returned record"},
		{"graphquery.go", "an entry point's outgoing edges"},
		{"bootstrap.go", "the bootstrap's inline content"},
	} {
		t.Run(path.file, func(t *testing.T) {
			src, err := os.ReadFile(path.file)
			if err != nil {
				t.Fatalf("read %s: %v", path.file, err)
			}
			body := string(src)
			usesPolicy := strings.Contains(body, "wingPolicyFor(") || strings.Contains(body, "NewWingPolicy(")
			consultsIt := strings.Contains(body, ".Place(") || strings.Contains(body, "MayReturnContent(") || strings.Contains(body, "CorrectionsFor(")
			if !usesPolicy || !consultsIt {
				t.Errorf("%s (%s) does not go through WingPolicy; it decides the wing boundary for itself, and four rules that agree today diverge on the path nobody tested",
					path.file, path.why)
			}
		})
	}

	// The structural half above asks whether a path MENTIONS the policy. That is
	// not the same as HEEDING it: a mutant that computed the placement and threw
	// the answer away survived this test completely, because the call was still
	// in the file. So the boundary is also driven for real, on the path where the
	// two checks disagree.
	t.Run("the bootstrap does not inline a foreign wing's record", func(t *testing.T) {
		ctx := context.Background()
		const team = "t-f19"
		svc := newTestService(t)

		// A local record in the entry room, so the wing has a front door whose
		// own provenance is local.
		local, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: EntryRoom, Content: "the local checklist"})
		if err != nil {
			t.Fatalf("add local: %v", err)
		}
		// And a record in ANOTHER wing.
		foreign, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", Content: "FOREIGN-WING-SECRET another project's content"})
		if err != nil {
			t.Fatalf("add foreign: %v", err)
		}

		// An entry edge whose OBJECT is the foreign record but whose provenance is
		// local. EntryPoint places an edge by its SourceDrawerID, so this passes
		// that filter — only the bootstrap's own check on the inlined record can
		// stop it. That asymmetry is the whole reason F-19 wants one rule applied
		// at every path rather than one filter per path.
		if _, err := svc.KGAdd(ctx, team, DerivedEdgeSubject("wing_acme", EntryRoom), DerivedEdgePredicate, foreign.Drawers[0].ID, "", "", "", "", local.Drawers[0].ID); err != nil {
			t.Fatalf("kgadd cross-wing entry edge: %v", err)
		}

		res, err := svc.Bootstrap(ctx, team, "wing_acme")
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		for _, d := range res.Eager {
			if d.Wing != "wing_acme" {
				t.Errorf("the bootstrap inlined a record from %q; only local content may cross", d.Wing)
			}
			if strings.Contains(d.Content, "FOREIGN-WING-SECRET") {
				t.Error("another wing's content was inlined into this wing's bootstrap")
			}
		}
	})
}

// factWorld seeds two wings and three facts: one whose provenance is in the
// searched wing, one whose provenance is in a sibling, and one with none at all.
// That is the three-state world F-8 describes, and it is the only shape in which
// F-1, F-2 and F-18 can each fail independently.
func factWorld(t *testing.T) (*Service, string, context.Context) {
	t.Helper()
	ctx := context.Background()
	const team = "t-facts"
	svc := newTestService(t)

	here, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the ledger service owns invoice numbering"})
	if err != nil {
		t.Fatalf("add here: %v", err)
	}
	there, err := svc.Add(ctx, team, AddInput{Wing: "wing_alpha", Room: "decisions", Content: "the ledger service is deployed nightly"})
	if err != nil {
		t.Fatalf("add there: %v", err)
	}

	// local: provenance resolves into the searched wing
	if _, err := svc.KGAdd(ctx, team, "ledger service", "owns", "invoice numbering", "", "", "", "", here.Drawers[0].ID); err != nil {
		t.Fatalf("kg local: %v", err)
	}
	// foreign: provenance resolves into a sibling wing
	if _, err := svc.KGAdd(ctx, team, "ledger service", "deploys", "nightly", "", "", "", "", there.Drawers[0].ID); err != nil {
		t.Fatalf("kg foreign: %v", err)
	}
	// unlocatable: no provenance at all — the majority case on the live corpus
	if _, err := svc.KGAdd(ctx, team, "ledger service", "written in", "go", "", "", "", "", ""); err != nil {
		t.Fatalf("kg unlocatable: %v", err)
	}
	if _, err := svc.BackfillEntityLabels(ctx, team); err != nil {
		t.Fatalf("backfill entity labels: %v", err)
	}
	return svc, team, ctx
}
