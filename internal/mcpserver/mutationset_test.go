package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeCalls are the ORM calls that change stored state. Everything else in the
// mutation analysis is derived; this is the only hand-written input, and it is
// small enough to read in one glance, which is the point.
var writeCalls = map[string]bool{
	"Create": true, "CreateInBatches": true, "Save": true, "Updates": true,
	"Update": true, "Delete": true, "Exec": true, "FirstOrCreate": true,
}

// incidentalWrites are methods the analysis correctly sees writing a row, which
// nonetheless must NOT require the writer role: the row is observability about a
// read, not memory a caller can recall later.
//
// This is a JUDGEMENT the analysis cannot make. "Writes a row" is derivable from
// the source; "changes what someone can recall" is not, and conflating them here
// would have made am_search a write tool and locked every read-only member out of
// recall — a worse outcome than the gap this file closes.
//
// Each entry names the row it writes. An entry that is not actually write-reaching
// is rejected below, so the list cannot be used to excuse a method that stopped
// writing (or never did).
var incidentalWrites = map[string]string{
	// SearchPage is where that row is actually written; Search is now a projection
	// of it and inherits the classification through the call. Both are listed
	// because the analysis reaches each of them, and dropping either would let a
	// genuine write into the read set on the next rename.
	"SearchPage":     "records a best-effort search_events row about the read it just served; the write must never fail the read, and it stores no memory",
	"Search":         "records a best-effort search_events row about the read it just served; recallstats.go:21 states the write must never fail the read",
	"RecallStats":    "reads search_events to summarise recall; reachable writes come from shared helper names, not from memory tables",
	"CheckDuplicate": "embeds the candidate text to compare it, and stores no drawer",
	// ADR-028 T3, and the same judgement as SearchPage one tool over. A fetch row
	// is observability ABOUT a read — which drawer a caller opened and which recall
	// sent them — and it stores no memory anybody can recall later. Classifying it
	// as a genuine write would make am_get_drawer a write tool and lock every
	// read-only member out of FETCHING A DRAWER, which is the worst possible
	// outcome for a signal whose entire purpose is to observe reading.
	"RecordFetch":  "records a best-effort drawer_fetches row about the read it just served; the write must never fail the read, and it stores no memory",
	"CountFetches": "reads drawer_fetches to publish two counts; reachable writes come from shared helper names, not from memory tables",
}

// TestMutatingCallListIsComplete: every service method an MCP handler calls that
// can change stored memory must appear in mutatingCalls.
//
// mutatingCalls is the input to TestEveryMutatingToolIsRegisteredAsAWrite, which
// is what forces a mutating tool through the role guard. So a name missing from
// that list does not merely weaken a check — it removes a tool from the
// authorization gate entirely, silently, and the suite stays green. ADR-012
// recorded exactly this as its top risk and shipped without closing it; the
// comment beside the list even named this test, which did not exist.
//
// "Mutates" is computed transitively, because only four exported palace methods
// write to the database directly — every other write goes through a repository
// layer, and a check that scanned one level would have called the domain almost
// entirely read-only.
func TestMutatingCallListIsComplete(t *testing.T) {
	mutating := mutatingMethods(t, filepath.Join("..", "palace"), filepath.Join("..", "skill"))
	if len(mutating) < 10 {
		t.Fatalf("only %d mutating methods found across the domain packages — the analysis has "+
			"stopped reading them, and a check that finds nothing to require finds no gaps either",
			len(mutating))
	}

	called := methodsCalledByHandlers(t)
	if len(called) == 0 {
		t.Fatal("no service calls found in any register* function; this check reads nothing")
	}

	var missing []string
	for name := range called {
		if mutating[name] && !mutatingCalls[name] && incidentalWrites[name] == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s changes stored memory and an MCP handler calls it, but it is absent from "+
			"mutatingCalls.\n  Any tool whose only mutating call is %s is therefore classified "+
			"read-only and skips the role guard entirely.", m, m)
	}

	// The exception list is gated too, or it becomes the hole. An entry the analysis
	// does not see writing at all is excusing nothing, and its presence makes the
	// list read as more considered than it is.
	for name, why := range incidentalWrites {
		if why == "" {
			t.Errorf("%s is excused as an incidental write with no reason given", name)
		}
		if !mutating[name] {
			t.Errorf("%s is listed in incidentalWrites, but the analysis does not see it writing "+
				"anything — a stale excuse outlives the thing it excused", name)
		}
		if mutatingCalls[name] {
			t.Errorf("%s is in BOTH mutatingCalls and incidentalWrites; one of the two is wrong", name)
		}
	}

	// A stale entry is the other half: a name nothing mutates any more makes the
	// list look more complete than it is, and the next reader trusts it.
	var stale []string
	for name := range mutatingCalls {
		if !mutating[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Logf("mutatingCalls names %d method(s) the analysis does not see as mutating: %v\n"+
			"  Not an error — a name may belong to a package this analysis does not read — but a "+
			"list of names that match nothing is how it rots.", len(stale), stale)
	}
}

// mutatingMethods returns every function or method in the given packages that can
// reach an ORM write, propagated to a fixed point over the intra-package call
// graph.
func mutatingMethods(t *testing.T, dirs ...string) map[string]bool {
	t.Helper()
	calls := map[string][]string{} // func name -> names it calls
	direct := map[string]bool{}    // func name -> writes directly

	for _, dir := range dirs {
		pkgs, err := parser.ParseDir(token.NewFileSet(), dir, notATest, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}
		for _, p := range pkgs {
			for _, f := range p.Files {
				for _, d := range f.Decls {
					fn, ok := d.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						continue
					}
					name := fn.Name.Name
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						call, ok := n.(*ast.CallExpr)
						if !ok {
							return true
						}
						switch fun := call.Fun.(type) {
						case *ast.SelectorExpr:
							if writeCalls[fun.Sel.Name] {
								direct[name] = true
							}
							calls[name] = append(calls[name], fun.Sel.Name)
						case *ast.Ident:
							calls[name] = append(calls[name], fun.Name)
						}
						return true
					})
				}
			}
		}
	}

	mut := map[string]bool{}
	for k := range direct {
		mut[k] = true
	}
	// Fixed point: a function that calls a mutating function mutates.
	for changed := true; changed; {
		changed = false
		for name, callees := range calls {
			if mut[name] {
				continue
			}
			for _, c := range callees {
				if mut[c] {
					mut[name] = true
					changed = true
					break
				}
			}
		}
	}
	return mut
}

// methodsCalledByHandlers returns the method names any register* function calls
// on a collaborator, which is the set the mutation question is asked about.
func methodsCalledByHandlers(t *testing.T) map[string]bool {
	t.Helper()
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", notATest, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	out := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "register") {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if sel, ok := n.(*ast.SelectorExpr); ok {
						out[sel.Sel.Name] = true
					}
					return true
				})
			}
		}
	}
	return out
}
