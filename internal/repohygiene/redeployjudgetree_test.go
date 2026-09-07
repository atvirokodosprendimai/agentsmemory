package repohygiene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// TestTheUnverifiedBranchOfTheKitCheckIsReachable RUNS judge_tree instead of
// reading it, because the defect it holds is invisible to a text gate.
//
// `judge_tree` carries a branch for "the artifact cannot be read", and its
// comment says what that branch is for: *"Not a failure — a gate that blocks with
// no way to satisfy it is the bug this block once fixed — but it must never look
// like a pass, because silence is not success."* Under `set -euo pipefail` the
// branch could not be reached. `go version -m` exits non-zero when the toolchain
// is absent (127) or the file carries no VCS stamp (1), the pipeline's status
// became the assignment's, and `set -e` aborted the whole script before any
// branch was tested. The operator saw the heading `==> the installed client kit,
// against this checkout`, nothing under it, and a bare exit 1 after every earlier
// stage had passed — a successful deploy reported as a failed one (#392).
//
// ⚠ THE REPORT CALLED IT A WINDOWS BUG AND IT IS NOT. #392 measured it on Git
// Bash, where `command -v` hands back an extensionless path `go` cannot open.
// Measured here on Linux 2026-09-07, the same abort happens with no Windows in
// sight: a file that is not a Go binary, or a PATH without `go`. The branch was
// unreachable on every platform, in exactly the case its own message names.
//
// ⚠ AND A TEXT GATE COULD NOT HAVE CAUGHT IT. Every line involved reads
// correctly: the branch is present, its message is right, `2>/dev/null` looks
// like the error is being handled. What is wrong is the interaction between a
// pipeline's exit status and a shell option set 400 lines earlier, which is a
// fact about running the script and about nothing else. So this executes the
// real function, sliced out of the real file.
func TestTheUnverifiedBranchOfTheKitCheckIsReachable(t *testing.T) {
	root := repoRoot(t)
	fns := judgeTreeSource(t, root)

	t.Run("a file carrying no vcs stamp is reported, not fatal", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "aiagentmemory")
		if err := os.WriteFile(bin, []byte("not a go binary\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := runJudgeTree(t, fns, dir, bin, nil)
		if err != nil {
			t.Fatalf("judge_tree aborted the script on an unreadable artifact, so the caller sees a "+
				"bare failure where the UNVERIFIED verdict should be: %v\n%s", err, out)
		}
		assertJudgeTree(t, out, "UNVERIFIED", bin)
	})

	t.Run("no go toolchain is reported, not fatal", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "aiagentmemory")
		if err := os.WriteFile(bin, []byte("not a go binary\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		// A PATH holding only what the function itself shells out to, so `go` is
		// absent by construction rather than by hoping this host puts it somewhere
		// the restricted PATH misses. That guess is what would make this subtest
		// pass vacuously on a machine that installs Go into /usr/bin.
		out, err := runJudgeTree(t, fns, dir, bin, minimalPATH(t))
		if err != nil {
			t.Fatalf("judge_tree aborted with no `go` on PATH — the exact case its UNVERIFIED "+
				"message names (\"need go on PATH\"): %v\n%s", err, out)
		}
		assertJudgeTree(t, out, "UNVERIFIED", bin)
	})

	// The Windows half, pinned from a POSIX host because the mechanism is a path
	// and not a platform: `command -v` resolves an extensionless name MSYS can
	// execute while only `<name>.exe` is on disk. judge_tree retries the sibling,
	// and the verdict line then names the file it actually read — which is how
	// this asserts the retry happened rather than merely that nothing crashed.
	t.Run("an extensionless path with an .exe sibling is judged as the sibling", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "aiagentmemory")
		for _, p := range []string{bin, bin + ".exe"} {
			if err := os.WriteFile(p, []byte("not a go binary\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		out, err := runJudgeTree(t, fns, dir, bin, nil)
		if err != nil {
			t.Fatalf("judge_tree aborted: %v\n%s", err, out)
		}
		assertJudgeTree(t, out, "UNVERIFIED", bin+".exe")
		if strings.Contains(out, bin+" ") {
			t.Errorf("the verdict names the extensionless path, so the .exe retry did not happen; "+
				"on Git Bash that is the steady state and the gate dies there:\n%s", out)
		}
	})

	t.Run("each half of the assertion is reported on its own", anAbsentVerdictIsReported)
}

// anAbsentVerdictIsReported is the falsifiability half.
//
// ⚠ A HEALTHY SCRIPT CANNOT EXERCISE THE REPORTING BRANCH, so without this the
// three conditions inside assertJudgeTree could be severed — or collapsed into
// one — and every subtest above would go on passing. It drives assertJudgeTree
// itself over outputs that ARE wrong, one condition at a time, so a check that
// stopped distinguishing "printed no verdict" from "printed one and then died"
// is caught here rather than by whoever next reads a green run over an aborting
// gate. The correct fixture is asserted too: a check that reports on everything
// is as useless as one that reports on nothing.
func anAbsentVerdictIsReported(t *testing.T) {
	const path = "/x/bin/aiagentmemory"
	healthy := "    binary  UNVERIFIED: no vcs stamp readable in " + path + " (need go on PATH)\n" +
		judgeTreeSentinel + " kit_stale=0\n"

	quiet := &recordingTB{}
	assertJudgeTree(quiet, healthy, "UNVERIFIED", path)
	if quiet.errors != 0 {
		t.Errorf("the assertion reported %d finding(s) over output that carries the verdict, names "+
			"the file and reaches the sentinel; it has started failing on a correct run", quiet.errors)
	}

	// One broken output per condition, each holding the other two, so a collapse
	// into a single test is caught rather than averaged away.
	for name, broken := range map[string]string{
		"no verdict at all": "    binary  ok " + path + "\n" + judgeTreeSentinel + "\n",
		"a different file":  "    binary  UNVERIFIED: no vcs stamp readable in /other/path\n" + judgeTreeSentinel + "\n",
		"aborted right after printing": "    binary  UNVERIFIED: no vcs stamp readable in " + path +
			" (need go on PATH)\n",
	} {
		rec := &recordingTB{}
		assertJudgeTree(rec, broken, "UNVERIFIED", path)
		if rec.errors == 0 {
			t.Errorf("%q went unreported, so that condition pins nothing:\n%s", name, broken)
		}
	}
}

// assertJudgeTree holds the two things every case asserts: the verdict was
// printed, and the caller ran on past it.
func assertJudgeTree(tb testing.TB, out, verdict, path string) {
	tb.Helper()
	if !strings.Contains(out, verdict) {
		tb.Errorf("no %s verdict in the output — the branch produced no line at all:\n%s", verdict, out)
	}
	if !strings.Contains(out, path) {
		tb.Errorf("the verdict does not name %s, so it judged a different file:\n%s", path, out)
	}
	// The sentinel is what separates "printed a verdict" from "printed a verdict
	// and then died", which is the difference between a gate and an abort.
	if !strings.Contains(out, judgeTreeSentinel) {
		tb.Errorf("the harness did not reach the line after judge_tree, so the script still "+
			"aborts once the verdict is printed:\n%s", out)
	}
}

// judgeTreeSentinel is echoed after the call, so a run that printed the verdict
// and then aborted is distinguishable from one that carried on.
const judgeTreeSentinel = "AFTER_JUDGE_TREE"

// judgeTreeSource slices read_stamp and judge_tree out of the real script.
//
// Reading the real file rather than restating it is the whole point: a copy of
// the function in a fixture would go on passing after the original changed, which
// is the failure mode this repository files as "a list kept beside the truth".
// The slice is bounded by the two declarations and by judge_tree's closing brace
// in column 1, and every step fails loudly, because a slice that silently came
// back empty would make every subtest below pass over nothing.
func judgeTreeSource(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "redeploy.sh"))
	if err != nil {
		t.Fatalf("read the redeploy script: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	start, judge := -1, -1
	for i, line := range lines {
		if line == "read_stamp() {" && start < 0 {
			start = i
		}
		if line == "judge_tree() {" && judge < 0 {
			judge = i
		}
	}
	if start < 0 || judge < 0 || judge < start {
		t.Fatalf("scripts/redeploy.sh no longer declares read_stamp() then judge_tree() at column 1 "+
			"(read_stamp at %d, judge_tree at %d); this gate has stopped reading the code it runs",
			start, judge)
	}
	end := -1
	for i := judge + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatal("judge_tree has no closing brace in column 1 — the slice would run to the end of " +
			"the script and drag the whole deploy into this test")
	}
	src := strings.Join(lines[start:end+1], "\n")
	if !strings.Contains(src, "UNVERIFIED") {
		t.Fatalf("the sliced source carries no UNVERIFIED branch, so this gate would be asserting "+
			"the absence of a line that was never in scope:\n%s", src)
	}
	return src
}

// runJudgeTree writes a harness around the sliced functions and runs it, giving
// the globals judge_tree reads the values the script gives them.
func runJudgeTree(t *testing.T, fns, dir, path string, env []string) (string, error) {
	t.Helper()
	harness := filepath.Join(dir, "harness.sh")
	// `set -euo pipefail` is copied from the script deliberately: it is the option
	// that makes the defect, so a harness without it would pass over the bug.
	body := "#!/usr/bin/env bash\nset -euo pipefail\nwant_rev=\"unknown\"\nwant_tree=\"\"\nkit_stale=0\n" +
		fns + "\njudge_tree \"binary \" \"$1\"\necho \"" + judgeTreeSentinel + " kit_stale=$kit_stale\"\n"
	if err := os.WriteFile(harness, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := testexec.Command(t, "bash", harness, path)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// minimalPATH builds a PATH holding only the programs judge_tree shells out to,
// so `go` is absent by construction. A run whose helpers are missing would fail
// for its own reasons, so each one is resolved here and the test stops if it
// cannot be found.
func minimalPATH(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"sed", "head", "git"} {
		src, err := testexec.Command(t, "sh", "-c", "command -v "+tool).Output()
		if err != nil || len(strings.TrimSpace(string(src))) == 0 {
			t.Skipf("%s is not on PATH, so a minimal PATH cannot be built here", tool)
		}
		if err := os.Symlink(strings.TrimSpace(string(src)), filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "go")); err == nil {
		t.Fatal("the minimal PATH contains go; this subtest would then prove nothing")
	}
	return append(os.Environ(), "PATH="+dir)
}
