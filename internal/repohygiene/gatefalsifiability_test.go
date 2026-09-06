package repohygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The defect this closes is the one this package exists to prevent, committed by
// this package: a gate whose error branch NEVER EXECUTES against the real corpus.
//
// ⚠ IT IS INVISIBLE TO EVERY OTHER CHECK, INCLUDING A MUTATION CAMPAIGN. The tree
// is intact, so the branch that reports an offender is never reached, so severing
// the comparison changes no observable behaviour and the suite stays green. The
// gate reads as covered — its name is true, its comment often narrates the exact
// incident it was written for — and it would not notice that incident recurring.
//
// AGENTS.md §Reachability records this four separate times under four names, each
// after it shipped: TestASpecBindingThatNamesNothingIsCaught ("a corpus with zero
// broken bindings cannot exercise the branch that reports one"),
// TestASignOffThatSaysStopIsCaught ("the first draft reimplemented it, and
// severing the real check left the subtest green"),
// TestAHumanObservedSignOffAgreesWithTheIndex ("severing the CALL to it left the
// suite at exit 0 while the gate printed that every sign-off agreed"), and
// TestNoToolDescriptionClaimsALongMemoryCannotBeMoved. Written down four times
// and enforced zero times, so it recurred a fifth — twice inside one PR, in the
// gate written to repair the defect the previous one shipped.
//
// The house remedy is already settled and already followed: drive the predicate
// through a substitutable testing.TB over a fixture that IS an offender. Ten of
// the twelve predicates in this package do it. This makes the eleventh impossible
// to omit, which is the only difference between a convention and a gate.

// substitutableGate is a predicate written to be driven over a fixture: it takes
// its reporter rather than closing over a *testing.T, which is the shape that
// allows a negative case at all.
type substitutableGate struct {
	name string
	file string
	line int
}

// gatesAndTheirRecorders parses this package and returns every substitutable
// predicate beside whether anything ever drives it with a recording reporter.
//
// It is AST rather than grep for the reason the other derived gates here are: a
// predicate added tomorrow joins the universe on the commit that adds it, and a
// name that merely APPEARS in a comment or an error string is not a call.
func gatesAndTheirRecorders(tb testing.TB, dir string) (gates []substitutableGate, driven map[string]bool) {
	tb.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	if err != nil {
		tb.Fatalf("parse %s: %v", dir, err)
	}
	driven = map[string]bool{}
	// ⚠ RECORDER TYPES ARE FOUND STRUCTURALLY, NOT BY NAME, and the first draft of
	// this gate keyed on the literal `recordingTB` — which is one of THREE reporters
	// in this package, so it reported `requireWrittenOverride` as undriven while
	// three subtests were driving it through a `recorder`. A gate that knows one
	// spelling of the thing it is looking for is a gate that reports the other
	// spellings as offences.
	//
	// The structural property is the real one: a struct EMBEDDING testing.TB is a
	// substitute reporter, whatever it is called.
	recorderTypes := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				for _, f := range st.Fields.List {
					if len(f.Names) != 0 { // an embedded field has no name
						continue
					}
					if sel, ok := f.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "TB" {
						if x, ok := sel.X.(*ast.Ident); ok && x.Name == "testing" {
							recorderTypes[ts.Name.Name] = true
						}
					}
				}
				return true
			})
		}
	}
	// Identifiers holding one — both `rec := &recordingTB{}` and `var spy recorder`,
	// because the package uses both. A call is only a NEGATIVE CASE if what it hands
	// the predicate can record: passing the live *testing.T runs the gate against the
	// real tree, which is the positive case and says nothing about the reporting branch.
	recorders := map[string]bool{}
	note := func(lhs ast.Expr) {
		if id, ok := lhs.(*ast.Ident); ok {
			recorders[id.Name] = true
		}
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.AssignStmt:
					for i, rhs := range node.Rhs {
						unary, ok := rhs.(*ast.UnaryExpr)
						if !ok || unary.Op != token.AND {
							continue
						}
						lit, ok := unary.X.(*ast.CompositeLit)
						if !ok {
							continue
						}
						if id, ok := lit.Type.(*ast.Ident); ok && recorderTypes[id.Name] && i < len(node.Lhs) {
							note(node.Lhs[i])
						}
					}
				case *ast.ValueSpec:
					if id, ok := node.Type.(*ast.Ident); ok && recorderTypes[id.Name] {
						for _, name := range node.Names {
							note(name)
						}
					}
				}
				return true
			})
		}
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.FuncDecl:
					if node.Recv != nil || node.Type.Params == nil || len(node.Type.Params.List) == 0 {
						return true
					}
					sel, ok := node.Type.Params.List[0].Type.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "TB" {
						return true
					}
					if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "testing" {
						return true
					}
					// ⚠ A VERDICT, NOT MERELY A FUNCTION THAT TAKES A REPORTER, and the
					// first run of this gate flagged eight of the latter. `walk`,
					// `offendersUnder`, `duplicatedCommentLines` and `directExecCalls`
					// accept a testing.TB to call Helper() and to die on unreadable input;
					// they RETURN their findings and report none. The branch that can
					// silently stop executing is the one that calls Errorf, so that is the
					// universe — and it puts each collector's verdict function in scope
					// instead, which is where the negative case belongs anyway.
					if !reportsAVerdict(node) {
						return true
					}
					gates = append(gates, substitutableGate{
						name: node.Name.Name,
						file: filepath.Base(name),
						line: fset.Position(node.Pos()).Line,
					})
				case *ast.CallExpr:
					id, ok := node.Fun.(*ast.Ident)
					if !ok || len(node.Args) == 0 {
						return true
					}
					// Either `check(rec, …)` or `check(&spy, …)`: both hand it a substitute.
					arg := node.Args[0]
					if unary, ok := arg.(*ast.UnaryExpr); ok && unary.Op == token.AND {
						arg = unary.X
					}
					if a, ok := arg.(*ast.Ident); ok && recorders[a.Name] {
						driven[id.Name] = true
					}
				}
				return true
			})
		}
	}
	sort.Slice(gates, func(i, j int) bool { return gates[i].name < gates[j].name })
	return gates, driven
}

// reportsAVerdict says whether a predicate ever calls Errorf/Error on the
// reporter it was handed — which is what makes it a gate rather than a collector,
// and what makes a negative case meaningful. Keyed to the FIRST PARAMETER'S NAME
// rather than to the literal `tb`, so a predicate that names it something else is
// still in scope.
func reportsAVerdict(fn *ast.FuncDecl) bool {
	if fn.Body == nil || len(fn.Type.Params.List[0].Names) == 0 {
		return false
	}
	reporter := fn.Type.Params.List[0].Names[0].Name
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != reporter {
			return true
		}
		if sel.Sel.Name == "Errorf" || sel.Sel.Name == "Error" {
			found = true
		}
		return true
	})
	return found
}

// checkGateFalsifiability is the verdict, through a substitutable testing.TB —
// which is also this gate submitting to its own rule.
func checkGateFalsifiability(tb testing.TB, dir string) {
	tb.Helper()
	gates, driven := gatesAndTheirRecorders(tb, dir)
	if len(gates) == 0 {
		tb.Fatal("no substitutable predicate found in this package, so this gate is checking " +
			"nothing — the shape it exists to refuse")
	}
	for _, g := range gates {
		if driven[g.name] {
			continue
		}
		tb.Errorf("%s:%d %s is never driven over a fixture that IS an offender.\n"+
			"  The tree is intact, so its reporting branch never executes and severing the "+
			"comparison inside it changes nothing observable. Add a subtest that hands it a "+
			"&recordingTB{} over a broken fixture and asserts it reports — and one over a "+
			"correct fixture asserting it does not, or the gate may be red for any reason at all.",
			g.file, g.line, g.name)
	}
}

// TestEveryHygieneGateHasAFalsifiabilityCase is the gate.
func TestEveryHygieneGateHasAFalsifiabilityCase(t *testing.T) {
	checkGateFalsifiability(t, ".")

	// ⚠ THE UNIVERSE MUST BE NON-TRIVIAL, and a count is not written here on
	// purpose — a frozen one is the drift this corpus keeps recording. What is
	// asserted is that the walk found the predicates it is standing next to,
	// because a parse that silently returned nothing would report a clean run.
	t.Run("the walk finds this package's own predicates", func(t *testing.T) {
		gates, _ := gatesAndTheirRecorders(t, ".")
		var names []string
		for _, g := range gates {
			names = append(names, g.name)
		}
		for _, want := range []string{"checkGateFalsifiability", "checkCitations"} {
			found := false
			for _, n := range names {
				if n == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("the walk did not find %s; it found %v", want, names)
			}
		}
	})

	// The falsifiability half, driving the SAME function over fixtures that are
	// offenders — not a copy of its loop, which is the failure mode this whole
	// gate is about.
	t.Run("a gate with no negative case is caught", func(t *testing.T) {
		write := func(src string) string {
			t.Helper()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			return dir
		}
		const undriven = `package p
import "testing"
type recordingTB struct{ testing.TB; errors int }
func checkThing(tb testing.TB, root string) { tb.Errorf("x") }
func TestThing(t *testing.T) { checkThing(t, ".") }
`
		rec := &recordingTB{}
		checkGateFalsifiability(rec, write(undriven))
		if rec.errors == 0 {
			t.Error("the gate reported nothing over a predicate that is only ever handed the " +
				"live *testing.T — the exact shape it exists to refuse")
		}

		const dr = `package p
import "testing"
type recordingTB struct{ testing.TB; errors int }
func checkThing(tb testing.TB, root string) { tb.Errorf("x") }
func TestThing(t *testing.T) {
	checkThing(t, ".")
	r := &recordingTB{}
	checkThing(r, "fixture")
	if r.errors == 0 { t.Error("no") }
}
`
		quiet := &recordingTB{}
		checkGateFalsifiability(quiet, write(dr))
		if quiet.errors != 0 {
			t.Errorf("the gate flags a predicate that IS driven over a fixture (%d finding(s)); "+
				"a gate that goes red on the correct shape is one somebody deletes", quiet.errors)
		}
	})
}
