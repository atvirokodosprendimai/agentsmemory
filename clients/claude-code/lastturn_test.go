package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// lastTurnFixture builds what the Stop hook's note describes: a git repo with
// one dirty file, a touched list of ten paths for session s1, and a transcript
// with five user lines — three plain prompts (one over 200 chars), one tool
// result (an array), one from a sidechain. Returns state dir, repo, transcript.
func lastTurnFixture(t *testing.T) (stateDir, repo, transcript string) {
	t.Helper()
	stateDir = t.TempDir()
	repo = gitRepoWithOneDirtyFile(t)
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
	long := strings.Repeat("y", 300)
	transcript = filepath.Join(t.TempDir(), "s1.jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"first prompt: fix the flaky hook test"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}`,
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":"a subagent prompt that must not appear"}}`,
		`{"type":"user","message":{"role":"user","content":"second prompt: ` + long + `"}}`,
		`{"type":"user","message":{"role":"user","content":"third prompt: ship it"}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return stateDir, repo, transcript
}

// runStopForNote drives the shipped Stop hook with a Stop event and returns the
// note directory's entries and the note body (empty when none was written).
func runStopForNote(t *testing.T, stateDir, repo, transcript, event string, env ...string) (names []string, note string) {
	t.Helper()
	cmd := testexec.Command(t, "bash", filepath.Join("hooks", "agentsmemory-stop-hook.sh"))
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"` + event + `","session_id":"s1","transcript_path":"` + transcript + `","stop_hook_active":false}`)
	var errb strings.Builder
	cmd.Stderr = &errb
	cmd.Env = append(os.Environ(), append([]string{
		"AGENTSMEMORY_STATE_DIR=" + stateDir, "CLAUDE_PROJECT_DIR=" + repo, "AGENTSMEMORY_STOP_HOOK=on", "AGENTSMEMORY_STATS=off",
	}, env...)...)
	_ = cmd.Run() // exit 2 is the nudge, not a failure
	entries, _ := os.ReadDir(filepath.Join(stateDir, "agentsmemory-last-turn"))
	for _, e := range entries {
		names = append(names, e.Name())
		raw, _ := os.ReadFile(filepath.Join(stateDir, "agentsmemory-last-turn", e.Name()))
		note = string(raw)
	}
	return names, note
}

// TestTheStopHookWritesTheLastTurnNote: the note is keyed by project (not
// session), bounded, and its prompts are the last plain user messages, newest
// first — no tool results, no sidechain, nothing over 200 characters. A
// SubagentStop writes nothing: a subagent's turn is not the session's last turn.
func TestTheStopHookWritesTheLastTurnNote(t *testing.T) {
	stateDir, repo, transcript := lastTurnFixture(t)
	names, note := runStopForNote(t, stateDir, repo, transcript, "Stop")
	if len(names) != 1 {
		t.Fatalf("expected one note, got %v", names)
	}
	if names[0] == "s1" || !strings.HasPrefix(names[0], filepath.Base(repo)+"-") {
		t.Errorf("the note is keyed %q; it must be keyed by the project (basename plus checksum), never by the session id", names[0])
	}
	for _, want := range []string{"at=", "session=s1\n", "branch=task/note\n", "dirty=1\n", "touched=10\n"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if n := strings.Count(note, "\nfile="); n != 8 {
		t.Errorf("note carries %d file= lines, want 8:\n%s", n, note)
	}
	var prompts []string
	for _, line := range strings.Split(note, "\n") {
		if strings.HasPrefix(line, "prompt=") {
			prompts = append(prompts, strings.TrimPrefix(line, "prompt="))
		}
	}
	if len(prompts) != 3 {
		t.Fatalf("note carries %d prompt= lines, want 3 (the plain user messages):\n%s", len(prompts), note)
	}
	if !strings.HasPrefix(prompts[0], "third prompt") || !strings.HasPrefix(prompts[1], "second prompt") || !strings.HasPrefix(prompts[2], "first prompt") {
		t.Errorf("prompts are not newest first: %q", prompts)
	}
	for _, p := range prompts {
		if len([]rune(p)) > 200 {
			t.Errorf("a prompt line is %d runes, over the 200 cut", len([]rune(p)))
		}
		if strings.Contains(p, "tool_use_id") || strings.Contains(p, "subagent") {
			t.Errorf("a prompt line carries a tool result or a sidechain message: %q", p)
		}
	}

	names, _ = runStopForNote(t, t.TempDir(), repo, transcript, "SubagentStop")
	if len(names) != 0 {
		t.Errorf("a SubagentStop wrote a note %v; a subagent's turn is not the session's last turn", names)
	}
}

// TestTheLastTurnNoteIsOffWhenAsked: the two knobs are read.
func TestTheLastTurnNoteIsOffWhenAsked(t *testing.T) {
	stateDir, repo, transcript := lastTurnFixture(t)
	if names, _ := runStopForNote(t, stateDir, repo, transcript, "Stop", "AGENTSMEMORY_LAST_TURN=off"); len(names) != 0 {
		t.Errorf("AGENTSMEMORY_LAST_TURN=off still wrote %v", names)
	}
	_, note := runStopForNote(t, stateDir, repo, transcript, "Stop", "AGENTSMEMORY_LAST_TURN_PROMPTS=1")
	if n := strings.Count(note, "\nprompt="); n != 1 {
		t.Errorf("AGENTSMEMORY_LAST_TURN_PROMPTS=1 recorded %d prompts:\n%s", n, note)
	}
}

// runRecallAfterStop writes a real last-turn note by driving the shipped Stop
// hook, optionally moves the repo to another branch, then drives the recall
// hook with the given source against a stub CLI that records every call.
//
// The note is written by the real writer rather than by the test, because the
// one thing no unit test could otherwise see is whether the key the Stop hook
// derives and the key the recall hook derives agree (T2's Stop Condition).
func runRecallAfterStop(t *testing.T, source string, switchBranch bool) (stdout string, calls []string) {
	t.Helper()
	stateDir, repo, transcript := lastTurnFixture(t)
	if names, _ := runStopForNote(t, stateDir, repo, transcript, "Stop"); len(names) != 1 {
		t.Fatalf("the Stop hook wrote %v notes; the fixture is broken", names)
	}
	if switchBranch {
		cmd := testexec.Command(t, "git", "checkout", "-q", "-b", "other")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git checkout -b: %v\n%s", err, out)
		}
	}
	stubDir := t.TempDir()
	callsFile := filepath.Join(stubDir, "calls")
	stub := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> " + callsFile + "\necho 'a hit'\n"
	if err := os.WriteFile(filepath.Join(stubDir, "aiagentmemory"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := testexec.Command(t, "bash", filepath.Join("hooks", "agentsmemory-recall-hook.sh"))
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"s2","source":"` + source + `"}`)
	var outb, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &outb, &errb
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+":"+os.Getenv("PATH"), "CLAUDE_PROJECT_DIR="+repo,
		"AGENTSMEMORY_STATE_DIR="+stateDir, "AGENTSMEMORY_WING=wing_acme", "AGENTSMEMORY_TOKEN=t")
	if err := cmd.Run(); err != nil {
		t.Fatalf("the recall hook exited non-zero: %v\n%s", err, errb.String())
	}
	raw, _ := os.ReadFile(callsFile)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			calls = append(calls, line)
		}
	}
	return outb.String(), calls
}

// TestAColdStartOnTheSameBranchHandsBackTheLastTurn: a startup on the branch
// the last turn was on opens with the note's facts and asks the wing's
// checkpoint room second, floor off — the same work, continued.
func TestAColdStartOnTheSameBranchHandsBackTheLastTurn(t *testing.T) {
	out, calls := runRecallAfterStop(t, "startup", false)
	if !strings.HasPrefix(out, "Last turn (") {
		t.Fatalf("the injection does not open with the last-turn block:\n%s", out)
	}
	for _, want := range []string{"session s1", "branch task/note at ", "1 uncommitted", "edited: dir/filea.go", "prompt: third prompt: ship it", "prompt: second prompt: ", "prompt: first prompt"} {
		if !strings.Contains(out, want) {
			t.Errorf("the block lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Before compaction") {
		t.Errorf("a startup rendered the compaction header:\n%s", out)
	}
	if len(calls) != 2 {
		t.Fatalf("expected two calls, got %d:\n%s", len(calls), strings.Join(calls, "\n"))
	}
	for _, want := range []string{"room=llm_open_threads", "max_distance=0 ", "wing=wing_acme"} {
		if !strings.Contains(calls[1], want) {
			t.Errorf("the second call on a matching branch lacks %q: %s", want, calls[1])
		}
	}
}

// TestAColdStartOnAnotherBranchKeepsCraft: the block is still handed back on a
// resume, the second call stays wing_craft when the branch differs (cold work),
// and a compact start never reads the last-turn note — ADR-059's own note is
// fresher there.
func TestAColdStartOnAnotherBranchKeepsCraft(t *testing.T) {
	out, calls := runRecallAfterStop(t, "resume", true)
	if !strings.HasPrefix(out, "Last turn (") || !strings.Contains(out, "branch task/note at ") {
		t.Errorf("a resume on another branch does not hand the note back:\n%s", out)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], "wing=wing_craft") {
		t.Errorf("the second call on a differing branch is not craft:\n%s", strings.Join(calls, "\n"))
	}
	out, calls = runRecallAfterStop(t, "compact", false)
	if strings.Contains(out, "Last turn (") {
		t.Errorf("a compact start read the last-turn note:\n%s", out)
	}
	for _, c := range calls {
		if strings.Contains(c, "llm_open_threads") {
			// No PreCompact note exists in this fixture, so ADR-059's block is
			// silent; but the compact checkpoint call is ADR-059's and stays.
			return
		}
	}
	t.Errorf("a compact start no longer asks the checkpoint room (ADR-059 regression):\n%s", strings.Join(calls, "\n"))
}

// TestBothProtocolsReadTheWakeUp: the two copies of the grounding protocol —
// the /am command and the bootstrap file the installer writes — both tell a
// session, inside Step 1c, to read the wake-up's `Last turn` and `checkpoint:`
// blocks before planning and to ask llm_open_threads itself when neither is
// there. Two copies is the repository's recorded hazard; this pins them equal
// on the one sentence ADR-061 adds.
func TestBothProtocolsReadTheWakeUp(t *testing.T) {
	for _, asset := range []string{"commands/am.md", "bootstrap.md"} {
		body, err := assets.ReadFile(asset)
		if err != nil {
			t.Fatalf("read embedded %s: %v", asset, err)
		}
		text := string(body)
		start := strings.Index(text, "1c.")
		end := strings.Index(text, "Reconcile the three")
		if start < 0 || end < 0 || end < start {
			t.Fatalf("%s has no Step 1c bounded by 'Reconcile the three'; the sentence has nowhere to live", asset)
		}
		step := text[start:end]
		for _, want := range []string{"Last turn", "checkpoint:", "llm_open_threads"} {
			if !strings.Contains(step, want) {
				t.Errorf("%s Step 1c does not mention %q: a session is not told to read the wake-up before planning", asset, want)
			}
		}
	}
}
