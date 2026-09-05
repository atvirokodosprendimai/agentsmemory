package store_test

import (
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/chromemvec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest"
	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// hybridPair builds the two REAL backends production pairs, rather than two
// fakes: Hybrid is the implementation every deployment actually holds, and it
// is the one whose behaviour is not its own — it forwards, and a forward to the
// wrong half is invisible to a test of either half alone.
func hybridPair(t *testing.T) store.VectorStore {
	dir := t.TempDir()
	gdb, err := gorm.Open(glebarez.Open(filepath.Join(dir, "sot.db")), &gorm.Config{
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
	// Closed because the handle is the TEST's, not the process's. POSIX unlinks
	// an open file so this leaks invisibly here; Windows refuses, and t.TempDir
	// registers RemoveAll at call time, so every test using the helper fails in
	// cleanup with its assertions passing (#162). Cleanup is LIFO, so this runs
	// before TempDir's own.
	t.Cleanup(func() { _ = sqlDB.Close() })

	idx, err := chromemvec.New(filepath.Join(dir, "chromem"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	return store.NewHybrid(sqlitevec.New(gdb), idx)
}

func TestHybridRunsTheConformanceSuite(t *testing.T) {
	storetest.RunPointsConformance(t, "hybrid", hybridPair)
}

// The same backend, the write half.
func TestHybridRunsTheSetPayloadConformanceSuite(t *testing.T) {
	storetest.RunCountConformance(t, "hybrid", hybridPair)
	storetest.RunSetPayloadConformance(t, "hybrid", func(t *testing.T) store.VectorStore {
		dir := t.TempDir()
		gdb, err := gorm.Open(glebarez.Open(filepath.Join(dir, "sot.db")), &gorm.Config{
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
		// Closed because the handle is the TEST's, not the process's. POSIX unlinks
		// an open file so this leaks invisibly here; Windows refuses, and t.TempDir
		// registers RemoveAll at call time, so every test using the helper fails in
		// cleanup with its assertions passing (#162). Cleanup is LIFO, so this runs
		// before TempDir's own.
		t.Cleanup(func() { _ = sqlDB.Close() })

		idx, err := chromemvec.New(filepath.Join(dir, "chromem"))
		if err != nil {
			t.Fatalf("open index: %v", err)
		}
		return store.NewHybrid(sqlitevec.New(gdb), idx)
	})
}
