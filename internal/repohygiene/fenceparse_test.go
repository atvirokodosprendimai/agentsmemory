package repohygiene

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// acceptanceBlock isolates a task's ## Acceptance section: everything up to the
// next `## ` heading, or to the end of the file when it is the last section.
var acceptanceBlock = regexp.MustCompile(`(?ms)^## Acceptance\s*$(.*?)(?:^## |\z)`)

// fencedScript pulls each ``` block out of that section.
var fencedScript = regexp.MustCompile("(?s)```(?:bash|sh)?\n(.*?)```")

// unparseableFence is one acceptance command the shell will not even parse.
type unparseableFence struct {
	file  string
	index int    // which fence within the file, 1-based, for the multi-fence tasks
	msg   string // the shell's own complaint, which names the offending token
}

// unparseableFences returns every acceptance fence in the corpus that a shell
// refuses to PARSE, and how many it examined.
//
// It exists because a fence that cannot start is invisible to everything else.
// `adr-lint` reads a fence's grammar and never executes it; `adr-verify` runs it
// but only when a human asks; and the failure mode is not a red test but a task
// that sits READY for weeks with its work finished. Measured 2026-09-07: ADR-030
// T1's fence had never run — it nested single quotes inside `sh -c '…'`, which
// bash reads as three concatenated words and rejects with `syntax error near
// unexpected token '('` before running anything. T2 is Depends-on T1, so the
// record was blocked behind a command nobody had executed.
//
// It parses rather than runs, which is the whole point: executing 166 fences
// would take hours and need Docker, while `sh -n` costs milliseconds and catches
// the entire class. It cannot tell whether a fence PASSES — nothing here claims
// that, and adr-verify remains the only thing that can.
//
// ⚠ `sh` RATHER THAN `bash`, DELIBERATELY. This package's own tests run inside
// the golang:1.26-alpine containers other fences use, and alpine ships no bash;
// a gate that needs one would fail there for a reason that has nothing to do
// with the corpus. Calibrated before it was trusted: over all 166 fences in the
// tree, `sh -n` and `bash -n` flag exactly the same three files, so the stricter
// POSIX parser costs no false positive here. It would report one if somebody
// wrote a bashism into a fence, and that is the trade — the fences are run by
// `sh -c` inside the container anyway, so POSIX is the right dialect to hold
// them to.
func unparseableFences(tb testing.TB, root string) (offenders []unparseableFence, scanned int) {
	tb.Helper()
	tasks, err := filepath.Glob(filepath.Join(root, "docs", "adr", "ADR-*", "tasks", "T*.md"))
	if err != nil {
		tb.Fatalf("glob tasks: %v", err)
	}
	// os.MkdirTemp rather than tb.TempDir: the falsifiability case drives this
	// through the package's recordingTB, which overrides only what a gate calls
	// and would nil-panic on TempDir.
	dir, err := os.MkdirTemp("", "fenceparse")
	if err != nil {
		tb.Fatalf("temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for _, path := range tasks {
		raw, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		block := acceptanceBlock.FindSubmatch(raw)
		if block == nil {
			continue
		}
		for i, m := range fencedScript.FindAllSubmatch(block[1], -1) {
			scanned++
			script := filepath.Join(dir, "fence.sh")
			if err := os.WriteFile(script, m[1], 0o600); err != nil {
				tb.Fatalf("write fence: %v", err)
			}
			out, err := testexec.Command(tb, "sh", "-n", script).CombinedOutput()
			if err == nil {
				continue
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			offenders = append(offenders, unparseableFence{
				file:  rel,
				index: i + 1,
				msg:   strings.TrimSpace(strings.ReplaceAll(string(out), script, "<fence>")),
			})
		}
	}
	return offenders, scanned
}

// checkFenceParses is the verdict, through a substitutable testing.TB.
//
// ⚠ THE FLOOR ON `scanned` IS NOT DECORATION. An empty offender list is what a
// clean corpus looks like AND what a glob that matched nothing looks like, and a
// gate that cannot tell those apart reports health over a directory that moved.
func checkFenceParses(tb testing.TB, root string) {
	tb.Helper()
	offenders, scanned := unparseableFences(tb, root)
	if scanned == 0 {
		tb.Errorf("no acceptance fence was examined anywhere under docs/adr/*/tasks; that is not " +
			"a clean bill of health, it is a gate that did not run")
	}
	for _, o := range offenders {
		tb.Errorf("%s: acceptance fence %d does not parse, so it can never run:\n    %s\n"+
			"  Almost always a single quote nested inside `sh -c '…'`, which the shell reads as "+
			"concatenated words. Use double quotes for the inner value — `-run \"^(TestA|TestB)$\"` "+
			"— since a `$` before a closing `\"` is literal.",
			o.file, o.index, o.msg)
	}
}

// TestEveryAcceptanceFenceParses is the gate: no task may carry an acceptance
// command the shell cannot even read.
func TestEveryAcceptanceFenceParses(t *testing.T) {
	checkFenceParses(t, repoRoot(t))

	// A corpus with zero unparseable fences cannot exercise the branch that
	// reports one, so the falsifiability half drives the SAME function over a
	// fixture that is broken — and through a substitutable TB, because a test
	// cannot pin its own reporting and a severed call site is otherwise silent.
	t.Run("a fence with a nested single quote is caught", func(t *testing.T) {
		fixture := t.TempDir()
		// Named after a REAL record, because TestEveryCitedADRResolves scans this
		// source — including its comments — and an invented record number fails it.
		// The number is incidental; the fixture is a synthetic copy of ADR-030 T1's
		// actual failure.
		tasks := filepath.Join(fixture, "docs", "adr", "ADR-030-fixture", "tasks")
		if err := os.MkdirAll(tasks, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body := "# Task ADR-030-T1: fixture\n\n## Acceptance\n\n" +
			"```bash\ndocker run --rm x sh -c 'set -e; go test -run '^(A|B)$' -v'\n```\n\n## Tests\n"
		if err := os.WriteFile(filepath.Join(tasks, "T1-fixture.md"), []byte(body), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		spy := &recordingTB{}
		checkFenceParses(spy, fixture)
		if spy.errors == 0 {
			t.Fatal("the gate passed a fence the shell refuses to parse; it would not have caught " +
				"ADR-030 T1, whose fence had never run")
		}
	})
}
