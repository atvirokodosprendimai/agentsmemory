package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
)

// TestJSONResultCarriesBothHalves pins the chokepoint every tool returns through.
//
// The text block is kept for backwards compatibility and structuredContent is
// added beside it, so a client written before the field existed reads the same
// document it always did. Dropping either half would leave one class of client
// working and every test here passing, because a test reads whichever half it
// was written against.
func TestJSONResultCarriesBothHalves(t *testing.T) {
	t.Run("an object gets both", func(t *testing.T) {
		res := jsonResult(map[string]any{"count": 2, "wing": "wing_acme"})
		if res.StructuredContent == nil {
			t.Error("no structuredContent; a caller still has to parse a string to reach a field")
		}
		if len(res.Content) == 0 {
			t.Error("no text block; a client written before structuredContent gets nothing")
		}
	})

	// structuredContent is specified as an object. An array or scalar payload is
	// served as text alone rather than smuggled into a field that may not hold it.
	t.Run("a non-object gets text only", func(t *testing.T) {
		for _, v := range []any{[]string{"a", "b"}, 42, "plain", nil} {
			res := jsonResult(v)
			if res.StructuredContent != nil {
				t.Errorf("%#v produced structuredContent, which the spec says must be an object", v)
			}
			if len(res.Content) == 0 {
				t.Errorf("%#v produced no text block either, so the caller gets nothing", v)
			}
		}
	})
}

// TestEveryDeclaredOutputSchemaIsSatisfiedByTheTool is the conformance half, and
// the reason a schema is worth more than a comment.
//
// Declaring an outputSchema is a PROMISE: a client may validate against it and
// reject what does not conform. A schema that describes something the tool does
// not return is worse than none, because it converts a working call into a
// client-side failure. Nothing else in this tree checks that pairing — the schema
// is generated from a Go type and the value is built by a handler, and those meet
// only at runtime.
//
// So this drives the real server, calls every tool that declares a schema, and
// validates what comes back against the schema the same server advertised.
func TestEveryDeclaredOutputSchemaIsSatisfiedByTheTool(t *testing.T) {
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	srv := New(Deps{
		Version: "test",
		Drawers: drawers,
		Usage:   usage.NewService(usage.NewRepo(gdb), graphTestCaps{}),
	})

	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	listed, err := cli.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: "team-structured", UserID: "u1", Role: tenant.RoleAdmin})
	checked := 0
	for _, tool := range listed.Tools {
		// ⚠ THE EMPTINESS TEST IS Type == "", WHICH IS WHAT mcp-go ITSELF USES
		// (mcp/tools.go: "If no output schema is specified, do not return
		// anything"). Marshalling the struct directly and comparing to "{}" does
		// NOT work — it renders a non-empty object for every tool, so a first
		// version of this loop reported all 41 as advertising an empty schema
		// while the wire carried three.
		if tool.OutputSchema.Type == "" {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Errorf("%s: cannot marshal its own advertised schema: %v", tool.Name, err)
			continue
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Errorf("%s advertises an outputSchema that does not parse as JSON Schema: %v", tool.Name, err)
			continue
		}
		// A schema with no properties describes nothing and would validate any
		// object, which is the shape that passes this gate while buying a caller
		// nothing.
		if len(schema.Properties) == 0 {
			t.Errorf("%s advertises an outputSchema with no properties; it constrains nothing", tool.Name)
			continue
		}

		res, err := cli.CallTool(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: tool.Name, Arguments: map[string]any{}},
		})
		if err != nil {
			t.Errorf("%s declares a schema but the call failed: %v", tool.Name, err)
			continue
		}
		if res.IsError {
			t.Errorf("%s declares a schema but returned an error result", tool.Name)
			continue
		}
		if res.StructuredContent == nil {
			t.Errorf("%s declares an outputSchema and returned no structuredContent — a client that validates gets nothing to validate", tool.Name)
			continue
		}

		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Errorf("%s: cannot resolve its own advertised schema: %v", tool.Name, err)
			continue
		}
		// Round-trip through JSON so the value is validated in the shape a client
		// receives it, not in Go's.
		var onTheWire any
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, &onTheWire); err != nil {
			t.Errorf("%s: structuredContent does not round-trip through JSON: %v", tool.Name, err)
			continue
		}
		if err := resolved.Validate(onTheWire); err != nil {
			t.Errorf("%s returns structuredContent that violates the schema it advertises: %v\n%s", tool.Name, err, b)
		}
		checked++
	}

	if checked == 0 {
		t.Fatal("no tool declared a usable outputSchema — this gate validated nothing, which is indistinguishable from passing")
	}
	t.Logf("validated structuredContent against the advertised schema for %d tool(s)", checked)
}
