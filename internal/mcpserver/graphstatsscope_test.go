package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// A TOOL THAT FILTERS AND DOES NOT SAY SO GETS ITS FILTER FILED AS A BUG.
//
// GraphRoomWings excludes the room named "general" on purpose — am_mine's default
// room is not a navigable idea — and am_graph_stats' description said only "room
// totals". So its room count is legitimately below am_list_rooms', and three
// separate sessions measured the gap and filed it as an undercount: 24 vs 25 on
// 2026-08-23, 38 vs 39 on 2026-08-27, 43 vs 44 on 2026-09-06. The second filing
// notes it was "the second time" and proposes fixing the mechanism; the mechanism
// was never wrong. Each session then guessed a cause — the standing hypothesis was
// that a room with no derived edge is invisible — and none of them was the filter
// sitting in one Where clause.
//
// AGENTS.md §Reachability already states the rule this violates from the other
// side: a description is the only route by which a caller learns what the server
// does, so a description that omits a filter unships nothing and costs a
// re-investigation every time somebody compares two tools.
//
// The gate DERIVES both halves rather than comparing two strings somebody typed.
// Change the excluded room in palace and this fails until the description follows;
// drop the disclosure and it fails too. Equality between two literals in one
// package would pin neither.

// excludedGraphRoom reads the room name GraphRoomWings filters out, from the
// palace package's own source.
//
// It binds the argument by the room placeholder's POSITION in the query, counting
// the `?` that precede it — which is how the driver binds it too. Reading the
// value rather than trusting a copy of it here is the whole point of the gate.
//
// ⚠ IT USED TO TAKE THE LAST ARGUMENT, and that was right only by coincidence.
// Review of PR #320: the room's `?` happens to be last today, so appending one
// more wing-exclusion placeholder to the same Where call silently rebound the
// extractor to that new literal, and the gate failed naming a WING as the
// excluded ROOM. It failed, which is the safe direction, but with a confidently
// wrong message that would send the next reader to disclose the wrong thing.
// This is the one step where the gate stopped deriving and started counting from
// the end.
func excludedGraphRoom(tb testing.TB, path string) string {
	tb.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}
	var room string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Where" {
			return true
		}
		query, ok := stringLiteral(call.Args[0])
		if !ok || !strings.Contains(query, "room != ?") {
			return true
		}
		// Args[0] is the query, so the Nth placeholder binds Args[1+N]. Count only
		// the `?` strictly BEFORE `room != ?`, never its own.
		at := strings.Index(query, "room != ?")
		idx := 1 + strings.Count(query[:at], "?")
		if idx >= len(call.Args) {
			tb.Fatalf("%s: the room placeholder is the %d(th) but the Where call passes %d "+
				"argument(s); the query and its bindings disagree, and a gate that guessed here "+
				"would report whichever literal it happened to land on", path, idx, len(call.Args)-1)
		}
		if lit, ok := stringLiteral(call.Args[idx]); ok {
			room = lit
		}
		return true
	})
	if room == "" {
		tb.Fatalf("%s: found no Where clause binding a room exclusion; either the filter moved "+
			"or this extractor stopped seeing it, and both make the gate below vacuous", path)
	}
	return room
}

// stringLiteral unwraps a plain Go string literal.
func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, "`\""), true
}

// graphStatsDescription returns the sentence am_graph_stats ships to callers.
func graphStatsDescription(tb testing.TB, path string) string {
	tb.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		tb.Fatalf("parse %s: %v", path, err)
	}
	var desc string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "registerGraphStats" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WithDescription" {
				return true
			}
			if lit, ok := stringLiteral(call.Args[0]); ok {
				desc = lit
			}
			return true
		})
	}
	if desc == "" {
		tb.Fatalf("%s: registerGraphStats declares no literal description; the gate below would "+
			"pass over a tool that describes itself with nothing", path)
	}
	return desc
}

// describesTheExcludedRoom reports whether a description discloses the filter.
//
// Returned rather than asserted so the falsifiability half can drive the SAME
// predicate over a description that does not, which a copy of the comparison could
// not do.
func describesTheExcludedRoom(description, room string) bool {
	return strings.Contains(description, room)
}

// TestGraphStatsDescribesTheRoomsItExcludes is the gate.
func TestGraphStatsDescribesTheRoomsItExcludes(t *testing.T) {
	room := excludedGraphRoom(t, "../palace/graph.go")
	desc := graphStatsDescription(t, "graph.go")

	if !describesTheExcludedRoom(desc, room) {
		t.Errorf("GraphRoomWings excludes the room %q and am_graph_stats' description never names "+
			"it, so its room total is below am_list_rooms' for a reason no caller can discover. "+
			"Three sessions have filed that gap as an undercount; the description is the only "+
			"place that can stop a fourth.", room)
	}

	t.Run("a description that omits the room is caught", func(t *testing.T) {
		// A corpus with the disclosure in place cannot exercise the failing branch,
		// so it is driven over a description that IS an offender — and over one that
		// is not, because a matcher that flags everything pins nothing either.
		silent := "Return aggregate metrics about the team's graph: room totals, cross-wing " +
			"connectors, edges, rooms-per-wing, and the top connectors."
		if describesTheExcludedRoom(silent, room) {
			t.Errorf("the predicate accepts the exact description that shipped while three "+
				"sessions filed the gap, so it cannot fail on the thing it is named for "+
				"(room %q)", room)
		}
		if !describesTheExcludedRoom(silent+" The room "+room+" is excluded.", room) {
			t.Error("the predicate rejects a description that does disclose the filter; a gate " +
				"that cannot pass is one somebody deletes rather than satisfies")
		}
	})
}
