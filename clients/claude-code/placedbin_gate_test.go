package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// placedBinIdent is the name placeServerBin's result is bound to at every
// registration site. The gate below reads the source for it, so the convention
// has to be one word rather than a shape.
const placedBinIdent = "placed"

// registrationSite is one place in the installer where a binary path is written
// into an agent's configuration.
type registrationSite struct {
	fn   string // the enclosing function
	line int
	arg  string // the expression naming the binary
}

// TestEveryStdioRegistrationNamesThePlacedBinary derives its universe from the
// source, so a registration added tomorrow joins the check on the same commit.
//
// ⚠ IT EXISTS BECAUSE ONE KIT WAS FIXED AND THE OTHER WAS NOT. The Desktop
// registration was changed to name the binary the installer places; socket mode
// went on recording `i.serverBin` — a PATH lookup frozen into an agent config
// that nothing re-resolves and nothing updates. Both halves had tests. Neither
// test could see the other site, because both were written against one function.
//
// A binary path recorded from `i.serverBin` is wherever the build happened to be
// on the day someone ran the installer. That is the multiple-install-paths defect
// itself, so the gate bans the expression rather than checking any behaviour:
// behaviour is per-kit and a new kit brings its own, while "which value did you
// record" is the one question every site answers the same way.
func TestEveryStdioRegistrationNamesThePlacedBinary(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "installer.go", nil, 0)
	if err != nil {
		t.Fatalf("parse installer.go: %v", err)
	}

	sites := stdioRegistrationSites(fset, f)
	if len(sites) == 0 {
		t.Fatal("found no registration sites at all — this gate has stopped reading the source " +
			"it derives its universe from, so it would pass over any number of offenders")
	}
	t.Logf("checked %d registration site(s)", len(sites))

	for _, s := range sites {
		if s.arg != placedBinIdent {
			t.Errorf("installer.go:%d %s records the server binary as %q, not %q — that is the path "+
				"the binary happened to have when the installer last ran, frozen into an agent's "+
				"config where nothing re-resolves it. Call placeServerBin and register its result.",
				s.line, s.fn, s.arg, placedBinIdent)
		}
	}
}

// stdioRegistrationSites finds every expression that hands a binary path to an
// agent configuration: an addStdioMCP call, and a "command" key in a map literal
// (which is how the Desktop config entry is built).
func stdioRegistrationSites(fset *token.FileSet, f *ast.File) []registrationSite {
	var sites []registrationSite
	var fn string
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			fn = v.Name.Name
		case *ast.CallExpr:
			sel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "addStdioMCP" || len(v.Args) < 2 {
				return true
			}
			// ⚠ OUR OWN MCP ONLY. arg 0 is the MCP name, and the installer also
			// registers codebase-memory — a third-party binary installed by
			// somebody else's script, at a path we neither own nor refresh.
			// placeServerBin is about OUR server binary; demanding it there would
			// mean copying a foreign program into our kit, which is a different
			// and worse idea than the one this gate protects.
			if exprText(v.Args[0]) != "mcpName" {
				return true
			}
			// arg 1 is the binary.
			sites = append(sites, registrationSite{fn, fset.Position(v.Args[1].Pos()).Line, exprText(v.Args[1])})
		case *ast.KeyValueExpr:
			key, ok := v.Key.(*ast.BasicLit)
			if !ok || key.Kind != token.STRING || key.Value != `"command"` {
				return true
			}
			sites = append(sites, registrationSite{fn, fset.Position(v.Value.Pos()).Line, exprText(v.Value)})
		}
		return true
	})
	return sites
}

// exprText renders an expression the way it reads in the source, so a failure
// names what is actually written there rather than an AST node type.
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprText(v.X) + "." + v.Sel.Name
	case *ast.BasicLit:
		return strings.Trim(v.Value, `"`)
	default:
		return fmt.Sprintf("%T", e)
	}
}

// TestTheRegistrationGateCatchesARawServerBin is the falsifiability half.
//
// ⚠ A CORPUS WITH ZERO OFFENDERS CANNOT EXERCISE THE BRANCH THAT REPORTS ONE, so
// without this the gate could be severed and still announce that every site is
// clean. It drives the SAME function over source that is deliberately wrong,
// rather than a copy of the loop — a falsifiability half sharing nothing with the
// gate pins nothing, which this repository has now shipped twice.
func TestTheRegistrationGateCatchesARawServerBin(t *testing.T) {
	const bad = `package main
func (i *Installer) registerSomethingMCP() error {
	entry := map[string]any{"command": i.serverBin}
	_ = entry
	// A foreign MCP's binary is not ours to place, and must be excluded rather
	// than reported — a gate that demanded placeServerBin here would be asking
	// the installer to copy somebody else's program into our kit.
	_ = i.addStdioMCP(codebaseMemoryName, bin)
	return i.addStdioMCP(mcpName, i.serverBin, "mcp-stdio")
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bad.go", bad, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	sites := stdioRegistrationSites(fset, f)
	if len(sites) != 2 {
		t.Fatalf("found %d site(s) in a fixture holding two of ours and one foreign; the finder "+
			"either stopped seeing a shape or started claiming a third-party MCP: %+v",
			len(sites), sites)
	}
	for _, s := range sites {
		if s.arg == placedBinIdent {
			t.Errorf("site %s read as %q over a fixture that records i.serverBin", s.fn, s.arg)
		}
		if s.arg != "i.serverBin" {
			t.Errorf("site %s rendered as %q, want \"i.serverBin\" — a failure message that "+
				"misnames the offending expression sends the reader to the wrong line", s.fn, s.arg)
		}
	}
}
