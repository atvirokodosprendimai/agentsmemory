package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// conditionalUndescribedOnPurpose lists conditional response keys that need no
// sentence in the tool that emits them, with the reason.
//
// Keyed by "tool.key" rather than by key alone, because this gate credits a key
// from the description of the TOOL THAT EMITS IT: the same key can be obvious on
// one surface and undiscoverable on another. That is the whole finding behind
// issue #239 — `facts` was documented on am_get_drawer and absent from
// am_search, and a package-wide check reads that as described.
var conditionalUndescribedOnPurpose = map[string]string{}

// responseMapIdent is the response map this package builds its answers in.
//
// Keyed on the identifier `out` because that is the convention every handler
// here follows. A handler naming its response map something else is outside this
// gate — recorded rather than pretended away, and the vacuity guard below is what
// catches the day the convention changes wholesale.
const responseMapIdent = "out"

// TestEveryConditionalWireKeyIsDescribedByItsOwnTool: a response key assigned
// inside an `if` is absent by construction until the case that produces it,
// exactly like an `omitempty` struct field, and must be named in the prose of the
// tool that emits it.
//
// ⚠ THIS IS THE POPULATION TestEveryOmitemptyWireKeyInThisPackageIsDescribed
// NAMES AND DECLINES. Its own comment says so: "A THIRD population is invisible
// to any struct-tag scan: conditional map[string]any keys, set inside `if` blocks
// … Out of scope here and named so the next reader knows it." That was a
// deliberate deferral and issue #239 is it coming due — am_search rendered
// facts, elsewhere_wings and unlocatable_facts, no description named them, and a
// struct-tag regexp cannot see a map key.
//
// ⚠ PER TOOL, NOT PER PACKAGE, AND THAT IS THE HALF THAT CATCHES THE BUG. The
// older gate pools every description in the package into one string, so a key is
// credited to prose belonging to a different tool. Measured while writing this:
// `facts` passes a package-wide check because am_get_drawer documents its own
// fact block, while am_search — the surface the issue was filed about — said
// nothing. "Documented somewhere" and "discoverable here" are not the same claim.
//
// ⚠ ATTRIBUTION IS BY FUNCTION AND ONE CALL HOP, NOT BY FILE POSITION, and the
// first draft of this gate got that wrong in a way worth recording: it bounded a
// tool's region at the next `newTool(` in the file, so `addDrawerFacts` — a
// helper shared by the fetch path — had its keys credited to `reconnect`, whose
// registration merely happened to sit above it. The gate reported
// `reconnect.next_cursor` and `reconnect.withheld`, neither of which reconnect
// can emit. A mis-attributing gate is worse than none: it is a confident wrong
// answer, and the reader has no way to tell it from a real finding.
//
// The one hop is a deliberate bound, not an oversight. A key rendered two helpers
// deep is not attributed to any tool and so is not checked; widening to a full
// call graph is a bigger change than this, and the limit is stated here so the
// next reader knows the shape of what is missed rather than assuming coverage.
func TestEveryConditionalWireKeyIsDescribedByItsOwnTool(t *testing.T) {
	pkg := parsePackage(t)

	// ⚠ A KEY BEYOND ONE HOP WOULD GO UNCHECKED SILENTLY, so it is counted rather
	// than left to the comment above. A helper that renders a conditional key and
	// is called by no registration attributes to no tool, so every key in it
	// disappears from this gate without anything going red. That set is empty
	// today; a helper-of-a-helper added tomorrow would otherwise make the gate
	// quietly narrower than its name.
	if orphans := pkg.unattributedHelpers(); len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf("conditional response keys render in %v, which no tool registration calls "+
			"directly.\n  They are attributed to no tool, so nothing checks whether a caller can "+
			"discover them. Either call the helper from the registration, or widen the attribution "+
			"past one hop — do not leave the gate narrower than its name.", orphans)
	}

	msgs := pkg.undescribed()

	// A universe of zero is a gate that cannot fail. This package renders dozens of
	// conditional keys; zero means the shape changed and the check went quiet.
	if pkg.conditionalKeyCount == 0 {
		t.Fatal("no conditional response-map key found in this package — it renders dozens, " +
			"so the pattern stopped matching and this check is passing vacuously")
	}
	if len(pkg.registrations) == 0 {
		t.Fatal("no tool registration found — the newTool matcher broke, and with no tool to " +
			"attribute a key to every finding would silently disappear")
	}

	for _, m := range msgs {
		t.Errorf("%s is rendered only when its case occurs and is named in no description of "+
			"that tool.\n  A caller who has not hit that case cannot learn the key exists. Name it "+
			"in the tool's own prose, or add \"tool.key\" to conditionalUndescribedOnPurpose with "+
			"the reason.", m)
	}

	// ⚠ A CORPUS WITH ZERO OFFENDERS CANNOT EXERCISE THE BRANCH THAT REPORTS ONE,
	// so the half above passing proves only that the tree is clean — never that the
	// gate can still see dirt. This drives the SAME analyze() over a fixture that IS
	// an offender. It is a SUBTEST rather than a sibling so one -run filter covers
	// both, and it shares the extractor rather than copying it: this repo has
	// shipped a falsifiability half that reimplemented the check twice, and
	// severing the real one left both green.
	t.Run("catches an offender", func(t *testing.T) {
		pkg := analyze(t, map[string]string{"fixture.go": offenderFixture})
		got := map[string]bool{}
		for _, m := range pkg.undescribed() {
			got[strings.SplitN(m, " ", 2)[0]] = true
		}

		// Undescribed and conditional, in the handler and one hop out.
		for _, want := range []string{"fixture_tool.undescribed_key", "fixture_tool.helper_key"} {
			if !got[want] {
				t.Errorf("the extractor did not report %s; with the fixture unchanged that means "+
					"this gate has stopped finding the defect it exists for, and the clean run "+
					"above proves nothing", want)
			}
		}
		// Described, so discoverable; and unconditional, so present in every answer.
		for _, unwanted := range []string{"fixture_tool.described_key", "fixture_tool.always"} {
			if got[unwanted] {
				t.Errorf("the extractor reported %s, which is not the class: a key named in the "+
					"tool's own prose, or set unconditionally, is discoverable from one call", unwanted)
			}
		}
	})
}

// offenderFixture is a registration that IS the defect: one conditional key named
// in its prose, one that is not, one rendered a hop out in a helper, and one set
// unconditionally. It is source text rather than a testdata file so the shape the
// gate must catch is readable beside the assertions about it.
const offenderFixture = `package fixture

func registerFixture(reg *registrar) {
	tool := newTool("fixture_tool",
		mcp.WithDescription("Prose naming described_key and nothing else."),
	)
	reg.add(tool, func() any {
		out := map[string]any{}
		out["always"] = 1
		if cond {
			out["described_key"] = 2
			out["undescribed_key"] = 3
		}
		fixtureHelper(out)
		return out
	})
}

func fixtureHelper(out map[string]any) {
	if cond {
		out["helper_key"] = 4
	}
}
`

// undescribed returns "tool.key (where)" for every conditional key a tool renders
// and its own prose does not name, exemptions removed, sorted.
func (p *packageView) undescribed() []string {
	var msgs []string
	seen := map[string]bool{}
	for _, reg := range p.registrations {
		for key, where := range p.keysFor(reg) {
			id := reg.tool + "." + key
			if seen[id] {
				continue
			}
			if _, exempt := conditionalUndescribedOnPurpose[id]; exempt {
				continue
			}
			if reg.describes(key) {
				continue
			}
			seen[id] = true
			msgs = append(msgs, id+" ("+where+")")
		}
	}
	sort.Strings(msgs)
	return msgs
}

// TestConditionalUndescribedOnPurposeIsJustified refuses an entry with no reason,
// one naming a key no tool renders, and one that has stopped excusing anything.
//
// The last two are what rots. An exemption outliving the key it excused, or left
// behind after the prose caught up, reads exactly like a live judgement — the
// failure TestUndescribedOnPurposeIsJustified records for its own list.
func TestConditionalUndescribedOnPurposeIsJustified(t *testing.T) {
	pkg := parsePackage(t)

	rendered := map[string]bool{}
	described := map[string]bool{}
	for _, reg := range pkg.registrations {
		for key := range pkg.keysFor(reg) {
			id := reg.tool + "." + key
			rendered[id] = true
			if reg.describes(key) {
				described[id] = true
			}
		}
	}

	for id, why := range conditionalUndescribedOnPurpose {
		if strings.TrimSpace(why) == "" {
			t.Errorf("conditionalUndescribedOnPurpose[%q] has no reason — the reason IS the review", id)
		}
		if !strings.Contains(id, ".") {
			t.Errorf("conditionalUndescribedOnPurpose[%q] is not in \"tool.key\" form, so it "+
				"excuses nothing and matches no finding", id)
			continue
		}
		if !rendered[id] {
			t.Errorf("conditionalUndescribedOnPurpose[%q] excuses a key no tool renders "+
				"conditionally any more; delete the entry rather than leaving a name for "+
				"something that is not there", id)
		}
		if described[id] {
			t.Errorf("conditionalUndescribedOnPurpose[%q] is dead: the key IS named in that "+
				"tool's prose, so the exemption excuses nothing. Delete it — an unnecessary "+
				"entry reads exactly like a necessary one.", id)
		}
	}
}

// registration is one tool: its name, the prose an agent reads at that call, the
// keys its own body renders conditionally, and the package-local functions it
// calls.
type registration struct {
	tool        string
	fn          string
	file        string
	description string
	calls       map[string]bool
	keys        map[string]bool
}

// describes reports whether this tool's prose names the key on a WORD BOUNDARY.
// The older gate records why a substring is not enough: `stale` was credited to
// the word "staleness" in a sentence about erasure.
func (r *registration) describes(key string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\b`).MatchString(r.description)
}

type packageView struct {
	registrations       []*registration
	helperKeys          map[string]map[string]bool // function name -> keys it renders conditionally
	helperFile          map[string]string
	conditionalKeyCount int
}

// keysFor is the tool's own conditional keys plus those of the package-local
// helpers it calls, one hop out.
func (p *packageView) keysFor(reg *registration) map[string]string {
	out := map[string]string{}
	for key := range reg.keys {
		out[key] = reg.file
	}
	for callee := range reg.calls {
		for key := range p.helperKeys[callee] {
			if _, already := out[key]; !already {
				out[key] = p.helperFile[callee] + " via " + callee
			}
		}
	}
	return out
}

var newToolCall = regexp.MustCompile(`newTool\(\s*"([a-z_]+)"`)

// unattributedHelpers names functions that render a conditional response key but
// are called by no registration, so their keys reach no tool's prose check.
//
// A registration renders its own keys, so it is never its own orphan.
func (p *packageView) unattributedHelpers() []string {
	called := map[string]bool{}
	isRegistration := map[string]bool{}
	for _, reg := range p.registrations {
		isRegistration[reg.fn] = true
		for callee := range reg.calls {
			called[callee] = true
		}
	}
	var orphans []string
	for fn := range p.helperKeys {
		if isRegistration[fn] || called[fn] {
			continue
		}
		orphans = append(orphans, fn)
	}
	return orphans
}

// parsePackage reads this package's non-test sources and hands them to analyze.
func parsePackage(t *testing.T) *packageView {
	t.Helper()
	return analyze(t, packageSources(t))
}

// analyze indexes, per function, the conditional response keys it renders and the
// package-local functions it calls; then binds each tool registration to the
// function that declares it.
//
// It takes sources rather than reading them so the falsifiability subtest can
// drive THIS function — not a copy of it — over a fixture that is an offender. A
// falsifiability half that reimplements the extraction pins nothing: this repo
// has shipped that mistake twice (TestASpecBindingThatNamesNothingIsCaught and
// TestAHumanObservedSignOffAgreesWithTheIndex both record it), and severing the
// real check left both green.
func analyze(t *testing.T, sources map[string]string) *packageView {
	t.Helper()
	view := &packageView{
		helperKeys: map[string]map[string]bool{},
		helperFile: map[string]string{},
	}
	fset := token.NewFileSet()

	for path, src := range sources {
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			keys := conditionalKeysIn(fn)
			view.conditionalKeyCount += len(keys)
			if len(keys) > 0 {
				view.helperKeys[fn.Name.Name] = keys
				view.helperFile[fn.Name.Name] = path
			}

			// A registration is the function whose body constructs a tool. Its
			// text is bounded by the function, so a helper declared beneath it in
			// the same file is not swept in by position.
			body := src[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset]
			m := newToolCall.FindStringSubmatch(body)
			if m == nil {
				continue
			}
			reg := &registration{
				tool:  m[1],
				fn:    fn.Name.Name,
				file:  path,
				keys:  keys,
				calls: calleesIn(fn),
			}
			reg.description = descriptionsIn(fn)
			view.registrations = append(view.registrations, reg)
		}
	}
	return view
}

// conditionalKeysIn returns the literal response-map keys this function assigns
// inside a conditional. Nesting counts: a key set two blocks deep is still absent
// until its case occurs.
func conditionalKeysIn(fn *ast.FuncDecl) map[string]bool {
	keys := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		branch, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		ast.Inspect(branch, func(inner ast.Node) bool {
			assign, ok := inner.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				if key, ok := responseKeyOf(lhs); ok {
					keys[key] = true
				}
			}
			return true
		})
		return true
	})
	return keys
}

// descriptionsIn returns the prose an agent reads at this tool's call: every
// string literal inside a Description or WithDescription argument, tool-level and
// per-parameter alike, since the older gate's rule is that either counts.
//
// ⚠ IT READS THE AST RATHER THAN REUSING descriptionText, AND THE FALSIFIABILITY
// SUBTEST IS WHAT FORCED THAT. The shared regexp ends at a literal `))`, which
// holds for `mcp.WithString(name, mcp.Description(…))` and fails for a
// tool-level `mcp.WithDescription(…)` that no second paren follows — it matches
// nothing, the tool reads as having no prose, and every key it renders reports as
// undescribed. On the real tree that never fired, because a later `))` always
// exists to close on and the over-capture happens to include the text; on the
// fixture it fired immediately. Walking the call is exact, needs no closing-paren
// coincidence, and handles a concatenated or fmt.Sprintf'd description, which the
// registrations here use.
func descriptionsIn(fn *ast.FuncDecl) string {
	var prose strings.Builder
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Description" && sel.Sel.Name != "WithDescription") {
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(inner ast.Node) bool {
				lit, ok := inner.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if text, err := strconv.Unquote(lit.Value); err == nil {
					prose.WriteString(text)
					prose.WriteString("\n")
				}
				return true
			})
		}
		return true
	})
	return prose.String()
}

// calleesIn returns the names of package-local functions this function calls.
func calleesIn(fn *ast.FuncDecl) map[string]bool {
	calls := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok {
			calls[ident.Name] = true
		}
		return true
	})
	return calls
}

// responseKeyOf returns the literal key of an assignment into the response map,
// e.g. `out["stale_hits"] = …`, and whether the expression is one.
func responseKeyOf(lhs ast.Expr) (string, bool) {
	index, ok := lhs.(*ast.IndexExpr)
	if !ok {
		return "", false
	}
	ident, ok := index.X.(*ast.Ident)
	if !ok || ident.Name != responseMapIdent {
		return "", false
	}
	lit, ok := index.Index.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	key, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return key, true
}
