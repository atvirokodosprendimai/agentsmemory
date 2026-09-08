package repohygiene

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// inertGuard is one `!`-inverted command in an acceptance fence whose status
// nothing reads.
type inertGuard struct {
	file string
	line int
	text string
}

// recordedPass matches a Verification Log entry claiming an exit-0 run. A task
// carrying one has been verified against the fence AS IT STOOD, which is what
// makes its fence history rather than a promise.
var recordedPass = regexp.MustCompile(`exit 0 ·`)

// inertVacuityGuards finds acceptance-fence commands that READ like a check and
// cannot fail.
//
// POSIX `set -e` does not apply to a command whose status is inverted with `!`,
// so `! grep -qE "no tests to run|^FAIL" out` placed mid-script exits nothing:
// the shell carries on and the fence's verdict is whatever runs last. Verified
// rather than assumed, 2026-09-08:
//
//	sh -c 'set -e; echo "FAIL x" > f; ! grep -qE "^FAIL" f; echo REACHED; exit 0'
//	→ REACHED, exit 0
//
// That is the vacuity guard — the one whose whole job is to catch a runner that
// scored nothing — silently not running. 37 of this corpus's fences carried one.
// `adr-lint` reports the class as advice and exits 0, so nothing stopped it
// spreading. None was actually vacuous: each also asserts `--- PASS: <TestName>`
// un-negated, and those DO fail. The guard was a missing layer, not an open hole.
//
// ⚠ ONLY TASKS WITH NO RECORDED exit-0 RUN ARE HELD TO THIS, and the reason is
// the whole design of the gate. Rewriting the fence of a task already verified
// invalidates its evidence: adr-lint then reports "marked done but no exit-0
// entry carries the current Acceptance digest". Measured 2026-09-08 by doing it
// — fixing all 37 took the corpus from 1 failing record to 12. A pending task's
// fence is a promise about a run that has not happened, so correcting it costs
// nothing; a done task's fence is the text its evidence was taken against, and
// changing it is a re-verification decision rather than a hygiene fix.
//
// The exemption is therefore keyed on a FACT IN THE FILE — the presence of an
// exit-0 log entry — never on a hand-kept list, which is what this repo's own
// gates record going stale.
//
// The fix, for a fence this reports:
//
//	if grep -qE "no tests to run|^FAIL|^--- FAIL" out; then exit 1; fi
//
// A `!`-inverted command is fine in two places, both excluded here: as the
// fence's LAST effective command, where its status IS the exit status, and
// inside an `&&` chain, where the status propagates.
func inertVacuityGuards(tb testing.TB, root string) (offenders []inertGuard, scanned int) {
	tb.Helper()
	tasks, err := filepath.Glob(filepath.Join(root, "docs", "adr", "ADR-*", "tasks", "T*.md"))
	if err != nil {
		tb.Fatalf("glob tasks: %v", err)
	}
	for _, path := range tasks {
		raw, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		block := acceptanceBlock.FindSubmatch(raw)
		if block == nil {
			continue
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		verified := recordedPass.Match(raw)
		for _, m := range fencedScript.FindAllSubmatch(block[1], -1) {
			scanned++
			if verified {
				continue
			}
			offenders = append(offenders, inertGuardsIn(string(m[1]), rel)...)
		}
	}
	return offenders, scanned
}

// inertGuardsIn is the decision, split out so the falsifiability case can drive
// the real predicate over a fixture rather than a copy of it.
func inertGuardsIn(script, file string) (out []inertGuard) {
	lines := strings.Split(script, "\n")
	// content reports whether a line carries a command, so the closing quote of
	// an `sh -c '…'` wrapper does not count as "something runs after the guard".
	content := func(s string) bool {
		s = strings.TrimSpace(s)
		return s != "" && s != "'" && s != `"` && !strings.HasPrefix(s, "#")
	}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "! ") {
			continue
		}
		if strings.Contains(l, "&&") || strings.Contains(l, "||") {
			continue // the status propagates to the chain
		}
		last := true
		for _, rest := range lines[i+1:] {
			if content(rest) {
				last = false
				break
			}
		}
		if !last {
			out = append(out, inertGuard{file: file, line: i + 1, text: t})
		}
	}
	return out
}

// checkNoInertGuards is the verdict, taken through a substitutable testing.TB
// so the falsifiability subtest can prove it reports.
//
// ⚠ THE FLOOR ON scanned IS NOT DECORATION. An empty offender list is what a
// clean corpus looks like AND what a glob that matched nothing looks like.
func checkNoInertGuards(tb testing.TB, root string) {
	tb.Helper()
	offenders, scanned := inertVacuityGuards(tb, root)
	if scanned == 0 {
		tb.Errorf("no acceptance fence was examined under docs/adr/*/tasks; that is not a clean " +
			"bill of health, it is a gate that did not run")
	}
	for _, o := range offenders {
		tb.Errorf("%s: line %d of its acceptance fence cannot fail:\n    %s\n"+
			"  `set -e` does not apply to a `!`-inverted command, so this reads as the vacuity "+
			"check while checking nothing — the fence's verdict is whatever runs after it. "+
			"This task has no recorded exit-0 run, so correcting it costs no evidence:\n"+
			"    if grep -qE \"…\" out; then exit 1; fi", o.file, o.line, o.text)
	}
}

// TestAPendingAcceptanceFenceCarriesNoInertVacuityGuard holds every task that
// has not yet been verified to a fence that can actually fail.
func TestAPendingAcceptanceFenceCarriesNoInertVacuityGuard(t *testing.T) {
	checkNoInertGuards(t, repoRoot(t))

	// A clean corpus cannot exercise the reporting branch, so the falsifiability
	// case drives the SAME predicate and the SAME verdict over fixtures that ARE
	// offenders — as subtests, because a sibling sits outside the one command an
	// acceptance fence has to run.
	t.Run("the predicate distinguishes an inert guard from a live one", func(t *testing.T) {
		inert := "set -e\ngo test ./x 2>&1 | tee out\n! grep -qE \"^FAIL\" out\ngo test ./y\n"
		if got := inertGuardsIn(inert, "fixture.md"); len(got) != 1 {
			t.Errorf("a mid-script negated guard must be reported, got %d: %+v", len(got), got)
		}
		last := "set -e\ngo test ./x 2>&1 | tee out\n! grep -qE \"^FAIL\" out\n"
		if got := inertGuardsIn(last, "fixture.md"); len(got) != 0 {
			t.Errorf("a negated guard that is the LAST command is the fence's exit status and "+
				"must not be reported, got %+v", got)
		}
		chained := "go test ./x 2>&1 | tee out \\\n  && ! grep -qE \"^FAIL\" out\ngo test ./y\n"
		if got := inertGuardsIn(chained, "fixture.md"); len(got) != 0 {
			t.Errorf("a negated guard inside an && chain propagates its status and must not be "+
				"reported, got %+v", got)
		}
		trailingQuote := "set -e\n! grep -qE \"^FAIL\" out\n'\n"
		if got := inertGuardsIn(trailingQuote, "fixture.md"); len(got) != 0 {
			t.Errorf("the closing quote of an `sh -c '…'` wrapper is not a command; a guard before "+
				"it is still the last one, got %+v", got)
		}
	})

	t.Run("the verdict reports, and exempts a task already verified", func(t *testing.T) {
		// "ADR-fixture" matches this gate's ADR-* glob without matching the
		// ADR-NNN citation pattern: a numbered fixture name reads as a citation to
		// a record that does not exist, and TestEveryCitedADRResolves refuses it.
		write := func(root, fence, log string) {
			dir := filepath.Join(root, "docs", "adr", "ADR-fixture", "tasks")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			body := "# Task fixture\n\n## Acceptance\n\n```bash\n" + fence + "```\n\n" +
				"## Verification Log\n" + log + "\n"
			if err := os.WriteFile(filepath.Join(dir, "T1-fixture.md"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		const bad = "set -e\ngo test ./x 2>&1 | tee out\n! grep -qE \"^FAIL\" out\ngo test ./y\n"

		pending := t.TempDir()
		write(pending, bad, "")
		rec := &recordingTB{}
		checkNoInertGuards(rec, pending)
		if rec.errors == 0 {
			t.Error("reported nothing over a PENDING fence whose guard cannot fail; the " +
				"reporting branch is unreachable and this gate pins nothing")
		}

		// The same broken fence, on a task that has been verified. Rewriting it
		// would invalidate that evidence, so the gate must leave it alone.
		verified := t.TempDir()
		write(verified, bad, "- 2026-01-01 · abc1234 · exit 0 · `go test ./x`")
		rec = &recordingTB{}
		checkNoInertGuards(rec, verified)
		if rec.errors != 0 {
			t.Errorf("reported %d error(s) over a task with recorded exit-0 evidence; correcting "+
				"that fence is a re-verification decision, not a hygiene fix", rec.errors)
		}

		clean := t.TempDir()
		write(clean, "set -e\ngo test ./x 2>&1 | tee out\nif grep -qE \"^FAIL\" out; then exit 1; fi\n", "")
		rec = &recordingTB{}
		checkNoInertGuards(rec, clean)
		if rec.errors != 0 {
			t.Errorf("reported %d error(s) over a correct pending fence; a gate that is red for "+
				"any reason at all proves nothing", rec.errors)
		}
	})
}
