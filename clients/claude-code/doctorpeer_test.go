package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorReportsTheCodebaseMemoryPeer is ADR-057 T1: doctor prints one row
// for codebase-memory judged from the two files the kit already reads, and
// exits non-zero on the two states an operator cannot otherwise see. The
// motivating fixture is real — the owner's settings.json on 2026-09-05
// carried cbm-session-reminder four times on SessionStart, and doctor said
// nothing — and the DUPLICATE subtest reproduces it exactly.
func TestDoctorReportsTheCodebaseMemoryPeer(t *testing.T) {
	// A kit hook must be registered too, or doctor stops earlier on its own
	// empty universe, which is not the state under test.
	base := func(t *testing.T) string {
		t.Helper()
		return doctorEnv(t,
			map[string]string{"agentsmemory-recall-hook.sh": injectingHookBody},
			map[string][]string{"SessionStart": {"agentsmemory-recall-hook.sh"}})
	}

	t.Run("ok: one registration, one of each hook", func(t *testing.T) {
		dir := base(t)
		bin := peerBinary(t, dir, true)
		peerMCP(t, dir, map[string]string{"codebase-memory-mcp": bin})
		peerHooks(t, dir, map[string][]string{"SessionStart": {"cbm-session-reminder"}, "SubagentStart": {"cbm-subagent-reminder"}})
		report, err := runDoctor(t, dir)
		if err != nil {
			t.Fatalf("a healthy peer failed doctor: %v\n%s", err, report)
		}
		assertPeerRow(t, report, "ok")
	})

	t.Run("absent: nothing registered, row printed, exit 0", func(t *testing.T) {
		dir := base(t)
		report, err := runDoctor(t, dir)
		if err != nil {
			t.Fatalf("an absent peer is optional and must not fail doctor: %v\n%s", err, report)
		}
		assertPeerRow(t, report, "absent")
	})

	t.Run("DUPLICATE: the same hook registered four times on one event", func(t *testing.T) {
		dir := base(t)
		bin := peerBinary(t, dir, true)
		peerMCP(t, dir, map[string]string{"codebase-memory-mcp": bin})
		peerHooks(t, dir, map[string][]string{"SessionStart": {"cbm-session-reminder", "cbm-session-reminder", "cbm-session-reminder", "cbm-session-reminder"}})
		report, err := runDoctor(t, dir)
		if err == nil {
			t.Fatalf("four copies of one hook passed doctor:\n%s", report)
		}
		assertPeerRow(t, report, "DUPLICATE")
		if !strings.Contains(report, "cbm-session-reminder") || !strings.Contains(report, "4") {
			t.Errorf("the report does not name the duplicated script and its count:\n%s", report)
		}
	})

	t.Run("DUPLICATE: the same binary under two MCP names", func(t *testing.T) {
		dir := base(t)
		bin := peerBinary(t, dir, true)
		peerMCP(t, dir, map[string]string{"codebase-memory-mcp": bin, "codebasememory": bin})
		peerHooks(t, dir, map[string][]string{"SessionStart": {"cbm-session-reminder"}})
		report, err := runDoctor(t, dir)
		if err == nil {
			t.Fatalf("two registrations of one server passed doctor:\n%s", report)
		}
		assertPeerRow(t, report, "DUPLICATE")
	})

	t.Run("a global install reads ~/.claude.json, not <dir>/.claude.json", func(t *testing.T) {
		// The ghost-file case: <config-dir>/.claude.json carries an old name and
		// the real registry beside the home dir carries only upstream's. Claude
		// reads the latter, so doctor must too — or it reports a server the
		// agent never spawns.
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".claude")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "agentsmemory-recall-hook.sh"), []byte(injectingHookBody), 0o755); err != nil {
			t.Fatal(err)
		}
		writeSettings(t, dir, map[string][]string{"SessionStart": {"agentsmemory-recall-hook.sh"}})
		bin := peerBinary(t, dir, true)
		peerMCP(t, dir, map[string]string{"codebasememory": bin}) // the ghost
		ghost := filepath.Join(dir, ".claude.json")
		real := filepath.Join(home, ".claude.json")
		raw, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"codebase-memory-mcp": map[string]any{"command": bin}}})
		if err := os.WriteFile(real, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		_ = ghost
		report, err := runDoctor(t, dir)
		if err != nil {
			t.Fatalf("doctor over a global install failed: %v\n%s", err, report)
		}
		assertPeerRow(t, report, "ok")
		if !strings.Contains(report, "codebase-memory-mcp →") || strings.Contains(report, "codebasememory →") {
			t.Errorf("the row was judged from the ghost file, not the registry Claude reads:\n%s", report)
		}
	})

	t.Run("ok under the retired name says so in the row, and does not fail", func(t *testing.T) {
		// The reviewer's sandbox on #266: one registration, the kit's old name.
		// Not a duplicate — one daemon — but a tool prefix no document names.
		dir := base(t)
		bin := peerBinary(t, dir, true)
		peerMCP(t, dir, map[string]string{"codebasememory": bin})
		report, err := runDoctor(t, dir)
		if err != nil {
			t.Fatalf("one registration under the retired name must not fail doctor: %v\n%s", err, report)
		}
		assertPeerRow(t, report, "ok")
		if !strings.Contains(report, "RETIRED") {
			t.Errorf("the row does not say the name is retired:\n%s", report)
		}
	})

	t.Run("BROKEN: registered, binary not executable", func(t *testing.T) {
		dir := base(t)
		bin := peerBinary(t, dir, false)
		peerMCP(t, dir, map[string]string{"codebase-memory-mcp": bin})
		report, err := runDoctor(t, dir)
		if err == nil {
			t.Fatalf("a registration pointing at a non-executable passed doctor:\n%s", report)
		}
		assertPeerRow(t, report, "BROKEN")
	})
}

// assertPeerRow finds the codebase-memory row and checks its label column.
func assertPeerRow(t *testing.T, report, label string) {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "codebase-memory ") {
			if !strings.Contains(line, label) {
				t.Errorf("row = %q, want label %q", strings.TrimSpace(line), label)
			}
			return
		}
	}
	t.Errorf("no codebase-memory row in the report:\n%s", report)
}

// peerBinary places a fake codebase-memory-mcp in dir, executable or not.
func peerBinary(t *testing.T, dir string, executable bool) string {
	t.Helper()
	p := filepath.Join(dir, "codebase-memory-mcp")
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// peerMCP writes .claude.json in the config dir with the given mcpServers.
func peerMCP(t *testing.T, dir string, servers map[string]string) {
	t.Helper()
	m := map[string]any{}
	for name, cmd := range servers {
		m[name] = map[string]any{"command": cmd, "type": "stdio"}
	}
	raw, _ := json.Marshal(map[string]any{"mcpServers": m})
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// peerHooks appends cbm-* hook registrations to the settings doctorEnv wrote,
// as upstream's installer does: one matcher-less entry per registration,
// duplicates and all.
func peerHooks(t *testing.T, dir string, regs map[string][]string) {
	t.Helper()
	path := filepath.Join(dir, "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for event, names := range regs {
		entries, _ := hooks[event].([]any)
		for _, n := range names {
			entries = append(entries, map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": `"$HOME/.claude/hooks/` + n + `"`,
			}}})
		}
		hooks[event] = entries
	}
	settings["hooks"] = hooks
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSettings writes a settings.json registering the named kit scripts, the
// shape doctorEnv produces, for a dir the test had to create itself.
func writeSettings(t *testing.T, dir string, regs map[string][]string) {
	t.Helper()
	hooks := map[string]any{}
	for event, names := range regs {
		entries := make([]any, 0, len(names))
		for _, n := range names {
			entries = append(entries, map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": bashHookCommand(filepath.Join(dir, n)),
			}}})
		}
		hooks[event] = entries
	}
	raw, _ := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
