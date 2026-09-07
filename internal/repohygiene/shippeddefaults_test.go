package repohygiene

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// defaultsFile is the one place a shipped default lives.
//
// Named rather than inlined for the reason adrCorpusDir is: moving it turns this
// gate red in one step, and a reader looking at that failure needs one place to
// confirm the move was deliberate. `config.Default()` being the sole home of a
// default is itself an invariant ADR-014 relies on — it changed the defaults
// "there, nowhere else" — so a second home would break more than this test.
const defaultsFile = "internal/config/config.go"

// measurementClaim matches a comment that says a default was CHOSEN BY EVIDENCE
// rather than picked.
//
// The vocabulary is deliberately narrow, and narrowness is the whole design. A
// gate over doc comments is only as good as its false-alarm rate — `internal/doclint`'s
// own comment records why a word-count rule for comment quality was measured and
// rejected — so this matches the small set of words that cannot mean anything
// except "a measurement happened": a claim to have measured, a sweep, a benchmark,
// and the two figures this project's evals actually report. A comment that merely
// explains a choice ("duplicated to keep config dependency-free", "derive
// max(4, NumCPU()) at open") says nothing about evidence and is not in the
// universe at all.
//
// ⚠ IT IS A FLOOR, NOT A CLASSIFIER, AND SAYING SO IS THE POINT. A default
// measured and described without any of these words is invisible here. That is
// accepted: this gate exists to stop an ATTRIBUTED-LOOKING claim from being
// unattributable, and a claim phrased so vaguely that no word marks it as a
// measurement is a different defect, which only a reader can catch.
var measurementClaim = regexp.MustCompile(`(?i)\bmeasured\b|\bsweep\b|\bbenchmark|\brecall@|\bMRR\b|[0-9]+(?:\.[0-9]+)?%`)

// evidencePointer matches what a measurement claim must carry: the record the
// measurement lives in (ADR-032), the identity of the questions it ran against
// (a case set), or the date it was taken.
//
// Three forms rather than one BECAUSE THE CORPUS HAS THREE HONEST CASES, and a
// rule admitting only the first would demand a record be written before a comment
// could state a fact. `RerankWeight` and `RerankNorm` each implement a decision an
// ADR took, so they cite it. `RerankTimeout` records a latency measurement — a
// pool of 50 costing ~22s on a CPU cross-encoder — that no record owns and for
// which a case-set id is meaningless, since it measured wall-clock rather than
// ranking; the date plus the conditions IS its attribution.
//
// The first form composes with TestEveryCitedADRResolves, which already fails when
// a cited record does not exist. So this gate never has to ask whether the ADR is
// real — that is somebody else's exhaustive job, and duplicating it here would be
// a second source of truth for the same question.
var evidencePointer = regexp.MustCompile(`ADR-[0-9]{3}|case[ _-]set|[0-9]{4}-[0-9]{2}-[0-9]{2}`)

// TestShippedDefaultsCiteTheirCorpus is ADR-032 T2's gate, planned there and never
// written because it "belongs with a default change and there was none".
//
// A default annotated as measured is a claim about evidence, and it is worth
// exactly as much as a reader's ability to go and find that evidence. Without a
// pointer the claim OUTLIVES THE CORPUS THAT PRODUCED IT: `RerankWeight: 0.5` said
// "chosen by the eval's weight sweep" and named no corpus, and ADR-032 then
// measured that the sweep ran on a paraphrase corpus which could not disagree with
// it — on the real corpus a LOWER weight wins. Both sentences were true; nothing in
// the tree connected them, so a reader of the literal had no way to learn the
// second existed.
//
// This is §Reachability's shape applied to prose rather than to wiring: the value
// is served, the comment is emitted, every existing gate is green, and the
// evidence is unreachable from the thing it justifies.
//
// ⚠ ITS UNIT IS THE FIELD, NOT THE CLAIM, AND A SURVIVED MUTANT IS HOW THAT WAS
// LEARNED. Deleting one of the two ADR-030 mentions in RerankNorm's comment left
// this green, because the other still matched — so a comment making TWO
// measurement claims and citing evidence for only one passes. Splitting the unit
// would mean deciding which sentence a pointer belongs to, which is the semantic
// judgement §Reachability keeps recording as review's job and not a linter's. The
// two mutants that DO kill it are the honest ones: strip every pointer from either
// field and it names that field, its position and what to add.
func TestShippedDefaultsCiteTheirCorpus(t *testing.T) {
	root := repoRoot(t)
	// The EMPTY-UNIVERSE guard lives here, at the caller, rather than inside the
	// helper: "this gate examined nothing" is a fact about the run, and a run that
	// examined nothing must not report that every default is attributed. Keeping it
	// in the body also means the test itself carries a failure call, which
	// `adr-lint` requires of a test a done task names. Measured 2026-09-07 across
	// three shapes, because the obvious explanation is wrong: a helper taking
	// `testing.TB` and one taking `*testing.T` BOTH still trip "nothing in it can go
	// red" when the body only delegates. The detector does not follow the failure
	// call into a same-file helper at all — so the parameter type is a red herring,
	// and reaching for `*testing.T` to satisfy it does measurably nothing.
	if checked := unattributed(t, filepath.Join(root, defaultsFile)); checked == 0 {
		t.Fatalf("%s yielded no defaults to check; an empty universe is indistinguishable "+
			"from every default being attributed", defaultsFile)
	}
}

// unattributed reports every default whose comment claims a measurement and names
// no evidence.
//
// It takes a testing.TB rather than a *testing.T so the falsifiability subtest can
// substitute a recorder and prove the gate REPORTS — a test cannot pin its own
// reporting, and without the shim a severed call site leaves the suite green while
// the gate announces that everything is attributed. That failure is not
// hypothetical in this package: TestAHumanObservedSignOffAgreesWithTheIndex shipped
// with exactly it.
func unattributed(tb testing.TB, path string) (checked int) {
	tb.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}

	lit := defaultsLiteral(file)
	if lit == nil {
		// Reported as zero rather than fatal here: the CALLER owns "nothing was
		// examined", so the falsifiability test can observe it without a panic.
		return 0
	}

	// The comment map is built over the WHOLE file and then consulted per field,
	// because a default's explanation is written above it as a free-floating
	// comment, not as a Doc on the KeyValueExpr — go/ast attaches neither Doc nor
	// Comment to an element of a composite literal.
	cmap := ast.NewCommentMap(fset, file, file.Comments)

	var offenders []string
	claiming := 0
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		checked++
		text := commentsFor(cmap, kv)
		if !measurementClaim.MatchString(text) {
			continue
		}
		claiming++
		if evidencePointer.MatchString(text) {
			continue
		}
		offenders = append(offenders, fmt.Sprintf("%s (%s)", name.Name, fset.Position(kv.Pos())))
	}

	sort.Strings(offenders)
	for _, o := range offenders {
		tb.Errorf("%s claims a measurement and names no evidence.\n"+
			"  Cite the record the measurement lives in (ADR-0NN), the case set it ran against, or the date it was\n"+
			"  taken. Without one the claim outlives the corpus that produced it: a later run can refute the number\n"+
			"  and a reader of this literal has no route to that.", o)
	}
	if len(offenders) == 0 {
		tb.Logf("%d default(s), %d claiming a measurement, all attributed", checked, claiming)
	}
	return checked
}

// defaultsLiteral returns the Config composite literal returned by func Default(),
// or nil.
//
// It reads the RETURN rather than trusting the function's name to imply a shape,
// so a Default() rewritten to build its value in steps is reported as an empty
// universe by the caller instead of silently passing.
func defaultsLiteral(file *ast.File) *ast.CompositeLit {
	var found *ast.CompositeLit
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Default" || fn.Recv != nil || fn.Body == nil {
			return true
		}
		for _, stmt := range fn.Body.List {
			ret, ok := stmt.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			if lit, ok := ret.Results[0].(*ast.CompositeLit); ok {
				found = lit
			}
		}
		return false
	})
	return found
}

// commentsFor returns every comment attached to one field of the literal, joined.
//
// ⚠ THE TRAILING COMMENT AND THE LEADING BLOCK ARE BOTH LOAD-BEARING AND THEY
// ATTACH DIFFERENTLY. `RerankWeight: 0.5, // chosen by the eval's weight sweep`
// carries its claim on the same line, while RerankNorm's sits in a block above it;
// a version reading only one of the two reports half the corpus as clean.
func commentsFor(cmap ast.CommentMap, kv *ast.KeyValueExpr) string {
	var b strings.Builder
	for _, group := range cmap[kv] {
		b.WriteString(group.Text())
		b.WriteString(" ")
	}
	for _, group := range cmap[kv.Value] {
		b.WriteString(group.Text())
		b.WriteString(" ")
	}
	return b.String()
}

// TestADefaultThatCitesNothingIsCaught is the falsifiability half, and it drives
// the REAL predicate over fixtures that are offenders rather than a copy of it.
//
// A corpus with zero unattributed defaults cannot exercise the branch that reports
// one, so without this the gate could be severed and would go on announcing that
// every default is attributed. That is not a hypothetical failure in this package:
// TestASpecBindingThatNamesNothingIsCaught and
// TestAHumanObservedSignOffAgreesWithTheIndex both record a first draft that
// reimplemented the check and stayed green while the real one was disabled.
//
// It is a SUBTEST rather than a sibling because a fence runs one test name, and a
// falsifiability case parked outside it is one a `-run` filter silently skips.
//
// Both directions are asserted. A matcher that flags everything would pass the
// first case and is the failure mode that gets a gate deleted — this package has
// already had one such incident (issue #16, the AGENTS.md gate false-positiving on
// every fresh install).
func TestADefaultThatCitesNothingIsCaught(t *testing.T) {
	const header = "package config\n\ntype Config struct{ W float64 }\n\nfunc Default() Config {\n\treturn Config{\n"
	const footer = "\t}\n}\n"

	for _, tc := range []struct {
		name   string
		body   string
		report bool
	}{
		{
			name:   "a measurement claim with no evidence is reported",
			body:   "\t\t// chosen by the eval's weight sweep\n\t\tW: 0.5,\n",
			report: true,
		},
		{
			name:   "the same claim citing a record is not",
			body:   "\t\t// chosen by the eval's weight sweep; ADR-032 names the corpus\n\t\tW: 0.5,\n",
			report: false,
		},
		{
			name:   "a dated latency measurement is attributed by its date",
			body:   "\t\t// Measured 2026-08-21: a pool of 50 costs ~22s on a CPU cross-encoder.\n\t\tW: 0.5,\n",
			report: false,
		},
		{
			// The universe is claims, not comments. A default explained without
			// asserting evidence is not something this gate has any opinion about,
			// and flagging it would make the gate noise.
			name:   "a comment that explains without claiming evidence is not in the universe",
			body:   "\t\t// duplicated here to keep config dependency-free\n\t\tW: 0.5,\n",
			report: false,
		},
		{
			// The trailing form attaches to the VALUE rather than the KeyValueExpr,
			// so a version reading only leading comments reports this as clean.
			name:   "a trailing comment carries its claim too",
			body:   "\t\tW: 0.5, // chosen by the eval's weight sweep\n",
			report: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.go")
			if err := os.WriteFile(path, []byte(header+tc.body+footer), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			rec := &recordingTB{TB: t}
			unattributed(rec, path)
			if got := rec.errors > 0; got != tc.report {
				t.Errorf("reported=%v, want %v — the gate does not decide this fixture the way it decides the corpus", got, tc.report)
			}
		})
	}

	// An empty universe must be FATAL rather than a pass: a Default() that stopped
	// returning a literal would otherwise make this gate report success over a file
	// it understood nothing about.
	t.Run("a Default that returns no literal is fatal, not clean", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.go")
		if err := os.WriteFile(path, []byte("package config\n\ntype Config struct{ W float64 }\n\nfunc Default() Config {\n\tc := Config{}\n\treturn c\n}\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		rec := &recordingTB{TB: t}
		if n := unattributed(rec, path); n != 0 {
			t.Errorf("a Default() building its value in steps reported %d checked; an empty universe must be 0 so the caller can fail it", n)
		}
	})
}
