package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	_ "github.com/glebarez/sqlite" // registers the cgo-free "sqlite" driver
	"github.com/pressly/goose/v3"
)

// TestMigrationsRoundTrip runs every migration Up, then all the way Down, then Up
// again against a real (modernc) sqlite file. It guards the whole schema — and
// especially newer statements like 00013's ADD COLUMN / partial index / DROP
// COLUMN — against a migration that applies but cannot be rolled back or re-run.
func TestMigrationsRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "roundtrip.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}

	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := goose.DownTo(sqlDB, "migrations", 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
