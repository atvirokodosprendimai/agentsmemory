package longmemeval

import (
	"context"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/gen"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// Store is the slice of the palace this grid uses.
//
// It is declared here, at the consumer, rather than exported from palace: the
// grid needs three methods of a service with dozens, and naming them here is
// what lets the acceptance fence stay hermetic — RunGrid is driven against an
// in-memory fake, so the central invariants are checked without a database, an
// embedder or a model.
//
// A write policy reaches the store only through Add, deliberately: a policy that
// bypassed it would measure a store no agent can actually write to.
type Store interface {
	Add(ctx context.Context, teamID string, in palace.AddInput) (palace.AddResult, error)
	Search(ctx context.Context, teamID string, q palace.SearchQuery) ([]palace.SearchHit, error)
	List(ctx context.Context, teamID, wing, room string, limit, offset int) ([]palace.Drawer, error)
}

// Model is the generative endpoint the reader and the judge share.
//
// One interface for both because ADR-047 property 3 holds them to ONE model
// across the whole grid: the cell delta is then the policy, and cells taken
// under different models are never pooled.
type Model interface {
	Generate(ctx context.Context, prompt string, temperature float64) (gen.Result, error)
}

// GridOptions is everything a run needs that is not a policy.
type GridOptions struct {
	Wing         string // base name; each (cell, question) gets its own scope beneath it
	TeamID       string
	ContextRunes int
	SearchLimit  int

	Reader Model
	Judge  Model

	// Recorded into the results header, not used to compute anything.
	DatasetPath    string
	DatasetSHA256  string
	ModelID        string
	EndpointKind   string
	RankingProfile string
	Commit         string
}

// readerTemperature is 0 for the reader and the judge alike.
//
// Both are being measured, not sampled: variance in either would show up as
// variance in a cell delta and be read as a difference between policies. The
// generator that produces eval questions runs warmer because it wants variety;
// this does not.
const readerTemperature = 0.0

// scratchWing is the isolation boundary: ONE scope per (cell, question).
//
// ⚠Per question, not per cell, and that distinction is the finding PR #148's
// review raised. Each LongMemEval question carries its own ~48-session haystack
// and upstream evaluates each instance against its own haystack alone. A scope
// created once per cell and written to for every question leaves question 2
// searching question 1's history, and the contamination grows monotonically —
// so the last questions of a cell are measured under a different instrument than
// the first, and the cell mean is not a mean of anything.
func scratchWing(base, write, query, questionID string) string {
	safe := func(s string) string {
		return strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
				return r
			case r >= 'A' && r <= 'Z':
				return r + 32
			default:
				return '_'
			}
		}, s)
	}
	return fmt.Sprintf("%s_%s_%s_%s", safe(base), safe(write), safe(query), safe(questionID))
}

// RunGrid scores every (write policy × query policy) cell over one subset under
// one shared context budget.
//
// One question in one cell is: create its own scope, ingest its haystack through
// the write policy, search under the query policy, assemble the returned
// memories up to the budget, read, judge, tally. The retrieval-only column is
// computed from the same search, which is why it is nearly free.
func RunGrid(ctx context.Context, store Store, sel Selection, writes, queries []string, opts GridOptions) (Cells, error) {
	if opts.ContextRunes <= 0 {
		return Cells{}, fmt.Errorf("context budget is %d: a run with no budget is unbounded, and "+
			"the shared budget is the property that makes these cells comparable at all", opts.ContextRunes)
	}
	out := Cells{Header: Header{
		DatasetPath:    opts.DatasetPath,
		DatasetSHA256:  opts.DatasetSHA256,
		SubsetIDs:      sel.IDs,
		Seed:           sel.Seed,
		ModelID:        opts.ModelID,
		EndpointKind:   opts.EndpointKind,
		ContextRunes:   opts.ContextRunes,
		RankingProfile: opts.RankingProfile,
		Commit:         opts.Commit,
	}}

	for _, wname := range writes {
		wp, ok := WritePolicyByName(wname)
		if !ok {
			return Cells{}, fmt.Errorf("unknown write policy %q; %s", wname, WritePolicyUsage())
		}
		for _, qname := range queries {
			qp, ok := QueryPolicyByName(qname)
			if !ok {
				return Cells{}, fmt.Errorf("unknown query policy %q; %s", qname, QueryPolicyUsage())
			}
			cell, err := runCell(ctx, store, sel, wp, qp, opts)
			if err != nil {
				return Cells{}, err
			}
			out.Cells = append(out.Cells, cell)
		}
	}
	return out, nil
}

// runCell scores one square of the grid.
func runCell(ctx context.Context, store Store, sel Selection, wp WritePolicy, qp QueryPolicy, opts GridOptions) (Cell, error) {
	cell := Cell{Write: wp.Name, Query: qp.Name}
	var budgetUsed, promptTokens int

	for _, q := range sel.Questions {
		wing := scratchWing(opts.Wing, wp.Name, qp.Name, q.ID)
		existing, err := store.List(ctx, opts.TeamID, wing, "", 1, 0)
		if err != nil {
			return Cell{}, fmt.Errorf("checking scope %s: %w", wing, err)
		}
		if len(existing) > 0 {
			return Cell{}, fmt.Errorf("scope %s is not empty: a run into it would measure "+
				"memories no policy in this grid wrote", wing)
		}

		// Ingest under the write policy, keeping each returned drawer id against
		// the Record that produced it. That map is the only thing that can score
		// the retrieval column: once a transformed record is in the store nothing
		// else recovers which session it came from.
		sessionOf := map[string]string{}
		for _, rec := range wp.Write(q) {
			res, err := store.Add(ctx, opts.TeamID, palace.AddInput{
				Wing: wing, Room: rec.Room, Content: rec.Content,
			})
			if err != nil {
				return Cell{}, fmt.Errorf("ingest into %s: %w", wing, err)
			}
			for _, d := range res.Drawers {
				sessionOf[d.ID] = rec.SessionID
			}
		}

		var hits []palace.SearchHit
		for _, query := range qp.Queries(q) {
			got, err := store.Search(ctx, opts.TeamID, palace.SearchQuery{
				Query: query, Wing: wing, Limit: opts.SearchLimit, SkipTelemetry: true,
			})
			if err != nil {
				return Cell{}, fmt.Errorf("search %s: %w", wing, err)
			}
			hits = append(hits, got...)
		}

		memories, used := assemble(hits, opts.ContextRunes)
		budgetUsed += used

		answer, err := opts.Reader.Generate(ctx, ReaderPrompt(q.Question, memories), readerTemperature)
		if err != nil {
			return Cell{}, fmt.Errorf("reader on %s: %w", q.ID, err)
		}
		// The endpoint's OWN count of the prompt it tokenized, where it supplies
		// one. This is what makes the rune budget auditable rather than assumed:
		// the policies rewrite text differently, so equal rune counts can carry
		// unequal tokens, and this is the only token figure obtainable without a
		// tokenizer this repository does not have. ADR-047 property 1.
		promptTokens += answer.PromptTokens

		raw, err := opts.Judge.Generate(ctx, JudgePrompt(Verdict{
			ID: q.ID, Type: q.Type, Question: q.Question, Gold: q.Answer, Candidate: answer.Text,
		}), readerTemperature)
		if err != nil {
			return Cell{}, fmt.Errorf("judge on %s: %w", q.ID, err)
		}
		// ⚠An unreadable verdict aborts the cell rather than scoring zero: a model
		// outage recorded as a policy losing is invisible afterwards, because the
		// cell is simply lower.
		correct, err := ParseVerdict(raw.Text)
		if err != nil {
			return Cell{}, fmt.Errorf("judge on %s: %w", q.ID, err)
		}
		cell.Scored++
		if correct {
			cell.Correct++
		}

		if scoresRetrieval(q) {
			cell.RetrievalScored++
			if retrieved(hits, sessionOf, q.GoldSessionIDs) {
				cell.RetrievalHit++
			}
		}
	}
	if cell.Scored > 0 {
		cell.BudgetRunesUsed = budgetUsed / cell.Scored
		cell.PromptTokensReported = promptTokens / cell.Scored
	}
	return cell, nil
}

// assemble takes returned memories in rank order until the budget is spent, and
// reports how much of it was used.
//
// A memory that does not fit is skipped rather than truncated: half a record is
// not what any policy wrote, and scoring a policy on a fragment it did not
// produce would make the budget a property of the assembler instead of the run.
func assemble(hits []palace.SearchHit, budget int) ([]string, int) {
	var out []string
	used := 0
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.MemoryID] {
			continue // a merged multi-query result can return one memory twice
		}
		seen[h.MemoryID] = true
		text := h.MemoryContent
		if text == "" {
			text = h.Drawer.Content
		}
		n := len([]rune(text))
		if used+n > budget {
			continue
		}
		out = append(out, text)
		used += n
	}
	return out, used
}

// retrieved reports whether any returned memory came from a gold session.
//
// It resolves through the ingest-time map rather than through position, because
// one-fact and bounded change the record count and duplicate content is legal —
// so nothing about a returned drawer's ordinal says which session produced it.
func retrieved(hits []palace.SearchHit, sessionOf map[string]string, gold []string) bool {
	want := make(map[string]bool, len(gold))
	for _, g := range gold {
		want[g] = true
	}
	for _, h := range hits {
		for _, id := range []string{h.Drawer.ID, h.MemoryID} {
			if want[sessionOf[id]] {
				return true
			}
		}
	}
	return false
}
