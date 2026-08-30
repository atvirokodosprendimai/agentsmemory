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

// installerSource is the file the gate derives its universe from. Every
// registration lives here today; a registration moved elsewhere would leave the
// gate reading a file that no longer holds any, which the empty-universe check
// below turns into a failure rather than a silent pass.
const installerSource = "installer.go"

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
// test could see the other site, because both were written against one function,
// which is what a hand-written test structurally cannot do.
//
// A binary path recorded from `i.serverBin` is wherever the build happened to be
// on the day someone ran the installer. That is the multiple-install-paths defect
// itself, so the gate bans the expression rather than checking any behaviour:
// behaviour is per-kit and a new kit brings its own, while "which value did you
// record" is the one question every site answers the same way.
func TestEveryStdioRegistrationNamesThePlacedBinary(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, installerSource, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", installerSource, err)
	}

	checkRegistrations(t, fset, f)

	t.Run("a registration recording the raw serverBin is reported", aRawServerBinIsReported)
}

// checkRegistrations is the gate's whole decision, reporting through a testing.TB.
//
// ⚠ THE TB PARAMETER IS THE POINT, AND THE FIRST DRAFT DID NOT HAVE IT. The
// falsifiability half drove only the site FINDER and did its own comparison, so
// severing this loop — the actual verdict — left the entire suite green over a
// tree that could carry any number of offenders. Measured: replacing the
// condition with `false &&` gave `go test ./... -count=1` exit 0. A test cannot
// pin its own reporting; only routing the verdict through a TB the subtest can
// substitute makes the reporting itself mutable-and-caught. AGENTS.md records
// this repository shipping the same mistake twice before.
func checkRegistrations(tb testing.TB, fset *token.FileSet, f *ast.File) {
	tb.Helper()
	sites := stdioRegistrationSites(fset, f)

	// A scan that found nothing to check is a gate that cannot fail.
	if len(sites) == 0 {
		tb.Fatal("found no registration sites at all — this gate has stopped reading the source " +
			"it derives its universe from, so it would pass over any number of offenders")
	}

	var offenders int
	for _, s := range sites {
		if s.arg == placedBinIdent {
			continue
		}
		offenders++
		tb.Errorf("%s:%d %s records the server binary as %q, not %q — that is the path the "+
			"binary happened to have when the installer last ran, frozen into an agent's config "+
			"where nothing re-resolves it. Call placeServerBin and register its result.",
			installerSource, s.line, s.fn, s.arg, placedBinIdent)
	}
	if offenders == 0 {
		tb.Logf("%d registration site(s), all naming the placed binary", len(sites))
	}
}

// aRawServerBinIsReported is the falsifiability half.
//
// ⚠ A CORPUS WITH ZERO OFFENDERS CANNOT EXERCISE THE BRANCH THAT REPORTS ONE, so
// without this the gate could be severed and still announce that every site is
// clean. It drives checkRegistrations — the SAME function the gate calls, not a
// copy of its loop — over source that is deliberately wrong, and asserts through
// a substituted TB that the verdict came out. It is a subtest rather than a
// sibling because the acceptance fence runs one test name.
func aRawServerBinIsReported(t *testing.T) {
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

	rec := &recordingTB{}
	checkRegistrations(rec, fset, f)
	if rec.fatal {
		t.Fatal("the gate declared an empty universe over a fixture holding two of our own " +
			"registrations — the finder stopped seeing them")
	}
	// Two of ours are wrong; the third registration is a foreign MCP and must not
	// be counted, or the gate would be demanding we place somebody else's binary.
	if rec.errors != 2 {
		t.Errorf("the gate reported %d offender(s) over a fixture holding exactly two of ours "+
			"and one foreign; with 0 the verdict is severed and this gate pins nothing, and with "+
			"3 it has started claiming a third-party MCP", rec.errors)
	}

	// And the finder names the offending expression as it reads in the source: a
	// failure message that misnames it sends the reader to the wrong line.
	for _, s := range stdioRegistrationSites(fset, f) {
		if s.arg != "i.serverBin" {
			t.Errorf("site %s rendered as %q, want \"i.serverBin\"", s.fn, s.arg)
		}
	}
}

// recordingTB is a testing.TB that remembers whether the gate reported anything.
//
// It embeds testing.TB for the unexported-method requirement and overrides only
// what the gate calls, so a method the gate starts using panics loudly rather
// than silently doing nothing.
type recordingTB struct {
	testing.TB
	errors int
	fatal  bool
}

func (r *recordingTB) Helper()                   {}
func (r *recordingTB) Errorf(string, ...any)     { r.errors++ }
func (r *recordingTB) Logf(string, ...any)       {}
func (r *recordingTB) Fatalf(f string, a ...any) { r.fatal = true }
func (r *recordingTB) Fatal(a ...any)            { r.fatal = true }

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
