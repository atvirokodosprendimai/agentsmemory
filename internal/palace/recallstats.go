package palace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
)

// This file answers the question drawer counts cannot: is the memory being USED,
// and does it answer? A palace that grows while every recall comes back empty is
// a filing cabinet nobody opens, and from the inside those two states look
// identical — both show more drawers every day.
//
// Recording is best-effort by design (see recordSearch): a statistics write must
// never be able to fail a recall.

// searchEventRow is the gorm view of one recorded recall.
type searchEventRow struct {
	ID         string  `gorm:"column:id;primaryKey"`
	TeamID     string  `gorm:"column:team_id"`
	Wing       string  `gorm:"column:wing"`
	Room       string  `gorm:"column:room"`
	Query      string  `gorm:"column:query"`
	Candidates int     `gorm:"column:candidates"`
	Hits       int     `gorm:"column:hits"`
	TopScore   float64 `gorm:"column:top_score"`
	// TopRerankScore is the cross-encoder's score for the best hit, 0 when no
	// cross-encoder ran (tell the two apart with Reranked — these are logits, so 0
	// is mid-range and a genuine value, not "no match").
	//
	// It exists because TopScore cannot carry this: under FUSION=rrf the fused
	// score is a rank encoding, so its top-1 value is nearly constant regardless of
	// how well anything matched. ADR-001 measured the cross-encoder separating
	// answerable from unanswerable by ~4.7 in median while cosine distance
	// separated them by 0.022 and "no threshold separates them at any value".
	TopRerankScore float64 `gorm:"column:top_rerank_score"`
	Reranked       int     `gorm:"column:reranked"`
	// RerankSkipReason is WHY the cross-encoder did not order this page, using
	// telemetry's reason vocabulary so a span and this row cannot say different
	// words about one recall. Empty when reranking ran; NULL on rows written
	// before the column existed, which the aggregate excludes rather than
	// counting as healthy.
	RerankSkipReason string `gorm:"column:rerank_skip_reason"`
	CreatedAt        string `gorm:"column:created_at"`
}

// TableName pins the table name so gorm does not pluralise the struct name.
func (searchEventRow) TableName() string { return "search_events" }

// SampleSearchQueries returns a random sample of distinct query texts agents
// actually ran against a wing. The eval replays them as CatReal cases: unlike a
// generated question, nothing about a real query was phrased to suit any
// ranking arm's feature, which is what makes this the arm that breaks the
// generator's circularity. Trivial fragments are excluded — a two-word probe
// tells the judge nothing — and the eval's own searches never appear here
// because they run with SkipTelemetry.
func (r *Repo) SampleSearchQueries(ctx context.Context, teamID, wing string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	q := r.reader.WithContext(ctx).Model(&searchEventRow{}).
		Where("team_id = ? AND length(query) >= 12", teamID)
	if wing != "" {
		q = q.Where("wing = ?", wing)
	}
	var queries []string
	if err := q.Distinct("query").Order("RANDOM()").Limit(limit).Pluck("query", &queries).Error; err != nil {
		return nil, err
	}
	return queries, nil
}

// WingRecall is one wing's recall record over a window: how often it was asked,
// how often it answered, and how much it holds.
//
// Answered is the number with at least one hit. It is deliberately the headline
// rather than an average score: a score is only comparable within one query,
// while "did this wing have anything to say" compares across all of them.
type WingRecall struct {
	Wing     string
	Searches int
	Answered int
	// AvgTop is the mean top-hit FUSED score across ANSWERED searches. Under
	// FUSION=rrf the fused score is a rank encoding, so its top-1 value is nearly
	// constant and this says almost nothing about match quality. Kept for
	// continuity with rows written before AvgTopRerank existed.
	AvgTop float64
	// AvgTopRerank is the mean top-hit CROSS-ENCODER score, over reranked answered
	// searches only. This is the number that separates a recall which answered from
	// one that did not: ADR-001 measured medians 0.891 against -3.832, where cosine
	// distance managed 0.401 against 0.423 and separated nothing.
	AvgTopRerank float64
	// Reranked is how many answered searches a cross-encoder actually ordered, and
	// it is the denominator AvgTopRerank was divided by. Reported so nobody reads
	// an average over three searches as a property of the wing.
	Reranked int
	// RerankSkips counts, per reason, the answered recalls where a cross-encoder
	// did NOT order the page. Rows where it ran contribute to nothing, and rows
	// predating the column (NULL) are excluded — "not recorded" is not "nothing
	// skipped". This is what tells a disabled reranker from a failing one, which
	// `Reranked` alone cannot: it is 0 for both.
	RerankSkips map[string]int
	Drawers     int    // drawers currently filed in this wing
	LastUsed    string // most recent search against this wing, RFC3339 ("" if none)
	LastFiled   string // most recent drawer filed into it ("" if none)
}

// AnsweredPct is the share of searches that returned something, 0 when the wing
// was never searched. This is the number to watch over weeks: it should climb as
// a wing accumulates the memories its questions are actually about.
func (w WingRecall) AnsweredPct() int {
	if w.Searches == 0 {
		return 0
	}
	return int(float64(w.Answered)/float64(w.Searches)*100 + 0.5)
}

// RecallStats is the whole picture for a window: per-wing recall, plus the
// queries that found nothing.
type RecallStats struct {
	Since    string
	Searches int
	Answered int
	Writes   int // drawers filed in the window and requested wing scope
	Wings    []WingRecall
	// Unanswered are recent queries that returned no hits, newest first. These are
	// the actionable half of the report: each one names a memory the team looked
	// for and does not have.
	Unanswered []string
	// Suggestions are the unanswered queries turned into a to-write list: near-
	// duplicate phrasings collapsed into one entry each, counted, wing-tagged, and
	// ranked. Where Unanswered says what was asked, Suggestions says what to DO —
	// five phrasings of the same missing memory read as one suggestion asked five
	// times, not as five separate gaps.
	Suggestions []MemorySuggestion
}

// MemorySuggestion is one memory the team should write: a cluster of recent
// searches that all came back empty, collapsed across paraphrasings. It carries
// the wing the searches were scoped to because "write this memory" is only
// actionable when it also says WHERE — the same ask against two wings is two
// different gaps.
type MemorySuggestion struct {
	Query     string // the most recent phrasing of the ask, verbatim
	Times     int    // how many unanswered searches collapsed into this one
	Wing      string // wing the searches were scoped to ("(unscoped)" when none)
	LastAsked string // RFC3339 time of the most recent ask
}

// AnsweredPct is the share of all searches in the window that returned something.
func (r RecallStats) AnsweredPct() int {
	if r.Searches == 0 {
		return 0
	}
	return int(float64(r.Answered)/float64(r.Searches)*100 + 0.5)
}

// recordSearch files one recall event. Errors are swallowed on purpose: this is
// measurement, and measurement that can break the thing it measures is worse than
// no measurement. The caller therefore ignores the return.
func (r *Repo) recordSearch(ctx context.Context, e searchEventRow) {
	if e.ID == "" {
		e.ID = randomID()
	}
	if e.CreatedAt == "" {
		e.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_ = r.db.WithContext(ctx).Create(&e).Error
}

// randomID returns a short unique id for an event row.
func randomID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A clock-based fallback keeps recording alive if the entropy source is
		// unavailable; a duplicate id here costs one lost statistic, nothing more.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// RecallStats aggregates a team's recall over the last `since` duration.
// A non-empty wing narrows every part of the report to that project; an empty
// wing deliberately reports the whole workspace.
//
// Wings with no searches still appear when they hold drawers: a wing that is
// written to and never read is exactly the pattern worth seeing, and it would be
// invisible in a search-only report.
func (s *Service) RecallStats(ctx context.Context, teamID, wing string, since time.Duration, unansweredLimit int) (RecallStats, error) {
	if since <= 0 {
		since = 24 * time.Hour
	}
	if unansweredLimit <= 0 {
		unansweredLimit = 10
	}
	cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
	out := RecallStats{Since: cutoff}

	type agg struct {
		Wing         string
		Searches     int
		Answered     int
		SumTop       float64
		SumTopRerank float64
		Reranked     int
		LastUsed     string
	}
	var rows []agg
	searches := s.repo.reader.WithContext(ctx).
		Model(&searchEventRow{}).
		Select("wing, COUNT(*) AS searches, SUM(CASE WHEN hits > 0 THEN 1 ELSE 0 END) AS answered, "+
			"SUM(CASE WHEN hits > 0 THEN top_score ELSE 0 END) AS sum_top, "+
			// Averaged over RERANKED answered searches only. Folding in rows where no
			// cross-encoder ran would divide a sum of real logits by a count that
			// includes zeros that mean "not measured", which is the shape of the
			// write-to-read ratio this project already had to retract.
			"SUM(CASE WHEN hits > 0 AND reranked = 1 THEN top_rerank_score ELSE 0 END) AS sum_top_rerank, "+
			"SUM(CASE WHEN hits > 0 AND reranked = 1 THEN 1 ELSE 0 END) AS reranked, "+
			"MAX(created_at) AS last_used").
		Where("team_id = ? AND created_at >= ?", teamID, cutoff)
	if wing != "" {
		searches = searches.Where("wing = ?", wing)
	}
	if err := searches.
		Group("wing").
		Scan(&rows).Error; err != nil {
		return RecallStats{}, fmt.Errorf("aggregate search events: %w", err)
	}

	// The skip breakdown is a SECOND query rather than more columns on the one
	// above, because it groups by (wing, reason) and that one groups by wing.
	// Folding them together would need a pivot over a value set that is defined
	// in Go, not in SQL — and would silently drop any reason added later.
	//
	// NULL is excluded, not counted: a row written before this column existed
	// says "not recorded", and treating that as "nothing was skipped" would
	// report every historical recall as healthy.
	type skipAgg struct {
		Wing   string
		Reason string
		N      int
	}
	var skipRows []skipAgg
	skips := s.repo.reader.WithContext(ctx).
		Model(&searchEventRow{}).
		Select("wing, rerank_skip_reason AS reason, COUNT(*) AS n").
		Where("team_id = ? AND created_at >= ?", teamID, cutoff).
		Where("rerank_skip_reason IS NOT NULL AND rerank_skip_reason != ''")
	if wing != "" {
		skips = skips.Where("wing = ?", wing)
	}
	if err := skips.Group("wing, rerank_skip_reason").Scan(&skipRows).Error; err != nil {
		return RecallStats{}, fmt.Errorf("aggregate rerank skips: %w", err)
	}
	skipsByWing := map[string]map[string]int{}
	for _, r := range skipRows {
		w := r.Wing
		if w == "" {
			w = "(unscoped)"
		}
		if skipsByWing[w] == nil {
			skipsByWing[w] = map[string]int{}
		}
		skipsByWing[w][r.Reason] = r.N
	}

	byWing := map[string]*WingRecall{}
	for _, a := range rows {
		wing := a.Wing
		if wing == "" {
			// An unscoped search belongs to no wing; it is still counted in the
			// totals below, and named so the report does not silently drop it.
			wing = "(unscoped)"
		}
		w := &WingRecall{Wing: wing, Searches: a.Searches, Answered: a.Answered, LastUsed: a.LastUsed}
		if a.Answered > 0 {
			w.AvgTop = a.SumTop / float64(a.Answered)
		}
		w.Reranked = a.Reranked
		w.RerankSkips = skipsByWing[wing]
		if a.Reranked > 0 {
			w.AvgTopRerank = a.SumTopRerank / float64(a.Reranked)
		}
		byWing[wing] = w
		out.Searches += a.Searches
		out.Answered += a.Answered
	}

	// Drawer counts and last-filed per wing, so a wing that is only written to
	// still shows up.
	type wingCount struct {
		Wing      string
		Drawers   int
		LastFiled string
	}
	var counts []wingCount
	drawers := s.repo.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Select("wing, COUNT(*) AS drawers, MAX(filed_at) AS last_filed").
		Where("team_id = ?", teamID)
	if wing != "" {
		drawers = drawers.Where("wing = ?", wing)
	}
	if err := drawers.
		Group("wing").
		Scan(&counts).Error; err != nil {
		return RecallStats{}, fmt.Errorf("count drawers per wing: %w", err)
	}
	for _, c := range counts {
		w, ok := byWing[c.Wing]
		if !ok {
			w = &WingRecall{Wing: c.Wing}
			byWing[c.Wing] = w
		}
		w.Drawers, w.LastFiled = c.Drawers, c.LastFiled
	}

	// Writes in the window, across every requested wing.
	var writes int64
	writesQuery := s.repo.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Where("team_id = ? AND filed_at >= ?", teamID, cutoff)
	if wing != "" {
		writesQuery = writesQuery.Where("wing = ?", wing)
	}
	if err := writesQuery.
		Count(&writes).Error; err != nil {
		return RecallStats{}, fmt.Errorf("count writes: %w", err)
	}
	out.Writes = int(writes)

	for _, w := range byWing {
		out.Wings = append(out.Wings, *w)
	}
	// Busiest wings first, then the merely-large ones — the report is read top
	// down and the first lines should be where the work happened.
	sortWings(out.Wings)

	// One fetch feeds both views of the same events: the raw Unanswered list is
	// the first unansweredLimit rows, and the grouped Suggestions are built over
	// the whole batch. The scan limit is deliberately wider than the display limit
	// — a suggestion's count is only honest if the paraphrases beyond the first
	// page are counted too.
	var unanswered []searchEventRow
	unansweredQuery := s.repo.reader.WithContext(ctx).
		Model(&searchEventRow{}).
		Where("team_id = ? AND created_at >= ? AND hits = 0 AND query <> ''", teamID, cutoff)
	if wing != "" {
		unansweredQuery = unansweredQuery.Where("wing = ?", wing)
	}
	if err := unansweredQuery.
		Order("created_at DESC").
		Limit(max(unansweredLimit, suggestionScanLimit)).
		Find(&unanswered).Error; err != nil {
		return RecallStats{}, fmt.Errorf("list unanswered searches: %w", err)
	}
	for i, u := range unanswered {
		if i >= unansweredLimit {
			break
		}
		out.Unanswered = append(out.Unanswered, strings.TrimSpace(u.Query))
	}
	out.Suggestions = groupSuggestions(unanswered)
	return out, nil
}

// suggestionScanLimit bounds how many unanswered events feed the grouping. It
// exists so a runaway session cannot make the stats endpoint scan an unbounded
// table; 200 covers any realistic session while keeping the query fast enough
// for the Stop hook's 1-2s budget.
const suggestionScanLimit = 200

// maxSuggestions caps the grouped list. The hook shows three; ten leaves room
// for other readers (the MCP tool, a human curling /stats) without turning the
// payload into a second unanswered dump.
const maxSuggestions = 10

// groupSuggestions collapses unanswered search events (newest first) into
// deduplicated, counted, ranked write-me suggestions.
//
// The near-duplicate rule is ONE deliberately simple check: two queries in the
// same wing collapse when their significant-token sets overlap by more than 60%
// of the smaller set. Containment-on-the-smaller-set was chosen over a token
// prefix rule and over symmetric Jaccard: a prefix rule breaks the moment a
// paraphrase reorders words ("annotations for kubernetes ingress" vs
// "kubernetes ingress annotations"), and Jaccard punishes elaboration — "how do
// kubernetes ingress annotations work" is the same ask as "kubernetes ingress
// annotations", but the extra glue drags a symmetric ratio below any sane
// threshold. Containment collapses a query and its longer restatement while
// distinct topics, which share few tokens, stay apart. No LLM is involved: this
// runs on the Stop-hook stats path, where determinism and a millisecond budget
// matter more than clustering finesse.
//
// The newest occurrence supplies the representative Query and LastAsked — the
// most recent phrasing is the freshest statement of what is actually wanted.
func groupSuggestions(events []searchEventRow) []MemorySuggestion {
	type group struct {
		s      MemorySuggestion
		tokens map[string]bool
		norm   string
	}
	var groups []*group
	for _, e := range events {
		q := strings.TrimSpace(e.Query)
		if q == "" {
			continue
		}
		wing := e.Wing
		if wing == "" {
			// Mirror the wing table's naming so the suggestion and the per-wing
			// rows describe the same place. "(unscoped)" is itself actionable: it
			// says the searches never named a wing, so the writer must pick one.
			wing = "(unscoped)"
		}
		tokens, norm := significantTokens(q), strings.ToLower(q)
		merged := false
		for _, g := range groups {
			if g.s.Wing == wing && sameAsk(tokens, g.tokens, norm, g.norm) {
				g.s.Times++
				merged = true
				break
			}
		}
		if !merged {
			groups = append(groups, &group{
				s:      MemorySuggestion{Query: q, Times: 1, Wing: wing, LastAsked: e.CreatedAt},
				tokens: tokens,
				norm:   norm,
			})
		}
	}
	out := make([]MemorySuggestion, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.s)
	}
	sortSuggestions(out)
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// sameAsk is the collapse rule described on groupSuggestions. Queries whose
// significant tokens all fell to the stopword filter cannot be compared by
// overlap (the ratio would divide by zero), so they collapse only on exact
// normalized equality — safer to show two suggestions than to merge two
// different asks.
func sameAsk(a, b map[string]bool, normA, normB string) bool {
	if len(a) == 0 || len(b) == 0 {
		return normA == normB
	}
	// A one-token query is 100% contained in every group that happens to share
	// that token, which would fold a bare "kubernetes" into whichever kubernetes
	// topic was seen first and inflate its count with a different ask. Where
	// containment cannot discriminate, demand the sets match exactly.
	if len(a) == 1 || len(b) == 1 {
		return len(a) == len(b) && sharedTokens(a, b) == len(a)
	}
	smaller := len(a)
	if len(b) < smaller {
		smaller = len(b)
	}
	return float64(sharedTokens(a, b))/float64(smaller) > 0.6
}

// sharedTokens counts the tokens two sets have in common.
func sharedTokens(a, b map[string]bool) int {
	shared := 0
	for t := range a {
		if b[t] {
			shared++
		}
	}
	return shared
}

// stopTokens are the glue words that appear in almost any phrasing of a question
// and carry no topic. The list is deliberately tiny: every word added here makes
// two DIFFERENT asks look more alike, and a false merge hides a gap the team
// should see.
var stopTokens = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "in": true,
	"on": true, "at": true, "for": true, "and": true, "or": true, "is": true,
	"are": true, "was": true, "do": true, "does": true, "did": true, "how": true,
	"what": true, "why": true, "where": true, "when": true, "which": true,
	"with": true, "about": true, "this": true, "that": true, "it": true,
	"its": true, "can": true, "should": true,
}

// significantTokens lowercases a query, splits on anything that is not a letter
// or digit, and drops the glue. Single-rune tokens go too — they are almost
// always leftovers of the split, not topics.
func significantTokens(q string) map[string]bool {
	tokens := map[string]bool{}
	for _, t := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		// Rune count, not byte length: a single CJK character or a Lithuanian
		// "ą" is a whole word, and dropping it by byte length would silently
		// empty the token set of a non-ASCII query.
		if utf8.RuneCountInString(t) < 2 || stopTokens[t] {
			continue
		}
		tokens[t] = true
	}
	return tokens
}

// sortSuggestions ranks the to-write list: most-asked first, then most recent.
// Count leads because a memory five people (or five phrasings) went looking for
// is worth writing before one asked once; recency breaks ties because RFC3339
// strings in one timezone compare correctly as text, and the newest gap is the
// one still on someone's mind. The query string is the final, stable tiebreak.
func sortSuggestions(ss []MemorySuggestion) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && lessSuggestion(ss[j], ss[j-1]); j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

func lessSuggestion(a, b MemorySuggestion) bool {
	if a.Times != b.Times {
		return a.Times > b.Times
	}
	if a.LastAsked != b.LastAsked {
		return a.LastAsked > b.LastAsked
	}
	return a.Query < b.Query
}

// SuggestionLines renders the top suggestions in the plain-text report's
// transport format, one line per suggestion:
//
//	write: 3x kubernetes ingress annotations [wing_acme]
//
// The "  write: " prefix is a contract with the Stop hook
// (clients/claude-code/hooks/agentsmemory-stop-hook.sh): the hook greps these
// lines out of the /stats body and re-renders them as its "memories to write"
// section, so the prefix must stay grep-stable. A human curling /stats reads
// the same lines unassisted. Nil when there is nothing to suggest — the report
// stays silent when it has nothing to say.
func (r RecallStats) SuggestionLines(max int) []string {
	if max <= 0 {
		max = 3
	}
	var lines []string
	for i, s := range r.Suggestions {
		if i >= max {
			break
		}
		lines = append(lines, fmt.Sprintf("  write: %dx %s [%s]", s.Times, s.Query, s.Wing))
	}
	return lines
}

// sortWings orders wings by searches, then drawers, then name — descending on the
// two numbers so the busiest wing leads, with the name as a stable tiebreak.
func sortWings(ws []WingRecall) {
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0 && less(ws[j], ws[j-1]); j-- {
			ws[j], ws[j-1] = ws[j-1], ws[j]
		}
	}
}

func less(a, b WingRecall) bool {
	if a.Searches != b.Searches {
		return a.Searches > b.Searches
	}
	if a.Drawers != b.Drawers {
		return a.Drawers > b.Drawers
	}
	return a.Wing < b.Wing
}

// compile-time guard that the stats queries keep using the gorm handle they were
// written against.
var _ = (*gorm.DB)(nil)
