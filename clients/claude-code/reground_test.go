package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// TestEverySkillEmbeddedIsInstalled covers the rung the installer's own tests
// cannot see: a skill that ships inside the binary and reaches no config dir.
//
// The embed is a GLOB, so adding skills/<name>/SKILL.md ships it whatever anyone
// remembers; the install loop used to read a hand-kept slice, so the two could
// disagree in the silent direction — present in the binary, installed by nothing,
// reported by no check. ADR-062 added the second skill and made that reachable
// for the first time. Deriving the list from the embed is the fix; this is what
// fails if someone lists them by hand again.
func TestEverySkillEmbeddedIsInstalled(t *testing.T) {
	entries, err := assets.ReadDir("skills")
	if err != nil {
		t.Fatalf("no embedded skills directory: %v", err)
	}
	var embedded []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := assets.ReadFile("skills/" + e.Name() + "/SKILL.md"); err == nil {
			embedded = append(embedded, e.Name())
		}
	}
	if len(embedded) < 2 {
		t.Fatalf("only %d skill(s) embedded; this check cannot see a divergence with fewer than two", len(embedded))
	}
	installed := map[string]bool{}
	for _, n := range nativeSkillAssets() {
		installed[n] = true
	}
	for _, n := range embedded {
		if !installed[n] {
			t.Errorf("skill %q is embedded and would be installed by nothing — it ships in the binary and reaches no config dir", n)
		}
	}
}

// TestACompactStartTellsTheSessionToReGround drives the two real scripts, so the
// key the PreCompact hook writes and the key the recall hook reads cannot drift
// apart unnoticed — the same reason ADR-059's own test drives them rather than
// asserting on a fixture.
//
// It asserts the SELECTION and the SUBJECT: that the pause names the task the
// session was on, and that the task is the last PLAIN user turn — not a hook's
// own injection and not a sidechain, both of which are `type=user` in a
// transcript and would name the wrong work.
func TestACompactStartTellsTheSessionToReGround(t *testing.T) {
	state := t.TempDir()
	transcript := filepath.Join(state, "t.jsonl")
	lines := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"the task that was interrupted"}}`,
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":"a subagent turn"}}`,
		`{"type":"user","message":{"role":"user","content":"agentsmemory recalled this about your request"}}`,
		// A tool RESULT is also type=user, and its content is an ARRAY whose
		// elements carry their own "content". Review of #280 reproduced a pattern
		// keyed on "type":"user" naming this as the task; the fixture that missed
		// it had only plain-string turns, which is the shape the kit itself writes.
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"tool output that is not a task"}]}}`,
	}, "\n")
	if err := os.WriteFile(transcript, []byte(lines+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"regroundprobe","transcript_path":"` + transcript + `","trigger":"auto"}`
	runHook(t, "agentsmemory-precompact-hook.sh", payload, state)

	note := filepath.Join(state, "agentsmemory-precompact", "regroundprobe")
	body, err := os.ReadFile(note)
	if err != nil {
		t.Fatalf("the PreCompact hook wrote no note: %v", err)
	}
	if !strings.Contains(string(body), "prompt=the task that was interrupted") {
		t.Errorf("the note does not carry the task in flight; got:\n%s", body)
	}
	for _, wrong := range []string{"a subagent turn", "agentsmemory recalled", "tool output that is not a task"} {
		if strings.Contains(string(body), wrong) {
			t.Errorf("the note names %q as the task: a sidechain or an injection was read as a user turn", wrong)
		}
	}

	out := runHook(t, "agentsmemory-recall-hook.sh", `{"session_id":"regroundprobe","source":"compact"}`, state)
	if !strings.Contains(out, "PAUSE") {
		t.Errorf("a compact start does not tell the session to stop before continuing; got:\n%s", out)
	}
	if !strings.Contains(out, "/amm the task that was interrupted") {
		t.Errorf("the pause does not name the task to re-ground on; got:\n%s", out)
	}
}

// TestAStartThatIsNotACompactIsUnchanged holds the blast radius: startup, resume
// and clear are the paths every session takes, and this record changes none of
// them. A regression here is silent — the injection would simply gain a line
// nobody asked for on every session start.
func TestAStartThatIsNotACompactIsUnchanged(t *testing.T) {
	state := t.TempDir()
	// ⚠ THE NOTE IS SEEDED, AND THAT IS THE WHOLE POINT OF THIS FIXTURE.
	// The directive is guarded by `$SOURCE = compact` AND a non-empty note. With
	// no note on disk the second condition is false for every source, so the
	// block never runs and this test passes whatever the source guard says — it
	// would sit one layer below the mechanism it claims to hold. Measured
	// 2026-09-05 on T1's S6: the `only on a compaction` mutant SURVIVED against
	// an unseeded fixture, the same way it survived twice on T3 before 1fcc2da.
	// Seeding a note makes the source the only remaining deciding condition, so
	// widening that guard is the one change this test can still see.
	noteDir := filepath.Join(state, "agentsmemory-precompact")
	if err := os.MkdirAll(noteDir, 0o700); err != nil {
		t.Fatal(err)
	}
	note := "at=2026-09-05T00:00:00Z\ntrigger=auto\nbranch=main\nhead=deadbee\ndirty=0\ntouched=0\nprompt=the task that was interrupted\n"
	if err := os.WriteFile(filepath.Join(noteDir, "regroundprobe"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"startup", "resume", "clear"} {
		out := runHook(t, "agentsmemory-recall-hook.sh", `{"session_id":"regroundprobe","source":"`+source+`"}`, state)
		if strings.Contains(out, "PAUSE") || strings.Contains(out, "/amm") {
			t.Errorf("source=%s carries the re-ground directive; only a compaction should: %s", source, out)
		}
	}
}

// runHook executes one shipped hook script with the payload on stdin and the
// recall itself off, so the assertions read the note-handling block and never a
// live palace.
func runHook(t *testing.T, script, payload, state string) string {
	t.Helper()
	cmd := testexec.Command(t, "bash", filepath.Join("hooks", script))
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(),
		"AGENTSMEMORY_STATE_DIR="+state,
		"AGENTSMEMORY_RECALL=off",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
	return string(out)
}
