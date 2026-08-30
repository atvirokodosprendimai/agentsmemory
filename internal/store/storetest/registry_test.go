package storetest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// coveredBackends names every VectorStore implementation that runs the
// conformance suite. A type declaring itself a VectorStore and absent from this
// list is the failure this test exists to catch.
//
// The fakes in other packages' tests are deliberately out of scope: they satisfy
// the interface to stand in for a backend, and holding a stub to a storage
// contract would make the list something to silence.
var coveredBackends = map[string]bool{
	"sqlitevec.Store":  true,
	"chromemvec.Index": true,
	"qdrant.Client":    true,
	"store.Hybrid":     true,
}

// TestEveryBackendRunsTheConformanceSuite fails when a type in internal/store
// declares itself a VectorStore (or SourceOfTruth) and is not covered.
//
// Compile-time proofs — `var _ store.VectorStore = (*Store)(nil)` — are how this
// repo already asserts a backend satisfies the seam, so they are also the
// declared set to check the covered set against. Four implementations exist and
// a fifth is one file away; the seam is exactly where "it compiles, so it works"
// is least true.
func TestEveryBackendRunsTheConformanceSuite(t *testing.T) {
	root := storeRoot(t)
	declared := map[string]string{} // "pkg.Type" -> file

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		pkg := f.Name.Name
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "_" || vs.Type == nil {
					continue
				}
				if !mentionsStoreInterface(vs.Type) || len(vs.Values) != 1 {
					continue
				}
				if name := concreteTypeName(vs.Values[0]); name != "" {
					declared[pkg+"."+name] = path
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(declared) == 0 {
		t.Fatal("found no compile-time VectorStore proofs anywhere in internal/store — " +
			"this check has stopped checking anything")
	}

	var missing []string
	for name, file := range declared {
		if !coveredBackends[name] {
			rel, _ := filepath.Rel(root, file)
			missing = append(missing, name+" ("+rel+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s declares itself a VectorStore and does not run the conformance suite.\n"+
			"  Add a test that calls storetest.RunPointsConformance against it, and add it to "+
			"coveredBackends. A backend can satisfy this interface with method bodies that do nothing.", m)
	}
	// The list must not outlive what it covers either: a stale entry makes the
	// count look healthy while the backend is gone.
	for name := range coveredBackends {
		if _, ok := declared[name]; !ok {
			t.Errorf("coveredBackends names %q, which no longer declares itself a VectorStore", name)
		}
	}

	// And being ON the list is not being covered BY the suite. The list is a
	// claim; this reads the tests and checks it. Without this, a backend is
	// "covered" by having been typed into a map — which is the same shape as
	// every unreachable capability this repository has shipped.
	ran := backendsWithAConformanceTest(t, root)
	perPkg := map[string]int{}
	for name := range coveredBackends {
		perPkg[strings.SplitN(name, ".", 2)[0]]++
	}
	for name := range coveredBackends {
		pkg := strings.SplitN(name, ".", 2)[0]
		if ran[pkg] == 0 {
			t.Errorf("coveredBackends claims %q runs the conformance suite, but no test in package %q "+
				"calls BOTH storetest.RunPointsConformance and storetest.RunSetPayloadConformance", name, pkg)
			continue
		}
		// Per TYPE, not per package. A package with two implementations and one
		// suite would otherwise mark both covered, and the second could satisfy
		// the seam with method bodies that do nothing.
		if ran[pkg] < perPkg[pkg] {
			t.Errorf("package %q declares %d covered implementation(s) and runs the suite %d time(s) — "+
				"a second implementation in the same package is marked covered by the first one's test",
				pkg, perPkg[pkg], ran[pkg])
		}
	}
}

// backendsWithAConformanceTest returns the packages under internal/store that
// actually call the suite, read out of the test sources.
func backendsWithAConformanceTest(t *testing.T, root string) map[string]int {
	t.Helper()
	ran := map[string]int{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		// ALL of the suite, not some: a backend that reads correctly and writes
		// a payload that erases the rest of it is covered by one half and broken
		// by the other, and one that cannot count its own points cannot
		// corroborate a rebuild trigger.
		text := string(src)
		if !strings.Contains(text, "RunPointsConformance(") || !strings.Contains(text, "RunSetPayloadConformance(") || !strings.Contains(text, "RunCountConformance(") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, src, parser.PackageClauseOnly)
		if perr != nil {
			return nil
		}
		// An external test package (store_test) covers the package it tests. Count
		// the CALLS, so a package with two implementations needs two suites.
		ran[strings.TrimSuffix(f.Name.Name, "_test")] += strings.Count(text, "RunPointsConformance(t,")
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return ran
}

// mentionsStoreInterface reports whether a type expression is store.VectorStore
// or store.SourceOfTruth — the two seams a backend can declare.
func mentionsStoreInterface(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		// Inside package store itself the proof is unqualified, and reading only
		// the qualified form made this check blind to Hybrid — the one
		// implementation that wraps the other two.
		return t.Name == "VectorStore" || t.Name == "SourceOfTruth"
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok || pkg.Name != "store" {
			return false
		}
		return t.Sel.Name == "VectorStore" || t.Sel.Name == "SourceOfTruth"
	}
	return false
}

// concreteTypeName pulls "Store" out of `(*Store)(nil)`.
func concreteTypeName(e ast.Expr) string {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return ""
	}
	p, ok := call.Fun.(*ast.ParenExpr)
	if !ok {
		return ""
	}
	star, ok := p.X.(*ast.StarExpr)
	if !ok {
		return ""
	}
	id, ok := star.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

func storeRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	return filepath.Dir(wd) // internal/store/storetest -> internal/store
}
