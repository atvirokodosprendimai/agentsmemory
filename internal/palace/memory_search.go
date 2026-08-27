package palace

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// maxCandidateWidening caps how far past candidateK the memory arm will widen
// its vector prefix: eight TIMES it, which is three doublings. That is far more
// headroom than a clustered prefix of siblings needs, and it converts an
// unbounded corpus walk into a bounded one when a scope filter and the index
// disagree.
const maxCandidateWidening = 8

// scopeDrops counts what the scope rule removed, split by cause.
//
// The four are NOT interchangeable. OutOfScope is an ALARM rather than a metric:
// the wing/room comparison is documented as redundant when the index honoured the
// filter, and kept solely so a stale index cannot surface another wing's memory —
// so a non-zero count there means the vector index and the durable rows have
// diverged. Orphan is the same divergence seen from the other side (an index row
// whose drawer is gone). OverDistance is the caller's own max_distance boundary
// and is ordinary.
//
// Without the split, all three plus chunk-to-memory collapse are one number: a
// trace showing retrieve count=200 and collapse count=3 reads identically whether
// 200 chunks belonged to 3 memories or 197 were thrown away.
type scopeDrops struct {
	Orphan       int
	OutOfScope   int
	OverDistance int
	// Superseded counts records the validity filter removed (ADR-038 T5), and it
	// is deliberately its OWN field rather than folded into any of the three
	// above.
	//
	// An ended drawer keeps its vector, so the index still returns it and the
	// filter drops it here — which means a page can come back shorter than limit
	// with nothing saying why. Reporting is the chosen answer rather than
	// over-fetching, because over-fetching changes what the pool means and every
	// measurement taken against it. A page short because records were superseded
	// and a page short because of wing policy are different facts about the
	// system, and one merged counter answers neither.
	Superseded int
}

// Any reports whether the predicate dropped anything at all.
func (d scopeDrops) Any() bool {
	return d.Orphan+d.OutOfScope+d.OverDistance+d.Superseded > 0
}

// survivorsFrom applies the scope rule to a retrieved prefix: it drops orphan
// vectors, rows outside the wing/room the caller asked for, and rows beyond the
// distance boundary, preserving the index's closest-first order. It also reports
// how many DISTINCT memories survived, which is what the widening loop needs to
// decide whether another round can help.
//
// It exists as one function because two callers need the identical predicate —
// searchCandidates while widening, and rankRetrieved while ranking. Spelling a
// scope rule twice is how one copy quietly goes stale, and a stale copy of THIS
// rule surfaces another wing's memory.
//
// The index filter is an optimization; the durable row remains the authority.
func survivorsFrom(hits []store.Hit, rows map[string]Drawer, q SearchQuery, stale bool) ([]SearchHit, int, scopeDrops) {
	survivors := make([]SearchHit, 0, len(hits))
	distinct := make(map[string]struct{}, len(hits))
	var drops scopeDrops
	for _, h := range hits {
		d, ok := rows[h.ID]
		if !ok {
			drops.Orphan++
			continue // orphan vector (row deleted) — skip
		}
		if !drawerMatchesSearch(d, q) {
			drops.OutOfScope++
			continue
		}
		// AFTER the wing/room check, so OutOfScope keeps its meaning as a
		// divergence alarm: a superseded record counted there would look like the
		// vector index and the durable rows had drifted apart.
		if d.ValidTo != "" && !q.IncludeHistory {
			drops.Superseded++
			continue
		}
		distance := distanceFromScore(h.Score)
		if q.MaxDistance > 0 && distance > q.MaxDistance {
			drops.OverDistance++
			continue
		}
		memoryID := memoryOf(d)
		survivors = append(survivors, SearchHit{Drawer: d, MemoryID: memoryID, Distance: distance, StaleIndex: stale})
		distinct[memoryID] = struct{}{}
	}
	return survivors, len(distinct), drops
}

// searchCandidates resolves a vector prefix to the rows behind it. It widens the
// ordered prefix until candidateK distinct logical memories survive, or the
// backend has no more results. Without this widening, a long memory can spend
// every slot on siblings before BM25 or the cross-encoder gets a chance to
// compare anything else.
//
// It returns the RAW hits and their rows rather than the survivors it filtered,
// which looks wasteful and is deliberate: rankRetrieved takes (hits, rows)
// because the eval arms share one retrieved pool across every arm, and an arm
// that retrieved for itself would confound the comparison those arms exist to
// make. Filtering here is for the widening decision only; rankRetrieved rebuilds
// the survivors from survivorsFrom, the same predicate, over an in-memory slice
// bounded by candidateK.
func (s *Service) searchCandidates(ctx context.Context, teamID string, q SearchQuery, vec []float32, candidateK int) ([]store.Hit, map[string]Drawer, bool, error) {
	retrieveCtx, retrieve := telemetry.Start(ctx, telemetry.StageRetrieve, attribute.Int("am.k", candidateK))
	hydrateCtx, hydrate := telemetry.Start(retrieveCtx, telemetry.StageHydrate)
	k := candidateK
	// Rows already resolved by a narrower prefix. Widening re-asks the index for
	// a SUPERSET in the same order, so every row loaded on a previous round is
	// still wanted on this one; refetching them made the doubling cost about
	// twice the final prefix in database work rather than once.
	rows := make(map[string]Drawer)
	// Separate from rows because an orphan vector resolves to no row at all;
	// without this it would be re-queried on every widening round.
	looked := make(map[string]bool)
	rounds := 0
	hydrated := 0
	finish := func(hits []store.Hit, err error, stop string) {
		if err != nil {
			hydrate.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
			retrieve.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
			return
		}
		if hydrated == 0 {
			hydrate.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonEmpty), attribute.Int("am.count", 0))
		} else {
			hydrate.End(telemetry.Ran, attribute.Int("am.count", hydrated))
		}
		attrs := []attribute.KeyValue{
			attribute.Int("am.count", len(hits)),
			attribute.Int("am.k", k),
			attribute.Int("am.rounds", rounds),
		}
		if stop != "" {
			attrs = append(attrs, telemetry.AttrReason(stop))
		}
		retrieve.End(telemetry.Ran, attrs...)
	}
	stale := false // the last round's index staleness; every surviving hit of this recall shares it
	for {
		rounds++
		res, err := s.vectors.Search(retrieveCtx, teamID, vec, k, searchFilter(q))
		if err != nil {
			// The widen loop's only error exit, and it must route through finish:
			// finish records the failed-closed outcome, ends the retrieve/hydrate
			// spans (and their counters). A bare return here would leave both
			// spans dangling and the failure invisible to exactly the telemetry a
			// debugging session needs.
			finish(nil, err, "")
			return nil, nil, false, fmt.Errorf("vector search: %w", err)
		}
		hits := res.H
		stale = res.StaleIndex

		missing := make([]string, 0, len(hits))
		for _, h := range hits {
			if !looked[h.ID] {
				looked[h.ID] = true
				missing = append(missing, h.ID)
			}
		}
		if len(missing) > 0 {
			fetched, err := s.repo.GetMany(hydrateCtx, teamID, missing)
			if err != nil {
				finish(nil, err, "")
				return nil, nil, false, fmt.Errorf("load drawer rows: %w", err)
			}
			hydrated += len(fetched)
			for id, d := range fetched {
				rows[id] = d
			}
		}

		// Only the distinct-memory COUNT is needed here; rankRetrieved rebuilds the
		// survivors itself from the same helper. Returning raw hits+rows rather
		// than survivors is what lets rankRetrieved keep the signature the eval
		// arms depend on — they share one retrieved pool across arms, so a
		// per-arm retrieval would confound the comparison they exist to make.
		// Drops are DISCARDED here on purpose: this call runs once per widening
		// round, so recording them would multiply the counts by the number of rounds.
		// rankRetrieved calls the same predicate once over the final pool and records
		// them there.
		_, distinct, _ := survivorsFrom(hits, rows, q, stale)

		stop := retrieveStop(distinct, candidateK, len(hits), k, q, hits)
		widen := []attribute.KeyValue{
			attribute.Int("am.k", k),
			attribute.Int("am.hits", len(hits)),
			attribute.Int("am.distinct", distinct),
		}
		if stop != "" {
			widen = append(widen, attribute.String("am.stop", stop))
		}
		retrieve.Event("widen", widen...)
		if stop != "" {
			finish(hits, nil, stop)
			return hits, rows, stale, nil
		}
		k *= 2
	}
}

// retrieveStop is why the widening loop ended. Empty means double and go again.
// The reason lands on both the widen event and the retrieve span so a dump can
// be compared to the loop in searchCandidates without re-reading the source.
//
// Hits are closest-first: once the farthest member of a full prefix is outside
// the caller's distance boundary, every later member is too (max_distance).
// The doubling ceiling is a safety stop for when the index filter and the
// durable row disagree and distinct never grows — not a tuning knob.
func retrieveStop(distinct, candidateK, nHits, k int, q SearchQuery, hits []store.Hit) string {
	if distinct >= candidateK {
		return telemetry.ReasonEnough
	}
	if nHits < k {
		return telemetry.ReasonExhausted
	}
	if q.MaxDistance > 0 && len(hits) > 0 && distanceFromScore(hits[len(hits)-1].Score) > q.MaxDistance {
		return telemetry.ReasonMaxDistance
	}
	if k >= candidateK*maxCandidateWidening {
		return telemetry.ReasonWidenCeiling
	}
	return ""
}

func drawerMatchesSearch(d Drawer, q SearchQuery) bool {
	return (q.Wing == "" || d.Wing == q.Wing) && (q.Room == "" || d.Room == q.Room)
}

// collapseCandidatesToMemories turns retrieved chunks into one scoring document
// per logical memory. The best vector distance and the number of retrieved
// matching chunks remain explicit signals; lexical and rerank text comes from
// the whole reassembled memory.
func (s *Service) collapseCandidatesToMemories(ctx context.Context, teamID string, q SearchQuery, chunks []SearchHit) ([]SearchHit, error) {
	// One pass, not one pass per root. Rescanning every chunk for each root is
	// quadratic in the candidate pool, and the pool is exactly what the memory
	// arm widens.
	roots := make([]string, 0, len(chunks))
	best := make(map[string]SearchHit, len(chunks))
	matched := make(map[string]int, len(chunks))
	for _, h := range chunks {
		if matched[h.MemoryID] == 0 {
			roots = append(roots, h.MemoryID)
			best[h.MemoryID] = h
		} else if h.Distance < best[h.MemoryID].Distance {
			best[h.MemoryID] = h
		}
		matched[h.MemoryID]++
	}
	byRoot, err := s.repo.MemoryChunksByRoots(ctx, teamID, roots)
	if err != nil {
		return nil, fmt.Errorf("load logical memories: %w", err)
	}

	// The correction sweep, once for the whole page. A correction attaches to the
	// record it corrects as an INCOMING edge, so nothing an outgoing walk does
	// can see it — which is why a session that bootstraps perfectly still reads
	// whatever the tier got wrong and believes it.
	//
	// Non-fatal: a page that cannot resolve corrections is worse than one that
	// can, and still better than no page.
	corrections, cerr := s.CorrectionsFor(ctx, teamID, roots, s.wingPolicyFor(ctx, teamID, q.Wing))
	if cerr != nil {
		slog.WarnContext(ctx, "corrections not resolved; the page may present a contradicted record as current", "err", cerr)
		corrections = nil
	}

	out := make([]SearchHit, 0, len(roots))
	for _, root := range roots {
		representative := best[root]

		// Filter into a fresh slice rather than over byRoot[root] in place.
		// Reusing that backing array truncates the map entry the caller handed
		// us, which is only safe while nothing else reads it — a property that
		// holds today and would break silently the moment this load is shared.
		memoryChunks := byRoot[root]
		inScope := make([]Drawer, 0, len(memoryChunks))
		for _, d := range memoryChunks {
			if !drawerMatchesSearch(d, q) {
				continue
			}
			// The ended-sibling filter, and it is NOT redundant with the one in
			// survivorsFrom. That one runs over the RETRIEVED chunk; this expansion
			// re-reads every sibling under the root through MemoryChunksByRoots,
			// which is history-inclusive — so an ended sibling of a current root is
			// reassembled into MemoryContent, scored by BM25 and the cross-encoder,
			// and returned as part of the memory.
			//
			// The mixed state is ROUTINE rather than exotic: purgeSource ends only
			// the chunks whose content key left the source, so any re-file that
			// drops a trailing chunk leaves a current root with an ended child. A
			// supersede ends a memory whole; a re-file does not.
			if d.ValidTo != "" && !q.IncludeHistory {
				continue
			}
			inScope = append(inScope, d)
		}
		representative.MemoryID = root
		representative.MemoryContent = reassembleMemory(inScope)
		if representative.MemoryContent == "" {
			representative.MemoryContent = representative.Drawer.Content
		}
		representative.ChunksMatched = matched[root]
		// Marked in its normal rank position. Hiding or demoting a corrected
		// record would be a ranking decision made on somebody else's assertion,
		// and a retraction can itself be wrong.
		representative.Corrections = corrections[normalizeEntityID(root)]
		out = append(out, representative)
	}
	return out, nil
}

// storedWithoutOverlap reports whether a chunk came from the ONE writer that
// does not overlap adjacent chunks: the diary.
//
// The discriminator used to be "has an author", which is wrong and was
// expensive. Mine stamps an author on every drawer it writes — defaulting to
// DefaultMineAgent when the caller supplies none, so it is never empty — while
// mineChunkText overlaps by MineChunkOverlap. Every multi-chunk MINED memory
// therefore took the no-overlap branch and re-emitted its overlap at each
// boundary: measured at +4,477 runes on a 19,390-rune source, with 57 of 260
// paragraphs appearing twice.
//
// Author-without-source is exact: WriteDiary and Mine are the only writers that
// set an author, Mine requires a source (sanitizeSource rejects an empty one),
// and Add sets no author at all. So this admits diary chunks and nothing else,
// including a memory mined into the diary room.
func storedWithoutOverlap(d Drawer) bool {
	return d.Agent != "" && d.SourceFile == ""
}

// reassembleMemory removes the exact overlap chunking added while preserving
// diary chunks, which were stored without any. It never summarizes or invents
// prose: the result is stored content in chunk order.
//
// ONE character is not stored content, and it is named here rather than left for
// a reader to find: when two adjacent chunks do not overlap and the join would
// weld a word to a word, a single SPACE is inserted. Without it "…a wor" and
// "d then…" reassemble into a token that appears in no chunk, which is worse
// than a space that appears in none — a fabricated word can be searched for and
// found, and a reader cannot tell it was fabricated.
//
// evidenceFromRegions is scrupulous about declaring its ellipsis as the one
// non-copied string it emits; this is the same declaration, for the same reason.
func reassembleMemory(chunks []Drawer) string {
	if len(chunks) == 0 {
		return ""
	}
	if len(chunks) == 1 {
		return chunks[0].Content
	}
	var b strings.Builder
	b.WriteString(chunks[0].Content)

	// Carry only the TAIL of what has been written, never the whole prefix.
	//
	// The seam between two chunks can overlap by at most ChunkOverlap runes, so
	// nothing earlier than the last ChunkOverlap runes can ever match — yet the
	// previous shape re-read the entire accumulated text on every chunk
	// (b.String(), then []rune of it, TWICE on the zero-overlap branch). That is
	// quadratic in the memory's length and it is paid per distinct memory in the
	// candidate pool AND once per returned hit, in both arms, on the default read
	// path. Benchmarked before the change: 512k runes cost 105ms and 419MB
	// against 6.6ms and 12.5MB bounded — 4x the input for 15x the allocation.
	tail := tailRunes(chunks[0].Content, ChunkOverlap)
	for i := 1; i < len(chunks); i++ {
		next := chunks[i].Content
		nextRunes := []rune(next)
		if storedWithoutOverlap(chunks[i]) {
			b.WriteString(next)
			tail = appendTail(tail, nextRunes, ChunkOverlap)
			continue
		}
		overlap := exactOverlap(tail, nextRunes, ChunkOverlap)
		if overlap == 0 && len(tail) > 0 && len(nextRunes) > 0 {
			if isWordRune(tail[len(tail)-1]) && isWordRune(nextRunes[0]) {
				b.WriteByte(' ')
				tail = appendTail(tail, []rune{' '}, ChunkOverlap)
			}
		}
		written := nextRunes[overlap:]
		b.WriteString(string(written))
		tail = appendTail(tail, written, ChunkOverlap)
	}
	return b.String()
}

// tailRunes returns the last n runes of s, or all of them when it is shorter.
func tailRunes(s string, n int) []rune {
	r := []rune(s)
	if len(r) > n {
		return append([]rune(nil), r[len(r)-n:]...)
	}
	return r
}

// appendTail extends tail with add and keeps only the last n runes.
//
// It COPIES when it trims rather than resliceing, because a reslice keeps the
// whole grown backing array alive — which would reintroduce, as retained memory,
// exactly the unbounded growth this bookkeeping exists to remove.
func appendTail(tail, add []rune, n int) []rune {
	if len(add) >= n {
		return append([]rune(nil), add[len(add)-n:]...)
	}
	combined := append(tail, add...)
	if len(combined) > n {
		return append([]rune(nil), combined[len(combined)-n:]...)
	}
	return combined
}

// exactOverlap returns how many runes of right's prefix repeat left's suffix, up
// to maxRunes. left is the bounded TAIL of the accumulated text, not the whole of
// it: capping the comparison at maxRunes makes every rune before that unreachable,
// so passing more would change the cost and never the answer.
func exactOverlap(left, right []rune, maxRunes int) int {
	maxRunes = min(maxRunes, len(left), len(right))
	for n := maxRunes; n > 0; n-- {
		if string(left[len(left)-n:]) == string(right[:n]) {
			return n
		}
	}
	return 0
}

// maxMemoryEvidenceRegions keeps each cross-encoder passage at least as large as
// the measured agent-visible snippet size. More, smaller fragments cover extra
// term occurrences but hide the reasoning that follows them — the live failure
// this limit fixes presented sixteen 100-rune shards from a 1600-rune budget.
const maxMemoryEvidenceRegions = ChunkSize / DefaultSnippetChars

// memoryEvidence gives the cross-encoder a few coherent matching regions within
// one existing chunk-sized budget. At most four places share that budget, so
// each selected passage carries the same 400-rune reasoning context as a normal
// search snippet. Region text is verbatim and position ordered; the ellipsis
// only marks omitted distance between those source slices.
func memoryEvidence(content, query, fallback string) string {
	regions := snippetRegions(content, query, ChunkSize, maxMemoryEvidenceRegions, true)
	matched := false
	for _, region := range regions {
		matched = matched || region.Score > 0
	}
	if len(regions) == 0 || !matched {
		runes := []rune(fallback)
		if len(runes) > ChunkSize {
			runes = runes[:ChunkSize]
		}
		return string(runes)
	}
	return evidenceFromRegions(regions)
}

// evidenceFromRegions joins verbatim, source-ordered passages inside the
// existing cross-encoder budget. The ellipsis marks omitted source text; it is
// the only text not copied from the memory itself.
func evidenceFromRegions(regions []Region) string {
	var b strings.Builder
	remaining := ChunkSize
	for i, region := range regions {
		separator := ""
		if i > 0 {
			separator = " … "
		}
		if len([]rune(separator)) >= remaining {
			break
		}
		b.WriteString(separator)
		remaining -= len([]rune(separator))
		runes := []rune(region.Text)
		if len(runes) > remaining {
			runes = runes[:remaining]
		}
		b.WriteString(string(runes))
		remaining -= len(runes)
		if remaining == 0 {
			break
		}
	}
	return b.String()
}

func (h SearchHit) rankingContent(query string, evidence bool) string {
	if h.MemoryContent == "" {
		return h.Drawer.Content
	}
	if evidence {
		return memoryEvidence(h.MemoryContent, query, h.Drawer.Content)
	}
	return h.MemoryContent
}
