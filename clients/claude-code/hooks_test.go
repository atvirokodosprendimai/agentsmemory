package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runStopHook executes the real Stop hook against a fake /stats server and
// returns everything it wrote to stderr.
//
// The hook is shell, so this drives the shipped script rather than a Go
// re-implementation of it — a re-implementation would pass while the file that
// actually runs on every Stop said something else.
func runStopHook(t *testing.T, statsBody string, env ...string) string {
	t.Helper()
	out, _ := runStopHookWithInput(t,
		`{"hook_event_name":"Stop","stop_hook_active":false}`, statsBody,
		append([]string{"AGENTSMEMORY_STOP_HOOK=on"}, env...)...)
	if strings.TrimSpace(out) == "" {
		t.Fatalf("the hook produced no output at all, so every assertion below would be vacuous")
	}
	return out
}

// runStopHookWithInput drives the same script with a caller-supplied event JSON
// and returns its stderr and exit code.
//
// The mode is NOT set here. runStopHook's callers want "on" and the subagent
// tests want "once" or a second switch, and appending a duplicate key to the
// child's environment leaves which one wins up to the platform — a test whose
// subject is a mode must set exactly one.
//
// Nor does it fatal on empty output: silence is the expected result for a
// disabled hook, and asserting on it is this task's job rather than the helper's.
func runStopHookWithInput(t *testing.T, input, statsBody string, env ...string) (string, int) {
	t.Helper()
	dir := t.TempDir()

	// A stand-in `curl` that ignores its arguments and prints the body. The hook
	// reaches the server through curl and nothing else, so replacing curl is
	// enough to control what the report is built from.
	fakeCurl := filepath.Join(dir, "curl")
	script := "#!/bin/sh\ncat <<'BODY'\n" + statsBody + "\nBODY\n"
	if err := os.WriteFile(fakeCurl, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}

	// A missing bash must FAIL, never skip and never pass quietly. Without it the
	// command does not start, stderr is empty, and every assertion of the form
	// "the output does not contain X" passes for free — which is exactly the
	// vacuous green this repository has a rule about. The hook's shebang is bash
	// and it uses bash-isms, so bash is a requirement of the test, not a detail.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is not installed, so the shipped hook cannot be executed: %v. "+
			"This test asserts on the hook's OUTPUT; without bash every negative assertion "+
			"would pass against an empty string.", err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-stop-hook.sh")
	cmd := exec.Command("bash", hook)
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Same injection the installer prefixes onto the registered command.
		// Without it the helper skips /stats and every report assertion is vacuous.
		"AGENTSMEMORY_MCP_URL=http://127.0.0.1:9/mcp",
	)
	cmd.Env = append(cmd.Env, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// The hook exits 2 by design, so a non-zero status is expected and the exit
	// CODE is a subject in its own right: exit 0 means the text never reaches the
	// agent, which is indistinguishable from a hook that was never registered.
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run hook: %v (stderr: %s)", err, stderr.String())
		}
		code = ee.ExitCode()
	}
	return stderr.String(), code
}

func repoRootForHooks(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(wd)) // clients/claude-code -> repo root
}

// statsWithSuggestions is a /stats body in the shape the server emits, including
// the "  write: " lines the hook re-renders as a task list.
const statsWithSuggestions = `memory, this session: 40 searches recalled, 38 answered (95%), 12 memories filed
  wing_acme                20/20 answered (100%), 52 drawers
  wing_alpha               18/20 answered (90%), 40 drawers
  write: 1x how long is the retention window [wing_alpha]
  write: 1x which service owns the invoice pdf [wing_acme]`

// TestNoTaskListWithoutAttribution is the whole point of this task.
//
// The "memories to write" section is not a statistic. It is a TASK LIST, and the
// server cannot say whose searches it is built from: search_events has no session
// column, so /stats filters by team and time only. On a machine running several
// sessions against one palace — eleven were live when this was found — each
// session is handed every other session's unanswered questions under a heading
// that reads as its own.
//
// Following it means writing a memory about a question you never asked, into a
// wing you never opened, from no evidence you hold. One session caught that and
// refused. The next will not, and the more diligent the agent the worse the
// outcome — so the list is printed only when the recalls can be attributed.
func TestNoTaskListWithoutAttribution(t *testing.T) {
	out := runStopHook(t, statsWithSuggestions)

	if strings.Contains(out, "memories to write") {
		t.Errorf("the hook printed a task list it cannot attribute to this session:\n%s", out)
	}
	if strings.Contains(out, "retention window") {
		t.Errorf("another session's unanswered question was handed to this one as a to-do:\n%s", out)
	}
	// The NUMBERS still print. They are useful at any scope; it is the task list
	// that is only useful when it is yours.
	if !strings.Contains(out, "searches recalled") {
		t.Errorf("the statistics were suppressed along with the task list; they are worth keeping:\n%s", out)
	}
}

// TestReportNamesItsPopulation: a statistic that names its population is useful
// at any scope, and one that does not is the defect. The heading must say whose
// recalls these are rather than leaving "this session" to be assumed.
func TestReportNamesItsPopulation(t *testing.T) {
	out := runStopHook(t, statsWithSuggestions)
	if !strings.Contains(out, "every session on this server") {
		t.Errorf("the report does not say whose recalls it describes, so it reads as this session's:\n%s", out)
	}
	if strings.Contains(out, "memory, this session:") {
		t.Errorf("the report still claims to be this session's, which the server cannot know:\n%s", out)
	}
}

// TestStopHookStillNudgesAndNeverBreaks: the checkpoint is the hook's actual job
// and must survive every change to the reporting half.
func TestStopHookStillNudgesAndNeverBreaks(t *testing.T) {
	out := runStopHook(t, statsWithSuggestions)
	for _, want := range []string{"am_diary_write", "am_kg_add", "am_add_drawer"} {
		if !strings.Contains(out, want) {
			t.Errorf("the persist checkpoint no longer names %s:\n%s", want, out)
		}
	}
}

// runSubagentHook drives the SubagentStart hook and returns its STDOUT.
//
// stdout, not stderr, and that is the whole contract: a SubagentStart hook
// injects context by printing a JSON envelope on stdout, where the Stop hook
// merely talks to a human on stderr. A hook that wrote its envelope to stderr
// would look correct in a terminal and inject nothing.
func runSubagentHook(t *testing.T, env ...string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is not installed, so the shipped hook cannot be executed: %v. "+
			"This test asserts on the hook's OUTPUT; without bash every assertion below "+
			"would be made against an empty string.", err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks",
		"agentsmemory-subagent-start-hook.sh")
	cmd := exec.Command("bash", hook)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SubagentStart"}`)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run hook: %v (stderr: %s)", err, stderr.String())
		}
	}
	return stdout.String(), code
}

// TestSubagentStartHookEmitsAContextEnvelope pins the one thing the injector must
// get right: a well-formed hookSpecificOutput envelope on stdout.
//
// The measurement this hook exists for compares "the whole protocol" against "the
// whole protocol plus one paragraph next to the task". If the envelope is
// malformed the harness drops it silently, the paragraph never arrives, and the
// control and treatment arms become the same arm — producing a confident "the
// injection changed nothing" that is really "the injection never happened".
func TestSubagentStartHookEmitsAContextEnvelope(t *testing.T) {
	out, code := runSubagentHook(t, "AGENTSMEMORY_SUBAGENT_HOOK=on")
	if code != 0 {
		t.Errorf("hook exited %d; a SubagentStart hook that fails blocks the dispatch", code)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("the hook printed nothing on stdout, so every assertion below would be vacuous")
	}

	var env struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not the JSON envelope the harness parses: %v\n%s", err, out)
	}
	if env.HookSpecificOutput.HookEventName != "SubagentStart" {
		t.Errorf("hookEventName is %q, want SubagentStart — the harness matches on this and "+
			"drops what it does not recognise", env.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(env.HookSpecificOutput.AdditionalContext, "am_search") {
		t.Errorf("the injected context never names am_search, so it cannot be what causes a "+
			"subagent to call it:\n%s", env.HookSpecificOutput.AdditionalContext)
	}
}

// TestSubagentStartHookFailsOpen pins that the hook cannot break a dispatch.
//
// Same rule as the SessionStart verify hook: nothing here is worth blocking work
// for. Disabled, or with no server reachable, it must exit 0 — and when disabled
// it must emit NOTHING, because the control arm of T1's measurement is exactly
// "this hook produced no context". An injector that still printed something when
// switched off would make the two arms identical and the measurement meaningless.
func TestSubagentStartHookFailsOpen(t *testing.T) {
	out, code := runSubagentHook(t, "AGENTSMEMORY_SUBAGENT_HOOK=off")
	if code != 0 {
		t.Errorf("disabled hook exited %d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("the disabled hook still injected context, so T1's control arm would carry "+
			"the treatment:\n%s", out)
	}

	// A broken PATH stands in for every environment failure at once.
	out, code = runSubagentHook(t, "AGENTSMEMORY_SUBAGENT_HOOK=on", "PATH=/nonexistent")
	if code != 0 {
		t.Errorf("hook exited %d with an unusable PATH; it must fail OPEN and never block a "+
			"dispatch over bookkeeping (stdout was %q)", code, out)
	}
}

// runSubagentHookWithEnv is runSubagentHook's stdout-only sibling, used by the
// installer tests that care about the context's CONTENT rather than its exit code.
func runSubagentHookWithEnv(t *testing.T, env ...string) string {
	t.Helper()
	out, _ := runSubagentHook(t, env...)
	return out
}

// subagentStopEvent is a REAL SubagentStop payload, captured from this harness by
// registering a hook that did nothing but tee its stdin, and dispatching one
// trivial subagent. Local paths and ids are neutralised; the SHAPE is verbatim.
//
// It is captured rather than written because a hand-authored fixture proves the
// branch works for the JSON the test's author imagined. Two of its fields decide
// this whole task and neither could be settled by reading:
//
//   - `stop_hook_active` IS sent on SubagentStop. The published payload reference
//     does not list it, and without it an exit-2 nudge would re-fire forever. It
//     is here, so the existing loop guard covers subagents too.
//   - `session_id` is IDENTICAL to the parent session's. That is what makes the
//     `once`-per-session marker a collision rather than a theory: the main
//     session and every subagent under it key the same file.
const subagentStopEvent = `{"session_id":"11111111-2222-3333-4444-555555555555",` +
	`"transcript_path":"/tmp/projects/example/11111111.jsonl","cwd":"/tmp/work",` +
	`"prompt_id":"66666666-7777-8888-9999-000000000000","permission_mode":"default",` +
	`"agent_id":"a1b2c3d4e5f607182","agent_type":"general-purpose",` +
	`"hook_event_name":"SubagentStop","stop_hook_active":false,` +
	`"agent_transcript_path":"/tmp/projects/example/11111111/subagents/agent-a1b2c3d4e5f607182.jsonl",` +
	`"last_assistant_message":"pong","background_tasks":[],"session_crons":[]}`

// sessionStopEvent is the main-session Stop payload, for the same reason.
const sessionStopEvent = `{"session_id":"11111111-2222-3333-4444-555555555555",` +
	`"transcript_path":"/tmp/projects/example/11111111.jsonl",` +
	`"hook_event_name":"Stop","stop_hook_active":false}`

// TestStopHookAsksASubagentForFindingsNotASummary is the point of ADR-017 T3.
//
// A subagent is asked for what it FOUND — a drawer, a fact — and explicitly not
// for a session summary. The dispatcher writes that one. A diary entry per
// subagent is how a journal stops being read: a 16-way fan-out would file
// seventeen summaries of one piece of work, sixteen of them written by an agent
// that saw a sliver of it.
//
// It also pins the WING advice, which is not the same advice the start hook
// gives. There, a guessed wing costs a bad recall — noise. Here it costs a WRITE
// into another project's palace, which the protocol names as poisoning it. The
// asymmetry is the reason this assertion exists on the stop side at all.
func TestStopHookAsksASubagentForFindingsNotASummary(t *testing.T) {
	sub, code := runStopHookWithInput(t, subagentStopEvent, statsWithSuggestions,
		"AGENTSMEMORY_STOP_HOOK=on")
	if code != 2 {
		t.Errorf("subagent nudge exited %d, want 2 — any other code and the text never "+
			"reaches the agent, so the hook is registered, fires, and changes nothing", code)
	}
	for _, want := range []string{"am_add_drawer", "am_kg_add"} {
		if !strings.Contains(sub, want) {
			t.Errorf("the subagent nudge does not name %s:\n%s", want, sub)
		}
	}
	if strings.Contains(sub, "am_diary_write") {
		t.Errorf("a subagent is being asked for a session summary; its dispatcher writes "+
			"that, and one diary entry per subagent is how a journal stops being read:\n%s", sub)
	}
	// The wing: a wrong-wing READ is noise, a wrong-wing WRITE is another
	// project's palace. The subagent must be told to pass none.
	if !strings.Contains(sub, "no wing") {
		t.Errorf("the subagent nudge does not tell it to pass no wing, so it will guess one "+
			"and file this project's work somewhere else:\n%s", sub)
	}
	// The server-wide recall report belongs to a session, not to each of its
	// subagents. The fake curl in the helper serves it on demand, so its presence
	// here would mean the subagent branch ran the whole session path.
	if strings.Contains(sub, "searches recalled") {
		t.Errorf("the session recall report was printed into a subagent's nudge:\n%s", sub)
	}

	// And the two must actually DIFFER. Without this the mutant "use the session
	// nudge verbatim" survives everything above that the session nudge happens to
	// satisfy.
	session, _ := runStopHookWithInput(t, sessionStopEvent, statsWithSuggestions,
		"AGENTSMEMORY_STOP_HOOK=on")
	if sub == session {
		t.Errorf("a subagent gets the session checkpoint verbatim:\n%s", sub)
	}
}

// TestSubagentStopIsNotSwallowedByTheOnceGuard pins the collision the captured
// payload proved: SubagentStop carries the PARENT session's `session_id`, so the
// `once`-per-session marker — the default mode — is one file shared by the main
// session and every subagent under it.
//
// Both directions are defects and only one of them is obvious. The main session
// stopping first silences every subagent afterwards; a subagent stopping first
// silences the human's own checkpoint. The fix is that a subagent stop neither
// reads nor writes that marker.
func TestSubagentStopIsNotSwallowedByTheOnceGuard(t *testing.T) {
	t.Run("a main stop that already fired does not silence subagents", func(t *testing.T) {
		tmp := t.TempDir()
		// The marker the session path writes, created directly: the subject here is
		// the guard, not the path that happens to set it.
		marker := filepath.Join(tmp, "agentsmemory-stop-11111111-2222-3333-4444-555555555555.done")
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatalf("seed marker: %v", err)
		}
		out, code := runStopHookWithInput(t, subagentStopEvent, statsWithSuggestions,
			"AGENTSMEMORY_STOP_HOOK=once", "TMPDIR="+tmp)
		if code != 2 || !strings.Contains(out, "am_add_drawer") {
			t.Errorf("the subagent nudge was swallowed by the session's marker (exit %d):\n%s",
				code, out)
		}
	})

	t.Run("a subagent stop does not silence the session", func(t *testing.T) {
		tmp := t.TempDir()
		if _, code := runStopHookWithInput(t, subagentStopEvent, statsWithSuggestions,
			"AGENTSMEMORY_STOP_HOOK=once", "TMPDIR="+tmp); code != 2 {
			t.Fatalf("subagent nudge exited %d, want 2", code)
		}
		marker := filepath.Join(tmp, "agentsmemory-stop-11111111-2222-3333-4444-555555555555.done")
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("a subagent stop claimed the session's once-marker at %s, so the human's "+
				"own checkpoint is now silenced for the rest of the session", marker)
		}
		out, code := runStopHookWithInput(t, sessionStopEvent, statsWithSuggestions,
			"AGENTSMEMORY_STOP_HOOK=once", "TMPDIR="+tmp)
		if code != 2 || !strings.Contains(out, "am_diary_write") {
			t.Errorf("the session checkpoint was swallowed by a subagent's stop (exit %d):\n%s",
				code, out)
		}
	})
}

// TestUnknownStopEventKeepsTheSessionBehaviour pins the degradation.
//
// The subagent branch turns on a string match against `hook_event_name`. If a
// future harness spells it differently, the match fails — and what must happen
// then is the CURRENT behaviour, not silence. A branch that failed closed would
// take the human's checkpoint away too, on a rename nobody announced.
func TestUnknownStopEventKeepsTheSessionBehaviour(t *testing.T) {
	out, code := runStopHookWithInput(t,
		`{"session_id":"s","hook_event_name":"SomethingElse","stop_hook_active":false}`,
		statsWithSuggestions, "AGENTSMEMORY_STOP_HOOK=on")
	if code != 2 || !strings.Contains(out, "am_diary_write") {
		t.Errorf("an unrecognised stop event lost the session checkpoint (exit %d):\n%s", code, out)
	}
}

// TestSubagentStopHookCanBeDisabledOnItsOwn pins the switch.
//
// Exit 2 costs a subagent one extra turn, and a wide fan-out pays it once per
// branch. That is a real bill, so it has its own off switch rather than forcing a
// choice between subagent writes and the human's checkpoint.
func TestSubagentStopHookCanBeDisabledOnItsOwn(t *testing.T) {
	out, code := runStopHookWithInput(t, subagentStopEvent, statsWithSuggestions,
		"AGENTSMEMORY_STOP_HOOK=on", "AGENTSMEMORY_SUBAGENT_STOP_HOOK=off")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Errorf("disabling the subagent half still nudged (exit %d):\n%s", code, out)
	}
	// ...and the session half is untouched by that switch.
	sess, code := runStopHookWithInput(t, sessionStopEvent, statsWithSuggestions,
		"AGENTSMEMORY_STOP_HOOK=on", "AGENTSMEMORY_SUBAGENT_STOP_HOOK=off")
	if code != 2 || !strings.Contains(sess, "am_diary_write") {
		t.Errorf("the subagent switch also disabled the session checkpoint (exit %d):\n%s",
			code, sess)
	}
}

// TestRetiredStatsEnvNamesAreGone fails when a second name for /stats or its
// off-switch returns. AGENTSMEMORY_STATS_URL, AGENTSMEMORY_STATS_BASE, and
// AGENTSMEMORY_SESSION_REPORT were three names for one endpoint and two
// switches for one report; setting one did not move the others.
func TestRetiredStatsEnvNamesAreGone(t *testing.T) {
	root := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks")
	retired := []string{"AGENTSMEMORY_STATS_URL", "AGENTSMEMORY_STATS_BASE", "AGENTSMEMORY_SESSION_REPORT"}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		found++
		body, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range retired {
			if strings.Contains(string(body), name) {
				t.Errorf("%s still names %s; that is a second knob for the same /stats fetch", e.Name(), name)
			}
		}
	}
	if found < 4 {
		t.Fatalf("only %d hook scripts; a missing file would let this pass against nothing", found)
	}
}

// TestHookScriptsDoNotGuessAPalace fails when a hook hardcodes localhost or the
// hosted origin. The installer injects AGENTSMEMORY_MCP_URL; a default in the
// script is a second palace.
func TestHookScriptsDoNotGuessAPalace(t *testing.T) {
	root := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks")
	banned := []string{"localhost:8080", "127.0.0.1:8080", "aiagentmemory.dev"}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range banned {
			if strings.Contains(string(body), lit) {
				t.Errorf("%s hardcodes %s; the palace is AGENTSMEMORY_MCP_URL from the installer", e.Name(), lit)
			}
		}
	}
}

// TestStatsFetchUsesTheMCPOrigin drives the real scripts against a curl that
// records its URL, so a second origin (STATS_BASE, a localhost default) cannot
// return silently.
func TestStatsFetchUsesTheMCPOrigin(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "curl-args")
	fakeCurl := filepath.Join(dir, "curl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"" + argsFile + "\"\ncat <<'BODY'\n" + statsWithSuggestions + "\nBODY\n"
	if err := os.WriteFile(fakeCurl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is not installed: %v", err)
	}

	palace := "http://palace.test:9/mcp"
	run := func(scriptName string, stdout bool) {
		t.Helper()
		os.Remove(argsFile)
		hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", scriptName)
		cmd := exec.Command("bash", hook)
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop","stop_hook_active":false}`)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"AGENTSMEMORY_MCP_URL="+palace,
			"AGENTSMEMORY_STOP_HOOK=on",
			"AGENTSMEMORY_STATS=on",
		)
		var buf strings.Builder
		if stdout {
			cmd.Stdout = &buf
		} else {
			cmd.Stderr = &buf
		}
		_ = cmd.Run()
		raw, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("%s did not invoke curl: %v", scriptName, err)
		}
		if !strings.Contains(string(raw), "http://palace.test:9/stats?") {
			t.Errorf("%s curl args = %q, want origin derived from AGENTSMEMORY_MCP_URL", scriptName, raw)
		}
	}
	run("agentsmemory-stop-hook.sh", false)
	run("agentsmemory-session-end-hook.sh", true)
}

// TestSessionEndHonoursTheSharedStatsOffSwitch pins that SessionEnd and Stop
// share AGENTSMEMORY_STATS. A second name (SESSION_REPORT) meant turning stats
// off in one hook left the other printing.
func TestSessionEndHonoursTheSharedStatsOffSwitch(t *testing.T) {
	dir := t.TempDir()
	fakeCurl := filepath.Join(dir, "curl")
	if err := os.WriteFile(fakeCurl, []byte("#!/bin/sh\necho should-not-run\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-session-end-hook.sh")
	cmd := exec.Command("bash", hook)
	cmd.Stdin = strings.NewReader(`{}`)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTSMEMORY_MCP_URL=http://palace.test:9/mcp",
		"AGENTSMEMORY_STATS=off",
	)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("session-end: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "" {
		t.Errorf("AGENTSMEMORY_STATS=off still printed: %q", got)
	}
}

// gnuStatShim is `stat` as GNU coreutils implements it, in the two respects this
// probe depends on: -c takes the format, and -f is --file-system — a MODE, not a
// format flag — so its operands are FILENAMES. Given `-f %B FILE` it therefore
// fails on the bogus "%B" operand, prints the filesystem block for FILE anyway,
// and exits non-zero.
const gnuStatShim = `#!/bin/sh
mode=file
fmt=
while [ $# -gt 0 ]; do
  case "$1" in
    -f) mode=fs; shift ;;
    -c) fmt="$2"; shift 2 ;;
    *) break ;;
  esac
done
if [ "$mode" = fs ]; then
  rc=0
  for op in "$@"; do
    if [ -e "$op" ]; then
      echo "  File: \"$op\""
      echo "    ID: 0 Namelen: 255     Type: ext2/ext3"
      echo "Block size: 4096       Fundamental block size: 4096"
    else
      echo "stat: cannot read file system information for '$op': No such file or directory" >&2
      rc=1
    fi
  done
  exit $rc
fi
case "$fmt" in
  %W) echo @BIRTH@ ;;
  %Y) echo @MTIME@ ;;
  *) echo "stat: invalid format" >&2; exit 1 ;;
esac
`

// bsdStatShim is `stat` as macOS ships it: -f carries the format and there is no
// -c whatsoever, so the GNU probe is REJECTED rather than reinterpreted. That
// asymmetry is the whole reason the shipped order can be GNU-first and still work
// on both platforms.
const bsdStatShim = `#!/bin/sh
case "$1" in
  -c) echo "stat: illegal option -- c" >&2
      echo "usage: stat [-FLnq] [-f format | -l | -r | -s | -x] [-t timefmt] [file ...]" >&2
      exit 1 ;;
  -f) fmt="$2" ;;
  *) exit 1 ;;
esac
case "$fmt" in
  %B) echo @BIRTH@ ;;
  %m) echo @MTIME@ ;;
  *) exit 1 ;;
esac
`

// runStatsQuery sources the SHIPPED helper with a stand-in `stat` on PATH and
// returns the STATS_QUERY it built, plus anything the shell complained about.
//
// The helper is sourced rather than executed because that is how both hooks use
// it; driving a Go re-implementation of the probe would pass while the file that
// actually runs on every Stop said something else.
func runStatsQuery(t *testing.T, shim string, birth, mtime int64) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is not installed, so the shipped helper cannot be sourced: %v", err)
	}
	dir := t.TempDir()
	body := strings.NewReplacer(
		"@BIRTH@", strconv.FormatInt(birth, 10),
		"@MTIME@", strconv.FormatInt(mtime, 10),
	).Replace(shim)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(body), 0o755); err != nil {
		t.Fatalf("write fake stat: %v", err)
	}
	// A real file, because the helper refuses to compute a window without one.
	transcript := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	helper := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-stats.sh")
	// Same shell options the hooks source it under, so an unset variable or a
	// failing probe surfaces here the way it would in production.
	cmd := exec.Command("bash", "-u", "-o", "pipefail", "-c",
		`. "$1"; INPUT="$2"; agentsmemory_stats_query; printf '%s' "$STATS_QUERY"`,
		"stats-probe", helper, `{"hook_event_name":"Stop","transcript_path":"`+transcript+`"}`)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("source helper: %v (stderr: %s)", err, stderr.String())
	}
	return stdout.String(), stderr.String()
}

// TestSessionWindowSurvivesBothStatImplementations pins the probe ORDER.
//
// The first version of this helper probed BSD-form first (`stat -f %B || stat -c
// %W`). On macOS that is correct and every developer here saw it work. On Linux
// it is not a fallback at all: GNU's -f means --file-system, so "%B" is read as a
// filename, the multiline filesystem block lands in BORN, the `-gt` comparison
// dies with "integer expression expected", and the session window silently
// degrades to the fixed hours= default on every Linux install. It shipped, and a
// user on Linux reported it.
//
// The order is only testable by supplying the OTHER implementation, so both are
// faked. Swap the shipped probes back to BSD-first and the gnu subtests go red.
func TestSessionWindowSurvivesBothStatImplementations(t *testing.T) {
	// An hour of slack: the window is (now-born)/60+1, so any plausible test
	// runtime lands on the same integer.
	born := time.Now().Add(-time.Hour).Unix()

	for _, tc := range []struct {
		name         string
		shim         string
		birth, mtime int64
	}{
		{"gnu", gnuStatShim, born, born},
		// ext4 and friends report no birth time as 0. The guard must fall through
		// to mtime — and the FALLBACK probe carries the same order, so it is a
		// second copy of the same bug and needs its own case.
		{"gnu without birthtime", gnuStatShim, 0, born},
		{"bsd", bsdStatShim, born, born},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, stderr := runStatsQuery(t, tc.shim, tc.birth, tc.mtime)
			if want := "minutes=61&label=this%20session"; got != want {
				t.Errorf("STATS_QUERY = %q, want %q — the session window was not computed, so "+
					"the report silently describes a fixed window instead of this session", got, want)
			}
			if strings.Contains(stderr, "integer expression") {
				t.Errorf("the probe put a non-integer in BORN: %s", stderr)
			}
		})
	}
}

// TestSessionEndHookDoesNotWaitOnStdin pins the bound on a read the hook can do
// without.
//
// ⚠ THE HOOK'S RUNTIME WAS THE HARNESS'S STDIN, NOT ITS OWN WORK. `INPUT="$(cat)"`
// blocks until EOF, and SessionEnd is the only event that runs while the harness
// is tearing down — so whether it closes stdin and grants a slice before exiting
// is a race, lost as `SessionEnd hook … failed: Hook cancelled`. Reported
// 2026-08-31 from a Windows install: 1112ms with stdin closed promptly, 9187ms
// with a writer holding it open for 8s, on a box whose CPU was saturated by an
// embedding run. The payload is a PRECISION input — agentsmemory_stats_query
// sets a working window before consulting it — so the fallback already existed
// and this read was what made it unreachable.
func TestSessionEndHookDoesNotWaitOnStdin(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash is not installed: %v", err)
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "curl-args")
	fakeCurl := filepath.Join(dir, "curl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"" + argsFile + "\"\ncat <<'BODY'\n" + statsWithSuggestions + "\nBODY\n"
	if err := os.WriteFile(fakeCurl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-session-end-hook.sh")

	// A writer that holds the pipe open far longer than the hook may wait. The
	// hook must finish on its own clock, not the writer's.
	const writerHeld = 8 * time.Second
	cmd := exec.Command("bash", hook)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTSMEMORY_MCP_URL=http://palace.test:9/mcp",
		"AGENTSMEMORY_STATS=on",
	)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Never written to and never closed until well after the hook should be gone.
	go func() {
		time.Sleep(writerHeld)
		_ = stdin.Close()
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(writerHeld - 2*time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the hook was still running %s after start with stdin held open — its runtime "+
			"is the harness's teardown clock, which is the race that gets it cancelled",
			time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the hook took %s with stdin held open; it does one 10ms GET and must not wait "+
			"on a payload it can do without", elapsed)
	}
	// It must still have DONE its work — exiting fast by doing nothing is not the
	// fix, it is the same lost report by another route.
	if _, err := os.ReadFile(argsFile); err != nil {
		t.Errorf("the hook exited without fetching /stats: %v", err)
	}
	if !strings.Contains(stdout.String(), "recall") {
		t.Errorf("the hook printed no report:\n%s", stdout.String())
	}
}

// TestSessionEndStillNarrowsTheWindowFromThePayload is the falsifiability half.
//
// Bounding the read must not become ignoring stdin: that would pass a timing test
// while silently discarding the precision transcript_path buys, and the report
// would quietly describe a fixed 2-hour window on every session forever after.
func TestSessionEndStillNarrowsTheWindowFromThePayload(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash is not installed: %v", err)
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "curl-args")
	fakeCurl := filepath.Join(dir, "curl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"" + argsFile + "\"\ncat <<'BODY'\n" + statsWithSuggestions + "\nBODY\n"
	if err := os.WriteFile(fakeCurl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// A transcript that exists, so the query builder can date it.
	transcript := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-session-end-hook.sh")
	cmd := exec.Command("bash", hook)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionEnd","transcript_path":"` + transcript + `"}`)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTSMEMORY_MCP_URL=http://palace.test:9/mcp",
		"AGENTSMEMORY_STATS=on",
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("session-end: %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the hook did not invoke curl: %v", err)
	}
	if !strings.Contains(string(raw), "label=this%20session") {
		t.Errorf("the payload was not read: the report describes the fixed window rather than "+
			"this session, which is what bounding the read must NOT cost.\ncurl args = %q", raw)
	}
}

// TestSessionEndKeepsAPayloadThatArrivedOnAStdinStillOpen is the case neither
// earlier test covered, and the one the shipped code got wrong.
//
// ⚠ THE COMBINATION IS THE BUG. One test sent no payload and held the pipe open;
// the other sent a payload and closed it. `read -d ” -t` passed both while
// FAILING the case that actually happens at shutdown — payload delivered, stdin
// still open — because bash 3.2 discards a timed-out read's accumulated bytes
// when it has not seen the delimiter. Probed on 3.2.57, 2026-08-31, after a
// review declined to take the comment's word for it.
func TestSessionEndKeepsAPayloadThatArrivedOnAStdinStillOpen(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash is not installed: %v", err)
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "curl-args")
	fakeCurl := filepath.Join(dir, "curl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"" + argsFile + "\"\ncat <<'BODY'\n" + statsWithSuggestions + "\nBODY\n"
	if err := os.WriteFile(fakeCurl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-session-end-hook.sh")

	cmd := exec.Command("bash", hook)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTSMEMORY_MCP_URL=http://palace.test:9/mcp",
		"AGENTSMEMORY_STATS=on",
	)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The payload lands immediately; the pipe stays open long past the hook's
	// bound, which is what a harness tearing down looks like.
	go func() {
		_, _ = io.WriteString(stdin, `{"hook_event_name":"SessionEnd","transcript_path":"`+transcript+`"}`+"\n")
		time.Sleep(8 * time.Second)
		_ = stdin.Close()
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the hook was still running %s after start", time.Since(start))
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the hook did not invoke curl: %v", err)
	}
	if !strings.Contains(string(raw), "label=this%20session") {
		t.Errorf("a payload that ARRIVED was discarded because stdin stayed open, so the report "+
			"describes a fixed window instead of this session.\ncurl args = %q", raw)
	}
}

// TestStopHookHonoursTheStatsOffSwitch covers the caller that has no guard of its
// own.
//
// ⚠ FOUND BY MUTATION. The guard inside agentsmemory_stats_fetch is the ONLY
// thing that suppresses the Stop hook's recall report: the session-end hook exits
// on its own guard before the helper is ever sourced, so deleting the helper's
// line left every test green while a documented knob stopped working.
// TestSessionEndHonoursTheSharedStatsOffSwitch names both hooks and drives one.
//
// That is this repository's recurring defect wearing a shell script — a knob an
// operator is told about in the hook's own header — it names the shared stats
// off-switch as the way to suppress the recall report — and which nothing keeps
// working. The
// assertion is that curl is never invoked, because an off-switch that fetches and
// discards is not off, it is quiet.
func TestStopHookHonoursTheStatsOffSwitch(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "curl-args")
	fakeCurl := filepath.Join(dir, "curl")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\necho should-not-run\n"
	if err := os.WriteFile(fakeCurl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-stop-hook.sh")
	cmd := exec.Command("bash", hook)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop","stop_hook_active":false}`)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTSMEMORY_MCP_URL=http://palace.test:9/mcp",
		"AGENTSMEMORY_STOP_HOOK=on",
		"AGENTSMEMORY_STATS=off",
	)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()

	if _, err := os.Stat(argsFile); err == nil {
		raw, _ := os.ReadFile(argsFile)
		t.Errorf("AGENTSMEMORY_STATS=off and the Stop hook still fetched stats: curl %s", raw)
	}
	if strings.Contains(out.String(), "should-not-run") {
		t.Errorf("the Stop hook printed a stats report with the switch off:\n%s", out.String())
	}
}

// TestVerifyHookPrintsDriftAndIsOtherwiseSilent pins that the hook SPEAKS, which
// is the half nobody was testing.
//
// ⚠ FOUND BY MUTATION: replacing the off-switch line with a bare `exit 0` — a
// hook that is permanently mute — left every test green. That is the failure
// AGENTS.md already describes for `doctor` ("one run cannot tell healthy silence
// from muteness"), and it is worse here, because this hook is silent by design
// when nothing drifted: a muted one is indistinguishable from a healthy one at
// runtime AND in the suite.
//
// A test can tell them apart where a live run cannot, by making drift certain: a
// fake `aiagentmemory` on PATH that reports a DRIFTED memory, and a fake `curl`
// so the health probe passes. Both directions are asserted, since a hook that
// prints on every session start is the other way this goes wrong — that is what
// the off-switch and the case statement exist for.
func TestVerifyHookPrintsDriftAndIsOtherwiseSilent(t *testing.T) {
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-verify-hook.sh")

	run := func(t *testing.T, report string, env ...string) string {
		t.Helper()
		dir := t.TempDir()
		// curl succeeds so the health probe does not short-circuit the hook.
		if err := os.WriteFile(filepath.Join(dir, "curl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cli := "#!/bin/sh\ncat <<'REPORT'\n" + report + "\nREPORT\n"
		if err := os.WriteFile(filepath.Join(dir, "aiagentmemory"), []byte(cli), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", hook)
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart"}`)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"AGENTSMEMORY_MCP_URL=http://palace.test:9/mcp",
		)
		cmd.Env = append(cmd.Env, env...)
		var out strings.Builder
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("verify hook: %v", err)
		}
		return out.String()
	}

	drifted := run(t, "DRIFTED  internal/palace/service.go — the pinned code is no longer there")
	if !strings.Contains(drifted, "no longer match the code") || !strings.Contains(drifted, "DRIFTED") {
		t.Errorf("the hook said nothing about a DRIFTED memory. A verify hook that cannot speak is "+
			"indistinguishable from one with nothing to say, at runtime and in this suite:\n%q", drifted)
	}

	if clean := run(t, "all anchors verified, nothing drifted"); strings.TrimSpace(clean) != "" {
		t.Errorf("the hook printed on a clean report; it is meant to be silent unless something "+
			"drifted, or it becomes noise every session start:\n%q", clean)
	}

	if off := run(t, "DRIFTED  something", "AGENTSMEMORY_VERIFY_HOOK=off"); strings.TrimSpace(off) != "" {
		t.Errorf("AGENTSMEMORY_VERIFY_HOOK=off still printed:\n%q", off)
	}
}
