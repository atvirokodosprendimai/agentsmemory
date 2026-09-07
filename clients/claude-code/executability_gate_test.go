package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// executeBitMask is the POSIX any-execute mask, as a VALUE rather than a
// spelling. `0o111`, the legacy `0111`, `73` and `0x49` are the same mask and
// the gate must not be dodgeable by choosing a different base.
const executeBitMask = 0o111

// executeBitSite is one place the source masks a file mode with the execute bit.
type executeBitSite struct {
	file string
	fn   string // the enclosing function — this is what decides legality
	line int
}

// TestOnlyTheSpawnablePredicateReadsThePOSIXExecuteBit keeps "can this platform
// run that file" to one predicate, because it has already been answered twice
// and the second answer was wrong.
//
// ⚠ THE RULE IS NOT REMEMBERED AT A NEW CALL SITE; IT IS RE-TYPED. Go never sets
// an execute bit on Windows — os.Stat reports 0666 for every regular file — so
// `Mode()&0o111 == 0` calls every binary there not-executable. The bridge rung
// hit it (issue #224) and was fixed by extracting spawnableOn, which judges by
// PATHEXT on Windows and by the mode everywhere else. The codebase-memory peer
// rung was written afterwards (ADR-057) and re-typed the POSIX test, so `doctor`
// reported `codebase-memory-mcp.exe: not executable` about a peer Claude Code
// held a live connection to, and exited 1 on a clean v0.0.125 install (issue
// #393, finding 2). Same defect, second call site, six weeks apart.
//
// A behaviour test cannot hold this from here: on a POSIX host the raw mask and
// spawnable agree on every input, so the mutant that reintroduces the bug is
// green in CI and red only on the platform with no runner. The coupling is an
// AST fact, so an AST check is what pins it.
//
// ⚠ ITS UNIVERSE IS PRODUCTION SOURCE, AND TWO TEST FILES DO HOLD THE RAW MASK.
// serverbin_test.go and desktopbridge_download_test.go assert that a file the
// installer JUST WROTE came out executable, which is a different question from
// judging a file someone else placed — and they are Windows-broken for more
// reasons than this one (serverbin_test.go builds its expected path from
// installedServerBinName, which carries no `.exe`, while the installer places
// installedServerBinFile, which does). Widening the gate over them would mean
// shipping a fix for a platform failure this session has not measured, so they
// are named here and filed separately rather than swept in silently.
func TestOnlyTheSpawnablePredicateReadsThePOSIXExecuteBit(t *testing.T) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range productionGoFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}

	checkExecuteBitSites(t, fset, files...)

	t.Run("a second rung reading the bit directly is reported", aRawExecuteBitIsReported)
}

// spawnablePredicate is the one function allowed to read the execute bit, and it
// is the only place the platform question is answered.
const spawnablePredicate = "spawnableOn"

// checkExecuteBitSites is the gate's whole decision, reported through a
// testing.TB so the falsifiability half can substitute one and see the verdict.
// A gate whose reporting is unreachable from its own test pins nothing — this
// repository has shipped that mistake more than once, and placedbin_gate_test.go
// records the measurement.
func checkExecuteBitSites(tb testing.TB, fset *token.FileSet, files ...*ast.File) {
	tb.Helper()
	var sites []executeBitSite
	for _, f := range files {
		sites = append(sites, executeBitSites(fset, f)...)
	}

	// The legal site is spawnableOn's own. Finding none means the predicate has
	// been rewritten or removed, and a gate reading nothing would report that
	// every rung is clean.
	if len(sites) == 0 {
		tb.Fatal("found no execute-bit test at all — spawnableOn no longer reads the mask this " +
			"gate exists to keep in one place, so it would pass over any number of rungs that do")
		return
	}

	var offenders int
	for _, s := range sites {
		if s.fn == spawnablePredicate {
			continue
		}
		offenders++
		tb.Errorf("%s:%d %s masks a file mode with the POSIX execute bit. Go sets no execute bit "+
			"on Windows (os.Stat reports 0666 for every regular file), so this calls every binary "+
			"there not-executable — issue #224 in the bridge rung, then #393 in the peer rung. "+
			"Ask spawnable(mode, path) instead; %s is the one place that question is answered.",
			s.file, s.line, s.fn, spawnablePredicate)
	}
	if offenders == 0 {
		tb.Logf("%d execute-bit test(s), all inside %s", len(sites), spawnablePredicate)
	}
}

// aRawExecuteBitIsReported is the falsifiability half.
//
// ⚠ A CORPUS WITH ZERO OFFENDERS CANNOT EXERCISE THE BRANCH THAT REPORTS ONE, so
// without this the verdict could be severed and the gate would go on announcing
// that every rung is clean. It drives checkExecuteBitSites — the same function
// the gate calls, not a copy of its loop — over source that is deliberately
// wrong. It is a subtest rather than a sibling because the acceptance fence runs
// one test name.
func aRawExecuteBitIsReported(t *testing.T) {
	// The fixture holds the legal site and three offenders, one per spelling, so
	// a gate keying on the literal text of `0o111` rather than on its value is
	// caught here rather than by whoever next chooses a different base.
	const bad = `package main
func spawnableOn(goos string, mode fs.FileMode) bool {
	return mode&0o111 != 0
}
func executable(info os.FileInfo) bool {
	return info.Mode()&0o111 != 0
}
func legacy(m fs.FileMode) bool  { return m&0111 != 0 }
func decimal(m fs.FileMode) bool { return m&73 != 0 }
func unrelated(m fs.FileMode) bool { return m&0o222 != 0 }`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bad.go", bad, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	rec := &recordingTB{}
	checkExecuteBitSites(rec, fset, f)
	if rec.fatal {
		t.Fatal("the gate declared an empty universe over a fixture holding four execute-bit " +
			"tests — the finder has stopped seeing them")
	}
	if rec.errors != 3 {
		t.Errorf("the gate reported %d offender(s) over a fixture holding three outside %s, one "+
			"inside it, and one masking the WRITE bit; with 0 the verdict is severed, with 1 or 2 a "+
			"spelling escapes it, and with 4 it has started accusing the predicate itself",
			rec.errors, spawnablePredicate)
	}

	// The empty universe is its own branch and needs its own fixture: source with
	// no execute-bit test at all must stop rather than report an all-clear.
	empty, err := parser.ParseFile(fset, "empty.go", "package main\nfunc unrelated() {}", 0)
	if err != nil {
		t.Fatalf("parse the empty fixture: %v", err)
	}
	blank := &recordingTB{}
	checkExecuteBitSites(blank, fset, empty)
	if !blank.fatal {
		t.Error("source holding no execute-bit test did not reach the empty-universe guard, so a " +
			"gate that had stopped finding anything would report success")
	}
	if blank.errors != 0 {
		t.Errorf("the empty universe reported %d offender(s); it should stop, not accuse", blank.errors)
	}
}

// executeBitSites finds every `<expr> & <execute mask>` in a file, whichever way
// the mask is spelled, and records the function it sits in.
//
// It keys on the VALUE rather than on the token text: `0o111`, `0111`, `73` and
// `0x49` are one mask, and a gate that reads only the first spelling is one a
// later author walks past without meaning to.
func executeBitSites(fset *token.FileSet, f *ast.File) []executeBitSite {
	var sites []executeBitSite
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			bin, ok := n.(*ast.BinaryExpr)
			if !ok || bin.Op != token.AND {
				return true
			}
			if !isExecuteMask(bin.X) && !isExecuteMask(bin.Y) {
				return true
			}
			pos := fset.Position(bin.Pos())
			sites = append(sites, executeBitSite{pos.Filename, fn.Name.Name, pos.Line})
			return true
		})
	}
	return sites
}

// isExecuteMask reports whether an expression is the execute-bit literal in any
// base Go accepts. ParseInt with base 0 reads the prefix, so one call covers
// every spelling.
func isExecuteMask(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return false
	}
	v, err := strconv.ParseInt(lit.Value, 0, 64)
	return err == nil && v == executeBitMask
}
