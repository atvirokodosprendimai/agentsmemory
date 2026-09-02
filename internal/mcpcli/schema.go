package mcpcli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/mark3labs/mcp-go/mcp"
)

// PrintSchema renders one tool's live input schema, followed by a markdown
// document that would satisfy it.
//
// It exists because the document path is otherwise UNDISCOVERABLE. A caller
// handed `mcp update_skill file.md` has no way to learn which frontmatter keys
// that tool reads, which one the body fills, or which are required — the
// knowledge lived only in whoever wrote the feature. Printing a fillable
// template rather than a bare property list is the point: the answer to "how do
// I format the file" is a file, not a description of one.
//
// The schema is read from the LIVE tools/list, so a tool that gains an argument
// server-side documents itself here without a new binary.
func PrintSchema(out io.Writer, tool mcp.Tool) error {
	fmt.Fprintf(out, "%s", tool.Name)
	if !IsReadOnly(tool) {
		fmt.Fprint(out, "   [WRITE — needs --write]")
	}
	fmt.Fprintln(out)
	if description := strings.TrimSpace(tool.Description); description != "" {
		fmt.Fprintf(out, "\n%s\n", description)
	}

	body := bodyKey(tool)
	required := requiredSet(tool)

	fmt.Fprintln(out, "\nARGUMENTS")
	for _, name := range schemaKeys(tool) {
		fmt.Fprintf(out, "  %-18s %-8s %-9s %s\n",
			name,
			propertyType(tool.InputSchema.Properties[name]),
			requiredLabel(required[name]),
			role(name, body))
	}

	fmt.Fprintln(out, "\nAS A MARKDOWN FILE")
	fmt.Fprintf(out, "  aiagentmemory mcp %s <file.md> --write\n\n",
		strings.TrimPrefix(tool.Name, mcpprotocol.ToolPrefix))
	writeTemplate(out, tool, required, body)

	fmt.Fprintln(out, "\nFrontmatter keys that are not arguments of this tool are ignored and")
	fmt.Fprintln(out, "reported on stderr. An explicit -a key=value beats the file.")
	return nil
}

// writeTemplate prints a fillable document: every scalar argument as a
// frontmatter key, and the body argument as prose below the fence.
func writeTemplate(out io.Writer, tool mcp.Tool, required map[string]bool, body string) {
	fmt.Fprintln(out, "  ---")
	for _, name := range schemaKeys(tool) {
		if name == body {
			continue
		}
		fmt.Fprintf(out, "  %s: %s\n", name, placeholder(tool.InputSchema.Properties[name], required[name]))
	}
	fmt.Fprintln(out, "  ---")
	if body == "" {
		fmt.Fprintln(out, "\n  (this tool takes no prose — every argument is a frontmatter key)")
		return
	}
	fmt.Fprintf(out, "\n  Everything below the fence becomes %q, verbatim.\n", body)
}

// role explains what an argument is for in terms of the FILE, which is the
// question the caller actually has when reading this.
func role(name, body string) string {
	if name == body {
		return "← the document body, verbatim"
	}
	return "← frontmatter key (or -a " + name + "=…)"
}

func requiredLabel(isRequired bool) string {
	if isRequired {
		return "required"
	}
	return "optional"
}

// placeholder suggests a value of the right shape, so a filled-in template does
// not fail on a type the caller could not see.
//
// ⚠ IT SAYS "optional" AND NOTHING ABOUT WHAT OMITTING ONE DOES. An earlier
// draft printed "omit the line to leave it unset", which is FALSE for the first
// tool anyone will point this at: am_update_skill passes an absent description
// through as the empty string and overwrites the stored one, so omitting the
// line DESTROYS the field rather than leaving it. What an omitted argument means
// is the server's business and varies per tool, so the template describes the
// schema — which it can see — and makes no promise about semantics it cannot.
func placeholder(spec any, isRequired bool) string {
	hint := "…"
	if property, ok := spec.(map[string]any); ok {
		switch property["type"] {
		case "number", "integer":
			hint = "0"
		case "boolean":
			hint = "false"
		}
	}
	if !isRequired {
		return hint + "   # optional"
	}
	return hint
}

func propertyType(spec any) string {
	property, ok := spec.(map[string]any)
	if !ok {
		return "string"
	}
	if kind, ok := property["type"].(string); ok {
		return kind
	}
	return "string"
}

// requiredSet indexes the schema's required list for lookup.
func requiredSet(tool mcp.Tool) map[string]bool {
	set := make(map[string]bool, len(tool.InputSchema.Required))
	for _, name := range tool.InputSchema.Required {
		set[name] = true
	}
	return set
}

// schemaKeys lists a tool's properties with the required ones first, then
// alphabetically — so the arguments a caller MUST supply are the ones they read
// first, rather than wherever the alphabet put them.
func schemaKeys(tool mcp.Tool) []string {
	required := requiredSet(tool)
	keys := make([]string, 0, len(tool.InputSchema.Properties))
	for name := range tool.InputSchema.Properties {
		keys = append(keys, name)
	}
	sort.Slice(keys, func(i, j int) bool {
		if required[keys[i]] != required[keys[j]] {
			return required[keys[i]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
