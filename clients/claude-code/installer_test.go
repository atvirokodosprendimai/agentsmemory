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
	// The shipped assets must be embedded; the retired agentsmemory.md must not be.
	for _, name := range []string{"commands/M.md", "commands/am.md", "commands/load-skill.md", hookAsset, bootstrapAsset} {
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
	for _, rel := range []string{"commands/M.md", "commands/am.md", "commands/load-skill.md", hookAsset} {
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

func TestInstallWritesMemoryBootstrap(t *testing.T) {
	// A default install must drop the always-on protocol and wire CLAUDE.md to
	// import it, so the memory-first workflow applies without typing /am.
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, bootstrapFile)); err != nil {
		t.Errorf("expected %s written: %v", bootstrapFile, err)
	}
	claudeMd, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(claudeMd), memoryImportLine) {
		t.Errorf("CLAUDE.md does not import the bootstrap: %q", claudeMd)
	}
}

func TestResolveInstallTarget(t *testing.T) {
	home := "/home/u"
	global := filepath.Join(home, ".claude")

	// --global cannot be combined with the other target selectors.
	for _, tc := range []struct{ sandbox, claudeDir string }{
		{sandbox: "proj"},
		{claudeDir: "/x"},
	} {
		if _, _, _, err := resolveInstallTarget(true, tc.sandbox, tc.claudeDir, home); err == nil {
			t.Errorf("resolveInstallTarget(global, %q, %q) = nil error, want conflict", tc.sandbox, tc.claudeDir)
		}
	}

	// Precedence and the explicit-target flag.
	cases := []struct {
		name         string
		global       bool
		sandbox      string
		claudeDir    string
		wantTarget   string
		wantSandbox  string
		wantExplicit bool
	}{
		{"global flag", true, "", "", global, "", true},
		{"sandbox", false, "proj", "", sandboxDir("proj"), "proj", true},
		{"claude-dir", false, "", "/custom", "/custom", "", true},
		{"bare default", false, "", "", global, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, sandbox, explicit, err := resolveInstallTarget(tc.global, tc.sandbox, tc.claudeDir, home)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if target != tc.wantTarget || sandbox != tc.wantSandbox || explicit != tc.wantExplicit {
				t.Errorf("got (target=%q sandbox=%q explicit=%v), want (target=%q sandbox=%q explicit=%v)",
					target, sandbox, explicit, tc.wantTarget, tc.wantSandbox, tc.wantExplicit)
			}
		})
	}

	// An invalid sandbox name is rejected here too (defense in depth with the CLI).
	if _, _, _, err := resolveInstallTarget(false, "../escape", "", home); err == nil {
		t.Error("resolveInstallTarget accepted an invalid sandbox name, want an error")
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

func TestPromptInstallModeSandbox(t *testing.T) {
	// A typed, valid name switches the install to that sandbox.
	inst := &Installer{
		targetDir: filepath.Join(homeDir(), ".claude"),
		out:       &bytes.Buffer{},
		in:        strings.NewReader("myproj\n"),
	}
	inst.promptInstallMode()
	if inst.sandboxName != "myproj" {
		t.Errorf("sandboxName = %q, want myproj", inst.sandboxName)
	}
	if want := sandboxDir("myproj"); inst.targetDir != want {
		t.Errorf("targetDir = %q, want %q", inst.targetDir, want)
	}
}

func TestPromptInstallModeGlobalOnBlank(t *testing.T) {
	// Pressing Enter (blank) keeps the global default untouched.
	global := filepath.Join(homeDir(), ".claude")
	inst := &Installer{targetDir: global, out: &bytes.Buffer{}, in: strings.NewReader("\n")}
	inst.promptInstallMode()
	if inst.sandboxName != "" {
		t.Errorf("sandboxName = %q, want empty", inst.sandboxName)
	}
	if inst.targetDir != global {
		t.Errorf("targetDir = %q, want %q (unchanged)", inst.targetDir, global)
	}
}

func TestPromptInstallModeSkipped(t *testing.T) {
	// An explicit --sandbox/--claude-dir (explicitTarget) or --yes must skip the
	// prompt entirely: even a name waiting on stdin is ignored, so the target set
	// by the flags is preserved.
	for _, tc := range []struct {
		name string
		inst *Installer
	}{
		{"explicitTarget", &Installer{targetDir: "/x", explicitTarget: true, out: &bytes.Buffer{}, in: strings.NewReader("myproj\n")}},
		{"yes", &Installer{targetDir: "/x", yes: true, out: &bytes.Buffer{}, in: strings.NewReader("myproj\n")}},
		{"dryRun", &Installer{targetDir: "/x", dryRun: true, out: &bytes.Buffer{}, in: strings.NewReader("myproj\n")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.inst.promptInstallMode()
			if tc.inst.sandboxName != "" || tc.inst.targetDir != "/x" {
				t.Errorf("prompt not skipped: sandbox=%q target=%q", tc.inst.sandboxName, tc.inst.targetDir)
			}
		})
	}
}

func TestPromptInstallModeInvalidThenEOF(t *testing.T) {
	// An invalid name is rejected; with no more input (EOF) the loop must not spin
	// forever — it falls back to the global default rather than hanging.
	global := filepath.Join(homeDir(), ".claude")
	var out bytes.Buffer
	inst := &Installer{targetDir: global, out: &out, in: strings.NewReader("bad name")}
	inst.promptInstallMode()
	if inst.sandboxName != "" || inst.targetDir != global {
		t.Errorf("expected global fallback, got sandbox=%q target=%q", inst.sandboxName, inst.targetDir)
	}
	if !strings.Contains(out.String(), "invalid sandbox name") {
		t.Errorf("expected an invalid-name message, got %q", out.String())
	}
}

func TestPromptModeThenTokenShareReader(t *testing.T) {
	// The mode prompt and the token prompt read from ONE stream: line 1 picks the
	// sandbox, line 2 is consumed as the token. A shared bufio.Reader is what makes
	// this work — a second reader would drop the buffered token line.
	inst := &Installer{
		targetDir: filepath.Join(homeDir(), ".claude"),
		out:       &bytes.Buffer{},
		in:        strings.NewReader("myproj\nTOKEN123\n"),
	}
	inst.promptInstallMode()
	if inst.sandboxName != "myproj" {
		t.Fatalf("sandboxName = %q, want myproj", inst.sandboxName)
	}
	if got := inst.resolveToken(); got != "TOKEN123" {
		t.Errorf("resolveToken() = %q, want TOKEN123 (reader not shared?)", got)
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
