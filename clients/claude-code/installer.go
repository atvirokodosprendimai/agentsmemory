package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

// hookAsset is the embedded Stop-hook path (relative to the binary's embed FS)
// and also the relative install path under the target config dir.
const hookAsset = "hooks/agentsmemory-stop-hook.sh"

// commandRunner executes external commands on behalf of the installer. It is an
// interface so tests can record calls and --dry-run can print them without ever
// shelling out. Kept tiny on purpose (accept interfaces) so the whole install
// flow is exercisable end to end in a unit test.
type commandRunner interface {
	// run executes program name with args. env holds extra KEY=VALUE entries
	// appended to the current environment (used to pin CLAUDE_CONFIG_DIR).
	run(name string, args, env []string) error
	// runShell executes a shell pipeline — needed for the codebase-memory
	// `curl … | bash` one-liner, which has no argv form.
	runShell(script string) error
}

// execRunner is the production commandRunner: it runs commands for real and
// streams their output to the installer's writer.
type execRunner struct{ out io.Writer }

func (e execRunner) run(name string, args, env []string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = e.out, e.out
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.Run()
}

func (e execRunner) runShell(script string) error {
	// bash -c so the pipe (curl | bash) is interpreted; the upstream installer
	// is distributed exactly this way, so we run it exactly as documented.
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdout, cmd.Stderr = e.out, e.out
	return cmd.Run()
}

// dryRunner prints the commands that would run and does nothing else. It backs
// --dry-run, letting a user preview the exact install plan (including the
// external Claude CLI calls) before committing to it.
type dryRunner struct{ out io.Writer }

func (d dryRunner) run(name string, args, env []string) error {
	var prefix strings.Builder
	for _, e := range env {
		prefix.WriteString(e)
		prefix.WriteByte(' ')
	}
	fmt.Fprintf(d.out, "  would run: %s%s %s\n", prefix.String(), name, strings.Join(args, " "))
	return nil
}

func (d dryRunner) runShell(script string) error {
	fmt.Fprintf(d.out, "  would run: %s\n", script)
	return nil
}

// Installer performs a single `install` invocation: it writes the embedded
// command + hook assets into a target Claude config dir, registers the Stop
// hook, wires up the agentsmemory MCP, and (with recommended=true) installs the
// companion extensions.
type Installer struct {
	targetDir   string        // Claude config dir to install into (~/.claude or a sandbox)
	sandboxName string        // non-empty in isolated mode; drives messaging + run hint
	claudeBin   string        // resolved Claude CLI name to drive for mcp/plugin ops
	mcpURL      string        // agentsmemory remote MCP endpoint
	scope       string        // Claude MCP/plugin scope (user|local|project)
	token       string        // agentsmemory workspace token (empty ⇒ prompt or skip)
	recommended bool          // also install codebase-memory + eidos + codex
	yes         bool          // non-interactive: never prompt
	dryRun      bool          // print instead of doing
	out         io.Writer     // progress + banners
	in          io.Reader     // interactive token prompt source
	runner      commandRunner // how external commands execute (exec / dry / fake)
}

// newInstaller builds an Installer from parsed CLI flags. It resolves the target
// config dir (isolated sandbox vs global ~/.claude) and the Claude CLI to drive,
// selecting a dry-run runner when --dry-run is set.
func newInstaller(c *cli.Command, out io.Writer, in io.Reader) (*Installer, error) {
	// Target config dir: an explicit --sandbox wins, then --claude-dir, then the
	// global ~/.claude default.
	targetDir := ""
	sandboxName := ""
	if name := c.String("sandbox"); name != "" {
		if err := validSandboxName(name); err != nil {
			return nil, err
		}
		sandboxName = name
		targetDir = sandboxDir(name)
	} else if dir := c.String("claude-dir"); dir != "" {
		targetDir = dir
	} else {
		targetDir = filepath.Join(homeDir(), ".claude")
	}

	dryRun := c.Bool("dry-run")

	// We always register our MCP, which needs the Claude CLI, so resolve it now.
	// Under --dry-run tolerate a missing CLI so the plan can still be printed.
	claudeBin, err := resolveClaudeBin(c.String("claude-bin"))
	if err != nil {
		if !dryRun {
			return nil, err
		}
		claudeBin = "claude"
	}

	var runner commandRunner = execRunner{out: out}
	if dryRun {
		runner = dryRunner{out: out}
	}

	return &Installer{
		targetDir:   targetDir,
		sandboxName: sandboxName,
		claudeBin:   claudeBin,
		mcpURL:      c.String("mcp-url"),
		scope:       c.String("scope"),
		token:       c.String("token"),
		recommended: c.Bool("recommended"),
		yes:         c.Bool("yes"),
		dryRun:      dryRun,
		out:         out,
		in:          in,
		runner:      runner,
	}, nil
}

// run executes the full install: assets + hook (core), our MCP (core), and the
// recommended extensions (opt-in). Core failures are fatal; the MCP and the
// extension steps are best-effort so a partial environment still leaves the
// useful pieces installed.
func (i *Installer) run() error {
	i.banner()

	i.step("1/4  slash commands + Stop hook")
	if err := i.writeAssets(); err != nil {
		return fmt.Errorf("writing kit assets: %w", err)
	}
	if err := i.registerStopHook(); err != nil {
		return fmt.Errorf("registering Stop hook: %w", err)
	}

	i.step("2/4  agentsmemory MCP")
	if err := i.registerAgentsMemoryMCP(); err != nil {
		// Non-fatal: the commands + hook are installed and useful on their own.
		i.warn("agentsmemory MCP not registered: %v", err)
	}

	i.step("3/4  recommended extensions")
	if i.recommended {
		i.installRecommended()
	} else {
		fmt.Fprintln(i.out, "  skipped (pass --recommended to add codebase-memory, eidos, codex)")
	}

	i.step("4/4  done")
	i.summary()
	return nil
}

// writeAssets writes the embedded slash commands and the Stop hook into the
// target config dir. am.md and M.md are the two bootstrap commands; the legacy
// agentsmemory.md was retired and is intentionally not shipped.
func (i *Installer) writeAssets() error {
	for _, name := range []string{"M.md", "am.md"} {
		data, err := assets.ReadFile("commands/" + name)
		if err != nil {
			return err // embed guarantees presence; an error here is a build bug
		}
		if err := i.writeFile(filepath.Join(i.targetDir, "commands", name), data, 0o644); err != nil {
			return err
		}
		i.ok("command /%s", strings.TrimSuffix(name, ".md"))
	}

	hook, err := assets.ReadFile(hookAsset)
	if err != nil {
		return err
	}
	if err := i.writeFile(i.hookPath(), hook, 0o755); err != nil {
		return err
	}
	i.ok("hook %s", filepath.Base(i.hookPath()))
	return nil
}

// hookPath is the absolute install path of the Stop hook under the target dir.
func (i *Installer) hookPath() string { return filepath.Join(i.targetDir, hookAsset) }

// writeFile writes data to path with perm, creating parent dirs. Under dry-run
// it prints the intended write instead of touching the filesystem.
func (i *Installer) writeFile(path string, data []byte, perm os.FileMode) error {
	if i.dryRun {
		fmt.Fprintf(i.out, "  would write: %s (%d bytes, %#o)\n", path, len(data), perm)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// registerStopHook adds the Stop hook to the target settings.json, idempotently.
func (i *Installer) registerStopHook() error {
	hookCmd := "bash " + i.hookPath()
	settings := filepath.Join(i.targetDir, "settings.json")
	if i.dryRun {
		fmt.Fprintf(i.out, "  would register Stop hook in %s: %q\n", settings, hookCmd)
		return nil
	}
	added, err := ensureStopHook(settings, hookCmd)
	if err != nil {
		return err
	}
	if added {
		i.ok("registered Stop hook in settings.json")
	} else {
		i.ok("Stop hook already registered")
	}
	return nil
}

// registerAgentsMemoryMCP wires up the agentsmemory remote MCP. It resolves the
// workspace token (flag/env, else an interactive prompt) and registers the HTTP
// server with the Claude CLI in a single shot. This is the product's core value,
// so it runs in the default install — not gated behind --recommended.
func (i *Installer) registerAgentsMemoryMCP() error {
	token := i.resolveToken()
	if token == "" {
		fmt.Fprintln(i.out, "  no token provided — skipping agentsmemory MCP.")
		fmt.Fprintf(i.out, "  add it later: %s mcp add --transport http %s %s --header \"Authorization: Bearer <token>\"\n",
			i.claudeBin, mcpName, i.mcpURL)
		return nil
	}
	header := "Authorization: Bearer " + token
	// `mcp add` is not idempotent by name, so remove any prior entry first
	// (ignoring "not found") and then add cleanly, all in one shot.
	i.claude(true, "mcp", "remove", "--scope", i.scope, mcpName)
	if err := i.claude(false, "mcp", "add", "--transport", "http", "--scope", i.scope, mcpName, i.mcpURL, "--header", header); err != nil {
		return err
	}
	i.ok("registered MCP %q → %s", mcpName, i.mcpURL)
	return nil
}

// installRecommended installs the companion ecosystem: the codebase-memory MCP
// (its own installer + registration) and the eidos and codex plugins. Each step
// is best-effort — one already-installed plugin or a network hiccup should not
// abort the whole install — so failures are reported, not fatal.
func (i *Installer) installRecommended() {
	if err := i.runner.runShell(codebaseMemoryInstall); err != nil {
		i.warn("codebase-memory install script failed: %v", err)
	} else {
		i.ok("installed codebase-memory-mcp")
	}
	bin := expandTilde(codebaseMemoryBin)
	i.claude(true, "mcp", "remove", "--scope", i.scope, codebaseMemoryName)
	if err := i.claude(false, "mcp", "add", "--transport", "stdio", "--scope", i.scope, codebaseMemoryName, "--", bin); err != nil {
		i.warn("register codebasememory MCP failed: %v", err)
	} else {
		i.ok("registered MCP %q → %s", codebaseMemoryName, bin)
	}

	// Marketplace add is effectively idempotent; ignore its error and let the
	// install surface any real problem.
	for _, p := range []struct{ marketplace, plugin string }{
		{"agenticnotetaking/eidos", "eidos@eidos"},
		{"openai/codex-plugin-cc", "codex@openai-codex"},
	} {
		i.claude(true, "plugin", "marketplace", "add", p.marketplace)
		if err := i.claude(false, "plugin", "install", p.plugin); err != nil {
			i.warn("install plugin %s failed: %v", p.plugin, err)
		} else {
			i.ok("installed plugin %s", p.plugin)
		}
	}
}

// claude runs the resolved Claude CLI with CLAUDE_CONFIG_DIR pinned to the target
// config dir, so MCP/plugin registration lands in the config we are installing
// into (a sandbox or the global dir) rather than wherever the process happens to
// point. When ignoreErr is true a failure is swallowed — used for the pre-emptive
// `mcp remove` and `marketplace add` that legitimately fail when nothing exists.
func (i *Installer) claude(ignoreErr bool, args ...string) error {
	env := []string{"CLAUDE_CONFIG_DIR=" + i.targetDir}
	if err := i.runner.run(i.claudeBin, args, env); err != nil && !ignoreErr {
		return err
	}
	return nil
}

// resolveToken returns the agentsmemory token from --token/env, or prompts for
// it interactively. Under --dry-run it returns a visible placeholder so the plan
// prints the full `mcp add`. In --yes / non-interactive mode (or on an empty
// stdin) it returns "" and the caller skips MCP registration with a hint.
func (i *Installer) resolveToken() string {
	if i.token != "" {
		return i.token
	}
	if i.dryRun {
		return "<token>"
	}
	if i.yes {
		return ""
	}
	fmt.Fprint(i.out, "  Enter your agentsmemory workspace API token (blank to skip): ")
	line, err := bufio.NewReader(i.in).ReadString('\n')
	if err != nil && line == "" {
		return "" // EOF (piped / non-interactive stdin) → skip
	}
	return strings.TrimSpace(line)
}

// --- terminal UX helpers -------------------------------------------------
//
// Output is intentionally plain ASCII (no ANSI): it stays readable when piped
// to a log or captured in a test, and the curl|bash installer often runs with
// stdout redirected.

// banner prints the header block describing the install target and mode.
func (i *Installer) banner() {
	fmt.Fprintln(i.out, "== agentsmemory installer ==")
	fmt.Fprintf(i.out, "mode        : %s\n", i.modeLabel())
	fmt.Fprintf(i.out, "config dir  : %s\n", i.targetDir)
	fmt.Fprintf(i.out, "claude CLI  : %s\n", i.claudeBin)
	fmt.Fprintf(i.out, "extensions  : %s\n", extensionsLabel(i.recommended))
	if i.dryRun {
		fmt.Fprintln(i.out, "dry-run     : no files written, no commands run")
	}
}

// modeLabel names the install mode for the banner.
func (i *Installer) modeLabel() string {
	if i.sandboxName != "" {
		return "isolated sandbox " + i.sandboxName
	}
	return "global (wrap your existing Claude)"
}

// extensionsLabel describes whether the recommended extensions are included.
func extensionsLabel(recommended bool) string {
	if recommended {
		return "core + recommended (codebase-memory, eidos, codex)"
	}
	return "core only"
}

func (i *Installer) step(title string)       { fmt.Fprintf(i.out, "\n> %s\n", title) }
func (i *Installer) ok(f string, a ...any)   { fmt.Fprintf(i.out, "  [ok] "+f+"\n", a...) }
func (i *Installer) warn(f string, a ...any) { fmt.Fprintf(i.out, "  [!!] "+f+"\n", a...) }

// summary prints the closing next-steps block, tailored to the install mode.
func (i *Installer) summary() {
	fmt.Fprintln(i.out)
	fmt.Fprintln(i.out, "Next steps:")
	if i.sandboxName != "" {
		fmt.Fprintf(i.out, "  - launch Claude in this sandbox:  aiagentmemory run %s\n", i.sandboxName)
	} else {
		fmt.Fprintln(i.out, "  - restart Claude Code (or /reload) to pick up the new commands + hook")
	}
	fmt.Fprintln(i.out, "  - run /M or /am in a project to start a memory-grounded session")
}
