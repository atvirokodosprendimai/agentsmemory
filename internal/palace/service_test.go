package palace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"

	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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
	return NewService(NewRepo(gdb), fakeEmbedder{}, sqlitevec.New(gdb), fakeDim)
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
	if _, err := svc.Update(ctx, team, id, DrawerPatch{Content: &newContent}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = svc.Get(ctx, team, id)
	if got.Content != newContent {
		t.Fatalf("update did not persist: %q", got.Content)
	}

	if err := svc.Delete(ctx, team, id); err != nil {
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

	long := strings.Repeat("alpha ", 400)  // ~2400 chars -> several chunks
	short := "now just a single short chunk" // 1 chunk

	first := mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", SourceFile: "notes.md", Content: long})
	if len(first) < 2 {
		t.Fatalf("expected the long content to chunk into >1 drawer, got %d", len(first))
	}
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", SourceFile: "notes.md", Content: short})

	list, err := svc.List(ctx, team, "w", "r", 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("re-adding a shorter source should purge stale chunks; want 1 drawer, got %d", len(list))
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

func mustAdd(t *testing.T, svc *Service, team string, in AddInput) []Drawer {
	t.Helper()
	d, err := svc.Add(context.Background(), team, in)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return d
}
