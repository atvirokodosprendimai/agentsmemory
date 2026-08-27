package palace

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-038 T6. The two properties this ADR decides, checked by exit code rather
// than by prose.
//
// Both gates DERIVE their universe from the source rather than reading a
// hand-kept list, because a list beside the truth is the shape this repository
// keeps being bitten by: a rule everybody agrees with, and four call sites that
// never heard of it.

// parsePalace parses this package's non-test files once per gate.
func parsePalace(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".",
		func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") },
		parser.ParseComments)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	out := map[string]*ast.File{}
	for _, p := range pkgs {
		for name, f := range p.Files {
			out[name] = f
		}
	}
	if len(out) == 0 {
		t.Fatal("parsed no files; this gate reads nothing")
	}
	return fset, out
}

// TestNoPathRederivesADrawerID pins the rule ADR-038 actually decided: a drawer's
// id is minted once and never recomputed.
//
// ⚠ It is expressed as "DrawerID has exactly ONE caller", not as "every call is
// assigned to a ContentKey", and the difference is the whole value of the gate.
// contentKeyOf is where the diary exemption lives — a journal is append-only, so
// its rows carry no key — and a mint that calls DrawerID directly gets a key that
// enters the partial unique index and DEDUPES two identical journal entries into
// one. That shipped: four of the five mint paths called DrawerID directly, and an
// import of two identical reflections produced one row and reported two.
//
// So the property under test is not a naming convention, it is: THERE IS NO WAY TO
// COMPUTE A CONTENT KEY THAT SKIPS THE EXEMPTION. A call site cannot satisfy this
// by restructuring, only by routing through the one function that knows the rule.
//
// It PARSES rather than greps, so a comment naming DrawerID neither satisfies nor
// trips it.
func TestNoPathRederivesADrawerID(t *testing.T) {
	const legalCaller = "contentKeyOf"

	var callers []string
	fset, files := parsePalace(t)
	for name, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok || ident.Name != "DrawerID" {
					return true
				}
				if fn.Name.Name == legalCaller {
					return true
				}
				callers = append(callers, fmt.Sprintf("%s:%d (%s)",
					filepath.Base(name), fset.Position(call.Pos()).Line, fn.Name.Name))
				return true
			})
		}
	}

	for _, where := range callers {
		t.Errorf("%s calls DrawerID directly. Route it through %s, which is where the diary "+
			"exemption lives.\n"+
			"  A key computed here skips that branch, enters the partial unique index, and dedupes\n"+
			"  two identical journal entries into one — the write reports success and one entry is\n"+
			"  simply not there. That is not hypothetical: Add, Mine, AbsorbDrawers and CopyWing all\n"+
			"  did this, and an import of two identical reflections produced one row and reported 2.",
			where, legalCaller)
	}

	// Non-vacuity: the one legal caller must exist, or this gate is asserting the
	// absence of something that is absent for an unrelated reason.
	var found bool
	for _, f := range files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == legalCaller {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("%s does not exist, so this gate is checking that nobody calls DrawerID at all — "+
			"which is a different and much weaker claim than the one it is named for", legalCaller)
	}
}

// TestEveryDrawerMintWritesAContentKey fails when a composite literal builds a
// Drawer with an ID and no ContentKey.
//
// A row with an id and no key is invisible to dedup: repo.Save upserts on
// (team_id, content_key) with `content_key != ”` in the conflict target, so a
// keyless row never matches and every re-file inserts beside it. The universe is
// derived from the source, so a mint path added tomorrow joins this check on the
// same commit rather than when somebody remembers to add it to a list.
//
// ⚠ NAMED LIMIT: it sees COMPOSITE LITERALS only. A mint built field-by-field on a
// `var d Drawer` escapes it entirely, and no wording of this test changes that —
// it is a property of what an AST literal check can see. Said here rather than
// claimed away, because a gate whose blind spot is undocumented reads as coverage
// it does not have.
func TestEveryDrawerMintWritesAContentKey(t *testing.T) {
	var offenders []string
	fset, files := parsePalace(t)
	for name, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := lit.Type.(*ast.Ident)
			if !ok || ident.Name != "Drawer" {
				return true
			}
			var hasID, hasKey bool
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "ID":
					hasID = true
				case "ContentKey":
					hasKey = true
				}
			}
			if hasID && !hasKey {
				offenders = append(offenders,
					fmt.Sprintf("%s:%d", filepath.Base(name), fset.Position(lit.Pos()).Line))
			}
			return true
		})
	}
	for _, where := range offenders {
		t.Errorf("%s builds a Drawer with an ID and no ContentKey.\n"+
			"  repo.Save upserts on (team_id, content_key) with `content_key != ''` in the conflict\n"+
			"  target, so a keyless row never matches: every re-file inserts a duplicate beside it.\n"+
			"  A DIARY mint satisfies this by setting ContentKey: \"\" explicitly, which documents the\n"+
			"  exemption at the site instead of in a list somewhere else.", where)
	}
}

// idClaimSites are the THREE declarations where "a drawer id is derived from its
// content" can be asserted. There are exactly three, which is what makes part (a)
// below exhaustive by construction rather than a guess about wording.
var idClaimSites = []string{"Drawer.ID field", "DrawerID function", "00006_drawers.sql id column"}

// derivationClaims are the phrases that assert the OLD, false nature of the id.
// Used only inside the two narrow scopes below — a phrase list over a whole
// repository is a guess, and this one hit four CSS cache-buster comments the first
// time it was tried.
var derivationClaims = []string{
	"deterministic identity", "deterministic hash", "hash of the locating",
	"derived from its content", "derived from the content", "id is a content hash",
	"recomputed from",
}

// TestNoCommentClaimsADrawerIdIsDerivedFromItsContent stops the documentation
// going false one instance at a time.
//
// ADR-038 changed what a drawer id IS, and prose describing the old nature is not
// a cosmetic problem: the next reader re-derives an id because a comment told them
// that is what ids are, and the anchors, tunnels and kg_triples pointing at the old
// one are silently orphaned. This repository treats documentation as load-bearing
// and enforces it by exit code elsewhere; this is that rule for the one claim
// ADR-038 exists to retire.
//
// It is in TWO parts, because the first draft was 3-for-5 against the very
// instances that motivated it — 00006:18 read "deterministic hash(team,…)" with no
// "of", and DrawerID's comment said "the deterministic IDENTITY of a drawer", so
// ZERO of its four phrases matched either. Reverting either fix would have left it
// green: a test that cannot fail, aimed at the mechanism meant to end that shape.
//
//	(a) asserts the POSITIVE claim at each of the three declarations. Requiring the
//	    word "opaque" to be PRESENT cannot be dodged by rewording the negative,
//	    which is exactly how a phrase blacklist is defeated.
//	(b) sweeps for the old phrases in the two directories that can hold this claim
//	    at all — the incidental mentions sitting on OTHER declarations, which (a)
//	    cannot see by construction.
func TestNoCommentClaimsADrawerIdIsDerivedFromItsContent(t *testing.T) {
	fset, files := parsePalace(t)

	// --- (a) declaration-anchored ------------------------------------------
	seen := map[string]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "DrawerID" && fn.Doc != nil {
				seen["DrawerID function"] = fn.Doc.Text()
			}
			gen, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != "Drawer" {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					for _, n := range field.Names {
						if n.Name == "ID" && field.Doc != nil {
							seen["Drawer.ID field"] = field.Doc.Text()
						}
					}
				}
			}
		}
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "00006_drawers.sql"))
	if err != nil {
		t.Fatalf("read 00006: %v", err)
	}
	for _, line := range strings.Split(string(sql), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "id ") && strings.Contains(line, "--") {
			seen["00006_drawers.sql id column"] = line
		}
	}

	for _, site := range idClaimSites {
		text, found := seen[site]
		if !found {
			t.Errorf("%s carries no comment at all. This gate resolves three KNOWN declarations, so "+
				"a missing one means the declaration moved and the gate silently stopped checking "+
				"it — which is the failure mode a derived universe exists to prevent", site)
			continue
		}
		if !strings.Contains(strings.ToLower(text), "opaque") {
			t.Errorf("%s does not say the id is OPAQUE.\n  got: %s\n"+
				"  Asserted as a PRESENCE rather than an absence on purpose: a blacklist of old\n"+
				"  phrases is defeated by rewording, and this claim has already gone false twice.",
				site, strings.TrimSpace(oneLine(text)))
		}
		for _, sentence := range strings.Split(text, ".") {
			low := strings.ToLower(sentence)
			// Same tense carve-out as (b): Drawer.ID's doc quotes the old sentence in
			// order to explain what changed, and that paragraph is the most useful
			// thing in the file.
			if containsAnyOf(low, []string{"previously", "used to", "no longer", "was wrong", "it once"}) {
				continue
			}
			for _, claim := range derivationClaims {
				if strings.Contains(low, claim) {
					t.Errorf("%s still asserts %q, in the present tense:\n  %s\n"+
						"  ADR-038 made the id opaque; a comment that says otherwise teaches the next\n"+
						"  reader to re-derive it, which orphans every anchor, tunnel and\n"+
						"  knowledge-graph fact pointing at the old one.", site, claim, oneLine(sentence))
				}
			}
		}
	}

	// --- (b) scoped phrase sweep -------------------------------------------
	//
	// Three narrowings, and each one was earned by a false positive this gate
	// produced on its first run over the real tree:
	//
	//   1. SCOPE. This package and the migrations are the only places the claim can
	//      be made about a DRAWER id. Unscoped, these phrases hit four cache-buster
	//      comments about stylesheet hashing — four allowlist entries about CSS to
	//      guard one claim about drawer ids, which is the noise that gets an
	//      allowlist rubber-stamped.
	//   2. SUBJECT. A sentence must also name a drawer id. Without this the sweep
	//      flagged closet.go's CLOSET id, kg.go's triple ids and eval.go's case
	//      ids — all correctly described as derived, because they are.
	//   3. TENSE. A sentence that says what the id USED TO BE is the most valuable
	//      comment in this file, not a defect. Drawer.ID's own doc explains that it
	//      previously read "a deterministic hash of (team, wing, room, source,
	//      chunkIndex)" and why that was wrong twice over. A gate that forced the
	//      deletion of that paragraph would be destroying the record of the change
	//      in the name of keeping documentation true.
	//
	// The carve-outs are at the SENTENCE level, not the file level, so a file stays
	// checked for every other sentence in it — a file allowlist would switch the
	// whole file off to excuse one line.
	subjects := []string{"drawer id", "drawer's id", "drawer.id", "drawers.id", "a drawer:", "the drawer's name"}
	historical := []string{"previously", "used to", "no longer", "was wrong", "before adr-038", "it once"}
	for name, f := range files {
		base := filepath.Base(name)
		if base == "identityrole_test.go" {
			continue // this gate's own phrase list
		}
		for _, group := range f.Comments {
			for _, sentence := range strings.Split(group.Text(), ".") {
				low := strings.ToLower(sentence)
				if !containsAnyOf(low, subjects) || containsAnyOf(low, historical) {
					continue
				}
				for _, claim := range derivationClaims {
					if strings.Contains(low, claim) {
						t.Errorf("%s:%d asserts %q about a drawer id, in the present tense:\n  %s\n"+
							"  ADR-038 made the id opaque. A comment that says otherwise teaches the\n"+
							"  next reader to re-derive it, which orphans every anchor, tunnel and\n"+
							"  knowledge-graph fact pointing at the old one.",
							base, fset.Position(group.Pos()).Line, claim, oneLine(sentence))
					}
				}
			}
		}
	}
}

// containsAnyOf reports whether s contains any of the needles.
func containsAnyOf(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// oneLine flattens a doc comment for a single-line failure message.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
