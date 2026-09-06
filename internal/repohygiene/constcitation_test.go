package repohygiene

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

// A comment naming a constant is a POINTER, and it is worth exactly what the
// constant is worth. Issue #315: `internal/embed/teiembed` told every reader that
// `palace.MaxEmbedRunes` refused an oversized input before the request was built.
// It did once. ADR-038 T4 deleted that check with the unbounded update path it
// guarded (bfe0b65), and the sentence outlived it by ten days while the constant
// stayed in the tree, read by nothing but the tests asserting it.
//
// Nothing could have caught it. The behaviour is sound — chunking bounds the path
// now — so no behaviour test fails; the constant compiles, so no build breaks; and
// every reachability gate in this repository asks whether code is REACHED, never
// whether the prose pointing at it is still true. §Reachability records the same
// shape from the other side ("Documentation is load-bearing in BOTH directions");
// this is the un-gated half.
//
// The rule is structural rather than linguistic on purpose. Judging whether a
// sentence CLAIMS enforcement needs a matcher over prose, and a matcher over prose
// either flags the correction that explains the history or misses the next
// phrasing. What can be decided without reading English is: does anything execute
// this constant? A constant no serving code reads may be named in its own
// declaration, where the reasoning for the number belongs, and nowhere else.
// Wire it and every citation becomes legal again on the same commit — the gate
// repairs itself rather than needing an exemption.

// constCitation is one comment naming a constant that no non-test code reads.
type constCitation struct {
	// file and line are where the naming comment sits, relative to the root.
	file string
	line int
	// name is the constant, and declaredIn the file declaring it — a reader who
	// wants the reasoning goes there, and that is the only place it may be named.
	name       string
	declaredIn string
}

// wordRE matches a constant name as a whole word, so `ChunkSize` in a comment is
// a citation and `ChunkSizeBytes` is a different symbol.
func wordRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
}

// unreadConstantCitations is the whole scan: it derives its universe from the
// source rather than from a list, so a constant that stops being read joins the
// check on the commit that stops reading it.
//
// ⚠ IT MATCHES ON NAME ALONE, ACROSS PACKAGES, and that is deliberately the
// conservative direction. Two packages may declare the same constant name, and a
// read of either counts as a read of both — so the gate UNDER-reports rather than
// accusing a comment whose pointer resolves fine. A gate that cries wolf gets
// deleted; one that misses a rare aliased case still catches the recorded defect.
//
// Only exported constants are considered. An unexported one is package-local, so
// a comment elsewhere naming it is discussing a different symbol anyway, and the
// name is likelier to be a common English word that would flood the report.
func unreadConstantCitations(tb testing.TB, root string, ignored func(string, bool) bool) (offenders []constCitation, mentions int) {
	tb.Helper()

	fset := token.NewFileSet()
	files := map[string]*ast.File{} // rel path -> parsed file, non-test only
	for _, path := range walk(tb, root, ignored) {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			// A file this package cannot parse is not this gate's business:
			// the build already refuses it, and reporting it here would make a
			// syntax error look like a documentation defect.
			continue
		}
		files[rel] = f
	}
	if len(files) == 0 {
		tb.Fatal("parsed no non-test .go file; the walk or the filter broke, and this gate is " +
			"passing vacuously")
	}

	// Pass 1: every exported constant, and where it is declared.
	declaredIn := map[string]string{}
	declSpecs := map[*ast.ValueSpec]bool{}
	for rel, f := range files {
		for _, d := range f.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, s := range gen.Specs {
				spec, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				declSpecs[spec] = true
				for _, n := range spec.Names {
					if n.IsExported() {
						declaredIn[n.Name] = rel
					}
				}
			}
		}
	}

	// Pass 2: who READS one. Every identifier occurrence that is not the name
	// being declared counts, which covers a bare use, a qualified `pkg.Name`
	// (the selector's Sel is an *ast.Ident), and a use inside another constant's
	// value — the last of which is a real read: it decides a number that ships.
	read := map[string]bool{}
	for _, f := range files {
		declNames := map[*ast.Ident]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok || !declSpecs[spec] {
				return true
			}
			for _, name := range spec.Names {
				declNames[name] = true
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || declNames[id] {
				return true
			}
			if _, isConst := declaredIn[id.Name]; isConst {
				read[id.Name] = true
			}
			return true
		})
	}

	// Pass 3: comments naming one. A citation in the declaring file is exempt —
	// that is where the reasoning for the number belongs, and requiring the
	// declaration to avoid its own name would be absurd.
	for rel, f := range files {
		for _, group := range f.Comments {
			for _, c := range group.List {
				for name, decl := range declaredIn {
					if read[name] || decl == rel {
						continue
					}
					if !wordRE(name).MatchString(c.Text) {
						continue
					}
					mentions++
					offenders = append(offenders, constCitation{
						file:       rel,
						line:       fset.Position(c.Pos()).Line,
						name:       name,
						declaredIn: decl,
					})
				}
			}
		}
	}
	return offenders, mentions
}

// checkUnreadConstantCitations is the gate's whole decision, reporting through a
// testing.TB.
//
// ⚠ THE TB PARAMETER IS THE POINT, and this repository has learnt it four times:
// a falsifiability half that drives only the SCAN leaves the gate's own use of it
// unguarded, so severing the call site keeps the suite green while the gate
// announces that everything resolves. The subtest below substitutes a TB and
// asserts the reporting itself, which is the only form that catches that.
func checkUnreadConstantCitations(tb testing.TB, root string, ignored func(string, bool) bool) {
	tb.Helper()
	offenders, _ := unreadConstantCitations(tb, root, ignored)
	for _, o := range offenders {
		tb.Errorf("%s:%d names %s, which is declared in %s and read by no non-test code.\n"+
			"  A comment naming a constant reads as a pointer to something the program does. "+
			"Either say what actually holds the property (and stop naming the constant), or "+
			"wire the constant so the pointer resolves — issue #315 is the recorded case.",
			o.file, o.line, o.name, o.declaredIn)
	}
}

// TestNoCommentCitesAConstantNothingReads is the gate. Its falsifiability half is
// a SUBTEST rather than a sibling, because this tree carries zero offenders once
// #315 is fixed, so the branch that reports one cannot otherwise be exercised —
// and the acceptance fence runs one test name.
func TestNoCommentCitesAConstantNothingReads(t *testing.T) {
	root := repoRoot(t)
	checkUnreadConstantCitations(t, root, gitignoreMatcher(t, root))

	t.Run("a citation of a constant nothing reads is caught", func(t *testing.T) {
		fixture := t.TempDir()
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(fixture, name), []byte(body), 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		// The declaring file may name both constants; only one of them is read.
		write("decl.go", `package p

const (
	// Unwired is named by a comment in another file and read by nothing.
	Unwired = 4000
	// Wired is read below, so a comment elsewhere may point at it.
	Wired = 1600
)

func use(s string) bool { return len(s) <= Wired }
`)
		// The negative half of the same fixture: a citation of a constant that IS
		// read, and prose of the shape this gate must NOT flag — the corrected
		// teiembed comment, which says what bounds the path without naming a
		// constant nobody executes.
		write("other.go", `package p

// A memory is embedded chunk by chunk whichever call stores it, at most Wired
// characters in one piece. An earlier version of this paragraph named a
// caller-side constant as a guard that stopped an oversized input before the
// request was built; that check was deleted along with the path it protected.
func bounded() int { return 1 }

// Unwired refuses an oversized input before the request is built.
func stale() int { return 2 }
`)

		rec := &recordingTB{}
		checkUnreadConstantCitations(rec, fixture, func(string, bool) bool { return false })
		if rec.errors != 1 {
			t.Fatalf("the gate reported %d offender(s) over a fixture carrying exactly one; a "+
				"falsifiability half that cannot go red pins nothing", rec.errors)
		}

		// And the corrected prose alone must be silent, or the gate would flag the
		// very fix it exists to keep in place.
		only := t.TempDir()
		if err := os.WriteFile(filepath.Join(only, "decl.go"), []byte(`package p

const (
	// Wired is read below.
	Wired = 1600
)

func use(s string) bool { return len(s) <= Wired }
`), 0o600); err != nil {
			t.Fatalf("write decl: %v", err)
		}
		if err := os.WriteFile(filepath.Join(only, "prose.go"), []byte(`package p

// A memory is embedded chunk by chunk whichever call stores it, at most Wired
// characters in one piece.
func bounded() int { return 1 }
`), 0o600); err != nil {
			t.Fatalf("write prose: %v", err)
		}
		quiet := &recordingTB{}
		checkUnreadConstantCitations(quiet, only, func(string, bool) bool { return false })
		if quiet.errors != 0 {
			t.Errorf("the gate reported %d offender(s) over prose citing a constant that IS read; "+
				"a gate that flags the correct sentence is one somebody deletes", quiet.errors)
		}
	})
}
