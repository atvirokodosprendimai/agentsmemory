package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// placedBinIdent is the name placeServerBin's result is bound to at every
// registration site. The gate below reads the source for it, so the convention
// has to be one word rather than a shape.
const placedBinIdent = "placed"

// productionGoFiles returns every non-test .go file in this package.
//
// ⚠ THE GATE USED TO READ installer.go AND NOTHING ELSE, and review named the hole:
// a registration added in a sibling file would be invisible, while the two sites
// already in installer.go kept the universe non-empty so the empty-universe guard
// never fired either. The universe has to be the PACKAGE, or "derives its universe
// from the source" is a claim about one file.
func productionGoFiles(tb testing.TB) []string {
	tb.Helper()
	all, err := filepath.Glob("*.go")
	if err != nil {
		tb.Fatalf("glob the package: %v", err)
	}
	var out []string
	for _, f := range all {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		tb.Fatal("no production .go file found in this package — the gate is reading nothing")
	}
	return out
}

// registrationSite is one place in the installer where a binary path is written
// into an agent's configuration.
type registrationSite struct {
	file string // the file it was found in — the gate reads the whole package
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
	var files []*ast.File
	for _, name := range productionGoFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}

	checkRegistrations(t, fset, files...)

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
func checkRegistrations(tb testing.TB, fset *token.FileSet, files ...*ast.File) {
	tb.Helper()
	var sites []registrationSite
	for _, f := range files {
		sites = append(sites, stdioRegistrationSites(fset, f)...)
	}

	// A scan that found nothing to check is a gate that cannot fail.
	if len(sites) == 0 {
		// ⚠ RETURN EXPLICITLY. testing.T.Fatal stops the goroutine; the recordingTB
		// the falsifiability half substitutes only records, so without this the
		// decision would run on past a verdict it had already reached and log that
		// every site was clean. Review found the divergence before it mattered.
		tb.Fatal("found no registration sites at all — this gate has stopped reading the source " +
			"it derives its universe from, so it would pass over any number of offenders")
		return
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
			s.file, s.line, s.fn, s.arg, placedBinIdent)
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
	// ⚠ THE FIXTURE MIRRORS THE REAL APIs, because the gate keys on them. It holds
	// one of each own-registration shape — a config entry written by
	// ensureMCPServer and a CLI registration through addStdioMCP — plus two that
	// must NOT be counted: a foreign MCP, and a hook registration whose map also
	// has a "command" key. That last one is not hypothetical; widening the gate to
	// the package made an earlier draft accuse settings.go of exactly this.
	const bad = `package main
func (i *Installer) registerSomethingMCP() error {
	entry := map[string]any{"command": i.serverBin, "args": args}
	if _, err := ensureMCPServer(path, mcpName, entry); err != nil {
		return err
	}
	_ = map[string]any{"type": "command", "command": reg.cmd}
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

	// ⚠ THE SECOND FILE IS THE POINT, and no mutant can prove it from this tree.
	// Both real registrations live in installer.go today, so narrowing the gate
	// back to that one file leaves the whole suite green — the widening is a gate
	// against a site added ELSEWHERE tomorrow, and today's corpus cannot exercise
	// it. A fixture can: the offender here is in the second file, so a gate that
	// reads only the first reports nothing.
	clean, err := parser.ParseFile(fset, "first.go", `package main
func (i *Installer) registerFineMCP() error {
	return i.addStdioMCP(mcpName, placed, "mcp-stdio")
}`, 0)
	if err != nil {
		t.Fatalf("parse the clean fixture: %v", err)
	}
	spread := &recordingTB{}
	checkRegistrations(spread, fset, clean, f)
	if spread.errors != 2 {
		t.Errorf("across two files the gate reported %d offender(s), want 2 — a gate that reads "+
			"only the first file would report 0 while a registration in any other file of the "+
			"package went unchecked", spread.errors)
	}

	// The empty universe is its own branch and needs its own fixture: a source
	// file with no registration in it must reach Fatal, not fall through to the
	// all-clear.
	empty, err := parser.ParseFile(fset, "empty.go", "package main\nfunc unrelated() {}", 0)
	if err != nil {
		t.Fatalf("parse the empty fixture: %v", err)
	}
	blank := &recordingTB{}
	checkRegistrations(blank, fset, empty)
	if !blank.fatal {
		t.Error("a source file holding no registration did not reach the empty-universe guard, " +
			"so a gate that had stopped finding anything would report success")
	}
	if blank.errors != 0 {
		t.Errorf("the empty universe reported %d offender(s); it should stop, not accuse", blank.errors)
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
// agent's MCP configuration. There are exactly two such APIs:
//
//   - addStdioMCP(name, bin, argv...)  — the agent CLIs, which we shell out to
//   - ensureMCPServer(path, name, entry) — a config file we write ourselves, where
//     the binary is the entry map's "command"
//
// ⚠ THE UNIVERSE IS THE API, NOT A KEY NAME, and the first draft got that wrong.
// It matched any `"command"` key in any map literal, which is a shape and not a
// meaning: widened from one file to the package it immediately accused
// settings.go's HOOK registration — `{"type": "command", "command": reg.cmd}` — of
// failing to place a server binary it has nothing to do with. A gate whose false
// positives are this easy to produce gets an exemption list, and the exemption
// list is where gates go to stop working. Keying on the two functions that
// actually write a registration needs no exemptions at all.
//
// Only our own MCP counts: the installer also registers codebase-memory, a
// third-party binary another project's script puts on disk, at a path we neither
// own nor refresh. Demanding placeServerBin there would mean copying a foreign
// program into our kit, which is a different and worse idea than the one this
// gate protects.
func stdioRegistrationSites(fset *token.FileSet, f *ast.File) []registrationSite {
	var sites []registrationSite
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// A method call (i.addStdioMCP) is a SelectorExpr; a package-level
			// function (ensureMCPServer) is a bare Ident. Handling only the first
			// silently halved the universe.
			var name string
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			case *ast.Ident:
				name = fun.Name
			default:
				return true
			}
			switch name {
			case "addStdioMCP":
				// addStdioMCP(name, bin, argv...): arg 0 names the MCP, arg 1 is the
				// binary.
				if len(call.Args) < 2 || exprText(call.Args[0]) != mcpNameIdent {
					return true
				}
				sites = append(sites, siteAt(fset, fn.Name.Name, call.Args[1]))
			case "ensureMCPServer":
				// ensureMCPServer(path, name, entry): arg 1 names the MCP, arg 2 is
				// the entry whose "command" is the binary.
				if len(call.Args) < 3 || exprText(call.Args[1]) != mcpNameIdent {
					return true
				}
				if cmd := commandOfEntry(fn, call.Args[2]); cmd != nil {
					sites = append(sites, siteAt(fset, fn.Name.Name, cmd))
				}
			}
			return true
		})
	}
	return sites
}

// mcpNameIdent is the constant every registration of OUR server is named by.
const mcpNameIdent = "mcpName"

// siteAt records one registration, carrying the file so a failure over a
// multi-file package sends the reader to the right place.
func siteAt(fset *token.FileSet, fn string, e ast.Expr) registrationSite {
	pos := fset.Position(e.Pos())
	return registrationSite{pos.Filename, fn, pos.Line, exprText(e)}
}

// commandOfEntry resolves the "command" value of the map handed to
// ensureMCPServer, following one level of local variable.
//
// The entry is almost always built a few lines above the call rather than inline,
// so refusing to follow the identifier would see nothing at all — an empty
// universe that the guard would then correctly, and uselessly,report as a broken gate.
func commandOfEntry(fn *ast.FuncDecl, arg ast.Expr) ast.Expr {
	if lit, ok := arg.(*ast.CompositeLit); ok {
		return commandKeyOf(lit)
	}
	ident, ok := arg.(*ast.Ident)
	if !ok {
		return nil
	}
	var found ast.Expr
	ast.Inspect(fn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); !ok || id.Name != ident.Name || i >= len(as.Rhs) {
				continue
			}
			if lit, ok := as.Rhs[i].(*ast.CompositeLit); ok {
				if c := commandKeyOf(lit); c != nil {
					found = c
				}
			}
		}
		return true
	})
	return found
}

// commandKeyOf returns the value of a map literal's "command" key, if it has one.
func commandKeyOf(lit *ast.CompositeLit) ast.Expr {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.BasicLit); ok && k.Kind == token.STRING && k.Value == `"command"` {
			return kv.Value
		}
	}
	return nil
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
