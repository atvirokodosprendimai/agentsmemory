package longmemeval

import (
	"context"
	"fmt"
	"sort"
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
	// DeleteWing releases a scratch scope once its question is scored.
	//
	// ⚠ WITHOUT IT A GRID IS SINGLE-USE AND LEAVES ITS SPOIL IN A REAL PALACE. The
	// per-question isolation fix made the scope `<base>_<write>_<query>_<question>`,
	// so a 4x3 grid at --n 20 writes 240 wings into whatever palace
	// EnsureLocalWorkspace resolved — the operator's own — and nothing removed them.
	// The emptiness guard then refused the SECOND run, correctly, because the first
	// had filled every scope, and the documented rollback names ONE exact wing so it
	// removed none of them. Reported in #167 after #148 merged.
	DeleteWing(ctx context.Context, teamID, wing, confirm string) (palace.DeleteWingResult, error)
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
	var budgetUsed, promptTokens, skipped int
	var reciprocalRank float64

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

		// ⚠ ONE CANDIDATE POOL, FUSED BY RANK — NOT A CONCATENATION OF FULL PAGES.
		//
		// This ran every sub-query at the FULL --search-limit and appended the
		// pages, which broke both retrieval columns in opposite directions and made
		// any row involving `decomposed` incomparable to the baselines:
		//
		//   RetrievalRate  inflated — 3 sub-queries saw 3x the candidates
		//   RetrievalMRR   deflated — goldRank ranks by position in the
		//                  concatenation, so the gold record as the BEST hit of
		//                  sub-query 3 scored RR 0.024 against 0.333 for finding it
		//                  third on sub-query 1.
		//
		// It also reached the reader: assemble walks the merged list in order, so at
		// the default budget against a ~9,808-rune median session both records the
		// reader saw came from sub-query 1 and the later searches were near-inert on
		// the judged column too. Reported in #167 before ADR-047 T5 read the grid.
		//
		// Two changes, because there were two faults. The per-query limit is DIVIDED
		// so the candidate pool is constant however many sub-queries a policy asks —
		// which is what decomposedCap's own comment says the design wants, that a
		// policy "could win by retrieving more rather than by asking better"; the cap
		// bounded the number of searches and nothing bounded the pool. And the pages
		// are fused by RECIPROCAL RANK rather than appended, so a record ranks on
		// where each sub-query put it and agreement across sub-queries counts for
		// something. RRF is this repository's existing fusion (palace.ArmRRF); using
		// it here keeps the instrument speaking the vocabulary the ranking work does.
		queries := qp.Queries(q)
		perQuery := opts.SearchLimit / max(1, len(queries))
		if perQuery < 1 {
			perQuery = 1
		}
		pages := make([][]palace.SearchHit, 0, len(queries))
		for _, query := range queries {
			got, err := store.Search(ctx, opts.TeamID, palace.SearchQuery{
				Query: query, Wing: wing, Limit: perQuery, SkipTelemetry: true,
			})
			if err != nil {
				return Cell{}, fmt.Errorf("search %s: %w", wing, err)
			}
			pages = append(pages, got)
		}
		hits := fuseByRank(pages)

		// ⚠ RELEASED HERE, NOT AFTER THE JUDGE. Everything downstream — assemble,
		// the reader, the judge, the retrieval rank — works from `hits` and
		// `sessionOf`, which are already in memory. Deleting now means a reader or
		// judge failure aborts the cell without stranding the scope, which is the
		// case that made the previous run's spoil permanent.
		if _, err := store.DeleteWing(ctx, opts.TeamID, wing, wing); err != nil {
			return Cell{}, fmt.Errorf("releasing scope %s: %w", wing, err)
		}

		memories, used, missed := assemble(hits, opts.ContextRunes)
		skipped += missed
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
			// The RANK, not merely whether it came back. A boolean hit saturates:
			// measured 2026-09-01 on the real corpus, every policy scored 1.000 at
			// a page limit of 20, so the column could not tell any of them apart.
			// The reciprocal rank still moves when a better-asked query pulls the
			// gold record from position 9 to position 1, which is the question a
			// query policy exists to answer.
			if r := goldRank(hits, sessionOf, q.GoldSessionIDs); r > 0 {
				cell.RetrievalHit++
				reciprocalRank += 1 / float64(r)
			}
		}
	}
	if cell.Scored > 0 {
		cell.BudgetRunesUsed = budgetUsed / cell.Scored
		cell.PromptTokensReported = promptTokens / cell.Scored
		cell.MemoriesSkipped = skipped
	}
	if cell.RetrievalScored > 0 {
		cell.RetrievalMRR = reciprocalRank / float64(cell.RetrievalScored)
	}
	return cell, nil
}

// assemble takes returned memories in rank order until the budget is spent, and
// reports how much of it was used.
//
// A memory that does not fit is skipped rather than truncated: half a record is
// not what any policy wrote, and scoring a policy on a fragment it did not
// produce would make the budget a property of the assembler instead of the run.
func assemble(hits []palace.SearchHit, budget int) (memories []string, used, skipped int) {
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
			// Counted, not silent: a policy whose records are all larger than the
			// budget assembles NOTHING, and without this the run reports 0 runes
			// used — indistinguishable from a search that returned nothing at all.
			skipped++
			continue
		}
		memories = append(memories, text)
		used += n
	}
	return memories, used, skipped
}

// goldRank reports the 1-based position of the first returned memory that came
// from a gold session, or 0 when none did.
//
// It resolves through the ingest-time map rather than through position, because
// one-fact and bounded change the record count and duplicate content is legal —
// so nothing about a returned drawer's ordinal says which session produced it.
func goldRank(hits []palace.SearchHit, sessionOf map[string]string, gold []string) int {
	want := make(map[string]bool, len(gold))
	for _, g := range gold {
		want[g] = true
	}
	// Deduped in the order presented, because that is the order the reader saw and
	// therefore the only order a rank can honestly describe. A merged multi-query
	// result returns some memories twice, and counting a repeat as a position
	// would make a decomposing policy's rank a function of how many searches it
	// ran rather than of how well it asked.
	seen := map[string]bool{}
	rank := 0
	for _, h := range hits {
		if seen[h.MemoryID] {
			continue
		}
		seen[h.MemoryID] = true
		rank++
		for _, id := range []string{h.Drawer.ID, h.MemoryID} {
			if want[sessionOf[id]] {
				return rank
			}
		}
	}
	return 0
}

// fuseByRank merges per-sub-query pages into one list by reciprocal rank, so a
// record's position reflects where each sub-query put it rather than which
// sub-query ran first.
//
// ⚠ THE ALTERNATIVE IS WHAT THIS REPLACED, AND IT WAS NOT NEUTRAL. Appending the
// pages makes position depend on sub-query ORDER, so the gold record as the best
// hit of the last sub-query ranks below every candidate the earlier ones returned
// — measured in #167 as RR 0.024 against 0.333 for the same record found third on
// the first sub-query. That reaches the reader too, because assemble walks this
// list in order under a fixed budget.
//
// The constant is 60, the value palace's own RRF arm uses. It is a rank offset,
// not a tuned parameter: it damps the difference between ranks 1 and 2 so a
// record two sub-queries agree on can outrank one that a single sub-query put
// first, which is the property fusion is for.
//
// A single page comes back in its own order, unchanged — verbatim and named-thing
// each return one query, so their columns mean exactly what they meant before.
func fuseByRank(pages [][]palace.SearchHit) []palace.SearchHit {
	if len(pages) == 1 {
		return pages[0]
	}
	const rrfK = 60.0
	score := map[string]float64{}
	first := map[string]palace.SearchHit{}
	var order []string
	for _, page := range pages {
		for i, hit := range page {
			id := hit.MemoryID
			if id == "" {
				id = hit.Drawer.ID
			}
			if _, seen := first[id]; !seen {
				first[id] = hit
				order = append(order, id)
			}
			score[id] += 1.0 / (rrfK + float64(i+1))
		}
	}
	// Sorted by fused score, ties broken by first appearance so the result is
	// stable: an unstable instrument reports a different number on a rerun of the
	// same data, which is indistinguishable from the policy having changed.
	sort.SliceStable(order, func(a, b int) bool { return score[order[a]] > score[order[b]] })
	out := make([]palace.SearchHit, 0, len(order))
	for _, id := range order {
		out = append(out, first[id])
	}
	return out
}
