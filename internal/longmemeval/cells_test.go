package longmemeval

import (
	"context"
	"strings"
	"testing"
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
