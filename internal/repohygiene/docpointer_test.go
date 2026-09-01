package repohygiene

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docCitedADRExemptions are the places a doc names a record NUMBER without
// pointing at a record, with the reason each one is not a broken pointer.
//
// ⚠ A MENTION IS NOT A POINTER, and this is the whole difficulty of gating prose.
// `TestEveryCitedADRResolves` needs no such list because Go source has no reason to
// discuss a number it is not referring to. Docs do: a Numbering line says which
// numbers are taken, and a record about the citation gate has to show what a
// failing citation looks like. Every unresolved citation in this corpus at the time
// the gate was written was a MENTION, not a pointer — so a gate shipped without this
// list would have been a false alarm on every single one on day one, which is how a
// gate gets switched off, and this repository has already had one such incident.
//
// ⚠ NO COUNT HERE, DELIBERATELY. The figures this comment carried were the
// origin/main figures, frozen into a comment dated with the head's date — the exact
// mechanism `citation_test.go` records against itself. `go test ./internal/repohygiene
// -run TestEveryCitedADRResolvesInDocsToo -v` prints the live ones on every run.
//
// Keyed by file AND NUMBER, valued by reason. TestDocCitedADRExemptionsAreJustified
// refuses an empty reason and an entry that no longer earns its place.
//
// ⚠ THE NUMBER IS HALF THE KEY, and this comment used to say "keyed by file" above a
// correction saying it was not — the superseded sentence left standing with the
// correction stacked under it, which is the drift class this whole file exists to
// gate, one file in. Keying by file alone skipped the whole file: the
// history record carries dozens of real citations beside the mentions exempted here
// — the two entries below are the whole exemption — so file scope took every one of
// those working pointers out of the gate to hide two words. Appending a citation to
// a record that does not exist then passed green. An exemption must hide exactly
// what it names.
//
// ⚠ AND THE NUMBER IS STORED BARE, without its `ADR-` prefix, because writing the
// prefixed form here would make THIS FILE cite a record that does not exist — and
// `TestEveryCitedADRResolves` reads Go source. It caught exactly that on the first
// attempt at this map. The doc gate's exemption list cannot spell the thing it
// exempts, which is a small, real constraint the two gates place on each other and
// is worth knowing before someone "tidies" these keys.
//
// file -> bare record number -> why it is a mention rather than a pointer.
var docCitedADRExemptions = map[string]map[string]string{
	"docs/adr/ADR-026-a-history-you-cannot-query.md": {
		"022": "its Numbering line states which numbers an open PR still claims — a statement " +
			"about allocation, not a reference to a record",
		"023": "the same Numbering line, second number",
	},
	"docs/adr/ADR-037-the-why-travels-with-the-code.md": {
		"999": "it shows a deliberately unresolvable record number as the failing example, in " +
			"the record that introduced the citation gate: the number has to resolve to nothing " +
			"for the point to land",
	},
	"docs/adr/ADR-037-the-why-travels-with-the-code/tasks/T1-every-cited-adr-resolves.md": {
		"999": "the same unresolvable-number fixture, in that task's Risks table about the regex " +
			"over-matching",
	},
}

// TestEveryCitedADRResolvesInDocsToo extends the citation gate's universe from Go
// source to the tracked documentation corpus.
//
// ⚠ THE GO GATE'S UNIVERSE IS `.go` AND ONLY `.go` — its walk skips anything whose
// name does not end in `.go`, in `offendersUnder` — so a
// record renamed or withdrawn is caught where a doc comment cites it and missed
// where an ADR, a task file, the README or the backlog does — which is where the
// large majority of this corpus's citations live. A pointer to nothing reads as
// provenance wherever it is written. Both universes are counted in the `-v` output
// of each gate rather than written down here, because a count written down is false
// at the commit that carries it.
//
// This is a sibling rather than a widening of the existing gate on purpose:
// `AGENTS.md` describes that gate's universe, its Go-only scope is what makes it
// need no exemptions, and a gate whose name claims more than it covers is worse
// than a narrower one.
func TestEveryCitedADRResolvesInDocsToo(t *testing.T) {
	root := repoRoot(t)
	checkDocCitations(t, root, gitignoreMatcher(t, root), recordNumbers(t, root))

	// ⚠ A SUBTEST, NOT A SIBLING, for the reason `citation_test.go` already gives:
	// an acceptance fence selects by test NAME, and AGENTS.md names the gates rather
	// than their falsifiability halves. A sibling can be skipped by a fence that
	// looks complete; a subtest cannot.
	t.Run("a doc citing no record is reported", aDocCitingNoRecordIsReported)
}

// checkDocCitations is the whole verdict path, taking a testing.TB so the
// falsifiability half below can substitute one. Pinning only a helper leaves the
// walk uncovered — the trap this package has now hit twice.
func checkDocCitations(tb testing.TB, root string, ignored func(string, bool) bool, records map[string]bool) {
	tb.Helper()
	var offenders []citation
	seen := map[string]bool{}
	total := 0
	for _, path := range walk(tb, root, ignored) {
		if !strings.HasSuffix(path, ".md") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		src, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		found := citationsIn(filepath.ToSlash(rel), string(src))
		total += len(found)
		for _, c := range found {
			seen[c.number] = true
		}
		for _, o := range unresolved(found, records) {
			if _, exempt := docCitedADRExemptions[o.file][strings.TrimPrefix(o.number, "ADR-")]; exempt {
				continue
			}
			offenders = append(offenders, o)
		}
	}

	// A scan that found nothing to check is a gate that cannot fail. This corpus
	// carries over a thousand doc citations; zero means the walk or the pattern
	// broke, not that the docs went quiet.
	if total == 0 {
		tb.Fatal("no ADR-NNN citation found in any tracked .md file — this corpus carries " +
			"more than a thousand, so zero means the walk or the pattern broke and the gate is " +
			"passing vacuously")
	}

	for _, o := range offenders {
		tb.Errorf("%s:%d cites %s, and no record %s/%s-*.md exists.\n"+
			"  A citation is the only route from this prose to the reasoning behind it. Either the "+
			"record was renamed or withdrawn and the prose was left behind, or the number is a typo. "+
			"If the doc is NAMING the number rather than pointing at a record, add it to "+
			"docCitedADRExemptions with the reason.",
			o.file, o.line, o.number, adrCorpusDir, o.number)
	}
	if len(offenders) == 0 {
		tb.Logf("%d ADR citations across %d distinct records in the doc corpus, all resolved", total, len(seen))
	}
}

// TestDocCitedADRExemptionsAreJustified refuses an exemption without a written
// reason, and one that has stopped earning its place.
//
// The escape hatch is where a gate goes to die: an entry added to silence a finding,
// with no reason, outlives whatever justified it. The reason is the review.
func TestDocCitedADRExemptionsAreJustified(t *testing.T) {
	root := repoRoot(t)
	records := recordNumbers(t, root)
	for file, numbers := range docCitedADRExemptions {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Errorf("%s is exempt but does not exist: %v\nRemove the entry.", file, err)
			continue
		}
		live := map[string]bool{}
		for _, o := range unresolved(citationsIn(file, string(body)), records) {
			live[strings.TrimPrefix(o.number, "ADR-")] = true
		}
		for number, reason := range numbers {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s exempts record %s with no reason given", file, number)
				continue
			}
			if !live[number] {
				t.Errorf("%s no longer cites an unresolved record %s, so that exemption is dead "+
					"weight.\nRemove it — an exemption nobody needs is one nobody re-reads.", file, number)
			}
		}
	}
}

// anyDocLineCiteRE finds every `<some/path>.md:<n>` in a document. Which of them
// are SELF-citations is decided against the corpus, not by the pattern.
var anyDocLineCiteRE = regexp.MustCompile(`([A-Za-z0-9_./-]*[A-Za-z0-9_-]\.md):(\d+)\b`)

// citesItself reports whether a line citation written in `from` points back at
// `from` itself.
//
// ⚠ A BASENAME IS NOT AN IDENTITY IN THIS CORPUS. The first version compared
// `filepath.Base`, and this tree holds 31 files called README.md and 28 called
// CLAUDE.md — so one README citing ANOTHER by line read as a self-citation.
// Reproduced: appending a correct, cross-file "`README.md:19`" pointer to
// clients/claude-code/README.md turned the gate red, with an error telling the
// author to cite a heading instead. That pointer was right, and the corpus already
// carries the same shape one file away, in ADR-003 T5.
//
// A false alarm is the worst failure a hygiene gate has — this PR's own entry says
// so — and two documents in this very change asserted this gate "cannot cry wolf".
// It can, unless the comparison is by PATH: a citation is a self-reference only
// when the path it names resolves to the citing file and to nothing else.
func citesItself(from, cited string, docs map[string]bool) bool {
	cited = filepath.ToSlash(cited)

	// An exact repo-relative path names one file and there is nothing to resolve.
	if cited == from {
		return true
	}

	// A path with a separator is read relative to the citing file's directory,
	// which is unambiguous.
	if strings.Contains(cited, "/") {
		return path.Join(path.Dir(from), cited) == from
	}

	// ⚠ A BARE BASENAME IS RESOLVED ONLY IF THE CORPUS HOLDS EXACTLY ONE FILE IT CAN
	// MEAN. It is tempting to read it as the sibling in the citing file's own
	// directory — and that reading is what produced the blocker: `sub/README.md`
	// saying "the top-level `README.md:2`" resolved to itself and was reported,
	// when the author plainly meant a different file. AMBIGUITY IS NOT A FINDING;
	// this gate declines instead of guessing.
	//
	// The cost is a real false NEGATIVE: a genuine self-citation written as a bare
	// `README.md:5` inside one of the 31 READMEs is missed. That is the correct
	// trade for a hygiene gate — a missed finding costs one drifted pointer, a false
	// alarm costs the gate.
	var match string
	n := 0
	for d := range docs {
		if d == cited || strings.HasSuffix(d, "/"+cited) {
			n++
			match = d
		}
	}
	return n == 1 && match == from
}

// TestNoDocCitesItsOwnLineNumbers bans the one citation form this corpus has proved
// cannot survive its own file being edited.
//
// ⚠ AN INSERTION ABOVE A LINE NUMBER INVALIDATES IT, AND AN APPEND-HEAVY FILE GETS
// INSERTIONS CONSTANTLY. One backlog entry cited a sibling bullet in the same file
// and the citation drifted `:690` → `:716` → `:744` → `:763` across four review
// rounds — each correction wrong again by the next round, because the entry doing
// the citing was itself inserting lines above the target. A second instance sat in
// `ADR-038` pointing at `:665` for a receipt that had moved to `:778`, stale before
// anyone noticed and widened by every commit since.
//
// The fix is not a better line number, it is not writing one: cite the heading or
// quote the text. That survives the next insertion, which no number does.
//
// The corpus holds ZERO of these today — they were converted to quoted anchors
// while this gate was being written — so this is a gate against recurrence rather
// than a cleanup. That is the profile worth having: silent now, loud on the next
// one. It never asks whether a line number is RIGHT, only whether one was written
// at all, so it cannot cry wolf.
func TestNoDocCitesItsOwnLineNumbers(t *testing.T) {
	root := repoRoot(t)
	checkSelfCitations(t, root, gitignoreMatcher(t, root))

	t.Run("a doc citing its own lines is reported", aDocCitingItsOwnLinesIsReported)
}

func checkSelfCitations(tb testing.TB, root string, ignored func(string, bool) bool) {
	tb.Helper()
	scanned, problems, cites := 0, 0, 0
	files := walk(tb, root, ignored)
	docs := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, ".md") {
			r, _ := filepath.Rel(root, f)
			docs[filepath.ToSlash(r)] = true
		}
	}
	for _, f := range files {
		if !strings.HasSuffix(f, ".md") {
			continue
		}
		scanned++
		body, err := os.ReadFile(f)
		if err != nil {
			tb.Fatalf("read %s: %v", f, err)
		}
		r, _ := filepath.Rel(root, f)
		rel := filepath.ToSlash(r)
		text := string(body)
		for _, m := range anyDocLineCiteRE.FindAllStringSubmatchIndex(text, -1) {
			cites++
			if !citesItself(rel, text[m[2]:m[3]], docs) {
				continue
			}
			problems++
			tb.Errorf("%s:%d cites its own file by line number (%s)\n"+
				"  A line number in the file doing the citing is invalidated by the next insertion "+
				"above it, and this corpus has watched one drift 690 → 716 → 744 → 763 across four "+
				"review rounds. Cite the heading, or quote the sentence — both survive an insert.",
				rel, lineAt(text, m[0]), text[m[0]:m[1]])
		}
	}
	if scanned == 0 {
		tb.Fatal("scanned no .md files — the walk or the suffix filter broke, and a green run " +
			"here would mean nothing")
	}
	// ⚠ SCANNING FILES IS NOT ATTEMPTING MATCHES. Guarding only on `scanned` leaves
	// a broken pattern invisible: the walk still reports every doc while the regex
	// finds nothing in any of them. The real corpus carries barely a dozen doc-to-doc
	// line citations — small enough that the guard is worth having rather than
	// assuming a big number — so zero means the pattern broke. The live figure is in
	// this test's own `-v` log; it is not written here, because a count in a comment
	// is false at the commit that carries it.
	if cites == 0 {
		tb.Fatalf("scanned %d docs and found NO `<file>.md:<n>` citation at all — the pattern "+
			"broke and this gate is passing vacuously", scanned)
	}
	if problems == 0 {
		tb.Logf("%d tracked docs, %d doc line citations, none self-referential", scanned, cites)
	}
}

// lineAt reports the 1-based line number a byte offset falls on.
func lineAt(text string, offset int) int { return strings.Count(text[:offset], "\n") + 1 }

// aDocCitingNoRecordIsReported is the falsifiability half for the doc citation
// gate, and aDocCitingItsOwnLinesIsReported for the self-citation ban.
//
// Both corpora are clean, so neither gate's reporting branch is reachable from the
// real tree — the gates would pass identically with their bodies deleted. Each
// drives the REAL function over a fixture built to be wrong, through a substituted
// testing.TB, because a test cannot pin its own reporting. A half that
// reimplements the check instead of calling it pins nothing: this package has hit
// that twice, once in `citation_test.go` and once in `specbinding_test.go`.
func aDocCitingNoRecordIsReported(t *testing.T) {
	root := t.TempDir()
	corpus := filepath.Join(root, adrCorpusDir)
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	// One real record, so `records` is non-empty and the vacuity guard does not fire.
	if err := os.WriteFile(filepath.Join(corpus, "ADR-001-a-fixture.md"), []byte("# ADR-001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real record number against a corpus that deliberately does not hold it — a
	// fixture naming a number nobody wrote would be flagged by the Go citation gate,
	// since this file is Go source like any other.
	doc := "# notes\n\nThis follows ADR-001, and supersedes ADR-002.\n"
	if err := os.WriteFile(filepath.Join(corpus, "notes.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	none := func(string, bool) bool { return false }

	rec := &recordingTB{}
	checkDocCitations(rec, root, none, map[string]bool{"ADR-001": true})
	if rec.errors == 0 {
		t.Error("a doc citing a record the corpus does not hold was not reported.\n" +
			"Without this the gate above passes over a clean corpus whatever its body says.")
	}

	// ⚠ AN EXEMPTION MUST HIDE ONE NUMBER, NOT A WHOLE FILE. Reverting the lookup to
	// file scope leaves the suite green without this cell — and it is not academic:
	// the exempted history record carries dozens of real citations beside the mentions
	// exempted from it, so file scope took every one of them out of the gate. This
	// uses a real exempted path so
	// the map actually applies, and asserts the OTHER citation is still reported.
	//
	// It runs with the OTHER fixture made clean first: leaving an unrelated offender
	// in place lets that one supply the failure and the cell passes for the wrong
	// reason. (It did, on the first attempt — the file-scope mutant stayed green.)
	if err := os.WriteFile(filepath.Join(corpus, "notes.md"),
		[]byte("# notes\n\nThis follows ADR-001 and nothing else.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exemptPath := "docs/adr/ADR-026-a-history-you-cannot-query.md"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, exemptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	// The exempt mention plus one that is not exempt — spelled without the literal
	// prefix, because this file is Go source the citation gate reads.
	both := "# history\n\nNumbering: ADR-0" + "22 is claimed. Superseded by ADR-0" + "44.\n"
	if err := os.WriteFile(filepath.Join(root, exemptPath), []byte(both), 0o644); err != nil {
		t.Fatal(err)
	}
	scoped := &recordingTB{}
	checkDocCitations(scoped, root, none, map[string]bool{"ADR-001": true})
	if scoped.errors == 0 {
		t.Error("a file exempt for ONE record number had a DIFFERENT unresolved citation, and it " +
			"was not reported.\nThe exemption is keyed by file and number for exactly this reason: " +
			"file scope hides every pointer in the file to excuse one word.")
	}
	if err := os.Remove(filepath.Join(root, exemptPath)); err != nil {
		t.Fatal(err)
	}

	// The negative half, without which "reports" is satisfied by reporting always.
	clean := "# notes\n\nThis follows ADR-001 and nothing else.\n"
	if err := os.WriteFile(filepath.Join(corpus, "notes.md"), []byte(clean), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := &recordingTB{}
	checkDocCitations(ok, root, none, map[string]bool{"ADR-001": true})
	if ok.errors != 0 {
		t.Errorf("a doc citing only records that exist was reported anyway (%d error(s)) — "+
			"a gate that fires on everything is one people switch off", ok.errors)
	}
}

func aDocCitingItsOwnLinesIsReported(t *testing.T) {
	root := t.TempDir()
	none := func(string, bool) bool { return false }

	// The exact shape that drifted four times: an entry pointing at a sibling
	// bullet in the file doing the pointing.
	bad := "# backlog\n\n- a finding\n- see the bullet at BACKLOG.md:3 for why\n"
	if err := os.WriteFile(filepath.Join(root, "BACKLOG.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recordingTB{}
	checkSelfCitations(rec, root, none)
	if rec.errors == 0 {
		t.Error("a doc citing its own file by line number was not reported.\n" +
			"That citation is invalidated by the next insertion above it, which is how one " +
			"drifted 690 → 716 → 744 → 763 across four review rounds.")
	}

	// A line citation into ANOTHER file is legitimate and must stay quiet — the ban
	// is on self-reference, not on line numbers as such.
	//
	// ⚠ INCLUDING ANOTHER DOC WITH THE SAME BASENAME. 31 files here are called
	// README.md and 28 CLAUDE.md, and the first version of this gate compared
	// basenames, so one README citing another read as self-reference. That is the
	// blocker this cell now pins.
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# top\n\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := "# backlog\n\n- a finding\n- see `internal/palace/repo.go:797` for why\n" +
		"- and the bullet under *\"A heading Somebody Wrote\"* for the rest\n"
	if err := os.WriteFile(filepath.Join(root, "BACKLOG.md"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	sibling := "# nested readme\n\nThe top-level `README.md:2` documents the default.\n"
	if err := os.WriteFile(filepath.Join(root, "sub", "README.md"), []byte(sibling), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := &recordingTB{}
	checkSelfCitations(ok, root, none)
	if ok.errors != 0 {
		t.Errorf("a legitimate citation was reported anyway (%d error(s)).\n"+
			"sub/README.md cites the TOP-LEVEL README.md by line — a different file that happens "+
			"to share a basename. Comparing basenames makes that read as self-reference, and a "+
			"gate that fires on a correct pointer is one people switch off.", ok.errors)
	}
}
