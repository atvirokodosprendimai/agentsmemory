// stdio.go implements the `mcp-stdio` subcommand: a stdio-to-HTTP MCP proxy, so
// an agent that can only spawn a subprocess (Claude Code, Codex) can talk to a
// server that speaks Streamable HTTP over a TCP port or a Unix socket.
//
// It is a RAW JSON-RPC passthrough, and that is the design decision worth
// keeping. The proxy parses no method names and knows no tool catalogue: it
// moves one message in, one message out. A tool added to internal/mcpserver is
// therefore reachable over stdio the moment the server restarts, with no change
// here and no proxy rebuild — the same drift-proof property the customer CLI's
// mcp client earned by reading tools/list instead of hard-coding arguments.
//
// It is the third driving adapter over the one domain core: the HTTP server, the
// read-only CLI (mcp.go) and this bridge all end at the same palace services.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"github.com/urfave/cli/v3"
)

// mcpPath is the endpoint the MCP handler is mounted on in both serving modes.
// Over a Unix socket there is no URL for an operator to pass, so the path is
// fixed rather than configurable.
const mcpPath = "/mcp"

// defaultProxyURL is where the proxy looks when neither --socket nor --url is
// given: the loopback form of the server's own default listen address, which is
// the server a developer most likely has running.
const defaultProxyURL = "http://127.0.0.1:8080" + mcpPath

// socketURL is the URL used when dialing a Unix socket. The host is a
// placeholder — the dialer ignores it and connects to the socket path — but
// net/http still requires a syntactically valid absolute URL.
//
// ⚠ IT SAYS "localhost" AND MUST KEEP SAYING SOMETHING LOOPBACK. The placeholder
// is not only cosmetic any more: it becomes the Host header the server sees, and
// the ADR-049 rebind guard refuses an authority that does not name this machine.
// This used to read "unix", which meant the guard needed a special case for it —
// one that was forgotten once (every socket client got 403) and then had to be
// scoped to socket deployments. Naming a loopback host here deletes that special
// case instead of parameterising it.
// TestTheSocketPlaceholderIsAcceptedByTheGuard pins the two together.
const socketURL = "http://localhost" + mcpPath

// jsonRPCInternalError is the JSON-RPC 2.0 "Internal error" code, returned to
// the agent when this proxy cannot reach the server at all.
const jsonRPCInternalError = -32603

// stdioCommand builds the `mcp-stdio` subcommand.
//
// It deliberately takes no storage or embedding flags: unlike serve and the mcp
// CLI, this process never opens the database. It is a pipe, and the server on
// the other end owns all the state — which is what lets several agents share one
// server (and one SQLite writer) instead of each opening their own.
func stdioCommand(def config.Config) *cli.Command {
	return stdioCommandWithIO(def, os.Stdin, os.Stdout)
}

// stdioCommandWithIO builds the bridge around explicit streams so the real CLI
// flag-to-request wiring can be exercised without replacing process globals.
func stdioCommandWithIO(def config.Config, stdin io.Reader, stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "mcp-stdio",
		Usage: "Bridge stdio MCP to a running server, so 'claude mcp add' / 'codex mcp add' can spawn it",
		Description: "Speaks MCP over stdin/stdout and forwards every message to a running agentsmemory\n" +
			"server. Point it at a Unix socket (--socket, matching the server's own --socket)\n" +
			"or at an HTTP endpoint (--url), and register it with an agent:\n\n" +
			"  claude mcp add agentsmemory -- aiagentmemory-server mcp-stdio --socket /run/am.sock\n" +
			"  codex  mcp add agentsmemory -- aiagentmemory-server mcp-stdio --socket /run/am.sock",
		Flags: []cli.Flag{
			// Shares AGENTSMEMORY_SOCKET with serve so one exported variable
			// configures both halves of the pair.
			&cli.StringFlag{Name: "socket", Sources: cli.EnvVars(mcpprotocol.SocketEnvVar), Usage: "Unix socket the server is listening on (takes precedence over --url)"},
			&cli.StringFlag{Name: "url", Sources: cli.EnvVars(mcpprotocol.ProxyURLEnvVar), Value: defaultProxyURL, Usage: "MCP endpoint URL when not using a socket"},
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars(mcpprotocol.TokenEnvVar), Usage: "API key forwarded as a Bearer token (multi-tenant servers; --local needs none)"},
			&cli.StringFlag{Name: "wing", Sources: cli.EnvVars(mcpprotocol.WingEnvVar), Usage: "registration wing forwarded on every request (omit for workspace scope; use a tool argument of \"*\" for deliberate cross-wing calls)"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			up, err := newUpstream(c.String("socket"), c.String("url"), c.String("token"), c.String("wing"))
			if err != nil {
				return err
			}
			return runStdioProxy(ctx, up, stdin, stdout)
		},
	}
}

// upstream is the server this proxy forwards to: an HTTP client (which may be
// wired to dial a Unix socket), the endpoint URL, and an optional bearer token.
type upstream struct {
	client   *http.Client
	endpoint string
	token    string
	wing     string
}

// newUpstream builds the upstream from the socket/url/token/wing flags. A socket
// path wins over a URL: it is the more specific instruction, and the two cannot
// both be honoured.
func newUpstream(socketPath, rawURL, token, wing string) (*upstream, error) {
	up := &upstream{endpoint: rawURL, token: token, wing: wing}

	if socketPath == "" {
		if rawURL == "" {
			return nil, fmt.Errorf("no server to proxy to: pass --socket or --url")
		}
		// No Timeout is set on purpose. http.Client.Timeout bounds the whole
		// exchange including reading the body, which would truncate the SSE
		// stream the server opens whenever a tool emits notifications. Request
		// lifetime is governed by ctx instead, so the agent stays in control.
		up.client = &http.Client{Transport: telemetry.WrapTransport(nil)}
		return up, nil
	}

	up.endpoint = socketURL
	up.client = &http.Client{
		Transport: telemetry.WrapTransport(&http.Transport{
			// Every connection goes to the socket regardless of the URL's host,
			// which is why the endpoint above is a placeholder.
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		}),
	}
	return up, nil
}

// post sends one raw JSON-RPC message to the server and returns the live
// response for the caller to drain.
func (u *upstream) post(ctx context.Context, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Both response shapes must be advertised: the server answers with plain
	// JSON normally, but upgrades the same POST to an SSE stream as soon as a
	// tool emits a notification. Omitting text/event-stream is the classic
	// failure against a spec-strict MCP server.
	req.Header.Set("Accept", "application/json, text/event-stream")
	if u.token != "" {
		req.Header.Set("Authorization", "Bearer "+u.token)
	}
	if u.wing != "" {
		req.Header.Set(mcpprotocol.WingHeader, u.wing)
	}
	return u.client.Do(req)
}

// runStdioProxy pumps newline-delimited JSON-RPC messages from in to the server
// and writes the replies to out. It returns when in reaches EOF — the agent
// closing the pipe is the normal shutdown — or when ctx is cancelled.
//
// Messages are handled one at a time. MCP allows a client to pipeline requests,
// but serialising them keeps replies in order and stdout free of interleaved
// writes, and an agent's memory calls are not a throughput problem worth the
// synchronisation.
func runStdioProxy(ctx context.Context, up *upstream, in io.Reader, out io.Writer) error {
	// bufio.Reader rather than Scanner: a scanner caps a line at its buffer
	// size, and an am_add_drawer call carrying a long document is exactly the
	// message that would exceed it. Truncating that into invalid JSON would be
	// a silent, content-dependent failure.
	reader := bufio.NewReader(in)
	for {
		line, err := reader.ReadBytes('\n')
		// Honour a final message that arrived without a trailing newline before
		// reporting EOF, otherwise the last request of a session is dropped.
		if len(bytes.TrimSpace(line)) > 0 {
			if perr := proxyMessage(ctx, up, line, out); perr != nil {
				return perr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// proxyMessage forwards one message and writes whatever the server sends back.
//
// A transport failure is reported to the agent as a JSON-RPC error rather than
// returned: killing the proxy would leave the agent waiting on a reply that can
// never arrive, and a server restart would look like a hung tool call. Only a
// failure to write to stdout is fatal, because the channel itself is then gone.
func proxyMessage(ctx context.Context, up *upstream, payload []byte, out io.Writer) error {
	resp, err := up.post(ctx, payload)
	if err != nil {
		log.Printf("mcp-stdio: forward failed: %v", err)
		return replyTransportError(payload, out, err)
	}
	defer resp.Body.Close()

	// 202 Accepted is the server acknowledging a notification or response — a
	// message with nothing to answer. Writing anything here would inject a
	// phantom reply into the agent's stream.
	if resp.StatusCode == http.StatusAccepted {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		log.Printf("mcp-stdio: server returned %s: %s", resp.Status, bytes.TrimSpace(body))
		// A non-2xx with a JSON-RPC error body (an auth challenge, say) is still
		// a usable answer, so pass it through when it parses; otherwise
		// synthesise one so the caller is not left hanging.
		if json.Valid(bytes.TrimSpace(body)) {
			return writeMessage(out, body)
		}
		return replyTransportError(payload, out, fmt.Errorf("server returned %s", resp.Status))
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		return forwardSSE(resp.Body, out)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("mcp-stdio: read response: %v", err)
		return replyTransportError(payload, out, err)
	}
	return writeMessage(out, body)
}

// forwardSSE relays an SSE response, writing each event's payload to out as its
// own stdio message. This is what carries server notifications (progress, log
// records) to the agent alongside the final result — the stream may hold several
// messages, so every event is forwarded, not just the last.
func forwardSSE(body io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(body)
	// Events are single-line compact JSON in practice; the larger buffer is
	// headroom for a tool result that carries a long drawer body.
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)

	var data []string
	// flush emits the event assembled so far. Per the SSE spec multiple data:
	// lines form one payload joined by newlines, so they are rejoined here
	// before being compacted back onto a single stdout line.
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		return writeMessage(out, []byte(payload))
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil { // blank line terminates an event
				return err
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		default:
			// event:, id:, retry: and comments carry no JSON-RPC payload.
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read event stream: %w", err)
	}
	// A stream that ends without a trailing blank line still holds a message.
	return flush()
}

// writeMessage writes one JSON-RPC message to the stdio channel.
//
// The stdio transport frames messages by newline, so the payload must occupy
// exactly one line. Compacting guarantees that: it strips any formatting the
// server applied and, more importantly, cannot leave a raw newline inside the
// message. Payloads that are not valid JSON are dropped rather than written,
// since a malformed line would desynchronise the agent's parser.
func writeMessage(out io.Writer, payload []byte) error {
	var buf bytes.Buffer
	if err := json.Compact(&buf, payload); err != nil {
		log.Printf("mcp-stdio: dropping non-JSON payload from server: %v", err)
		return nil
	}
	buf.WriteByte('\n')
	if _, err := out.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// replyTransportError answers a request the proxy could not deliver, echoing the
// original id so the agent can match the error to its call.
//
// Notifications carry no id and expect no reply, so they are only logged: a
// response to one would be a protocol violation. The id is copied verbatim as
// raw JSON because JSON-RPC permits both string and number ids, and re-encoding
// a number through interface{} would risk handing back 1e+06 for 1000000.
func replyTransportError(payload []byte, out io.Writer, cause error) error {
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil // unparseable input; the server never saw it and nothing can be answered
	}
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		return nil // a notification: no reply is owed
	}

	reply, err := json.Marshal(struct {
		Version string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		Version: "2.0",
		ID:      msg.ID,
		Error: struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: jsonRPCInternalError, Message: "agentsmemory mcp-stdio: " + cause.Error()},
	})
	if err != nil {
		return fmt.Errorf("encode transport error: %w", err)
	}
	return writeMessage(out, reply)
}
