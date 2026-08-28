package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/urfave/cli/v3"
)

// version is stamped at build time via -ldflags "-X main.version=<tag>". The
// release workflow sets it from the git tag; a plain `go build` leaves "dev".
var version = "dev"

const (
	// defaultMCPURL is the agentsmemory remote MCP endpoint the installer wires
	// up. It is a stateless Streamable-HTTP MCP server authed by a per-workspace
	// bearer token (see the README "Connect the MCP" section).
	defaultMCPURL = mcpprotocol.HostedMCPURL

	// localMCPURL is the endpoint --local wires up instead: a self-hosted server
	// running `agentsmemory --local`, which serves one workspace over an
	// unauthenticated /mcp on the loopback interface. The port matches that
	// server's own default (and the published port in its docker-compose.yml), so
	// the common case needs no --mcp-url at all.
	localMCPURL = "http://localhost:8080/mcp"

	// mcpName and codebaseMemoryName are the server names registered with the
	// Claude CLI. A server name doubles as the tool prefix (mcp__<name>__<tool>),
	// which the /am and /M commands reference, so these must stay stable.
	mcpName            = "agentsmemory"
	codebaseMemoryName = "codebasememory"

	// codebaseMemoryInstall is the upstream one-liner that drops the
	// codebase-memory-mcp binary into ~/.local/bin. Run only with --recommended.
	codebaseMemoryInstall = "curl -fsSL https://raw.githubusercontent.com/DeusData/codebase-memory-mcp/main/install.sh | bash"

	// codebaseMemoryBin is where that upstream script installs its binary; we
	// register it with the Claude CLI as a stdio MCP server.
	codebaseMemoryBin = "~/.local/bin/codebase-memory-mcp"
)

// main builds the CLI and dispatches. Errors are printed to stderr with a
// non-zero exit so the curl|bash installer and shell callers can detect failure.
func main() {
	cmd := &cli.Command{
		Name:    "aiagentmemory",
		Usage:   "install the agentsmemory Claude Code kit and wrap Claude with per-project sandboxes",
		Version: version,
		Commands: []*cli.Command{
			installCommand(),
			verifyCommand(),
			mineClaudeCommand(),
			updateCommand(),
			updateSkillCommand(),
			initCommand(),
			loadCommand(),
			runCommand(),
			wrapCommand(),
			mcpCommand(),
			recallObserveCommand(),
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "aiagentmemory:", err)
		os.Exit(1)
	}
}

// installCommand builds the `install` subcommand. With no --sandbox it performs
// a global install into ~/.claude (wrap your existing Claude with our MCP); with
// --sandbox <name> it installs an isolated config under ~/.sandboxes/<name>.
func installCommand() *cli.Command {
	return &cli.Command{
		Name:  "install",
		Usage: "install the kit globally (~/.claude, ~/.codex, ~/.pi/agent) or into an isolated --sandbox",
		Description: "Global (default):   aiagentmemory install\n" +
			"Isolated sandbox:   aiagentmemory install --sandbox <name> [--recommended]\n" +
			"Codex instead:      aiagentmemory install --agent codex\n" +
			"pi instead:         aiagentmemory install --agent pi\n" +
			"Claude + codex:     aiagentmemory install --agent both\n" +
			"Every agent:        aiagentmemory install --agent all\n\n" +
			"The default install wires up our slash commands, the Stop hook, and the\n" +
			"agentsmemory MCP. --recommended additionally installs the codebase-memory\n" +
			"MCP and (Claude only) the codex review plugin. pi has no MCP client and\n" +
			"no hooks, so it gets a bridge extension that provides both.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "agent",
				Value: agentClaude,
				Usage: "agent CLI to install for: claude | codex | pi | both (claude+codex) | all",
			},
			&cli.BoolFlag{
				Name:  "global",
				Usage: "install into the agent's global config dir non-interactively (skips the mode prompt); mutually exclusive with --sandbox/--claude-dir",
			},
			&cli.BoolFlag{
				Name: "local",
				Usage: "wire up a self-hosted `agentsmemory --local` server (" + localMCPURL + ") instead of the hosted service: " +
					"no token is prompted for (pass --token only if the server was started with one), " +
					"and the install goes global unless --sandbox/--config-dir says otherwise",
			},
			&cli.StringFlag{
				Name:  "sandbox",
				Usage: "install into an isolated config at ~/.sandboxes/<name> instead of the global ~/.claude",
			},
			&cli.StringFlag{
				Name: "wing",
				Usage: "file this project's memories into this wing. It rides as a header on every MCP call, so writes " +
					"land in the right project even when the agent passes no wing — pair it with --scope project to keep " +
					"the registration in this repo. The installer uses each client's supported registration channel " +
					"(header, URL query, bridge flag, or pi environment) rather than dropping the scope",
			},
			&cli.StringFlag{
				Name: "claude-dir",
				// config-dir is the name that reads right for codex; claude-dir stays
				// the primary so existing scripts and docs keep working.
				Aliases: []string{"config-dir"},
				Usage:   "override the target agent config dir (ignored when --sandbox is set)",
			},
			&cli.BoolFlag{
				Name: "copy",
				Usage: "seed the sandbox from the agent's global config: logins, MCP servers, plugins, skills and settings " +
					"(no history, logs or caches). Requires --sandbox or --config-dir",
			},
			&cli.BoolFlag{
				Name: "shared-auth",
				Usage: "link the sandbox's credential files to the agent's global config, so one login serves every sandbox " +
					"(Claude on macOS already shares its keychain). Requires --sandbox or --config-dir",
			},
			&cli.BoolFlag{
				Name:  "recommended",
				Usage: "also install the recommended extensions: codebase-memory MCP + codex review plugin",
			},
			&cli.StringFlag{
				Name: "token",
				// AGENTSMEMORY_LOCAL_TOKEN comes first so it wins in a shell that also
				// holds a hosted workspace key: it is the same variable the server
				// reads for its --token, so exporting it once configures both halves.
				Sources: cli.EnvVars(localTokenEnvVar, tokenEnvVar),
				Usage:   "bearer token to present: the hosted workspace API token (prompted if omitted), or with --local the token the self-hosted server was started with",
			},
			&cli.StringFlag{
				Name:  "mcp-url",
				Value: defaultMCPURL,
				Usage: "agentsmemory remote MCP endpoint",
			},
			&cli.StringFlag{
				Name:    "socket",
				Sources: cli.EnvVars(socketEnvVar),
				Usage: "register the MCP over stdio against a --local server listening on this Unix socket " +
					"(the socket the server was started with); requires --local and replaces --mcp-url",
			},
			&cli.StringFlag{
				Name:    "server-bin",
				Sources: cli.EnvVars("AIAGENTMEMORY_SERVER_BIN"),
				Usage:   "agentsmemory server binary the --socket stdio bridge spawns (default: found on PATH)",
			},
			&cli.StringFlag{
				Name:  "scope",
				Value: "user",
				Usage: "Claude MCP/plugin scope: user | local | project",
			},
			&cli.StringFlag{
				Name:    "claude-bin",
				Sources: cli.EnvVars("AIAGENTMEMORY_CLAUDE_BIN"),
				Usage:   "Claude CLI binary to drive (default: claude)",
			},
			&cli.StringFlag{
				Name:    "codex-bin",
				Sources: cli.EnvVars("AIAGENTMEMORY_CODEX_BIN"),
				Usage:   "codex CLI binary to drive (default: codex)",
			},
			&cli.StringFlag{
				Name:    "pi-bin",
				Sources: cli.EnvVars("AIAGENTMEMORY_PI_BIN"),
				Usage:   "pi CLI binary to drive (default: pi)",
			},
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "non-interactive: never prompt (skip the token prompt if none supplied)",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "print what would happen without writing files or running commands",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			// --agent both installs the same kit assets into each agent's config in
			// turn; one failing agent aborts rather than half-installing silently.
			kits, err := resolveAgentKits(c.String("agent"))
			if err != nil {
				return err
			}
			for _, kit := range kits {
				inst, err := newInstaller(kit, c, os.Stdout, os.Stdin)
				if err != nil {
					return err
				}
				if err := inst.run(); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// initCommand builds `init` — record how this project should be launched, so
// everyone working on it types one command instead of remembering a sandbox name
// and a flag list.
//
// The record is deliberately split in two. The agent and its flags are a
// team-wide decision and go into ./.aiagentmemory, which is meant to be
// committed. The sandbox name is a fact about THIS machine — a teammate names
// their sandbox differently — so it goes into ~/.sandboxes/agents keyed by the
// project's absolute path, where it needs no .gitignore entry and can never
// travel to someone else's clone.
func initCommand() *cli.Command {
	return &cli.Command{
		Name:      "init",
		Usage:     "record this project's launch config: aiagentmemory init [--sandbox <name>] [--agent claude] [-- agent args...]",
		ArgsUsage: "[-- agent args...]",
		Description: "Pin a project to a sandbox:  aiagentmemory init --sandbox acme\n" +
			"With agent flags:            aiagentmemory init --sandbox acme -- --model opus\n" +
			"For codex instead:           aiagentmemory init --sandbox acme --agent codex\n\n" +
			"Writes ./" + projectConfigFile + " (agent + flags, safe to commit) and records the\n" +
			"sandbox in ~/.sandboxes/" + agentRegistryFile + " (this machine only). Everything after --\n" +
			"is stored verbatim and handed to the agent by `aiagentmemory load`.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "sandbox",
				Usage: "sandbox this project launches with, recorded in ~/.sandboxes/" + agentRegistryFile + " (this machine only)",
			},
			&cli.StringFlag{
				Name:  "agent",
				Usage: "agent CLI to launch: claude | codex | pi (recorded in " + projectConfigFile + ")",
			},
			&cli.StringFlag{
				Name:  "wing",
				Usage: "memory wing this project's drawers and diary entries are filed under (recorded in " + projectConfigFile + "; default: derived from the git remote)",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine the project directory: %w", err)
			}

			// Validate both names now rather than at launch: init is where the
			// user is looking, and a typo caught here saves a confusing failure
			// from `load` days later.
			agent := c.String("agent")
			if agent != "" {
				kit, err := resolveAgentKit(agent)
				if err != nil {
					return err
				}
				agent = kit.name
			}
			sandbox := c.String("sandbox")
			if sandbox != "" {
				if err := validSandboxName(sandbox); err != nil {
					return err
				}
			}

			cfg := projectConfig{agent: agent, args: c.Args().Slice(), wing: c.String("wing")}
			path := filepath.Join(dir, projectConfigFile)
			if err := os.WriteFile(path, renderProjectConfig(cfg), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			fmt.Printf("wrote %s (agent %s)\n", projectConfigFile, agentOrDefault(agent))
			if cfg.wing != "" {
				fmt.Printf("  memory wing: %s\n", cfg.wing)
			}
			if len(cfg.args) > 0 {
				fmt.Printf("  agent flags: %s\n", formatArgs(cfg.args))
			}

			if sandbox == "" {
				fmt.Printf("no --sandbox given: set yours with `aiagentmemory init --sandbox <name>` before `load`\n")
				return nil
			}
			if err := writeAgentRegistry(dir, sandbox); err != nil {
				return err
			}
			fmt.Printf("recorded sandbox %q for %s in ~/.sandboxes/%s\n", sandbox, dir, agentRegistryFile)

			// A missing sandbox is a warning, not a failure: recording intent
			// before creating the sandbox is a legitimate order to work in, and
			// `load` is the command that refuses to launch without one.
			if !dirExists(sandboxDir(sandbox)) {
				fmt.Fprintf(os.Stderr, "aiagentmemory: warning — sandbox %q does not exist yet\n", sandbox)
				fmt.Fprintf(os.Stderr, "  create it: aiagentmemory install --agent %s --sandbox %s\n", agentOrDefault(agent), sandbox)
				return nil
			}
			fmt.Printf("launch it with: aiagentmemory load\n")
			return nil
		},
	}
}

// loadCommand builds `load` — launch the agent this project was `init`ed with.
// It resolves the sandbox across every layer (see resolveLaunch) and then hands
// off to the same planRun/execAgent path `run` uses, so environment passthrough,
// the workspace token hand-off and the shared-auth warning behave identically.
func loadCommand() *cli.Command {
	return &cli.Command{
		Name:      "load",
		Usage:     "launch the agent recorded for this project: aiagentmemory load [-- extra agent args...]",
		ArgsUsage: "[-- extra agent args...]",
		Description: "Reads ./" + projectConfigFile + " for the agent and its flags, and resolves the\n" +
			"sandbox in this order, most specific first:\n" +
			"  --sandbox  >  $" + sandboxEnvVar + "  >  ~/.sandboxes/" + agentRegistryFile + "  >  " + projectLocalFile + "  >  " + projectConfigFile + "\n\n" +
			"Arguments after -- are appended to the recorded flags, so they win when\n" +
			"the same flag appears in both.\n\n" +
			"The project's memory wing resolves as $" + wingEnvVar + "  >  " + projectLocalFile +
			"  >  " + projectConfigFile + ",\n" +
			"and is exported to the agent as $" + wingEnvVar + ". Unset means the memory protocol\n" +
			"derives one from the git remote.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "sandbox",
				Usage: "launch this sandbox instead of the recorded one (overrides every file)",
			},
			&cli.StringFlag{
				Name:  "agent",
				Usage: "launch this agent instead of the recorded one: claude | codex | pi",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			dir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine the project directory: %w", err)
			}
			shared, local, projectDir := findProjectConfig(dir)
			// A missing registry is normal (nothing pinned yet on this machine),
			// so an unreadable one contributes no entry rather than failing the
			// launch — the layers below it may still resolve a sandbox.
			registry, _ := os.ReadFile(agentRegistryPath())

			res, err := resolveLaunch(launchInputs{
				flagSandbox: c.String("sandbox"),
				flagAgent:   c.String("agent"),
				envSandbox:  os.Getenv(sandboxEnvVar),
				registry:    lookupAgentRegistry(registry, dir),
				local:       local,
				shared:      shared,
				extraArgs:   c.Args().Slice(),
				envWing:     os.Getenv(wingEnvVar),
			})
			if err != nil {
				return err
			}
			kit, err := resolveAgentKit(res.agent)
			if err != nil {
				return err
			}

			plan, err := planRun(kit, res.sandbox, dirExists(sandboxDir(res.sandbox)))
			if err != nil {
				return err
			}
			// The wing rides along with the config dir: both are things the agent
			// cannot work out for itself from inside a sandbox.
			plan.wing = res.wing
			if plan.configDir == "" {
				// planRun falls back to launching an agent of the same name when
				// the sandbox is missing, which is right for `run claude` but
				// wrong here: a project pinned to a sandbox that no longer exists
				// must say so, not quietly run against the global config.
				return fmt.Errorf("sandbox %q (from %s) does not exist — create it with `aiagentmemory install --agent %s --sandbox %s`",
					res.sandbox, res.origin, kit.name, res.sandbox)
			}

			// stdout belongs to the agent we are about to become, so the summary
			// goes to stderr. It names the origin because five layers can supply
			// the sandbox and only one of them wins.
			fmt.Fprintf(os.Stderr, "aiagentmemory: %s in sandbox %s (from %s)\n", kit.name, res.sandbox, res.origin)
			if projectDir != "" && projectDir != dir {
				// Launching from a subdirectory is supported, but say which
				// project's flags were picked up — the answer is not on screen.
				fmt.Fprintf(os.Stderr, "  config: %s\n", filepath.Join(projectDir, projectConfigFile))
			}
			return execAgent(kit, plan, res.args)
		},
	}
}

// agentOrDefault names the agent for user-facing output, spelling out the
// default rather than printing an empty string when none was recorded.
func agentOrDefault(agent string) string {
	if agent == "" {
		return agentClaude
	}
	return agent
}

// runCommand builds `run [--agent codex] <name> [agent args...]` — launch an agent
// against an isolated sandbox. SkipFlagParsing forwards every argument after the
// sandbox name to the agent untouched, so `run foo -p "hi"` reaches it as
// `-p "hi"`; that is why --agent is hand-parsed from the front of the argument
// list instead of being declared as a flag.
//
// <name> is a sandbox first; if no such sandbox exists it may name an agent CLI
// (see planRun), which makes `aiagentmemory run claude` launch Claude globally
// rather than erroring on a sandbox nobody created. Either way the launched
// agent inherits the caller's environment, so `SET_NEW_ENV=1 aiagentmemory run
// …` passes that variable straight through.
func runCommand() *cli.Command {
	return &cli.Command{
		Name:            "run",
		Usage:           "run an agent against a sandbox: aiagentmemory run [--agent codex] <name> [agent args...]",
		ArgsUsage:       "[--agent claude|codex|pi] <name> [agent args...]",
		SkipFlagParsing: true,
		Action: func(_ context.Context, c *cli.Command) error {
			kit, args, err := takeAgentFlag(c.Args().Slice())
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return errors.New("run: missing sandbox name (usage: aiagentmemory run [--agent codex] <name> [agent args...])")
			}
			name := args[0]
			plan, err := planRun(kit, name, dirExists(sandboxDir(name)))
			if err != nil {
				return err
			}
			if plan.configDir == "" {
				// The fallback silently changes which config is in play, so say
				// so on stderr — stdout belongs to the agent we are about to become.
				fmt.Fprintf(os.Stderr, "aiagentmemory: no sandbox %q — launching %s with the global config\n", name, plan.bin)
			}
			return execAgent(kit, plan, args[1:])
		},
	}
}

// wrapCommand builds `wrap [--agent codex] [agent args...]` — launch the agent
// against its default global config (~/.claude, ~/.codex). It is the "global mode"
// counterpart to run: same passthrough, but no sandbox and no config-dir override.
func wrapCommand() *cli.Command {
	return &cli.Command{
		Name:            "wrap",
		Usage:           "run an agent against its global config: aiagentmemory wrap [--agent codex] [agent args...]",
		ArgsUsage:       "[--agent claude|codex|pi] [agent args...]",
		SkipFlagParsing: true,
		Action: func(_ context.Context, c *cli.Command) error {
			kit, args, err := takeAgentFlag(c.Args().Slice())
			if err != nil {
				return err
			}
			// Zero launchPlan → the kit's configured CLI with no config-dir
			// override, so the agent uses its own default config.
			return execAgent(kit, launchPlan{}, args)
		},
	}
}

// takeAgentFlag pulls a leading `--agent <name>` (or `--agent=<name>`) off args and
// resolves it to a kit, returning the remaining arguments. Only the leading
// position is claimed: everything after it belongs to the agent being launched, so
// `run foo --agent codex` passes those two words through untouched rather than
// re-steering the launch. Absent the flag, Claude stays the default.
func takeAgentFlag(args []string) (agentKit, []string, error) {
	if len(args) == 0 || !strings.HasPrefix(args[0], "--agent") {
		return claudeKit, args, nil
	}
	if name, ok := strings.CutPrefix(args[0], "--agent="); ok {
		kit, err := resolveAgentKit(name)
		return kit, args[1:], err
	}
	if args[0] != "--agent" {
		return claudeKit, args, nil // e.g. --agentfoo: not ours, pass it through
	}
	if len(args) < 2 {
		return agentKit{}, nil, errors.New("--agent needs a value: claude, codex or pi")
	}
	kit, err := resolveAgentKit(args[1])
	return kit, args[2:], err
}
