package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordedCall captures one commandRunner invocation so tests can assert the
// exact external command sequence the installer would drive.
type recordedCall struct {
	shell string   // non-empty for a runShell call
	name  string   // program, for a run call
	args  []string // args, for a run call
	env   []string // extra env, for a run call
}

// recordingRunner is a fake commandRunner: it records calls instead of executing
// them, so the whole install flow can be exercised without a Claude CLI present.
type recordingRunner struct{ calls []recordedCall }

func (r *recordingRunner) run(name string, args, env []string) error {
	r.calls = append(r.calls, recordedCall{name: name, args: args, env: env})
	return nil
}

func (r *recordingRunner) runShell(script string) error {
	r.calls = append(r.calls, recordedCall{shell: script})
	return nil
}

// rendered flattens a recorded call to a single comparable string: "SHELL: …"
// for a shell pipeline, or the joined args for a run call.
func (c recordedCall) rendered() string {
	if c.shell != "" {
		return "SHELL: " + c.shell
	}
	return strings.Join(c.args, " ")
}

func renderAll(calls []recordedCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.rendered()
	}
	return out
}

// newTestInstaller builds an Installer wired to a recording runner and a temp
// config dir, with a fixed token so the MCP step always runs non-interactively.
func newTestInstaller(t *testing.T, recommended bool) (*Installer, *recordingRunner, string) {
	t.Helper()
	dir := t.TempDir()
	rr := &recordingRunner{}
	inst := &Installer{
		targetDir:   dir,
		claudeBin:   "claude",
		mcpURL:      defaultMCPURL,
		scope:       "user",
		token:       "TESTTOK",
		recommended: recommended,
		out:         &bytes.Buffer{},
		in:          strings.NewReader(""),
		runner:      rr,
	}
	return inst, rr, dir
}

func TestAssetsEmbedded(t *testing.T) {
	// The three shipped assets must be embedded; the retired agentsmemory.md
	// must not be.
	for _, name := range []string{"commands/M.md", "commands/am.md", hookAsset} {
		data, err := assets.ReadFile(name)
		if err != nil {
			t.Fatalf("asset %s not embedded: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("asset %s is empty", name)
		}
	}
	if _, err := assets.ReadFile("commands/agentsmemory.md"); err == nil {
		t.Fatal("retired commands/agentsmemory.md is embedded but should not be")
	}
}

func TestInstallCoreWritesAssetsAndRegistersMCP(t *testing.T) {
	inst, rr, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Commands + hook must be on disk.
	for _, rel := range []string{"commands/M.md", "commands/am.md", hookAsset} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}

	// Stop hook must be registered pointing at the installed hook.
	wantCmd := "bash " + filepath.Join(dir, hookAsset)
	if !stopHookPresent(readStop(t, filepath.Join(dir, "settings.json")), wantCmd) {
		t.Errorf("Stop hook %q not registered", wantCmd)
	}

	// Only the two agentsmemory MCP calls should have run (no extensions).
	want := []string{
		"mcp remove --scope user agentsmemory",
		"mcp add --transport http --scope user agentsmemory " + defaultMCPURL + " --header Authorization: Bearer TESTTOK",
	}
	got := renderAll(rr.calls)
	if !equalStrings(got, want) {
		t.Errorf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}

	// Every claude call must pin CLAUDE_CONFIG_DIR to the target dir.
	for _, c := range rr.calls {
		if c.shell != "" {
			continue
		}
		if len(c.env) == 0 || c.env[0] != "CLAUDE_CONFIG_DIR="+dir {
			t.Errorf("call %q missing CLAUDE_CONFIG_DIR=%s env, got %v", c.rendered(), dir, c.env)
		}
	}
}

func TestInstallRecommendedSequence(t *testing.T) {
	inst, rr, _ := newTestInstaller(t, true)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	bin := expandTilde(codebaseMemoryBin)
	want := []string{
		// core: our MCP first
		"mcp remove --scope user agentsmemory",
		"mcp add --transport http --scope user agentsmemory " + defaultMCPURL + " --header Authorization: Bearer TESTTOK",
		// recommended: codebase-memory installer + registration
		"SHELL: " + codebaseMemoryInstall,
		"mcp remove --scope user codebasememory",
		"mcp add --transport stdio --scope user codebasememory -- " + bin,
		// recommended: plugins
		"plugin marketplace add agenticnotetaking/eidos",
		"plugin install eidos@eidos",
		"plugin marketplace add openai/codex-plugin-cc",
		"plugin install codex@openai-codex",
	}
	got := renderAll(rr.calls)
	if !equalStrings(got, want) {
		t.Errorf("recommended sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestResolveClaudeBinOverride(t *testing.T) {
	got, err := resolveClaudeBin("my-claude")
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-claude" {
		t.Errorf("resolveClaudeBin(override) = %q, want my-claude", got)
	}
}

func TestValidSandboxName(t *testing.T) {
	valid := []string{"proj", "proj1", "my-project", "team_work"}
	for _, name := range valid {
		if err := validSandboxName(name); err != nil {
			t.Errorf("validSandboxName(%q) = %v, want nil", name, err)
		}
	}
	// Reject traversal, separators, leading-dot hidden names, and control bytes.
	invalid := []string{"", ".", "..", "a/b", "../escape", `a\b`, ".ssh", "a.b", "bad name", "x\x00y"}
	for _, name := range invalid {
		if err := validSandboxName(name); err == nil {
			t.Errorf("validSandboxName(%q) = nil, want an error", name)
		}
	}
}

func TestDryRunnerRedactsToken(t *testing.T) {
	// --dry-run must never echo a bearer token to stdout or a captured log.
	var buf bytes.Buffer
	d := dryRunner{out: &buf}
	if err := d.run("claude",
		[]string{"mcp", "add", "--header", "Authorization: Bearer SUPERSECRET"},
		[]string{"CLAUDE_CONFIG_DIR=/x"}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "SUPERSECRET") {
		t.Errorf("dry-run output leaked the token: %q", got)
	}
	if !strings.Contains(got, "Authorization: Bearer ***") {
		t.Errorf("expected a redacted header, got %q", got)
	}
}

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
