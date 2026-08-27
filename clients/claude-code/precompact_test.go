package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-041 T4. The mechanism ADR-017 named in 2026-08 and left unbuilt pending a
// measurement — which ADR-041 T2 now supplies.

// TestPreCompactHookIsRegistered is rung 2. The script is inert without this one
// line in the installer, and a hook that is written but never registered is this
// repository's characteristic defect wearing a shell script.
func TestPreCompactHookIsRegistered(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, precompactHookFile)); err != nil {
		t.Fatalf("the PreCompact hook was not written: %v", err)
	}

	var found bool
	for _, p := range inst.hookPlans() {
		if p.event == "PreCompact" {
			found = true
			if !strings.Contains(p.cmd, precompactHookFile) {
				t.Errorf("PreCompact registered against %q, which is not the hook", p.cmd)
			}
		}
	}
	if !found {
		t.Error("no PreCompact entry in the hook plans — the script is on disk and nothing " +
			"invokes it, so a fresh context still starts blind")
	}
}

// TestF6AHookIsSilentInTheCommonCase drives the SCRIPT, not a description of it.
//
// F-6 is the constraint every pushed-recall mechanism lives inside: the SessionStart
// verify hook states the reasoning in its own header — "a hook that reports 'all
// good' at every session start is a hook people stop reading, and its output is
// spent context". A compaction hook that speaks every time is that mistake at a
// higher frequency.
//
// ⚠ IT STUBS THE BINARY, AND THE FIRST VERSION DID NOT — which made it a test that
// could not fail. Without a stub the hook was silent because the REAL aiagentmemory
// found nothing in a temp directory, not because the guard under test worked, and
// two mutants survived: removing the empty-query guard and removing the off-switch
// both left the output empty for that downstream reason. The stub records that it
// ran, so "silent" and "never got that far" stop being the same observation.
//
// The speaks-when-it-has-something case is not decoration either. A test that only
// asserts silence is satisfied by `exit 0` on line one.
func TestF6AHookIsSilentInTheCommonCase(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	script, err := assets.ReadFile(precompactHookAsset)
	if err != nil {
		t.Fatalf("read embedded hook: %v", err)
	}

	// A stub on PATH that records the fact it was called and returns one hit.
	stubDir := t.TempDir()
	marker := filepath.Join(stubDir, "was-called")
	// ⚠ THE STUB ASSERTS THE INVOCATION, not just its own existence. The flags that
	// keep this hook useful — room=decisions, and a distance floor — are arguments,
	// not branches, so no assertion on the hook's OUTPUT can see them: a stub that
	// ignores its arguments returns a hit either way and the mutant survives.
	// Measured 2026-08-28: without the scope the top three hits for a real mid-work
	// query were this session's own transcript chunks, so dropping these flags makes
	// the hook actively harmful rather than merely quiet.
	stub := "#!/usr/bin/env bash\n" +
		"touch " + marker + "\n" +
		"case \"$*\" in\n" +
		"  *room=decisions*max_distance*) echo '{\"count\":1,\"hits\":[{\"id\":\"x\"}]}' ;;\n" +
		"  *) echo '{\"count\":0,\"hits\":[]}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(stubDir, "aiagentmemory"), []byte(stub), 0o755); err != nil {
		t.Fatalf("stub: %v", err)
	}

	run := func(t *testing.T, extraEnv []string, workdir string) (string, bool) {
		t.Helper()
		_ = os.Remove(marker)
		cmd := exec.Command("bash", filepath.Join(workdir, "precompact.sh"))
		cmd.Dir = workdir
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"PreCompact"}`)
		cmd.Env = append(os.Environ(),
			append([]string{"PATH=" + stubDir + ":" + os.Getenv("PATH")}, extraEnv...)...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("the hook failed the session (%v) — it must never do that", err)
		}
		_, statErr := os.Stat(marker)
		return string(out), statErr == nil
	}

	place := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "precompact.sh"), script, 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
		return dir
	}

	// Not a repository: no branch, no diff, so no query. A query built from
	// nothing recalls whatever is most popular, which is worse than silence.
	quiet := place(t)
	if out, called := run(t, []string{"CLAUDE_PROJECT_DIR=" + quiet}, quiet); out != "" || called {
		t.Errorf("with nothing to go on the hook spoke (%q) or searched anyway (called=%v)", out, called)
	}

	// And it SPEAKS when it has something. Without this the assertions around it
	// are satisfied by a script that does nothing at all.
	live := place(t)
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@example.test"},
		{"config", "user.name", "t"}, {"commit", "--allow-empty", "-m", "base"}} {
		c := exec.Command("git", args...)
		c.Dir = live
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git unavailable for the positive case: %v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(live, "recallrate.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed a change: %v", err)
	}
	c := exec.Command("git", "add", "-A")
	c.Dir = live
	_, _ = c.CombinedOutput()
	out, called := run(t, []string{"CLAUDE_PROJECT_DIR=" + live}, live)
	if !called {
		t.Error("on a branch with changed files the hook never searched — it cannot inject a " +
			"recall it did not perform")
	}
	if !strings.Contains(out, "Memory recalled") {
		t.Errorf("the hook had a hit and said nothing: %q", out)
	}

	// ⚠ A FAILING RECALL IS NOT SILENCE. The stub exits non-zero; the hook must say
	// so. Without this the hook is silent whether the palace had nothing or the call
	// never worked, and on a --local install — where `mcp` demands a token that does
	// not exist — it would have been permanently the second while looking like the
	// first. That is how this was found: 25 measurement queries returned clean
	// zeroes that were 25 swallowed errors.
	broken := place(t)
	brokenBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(brokenBin, "aiagentmemory"),
		[]byte("#!/usr/bin/env bash\necho 'no workspace token found' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("stub: %v", err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@example.test"},
		{"config", "user.name", "t"}, {"commit", "--allow-empty", "-m", "base"}} {
		c := exec.Command("git", args...)
		c.Dir = broken
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(broken, "recallrate.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stage := exec.Command("git", "add", "-A")
	stage.Dir = broken
	_, _ = stage.CombinedOutput()
	cmd := exec.Command("bash", filepath.Join(broken, "precompact.sh"))
	cmd.Dir = broken
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"PreCompact"}`)
	cmd.Env = append(os.Environ(), "PATH="+brokenBin+":"+os.Getenv("PATH"), "CLAUDE_PROJECT_DIR="+broken)
	bout, err := cmd.Output()
	if err != nil {
		t.Fatalf("the hook failed the session on a failed recall (%v) — it must never do that", err)
	}
	if !strings.Contains(string(bout), "could not run") {
		t.Errorf("a failed recall produced %q — silence here is indistinguishable from having "+
			"nothing to say, which is how a hook that can never speak looks healthy", string(bout))
	}

	// ⚠ THE OFF-SWITCH IS TESTED HERE, IN THE LIVE TREE, and the placement is the
	// point. Run in an empty directory it passes whatever the switch does, because
	// the empty-query guard produces the silence — which is exactly how its first
	// mutant survived. Only a working tree, where a query exists and the hook would
	// otherwise speak, leaves the switch as the one thing that can keep it quiet.
	if out, called := run(t, []string{"AGENTSMEMORY_PRECOMPACT=off", "CLAUDE_PROJECT_DIR=" + live}, live); out != "" || called {
		t.Errorf("AGENTSMEMORY_PRECOMPACT=off produced output (%q) or still searched (called=%v) "+
			"on a tree where the hook otherwise speaks", out, called)
	}
}
