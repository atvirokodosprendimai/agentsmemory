package mcptest_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestAnEndedRecordIsReturnedByNoDefaultRoute is ADR-038 T5's real gate, driven
// through the TOOLS rather than the service.
//
// The palace test of the same name proves the predicate; this proves an agent
// cannot reach around it. Every route has its own resolution and its own handler,
// and this exact failure shipped once already — a live chunk 1 with its own
// embedding, ranking above the correction that replaced it, with the update
// reporting success throughout. A unit test on the filter would not have seen it,
// because the filter was not the part that was missing.
func TestAnEndedRecordIsReturnedByNoDefaultRoute(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_alpha")

	// A marker that appears in the RETRACTED text and nowhere else. If both
	// records answered the same query, `limit` could drop the ended one by ranking
	// accident and this would pass whether or not the filter ran.
	out := h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "ZOETROPEMARKER the viewer spins the drum at twelve frames",
	})
	id := firstDrawerID(t, h, out)
	res := h.JSON(t, h.MustCall(t, "am_update_drawer", map[string]any{
		"id": id, "content": "the viewer uses a fixed lens at twelve frames",
		"reason": "the zoetrope prototype was abandoned",
	}))
	newID, _ := res["drawer"].(map[string]any)["id"].(string)
	if newID == "" || newID == id {
		t.Fatalf("the correction minted no new record: %v", res)
	}

	if got := h.MustCall(t, "am_search", map[string]any{
		"query": "ZOETROPEMARKER drum", "wing": "wing_alpha", "limit": 50,
	}); contains(got, "ZOETROPEMARKER") {
		t.Errorf("the retracted text is still on the default recall route. It kept its embedding, so "+
			"it competes with the correction that replaced it — and can outrank it:\n%s", got)
	}
	if got := h.MustCall(t, "am_list_drawers", map[string]any{"wing": "wing_alpha", "limit": 100}); contains(got, "ZOETROPEMARKER") {
		t.Errorf("the retracted record is still enumerated:\n%s", got)
	}
	refused := h.MustRefuse(t, "am_get_drawer", map[string]any{"id": id})
	if !contains(refused, "include_history") {
		t.Errorf("the refusal does not name the history route, so an agent holding the supersedes id "+
			"the correction just returned dead-ends on a record that exists:\n%s", refused)
	}

	// Exactly one explicit route reaches it, on all three tools.
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"am_search", map[string]any{"query": "ZOETROPEMARKER drum", "wing": "wing_alpha", "limit": 50, "include_history": true}},
		{"am_list_drawers", map[string]any{"wing": "wing_alpha", "limit": 100, "include_history": true}},
		{"am_get_drawer", map[string]any{"id": id, "include_history": true}},
	} {
		if got := h.MustCall(t, call.tool, call.args); !contains(got, "ZOETROPEMARKER") {
			t.Errorf("%s with include_history did not reach the retracted record, so history is "+
				"reachable by NO route rather than exactly one:\n%s", call.tool, got)
		}
	}

	// And the live record names what it replaced, on the DEFAULT route — a session
	// about to redo a rejected thing does not know to ask for history.
	live := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{"id": newID}))
	if live["supersedes"] != id {
		t.Errorf("supersedes = %v; want %q", live["supersedes"], id)
	}
	if reason, _ := live["superseded_reason"].(string); !strings.Contains(reason, "abandoned") {
		t.Errorf("superseded_reason = %q; the reason is what stops the next session re-deriving "+
			"the rejected version by doing it", reason)
	}
}
