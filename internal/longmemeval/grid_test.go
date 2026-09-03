package longmemeval

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/gen"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// fakeStore is the slice of the palace RunGrid uses, recorded per wing so a test
// can assert what each question could actually see.
type fakeStore struct {
	byWing  map[string][]palace.Drawer
	seeded  map[string][]palace.Drawer // wings that are NOT empty before the run
	queries []string
	// padHits prepends this many non-gold decoy hits to every search, which puts
	// the gold record at a known position greater than 1. A test that needs a
	// RANK other than 1 has no other way to arrange one here, and without it an
	// MRR assertion cannot tell a reciprocal from a hit rate — relying on
	// insertion order does not work, because the fixture's gold session lands
	// first under both orders.
	padHits int
	// deleted records every scope released, so a test can assert the grid cleans
	// up rather than only that it did not crash.
	deleted []string
	// released snapshots what each scope HELD at the moment it was deleted. The
	// grid now cleans up after itself, so a test that inspects byWing afterwards
	// sees nothing — this keeps the evidence isolation is asserted from.
	released map[string][]palace.Drawer
}

func newFakeStore() *fakeStore {
	return &fakeStore{byWing: map[string][]palace.Drawer{}, seeded: map[string][]palace.Drawer{}}
}

func (f *fakeStore) Add(_ context.Context, _ string, in palace.AddInput) (palace.AddResult, error) {
	d := palace.Drawer{ID: fmt.Sprintf("%s-%d", in.Wing, len(f.byWing[in.Wing])), Content: in.Content}
	f.byWing[in.Wing] = append(f.byWing[in.Wing], d)
	return palace.AddResult{Drawers: []palace.Drawer{d}}, nil
}

func (f *fakeStore) Search(_ context.Context, _ string, q palace.SearchQuery) ([]palace.SearchHit, error) {
	f.queries = append(f.queries, q.Query)
	all := append(append([]palace.Drawer{}, f.seeded[q.Wing]...), f.byWing[q.Wing]...)
	var out []palace.SearchHit
	for i := 0; i < f.padHits; i++ {
		id := fmt.Sprintf("decoy-%d", i)
		out = append(out, palace.SearchHit{
			Drawer: palace.Drawer{ID: id, Content: "decoy"}, MemoryID: id, MemoryContent: "decoy",
		})
	}
	for _, d := range all {
		out = append(out, palace.SearchHit{Drawer: d, MemoryID: d.ID, MemoryContent: d.Content})
	}
	return out, nil
}

// List reports what the wing HOLDS: what a run seeded plus what Add wrote.
//
// ⚠ IT USED TO RETURN ONLY THE SEEDED ROWS, WHICH MADE THE EMPTINESS GUARD
// UNTESTABLE. A fake whose List cannot see its own Add is inconsistent with the
// real Store on exactly the property the guard is about, so
// TestRunGridRefusesANonEmptyWing passed over a fixture that could not produce
// non-emptiness by itself — and the real defect it was meant to cover (a second
// run aborting because the first never released its scopes) was invisible.
// Reported in #167.
func (f *fakeStore) List(_ context.Context, _, wing, _ string, _, _ int) ([]palace.Drawer, error) {
	return append(append([]palace.Drawer{}, f.seeded[wing]...), f.byWing[wing]...), nil
}

// DeleteWing removes a scope, and REFUSES a confirm that does not match, because
// that is what palace.Service.DeleteWing does — a fake that accepts any confirm
// would let a caller pass the wrong one forever.
func (f *fakeStore) DeleteWing(_ context.Context, _, wing, confirm string) (palace.DeleteWingResult, error) {
	if confirm != wing {
		return palace.DeleteWingResult{}, fmt.Errorf("confirm %q does not match wing %q", confirm, wing)
	}
	n := len(f.seeded[wing]) + len(f.byWing[wing])
	// ⚠ SNAPSHOT BEFORE THE DELETES. Taken after, it captures nothing, and the
	// isolation assertion that reads it then iterates an empty slice and passes
	// vacuously — a test that proves less than it did before the cleanup existed.
	if f.released == nil {
		f.released = map[string][]palace.Drawer{}
	}
	f.released[wing] = append(append([]palace.Drawer{}, f.seeded[wing]...), f.byWing[wing]...)
	delete(f.seeded, wing)
	delete(f.byWing, wing)
	f.deleted = append(f.deleted, wing)
	return palace.DeleteWingResult{Drawers: int64(n)}, nil
}

// stubModel answers every prompt with a fixed string and records what it saw.
type stubModel struct {
	answer       string
	promptTokens int
	prompts      []string
}

func (m *stubModel) Generate(_ context.Context, prompt string, _ float64) (gen.Result, error) {
	m.prompts = append(m.prompts, prompt)
	answer := m.answer
	if answer == "" {
		answer = "yes"
	}
	// A non-zero PromptTokens stands in for what a real endpoint reports, so a
	// grid that discards the figure fails rather than averaging zeroes.
	return gen.Result{Text: answer, PromptTokens: m.promptTokens}, nil
}

func gridOpts(store *fakeStore) (GridOptions, *stubModel) {
	m := &stubModel{answer: "yes", promptTokens: 74}
	return GridOptions{
		Wing:         "scratch",
		TeamID:       "t",
		ContextRunes: 400,
		Reader:       m,
		Judge:        m,
		SearchLimit:  10,
	}, m
}

func twoQuestionSelection(t *testing.T) Selection {
	t.Helper()
	ds, err := Load(samplePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return Subset(ds, 2, 7)
}

// TestRunGridHoldsTheContextBudgetAcrossCells is ADR-047's central invariant: a
// policy that writes more text must fit it into the same reader window as one
// that writes less, or the metric is one a superset wins by construction.
func TestRunGridHoldsTheContextBudgetAcrossCells(t *testing.T) {
	store := newFakeStore()
	opts, model := gridOpts(store)
	sel := twoQuestionSelection(t)

	if _, err := RunGrid(context.Background(), store, sel,
		[]string{"verbatim", "one-fact"}, []string{"verbatim"}, opts); err != nil {
		t.Fatalf("RunGrid: %v", err)
	}
	for _, p := range model.prompts {
		if !strings.HasPrefix(p, "Answer the question using only the memories") {
			continue // a judge prompt, not a reader prompt
		}
		if n := len([]rune(p)); n > opts.ContextRunes+len([]rune(ReaderPrompt("", nil)))+512 {
			t.Errorf("a reader prompt ran to %d runes against a %d-rune budget — the budget is "+
				"what makes this metric one a verbatim blob cannot win by construction",
				n, opts.ContextRunes)
		}
	}
}

func TestRunGridRefusesANonEmptyWing(t *testing.T) {
	store := newFakeStore()
	opts, _ := gridOpts(store)
	sel := twoQuestionSelection(t)
	// Seed the wing the first question would use.
	store.seeded[scratchWing(opts.Wing, "verbatim", "verbatim", sel.Questions[0].ID)] =
		[]palace.Drawer{{ID: "pre", Content: "somebody else's memory"}}

	_, err := RunGrid(context.Background(), store, sel,
		[]string{"verbatim"}, []string{"verbatim"}, opts)
	if err == nil {
		t.Fatal("a run into a populated scope must be refused: it would measure memories no " +
			"policy in this grid wrote")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("the error should say the scope was not empty, got: %v", err)
	}
}

func TestRunGridRefusesAZeroBudget(t *testing.T) {
	store := newFakeStore()
	opts, _ := gridOpts(store)
	opts.ContextRunes = 0
	_, err := RunGrid(context.Background(), store, twoQuestionSelection(t),
		[]string{"verbatim"}, []string{"verbatim"}, opts)
	if err == nil {
		t.Fatal("a zero budget is an unbounded run wearing the shape of a bounded one, and the " +
			"shared budget is the property the whole instrument rests on")
	}
}

// TestRunGridIsolatesEveryQuestion is the finding PR #148's review raised: the
// scratch scope was per CELL, so question 2 searched question 1's history and
// the contamination grew monotonically through the cell — leaving the cell mean
// a mean of nothing.
func TestRunGridIsolatesEveryQuestion(t *testing.T) {
	store := newFakeStore()
	opts, _ := gridOpts(store)
	sel := twoQuestionSelection(t)
	if len(sel.Questions) < 2 {
		t.Fatalf("need two questions to test isolation, got %d", len(sel.Questions))
	}

	if _, err := RunGrid(context.Background(), store, sel,
		[]string{"verbatim"}, []string{"verbatim"}, opts); err != nil {
		t.Fatalf("RunGrid: %v", err)
	}

	first := scratchWing(opts.Wing, "verbatim", "verbatim", sel.Questions[0].ID)
	second := scratchWing(opts.Wing, "verbatim", "verbatim", sel.Questions[1].ID)
	if first == second {
		t.Fatal("both questions wrote into one scope — the second can retrieve the first's " +
			"haystack, and every question after it is measured under a different instrument")
	}
	// ⚠ ASSERTED FROM WHAT EACH SCOPE HELD WHEN IT WAS RELEASED, not from what
	// survives the run. The grid now deletes each scope once its question is
	// scored, so byWing is empty afterwards and an assertion over it would iterate
	// nothing and pass vacuously — proving less than before the cleanup existed.
	if len(store.released[first]) == 0 || len(store.released[second]) == 0 {
		t.Fatalf("expected both scopes written and released: %d and %d",
			len(store.released[first]), len(store.released[second]))
	}
	// The decisive assertion: nothing written for question 1 is reachable from
	// question 2's scope.
	for _, d := range store.released[second] {
		for _, other := range store.released[first] {
			if d.Content == other.Content && d.ID == other.ID {
				t.Errorf("question 2's scope holds question 1's record %q", d.ID)
			}
		}
	}
	// And the scopes really are gone, which is what makes a second run possible.
	for _, w := range []string{first, second} {
		if len(store.byWing[w]) != 0 {
			t.Errorf("scope %s survived the run with %d drawer(s): the next run's emptiness "+
				"guard will refuse it", w, len(store.byWing[w]))
		}
	}
}

// TestASecondRunSucceedsBecauseScopesAreReleased pins that the grid is not
// single-use.
//
// ⚠ IT WAS, AND THE SUITE COULD NOT SEE IT. The per-question isolation fix made
// the scope `<base>_<write>_<query>_<question>` and nothing deleted it, so the
// emptiness guard — correctly — refused the second run. That was invisible here
// because fakeStore.List returned only the seeded rows and never what Add wrote,
// so no fixture could produce non-emptiness by itself; List now reports both,
// which is what lets this test fail when the cleanup is removed.
//
// The cost on a real machine was not an aborted run. A 4x3 grid at --n 20 writes
// 240 wings into whatever palace EnsureLocalWorkspace resolved — the operator's
// own — and the documented rollback names ONE exact wing, so it removed none of
// them. Reported in #167 after #148 merged.
func TestASecondRunSucceedsBecauseScopesAreReleased(t *testing.T) {
	store := newFakeStore()
	opts, _ := gridOpts(store)
	sel := twoQuestionSelection(t)

	for run := 1; run <= 2; run++ {
		if _, err := RunGrid(context.Background(), store, sel,
			[]string{"verbatim"}, []string{"verbatim"}, opts); err != nil {
			t.Fatalf("run %d failed: %v\nA grid that cannot be run twice leaves its scopes in "+
				"the operator's palace and refuses to measure anything again", run, err)
		}
	}

	// Every scope the run touched must be gone, not merely most of them: the guard
	// aborts on the FIRST non-empty one, so a single survivor is a failed rerun.
	for _, q := range sel.Questions {
		wing := scratchWing(opts.Wing, "verbatim", "verbatim", q.ID)
		if n := len(store.byWing[wing]); n != 0 {
			t.Errorf("scope %s still holds %d drawer(s) after two runs", wing, n)
		}
	}
	// And it released them by NAME each time, which is the confirm the real
	// DeleteWing requires — a fake that ignored the confirm would hide a caller
	// passing the wrong one.
	if len(store.deleted) != 2*len(sel.Questions) {
		t.Errorf("released %d scope(s) over two runs, want %d",
			len(store.deleted), 2*len(sel.Questions))
	}
}

// TestFusionMakesTheRetrievalColumnComparableAcrossPolicies pins that a record's
// rank does not depend on which sub-query returned it.
//
// ⚠ IT DID, AND IT BROKE BOTH RETRIEVAL COLUMNS IN OPPOSITE DIRECTIONS. Each
// sub-query ran at the FULL --search-limit and the pages were appended, so
// RetrievalRate inflated by pool size (3 sub-queries, 3x the candidates) while
// RetrievalMRR deflated by concatenation order. Measured in #167: the gold record
// as the BEST hit of sub-query 3 scored RR 0.024 against 0.333 for the same record
// found third on sub-query 1 — an order of magnitude worse for asking better.
//
// That is not a rounding problem, it is the column meaning something different for
// `decomposed` than for the baselines, and ADR-047 T5's checklist reads the whole
// row: "decide whether the retrieval-only column and the judged column disagree".
func TestFusionMakesTheRetrievalColumnComparableAcrossPolicies(t *testing.T) {
	hit := func(id string) palace.SearchHit {
		return palace.SearchHit{Drawer: palace.Drawer{ID: id}, MemoryID: id, MemoryContent: id}
	}
	decoys := func(prefix string, n int) []palace.SearchHit {
		out := make([]palace.SearchHit, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, hit(fmt.Sprintf("%s-%d", prefix, i)))
		}
		return out
	}
	sessionOf := map[string]string{"gold": "s-gold"}
	gold := []string{"s-gold"}

	t.Run("gold first on the LAST sub-query ranks near the top", func(t *testing.T) {
		pages := [][]palace.SearchHit{
			decoys("a", 3),
			decoys("b", 3),
			append([]palace.SearchHit{hit("gold")}, decoys("c", 2)...),
		}
		rank := goldRank(fuseByRank(pages), sessionOf, gold)
		if rank == 0 {
			t.Fatal("the gold record was not found at all after fusion")
		}
		// Appended, it would have been 7th. Fused, it shares rank 1 with the other
		// sub-queries' own top hits, so it lands in the first three.
		if rank > 3 {
			t.Errorf("gold ranked %d after fusion: a record found FIRST by a sub-query must not "+
				"be pushed below every candidate the earlier sub-queries returned", rank)
		}
	})

	t.Run("a single-query policy is left exactly as it was", func(t *testing.T) {
		page := append(decoys("a", 2), hit("gold"))
		fused := fuseByRank([][]palace.SearchHit{page})
		if got, want := goldRank(fused, sessionOf, gold), 3; got != want {
			t.Errorf("single-page rank = %d, want %d — verbatim and named-thing each return one "+
				"query, so their columns must mean exactly what they meant before", got, want)
		}
	})

	t.Run("agreement across sub-queries outranks one sub-query's first place", func(t *testing.T) {
		// "agreed" is 2nd in two pages; "solo" is 1st in one. Reciprocal rank sums
		// 2/(60+2) = 0.0323 against 1/(60+1) = 0.0164, so agreement wins — which is
		// the property fusion exists for.
		pages := [][]palace.SearchHit{
			{hit("solo"), hit("agreed")},
			{hit("x"), hit("agreed")},
		}
		fused := fuseByRank(pages)
		if fused[0].MemoryID != "agreed" {
			t.Errorf("fused top hit = %q, want \"agreed\"", fused[0].MemoryID)
		}
	})
}
