package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// gitRepoWithOneDirtyFile builds the tree the PreCompact note describes: one
// commit on a named branch and one uncommitted edit. Returned is the repo path.
//
// A real repository rather than a stub, because the note's git fields are the
// whole point and a stub that prints "main" proves the script can print.
func gitRepoWithOneDirtyFile(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := testexec.Command(t, "git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "task/note")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")
	run("commit", "-q", "-m", "one")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// runPreCompactHook drives the shipped script with one event and returns the
// note it wrote (empty when none), its stdout and its stderr.
func runPreCompactHook(t *testing.T, stateDir, repo, session, input string) (note, stdout, stderr string) {
	t.Helper()
	cmd := testexec.Command(t, "bash", filepath.Join("hooks", "agentsmemory-precompact-hook.sh"))
	cmd.Stdin = strings.NewReader(input)
	var outb, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &outb, &errb
	cmd.Env = append(os.Environ(), "AGENTSMEMORY_STATE_DIR="+stateDir, "CLAUDE_PROJECT_DIR="+repo)
	if err := cmd.Run(); err != nil {
		t.Fatalf("the hook exited non-zero: %v\n%s", err, errb.String())
	}
	raw, _ := os.ReadFile(filepath.Join(stateDir, "agentsmemory-precompact", session))
	return string(raw), outb.String(), errb.String()
}

// TestThePreCompactHookWritesTheStateNote drives the SCRIPT: the note is keyed by
// session id, describes the tree as git sees it, copies at most eight paths from
// the session's touched list, and puts nothing on stdout — PreCompact stdout goes
// to the debug log, and a line there is a line somebody will one day expect the
// model to have read (ADR-041's shipped defect).
func TestThePreCompactHookWritesTheStateNote(t *testing.T) {
	stateDir := t.TempDir()
	repo := gitRepoWithOneDirtyFile(t)
	touched := filepath.Join(stateDir, "agentsmemory-touched")
	if err := os.MkdirAll(touched, 0o755); err != nil {
		t.Fatal(err)
	}
	var list strings.Builder
	for i := 0; i < 10; i++ {
		list.WriteString("dir/file" + string(rune('a'+i)) + ".go\n")
	}
	if err := os.WriteFile(filepath.Join(touched, "s1"), []byte(list.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	note, stdout, stderr := runPreCompactHook(t, stateDir, repo, "s1",
		`{"hook_event_name":"PreCompact","session_id":"s1","trigger":"auto","custom_instructions":""}`)
	if note == "" {
		t.Fatalf("no note written for session s1; stderr:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("the hook wrote to stdout, which PreCompact discards and a reader may still expect: %q", stdout)
	}
	for _, want := range []string{"at=", "trigger=auto\n", "branch=task/note\n", "dirty=1\n", "touched=10\n"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	head := ""
	for _, line := range strings.Split(note, "\n") {
		if strings.HasPrefix(line, "head=") {
			head = strings.TrimPrefix(line, "head=")
		}
	}
	if len(head) < 7 {
		t.Errorf("head=%q is not a short sha", head)
	}
	if n := strings.Count(note, "\nfile="); n != 8 {
		t.Errorf("note carries %d file= lines, want exactly 8 (bounded copy of the touched list):\n%s", n, note)
	}

	// An unsafe session id becomes a path component nowhere: no file, no crash.
	_, _, stderr = runPreCompactHook(t, stateDir, repo, "..", `{"hook_event_name":"PreCompact","session_id":"../x","trigger":"manual"}`)
	entries, _ := os.ReadDir(filepath.Join(stateDir, "agentsmemory-precompact"))
	for _, e := range entries {
		if e.Name() != "s1" {
			t.Errorf("an unsafe session id produced a file %q; stderr:\n%s", e.Name(), stderr)
		}
	}
}

// TestThePreCompactHookIsRegistered is rung 2: the script is inert unless the
// installer plans a PreCompact registration naming it, and this is the only test
// that fails when that plan is deleted.
func TestThePreCompactHookIsRegistered(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, precompactHookFile)); err != nil {
		t.Fatalf("the precompact hook was not written: %v", err)
	}
	var events []string
	for _, p := range inst.hookPlans() {
		if strings.Contains(p.cmd, precompactHookFile) {
			events = append(events, p.event)
		}
	}
	if strings.Join(events, ",") != "PreCompact" {
		t.Fatalf("the precompact hook is planned on %v, want exactly [PreCompact] — on any other event the note is written at the wrong moment or never", events)
	}
}
