package mcptest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheHarnessOpensTheShippedDatabase reads three pragmas back from a
// harness-opened database and requires the values the server ships.
//
// ADR-052 T3. Before this test the harness opened SQLite with no DSN pragmas
// at all, so every scenario in this package measured a database in rollback
// journal mode with a zero busy timeout and foreign keys off — none of which
// is what `openWriterDB` in cmd/server serves. A scenario that passes there
// says nothing about the database an operator runs, and the concurrent
// scenarios the backlog defers could not have been written honestly at all.
// The three values are read from the connection rather than from the DSN
// string, because the DSN is a promise and the pragma is what SQLite did.
func TestTheHarnessOpensTheShippedDatabase(t *testing.T) {
	t.Parallel()

	gdb := openDB(t, filepath.Join(t.TempDir(), "pragmas.db"))
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	for pragma, want := range map[string]string{
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"foreign_keys": "1",
	} {
		var got string
		if err := sqlDB.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if got != want {
			t.Errorf("harness database reads %s=%q; the server ships %q, so a scenario "+
				"here measures a different database than the one we run", pragma, got, want)
		}
	}
}

// TestTheHarnessNamesOneDSNSource reads the harness's open call out of the
// AST and requires its DSN to reference the shared constant rather than carry
// a pragma string of its own — and then sweeps the tree for a second copy.
//
// ADR-052 T3's invariant is that exactly one writer DSN string exists. Two
// values that happen to be equal today are not that: a test comparing
// `mcptest.X == db.Y` stays green while somebody edits one and forgets the
// other, right up until the constants diverge, which is the day the harness
// starts measuring a database nobody serves. So this looks at the SOURCE. The
// first half fails when the harness's `sqlite.Open` argument holds a literal
// with `_pragma` in it, or fails to name `db.WriterPragmas`; the second half
// fails when any non-test Go file other than `db/pragmas.go` carries a string
// literal spelling `journal_mode(`, which is the writer DSN wherever it is
// typed.
func TestTheHarnessNamesOneDSNSource(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "harness.go", nil, 0)
	if err != nil {
		t.Fatalf("parse harness.go: %v", err)
	}
	var opens int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Open" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "sqlite" || len(call.Args) != 1 {
			return true
		}
		opens++
		namesShared, literal := false, ""
		ast.Inspect(call.Args[0], func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "db" && x.Sel.Name == "WriterPragmas" {
					namesShared = true
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING && strings.Contains(x.Value, "_pragma") {
					literal = x.Value
				}
			}
			return true
		})
		pos := fset.Position(call.Pos())
		if literal != "" {
			t.Errorf("%s: the harness DSN carries its own pragma literal %s — a second copy of "+
				"the writer DSN, which is the drift ADR-052 T3 exists to remove", pos, literal)
		}
		if !namesShared {
			t.Errorf("%s: the harness DSN does not reference db.WriterPragmas, so nothing ties "+
				"the database scenarios measure to the one the server opens", pos)
		}
		return true
	})
	if opens != 1 {
		t.Fatalf("found %d sqlite.Open calls in harness.go, want exactly 1 — the check above "+
			"inspected the wrong thing or nothing", opens)
	}

	root := moduleRoot(t)
	var carriers []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name != "." && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil // a file that does not parse is another gate's finding
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "journal_mode(") {
				rel, _ := filepath.Rel(root, path)
				carriers = append(carriers, rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if want := filepath.Join("db", "pragmas.go"); len(carriers) != 1 || carriers[0] != want {
		t.Errorf("the writer DSN literal is spelled in %v; want exactly one, in %s — every other "+
			"copy is a place the pragmas can drift apart", carriers, want)
	}
}

// moduleRoot walks up from the package directory to the go.mod, so the sweep
// covers the whole module rather than whatever directory `go test` ran in.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package directory")
		}
		dir = parent
	}
}
