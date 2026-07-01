package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

// version is stamped at build time via -ldflags "-X main.version=<tag>". The
// release workflow sets it from the git tag; a plain `go build` leaves "dev".
var version = "dev"

const (
	// defaultMCPURL is the agentsmemory remote MCP endpoint the installer wires
	// up. It is a stateless Streamable-HTTP MCP server authed by a per-workspace
	// bearer token (see the README "Connect the MCP" section).
	defaultMCPURL = "https://aiagentmemory.dev/mcp"

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
			runCommand(),
			wrapCommand(),
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
		Usage: "install the kit globally (~/.claude) or into an isolated --sandbox",
		Description: "Global (default):   aiagentmemory install\n" +
			"Isolated sandbox:   aiagentmemory install --sandbox <name> [--recommended]\n\n" +
			"The default install wires up our slash commands, the Stop hook, and the\n" +
			"agentsmemory MCP. --recommended additionally installs the codebase-memory\n" +
			"MCP and the eidos and codex plugins.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "sandbox",
				Usage: "install into an isolated config at ~/.sandboxes/<name> instead of the global ~/.claude",
			},
			&cli.StringFlag{
				Name:  "claude-dir",
				Usage: "override the target Claude config dir (ignored when --sandbox is set)",
			},
			&cli.BoolFlag{
				Name:  "recommended",
				Usage: "also install the recommended extensions: codebase-memory MCP, eidos + codex plugins",
			},
			&cli.StringFlag{
				Name:    "token",
				Sources: cli.EnvVars("AGENTSMEMORY_TOKEN"),
				Usage:   "agentsmemory workspace API token for the remote MCP (prompted if omitted)",
			},
			&cli.StringFlag{
				Name:  "mcp-url",
				Value: defaultMCPURL,
				Usage: "agentsmemory remote MCP endpoint",
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
			inst, err := newInstaller(c, os.Stdout, os.Stdin)
			if err != nil {
				return err
			}
			return inst.run()
		},
	}
}

// runCommand builds `run <name> [claude args...]` — launch Claude against an
// isolated sandbox. SkipFlagParsing forwards every argument after the sandbox
// name to Claude untouched, so `run foo -p "hi"` reaches Claude as `-p "hi"`.
func runCommand() *cli.Command {
	return &cli.Command{
		Name:            "run",
		Usage:           "run Claude against a sandbox: aiagentmemory run <name> [claude args...]",
		ArgsUsage:       "<name> [claude args...]",
		SkipFlagParsing: true,
		Action: func(_ context.Context, c *cli.Command) error {
			args := c.Args().Slice()
			if len(args) == 0 {
				return errors.New("run: missing sandbox name (usage: aiagentmemory run <name> [claude args...])")
			}
			name := args[0]
			if err := validSandboxName(name); err != nil {
				return err
			}
			return wrapClaude(sandboxDir(name), args[1:])
		},
	}
}

// wrapCommand builds `wrap [claude args...]` — launch Claude against the default
// global config (~/.claude). It is the "global mode" counterpart to run: same
// passthrough, but no sandbox and no CLAUDE_CONFIG_DIR override.
func wrapCommand() *cli.Command {
	return &cli.Command{
		Name:            "wrap",
		Usage:           "run Claude against the global config: aiagentmemory wrap [claude args...]",
		ArgsUsage:       "[claude args...]",
		SkipFlagParsing: true,
		Action: func(_ context.Context, c *cli.Command) error {
			// Empty configDir → let Claude use its own default; don't set CLAUDE_CONFIG_DIR.
			return wrapClaude("", c.Args().Slice())
		},
	}
}
