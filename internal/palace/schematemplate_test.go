package palace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"

	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// schemaTemplate is a fully migrated, empty database, built ONCE for the whole
// package and copied per test.
//
// It exists because migrating per test was almost the entire cost of this
// package. Measured 2026-08-30 under -race: one newTestService costs 297ms, of
// which the 35-file goose chain is nearly all of it; there are 263 fixture
// constructions here, and the package takes 118.6s with a mean test time of
// 287ms — indistinguishable from the fixture cost, because for most tests the
// fixture IS the test's runtime. Copying a ~200KB file instead costs
// microseconds, so the chain runs once rather than 263 times.
//
// -race is what makes it worth doing: the same fixture is 23ms uninstrumented
// and 297ms instrumented, a 13x multiplier that lands on migration code rather
// than on anything the tests assert. CI's race job was 384s of a 565s build.
//
// Isolation is UNCHANGED, which is the property that made this safe: every test
// still gets its own file in its own t.TempDir(), so nothing is shared between
// tests at runtime. Only the schema's CONSTRUCTION is shared, and it is copied
// rather than reused.
//
// A second reason to prefer this to the obvious alternative: goose.SetBaseFS
// and goose.SetDialect are package-level GLOBALS in goose, and calling them
// per fixture is what stops this package from adopting t.Parallel() — the race
// detector would fail on goose's own state rather than on ours. Building the
// template once removes those calls from the per-test path entirely.
var schemaTemplate string

// TestMain builds the schema template before any test runs and removes it after.
//
// A package-level sync.Once would also work, but TestMain makes the lifetime
// explicit and gives the temporary directory somewhere to be cleaned up — an
// os.MkdirTemp with no owner is a leak that only shows up on a developer's
// machine weeks later.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "palace-schema-template")
	if err != nil {
		fmt.Fprintf(os.Stderr, "build schema template: %v\n", err)
		os.Exit(1)
	}
	schemaTemplate = filepath.Join(dir, "template.db")
	if err := buildSchemaTemplate(schemaTemplate); err != nil {
		fmt.Fprintf(os.Stderr, "build schema template: %v\n", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// buildSchemaTemplate runs the real migration chain into path and closes it, so
// the file on disk is a complete, checkpointed database with no sidecars left to
// copy alongside it.
func buildSchemaTemplate(path string) error {
	gdb, err := gorm.Open(glebarez.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("sql handle: %w", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("dialect: %w", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return sqlDB.Close()
}

// newMigratedDB returns a private, fully migrated database for one test, copied
// from the package template rather than migrated from scratch.
//
// The copy is the isolation boundary: the returned handle owns its own file
// under the test's own t.TempDir(), so a test may write whatever it likes.
func newMigratedDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	schema, err := os.ReadFile(schemaTemplate)
	if err != nil {
		t.Fatalf("read schema template: %v", err)
	}
	if err := os.WriteFile(path, schema, 0o600); err != nil {
		t.Fatalf("write test db: %v", err)
	}
	gdb, err := gorm.Open(glebarez.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return gdb
}
