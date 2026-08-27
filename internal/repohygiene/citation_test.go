package repohygiene

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// adrCorpusDir is where a record lives, as ONE named constant.
//
// It is named rather than inlined because moving the corpus is the change that
// turns this gate red across the whole tree at once, and a reader looking at that
// failure needs one place to confirm the move was deliberate. ADR-037's Rollback
// section records that an archive move re-scopes it on purpose.
const adrCorpusDir = "docs/adr"

// citedADR matches a record number in Go source.
//
// The trailing digit exclusion is load-bearing: without it a four-digit number
// matches on its first three, and the gate reports an unresolved record nobody
// wrote — the worst failure a hygiene check can have, because a false alarm teaches
// people to skip it.
//
// This comment used to demonstrate that with a literal example, and the gate
// flagged its own explanation on the first run. Kept as prose rather than fixed
// with an exclusion, because a scanner that skips its own package is a scanner with
// a blind spot at exactly the place someone will put a citation.
var citedADR = regexp.MustCompile(`ADR-([0-9]{3})(?:[^0-9]|$)`)

// TestEveryCitedADRResolves is ADR-037 T1.
//
// A doc comment citing ADR-031 is the only pointer a reader gets from the code to
// the reasoning behind it, and it is worth exactly as much as the record it names.
// A citation that resolves to nothing is worse than none: it reads as provenance,
// costs a reader the search, and there is no way to tell a typo from a record that
// was renamed or never written.
//
// Nothing caught this class before. adr-lint reads record-to-record cross
// references and never looks at Go source; go vet does not know what an ADR is; a
// rename passes every test in the tree. Measured on this tree 2026-08-27: 283
// citations across 31 distinct records, 0 unresolved — so the gate ships green and
// its first red will be a real one.
//
// It judges RESOLUTION and nothing else. Whether a comment is long enough, or
// present at all, was measured and rejected in ADR-037's Alternatives; those are
// judgements a test would make badly.
func TestEveryCitedADRResolves(t *testing.T) {
	root := repoRoot(t)
	ignored := gitignoreMatcher(t, root)

	// The same view of the tree the other hygiene checks read. A hand-kept list of
	// files would drift the moment somebody adds a package, and drift silently:
	// a file the scan never opens has no citations to report.
	records := recordNumbers(t, root)
	if len(records) == 0 {
		t.Fatalf("no records found under %s/ — either the corpus moved and this gate needs "+
			"re-scoping, or the glob stopped matching. Both are worth a look before deleting "+
			"this test, because with no records EVERY citation resolves to nothing and this "+
			"check would be shouting rather than working", adrCorpusDir)
	}

	var all []citation
	for _, path := range walk(t, root, ignored) {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		all = append(all, citationsIn(rel, string(src))...)
	}
	offenders := unresolved(all, records)
	distinct := map[string]bool{}
	for _, c := range all {
		distinct[c.number] = true
	}
	citations := len(all)

	// A scan that found nothing to check is a gate that cannot fail. This tree is
	// full of citations; zero means the walker or the regex stopped working, not
	// that the code went quiet.
	if citations == 0 {
		t.Fatal("no ADR-NNN citation found in any .go file — this tree carries hundreds, so " +
			"zero means the walk or the pattern broke, and the gate is passing vacuously")
	}

	for _, o := range offenders {
		t.Errorf("%s:%d cites %s, and no record %s/%s-*.md exists.\n"+
			"  A citation is the only pointer from this code to the reasoning behind it. "+
			"Either the record was renamed or never written, or the number is a typo.",
			o.file, o.line, o.number, adrCorpusDir, o.number)
	}
	if len(offenders) == 0 {
		t.Logf("%d citations across %d distinct records, all resolved", citations, len(distinct))
	}

	t.Run("a citation naming no record is reported", aCitationNamingNoRecordIsReported)
}

// recordNumbers reads the corpus itself for which records exist, rather than
// taking a number range on trust: a range would accept a gap where a record was
// withdrawn, which is exactly the citation this gate exists to catch.
func recordNumbers(t *testing.T, root string) map[string]bool {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, adrCorpusDir, "ADR-*"))
	if err != nil {
		t.Fatalf("glob %s: %v", adrCorpusDir, err)
	}
	out := map[string]bool{}
	for _, m := range matches {
		// Both shapes count: a record is ADR-NNN-slug.md, and a split-task record
		// also has an ADR-NNN-slug/ directory beside it. Requiring the .md alone
		// would be right today and brittle the day a record ships as a directory.
		name := filepath.Base(m)
		if len(name) >= 7 && strings.HasPrefix(name, "ADR-") {
			out[name[:7]] = true
		}
	}
	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	t.Logf("corpus holds %d records: %s", len(names), strings.Join(names, " "))
	return out
}

// citation is one ADR-NNN occurrence, with enough to point a reader at it.
type citation struct {
	file, number string
	line         int
}

// citationsIn extracts every record number cited in one file's source.
//
// Split out from the walk so the decision below can be driven by a fixture. The
// corpus has zero unresolved citations — which is the goal, and which also means
// the corpus can never exercise the branch that reports one. A gate whose failure
// path no input reaches is not a gate; see the subtest.
func citationsIn(file, src string) []citation {
	var out []citation
	for i, line := range strings.Split(src, "\n") {
		for _, m := range citedADR.FindAllStringSubmatch(line, -1) {
			out = append(out, citation{file: file, number: "ADR-" + m[1], line: i + 1})
		}
	}
	return out
}

// unresolved returns the citations naming no record in the corpus.
func unresolved(cites []citation, records map[string]bool) []citation {
	var out []citation
	for _, c := range cites {
		if !records[c.number] {
			out = append(out, c)
		}
	}
	return out
}

// aCitationNamingNoRecordIsReported drives the branch the corpus cannot.
//
// It is a SUBTEST of the gate rather than a sibling, and that is not cosmetic: the
// acceptance fence runs -run "TestEveryCitedADRResolves", so a sibling would sit
// outside the only command that has to pass, and a mutant breaking the resolve
// check would be recorded as killed by a fence that never ran it.
//
// It exists because the first version of this gate could not fail. With zero
// unresolved citations in the tree, deleting the resolve check left the whole test
// green, so a mutation pass proved nothing about the only decision it makes.
//
// ⚠ EVERY NUMBER HERE NAMES A REAL RECORD, and the CORPUS is the synthetic half.
// The first draft cited a number nobody had written, and the corpus-wide scan above
// flagged this file — correctly, because a fixture is Go source like any other and
// an unresolved citation in it is still unresolved. Making the records synthetic
// instead tests the same decision, leaves the tree honest, and tests something
// stronger: that resolution is decided against the corpus that exists rather than
// against a list kept beside it.
func aCitationNamingNoRecordIsReported(t *testing.T) {
	// A corpus that deliberately does not hold ADR-002.
	records := map[string]bool{"ADR-001": true, "ADR-038": true}

	src := "// Filed under ADR-001, superseded per ADR-038.\n" +
		"// Calibration follows ADR-002.\n"
	cites := citationsIn("internal/example/thing.go", src)
	if len(cites) != 3 {
		t.Fatalf("found %d citations in the fixture, want 3 — the pattern changed and the "+
			"corpus scan is now reading a different tree than this test believes", len(cites))
	}

	bad := unresolved(cites, records)
	if len(bad) != 1 {
		t.Fatalf("reported %d unresolved, want exactly 1: %+v", len(bad), bad)
	}
	if bad[0].number != "ADR-002" {
		t.Errorf("reported %q; the unresolved one is the record this corpus does not hold", bad[0].number)
	}
	if bad[0].line != 2 {
		t.Errorf("reported line %d, want 2 — a report a reader cannot navigate to is a count, "+
			"not a finding", bad[0].line)
	}

	// The bound on the match, driven rather than described. Without it a four-digit
	// number is reported as an unresolved three-digit record nobody wrote, and a
	// false alarm is how a gate gets switched off.
	if got := citationsIn("x.go", "// see ADR-0015 upstream"); len(got) != 0 {
		t.Errorf("a four-digit number matched as %+v; three digits must not match inside a longer run", got)
	}
}
