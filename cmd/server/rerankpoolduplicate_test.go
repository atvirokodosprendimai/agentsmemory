package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// duplicatedDefaults pairs every value config.Default() types a SECOND time with
// the palace constant it is a copy of.
//
// internal/config is deliberately free of a dependency on internal/palace, so
// these numbers are spelled twice rather than imported, and each copy says so in
// a comment. What was missing is the half that makes a deliberate copy safe:
// nothing compared them. Measured 2026-09-07 on main, mutating the CONFIG side —
// the side that matters, because config.Default() is what `--help` prints and
// what an unset flag resolves to:
//
//	RerankWeight 0.5    -> 0.55       go test ./... exit 0
//	RerankNorm   sigmoid -> minmax    go test ./... exit 0
//
// The palace side happened to be caught, but only incidentally, by behaviour
// tests inside internal/palace that depend on the value (TestServedBlendTiesOnA
// TwoCandidatePool, TestSmallPoolArmsDisagree). Those cannot see a config-side
// drift at all, which is why "something went red when I changed it" is not the
// same question as "are the two copies equal".
//
// RerankNorm is the sharpest of the three: ADR-030 shipped sigmoid AHEAD of its
// own corpus eval on an explicit instruction and records that the default reverts
// if the eval contradicts it. A silent drift to minmax is that record being
// overturned by an accident rather than by the evidence it named.
var duplicatedDefaults = []struct {
	field  string // the config.Default() field
	source string // the palace constant it copies, as written in the comment
	got    any
	want   any
}{
	{"RerankPool", "palace.DefaultRerankPool", config.Default().RerankPool, palace.DefaultRerankPool},
	{"RerankWeight", "palace.DefaultRerankWeight", config.Default().RerankWeight, palace.DefaultRerankWeight},
	{"RerankNorm", "palace.DefaultRerankNorm", config.Default().RerankNorm, string(palace.DefaultRerankNorm)},
	{"LexNorm", "palace.DefaultLexNorm", config.Default().LexNorm, palace.DefaultLexNorm},
}

// TestTheDuplicatedRerankPoolDefaultMatchesItsSource pins every value
// config.Default() copies from internal/palace to the constant it copies.
//
// It lives in cmd/server because that is the composition root: the one package
// that already imports both, so the gate costs no new dependency edge — which is
// the entire reason the duplicates exist. A gate inside internal/config would
// create the import the duplication was avoiding, and would then be an argument
// for deleting the copy rather than for checking it.
//
// ⚠ A COPY WITH NO COMPARISON IS NOT A COPY, IT IS A SECOND SOURCE OF TRUTH.
func TestTheDuplicatedRerankPoolDefaultMatchesItsSource(t *testing.T) {
	for _, d := range duplicatedDefaults {
		if d.got != d.want {
			t.Errorf("config.Default().%s = %v, %s = %v.\n"+
				"  internal/config duplicates this to stay free of a dependency on internal/palace, "+
				"so the two must be compared here or the copy drifts in silence: the default an "+
				"operator sees in --help would stop being the value the search uses.",
				d.field, d.got, d.source, d.want)
		}
	}
}

// TestEveryDuplicatedPalaceDefaultIsPinned derives its universe from
// internal/config/config.go itself, so a duplicate added tomorrow joins the check
// on the same commit rather than when somebody remembers this file.
//
// A hand-kept list beside the truth goes stale — this repository's recorded
// failure — and the list above IS such a list. This is what stops it: every
// `palace.Default*` named in a comment in that file must appear in
// duplicatedDefaults. It cannot verify the VALUES (a Go constant cannot be looked
// up by name at run time, which is why the table is typed out), but it can refuse
// a copy that nothing compares, and that is the half that was missing.
func TestEveryDuplicatedPalaceDefaultIsPinned(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file, so the config source cannot be found")
	}
	src := filepath.Join(filepath.Dir(thisFile), "..", "..", "internal", "config", "config.go")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("config source not where this gate expects it (%s): %v", src, err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}

	named := namedPalaceDefaults(f)
	if len(named) == 0 {
		t.Fatalf("%s names no palace.Default* constant in any comment; an empty universe is "+
			"indistinguishable from every duplicate being pinned", src)
	}
	pinned := map[string]bool{}
	for _, d := range duplicatedDefaults {
		pinned[d.source] = true
	}
	for _, n := range named {
		if !pinned[n] {
			t.Errorf("%s names %s as a duplicated default, and duplicatedDefaults does not pin it: "+
				"the copy is compared by nothing and will drift in silence", filepath.Base(src), n)
		}
	}
	t.Logf("%d duplicated palace default(s) named in config.go, all pinned: %s",
		len(named), strings.Join(named, " "))
}

var palaceDefaultRef = regexp.MustCompile(`palace\.Default[A-Za-z0-9_]+`)

// namedPalaceDefaults returns every distinct palace.Default* identifier mentioned
// in a comment in f, sorted.
//
// Comments are the universe rather than the field values because that is where a
// duplicate DECLARES itself: each copy in config.Default() carries a comment
// naming its source, and a copy added without one is a separate defect that no
// gate here can see — flagged in review, not by this test.
func namedPalaceDefaults(f *ast.File) []string {
	seen := map[string]bool{}
	for _, g := range f.Comments {
		for _, m := range palaceDefaultRef.FindAllString(g.Text(), -1) {
			seen[m] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
