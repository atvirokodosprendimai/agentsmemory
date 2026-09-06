package repohygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
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
	const toTree = "repoRoot"
	// Both gates, because the markdown half arrived a day after the Go half and a
	// registration test that covers one of two is the same absence with a name.
	for _, g := range []struct{ gate, applies string }{
		{"TestNoCommentLineIsRepeatedVerbatim", "checkDuplicatedCommentLines"},
		{"TestNoMarkdownLineIsRepeatedVerbatim", "checkDuplicatedProseLines"},
	} {
		assertGateIsApplied(t, g.gate, g.applies, toTree)
	}
}

// assertGateIsApplied reads this file's own source and requires the named gate to
// hand the named check the repository root.
func assertGateIsApplied(t *testing.T, gate, applies, toTree string) {
	t.Helper()
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

// verificationEntryRE matches an `adr-verify` Verification Log line:
// `- 2026-09-01 · f1d3468* · exit 0 · ...`.
//
// ⚠ THIS EXCLUSION IS THE WHOLE REASON THE MARKDOWN HALF IS A SEPARATE DECISION
// rather than one HasSuffix on the Go gate. Measured over every tracked .md when
// it was written: 23 adjacent repetitions, 22 of them this one shape — an
// identical acceptance run appended twice — and ONE real prose duplicate. A naive
// extension is 95% false alarms on its first run, which is how a gate teaches
// people to ignore it.
//
// Two identical entries are either legitimate (the same acceptance re-run, which
// the log is entitled to record twice) or a defect in `adr-verify`'s append. Both
// are somebody else's question; neither is a wrap-tail write, which is the only
// thing this file judges.
//
// It matches the SHAPE rather than a `## Verification Log` heading on purpose: a
// section rule needs the heading state to be tracked correctly through fences and
// sub-headings, and the first draft of this measurement got that wrong and
// reported four of these as real.
var verificationEntryRE = regexp.MustCompile(`^-\s+\d{4}-\d{2}-\d{2}\s+·`)

// duplicatedProseLines is the markdown half. AGENTS.md §7's own record of this
// defect is in the ADR corpus — "Three such duplicates shipped in ADR-052's record
// and the repair produced a fourth" — so the Go gate was pointed at the smaller
// half of the tree.
//
// ⚠ FENCED BLOCKS ARE SKIPPED, and this file is why: a document explaining the
// defect SHOWS it, so the sample would be reported as an instance of itself.
func duplicatedProseLines(tb testing.TB, root string, ignored func(string, bool) bool) (dups []duplicateLine, scanned int) {
	tb.Helper()
	for _, path := range walk(tb, root, ignored) {
		if !strings.HasSuffix(path, ".md") {
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
		var prev string
		var fenced bool
		for i, raw := range strings.Split(string(src), "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
				prev = ""
				continue
			}
			if fenced {
				continue
			}
			scanned++
			if line != "" && line == prev && len(line) >= minDuplicateLen &&
				!verificationEntryRE.MatchString(line) {
				dups = append(dups, duplicateLine{file: rel, line: i + 1, text: line})
			}
			prev = line
		}
	}
	return dups, scanned
}

// checkDuplicatedProseLines is the markdown verdict, through a substitutable
// testing.TB for the reason the Go one is.
func checkDuplicatedProseLines(tb testing.TB, root string, ignored func(string, bool) bool) {
	tb.Helper()
	dups, scanned := duplicatedProseLines(tb, root, ignored)
	if scanned == 0 {
		tb.Errorf("the scan read no markdown line anywhere in the tree; that is not a clean bill " +
			"of health, it is a gate that did not run")
	}
	for _, d := range dups {
		tb.Errorf("%s:%d repeats the line above it verbatim:\n    %s\n"+
			"  AGENTS.md §7's wrap-tail write, in the corpus where that section recorded it. "+
			"Delete the second copy.", d.file, d.line, d.text)
	}
}

// TestNoMarkdownLineIsRepeatedVerbatim is the gate over the documents.
func TestNoMarkdownLineIsRepeatedVerbatim(t *testing.T) {
	root := repoRoot(t)
	checkDuplicatedProseLines(t, root, gitignoreMatcher(t, root))

	t.Run("a repeated prose line is caught", func(t *testing.T) {
		fixture := t.TempDir()
		body := "# A record\n\n" +
			"Repoint the two commands and run the existing suite; it is inside the fence.\n" +
			"Repoint the two commands and run the existing suite; it is inside the fence.\n\n" +
			"```\n" +
			"// a duplicated line shown as an EXAMPLE of the defect, not an instance\n" +
			"// a duplicated line shown as an EXAMPLE of the defect, not an instance\n" +
			"```\n\n" +
			"## Verification Log\n\n" +
			"- 2026-09-01 · f1d3468* · exit 0 · `set -o pipefail …` · acceptance-sha256:d5f7\n" +
			"- 2026-09-01 · f1d3468* · exit 0 · `set -o pipefail …` · acceptance-sha256:d5f7\n"
		if err := os.WriteFile(filepath.Join(fixture, "record.md"), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		rec := &recordingTB{}
		checkDuplicatedProseLines(rec, fixture, func(string, bool) bool { return false })
		if rec.errors != 1 {
			t.Fatalf("the gate reported %d finding(s) over a fixture with one real duplicate, one "+
				"inside a fence and one repeated acceptance entry; the two exclusions are what "+
				"make this gate obeyable rather than argued with", rec.errors)
		}

		blind := t.TempDir()
		if err := os.WriteFile(filepath.Join(blind, "empty.txt"), []byte("not markdown\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		mute := &recordingTB{}
		checkDuplicatedProseLines(mute, blind, func(string, bool) bool { return false })
		if mute.errors == 0 {
			t.Error("a scan that read no markdown at all reported success; an empty finding list " +
				"and a gate that never ran are byte-identical without the floor")
		}
	})
}
