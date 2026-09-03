package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

// newTestInstaller builds a Claude Installer wired to a recording runner and a
// temp config dir, with a fixed token so the MCP step always runs
// non-interactively.
func newTestInstaller(t *testing.T, recommended bool) (*Installer, *recordingRunner, string) {
	t.Helper()
	return newTestInstallerFor(t, claudeKit, recommended)
}

// newTestInstallerFor is newTestInstaller for an explicit agent kit, so the codex
// install path is exercised through exactly the same flow as the Claude one.
func newTestInstallerFor(t *testing.T, kit agentKit, recommended bool) (*Installer, *recordingRunner, string) {
	t.Helper()
	dir := t.TempDir()
	rr := &recordingRunner{}
	inst := &Installer{
		targetDir:   dir,
		kit:         kit,
		agentBin:    kit.bin,
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
	for _, name := range []string{"commands/am.md", "commands/load-skill.md", hookAsset, statsHelperAsset, bootstrapAsset, piExtensionAsset} {
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

	// Commands + both hooks must be on disk.
	for _, rel := range []string{"commands/am.md", "commands/load-skill.md", hookFile, verifyHookFile, sessionEndHookFile, statsHelperFile} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}

	// Stop hook must be registered pointing at the installed hook.
	wantCmd := inst.hookCommand(filepath.Join(dir, hookFile))
	if !hookPresent(readStop(t, filepath.Join(dir, "settings.json")), wantCmd) {
		t.Errorf("Stop hook %q not registered", wantCmd)
	}

	// ...and its SessionStart companion, which is what makes anchor verification
	// automatic rather than a command nobody remembers to run.
	wantVerify := inst.hookCommand(filepath.Join(dir, verifyHookFile))
	if !hookPresent(readHookEvent(t, filepath.Join(dir, "settings.json"), "SessionStart"), wantVerify) {
		t.Errorf("SessionStart hook %q not registered", wantVerify)
	}

	// ...and the closing report, which is the only one of the three that sees a
	// whole session.
	wantEnd := inst.hookCommand(filepath.Join(dir, sessionEndHookFile))
	if !hookPresent(readHookEvent(t, filepath.Join(dir, "settings.json"), "SessionEnd"), wantEnd) {
		t.Errorf("SessionEnd hook %q not registered", wantEnd)
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

func TestInstallReplacesEveryPreQuoteHookCommand(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	legacyPaths := map[string]string{
		"Stop":          inst.hookPath(),
		"SessionStart":  inst.verifyHookPath(),
		"SubagentStart": inst.subagentHookPath(),
		"SubagentStop":  inst.hookPath(),
		"SessionEnd":    inst.sessionEndHookPath(),
	}
	hooks := map[string]any{}
	for event, path := range legacyPaths {
		hooks[event] = []any{map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": "bash " + path,
			}},
		}}
	}
	body, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(dir, claudeKit.hooksFile)
	if err := os.WriteFile(settingsPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := inst.run(); err != nil {
		t.Fatalf("upgrade install: %v", err)
	}
	// One registration PER PLAN, not per event: ADR-041 T4 put a second hook on
	// SessionStart beside the verify hook. The property under test is unchanged —
	// a legacy unquoted entry that survived the upgrade shows up as one more
	// registration than there are plans for that event.
	want := map[string]int{}
	for _, plan := range inst.hookPlans() {
		want[plan.event]++
	}
	for _, plan := range inst.hookPlans() {
		entries := readHookEvent(t, settingsPath, plan.event)
		if len(entries) != want[plan.event] {
			t.Errorf("%s has %d registrations after upgrade, want %d",
				plan.event, len(entries), want[plan.event])
		}
		if !hookPresent(entries, plan.cmd) {
			t.Errorf("%s does not contain current quoted command %q", plan.event, plan.cmd)
		}
	}
}

// TestGlobalInstallDoesNotPinConfigDir pins the fix for the silent-no-tools bug:
// a global install must leave the agent's config-dir variable alone. Pinning
// CLAUDE_CONFIG_DIR=~/.claude moves the MCP registry to ~/.claude/.claude.json,
// while a later plain `claude` reads ~/.claude.json and finds nothing.
func TestGlobalInstallDoesNotPinConfigDir(t *testing.T) {
	rr := &recordingRunner{}
	inst := &Installer{
		targetDir: claudeKit.globalConfigDir(homeDir()),
		kit:       claudeKit,
		agentBin:  claudeKit.bin,
		out:       &bytes.Buffer{},
		runner:    rr,
	}
	if err := inst.agent(false, "mcp", "add", "agentsmemory"); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(rr.calls))
	}
	if len(rr.calls[0].env) != 0 {
		t.Errorf("global install pinned %v; it must inherit the environment", rr.calls[0].env)
	}
}

// TestSandboxInstallPinsConfigDir is the other half: a sandbox is not a directory
// the agent looks in on its own, so registration only lands there with the
// variable set — and `aiagentmemory run <name>` exports the same one at launch.
func TestSandboxInstallPinsConfigDir(t *testing.T) {
	rr := &recordingRunner{}
	dir := sandboxDir("acme")
	inst := &Installer{
		targetDir:   dir,
		sandboxName: "acme",
		kit:         claudeKit,
		agentBin:    claudeKit.bin,
		out:         &bytes.Buffer{},
		runner:      rr,
	}
	if err := inst.agent(false, "mcp", "add", "agentsmemory"); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if want := "CLAUDE_CONFIG_DIR=" + dir; len(rr.calls[0].env) == 0 || rr.calls[0].env[0] != want {
		t.Errorf("sandbox install env = %v, want %s", rr.calls[0].env, want)
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
		// recommended: review plugin
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
		if _, _, _, err := resolveInstallTarget(claudeKit, true, false, tc.sandbox, tc.claudeDir, home); err == nil {
			t.Errorf("resolveInstallTarget(global, %q, %q) = nil error, want conflict", tc.sandbox, tc.claudeDir)
		}
	}

	// Precedence and the explicit-target flag.
	cases := []struct {
		name         string
		global       bool
		local        bool
		sandbox      string
		claudeDir    string
		wantTarget   string
		wantSandbox  string
		wantExplicit bool
	}{
		{"global flag", true, false, "", "", global, "", true},
		{"sandbox", false, false, "proj", "", sandboxDir("proj"), "proj", true},
		{"claude-dir", false, false, "", "/custom", "/custom", "", true},
		{"bare default", false, false, "", "", global, "", false},
		// --local implies global, and implies it EXPLICITLY: a self-hoster must not
		// be stopped by the interactive global-vs-sandbox prompt.
		{"local implies global", false, true, "", "", global, "", true},
		// ...but only as a default. A named target still wins, so "--local
		// --sandbox proj" is a local server in an isolated config, not an error.
		{"local yields to sandbox", false, true, "proj", "", sandboxDir("proj"), "proj", true},
		{"local yields to claude-dir", false, true, "", "/custom", "/custom", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, sandbox, explicit, err := resolveInstallTarget(claudeKit, tc.global, tc.local, tc.sandbox, tc.claudeDir, home)
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
	if _, _, _, err := resolveInstallTarget(claudeKit, false, false, "../escape", "", home); err == nil {
		t.Error("resolveInstallTarget accepted an invalid sandbox name, want an error")
	}
}

func TestResolveInstallTargetMakesConfigDirAbsolute(t *testing.T) {
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(start); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	target, _, _, err := resolveInstallTarget(codexKit, false, false, "", "relative-config", "/unused")
	if err != nil {
		t.Fatalf("resolve relative --config-dir: %v", err)
	}
	// Use the OS-resolved working directory because macOS exposes /var through
	// the /private/var symlink while filepath.Abs follows os.Getwd.
	want := filepath.Join(cwd, "relative-config")
	if target != want || !filepath.IsAbs(target) {
		t.Fatalf("relative --config-dir resolved to %q, want absolute %q", target, want)
	}
}

func TestHookCommandCarriesTheInstallMCPURL(t *testing.T) {
	path := "/cfg/agentsmemory-stop-hook.sh"
	hosted := hookCommand(defaultMCPURL, path)
	local := hookCommand(localMCPURL, path)
	if hosted == local {
		t.Fatal("hosted and --local hook commands are identical, so the install's palace never reaches /stats")
	}
	if !strings.Contains(hosted, defaultMCPURL) || !strings.Contains(local, localMCPURL) {
		t.Errorf("hookCommand did not embed the install URL\n hosted: %s\n local: %s", hosted, local)
	}
	if strings.Contains(hosted, localMCPURL) {
		t.Errorf("hosted hook command still names the local palace: %s", hosted)
	}
}

func TestHookPlansShellQuoteLiteralConfigPath(t *testing.T) {
	inst := &Installer{
		kit:       claudeKit,
		targetDir: "/tmp/a b/it's;literal",
		mcpURL:    defaultMCPURL,
	}
	got := inst.hookPlans()[0].cmd
	want := hookCommand(defaultMCPURL, "/tmp/a b/it's;literal/agentsmemory-stop-hook.sh")
	if got != want {
		t.Fatalf("Stop hook command = %q, want %q", got, want)
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

// TestInstallCodexCore covers the codex layout end to end: the same command
// markdown lands in prompts/ instead of commands/, the Stop hook registers in
// config.toml instead of Claude's settings.json, AGENTS.md carries the protocol inlined
// (there is no @import on codex), and the MCP is registered with
// --bearer-token-env-var since `codex mcp add` has no static-header flag.
func TestInstallCodexCore(t *testing.T) {
	inst, rr, dir := newTestInstallerFor(t, codexKit, false)
	configBefore := []byte("# user formatting stays\nmodel = \"gpt-5.6-sol\"\n")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), configBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	wantCmd := inst.hookCommand(filepath.Join(dir, hookFile))
	legacy := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"hooks": []any{map[string]any{"type": "command", "command": wantCmd}},
			}},
		},
	}
	legacyBody, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), legacyBody, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	for _, rel := range []string{"prompts/am.md", "prompts/load-skill.md", hookFile, statsHelperFile} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "commands")); err == nil {
		t.Error("codex install wrote a commands/ dir; codex reads prompts/")
	}

	configAfter, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(configAfter), string(configBefore)) {
		t.Errorf("existing config.toml was reformatted or replaced:\n%s", configAfter)
	}
	if !strings.Contains(string(configAfter), `command = "`+wantCmd+`"`) {
		t.Errorf("Stop hook %q not registered in config.toml:\n%s", wantCmd, configAfter)
	}
	if _, err := os.Stat(filepath.Join(dir, "hooks.json")); !os.IsNotExist(err) {
		t.Errorf("legacy hooks.json was not removed: %v", err)
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "hooks.json.bak.*")); len(backups) != 1 {
		t.Errorf("legacy hooks.json backups = %d, want 1", len(backups))
	}

	// AGENTS.md must hold the protocol itself: an @import line would be inert.
	agentsMd, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if strings.Contains(string(agentsMd), memoryImportLine) {
		t.Errorf("AGENTS.md uses an @import, which codex does not resolve: %q", agentsMd)
	}
	if !strings.Contains(string(agentsMd), "agentsmemory — operating protocol") {
		t.Errorf("AGENTS.md does not carry the inlined protocol: %q", agentsMd)
	}

	// The token is persisted for the wrapper to export, and must not be readable
	// by anyone else — codex reads it from the environment, not from its config.
	tokenPath := filepath.Join(dir, tokenFile)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat %s: %v", tokenFile, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode = %#o, want 0600", tokenFile, perm)
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(raw)), tokenEnvVar+"=TESTTOK"; got != want {
		t.Errorf("token file = %q, want %q", got, want)
	}

	want := []string{
		"mcp remove agentsmemory",
		"mcp add agentsmemory --url " + defaultMCPURL + " --bearer-token-env-var " + tokenEnvVar,
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Errorf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}

	// Registration must land in the config dir we are installing into.
	for _, c := range rr.calls {
		if len(c.env) == 0 || c.env[0] != "CODEX_HOME="+dir {
			t.Errorf("call %q missing CODEX_HOME=%s env, got %v", c.rendered(), dir, c.env)
		}
	}
}

// TestCodexSummaryDoesNotAskForLegacyHookTrust pins the next steps to Codex's
// native config.toml hook. Reintroducing the old /hooks instruction would send
// every successful install through a step that no longer applies.
func TestCodexSummaryDoesNotAskForLegacyHookTrust(t *testing.T) {
	inst, _, _ := newTestInstallerFor(t, codexKit, false)
	out := &bytes.Buffer{}
	inst.out = out
	inst.summary()

	got := strings.ToLower(out.String())
	if strings.Contains(got, "/hooks") || strings.Contains(got, "trust the") {
		t.Errorf("Codex summary still asks for legacy hook trust: %q", out.String())
	}
}

// TestInstallCodexRecommended pins the codex extension set: codebase-memory only,
// registered in codex's stdio form, with no Claude plugin-marketplace calls.
func TestInstallCodexRecommended(t *testing.T) {
	inst, rr, _ := newTestInstallerFor(t, codexKit, true)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	want := []string{
		"mcp remove agentsmemory",
		"mcp add agentsmemory --url " + defaultMCPURL + " --bearer-token-env-var " + tokenEnvVar,
		"SHELL: " + codebaseMemoryInstall,
		"mcp remove codebasememory",
		"mcp add codebasememory -- " + expandTilde(codebaseMemoryBin),
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Errorf("recommended sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestResolveInstallTargetCodex checks the global default follows the agent:
// ~/.codex, not ~/.claude. A sandbox stays one shared dir — the two agents never
// collide on a filename — so `--agent both --sandbox x` yields a single config.
func TestResolveInstallTargetCodex(t *testing.T) {
	home := "/home/u"
	target, _, _, err := resolveInstallTarget(codexKit, true, false, "", "", home)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".codex"); target != want {
		t.Errorf("codex global target = %q, want %q", target, want)
	}

	target, sandbox, _, err := resolveInstallTarget(codexKit, false, false, "proj", "", home)
	if err != nil {
		t.Fatal(err)
	}
	if target != sandboxDir("proj") || sandbox != "proj" {
		t.Errorf("codex sandbox target = (%q, %q), want (%q, proj)", target, sandbox, sandboxDir("proj"))
	}
}

// TestInstallPiCore covers the pi layout end to end. pi is the agent with no MCP
// client and no hooks, so the install must land the bridge extension, persist the
// endpoint alongside the token for it to read, and drive no agent CLI at all —
// there is no `pi mcp add` to call.
func TestInstallPiCore(t *testing.T) {
	inst, rr, dir := newTestInstallerFor(t, piKit, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	for _, rel := range []string{"prompts/am.md", "prompts/load-skill.md", piExtensionAsset} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}

	// No hook script and no hook JSON: pi has neither, and a stray .sh would only
	// suggest a gate that never fires.
	if _, err := os.Stat(filepath.Join(dir, hookFile)); err == nil {
		t.Error("pi install wrote a Stop-hook script; pi has no hook system")
	}
	for _, name := range []string{"settings.json", "config.toml", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("pi install wrote %s; pi registers no hooks", name)
		}
	}

	// AGENTS.md must hold the protocol itself — pi resolves no @import either.
	agentsMd, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if strings.Contains(string(agentsMd), memoryImportLine) {
		t.Errorf("AGENTS.md uses an @import, which pi does not resolve: %q", agentsMd)
	}
	if !strings.Contains(string(agentsMd), "agentsmemory — operating protocol") {
		t.Errorf("AGENTS.md does not carry the inlined protocol: %q", agentsMd)
	}

	// The extension reads both the token and the endpoint from the environment,
	// so both are persisted, and only the owner may read them.
	tokenPath := filepath.Join(dir, tokenFile)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat %s: %v", tokenFile, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode = %#o, want 0600", tokenFile, perm)
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	want := tokenEnvVar + "=TESTTOK\n" + mcpURLEnvVar + "=" + defaultMCPURL + "\n"
	if string(raw) != want {
		t.Errorf("token file = %q, want %q", raw, want)
	}

	if got := renderAll(rr.calls); len(got) != 0 {
		t.Errorf("pi install ran agent CLI commands %v, want none (pi has no mcp subcommand)", got)
	}
}

// TestInstallPiRecommended pins that --recommended adds nothing for pi: the
// codebase-memory MCP is stdio and the codex review plugin belongs to Claude,
// so neither has anything to attach to.
func TestInstallPiRecommended(t *testing.T) {
	inst, rr, _ := newTestInstallerFor(t, piKit, true)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := renderAll(rr.calls); len(got) != 0 {
		t.Errorf("pi --recommended ran %v, want no commands", got)
	}
}

// TestPiGlobalConfigDirNested checks the one structural difference in pi's kit:
// its default config dir is two levels deep, so globalDir carries a separator.
func TestPiGlobalConfigDirNested(t *testing.T) {
	home := "/home/u"
	if got, want := piKit.globalConfigDir(home), filepath.Join(home, ".pi", "agent"); got != want {
		t.Errorf("piKit.globalConfigDir = %q, want %q", got, want)
	}
}

// TestInstallMigratesLegacyHookDir covers the upgrade path for a config dir
// created before the hook was relocated: the old hooks/ directory must be gone
// (pi halts its launch on one, and sandboxes are shared), the script must live
// flat in the config dir, and the stale Stop entry pointing at the deleted file
// must be pruned rather than left to fail on every stop.
func TestInstallMigratesLegacyHookDir(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)

	legacy := filepath.Join(dir, legacyHookRel)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyCmd := "bash " + legacy
	if _, err := ensureHook(filepath.Join(dir, "settings.json"), "Stop", legacyCmd, nil); err != nil {
		t.Fatal(err)
	}

	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "hooks")); err == nil {
		t.Error("hooks/ still exists after install; pi halts its launch on it")
	}
	if _, err := os.Stat(filepath.Join(dir, hookFile)); err != nil {
		t.Errorf("relocated hook not written: %v", err)
	}

	stop := readStop(t, filepath.Join(dir, "settings.json"))
	if hookPresent(stop, legacyCmd) {
		t.Error("the stale Stop entry survived; it would run a deleted file on every stop")
	}
	if want := inst.hookCommand(filepath.Join(dir, hookFile)); !hookPresent(stop, want) {
		t.Errorf("relocated Stop hook %q not registered", want)
	}
}

// TestLegacyHookDirKeptWhenNotOnlyOurs guards the destructive edge: a hooks/
// directory holding the user's own script keeps that script (and the directory).
// Their files outweigh our warning.
func TestLegacyHookDirKeptWhenNotOnlyOurs(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)

	hooksDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Base(legacyHookRel), "user-own-hook.sh"} {
		if err := os.WriteFile(filepath.Join(hooksDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "user-own-hook.sh")); err != nil {
		t.Errorf("the user's own hook was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, legacyHookRel)); err == nil {
		t.Error("our legacy hook script should still have been removed")
	}
}

func TestResolveAgentKits(t *testing.T) {
	// No --agent must keep the pre-codex behaviour: Claude, nothing else.
	for _, name := range []string{"", "claude", "CLAUDE"} {
		kits, err := resolveAgentKits(name)
		if err != nil {
			t.Fatalf("resolveAgentKits(%q): %v", name, err)
		}
		if len(kits) != 1 || kits[0].name != agentClaude {
			t.Errorf("resolveAgentKits(%q) = %+v, want just claude", name, kits)
		}
	}
	kits, err := resolveAgentKits("both")
	if err != nil {
		t.Fatal(err)
	}
	// "both" predates pi and must keep meaning exactly Claude + codex, so an
	// existing script never grows a third install target behind the user's back.
	if len(kits) != 2 || kits[0].name != agentClaude || kits[1].name != agentCodex {
		t.Errorf("resolveAgentKits(both) = %+v, want [claude codex]", kits)
	}

	kits, err = resolveAgentKits("pi")
	if err != nil {
		t.Fatal(err)
	}
	if len(kits) != 1 || kits[0].name != agentPi {
		t.Errorf("resolveAgentKits(pi) = %+v, want just pi", kits)
	}

	kits, err = resolveAgentKits("all")
	if err != nil {
		t.Fatal(err)
	}
	// `all` grows as kits are added — cursor joined 2026-08-22 (ADR-020). The
	// order is the docs' order, and the assertion names every member rather than
	// only counting them: a count-only check passes when a kit is swapped for
	// another.
	wantAll := []string{agentClaude, agentCodex, agentPi, agentCursor, agentClaudeDesktop}
	if len(kits) != len(wantAll) {
		t.Errorf("resolveAgentKits(all) returned %d kits, want %d (%v)", len(kits), len(wantAll), wantAll)
	} else {
		for n, want := range wantAll {
			if kits[n].name != want {
				t.Errorf("resolveAgentKits(all)[%d] = %q, want %q", n, kits[n].name, want)
			}
		}
	}

	if _, err := resolveAgentKits("gemini"); err == nil {
		t.Error("resolveAgentKits(gemini) = nil error, want a rejection")
	}
	if _, err := resolveAgentKit("both"); err == nil {
		t.Error("resolveAgentKit(both) = nil error, want a rejection (run launches one agent)")
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

// TestWingReachesEveryClientOrSaysWhyNot pins the parity --wing promises. It is
// a promise about the CONNECTION — every call carries the wing, so a write lands
// in the right project even when the agent names none — and a promise silently
// unkept is worse than one refused: the memories still land, just in the wrong
// wing. Claude and Cursor carry it as a header, pi as an env var its bridge
// turns back into a header, Codex as the server's supported URL query, and
// Desktop as an mcp-stdio argument the bridge turns back into a header.
func TestWingReachesEveryClientOrSaysWhyNot(t *testing.T) {
	const wing = "wing_acme"

	t.Run("claude sends the header", func(t *testing.T) {
		inst, rr, _ := newTestInstallerFor(t, claudeKit, false)
		inst.wing = wing
		if err := inst.registerAgentsMemoryMCP(); err != nil {
			t.Fatalf("register: %v", err)
		}
		var sawHeader bool
		for _, call := range rr.calls {
			for i, a := range call.args {
				if a == "--header" && i+1 < len(call.args) && strings.Contains(call.args[i+1], wingHeader+": "+wing) {
					sawHeader = true
				}
			}
		}
		if !sawHeader {
			t.Fatalf("claude registration must pass %s; calls were %+v", wingHeader, rr.calls)
		}
	})

	t.Run("pi persists it for its bridge", func(t *testing.T) {
		inst, _, dir := newTestInstallerFor(t, piKit, false)
		inst.wing = wing
		if err := inst.registerAgentsMemoryMCP(); err != nil {
			t.Fatalf("register: %v", err)
		}
		env, err := os.ReadFile(inst.tokenPath())
		if err != nil {
			t.Fatalf("read pi env: %v", err)
		}
		if !strings.Contains(string(env), wingEnvVar+"="+wing) {
			t.Fatalf("pi env must carry %s; got %q", wingEnvVar, env)
		}
		// The extension is what turns that variable into a header, so the asset
		// installed beside it must actually read one and send the other.
		ext, err := os.ReadFile(filepath.Join(dir, piExtensionAsset))
		if err != nil {
			t.Fatalf("read pi extension: %v", err)
		}
		for _, want := range []string{wingEnvVar, strings.ToLower(wingHeader)} {
			if !strings.Contains(string(ext), want) {
				t.Errorf("pi bridge must reference %q to keep the wing promise", want)
			}
		}
	})

	t.Run("cursor sends the header", func(t *testing.T) {
		inst, _, dir := newTestInstallerFor(t, cursorKit, false)
		inst.wing = wing
		if err := inst.registerAgentsMemoryMCP(); err != nil {
			t.Fatalf("register: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(dir, cursorKit.mcpConfigFile))
		if err != nil {
			t.Fatalf("read Cursor MCP config: %v", err)
		}
		var got struct {
			MCPServers map[string]struct {
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("parse Cursor MCP config: %v\n%s", err, body)
		}
		if got.MCPServers[mcpName].Headers[wingHeader] != wing {
			t.Fatalf("Cursor registration must pass %s=%s; got %s", wingHeader, wing, body)
		}
	})

	t.Run("codex sends the query parameter", func(t *testing.T) {
		inst, rr, _ := newTestInstallerFor(t, codexKit, false)
		inst.mcpURL = "https://memory.example.test/mcp?existing=kept"
		inst.wing = wing
		if err := inst.registerAgentsMemoryMCP(); err != nil {
			t.Fatalf("register: %v", err)
		}
		var registeredURL string
		for _, call := range rr.calls {
			for n, arg := range call.args {
				if arg == "--url" && n+1 < len(call.args) {
					registeredURL = call.args[n+1]
				}
			}
		}
		parsed, err := url.Parse(registeredURL)
		if err != nil {
			t.Fatalf("parse registered URL %q: %v", registeredURL, err)
		}
		if got := parsed.Query().Get("wing"); got != wing {
			t.Fatalf("Codex registration wing = %q, want %q (URL %q)", got, wing, registeredURL)
		}
		if got := parsed.Query().Get("existing"); got != "kept" {
			t.Fatalf("Codex registration dropped existing query value: %q", registeredURL)
		}
	})

	t.Run("claude desktop passes it to the bridge", func(t *testing.T) {
		inst, _, dir := newTestInstallerFor(t, claudeDesktopKit, false)
		inst.serverBin = fakeBuiltServerBin(t)
		inst.wing = wing
		if err := inst.registerAgentsMemoryMCP(); err != nil {
			t.Fatalf("register: %v", err)
		}
		body, err := os.ReadFile(filepath.Join(dir, claudeDesktopKit.mcpConfigFile))
		if err != nil {
			t.Fatalf("read Desktop MCP config: %v", err)
		}
		var got struct {
			MCPServers map[string]struct {
				Args []string `json:"args"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("parse Desktop MCP config: %v\n%s", err, body)
		}
		args := got.MCPServers[mcpName].Args
		if !contains(args, "--wing") || !contains(args, wing) {
			t.Fatalf("Desktop bridge args must carry --wing %s; got %v", wing, args)
		}
	})
}

func TestNoTokenRecoveryHintRetainsWingForEveryClient(t *testing.T) {
	const wing = "wing_acme"
	for _, kit := range []agentKit{claudeKit, codexKit, piKit, cursorKit, claudeDesktopKit} {
		t.Run(kit.name, func(t *testing.T) {
			inst, rr, _ := newTestInstallerFor(t, kit, false)
			inst.token = ""
			inst.yes = true
			inst.wing = wing
			inst.mcpURL = "https://memory.example.test/mcp?existing=kept"
			extraWants := []string{}
			switch kit.name {
			case agentClaude:
				inst.scope = "project"
				inst.agentBin = "/opt/custom bins/claude"
				extraWants = append(extraWants, "--scope 'project'", "--claude-bin '/opt/custom bins/claude'")
			case agentCodex:
				inst.agentBin = "/opt/custom bins/codex"
				extraWants = append(extraWants, "--codex-bin '/opt/custom bins/codex'")
			case agentPi:
				inst.agentBin = "/opt/custom bins/pi"
				extraWants = append(extraWants, "--pi-bin '/opt/custom bins/pi'")
			case agentClaudeDesktop:
				inst.serverBin = "/opt/bin/aiagentmemory-server"
				extraWants = append(extraWants, "--server-bin '/opt/bin/aiagentmemory-server'")
			}

			if err := inst.registerAgentsMemoryMCP(); err != nil {
				t.Fatalf("register without token: %v", err)
			}
			if len(rr.calls) != 0 {
				t.Fatalf("no-token registration made calls: %+v", rr.calls)
			}
			got := inst.out.(*bytes.Buffer).String()
			wants := []string{
				"aiagentmemory install",
				"--agent '" + kit.name + "'",
				"--wing '" + wing + "'",
				"--mcp-url 'https://memory.example.test/mcp?existing=kept'",
			}
			wants = append(wants, extraWants...)
			for _, want := range wants {
				if !strings.Contains(got, want) {
					t.Errorf("recovery hint missing %q:\n%s", want, got)
				}
			}
			if (kit.name == agentCursor || kit.name == agentClaudeDesktop) && !strings.Contains(got, "--global") {
				t.Errorf("%s recovery hint must use its supported global target:\n%s", kit.name, got)
			}
		})
	}
}

// TestInstallerRegistersSubagentStart pins that the injector is installed and
// registered by the installer, not by hand.
//
// T1 measured 5/5 subagents recalling with the injection and 0/5 without it, on a
// control arm that already carried the entire protocol. A mechanism that decisive
// and only ever hand-registered is a mechanism nobody else gets.
func TestInstallerRegistersSubagentStart(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, subagentHookFile)); err != nil {
		t.Errorf("expected %s written: %v", subagentHookFile, err)
	}
	want := inst.hookCommand(filepath.Join(dir, subagentHookFile))
	if !hookPresent(readHookEvent(t, filepath.Join(dir, "settings.json"), "SubagentStart"), want) {
		t.Errorf("SubagentStart hook %q not registered — the injection then only exists on "+
			"machines where someone edited settings.json by hand", want)
	}
}

// TestSubagentContextNamesTheWing pins the one piece of orientation a subagent
// cannot derive for itself cheaply.
//
// A recall scoped to the wrong wing returns confident, on-topic, irrelevant
// results and says nothing about it — measured elsewhere in this repo at 16% of a
// curated benchmark. Telling the subagent which wing it is in costs one line and
// removes the failure entirely for the case where it would have guessed.
func TestSubagentContextNamesTheWing(t *testing.T) {
	out := runSubagentHookWithEnv(t, "AGENTSMEMORY_SUBAGENT_HOOK=on", "AGENTSMEMORY_WING=wing_acme")
	if !strings.Contains(out, "wing_acme") {
		t.Errorf("the injected context does not name the wing it was given:\n%s", out)
	}
	if !strings.Contains(out, "am_search") {
		t.Errorf("the injected context does not name the recall call:\n%s", out)
	}
}

// TestSubagentContextStaysShort pins the budget.
//
// A subagent has one job and a context window that its dispatcher is paying for.
// The protocol it already receives is thousands of words and produced zero
// recalls; restating it here would spend budget to repeat something measured not
// to work. The ceiling is deliberately tight enough that a future edit which
// starts re-explaining the protocol fails rather than merely bloats.
func TestSubagentContextStaysShort(t *testing.T) {
	out := runSubagentHookWithEnv(t, "AGENTSMEMORY_SUBAGENT_HOOK=on", "AGENTSMEMORY_WING=wing_acme")
	var env struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope did not parse: %v\n%s", err, out)
	}
	const ceiling = 1200
	if n := len(env.HookSpecificOutput.AdditionalContext); n > ceiling {
		t.Errorf("injected context is %d chars, ceiling %d — a subagent's context is paid for "+
			"by its dispatcher, and the full protocol it already receives measured 0/5", n, ceiling)
	}
}

// TestSubagentContextNeverGuessesTheWing pins the half that running the hook
// exposed, and that reading it did not.
//
// An earlier version derived the wing from the git remote when nothing
// authoritative was set. On THIS repository the derived name and the one the MCP
// registration actually writes to differ — so every subagent would have been
// told, in its first line, a wing that does not exist.
//
// The protocol already names the failure: a derived wing that disagrees with the
// registration "does not move where your memories land, it only makes your report
// of them wrong". And the guess buys nothing, because a recall that passes no
// wing is ALREADY scoped correctly server-side. So the rule is: name a wing when
// told one, say nothing about wings otherwise.
func TestSubagentContextNeverGuessesTheWing(t *testing.T) {
	// No AGENTSMEMORY_WING, and the test process runs inside this git repo, so an
	// earlier version would have derived one here.
	out := runSubagentHookWithEnv(t, "AGENTSMEMORY_SUBAGENT_HOOK=on")
	var env struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope did not parse: %v\n%s", err, out)
	}
	ctx := env.HookSpecificOutput.AdditionalContext
	if strings.Contains(ctx, "working in wing_") {
		t.Errorf("the hook asserted a wing with nothing authoritative to take it from:\n%s", ctx)
	}
	if !strings.Contains(ctx, "already scoped") {
		t.Errorf("with no wing available the context must say recall is already scoped, so the "+
			"subagent passes no wing rather than inventing one:\n%s", ctx)
	}
}

// TestShippedAgentDefinitionsNameTheMemoryTools pins the half of ADR-017 that
// does not depend on compliance at all.
//
// An agent definition with a tool allowlist can only call what the list names. A
// subagent so defined cannot reach memory HOWEVER it is instructed — the
// injection measured at 5/5 in T1 would be wasted on it, silently, because the
// instruction arrives and the tool does not exist to obey it. This is the one
// part of the ADR that changes what is POSSIBLE rather than what is asked for.
//
// Both dialects are checked, because the allowlist is spelled differently in
// each and only one of them was ever verified by hand: Claude's markdown lists
// `mcp__agentsmemory__am_search` under `tools:`, codex's TOML lists the BARE
// name `am_search` in `enabled_tools`. Getting that wrong produces a definition
// the agent accepts and that grants nothing.
//
// The check is deliberately not vacuous: it fails if the directory is missing or
// empty, so "zero definitions inspected" cannot be mistaken for "every definition
// passed" — which is exactly what a glob pointed at the wrong path produces.
func TestShippedAgentDefinitionsNameTheMemoryTools(t *testing.T) {
	dir := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "agents")
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("glob agents: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no agent definitions found to inspect — an empty result here is " +
			"indistinguishable from every definition passing, which is how a check " +
			"pointed at the wrong path reports success forever")
	}
	seen := map[string]int{}
	for _, path := range entries {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text, base, ext := string(body), filepath.Base(path), filepath.Ext(path)
		seen[ext]++
		switch ext {
		case ".md":
			if !strings.Contains(text, "tools:") {
				continue // no allowlist means every tool is available
			}
			if !strings.Contains(text, "mcp__agentsmemory__am_search") {
				t.Errorf("%s restricts tools but does not name mcp__agentsmemory__am_search: a "+
					"subagent under this definition cannot recall however it is instructed", base)
			}
		case ".toml":
			if !strings.Contains(text, "enabled_tools") {
				continue
			}
			// Bare names, NOT the mcp__server__tool form: codex scopes the
			// allowlist by putting it under [mcp_servers.<name>]. Only the
			// enabled_tools LINE is inspected — the file's comments contrast the
			// two spellings on purpose, and a whole-file match would reject the
			// explanation along with the mistake.
			allow := ""
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "enabled_tools") {
					allow = line
					break
				}
			}
			if allow == "" {
				t.Errorf("%s mentions enabled_tools but has no enabled_tools assignment", base)
				continue
			}
			if !strings.Contains(allow, `"am_search"`) {
				t.Errorf("%s restricts enabled_tools but does not name am_search: %s", base, allow)
			}
			if strings.Contains(allow, "mcp__") {
				t.Errorf("%s uses Claude's mcp__server__tool naming in a codex enabled_tools "+
					"list; codex expects bare tool names and grants nothing for these: %s",
					base, allow)
			}
		default:
			t.Errorf("%s is in an unrecognised dialect (%s) — no kit reads it, so it ships "+
				"in the binary and is installed nowhere", base, ext)
		}
	}
	for _, ext := range []string{".md", ".toml"} {
		if seen[ext] == 0 {
			t.Errorf("no %s definitions were inspected, so that dialect's assertions ran "+
				"against nothing", ext)
		}
	}
}

// TestEveryShippedAgentDefinitionExistsInEveryDialect pins the cost of shipping
// the same definition twice: a base name added for one agent and forgotten for
// the other installs cleanly on the first and fails at read time on the second.
func TestEveryShippedAgentDefinitionExistsInEveryDialect(t *testing.T) {
	root := repoRootForHooks(t)
	kits := []agentKit{claudeKit, codexKit, piKit}
	checked := 0
	for _, kit := range kits {
		if kit.agentsDir == "" {
			continue // no subagent system; nothing to ship
		}
		for _, name := range agentAssets {
			rel := filepath.Join("clients", "claude-code", kit.agentsDir, name+kit.agentAssetExt)
			if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
				t.Errorf("%s installs %s but %s is missing: the install fails at ReadFile, after "+
					"it has already written half a kit", kit.name, name, rel)
			}
			checked++
		}
	}
	if checked < 2 {
		t.Fatalf("checked %d definitions across all kits — fewer than two dialects means this "+
			"test is asserting almost nothing", checked)
	}
}

// TestInstallerRegistersSubagentStop pins the write half of ADR-017.
//
// T2 gave a subagent recall; without this it still finishes with everything it
// learned inside a transcript that `mineclaude` drops by design as "subagent
// traffic". Half a loop, and the enforced half is the one that was already
// working.
//
// The registration reuses the SESSION stop script rather than a second file: the
// two nudges differ in text, not in machinery, and a second script is a second
// thing to keep in step with a shape that has already drifted once.
func TestInstallerRegistersSubagentStop(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	want := inst.hookCommand(filepath.Join(dir, hookFile))
	settings := filepath.Join(dir, "settings.json")
	if !hookPresent(readHookEvent(t, settings, "SubagentStop"), want) {
		t.Fatalf("SubagentStop hook %q not registered — a subagent then finishes with its "+
			"findings in a transcript nothing reads", want)
	}

	// A re-install must supersede rather than accumulate: two entries mean two
	// blocking nudges on every subagent stop, which is how a checkpoint teaches
	// an agent to dismiss it unread.
	if err := inst.run(); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	entries := readHookEvent(t, settings, "SubagentStop")
	if n := len(entries); n != 1 {
		t.Errorf("re-install left %d SubagentStop entries, want 1", n)
	}
}

// TestInstallerInstallsAgentDefinitions is the rung-2 test T2 did not have.
//
// T2 shipped agentsmemory-researcher.md into the binary's embed directive and
// wrote it to no disk anywhere: the definition existed, was correct, was tested,
// and no install produced it. The test that was supposed to cover it globbed the
// REPOSITORY's agents/ directory, so it passed forever against a file that never
// reached a config dir — a check on the component rather than on the selection.
//
// This one asserts the INSTALLED artifact, which is the only assertion that could
// have failed.
func TestInstallerInstallsAgentDefinitions(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(agentAssets) == 0 {
		t.Fatal("no agent definitions are shipped, so this test asserts nothing — an empty " +
			"list here is indistinguishable from every definition installing")
	}
	for _, name := range agentAssets {
		path := inst.agentDefinitionPath(name)
		_ = dir
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("agent definition %s was not installed: %v — it exists in the binary and "+
				"on no disk, which is the whole class of defect this repository keeps shipping",
				name, err)
		}
		// Installed and useless is still not delivered: the point of shipping a
		// definition at all is that its allowlist names the memory tools.
		if !strings.Contains(string(body), "am_search") {
			t.Errorf("installed %s does not name am_search", name)
		}
	}
}

// TestEveryShippedAgentDefinitionIsInstalled closes the hole that produced the
// defect above: a definition added to the repository but not to agentAssets is
// embedded, shipped, and installed nowhere, in silence.
func TestEveryShippedAgentDefinitionIsInstalled(t *testing.T) {
	dir := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "agents")
	found, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("glob agents: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("no agent definitions found in the repository — an empty result here is " +
			"indistinguishable from every definition being listed")
	}
	listed := map[string]bool{}
	for _, name := range agentAssets {
		listed[name] = true
	}
	for _, path := range found {
		// agentAssets holds BASE names; the dialect extension comes from the kit.
		base := filepath.Base(path)
		if name := strings.TrimSuffix(base, filepath.Ext(base)); !listed[name] {
			t.Errorf("%s is shipped in the repository but not in agentAssets, so no install "+
				"writes it: it would be embedded in the binary and present on no disk", base)
		}
	}
}

// TestRedeployKitCheckCoversEveryInstalledArtifact keeps the deploy gate's
// hand-maintained list honest against the kit it is supposed to be checking.
//
// scripts/redeploy.sh byte-compares the installed client kit against this
// checkout and FAILS on drift — which is worth nothing for an artifact missing
// from its list. That already happened: T2 added the SubagentStart hook, nobody
// added it to the list, and the one artifact the kit had just gained was the one
// artifact the staleness gate could not see. A stale copy of it would have
// reported "deployed and verified".
//
// extensions/ is deliberately excluded: it is pi's bridge, installed into pi's
// sandbox rather than into a Claude config dir, so the Claude kit check has
// nothing to compare it against.
func TestRedeployKitCheckCoversEveryInstalledArtifact(t *testing.T) {
	root := repoRootForHooks(t)
	script, err := os.ReadFile(filepath.Join(root, "scripts", "redeploy.sh"))
	if err != nil {
		t.Fatalf("read redeploy.sh: %v", err)
	}
	body := string(script)

	// Driven by the lists the INSTALLER iterates, not by a directory glob: the
	// subject is "everything an install writes", and commands/ also holds a
	// CLAUDE.md generated by an unrelated tool that no install has ever shipped.
	want := []string{"clients/claude-code/bootstrap.md"}
	for _, name := range commandAssets {
		want = append(want, "clients/claude-code/commands/"+name)
	}
	for _, name := range agentAssets {
		for _, ext := range []string{".md", ".toml"} {
			want = append(want, "clients/claude-code/agents/"+name+ext)
		}
	}
	for _, asset := range []string{hookAsset, verifyHookAsset, sessionEndHookAsset, statsHelperAsset, subagentHookAsset} {
		want = append(want, "clients/claude-code/"+asset)
	}
	if len(want) < 5 {
		t.Fatalf("only %d kit artifacts found, so this check is asserting almost nothing — "+
			"the asset lists are empty", len(want))
	}
	for _, rel := range want {
		if !strings.Contains(body, rel) {
			t.Errorf("scripts/redeploy.sh's kit freshness list does not mention %s, so a stale "+
				"installed copy of it reports as verified", rel)
		}
	}
}

// TestReadmeNamesEveryHookEventTheInstallerRegisters makes the install
// documentation load-bearing, the way TestCatalogSizeIsWhatTheReadmeClaims does
// for the tool count.
//
// Both READMEs described the install as shipping "the MCP, commands, and Stop
// hook" for as long as there were five registrations. Three of them — SessionEnd,
// SubagentStart, SubagentStop — were invisible to anyone deciding whether to
// install, and to anyone auditing what an install had just done to their config.
// Prose about what ships drifts exactly like prose about anything else.
//
// The expected set is read from the SOURCE, so adding a sixth `ensureHook` call
// and forgetting the docs fails here rather than being noticed by a reader who
// never had reason to look.
func TestReadmeNamesEveryHookEventTheInstallerRegisters(t *testing.T) {
	root := repoRootForHooks(t)
	src, err := os.ReadFile(filepath.Join(root, "clients", "claude-code", "installer.go"))
	if err != nil {
		t.Fatalf("read installer.go: %v", err)
	}
	// hookPlans' `event:` fields — the one list that says what an install
	// registers. It was `ensureHook(hooksFile, "<Event>", …)` until the five
	// per-event calls were batched into one; the guard below is what turned that
	// refactor into a loud failure instead of a check that silently matched
	// nothing.
	re := regexp.MustCompile(`event:\s*"([A-Za-z]+)"`)
	matches := re.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 2 {
		t.Fatalf("found %d hook registrations in installer.go — the pattern is wrong, and "+
			"an empty set would let this check pass against a README naming nothing", len(matches))
	}
	events := map[string]bool{}
	for _, m := range matches {
		events[m[1]] = true
	}

	for _, rel := range []string{"README.md", filepath.Join("clients", "claude-code", "README.md")} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(body)
		for event := range events {
			if !strings.Contains(text, event) {
				t.Errorf("%s never mentions the %s hook, which the installer registers: someone "+
					"reading it cannot tell what an install puts in their config", rel, event)
			}
		}
	}
}

// TestOneInstallLeavesAtMostOneBackup pins the reason the five per-event
// ensureHook calls were batched into one ensureHooks call.
//
// Every write of the settings file backs it up first. Registering five events one
// at a time therefore wrote the file five times and left FOUR timestamped
// backups in the user's config dir — observed on a real install — with the count
// set to grow by one for every hook the product gains. The backups are of a file
// the installer merges into rather than replaces, so they were pure accumulation.
//
// The fresh case leaves NONE, because there was no prior file to preserve.
func TestOneInstallLeavesAtMostOneBackup(t *testing.T) {
	t.Run("fresh config dir leaves no backups", func(t *testing.T) {
		inst, _, dir := newTestInstaller(t, false)
		if err := inst.run(); err != nil {
			t.Fatalf("install: %v", err)
		}
		// Guard against the vacuous version of this test: if nothing was
		// registered there would be nothing to back up either.
		if len(readHookEvent(t, filepath.Join(dir, "settings.json"), "SubagentStop")) == 0 {
			t.Fatal("no hooks were registered, so a backup count of zero proves nothing")
		}
		if backups := settingsBackups(t, dir); len(backups) != 0 {
			t.Errorf("a fresh install left %d settings backups (%v); there was no prior file to "+
				"preserve, so every one of them is a copy of something this install wrote",
				len(backups), backups)
		}
	})

	t.Run("existing settings file is backed up exactly once", func(t *testing.T) {
		inst, _, dir := newTestInstaller(t, false)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		prior := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]}}`)
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), prior, 0o644); err != nil {
			t.Fatalf("seed settings: %v", err)
		}
		if err := inst.run(); err != nil {
			t.Fatalf("install: %v", err)
		}
		backups := settingsBackups(t, dir)
		if len(backups) != 1 {
			t.Fatalf("install left %d settings backups (%v), want exactly 1", len(backups), backups)
		}
		// The one backup must hold what was there BEFORE, which is the only thing
		// a backup is for.
		body, err := os.ReadFile(backups[0])
		if err != nil {
			t.Fatalf("read backup: %v", err)
		}
		if string(body) != string(prior) {
			t.Errorf("the backup does not hold the pre-install file:\n got: %s\nwant: %s", body, prior)
		}
		// ...and the user's own hook survived the merge.
		if !hookPresent(readHookEvent(t, filepath.Join(dir, "settings.json"), "Stop"), "echo mine") {
			t.Error("the user's own Stop hook was dropped by the merge")
		}
	})
}

// settingsBackups lists the timestamped settings backups in a config dir.
func settingsBackups(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, "settings.json.bak.*"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	return found
}

// TestInstalledAgentDefinitionNamesTheRealEndpoint pins the substitution.
//
// codex names the MCP server INSIDE the agent definition rather than inheriting
// the global registration, and that URL is not a constant: a self-hosted install
// points at localhost and a hosted one at the service. Shipping the placeholder
// verbatim — or a hard-coded localhost — produces an agent whose memory tools
// point nowhere, with no error saying so. That is the same defect class as an
// unreachable asset, one layer down: installed, well-formed, and inert.
func TestInstalledAgentDefinitionNamesTheRealEndpoint(t *testing.T) {
	const endpoint = "https://memory.example.test/mcp"
	const wing = "wing_acme"
	inst, _, _ := newTestInstaller(t, false)
	inst.kit = codexKit
	inst.agentBin = codexKit.bin
	inst.mcpURL = endpoint
	inst.wing = wing
	if err := inst.writeAgentDefinitions(); err != nil {
		t.Fatalf("write agent definitions: %v", err)
	}

	checked := 0
	for _, name := range agentAssets {
		body, err := os.ReadFile(inst.agentDefinitionPath(name))
		if err != nil {
			t.Fatalf("read installed %s: %v", name, err)
		}
		text := string(body)
		if strings.Contains(text, mcpURLPlaceholder) {
			t.Errorf("%s was installed with the placeholder still in it, so its memory tools "+
				"point at a URL that does not exist", name)
		}
		if !strings.Contains(text, endpoint+"?wing="+wing) {
			t.Errorf("%s does not name the scoped endpoint this install registered (%s, wing %s)", name, endpoint, wing)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no definitions were inspected, so this test asserts nothing")
	}
}

// TestCodexGetsItsOwnDialect pins that the codex install writes TOML and the
// Claude install writes markdown, from the same base name.
//
// The two agents share a directory NAME and disagree about everything in it. An
// install that wrote Claude's markdown into ~/.codex/agents would leave a file
// codex cannot parse, and the failure surfaces as a subagent that simply has no
// memory tools.
func TestCodexGetsItsOwnDialect(t *testing.T) {
	for _, tc := range []struct {
		kit                 agentKit
		wantExt, wantMarker string
	}{
		{claudeKit, ".md", "mcp__agentsmemory__am_search"},
		{codexKit, ".toml", "enabled_tools"},
	} {
		t.Run(tc.kit.name, func(t *testing.T) {
			inst, _, dir := newTestInstaller(t, false)
			inst.kit = tc.kit
			inst.agentBin = tc.kit.bin
			if err := inst.writeAgentDefinitions(); err != nil {
				t.Fatalf("write: %v", err)
			}
			path := filepath.Join(dir, tc.kit.agentsDir, agentAssets[0]+tc.wantExt)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s install did not write %s: %v", tc.kit.name, path, err)
			}
			if !strings.Contains(string(body), tc.wantMarker) {
				t.Errorf("%s definition does not contain %q, so it is the wrong dialect",
					tc.kit.name, tc.wantMarker)
			}
		})
	}

	// pi has no subagent system; writing definitions into its config dir would be
	// litter, and the guard must be the kit rather than a name comparison.
	inst, _, dir := newTestInstaller(t, false)
	inst.kit = piKit
	inst.agentBin = piKit.bin
	if err := inst.writeAgentDefinitions(); err != nil {
		t.Fatalf("pi write: %v", err)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "agents", "*")); len(entries) != 0 {
		t.Errorf("pi has no subagent system but got %v", entries)
	}
}

// TestAgentWithoutACommandsDirWritesNoCommands pins that an empty capability means
// "this agent has none", not "join the path with an empty segment".
//
// filepath.Join(dir, "", "am.md") is dir/am.md, so an unguarded write puts the slash
// commands loose in the config root — files Cursor never reads, in a directory
// the user shares with a product we did not write. The assertion is that NOTHING
// unexpected lands, rather than that one named file is absent, because the failure
// is a whole class of writes rather than one.
func TestAgentWithoutACommandsDirWritesNoCommands(t *testing.T) {
	inst, _, dir := newTestInstallerFor(t, cursorKit, false)
	if err := inst.writeAssets(); err != nil {
		t.Fatalf("write assets: %v", err)
	}
	for _, name := range commandAssets {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was written into the config root because commandsDir is empty", name)
		}
	}
	// Whatever DID land must be something the kit declares. Cursor's kit names an
	// agents dir and a rules file and nothing else.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read config dir: %v", err)
	}
	allowed := map[string]bool{"agents": true, "rules": true}
	for _, e := range entries {
		if !allowed[e.Name()] {
			t.Errorf("unexpected %q in a Cursor config dir: the kit declares no capability that "+
				"writes it, so it is a file nothing reads", e.Name())
		}
	}

	// ...and what the kit DOES declare must land. Allowing a directory without
	// requiring it is how this test passed while Cursor got no subagent
	// definition at all: writeAssets returned early for a hookless agent, before
	// the definitions were written, and nothing said so.
	if _, err := os.Stat(inst.agentDefinitionPath(agentAssets[0])); err != nil {
		t.Errorf("the kit declares agentsDir %q but installed no definition: %v — an agent "+
			"whose tool allowlist omits am_* cannot recall however it is instructed",
			cursorKit.agentsDir, err)
	}
}

// TestSandboxIsRefusedForAnAgentThatCannotRelocate pins that an install which
// cannot be honoured fails instead of reporting success.
//
// --sandbox works by pinning the agent's config-dir variable at launch. Cursor
// exposes none, so a sandbox install writes a complete, correct kit into a
// directory no Cursor will ever open — and prints the same green output as one
// that worked. Silence is the defect; the error is the feature.
func TestSandboxIsRefusedForAnAgentThatCannotRelocate(t *testing.T) {
	for _, tc := range []struct{ name, sandbox, configDir string }{
		{"--sandbox", "acme", ""},
		{"--config-dir", "", "/tmp/somewhere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := resolveInstallTarget(cursorKit, false, false, tc.sandbox, tc.configDir, "/home/u")
			if err == nil {
				t.Fatalf("%s with --agent cursor was accepted; the kit would be written where no "+
					"Cursor looks, and the install would report success", tc.name)
			}
			if !strings.Contains(err.Error(), "cursor") {
				t.Errorf("the refusal does not name the agent it applies to: %v", err)
			}
		})
	}

	// The agents that CAN relocate are unaffected.
	if _, _, _, err := resolveInstallTarget(claudeKit, false, false, "acme", "", "/home/u"); err != nil {
		t.Errorf("--sandbox was refused for claude, which relocates fine: %v", err)
	}
}

// TestCursorInstallRegistersTheMCP drives the whole install rather than the
// writer, so the switch case in registerAgentsMemoryMCP is what is under test.
//
// A writer that works and a switch that never reaches it is rung 1 without rung
// 2 — the defect this repository ships often enough to have a ladder for.
func TestCursorInstallRegistersTheMCP(t *testing.T) {
	inst, rr, dir := newTestInstallerFor(t, cursorKit, false)
	inst.mcpURL = "http://localhost:8080/mcp"
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if err != nil {
		t.Fatalf("mcp.json was not written: %v", err)
	}
	var got struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("mcp.json does not parse: %v\n%s", err, body)
	}
	entry, ok := got.MCPServers["agentsmemory"]
	if !ok {
		t.Fatalf("no agentsmemory entry under mcpServers — Cursor reads that key and nothing "+
			"else:\n%s", body)
	}
	if entry.Type != "http" || entry.URL != "http://localhost:8080/mcp" {
		t.Errorf("entry = %+v, want type http at the install's mcpURL", entry)
	}
	if entry.Headers["Authorization"] != "Bearer TESTTOK" {
		t.Errorf("the resolved token did not reach the entry: %+v", entry.Headers)
	}

	// Cursor has no CLI to drive for this, which is the whole point of the task:
	// no agent command should have been run for the registration.
	for _, c := range rr.calls {
		t.Errorf("the cursor install shelled out to %q; cursor-agent has no `mcp add` and the "+
			"registration is a file write", c.rendered())
	}

	// A registered-but-unapproved server is byte-identical on disk to a working
	// one, so the install has to say the approval step out loud.
	if out := inst.out.(*bytes.Buffer).String(); !strings.Contains(out, "cursor-agent mcp enable") {
		t.Errorf("the install never mentions the approval step, without which Cursor loads "+
			"nothing:\n%s", out)
	}
}

// TestCursorInstallWritesTheProtocolRule and TestCursorRuleIsAlwaysApplied pin the
// read half of the kit: Cursor has no CLAUDE.md/AGENTS.md, so the protocol reaches
// it as a rule file or not at all.
func TestCursorInstallWritesTheProtocolRule(t *testing.T) {
	inst, _, dir := newTestInstallerFor(t, cursorKit, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, cursorKit.rulesFile))
	if err != nil {
		t.Fatalf("the protocol rule was not written: %v", err)
	}
	// Not a stub: the rule must carry the protocol body, and the cheapest proof
	// that it does is the instruction the whole thing exists for.
	if !strings.Contains(string(body), "am_search") {
		t.Errorf("the rule does not carry the protocol — it never names am_search:\n%.400s", body)
	}
	if len(body) < 1000 {
		t.Errorf("the rule is %d bytes; the protocol is thousands, so this is a stub", len(body))
	}
}

// TestCursorRuleIsAlwaysApplied pins the one line that separates a protocol from a
// document nobody opens. Without `alwaysApply: true` Cursor loads the rule on
// demand, and "on demand" for an always-on operating protocol means never.
func TestCursorRuleIsAlwaysApplied(t *testing.T) {
	inst, _, dir := newTestInstallerFor(t, cursorKit, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, cursorKit.rulesFile))
	if err != nil {
		t.Fatalf("read rule: %v", err)
	}
	head := string(body)
	if len(head) > 400 {
		head = head[:400]
	}
	if !strings.HasPrefix(head, "---\n") {
		t.Fatalf("the rule has no front matter, so Cursor reads it as plain content:\n%s", head)
	}
	for _, want := range []string{"alwaysApply: true", "description:"} {
		if !strings.Contains(head, want) {
			t.Errorf("the rule's front matter is missing %q:\n%s", want, head)
		}
	}
}

// TestReadmeNamesEveryInstallableAgent makes the install documentation
// load-bearing, in the same shape as the hook-event gate.
//
// A kit that resolves from --agent and appears in no README is one only its
// author installs. This reads the names out of resolveAgentKits — the single
// function every --agent value goes through — so adding a fifth agent and
// forgetting the docs fails a build.
func TestReadmeNamesEveryInstallableAgent(t *testing.T) {
	root := repoRootForHooks(t)
	var agents []string
	for _, name := range []string{agentClaude, agentCodex, agentPi, agentCursor, agentClaudeDesktop} {
		kits, err := resolveAgentKits(name)
		if err != nil {
			t.Fatalf("--agent %s does not resolve: %v", name, err)
		}
		if len(kits) != 1 {
			t.Fatalf("--agent %s resolved to %d kits, want 1", name, len(kits))
		}
		agents = append(agents, name)
	}
	// `all` is the definitive list; anything it returns must be documented, and
	// this catches a kit added to `all` without its own single-name case.
	all, err := resolveAgentKits(agentAll)
	if err != nil {
		t.Fatalf("--agent all: %v", err)
	}
	for _, k := range all {
		if !contains(agents, k.name) {
			agents = append(agents, k.name)
		}
	}
	if len(agents) < 5 {
		t.Fatalf("found %d installable agents — fewer than the five that exist means the list "+
			"is wrong and this check asserts almost nothing", len(agents))
	}

	for _, rel := range []string{"README.md", filepath.Join("clients", "claude-code", "README.md")} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(body)
		for _, name := range agents {
			if !strings.Contains(text, "--agent "+name) {
				t.Errorf("%s never shows `--agent %s`, so a reader cannot tell the kit installs "+
					"for it", rel, name)
			}
		}
	}
}

// TestClaudeDesktopKitResolves pins that --agent claude-desktop reaches a kit.
func TestClaudeDesktopKitResolves(t *testing.T) {
	kits, err := resolveAgentKits(agentClaudeDesktop)
	if err != nil {
		t.Fatalf("--agent claude-desktop: %v", err)
	}
	if len(kits) != 1 || kits[0].name != agentClaudeDesktop {
		t.Fatalf("--agent claude-desktop resolved to %v, want exactly the desktop kit", names(kits))
	}
	all, err := resolveAgentKits(agentAll)
	if err != nil {
		t.Fatalf("--agent all: %v", err)
	}
	if !contains(names(all), agentClaudeDesktop) {
		t.Errorf("--agent all resolved to %v, which omits claude-desktop", names(all))
	}
	if both, _ := resolveAgentKits(agentBoth); contains(names(both), agentClaudeDesktop) {
		t.Errorf("--agent both grew: %v. both is claude+codex and must not change", names(both))
	}
}

// TestClaudeDesktopInstallRegistersTheBridge pins the registration, driven
// through run() so the switch case is under test rather than the writer.
//
// It must be a STDIO entry. Claude Desktop's config file speaks to local
// processes, and the product ships its own bridge (mcp-stdio --url) — so the
// route the project's own windows-guide recommends, `npx mcp-remote`, drags in
// Node.js for a self-hosted server that needs none.
func TestClaudeDesktopInstallRegistersTheBridge(t *testing.T) {
	inst, rr, dir := newTestInstallerFor(t, claudeDesktopKit, false)
	inst.mcpURL = "http://localhost:8080/mcp"
	inst.serverBin = fakeBuiltServerBin(t)

	// A server the user already had must survive; this file is shared with every
	// other MCP server they run.
	cfgPath := filepath.Join(dir, claudeDesktopKit.mcpConfigFile)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	prior := `{"mcpServers":{"theirs":{"command":"/usr/bin/theirs"}},"otherKey":1}`
	if err := os.WriteFile(cfgPath, []byte(prior), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var got struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Type    string   `json:"type"`
			URL     string   `json:"url"`
		} `json:"mcpServers"`
		OtherKey *int `json:"otherKey"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("config does not parse: %v\n%s", err, body)
	}
	if _, ok := got.MCPServers["theirs"]; !ok {
		t.Errorf("the user's own MCP server was lost:\n%s", body)
	}
	if got.OtherKey == nil {
		t.Errorf("an unrelated top-level key was dropped:\n%s", body)
	}
	entry, ok := got.MCPServers[mcpName]
	if !ok {
		t.Fatalf("no %s entry under mcpServers:\n%s", mcpName, body)
	}
	if entry.Type != "" || entry.URL != "" {
		t.Errorf("an HTTP entry was written (%+v); Desktop's config file spawns local processes, "+
			"so this must be the stdio bridge", entry)
	}
	// ⚠ THE PLACED PATH, NOT THE SOURCE. The installer copies the binary it
	// resolved into the kit and registers that copy, so the command Desktop
	// spawns keeps working when the build directory it came from is gone.
	if want := filepath.Join(dir, "bin", installedServerBinName); entry.Command != want {
		t.Errorf("command = %q, want the installed server binary %q", entry.Command, want)
	}
	if len(entry.Args) < 3 || entry.Args[0] != "mcp-stdio" ||
		!contains(entry.Args, "--url") || !contains(entry.Args, inst.mcpURL) {
		t.Errorf("args = %v, want mcp-stdio --url %s", entry.Args, inst.mcpURL)
	}

	// Desktop has no CLI, so nothing should have been shelled out to.
	for _, c := range rr.calls {
		t.Errorf("the desktop install ran %q; Claude Desktop has no CLI and the registration is "+
			"a file write", c.rendered())
	}
}

// TestClaudeDesktopRefusesWithoutAServerBinary pins the prerequisite the
// reference machine did not meet.
//
// The bridge is a binary on the HOST, and a Docker-only install produces none —
// `command -v agentsmemory` was empty on the machine this was built for. Writing
// a `command` that does not exist yields a client that fails at spawn with a
// message naming our binary, which reads as our bug on the user's machine.
func TestClaudeDesktopRefusesWithoutAServerBinary(t *testing.T) {
	inst, _, _ := newTestInstallerFor(t, claudeDesktopKit, false)
	inst.serverBin = "" // nothing resolvable
	err := inst.registerAgentsMemoryMCP()
	if err == nil {
		t.Fatal("the install accepted a missing server binary; it would write a command that " +
			"does not exist and Claude Desktop would fail at spawn")
	}
	// The error has to be actionable: it names the thing to build.
	if !strings.Contains(err.Error(), "mcp-stdio") && !strings.Contains(err.Error(), "server") {
		t.Errorf("the refusal does not say what is missing or how to get it: %v", err)
	}
}

// TestKitWithNoCLINeedsNoCLI pins the last absent capability, and the one that
// made the Claude Desktop install fail on its first real run.
//
// Every kit until now drives an agent CLI, so resolveKitBin demanded one on PATH.
// Claude Desktop has none — it is an application, not a command — and the install
// died with "no claude-desktop CLI found on PATH (looked for )", an error naming
// an empty binary. Same shape as ADR-020 T1's empty commandsDir: a capability
// that is ABSENT rather than different, and a step that assumed it was there.
//
// Found by running the install, not by reading it. The suite was green.
func TestKitWithNoCLINeedsNoCLI(t *testing.T) {
	bin, err := resolveKitBin(claudeDesktopKit, "", "AIAGENTMEMORY_NOSUCH_BIN")
	if err != nil {
		t.Fatalf("a kit that drives no CLI still demanded one: %v", err)
	}
	if bin != "" {
		t.Errorf("resolveKitBin returned %q for a kit with no CLI; nothing should be spawned", bin)
	}

	// The kits that DO drive a CLI must still fail loudly when it is missing —
	// this guard must not become a blanket excuse.
	missing := agentKit{name: "phantom", bin: "definitely-not-on-path-xyz"}
	if _, err := resolveKitBin(missing, "", "AIAGENTMEMORY_NOSUCH_BIN"); err == nil {
		t.Error("a kit that names a CLI was allowed to proceed without it")
	}
}
