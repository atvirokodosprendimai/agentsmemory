package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestThePlacedServerBinaryCarriesThePlatformExtension asserts, from any host,
// the one thing about the placed binary's name that only Windows makes visible.
//
// ⚠ THE CLAIM WAS UNREACHABLE UNTIL THE PARAMETER EXISTED. installedServerBinFile
// reads runtime.GOOS, and this project has no Windows runner — so every test that
// could be written against it was a test about the machine that happened to run
// it, and the `.exe` half was asserted by nothing. Six test sites went on
// building the placed path out of installedServerBinName, which carries no
// extension; all six passed everywhere they were ever run and all six were wrong
// on Windows (issue #407). Passing goos in is what turns "correct on the platform
// we cannot run" into an ordinary assertion.
// It spells the filename out rather than deriving it from installedServerBinName,
// and that is deliberate twice over. Claude Desktop's config names this path
// VERBATIM, so the name is part of the contract and a test that recomputes it
// from the constant would follow a rename silently — the one thing that must not
// happen quietly here. And the gate below has exactly one legal site because of
// it: an exemption for this test would be a list kept beside the truth, which
// this repository has already watched go stale.
func TestThePlacedServerBinaryCarriesThePlatformExtension(t *testing.T) {
	if got := installedServerBinFileOn("windows"); got != "aiagentmemory-server.exe" {
		t.Errorf("installedServerBinFileOn(windows) = %q, want aiagentmemory-server.exe — Claude "+
			"Desktop's config names this path verbatim, and an install is not the place to depend "+
			"on the spawner appending an extension", got)
	}
	for _, goos := range []string{"linux", "darwin"} {
		if got := installedServerBinFileOn(goos); got != "aiagentmemory-server" {
			t.Errorf("installedServerBinFileOn(%s) = %q, want aiagentmemory-server", goos, got)
		}
	}
}

// placedNameHelper is the one function allowed to name the placed binary without
// its platform extension, because deciding the extension is what it is for.
const placedNameHelper = "installedServerBinFileOn"

// binNameSite is one place the source names the placed server binary by the
// extension-less constant.
type binNameSite struct {
	file string
	fn   string // the enclosing function — this is what decides legality
	line int
}

// TestOnlyTheExtensionHelperNamesThePlacedServerBinary keeps the placed
// binary's filename to one function, for the same reason the execute bit is kept
// to one predicate: on every host this project can run, the two spellings agree.
//
// ⚠ A BEHAVIOUR TEST CANNOT HOLD THIS. installedServerBinName and
// installedServerBinFile() are the same string on Linux and macOS, so a call site
// that re-types the constant is green in CI and wrong only on the platform with
// no runner. That is not hypothetical and it is not one slip: SIX sites had it —
// installer_test.go, socket_test.go twice and serverbin_test.go three times —
// each asserting a path the installer owns, each built by hand from the constant
// instead of from the function the installer itself calls. The coupling is an
// AST fact, so an AST check is what pins it, exactly as
// TestOnlyTheSpawnablePredicateReadsThePOSIXExecuteBit does for the mode mask.
//
// It reads the whole package, tests included, because the tests are where the
// defect actually lived.
func TestOnlyTheExtensionHelperNamesThePlacedServerBinary(t *testing.T) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range packageGoFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}

	checkBinNameSites(t, fset, files...)

	t.Run("a hand-built placed path is reported", aHandBuiltPlacedPathIsReported)
}

// checkBinNameSites is the gate's whole decision, reported through a testing.TB
// so the falsifiability half can substitute one and see the verdict. A gate whose
// reporting is unreachable from its own test pins nothing — placedbin_gate_test.go
// records the measurement that made this the house shape.
func checkBinNameSites(tb testing.TB, fset *token.FileSet, files ...*ast.File) {
	tb.Helper()
	var sites []binNameSite
	for _, f := range files {
		sites = append(sites, binNameSites(fset, f)...)
	}

	// The legal site is the helper's own. Finding none means the constant has
	// been renamed or the helper rewritten, and a gate reading nothing would
	// report that every call site is clean.
	if len(sites) == 0 {
		tb.Fatal("found no reference to the placed binary's bare name at all — " + placedNameHelper +
			" no longer builds the filename this gate exists to keep in one place, so it would " +
			"pass over any number of call sites that do")
		return
	}

	var offenders int
	for _, s := range sites {
		if s.fn == placedNameHelper {
			continue
		}
		offenders++
		tb.Errorf("%s:%d %s names the placed server binary as %s, which carries no executable "+
			"extension. On Windows the installer places %s+\".exe\", so this path is one the "+
			"installer never writes — issue #407, where six such sites passed on every host that "+
			"ran them. Call %s() instead; %s is the one place the extension is decided.",
			s.file, s.line, s.fn, binNameConst, binNameConst, "installedServerBinFile",
			placedNameHelper)
	}
	if offenders == 0 {
		tb.Logf("%d bare-name site(s), all inside %s", len(sites), placedNameHelper)
	}
}

// binNameConst is the extension-less constant the gate looks for.
const binNameConst = "installedServerBinName"

// binNameSites returns every reference to binNameConst in one file, tagged with
// the function it sits in.
//
// The constant's own declaration is not a reference and is skipped: a gate that
// reported the `const` line would be unsatisfiable, and an unsatisfiable gate is
// one somebody deletes. Doc comments are not in the AST's expression tree at all,
// so a comment naming the constant — installedServerBinFile's does — costs
// nothing here.
func binNameSites(fset *token.FileSet, f *ast.File) []binNameSite {
	var out []binNameSite
	var fn string
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			fn = v.Name.Name
		case *ast.ValueSpec:
			// The declaration itself, and any other const/var spec naming it on
			// the left-hand side.
			for _, name := range v.Names {
				if name.Name == binNameConst {
					return false
				}
			}
		case *ast.Ident:
			if v.Name == binNameConst && fn != "" {
				out = append(out, binNameSite{
					file: shortFile(fset.Position(v.Pos()).Filename),
					fn:   fn,
					line: fset.Position(v.Pos()).Line,
				})
			}
		}
		return true
	})
	return out
}

// shortFile trims a path to its basename so a finding reads the way a reviewer
// cites one.
func shortFile(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// aHandBuiltPlacedPathIsReported is the falsifiability half.
//
// ⚠ A CORPUS WITH ZERO OFFENDERS CANNOT EXERCISE THE BRANCH THAT REPORTS ONE, so
// without this the verdict could be severed and the gate would go on announcing
// that every call site is clean. It drives checkBinNameSites — the same function
// the gate calls, not a copy of its loop — over source that is deliberately
// wrong. It is a subtest rather than a sibling because the acceptance fence runs
// one test name.
func aHandBuiltPlacedPathIsReported(t *testing.T) {
	const bad = `package main
const installedServerBinName = "aiagentmemory-server"
func installedServerBinFileOn(goos string) string {
	if goos == "windows" {
		return installedServerBinName + ".exe"
	}
	return installedServerBinName
}
func aTest() { _ = filepath.Join(dir, "bin", installedServerBinName) }
func anotherTest() { _ = filepath.Join(other, "bin", installedServerBinName) }`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bad.go", bad, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	rec := &recordingTB{}
	checkBinNameSites(rec, fset, f)
	if rec.fatal {
		t.Fatal("the gate declared an empty universe over a fixture holding four references — " +
			"the finder has stopped seeing them")
	}
	if rec.errors != 2 {
		t.Errorf("the gate reported %d offender(s) over a fixture holding two outside %s; the two "+
			"inside it are legal and the const declaration is not a reference", rec.errors,
			placedNameHelper)
	}

	// The empty universe is its own branch and needs its own fixture: source that
	// names the constant nowhere must reach Fatal rather than fall through to the
	// all-clear, or a renamed constant would read as a clean package.
	empty, err := parser.ParseFile(fset, "empty.go", "package main\nfunc unrelated() {}", 0)
	if err != nil {
		t.Fatalf("parse the empty fixture: %v", err)
	}
	blank := &recordingTB{}
	checkBinNameSites(blank, fset, empty)
	if !blank.fatal {
		t.Error("source naming the constant nowhere did not reach the empty-universe guard, so a " +
			"gate that had stopped finding anything would report success")
	}
	if blank.errors != 0 {
		t.Errorf("the empty universe reported %d offender(s); it should stop, not accuse", blank.errors)
	}
}
