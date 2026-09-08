package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// recallHookRun runs one of the two recall hooks with a stub aiagentmemory on
// PATH that records every argv it receives (one line per call) and answers
// with the digest text the caller supplies; stubExit non-zero makes the stub
// fail with stubErr on stderr instead.
func recallHookRun(t *testing.T, hookName string, extraEnv []string, stubOut string, stubExit int, stubErr string) (stdout, stderr string, calls []string) {
	t.Helper()
	dir := t.TempDir()
	calls_ := filepath.Join(dir, "calls")
	stub := "#!/usr/bin/env bash\n" +
		"printf '%s\\n' \"$*\" >> " + calls_ + "\n" +
		"if [ " + itoaTest(stubExit) + " -ne 0 ]; then printf '%s\\n' " + shellQuote(stubErr) + " >&2; exit " + itoaTest(stubExit) + "; fi\n" +
		"printf '%s' " + shellQuote(stubOut) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "aiagentmemory"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", hookName)
	cmd := testexec.Command(t, "bash", hook)
	cmd.Dir = repoRootForHooks(t)
	input := `{"hook_event_name":"UserPromptSubmit","prompt":"fix the flaky session end hook test","cwd":"` + cmd.Dir + `"}`
	if hookName == "agentsmemory-recall-hook.sh" {
		input = `{"hook_event_name":"SessionStart","source":"startup","cwd":"` + cmd.Dir + `"}`
	}
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(), "AGENTSMEMORY_STATE_DIR="+t.TempDir(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTSMEMORY_MCP_URL=http://127.0.0.1:9/mcp",
		"AGENTSMEMORY_TOKEN=t",
		// The ladder walks up from CLAUDE_PROJECT_DIR, and unset that is this
		// repository — whose own .aiagentmemory would then decide both the "with a
		// wing" and the "without a wing" case. See unpinnedProjectDir.
		"CLAUDE_PROJECT_DIR="+unpinnedProjectDir(t),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s failed the session (%v) — a recall hook must never do that\nstderr: %s", hookName, err, se.String())
	}
	raw, _ := os.ReadFile(calls_)
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			calls = append(calls, l)
		}
	}
	return so.String(), se.String(), calls
}

func itoaTest(n int) string { return strconv.Itoa(n) }

// TestTheRecallHookCarriesTheInstalledWing is ADR-058 T2's scoping half: with
// AGENTSMEMORY_WING set the hook makes TWO searches — the installed wing's,
// then wing_craft's under a `craft:` line — both through --digest; without it,
// exactly one search carrying no wing, as before the record. Review of #268
// found that a single scoped call silently drops the 487-memory craft wing
// every project is meant to read.
func TestTheRecallHookCarriesTheInstalledWing(t *testing.T) {
	for _, hookName := range []string{"agentsmemory-task-recall-hook.sh", "agentsmemory-recall-hook.sh"} {
		t.Run(hookName+" with a wing", func(t *testing.T) {
			out, _, calls := recallHookRun(t, hookName, []string{"AGENTSMEMORY_WING=wing_alpha"}, "A PROJECT MEMORY\n  wing_alpha/decisions\n", 0, "")
			if len(calls) != 2 {
				t.Fatalf("with a wing set the hook must search the wing and then wing_craft — %d call(s): %q", len(calls), calls)
			}
			if !strings.Contains(calls[0], "wing=wing_alpha") || !strings.Contains(calls[0], "--digest") {
				t.Errorf("first call is not the installed wing through --digest: %q", calls[0])
			}
			if !strings.Contains(calls[1], "wing=wing_craft") || !strings.Contains(calls[1], "--digest") {
				t.Errorf("second call is not wing_craft through --digest: %q", calls[1])
			}
			// ⚠ THE CRAFT CALL READS THE WHOLE WING, IN BOTH HOOKS, FOR DIFFERENT
			// REASONS — and the project call above does not. Until #438 this call
			// named no room and so inherited the SessionStart hook's `diary`, which
			// holds a rounding error of wing_craft: the call was made, cost a round
			// trip, and returned nothing on 120 of 120 measured queries. The wing
			// assertion above passes either way, which is how the craft call could be
			// "covered by a killed mutant" and still be inert — a mutant proves a test
			// notices a change, never that the thing under test reaches anything.
			//
			// Scoping it to ANY room is what review rejected, and the reason is the
			// asymmetry between the wings: the project call scopes to keep `sessions`
			// out, 719 drawers of mined transcript, and wing_craft has no `sessions`
			// room at all — sessions-only queries there, 0 of 120. A room name is also
			// a fact about one palace: the room holding 74% of the wing locally holds
			// ~26% on hosted. So the assertion is that NEITHER hook scopes this call.
			if strings.Contains(calls[1], "room=") {
				t.Errorf("the craft call names a room. wing_craft has no transcript room to "+
					"scope away, unscoped tied on coverage and returned more (#438), and any "+
					"room name is a claim about one palace's shape rather than about craft: %q",
					calls[1])
			}
			// The project call is the half that DOES scope, and only in this hook —
			// pinned so the two are never tidied into each other without the numbers.
			if hookName == "agentsmemory-recall-hook.sh" && !strings.Contains(calls[0], "room=diary") {
				t.Errorf("the project call stopped scoping to diary. That is a real trade "+
					"(#438: 57%% against unscoped's 78%%, bought with transcript noise on 36%% "+
					"of queries) — take it with the numbers, not as a tidy-up: %q", calls[0])
			}
			if !strings.Contains(out, "A PROJECT MEMORY") || !strings.Contains(out, "craft:") {
				t.Errorf("the injection lacks the digest text or the craft: line:\n%s", out)
			}
			if strings.Contains(out, "may be about a different project") || strings.Contains(out, "not scoped to one") {
				t.Errorf("with a wing set the preamble must not disclaim provenance it now has:\n%s", out)
			}
		})
		t.Run(hookName+" without a wing", func(t *testing.T) {
			out, _, calls := recallHookRun(t, hookName, nil, "A MEMORY\n  wing_alpha/decisions\n", 0, "")
			if len(calls) != 1 || strings.Contains(calls[0], "wing=") {
				t.Fatalf("without a wing the hook must make ONE call carrying no wing argument "+
					"(the server scopes that to the registration's default_wing — it is not an "+
					"unscoped search, and #432 is about the difference): %q", calls)
			}
			// Each hook words its disclaimer its own way; both say the search
			// crossed projects, and both must keep saying so without a wing.
			if !strings.Contains(out, "may be about a different project") && !strings.Contains(out, "not scoped to one") {
				t.Errorf("without a wing the preamble must keep its disclaimer:\n%s", out)
			}
		})
	}
}

// TestARecallThatCouldNotLookSaysSoOnBothChannels: a dead server is named to
// the model (hookSpecificOutput.additionalContext) and to the transcript
// (stderr). The CONNECTION_CLOSED class was diagnosed for weeks as "the agent
// forgot" because the failure reached stderr alone.
func TestARecallThatCouldNotLookSaysSoOnBothChannels(t *testing.T) {
	for _, hookName := range []string{"agentsmemory-task-recall-hook.sh", "agentsmemory-recall-hook.sh"} {
		t.Run(hookName, func(t *testing.T) {
			// The stub prints the CLI's token notice BEFORE the error, as the real
			// CLI does: on 2026-09-05 both hooks reported that notice as the cause.
			out, errText, _ := recallHookRun(t, hookName, []string{"AGENTSMEMORY_WING=wing_alpha"}, "", 1, "aiagentmemory: token from a loopback server, which needs none\ndial tcp 127.0.0.1:9: connect: connection refused")
			var payload struct {
				HookSpecificOutput struct {
					AdditionalContext string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
				t.Fatalf("stdout is not a hookSpecificOutput payload (the model would see nothing):\n%s\nerr: %v", out, err)
			}
			if !strings.Contains(payload.HookSpecificOutput.AdditionalContext, "could not look") || !strings.Contains(payload.HookSpecificOutput.AdditionalContext, "connection refused") {
				t.Errorf("additionalContext does not say the recall could not look, with the cause:\n%s", out)
			}
			if !strings.Contains(errText, "could not look") {
				t.Errorf("stderr does not carry the same line for the transcript:\n%s", errText)
			}
		})
	}
}
