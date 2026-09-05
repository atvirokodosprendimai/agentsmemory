package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// runRecallHookWithNote drives the SessionStart recall hook against a stub CLI
// that records every argv line, with a note on disk for session s1, and returns
// the hook's stdout plus the stub's recorded calls.
//
// The stub answers whatever it is asked, so no assertion on the OUTPUT can tell
// which room or wing the hook asked; the calls are recorded for that reason —
// the same shape TestTheQueryCarriesTheBranchWorkOnACleanTree uses.
func runRecallHookWithNote(t *testing.T, source string) (stdout string, calls []string) {
	t.Helper()
	stateDir := t.TempDir()
	repo := gitRepoWithOneDirtyFile(t)
	noteDir := filepath.Join(stateDir, "agentsmemory-precompact")
	if err := os.MkdirAll(noteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	note := "at=2026-09-05T12:00:00Z\ntrigger=auto\nbranch=task/note\nhead=abc1234\ndirty=3\ntouched=10\n" +
		"file=one.go\nfile=two.go\nfile=three.go\nfile=four.go\nfile=five.go\nfile=six.go\nfile=seven.go\nfile=eight.go\n"
	if err := os.WriteFile(filepath.Join(noteDir, "s1"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	stubDir := t.TempDir()
	callsFile := filepath.Join(stubDir, "calls")
	stub := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> " + callsFile + "\necho 'a hit'\n"
	if err := os.WriteFile(filepath.Join(stubDir, "aiagentmemory"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := testexec.Command(t, "bash", filepath.Join("hooks", "agentsmemory-recall-hook.sh"))
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"s1","source":"` + source + `"}`)
	var outb, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &outb, &errb
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+":"+os.Getenv("PATH"), "CLAUDE_PROJECT_DIR="+repo,
		"AGENTSMEMORY_STATE_DIR="+stateDir, "AGENTSMEMORY_WING=wing_acme", "AGENTSMEMORY_TOKEN=t")
	if err := cmd.Run(); err != nil {
		t.Fatalf("the hook exited non-zero: %v\n%s", err, errb.String())
	}
	raw, _ := os.ReadFile(callsFile)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			calls = append(calls, line)
		}
	}
	return outb.String(), calls
}

// TestACompactStartHandsBackTheStateNote is the read half of ADR-059: on
// `source=compact` the injection opens with the note T1 wrote, and the second
// recall asks the installed wing's crash-resume room for the session's own
// checkpoint instead of wing_craft — under the same 400-character slot.
func TestACompactStartHandsBackTheStateNote(t *testing.T) {
	out, calls := runRecallHookWithNote(t, "compact")
	if !strings.HasPrefix(out, "Before compaction (2026-09-05T12:00:00Z, auto): branch task/note at abc1234, 3 uncommitted") {
		t.Errorf("the injection does not open with the note:\n%s", out)
	}
	for _, want := range []string{"one.go", "eight.go", "(+2 more)", "checkpoint:"} {
		if !strings.Contains(out, want) {
			t.Errorf("the injection lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "craft:") {
		t.Errorf("a compact start still renders a craft block:\n%s", out)
	}
	if len(calls) != 2 {
		t.Fatalf("expected two recall calls (project, checkpoint), got %d:\n%s", len(calls), strings.Join(calls, "\n"))
	}
	second := calls[1]
	// ⚠ max_distance=0 AND the branch in the query, both measured (see the hook's
	// comment): under the 0.42 floor the fixed sentence returned zero checkpoints.
	for _, want := range []string{"room=llm_open_threads", "wing=wing_acme", "limit=1", "max_distance=0 ", "WHERE SHOULD WORK RESUME AFTER A CRASH task/note "} {
		if !strings.Contains(second, want) {
			t.Errorf("the checkpoint call lacks %q: %s", want, second)
		}
	}
	// The branch, NOT the branch-work query: measured 2026-09-05, appending the
	// changed basenames ranked a day-old checkpoint about those files first.
	if strings.Contains(second, "a.go") {
		t.Errorf("the checkpoint call carries changed file names, which pull the rank toward the wrong checkpoint: %s", second)
	}
	for _, c := range calls {
		if strings.Contains(c, "wing=wing_craft") {
			t.Errorf("a compact start still asks wing_craft: %s", c)
		}
	}
}

// TestAColdStartDoesNotReadTheNote pins the other side of the source gate: a
// `startup` with a note on disk for the same session id is exactly ADR-058's
// hook — no note block, and craft is still asked. A note read on the wrong
// source describes a tree that has moved.
func TestAColdStartDoesNotReadTheNote(t *testing.T) {
	out, calls := runRecallHookWithNote(t, "startup")
	if strings.Contains(out, "Before compaction") {
		t.Errorf("a startup start read the note:\n%s", out)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], "wing=wing_craft") {
		t.Errorf("a startup start no longer asks wing_craft second:\n%s", strings.Join(calls, "\n"))
	}
	for _, c := range calls {
		if strings.Contains(c, "llm_open_threads") {
			t.Errorf("a startup start asked the checkpoint room: %s", c)
		}
	}
}

// TestThePreCompactHookDoesNotScanEveryTranscriptLine drives the SCRIPT over a
// transcript built from the line shape a real one is expensive on, and holds it
// to a wall-clock bound.
//
// It exists because every other test over the task-in-flight extraction drives a
// fixture of short lines, so none of them can see what the stage costs on a real
// transcript: 47.25s of a 49.19s run on a 29MB one, against 0.01s to read the
// whole file (GNU sed 4.9, Linux, 2026-09-05).
//
// ⚠ THE FIXTURE MUST BE TOOL RESULTS, AND THE FIRST VERSION WAS NOT — THE MUTANT
// SURVIVED IT. Long ASSISTANT lines are free (0.02s for 20 x 200,000
// characters): `.*"role"…"user"` fails on them early. Every expensive line
// carries `"role":"user"` NOT followed by `"content":"` — array content, which
// is every tool result — so the pattern's whole prefix matches and only then
// fails. The shape is spelled out here because the one a reader reaches for
// first pins nothing.
//
// ⚠ AND THE SHAPE IS ALL THIS PINS. An earlier version of this comment claimed
// the cost was quadratic in LINE LENGTH; the corpus refutes it. A 1.5MB
// transcript costs 16.67s while one 19x larger costs 47.25s, and inside a single
// file the shortest expensive line (34,081 chars) costs 1499ms against 1373ms
// for one nearly 3x longer (96,777 chars), with the near-match at a constant
// offset on both. The driver is not known and is deliberately not asserted.
// The same comment also claimed the hook was KILLED by its `timeout: 75`
// registration, costing the note and the wake — extrapolation, not measurement:
// the transcript from the session that prompted this took 16.67s. A missing note
// has some other cause.
//
// Sized and bounded from measurement. Against a 29MB fixture the hook takes
// 0.13s with the prefilter and 12.72s without, so the 5s bound leaves ~38x
// headroom on the passing side while the mutant misses it by 2.5x — neither a
// loaded CI box nor a fast one changes the verdict. On an engine that does not
// backtrack this way the margin is smaller (a reviewer measured 15x on BSD sed),
// which the bound's headroom absorbs.
func TestThePreCompactHookDoesNotScanEveryTranscriptLine(t *testing.T) {
	state, repo := t.TempDir(), gitRepoWithOneDirtyFile(t)

	// 120 tool-result turns of 250,000 characters, then the one plain user turn
	// that names the work, then the `/compact` that triggered the compaction —
	// so this also holds the chrome rule while it holds the clock.
	var b strings.Builder
	huge := strings.Repeat("x", 250_000)
	for i := 0; i < 120; i++ {
		b.WriteString(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` + huge + `"}]}}` + "\n")
	}
	b.WriteString(`{"type":"user","message":{"role":"user","content":"the work the compaction interrupted"}}` + "\n")
	b.WriteString(`{"type":"user","message":{"role":"user","content":"/compact"}}` + "\n")

	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	note, _, stderr := runPreCompactHook(t, state, repo, "longline",
		`{"session_id":"longline","trigger":"manual","transcript_path":"`+transcript+`"}`)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("the hook took %s over a %d-byte transcript of %d-character tool-result lines; "+
			"the task-in-flight extraction is scanning lines that cannot match, which on a real "+
			"transcript costs tens of seconds per compaction — put the grep prefilter back",
			elapsed, b.Len(), len(huge))
	}

	// Exiting fast by extracting nothing is the same lost wake by another route,
	// so the note must still name the work — and still skip the `/compact` turn
	// that triggered the compaction.
	if !strings.Contains(note, "prompt=the work the compaction interrupted") {
		t.Errorf("the hook finished in %s but the note does not name the work in flight:\n%s\nstderr:\n%s",
			elapsed, note, stderr)
	}
}
