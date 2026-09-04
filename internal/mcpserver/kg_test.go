package mcpserver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestKgInvalidateRequiresAReason: the half of the store that already kept
// history stops keeping only THAT a fact ended.
//
// valid_to has always recorded that a fact stopped being true. Nothing recorded
// why, so the cheapest half of a correction was kept and the expensive half —
// the reason, which is the part a later reader cannot reconstruct — was dropped
// at the tool boundary.
func TestKgInvalidateRequiresAReason(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_alpha")
	h.MustCall(t, "am_kg_add", map[string]any{
		"subject": "svc", "predicate": "deploys to", "object": "old-host",
	})

	refusal := h.MustRefuse(t, "am_kg_invalidate", map[string]any{
		"subject": "svc", "predicate": "deploys to", "object": "old-host",
	})
	if !strings.Contains(strings.ToLower(refusal), "reason") {
		t.Errorf("the refusal does not name the missing argument: %s", refusal)
	}

	res := h.JSON(t, h.MustCall(t, "am_kg_invalidate", map[string]any{
		"subject": "svc", "predicate": "deploys to", "object": "old-host",
		"reason": "the rack was decommissioned",
	}))
	if n, _ := res["ended_facts"].(float64); n != 1 {
		t.Errorf("ended_facts = %v; want 1", res["ended_facts"])
	}
}

// TestKgSupersedeIsReachableFromTheToolSurface is rung 3 for the atomic verb.
// Service.KGSupersede being correct is worth nothing while no agent can call it:
// the hand-rolled invalidate-then-add stays the only expressible replacement, and
// that is the sequence issue #74 reproduced a day-scale overlap in.
func TestKgSupersedeIsReachableFromTheToolSurface(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_beta")
	h.MustCall(t, "am_kg_add", map[string]any{
		"subject": "svc", "predicate": "deploys to", "object": "old-host",
	})
	res := h.JSON(t, h.MustCall(t, "am_kg_supersede", map[string]any{
		"subject": "svc", "predicate": "deploys to",
		"old_object": "old-host", "new_object": "new-host",
		"reason": "migrated off the old rack",
	}))
	boundary, _ := res["boundary"].(string)
	if !strings.Contains(boundary, "T") {
		t.Errorf("boundary = %q; it must be an instant, never a date — a date-only endpoint is "+
			"stretched to T23:59:59Z and both values stay in effect for 86,400 seconds", boundary)
	}

	facts := h.JSON(t, h.MustCall(t, "am_kg_query", map[string]any{"entity": "svc"}))
	if !strings.Contains(strings.ToLower(h.MustCall(t, "am_kg_query", map[string]any{"entity": "svc"})), "new-host") {
		t.Errorf("the successor fact is not current: %v", facts)
	}
}

// TestAGraphAnswerIsBoundedAndSaysWhatItCut is ADR-053 T1's gate.
//
// ⚠ THE GRAPH WAS THE ONE AGENT-FACING READ WITH NO BOUND. `am_search` and
// `am_list_drawers` have spent `ResponseBudget` runes since ADR-044; `KGQuery`
// contained zero `Limit(` calls and its `withheld` field counted only what the
// status filter removed, which looks like a bound and is not. Measured on the
// live corpus 2026-09-04: one entity fanned out to 184 edges and the bare
// predicate `holds` to 587, both past the budget, and both reproduce as a spill
// — a tool result too large does not arrive smaller, it does not arrive at all,
// so the agent reads an empty answer as "the graph holds nothing about this".
//
// The assertion is on the RENDERED response, not on the fact count, because the
// budget is about what reaches the model and a count says nothing about bytes.
func TestAGraphAnswerIsBoundedAndSaysWhatItCut(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_gamma")
	for i := range 400 {
		h.MustCall(t, "am_kg_add", map[string]any{
			"subject":   "crowded",
			"predicate": fmt.Sprintf("leaf-%03d", i),
			"object":    strings.Repeat("x", 120),
		})
	}

	raw := h.MustCall(t, "am_kg_query", map[string]any{"entity": "crowded"})
	if n := len([]rune(raw)); n > mcpserver.ResponseBudget {
		t.Errorf("the graph answer is %d runes, past the %d-rune budget every other read obeys — "+
			"a response this size spills to a file the model never reads", n, mcpserver.ResponseBudget)
	}

	res := h.JSON(t, raw)
	withheld, ok := res["withheld"].(map[string]any)
	if !ok {
		t.Fatalf("withheld is not a map keyed by cause: %#v", res["withheld"])
	}
	if n, _ := withheld["budget"].(float64); n <= 0 {
		t.Errorf("withheld[budget] = %v; a page that was cut must say so, or the caller reads a "+
			"partial answer as the whole one", withheld["budget"])
	}
	if _, ok := res["next_cursor"].(string); !ok {
		t.Errorf("no next_cursor on a cut page: a fan-out has no ranking, so a truncated answer "+
			"with no continuation is an arbitrary subset the caller cannot complete; got %#v", res["next_cursor"])
	}
}

// TestAGraphCursorReturnsTheRestExactlyOnce is what makes the cut honest.
//
// A fan-out has no ranking, so a truncated page is not "the best N" the way a
// search page is — it is an arbitrary subset. Without a working continuation the
// caller cannot tell which edges they were not shown, which is the same silence
// the spill produced, arriving through a smaller door.
//
// It asserts the UNION over pages equals the unpaged set: a cursor that returns
// a different page is worse than no cursor, because the caller believes they
// have seen everything.
func TestAGraphCursorReturnsTheRestExactlyOnce(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_delta")
	const total = 25
	for i := range total {
		h.MustCall(t, "am_kg_add", map[string]any{
			"subject": "paged", "predicate": fmt.Sprintf("edge-%03d", i), "object": "target",
		})
	}

	seen := map[string]int{}
	cursor := ""
	for page := 0; page < 20; page++ {
		args := map[string]any{"entity": "paged", "limit": 7}
		if cursor != "" {
			args["cursor"] = cursor
		}
		res := h.JSON(t, h.MustCall(t, "am_kg_query", args))
		facts, _ := res["facts"].([]any)
		for _, f := range facts {
			m, _ := f.(map[string]any)
			id, _ := m["id"].(string)
			if id == "" {
				t.Fatalf("a fact carries no id, so no cursor can be built from it: %#v", m)
			}
			seen[id]++
		}
		next, _ := res["next_cursor"].(string)
		if next == "" {
			break
		}
		if next == cursor {
			t.Fatalf("the cursor did not advance past %q — a page that returns itself is an infinite walk", cursor)
		}
		cursor = next
	}

	if len(seen) != total {
		t.Errorf("paging saw %d distinct facts; want %d — a page was skipped, which is exactly what "+
			"an OFFSET would do under a concurrent write and why the cursor is an id", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("fact %s appeared %d times across pages; a repeat means the cursor resumed too early", id, n)
		}
	}
}

// TestWithheldNamesEveryCauseThatRemovedSomething pins the reshape.
//
// `withheld` used to be one count with a WithheldStatus beside it, which can
// name exactly one cause. Two filters then meant one silently overwriting the
// other, and a caller cannot act on a number whose reason is ambiguous — "40
// facts are missing" is not a sentence you can respond to without knowing
// whether they are history or whether they did not fit.
func TestWithheldNamesEveryCauseThatRemovedSomething(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_epsilon")
	for i := range 300 {
		h.MustCall(t, "am_kg_add", map[string]any{
			"subject": "mixed", "predicate": fmt.Sprintf("live-%03d", i),
			"object": strings.Repeat("y", 120),
		})
	}
	h.MustCall(t, "am_kg_add", map[string]any{
		"subject": "mixed", "predicate": "retired", "object": "gone",
	})
	h.MustCall(t, "am_kg_invalidate", map[string]any{
		"subject": "mixed", "predicate": "retired", "object": "gone",
		"reason": "so this page has a status filter to report as well as a budget",
	})

	res := h.JSON(t, h.MustCall(t, "am_kg_query", map[string]any{
		"entity": "mixed", "status": "current", "limit": 1000,
	}))
	withheld, ok := res["withheld"].(map[string]any)
	if !ok {
		t.Fatalf("withheld is not a map keyed by cause: %#v", res["withheld"])
	}
	// ⚠ The status cause is keyed by the status NAME, not by the word "status".
	// A caller acting on the number needs to know the missing rows are ENDED
	// ones; "a status filter ran" is not something you can respond to. ADR-053's
	// Decision says "keyed by cause" and this is the sharper reading of it —
	// the half that predates the record, kept rather than flattened.
	for _, cause := range []string{"ended", "budget"} {
		if n, _ := withheld[cause].(float64); n <= 0 {
			t.Errorf("withheld[%s] = %v; both causes removed facts here, and a shape that can carry "+
				"only one is the shape this reshape replaced", cause, withheld[cause])
		}
	}
}
