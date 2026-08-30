package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestPlaybookIsRegistered covers the rung this command's own tests cannot see.
//
// ⚠ EVERY OTHER TEST IN THIS FILE BUILDS ITS OWN COMMAND, so all of them stay
// green with `playbookCommand(def),` deleted from rootCommand — the capability
// would be finished, tested, and unreachable, which is this repository's
// signature defect. This one asserts against the REAL root, the same one main()
// runs, so removing the registration turns it red.
func TestPlaybookIsRegistered(t *testing.T) {
	root := rootCommand(config.Default())
	var names []string
	for _, c := range root.Commands {
		names = append(names, c.Name)
	}
	for _, n := range names {
		if n == "playbook" {
			return
		}
	}
	t.Errorf("the CLI registers %v and not \"playbook\" — the command exists and cannot be run", names)
}

// TestPlaybookHelpNamesForceAsWhatLiftsTheRefusal pins the greppable half of the
// guard. A refusal an operator cannot act on is a wall, so the text that explains
// how to proceed has to name the flag by name — not "override it" or "use the
// flag", which a reader has to guess at and a grep cannot find.
func TestPlaybookHelpNamesForceAsWhatLiftsTheRefusal(t *testing.T) {
	cmd := playbookCommand(config.Default())
	if !strings.Contains(cmd.Description, "--force") {
		t.Error("the description never names --force, so the refusal does not say what lifts it")
	}
	if !strings.Contains(cmd.Description, "--reseed") {
		t.Error("the description never names --reseed, so the read-only default does not say what writes")
	}
	var reseed, force bool
	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			switch n {
			case "reseed":
				reseed = true
			case "force":
				force = true
			}
		}
	}
	if !reseed || !force {
		t.Errorf("playbook declares reseed=%v force=%v; both are required for the guard to be expressible", reseed, force)
	}
}

// TestPlaybookRefusesToOverwriteAnAuthoredRow is the guard itself, driven through
// runPlaybook's own decision rather than a copy of it.
//
// ⚠ THE FIXTURE MATTERS: the row must be authored (a non-empty updated_by, which
// is what the dashboard editor writes) AND its content must differ from the
// binary's. A fixture whose content already matched would pass whether or not the
// guard exists, because there would be nothing to overwrite.
func TestPlaybookRefusesToOverwriteAnAuthoredRow(t *testing.T) {
	ctx := context.Background()
	repo := skillset.NewRepo(newTestGormDB(t))
	if _, err := repo.Set(ctx, "a human wrote this", "someone@example.com"); err != nil {
		t.Fatalf("seed an authored row: %v", err)
	}

	err := reseedInto(ctx, repo, false)
	if err == nil {
		t.Fatal("reseed overwrote a human-authored playbook without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("the refusal does not name the flag that would proceed: %v", err)
	}

	// And the row is untouched — a refusal that had already written would be
	// worse than no guard, because the error would misdescribe what happened.
	after, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Content != "a human wrote this" {
		t.Errorf("the refused reseed changed the row anyway: %.40q", after.Content)
	}
}

// TestPlaybookForceOverwritesAndLeavesItReseedable pins the other half, including
// the provenance stamp.
//
// ⚠ THE STAMP IS THE POINT. A reseed writes an EMPTY updated_by, the same value
// the server's own seed leaves, so the next reseed does not refuse for a reason
// this one created. Stamping any marker of our own would make every reseeded row
// read as authored and turn --force into a permanent requirement.
func TestPlaybookForceOverwritesAndLeavesItReseedable(t *testing.T) {
	ctx := context.Background()
	repo := skillset.NewRepo(newTestGormDB(t))
	if _, err := repo.Set(ctx, "a human wrote this", "someone@example.com"); err != nil {
		t.Fatalf("seed an authored row: %v", err)
	}

	if err := reseedInto(ctx, repo, true); err != nil {
		t.Fatalf("reseed --force: %v", err)
	}
	after, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Content != skillset.DefaultPlaybook {
		t.Error("the row does not carry this binary's playbook after a forced reseed")
	}
	if after.UpdatedBy != "" {
		t.Errorf("a reseed stamped updated_by=%q; it must leave the seeded marker, or the next reseed refuses", after.UpdatedBy)
	}
	if after.Version != 2 {
		t.Errorf("version %d after one overwrite of a v1 row, want 2 — Repo.Set owns the bump", after.Version)
	}

	// Now reseedable again with no --force, which is the property the empty
	// stamp buys.
	if err := reseedInto(ctx, repo, false); err != nil {
		t.Errorf("a second reseed refused, so the first one made the row look authored: %v", err)
	}
}

// newTestGormDB opens a migrated, empty palace in a temp dir. It runs the real
// migrations rather than AutoMigrate so the skillset table under test is the one
// db/migrations declares, column for column.
func newTestGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "playbook.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetLogger(goose.NopLogger())
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return gdb
}

// TestUnforcedReseedIsACompareAndSwap pins the promise against a concurrent edit.
//
// ⚠ CHECK-THEN-SET IS NOT THE SAME PROMISE, and a review found the window. The
// first version read UpdatedBy, decided, then called Set — which rereads and
// saves unconditionally. A dashboard edit landing between those two steps was
// silently overwritten by the command whose entire contract is that it will not
// do that. The guard is the write now, so the database decides.
func TestUnforcedReseedIsACompareAndSwap(t *testing.T) {
	ctx := context.Background()
	repo := skillset.NewRepo(newTestGormDB(t))
	if _, err := repo.Set(ctx, "seeded text", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A seeded row reseeds.
	ok, err := repo.SetIfSeeded(ctx, skillset.DefaultPlaybook)
	if err != nil {
		t.Fatalf("SetIfSeeded on a seeded row: %v", err)
	}
	if !ok {
		t.Fatal("SetIfSeeded refused a row that was still seeded")
	}

	// An authored row does NOT, and the refusal is the row count rather than a
	// prior read — which is what closes the window.
	if _, err := repo.Set(ctx, "a human wrote this", "someone@example.com"); err != nil {
		t.Fatalf("author: %v", err)
	}
	ok, err = repo.SetIfSeeded(ctx, skillset.DefaultPlaybook)
	if err != nil {
		t.Fatalf("SetIfSeeded on an authored row: %v", err)
	}
	if ok {
		t.Error("SetIfSeeded overwrote a human-authored playbook")
	}
	after, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Content != "a human wrote this" {
		t.Errorf("the authored playbook was replaced anyway: %.40q", after.Content)
	}
}
