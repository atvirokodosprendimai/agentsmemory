package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
)

// parseNonTestGoFiles parses every non-test Go file in dir, in a stable order,
// so a gate reads the package the way the compiler does rather than a list of
// file names kept beside it.
func parseNonTestGoFiles(t *testing.T, fset *token.FileSet, dir string) []*ast.File {
	t.Helper()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	var files []*ast.File
	for _, pkg := range pkgs {
		names := make([]string, 0, len(pkg.Files))
		for name := range pkg.Files {
			names = append(names, name)
		}
		// Map order is random; a finding's position must not depend on it.
		for i := 1; i < len(names); i++ {
			for j := i; j > 0 && names[j] < names[j-1]; j-- {
				names[j], names[j-1] = names[j-1], names[j]
			}
		}
		for _, name := range names {
			files = append(files, pkg.Files[name])
		}
	}
	if len(files) == 0 {
		t.Fatalf("no non-test Go files under %s", filepath.Clean(dir))
	}
	return files
}

// servingOpeners are the three roles ADR-052 names. A fourth function calling
// openDBWithPragmas is a handle whose role nobody decided, which is the gate's
// first finding; an exemption list here would be the list the task's Stop
// Condition forbids, so a legitimate fourth role is added by reshaping this
// set in the same commit that adds the opener.
var servingOpeners = map[string]bool{"openWriterDB": true, "openReaderDB": true, "openInspectionDB": true}

// servingHandleFindings reports every way the composition root's database
// wiring has drifted from ADR-052, and how many openDBWithPragmas call sites it
// judged, so a caller can tell an empty finding list from an empty universe.
//
// Two facts are checked: every openDBWithPragmas call sits inside one of the
// three named openers, and openWriterDB caps its pool with the literal 1 —
// a variable, a constant or a bigger number all read as the cap being treated
// as a tuning parameter again, which is the defect the record exists to
// remove rather than a configuration gap.
func servingHandleFindings(fset *token.FileSet, files []*ast.File) (universe int, findings []string) {
	writerSeen := false
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if fun.Name == "openDBWithPragmas" {
						universe++
						if !servingOpeners[fd.Name.Name] {
							findings = append(findings, fmt.Sprintf("%s: %s opens a serving handle directly; only openWriterDB, openReaderDB and openInspectionDB may (ADR-052 T6)",
								fset.Position(call.Pos()), fd.Name.Name))
						}
					}
				case *ast.SelectorExpr:
					if fun.Sel.Name == "SetMaxOpenConns" && fd.Name.Name == "openWriterDB" {
						writerSeen = true
						if lit, ok := literalInt(call); !ok || lit != "1" {
							findings = append(findings, fmt.Sprintf("%s: openWriterDB caps its pool with %s, want the literal 1 — one writer is ADR-052's decision, and anything else here means it became a knob",
								fset.Position(call.Pos()), exprText(fset, call.Args)))
						}
					}
				}
				return true
			})
		}
	}
	if !writerSeen {
		findings = append(findings, "openWriterDB never calls SetMaxOpenConns: the writer pool is unbounded, which is the lock-upgrade failure ADR-052 measured (280 of 320)")
	}
	return universe, findings
}

// transactionHandleFindings walks every Transaction( call in files and reports
// each whose receiver is not the writer handle or the tx it was handed.
//
// ADR-052 T6 S6. A read-first transaction is not itself a defect — one writer
// connection cannot deadlock against itself — but a transaction opened on the
// pooled reader is: it would be refused by SQLite when it writes, and until
// then it holds a snapshot no write can see. The receiver is resolved by
// walking down the call chain (WithContext, Session, ...) to the field it
// hangs off, so `s.repo.db.WithContext(ctx).Transaction` and `r.db.Transaction`
// both resolve to db, and a nested `tx.Transaction` to tx.
func transactionHandleFindings(fset *token.FileSet, files []*ast.File) (count int, findings []string) {
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Transaction" {
					return true
				}
				count++
				switch root := handleRoot(sel.X); root {
				case "db", "tx":
				default:
					findings = append(findings, fmt.Sprintf("%s: %s opens a Transaction on %q, not on the writer handle (db) or the tx it was handed — that is the split ADR-052's invariant forbids",
						fset.Position(call.Pos()), fd.Name.Name, root))
				}
				return true
			})
		}
	}
	return count, findings
}

// handleRoot names the handle a gorm call chain hangs off: the first selector
// called db or reader walking down, or the bare identifier at the bottom.
func handleRoot(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.CallExpr:
			e = x.Fun
		case *ast.SelectorExpr:
			if x.Sel.Name == "db" || x.Sel.Name == "reader" {
				return x.Sel.Name
			}
			e = x.X
		case *ast.Ident:
			return x.Name
		case *ast.ParenExpr:
			e = x.X
		default:
			return fmt.Sprintf("%T", e)
		}
	}
}

// packageConsts collects every package-level constant's expression, so a DSN
// argument that names one can be evaluated from the source.
func packageConsts(files []*ast.File) map[string]ast.Expr {
	consts := map[string]ast.Expr{}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						consts[name.Name] = vs.Values[i]
					}
				}
			}
		}
	}
	return consts
}

// pragmaValue evaluates a DSN expression the way the compiler would for the
// shapes this package uses: a string literal, a package constant, the one
// cross-package constant db.WriterPragmas, and concatenations of those.
// Anything else is reported as unevaluable rather than guessed at.
func pragmaValue(e ast.Expr, consts map[string]ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.Ident:
		if v, ok := consts[x.Name]; ok {
			return pragmaValue(v, consts)
		}
		return "", false
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "db" && x.Sel.Name == "WriterPragmas" {
			return db.WriterPragmas, true
		}
		return "", false
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, okL := pragmaValue(x.X, consts)
		r, okR := pragmaValue(x.Y, consts)
		return l + r, okL && okR
	case *ast.ParenExpr:
		return pragmaValue(x.X, consts)
	}
	return "", false
}

// literalInt returns the single integer literal argument of call, if that is
// what it has.
func literalInt(call *ast.CallExpr) (string, bool) {
	if len(call.Args) != 1 {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return "", false
	}
	return lit.Value, true
}

// exprText renders arguments for a finding, position-free, so the message
// says what was written rather than where a reader should go look it up.
func exprText(fset *token.FileSet, args []ast.Expr) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch x := a.(type) {
		case *ast.BasicLit:
			parts = append(parts, x.Value)
		case *ast.Ident:
			parts = append(parts, x.Name)
		default:
			parts = append(parts, fmt.Sprintf("<%T at %s>", a, fset.Position(a.Pos())))
		}
	}
	return strings.Join(parts, ", ")
}
