package sqlitevec

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestStore opens a throwaway file-backed SQLite DB and runs the real goose
// migrations against it, so the test exercises the actual "vectors" schema
// (00005) — not a hand-rolled CREATE TABLE that could drift from production.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vectors_test.db")
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
	return New(gdb)
}

func TestUpsertSearchRanking(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const ns = "team1"

	points := []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}, Payload: map[string]any{"label": "x-axis"}},
		{ID: "b", Vector: []float32{0, 1, 0}},
		{ID: "c", Vector: []float32{0.9, 0.1, 0}}, // close to a
	}
	if err := s.Upsert(ctx, ns, points); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	hits, err := s.Search(ctx, ns, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	if hits[0].ID != "a" || hits[1].ID != "c" {
		t.Fatalf("want ranking [a c], got [%s %s]", hits[0].ID, hits[1].ID)
	}
	if hits[0].Score < hits[1].Score {
		t.Fatalf("expected closest-first ordering, got %.4f then %.4f", hits[0].Score, hits[1].Score)
	}
	// Payload must round-trip verbatim.
	if got := hits[0].Payload["label"]; got != "x-axis" {
		t.Fatalf("payload not round-tripped: got %v", got)
	}
}

func TestUpsertReplacesByID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const ns = "team1"

	if err := s.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Same ID, different vector — must replace, not duplicate.
	if err := s.Upsert(ctx, ns, []store.Point{{ID: "a", Vector: []float32{0, 0, 1}}}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	all, err := s.AllPoints(ctx, ns)
	if err != nil {
		t.Fatalf("all points: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 point after replace, got %d", len(all))
	}
	hits, err := s.Search(ctx, ns, []float32{0, 0, 1}, 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Score < 0.99 {
		t.Fatalf("replacement vector not searchable: %+v", hits)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const ns = "team1"

	if err := s.Upsert(ctx, ns, []store.Point{
		{ID: "a", Vector: []float32{1, 0, 0}},
		{ID: "b", Vector: []float32{0, 1, 0}},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.Delete(ctx, ns, []string{"a", "missing"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, err := s.AllPoints(ctx, ns)
	if err != nil {
		t.Fatalf("all points: %v", err)
	}
	if len(all) != 1 || all[0].ID != "b" {
		t.Fatalf("want only [b] left, got %+v", all)
	}
}

func TestNamespaceIsolation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, "team1", []store.Point{{ID: "a", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatalf("upsert team1: %v", err)
	}
	if err := s.Upsert(ctx, "team2", []store.Point{{ID: "a", Vector: []float32{0, 1, 0}}}); err != nil {
		t.Fatalf("upsert team2: %v", err)
	}

	one, err := s.AllPoints(ctx, "team1")
	if err != nil {
		t.Fatalf("all team1: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("team1 should hold exactly its own point, got %d", len(one))
	}
	// Same ID in team2 must be a distinct row, not an overwrite of team1's.
	hits, err := s.Search(ctx, "team1", []float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatalf("search team1: %v", err)
	}
	if len(hits) != 1 || hits[0].Score < 0.99 {
		t.Fatalf("team1 vector leaked or got overwritten: %+v", hits)
	}
}
