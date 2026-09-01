package longmemeval

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

func TestCellsCarryTheRankingProfileAndModel(t *testing.T) {
	store := newFakeStore()
	opts, _ := gridOpts(store)
	opts.RankingProfile = "fusion=rrf rerank=on"
	opts.ModelID = "qwen3:8b"
	opts.DatasetPath, opts.DatasetSHA256 = "corpus.json", "abc123"

	cells, err := RunGrid(context.Background(), store, twoQuestionSelection(t),
		[]string{"verbatim"}, []string{"verbatim"}, opts)
	if err != nil {
		t.Fatalf("RunGrid: %v", err)
	}
	h := cells.Header
	// ADR-007: no number without the population it was taken on. Every one of
	// these is a configuration a later run could differ in, and a table that
	// cannot say which one it was taken under cannot be compared with anything.
	for name, got := range map[string]string{
		"ranking profile": h.RankingProfile,
		"model id":        h.ModelID,
		"dataset path":    h.DatasetPath,
		"dataset sha256":  h.DatasetSHA256,
	} {
		if got == "" {
			t.Errorf("the results header carries no %s", name)
		}
	}
	if h.ContextRunes != opts.ContextRunes {
		t.Errorf("header budget = %d, want %d", h.ContextRunes, opts.ContextRunes)
	}
	if len(h.SubsetIDs) == 0 || h.Seed == 0 {
		t.Error("the header must carry the subset ids and seed, or a later larger run cannot say " +
			"whether it saw the same questions")
	}
}

func TestCellsRefuseToMergeAcrossDifferentHeaders(t *testing.T) {
	base := Header{
		DatasetSHA256: "abc", ModelID: "m", ContextRunes: 400,
		RankingProfile: "p", Seed: 7, SubsetIDs: []string{"q1"},
	}
	for _, tc := range []struct {
		name  string
		mutle func(*Header)
	}{
		{"a different corpus", func(h *Header) { h.DatasetSHA256 = "def" }},
		{"a different reader model", func(h *Header) { h.ModelID = "other" }},
		{"a different context budget", func(h *Header) { h.ContextRunes = 800 }},
		{"a different ranking profile", func(h *Header) { h.RankingProfile = "q" }},
	} {
		other := base
		tc.mutle(&other)
		if err := base.Mergeable(other); err == nil {
			t.Errorf("cells taken under %s must not pool: the delta between them would be that "+
				"difference, reported as if it were the policy", tc.name)
		}
	}
	same := base
	if err := base.Mergeable(same); err != nil {
		t.Errorf("identical headers must merge, got: %v", err)
	}
}

// TestCellsReportTheRetrievalOnlyColumnBeside keeps the secondary metric present
// so the two can disagree in public — which is the disagreement ADR-047 exists
// to look for.
func TestCellsReportTheRetrievalOnlyColumnBeside(t *testing.T) {
	store := newFakeStore()
	opts, _ := gridOpts(store)
	cells, err := RunGrid(context.Background(), store, twoQuestionSelection(t),
		[]string{"verbatim"}, []string{"verbatim"}, opts)
	if err != nil {
		t.Fatalf("RunGrid: %v", err)
	}
	if len(cells.Cells) == 0 {
		t.Fatal("no cells")
	}
	for _, c := range cells.Cells {
		if c.Scored == 0 {
			t.Errorf("cell %s/%s judged nothing", c.Write, c.Query)
		}
		if c.RetrievalScored == 0 {
			t.Errorf("cell %s/%s reports no retrieval-only column; it is nearly free once the "+
				"harness exists and the ADR wants the two metrics able to disagree", c.Write, c.Query)
		}
	}
}

// TestRetrievalColumnExcludesAbstentionQuestions: an _abs item has no answer
// location, which is why upstream's retrieval evaluator excludes them. Scoring
// them would put a fixed zero into every cell alike and damp every contrast.
func TestRetrievalColumnExcludesAbstentionQuestions(t *testing.T) {
	q := Question{
		ID: "q_x_abs", Type: TypeSingleSessionUser, Question: "when?",
		Haystack:       []Session{{ID: "s1", Date: "2023-01-01", Turns: []Turn{{Role: "user", Content: "hi"}}}},
		GoldSessionIDs: nil,
	}
	// The ordinary control must differ ONLY in the _abs suffix, so the exclusion
	// is attributable to abstention rather than to a missing gold session — a
	// fixture that satisfied two exclusion conditions at once would pass with the
	// abstention check deleted.
	plain := q
	plain.ID = "q_x"
	plain.GoldSessionIDs = []string{"s1"}
	q.GoldSessionIDs = []string{"s1"}

	if scoresRetrieval(q) {
		t.Error("an abstention question must be out of the retrieval column — it has no gold " +
			"session, so scoring it puts the same zero into every cell and damps every contrast")
	}
	if !scoresRetrieval(plain) {
		t.Error("an ordinary question must be in the retrieval column")
	}
}

// TestCellsRecordTheReportedPromptTokens gates a field that shipped populated by
// NOTHING.
//
// T4 declared Cell.PromptTokensReported as what makes ADR-047's rune budget
// auditable — and gen.Client.Generate returned only the text, so the figure was
// discarded and every cell reported 0. Emitted, documented, and fed by no code
// path: this repository's signature defect, inside the ADR that exists to gate
// it. Found 2026-09-01 by reading a live endpoint's reply, which carried
// prompt_eval_count: 74 — a number the grid had nowhere to put.
//
// A run whose endpoint reports nothing still reports 0 here, and that is the
// honest value; what this test forbids is discarding a figure the endpoint DID
// supply.
func TestCellsRecordTheReportedPromptTokens(t *testing.T) {
	store := newFakeStore()
	opts, model := gridOpts(store)
	if model.promptTokens == 0 {
		t.Fatal("the stub reports no prompt tokens, so this test would pass over nothing")
	}
	cells, err := RunGrid(context.Background(), store, twoQuestionSelection(t),
		[]string{"verbatim"}, []string{"verbatim"}, opts)
	if err != nil {
		t.Fatalf("RunGrid: %v", err)
	}
	for _, c := range cells.Cells {
		if c.PromptTokensReported == 0 {
			t.Errorf("cell %s/%s reports 0 prompt tokens while the endpoint supplied %d — the "+
				"figure is the only token number obtainable without a tokenizer, and discarding "+
				"it leaves the rune budget an assumption rather than a measurement",
				c.Write, c.Query, model.promptTokens)
		}
	}
}

// TestCellsCountMemoriesTooLargeForTheBudget pins the pathology the first real
// run walked into.
//
// assemble SKIPS a memory larger than the remaining budget rather than
// truncating it — right, because half a record is not what any policy wrote.
// But skipping silently made two very different failures print the same thing:
// "every record was too big" and "search returned nothing" both reported 0 runes
// used. Measured 2026-09-01 on the real corpus, where the median session is
// 9,808 characters and the budget was 4,000: the verbatim baseline assembled
// nothing and scored 0, which would have made every chunking policy beat it by
// construction rather than by merit.
//
// The count is what turns that from a mystery into the finding ADR-047 says it
// is: a policy that cannot fill the window.
func TestCellsCountMemoriesTooLargeForTheBudget(t *testing.T) {
	store := newFakeStore()
	opts, _ := gridOpts(store)
	// Well under one verbatim session, so every record is too large to place.
	opts.ContextRunes = 50

	cells, err := RunGrid(context.Background(), store, twoQuestionSelection(t),
		[]string{"verbatim"}, []string{"verbatim"}, opts)
	if err != nil {
		t.Fatalf("RunGrid: %v", err)
	}
	for _, c := range cells.Cells {
		if c.MemoriesSkipped == 0 {
			t.Errorf("cell %s/%s assembled %d runes and reported 0 skipped memories — a run that "+
				"could place nothing must say so, or it is indistinguishable from a search that "+
				"returned nothing", c.Write, c.Query, c.BudgetRunesUsed)
		}
		if c.BudgetRunesUsed != 0 {
			t.Errorf("cell %s/%s used %d runes against a %d-rune budget with oversized records",
				c.Write, c.Query, c.BudgetRunesUsed, opts.ContextRunes)
		}
	}
}

// TestGoldRankIsAPositionNotABoolean is what makes "did asking better help?"
// answerable at all.
//
// The retrieval column was a hit RATE, and measured 2026-09-01 against the real
// corpus every write policy scored exactly 1.000 — saturated at a page limit of
// 20 with a cross-encoder in front, so the column separated nothing. A rate says
// the gold record was on the page; the rank says where, and moving it from
// position 9 to position 1 is the whole claim a query policy makes.
func TestGoldRankIsAPositionNotABoolean(t *testing.T) {
	sessionOf := map[string]string{"d1": "s_other", "d2": "s_other", "d3": "s_gold"}
	hits := []palace.SearchHit{
		{Drawer: palace.Drawer{ID: "d1"}, MemoryID: "d1"},
		{Drawer: palace.Drawer{ID: "d2"}, MemoryID: "d2"},
		{Drawer: palace.Drawer{ID: "d3"}, MemoryID: "d3"},
	}
	if got := goldRank(hits, sessionOf, []string{"s_gold"}); got != 3 {
		t.Errorf("goldRank = %d, want 3 — the position is the measurement", got)
	}
	if got := goldRank(hits, sessionOf, []string{"s_absent"}); got != 0 {
		t.Errorf("goldRank = %d for a gold session that never came back, want 0", got)
	}

	// A repeated memory must not consume a position. A decomposing policy merges
	// several searches and returns duplicates, and counting them would make its
	// rank a function of how many searches it ran rather than of how well it
	// asked — which is the confound the cap on decomposed already guards.
	dup := []palace.SearchHit{
		{Drawer: palace.Drawer{ID: "d1"}, MemoryID: "d1"},
		{Drawer: palace.Drawer{ID: "d1"}, MemoryID: "d1"},
		{Drawer: palace.Drawer{ID: "d3"}, MemoryID: "d3"},
	}
	if got := goldRank(dup, sessionOf, []string{"s_gold"}); got != 2 {
		t.Errorf("goldRank = %d with a duplicated hit, want 2 — a repeat is not a position", got)
	}
}

// TestCellsReportTheRetrievalRank asserts the column is a RECIPROCAL RANK and
// not the hit rate wearing a new name.
//
// ⚠The first version of this test checked only that the value was non-zero and
// at most 1 — which the broken arithmetic also satisfies, so a mutant that
// counted every hit as rank 1 SURVIVED it. That is the gate-that-cannot-fail
// class this repository keeps finding, and it is why the store returns hits in
// reverse order here: the gold record is then not at position 1, so a genuine
// reciprocal rank must come out STRICTLY BELOW the hit rate.
func TestCellsReportTheRetrievalRank(t *testing.T) {
	store := newFakeStore()
	// Two decoys ahead of everything, so the gold record cannot be at position 1
	// and a real reciprocal rank must fall below the hit rate.
	store.padHits = 2
	opts, _ := gridOpts(store)
	cells, err := RunGrid(context.Background(), store, twoQuestionSelection(t),
		[]string{"verbatim"}, []string{"verbatim"}, opts)
	if err != nil {
		t.Fatalf("RunGrid: %v", err)
	}
	var checked int
	for _, c := range cells.Cells {
		if c.RetrievalScored == 0 {
			continue
		}
		checked++
		if c.RetrievalMRR == 0 {
			t.Errorf("cell %s/%s scored %d retrieval questions and reports no MRR — the rate is "+
				"saturated on the real corpus, so the rank is the only column that can separate "+
				"one query policy from another", c.Write, c.Query, c.RetrievalScored)
		}
		if c.RetrievalMRR > 1 {
			t.Errorf("MRR = %f, which is not a reciprocal rank", c.RetrievalMRR)
		}
		// The binding assertion: gold is deliberately not first, so 1/rank must be
		// strictly less than the hit rate. Equality means the reciprocal was never
		// taken and this column is the saturated rate it exists to replace.
		if c.RetrievalMRR >= c.RetrievalRate() {
			t.Errorf("cell %s/%s reports MRR %.4f against hit rate %.4f with the gold record "+
				"pushed off position 1 — the two can only match if the rank is being ignored",
				c.Write, c.Query, c.RetrievalMRR, c.RetrievalRate())
		}
	}
	if checked == 0 {
		t.Fatal("no cell scored a retrieval question, so every assertion above passed over nothing")
	}
}

func TestCellsJSONNamesItsConfiguration(t *testing.T) {
	store := newFakeStore()
	opts, _ := gridOpts(store)
	opts.RankingProfile, opts.ModelID = "p", "m"
	cells, err := RunGrid(context.Background(), store, twoQuestionSelection(t),
		[]string{"verbatim"}, []string{"verbatim"}, opts)
	if err != nil {
		t.Fatalf("RunGrid: %v", err)
	}
	blob, err := cells.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	for _, want := range []string{"ranking_profile", "context_runes", "subset_ids", "dataset_sha256"} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("the results file does not name %q — a number whose configuration is not "+
				"written beside it cannot be compared with a later run", want)
		}
	}
}
