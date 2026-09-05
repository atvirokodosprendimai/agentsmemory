package repohygiene

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryTestChildCarriesADeadline refuses a direct exec.Command or
// exec.CommandContext in any test file: children are started through
// internal/testexec, which binds them to the test's context with a deadline
// and kills the whole process group when it fires.
//
// The rule is the owner's, set 2026-09-05 after two hook children in a
// sibling project outlived their FAILED test by fifteen hours, reparented to
// launchd and burning a core each. `go test`'s package timeout does not
// close it: it kills the test binary, not the grandchildren a `bash <hook>`
// started, and this tree's tests are mostly `bash <hook>`. Counted the same
// day: 41 sites in eleven files, none with a deadline. The gate derives its
// universe from the tree rather than from that list, so a 42nd site joins
// the check on the commit that adds it. It reads the AST rather than
// grepping, so a comment that mentions exec.Command is not an offender and
// a call hidden behind an alias is.
func TestEveryTestChildCarriesADeadline(t *testing.T) {
	root := repoRoot(t)
	ignored := gitignoreMatcher(t, root)

	var files []string
	for _, path := range walk(t, root, ignored) {
		if strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
	}
	if len(files) == 0 {
		t.Fatal("no _test.go file found — the walk broke, and the gate is passing vacuously")
	}

	checkTestChildren(t, root, files)

	t.Run("a direct exec.Command in a test file is caught", aDirectExecCommandIsCaught)
}

// checkTestChildren is the gate's decision, reporting through a testing.TB
// the falsifiability subtest substitutes — the only form that catches a
// severed call site (see checkCitations for the two attempts it took).
func checkTestChildren(tb testing.TB, root string, files []string) {
	tb.Helper()
	offenders := directExecCalls(tb, root, files)
	for _, o := range offenders {
		tb.Errorf("%s starts a child with %s and no deadline.\n"+
			"  Use testexec.Command(t, name, args...) from internal/testexec: same arguments, "+
			"and the child dies with the test instead of outliving it.", o.at, o.call)
	}
	tb.Logf("%d test file(s) scanned, %d direct exec call(s)", len(files), len(offenders))
}

// execCall is one direct call, with enough to point a reader at it.
type execCall struct {
	at, call string
}

// directExecCalls parses every file and returns each exec.Command /
// exec.CommandContext selector call, keyed by the package the file imports
// os/exec under — so `import x "os/exec"` is still found and a local helper
// named exec is not. internal/testexec is the one package allowed to call it,
// and its own files are not tests anyway.
func directExecCalls(tb testing.TB, root string, files []string) []execCall {
	tb.Helper()
	var out []execCall
	fset := token.NewFileSet()
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, filepath.Join("internal", "testexec")+string(filepath.Separator)) {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			tb.Fatalf("parse %s: %v", path, err)
		}
		alias := ""
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) != "os/exec" {
				continue
			}
			alias = "exec"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
		}
		if alias == "" || alias == "_" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != alias {
				return true
			}
			if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
				return true
			}
			pos := fset.Position(call.Pos())
			out = append(out, execCall{
				at:   rel + ":" + strconv.Itoa(pos.Line),
				call: alias + "." + sel.Sel.Name,
			})
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out
}

// aDirectExecCommandIsCaught drives the SAME decision over a fixture that is
// an offender, since a tree with zero offenders cannot exercise the branch
// that reports one. Aliased import on purpose: a grep for "exec.Command"
// would miss it, and the AST read must not.
func aDirectExecCommandIsCaught(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "offender_test.go")
	src := "package x\n\nimport (\n\tx \"os/exec\"\n\t\"testing\"\n)\n\n" +
		"// exec.Command in a comment is not a call.\n" +
		"func TestX(t *testing.T) { _ = x.Command(\"bash\", \"hook\") }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &capturingTB{TB: t}
	checkTestChildren(rec, dir, []string{path})
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "x.Command") {
		t.Fatalf("the gate did not report the aliased direct call; errors = %q", rec.errors)
	}
	// And the clean shape passes: the same call through testexec is not a call
	// on the os/exec package at all.
	clean := filepath.Join(dir, "clean_test.go")
	if err := os.WriteFile(clean, []byte("package x\n\nimport \"testing\"\n\nfunc TestY(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = &capturingTB{TB: t}
	checkTestChildren(rec, dir, []string{clean})
	if len(rec.errors) != 0 {
		t.Fatalf("a file with no exec call was reported: %q", rec.errors)
	}
}

// capturingTB keeps the TEXT of each report, because this gate's message names
// the call it found and the subtest checks that name; recordingTB counts only.
type capturingTB struct {
	testing.TB
	errors []string
}

func (c *capturingTB) Helper()                   {}
func (c *capturingTB) Logf(string, ...any)       {}
func (c *capturingTB) Errorf(f string, a ...any) { c.errors = append(c.errors, fmt.Sprintf(f, a...)) }
func (c *capturingTB) Fatalf(f string, a ...any) { panic("fatal: " + fmt.Sprintf(f, a...)) }
func (c *capturingTB) Fatal(a ...any)            { panic(fmt.Sprint(a...)) }
