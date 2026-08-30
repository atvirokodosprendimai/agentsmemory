package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// TestServerInfoCarriesTheBuildVersion drives a real handshake, because that is
// the only place the answer lives. serverInfo.version is the MCP spec's channel
// for identifying the running implementation, and this server answered every
// client that ever connected with the frozen literal "0.1.0" while releases
// ticked 0.0.10x (issue #106) — an operator probing "what is deployed?" got a
// number that disagreed with the container label.
//
// Nothing caught it for the reason AGENTS.md §Reachability names: every test in
// this package constructed the server with a throwaway version ("0.0.0", "test")
// and asserted on behaviour that did not involve it, so the drift stayed green
// forever. Sever the argument in New and this goes red.
func TestServerInfoCarriesTheBuildVersion(t *testing.T) {
	const want = "v9.9.9-probe"
	result := initializeResultWith(t, Deps{Version: want})

	info, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("the handshake carries no serverInfo block:\n%v", result)
	}
	got, _ := info["version"].(string)
	if got != want {
		t.Errorf("serverInfo.version = %q; want %q — the build version is not reaching the "+
			"handshake, so a client cannot tell which binary answered it", got, want)
	}
}

// TestServerInfoVersionIsNotALiteral pins the SELECTION rather than the value.
// The test above passes if some future edit hardcodes a different constant that
// happens to match; this one derives its universe from the source, so a literal
// second argument to NewMCPServer fails whatever the literal says. It is the
// house shape — see TestEveryDrawerMintWritesAContentKey.
func TestServerInfoVersionIsNotALiteral(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}

	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "NewMCPServer" {
			return true
		}
		found = true
		if len(call.Args) < 2 {
			t.Errorf("%s: NewMCPServer takes no version argument", fset.Position(call.Pos()))
			return false
		}
		if lit, isLit := call.Args[1].(*ast.BasicLit); isLit {
			t.Errorf("%s: NewMCPServer's version argument is the literal %s — that is exactly the "+
				"frozen \"0.1.0\" issue #106 was filed about; pass the resolved build version",
				fset.Position(lit.Pos()), lit.Value)
		}
		return false
	})
	if !found {
		t.Fatal("no NewMCPServer call in server.go — this check has stopped checking anything")
	}
}

// TestStatusResponseCarriesTheVersion pins the selection for am_status. The
// resolver can be correct and the field never reach the wire: a version computed
// and not marshalled is invisible, which is where it sat while the only way to
// confirm a deployed build was ssh plus grepping the container binary for a
// needle string (issue #70).
//
// Source-derived rather than behavioural because calling am_status needs a tenant
// on the context, a usage service and a database; the marshalling is the half
// that was missing, and it is the half that can be read straight off the source.
func TestStatusResponseCarriesTheVersion(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	body := string(src)

	// Anchored on the response map's own opening rather than on a key inside it:
	// the version sits ahead of "total_drawers" in the literal, and an anchor that
	// happens to fall after the key under test reports a missing field that is
	// right there. That is not hypothetical — it is how this test first failed.
	i := strings.Index(body, `"team_id": t.TeamID,`)
	if i < 0 {
		t.Fatal("the am_status response map has moved — this check has stopped checking anything")
	}
	end := strings.Index(body[i:], "})")
	if end < 0 {
		t.Fatal("could not find the end of the am_status response map")
	}
	if resp := body[i : i+end]; !strings.Contains(resp, `"version"`) {
		t.Error("am_status does not marshal a version field — a client still has no way to tell " +
			"which binary it is talking to, which is the whole of issue #70")
	}

	// The field is only worth having if a client can learn it exists. An
	// undocumented response key is discoverable only by someone who already knows
	// it is there — see TestEveryOmitemptyWireKeyInThisPackageIsDescribed for the
	// same argument applied to the keys that can be absent.
	if !strings.Contains(statusDescription(t), "version") {
		t.Error("the am_status tool description never mentions version, so no client learns the " +
			"field is available")
	}
}

// statusDescription returns the am_status tool's description as REGISTERED,
// read off the catalogue the registrar builds rather than off the source, so it
// is the string a client actually receives.
func statusDescription(t *testing.T) string {
	t.Helper()
	reg := &registrar{srv: server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))}
	registerAll(reg, Deps{})
	for _, e := range reg.catalog {
		if strings.HasSuffix(e.Name, "status") {
			return e.Description
		}
	}
	t.Fatal("no status tool in the catalogue")
	return ""
}
