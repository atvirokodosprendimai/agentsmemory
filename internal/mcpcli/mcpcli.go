// Package mcpcli contains transport-level helpers shared by the direct and
// remote agentsmemory MCP command-line clients. Tool definitions remain owned
// by the production server; this package only consumes their live schemas and
// annotations.
package mcpcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/mark3labs/mcp-go/mcp"
)

// Endpoint is the transport seam used by the shared CLI execution path. HTTP
// and in-process clients supply different implementations, but discovery and
// invocation policy remain here.
type Endpoint struct {
	ListTools func(context.Context) ([]mcp.Tool, error)
	CallTool  func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// Invocation is the transport-independent input to a CLI MCP call.
type Invocation struct {
	Tool     string
	ArgFlags []string
	Tail     []string
	Raw      bool

	// Digest, when positive, prints RenderDigest of the result inside that
	// many characters instead of the JSON page (ADR-058). It is what the recall
	// hooks pass; every other caller keeps the page.
	Digest int

	// AllowWrites lifts the read-only refusal for this one call. It is opt-in
	// because the default protects a shell typo from mutating team memory, which
	// was the whole reason the CLI shipped read-only; it is not a capability the
	// binary lacks, so a caller who means it says so.
	AllowWrites bool

	// Schema prints the selected tool's arguments and a fillable markdown
	// template instead of calling it. It needs no write permission: describing a
	// tool changes nothing.
	Schema bool

	// Log receives diagnostics about what a document contributed. It is separate
	// from out because out is piped into jq — a note that lands there corrupts
	// the result. A nil Log discards the notes.
	Log io.Writer
}

// Run executes the one CLI contract shared by local and remote consumers:
// discover the live tools, fail closed on write policy, build arguments from
// the live schema, call the selected production handler, and render its result.
func Run(ctx context.Context, out io.Writer, endpoint Endpoint, invocation Invocation) error {
	tools, err := endpoint.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("list MCP tools: %w", err)
	}

	name := strings.TrimPrefix(invocation.Tool, mcpprotocol.ToolPrefix)
	if name == "" {
		return PrintTools(out, tools, invocation.Raw)
	}

	tool, ok := FindTool(tools, name)
	if !ok {
		return fmt.Errorf("unknown tool %q; run the mcp command without a tool to list the available tools", name)
	}
	// Describing a tool is answered before any policy: a caller who cannot yet
	// call a write tool is exactly the one who needs to read its schema.
	if invocation.Schema {
		return PrintSchema(out, tool)
	}
	if !publishesReadOnlyHints(tools) {
		return fmt.Errorf("%q has no read-only annotation on this server; the CLI fails closed until the server publishes one (upgrade the server, or call it from an agent)", name)
	}
	// ⚠ THE CATALOGUE CHECK ABOVE IS NOT WAIVED BY AllowWrites, and the order is
	// the reason. A server that annotates nothing cannot tell a read from a write,
	// so --write there would authorise every tool on the strength of a promise
	// nobody made. --write says "I meant this write", never "classify for me".
	if !IsReadOnly(tool) && !invocation.AllowWrites {
		return fmt.Errorf("%q writes to the palace; pass --write to allow it, or run `mcp %s --schema` to see what it takes", name, name)
	}

	args := ParseArgs(invocation.ArgFlags, invocation.Tail, tool.InputSchema.Properties, PrimaryArg(tool))
	if err := applyDocument(tool, invocation, args); err != nil {
		return err
	}

	result, err := Call(ctx, endpoint.CallTool, tool.Name, args)
	if err != nil {
		return fmt.Errorf("call %s: %w", tool.Name, err)
	}
	if invocation.Digest > 0 && !result.IsError {
		// ADR-058: the line that SELECTS the digest. TestTheDigestIsSelectedByTheFlag
		// drives the real CLI and goes red when this branch is deleted.
		query := ""
		if len(invocation.Tail) > 0 {
			query = invocation.Tail[0]
		}
		for _, content := range result.Content {
			if text, ok := mcp.AsTextContent(content); ok {
				fmt.Fprint(out, RenderDigest([]byte(text.Text), query, invocation.Digest))
			}
		}
		return nil
	}
	if err := PrintCallResult(out, result, invocation.Raw); err != nil {
		return err
	}
	if result.IsError {
		return errors.New("the tool reported an error")
	}
	return nil
}

// WireName is the one catalogue name every client sends. Bare and already
// prefixed names collapse to the same wire name.
func WireName(name string) string {
	return mcpprotocol.ToolPrefix + strings.TrimPrefix(name, mcpprotocol.ToolPrefix)
}

// NewCall builds the one CallToolRequest every production MCP client sends.
func NewCall(name string, args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = WireName(name)
	req.Params.Arguments = args
	return req
}

// Call invokes a production MCP tool through NewCall. Transport errors are
// returned; a tool-level IsError stays on the result so a caller that must
// observe refusals (the test harness, write clients) can do so.
func Call(ctx context.Context, invoke func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), name string, args map[string]any) (*mcp.CallToolResult, error) {
	return invoke(ctx, NewCall(name, args))
}

// Failed reports a tool-level IsError as a Go error, using the first text
// block when the server sent one.
func Failed(name string, result *mcp.CallToolResult) error {
	if result == nil || !result.IsError {
		return nil
	}
	if text, ok := firstText(result); ok && text != "" {
		return fmt.Errorf("%s", text)
	}
	return fmt.Errorf("%s returned an error", WireName(name))
}

// DecodeJSON calls a tool and unmarshals its first text content as JSON.
func DecodeJSON(ctx context.Context, invoke func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), name string, args map[string]any, out any) error {
	result, err := Call(ctx, invoke, name, args)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	text, ok := firstText(result)
	if !ok {
		if result == nil || len(result.Content) == 0 {
			return fmt.Errorf("%s: empty response", name)
		}
		return fmt.Errorf("%s: unexpected response type", name)
	}
	if result.IsError {
		return fmt.Errorf("%s: %s", name, text)
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("%s: decode response: %w", name, err)
	}
	return nil
}

func firstText(result *mcp.CallToolResult) (string, bool) {
	if result == nil || len(result.Content) == 0 {
		return "", false
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		return "", false
	}
	return text.Text, true
}

// IsReadOnly reports whether the live tool definition explicitly promises not
// to modify its environment. Missing or false metadata fails closed.
func IsReadOnly(tool mcp.Tool) bool {
	return tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint
}

// publishesReadOnlyHints reports whether this catalogue is from a server that
// actually annotates reads. mcp-go defaults an unset hint to false, so an older
// agentsmemory process (no WithReadOnlyHintAnnotation anywhere) looks like a
// catalogue of writes. One true hint means the server is in on the contract;
// zero means fail closed with the upgrade diagnosis, not "this tool writes".
func publishesReadOnlyHints(tools []mcp.Tool) bool {
	for _, tool := range tools {
		if IsReadOnly(tool) {
			return true
		}
	}
	return false
}

// PrimaryArg returns the first required input named by the live schema, or an
// empty string when a bare positional must not be folded into the call.
func PrimaryArg(tool mcp.Tool) string {
	if len(tool.InputSchema.Required) == 0 {
		return ""
	}
	return tool.InputSchema.Required[0]
}

// FindTool resolves a bare or am_-prefixed name against the live catalogue.
func FindTool(tools []mcp.Tool, name string) (mcp.Tool, bool) {
	wireName := WireName(name)
	for _, tool := range tools {
		if tool.Name == wireName {
			return tool, true
		}
	}
	return mcp.Tool{}, false
}

// TailArgs returns the positional tokens after the tool name.
func TailArgs(args []string) []string {
	if len(args) <= 1 {
		return nil
	}
	return args[1:]
}

// isArgToken reports whether a bare tail token is a key=value argument rather
// than the positional value, deciding it against the tool's own schema.
//
// A token qualifies only when the text before the first "=" names a property the
// tool actually declares. That keeps `limit=3` an argument and leaves a query
// like "key=value pair" — or any prose carrying an equals sign — as the
// positional it plainly is.
//
// ⚠ WITH NO SCHEMA IN HAND THERE IS NOTHING TO ASK, so an empty property map
// answers yes and the pre-schema shape stands. The alternative fails in the
// direction that has no floor: every argument to every tool would become a
// positional at once, which is a far wider break than the defect being fixed.
func isArgToken(token string, properties map[string]any) bool {
	key, _, ok := strings.Cut(token, "=")
	if !ok {
		return false
	}
	// ⚠ AN ARGUMENT IS ONE SHELL WORD; PROSE IS NOT. The declared-key test alone
	// leaves the defect live for any query that OPENS with a property name —
	// "wing=wing_acme is what I set" cuts to the declared key "wing" and vanishes
	// into raw exactly as before. That is not a corner: a session working this
	// repository types "wing", "room" and "limit" in prose all day. Whitespace is
	// what the shell already used to tell the two apart, so nothing that arrives
	// as a single token is refused by it, and an explicit -a never reaches here.
	if strings.ContainsAny(token, " \t\n") {
		return false
	}
	if len(properties) == 0 {
		return true
	}
	_, declared := properties[strings.TrimSpace(key)]
	return declared
}

// ParseArgs folds CLI key=value arguments and one bare positional into a map
// typed according to the live JSON schema. Explicit key=value input wins over
// the positional for the same key.
//
// A tail token that is not introduced by -a is an argument only when isArgToken
// says so — the tool's own schema and the absence of whitespace decide it. That
// is narrower than it once was, and deliberately: testing for "=" alone made any
// positional carrying an equals sign disappear into the argument map, which on
// the recall path meant a session silently got no memory at all.
func ParseArgs(argFlags, rawTail []string, properties map[string]any, primaryKey string) map[string]any {
	raw := map[string]string{}
	add := func(kv string) {
		if key, value, ok := strings.Cut(kv, "="); ok {
			raw[strings.TrimSpace(key)] = value
		}
	}
	for _, kv := range argFlags {
		add(kv)
	}

	var positional string
	for i := 0; i < len(rawTail); i++ {
		token := rawTail[i]
		switch {
		case token == "-a" || token == "--arg":
			if i+1 < len(rawTail) {
				add(rawTail[i+1])
				i++
			}
		// ⚠ A BARE TOKEN IS ONLY AN ARGUMENT IF ITS KEY IS ONE. Testing for "="
		// alone made any positional containing an equals sign disappear into raw
		// as a key nobody asked for, and the primary key then arrived empty — the
		// server answering `required argument "query" not found` while the caller
		// could see the query in their own command line.
		//
		// The cost was silent and it was on the recall path: the UserPromptSubmit
		// hook builds its query from the user's prompt, so any prompt containing an
		// "=" — a URL, a snippet, `key=value`, an XML-ish attribute — lost its
		// recall entirely and reported only "agentsmemory could not look". Measured
		// 2026-09-06 after a peer session saw it on "roughly half" its prompts;
		// deterministic once the query was the variable rather than load.
		//
		// The schema is the discriminator and it was already in hand: `limit=3` is
		// an argument because "limit" is a property of the tool, and "key=value
		// pair" is a query because "key" is not. Nothing new is passed in.
		case isArgToken(token, properties):
			add(token)
		case positional == "":
			positional = token
		// A key=value isArgToken turned down — an undeclared key, or a declared
		// one carrying whitespace — with the positional already spoken for has no
		// role left to play, so it travels as the argument the caller typed and
		// the server rejects it by name. Dropping it instead would be the same
		// silence this whole branch exists to remove, and it is the documented
		// hybrid syntax: TestParseToolArgsHybridSyntax passes wing= and room=
		// against a schema that declares neither.
		case strings.Contains(token, "="):
			add(token)
		}
	}
	if positional != "" && primaryKey != "" {
		if _, exists := raw[primaryKey]; !exists {
			raw[primaryKey] = positional
		}
	}

	args := make(map[string]any, len(raw))
	for key, value := range raw {
		args[key] = coerce(properties[key], value)
	}
	return args
}

func coerce(spec any, value string) any {
	property, ok := spec.(map[string]any)
	if !ok {
		return value
	}
	switch property["type"] {
	case "number", "integer":
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			return number
		}
	case "boolean":
		if boolean, err := strconv.ParseBool(value); err == nil {
			return boolean
		}
	}
	return value
}

// PrintResult writes MCP text content, pretty-printing JSON while preserving
// non-JSON tool messages verbatim. Non-text blocks remain available through a
// client's raw-envelope mode.
func PrintResult(out io.Writer, result *mcp.CallToolResult) error {
	for _, content := range result.Content {
		text, ok := mcp.AsTextContent(content)
		if !ok {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
			fmt.Fprintln(out, text.Text)
			continue
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(decoded); err != nil {
			return fmt.Errorf("render MCP result: %w", err)
		}
	}
	return nil
}

// PrintCallResult renders either the useful tool content or the complete MCP
// envelope requested by a diagnostic caller.
func PrintCallResult(out io.Writer, result *mcp.CallToolResult, raw bool) error {
	if raw {
		return printJSON(out, result)
	}
	return PrintResult(out, result)
}

// PrintTools renders the callable read surface from the live catalogue. Raw
// mode emits the complete tools/list payload, schemas included.
func PrintTools(out io.Writer, tools []mcp.Tool, raw bool) error {
	if raw {
		return printJSON(out, tools)
	}

	sorted := append([]mcp.Tool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	readable := make([]mcp.Tool, 0, len(sorted))
	writes, unlabeled := 0, 0
	if !publishesReadOnlyHints(sorted) {
		unlabeled = len(sorted)
	} else {
		for _, tool := range sorted {
			if IsReadOnly(tool) {
				readable = append(readable, tool)
			} else {
				writes++
			}
		}
	}

	fmt.Fprintf(out, "%d read-only tools (of %d on the production MCP surface):\n\n", len(readable), len(sorted))
	for _, tool := range readable {
		usage := strings.TrimPrefix(tool.Name, mcpprotocol.ToolPrefix)
		if primary := PrimaryArg(tool); primary != "" {
			usage += " <" + primary + ">"
		}
		fmt.Fprintf(out, "  %s\n      %s\n", usage, firstLine(tool.Description, 96))
	}
	if unlabeled > 0 {
		fmt.Fprintf(out, "\n%d tools have no read-only annotation — this server is older than the CLI; upgrade the server to call them from here.\n", unlabeled)
	}
	fmt.Fprintf(out, "\n%d write tools are not callable here — ask your agent to run those.\n", writes)
	fmt.Fprintln(out, "Arguments: `mcp <tool> <primary-arg> -a key=value`; raw mode prints every schema.")
	return nil
}

func firstLine(text string, max int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return strings.TrimSpace(string(runes[:max])) + "…"
}

func printJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("render MCP value: %w", err)
	}
	return nil
}
