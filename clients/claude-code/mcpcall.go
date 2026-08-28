// mcpcall.go implements the `mcp` subcommand: a read-only shell window onto the
// remote agentsmemory MCP, so a customer can see exactly what a tool returns
// without going through an agent — `aiagentmemory mcp status`,
// `aiagentmemory mcp search "auth bug" -a limit=3`.
//
// It is the customer-side twin of the server's `agentsmemory mcp` CLI
// (cmd/server/mcp.go). Both consume the production tools/list contract and call
// the production handlers; only the transport differs. The server CLI connects
// in process to its own SQLite-backed server, while this one uses Streamable
// HTTP with the workspace bearer token the installer wires into agents.
//
// Two properties shape the design:
//
//   - Passthrough, not hand-written subcommands. The catalogue, each tool's
//     arguments, and the primary positional all come from the live tools/list, so
//     a tool added server-side is callable without shipping a new binary.
//   - Read-only by construction. The remote endpoint exposes writes too, but a
//     mistyped shell command must never mutate team memory, so calls are gated by
//     the readOnlyHint on the live tools/list entry. A missing or false hint is
//     refused, so an unclassified server tool cannot become writable by accident.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/urfave/cli/v3"
)

// mcpCommand builds the `mcp` subcommand. With no tool it prints the catalogue;
// with one it calls the tool and prints what came back.
func mcpCommand() *cli.Command {
	return &cli.Command{
		Name:      "mcp",
		Usage:     "call a read-only memory tool on the remote MCP (run with no tool to list them)",
		ArgsUsage: "[tool] [primary-arg]",
		Description: "List the tools:     aiagentmemory mcp\n" +
			"Call one:           aiagentmemory mcp status\n" +
			"With an argument:   aiagentmemory mcp search \"auth bug\"\n" +
			"With more:          aiagentmemory mcp search \"auth bug\" -a limit=3 -a wing=wing_x\n" +
			"Pipe it:            aiagentmemory mcp search \"auth bug\" | jq '.hits[].room'\n\n" +
			"The bare positional fills the tool's first required argument, so `mcp search \"x\"`\n" +
			"means `-a query=x`. Writes are refused: this is a read-only window on the palace.\n\n" +
			"The workspace token is taken from --token/$AGENTSMEMORY_TOKEN, else from an\n" +
			"install already on this machine (--sandbox <name> selects one).",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "arg",
				Aliases: []string{"a"},
				Usage:   "tool argument as key=value (repeatable)",
			},
			&cli.StringFlag{
				Name:    "token",
				Sources: cli.EnvVars(tokenEnvVar),
				Usage:   "agentsmemory workspace API token (default: read from an install on this machine)",
			},
			&cli.StringFlag{
				Name:    "mcp-url",
				Sources: cli.EnvVars(mcpURLEnvVar),
				Value:   defaultMCPURL,
				Usage:   "agentsmemory remote MCP endpoint",
			},
			&cli.StringFlag{
				Name:  "sandbox",
				Usage: "read the token from the sandbox install at ~/.sandboxes/<name>",
			},
			&cli.StringFlag{
				Name:    "config-dir",
				Aliases: []string{"claude-dir"},
				Usage:   "read the token from an install in this config dir",
			},
			&cli.BoolFlag{
				Name:  "raw",
				Usage: "print the whole MCP envelope (content blocks, isError) instead of just the result",
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Value: 60 * time.Second,
				Usage: "give up on the endpoint after this long",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runRemoteMCP(ctx, c, os.Stdout)
		},
	}
}

// runRemoteMCP performs one CLI invocation: resolve the token, open the MCP
// session, then either print the catalogue or call the named tool.
//
// Only tool output goes to out (stdout); every diagnostic goes to stderr, so
// `aiagentmemory mcp search q | jq` keeps working.
func runRemoteMCP(ctx context.Context, c *cli.Command, out io.Writer) error {
	token, source, err := resolveWorkspaceToken(c)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "aiagentmemory: token from %s\n", source)

	ctx, cancel := context.WithTimeout(ctx, c.Duration("timeout"))
	defer cancel()

	session, err := dialMCP(ctx, c.String("mcp-url"), token, c.Duration("timeout"))
	if err != nil {
		return err
	}
	defer session.Close()

	endpoint := mcpcli.Endpoint{
		ListTools: func(callCtx context.Context) ([]mcp.Tool, error) {
			result, err := session.ListTools(callCtx, mcp.ListToolsRequest{})
			if err != nil {
				return nil, err
			}
			return result.Tools, nil
		},
		CallTool: func(callCtx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return session.CallTool(callCtx, request)
		},
	}
	return mcpcli.Run(ctx, out, endpoint, mcpcli.Invocation{
		Tool:     c.Args().First(),
		ArgFlags: c.StringSlice("arg"),
		Tail:     mcpcli.TailArgs(c.Args().Slice()),
		Raw:      c.Bool("raw"),
	})
}

// dialMCP opens and initialises a Streamable-HTTP MCP session against url,
// authenticated with the workspace bearer token — the same handshake the pi
// bridge extension performs (clients/claude-code/extensions/agentsmemory.ts).
func dialMCP(ctx context.Context, url, token string, timeout time.Duration) (*client.Client, error) {
	// ⚠ NO TOKEN MEANS NO HEADER, not an empty one. `Authorization: Bearer ` with
	// nothing after it is a credential that was offered and is blank — a server is
	// entitled to reject it, and neither ours nor the hosted one is tested against
	// that shape. Omitting the header is the case a --local server already answers:
	// its MCP registration carries no headers at all.
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	c, err := client.NewStreamableHttpClient(url,
		transport.WithHTTPHeaders(headers),
		transport.WithHTTPTimeout(timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", url, err)
	}
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("connect %s: %w", url, err)
	}

	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "aiagentmemory-cli", Version: version}
	if _, err := c.Initialize(ctx, init); err != nil {
		c.Close()
		// A bad or revoked token surfaces here as an HTTP 401 from the endpoint.
		return nil, fmt.Errorf("initialize %s: %w", url, err)
	}
	return c, nil
}

// resolveWorkspaceToken finds the token to authenticate with and describes where
// it came from (for the stderr note — a call made with a different workspace's
// token is otherwise a confusing empty result).
//
// The flag/env wins; otherwise an install already on this machine is read, since
// the token was typed once during `install` and asking for it again on every
// shell command would make this feature useless in practice.
func resolveWorkspaceToken(c *cli.Command) (token, source string, err error) {
	if t := c.String("token"); t != "" {
		return t, "--token/$" + tokenEnvVar, nil
	}

	dirs := tokenSearchDirs(c)
	for _, dir := range dirs {
		if t, from := tokenFromConfigDir(dir); t != "" {
			return t, from, nil
		}
	}

	// ⚠ A LOOPBACK SERVER IS NOT ASKED FOR A CREDENTIAL IT DOES NOT WANT. An
	// `install --local` populates NONE of the four token sources by design — its
	// own --help says "no token is prompted for" — so this used to refuse every
	// call against a server that accepts no credentials at all: a client-side gate
	// with nothing behind it. Measured 2026-08-28: every shipped hook was silent on
	// a --local install for exactly this reason, including ADR-041 T4's recall.
	//
	// A token still WINS when one is configured, because a --local server may have
	// been started with one.
	if isLoopbackEndpoint(c.String("mcp-url")) {
		return "", "a loopback server, which needs none", nil
	}

	return "", "", fmt.Errorf("no workspace token found: pass --token (or set %s), or point at an install with --sandbox <name>/--config-dir <dir>; looked in %s",
		tokenEnvVar, strings.Join(dirs, ", "))
}

// isLoopbackEndpoint reports whether the endpoint addresses this machine.
//
// ⚠ IT PARSES THE HOST; IT DOES NOT SUBSTRING-MATCH THE URL. `strings.Contains(url,
// "localhost")` is the obvious version and it accepts
// http://localhost.example.invalid/mcp — a REMOTE host — which would drop
// authentication on somebody else's server. The whole point of this function is
// to decide when it is safe to send no credential, so the one thing it must not
// do is be generous about what counts as local.
func isLoopbackEndpoint(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// tokenSearchDirs lists the config dirs to look for a token in. An explicit
// --config-dir or --sandbox selects exactly one — naming a sandbox and silently
// falling back to a different workspace's token would be worse than failing.
// With neither, the global installs of the three agents are searched, plus $HOME
// itself because that is where Claude keeps user-scope MCP servers when
// CLAUDE_CONFIG_DIR is unset.
func tokenSearchDirs(c *cli.Command) []string {
	if dir := c.String("config-dir"); dir != "" {
		return []string{dir}
	}
	if name := c.String("sandbox"); name != "" {
		return []string{sandboxDir(name)}
	}
	home := homeDir()
	return []string{
		home,
		claudeKit.globalConfigDir(home),
		codexKit.globalConfigDir(home),
		piKit.globalConfigDir(home),
	}
}

// tokenFromConfigDir reads the workspace token out of one config dir, trying
// both shapes the installer produces: agentsmemory.env (written for codex and
// pi, which take the token through an env var) and the agent's own MCP
// registration in .claude.json (Claude, where the token is embedded in the
// Authorization header). It returns the token and the file it came from.
func tokenFromConfigDir(dir string) (token, source string) {
	envPath := filepath.Join(dir, tokenFile)
	if t := tokenFromEnvFile(envPath); t != "" {
		return t, envPath
	}
	jsonPath := filepath.Join(dir, ".claude.json")
	if t := tokenFromClaudeJSON(jsonPath); t != "" {
		return t, jsonPath
	}
	return "", ""
}

// tokenFromEnvFile pulls AGENTSMEMORY_TOKEN out of an agentsmemory.env file
// (KEY=value lines, 0600, written by registerCodexMCP/registerPiMCP).
func tokenFromEnvFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == tokenEnvVar {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// tokenFromClaudeJSON pulls the bearer token out of a Claude config's
// agentsmemory MCP registration — mcpServers.agentsmemory.headers.Authorization,
// which registerClaudeMCP filled with "Bearer <token>".
func tokenFromClaudeJSON(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "" // a config we cannot parse is one we have no token in
	}
	auth := cfg.MCPServers[mcpName].Headers["Authorization"]
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}
