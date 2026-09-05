package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestEveryHookRegistrationCarriesATimeout pins the hook half of the owner's
// rule of 2026-09-05: every child a hook starts carries a deadline. The kit
// declares it in the registration itself (Claude Code's `timeout`, seconds),
// rather than relying on the harness default, which is a number nothing in
// this tree can read back. The second half is the one that matters on an
// existing install: a registration an older kit wrote has no `timeout`, and
// "already registered" used to mean "left exactly as it was" — so it is
// upgraded in place, reported as a change, and left alone on the next run.
func TestEveryHookRegistrationCarriesATimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cmd := "bash -- /tmp/agentsmemory-recall-hook.sh"

	// Fresh file: the registration is written with the deadline.
	if _, err := ensureHook(path, "SessionStart", cmd, nil); err != nil {
		t.Fatalf("ensureHook: %v", err)
	}
	if got := hookTimeouts(t, path, "SessionStart", cmd); len(got) != 1 || got[0] != hookTimeoutSeconds {
		t.Fatalf("fresh registration timeouts = %v, want [%d]", got, hookTimeoutSeconds)
	}

	// A registration from an older kit: same command, no timeout. It must be
	// upgraded, and the upgrade must count as a change so the file is written.
	stale := map[string]any{"hooks": map[string]any{"Stop": []any{
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}},
	}}}
	raw, _ := json.Marshal(stale)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ensureHook(path, "Stop", cmd, nil)
	if err != nil {
		t.Fatalf("ensureHook over a stale registration: %v", err)
	}
	if !changed {
		t.Fatal("a registration without a timeout was reported unchanged, so it was never written back")
	}
	if got := hookTimeouts(t, path, "Stop", cmd); len(got) != 1 || got[0] != hookTimeoutSeconds {
		t.Fatalf("upgraded registration timeouts = %v, want [%d]", got, hookTimeoutSeconds)
	}

	// Idempotent: a bounded registration is left alone.
	changed, err = ensureHook(path, "Stop", cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("a registration that already carries its timeout was rewritten")
	}
}

// hookTimeouts returns the timeout of every registration of cmd under event,
// as -1 where the field is absent, so an absent field is visible rather than
// zero-valued.
func hookTimeouts(t *testing.T, path, event, cmd string) []int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, entry := range settings.Hooks[event] {
		for _, h := range entry.Hooks {
			if h["command"] != cmd {
				continue
			}
			v, ok := h["timeout"].(float64)
			if !ok {
				out = append(out, -1)
				continue
			}
			out = append(out, int(v))
		}
	}
	return out
}
