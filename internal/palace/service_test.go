package palace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"

	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"time"
)

// fakeEmbedder turns text into a deterministic bag-of-bytes histogram vector:
// identical text yields an identical vector (cosine 1), and the more two strings
// share, the closer they sit. That is enough to assert recall ordering without a
// live Ollama, and it keeps the test hermetic.
type fakeEmbedder struct{}

const fakeDim = 32

func (fakeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, s := range inputs {
		v := make([]float32, fakeDim)
		for _, b := range []byte(s) {
			v[int(b)%fakeDim]++
		}
		out[i] = v
	}
	return out, nil
}

func (f fakeEmbedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	v, err := f.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

// newTestService builds a Service over a throwaway migrated SQLite DB (so the
// real 00006 schema is exercised) using the SQLite store as both source of truth
// and search index, plus the fake embedder.
func newTestService(t *testing.T) *Service {
	t.Helper()
	return newTestServiceWith(t, fakeEmbedder{})
}

// newTestServiceWith is newTestService with the embedder chosen by the caller, so
// a test can observe what the service asks the embedder to do rather than only
// what it returns.
func newTestServiceWith(t *testing.T, embedder Embedder) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "palace_test.db")
	gdb, err := gorm.Open(glebarez.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewService(NewRepo(gdb), embedder, sqlitevec.New(gdb), fakeDim)
}

// fakeReranker is a cross-encoder stand-in: it ranks by how many query words a
// document literally contains, which is enough to reorder a page deterministically
// without a model. err makes every call fail, for the degradation test.
type fakeReranker struct {
	err    error
	called int
	budget time.Duration
}

// RerankBudget satisfies RerankDescriber so a fixture can put a budget on the
// span. A zero budget means "states none" and emits nothing, matching a
// reranker that enforces no ceiling.
func (f *fakeReranker) RerankBudget() time.Duration { return f.budget }

func (f *fakeReranker) Rerank(_ context.Context, query string, docs []string) ([]float64, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}
	scores := make([]float64, len(docs))
	for i, d := range docs {
		for _, term := range strings.Fields(strings.ToLower(query)) {
			if strings.Contains(strings.ToLower(d), term) {
				scores[i]++
			}
		}
	}
	return scores, nil
}

// TestSearchRerankerPromotesFromOutsideThePage is the point of a cross-encoder:
// it must be able to pull a drawer the hybrid ranking put below the page INTO it.
// Reranking after paging would make that impossible, so this pins the ordering of
// the two steps, not just the presence of the reranker.
func TestSearchRerankerPromotesFromOutsideThePage(t *testing.T) {
	ctx := context.Background()
	rr := &fakeReranker{}
	svc := newTestService(t).WithReranker(rr, 10)
	const team = "team-rerank"

	// The fake embedder maps bytes to dimensions, so these are near-identical
	// vectors: retrieval surfaces all of them and the fused order is essentially
	// arbitrary — which is exactly when the cross-encoder should decide.
	for _, content := range []string{
		"aaa bbb ccc filler one",
		"aaa bbb ccc filler two",
		"aaa bbb ccc filler three",
		"the installer pins CLAUDE_CONFIG_DIR and the registration lands in an unread file",
	} {
		if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: content}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "installer pins claude_config_dir", Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if rr.called != 1 {
		t.Fatalf("reranker called %d times, want 1", rr.called)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Drawer.Content, "CLAUDE_CONFIG_DIR") {
		t.Fatalf("cross-encoder did not decide the page: %+v", hits)
	}
	if hits[0].RerankScore == 0 {
		t.Error("RerankScore not reported on the hit")
	}
}

// TestSearchSurvivesRerankerFailure: the cross-encoder is a refinement, so a
// server that is down must cost ranking quality and nothing else. Recall is the
// product; it cannot depend on an optional service.
func TestSearchSurvivesRerankerFailure(t *testing.T) {
	ctx := context.Background()
	rr := &fakeReranker{err: errors.New("connection refused")}
	svc := newTestService(t).WithReranker(rr, 10)
	const team = "team-rerank-down"

	for _, content := range []string{"alpha memory", "beta memory", "gamma memory"} {
		if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: content}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "beta memory", Limit: 3})
	if err != nil {
		t.Fatalf("search must not fail when the reranker is down: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("want the full hybrid page, got %d hit(s)", len(hits))
	}
	// The lexical half of the hybrid ranking still works, so the literal match
	// leads — proof the results are the fused ordering rather than an empty or
	// arbitrary one.
	if !strings.Contains(hits[0].Drawer.Content, "beta") {
		t.Errorf("hybrid order lost: %q leads", hits[0].Drawer.Content)
	}
	for _, h := range hits {
		if h.RerankScore != 0 {
			t.Errorf("failed rerank must leave RerankScore unset, got %v", h.RerankScore)
		}
	}
}

func TestServiceAddAndSearch(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.Add(ctx, team, AddInput{Wing: "proj", Room: "backend", Content: "the cache uses an LRU eviction policy"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.Add(ctx, team, AddInput{Wing: "proj", Room: "frontend", Content: "the button turns blue on hover"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "the cache uses an LRU eviction policy"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	if hits[0].Drawer.Content != "the cache uses an LRU eviction policy" {
		t.Fatalf("top hit is not the exact match: %q (score %.3f)", hits[0].Drawer.Content, hits[0].Score)
	}
	if hits[0].Distance < 0 || hits[0].Distance > 2 {
		t.Fatalf("distance out of [0,2]: %f", hits[0].Distance)
	}
}

func TestServiceSearchWingFilter(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "alpha", Room: "r", Content: "shared phrase here alpha"})
	mustAdd(t, svc, team, AddInput{Wing: "beta", Room: "r", Content: "shared phrase here beta"})

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "shared phrase here", Wing: "beta"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, h := range hits {
		if h.Drawer.Wing != "beta" {
			t.Fatalf("wing filter leaked: got wing %q", h.Drawer.Wing)
		}
	}
}

func TestServiceGetUpdateDelete(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	created := mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "original text"})
	id := created[0].ID

	got, err := svc.Get(ctx, team, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != "original text" {
		t.Fatalf("get returned %q", got.Content)
	}

	newContent := "rewritten text"
	up, err := svc.Update(ctx, team, id, DrawerPatch{Content: &newContent, Reason: "the first draft was wrong"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = svc.Get(ctx, team, up.Drawer.ID)
	if got.Content != newContent {
		t.Fatalf("update did not persist: %q", got.Content)
	}
	id = up.Drawer.ID

	if _, err := svc.Delete(ctx, team, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, team, id); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestServiceGetUnknownIsNotFound(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Get(context.Background(), "team-1", "nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestServiceAggregations(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "proj", Room: "backend", Content: "alpha"})
	mustAdd(t, svc, team, AddInput{Wing: "proj", Room: "frontend", Content: "beta"})
	mustAdd(t, svc, team, AddInput{Wing: "notes", Room: "ideas", Content: "gamma"})

	wings, err := svc.Wings(ctx, team)
	if err != nil {
		t.Fatalf("wings: %v", err)
	}
	got := map[string]WingStat{}
	for _, w := range wings {
		got[w.Wing] = w
	}
	if got["proj"].Drawers != 2 || got["proj"].Rooms != 2 {
		t.Fatalf("proj wing stats wrong: %+v", got["proj"])
	}
	if got["notes"].Drawers != 1 || got["notes"].Rooms != 1 {
		t.Fatalf("notes wing stats wrong: %+v", got["notes"])
	}

	tax, err := svc.GetTaxonomy(ctx, team)
	if err != nil {
		t.Fatalf("taxonomy: %v", err)
	}
	if len(tax.Wings) != 2 {
		t.Fatalf("want 2 wings in taxonomy, got %d", len(tax.Wings))
	}
}

func TestServiceCheckDuplicate(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a uniquely worded memory about otters"})

	dup, err := svc.CheckDuplicate(ctx, team, "a uniquely worded memory about otters", DefaultDupThreshold)
	if err != nil {
		t.Fatalf("check duplicate: %v", err)
	}
	if !dup.IsDuplicate || dup.Drawer == nil {
		t.Fatalf("identical content should be a duplicate: %+v", dup)
	}

	none, err := svc.CheckDuplicate(ctx, team, "completely different subject matter zzz", DefaultDupThreshold)
	if err != nil {
		t.Fatalf("check duplicate: %v", err)
	}
	if none.IsDuplicate {
		t.Fatalf("unrelated content flagged as duplicate (sim %.3f)", none.Similarity)
	}
}

func TestServiceAddNoSourceKeepsDistinctMemories(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// Two different memories, same wing/room, no source_file: both must survive
	// (the content-hashed id prevents the second from overwriting the first).
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "first memory about cats"})
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "second memory about dogs"})

	list, err := svc.List(ctx, team, "w", "r", 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 distinct drawers, got %d (collision overwrote one)", len(list))
	}
}

func TestServiceReAddNamedSourcePurgesStaleChunks(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	long := strings.Repeat("alpha ", 400)    // ~2400 chars -> several chunks
	short := "now just a single short chunk" // 1 chunk

	first := mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", SourceFile: "notes.md", Content: long})
	if len(first) < 2 {
		t.Fatalf("expected the long content to chunk into >1 drawer, got %d", len(first))
	}
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", SourceFile: "notes.md", Content: short})

	// CURRENT rows: a re-file now ENDS the chunks it dropped rather than deleting
	// them (ADR-038 T3), and List does not filter by validity until T5.
	list, err := svc.repo.CurrentDrawers(ctx, team, "w")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("re-adding a shorter source should leave one CURRENT drawer; got %d", len(list))
	}
}

func TestServiceUpdateRejectsEmptyField(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"
	created := mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "keep me addressable"})

	empty := ""
	if _, err := svc.Update(ctx, team, created[0].ID, DrawerPatch{Wing: &empty}); err == nil {
		t.Fatal("expected an error updating wing to empty")
	}
}

func TestServiceCheckDuplicateClampsThreshold(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "an exact phrase to match"})

	// threshold > 1 is nonsense; clamped to 1, an exact match (sim 1.0) still counts.
	dup, err := svc.CheckDuplicate(ctx, team, "an exact phrase to match", 2.0)
	if err != nil {
		t.Fatalf("check duplicate: %v", err)
	}
	if !dup.IsDuplicate {
		t.Fatalf("threshold>1 should clamp so an exact duplicate still matches (sim %.3f)", dup.Similarity)
	}
}

func TestServiceAddValidates(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Add(context.Background(), "team-1", AddInput{Wing: "", Room: "r", Content: "x"}); err == nil {
		t.Fatal("expected validation error for empty wing")
	}
}

// brokenEmbedder stands in for an Ollama that is not running — the single most
// common self-hosted failure, and the one that used to lose memories.
type brokenEmbedder struct{ fakeEmbedder }

func (brokenEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("dial tcp 127.0.0.1:11434: connect: connection refused")
}

func (b brokenEmbedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	return nil, errors.New("dial tcp 127.0.0.1:11434: connect: connection refused")
}

// TestFilingSurvivesAnEmbedderOutage is the whole point of the deferred path: a
// memory written while the embedder is down must still EXIST. Losing the text
// because the index could not be built is the worst trade this system can make —
// the text is the memory, the vector is only how it is found.
func TestFilingSurvivesAnEmbedderOutage(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	svc.embed = brokenEmbedder{}
	const team = "team-outage"

	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "the embedder was down when this was written"})
	if err != nil {
		t.Fatalf("add must not fail when the embedder is down: %v", err)
	}
	if !res.PendingEmbedding {
		t.Error("PendingEmbedding not reported; the caller cannot tell the memory is unsearchable")
	}
	if len(res.Drawers) != 1 {
		t.Fatalf("want 1 drawer, got %d", len(res.Drawers))
	}

	diary, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "tester", Entry: "journal written during the outage"})
	if err != nil {
		t.Fatalf("diary_write must not fail when the embedder is down: %v", err)
	}
	if !diary.PendingEmbedding {
		t.Error("diary PendingEmbedding not reported")
	}

	// The rows are durable and queued — which is exactly the state the background
	// worker drains.
	pending, err := svc.PendingCount(ctx, team)
	if err != nil {
		t.Fatalf("pending count: %v", err)
	}
	if pending != 2 {
		t.Fatalf("want 2 rows awaiting embedding, got %d", pending)
	}
	stored, err := svc.Get(ctx, team, res.Drawers[0].ID)
	if err != nil {
		t.Fatalf("the drawer must be readable back immediately: %v", err)
	}
	if stored.Content != "the embedder was down when this was written" {
		t.Errorf("content not stored verbatim: %q", stored.Content)
	}

	// Embedder returns: the queue drains and the memories become searchable, with
	// no re-filing by anyone.
	svc.embed = fakeEmbedder{}
	n, err := svc.EmbedPendingForTeam(ctx, team, 10)
	if err != nil {
		t.Fatalf("drain queue: %v", err)
	}
	if n != 2 {
		t.Fatalf("worker embedded %d rows, want 2", n)
	}
	if pending, err = svc.PendingCount(ctx, team); err != nil || pending != 0 {
		t.Fatalf("queue not drained: %d (err %v)", pending, err)
	}
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "journal written during the outage", Limit: 5})
	if err != nil {
		t.Fatalf("search after recovery: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("memories written during the outage never became searchable")
	}
}

func mustAdd(t *testing.T, svc *Service, team string, in AddInput) []Drawer {
	t.Helper()
	res, err := svc.Add(context.Background(), team, in)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.PendingEmbedding {
		t.Fatalf("add deferred embedding unexpectedly (is the fake embedder failing?)")
	}
	return res.Drawers
}

// TestRerankBlendsRatherThanOverwrites pins a measured regression. Letting the
// cross-encoder's score decide alone throws away the lexical evidence in the
// fused score — on this palace's eval that was MRR 1.000 → 0.686 on the queries
// that carry an identifier, which is what a developer actually types.
func TestRerankBlendsRatherThanOverwrites(t *testing.T) {
	svc := newTestService(t)

	// Fused ranking is confident about A (an exact lexical match); the
	// cross-encoder mildly prefers B. A blend keeps A; a handover does not.
	survivors := []SearchHit{
		{Drawer: Drawer{ID: "A", Content: "exact identifier match"}},
		{Drawer: Drawer{ID: "B", Content: "topically similar"}},
	}
	ranked := []HybridScore{{Index: 0, Fused: 1.0}, {Index: 1, Fused: 0.2}}
	svc.rerank = &staticReranker{scores: []float64{1, 2}} // B scored higher
	svc.rerankPool = 2

	blended, _, _ := svc.applyRerankWith(context.Background(), "q", "q", nil, survivors, ranked, DefaultRerankWeight)
	if survivors[blended[0].Index].Drawer.ID != "A" {
		t.Errorf("a mild cross-encoder preference overturned a confident fused score at w=%.2f", DefaultRerankWeight)
	}

	// w=1 is the handover, kept reachable so the eval can measure what it costs.
	if over, _, _ := svc.applyRerankWith(context.Background(), "q", "q", nil, survivors, ranked, 1); survivors[over[0].Index].Drawer.ID != "B" {
		t.Error("w=1 must hand the decision to the cross-encoder")
	}
	// w=0 does not consult it at all.
	if none, _, _ := svc.applyRerankWith(context.Background(), "q", "q", nil, survivors, ranked, 0); survivors[none[0].Index].Drawer.ID != "A" {
		t.Error("w=0 must leave the hybrid order alone")
	}
}

// TestRerankKeepsTheWholePage: a partial or failed response costs precision, not
// results.
func TestRerankKeepsTheWholePage(t *testing.T) {
	svc := newTestService(t)
	survivors := []SearchHit{
		{Drawer: Drawer{ID: "A"}}, {Drawer: Drawer{ID: "B"}}, {Drawer: Drawer{ID: "C"}},
	}
	ranked := []HybridScore{{Index: 0, Fused: 0.9}, {Index: 1, Fused: 0.5}, {Index: 2, Fused: 0.1}}

	// Wrong count: upstream's guard rejects it and the hybrid order stands.
	svc.rerank = &staticReranker{scores: []float64{5}}
	svc.rerankPool = 3
	if got, _, _ := svc.applyRerankWith(context.Background(), "q", "q", nil, survivors, ranked, DefaultRerankWeight); len(got) != 3 {
		t.Fatalf("page shrank to %d", len(got))
	}
}

// staticReranker returns a fixed ordering, so blending is testable without a
// model.
type staticReranker struct{ scores []float64 }

func (s *staticReranker) Rerank(context.Context, string, []string) ([]float64, error) {
	return s.scores, nil
}

// TestWithFusionRRFChangesOrder pins that FUSION=rrf actually reaches the search
// path: the mechanism it exists for is bounding one bad signal's influence, so
// a candidate that a lexical score would sink must survive on its vector rank.
func TestWithFusionRRFChangesOrder(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-fusion"

	// The closest vector match shares no vocabulary with the query; the lexical
	// winner is a farther, wordier note. Linear fusion lets the lexical score
	// pull the second one up; RRF caps that to one rank position.
	mustAdd(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "a.md",
		Content: "cache eviction policy"})
	mustAdd(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "b.md",
		Content: "cache eviction policy cache eviction policy cache eviction unrelated tail"})

	order := func(s *Service) []string {
		hits, err := s.Search(ctx, team, SearchQuery{Query: "cache eviction policy", Wing: "wing_acme", Limit: 5, SkipTelemetry: true})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		out := make([]string, len(hits))
		for i, h := range hits {
			out[i] = h.Drawer.SourceFile
		}
		return out
	}
	linear := order(svc)
	rrf := order(svc.WithFusion("rrf"))
	if len(linear) != 2 || len(rrf) != 2 {
		t.Fatalf("expected both drawers on the page, got linear=%v rrf=%v", linear, rrf)
	}
	// The assertion that matters is reachability: the switch must be observable
	// from Search, not merely stored on the struct.
	if !svc.fusionRRF {
		t.Fatal("WithFusion(\"rrf\") did not set the fusion mode")
	}
	if svc.WithFusion("linear").fusionRRF {
		t.Fatal("WithFusion(\"linear\") must turn rank fusion off")
	}
}

// TestLexicalIDFChangesWhatSearchReturns pins reachability the only way that
// works: by requiring the two modes to produce DIFFERENT scores through Search.
//
// The previous version of this test asserted that both modes returned a result,
// which passed while the flag was read by nothing at all — Search had no IDF
// branch, so BM25_WEIGHT=auto-idf set a field and changed no behaviour. A test
// that cannot fail when the feature is absent is not a test of the feature.
//
// The fixture is built so the two coverage measures must disagree: "deploy"
// appears in nearly every candidate, which the binary count reads as full
// lexical signal and the IDF weighting reads as almost none.
func TestLexicalIDFChangesWhatSearchReturns(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-idf"

	for i, content := range []string{
		"deploy the batching service to the cluster tonight",
		"deploy notes for the batching rollout and its owner",
		"deploy checklist for the batching gateway and alarms",
		"deploy runbook covering the batching queue drain",
	} {
		mustAdd(t, svc, team, AddInput{
			Wing: "wing_acme", Room: "decisions",
			SourceFile: fmt.Sprintf("note-%d.md", i),
			Content:    content,
		})
	}

	scores := func(idf bool) []float64 {
		hits, err := svc.WithLexicalIDF(idf).Search(ctx, team, SearchQuery{
			Query: "deploy batching", Wing: "wing_acme", Limit: 10, SkipTelemetry: true,
		})
		if err != nil {
			t.Fatalf("search (idf=%v): %v", idf, err)
		}
		if len(hits) < 2 {
			t.Fatalf("search (idf=%v) returned %d hits, need several to compare", idf, len(hits))
		}
		out := make([]float64, len(hits))
		for i, h := range hits {
			out[i] = h.Score
		}
		return out
	}

	binary, idf := scores(false), scores(true)
	same := len(binary) == len(idf)
	if same {
		for i := range binary {
			if binary[i] != idf[i] {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatalf("auto-idf changed nothing through Search: binary=%v idf=%v — the flag is set but the search path does not read it", binary, idf)
	}

	// And the default must stay binary: a ranking default changes what every
	// existing palace returns.
	if newTestService(t).bm25IDF {
		t.Error("the binary count must remain the default")
	}
}

// TestRerankPresenceSurvivesAZeroScore pins the distinction a value cannot
// carry. TEI is asked for sigmoid scores in (0,1); llama.cpp's server returns
// bare logits, where 0.0 is an ordinary result — so "did a cross-encoder score
// this?" cannot be answered by comparing the score against zero. The abstention
// gate's calibration data depends on getting this right: a case wrongly read as
// unscored silently leaves the distribution.
func TestRerankPresenceSurvivesAZeroScore(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-zero"
	mustAdd(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "a.md",
		Content: "the queue drains every five minutes"})

	// A reranker that scores everything exactly 0.0 — legal for a logit backend.
	svc = svc.WithReranker(&staticReranker{scores: []float64{0}}, 10).WithRerankWeight(0.5)
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "queue drain", Wing: "wing_acme", Limit: 5, SkipTelemetry: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("search returned nothing")
	}
	if hits[0].RerankScore != 0 {
		t.Fatalf("fixture expects a zero score, got %v", hits[0].RerankScore)
	}
	if !hits[0].Reranked {
		t.Fatal("a hit scored 0.0 by a logit backend must still report Reranked — otherwise the gate's data silently loses it")
	}
}

// TestUpdateRefusesToHalfRewriteAMultiChunkMemory pins the failure a live
// session hit: an update that reports success while half the memory keeps
// contradicting it.
//
// A memory over ChunkSize is stored as several rows sharing a parent. Update used
// to rewrite ONE row, so patching the parent's content left the children live,
// individually embedded, and still returning the retracted claim — ranked ABOVE
// the correction, with nothing marking them superseded. The call returned the
// updated drawer and reported success throughout.
//
// It was fixed by REFUSING a multi-chunk content edit and telling the caller to
// delete the memory and file it again by hand. ADR-038 T4 does that correctly and
// without the delete: a correction supersedes, so EVERY old chunk ends and one new
// set is written. What still refuses is a MOVE, and for a different reason — a
// move relocates one chunk and splits the memory across two scopes, so no single
// search returns all of it and the fragment does not say it is one.
func TestUpdateRefusesToHalfRewriteAMultiChunkMemory(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	long := strings.Repeat("The retention window is THIRTY days and this sentence forces chunking. ", 40)
	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "policy", Content: long})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(res.Drawers) < 2 {
		t.Fatalf("fixture: need a multi-chunk memory, got %d chunk(s)", len(res.Drawers))
	}
	parent := res.Drawers[0].ID

	corrected := "CORRECTED: the retention window is NINETY days."
	up, err := svc.Update(ctx, team, parent, DrawerPatch{
		Content: &corrected, Reason: "the window was changed to ninety days",
	})
	if err != nil {
		t.Fatalf("correcting a multi-chunk memory must be possible — refusing it was the old fix, "+
			"and it made correction impossible for exactly the long documents that most need it: %v", err)
	}

	// EVERY old chunk ends. The defect this test was written for is a chunk left
	// current with the retracted claim, and it does not matter whether that
	// happened by a partial write or a partial ending.
	after, err := svc.repo.MemoryChunks(ctx, team, parent)
	if err != nil {
		t.Fatalf("MemoryChunks: %v", err)
	}
	if len(after) != len(res.Drawers) {
		t.Errorf("the memory has %d chunk(s) after the correction, want %d — ending is not deleting",
			len(after), len(res.Drawers))
	}
	for _, c := range after {
		if c.ValidTo == "" {
			t.Errorf("chunk %d of the superseded memory is still current; it holds the old text, so "+
				"it still outranks the correction in search", c.ChunkIndex)
		}
		if c.SupersededBy != up.Drawer.ID {
			t.Errorf("chunk %d links to %q, want the successor %q", c.ChunkIndex, c.SupersededBy, up.Drawer.ID)
		}
		if strings.Contains(c.Content, "CORRECTED") {
			t.Error("a superseded chunk was rewritten in place; the old text is what survives")
		}
	}

	// A wing or room MOVE splits the same memory instead of contradicting it: one
	// chunk leaves and the rest stay. This release sharpens the consequence,
	// because recall now defaults to the registration's wing — after a split
	// neither wing returns the whole memory, and nothing marks what you get as a
	// fragment. Found by review, one field over from the reported defect.
	other := "other-wing"
	if _, err := svc.Update(ctx, team, parent, DrawerPatch{Wing: &other}); err == nil {
		t.Error("moving one chunk of a multi-chunk memory to another wing was accepted — the memory " +
			"is now split and no single scope returns all of it")
	}
	otherRoom := "other-room"
	if _, err := svc.Update(ctx, team, parent, DrawerPatch{Room: &otherRoom}); err == nil {
		t.Error("moving one chunk of a multi-chunk memory to another room was accepted")
	}
	if chunks, err := svc.repo.MemoryChunks(ctx, team, parent); err != nil {
		t.Fatalf("MemoryChunks: %v", err)
	} else {
		for _, c := range chunks {
			if c.Wing != "w" || c.Room != "r" {
				t.Errorf("a refused move still relocated chunk %d to %s/%s", c.ChunkIndex, c.Wing, c.Room)
			}
		}
	}

	// A single-chunk memory still updates, or the fix has broken the common case.
	one, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "short", Content: "a short memory"})
	if err != nil {
		t.Fatalf("add short: %v", err)
	}
	if _, err := svc.Update(ctx, team, one.Drawers[0].ID, DrawerPatch{
		Content: &corrected, Reason: "the window was changed to ninety days",
	}); err != nil {
		t.Errorf("a single-chunk memory must still be correctable: %v", err)
	}
}

// TestDeleteRemovesTheWholeMemory pins that a delete takes every chunk.
//
// It used to take one row. Deleting the parent of a multi-chunk memory left the
// children live — still embedded, still returned by search, and pointing at a
// parent that no longer existed. Same shape as the update defect one door over:
// an operation that treats one row as the whole memory and reports success.
//
// A delete has no reference ambiguity to weigh, unlike an update: the caller is
// removing the memory, so removing all of it is what they asked for.
func TestDeleteRemovesTheWholeMemory(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	long := strings.Repeat("The retention window is thirty days and this forces chunking. ", 40)
	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "p", Content: long})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(res.Drawers) < 2 {
		t.Fatalf("fixture: need a multi-chunk memory, got %d", len(res.Drawers))
	}

	n, err := svc.Delete(ctx, team, res.Drawers[0].ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != len(res.Drawers) {
		t.Errorf("delete reported %d chunk(s) removed, want %d — the count is what lets a caller "+
			"say how much went instead of echoing the one id it was handed", n, len(res.Drawers))
	}
	left, err := svc.List(ctx, team, "w", "r", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d chunk(s) survived the delete — orphaned, still embedded, still searchable, and "+
			"pointing at a parent that no longer exists", len(left))
	}

	// Deleting by a CHILD's id must take the memory too, or the same orphaning
	// happens from the other end.
	res2, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "p2", Content: long})
	if err != nil {
		t.Fatalf("add second: %v", err)
	}
	if _, err := svc.Delete(ctx, team, res2.Drawers[len(res2.Drawers)-1].ID); err != nil {
		t.Fatalf("delete by child: %v", err)
	}
	if left, err := svc.List(ctx, team, "w", "r", 100, 0); err != nil {
		t.Fatal(err)
	} else if len(left) != 0 {
		t.Errorf("deleting by a child's id left %d chunk(s)", len(left))
	}
}
