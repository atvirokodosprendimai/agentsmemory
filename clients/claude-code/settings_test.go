package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readStop is a small test helper that reads settings.json and returns the Stop
// hook array, failing the test on any structural surprise.
func readStop(t *testing.T, path string) []any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	hooks, ok := m["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks is %T, want object", m["hooks"])
	}
	stop, ok := hooks["Stop"].([]any)
	if !ok {
		t.Fatalf("Stop is %T, want array", hooks["Stop"])
	}
	return stop
}

func TestEnsureStopHookFreshFile(t *testing.T) {
	// A brand-new install has no settings.json; ensureStopHook must create it.
	path := filepath.Join(t.TempDir(), "settings.json")
	cmd := "bash /x/hooks/agentsmemory-stop-hook.sh"

	added, err := ensureStopHook(path, cmd)
	if err != nil {
		t.Fatalf("ensureStopHook: %v", err)
	}
	if !added {
		t.Fatal("added = false, want true on a fresh file")
	}
	if stop := readStop(t, path); len(stop) != 1 {
		t.Fatalf("Stop entries = %d, want 1", len(stop))
	}
	if !stopHookPresent(readStop(t, path), cmd) {
		t.Fatal("hook command not present after install")
	}
}

func TestEnsureStopHookIdempotent(t *testing.T) {
	// Re-running the installer must not duplicate the hook.
	path := filepath.Join(t.TempDir(), "settings.json")
	cmd := "bash /x/hooks/agentsmemory-stop-hook.sh"

	if _, err := ensureStopHook(path, cmd); err != nil {
		t.Fatalf("first ensureStopHook: %v", err)
	}
	added, err := ensureStopHook(path, cmd)
	if err != nil {
		t.Fatalf("second ensureStopHook: %v", err)
	}
	if added {
		t.Fatal("added = true on second run, want false (already present)")
	}
	if stop := readStop(t, path); len(stop) != 1 {
		t.Fatalf("Stop entries = %d, want 1 (no duplicate)", len(stop))
	}
}

func TestEnsureStopHookPreservesExisting(t *testing.T) {
	// Existing settings — including an unrelated Stop hook — must survive, and a
	// timestamped backup of the original must be written.
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := []byte(`{
  "model": "claude-opus-4-8",
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "bash /other/hook.sh" } ] }
    ]
  }
}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := "bash /x/hooks/agentsmemory-stop-hook.sh"
	added, err := ensureStopHook(path, cmd)
	if err != nil {
		t.Fatalf("ensureStopHook: %v", err)
	}
	if !added {
		t.Fatal("added = false, want true")
	}

	stop := readStop(t, path)
	if len(stop) != 2 {
		t.Fatalf("Stop entries = %d, want 2 (existing + ours)", len(stop))
	}
	if !stopHookPresent(stop, "bash /other/hook.sh") {
		t.Fatal("pre-existing hook was dropped")
	}
	if !stopHookPresent(stop, cmd) {
		t.Fatal("our hook was not added")
	}

	// The unrelated top-level key must be preserved.
	raw, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "claude-opus-4-8" {
		t.Fatalf("model = %v, want it preserved", m["model"])
	}

	// A backup of the original bytes must exist.
	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) == 0 {
		t.Fatal("no timestamped backup written")
	}
	got, _ := os.ReadFile(backups[0])
	if string(got) != string(original) {
		t.Fatal("backup does not match the original file bytes")
	}
}

func TestEnsureStopHookMalformedRefuses(t *testing.T) {
	// A settings.json we cannot parse must fail loudly and be left untouched,
	// never overwritten.
	path := filepath.Join(t.TempDir(), "settings.json")
	broken := []byte("{ this is not json")
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureStopHook(path, "bash /x.sh"); err == nil {
		t.Fatal("ensureStopHook accepted malformed JSON, want an error")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(broken) {
		t.Fatal("malformed settings.json was modified; it must be left untouched")
	}
}
