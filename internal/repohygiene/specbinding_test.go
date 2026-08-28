package repohygiene

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// specBindingRE matches the `path/to/file_test.go::<test function>` form a spec's
// Facts table and scenario headings use to bind an assertion to the test that
// proves it.
//
// ⚠ THE SUBTEST HALF IS RESOLVED ON ITS PARENT AND NOTHING MORE. A binding may
// name `…::TestParent/the_case`, and this matches `TestParent`, checks that the
// parent function is declared, and says nothing about whether the subtest exists
// — go/parser sees `t.Run` names as string literals, not declarations. No spec
// binds a subtest today, so it is latent; it is named here rather than left for a
// reader to assume the gate covers more than it does, because a gate whose name
// claims more than it covers is worse than a narrower one. Found in review.
var specBindingRE = regexp.MustCompile(`([A-Za-z0-9_./-]+_test\.go)::(Test[A-Za-z0-9_]+)(/\S*)?`)

// TestEverySpecBindingNamesATestThatExists closes the one hole a spec's own gate
// cannot see.
//
// ⚠ A BINDING IS A POINTER, AND `spec-verify` NEVER FOLLOWS IT. It parses the
// heading grammar and the Facts table and checks that a binding is PRESENT; it
// does not open the file or look for the function. Renaming a bound test — or
// deleting the stub entirely — leaves `spec-verify --draft` at [PASS], so the
// document goes on claiming a fact is proved by a test nothing runs. Found in
// review of the read-cost spec 2026-08-28, which is the first spec in this tree
// whose bindings live behind a build tag and are therefore invisible to the
// default lane as well.
//
// Build tags are irrelevant here on purpose: this walks the source with
// go/parser rather than running anything, so a deliberately-red binding parked
// behind `-tags readcostspec` is checked exactly like a green one. That is the
// property that makes the gate useful during the @spec phase, when by definition
// no bound test passes yet.
func TestEverySpecBindingNamesATestThatExists(t *testing.T) {
	root := repoRoot(t)
	specs, err := filepath.Glob(filepath.Join(root, "docs", "specs", "*.md"))
	if err != nil {
		t.Fatalf("glob specs: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("no specs found under docs/specs — this gate derives its universe from that " +
			"directory, so an empty result means the path moved, not that there is nothing to check")
	}

	// declaredTests caches one parse per test file, because several bindings
	// usually name the same file.
	declared := map[string]map[string]bool{}
	testsIn := func(path string) (map[string]bool, error) {
		if got, ok := declared[path]; ok {
			return got, nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, err
		}
		names := map[string]bool{}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if ok && fn.Recv == nil {
				names[fn.Name.Name] = true
			}
		}
		declared[path] = names
		return names, nil
	}

	checked, problems := 0, 0
	for _, spec := range specs {
		body, err := os.ReadFile(spec)
		if err != nil {
			t.Fatalf("read %s: %v", spec, err)
		}
		rel, _ := filepath.Rel(root, spec)
		found, unresolved := unresolvedBindings(string(body), root, testsIn)
		checked += found
		for _, u := range unresolved {
			problems++
			t.Errorf("%s: %s\n"+
				"A binding is the only route from an assertion to its proof, and `spec-verify` "+
				"checks it is PRESENT, never that it RESOLVES — so this stays [PASS] while the "+
				"fact is proved by nothing. Rename the binding or add the stub; a failing stub is "+
				"the correct @spec state.", rel, u)
		}
	}

	// A self-extracted universe is worth exactly what the extraction is worth, so
	// prove the regex matched something before reporting all-clear.
	if checked == 0 {
		t.Errorf("parsed %d spec(s) and found ZERO bindings — a green run here would mean the "+
			"binding syntax changed, not that every binding resolves", len(specs))
	}
	// ⚠ Report the count only when the verdict is clean. A summary line that says
	// "N bindings resolve" underneath a failure is the shape that let a disabled
	// gate stay green and announce all-clear over a real offender.
	if !t.Failed() {
		t.Logf("%d binding(s) across %d spec(s) resolve to a declared test", checked, len(specs))
	}
}

// unresolvedBindings is the whole judgement, in one place, so the falsifiability
// half below drives THIS code rather than a copy of it. It returns how many
// distinct bindings it looked at and a message per binding that does not resolve.
//
// The first draft had the subtest reimplement the loop with its own map. Severing
// the reporting branch here then left that subtest green and the whole suite at
// exit 0 — the gate was unpinned and its own comment claimed otherwise. Found in
// review; it is the same defect this file exists to catch, one level up.
func unresolvedBindings(spec, root string, testsIn func(string) (map[string]bool, error)) (int, []string) {
	var out []string
	seen := map[string]bool{}
	for _, m := range specBindingRE.FindAllStringSubmatch(spec, -1) {
		if seen[m[0]] {
			continue
		}
		seen[m[0]] = true
		names, err := testsIn(filepath.Join(root, filepath.FromSlash(m[1])))
		if err != nil {
			out = append(out, fmt.Sprintf("binds %s, but %s cannot be read: %v", m[0], m[1], err))
			continue
		}
		if !names[m[2]] {
			out = append(out, fmt.Sprintf("binds %s, but %s declares no func %s", m[0], m[1], m[2]))
		}
	}
	return len(seen), out
}

// TestASpecBindingThatNamesNothingIsCaught is the falsifiability half.
//
// A corpus with zero broken bindings cannot exercise the branch that reports one,
// so the gate above would pass identically if its check were deleted. This drives
// unresolvedBindings — the same function, not a copy — over a fixture that IS
// broken, so severing the resolution check fails here too.
func TestASpecBindingThatNamesNothingIsCaught(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\nimport \"testing\"\n\nfunc TestRealOne(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	testsIn := func(path string) (map[string]bool, error) {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil, err
		}
		names := map[string]bool{}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				names[fn.Name.Name] = true
			}
		}
		return names, nil
	}

	spec := "| F-1 | thing | `sample_test.go::TestRealOne` | @spec |\n" +
		"| F-2 | other | `sample_test.go::TestRenamedAway` | @spec |\n" +
		"| F-3 | gone  | `absent_test.go::TestNoSuchFile` | @spec |\n"
	found, problems := unresolvedBindings(spec, dir, testsIn)
	if found != 3 {
		t.Errorf("saw %d distinct bindings, want 3 — the extraction is what the verdict rests on", found)
	}
	if len(problems) != 2 {
		t.Fatalf("caught %d unresolved bindings, want 2 (a renamed test and a missing file): %v\n"+
			"Without this the gate above passes over a clean corpus whatever its body says.",
			len(problems), problems)
	}
	if !strings.Contains(problems[0], "TestRenamedAway") || !strings.Contains(problems[1], "absent_test.go") {
		t.Errorf("the two problems are not the two planted ones: %v", problems)
	}
}
