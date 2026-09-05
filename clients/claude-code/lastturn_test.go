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
