package repohygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// migratesAndClosesIn reports every function in one file that migrates a database
// but never closes the handle it opened, as "<func> (<file>)" strings.
//
// It returns the offenders instead of asserting on them so the falsifiability
// subtest can drive the SAME function over a fixture that IS one. A gate whose
// negative case reimplements the detection pins nothing: severing the real check
// then leaves the subtest green, which this corpus has recorded twice.
func migratesAndClosesIn(fset *token.FileSet, file *ast.File, path string) []string {
	var bad []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		// ⚠ SCOPED TO FUNCTIONS TAKING *testing.T, and the scope is the finding.
		// cmd/server's own `migrate` calls goose.Up and does NOT close: it opens the
		// handle the process serves from, and closing it would be the bug. What makes
		// a handle a TEST handle is that a *testing.T is in scope to own its lifetime,
		// which is also exactly when t.TempDir's cleanup is waiting on it.
		if !takesTestingT(fn) {
			return true
		}
		migrates, closes := false, false
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// goose.Up is what makes a handle a MIGRATED palace rather than an
			// incidental open — the shape both offenders had.
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "goose" && sel.Sel.Name == "Up" {
				migrates = true
			}
			if sel.Sel.Name == "Close" {
				closes = true
			}
			return true
		})
		if migrates && !closes {
			bad = append(bad, fn.Name.Name+" ("+path+")")
		}
		return true
	})
	return bad
}

// takesTestingT reports whether fn accepts a *testing.T or testing.TB, which is
// what distinguishes a helper that owns a throwaway handle from production code
// that owns the process's own.
func takesTestingT(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, f := range fn.Type.Params.List {
		t := f.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if sel, ok := t.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "testing" {
				return true
			}
		}
	}
	return false
}

// fileMentions reports whether the file calls pkg.name anywhere, so the walk can
// skip files that cannot be offenders without reading them twice.
func fileMentions(file *ast.File, pkg, name string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg && sel.Sel.Name == name {
			found = true
		}
		return true
	})
	return found
}

// TestEveryMigratingTestHelperClosesItsHandle catches a leak this platform cannot
// feel.
//
// Two helpers migrated a throwaway palace and returned the *gorm.DB without ever
// closing the underlying *sql.DB. POSIX unlinks a file that still has an open
// descriptor, so on Linux and macOS the leak produces NO SIGNAL — not a slow test,
// not a warning, nothing. Windows refuses the unlink, and t.TempDir registers its
// RemoveAll at call time, so there it surfaced as 40 tests failing in cleanup with
// every assertion passing, and docs/architecture.md's own Gate command could not
// pass on the host (#162).
//
// That asymmetry is why this is an AST gate and not a behavioural one. A test that
// observed the symptom would be a test that only ever runs where the symptom does
// not exist. Reading the source asks the question on every platform: a function
// that migrates a database owns the handle, and owning it means closing it.
//
// The universe is derived — any function calling goose.Up — so a helper written
// tomorrow joins the check on the commit that adds it, rather than waiting for
// someone to remember this issue.
func TestEveryMigratingTestHelperClosesItsHandle(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	var offenders []string
	migrators := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == ".claude" || base == "node_modules" || base == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // not this gate's job to report a parse error
		}
		rel, _ := filepath.Rel(root, path)
		if !fileMentions(src, "goose", "Up") {
			return nil
		}
		migrators++
		offenders = append(offenders, migratesAndClosesIn(fset, src, rel)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if migrators == 0 {
		t.Fatal("no file calling goose.Up was found, so this gate looked at nothing — " +
			"the migration helper moved or was renamed, and the check must be re-pointed " +
			"rather than left reporting a clean tree it never read")
	}

	for _, o := range offenders {
		t.Errorf("%s migrates a database and never closes the handle.\n"+
			"  This is silent on POSIX — the unlink succeeds against an open descriptor — and on "+
			"Windows it fails t.TempDir cleanup in every test that used the helper, with all of "+
			"their assertions passing. Register the close with t.Cleanup, which runs LIFO and "+
			"therefore before TempDir's own RemoveAll.", o)
	}

	// A corpus with zero offenders cannot exercise the branch that reports one, so
	// the falsifiability half drives the same function over a fixture that IS an
	// offender — inside this test, because the fence runs one name.
	t.Run("a leaking helper is caught", func(t *testing.T) {
		const fixture = `package p
import "testing"
func leaks(t *testing.T) *DB {
	goose.Up(sqlDB, "migrations")
	return gdb
}
func closes(t *testing.T) *DB {
	goose.Up(sqlDB, "migrations")
	t.Cleanup(func() { sqlDB.Close() })
	return gdb
}`
		fs2 := token.NewFileSet()
		f, err := parser.ParseFile(fs2, "fixture.go", fixture, 0)
		if err != nil {
			t.Fatalf("parse fixture: %v", err)
		}
		got := migratesAndClosesIn(fs2, f, "fixture.go")
		if len(got) != 1 || !strings.HasPrefix(got[0], "leaks") {
			t.Fatalf("the detector reported %v over a fixture with exactly one leaking helper; "+
				"a gate that cannot see an offender cannot report a clean tree either", got)
		}
	})
}
