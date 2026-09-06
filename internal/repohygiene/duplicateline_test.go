package repohygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AGENTS.md §7 records the write that produces this: mrw replaces a wrapped line
// with re-wrapped text and KEEPS the old wrap's tail, and the receipt says `ok`
// because the write succeeded, not because the file says what was meant. The
// remedy it prescribes is to READ THE RESULT — which works exactly as often as
// somebody remembers.
//
// Nothing caught it. `internal/doclint` is green over a duplicated line, because
// the declaration is still documented; `gofmt` reformats it happily; the compiler
// has no opinion about prose. Three instances were sitting in the tree when this
// gate was written, in three different packages, one of them in the fix for a
// review finding about a different stale comment.
//
// The rule is narrow on purpose: two ADJACENT comment lines with identical text.
// A repeated `//` separator, a box-drawing rule, an indented code sample inside a
// comment — none of those survive that test, because the duplicate this catches is
// a whole sentence fragment appearing twice in a row, which no author writes.

// duplicateLine is one repetition: the second of two identical adjacent comment
// lines, which is the one to delete.
type duplicateLine struct {
	file string
	line int
	text string
}

// minDuplicateLen is how much comment text must repeat before it counts.
//
// A short line repeats legitimately — `//`, `// ---`, `// }` closing a sample —
// and a gate that flagged those would be argued with rather than obeyed. The
// wrap-tail defect leaves a fragment of prose, which is long. Measured against the
// three instances in the tree when this was written: 46, 60 and 70 characters.
const minDuplicateLen = 24

// duplicatedCommentLines walks the tree and returns every adjacent repetition.
//
// It reads COMMENTS from the parsed file rather than grepping lines, so a string
// literal that happens to contain two identical `//` lines — a test fixture of a
// broken file, for instance — is not a finding. That distinction is not
// hypothetical: this package's own fixtures hold exactly that shape.
func duplicatedCommentLines(tb testing.TB, root string, ignored func(string, bool) bool) (dups []duplicateLine, scanned int) {
	tb.Helper()
	fset := token.NewFileSet()
	for _, path := range walk(tb, root, ignored) {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			// Unparseable is the build's problem, not this gate's.
			continue
		}
		for _, group := range f.Comments {
			var prev string
			for _, c := range group.List {
				text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
				scanned++
				if text != "" && text == prev && len(text) >= minDuplicateLen {
					dups = append(dups, duplicateLine{
						file: rel,
						line: fset.Position(c.Pos()).Line,
						text: text,
					})
				}
				prev = text
			}
		}
	}
	return dups, scanned
}

// checkDuplicatedCommentLines is the verdict, through a substitutable testing.TB.
//
// ⚠ THE FLOOR ON `scanned` IS NOT DECORATION. An empty finding list is what a
// clean tree looks like AND what a walk that found no files looks like, and those
// must not be the same answer. It does not catch the deletion of the call to this
// function — no assertion inside a function can — which is what the sibling
// registration gate is for.
func checkDuplicatedCommentLines(tb testing.TB, root string, ignored func(string, bool) bool) {
	tb.Helper()
	dups, scanned := duplicatedCommentLines(tb, root, ignored)
	if scanned == 0 {
		tb.Errorf("the scan read no comment line anywhere in the tree; that is not a clean bill " +
			"of health, it is a gate that did not run")
	}
	for _, d := range dups {
		tb.Errorf("%s:%d repeats the comment line above it verbatim:\n    %s\n"+
			"  This is AGENTS.md §7's wrap-tail write: a re-wrapped replacement kept the old "+
			"wrap's tail and the receipt still said ok. Delete the second copy.",
			d.file, d.line, d.text)
	}
}

// TestNoCommentLineIsRepeatedVerbatim is the gate.
func TestNoCommentLineIsRepeatedVerbatim(t *testing.T) {
	root := repoRoot(t)
	checkDuplicatedCommentLines(t, root, gitignoreMatcher(t, root))

	t.Run("a repeated comment line is caught", func(t *testing.T) {
		fixture := t.TempDir()
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(fixture, name), []byte(body), 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		// One offender, plus every shape that must NOT be one: a short repeat, a
		// bare separator, the same sentence twice with a line between them, and a
		// duplicated line inside a STRING rather than a comment.
		write("a.go", "package p\n\n"+
			"// the caller is what fixes this, and the sentence was re-wrapped here\n"+
			"// the caller is what fixes this, and the sentence was re-wrapped here\n"+
			"func one() {}\n\n"+
			"//\n"+
			"//\n"+
			"// short\n"+
			"// short\n"+
			"func two() {}\n\n"+
			"// a sentence long enough to be judged by this gate, said once\n"+
			"//\n"+
			"// a sentence long enough to be judged by this gate, said once\n"+
			"func three() {}\n\n"+
			"const fixtureOfABrokenFile = `\n"+
			"// a duplicated line that lives inside a string literal, not a comment\n"+
			"// a duplicated line that lives inside a string literal, not a comment\n"+
			"`\n")

		rec := &recordingTB{}
		checkDuplicatedCommentLines(rec, fixture, func(string, bool) bool { return false })
		if rec.errors != 1 {
			t.Fatalf("the gate reported %d finding(s) over a fixture carrying exactly one "+
				"wrap-tail duplicate beside four shapes that are not; a gate that cannot "+
				"tell them apart is one somebody switches off", rec.errors)
		}

		// And the floor, over a tree with no comment to read.
		blind := t.TempDir()
		if err := os.WriteFile(filepath.Join(blind, "b.go"), []byte("package p\n\nfunc f() {}\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		mute := &recordingTB{}
		checkDuplicatedCommentLines(mute, blind, func(string, bool) bool { return false })
		if mute.errors == 0 {
			t.Error("a scan that read no comment at all reported success; an empty finding list " +
				"and a gate that never ran are byte-identical without the floor")
		}
	})
}

// TestTheDuplicateLineGateIsAppliedToTheTree is the rung the gate cannot reach
// itself: delete the one line that applies it to this repository and its fixtures
// keep passing, with the gate's own name printed as PASS. Demonstrated on the
// sibling gate in review of PR #316, which is why it is written on the same day as
// the gate rather than after the first time somebody deletes the line.
func TestTheDuplicateLineGateIsAppliedToTheTree(t *testing.T) {
	const (
		gate    = "TestNoCommentLineIsRepeatedVerbatim"
		applies = "checkDuplicatedCommentLines"
		toTree  = "repoRoot"
	)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "duplicateline_test.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse this file: %v", err)
	}
	var applied, rooted bool
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != gate || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			switch id.Name {
			case applies:
				for _, arg := range call.Args {
					if inner, ok := arg.(*ast.Ident); ok && inner.Obj != nil && inner.Name == "root" {
						applied = true
					}
				}
			case toTree:
				rooted = true
			}
			return true
		})
	}
	if !rooted {
		t.Errorf("%s does not call %s, so whatever it checks, it is not this repository", gate, toTree)
	}
	if !applied {
		t.Errorf("%s never applies %s to the repository root; its fixtures would still pass and "+
			"the package would still report PASS over a tree carrying real duplicates",
			gate, applies)
	}
}
