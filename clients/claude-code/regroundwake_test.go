package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// monitorScript is the bash fence in the `/am` command that a session arms as a
// persistent monitor. It is extracted rather than copied because copying is what
// this test exists to catch: the hook writes a marker into one directory and the
// shipped instruction tells the session to watch another, and nothing else in the
// tree compares them.
var monitorScript = regexp.MustCompile("(?s)```bash\n(.*?agentsmemory-reground.*?)```")

// TestACompactionWakesTheSessionThroughTheMonitor drives the real PreCompact
// hook, the real recall hook, and the real monitor script out of the shipped
// `/am` command, and fails unless the script emits a line naming the task.
//
// It exists because ADR-062 shipped the re-ground as an INSTRUCTION and recorded
// a trigger as impossible — "no hook can invoke a skill, on a timer or
// otherwise, and nothing outside a session can make it take a turn". The first
// clause is true; the conclusion was false, and it was written into the record,
// the hook's comment and this command at once. A persistent monitor's stdout
// line arrives as a notification and a notification makes the session take a
// turn, so the hook does not need to invoke anything: it leaves a marker whose
// APPEARANCE is the event.
//
// The path agreement is proved by running the two halves rather than by
// comparing two string literals, for the reason
// TestTheSocketPlaceholderIsAcceptedByTheGuard already records: equality between
// two things you typed pins nothing.
//
// ⚠ IT RUNS THE PAIR TWICE, AND THE SECOND RUN IS THE ONE THAT EARNS ITS PLACE.
// Both files address the directory as ${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}},
// so a test that always sets AGENTSMEMORY_STATE_DIR never executes the default
// branch — measured 2026-09-05, the first version of this test PASSED with the
// command's default drifted to $HOME/.local/state, which is a monitor watching a
// directory nothing writes to. The "default" mode leaves that variable unset so
// the fallback each file computes for itself is the thing under test.
func TestACompactionWakesTheSessionThroughTheMonitor(t *testing.T) {
	for _, mode := range []string{"explicit state dir", "default state dir"} {
		t.Run(mode, func(t *testing.T) {
			state := t.TempDir()
			env := regroundEnv(state, mode == "explicit state dir")

			transcript := filepath.Join(state, "t.jsonl")
			line := `{"type":"user","message":{"role":"user","content":"the task that was interrupted"}}` + "\n"
			if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			payload := `{"session_id":"wakeprobe","transcript_path":"` + transcript + `","trigger":"auto"}`
			runHookEnv(t, "agentsmemory-precompact-hook.sh", payload, env)
			runHookEnv(t, "agentsmemory-recall-hook.sh", `{"session_id":"wakeprobe","source":"compact"}`, env)

			marker := filepath.Join(state, "agentsmemory-reground", "wakeprobe")
			body, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("the recall hook wrote no re-ground marker on a compaction: %v", err)
			}
			if got := strings.TrimSpace(string(body)); got != "the task that was interrupted" {
				t.Errorf("marker carries %q, not the task in flight", got)
			}

			// The shipped script, verbatim, against the marker the hook just wrote.
			doc, err := os.ReadFile(filepath.Join("commands", "am.md"))
			if err != nil {
				t.Fatal(err)
			}
			m := monitorScript.FindSubmatch(doc)
			if m == nil {
				t.Fatal("the /am command carries no monitor script naming agentsmemory-reground; the instruction to arm the wake is gone or renamed")
			}
			cmd := testexec.Command(t, "bash", "-c", string(m[1]))
			cmd.Env = env
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// The loop never exits on its own; testexec kills its process group when
			// this test ends, which is the whole reason children go through it.
			lines := make(chan string, 1)
			go func() {
				s := bufio.NewScanner(out)
				for s.Scan() {
					if strings.TrimSpace(s.Text()) != "" {
						lines <- s.Text()
						return
					}
				}
				close(lines)
			}()
			select {
			case got, ok := <-lines:
				if !ok {
					t.Fatal("the monitor script exited without emitting an event for a marker that exists")
				}
				if !strings.Contains(got, "/amm the task that was interrupted") {
					t.Errorf("the monitor woke the session without naming the task: %q", got)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the monitor script emitted nothing for a marker the hook wrote — the two halves resolve different directories, which is a trigger that cannot fire")
			}
		})
	}
}

// TestOnlyACompactionArmsTheWake holds the blast radius. A marker written on an
// ordinary start would wake every session into a re-ground it does not need, and
// the failure is silent in the expensive direction: the notification arrives and
// the session obeys it.
func TestOnlyACompactionArmsTheWake(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear"} {
		state := t.TempDir()
		runHookEnv(t, "agentsmemory-recall-hook.sh", `{"session_id":"wakeprobe","source":"`+source+`"}`, regroundEnv(state, true))
		if _, err := os.Stat(filepath.Join(state, "agentsmemory-reground", "wakeprobe")); err == nil {
			t.Errorf("source=%s wrote a re-ground marker; only a compaction should", source)
		}
	}
}

// regroundEnv builds the child environment for one addressing mode. When
// explicit is false AGENTSMEMORY_STATE_DIR is REMOVED rather than left to the
// ambient value, so the fallback the scripts compute from TMPDIR is what runs —
// appending to os.Environ() alone cannot unset an inherited variable.
func regroundEnv(state string, explicit bool) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "AGENTSMEMORY_STATE_DIR="),
			strings.HasPrefix(kv, "TMPDIR="),
			strings.HasPrefix(kv, "AGENTSMEMORY_RECALL="):
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "AGENTSMEMORY_RECALL=off", "TMPDIR="+state)
	if explicit {
		env = append(env, "AGENTSMEMORY_STATE_DIR="+state)
	}
	return env
}

// runHookEnv is runHook with the environment supplied, so one test can drive the
// same script under both addressing modes.
func runHookEnv(t *testing.T, script, payload string, env []string) string {
	t.Helper()
	cmd := testexec.Command(t, "bash", filepath.Join("hooks", script))
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
	return string(out)
}
