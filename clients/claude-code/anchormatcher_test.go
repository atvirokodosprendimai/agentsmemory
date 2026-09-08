package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheAnchorCueIsScopedToTheToolsItCanActOn is ADR-051 T2's step 5, which the
// task recorded as done without carrying it out.
//
// The cue reads `file_path` and can do nothing without one, but a matcher-less
// registration makes the agent SPAWN it for every tool — Bash, TodoWrite, Grep —
// each of which pays a process start to decide it has nothing to do. Measured
// 2026-09-08: 0.16s per call. The script's own guard cannot save that, because it
// runs inside a shell the agent has already started.
func TestTheAnchorCueIsScopedToTheToolsItCanActOn(t *testing.T) {
	i := &Installer{kit: claudeKit}
	var found *hookPlan
	for _, p := range i.hookPlansOn("darwin") {
		if p.event == "PreToolUse" && strings.Contains(p.cmd, "anchor-cue") {
			found = &p
			break
		}
	}
	if found == nil {
		t.Fatal("no PreToolUse plan registers the anchor cue; the script is inert without it")
	}
	if found.matcher == "" {
		t.Fatal("the anchor cue is registered for EVERY tool. ADR-051 T2 step 5 ordered a matcher, " +
			"and without one the agent spawns this hook on every Bash and TodoWrite call")
	}
	// Every tool whose input can carry a file_path must be admitted: narrowing
	// below this set silently removes the cue for a tool that has it today.
	for _, tool := range []string{"Read", "Edit", "Write", "MultiEdit", "NotebookEdit"} {
		if !strings.Contains(found.matcher, tool) {
			t.Errorf("matcher %q does not admit %s, which carries a file_path — the cue would stop "+
				"firing for a tool it serves today", found.matcher, tool)
		}
	}
	for _, tool := range []string{"Bash", "TodoWrite", "Task", "WebFetch"} {
		if strings.Contains(found.matcher, tool) {
			t.Errorf("matcher %q admits %s, which names no file: the hook can only exit", found.matcher, tool)
		}
	}
}

// TestTheShippedManifestAgreesWithTheInstallersMatcher pins two deliberate
// copies to each other.
//
// hooks.json is what the plugin registers and hookPlansOn is what `install`
// writes; they describe the same registration through different mechanisms. A
// duplicate with no comparison is a second source of truth, and this pair would
// drift the moment one is widened — leaving a plugin user and an install user
// with hooks that fire on different tools.
func TestTheShippedManifestAgreesWithTheInstallersMatcher(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, g := range doc.Hooks["PreToolUse"] {
		for _, h := range g.Hooks {
			if !strings.Contains(h.Command, "anchor-cue") {
				continue
			}
			seen = true
			if g.Matcher != anchorCueMatcher {
				t.Errorf("hooks.json registers the anchor cue with matcher %q; the installer uses %q. "+
					"A plugin user and an install user would get hooks firing on different tools",
					g.Matcher, anchorCueMatcher)
			}
		}
	}
	if !seen {
		t.Fatal("hooks.json does not register the anchor cue on PreToolUse at all")
	}
}

// TestAddingAMatcherSupersedesTheMatcherlessRegistration is the migration, and
// without it this change CREATES the defect it exists to remove.
//
// Every install before 2026-09-08 wrote this command with no matcher. hookPresent
// compares commands, so it finds that entry and appends nothing — and
// foreignHookPredicate spares it too, because the command is identical to the one
// being installed. The result would be a hook still registered for every tool,
// with a scoped entry beside it: two registrations, which is #416 all over again
// and introduced by the fix for it.
func TestAddingAMatcherSupersedesTheMatcherlessRegistration(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	cmd := "bash -- '/h/.claude/agentsmemory-anchor-cue-hook.sh'"
	if err := os.WriteFile(p, []byte(`{"hooks":{"PreToolUse":[{"hooks":[
	  {"type":"command","command":`+jsonQuote(cmd)+`,"timeout":75}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := ensureHooksReporting(p, []hookReg{{
		event: "PreToolUse", cmd: cmd, matcher: anchorCueMatcher,
		obsolete: foreignHookPredicate(cmd),
	}}, ""); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var matchers []string
	for _, g := range doc.Hooks["PreToolUse"] {
		for _, h := range g.Hooks {
			if h.Command == cmd {
				matchers = append(matchers, g.Matcher)
			}
		}
	}
	if len(matchers) != 1 {
		t.Fatalf("the anchor cue is registered %d times after the upgrade (matchers %v), want 1 — "+
			"the matcher-less entry from an older install has to be superseded, not left beside the "+
			"new one, or this change ships the duplication it was written to remove", len(matchers), matchers)
	}
	if matchers[0] != anchorCueMatcher {
		t.Errorf("the surviving registration carries matcher %q, want %q", matchers[0], anchorCueMatcher)
	}
}
