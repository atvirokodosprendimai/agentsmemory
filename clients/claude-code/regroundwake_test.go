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

			// ⚠ THE ORDER IS THE REAL ONE, AND THE FIRST VERSION HAD IT BACKWARDS.
			// `/am` arms the monitor DURING a session; the compaction that writes
			// the marker happens later. A test that writes the marker first and
			// then arms is testing a sequence that never occurs, and it hid the
			// defect below: review of #283 reproduced a monitor replaying every
			// marker already on disk the moment it was armed.
			dir := filepath.Join(state, "agentsmemory-reground")
			if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
				t.Fatal(err)
			}
			// A marker from a session that finished long ago. Markers outlive their
			// sessions by design, so this is the ordinary state of the directory —
			// and re-grounding a new session on it is worse than not waking at all.
			if err := os.WriteFile(filepath.Join(dir, "oldsession"), []byte("an old task from a previous session\n"), 0o600); err != nil {
				t.Fatal(err)
			}

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
			lines := make(chan string, 8)
			go func() {
				s := bufio.NewScanner(out)
				for s.Scan() {
					if strings.TrimSpace(s.Text()) != "" {
						lines <- s.Text()
					}
				}
				close(lines)
			}()

			// Now the compaction, with the watch already running.
			transcript := filepath.Join(state, "t.jsonl")
			line := `{"type":"user","message":{"role":"user","content":"the task that was interrupted"}}` + "\n"
			if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			payload := `{"session_id":"wakeprobe","transcript_path":"` + transcript + `","trigger":"auto"}`
			runHookEnv(t, "agentsmemory-precompact-hook.sh", payload, env)
			runHookEnv(t, "agentsmemory-recall-hook.sh", `{"session_id":"wakeprobe","source":"compact"}`, env)

			var got []string
		collect:
			for {
				select {
				case l, ok := <-lines:
					if !ok {
						break collect
					}
					got = append(got, l)
					if strings.Contains(l, "/amm the task that was interrupted") {
						break collect
					}
				case <-time.After(30 * time.Second):
					t.Fatalf("the monitor emitted nothing for the marker the hook wrote — the two halves resolve different directories, which is a trigger that cannot fire. saw: %q", got)
				}
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, "/amm the task that was interrupted") {
				t.Errorf("the monitor woke the session without naming the task: %q", joined)
			}
			// The two ways a wake is WORSE than no wake: it names finished work, or
			// it names nothing at all. A subdirectory `cat`s to the empty string.
			if strings.Contains(joined, "an old task from a previous session") {
				t.Errorf("the monitor replayed a marker that was already on disk when it was armed — every new session would re-ground on somebody else's finished work: %q", joined)
			}
			if strings.Contains(joined, "/amm `") || strings.Contains(joined, "/amm ,") {
				t.Errorf("the monitor emitted an empty task label, which grounds nothing: %q", joined)
			}

			// ⚠ THE SECOND COMPACTION OF THE SAME SESSION, which is the case the
			// first version of this loop could not serve. A marker is named for
			// the SESSION ID, so its path is constant for the session's whole
			// life; the loop kept a `seen` list keyed by that path, so the first
			// compaction pinned it and every later one was skipped in silence.
			// Measured 2026-09-05 on a live session: the second marker sat
			// unconsumed for eight minutes beside a two-second loop, while a
			// marker at a novel path in the same directory was consumed in under
			// six. One compaction per session is exactly the shape a single-shot
			// test cannot see, and the wake degraded as the session got longer —
			// which is when re-grounding matters most.
			second := `{"type":"user","message":{"role":"user","content":"the task the second compaction interrupted"}}` + "\n"
			if err := os.WriteFile(transcript, []byte(second), 0o600); err != nil {
				t.Fatal(err)
			}
			runHookEnv(t, "agentsmemory-precompact-hook.sh", payload, env)
			runHookEnv(t, "agentsmemory-recall-hook.sh", `{"session_id":"wakeprobe","source":"compact"}`, env)

			for {
				select {
				case l, ok := <-lines:
					if !ok {
						t.Fatalf("the monitor's output ended before the second compaction woke the session; saw: %q", got)
					}
					got = append(got, l)
					if strings.Contains(l, "/amm the task the second compaction interrupted") {
						return
					}
				case <-time.After(30 * time.Second):
					t.Fatalf("the second compaction of the same session emitted nothing: the marker path is the session id, so a loop that remembers paths it has emitted wakes a session exactly once and is silently dead for the rest of its life. saw: %q", got)
				}
			}
		})
	}
}

// TestOnlyACompactionArmsTheWake holds the blast radius. A marker written on an
// ordinary start would wake every session into a re-ground it does not need, and
// the failure is silent in the expensive direction: the notification arrives and
// the session obeys it.
// ⚠ IT MUST SEED THE NOTE FIRST, AND THE FIRST VERSION DID NOT — the mutant said
// so. The guard is `source = compact` AND a non-empty note; over a fresh temp dir
// there is no note, so the note check alone suppressed the block and deleting the
// `compact` test changed nothing. adr-verify recorded that mutant as SURVIVED
// against this fence. Writing the note first makes the source the only thing
// left deciding, which is the mechanism this test claims to cover.
func TestOnlyACompactionArmsTheWake(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear"} {
		state := t.TempDir()
		env := regroundEnv(state, true)

		transcript := filepath.Join(state, "t.jsonl")
		line := `{"type":"user","message":{"role":"user","content":"work from an earlier compaction"}}` + "\n"
		if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		runHookEnv(t, "agentsmemory-precompact-hook.sh",
			`{"session_id":"wakeprobe","transcript_path":"`+transcript+`","trigger":"auto"}`, env)
		if _, err := os.Stat(filepath.Join(state, "agentsmemory-precompact", "wakeprobe")); err != nil {
			t.Fatalf("the note was not seeded, so this test cannot bind the source guard: %v", err)
		}

		runHookEnv(t, "agentsmemory-recall-hook.sh", `{"session_id":"wakeprobe","source":"`+source+`"}`, env)
		if _, err := os.Stat(filepath.Join(state, "agentsmemory-reground", "wakeprobe")); err == nil {
			t.Errorf("source=%s wrote a re-ground marker with a note present; only a compaction should", source)
		}
	}
}

// TestASlashCommandIsNotTheTaskInFlight covers the case a live compaction found
// and no fixture had: the turn that TRIGGERS a compaction is usually the command
// that triggered it.
//
// Measured 2026-09-05 in this checkout — the real compaction ADR-062's follow-up
// asked for. The session was compacted by `/compact`, the note recorded
// `prompt=/compact`, and the injected directive read "your first action is
// `/amm /compact`". That is a label naming no work, produced on the one occasion
// the session cannot recover the work any other way, and it degrades the wake as
// much as the printed pause: the monitor would emit the same empty label.
func TestASlashCommandIsNotTheTaskInFlight(t *testing.T) {
	// Each case is the LAST turn before the compaction. Every one of them is
	// harness chrome that names no work, and the note must fall back past it.
	//
	// ⚠ THE WRAPPED CASE IS THE ONE THE FIXTURES MISSED, and only a live wake
	// found it: `^/` matches the bare spelling, but a slash command reaches the
	// transcript inside <command-name> tags, so its content starts with `<`.
	for _, tc := range []struct {
		name string
		last string
	}{
		{"bare slash command", `/compact`},
		{"wrapped slash command", `<command-message>am</command-message>\n<command-name>/am</command-name>\n<command-args>recall</command-args>`},
		{"local command stdout", `<local-command-stdout>Compacted</local-command-stdout>`},
		// A SIBLING OF THE ABOVE, and the case that proves the guard must match
		// the `<local-command` family rather than its members: narrowing the
		// pattern to `-stdout` while tidying the deny list let this one through
		// on the next real-transcript run, with every other case still green.
		{"local command caveat", `<local-command-caveat>Caveat: The messages below were generated by the user while running local commands.`},
		{"task notification", `<task-notification>\n<task-id>bz4bkmjl3</task-id>`},
		{"system reminder", `<system-reminder>background context</system-reminder>`},
		// The compounding one: after any compaction the harness injects this as a
		// plain user turn, so a session's SECOND compaction would label its wake
		// with the FIRST one's preamble. Prose, so no bracket rule reaches it.
		{"continuation preamble", `This session is being continued from a previous conversation that ran out of context.`},
		// Found by a reviewer running the extraction against a DIFFERENT
		// session's transcript — the fixtures here could not have produced it,
		// and neither could the session that fixed it, which has no peers.
		{"peer session message", `Another Claude session sent a message:\n<cross-session-message from=`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			env := regroundEnv(state, true)
			transcript := filepath.Join(state, "t.jsonl")
			lines := strings.Join([]string{
				`{"type":"user","message":{"role":"user","content":"the work that was actually in flight"}}`,
				`{"type":"user","message":{"role":"user","content":"` + tc.last + `"}}`,
			}, "\n") + "\n"
			if err := os.WriteFile(transcript, []byte(lines), 0o600); err != nil {
				t.Fatal(err)
			}
			runHookEnv(t, "agentsmemory-precompact-hook.sh",
				`{"session_id":"slashprobe","transcript_path":"`+transcript+`","trigger":"manual"}`, env)

			body, err := os.ReadFile(filepath.Join(state, "agentsmemory-precompact", "slashprobe"))
			if err != nil {
				t.Fatalf("no note: %v", err)
			}
			if !strings.Contains(string(body), "prompt=the work that was actually in flight") {
				t.Errorf("the note does not fall back past %s to the last real turn:\n%s", tc.name, body)
			}
		})
	}
}

// TestBothProtocolsNameTheRegroundWake pins the two copies of the grounding
// protocol equal on the wake, the way TestBothProtocolsReadTheWakeUp already
// pins them on ADR-061's sentence.
//
// The hazard is this repository's recorded one: `/am` and `bootstrap.md` are two
// copies of one protocol, and the copy nobody maintains is the one that goes
// wrong. It also pins the CAVEAT, because `Monitor` is a Claude Code tool and
// codex and pi run the same bootstrap — a copy that described the wake without
// saying so would promise those agents a trigger they cannot arm.
func TestBothProtocolsNameTheRegroundWake(t *testing.T) {
	for _, asset := range []string{"commands/am.md", "bootstrap.md"} {
		body, err := assets.ReadFile(asset)
		if err != nil {
			t.Fatalf("read embedded %s: %v", asset, err)
		}
		text := strings.Join(strings.Fields(string(body)), " ")
		for _, want := range []string{
			"The re-ground wake is Claude-only.",
			"codex and pi",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not carry %q: the two copies of the protocol disagree about the wake", asset, want)
			}
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
