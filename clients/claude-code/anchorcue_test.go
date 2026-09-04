package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runAnchorCue drives the shipped script with a fake `aiagentmemory` on PATH, so
// the hook's own behaviour is measured without a server.
//
// The stub is what makes the silence assertions meaningful: a hook that stayed
// quiet because the binary was missing would pass a naive "no output" test while
// being broken, so the stub always succeeds and the script's own branches decide.
func runAnchorCue(t *testing.T, input, stubStdout string) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "aiagentmemory")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncat <<'JSON'\n"+stubStdout+"\nJSON\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("hooks", "agentsmemory-anchor-cue-hook.sh"))
	cmd.Stdin = strings.NewReader(input)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	// CLAUDE_PROJECT_DIR is what makes an absolute tool path repo-relative. Anchors
	// are stored repo-relative, so without it every lookup asks about a path no
	// anchor can match and the cue is silent for the wrong reason.
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CLAUDE_PROJECT_DIR=/repo")
	if err := cmd.Run(); err != nil {
		t.Fatalf("the cue exited non-zero (%v); a PreToolUse hook that fails BLOCKS the tool call", err)
	}
	return out.String(), errb.String()
}

const anchorHit = `{"anchors":[{"path":"internal/palace/chunk.go","repo":"agentsmemory","snippet":"ChunkOverlap = 320","status":"verified","drawer_id":"abc"}],"count":1}`

// TestTheAnchorCueIsSilentWithoutAnAnchor is the assertion the whole design rests
// on, and the one a careless implementation fails.
//
// Nothing pins most files. If the cue spoke on those it would fire on nearly every
// tool call, and a channel that talks when it has nothing to say is one a reader
// learns to skip — which is worse than never shipping it. ADR-041's T5 measured
// its own cue at 3.4% of turns and still recorded frequency as the thing to check
// BEFORE relevance.
func TestTheAnchorCueIsSilentWithoutAnAnchor(t *testing.T) {
	out, _ := runAnchorCue(t, `{"tool_name":"Read","tool_input":{"file_path":"/repo/internal/nothing.go"}}`,
		`{"anchors":[],"count":0}`)
	if out != "" {
		t.Errorf("the cue emitted %d bytes for a path nothing pins:\n%s", len(out), out)
	}
}

// TestTheAnchorCueIsSilentWithoutAFilePath covers every tool that names no file.
//
// PreToolUse fires for tools this kit has never heard of, which is why the script
// filters rather than the registration: a matcher would be a second copy of a
// guard that has to exist anyway.
func TestTheAnchorCueIsSilentWithoutAFilePath(t *testing.T) {
	out, errs := runAnchorCue(t, `{"tool_name":"Bash","tool_input":{"command":"ls"}}`, anchorHit)
	if out != "" {
		t.Errorf("the cue spoke for a tool call carrying no file_path:\n%s", out)
	}
	if !strings.Contains(errs, "no file_path") {
		t.Errorf("it should say on stderr why it stayed quiet; got %q", errs)
	}
}

// TestTheAnchorCueEmitsAParseableEnvelope is the delivery half.
//
// PreToolUse does not inject plain stdout, so the payload has to be a JSON
// envelope carrying additionalContext. Hand-assembled JSON is how an envelope
// becomes unparseable and is then dropped in SILENCE by the harness — the failure
// mode that looks identical to a hook that chose not to speak.
func TestTheAnchorCueEmitsAParseableEnvelope(t *testing.T) {
	out, _ := runAnchorCue(t, `{"tool_name":"Read","tool_input":{"file_path":"/repo/internal/palace/chunk.go"}}`, anchorHit)
	if out == "" {
		t.Fatal("the cue said nothing for a path an anchor pins")
	}
	var env struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("the envelope does not parse (%v); the harness would drop it silently:\n%s", err, out)
	}
	if env.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want PreToolUse", env.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(env.HookSpecificOutput.AdditionalContext, "chunk.go") {
		t.Errorf("the context does not name the file it is about:\n%s", env.HookSpecificOutput.AdditionalContext)
	}
}

// TestTheAnchorCueRefusesAnUnfilteredAnswer is the hardening a live run demanded.
//
// ⚠ MEASURED 2026-09-04: an MCP server that does not recognise an argument IGNORES
// it. Against a container one commit behind this hook, `path=` was dropped and the
// call returned five anchors from THREE DIFFERENT REPOSITORIES for a file nothing
// pinned. A cue that fires with another project's memories attached is worse than
// one that never fires, and during any rollout the server is briefly older than
// the kit — so the hook confirms the path it asked about is in the answer instead
// of trusting that filtering happened.
func TestTheAnchorCueRefusesAnUnfilteredAnswer(t *testing.T) {
	unfiltered := `{"anchors":[{"path":"docs/other/thing.md","repo":"some_other_project","snippet":"x","status":"unchecked","drawer_id":"z"}],"count":1}`
	out, errs := runAnchorCue(t, `{"tool_name":"Read","tool_input":{"file_path":"/repo/internal/palace/chunk.go"}}`, unfiltered)
	if out != "" {
		t.Errorf("the cue passed on anchors that do not match the path it asked about:\n%s", out)
	}
	if !strings.Contains(errs, "ignored the path filter") {
		t.Errorf("it should name why it declined; got %q", errs)
	}
}

// TestThePreToolUseHookIsRegistered covers the rung the script's own tests cannot
// see.
//
// Every test above drives the FILE. A hook script can be perfect, embedded and
// installed while nothing registers it — the defect AGENTS.md §Reachability
// records repeatedly, and the one that made ADR-041's PreCompact recall run and be
// thrown away. This reads the installer's own plan.
func TestThePreToolUseHookIsRegistered(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	var found *hookPlan
	for _, p := range inst.hookPlans() {
		if p.event == "PreToolUse" && !p.retire {
			pp := p
			found = &pp
		}
	}
	if found == nil {
		t.Fatal("the installer registers no PreToolUse hook; the anchor cue ships and is never selected")
	}
	if !strings.Contains(found.cmd, anchorCueHookFile) {
		t.Errorf("the PreToolUse registration runs %q, not the anchor cue", found.cmd)
	}
}

// TestNoHookPlanIsRegisteredTwice was earned by nearly shipping one.
//
// ⚠ While landing the PreToolUse cue the plan was inserted twice: an earlier grep
// checking whether it existed was truncated by `head -3`, so the first insert was
// read as a no-op and repeated. The build stayed green and every other test
// passed, because a duplicate registration is not a compile error and not a
// behaviour change any single test observes — it just runs the hook twice, which
// doubles the injected context and is invisible in a transcript that already
// contains the text once.
//
// The pair is (event, command). The same SCRIPT on two different events is
// deliberate here — Stop and SubagentStop share one, branching on the event name —
// so keying on the command alone would fail the design rather than the defect.
func TestNoHookPlanIsRegisteredTwice(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	plans := inst.hookPlans()
	if len(plans) == 0 {
		t.Fatal("no hook plans — this check would pass vacuously")
	}
	seen := map[string]bool{}
	for _, p := range plans {
		if p.retire {
			continue
		}
		key := p.event + "\x00" + p.cmd
		if seen[key] {
			t.Errorf("%s is registered twice with the same command; the hook runs twice and injects twice", p.event)
		}
		seen[key] = true
	}
}
