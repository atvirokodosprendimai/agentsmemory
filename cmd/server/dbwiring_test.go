package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"

	"gorm.io/gorm"
)

// TestTheReadHandleCannotWrite proves the reader's refusal is SQLite's, not a
// naming convention — and that the handle still reads.
//
// ADR-052 T4. A handle called "reader" that can write is a convention, and a
// convention is what review catches on a good day; `query_only(1)` in the DSN
// is what the driver refuses on every day. Both directions are asserted on
// purpose: a handle that refused everything would also pass a refusal-only
// check, and a handle that is merely a second writer would pass a read-only
// check. The write is attempted AFTER a successful read on the same handle so
// the refusal cannot be blamed on a connection that never worked.
func TestTheReadHandleCannotWrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "roles.db")
	w, err := openWriterDB(path, false)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if err := w.Exec("CREATE TABLE adr052_roles (id INTEGER PRIMARY KEY, v INTEGER)").Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.Exec("INSERT INTO adr052_roles (v) VALUES (1)").Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	r, err := openReaderDB(path, false, 0)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	var n int64
	if err := r.Raw("SELECT COUNT(*) FROM adr052_roles").Scan(&n).Error; err != nil {
		t.Fatalf("a read through the reader failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("reader counted %d rows, want 1 — it is not looking at the writer's database", n)
	}
	err = r.Exec("INSERT INTO adr052_roles (v) VALUES (2)").Error
	if err == nil {
		t.Fatal("a write through the reader succeeded: the handle is a second writer wearing a reader's name")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("the reader refused the write for the wrong reason: %v — want SQLite's readonly refusal, "+
			"which is the only one that does not depend on somebody remembering", err)
	}
	if err := w.Raw("SELECT COUNT(*) FROM adr052_roles").Scan(&n).Error; err != nil || n != 1 {
		t.Fatalf("after the refusal the writer sees %d rows (err %v), want 1 — the refusal was not real", n, err)
	}
}

// TestTheServePathOpensBothHandles proves the composition root builds a
// reader beside the writer and closes both, before anything reads through it.
//
// ADR-052 T4 S5. Nothing consumes the reader until T5, so this is the rung
// that would otherwise go unproved: `openReaderDB` could exist, pass
// TestTheReadHandleCannotWrite, and be called by nothing — the finished,
// unreachable capability AGENTS.md §Reachability records four times over.
// The mutant is deleting the `openReaderDB` call from buildServicesWith.
func TestTheServePathOpensBothHandles(t *testing.T) {
	// Not parallel: buildServices migrates through goose, whose SetBaseFS and
	// SetDialect are package globals, and two migrations at once are a data race
	// the -race job on main caught (2026-09-04) after this test shipped parallel.

	cfg := config.Default()
	cfg.DBPath = filepath.Join(t.TempDir(), "serve.db")
	cfg.VectorBackend = config.VectorBackendSQLite
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	if svc.gdb == nil || svc.rdb == nil {
		t.Fatalf("serve path built writer=%v reader=%v; both must exist at the composition root, "+
			"which is where the split is a decision rather than a convention", svc.gdb != nil, svc.rdb != nil)
	}
	err = svc.rdb.Exec("CREATE TABLE adr052_probe (id INTEGER PRIMARY KEY)").Error
	if err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("the serve path's reader accepted a write (err %v): it was not opened with readerDBPragmas", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for name, gdb := range map[string]*gorm.DB{"writer": svc.gdb, "reader": svc.rdb} {
		sqlDB, err := gdb.DB()
		if err != nil {
			t.Fatalf("%s: sql handle: %v", name, err)
		}
		if err := sqlDB.Ping(); err == nil {
			t.Errorf("%s handle still answers after Close: shutdown left a connection open", name)
		}
	}
}

// TestTheReaderPoolFlagReachesTheHandle proves --db-reader-pool is not
// decoration: the value the operator passes is the pool the serve path opens.
//
// ADR-052 T4 S8. A knob that is read into a config field nothing passes on is
// exactly the inert setting ADR-006 rejects, and it passes every reachability
// gate here — the field is assigned, the field is read. So the value is driven
// through the real CLI parse, through buildServices, and read back from the
// reader handle's pool statistics. 3 is chosen because the derivation
// (max(4, NumCPU)) can never produce it. The unset case pins the derivation
// itself, so a default that silently became 1 — a second serialised handle —
// would be caught here rather than in production latency.
func TestTheReaderPoolFlagReachesTheHandle(t *testing.T) {
	// Not parallel, for the reason TestTheServePathOpensBothHandles states: two
	// concurrent buildServices calls race on goose's globals.

	build := func(t *testing.T, args ...string) *services {
		t.Helper()
		cfg := parseThroughCLI(t, config.Default(), args...)
		cfg.DBPath = filepath.Join(t.TempDir(), "pool.db")
		cfg.VectorBackend = config.VectorBackendSQLite
		svc, err := buildServices(cfg)
		if err != nil {
			t.Fatalf("build services with %v: %v", args, err)
		}
		t.Cleanup(func() { _ = svc.Close() })
		return svc
	}
	pool := func(t *testing.T, svc *services) int {
		t.Helper()
		sqlDB, err := svc.rdb.DB()
		if err != nil {
			t.Fatalf("reader sql handle: %v", err)
		}
		return sqlDB.Stats().MaxOpenConnections
	}

	if got := pool(t, build(t, "--db-reader-pool=3")); got != 3 {
		t.Errorf("--db-reader-pool=3 opened a reader pool of %d: the flag parses and the value goes "+
			"somewhere other than SetMaxOpenConns", got)
	}
	want := max(4, runtime.NumCPU())
	if got := pool(t, build(t)); got != want {
		t.Errorf("with the flag unset the reader pool is %d, want max(4, NumCPU)=%d — the derivation "+
			"the --help text promises is not what the handle got", got, want)
	}
}
