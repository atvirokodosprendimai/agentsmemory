package longmemeval

import (
	"encoding/json"
	"fmt"
)

// Header identifies the configuration a table of cells is valid FOR.
//
// ADR-007's rule applied to this instrument: there is no number without its
// population. Every field here is something a later run could differ in, and a
// results file that cannot say which one it was taken under cannot be compared
// with anything — the numbers would look poolable and would not be.
type Header struct {
	DatasetPath    string   `json:"dataset_path"`
	DatasetSHA256  string   `json:"dataset_sha256"`
	SubsetIDs      []string `json:"subset_ids"`
	Seed           int64    `json:"seed"`
	ModelID        string   `json:"model_id"`
	EndpointKind   string   `json:"endpoint_kind"`
	ContextRunes   int      `json:"context_runes"`
	RankingProfile string   `json:"ranking_profile"`
	Commit         string   `json:"commit"`
}

// Mergeable reports whether two tables may be pooled.
//
// It refuses on any difference that would show up as a delta: pooling cells from
// two reader models, two budgets, two corpora or two ranking profiles reports
// that difference as if it were the policy under test, which is the exact error
// ADR-032 recorded when a corpus was substituted underneath a comparison.
func (h Header) Mergeable(other Header) error {
	for _, f := range []struct {
		name string
		a, b any
	}{
		{"dataset", h.DatasetSHA256, other.DatasetSHA256},
		{"reader/judge model", h.ModelID, other.ModelID},
		{"context budget", h.ContextRunes, other.ContextRunes},
		{"ranking profile", h.RankingProfile, other.RankingProfile},
	} {
		if f.a != f.b {
			return fmt.Errorf("cells are not comparable: %s differs (%v vs %v) — the contrast "+
				"between them would be that difference reported as the policy", f.name, f.a, f.b)
		}
	}
	return nil
}

// Cell is one (write policy × query policy) square of the grid.
//
// Scored and RetrievalScored are separate denominators on purpose: the retrieval
// column excludes abstention questions, which have no answer location, so the two
// metrics are taken over different populations and a shared denominator would
// silently misreport one of them.
type Cell struct {
	Write   string `json:"write"`
	Query   string `json:"query"`
	Correct int    `json:"correct"`
	Scored  int    `json:"scored"`

	RetrievalHit    int `json:"retrieval_hit"`
	RetrievalScored int `json:"retrieval_scored"`

	// RetrievalMRR is the mean reciprocal rank of the first gold-session record,
	// over the questions the retrieval column scores.
	//
	// It exists because RetrievalHit SATURATES and therefore cannot answer the
	// question a query policy is for. Measured 2026-09-01 against the real corpus
	// with a page limit of 20 and a cross-encoder in front: every write policy
	// scored a retrieval rate of exactly 1.000, so the column separated nothing.
	// A rate says the gold record was somewhere on the page; the reciprocal rank
	// says WHERE, and moving it from position 9 to position 1 is precisely what
	// "ask the question better" is supposed to do.
	//
	// ⚠A higher MRR is not by itself a reason to promote a policy. ADR-047 exists
	// because rank is the metric a superset wins: this column is here so it can
	// DISAGREE with the judged one in public, not so it can stand in for it.
	RetrievalMRR float64 `json:"retrieval_mrr"`

	// BudgetRunesUsed is how much of the shared budget this cell actually spent,
	// averaged over its questions. A policy that cannot fill the window is a
	// finding rather than a footnote: it means the budget was not the binding
	// constraint for that row, so its delta is not a budget-constrained one.
	BudgetRunesUsed int `json:"budget_runes_used"`
	// PromptTokensReported is the reader endpoint's own count where it supplies
	// one, and 0 where it does not. It is what makes the rune budget auditable
	// rather than assumed — see ADR-047 property 1.
	PromptTokensReported int `json:"prompt_tokens_reported"`
	// MemoriesSkipped is how many retrieved memories did not fit the budget and
	// were left out, summed over the cell's questions.
	//
	// ADR-047 says a policy that cannot fill the window is a finding rather than
	// a footnote, and without this the finding is INVISIBLE: assemble skips an
	// oversized memory rather than truncating it, so a policy whose records are
	// all too large reports BudgetRunesUsed 0 — indistinguishable from a policy
	// whose search returned nothing. Measured 2026-09-01 on the real corpus: at a
	// 4000-rune budget the verbatim baseline assembled NOTHING, because the median
	// LongMemEval session is 9,808 characters.
	MemoriesSkipped int `json:"memories_skipped"`
}

// Accuracy is the judged share correct, the headline number.
func (c Cell) Accuracy() float64 {
	if c.Scored == 0 {
		return 0
	}
	return float64(c.Correct) / float64(c.Scored)
}

// RetrievalRate is the secondary column: did the policy's records for the gold
// session come back at all, judged by nothing.
func (c Cell) RetrievalRate() float64 {
	if c.RetrievalScored == 0 {
		return 0
	}
	return float64(c.RetrievalHit) / float64(c.RetrievalScored)
}

// Cells is one run's whole table together with the configuration it is valid for.
type Cells struct {
	Header Header `json:"header"`
	Cells  []Cell `json:"cells"`
}

// JSON renders the results file.
func (c Cells) JSON() ([]byte, error) { return json.MarshalIndent(c, "", "  ") }

// scoresRetrieval reports whether a question belongs in the retrieval-only
// column.
//
// Abstention items do not: they have no answer location, which is why the
// benchmark's own retrieval evaluator excludes them. Scoring them would put the
// same zero into every cell alike and damp every contrast the grid exists to
// measure — while leaving the judged column, where unanswerability IS the thing
// being scored, untouched. Raised in review of PR #148.
func scoresRetrieval(q Question) bool {
	return !Verdict{ID: q.ID}.IsAbstention() && len(q.GoldSessionIDs) > 0
}
