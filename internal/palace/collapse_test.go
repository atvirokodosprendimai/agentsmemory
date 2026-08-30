package palace

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// orderedVectors returns a caller-controlled vector prefix and records every
// requested depth. It makes chunk crowding deterministic: the real vector
// backend is allowed to order siblings this way, but a corpus fixture should not
// depend on a fake embedder happening to do so today.
type orderedVectors struct {
	store.VectorStore
	hits []store.Hit
	ks   []int
}

func (o *orderedVectors) Search(ctx context.Context, namespace string, vector []float32, k int, filter store.Filter) (store.SearchResult, error) {
	if strings.HasSuffix(namespace, "::closets") {
		return o.VectorStore.Search(ctx, namespace, vector, k, filter)
	}
	o.ks = append(o.ks, k)
	if k > len(o.hits) {
		k = len(o.hits)
	}
	return store.SearchResult{H: append([]store.Hit(nil), o.hits[:k]...)}, nil
}

// recordingDocuments is a cross-encoder spy. Scores are intentionally equal:
// these tests inspect the documents the served path supplied, not a fake model's
// opinion of them.
type recordingDocuments struct {
	docs [][]string
}

func (r *recordingDocuments) Rerank(_ context.Context, _ string, docs []string) ([]float64, error) {
	r.docs = append(r.docs, append([]string(nil), docs...))
	return make([]float64, len(docs)), nil
}

// longMemory builds content long enough that Add chunks it, with a marker term
// repeated so every chunk matches the same query — which is exactly the shape
// that makes chunks crowd a page.
func longMemory(marker string, chunks int) string {
	var b strings.Builder
	for i := 0; i < chunks; i++ {
		b.WriteString(marker + " ")
		// ChunkSize is 1600 characters; fill past it so the splitter produces a
		// new chunk, and vary the filler so the chunks are not identical rows.
		for j := 0; j < 200; j++ {
			b.WriteString("filler")
			b.WriteByte(byte('a' + (i+j)%26))
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// TestSearchReturnsOneHitPerMemory: a page is a page of memories.
//
// Chunks of one memory are similar to the same query, so they cluster and crowd
// each other rather than spreading. Measured on a live palace before this
// existed: at limit 10, one query spent 2 slots on a single memory and another
// spent 4 slots on two, with the duplicate pairs adjacent.
//
// The eval could not see it. eval.go folds every hit onto its ParentID before
// scoring — including for the production arm — so it scores MEMORIES over a page
// production returned as CHUNKS. Its headline would be unchanged if production
// returned ten chunks of one memory.
func TestSearchReturnsOneHitPerMemory(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-collapse"

	if _, err := svc.Add(ctx, team, AddInput{
		Wing: "w", Room: "r", Content: longMemory("zephyrine", 4),
	}); err != nil {
		t.Fatalf("add chunked: %v", err)
	}
	for _, c := range []string{
		"zephyrine appears here too, in a memory short enough to stay whole",
		"zephyrine again, another distinct short memory",
	} {
		if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: c}); err != nil {
			t.Fatalf("add short: %v", err)
		}
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "zephyrine", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits at all — the fixture cannot exhibit the defect, so this test proves nothing")
	}

	seen := map[string]int{}
	for _, h := range hits {
		seen[memoryOf(h.Drawer)]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("memory %s occupies %d of %d slots on one page — chunks of one memory are "+
				"crowding out other memories, and `limit` does not mean what a caller thinks",
				id[:8], n, len(hits))
		}
	}
	if len(seen) != len(hits) {
		t.Errorf("%d slots hold only %d distinct memories", len(hits), len(seen))
	}
}

// TestSearchReportsHowManyChunksMatched: collapsing must not destroy the signal
// it collapses. A memory that matched in four places is stronger evidence than
// one that matched in one, and a silent collapse throws that away.
func TestSearchReportsHowManyChunksMatched(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-chunkcount"

	if _, err := svc.Add(ctx, team, AddInput{
		Wing: "w", Room: "r", Content: longMemory("quintessa", 4),
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "quintessa", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want one hit for one memory, got %d", len(hits))
	}
	if hits[0].ChunksMatched < 2 {
		t.Errorf("ChunksMatched = %d for a memory whose chunks all matched; the collapse threw "+
			"away how much of the memory was relevant", hits[0].ChunksMatched)
	}
}

// TestMemoryLevelSearchFillsThePoolWithDistinctMemories pins the defect that
// post-ranking collapse cannot repair: if the vector prefix is all siblings,
// BM25 and the cross-encoder never see the next memory at all.
//
// Search must widen the ordered prefix until distinct memories fill the pool,
// at which point BM25 promotes NEEDLE. Removing the widening makes this red;
// merely keeping the final collapse does not help.
func TestMemoryLevelSearchFillsThePoolWithDistinctMemories(t *testing.T) {
	ctx := context.Background()
	base := newTestService(t)
	const team = "team-memory-pool"

	long, err := base.Add(ctx, team, AddInput{
		Wing: "w", Room: "r", Content: longMemory("crowding", 6),
	})
	if err != nil {
		t.Fatalf("add long memory: %v", err)
	}
	if len(long.Drawers) < 6 {
		t.Fatalf("fixture produced %d chunks, want at least 6", len(long.Drawers))
	}
	target := mustAddOne(t, base, team, AddInput{
		Wing: "w", Room: "r", Content: "NEEDLE the memory-level candidate must become reachable",
	})
	var fillers []Drawer
	for i := 0; i < 5; i++ {
		fillers = append(fillers, mustAddOne(t, base, team, AddInput{
			Wing: "w", Room: "r", Content: fmt.Sprintf("unrelated distinct memory %d for pool capacity", i),
		}))
	}

	ordered := make([]store.Hit, 0, len(long.Drawers)+1+len(fillers))
	for i, d := range append(append(append([]Drawer(nil), long.Drawers...), target), fillers...) {
		ordered = append(ordered, store.Hit{ID: d.ID, Score: float32(1 - float64(i)/100)})
	}

	vectors := &orderedVectors{VectorStore: base.vectors, hits: ordered}
	svc := base.Clone()
	svc.vectors = vectors
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "NEEDLE", Limit: 2, SkipTelemetry: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(vectors.ks) < 2 {
		t.Fatalf("asked only for %v; sibling chunks still consume the declared memory pool", vectors.ks)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].MemoryContent, "NEEDLE") {
		t.Fatalf("memory outside the first chunk prefix was not promoted after pool fill: %+v", hits)
	}
}

// TestMemoryLevelRerankingCombinesCrossChunkEvidence proves the served Search
// path gives the cross-encoder one bounded document per memory, and that one
// document carries evidence assembled from separate chunks.
func TestMemoryLevelRerankingCombinesCrossChunkEvidence(t *testing.T) {
	ctx := context.Background()
	base := newTestService(t)
	const team = "team-memory-evidence"

	content := "ALPHA premise " + strings.Repeat("neutral filler sentence. ", 90) + " OMEGA conclusion"
	created, err := base.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: content})
	if err != nil {
		t.Fatalf("add split evidence: %v", err)
	}
	if len(created.Drawers) < 2 {
		t.Fatalf("fixture produced %d chunk(s); evidence is not split", len(created.Drawers))
	}
	mustAddOne(t, base, team, AddInput{Wing: "w", Room: "r", Content: "ALPHA competitor with one half"})

	reranker := &recordingDocuments{}
	svc := base.Clone().WithReranker(reranker, 10).WithRerankWeight(0.5)
	if _, err := svc.Search(ctx, team, SearchQuery{Query: "ALPHA OMEGA", Limit: 5, SkipTelemetry: true}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(reranker.docs) != 1 {
		t.Fatalf("reranker calls = %d, want 1", len(reranker.docs))
	}
	if got, want := len(reranker.docs[0]), 2; got != want {
		t.Fatalf("cross-encoded %d documents, want %d distinct memories", got, want)
	}
	combined := false
	for _, doc := range reranker.docs[0] {
		if got := len([]rune(doc)); got > ChunkSize {
			t.Errorf("evidence is %d runes, above the %d-rune model budget", got, ChunkSize)
		}
		if strings.Contains(doc, "ALPHA") && strings.Contains(doc, "OMEGA") {
			combined = true
		}
	}
	if !combined {
		t.Fatalf("no document combined evidence from separate chunks: %#v", reranker.docs[0])
	}
}

// TestMemoryLevelRerankingKeepsEnoughContextToJudgeTheAnswer reproduces the
// live failure where a complete long memory lost to a shorter, partial diary.
// The query term occurs in three distant passages and each answer follows it by
// more than 100 runes: fragmenting the model's 1600-rune budget into sixteen
// tiny slices hides all three reasons, while a few coherent passages expose the
// complete answer to the cross-encoder.
func TestMemoryLevelRerankingKeepsEnoughContextToJudgeTheAnswer(t *testing.T) {
	ctx := context.Background()
	base := newTestService(t)
	const team = "team-memory-reasoning"

	section := func(reason string, fill string) string {
		return "constraint " + strings.Repeat("background ", 18) + reason + " " + strings.Repeat(fill+" ", 260)
	}
	complete, err := base.Add(ctx, team, AddInput{
		Wing: "w", Room: "operations",
		Content: section("CAUSE_ALPHA", "amber") + section("CAUSE_BETA", "birch") + section("CAUSE_GAMMA", "cedar"),
	})
	if err != nil {
		t.Fatalf("add complete memory: %v", err)
	}
	if len(complete.Drawers) < 3 {
		t.Fatalf("fixture produced %d chunks, want at least 3", len(complete.Drawers))
	}
	partial := mustAddOne(t, base, team, AddInput{
		Wing: "w", Room: "operations", Content: "constraint short diary mentions CAUSE_ALPHA and CAUSE_BETA only",
	})

	ordered := make([]store.Hit, 0, len(complete.Drawers)+1)
	for i, d := range append(append([]Drawer(nil), complete.Drawers...), partial) {
		ordered = append(ordered, store.Hit{ID: d.ID, Score: float32(1 - float64(i)/100)})
	}
	reranker := rerankFunc(func(_ context.Context, _ string, docs []string) ([]float64, error) {
		scores := make([]float64, len(docs))
		for i, doc := range docs {
			for _, reason := range []string{"CAUSE_ALPHA", "CAUSE_BETA", "CAUSE_GAMMA"} {
				if strings.Contains(doc, reason) {
					scores[i]++
				}
			}
		}
		return scores, nil
	})

	svc := base.Clone().WithReranker(reranker, 10).WithRerankWeight(1)
	svc.vectors = &orderedVectors{VectorStore: base.vectors, hits: ordered}
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "constraint", Limit: 2, SkipTelemetry: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 || hits[0].MemoryID != complete.Drawers[0].ID {
		t.Fatalf("top memory = %+v, want the complete cross-chunk answer", hits)
	}
}

// TestMemoryEvidenceCoversDistinctQuestionClauses reproduces the second live
// failure: four passages repeating the dense first clause consumed all four
// evidence slots, so the lower-density "what remained open" clause vanished and
// an incomplete memory won. A multi-part question needs new vocabulary covered
// before another occurrence of vocabulary already represented.
func TestMemoryEvidenceCoversDistinctQuestionClauses(t *testing.T) {
	dense := "subject field deriving address wing room " + strings.Repeat("amber ", 150)
	content := strings.Repeat(dense, maxMemoryEvidenceRegions) +
		"remained open " + strings.Repeat("background ", 18) + "OPEN_REASON " + strings.Repeat("tail ", 30)

	evidence := memoryEvidence(content, "subject field deriving address wing room remained open", content[:ChunkSize])
	if !strings.Contains(evidence, "OPEN_REASON") {
		t.Fatalf("evidence omitted the distinct low-density query clause: %q", evidence)
	}
}

// TestReassembleMemoryPreservesChunkedText makes the whole-memory document
// falsifiable independently of ranking. ChunkText overlaps and trims window
// edges; removing the wrong overlap silently duplicates or drops prose.
func TestReassembleMemoryPreservesChunkedText(t *testing.T) {
	content := strings.Repeat("alpha beta gamma delta. ", 180) + "TAIL-MARKER"
	chunks := ChunkText(content, ChunkSize, ChunkOverlap, ChunkMin)
	if len(chunks) < 2 {
		t.Fatalf("fixture produced %d chunk(s)", len(chunks))
	}
	drawers := make([]Drawer, len(chunks))
	for i, chunk := range chunks {
		drawers[i] = Drawer{ChunkIndex: chunk.Index, Content: chunk.Content}
	}
	if got, want := reassembleMemory(drawers), strings.TrimSpace(content); got != want {
		t.Fatalf("reassembled add-drawer text changed: got %d runes, want %d", len([]rune(got)), len([]rune(want)))
	}
}

// TestReassembleMemoryPreservesDiaryWhitespace covers the other chunk protocol:
// diary windows never overlap or trim, so concatenation must be byte-exact.
func TestReassembleMemoryPreservesDiaryWhitespace(t *testing.T) {
	content := "  opening\n" + strings.Repeat("journal line with spacing  \n", 100) + "  closing  "
	chunks := diaryChunks(content, 211)
	drawers := make([]Drawer, len(chunks))
	for i, chunk := range chunks {
		drawers[i] = Drawer{Agent: "codex", ChunkIndex: chunk.Index, Content: chunk.Content}
	}
	if got := reassembleMemory(drawers); got != content {
		t.Fatalf("reassembled diary changed: got %q, want %q", got, content)
	}
}

// TestSearchKeepsTheBestChunkNotTheFirst: the surviving chunk must be the one
// that matched, because its snippet is the passage the caller asked for. Keeping
// chunk 0 would answer a different question than the one asked.
func TestSearchKeepsTheBestChunkNotTheFirst(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-bestchunk"

	// A memory whose DISTINCTIVE term lives only in a later chunk.
	var b strings.Builder
	for j := 0; j < 400; j++ {
		b.WriteString("preamble ")
	}
	b.WriteString(" ")
	for j := 0; j < 400; j++ {
		b.WriteString("nimbostratus ")
	}
	if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: b.String()}); err != nil {
		t.Fatalf("add: %v", err)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "nimbostratus", Limit: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if !strings.Contains(hits[0].Drawer.Content, "nimbostratus") {
		t.Errorf("the surviving chunk does not contain the term that was searched for — "+
			"the collapse kept the first chunk rather than the matching one: %.80s",
			hits[0].Drawer.Content)
	}
}

// TestGetMemoryReturnsEveryChunkInOrder: collapsing is only safe because the rest
// of the memory can be fetched. Before this, Repo.MemoryChunks was called by
// Update and Delete alone — no read path could reach a whole chunked memory.
func TestGetMemoryReturnsEveryChunkInOrder(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-whole"

	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: longMemory("thalassa", 3)})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(res.Drawers) < 2 {
		t.Fatalf("the fixture produced %d chunk(s); it cannot show a whole-memory read", len(res.Drawers))
	}

	chunks, err := svc.GetMemory(ctx, team, res.Drawers[0].ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("want every chunk of a chunked memory, got %d — the fixture did not chunk, so "+
			"this test cannot show the property", len(chunks))
	}
	for i, c := range chunks {
		if c.ChunkIndex != i {
			t.Errorf("chunk %d carries ChunkIndex %d — the chunks are not in order, so a caller "+
				"reassembling the memory gets it scrambled", i, c.ChunkIndex)
		}
	}
	// Asking with a LATER chunk's id must return the same whole memory: a caller
	// holding a search hit holds whichever chunk matched, not the first.
	fromLater, err := svc.GetMemory(ctx, team, chunks[len(chunks)-1].ID)
	if err != nil {
		t.Fatalf("GetMemory from a later chunk: %v", err)
	}
	if len(fromLater) != len(chunks) {
		t.Errorf("asking with the last chunk's id returned %d chunks, want %d — a caller can only "+
			"reassemble the memory if it holds chunk 0", len(fromLater), len(chunks))
	}
}

// TestRankOfCountsMemorySlotsNotChunkPositions: the eval's rank must be the slot
// the agent sees the answer at, and since Search collapses sibling chunks that is
// a count of MEMORIES above the gold, not of candidates.
//
// Without this, two chunks of an irrelevant memory sitting above the gold made
// the eval report rank 3 for something the served page puts in slot 2 — the same
// unit mismatch ADR-013 removed from Search, one level down, in the arithmetic.
func TestRankOfCountsMemorySlotsNotChunkPositions(t *testing.T) {
	// Two sibling chunks of memory A, then the gold B. Ordered as fetched.
	ids := []string{"A", "A", "B"}
	ordered := []int{0, 1, 2}
	got := rankOf(ids, ordered, map[string]bool{"B": true})
	if got != 2 {
		t.Errorf("rankOf = %d, want 2: memory A occupies ONE slot on the served page however "+
			"many of its chunks matched, so the gold is the second thing the agent sees", got)
	}

	// And the degenerate case still holds: no siblings, no change.
	if got := rankOf([]string{"A", "B"}, []int{0, 1}, map[string]bool{"B": true}); got != 2 {
		t.Errorf("rankOf with no sibling chunks = %d, want 2 — the fold must not move a rank "+
			"when there is nothing to fold", got)
	}
}

// TestReassemblingAMinedMemoryDoesNotDuplicateItsOverlap is a gate on the
// content an agent reads.
//
// reassembleMemory skipped overlap removal for chunks carrying an author,
// documented as "diary chunks, which were stored without overlap". Mine stamps
// an author on every drawer it writes and overlaps by MineChunkOverlap, so
// every multi-chunk mined memory was reassembled with its overlap re-emitted at
// each boundary — measured at +4,477 runes on a 19,390-rune source, 57 of 260
// paragraphs twice.
//
// collapseCandidatesToMemories reassembles every ranked memory, so Identity,
// Coverage, FullLength and the snippet regions are computed over that text.
func TestReassemblingAMinedMemoryDoesNotDuplicateItsOverlap(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	var b strings.Builder
	for i := 0; i < 260; i++ {
		fmt.Fprintf(&b, "Paragraph %d states a distinct claim about retrieval and its measurement. ", i)
	}
	original := b.String()

	if _, err := svc.Mine(ctx, "team-mine", MineInput{
		Wing: "wing_alpha", Room: "decisions", Source: "overlap.md", Content: original,
	}); err != nil {
		t.Fatalf("mine: %v", err)
	}

	var rows []drawerRow
	if err := svc.repo.db.WithContext(ctx).
		Where("team_id = ? AND source_file = ?", "team-mine", "overlap.md").
		Order("chunk_index ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load mined chunks: %v", err)
	}
	stored := make([]Drawer, 0, len(rows))
	for _, r := range rows {
		stored = append(stored, fromRow(r))
	}
	if len(stored) < 3 {
		t.Fatalf("mined into %d chunks; the fixture needs several boundaries", len(stored))
	}

	got := reassembleMemory(stored)
	if len([]rune(got)) > len([]rune(original)) {
		t.Fatalf("reassembled mined memory is %d runes against an original of %d (+%d, about %d boundaries x %d overlap): the stored overlap was re-emitted",
			len([]rune(got)), len([]rune(original)),
			len([]rune(got))-len([]rune(original)), len(stored)-1, MineChunkOverlap)
	}
	for i := 0; i < 260; i++ {
		marker := fmt.Sprintf("Paragraph %d states", i)
		if strings.Count(got, marker) > 1 {
			t.Fatalf("%q appears %d times in the reassembled memory; an agent reads the same prose twice",
				marker, strings.Count(got, marker))
		}
	}
}

// TestReassemblingADiaryMemoryKeepsEveryChunk pins the other side: the diary is
// the one writer that does NOT overlap, so stripping there would delete real
// prose rather than a duplicate.
func TestReassemblingADiaryMemoryKeepsEveryChunk(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "SESSION line %d recording what happened and why it mattered. ", i)
	}
	entry := b.String()

	if _, err := svc.WriteDiary(ctx, "team-diary", DiaryWriteInput{
		Agent: "claude-code-aks", Wing: "wing_alpha", Entry: entry,
	}); err != nil {
		t.Fatalf("write diary: %v", err)
	}

	var rows []drawerRow
	if err := svc.repo.db.WithContext(ctx).
		Where("team_id = ? AND room = ?", "team-diary", DiaryRoom).
		Order("chunk_index ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load diary chunks: %v", err)
	}
	stored := make([]Drawer, 0, len(rows))
	for _, r := range rows {
		stored = append(stored, fromRow(r))
	}
	if len(stored) < 2 {
		t.Fatalf("diary entry stored as %d chunk(s); the fixture needs a boundary", len(stored))
	}
	if !storedWithoutOverlap(stored[1]) {
		t.Fatalf("a diary chunk (agent=%q source=%q) is not recognised as overlap-free",
			stored[1].Agent, stored[1].SourceFile)
	}

	got := reassembleMemory(stored)
	for i := 0; i < 200; i++ {
		if !strings.Contains(got, fmt.Sprintf("SESSION line %d ", i)) {
			t.Fatalf("line %d is missing from the reassembled diary entry: overlap stripping deleted real prose", i)
		}
	}
}
