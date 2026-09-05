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

// TestThePreCompactHookSurvivesALongTranscriptLine drives the SCRIPT over a
// transcript whose lines are the length a real one reaches, and holds it to a
// wall-clock bound.
//
// It exists because every other test over the task-in-flight extraction drives a
// fixture of short lines, and the stage is quadratic in LINE LENGTH rather than
// in file size: `sed`'s `s/.*"role"…/` retries from every position, so one
// 243,820-character line costs more than the other 16,405 lines together.
// Measured 2026-09-05 on this checkout's own 29MB transcript, the stage took
// 47.25s of a 49.19s run while reading the whole file took 0.01s.
//
// A bound rather than a benchmark, because what broke was not slowness. The hook
// is registered with `timeout: 75`; past that it is killed, writes no note, and
// the recall hook's `[ -s "$NOTE" ]` then skips the re-ground marker entirely —
// so the compaction completes and the monitor waits for ever on a file nobody
// will write. The owner saw only "/compact and nothing happens".
//
// ⚠ THE LINE MUST BE A TOOL RESULT, AND THE FIRST VERSION OF THIS FIXTURE WAS
// NOT — THE MUTANT SURVIVED IT. Long ASSISTANT lines cost nothing (0.02s for 20
// x 200,000 characters, measured): `.*"role"…"user"` fails on them early and sed
// gives up. The expensive shape is a user turn whose `content` is an ARRAY, which
// a tool result always is: `"role":"user","content":[` matches the pattern's
// whole prefix and only then hits `[` where a quote was required, so every one of
// those lines is a full backtracking scan. That is the same array-valued content
// this extraction already documents as unreachable — it is not merely skipped,
// it is what the scan spends its time on. A fixture built from the shape a reader
// would reach for first pins nothing, which is why the shape is spelled out here
// rather than left to the generator below.
//
// Sized and bounded from measurement, not from taste. Against a 29MB fixture —
// the size this checkout's real transcript had reached — the hook takes 0.13s
// with the prefilter and 12.72s without it. The 5s bound therefore leaves ~38x
// headroom on the passing side while the mutant misses it by 2.5x, so neither a
// loaded CI box nor a fast one changes the verdict.
func TestThePreCompactHookSurvivesALongTranscriptLine(t *testing.T) {
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
			"the task-in-flight extraction is scanning them with a backtracking regexp, and at 75s "+
			"the harness kills it — which costs the note, the marker and the re-ground wake, silently",
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

// TestTheNoteSurvivesATranscriptWithNoPromptToExtract drives the SCRIPT over a
// transcript whose every plain user turn is chrome, so the task-in-flight
// extraction yields nothing, and requires the note to be written anyway.
//
// It exists because the note was DELETED in exactly that case, and the deletion
// was invisible from every side. The note's fields are produced by a `{ … }`
// group whose exit status is its LAST command's, and that status decides between
// `mv -f "$TMP" "$NOTE"` and the `||` arm that removes the temp file. The last
// line was `[ -n "$PENDING" ] && printf 'prompt=%s\n' "$PENDING"`, so an empty
// extraction ended the group on a false test, the group exited 1, and the note
// went in the bin — while the hook still exited 0, because it is deliberately
// silent about a state dir it cannot write.
//
// Measured 2026-09-05 on a live session (brolis-lizdai, 404859e7): it compacted,
// the harness recorded `PreCompact … completed successfully`, its re-ground
// monitor was armed on the correct path, and no note and no marker were ever
// written. The wake could not fire, and nothing anywhere said why.
//
// PENDING is empty whenever every plain user turn is chrome — a short session
// driven by slash commands reaches that easily — so this is an ordinary session,
// not a corner case. What the note carries then is still worth having: the
// branch, the head and the uncommitted count are what ADR-059 exists to hand
// back, and the wake's marker is written whether or not a task was named.
func TestTheNoteSurvivesATranscriptWithNoPromptToExtract(t *testing.T) {
	state, repo := t.TempDir(), gitRepoWithOneDirtyFile(t)

	// Every one of these is on the extraction's chrome deny list, so the pipeline
	// runs, matches, and is filtered down to nothing — which is the path that
	// broke. A transcript with no user turns at all would exercise a different
	// branch and would not have caught this.
	var b strings.Builder
	b.WriteString(`{"type":"user","message":{"role":"user","content":"/compact"}}` + "\n")
	b.WriteString(`{"type":"user","message":{"role":"user","content":"<local-command-stdout>Compacted</local-command-stdout>"}}` + "\n")
	b.WriteString(`{"type":"user","message":{"role":"user","content":"<system-reminder>x</system-reminder>"}}` + "\n")

	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	note, _, stderr := runPreCompactHook(t, state, repo, "nochrome",
		`{"session_id":"nochrome","trigger":"manual","transcript_path":"`+transcript+`"}`)

	if note == "" {
		t.Fatalf("no note was written for a transcript whose user turns are all chrome; the "+
			"compaction then hands back nothing and the re-ground marker is never created, so an "+
			"armed monitor waits for ever. stderr:\n%s", stderr)
	}
	for _, want := range []string{"branch=task/note\n", "dirty=1\n", "trigger=manual\n"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note lacks %q, so the state ADR-059 exists to hand back is missing:\n%s", want, note)
		}
	}
	// The absent prompt is correct — chrome names no work — and must be absent
	// rather than empty, or the read side labels the wake with a blank task.
	if strings.Contains(note, "prompt=") {
		t.Errorf("the note carries a prompt= line built from chrome:\n%s", note)
	}
}
