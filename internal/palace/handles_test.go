package palace

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"

	glebarez "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// readOnlyTwin opens a second handle on live's file that SQLite refuses writes
// on, so a Repo built as NewRepo(twin, live) fails loudly the moment a read
// method writes or a transaction runs on the reader.
//
// ADR-052 T5. The pragma is query_only(1) plus a busy timeout, and deliberately
// NOT the server's reader DSN: that one carries journal_mode(WAL), which a
// query_only connection cannot apply to a file the test fixture opened in
// rollback-journal mode. The twin is about the refusal, not the journal.
func readOnlyTwin(t *testing.T, live *gorm.DB) *gorm.DB {
	t.Helper()
	d, ok := live.Dialector.(*glebarez.Dialector)
	if !ok {
		t.Fatalf("live handle is %T, not the sqlite dialector this twin knows how to reopen", live.Dialector)
	}
	sep := "?"
	if strings.Contains(d.DSN, "?") {
		sep = "&"
	}
	twin, err := gorm.Open(glebarez.Open(d.DSN+sep+"_pragma=query_only(1)&_pragma=busy_timeout(5000)"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open read-only twin: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := twin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return twin
}

// mustRefuseWrites is the fixture proving itself: a "reader" that accepts a
// write makes every assertion built on it vacuous, so each test checks the
// refusal inside its own fence rather than trusting the helper's name.
func mustRefuseWrites(t *testing.T, reader *gorm.DB) {
	t.Helper()
	err := reader.Exec("CREATE TABLE adr052_probe (id INTEGER)").Error
	if err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("the reader accepted a write (err %v): the fixture is not strict, so nothing below could fail", err)
	}
}

// TestReadMethodsUseTheReadHandle proves the read methods run on the reader —
// they succeed against a query_only handle, and they stop answering when that
// handle is closed while the writer is untouched.
//
// ADR-052 T5. The first half catches a read method that writes (an upsert on
// a cache table, a lazily-created row): SQLite refuses it on the reader. The
// second half catches the opposite defect, a Repo that quietly reads through
// the writer while carrying a reader field nobody consults — the mutant that
// survives "every read succeeds", because reads succeed on either handle. A
// closed reader is the one observation that tells the two handles apart from
// outside the method.
func TestReadMethodsUseTheReadHandle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	live := newMigratedDB(t, "handles.db")
	reader := readOnlyTwin(t, live)
	mustRefuseWrites(t, reader)
	svc := NewService(NewRepo(reader, live), fakeEmbedder{}, sqlitevec.New(live), fakeDim)
	repo := svc.repo

	const team = "team-handles"
	added, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "reads go to the reader"})
	if err != nil {
		t.Fatalf("add through the writer: %v", err)
	}
	id := added.Drawers[0].ID

	reads := []struct {
		name string
		call func() error
	}{
		{"Repo.Get", func() error { _, err := repo.Get(ctx, team, id); return err }},
		{"Repo.GetMany", func() error { _, err := repo.GetMany(ctx, team, []string{id}); return err }},
		{"Repo.List", func() error { _, err := repo.List(ctx, team, "wing_acme", "", 10, 0); return err }},
		{"Repo.ListCurrent", func() error { _, err := repo.ListCurrent(ctx, team, "wing_acme", "", 10, 0); return err }},
		{"Repo.Wings", func() error { _, err := repo.Wings(ctx, team); return err }},
		{"Repo.Rooms", func() error { _, err := repo.Rooms(ctx, team, "wing_acme"); return err }},
		{"Repo.CountWing", func() error { _, err := repo.CountWing(ctx, team, "wing_acme"); return err }},
		{"Repo.WingIsEmpty", func() error { _, err := repo.WingIsEmpty(ctx, team, "wing_acme"); return err }},
		{"Repo.DrawerWings", func() error { _, _, err := repo.DrawerWings(ctx, team); return err }},
		{"Repo.InboxCount", func() error { _, err := repo.InboxCount(ctx, team, "wing_acme", "inbox"); return err }},
		{"Repo.FiledAwaySummary", func() error { _, _, _, _, _, err := repo.FiledAwaySummary(ctx, team); return err }},
		{"Repo.KGCounts", func() error { _, _, _, err := repo.KGCounts(ctx, team); return err }},
		{"Repo.ListTunnels", func() error { _, err := repo.ListTunnels(ctx, team, "wing_acme"); return err }},
		{"Repo.ListHallways", func() error { _, err := repo.ListHallways(ctx, team, "wing_acme"); return err }},
		{"Repo.Diary", func() error { _, err := repo.Diary(ctx, team, "agent", "wing_acme", 5); return err }},
		{"Service.Get", func() error { _, err := svc.Get(ctx, team, id); return err }},
		{"Service.ListAnchors", func() error { _, err := svc.ListAnchors(ctx, team, AnchorFilter{}); return err }},
		{"Service.AnchorsForDrawers", func() error { _, err := svc.AnchorsForDrawers(ctx, team, []string{id}); return err }},
		{"Service.RecallStats", func() error { _, err := svc.RecallStats(ctx, team, "", 24*time.Hour, 5); return err }},
		{"Service.CountFetches", func() error { _, _, err := svc.CountFetches(ctx, team, 24*time.Hour); return err }},
	}
	for _, r := range reads {
		if err := r.call(); err != nil {
			t.Errorf("%s against a query_only reader: %v", r.name, err)
		}
	}

	sqlReader, err := reader.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlReader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, team, id); err == nil {
		t.Fatal("Repo.Get still answers with the reader closed: it is reading through the writer, and the reader field is decoration")
	}
	if err := live.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("the writer stopped working when the reader closed: %v — they share a pool", err)
	}
}

// TestEveryTransactionUsesTheWriteHandle drives every write flow that opens a
// Transaction through a Service whose reader is query_only, so a transaction
// that had drifted onto the reader fails here with SQLite's readonly error
// rather than in a served palace.
//
// ADR-052 T5 S4/S5. The flows are the ones the record names: a content
// correction (supersedeInto), a relocation (moveMemory — the ADR-045 read
// inside Repo.Update that S6 asks a reviewer to follow), ending a record,
// superseding a fact, recomputing hallways and merging a wing. Each is also a
// read-then-write, which is why "wholly on the writer" is the rule: a read on
// the reader followed by a write on the writer would be two connections and
// no transaction at all, and this test could not see that — T6's source gate
// is what does.
func TestEveryTransactionUsesTheWriteHandle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	svc := newTestService(t)
	mustRefuseWrites(t, svc.repo.reader)

	const team = "team-tx"
	added, err := svc.Add(ctx, team, AddInput{Wing: "wing_a", Room: "decisions", Content: "a record that will be moved, corrected, and ended"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	id := added.Drawers[0].ID

	room := "gotchas"
	if _, err := svc.Update(ctx, team, id, DrawerPatch{Room: &room}); err != nil {
		t.Errorf("relocation (moveMemory) on the writer: %v", err)
	}
	corrected := "corrected in one transaction"
	upd, err := svc.Update(ctx, team, id, DrawerPatch{Content: &corrected, Reason: "handles test"})
	if err != nil {
		t.Fatalf("content correction (supersedeInto) on the writer: %v", err)
	}
	if err := svc.EndDrawer(ctx, team, upd.Drawer.ID, "handles test"); err != nil {
		t.Errorf("EndDrawer on the writer: %v", err)
	}
	if _, err := svc.KGAdd(ctx, team, "handles", "routes", "reads", "", "", "", "", ""); err != nil {
		t.Fatalf("KGAdd: %v", err)
	}
	if _, err := svc.KGSupersede(ctx, team, "handles", "routes", "reads", "reads-and-writes", "handles test"); err != nil {
		t.Errorf("KGSupersede on the writer: %v", err)
	}
	if _, err := svc.RecomputeGraph(ctx, team, "wing_a", false); err != nil {
		t.Errorf("RecomputeGraph (ReplaceWingHallways) on the writer: %v", err)
	}
	if _, err := svc.MergeWing(ctx, team, []string{"wing_a"}, "wing_b"); err != nil {
		t.Errorf("MergeWing (Relabel*ReturningIDs) on the writer: %v", err)
	}
}
