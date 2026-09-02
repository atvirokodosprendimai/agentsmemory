package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// relocationRefusalClaim matches a description asserting that a long or chunked
// memory cannot be relocated.
//
// It is deliberately narrow. The looser rule — "descriptions must be true" — is
// not gateable, and a matcher broad enough to catch any sentence about chunking
// would flag the advice this package SHOULD keep saying: that content over the
// threshold is chunked, and that one drawer is one vector. What is banned is the
// claim of a REFUSAL that ADR-045 removed.
var relocationRefusalClaim = regexp.MustCompile(`(?i)never\s+moved|cannot be moved|relocated for life|can never be (?:moved|relocated)`)

// descriptionStringsIn returns every string literal in a Go file, concatenations
// flattened, so a description built with fmt.Sprintf across a dozen "+"-joined
// lines is matched as the one sentence a caller actually reads.
//
// Matching the raw file text instead would work too and was rejected: it cannot
// tell a description from a comment, and this package's comments discuss the very
// claim being banned — including the one directly above the description, which
// records why the number is derived rather than restated. A gate that fires on the
// commentary explaining it is a gate people delete.
func descriptionStringsIn(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0) // no comments: they are not the surface
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	var flatten func(ast.Expr) (string, bool)
	flatten = func(e ast.Expr) (string, bool) {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				return strings.Trim(v.Value, "`\""), true
			}
		case *ast.BinaryExpr:
			if v.Op == token.ADD {
				l, lok := flatten(v.X)
				r, rok := flatten(v.Y)
				if lok || rok {
					return l + r, true
				}
			}
		case *ast.CallExpr:
			// fmt.Sprintf("...", args) — the format string is the sentence.
			if len(v.Args) > 0 {
				return flatten(v.Args[0])
			}
		}
		return "", false
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, arg := range call.Args {
			if s, ok := flatten(arg); ok && len(s) > 40 {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// TestNoToolDescriptionClaimsALongMemoryCannotBeMoved keeps ADR-045's retirement
// from being undone one sentence at a time.
//
// The am_add_drawer description told every session that a memory over the chunk
// threshold is "never MOVED" and that a short one "can be relocated for life".
// That was true, and it is the reason agents spent rounds trimming to fit —
// measured on this repo 2026-09-01, four measure-and-trim rounds on one record
// whose only purchase was keeping the move available. ADR-045 made it false.
//
// A description is the ONLY route by which a caller learns what the server will
// accept, so a false one is not cosmetic: it is a capability that exists and that
// nobody will use. Nothing else in the tree checks it — reachability gates ask
// whether code is selected, not whether the sentence describing it is still true.
//
// ⚠ SCOPE IS IN THE NAME: the universe is this package's tool descriptions, not
// every string in the repository. A gate whose name claims more than it covers is
// worse than a narrower one — the rule TestEveryOmitemptyWireKeyInThisPackageIsDescribed
// already states for the same reason.
func TestNoToolDescriptionClaimsALongMemoryCannotBeMoved(t *testing.T) {
	// The falsifiability half is a SUBTEST, not a sibling: it must sit inside the
	// one command the acceptance fence runs, or a mutation campaign can report
	// "killed" from a fence that never executed it. A corpus with zero offenders
	// cannot exercise the branch that reports one, so the branch is driven over a
	// fixture that IS an offender — through the same regexp, not a copy of it.
	t.Run("the matcher catches the sentence this gate was written against", func(t *testing.T) {
		retired := "⚠That threshold binds at CREATION: a multi-chunk memory can be CORRECTED " +
			"but never MOVED, because moving one chunk would split the memory across two scopes."
		if !relocationRefusalClaim.MatchString(retired) {
			t.Error("the matcher does not match the exact clause ADR-045 retired, so this gate " +
				"cannot fail on the thing it is named for and proves nothing about the corpus")
		}
		keep := "Content over 1600 runes is chunked into several drawers sharing a parent. One " +
			"drawer is one vector, so a shorter memory matches more sharply."
		if relocationRefusalClaim.MatchString(keep) {
			t.Error("the matcher flags the chunking ADVICE this package should keep saying; a gate " +
				"that forbids the true sentence as well as the false one gets deleted")
		}
	})

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		for _, s := range descriptionStringsIn(t, path) {
			checked++
			if loc := relocationRefusalClaim.FindString(s); loc != "" {
				t.Errorf("%s: a tool description still claims %q. ADR-045 made a memory of any "+
					"chunk count relocatable; a description is the only way a caller learns that, "+
					"so this sentence turns a shipped capability into one nobody uses.", path, loc)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no description strings were examined, so this gate passed without looking at " +
			"anything — the shape that makes a green run meaningless")
	}
	t.Logf("examined %d description string(s) across %d file(s)", checked, len(files))
}

// TestNoLiveToolDescriptionClaimsALongMemoryCannotBeMoved closes the half the
// static scan cannot reach.
//
// TestNoToolDescriptionClaimsALongMemoryCannotBeMoved parses SOURCE and collects
// string literals used as call arguments, which was the whole surface until
// descriptions started carrying GENERATED text: classifyTool appends a retry
// contract built from writeToolSemantics, and neither a retrySentence return
// literal nor a `why` field is a call argument, so a retired claim reintroduced
// through the generator would restore the banned sentence while the load-bearing
// gate stayed green. Reported by review on the commit that added the generator.
//
// It reads the LIVE descriptions for that reason — the same surface a caller
// receives, after every append the server makes.
func TestNoLiveToolDescriptionClaimsALongMemoryCannotBeMoved(t *testing.T) {
	_, live := liveSurface(t, false)
	if len(live) == 0 {
		t.Fatal("no live tools were listed, so this gate examined nothing")
	}
	for _, tool := range live {
		if loc := relocationRefusalClaim.FindString(tool.Description); loc != "" {
			t.Errorf("%s's served description claims %q. ADR-045 made a memory of any chunk count "+
				"relocatable; a description is the only way a caller learns that, so this sentence "+
				"turns a shipped capability into one nobody uses.", tool.Name, loc)
		}
	}
	t.Logf("examined %d live description(s)", len(live))
}
