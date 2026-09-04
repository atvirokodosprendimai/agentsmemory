package main

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// TestEveryServingHandleDeclaresItsRole reads the database wiring out of the
// AST and fails when the composition root stops saying which handle is which.
//
// ADR-052 T6. The decision is two one-line facts in this package — the writer
// is ONE connection, and every serving handle is opened by one of three named
// openers — and both are facts every behavioural test survives the loss of:
// raise SetMaxOpenConns(1) and the lock-upgrade failure returns with the suite
// green (T1's test is the exception, and it is one test); add a fourth opener
// beside the three and nothing objects. So the gate derives its universe from
// the source rather than from a list of file names beside it: every
// openDBWithPragmas call must sit inside openWriterDB, openReaderDB or
// openInspectionDB, and openWriterDB's cap must be the literal 1. The
// falsifiability case is a SUBTEST over a fixture that IS an offender, inside
// this fence, because an AST check can be real, passing, and unable to see the
// thing it names — and the Acceptance fence greps for the subtest's name so a
// skipped negative case cannot report success. The third section walks every
// Transaction( in internal/palace and requires it to open on the writer or on
// the tx it was handed: a read-first transaction on the pooled reader is the
// exact shape that reintroduces the failure, and T5's repoOn exists so that no
// tx-internal read lands there.
//
// What it cannot see is stated in AGENTS.md §Reachability: a read method that
// routes itself onto the writer, or a new read that writes. That stays
// review's job, and internal/palace's strict fixture is what makes the second
// fail loudly.
func TestEveryServingHandleDeclaresItsRole(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	files := parseNonTestGoFiles(t, fset, ".")
	universe, findings := servingHandleFindings(fset, files)
	if universe < 3 {
		t.Fatalf("saw %d openDBWithPragmas call(s) in cmd/server, want at least the three openers — the gate is looking at the wrong tree", universe)
	}
	for _, f := range findings {
		t.Error(f)
	}

	palaceFset := token.NewFileSet()
	palaceFiles := parseNonTestGoFiles(t, palaceFset, filepath.Join("..", "..", "internal", "palace"))
	txCount, txFindings := transactionHandleFindings(palaceFset, palaceFiles)
	if txCount == 0 {
		t.Fatal("saw no Transaction( call in internal/palace — the gate is looking at the wrong tree")
	}
	for _, f := range txFindings {
		t.Error(f)
	}

	t.Run("catches an unbounded pool", func(t *testing.T) {
		const fixture = `package main

func openWriterDB(path string, debug bool) (*gorm.DB, error) {
	gdb, err := openDBWithPragmas(path, debug, db.WriterPragmas)
	if err != nil {
		return nil, err
	}
	sqlDB, _ := gdb.DB()
	sqlDB.SetMaxOpenConns(8)
	return gdb, nil
}

func openSideDoor(path string) (*gorm.DB, error) {
	return openDBWithPragmas(path, false, "?_pragma=busy_timeout(1)")
}
`
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "fixture.go", fixture, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, got := servingHandleFindings(fset, []*ast.File{f})
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, "openWriterDB caps its pool with 8") {
			t.Errorf("the extractor did not report the writer pool of 8 — a raised cap would pass this gate:\n%s", joined)
		}
		if !strings.Contains(joined, "openSideDoor") {
			t.Errorf("the extractor did not report the fourth opener — a handle nobody named would pass this gate:\n%s", joined)
		}
	})

	t.Run("catches a transaction on the reader", func(t *testing.T) {
		const fixture = `package palace

func (s *Service) drift(ctx context.Context) error {
	return s.repo.reader.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return nil })
}

func (r *Repo) fine(ctx context.Context) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Transaction(func(inner *gorm.DB) error { return nil })
	})
}
`
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "fixture.go", fixture, 0)
		if err != nil {
			t.Fatal(err)
		}
		n, got := transactionHandleFindings(fset, []*ast.File{f})
		if n != 3 {
			t.Errorf("counted %d Transaction( calls in the fixture, want 3", n)
		}
		if len(got) != 1 || !strings.Contains(got[0], "drift") {
			t.Errorf("want exactly one finding, naming drift; got %v", got)
		}
	})
}

// TestNoServingOpenerAddsAWriteSerialisationPragma evaluates each opener's DSN
// argument from the AST and refuses a write-side serialisation pragma.
//
// ADR-052 rejected _txlock=immediate deliberately: with ONE writer connection
// there is nothing to serialise against, and a serialisation pragma appearing
// on a serving DSN means the writer count stopped being one and somebody
// papered over the second writer instead of removing it. T2's constant test
// pins the two named constants; this one reads the ARGUMENT each opener
// actually passes, so `readerDBPragmas + "&_txlock=immediate"` typed at the
// call site — invisible to a test of the constants — is caught here.
func TestNoServingOpenerAddsAWriteSerialisationPragma(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	files := parseNonTestGoFiles(t, fset, ".")
	consts := packageConsts(files)
	seen := 0
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "openDBWithPragmas" || len(call.Args) != 3 {
				return true
			}
			seen++
			pos := fset.Position(call.Pos())
			dsn, ok := pragmaValue(call.Args[2], consts)
			if !ok {
				t.Errorf("%s: cannot evaluate the DSN argument from the source — a pragma string this gate cannot read is one it cannot refuse", pos)
				return true
			}
			for _, banned := range []string{"_txlock", "locking_mode"} {
				if strings.Contains(dsn, banned) {
					t.Errorf("%s: the serving DSN carries %s — a write-side serialisation pragma means the writer count stopped being one (ADR-052)", pos, banned)
				}
			}
			return true
		})
	}
	if seen < 3 {
		t.Fatalf("evaluated %d opener DSN(s), want at least 3", seen)
	}

	t.Run("catches _txlock typed at the call site", func(t *testing.T) {
		const fixture = `package main

const readerDBPragmas = db.WriterPragmas + "&_pragma=query_only(1)"

func openWriterDB(path string, debug bool) (*gorm.DB, error) {
	return openDBWithPragmas(path, debug, readerDBPragmas+"&_txlock=immediate")
}
`
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "fixture.go", fixture, 0)
		if err != nil {
			t.Fatal(err)
		}
		files := []*ast.File{f}
		consts := packageConsts(files)
		var call *ast.CallExpr
		ast.Inspect(f, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "openDBWithPragmas" {
					call = c
				}
			}
			return true
		})
		dsn, ok := pragmaValue(call.Args[2], consts)
		if !ok || !strings.Contains(dsn, "_txlock=immediate") {
			t.Errorf("the evaluator did not see _txlock through the constant and the concatenation: ok=%v dsn=%q", ok, dsn)
		}
	})
}
