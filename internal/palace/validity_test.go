package palace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"path/filepath"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
)

// ADR-038 T1. A drawer can be current or ended, ending never deletes, and every
// existing row reads as current with no backfill.

func TestAFreshDrawerIsCurrent(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-validity"

	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "hello"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	d, err := svc.Get(ctx, team, res.Drawers[0].ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if d.ValidTo != "" {
		t.Errorf("a freshly filed drawer has ValidTo=%q; empty means current, and there is no backfill", d.ValidTo)
	}
	rows, err := svc.repo.CurrentDrawers(ctx, team, "w")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("current() returned %d rows; want the one drawer just filed", len(rows))
	}
}

func TestEndSetsTheWindowAndKeepsTheRow(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-validity2"

	res, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "kafka until march"})
	id := res.Drawers[0].ID

	if err := svc.EndDrawer(ctx, team, id, "replaced by NATS after rebalancing stalled"); err != nil {
		t.Fatalf("end: %v", err)
	}
	d, err := svc.GetAnyVersion(ctx, team, id)
	if err != nil {
		t.Fatalf("the ended row must still be readable by id — ending is not deleting: %v", err)
	}
	if d.Content != "kafka until march" {
		t.Errorf("content changed on ending: %q", d.Content)
	}
	if d.ValidTo == "" || d.EndedAt == "" {
		t.Errorf("ValidTo=%q EndedAt=%q; both must be stamped", d.ValidTo, d.EndedAt)
	}
	if d.EndedReason != "replaced by NATS after rebalancing stalled" {
		t.Errorf("EndedReason=%q; the reason is the only thing worth keeping about an ending", d.EndedReason)
	}
	rows, _ := svc.repo.CurrentDrawers(ctx, team, "w")
	if len(rows) != 0 {
		t.Errorf("current() returned %d rows after the only drawer was ended", len(rows))
	}
}

func TestEndRefusesAnAlreadyEndedDrawer(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-validity3"

	res, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "x"})
	id := res.Drawers[0].ID
	if err := svc.EndDrawer(ctx, team, id, "first ending"); err != nil {
		t.Fatalf("first end: %v", err)
	}
	err := svc.EndDrawer(ctx, team, id, "second ending")
	if err == nil {
		t.Fatal("ending an already-ended drawer succeeded; the FIRST ending is the true one and a second would overwrite its reason")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v; want ErrInvalidInput", err)
	}
	d, _ := svc.GetAnyVersion(ctx, team, id)
	if d.EndedReason != "first ending" {
		t.Errorf("EndedReason=%q; the refused second ending must not have overwritten it", d.EndedReason)
	}
}

func TestEndRefusesAnEmptyReason(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-validity4"

	res, _ := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "x"})
	for _, reason := range []string{"", "   ", "\t\n"} {
		if err := svc.EndDrawer(ctx, team, res.Drawers[0].ID, reason); err == nil {
			t.Errorf("EndDrawer accepted reason %q; an ending with no why records THAT something ended and destroys the only thing worth keeping about it", reason)
		}
	}
}

// TestExistingRowsReadAsCurrentAfterMigration is the Stop Condition's
// anti-tautology guard: "every existing row reads as current with no backfill"
// is a claim about rows this test did not write through the post-migration code
// path, so writing them with Add would make it unfalsifiable.
//
// The task file asked for a copy of a real database behind an env var, failing
// rather than skipping when unset. That is unworkable as written — a test that
// fails when an env var is unset makes `go test ./...` permanently red for
// everyone — and skipping is the hole it was trying to close. So the guard is
// HERMETIC and always runs: migrate to the version BEFORE the validity window,
// insert rows with raw SQL exactly as the old schema held them, then apply the
// migration and read them back. Those rows were never touched by the new code.
// Deviation recorded in the task file rather than made silently.
func TestExistingRowsReadAsCurrentAfterMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "premigration.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlDB, _ := gdb.DB()
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	// Everything up to but NOT including the validity window.
	if err := goose.UpTo(sqlDB, "migrations", validityWindowMigrationVersion-1); err != nil {
		t.Fatalf("migrate to pre-window: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO drawers (team_id,id,wing,room,source_file,chunk_index,content,entities,parent_id,filed_at,content_date,agent,topic)
		 VALUES ('t','old-row-1','w','r','',0,'written before the window existed','','','2026-01-01T00:00:00Z','','','')`); err != nil {
		t.Fatalf("insert pre-migration row: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("apply the validity window: %v", err)
	}

	svc := NewService(NewRepo(gdb, gdb), fakeEmbedder{}, sqlitevec.New(gdb), fakeDim)
	d, err := svc.Get(ctx, "t", "old-row-1")
	if err != nil {
		t.Fatalf("a row written before the migration must still read: %v", err)
	}
	if d.ValidTo != "" {
		t.Errorf("a pre-existing row reads as ended (ValidTo=%q); empty-means-current is what makes the migration backfill-free", d.ValidTo)
	}
	if d.Content != "written before the window existed" {
		t.Errorf("the migration altered existing content: %q", d.Content)
	}
	rows, err := svc.repo.CurrentDrawers(ctx, "t", "w")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("current() returned %d rows; the pre-existing row must be among them", len(rows))
	}
	if strings.TrimSpace(d.EndedReason) != "" {
		t.Errorf("EndedReason=%q on a row nobody ended", d.EndedReason)
	}
}
