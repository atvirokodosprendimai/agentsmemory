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
// failure needs one place to confirm the move was deliberate. ADR-037's DECISION
// records that an archive move re-scopes it on purpose — the Rollback section does
// not, and three references said it did. A pointer to the wrong section reads as
// provenance exactly as convincingly as a pointer to nothing, and this gate cannot
// see it: it proves the record EXISTS, never that the sentence about it is true.
//
// ⚠ WHEN THIS FIRES CORPUS-WIDE, suspect a retirement before a typo. `adr-retire`
// moves a record under an archive directory, which drops it from this glob and
// turns every citation to it red on one commit. Re-scope the constant; do not
// delete the citations.
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
// rename passes every test in the tree.
//
// ⚠ NO FROZEN COUNT LIVES HERE ANY MORE. Two shipped in the first draft — 283 in
// this comment and 287 in AGENTS.md — and BOTH were false at the commit carrying
// them: 283 was the count of the tree WITHOUT this file, taken before the number
// was written into it. The tree reports 294. A hand-maintained integrity number is
// not a check, it is a second source of truth, and nothing pinned either one. The
// gate logs the live figure on every -v run; read it there.
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

	checkCitations(t, root, ignored, records)

	t.Run("a citation naming no record is reported", aCitationNamingNoRecordIsReported)
}

// checkCitations is the gate's whole decision, reporting through a testing.TB.
//
// ⚠ THE TB PARAMETER IS THE POINT, and it took two attempts to get here. A test
// cannot pin its own reporting: with the scan inlined, replacing the offenders
// value with an empty slice left the fence green over a tree carrying a real
// unresolved citation — and the gate then reported "295 citations across 33
// distinct records, all resolved". Extracting the SCAN was not enough either,
// because the subtest still drove the scan while the gate's use of it stayed
// unguarded. Only routing the verdict through a TB the subtest can substitute
// makes the reporting itself mutable-and-caught.
func checkCitations(tb testing.TB, root string, ignored func(string, bool) bool, records map[string]bool) {
	tb.Helper()
	offenders, citations, distinct := offendersUnder(tb, root, ignored, records)

	// A scan that found nothing to check is a gate that cannot fail. This tree is
	// full of citations; zero means the walker or the regex stopped working, not
	// that the code went quiet.
	if citations == 0 {
		tb.Fatal("no ADR-NNN citation found in any .go file — this tree carries hundreds, so " +
			"zero means the walk or the pattern broke, and the gate is passing vacuously")
	}

	for _, o := range offenders {
		tb.Errorf("%s:%d cites %s, and no record %s/%s-*.md exists.\n"+
			"  A citation is the only pointer from this code to the reasoning behind it. "+
			"Either the record was renamed or never written, or the number is a typo.",
			o.file, o.line, o.number, adrCorpusDir, o.number)
	}
	if len(offenders) == 0 {
		tb.Logf("%d citations across %d distinct records, all resolved", citations, distinct)
	}
}

// recordingTB is a testing.TB that remembers whether the gate reported anything.
//
// It embeds testing.TB for the unexported-method requirement and overrides only
// what the gate calls, so a missing method panics loudly rather than silently
// doing nothing.
type recordingTB struct {
	testing.TB
	errors int
	fatal  bool
}

func (r *recordingTB) Helper()                   {}
func (r *recordingTB) Errorf(string, ...any)     { r.errors++ }
func (r *recordingTB) Logf(string, ...any)       {}
func (r *recordingTB) Fatalf(f string, a ...any) { r.fatal = true; panic("fatal: " + f) }
func (r *recordingTB) Fatal(a ...any)            { r.fatal = true; panic("fatal") }

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

// offendersUnder walks a tree, extracts every citation, and returns the ones that
// resolve to no record, with the totals for the report.
//
// ⚠ IT EXISTS SO THE DECISION IS TESTABLE AS A UNIT. The first version inlined this
// in the gate and the subtest exercised `citationsIn` and `unresolved` directly —
// which pins the helpers and leaves the path the verdict actually flows through
// covered by nothing. Review of PR #80 proved it with a mutant one line further out
// than the one recorded as killed: replacing the call with `var offenders []citation`
// left the fence green over a tree carrying a real unresolved citation, and the gate
// reported "295 citations across 33 distinct records, all resolved". A disabled gate
// that stays quiet is bad; one that affirmatively reports success is worse.
//
// That is this repository's signature defect — the component exercised instead of
// the selection — inside the change that extends the section naming it.
func offendersUnder(tb testing.TB, root string, ignored func(string, bool) bool, records map[string]bool) (offenders []citation, citations, distinct int) {
	tb.Helper()
	var all []citation
	for _, path := range walk(tb, root, ignored) {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		all = append(all, citationsIn(rel, string(src))...)
	}
	seen := map[string]bool{}
	for _, c := range all {
		seen[c.number] = true
	}
	return unresolved(all, records), len(all), len(seen)
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
// It is a SUBTEST of the gate rather than a sibling: the acceptance fence runs
// -run "TestEveryCitedADRResolves", so a sibling would sit outside the only command
// that has to pass, and a mutant would be recorded as killed by a fence that never
// ran it.
//
// ⚠ IT DRIVES offendersUnder AGAINST A REAL TREE, not the helpers in isolation, and
// that is the correction PR #80's review forced. Exercising `citationsIn` and
// `unresolved` directly pins the helpers and leaves the CALL SITE — the line the
// verdict actually flows through — covered by nothing: with `offenders := ...`
// replaced by `var offenders []citation`, the old subtest stayed green over a tree
// carrying a real unresolved citation, and the gate reported "all resolved".
//
// The fixture is a temporary tree holding one .go file with a bad citation, scanned
// with the same walker the gate uses, against a synthetic corpus. That pins walk →
// extract → resolve as one unit and leaves only the two-line reporting loop
// unguarded.
func aCitationNamingNoRecordIsReported(t *testing.T) {
	// A corpus that deliberately does not hold ADR-002. Real record numbers against
	// a synthetic corpus: a fixture citing a number nobody wrote would be flagged by
	// the corpus-wide scan above, since a fixture is Go source like any other.
	records := map[string]bool{"ADR-001": true, "ADR-038": true}

	dir := t.TempDir()
	src := "package fixture\n\n// Filed under ADR-001, superseded per ADR-038.\n" +
		"// Calibration follows ADR-002.\nfunc F() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "thing.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	none := func(string, bool) bool { return false }

	// ⚠ THE GATE'S OWN VERDICT PATH, driven through a recorder. Asserting on
	// offendersUnder alone pins the scan and leaves the reporting loop — the thing
	// that turns an offender into a failure — covered by nothing: PR #80's review
	// showed the fence staying green, and the gate announcing "all resolved", with
	// the offenders value replaced by an empty slice. A test cannot pin its own
	// t.Errorf, so the verdict is routed through a TB this can substitute.
	rec := &recordingTB{}
	checkCitations(rec, dir, none, records)
	if rec.errors != 1 {
		t.Fatalf("the gate reported %d errors over a tree with one unresolved citation, want 1 — "+
			"if this is 0 the decision is reachable from no test and a disabled gate would "+
			"announce success", rec.errors)
	}

	// And the same tree with a corpus that DOES hold every cited record must be
	// silent, or the check above passes for a gate that always errors.
	full := map[string]bool{"ADR-001": true, "ADR-038": true, "ADR-002": true}
	quiet := &recordingTB{}
	checkCitations(quiet, dir, none, full)
	if quiet.errors != 0 {
		t.Errorf("the gate reported %d errors over a fully-resolved tree", quiet.errors)
	}

	offenders, citations, distinct := offendersUnder(t, dir, none, records)

	if citations != 3 || distinct != 3 {
		t.Fatalf("scanned %d citations across %d records, want 3 and 3 — the walk or the "+
			"pattern changed and this fixture is no longer exercising what it claims",
			citations, distinct)
	}
	if len(offenders) != 1 {
		t.Fatalf("reported %d unresolved, want exactly 1: %+v", len(offenders), offenders)
	}
	if offenders[0].number != "ADR-002" {
		t.Errorf("reported %q; the unresolved one is the record this corpus does not hold",
			offenders[0].number)
	}
	if offenders[0].line != 4 {
		t.Errorf("reported line %d, want 4 — a report a reader cannot navigate to is a count, "+
			"not a finding", offenders[0].line)
	}
	if offenders[0].file != "thing.go" {
		t.Errorf("reported file %q, want the path relative to the scanned root", offenders[0].file)
	}

	// The bound on the match, driven rather than described. Without it a four-digit
	// number is reported as an unresolved three-digit record nobody wrote, and a
	// false alarm is how a gate gets switched off.
	if got := citationsIn("x.go", "// see ADR-0015 upstream"); len(got) != 0 {
		t.Errorf("a four-digit number matched as %+v; three digits must not match inside a longer run", got)
	}
}
