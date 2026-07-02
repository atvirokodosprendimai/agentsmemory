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

const (
	// bootstrapAsset is the embedded always-on protocol; bootstrapFile is the name
	// it is installed under in the target config dir; memoryImportLine is the line
	// merged into CLAUDE.md to pull it in. Claude Code resolves an @import relative
	// to the importing file, so the import names a sibling of CLAUDE.md.
	bootstrapAsset   = "bootstrap.md"
	bootstrapFile    = "agentsmemory-bootstrap.md"
	memoryImportLine = "@agentsmemory-bootstrap.md"
)

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
	fmt.Fprintf(d.out, "  would run: %s%s %s\n", prefix.String(), name, strings.Join(redactArgs(args), " "))
	return nil
}

// redactArgs masks secret-bearing argument values so --dry-run never echoes a
// token to the terminal or a captured log. The Authorization bearer header is
// the only secret the installer passes on a command line.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "Authorization: Bearer ") {
			out[i] = "Authorization: Bearer ***"
		} else {
			out[i] = a
		}
	}
	return out
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
	targetDir      string        // Claude config dir to install into (~/.claude or a sandbox)
	sandboxName    string        // non-empty in isolated mode; drives messaging + run hint
	explicitTarget bool          // true when --sandbox/--claude-dir pinned the target ⇒ skip the mode prompt
	claudeBin      string        // resolved Claude CLI name to drive for mcp/plugin ops
	mcpURL         string        // agentsmemory remote MCP endpoint
	scope          string        // Claude MCP/plugin scope (user|local|project)
	token          string        // agentsmemory workspace token (empty ⇒ prompt or skip)
	recommended    bool          // also install codebase-memory + eidos + codex
	yes            bool          // non-interactive: never prompt
	dryRun         bool          // print instead of doing
	out            io.Writer     // progress + banners
	in             io.Reader     // interactive prompt source (mode + token)
	reader         *bufio.Reader // shared line reader over in; lazily built so both prompts read one stream
	runner         commandRunner // how external commands execute (exec / dry / fake)
}

// resolveInstallTarget picks the install target from the mode flags and reports
// whether it was pinned on the command line. Precedence is --sandbox, then
// --claude-dir, then an explicit --global, then the bare default (global
// ~/.claude). explicit is true whenever the user named the target on the command
// line; when it is false, run() offers the interactive mode prompt so a bare
// `curl|bash` install isn't silently forced global.
//
// --global is the flag form of the global choice: it pins ~/.claude and marks the
// target explicit, so `install --global --token <t>` is fully non-interactive.
// Because --global names the same target the bare default and the prompt would,
// combining it with --sandbox or --claude-dir is ambiguous and rejected rather
// than silently resolved. home is passed in (not read here) so the helper is pure
// and testable.
func resolveInstallTarget(global bool, sandbox, claudeDir, home string) (targetDir, sandboxName string, explicit bool, err error) {
	if global && (sandbox != "" || claudeDir != "") {
		return "", "", false, fmt.Errorf("--global cannot be combined with --sandbox or --claude-dir")
	}
	switch {
	case sandbox != "":
		if err := validSandboxName(sandbox); err != nil {
			return "", "", false, err
		}
		return sandboxDir(sandbox), sandbox, true, nil
	case claudeDir != "":
		return claudeDir, "", true, nil
	case global:
		return filepath.Join(home, ".claude"), "", true, nil
	default:
		return filepath.Join(home, ".claude"), "", false, nil
	}
}

// newInstaller builds an Installer from parsed CLI flags. It resolves the target
// config dir (isolated sandbox vs global ~/.claude) and the Claude CLI to drive,
// selecting a dry-run runner when --dry-run is set.
func newInstaller(c *cli.Command, out io.Writer, in io.Reader) (*Installer, error) {
	// Resolve the install target (and whether it was pinned on the command line)
	// from the mode flags. Kept as a pure helper so the precedence and the
	// mutually-exclusive-flags rule are testable without CLI plumbing.
	targetDir, sandboxName, explicitTarget, err := resolveInstallTarget(
		c.Bool("global"), c.String("sandbox"), c.String("claude-dir"), homeDir())
	if err != nil {
		return nil, err
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
		targetDir:      targetDir,
		sandboxName:    sandboxName,
		explicitTarget: explicitTarget,
		claudeBin:      claudeBin,
		mcpURL:         c.String("mcp-url"),
		scope:          c.String("scope"),
		token:          c.String("token"),
		recommended:    c.Bool("recommended"),
		yes:            c.Bool("yes"),
		dryRun:         dryRun,
		out:            out,
		in:             in,
		runner:         runner,
	}, nil
}

// run executes the full install: assets + hook (core), our MCP (core), and the
// recommended extensions (opt-in). Core failures are fatal; the MCP and the
// extension steps are best-effort so a partial environment still leaves the
// useful pieces installed.
func (i *Installer) run() error {
	// Ask global-vs-sandbox before anything is written, so the banner and every
	// subsequent step reflect the chosen target. No-op unless we're interactive.
	i.promptInstallMode()
	i.banner()

	i.step("1/4  commands, memory protocol, Stop hook")
	if err := i.writeAssets(); err != nil {
		return fmt.Errorf("writing kit assets: %w", err)
	}
	if err := i.registerStopHook(); err != nil {
		return fmt.Errorf("registering Stop hook: %w", err)
	}
	if err := i.registerMemoryBootstrap(); err != nil {
		return fmt.Errorf("installing memory bootstrap: %w", err)
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

// registerMemoryBootstrap installs the always-on operating protocol so the
// memory-first workflow applies every session without the user typing /am. It
// writes our owned copy of the embedded protocol as agentsmemory-bootstrap.md and
// merges a single managed @import line into CLAUDE.md. Claude Code loads
// $CLAUDE_CONFIG_DIR/CLAUDE.md as user memory, so this applies both in a sandbox
// (where we own the whole config dir) and in the global ~/.claude (where the merge
// preserves the user's existing CLAUDE.md and only adds the import line).
func (i *Installer) registerMemoryBootstrap() error {
	data, err := assets.ReadFile(bootstrapAsset)
	if err != nil {
		return err // embed guarantees presence; an error here is a build bug
	}
	bootstrapPath := filepath.Join(i.targetDir, bootstrapFile)
	if err := i.writeFile(bootstrapPath, data, 0o644); err != nil {
		return err
	}
	i.ok("memory protocol %s", bootstrapFile)

	// The @import lands in the user's memory file, so it goes through the managed
	// idempotent merge (not a blind overwrite). Under dry-run, print the intent —
	// mirroring registerStopHook, which also can't preview through the merge.
	claudeMd := filepath.Join(i.targetDir, "CLAUDE.md")
	if i.dryRun {
		fmt.Fprintf(i.out, "  would import %q into %s (managed block)\n", memoryImportLine, claudeMd)
		return nil
	}
	changed, err := ensureMemoryImport(claudeMd, memoryImportLine)
	if err != nil {
		return err
	}
	if changed {
		i.ok("imported memory protocol into CLAUDE.md")
	} else {
		i.ok("CLAUDE.md already imports the memory protocol")
	}
	return nil
}

// installRecommended installs the companion ecosystem: the codebase-memory MCP
// (its own installer + registration) and the eidos and codex plugins. Each step
// is best-effort — one already-installed plugin or a network hiccup should not
// abort the whole install — so failures are reported, not fatal.
func (i *Installer) installRecommended() {
	// Register the stdio MCP only if its binary actually landed: if the upstream
	// installer failed, pointing the Claude CLI at a missing path would register
	// a broken server. (--dry-run still shows the full plan.)
	shellErr := i.runner.runShell(codebaseMemoryInstall)
	if shellErr != nil {
		i.warn("codebase-memory install script failed: %v", shellErr)
	} else {
		i.ok("installed codebase-memory-mcp")
	}
	bin := expandTilde(codebaseMemoryBin)
	if shellErr == nil || i.dryRun {
		i.claude(true, "mcp", "remove", "--scope", i.scope, codebaseMemoryName)
		if err := i.claude(false, "mcp", "add", "--transport", "stdio", "--scope", i.scope, codebaseMemoryName, "--", bin); err != nil {
			i.warn("register codebasememory MCP failed: %v", err)
		} else {
			i.ok("registered MCP %q → %s", codebaseMemoryName, bin)
		}
	} else {
		i.warn("skipping codebasememory MCP registration — installer did not complete")
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

// promptInstallMode asks, interactively, whether to install globally or into an
// isolated sandbox — the choice a bare `curl|bash` user otherwise never gets,
// since the mode is only selectable via the --sandbox flag and thus defaults to
// global silently. It runs only when no target was pinned on the command line and
// we can actually interact (not --yes, not --dry-run). On blank input or EOF it
// leaves the global default in place; a typed, valid name switches the install to
// that sandbox. It never fails the install: an unreadable stdin just falls back
// to global, which is the safe, documented default.
func (i *Installer) promptInstallMode() {
	// Respect an explicit choice and every non-interactive path. install.sh adds
	// --yes when there is no /dev/tty (CI), so this correctly stays silent there.
	if i.explicitTarget || i.yes || i.dryRun {
		return
	}
	fmt.Fprintln(i.out, "Where should the kit be installed?")
	fmt.Fprintln(i.out, "  - press Enter for a GLOBAL install into ~/.claude (wraps your existing Claude)")
	fmt.Fprintln(i.out, "  - or type a NAME for an isolated sandbox at ~/.sandboxes/<name>")
	for {
		fmt.Fprint(i.out, "Sandbox name (blank = global): ")
		line, err := i.line()
		name := strings.TrimSpace(line)
		if name == "" {
			// Blank line, or EOF on a piped/closed stdin → keep global default.
			return
		}
		if verr := validSandboxName(name); verr != nil {
			fmt.Fprintf(i.out, "  %v\n", verr)
			if err != nil {
				// Reader is exhausted (EOF); don't spin forever re-prompting a
				// closed stdin — fall back to the global default.
				return
			}
			continue // re-prompt on a live terminal
		}
		i.sandboxName = name
		i.targetDir = sandboxDir(name)
		return
	}
}

// line reads one line from the shared prompt reader, building it from i.in on
// first use. A single *bufio.Reader is essential: two separate bufio readers over
// the same terminal fd would let the first buffer-read swallow bytes meant for
// the second, so the mode prompt and the token prompt must share this one.
func (i *Installer) line() (string, error) {
	if i.reader == nil {
		i.reader = bufio.NewReader(i.in)
	}
	return i.reader.ReadString('\n')
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
	line, err := i.line()
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
	fmt.Fprintln(i.out, "  - the memory protocol auto-loads every session via CLAUDE.md — no need to type /am")
	fmt.Fprintln(i.out, "  - run /M or /am with a task to run the full grounding sequence on demand")
}
