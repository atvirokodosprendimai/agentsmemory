package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// injectingEvents are the hook events whose plain stdout Claude Code adds to the
// model's context as text the model can read and act on. Every other event sends
// a hook's stdout to the debug log, where nothing reads it.
//
// The list is exhaustive and it is short on purpose — from Claude Code's hooks
// reference, 2026-08-28: "For most events, stdout is written to the debug log but
// not shown in the transcript. The exceptions are UserPromptSubmit,
// UserPromptExpansion, and SessionStart, where Claude Code adds plain-text stdout
// as context that Claude can see and act on."
var injectingEvents = map[string]bool{
	"SessionStart":        true,
	"UserPromptSubmit":    true,
	"UserPromptExpansion": true,
}

// hookOutputDecl is the `# hook-output: <channel>` line every script in hooks/
// carries. The channel is the first token; anything after an em dash is the
// author's reason for not using the injecting channel.
var hookOutputDecl = regexp.MustCompile(`(?m)^# hook-output:[ \t]*([a-z-]+)[ \t]*(.*)$`)

// hookScripts reads the shipped hooks directory and returns each script's name,
// body and declared channel.
//
// ⚠ THE UNIVERSE IS THE DIRECTORY, not a list kept beside it. A script added
// tomorrow joins these checks on the same commit; a list would have to be
// remembered, and the defect these tests exist to catch is precisely the one
// nobody remembered to look for.
func hookScripts(t *testing.T) map[string]struct{ body, channel, reason string } {
	t.Helper()
	const dir = "hooks"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := map[string]struct{ body, channel, reason string }{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		body := string(raw)
		var channel, reason string
		if m := hookOutputDecl.FindStringSubmatch(body); m != nil {
			channel, reason = m[1], strings.TrimSpace(m[2])
		}
		out[e.Name()] = struct{ body, channel, reason string }{body, channel, reason}
	}
	if len(out) == 0 {
		t.Fatal("no hook scripts found — this check would pass vacuously")
	}
	return out
}

// TestEveryHookScriptIsEmbedded closes the gap between the two universes these
// checks use. hookScripts reads the SOURCE DIRECTORY; the installer writes what
// is in the //go:embed list in assets.go. A script present in hooks/ but absent
// from that list passes every channel check here and then ships as nothing at
// all — the same unreachability one layer down, and the embed line is hand-edited
// (it had to be, for this task's rename), so it is exactly the sort of list that
// drifts.
func TestEveryHookScriptIsEmbedded(t *testing.T) {
	for name := range hookScripts(t) {
		if _, err := assets.ReadFile("hooks/" + name); err != nil {
			t.Errorf("hooks/%s is in the source tree but not in the //go:embed list in "+
				"assets.go, so an install writes nothing for it: %v", name, err)
		}
	}
}

// TestEveryHookScriptDeclaresItsOutputChannel is the derivation the other two
// checks stand on: an undeclared script is invisible to them, so silence here
// would make the gate below pass for the wrong reason.
func TestEveryHookScriptDeclaresItsOutputChannel(t *testing.T) {
	valid := map[string]bool{
		"stdout-injected": true, // the model reads stdout — only on an injecting event
		"structured":      true, // hookSpecificOutput.additionalContext
		"blocking":        true, // exit 2, whose stderr the model is shown
		"none":            true, // reaches the model on no channel
		"not-a-hook":      true, // a helper, registered on no event
	}
	for name, s := range hookScripts(t) {
		if s.channel == "" {
			t.Errorf("%s carries no \"# hook-output:\" line, so nothing can check that what it "+
				"prints has anywhere to go", name)
			continue
		}
		if !valid[s.channel] {
			t.Errorf("%s declares unknown channel %q", name, s.channel)
		}
	}
}

// TestEveryInjectingHookIsOnAnInjectingEvent is the gate this file exists for.
//
// ⚠ IT IS THE RUNG NO UNIT TEST REACHES. ADR-041 T4 shipped registered on
// PreCompact: the script performed a recall, printed it, and Claude Code sent it
// to the debug log, where the model never saw it. Every test passed — they drove
// the SCRIPT and asserted what it wrote. Two mutants were killed against a
// mechanism that could not work, because a mutant proves a test notices a change,
// not that the thing under test is reachable.
//
// A hook is a capability whose selection is its event. Testing the script without
// testing the event is testing the component and not the wiring, which is this
// repository's characteristic defect.
func TestEveryInjectingHookIsOnAnInjectingEvent(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	plans := inst.hookPlans()
	if len(plans) == 0 {
		t.Fatal("no hook plans — this check would pass vacuously")
	}

	eventsFor := func(script string) []string {
		var events []string
		for _, p := range plans {
			if strings.Contains(p.cmd, script) {
				events = append(events, p.event)
			}
		}
		return events
	}

	var checked int
	for name, s := range hookScripts(t) {
		events := eventsFor(name)
		switch s.channel {
		case "stdout-injected":
			checked++
			if len(events) == 0 {
				t.Errorf("%s prints for the model to read and no hook plan registers it — "+
					"the script is on disk and nothing invokes it", name)
				continue
			}
			for _, ev := range events {
				if !injectingEvents[ev] {
					t.Errorf("%s declares stdout-injected but is registered on %q, whose stdout "+
						"Claude Code writes to the debug log. The recall would run and be "+
						"discarded. Injecting events: SessionStart, UserPromptSubmit, "+
						"UserPromptExpansion.", name, ev)
				}
			}
		case "not-a-hook":
			if len(events) > 0 {
				t.Errorf("%s declares not-a-hook but is registered on %v", name, events)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no script declares stdout-injected, so this gate checked nothing — either the " +
			"declarations are wrong or the check is looking at the wrong universe")
	}
}

// TestANonInjectedChannelIsJustified keeps the declaration from becoming the
// dodge. A script whose stdout the model cannot read is a legitimate thing to
// ship — the SessionEnd report is for the operator — but saying so must cost the
// same sentence of thought the gate exists to force. It mirrors
// TestNotOperatorFacingIsJustified: the reason is the review.
func TestANonInjectedChannelIsJustified(t *testing.T) {
	for name, s := range hookScripts(t) {
		if s.channel == "" || s.channel == "stdout-injected" {
			continue
		}
		if len(s.reason) < 20 {
			t.Errorf("%s declares %q without saying why the model cannot or need not read its "+
				"stdout; write the reason on the \"# hook-output:\" line", name, s.channel)
		}
	}
}
