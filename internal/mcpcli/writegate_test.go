package mcpcli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// catalogue is one annotated read and one annotated write, which is what a
// current server publishes.
func catalogue() []mcp.Tool {
	return []mcp.Tool{
		mcp.NewTool("am_search", mcp.WithString("query", mcp.Required()), mcp.WithReadOnlyHintAnnotation(true)),
		updateSkill(),
	}
}

func TestAWriteIsRefusedWithoutTheFlagAndAllowedWithIt(t *testing.T) {
	calls := 0
	endpoint := Endpoint{
		ListTools: func(context.Context) ([]mcp.Tool, error) { return catalogue(), nil },
		CallTool: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls++
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: `{"ok":true}`}}}, nil
		},
	}
	var out bytes.Buffer

	err := Run(t.Context(), &out, endpoint, Invocation{Tool: "update_skill", ArgFlags: []string{"name=x", "content=y"}})
	if err == nil || !strings.Contains(err.Error(), "--write") {
		t.Fatalf("refusal = %v, want one naming the flag that lifts it", err)
	}
	if calls != 0 {
		t.Fatalf("a refused write reached the transport (%d calls)", calls)
	}

	if err := Run(t.Context(), &out, endpoint, Invocation{
		Tool: "update_skill", ArgFlags: []string{"name=x", "content=y"}, AllowWrites: true,
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("--write did not reach the transport (%d calls)", calls)
	}
}

func TestWriteFlagDoesNotWaiveTheUnannotatedCatalogueRefusal(t *testing.T) {
	// ⚠ THE ORDER OF THE TWO CHECKS IS THE PROPERTY. A server that annotates
	// nothing cannot tell a read from a write, so honouring --write there would
	// authorise every tool on the strength of a promise nobody made. --write
	// means "I meant this write", never "classify for me".
	unannotated := []mcp.Tool{mcp.NewTool("am_search"), mcp.NewTool("am_add_drawer")}
	calls := 0
	endpoint := Endpoint{
		ListTools: func(context.Context) ([]mcp.Tool, error) { return unannotated, nil },
		CallTool: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls++
			return &mcp.CallToolResult{}, nil
		},
	}

	var out bytes.Buffer
	err := Run(t.Context(), &out, endpoint, Invocation{Tool: "add_drawer", AllowWrites: true})
	if err == nil || !strings.Contains(err.Error(), "no read-only annotation") {
		t.Fatalf("err = %v, want the upgrade diagnosis even with --write", err)
	}
	if calls != 0 {
		t.Fatalf("--write reached an unannotated server (%d calls)", calls)
	}
}

func TestSchemaDescribesAWriteToolWithoutCallingItOrNeedingPermission(t *testing.T) {
	// The point of --schema is that the caller who cannot yet call the tool is
	// exactly the one who needs to read it, so it must answer BEFORE the gate.
	calls := 0
	endpoint := Endpoint{
		ListTools: func(context.Context) ([]mcp.Tool, error) { return catalogue(), nil },
		CallTool: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls++
			return &mcp.CallToolResult{}, nil
		},
	}

	var out bytes.Buffer
	if err := Run(t.Context(), &out, endpoint, Invocation{Tool: "update_skill", Schema: true}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("--schema called the tool (%d calls)", calls)
	}

	printed := out.String()
	for _, want := range []string{
		"am_update_skill",
		"[WRITE — needs --write]",
		"name",        // a required argument
		"description", // the optional one the server blanks if omitted
		"required",
		"optional",
		"AS A MARKDOWN FILE",
		"---", // the template's frontmatter fence
		"the document body, verbatim",
	} {
		if !strings.Contains(printed, want) {
			t.Fatalf("--schema output is missing %q:\n%s", want, printed)
		}
	}
	// The body argument must not also be offered as a frontmatter key, or a
	// caller fills it twice and the file's prose is silently discarded.
	if strings.Contains(printed, "\n  content: ") {
		t.Fatalf("the body argument was printed as a frontmatter key:\n%s", printed)
	}
	// ⚠ THE TEMPLATE MUST NOT SAY WHAT OMITTING AN ARGUMENT DOES. An earlier
	// draft printed "omit the line to leave it unset", which is false for this
	// very tool: am_update_skill overwrites the stored description with the empty
	// string, so following that advice DESTROYS the field am_list_skills shows
	// every session. Omission semantics are the server's and vary per tool.
	if strings.Contains(printed, "unset") || strings.Contains(printed, "omit the line") {
		t.Fatalf("the template promised omission semantics it cannot know:\n%s", printed)
	}
}

func TestSchemaTemplateIsFillableForEveryToolShape(t *testing.T) {
	// A tool with no prose argument must still describe itself rather than
	// printing a template with a body that goes nowhere.
	noProse := mcp.NewTool("am_kg_query", mcp.WithString("entity"), mcp.WithReadOnlyHintAnnotation(true))
	var out bytes.Buffer
	if err := PrintSchema(&out, noProse); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "takes no prose") {
		t.Fatalf("no-body tool not described:\n%s", out.String())
	}
	if strings.Contains(out.String(), "[WRITE") {
		t.Fatalf("a read tool was labelled a write:\n%s", out.String())
	}
}
