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
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"io"
	"net"
	"net/http"
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
		Usage:     "call a memory tool on the remote MCP (run with no tool to list them; writes need --write)",
		ArgsUsage: "[tool] [primary-arg|file.md]",
		Description: "List the tools:     aiagentmemory mcp\n" +
			"Call one:           aiagentmemory mcp status\n" +
			"With an argument:   aiagentmemory mcp search \"auth bug\"\n" +
			"With more:          aiagentmemory mcp search \"auth bug\" -a limit=3 -a wing=wing_x\n" +
			"Pipe it:            aiagentmemory mcp search \"auth bug\" | jq '.hits[].room'\n" +
			"Learn a tool:       aiagentmemory mcp update_skill --schema\n" +
			"Write from a file:  aiagentmemory mcp update_skill start-here.md --write\n\n" +
			"The bare positional fills the tool's first required argument, so `mcp search \"x\"`\n" +
			"means `-a query=x`. A positional ending in .md is read as a DOCUMENT instead:\n" +
			"its YAML frontmatter becomes named arguments and its body becomes the tool's\n" +
			"content, so a long memory never has to pass through an agent's context to be\n" +
			"written. Its rune count is reported on stderr against the 1600-rune chunk size.\n\n" +
			"Tools that write are refused unless you pass --write. `--schema` prints any\n" +
			"tool's arguments and a fillable template, and never calls it.\n\n" +
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
			&cli.IntFlag{
				Name:  "digest",
				Usage: "render a search page as a plain-text digest of at most this many characters — whole hits, one line per fact, a trailing 'N more' line — instead of the JSON page (what the recall hooks inject)",
			},
			&cli.BoolFlag{
				Name:  "write",
				Usage: "allow a tool that writes to the palace (refused without this)",
			},
			&cli.BoolFlag{
				Name:  "schema",
				Usage: "describe the tool's arguments and print a markdown template, instead of calling it",
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Value: 60 * time.Second,
				Usage: "give up on the endpoint after this long",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			// The root's Writer when a caller set one (the tests capture it), else
			// stdout — the same rule doctor follows, so the CLI test can read what
			// the digest printed.
			var out io.Writer = os.Stdout
			if w := c.Root().Writer; w != nil {
				out = w
			}
			return runRemoteMCP(ctx, c, out)
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
	fmt.Fprintf(os.Stderr, tokenNoticeFormat, source)

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
		Tool:        c.Args().First(),
		ArgFlags:    c.StringSlice("arg"),
		Tail:        mcpcli.TailArgs(c.Args().Slice()),
		Raw:         c.Bool("raw"),
		Digest:      c.Int("digest"),
		AllowWrites: c.Bool("write"),
		Schema:      c.Bool("schema"),
		Log:         os.Stderr,
	})
}

// dialMCP opens and initialises a Streamable-HTTP MCP session against url,
// authenticated with the workspace bearer token — the same handshake the pi
// bridge extension performs (clients/claude-code/extensions/agentsmemory.ts).
func dialMCP(ctx context.Context, url, token string, timeout time.Duration) (*client.Client, error) {
	c, _, err := dialMCPInit(ctx, url, token, timeout)
	return c, err
}

// dialMCPInit is dialMCP keeping the initialize result, for the one caller that
// reads it: doctor's server rung wants serverInfo.version, which is the string
// am_status and --version also carry (issue #70), and a second handshake just to
// read it would be a second connection for nothing.
func dialMCPInit(ctx context.Context, url, token string, timeout time.Duration) (*client.Client, *mcp.InitializeResult, error) {
	// ⚠ NO TOKEN MEANS NO HEADER, not an empty one. `Authorization: Bearer ` with
	// nothing after it is a credential that was offered and is blank — a server is
	// entitled to reject it, and neither ours nor the hosted one is tested against
	// that shape. Omitting the header is the case a --local server already answers:
	// its MCP registration carries no headers at all.
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	// WHO is calling, for the search_events row (ADR-054 T2). A shipped hook
	// exports AGENTSMEMORY_ORIGIN=hook:<its name> before calling this client, and
	// this is the ONE client every hook goes through, so the origin is set here
	// and never by anything an agent types. An empty variable sends no header at
	// all: an empty value would be an origin of '' claimed explicitly, which is
	// indistinguishable from the absent case and a byte on every call.
	if origin := os.Getenv(mcpprotocol.OriginEnvVar); origin != "" {
		headers[mcpprotocol.OriginHeader] = origin
	}

	// ⚠ A REDIRECT CAN MOVE THE REQUEST OFF THE HOST THE AUTH DECISION WAS MADE
	// ABOUT. resolveWorkspaceToken waives the credential because the ENDPOINT is
	// loopback, but mcp-go builds a bare &http.Client{} with no CheckRedirect, so
	// Go follows redirects by default: a loopback server answering 307 sends the
	// MCP request BODY on to whatever host it names. The waiver was for this
	// machine, and without this it silently extends to any host a redirect picks.
	//
	// Enforced only on the waived path, which is the one this change opened. With
	// a token, Go already strips Authorization across hosts.
	httpClient := &http.Client{}
	if token == "" {
		httpClient.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
			if !isLoopbackEndpoint(req.URL.String()) {
				return fmt.Errorf("refusing a redirect to %s: this request carries no credential "+
					"because %s is on this machine, and that waiver does not travel", req.URL.Host, url)
			}
			return nil
		}
	}

	c, err := client.NewStreamableHttpClient(url,
		transport.WithHTTPHeaders(headers),
		transport.WithHTTPTimeout(timeout),
		transport.WithHTTPBasicClient(httpClient),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connect %s: %w", url, err)
	}
	if err := c.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("connect %s: %w", url, err)
	}

	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "aiagentmemory-cli", Version: version}
	res, err := c.Initialize(ctx, init)
	if err != nil {
		c.Close()
		// A bad or revoked token surfaces here as an HTTP 401 from the endpoint.
		return nil, nil, fmt.Errorf("initialize %s: %w", url, err)
	}
	return c, res, nil
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

// tokenNoticeFormat is the stderr line `mcp` prints once the credential is
// resolved, before anything can fail. Named because the two recall hooks filter
// it out when naming the cause of a failed recall — a hook that reported
// `head -n1` of stderr named this notice as the error twice on 2026-09-05 — and
// a literal in a shell script is coupled to this one by nothing but
// TestTheHooksSkipTheNoticeTheCLIActuallyPrints, which renders this format and
// checks each hook's filter against the rendered line rather than against a
// second copy of the text.
const tokenNoticeFormat = "aiagentmemory: token from %s\n"
