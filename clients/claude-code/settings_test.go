package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// readStop is a small test helper that reads settings.json and returns the Stop
// hook array, failing the test on any structural surprise.
func readStop(t *testing.T, path string) []any {
	t.Helper()
	return readHookEvent(t, path, "Stop")
}

// readHookEvent returns the entries registered for one hook event, so a test can
// assert on Stop and SessionStart through the same reader.
func readHookEvent(t *testing.T, path, event string) []any {
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
	entries, ok := hooks[event].([]any)
	if !ok {
		t.Fatalf("%s is %T, want array", event, hooks[event])
	}
	return entries
}

func TestEnsureStopHookFreshFile(t *testing.T) {
	// A brand-new install has no settings.json; ensureStopHook must create it.
	path := filepath.Join(t.TempDir(), "settings.json")
	cmd := "bash /x/hooks/agentsmemory-stop-hook.sh"

	added, err := ensureHook(path, "Stop", cmd, nil)
	if err != nil {
		t.Fatalf("ensureStopHook: %v", err)
	}
	if !added {
		t.Fatal("added = false, want true on a fresh file")
	}
	if stop := readStop(t, path); len(stop) != 1 {
		t.Fatalf("Stop entries = %d, want 1", len(stop))
	}
	if !hookPresent(readStop(t, path), cmd) {
		t.Fatal("hook command not present after install")
	}
}

func TestEnsureStopHookIdempotent(t *testing.T) {
	// Re-running the installer must not duplicate the hook.
	path := filepath.Join(t.TempDir(), "settings.json")
	cmd := "bash /x/hooks/agentsmemory-stop-hook.sh"

	if _, err := ensureHook(path, "Stop", cmd, nil); err != nil {
		t.Fatalf("first ensureStopHook: %v", err)
	}
	added, err := ensureHook(path, "Stop", cmd, nil)
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
	added, err := ensureHook(path, "Stop", cmd, nil)
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
	if !hookPresent(stop, "bash /other/hook.sh") {
		t.Fatal("pre-existing hook was dropped")
	}
	if !hookPresent(stop, cmd) {
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

	if _, err := ensureHook(path, "Stop", "bash /x.sh", nil); err == nil {
		t.Fatal("ensureStopHook accepted malformed JSON, want an error")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(broken) {
		t.Fatal("malformed settings.json was modified; it must be left untouched")
	}
}

func TestEnsureCodexStopHookPreservesConfigAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := []byte(`# keep this comment and formatting exactly
model = "gpt-5.6-sol"

[[hooks.SessionStart]]
matcher = "startup|resume"

[[hooks.SessionStart.hooks]]
type = "command"
command = "'/opt/codebase-memory' hook-augment"
`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := "bash /Users/me/.codex/agentsmemory-stop-hook.sh"
	changed, err := ensureCodexStopHook(path, cmd)
	if err != nil {
		t.Fatalf("ensure codex Stop hook: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true on first registration")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), string(original)) {
		t.Fatalf("existing config was reformatted or replaced\noriginal:\n%s\ngot:\n%s", original, got)
	}
	for _, want := range []string{
		codexStopHookStart,
		`[[hooks.Stop]]`,
		`matcher = "*"`,
		`[[hooks.Stop.hooks]]`,
		`command = "bash /Users/me/.codex/agentsmemory-stop-hook.sh"`,
		codexStopHookEnd,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("config.toml does not contain %q:\n%s", want, got)
		}
	}

	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) != 1 {
		t.Fatalf("backups after first registration = %d, want 1", len(backups))
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(original) {
		t.Fatal("config.toml backup does not match the original bytes")
	}
	backupInfo, err := os.Stat(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := backupInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("config.toml backup mode = %04o, want 0600", got)
	}

	changed, err = ensureCodexStopHook(path, cmd)
	if err != nil {
		t.Fatalf("re-register codex Stop hook: %v", err)
	}
	if changed {
		t.Fatal("changed = true on an identical re-registration")
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(got) {
		t.Fatal("idempotent registration rewrote config.toml")
	}
	if backups, _ := filepath.Glob(path + ".bak.*"); len(backups) != 1 {
		t.Fatalf("idempotent registration created another backup: %d", len(backups))
	}
}

func TestEnsureCodexStopHookReplacesOnlyItsManagedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	oldBlock := codexStopHookBlock("bash /old/agentsmemory-stop-hook.sh")
	original := "model = \"gpt-5.6\"\n\n" + oldBlock + "\n[features]\nweb_search = true\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := ensureCodexStopHook(path, "bash /new/agentsmemory-stop-hook.sh")
	if err != nil {
		t.Fatalf("replace codex Stop hook: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true for a relocated hook")
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "/old/") {
		t.Fatalf("old managed command survived replacement:\n%s", got)
	}
	if !strings.Contains(string(got), `command = "bash /new/agentsmemory-stop-hook.sh"`) {
		t.Fatalf("new managed command missing:\n%s", got)
	}
	if !strings.Contains(string(got), "[features]\nweb_search = true\n") {
		t.Fatalf("config after the managed block was lost:\n%s", got)
	}
	if strings.Count(string(got), codexStopHookStart) != 1 || strings.Count(string(got), codexStopHookEnd) != 1 {
		t.Fatalf("managed block was duplicated:\n%s", got)
	}
}

func TestCodexStopHookClosesOnItsCommandLine(t *testing.T) {
	// `codex mcp add` inserts a new TOML table before a trailing standalone
	// comment. If our closing marker occupies its own final line, Codex therefore
	// puts the new table INSIDE our managed range and the next install deletes it.
	// Keeping the marker on the command line makes the range impossible to grow
	// when Codex appends another section.
	block := codexStopHookBlock("bash /x/agentsmemory-stop-hook.sh")
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "command = ") {
			if !strings.Contains(line, codexStopHookEnd) {
				t.Fatalf("closing marker is not on the managed command line: %q", line)
			}
			return
		}
	}
	t.Fatalf("managed block has no command line:\n%s", block)
}

func TestEnsureCodexStopHookRefusesUnbalancedManagedMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("model = \"gpt-5.6\"\n" + codexStopHookStart + "\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureCodexStopHook(path, "bash /x.sh"); err == nil {
		t.Fatal("unbalanced managed markers were accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("malformed managed block was modified")
	}
	if backups, _ := filepath.Glob(path + ".bak.*"); len(backups) != 0 {
		t.Fatalf("refused config left %d backups, want none", len(backups))
	}
}

func TestEnsureCodexStopHookRefusesInlineHookNamespaceConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	// Valid TOML, but inline tables are immutable: appending [[hooks.Stop]]
	// attempts to reopen `hooks` and makes the whole file invalid.
	original := []byte(`hooks = { SessionStart = [{ matcher = "*", hooks = [{ type = "command", command = "echo theirs" }] }] }
model = "gpt-5.6-sol"
`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureCodexStopHook(path, "bash /x/agentsmemory-stop-hook.sh"); err == nil {
		t.Fatal("inline hooks namespace conflict was accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatalf("conflicting valid config was modified:\n%s", got)
	}
	if backups, _ := filepath.Glob(path + ".bak.*"); len(backups) != 0 {
		t.Fatalf("refused namespace conflict left %d backups, want none", len(backups))
	}
}

func TestEnsureCodexStopHookRefusesMalformedTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("model = [this is not valid TOML\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureCodexStopHook(path, "bash /x/agentsmemory-stop-hook.sh"); err == nil {
		t.Fatal("malformed config.toml was accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("malformed config.toml was modified")
	}
}

func TestRetireLegacyCodexHookDeletesOwnedHooksFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	original := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"bash /Users/me/.codex/agentsmemory-stop-hook.sh"}]}]}}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	changed, remains, err := retireLegacyCodexHook(path)
	if err != nil {
		t.Fatalf("retire legacy hook: %v", err)
	}
	if !changed || remains {
		t.Fatalf("retire result = changed %v, remains %v; want true, false", changed, remains)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owned hooks.json still exists: %v", err)
	}
	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) != 1 {
		t.Fatalf("legacy backups = %d, want 1", len(backups))
	}
	backup, _ := os.ReadFile(backups[0])
	if string(backup) != string(original) {
		t.Fatal("legacy backup does not match original bytes")
	}
}

func TestRetireLegacyCodexHookPreservesCompositeCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	owned := "bash /Users/me/.codex/" + hookFile
	composite := "run-user-check && " + owned
	original := []byte(`{"hooks":{"Stop":[{"hooks":[` +
		`{"type":"command","command":` + strconv.Quote(owned) + `},` +
		`{"type":"command","command":` + strconv.Quote(composite) + `}` +
		`] }]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	changed, remains, err := retireLegacyCodexHook(path)
	if err != nil {
		t.Fatalf("retire legacy Codex hook: %v", err)
	}
	if !changed || !remains {
		t.Fatalf("retire result = changed %v, remains %v; want true, true", changed, remains)
	}
	stop := readStop(t, path)
	if hookPresent(stop, owned) {
		t.Fatal("exact installer-owned command survived retirement")
	}
	if !hookPresent(stop, composite) {
		t.Fatal("composite user command containing the hook filename was deleted")
	}
}

func TestRetireLegacyCodexHookRecognizesOwnedPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hooks.json")
	owned := "bash " + filepath.Join(dir, hookFile)
	original := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":` +
		strconv.Quote(owned) + `}]}]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	changed, remains, err := retireLegacyCodexHook(path)
	if err != nil {
		t.Fatalf("retire legacy Codex hook: %v", err)
	}
	if !changed || remains {
		t.Fatalf("retire result = changed %v, remains %v; want true, false", changed, remains)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owned hooks.json with spaced config path still exists: %v", err)
	}
}

func TestRetireLegacyCodexHookPreservesForeignContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	original := []byte(`{
  "owner": "keep",
  "hooks": {
    "Stop": [
      {"hooks":[{"type":"command","command":"bash /Users/me/.codex/agentsmemory-stop-hook.sh"}]},
      {"hooks":[{"type":"command","command":"echo theirs"}]}
    ]
  }
}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	changed, remains, err := retireLegacyCodexHook(path)
	if err != nil {
		t.Fatalf("retire legacy hook: %v", err)
	}
	if !changed || !remains {
		t.Fatalf("retire result = changed %v, remains %v; want true, true", changed, remains)
	}
	stop := readStop(t, path)
	if hookPresent(stop, "bash /Users/me/.codex/agentsmemory-stop-hook.sh") {
		t.Fatal("agentsmemory hook survived legacy cleanup")
	}
	if !hookPresent(stop, "echo theirs") {
		t.Fatal("foreign Stop hook was removed")
	}
	raw, _ := os.ReadFile(path)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["owner"] != "keep" {
		t.Fatalf("foreign top-level content was lost: %v", got)
	}
}

func TestRetireLegacyCodexHookPreservesLargeForeignInteger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	original := []byte(`{"foreign":9007199254740993,"hooks":{"Stop":[{"hooks":[` +
		`{"type":"command","command":"bash /Users/me/.codex/agentsmemory-stop-hook.sh"}` +
		`] }]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	changed, remains, err := retireLegacyCodexHook(path)
	if err != nil {
		t.Fatalf("retire legacy Codex hook: %v", err)
	}
	if !changed || !remains {
		t.Fatalf("retire result = changed %v, remains %v; want true, true", changed, remains)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("foreign integer lost precision:\n%s", raw)
	}
}

func TestRetireLegacyCodexHookRefusesMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	original := []byte(`{"hooks":`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := retireLegacyCodexHook(path); err == nil {
		t.Fatal("malformed legacy hooks.json was accepted")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("malformed legacy hooks.json was modified")
	}
}

// TestCursorMCPRegistrationPreservesForeignServers is the highest-impact
// assertion in ADR-020.
//
// mcp.json is a file the user shares with every other MCP server they run, and
// this is the first registration path with no CLI between us and it. Every other
// agent's registration goes through `<agent> mcp add`, which merges on our behalf
// and cannot lose anything. Here a careless write silently deletes the user's
// other servers, and they find out when a tool they rely on stops existing.
func TestCursorMCPRegistrationPreservesForeignServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	prior := `{
  "mcpServers": {
    "someone-elses": {"command": "/usr/local/bin/theirs", "args": ["--flag"]}
  },
  "unrelatedTopLevelKey": {"keep": "me"}
}`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	entry := map[string]any{"type": "http", "url": "http://localhost:8080/mcp"}
	changed, err := ensureMCPServer(path, "agentsmemory", entry)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !changed {
		t.Fatal("registering a new server reported no change")
	}

	var got map[string]any
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the file we wrote does not parse: %v\n%s", err, body)
	}
	servers, _ := got["mcpServers"].(map[string]any)
	if _, ok := servers["someone-elses"]; !ok {
		t.Errorf("the user's own MCP server was lost:\n%s", body)
	}
	if _, ok := servers["agentsmemory"]; !ok {
		t.Errorf("our server was not registered:\n%s", body)
	}
	if _, ok := got["unrelatedTopLevelKey"]; !ok {
		t.Errorf("an unrelated top-level key was dropped:\n%s", body)
	}

	// One backup of the pre-existing file, and re-running writes nothing at all.
	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) != 1 {
		t.Errorf("expected exactly 1 backup, got %d", len(backups))
	}
	changed, err = ensureMCPServer(path, "agentsmemory", entry)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if changed {
		t.Error("re-registering an identical entry rewrote the file")
	}
	if again, _ := filepath.Glob(path + ".bak.*"); len(again) != 1 {
		t.Errorf("a no-op registration left another backup: %d", len(again))
	}
}

// TestCursorMCPRefusesUnparseableJSON: a file we cannot parse is a file we must
// not replace. The same stance ensureHooks takes on settings.json, and it matters
// more here — a hand-edited mcp.json with a trailing comma is common, and
// overwriting it would destroy configuration we never read.
func TestCursorMCPRefusesUnparseableJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	broken := `{"mcpServers": {"theirs": {"command": "x"},}}` // trailing comma
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ensureMCPServer(path, "agentsmemory", map[string]any{"url": "u"}); err == nil {
		t.Fatal("an unparseable mcp.json was accepted; the next step overwrites it")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(after) != broken {
		t.Errorf("the unparseable file was modified:\n got: %s\nwant: %s", after, broken)
	}
}

// TestEveryInstallRemovesDuplicateHookEntries is ADR-057 T2's settings half:
// exact-duplicate hook entries within an event — any command, not only the
// kit's — collapse to one on every install, the collapse counts as a change,
// and a second run changes nothing. The fixture is the owner's real file of
// 2026-09-05: cbm-session-reminder four times on SessionStart, appended by an
// upstream installer that never dedupes.
func TestEveryInstallRemovesDuplicateHookEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cbm := `"$HOME/.claude/hooks/cbm-session-reminder"`
	other := `"$HOME/.claude/hooks/someone-elses-hook"`
	entry := func(cmd string) map[string]any {
		return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}}
	}
	seed := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{entry(cbm), entry(cbm), entry(cbm), entry(cbm), entry(other), entry(other)},
		"Stop":         []any{entry(other)},
	}}
	raw, _ := json.Marshal(seed)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	kit := "bash -- /tmp/agentsmemory-recall-hook.sh"
	changed, err := ensureHook(path, "SessionStart", kit, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("six entries collapsing to two was reported as no change")
	}
	count := func(event, cmd string) int {
		n := 0
		for _, c := range hookCommandsUnder(t, path, event) {
			if c == cmd {
				n++
			}
		}
		return n
	}
	if got := count("SessionStart", cbm); got != 1 {
		t.Errorf("cbm-session-reminder registered %d time(s) after install, want 1", got)
	}
	if got := count("SessionStart", other); got != 1 {
		t.Errorf("an unrelated duplicated command was left at %d, want 1 — the dedupe is for every command", got)
	}
	if got := count("Stop", other); got != 1 {
		t.Errorf("a single entry on another event was touched: %d", got)
	}
	if got := count("SessionStart", kit); got != 1 {
		t.Errorf("the kit's own registration = %d, want 1", got)
	}
	// Idempotent.
	changed, err = ensureHook(path, "SessionStart", kit, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("a deduplicated file was rewritten on the next run")
	}
}

// hookCommandsUnder lists every hook command registered under event.
func hookCommandsUnder(t *testing.T, path, event string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range doc.Hooks[event] {
		for _, h := range m.Hooks {
			out = append(out, h.Command)
		}
	}
	return out
}
