package mcpserver

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// mutatingCalls are the service methods that CHANGE stored memory. A tool whose
// handler reaches one of these is a write tool, whatever it is called.
//
// This list is the check's input, so it is itself gated: TestMutatingCallListIsComplete
// fails when a method matching the write-verb shape exists on a service and is
// missing here. An exemption list that nobody checks is how a gate quietly stops
// covering the thing it names.
var mutatingCalls = map[string]bool{
	"Add": true, "AddAnchors": true, "Update": true, "ReplaceAnchors": true,
	"Delete": true, "DeleteWing": true, "MergeWing": true, "DeleteTunnel": true,
	"DeleteHallway": true, "CreateTunnel": true, "WriteDiary": true,
	"KGAdd": true, "KGInvalidate": true, "MarkAnchors": true,
	// ADR-038 T4. InvalidateDrawer and KGSupersede end records and write reasons;
	// Supersede is reached through Update, which is already here.
	"InvalidateDrawer": true, "KGSupersede": true, "Supersede": true,
	"RecomputeGraph": true, "Reconnect": true, "Mine": true,
	// Skill writes go through skill.Service.Upsert, reached from the handler as
	// Update; the three speculative spellings this list once carried
	// (UpdateSkill, SaveSkill, PutSkill) named no method in the tree and were
	// removed when TestMutatingCallListIsComplete reported them as matching
	// nothing. A name that matches nothing is not harmless: it makes the list read
	// as more complete than it is.
	"Upsert": true,
}

// TestEveryMutatingToolIsRegisteredAsAWrite: a tool that changes stored memory
// must be registered through registrar.addWrite, which is where the caller's role
// is enforced.
//
// The defect this closes: the server resolved a real role for every MCP call
// (tenantFromKey reads the membership row and defaults to the least-privileged
// member), reported it back in am_status, and enforced it on ONE tool out of
// forty-one — while the dashboard enforced it in twenty places. A member-role key
// could delete any drawer through the agent surface, which is the surface agents
// actually use.
//
// It is checked structurally rather than by asserting a list of names: the
// classification lives at the registration that performs the enforcement, so a
// tool cannot be marked "write" and left unguarded, nor guarded and left unmarked.
func TestEveryMutatingToolIsRegisteredAsAWrite(t *testing.T) {
	writes := registeredWrites(t)
	if len(writes) == 0 {
		t.Fatal("no tools are registered as writes at all — the classification has stopped happening")
	}

	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", notATest, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	checked := 0
	for _, p := range pkg {
		for path, file := range p.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || !strings.HasPrefix(fn.Name.Name, "register") || fn.Body == nil {
					return true
				}
				for _, tool := range toolsRegisteredIn(fn) {
					checked++
					if !reachesMutation(fn, tool) {
						continue
					}
					if !writes[tool] {
						t.Errorf("am_%s changes stored memory but is registered with add, not addWrite (%s).\n"+
							"  Its handler calls a mutating service method, so any role — including a "+
							"read-only member — can invoke it. Register it with addWrite.",
							tool, filepath.Base(path))
					}
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no register* function yielded a tool; this check has stopped reading the registrations")
	}
}

// registeredWrites runs the real registration and returns the tools the server
// itself classified as writes — the same catalogue am_status and tools/list are
// built from, never a parallel list.
func registeredWrites(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, local := range []bool{false, true} {
		reg := &registrar{srv: server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))}
		registerAll(reg, Deps{Local: local})
		for _, e := range reg.catalog {
			if e.Write {
				out[strings.TrimPrefix(e.Name, mcpprotocol.ToolPrefix)] = true
			}
		}
	}
	return out
}

// toolsRegisteredIn returns the tool names a register* function builds.
func toolsRegisteredIn(fn *ast.FuncDecl) []string {
	var out []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "newTool" || len(call.Args) == 0 {
			return true
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok {
			out = append(out, strings.Trim(lit.Value, `"`))
		}
		return true
	})
	sort.Strings(out)
	return out
}

// reachesMutation reports whether the register function's body calls a method in
// mutatingCalls. One register* builds one tool in this package, which the
// single-tool assertion below keeps true.
func reachesMutation(fn *ast.FuncDecl, _ string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if mutatingCalls[sel.Sel.Name] {
			found = true
		}
		return true
	})
	return found
}

// TestOneToolPerRegistration keeps the assumption reachesMutation rests on true:
// it attributes a mutating call to the tool its register function builds, which
// is sound only while each builds one. A register* that grows a second tool would
// otherwise inherit the first's classification silently.
func TestOneToolPerRegistration(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", notATest, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	for _, p := range pkg {
		for _, file := range p.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || !strings.HasPrefix(fn.Name.Name, "register") || fn.Body == nil {
					return true
				}
				if tools := toolsRegisteredIn(fn); len(tools) > 1 {
					t.Errorf("%s builds %d tools (%v); the write classification attributes a mutating "+
						"call to the one tool its register function builds, so split it",
						fn.Name.Name, len(tools), tools)
				}
				return true
			})
		}
	}
}

// notATest keeps parser.ParseDir off the test files, which is where the
// classification's own copies would otherwise be read as registrations.
func notATest(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }

// TestAReadOnlyRoleIsRefusedByEveryWriteTool drives the registered handler, so it
// answers the question the structural check cannot: does the guard actually
// refuse, and does it let a writer through?
//
// The structural test proves the classification is right. This proves the
// classification does something. Both are needed — a registrar whose addWrite
// forgot to call tenant.CanWrite would pass the first and fail this one.
func TestAReadOnlyRoleIsRefusedByEveryWriteTool(t *testing.T) {
	for _, tc := range []struct {
		role    tenant.Role
		refused bool
	}{
		{tenant.RoleMember, true},
		{tenant.Role(""), true},
		{tenant.Role("observer"), true},
		{tenant.RoleWriter, false},
		{tenant.RoleAdmin, false},
	} {
		reached := false
		handler := writeGuard("am_probe_write", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			reached = true
			return jsonResult(map[string]any{"ok": true}), nil
		})
		ctx := auth.WithTenant(context.Background(), tenant.Tenant{TeamID: "t1", UserID: "u1", Role: tc.role})
		res, err := handler(ctx, mcp.CallToolRequest{})
		if err != nil {
			t.Fatalf("role %q: %v", tc.role, err)
		}
		if tc.refused && reached {
			t.Errorf("role %q reached the handler of a write tool; it must be refused before the "+
				"handler runs, or the refusal is advisory", tc.role)
		}
		if !tc.refused && !reached {
			t.Errorf("role %q was refused by a write tool; result=%v", tc.role, res)
		}
		if tc.refused && !res.IsError {
			t.Errorf("role %q got a non-error result from a write tool", tc.role)
		}
	}
}

// TestAnUnauthenticatedCallIsRefusedBeforeTheRoleCheck: the guard must fail
// closed on a missing tenant rather than reading a zero Role and deciding on it.
func TestAnUnauthenticatedCallIsRefusedBeforeTheRoleCheck(t *testing.T) {
	reached := false
	handler := writeGuard("am_probe_write", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		reached = true
		return jsonResult(map[string]any{"ok": true}), nil
	})
	res, err := handler(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if reached {
		t.Error("an unauthenticated call reached a write handler")
	}
	if !res.IsError {
		t.Error("an unauthenticated call to a write tool did not return an error result")
	}
	// The refusal must name the real reason. Deleting the unauthenticated branch
	// leaves the call refused anyway — a zero Tenant carries an empty role and
	// tenant.CanWrite("") is false — so a test asserting only "refused" passes on a guard
	// that tells an anonymous caller their ROLE is insufficient. Failing closed and
	// explaining correctly are two properties, and only one of them is free.
	if msg := errText(res); !strings.Contains(msg, "unauthenticated") {
		t.Errorf("an unauthenticated call was refused for the wrong reason: %q\n"+
			"  It fails closed either way, which is why this must assert the message: the branch "+
			"can be deleted without any behaviour changing.", msg)
	}
}

// errText pulls the message out of an error result.
func errText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
