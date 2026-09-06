package palace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// A FIELD COMPUTED FOR A CASE AND NOT COPIED INTO ITS DETAIL IS UNREACHABLE FROM
// EVERY CONSUMER OF THE REPORT, AND NOTHING IN THIS PACKAGE COULD SEE IT.
//
// PR #332 fixed exactly that: `caseOutcome` computed `DistractorRanks` and
// `DistractorPoolRank` for every case naming a superseded version, the
// `EvalCaseResult` appended to `report.Details` copied neither, and
// `StaleAboveRate` opens by skipping a case whose `DistractorRanks` is nil. So
// the supersession cell was empty on EVERY corpus, `supersessionGateReady` always
// took its "this run scored none" branch, and the refusal named a cause that sent
// the operator to regenerate a case file that was never the problem. ADR-004's
// pre-registered measurement could not speak, which was issue #34's whole subject.
//
// Every unit test of `StaleAboveRate` passed throughout, because they build their
// own cases — the §Reachability split this repository keeps recording, component
// against selection.
//
// ⚠ WHY THIS IS A GATE AND NOT TWO MORE ASSERTIONS. #332 shipped with a test
// naming those two fields, under a comment saying it "is what fails first when
// somebody adds a field to caseOutcome and forgets the literal that copies it". It
// does not do that: it asserts two NAMED fields, so a THIRD field added tomorrow
// and dropped from the same literal reproduces the defect with the suite green,
// leaving a sentence that reads as coverage while covering nothing. Raised in
// review of #332 and merged unaddressed, so the sentence is on main. This is the
// version that makes the claim true — the universe is derived from the source, so
// a field joins the check on the commit that adds it.

// sharedOutcomeFieldNames returns every field name declared by BOTH caseOutcome
// and EvalCaseResult, which is the only universe this coupling has.
//
// The intersection is what makes the gate exemption-free. A field on caseOutcome
// alone (TopDistance, Degraded) is internal to the computation and has nowhere to
// go; a field on EvalCaseResult alone (Query, Category, Population) is filled from
// the case rather than from the outcome. Only a name on both is a value computed
// here that the report is shaped to carry — and therefore a value whose absence
// from the literal is a drop rather than a decision.
func sharedOutcomeFieldNames(tb testing.TB, path string) []string {
	tb.Helper()
	fields := structFieldNames(tb, path, "caseOutcome", "EvalCaseResult")
	var shared []string
	for name := range fields["caseOutcome"] {
		if fields["EvalCaseResult"][name] {
			shared = append(shared, name)
		}
	}
	sort.Strings(shared)
	if len(shared) == 0 {
		tb.Fatalf("%s: caseOutcome and EvalCaseResult share no field name, so this gate would "+
			"pass over any drop at all; either a type was renamed or the extractor stopped "+
			"seeing them", path)
	}
	return shared
}

// structFieldNames reads the named struct types out of one file's AST.
func structFieldNames(tb testing.TB, path string, want ...string) map[string]map[string]bool {
	tb.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}
	wanted := map[string]bool{}
	for _, w := range want {
		wanted[w] = true
	}
	out := map[string]map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || !wanted[ts.Name.Name] {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		names := map[string]bool{}
		for _, fld := range st.Fields.List {
			for _, id := range fld.Names {
				names[id.Name] = true
			}
		}
		out[ts.Name.Name] = names
		return true
	})
	for _, w := range want {
		if len(out[w]) == 0 {
			tb.Fatalf("%s: found no struct type %s with fields; the gate below would compare "+
				"against nothing", path, w)
		}
	}
	return out
}

// detailLiteralKeys returns the keys set by the EvalCaseResult literal appended to
// report.Details.
//
// ⚠ IT MUST FIND THAT LITERAL AND NOT THE OTHER ONE. EvaluateWith builds a SECOND
// EvalCaseResult two lines below, passed to accumulate, carrying Category and
// Ranks alone — and that one is correct as it stands, because accumulate reads
// only those two and never appends to report.Details. A gate that matched every
// EvalCaseResult literal in the package would report the aggregation value as
// dropping eight fields, which is a false alarm on the first run and the fastest
// way to get a check deleted. The append target is what distinguishes them.
func detailLiteralKeys(tb testing.TB, path string) (map[string]bool, bool) {
	tb.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}
	keys := map[string]bool{}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "append" {
			return true
		}
		// The destination is what names this the details slice, rather than the
		// literal's own type — several slices hold EvalCaseResult.
		sel, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Details" {
			return true
		}
		lit, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			return true
		}
		if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "EvalCaseResult" {
			return true
		}
		found = true
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if id, ok := kv.Key.(*ast.Ident); ok {
				keys[id.Name] = true
			}
		}
		return true
	})
	return keys, found
}

// checkEveryComputedFieldReachesTheDetail is the verdict, through a substitutable
// testing.TB.
//
// Returned through the reporter rather than asserted inline so the falsifiability
// case can drive THIS function over a source file that IS an offender. A corpus
// with no dropped field cannot exercise the branch that reports one, and a
// negative case that reimplemented the comparison would pin nothing — the shape
// AGENTS.md §Reachability records against two earlier gates in this tree.
func checkEveryComputedFieldReachesTheDetail(tb testing.TB, path string) {
	tb.Helper()
	shared := sharedOutcomeFieldNames(tb, path)
	keys, found := detailLiteralKeys(tb, path)
	if !found {
		tb.Fatalf("%s: found no EvalCaseResult literal appended to a .Details slice. Either the "+
			"report stopped being built that way or this extractor stopped seeing it, and both "+
			"make the check below vacuous", path)
	}
	for _, name := range shared {
		if keys[name] {
			continue
		}
		tb.Errorf("%s: caseOutcome computes %s and EvalCaseResult declares it, but the literal "+
			"appended to report.Details never copies it.\n"+
			"  Every consumer of the report is therefore told this case has no %s. That is how "+
			"the supersession cell came to be empty on every corpus while StaleAboveRate's own "+
			"unit tests passed (#332, issue #34): they build their own cases, so nothing between "+
			"the computation and the consumer was ever exercised.", path, name, name)
	}
}

// TestEveryFieldComputedForACaseReachesItsDetail is the gate.
func TestEveryFieldComputedForACaseReachesItsDetail(t *testing.T) {
	checkEveryComputedFieldReachesTheDetail(t, "eval.go")

	t.Run("a computed field dropped from the literal is caught", func(t *testing.T) {
		// The real file carries no drop, so the reporting branch cannot run against
		// it. Driven over a fixture that IS the defect — and over one that is not,
		// because a check that flags everything pins nothing either.
		offender := writeEvalFixture(t, `report.Details = append(report.Details, EvalCaseResult{
		Ranks: oc.Ranks,
	})`)
		rec := &recordingReporter{}
		checkEveryComputedFieldReachesTheDetail(rec, offender)
		if rec.errors != 1 {
			t.Errorf("the gate reported %d finding(s) over a literal dropping exactly one shared "+
				"field; that is the shape #332 fixed", rec.errors)
		}

		whole := writeEvalFixture(t, `report.Details = append(report.Details, EvalCaseResult{
		Ranks: oc.Ranks, DistractorPoolRank: oc.DistractorPoolRank,
	})`)
		quiet := &recordingReporter{}
		checkEveryComputedFieldReachesTheDetail(quiet, whole)
		if quiet.errors != 0 {
			t.Errorf("the gate reported %d finding(s) over a literal that copies every shared "+
				"field; a gate that cannot pass is one somebody deletes rather than satisfies",
				quiet.errors)
		}
	})

	t.Run("the aggregation literal is not mistaken for the detail", func(t *testing.T) {
		// EvaluateWith's second EvalCaseResult carries Category and Ranks alone and
		// is correct: accumulate reads only those and never appends to Details. A
		// gate keying on the literal's TYPE instead of the append target would call
		// it a drop, which is a false alarm on the real file.
		aggregation := writeEvalFixture(t, `report.Details = append(report.Details, EvalCaseResult{
		Ranks: oc.Ranks, DistractorPoolRank: oc.DistractorPoolRank,
	})
	s.accumulate(byArm, &report, EvalCaseResult{Ranks: ranks}, arms)`)
		quiet := &recordingReporter{}
		checkEveryComputedFieldReachesTheDetail(quiet, aggregation)
		if quiet.errors != 0 {
			t.Errorf("the gate reported %d finding(s) over a file whose DETAIL literal is complete "+
				"and whose aggregation literal is deliberately narrow; keying on the type rather "+
				"than the append target is what produces that false alarm", quiet.errors)
		}
	})
}

// writeEvalFixture renders a miniature eval.go carrying both types and the
// statement given, and returns its path.
//
// Only parsed, never compiled, so it needs the shapes rather than a working
// program — which is what lets a fixture state the defect in four lines.
func writeEvalFixture(t *testing.T, stmt string) string {
	t.Helper()
	src := `package p

type caseOutcome struct {
	Ranks              int
	DistractorPoolRank int
	Degraded           bool
}

type EvalCaseResult struct {
	Query              string
	Ranks              int
	DistractorPoolRank int
}

func build() {
	` + stmt + `
}
`
	path := filepath.Join(t.TempDir(), "eval.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// recordingReporter counts verdicts so a negative case can drive the real check.
type recordingReporter struct {
	testing.TB
	errors int
}

func (r *recordingReporter) Helper()                   {}
func (r *recordingReporter) Errorf(string, ...any)     { r.errors++ }
func (r *recordingReporter) Logf(string, ...any)       {}
func (r *recordingReporter) Fatalf(f string, a ...any) { panic("fatal: " + f) }
