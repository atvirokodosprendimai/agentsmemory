package main

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/urfave/cli/v3"
)

// hookAsset is the embedded Stop-hook path inside the binary's embed FS.
const hookAsset = "hooks/agentsmemory-stop-hook.sh"

// verifyHookAsset is the embedded SessionStart hook: it verifies this project's
// code anchors before the session starts using its memories.
const verifyHookAsset = "hooks/agentsmemory-verify-hook.sh"

// sessionEndHookAsset is the embedded SessionEnd hook: the closing recall report.
const sessionEndHookAsset = "hooks/agentsmemory-session-end-hook.sh"

// statsHelperAsset is the /stats fetch Stop and SessionEnd source. It is not
// registered as a hook; both scripts `.` it from the same directory the
// installer writes them into.
const statsHelperAsset = "hooks/agentsmemory-stats.sh"

// subagentHookAsset is the embedded SubagentStart hook: it puts the recall
// instruction NEXT TO the subagent's task.
//
// It exists because ADR-017 T1 measured 5/5 subagents recalling with it and 0/5
// without — on a control arm that already carried the whole protocol, the
// bootstrap and this repo's hard gate, verbatim. Placement, not instruction, was
// the gap, and a mechanism that decisive should not need hand-registration.
const subagentHookAsset = "hooks/agentsmemory-subagent-start-hook.sh"

// recallHookAsset is the embedded recall hook: it PERFORMS a recall and injects
// the result, so a fresh context does not start blind (ADR-041 T4).
//
// ADR-017 named this mechanism in 2026-08 and left it unbuilt pending measurement
// — "a subagent cannot skip a recall that already happened". The measurement is
// ADR-041 T2's baseline; this is the mechanism it was waiting on.
const recallHookAsset = "hooks/agentsmemory-recall-hook.sh"

// taskRecallHookAsset is the UserPromptSubmit sibling of the recall hook: it asks
// the palace about the TASK, using the user's own words, at the moment the task
// arrives.
const taskRecallHookAsset = "hooks/agentsmemory-task-recall-hook.sh"

const (
	// hookFile is where the Stop hook is installed: flat in the config dir, not
	// under hooks/. The directory name matters because a sandbox is shared — pi
	// treats any hooks/ directory as its own deprecated layout and halts the
	// launch on a "press any key to continue" deprecation notice, even though the
	// directory is ours and has nothing to do with pi. Claude and codex register
	// the hook by absolute path, so where it lives is ours to choose.
	hookFile = "agentsmemory-stop-hook.sh"

	// verifyHookFile is where the SessionStart hook lands, beside the Stop hook
	// and for the same reason: flat in the config dir, so the registered command
	// is a stable path a user can read in settings.json.
	verifyHookFile = "agentsmemory-verify-hook.sh"

	// subagentHookFile is where the SubagentStart hook lands, beside the others.
	subagentHookFile = "agentsmemory-subagent-start-hook.sh"

	// sessionEndHookFile is where the SessionEnd hook lands.
	sessionEndHookFile = "agentsmemory-session-end-hook.sh"

	// recallHookFile is where the recall hook lands, beside the others.
	recallHookFile = "agentsmemory-recall-hook.sh"

	// taskRecallHookFile is where the per-prompt recall hook lands.
	taskRecallHookFile = "agentsmemory-task-recall-hook.sh"

	// statsHelperFile is the sourced /stats helper, beside the hook scripts.
	statsHelperFile = "agentsmemory-stats.sh"

	// legacyHookRel is where installs before that change put the hook. It is
	// removed on the next install (along with its now-stale Stop entry) so the
	// pi warning stops firing on sandboxes created earlier.
	legacyHookRel = "hooks/agentsmemory-stop-hook.sh"
)

// piExtensionAsset is the embedded pi bridge extension, installed at the same
// relative path under the target config dir — pi auto-discovers any *.ts under
// <config dir>/extensions. It is pi's stand-in for both the MCP registration and
// the Stop hook, neither of which pi supports natively.
const piExtensionAsset = "extensions/agentsmemory.ts"

const (
	// bootstrapAsset is the embedded always-on protocol; bootstrapFile is the name
	// it is installed under in the target config dir; memoryImportLine is the line
	// merged into CLAUDE.md to pull it in. Claude Code resolves an @import relative
	// to the importing file, so the import names a sibling of CLAUDE.md.
	bootstrapAsset   = "bootstrap.md"
	bootstrapFile    = "agentsmemory-bootstrap.md"
	memoryImportLine = "@agentsmemory-bootstrap.md"
)

const (
	// tokenEnvVar is the environment variable an agent reads the workspace bearer
	// token from. Two agents need it: unlike `claude mcp add`, `codex mcp add` has
	// no static-header flag, so an HTTP MCP server is authed with
	// `bearer_token_env_var` — codex stores the variable NAME and reads the value
	// from its own environment at launch — and pi has no MCP client at all, so our
	// bridge extension reads the same variable.
	tokenEnvVar = mcpprotocol.TokenEnvVar

	// mcpURLEnvVar tells the pi bridge extension which endpoint to talk to. Only
	// pi needs it: Claude and codex store the URL in their own MCP config, but
	// the extension has no config of its own to read.
	mcpURLEnvVar = mcpprotocol.MCPURLEnvVar

	// localEnvVar tells the pi bridge extension that the endpoint is a self-hosted
	// `agentsmemory --local` server, so a missing token means "this server wants
	// none" rather than "the user skipped it". The extension needs the difference:
	// without a token it must stay silent against the hosted service (where it
	// would only 401), but connect anyway against a local one.
	localEnvVar = mcpprotocol.LocalEnvVar

	// socketEnvVar is the Unix socket serve and the installer share.
	socketEnvVar = mcpprotocol.SocketEnvVar

	// localTokenEnvVar is the variable a self-hosted `agentsmemory --local --token`
	// server reads its required token from. The installer reads the same one (it is
	// the first source behind --token), so exporting it once configures both the
	// server that demands the credential and the agent that presents it.
	localTokenEnvVar = mcpprotocol.LocalTokenEnvVar

	// wingHeader names the project a registration files into. The installer and
	// server compile against one wire constant, even though they ship in separate
	// binaries.
	wingHeader = mcpprotocol.WingHeader

	// tokenFile is where we persist that token (0600) inside the agent's config
	// dir, so `aiagentmemory run` can export it without the user wiring up a shell
	// rc. Kept beside the config it belongs to, so deleting a sandbox deletes its
	// token with it.
	tokenFile = "agentsmemory.env"
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
	kit            agentKit // which agent CLI we are installing for (claude|codex|pi)
	goos           string   // OS to plan for; empty means this machine (see platform)
	targetDir      string   // agent config dir to install into (~/.claude, ~/.codex, ~/.pi/agent or a sandbox)
	sandboxName    string   // non-empty in isolated mode; drives messaging + run hint
	explicitTarget bool     // true when --sandbox/--config-dir pinned the target ⇒ skip the mode prompt
	agentBin       string   // resolved agent CLI name to drive for mcp/plugin ops
	mcpURL         string   // agentsmemory remote MCP endpoint
	socket         string   // non-empty ⇒ reach a --local server over this Unix socket, via the mcp-stdio bridge
	// serverBin is the agentsmemory server binary the stdio bridge is spawned from.
	// It is the SOURCE for placeServerBin, never the path that gets registered:
	// every registration names the copy placed under targetDir/bin instead. (This
	// comment said "socket mode only" until Claude Desktop began using it too, and
	// went on saying it for the commit that made the change.)
	serverBin string
	scope     string // Claude MCP/plugin scope (user|local|project)
	local     bool   // target a self-hosted `agentsmemory --local` server
	// wing is the project this registration files memories into. It travels as a
	// header on every MCP call, so writes from THIS project land in THIS project's
	// wing whether or not the agent remembers to pass one. Empty keeps the old
	// behaviour: the agent names a wing per call.
	wing  string
	token string // agentsmemory workspace token (empty ⇒ prompt or skip)
	// resolvedToken is what registration actually wrote, decided once by
	// resolveToken. summary() reads it rather than inferring from local, because
	// a self-hosted server may or may not require a token (server --token) and the
	// follow-up steps differ: a token that was persisted has to be exported.
	resolvedToken string
	copyGlobal    bool          // seed the target from the agent's global config dir
	sharedAuth    bool          // link credentials back to the global config dir
	recommended   bool          // also install codebase-memory + codex review
	yes           bool          // non-interactive: never prompt
	dryRun        bool          // print instead of doing
	out           io.Writer     // progress + banners
	in            io.Reader     // interactive prompt source (mode + token)
	reader        *bufio.Reader // shared line reader over in; lazily built so both prompts read one stream
	runner        commandRunner // how external commands execute (exec / dry / fake)

	// src is where assets are read from. Leave it nil for the embedded kit —
	// `update-skill` sets it to a source that fetches from GitHub instead.
	src assetSource
}

// source returns where this install reads its assets from, defaulting to the
// embedded kit. Keeping the zero value useful means an Installer built as a
// struct literal — which every test and `update-skill` does — needs no extra
// wiring to behave exactly as it did before a remote source existed.
func (i *Installer) source() assetSource {
	if i.src != nil {
		return i.src
	}
	return assets
}

// resolveInstallTarget picks the install target from the mode flags and reports
// whether it was pinned on the command line. Precedence is --sandbox, then
// --config-dir, then an explicit --global, then the bare default (the kit's
// global dir: ~/.claude for Claude, ~/.codex for codex). explicit is true whenever
// the user named the target on the command line; when it is false, run() offers
// the interactive mode prompt so a bare `curl|bash` install isn't silently forced
// global.
//
// --global is the flag form of the global choice: it pins the kit's global dir and
// marks the target explicit, so `install --global --token <t>` is fully
// non-interactive. Because --global names the same target the bare default and the
// prompt would, combining it with --sandbox or --config-dir is ambiguous and
// rejected rather than silently resolved. home is passed in (not read here) so the
// helper is pure and testable.
//
// A sandbox holds both agents' configs in one directory. The two kits share
// config.toml only with Codex itself; Claude writes settings.json, commands/ and
// CLAUDE.md while Codex writes prompts/ and AGENTS.md, so `install --agent both
// --sandbox x` yields one dir that CLAUDE_CONFIG_DIR and CODEX_HOME can each use.
// --local implies the global target too, but as a DEFAULT rather than an
// assertion: someone self-hosting is setting up their own machine, so stopping to
// ask global-vs-sandbox is a prompt with an obvious answer. It therefore behaves
// like --global when no target is named, and yields to --sandbox/--config-dir
// when one is — which is why it is not part of the mutual-exclusion check below,
// and why "--local --sandbox x" is a legitimate combination (a local server, an
// isolated config) rather than an error.
func resolveInstallTarget(kit agentKit, global, local bool, sandbox, configDir, home string) (targetDir, sandboxName string, explicit bool, err error) {
	if global && (sandbox != "" || configDir != "") {
		return "", "", false, fmt.Errorf("--global cannot be combined with --sandbox or --config-dir")
	}
	// An isolated install works by pinning the agent's config-dir variable at
	// launch. An agent that exposes none cannot be pointed anywhere, so the kit
	// would be written complete and correct into a directory it will never open —
	// and the install would print the same green output as one that worked.
	// Refuse: an install that cannot be honoured must not report success.
	if kit.configEnv == "" && (sandbox != "" || configDir != "") {
		return "", "", false, fmt.Errorf("--agent %s cannot use --sandbox or --config-dir: %s "+
			"exposes no variable that relocates its config dir, so the kit would be installed "+
			"where it will never be read. Install globally instead", kit.name, kit.name)
	}
	switch {
	case sandbox != "":
		if err := validSandboxName(sandbox); err != nil {
			return "", "", false, err
		}
		return sandboxDir(sandbox), sandbox, true, nil
	case configDir != "":
		absolute, err := filepath.Abs(configDir)
		if err != nil {
			return "", "", false, fmt.Errorf("resolve --config-dir %q: %w", configDir, err)
		}
		return absolute, "", true, nil
	case global, local:
		return kit.globalConfigDir(home), "", true, nil
	default:
		return kit.globalConfigDir(home), "", false, nil
	}
}

// serverBinCandidates are the names the agentsmemory server binary is commonly
// installed under, tried in order when --server-bin is not given: the release
// asset's own name first, then the shorter name the README's download snippet
// saves it as.
var serverBinCandidates = []string{"aiagentmemory-server", "agentsmemory"}

// serverBinLookupCandidates are the names resolveServerBin actually tries.
//
// ⚠ exec.LookPath's PATHEXT HELP ONLY REACHES A BARE NAME. On Windows a name
// searched on PATH resolves aiagentmemory-server.exe on its own, but the same
// string used as an explicit path does not — and --server-bin is exactly that
// case. Trying the .exe spelling first costs one stat and removes the asymmetry.
func serverBinLookupCandidates(flagValue string) []string {
	return serverBinLookupCandidatesOn(runtime.GOOS, flagValue)
}

// serverBinLookupCandidatesOn takes the platform as an argument so the ORDER is
// checkable from any host — the same reason agentKit.globalConfigDirOn does.
func serverBinLookupCandidatesOn(goos, flagValue string) []string {
	names := serverBinCandidates
	if flagValue != "" {
		names = []string{flagValue}
	}
	if goos != "windows" {
		return names
	}
	// ⚠ THE EXACT VALUE FIRST, .exe ONLY AS A FALLBACK. The first version tried
	// the suffixed spelling first, which silently overrode an operator who named a
	// specific file with --server-bin whenever a same-named .exe sat beside it.
	// Review caught it: adding a spelling must not re-rank the one that was asked
	// for.
	out := make([]string, 0, len(names)*2)
	for _, n := range names {
		out = append(out, n)
		if !strings.HasSuffix(strings.ToLower(n), ".exe") {
			out = append(out, n+".exe")
		}
	}
	return out
}

// kitNeedsServerBin reports whether this kit registers by spawning the stdio
// bridge rather than by driving an agent CLI or handing over a URL. Claude
// Desktop is the case: its config file starts local processes, so the entry names
// our binary and the install has to find one.
func kitNeedsServerBin(kit agentKit) bool {
	return kit.bin == "" && kit.mcpConfigFile != ""
}

// resolveServerBin finds the server binary the stdio bridge will be spawned from
// and returns an ABSOLUTE path.
//
// Absolute matters: the agent launches this command itself, from whatever working
// directory it happens to be in and with a PATH that may not match the installing
// shell's. A bare name that resolves here can easily fail there, and the failure
// surfaces to the user as an MCP server that simply never connects.
//
// Under --dry-run a missing binary is tolerated so the plan still prints, matching
// how the agent CLI itself is resolved.
func resolveServerBin(flagValue string, dryRun bool) (string, error) {
	candidates := serverBinLookupCandidates(flagValue)

	for _, name := range candidates {
		// LookPath handles both a bare name (searched on PATH) and an explicit
		// path, and confirms the file is actually executable either way.
		if path, err := exec.LookPath(name); err == nil {
			if abs, err := filepath.Abs(path); err == nil {
				return abs, nil
			}
			return path, nil
		}
	}

	if dryRun {
		return candidates[0], nil
	}
	return "", fmt.Errorf("cannot find the agentsmemory server binary (tried %s) — pass --server-bin /path/to/agentsmemory",
		strings.Join(candidates, ", "))
}

// newInstaller builds an Installer for one agent kit from parsed CLI flags. It
// resolves the target config dir (isolated sandbox vs the kit's global dir) and
// the agent CLI to drive, selecting a dry-run runner when --dry-run is set.
// `install --agent both` calls this once per kit.
func newInstaller(kit agentKit, c *cli.Command, out io.Writer, in io.Reader) (*Installer, error) {
	// Resolve the install target (and whether it was pinned on the command line)
	// from the mode flags. Kept as a pure helper so the precedence and the
	// mutually-exclusive-flags rule are testable without CLI plumbing.
	local := c.Bool("local")
	targetDir, sandboxName, explicitTarget, err := resolveInstallTarget(
		kit, c.Bool("global"), local, c.String("sandbox"), c.String("claude-dir"), homeDir())
	if err != nil {
		return nil, err
	}

	// --local swaps the endpoint default, not the endpoint: an explicit --mcp-url
	// still wins, so a self-hosted server on another port or host is reachable
	// with both flags.
	mcpURL := c.String("mcp-url")
	if local && !c.IsSet("mcp-url") {
		mcpURL = localMCPURL
	}

	dryRun := c.Bool("dry-run")

	// --socket registers the stdio bridge instead of an HTTP endpoint, which only
	// makes sense against a self-hosted server: the bridge carries no credential,
	// so pointing it at the multi-tenant service would register an MCP that can
	// only ever answer 401. Requiring --local says that up front instead.
	socket, serverBin := c.String("socket"), ""
	if socket != "" {
		if !local {
			return nil, fmt.Errorf("--socket requires --local: a socket-served MCP carries no token, so it only reaches a self-hosted server")
		}
		if serverBin, err = resolveServerBin(c.String("server-bin"), dryRun); err != nil {
			return nil, err
		}
	}
	// A kit with no CLI registers by SPAWNING the bridge, so it needs the server
	// binary for the same reason --socket does. Resolving it only for --socket
	// left Claude Desktop refusing an install on a machine where the binary was
	// sitting on PATH the whole time — the refusal was right, the lookup never ran.
	// Tolerated when missing: registerClaudeDesktopMCP produces the actionable
	// error, and failing here would take the whole install down over one agent.
	if serverBin == "" && kitNeedsServerBin(kit) {
		serverBin, _ = resolveServerBin(c.String("server-bin"), true)
	}

	// We always register our MCP, which needs the agent's own CLI, so resolve it
	// now. Under --dry-run tolerate a missing CLI so the plan can still be printed.
	agentBin, err := resolveAgentCLI(kit, c)
	if err != nil {
		if !dryRun {
			return nil, err
		}
		agentBin = kit.bin
	}

	var runner commandRunner = execRunner{out: out}
	if dryRun {
		runner = dryRunner{out: out}
	}

	return &Installer{
		kit:            kit,
		targetDir:      targetDir,
		sandboxName:    sandboxName,
		explicitTarget: explicitTarget,
		agentBin:       agentBin,
		mcpURL:         mcpURL,
		socket:         socket,
		serverBin:      serverBin,
		scope:          c.String("scope"),
		local:          local,
		wing:           strings.TrimSpace(c.String("wing")),
		token:          c.String("token"),
		copyGlobal:     c.Bool("copy"),
		sharedAuth:     c.Bool("shared-auth"),
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

	// Seeding runs before anything of ours is written, so the kit's own files
	// (which the copy never overwrites) land on top of the inherited config.
	if err := i.seedFromGlobal(); err != nil {
		return err
	}
	// Sharing comes after the copy: --copy may have just written a private
	// snapshot of the credentials, and a link supersedes a snapshot.
	if err := i.linkSharedAuth(); err != nil {
		return err
	}

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
	switch {
	case i.kit.name == agentPi:
		// Both companions need something pi does not have: codebase-memory is a
		// stdio MCP server and codex review is a Claude plugin marketplace. Say so
		// instead of running an installer whose output nothing would consume.
		fmt.Fprintln(i.out, "  none for pi: codebase-memory is a stdio MCP and codex review is a Claude plugin — pi supports neither")
	case i.recommended:
		i.installRecommended()
	default:
		fmt.Fprintf(i.out, "  skipped (pass --recommended to add %s)\n", extensionsList(i.kit))
	}

	i.step("4/4  done")
	i.summary()
	return nil
}

// seedFromGlobal copies the agent's existing global configuration into the
// target dir before the kit is installed, so a fresh sandbox inherits the things
// that are painful to recreate: the provider logins in auth.json, the MCP servers
// and plugins already registered, custom skills, themes and settings.
//
// Only configuration travels. Conversation history, logs, caches and extracted
// binaries are excluded (see skipCopy) — a global ~/.codex runs to hundreds of
// megabytes, nearly all of it per-machine runtime state that a new sandbox is
// better off without.
//
// Nothing already in the target is overwritten, so this is safe to re-run: the
// copy fills gaps, the install then writes the kit on top.
func (i *Installer) seedFromGlobal() error {
	if !i.copyGlobal {
		return nil
	}
	src := i.kit.globalConfigDir(homeDir())
	// Copying the global dir onto itself would be a no-op at best; more likely the
	// user meant --sandbox and would otherwise get a silent nothing.
	if sameDir(src, i.targetDir) {
		return fmt.Errorf("--copy needs a target other than the global config dir: pass --sandbox <name> or --config-dir <dir>")
	}
	if _, err := os.Stat(src); err != nil {
		i.warn("--copy: no global %s config at %s — nothing to inherit", i.kit.name, src)
		return nil
	}

	i.step("0/4  inherit the global " + i.kit.name + " config")
	if i.dryRun {
		fmt.Fprintf(i.out, "  would copy %s → %s (config, credentials, plugins and skills; no history, logs or caches)\n", src, i.targetDir)
		return nil
	}
	stats, err := copyConfigTree(src, i.targetDir)
	if err != nil {
		// A partial copy is still useful, and the install that follows is what the
		// user actually asked for — report and carry on rather than abort.
		i.warn("--copy: %v (copied %d files before stopping)", err, stats.Files)
		return nil
	}
	i.ok("copied %d files (%s) from %s", stats.Files, humanBytes(stats.Bytes), src)
	if stats.Skipped > 0 {
		i.ok("kept %d file(s) already in the target untouched", stats.Skipped)
	}
	fmt.Fprintln(i.out, "  note: credentials came too — this config can act as you until you sign it out")
	return nil
}

// sameDir reports whether two paths name the same directory, resolving symlinks
// so ~/.claude and a symlinked twin are not treated as different targets.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

// writeAssets writes the embedded slash commands and the Stop hook into the
// target config dir. M.md and am.md are the bootstrap commands; load-skill.md is
// the /load-skill nicety over the am_load_skill tool. The legacy agentsmemory.md
// was retired and is intentionally not shipped.
//
// The same markdown serves both agents: codex reads top-level files in
// <CODEX_HOME>/prompts with the same `description:` / `argument-hint:` front
// matter and `$ARGUMENTS` expansion Claude uses for commands/, so only the
// directory name (and the `/prompts:` invocation prefix) differs.
func (i *Installer) writeAssets() error {
	if err := i.writeCommands(); err != nil {
		return err
	}

	// Subagent definitions are independent of hooks and must be written BEFORE the
	// hookless early return below. They were not, and Cursor — the first agent
	// with an agents/ directory and no hook system — silently got none: rung 1 of
	// the reachability ladder with no rung 2, in the commit that made definitions
	// kit-driven. Found by reading the installed config dir after a real install,
	// not by a test; TestAgentWithoutACommandsDirWritesNoCommands ALLOWED the
	// agents directory without requiring it, which is a check on what must not
	// happen with nothing asserting what must.
	if err := i.writeAgentDefinitions(); err != nil {
		return err
	}

	// An agent with no hook system gets no hook script: pi retired hooks/ in
	// favour of extensions, so its end-of-turn checkpoint ships inside the bridge
	// extension (see registerPiMCP) and a stray .sh here would only confuse.
	// Cursor is hookless for a different reason — its hook shape was never
	// established — and gets no legacy note, which is pi's alone.
	if i.kit.hooksFile == "" {
		if i.kit.name == agentPi {
			i.notePiLegacyHook()
		}
		return nil
	}

	hook, err := i.source().ReadFile(hookAsset)
	if err != nil {
		return err
	}
	if err := i.writeFile(i.hookPath(), hook, 0o755); err != nil {
		return err
	}
	i.ok("hook %s", filepath.Base(i.hookPath()))

	// Stop (every hook-owning kit) and SessionEnd (Claude) both source this.
	// Codex only registers Stop, but that script still `.` the helper, so it
	// has to land for every kit that gets a hook script, not only Claude.
	statsHelper, err := i.source().ReadFile(statsHelperAsset)
	if err != nil {
		return err
	}
	if err := i.writeFile(i.statsHelperPath(), statsHelper, 0o644); err != nil {
		return err
	}
	i.ok("hook %s", filepath.Base(i.statsHelperPath()))

	// The companion hooks remain Claude-only. Codex 0.144.5 exposes the two
	// SUBAGENT event names, but event availability is not the execution contract:
	// its input fields, stdout feedback envelope, and exit-2 retry behaviour have
	// not been captured. Ship no script until a live Codex dispatch proves those
	// details; this audit made no claim about the other session events.
	if i.kit.shipsCompanionHooks {
		verifyHook, err := i.source().ReadFile(verifyHookAsset)
		if err != nil {
			return err
		}
		if err := i.writeFile(i.verifyHookPath(), verifyHook, 0o755); err != nil {
			return err
		}
		i.ok("hook %s", filepath.Base(i.verifyHookPath()))

		subHook, err := i.source().ReadFile(subagentHookAsset)
		if err != nil {
			return err
		}
		if err := i.writeFile(i.subagentHookPath(), subHook, 0o755); err != nil {
			return err
		}
		i.ok("hook %s", filepath.Base(i.subagentHookPath()))

		endHook, err := i.source().ReadFile(sessionEndHookAsset)
		if err != nil {
			return err
		}
		if err := i.writeFile(i.sessionEndHookPath(), endHook, 0o755); err != nil {
			return err
		}
		i.ok("hook %s", filepath.Base(i.sessionEndHookPath()))

		recallHook, err := i.source().ReadFile(recallHookAsset)
		if err != nil {
			return err
		}
		if err := i.writeFile(i.recallHookPath(), recallHook, 0o755); err != nil {
			return err
		}
		i.ok("hook %s", filepath.Base(i.recallHookPath()))

		taskRecallHook, err := i.source().ReadFile(taskRecallHookAsset)
		if err != nil {
			return err
		}
		if err := i.writeFile(i.taskRecallHookPath(), taskRecallHook, 0o755); err != nil {
			return err
		}
		i.ok("hook %s", filepath.Base(i.taskRecallHookPath()))
	}
	// Only a hook-owning kit relocates the script: it is the one that also
	// re-registers the new path, so no agent is left pointing at a deleted file.
	i.clearLegacyHook()
	i.clearRetiredCommands()
	return nil
}

// mcpURLPlaceholder is what a shipped agent definition carries where the MCP
// endpoint goes. codex names the server inside the definition rather than
// inheriting the global registration, and that URL is not a constant: a
// self-hosted install points at localhost, a hosted one at the service. Left
// unsubstituted it produces an agent whose memory tools point nowhere, silently.
const mcpURLPlaceholder = "__AGENTSMEMORY_MCP_URL__"

// agentDefinitionPath is where a shipped subagent definition is installed, in the
// dialect this agent reads.
func (i *Installer) agentDefinitionPath(name string) string {
	return filepath.Join(i.targetDir, i.kit.agentsDir, name+i.kit.agentAssetExt)
}

// writeAgentDefinitions installs the shipped subagent definitions.
//
// This is ADR-017's mechanism 1, and the ADR calls it "the one unambiguous
// structural fix… the only one of the three that cannot fail for compliance
// reasons, because it changes what is POSSIBLE rather than what is asked". An
// agent whose definition declares a `tools:` allowlist can call only what the
// list names, so a subagent defined without the am_* tools cannot recall however
// it is instructed — the injection arrives and there is no tool to obey it.
//
// It exists as a separate function because T2 shipped the definition into the
// binary's embed and wrote it nowhere: rung 1 of the reachability ladder with no
// rung 2, in the very commit whose purpose was to add it. The test that was
// supposed to cover it globbed the REPOSITORY's agents/ directory, so it passed
// against a file no install ever produced — the "exercises the component rather
// than the selection" shape this repository has now shipped five times.
func (i *Installer) writeAgentDefinitions() error {
	if i.kit.agentsDir == "" {
		return nil // pi has no subagent system to define agents for
	}
	endpoint := i.mcpURL
	if i.kit.name == agentCodex {
		var err error
		endpoint, err = mcpURLWithWing(i.mcpURL, i.wing)
		if err != nil {
			return err
		}
	}
	for _, name := range agentAssets {
		asset := i.kit.agentsDir + "/" + name + i.kit.agentAssetExt
		data, err := i.source().ReadFile(asset)
		if err != nil {
			return err
		}
		// The endpoint the definition names must be the one this install just
		// registered, not the one that happened to be in the checked-in file.
		data = []byte(strings.ReplaceAll(string(data), mcpURLPlaceholder, endpoint))
		if err := i.writeFile(i.agentDefinitionPath(name), data, 0o644); err != nil {
			return err
		}
		i.ok("agent %s", filepath.Base(i.agentDefinitionPath(name)))
	}
	return nil
}

// writeCommands writes the slash-command markdown into the kit's commands dir.
// It is split out of writeAssets so `update-skill` can refresh the commands
// without touching the Stop hook, which it deliberately leaves alone.
//
// The same markdown serves every agent: codex and pi read top-level files in
// their prompts dir with the same `description:` / `argument-hint:` front matter
// and `$ARGUMENTS` expansion Claude uses for commands/, so only the directory
// name (and the invocation prefix) differs.
func (i *Installer) writeCommands() error {
	// Empty means the agent HAS no commands directory, the way an empty hooksFile
	// means it has no hook system. Without this, filepath.Join(dir, "", "M.md") is
	// dir/M.md and all three commands land loose in the config root — files the
	// agent never reads, in a directory it shares with products we did not write.
	if i.kit.commandsDir == "" {
		i.ok("%s has no slash-command directory — the protocol loads itself", i.kit.name)
		return nil
	}
	for _, name := range commandAssets {
		data, err := i.source().ReadFile("commands/" + name)
		if err != nil {
			return err
		}
		if err := i.writeFile(filepath.Join(i.targetDir, i.kit.commandsDir, name), data, 0o644); err != nil {
			return err
		}
		i.ok("command %s", i.commandLabel(name))
	}
	return nil
}

// notePiLegacyHook warns when a pi install finds a hooks/ directory it must not
// touch. pi halts its launch on one, but the directory belongs to the Claude or
// codex kit installed in this same (shared) config dir: deleting the script here
// would leave that agent's Stop registration pointing at a missing file. So the
// user is told which install re-locates it instead.
func (i *Installer) notePiLegacyHook() {
	if _, err := os.Stat(filepath.Join(i.targetDir, "hooks")); err != nil {
		return
	}
	i.warn("pi halts on the hooks/ directory in %s", i.targetDir)
	fmt.Fprintf(i.out, "       it belongs to the Claude/codex kit — re-run that install to relocate it:\n")
	fmt.Fprintf(i.out, "         aiagentmemory install --agent both --config-dir %s --yes\n", i.targetDir)
}

// hookPath is the absolute install path of the Stop hook under the target dir.
func (i *Installer) hookPath() string { return filepath.Join(i.targetDir, hookFile) }

// verifyHookPath is where the SessionStart hook is installed.
func (i *Installer) verifyHookPath() string { return filepath.Join(i.targetDir, verifyHookFile) }

// subagentHookPath is where the SubagentStart hook is installed.
func (i *Installer) subagentHookPath() string {
	return filepath.Join(i.targetDir, subagentHookFile)
}

// sessionEndHookPath is where the SessionEnd hook is installed.
func (i *Installer) sessionEndHookPath() string {
	return filepath.Join(i.targetDir, sessionEndHookFile)
}

// recallHookPath is where the recall hook is installed.
func (i *Installer) recallHookPath() string {
	return filepath.Join(i.targetDir, recallHookFile)
}

// taskRecallHookPath is where the UserPromptSubmit recall hook is installed.
func (i *Installer) taskRecallHookPath() string {
	return filepath.Join(i.targetDir, taskRecallHookFile)
}

// statsHelperPath is where the sourced /stats helper lands, beside the scripts
// that `.` it. The filename is the contract: both hook scripts resolve it as
// a sibling of $0.
func (i *Installer) statsHelperPath() string {
	return filepath.Join(i.targetDir, statsHelperFile)
}

// legacyHookPath is where earlier installs wrote the hook, under hooks/.
func (i *Installer) legacyHookPath() string { return filepath.Join(i.targetDir, legacyHookRel) }

// clearRetiredCommands deletes command files this kit no longer ships from the
// config dir it is installing into.
//
// It exists because stopping shipping an asset does not remove it: an upgraded
// machine keeps the old file and keeps offering the command, while the
// installer's output lists only what it wrote. ADR-041 recorded that shape for a
// hook registration; a slash command has the same one, and a retired /M sitting
// beside /am is a second grounding protocol nobody maintains.
func (i *Installer) clearRetiredCommands() {
	if i.kit.commandsDir == "" {
		return // an agent with no commands directory has nothing to retire
	}
	for _, name := range retiredCommands {
		path := filepath.Join(i.targetDir, i.kit.commandsDir, name)
		if _, err := os.Stat(path); err != nil {
			continue // never installed here, or already gone
		}
		if i.dryRun {
			fmt.Fprintf(i.out, "  would remove the retired command %s\n", path)
			continue
		}
		if err := os.Remove(path); err != nil {
			i.warn("could not remove the retired command %s: %v", path, err)
			continue
		}
		i.ok("removed the retired command %s", i.commandLabel(name))
	}
}

// clearLegacyHook removes the pre-relocation hook script and, if it leaves the
// directory empty, the hooks/ directory itself — which is the whole point: pi
// halts its launch on any hooks/ directory in a config dir it shares. The
// directory is only removed when empty, so a hooks/ folder holding the user's own
// scripts is left alone (they keep the warning, but losing their files would be
// far worse).
func (i *Installer) clearLegacyHook() {
	legacy := i.legacyHookPath()
	if _, err := os.Stat(legacy); err != nil {
		return // nothing from an older install here
	}
	if i.dryRun {
		fmt.Fprintf(i.out, "  would remove the legacy hook %s (pi halts on a hooks/ dir)\n", legacy)
		return
	}
	if err := os.Remove(legacy); err != nil {
		i.warn("could not remove the legacy hook %s: %v", legacy, err)
		return
	}
	// os.Remove on a directory succeeds only when it is empty, which is exactly
	// the condition we want — no need to read it first.
	if err := os.Remove(filepath.Dir(legacy)); err == nil {
		i.ok("removed the legacy hooks/ directory (pi halts on it)")
	} else {
		i.ok("removed the legacy hook script from hooks/")
	}
}

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

// installedServerBinName is the filename the placed server binary takes inside a
// kit's own config directory.
const installedServerBinName = "aiagentmemory-server"

// installedServerBinFile is installedServerBinName with the platform's executable
// extension, which is the name the placed file must actually carry.
//
// ⚠ WINDOWS NEEDS THE .exe AND THE CONFIG NAMES THIS PATH VERBATIM. Claude
// Desktop's entry spawns the file we place, and an entry pointing at an
// extension-less path relies on the spawner appending one — CreateProcess does,
// but nothing here controls whether Electron spawns that way, and an install is
// not the place to depend on it. Flagged as unverified rather than broken in the
// 2026-08-31 report; carrying the extension costs one line and removes the
// question.
func installedServerBinFile() string {
	if runtime.GOOS == "windows" {
		return installedServerBinName + ".exe"
	}
	return installedServerBinName
}

// placeServerBin copies the resolved server binary into the kit's OWN config
// directory and returns the path to write into the MCP registration.
//
// ⚠ THE REGISTRATION USED TO FREEZE WHEREVER THE BINARY HAPPENED TO BE. It
// recorded the absolute path resolveServerBin found on $PATH at install time, and
// nothing ever re-resolved it: not a later install, not an upgrade, not doctor.
// Measured 2026-08-30 on the author's machine — Claude Desktop had been spawning a
// FIVE-DAY-OLD binary from one directory while a current one sat on PATH in
// another, and the only symptom was a server quietly serving old code. It was
// found by listing processes, not by any check.
//
// Copying makes the path the installer's to own: every install refreshes it, so
// the registration cannot drift from the binary the operator just built. The
// absolute-path reasoning in resolveServerBin still holds and is unchanged — this
// only decides WHICH absolute path, replacing "wherever it was that day" with
// "the one this kit installs".
//
// ⚠ STAGE THEN RENAME, NEVER WRITE OVER THE LIVE PATH. Two failures meet here and
// only an atomic swap avoids both. macOS caches an executable's code signature by
// inode, so truncating a mapped binary in place leaves the next exec dying with
// SIGKILL — and with a checksum identical to a copy that runs fine, which is why
// it cost an afternoon to attribute. Measured 2026-08-30, twice. But the obvious
// remedy, os.Remove followed by os.WriteFile, is worse than it looks: between
// those two calls there is NO file at a path an agent config already points at,
// so an interrupted or failing install leaves a registration aimed at nothing.
// Review found it before it shipped. Renaming a staged file within the same
// directory is atomic, gives the new file a fresh inode, and leaves the previous
// binary untouched on any failure — the pattern replaceBinary already uses for
// self-update, for the same reasons.
func (i *Installer) placeServerBin() (string, error) {
	dest := filepath.Join(i.targetDir, "bin", installedServerBinFile())
	if i.dryRun {
		fmt.Fprintf(i.out, "  would install the server binary: %s → %s\n", i.serverBin, dest)
		return dest, nil
	}

	// ⚠ NO SAME-PATH SHORTCUT. An earlier version returned early when
	// filepath.Abs(i.serverBin) equalled dest, on the theory that re-installing an
	// unchanged binary need not copy. But filepath.Abs is lexical — it resolves no
	// symlinks — so the shortcut also accepted a symlink, a non-executable file, or
	// nothing at all sitting at the canonical path, and returned it as though this
	// install owned it. Copying unconditionally is cheap on an install path and is
	// the only version that makes the ownership claim true. Review found this.
	data, err := os.ReadFile(i.serverBin)
	if err != nil {
		return "", fmt.Errorf("read the server binary %s: %w", i.serverBin, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}

	// Stage beside the destination so the rename below stays within one filesystem;
	// os.Rename across devices fails, and a temp dir may well be on another.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".aiagentmemory-server-*")
	if err != nil {
		return "", fmt.Errorf("stage the server binary next to %s: %w", dest, err)
	}
	staged := tmp.Name()
	defer os.Remove(staged) // a no-op once the rename has consumed it

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write the staged server binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close the staged server binary: %w", err)
	}
	// Chmod explicitly rather than trusting CreateTemp's 0600-and-umask: the file
	// must be executable, and an unusual umask must not be able to decide otherwise.
	if err := os.Chmod(staged, 0o755); err != nil {
		return "", fmt.Errorf("make the staged server binary executable: %w", err)
	}
	if err := os.Rename(staged, dest); err != nil {
		return "", fmt.Errorf("install the server binary to %s: %w", dest, err)
	}
	i.ok("installed server binary → %s", dest)
	return dest, nil
}

// registerStopHook adds the Stop hook idempotently: Claude's JSON registration
// is merged into settings.json, while Codex's native TOML registration is one
// marked block in config.toml. Both hand the script a Stop event carrying
// stop_hook_active, which is what it uses for loop prevention.
func (i *Installer) registerStopHook() error {
	if i.kit.hooksFile == "" {
		// Two different reasons, and saying the wrong one is worse than saying
		// nothing. pi retired hooks in favour of extensions, so its checkpoint
		// ships inside the bridge we install. Cursor has a hooks directory whose
		// events and payloads were never established, so we register nothing there
		// on purpose — and the operator should know the write half is missing
		// rather than assume it landed.
		if i.kit.name == agentPi {
			i.ok("%s has no hooks — the memory checkpoint ships in the bridge extension", i.kit.name)
		} else {
			i.warn("%s gets no memory checkpoint: its hook shape is not established, so nothing "+
				"will prompt you to persist a session", i.kit.name)
		}
		return nil
	}
	hooksFile := filepath.Join(i.targetDir, i.kit.hooksFile)
	plans := i.hookPlansOn(i.platform())
	i.noteSessionEndSkippedOn(i.platform())
	i.warnIfRepointing(hooksFile)
	i.warnSocketHooksCannotReachTheServer()

	if i.dryRun {
		for _, p := range plans {
			verb := "register"
			if p.retire {
				verb = "retire the"
			}
			fmt.Fprintf(i.out, "  would %s %s hook in %s: %q\n", verb, p.event, hooksFile, p.cmd)
		}
		if i.kit.name == agentCodex {
			fmt.Fprintf(i.out, "  would retire the agentsmemory Stop hook from %s if present\n",
				filepath.Join(i.targetDir, "hooks.json"))
		}
		return nil
	}
	if i.kit.name == agentCodex {
		// Codex 0.144.5 reads TOML hooks from config.toml and warns whenever the
		// second hooks.json representation also exists. Land the TOML registration
		// FIRST: cleanup must never leave the user with no checkpoint if writing
		// config.toml fails.
		p := plans[0]
		changed, err := ensureCodexStopHook(hooksFile, p.cmd)
		if err != nil {
			return err
		}
		if changed {
			i.ok("%s", p.note)
		} else {
			i.ok("%s hook already registered", p.event)
		}

		legacy := filepath.Join(i.targetDir, "hooks.json")
		retired, remains, err := retireLegacyCodexHook(legacy)
		if err != nil {
			return fmt.Errorf("retire previous Codex hook registration: %w", err)
		}
		if retired && !remains {
			i.ok("migrated agentsmemory hook and removed its previous hooks.json representation")
		}
		if remains {
			i.warn("preserved non-agentsmemory content in %s; Codex may continue warning about two hook representations", legacy)
		}
		return nil
	}

	regs := make([]hookReg, len(plans))
	for n, p := range plans {
		// A retirement matches OUR script wherever it points — including the exact
		// command a previous install wrote, which foreignHookPredicate deliberately
		// spares because for a live registration that command is the one to keep.
		obsolete := foreignHookPredicate(p.cmd)
		if p.retire {
			script := p.cmd
			obsolete = func(cmd string) bool { return ourHookCommand(cmd, script) }
		}
		regs[n] = hookReg{event: p.event, cmd: p.cmd, obsolete: obsolete, retire: p.retire}
	}
	changed, err := ensureHooks(hooksFile, regs)
	if err != nil {
		return err
	}
	for _, p := range plans {
		if changed[p.event] {
			i.ok("%s", p.note)
		} else {
			i.ok("%s hook already registered", p.event)
		}
	}
	return nil
}

// hookPlan is one hook registration plus the line the operator is told when it
// lands. The note travels WITH the registration rather than beside it, because
// the previous shape — one hand-written if/else per event — is how SubagentStart
// shipped registered and silently, its result assigned to `_`.
type hookPlan struct {
	event string
	cmd   string
	note  string

	// retire says this plan REMOVES a registration rather than writing one. The
	// command is then the script whose registrations are dropped, not one that
	// will be installed.
	retire bool
}

// hookPlans is every hook this kit registers, in the order they are reported.
//
// This list is the single answer to "what does an install put in my settings
// file", and three things read it: the registration itself, the --dry-run
// preview (which previously previewed two of five), and
// TestReadmeNamesEveryHookEventTheInstallerRegisters, which fails when a README
// stops naming one of them.
//
// Claude-only past the Stop hook. Codex supports SubagentStart and SubagentStop,
// but their payload fields, output injection, and retry contract have not been
// measured there; registering scripts proven only against Claude would create a
// reachable branch with an unverified protocol. pi never reaches here — it has
// no hooks file, and its checkpoint ships inside the bridge extension.
func (i *Installer) hookPlans() []hookPlan {
	return i.hookPlansOn(i.platform())
}

// platform is the OS this install is planning for, injectable so the one
// platform-conditional decision in this file can be driven from any machine.
//
// A zero value means "this machine", so nothing but a test passes it. It exists
// because the Windows branch is otherwise unreachable in CI and on every
// developer box here — a conditional nothing can execute is a conditional nobody
// checks, which is what let its first version ship with a test file named
// sessionend_windows_test.go that Go compiled on Windows only.
func (i *Installer) platform() string {
	if i.goos != "" {
		return i.goos
	}
	return runtime.GOOS
}

// hookPlansOn is hookPlans with the platform passed in, so the one
// platform-conditional registration below can be exercised from any machine —
// the same seam globalConfigDirOn and serverBinLookupCandidatesOn already use.
//
// ⚠ SessionEnd IS NOT REGISTERED ON WINDOWS, and the reason is a floor rather
// than a defect in the script. Measured on Windows 11 (issue #150, medians of
// five): the full hook takes 3,210ms and loses the teardown race, reporting
// "Hook cancelled" on essentially every exit. Almost none of that is its own
// work — a bare `bash -c exit </dev/null` is already 1,032ms and `curl` another
// 708ms, because process creation there costs ~1s each. The bounded stdin read
// from fa918e1 was correct and contributes ~0 in the healthy case.
//
// So the hook cannot be made fast enough by editing it: what it does after
// starting is a fraction of what starting it costs. An honest absence beats a
// cancellation notice on every exit, which teaches an operator to ignore hook
// errors — and the errors they then ignore are the ones that matter. The stats
// it would have reported are available on demand from `/stats`; what is lost is
// the automatic end-of-session line, and the install says so rather than leaving
// a silent gap.
func (i *Installer) hookPlansOn(goos string) []hookPlan {
	plans := []hookPlan{
		{event: "Stop", cmd: i.hookCommand(i.hookPath()), note: "registered Stop hook in " + i.kit.hooksFile},
	}
	if i.kit.name != agentClaude {
		return plans
	}
	plans = append(plans,
		hookPlan{
			event: "SessionStart",
			cmd:   i.hookCommand(i.verifyHookPath()),
			note:  "registered SessionStart hook (verifies memories against your code)",
		},
		hookPlan{
			event: "SubagentStart",
			cmd:   i.hookCommand(i.subagentHookPath()),
			note:  "registered SubagentStart hook (a subagent wakes knowing memory exists)",
		},
		// The WRITE half (ADR-017 T3), and deliberately the SAME script as Stop:
		// it branches on hook_event_name, so the two nudges differ in text and not
		// in machinery. A second script would be a second thing to keep in step.
		hookPlan{
			event: "SubagentStop",
			cmd:   i.hookCommand(i.hookPath()),
			note:  "registered SubagentStop hook (a subagent offers back what it found)",
		},

		// ADR-041 T4. THIS LINE IS THE MECHANISM: the script is inert without it,
		// and a hook that is written but never registered is this repository's
		// characteristic defect wearing a shell script.
		//
		// ⚠ THE EVENT IS PART OF THE MECHANISM, not a label on it. This shipped
		// first as PreCompact, where Claude Code sends a hook's stdout to the
		// debug log and no further: the recall ran, printed, and was discarded,
		// and every test passed because they asserted what the script wrote. Only
		// SessionStart, UserPromptSubmit and UserPromptExpansion inject stdout
		// into the model's context. TestEveryInjectingHookIsOnAnInjectingEvent
		// is what makes that a gate rather than a paragraph.
		hookPlan{
			event: "SessionStart",
			cmd:   i.hookCommand(i.recallHookPath()),
			note:  "registered SessionStart hook (a fresh context starts with a recall already done)",
		},
		// ⚠ UserPromptSubmit, WHICH IS AN INJECTING EVENT AND WAS UNUSED. The
		// SessionStart recall above fires once per context and can only ask with
		// the branch name and the changed filenames; this one asks with the user's
		// own words, every prompt. Measured 2026-09-03, a real question reaches
		// `decisions` and `gotchas` at 0.354-0.415 where a branch-plus-filenames
		// query reached 0.404-0.409 and recalled nothing useful — the two hooks
		// answer different questions and neither subsumes the other.
		hookPlan{
			event: "UserPromptSubmit",
			cmd:   i.hookCommand(i.taskRecallHookPath()),
			note:  "registered UserPromptSubmit hook (each task starts with a recall about that task)",
		},
	)
	if goos != "windows" {
		plans = append(plans, hookPlan{
			event: "SessionEnd",
			cmd:   i.hookCommand(i.sessionEndHookPath()),
			note:  "registered SessionEnd hook (reports what recall did this session)",
		})
		return plans
	}
	// ⚠ AND ON WINDOWS, RETIRE IT — an install that only stops PLANNING the hook
	// leaves every upgraded machine registered. ensureHooks walks the events it is
	// handed, so an omitted event keeps whatever an older install wrote: the hook
	// goes on losing the teardown race and reporting "Hook cancelled" on every
	// exit, while the installer's own output says it is not registered. Reported
	// by review before this shipped.
	return append(plans, hookPlan{
		event:  "SessionEnd",
		cmd:    i.hookCommand(i.sessionEndHookPath()),
		note:   "retired the SessionEnd hook (it cannot finish before Windows tears the session down)",
		retire: true,
	})
}

// noteSessionEndSkippedOn says out loud what hookPlansOn leaves out on Windows,
// at the one moment an operator is looking.
//
// An absence nobody announces is indistinguishable from a bug: the same operator
// who saw "Hook cancelled" on every exit would otherwise see the hook simply stop
// appearing and have no way to tell a deliberate omission from a broken install.
// The remedy line matters as much as the absence — the stats are still there on
// demand, so what is lost is the automatic report, not the data.
func (i *Installer) noteSessionEndSkippedOn(goos string) {
	if goos != "windows" || i.kit.name != agentClaude {
		return
	}
	// ⚠ THE URL IS THE ONE THIS INSTALL WROTE, not an environment variable an
	// operator may not have. The first version suggested $AGENTSMEMORY_URL, which
	// is the PROXY origin (mcpprotocol.ProxyURLEnvVar) and is unset in an ordinary
	// install — the hooks read AGENTSMEMORY_MCP_URL — so the command it printed
	// expanded to a bare path. A remedy line that does not run is worse than none:
	// it reads as help and fails in front of someone already looking at an error.
	i.warn("SessionEnd hook NOT registered on Windows: process creation costs ~1s there, the "+
		"hook needs ~3.2s, and it loses the teardown race — reporting \"Hook cancelled\" on every "+
		"exit (#150). Any registration from an earlier install is retired. Nothing else is "+
		"affected. For the same numbers on demand: curl -fsS %q",
		strings.TrimSuffix(i.mcpURL, "/mcp")+"/stats?hours=2")
}

// ourHookCommand reports whether cmd is a registration of the same installer
// script as keep, the exact command included.
//
// It is the retirement counterpart of foreignHookPredicate, which answers the
// opposite question: that one spares the exact command because for a live
// registration it is the one to keep, and a retirement has nothing to keep. A
// hook somebody else wrote is left alone by both — an install may stop shipping a
// hook, and it may never delete a hook it did not write.
func ourHookCommand(cmd, keep string) bool {
	path, ok := installerHookPath(keep)
	if !ok {
		return false
	}
	return cmd == keep || installerHookCommandMatches(cmd, path)
}

// hookCommandURL is the endpoint baked into an installed hook command, or "" when
// the command carries none.
// ⚠ NOT ANCHORED. It used to carry a leading ^ from when it was handed one command
// string at a time; scanning the raw hooks file, nothing sits at position 0, so the
// anchored form matched nothing and warned nobody — on every agent.
var hookCommandURL = regexp.MustCompile(regexp.QuoteMeta(mcpprotocol.MCPURLEnvVar) + `='([^']*)'`)

// decodeHookCommandURL undoes the one escape a hooks file can leave inside the
// endpoint the regex above matches.
//
// ⚠ MATCHING RAW TEXT MEANS READING WHATEVER THE WRITER ESCAPED. `/` is a character
// JSON MAY escape as `\/` — legal, optional, and emitted by several writers,
// including whichever one last rewrote this machine's settings.json. Reading raw
// bytes is deliberate and stays: codex keeps its hooks in config.toml, and the
// unmarshal this replaced failed there and so warned nobody, which is the
// reachability defect this repository is named for. The price of that choice is
// that a JSON escape arrives at the comparison intact, and this is where it is paid.
//
// Measured 2026-09-02 on a healthy install: settings.json carried
// `http:\/\/localhost:8080\/mcp` while the install pointed at exactly
// `http://localhost:8080/mcp`. Nothing was being repointed, and the warning fired
// anyway — with BOTH endpoints rendered "(an endpoint that does not parse)", since
// a backslash is not legal in a host, so the message named no URL a reader could
// act on and its own advice was `--mcp-url (an endpoint that does not parse)`.
// A false alarm on a healthy install is how a check earns being switched off.
//
// Only `\/` is undone, and only because a backslash cannot appear in a real host
// or path — so an endpoint loses nothing. A general unquote is the wrong tool: it
// would rewrite TOML values that never carried an escape, which is the format the
// raw-text match exists to serve.
func decodeHookCommandURL(raw string) string {
	return strings.ReplaceAll(raw, `\/`, "/")
}

// warnIfRepointing says so out loud when this install is about to send the hooks
// at a DIFFERENT server than the one they currently talk to.
//
// ⚠ THIS IS THE LOUDEST THING THE INSTALLER DOES, and it exists because the
// silent version cost a whole session. `install --agent claude` with no --mcp-url
// takes the hosted default, and the default wins over whatever is already
// configured: on 2026-08-28 that repointed five working hooks from a local server
// to the hosted one, every hook went mute because the local token did not
// authenticate there, and NOTHING said a word. The symptom looked like broken
// hooks; the cause was a re-install.
//
// It reports and does not decide. Which URL wins is upgrade semantics — someone
// migrating local→hosted needs the new one to take effect — and changing that is
// a separate decision. Being told is what was missing.
func (i *Installer) warnIfRepointing(hooksFile string) {
	raw, err := os.ReadFile(hooksFile)
	if err != nil {
		return // no existing hooks file: nothing to repoint, and a fresh install says enough
	}
	// ⚠ THE RAW TEXT, NOT A PARSED DOCUMENT. This first unmarshalled JSON, which
	// made it silently useless for codex — whose hooks live in config.toml, so the
	// unmarshal failed and the function returned having warned nobody. A warning
	// that exists for one agent and quietly not for another is the reachability
	// defect this repository is named for. The assignment we are looking for has
	// the same shape in every format because WE wrote it, so match that instead of
	// the container around it.
	seen := map[string]bool{}
	for _, m := range hookCommandURL.FindAllStringSubmatch(string(raw), -1) {
		if got := decodeHookCommandURL(m[1]); got != "" {
			seen[got] = true
		}
	}
	for existing := range seen {
		if existing == i.mcpURL {
			continue
		}
		i.warn("this install REPOINTS your hooks: they currently talk to %s, and will now talk "+
			"to %s. If that is not what you meant, re-run with --mcp-url %s (or --local for "+
			"a server on this machine) — a hook pointed at a server it cannot authenticate "+
			"to goes silent rather than failing loudly.",
			redactURL(existing), redactURL(i.mcpURL), redactURL(existing))
	}
}

// redactURL renders an endpoint for display with anything secret removed.
//
// ⚠ THE WARNING PRINTS A URL THAT CAME OUT OF A USER-CONTROLLED FILE. An endpoint
// may legitimately carry credentials — userinfo, or a signed query — and this
// message goes to a terminal and very often into a log or a pasted bug report.
// Control characters are stripped too: the source is a file anyone can edit, and
// a warning is the wrong place to let it drive a terminal.
func redactURL(raw string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	u, err := url.Parse(clean)
	if err != nil || u.Host == "" {
		return "(an endpoint that does not parse)"
	}
	shown := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	out := shown.String()
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		out += " (credentials removed)"
	}
	return out
}

// warnSocketHooksCannotReachTheServer says out loud that a --socket install
// writes hooks which cannot talk to the server it just registered.
//
// ⚠ THE HOOKS ARE HTTP AND A SOCKET-ONLY SERVER HAS NO PORT. `--socket`
// registers the agent's MCP over the stdio bridge and leaves `i.mcpURL` alone,
// while `hookCommand` exports that URL — and only that URL — into every hook
// command. `listenerFor` (cmd/server/listen.go) binds EITHER the socket OR the
// TCP address, never both, so the endpoint those hooks carry is a port nothing
// is listening on. The recall hook shells out to `aiagentmemory mcp`, which has
// no socket flag at all; verify and stats use curl.
//
// This warns rather than fixes, and the distinction is deliberate. Making the
// hooks work over a socket needs a transport the `mcp` subcommand does not have
// — new capability, and a product decision about whether hooks should follow the
// bridge or the server should also bind a loopback port. What is cheap and
// correct today is to stop the failure being silent: a hook that cannot reach
// its palace exits quietly, which is exactly what a hook with nothing to say
// looks like.
func (i *Installer) warnSocketHooksCannotReachTheServer() {
	if i.socket == "" {
		return
	}
	i.warn("the hooks this install writes CANNOT reach a socket-only server. They carry "+
		"%s=%s, and a server started with --socket binds that socket instead of a TCP port, "+
		"so every hook will fail to connect and go quiet — which looks identical to a hook "+
		"with nothing to report. The MCP registration itself is fine: it reaches the server "+
		"over the stdio bridge. Give the server a TCP address as well if you want the hooks.",
		mcpURLEnvVar, i.mcpURL)
}

// shellQuote renders one literal POSIX-shell argument. Hook commands are stored
// as shell strings and execute long after installation, so config directories
// containing spaces, quotes, or metacharacters must remain one inert argument.
func shellQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
}

// bashHookCommand renders the bash invocation for an installed hook script.
// `--` keeps a path beginning with a dash from becoming a bash option;
// shellQuote keeps the path data rather than executable shell syntax.
func bashHookCommand(path string) string {
	return "bash -- " + shellQuote(path)
}

// hookCommand is the shell command stored in an agent's hook registration: the
// MCP URL this install talks to, then bash running the installed script. The
// URL is injected here rather than defaulted inside the script so a hosted
// install and a --local install cannot disagree about which palace /stats hits.
func hookCommand(mcpURL, path string) string {
	return mcpURLEnvVar + "=" + shellQuote(mcpURL) + " " + bashHookCommand(path)
}

func (i *Installer) hookCommand(path string) string {
	return hookCommand(i.mcpURL, path)
}

// stripMCPURLAssignment removes the leading AGENTSMEMORY_MCP_URL='…' this
// installer prefixes onto hook commands, leaving the bash invocation that
// installerHookPath already knows how to parse. Unprefixed legacy commands
// pass through unchanged so an upgrade can still match them.
func stripMCPURLAssignment(cmd string) string {
	// EVERY leading assignment, not just this installer's one variable. The
	// matching half and the reproducing half (hookCommandEnv) must accept the same
	// command shapes: a prefix one recognises and the other cannot reproduce is a
	// registration doctor would run with the wrong environment, which is the
	// defect hookCommandEnv documents. Caught by the falsifiability half of
	// TestAnUnprefixedRegistrationFallsBackToTheFlag, which asserts the two agree.
	for {
		_, _, rest, ok := splitLeadingAssignment(cmd)
		if !ok {
			return cmd
		}
		cmd = rest
	}
}

// splitLeadingAssignment parses ONE leading `VAR='value'` off a hook command,
// returning the name, the unquoted value, and what follows.
//
// It is the shared half of two jobs that must agree: matching a registration
// (stripMCPURLAssignment, which drops the prefix to reach the bash invocation)
// and REPRODUCING one (hookCommandEnv, which keeps it). They read the same
// quoting rules because a registration this parser can match and that parser
// cannot reproduce is exactly the defect doctor shipped — see hookCommandEnv.
//
// Only the single-quoted form is accepted, because it is the only one
// hookCommand emits (shellQuote), and a parser that guessed at bare or
// double-quoted values would be reading commands this installer never wrote.
func splitLeadingAssignment(cmd string) (name, value, rest string, ok bool) {
	eq := strings.IndexByte(cmd, '=')
	if eq <= 0 {
		return "", "", cmd, false
	}
	name = cmd[:eq]
	for i := 0; i < len(name); i++ {
		c := name[i]
		alpha := c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !alpha && !(i > 0 && c >= '0' && c <= '9') {
			return "", "", cmd, false
		}
	}
	body := cmd[eq+1:]
	if !strings.HasPrefix(body, "'") {
		return "", "", cmd, false
	}
	var b strings.Builder
	i := 1
	for i < len(body) {
		if body[i] == '\'' {
			// The `'"'"'` sequence shellQuote emits for an embedded quote: close,
			// escaped quote, reopen. Anything else ends the value.
			if strings.HasPrefix(body[i:], `'"'"'`) {
				b.WriteByte('\'')
				i += 5
				continue
			}
			return name, b.String(), strings.TrimSpace(body[i+1:]), true
		}
		b.WriteByte(body[i])
		i++
	}
	return "", "", cmd, false
}

// hookCommandEnv returns the `VAR=value` assignments a registered hook command
// carries, in the order they appear, ready for exec.Cmd.Env.
//
// ⚠ IT EXISTS BECAUSE doctor RAN A RECONSTRUCTION RATHER THAN THE REGISTRATION.
// The installer writes `AGENTSMEMORY_MCP_URL='http://localhost:8080/mcp' bash --
// <script>`; doctor kept only the script path and supplied the endpoint from its
// own flag, which defaults to the HOSTED URL. So on every self-hosted install it
// pointed the hook at a palace the operator does not use, the CLI demanded a
// workspace token for a non-loopback endpoint, and the recall hook took its
// no-credential branch and exited 0 — reported to the operator as
// "no credential configured", on an install that was working. Found 2026-08-31
// on a first Windows install, where it is the first thing a new operator sees.
//
// Returning the assignments rather than the one variable is deliberate: the
// narrow fix repairs this variable and leaves the defect standing, so the next
// hook that gains an environment variable reintroduces it silently.
func hookCommandEnv(cmd string) []string {
	var env []string
	for {
		name, value, rest, ok := splitLeadingAssignment(cmd)
		if !ok {
			return env
		}
		env = append(env, name+"="+value)
		cmd = rest
	}
}

// installerHookPath parses only the two command shapes this installer has
// emitted: today's quoted `bash -- <path>` and the old unquoted `bash <path>`.
// The old arm accepts one shell field; paths with spaces are matched separately
// against the exact target path because they cannot be distinguished safely
// from arbitrary multi-argument shell commands in the general case.
func installerHookPath(cmd string) (string, bool) {
	cmd = stripMCPURLAssignment(cmd)
	const quotedPrefix = "bash -- "
	if strings.HasPrefix(cmd, quotedPrefix) {
		encoded := strings.TrimPrefix(cmd, quotedPrefix)
		if len(encoded) < 2 || encoded[0] != '\'' || encoded[len(encoded)-1] != '\'' {
			return "", false
		}
		decoded := strings.ReplaceAll(encoded[1:len(encoded)-1], `'"'"'`, "'")
		if shellQuote(decoded) != encoded {
			return "", false
		}
		return decoded, true
	}

	fields := strings.Fields(cmd)
	if len(fields) != 2 || fields[0] != "bash" {
		return "", false
	}
	return fields[1], true
}

// installerHookCommandMatches recognizes one installer-owned script without
// claiming a user wrapper that merely mentions the same filename. Exact path
// matching also retires the old broken unquoted form from config directories
// containing spaces; the general parser stays strict for commands from another
// directory where an unquoted multi-field string would be ambiguous.
func installerHookCommandMatches(cmd, expectedPath string) bool {
	cmd = stripMCPURLAssignment(cmd)
	if cmd == "bash "+expectedPath || cmd == bashHookCommand(expectedPath) {
		return true
	}
	path, ok := installerHookPath(cmd)
	return ok && filepath.Base(path) == filepath.Base(expectedPath)
}

// foreignHookPredicate matches any Stop registration of our hook script that is
// not the one this install is writing. Two of those turn up: the pre-relocation
// entry under hooks/ (whose script the install has just deleted), and an entry
// inherited by --copy from another config dir (whose script still exists, so the
// checkpoint would fire twice every stop). Both are ours to retire; a hook the
// user wrote never matches, because the match is on our own filename.
func foreignHookPredicate(keep string) func(string) bool {
	keepPath, ok := installerHookPath(keep)
	if !ok {
		return func(string) bool { return false }
	}
	return func(cmd string) bool {
		if cmd == keep {
			return false
		}
		if installerHookCommandMatches(cmd, keepPath) {
			return true
		}
		return filepath.Base(keepPath) == hookFile &&
			installerHookCommandMatches(cmd, filepath.Join(filepath.Dir(keepPath), legacyHookRel))
	}
}

// registerAgentsMemoryMCP wires up the agentsmemory remote MCP. It resolves the
// workspace token (flag/env, else an interactive prompt) and registers the HTTP
// server with the agent's own CLI. This is the product's core value, so it runs in
// the default install — not gated behind --recommended.
//
// The two CLIs authenticate an HTTP MCP server differently, so the registration
// itself is the one step that genuinely diverges per agent.
func (i *Installer) registerAgentsMemoryMCP() error {
	token := i.resolveToken()
	i.resolvedToken = token
	// A self-hosted --local server usually has no credential to present, so an
	// empty token is the expected state there rather than the "user skipped it"
	// state below.
	if token == "" && !i.local {
		fmt.Fprintln(i.out, "  no token provided — skipping agentsmemory MCP.")
		fmt.Fprintf(i.out, "  add it later: %s\n", i.mcpAddHint())
		return nil
	}
	// A socket has no URL, so the agent cannot speak HTTP to it: it spawns the
	// server's own mcp-stdio bridge instead. This is checked before the per-agent
	// split because the stdio registration is identical for Claude and codex.
	if i.socket != "" {
		return i.registerSocketMCP()
	}

	switch i.kit.name {
	case agentCodex:
		return i.registerCodexMCP(token)
	case agentPi:
		return i.registerPiMCP(token)
	case agentCursor:
		return i.registerCursorMCP(token)
	case agentClaudeDesktop:
		return i.registerClaudeDesktopMCP(token)
	default:
		return i.registerClaudeMCP(token)
	}
}

// registerSocketMCP wires the agent to a server listening on a Unix socket, via
// the mcp-stdio bridge built into the server binary.
//
// No token is passed. --socket requires --local (enforced in newInstaller), and
// a local server authenticates by socket permissions rather than a credential —
// which is just as well, since an MCP command line is stored in plain config and
// visible in `ps`, so a token on argv would leak.
func (i *Installer) registerSocketMCP() error {
	if i.kit.name == agentPi {
		// pi has no MCP client of its own; its bridge extension speaks HTTP to a
		// URL. Spawning a stdio child is a different mechanism entirely, so this
		// is reported rather than silently registered as something else.
		return fmt.Errorf("--socket is not supported for pi (its bridge extension connects over HTTP): run the server on --addr and install pi with --mcp-url")
	}

	// The registration names the binary this install PLACED, for the same reason
	// the Desktop one does — see placeServerBin. Socket mode froze a PATH lookup
	// into the agent's config exactly like Desktop did, and a rebuild elsewhere
	// left the bridge spawning a stale server with nothing able to say so.
	placed, err := i.placeServerBin()
	if err != nil {
		return err
	}
	argv := []string{"mcp-stdio", "--socket", i.socket}
	if i.wing != "" {
		argv = append(argv, "--wing", i.wing)
	}
	if err := i.addStdioMCP(mcpName, placed, argv...); err != nil {
		return err
	}
	i.ok("registered MCP %q → %s (stdio bridge to %s)", mcpName, i.socket, placed)
	return nil
}

// registerClaudeMCP registers the remote MCP with the Claude CLI, which takes the
// bearer token inline as a header value.
func (i *Installer) registerClaudeMCP(token string) error {
	args := []string{"mcp", "add", "--transport", "http", "--scope", i.scope, mcpName, i.mcpURL}
	// A token-less server takes no Authorization header at all. Sending an empty
	// bearer would work against our own --local server (which ignores inbound
	// credentials) but is a lie in the config file: it reads as auth that exists.
	if token != "" {
		args = append(args, "--header", "Authorization: Bearer "+token)
	}
	// The wing rides on the connection rather than on the agent's memory: this is
	// what makes one palace hold many projects without them bleeding together.
	if i.wing != "" {
		args = append(args, "--header", wingHeader+": "+i.wing)
	}
	// `mcp add` is not idempotent by name, so remove any prior entry first
	// (ignoring "not found") and then add cleanly, all in one shot.
	i.agent(true, "mcp", "remove", "--scope", i.scope, mcpName)
	if err := i.agent(false, args...); err != nil {
		return err
	}
	i.ok("registered MCP %q → %s", mcpName, i.mcpURL)
	return nil
}

// registerCodexMCP registers the remote MCP with codex. `codex mcp add` has no
// static-header flag: a streamable-HTTP server is authed with
// --bearer-token-env-var, which persists the variable NAME in config.toml and
// makes codex read the value from its environment at launch. So we register the
// variable and persist the token itself (0600) inside CODEX_HOME, where
// `aiagentmemory run --agent codex` picks it up. Users who launch plain `codex`
// get the export line in summary().
//
// Writing the token to a file we own beats the alternatives: rewriting the user's
// config.toml to hold a static Authorization header would reformat a file that
// also carries their plugins, hook trust hashes and shell policy, and passing it
// on argv would leak it to `ps`.
func (i *Installer) registerCodexMCP(token string) error {
	endpoint, err := mcpURLWithWing(i.mcpURL, i.wing)
	if err != nil {
		return err
	}
	args := []string{"mcp", "add", mcpName, "--url", endpoint}
	// With no token there is nothing to persist and no variable for codex to read,
	// so the token file is not written at all — an empty AGENTSMEMORY_TOKEN file
	// would only mislead the next reader (and summary() would tell them to source
	// it for nothing).
	if token != "" {
		if err := i.writeFile(i.tokenPath(), []byte(tokenEnvVar+"="+token+"\n"), 0o600); err != nil {
			return err
		}
		i.ok("stored workspace token in %s (0600)", tokenFile)
		args = append(args, "--bearer-token-env-var", tokenEnvVar)
	}

	// Same remove-then-add shape as Claude: `codex mcp add` fails on a name that
	// already exists, and `remove` fails when nothing is there — so ignore that one.
	i.agent(true, "mcp", "remove", mcpName)
	if err := i.agent(false, args...); err != nil {
		return err
	}
	if token == "" {
		i.ok("registered MCP %q → %s (no token: self-hosted server)", mcpName, endpoint)
		return nil
	}
	i.ok("registered MCP %q → %s (token via $%s)", mcpName, endpoint, tokenEnvVar)
	return nil
}

// mcpURLWithWing scopes clients that cannot attach arbitrary HTTP headers. The
// server treats this registration query exactly like X-Agentsmemory-Wing, while
// an explicit wing argument on a tool call still wins (including "*" opt-in).
func mcpURLWithWing(rawURL, wing string) (string, error) {
	if wing == "" {
		return rawURL, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse MCP URL %q: %w", rawURL, err)
	}
	query := parsed.Query()
	query.Set("wing", wing)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// registerPiMCP wires the remote MCP into pi. pi ships no MCP client — it
// "intentionally does not include built-in MCP" and points at extensions instead
// — so there is no `pi mcp add` to call. Instead we install our bridge extension
// into <config dir>/extensions, where pi auto-discovers it: at startup it lists
// the remote tools and re-registers each one as a native pi tool, so `am_*` calls
// in the memory protocol work unchanged. The same extension carries the
// end-of-turn checkpoint that the Stop hook provides on the other agents.
//
// The extension reads its endpoint and token from the environment (it has no
// config of its own), so both are persisted 0600 beside it and exported by
// `aiagentmemory run --agent pi`. Nothing is passed on argv, which would leak the
// token to `ps`.
func (i *Installer) registerPiMCP(token string) error {
	ext, err := i.source().ReadFile(piExtensionAsset)
	if err != nil {
		return err // embed guarantees presence; an error here is a build bug
	}
	if err := i.writeFile(filepath.Join(i.targetDir, piExtensionAsset), ext, 0o644); err != nil {
		return err
	}
	i.ok("installed pi bridge extension %s", piExtensionAsset)

	// A local server needs no token, so the file carries the endpoint plus the flag
	// that tells the extension the absence is intentional. Everything the
	// extension reads still lives in one file that `aiagentmemory run` exports.
	env := fmt.Sprintf("%s=%s\n%s=%s\n", tokenEnvVar, token, mcpURLEnvVar, i.mcpURL)
	if token == "" {
		env = fmt.Sprintf("%s=%s\n%s=1\n", mcpURLEnvVar, i.mcpURL, localEnvVar)
	}
	// The wing rides in the same file the extension already reads, because pi's
	// bridge builds its own headers — so --wing means the same thing here as it
	// does for Claude rather than being quietly dropped. wingEnvVar is the
	// variable the memory protocol already reads, so a pi session and the
	// protocol agree on one name for one thing.
	if i.wing != "" {
		env += fmt.Sprintf("%s=%s\n", wingEnvVar, i.wing)
	}
	if err := i.writeFile(i.tokenPath(), []byte(env), 0o600); err != nil {
		return err
	}
	if token == "" {
		i.ok("stored endpoint in %s (0600; no token: self-hosted server)", tokenFile)
		i.ok("bridged MCP %q → %s", mcpName, i.mcpURL)
		return nil
	}
	i.ok("stored workspace token + endpoint in %s (0600)", tokenFile)
	i.ok("bridged MCP %q → %s (token via $%s)", mcpName, i.mcpURL, tokenEnvVar)
	return nil
}

// resolveAgentCLI picks the CLI binary to drive for the kit, honouring the
// per-agent override flag (--claude-bin / --codex-bin / --pi-bin) and its env var.
func resolveAgentCLI(kit agentKit, c *cli.Command) (string, error) {
	switch kit.name {
	case agentCodex:
		return resolveKitBin(kit, c.String("codex-bin"), kitBinEnv(kit))
	case agentPi:
		return resolveKitBin(kit, c.String("pi-bin"), kitBinEnv(kit))
	default:
		return resolveKitBin(kit, c.String("claude-bin"), kitBinEnv(kit))
	}
}

// registerCursorMCP registers the agentsmemory MCP server by writing Cursor's own
// config file, because Cursor ships no command that would do it.
//
// The entry shape is copied from Cursor's existing HTTP entries rather than
// invented: {"type":"http","url":…}, plus a headers object when a token is
// resolved. A self-hosted --local server usually has none, and an empty
// Authorization header is worse than no header at all.
//
// The approval step is printed rather than performed. Cursor gates every server
// behind an explicit approval, a registered-but-unapproved server is
// byte-identical on disk to a working one, and an installer that approves its own
// server on the user's behalf defeats the point of the gate.
func (i *Installer) registerCursorMCP(token string) error {
	path := filepath.Join(i.targetDir, i.kit.mcpConfigFile)
	entry := map[string]any{"type": "http", "url": i.mcpURL}
	headers := map[string]any{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	// Cursor's entries carry arbitrary headers, so --wing rides the connection
	// here as it does for Claude.
	if i.wing != "" {
		headers[wingHeader] = i.wing
	}
	if len(headers) > 0 {
		entry["headers"] = headers
	}
	if i.dryRun {
		fmt.Fprintf(i.out, "  would register the agentsmemory MCP in %s → %s\n", path, i.mcpURL)
		return nil
	}
	changed, err := ensureMCPServer(path, mcpName, entry)
	if err != nil {
		return err
	}
	if changed {
		i.ok("registered MCP %q in %s → %s", mcpName, i.kit.mcpConfigFile, i.mcpURL)
	} else {
		i.ok("MCP %q already registered in %s", mcpName, i.kit.mcpConfigFile)
	}
	// Say this every time, not only when the file changed: the approval is stored
	// outside mcp.json, so a re-install cannot tell whether it has happened.
	i.ok("approve it once so Cursor loads it:  cursor-agent mcp enable %s", mcpName)
	return nil
}

// registerClaudeDesktopMCP registers the server in Claude Desktop's own config
// file, as a STDIO entry spawning the bridge this product already ships.
//
// Desktop's config file speaks to local processes. The project's own
// windows-guide offers two remote routes — the Custom-connector UI, which is not
// on every plan, and `npx mcp-remote`, which needs Node.js — and both are aimed
// at the hosted service. For a self-hosted server there is a third and better
// one: `mcp-stdio --url` is a bridge in the server binary that opens no database
// and needs nothing else installed.
//
// It REFUSES rather than writing a command it cannot find. A Docker-only install
// produces no host binary at all — measured on the reference machine, where both
// candidate names resolved to nothing — and an entry naming a binary that is not
// there fails at spawn inside Claude Desktop, where the error reads as ours.
func (i *Installer) registerClaudeDesktopMCP(token string) error {
	if i.serverBin == "" {
		return fmt.Errorf("%s registers an mcp-stdio bridge, which needs the agentsmemory server "+
			"binary on this machine, and none was found. Build one "+
			"(go build -o ~/.local/bin/aiagentmemory-server ./cmd/server) or pass --server-bin "+
			"<path>. A Docker-only install produces no host binary", i.kit.name)
	}
	// The registration names the binary this install PLACED, not whatever was on
	// PATH when someone last ran it — see placeServerBin.
	placed, err := i.placeServerBin()
	if err != nil {
		return err
	}
	path := filepath.Join(i.targetDir, i.kit.mcpConfigFile)
	args := []any{"mcp-stdio", "--url", i.mcpURL}
	if i.wing != "" {
		args = append(args, "--wing", i.wing)
	}
	if token != "" {
		args = append(args, "--token", token)
	}
	entry := map[string]any{"command": placed, "args": args}
	if i.dryRun {
		fmt.Fprintf(i.out, "  would register the agentsmemory MCP in %s → %s mcp-stdio --url %s\n",
			path, placed, i.mcpURL)
		return nil
	}
	changed, err := ensureMCPServer(path, mcpName, entry)
	if err != nil {
		return err
	}
	if changed {
		i.ok("registered MCP %q in %s → %s mcp-stdio", mcpName, i.kit.mcpConfigFile, placed)
	} else {
		i.ok("MCP %q already registered in %s", mcpName, i.kit.mcpConfigFile)
	}
	i.ok("restart Claude Desktop to pick it up — it reads this file only at launch")
	return nil
}

// tokenPath is where the workspace token is persisted inside CODEX_HOME.
func (i *Installer) tokenPath() string { return filepath.Join(i.targetDir, tokenFile) }

// mcpAddHint is the command a user runs to add the MCP later, when they skipped
// the token prompt. Re-running this installer is deliberate: it works for the
// clients with no `mcp add` command and preserves the endpoint, target, bridge,
// and registration wing instead of silently falling back to a broad connection.
func (i *Installer) mcpAddHint() string {
	parts := []string{"aiagentmemory", "install", "--agent", shellQuote(i.kit.name)}
	switch {
	case i.sandboxName != "":
		parts = append(parts, "--sandbox", shellQuote(i.sandboxName))
	case i.kit.configEnv != "":
		parts = append(parts, "--config-dir", shellQuote(i.targetDir))
	default:
		// Cursor and Claude Desktop expose no relocatable config directory; their
		// installer already refuses --config-dir, so the recovery command must not
		// recommend a flag those clients cannot honor.
		parts = append(parts, "--global")
	}
	parts = append(parts, "--mcp-url", shellQuote(i.mcpURL))
	if i.wing != "" {
		parts = append(parts, "--wing", shellQuote(i.wing))
	}
	if i.kit.name == agentClaude && i.scope != "" {
		parts = append(parts, "--scope", shellQuote(i.scope))
	}
	if i.agentBin != "" && i.agentBin != i.kit.bin {
		var flag string
		switch i.kit.name {
		case agentClaude:
			flag = "--claude-bin"
		case agentCodex:
			flag = "--codex-bin"
		case agentPi:
			flag = "--pi-bin"
		}
		if flag != "" {
			parts = append(parts, flag, shellQuote(i.agentBin))
		}
	}
	if i.kit.name == agentClaudeDesktop && i.serverBin != "" {
		parts = append(parts, "--server-bin", shellQuote(i.serverBin))
	}
	parts = append(parts, "--token", "'<token>'", "--yes")
	return strings.Join(parts, " ")
}

// registerMemoryBootstrap installs the always-on operating protocol so the
// memory-first workflow applies every session without the user typing /am. It
// writes our owned copy of the embedded protocol as agentsmemory-bootstrap.md and
// merges a managed block into the agent's memory file. Both agents load that file
// as user memory from their config dir, so this applies in a sandbox (where we own
// the whole dir) and in the global dir (where the merge preserves whatever the
// user already wrote).
//
// What goes in the block differs: Claude Code resolves `@file.md` imports, so it
// gets a one-line import of the sibling protocol file — edit the file, every
// session picks it up. Codex has no import directive in AGENTS.md, so the protocol
// is inlined there instead; the sibling copy is still written, as the file the
// block is regenerated from on the next install.
func (i *Installer) registerMemoryBootstrap() error {
	// Some agents can hold no protocol at all: Claude Desktop has no memory file,
	// no rules directory and no commands directory. Writing the bootstrap into its
	// config dir would be litter nothing reads, so the protocol reaches it through
	// the MCP handshake instead (internal/mcpserver.serverInstructions, ADR-021
	// T1) — which is the ONLY channel that reaches a client like this, and the
	// reason that task exists.
	if i.kit.memoryFile == "" && i.kit.rulesFile == "" {
		i.ok("%s holds no protocol file — the server sends it on the MCP handshake instead",
			i.kit.name)
		return nil
	}
	data, err := i.source().ReadFile(bootstrapAsset)
	if err != nil {
		return err
	}
	bootstrapPath := filepath.Join(i.targetDir, bootstrapFile)
	if err := i.writeFile(bootstrapPath, data, 0o644); err != nil {
		return err
	}
	i.ok("memory protocol %s", bootstrapFile)

	// An agent with no memory file takes the protocol another way. Cursor loads
	// every rules/*.mdc marked `alwaysApply: true`, which is a whole file we own
	// rather than a managed block merged into the user's — so there is nothing to
	// merge and nothing of theirs to preserve.
	if i.kit.memoryFile == "" {
		return i.writeProtocolRule(data)
	}

	body := memoryImportLine
	if !i.kit.supportsImport {
		body = string(data)
	}

	// The block lands in the user's memory file, so it goes through the managed
	// idempotent merge (not a blind overwrite). Under dry-run, print the intent —
	// mirroring registerStopHook, which also can't preview through the merge.
	memoryPath := filepath.Join(i.targetDir, i.kit.memoryFile)
	if i.dryRun {
		fmt.Fprintf(i.out, "  would merge the memory protocol into %s (managed block)\n", memoryPath)
		return nil
	}
	changed, err := ensureManagedBlock(memoryPath, body)
	if err != nil {
		return err
	}
	if changed {
		i.ok("merged memory protocol into %s", i.kit.memoryFile)
	} else {
		i.ok("%s already carries the memory protocol", i.kit.memoryFile)
	}
	return nil
}

// writeProtocolRule delivers the always-on protocol to an agent that has no
// memory file, as a rule file it loads every session.
//
// Cursor is the case. The front matter is what makes it always-on: without
// `alwaysApply: true` the rule is loaded on demand, which is the difference
// between a protocol and a document nobody opens. Both keys are copied from a
// rule already loading on the reference machine, not invented.
func (i *Installer) writeProtocolRule(protocol []byte) error {
	if i.kit.rulesFile == "" {
		return fmt.Errorf("%s has no memory file and no rules file: the protocol would reach it "+
			"by no route at all", i.kit.name)
	}
	path := filepath.Join(i.targetDir, i.kit.rulesFile)
	if i.dryRun {
		fmt.Fprintf(i.out, "  would write the memory protocol to %s\n", path)
		return nil
	}
	front := "---\ndescription: agentsmemory operating protocol — recall team memory before acting, " +
		"persist before stopping\nalwaysApply: true\n---\n\n"
	if err := i.writeFile(path, append([]byte(front), protocol...), 0o644); err != nil {
		return err
	}
	i.ok("memory protocol %s (always applied)", i.kit.rulesFile)
	return nil
}

// installRecommended installs the companion ecosystem. Both agents get the
// codebase-memory MCP (its own installer + a stdio registration); Claude
// additionally gets the codex review plugin, which has no codex equivalent.
// Each step is best-effort — one
// already-installed plugin or a network hiccup should not abort the whole install
// — so failures are reported, not fatal.
func (i *Installer) installRecommended() {
	// Register the stdio MCP only if its binary actually landed: if the upstream
	// installer failed, pointing the agent CLI at a missing path would register
	// a broken server. (--dry-run still shows the full plan.)
	shellErr := i.runner.runShell(codebaseMemoryInstall)
	if shellErr != nil {
		i.warn("codebase-memory install script failed: %v", shellErr)
	} else {
		i.ok("installed codebase-memory-mcp")
	}
	bin := expandTilde(codebaseMemoryBin)
	if shellErr == nil || i.dryRun {
		if err := i.addStdioMCP(codebaseMemoryName, bin); err != nil {
			i.warn("register codebasememory MCP failed: %v", err)
		} else {
			i.ok("registered MCP %q → %s", codebaseMemoryName, bin)
		}
	} else {
		i.warn("skipping codebasememory MCP registration — installer did not complete")
	}

	if i.kit.name == agentCodex {
		// The codex review plugin is for Claude; codex has its own bundled review
		// capabilities, so say what is not happening rather than silently
		// installing less than the flag promises.
		fmt.Fprintln(i.out, "  note: the codex review plugin is Claude-only — nothing to install for codex")
		return
	}

	// Marketplace add is effectively idempotent; ignore its error and let the
	// install surface any real problem.
	i.agent(true, "plugin", "marketplace", "add", "openai/codex-plugin-cc")
	if err := i.agent(false, "plugin", "install", "codex@openai-codex"); err != nil {
		i.warn("install plugin codex@openai-codex failed: %v", err)
	} else {
		i.ok("installed plugin codex@openai-codex")
	}
}

// addStdioMCP registers a local stdio MCP server, remove-then-add so a re-run is
// idempotent. The two CLIs spell it differently: Claude scopes the entry and marks
// the command with --transport stdio, codex infers stdio from a trailing command
// and has no scope.
//
// argv carries any arguments the command needs after the binary — everything past
// the `--` separator, so a flag like --socket reaches the server rather than the
// agent CLI parsing it as its own.
func (i *Installer) addStdioMCP(name, bin string, argv ...string) error {
	if i.kit.name == agentCodex {
		i.agent(true, "mcp", "remove", name)
		return i.agent(false, append([]string{"mcp", "add", name, "--", bin}, argv...)...)
	}
	i.agent(true, "mcp", "remove", "--scope", i.scope, name)
	return i.agent(false, append([]string{"mcp", "add", "--transport", "stdio", "--scope", i.scope, name, "--", bin}, argv...)...)
}

// agent runs the resolved agent CLI, pinning its config-dir env var
// (CLAUDE_CONFIG_DIR / CODEX_HOME / PI_CODING_AGENT_DIR) to the target dir when —
// and only when — that dir is NOT where the agent already looks by default. See
// pinConfigDir for why the distinction is load-bearing. When ignoreErr is true a
// failure is swallowed — used for the pre-emptive `mcp remove` and
// `marketplace add` that legitimately fail when nothing exists.
func (i *Installer) agent(ignoreErr bool, args ...string) error {
	var env []string
	if i.pinConfigDir() {
		env = []string{i.kit.configEnv + "=" + i.targetDir}
	}
	if err := i.runner.run(i.agentBin, args, env); err != nil && !ignoreErr {
		return err
	}
	return nil
}

// pinConfigDir reports whether the agent CLI must be told where its config lives.
//
// A sandbox (or an explicit --config-dir) is not a place the agent looks on its
// own, so registration there only lands correctly with the env var set — and
// `aiagentmemory run <name>` exports the same variable at launch, so what the
// install wrote is what the launch reads.
//
// The GLOBAL install is the opposite case, and pinning it is actively wrong for
// Claude: CLAUDE_CONFIG_DIR=~/.claude moves the MCP registry from ~/.claude.json
// to ~/.claude/.claude.json, and a later plain `claude` — with no such variable
// exported — reads ~/.claude.json and finds no agentsmemory server. The install
// reports success, the agent has no am_* tools, and the memory protocol then runs
// its whole "tools are absent" ceremony against a server that is running fine.
// Leaving the environment alone lets the agent resolve its own default, which is
// exactly what a global install means.
func (i *Installer) pinConfigDir() bool {
	return i.targetDir != i.kit.globalConfigDir(homeDir())
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
	// A self-hosted server issues no tokens of its own, so prompting for one would
	// ask a question that usually has no answer: `agentsmemory --local` requires a
	// credential only when it was started with --token, and the common loopback and
	// --socket installs never are. So local mode takes an explicitly supplied token
	// (which is meaningful now that the server can check one) but never asks for
	// one, and an absent token stays the expected state rather than a skip.
	if i.local {
		return ""
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
	fmt.Fprintf(i.out, "agent       : %s\n", i.kit.name)
	fmt.Fprintf(i.out, "mode        : %s\n", i.modeLabel())
	fmt.Fprintf(i.out, "config dir  : %s\n", i.targetDir)
	fmt.Fprintf(i.out, "agent CLI   : %s\n", i.agentBin)
	fmt.Fprintf(i.out, "extensions  : %s\n", i.extensionsLabel())
	if i.dryRun {
		fmt.Fprintln(i.out, "dry-run     : no files written, no commands run")
	}
}

// modeLabel names the install mode for the banner.
func (i *Installer) modeLabel() string {
	if i.sandboxName != "" {
		return "isolated sandbox " + i.sandboxName
	}
	return fmt.Sprintf("global (wrap your existing %s)", i.kit.name)
}

// commandLabel renders how an installed command file is invoked in this agent —
// "/M" on Claude, "/prompts:M" on codex, since codex namespaces prompt files.
func (i *Installer) commandLabel(assetName string) string {
	return strings.Replace(i.kit.commandHint, "M", strings.TrimSuffix(assetName, ".md"), 1)
}

// extensionsLabel describes whether the recommended extensions are included.
func (i *Installer) extensionsLabel() string {
	if i.kit.name == agentPi {
		return "core only (pi takes neither a stdio MCP nor Claude plugins)"
	}
	if i.recommended {
		return "core + recommended (" + extensionsList(i.kit) + ")"
	}
	return "core only"
}

// extensionsList names the companion extensions --recommended installs for the
// kit. The codex review plugin lives in a Claude plugin marketplace, so a codex
// install gets the codebase-memory MCP only.
func extensionsList(kit agentKit) string {
	if kit.name == agentCodex {
		return "codebase-memory"
	}
	return "codebase-memory, codex"
}

func (i *Installer) step(title string)       { fmt.Fprintf(i.out, "\n> %s\n", title) }
func (i *Installer) ok(f string, a ...any)   { fmt.Fprintf(i.out, "  [ok] "+f+"\n", a...) }
func (i *Installer) warn(f string, a ...any) { fmt.Fprintf(i.out, "  [!!] "+f+"\n", a...) }

// summary prints the closing next-steps block, tailored to the agent and the
// install mode. The codex lines carry the token env var, which codex reads from
// its environment rather than from its config.
func (i *Installer) summary() {
	fmt.Fprintln(i.out)
	fmt.Fprintln(i.out, "Next steps:")
	if i.sandboxName != "" {
		fmt.Fprintf(i.out, "  - launch it in this sandbox:  aiagentmemory run --agent %s %s\n", i.kit.name, i.sandboxName)
	} else {
		what := "the new commands + hook"
		if i.kit.commandsDir == "" && i.kit.hooksFile == "" {
			what = "the memory tools"
		}
		fmt.Fprintf(i.out, "  - restart %s to pick up %s\n", i.kit.name, what)
	}
	// Both of these assume the agent HAS a memory file and slash commands. Claude
	// Desktop has neither, and interpolating its commandHint into them printed
	// "auto-loads every session via  — no need to type Claude Desktop has no slash
	// commands…". A summary that garbles itself is how an install stops being read.
	if i.kit.memoryFile != "" {
		fmt.Fprintf(i.out, "  - the memory protocol auto-loads every session via %s — no need to type %s\n",
			i.kit.memoryFile, i.commandLabel("am.md"))
	} else if i.kit.rulesFile != "" {
		fmt.Fprintf(i.out, "  - the memory protocol auto-loads every session from %s\n", i.kit.rulesFile)
	} else {
		fmt.Fprintf(i.out, "  - %s holds no protocol file; the server sends its instructions on the MCP handshake\n", i.kit.name)
	}
	if i.kit.commandsDir != "" {
		fmt.Fprintf(i.out, "  - run %s or %s with a task to run the full grounding sequence on demand\n",
			i.commandLabel("am.md"), i.commandLabel("load-skill.md"))
	}

	if i.wing != "" {
		fmt.Fprintf(i.out, "  - memories from this project file into %s on their own — no wing argument needed\n", i.wing)
	}

	if i.local {
		// The self-hosted server is the one thing that has to be running for any of
		// this to work, and nothing else in the output would say so. The reminder
		// must echo the transport that was actually registered: telling a socket
		// install to run the server on a port would wire up a bridge that dials a
		// socket nothing is listening on.
		if i.socket != "" {
			fmt.Fprintf(i.out, "  - keep your server running: agentsmemory --local --socket %s   (this install bridges to that socket over stdio)\n", i.socket)
		} else {
			// Echo --token when one was registered: the agent will now send a bearer,
			// and a server started without the matching flag answers 401 on every
			// call, which reads as a broken install rather than a missing flag.
			//
			// Named as the env var, not the value — same policy as redactArgs, which
			// keeps --dry-run from echoing a token into a terminal or captured log.
			// A shell that exported it substitutes the real value, so the line stays
			// copy-pasteable either way.
			serverToken := ""
			if i.resolvedToken != "" {
				serverToken = ` --token "$` + localTokenEnvVar + `"`
			}
			fmt.Fprintf(i.out, "  - keep your server running: agentsmemory --local%s   (this install points at %s)\n", serverToken, i.mcpURL)
		}
	}

	if i.kit.name == agentPi {
		fmt.Fprintln(i.out, "  - pi has no MCP client: the memory tools arrive through the bridge extension in extensions/")
		if i.sandboxName != "" {
			// PI_CODING_AGENT_DIR relocates the whole agent dir, and pi keeps
			// auth.json there — so an isolated config starts with no provider
			// credentials of its own.
			fmt.Fprintf(i.out, "  - a sandbox has its own auth.json: sign in inside it, or pass a provider key (PI_CODING_AGENT_DIR=%s pi)\n", i.targetDir)
		}
		what := "the token"
		if i.resolvedToken == "" {
			what = "the endpoint" // no token was written; the file carries the URL
		}
		fmt.Fprintf(i.out, "  - launching plain `pi`? export %s first, e.g. add to your shell rc:\n", what)
		fmt.Fprintf(i.out, "      set -a; . %s; set +a\n", i.tokenPath())
		return
	}

	if i.kit.name != agentCodex {
		return
	}
	if i.sandboxName != "" {
		// A sandbox is a whole CODEX_HOME, and codex keeps auth.json there — so an
		// isolated config starts logged out and every request 401s until you say so.
		fmt.Fprintf(i.out, "  - a sandbox has its own login: CODEX_HOME=%s codex login\n", i.targetDir)
	}
	// Only meaningful when a token was actually persisted; without one no file was
	// written, and codex reads the endpoint straight out of its own config.toml.
	if i.resolvedToken == "" {
		return
	}
	fmt.Fprintf(i.out, "  - launching plain `codex`? export the token first, e.g. add to your shell rc:\n")
	fmt.Fprintf(i.out, "      set -a; . %s; set +a\n", i.tokenPath())
}
