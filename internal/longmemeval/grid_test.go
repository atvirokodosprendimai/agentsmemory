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

func (f *fakeStore) List(_ context.Context, _, wing, _ string, _, _ int) ([]palace.Drawer, error) {
	return f.seeded[wing], nil
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
	if len(store.byWing[first]) == 0 || len(store.byWing[second]) == 0 {
		t.Fatalf("expected both scopes written: %d and %d", len(store.byWing[first]), len(store.byWing[second]))
	}
	// The decisive assertion: nothing written for question 1 is reachable from
	// question 2's scope.
	for _, d := range store.byWing[second] {
		for _, other := range store.byWing[first] {
			if d.Content == other.Content && d.ID == other.ID {
				t.Errorf("question 2's scope holds question 1's record %q", d.ID)
			}
		}
	}
}
