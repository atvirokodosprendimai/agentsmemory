package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// ADR-038 T4 at the tool surface. The palace tests prove the supersede; these
// prove an AGENT can reach it, which is the rung the palace tests cannot see.

func TestUpdateWithoutAReasonIsRefused(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_alpha")
	out := h.MustCall(t, "am_add_drawer", map[string]any{"room": "r", "content": "we use Kafka for the bus"})
	id, _ := h.JSON(t, out)["drawers"].([]any)[0].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("no id in add result: %s", out)
	}

	refusal := h.MustRefuse(t, "am_update_drawer", map[string]any{
		"id": id, "content": "we use NATS for the bus",
	})
	if !strings.Contains(strings.ToLower(refusal), "reason") {
		t.Errorf("the refusal does not say a reason is required, so an agent cannot fix the call "+
			"from it: %s", refusal)
	}

	// A move corrects nothing, so it must NOT demand a reason — requiring one
	// everywhere would teach agents to type "obsolete" and mean nothing by it.
	h.MustCall(t, "am_update_drawer", map[string]any{"id": id, "room": "other"})
}

func TestUpdateReturnsTheNewIdNamingTheOneItEnded(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_beta")
	out := h.MustCall(t, "am_add_drawer", map[string]any{"room": "r", "content": "the old claim"})
	id := h.JSON(t, out)["drawers"].([]any)[0].(map[string]any)["id"].(string)

	res := h.JSON(t, h.MustCall(t, "am_update_drawer", map[string]any{
		"id": id, "content": "the corrected claim", "reason": "the measurement was wrong",
	}))
	if res["supersedes"] != id {
		t.Errorf("supersedes = %v; want %q — an agent told only \"ok\" learns neither the id to keep "+
			"working with nor the id it just ended", res["supersedes"], id)
	}
	drawer, ok := res["drawer"].(map[string]any)
	if !ok {
		t.Fatalf("no drawer in the result: %v", res)
	}
	if drawer["id"] == id {
		t.Error("the returned id is the OLD one; a correction mints a new record")
	}
}

func TestUpdateAppliesAnchorsToTheCorrectingRecord(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_anchor")
	out := h.MustCall(t, "am_add_drawer", map[string]any{"room": "r", "content": "explains the parser"})
	id := h.JSON(t, out)["drawers"].([]any)[0].(map[string]any)["id"].(string)

	// code_anchors on an update exists for exactly this case: a memory is
	// corrected, and its old anchor still pins the old text. Once the content path
	// supersedes, applying them to the id the CALLER sent writes them onto the row
	// that was just ended — the correction ships with no anchors at all, and the
	// parameter silently stops doing the only thing it was written for.
	res := h.JSON(t, h.MustCall(t, "am_update_drawer", map[string]any{
		"id": id, "content": "explains the parser, corrected", "reason": "named the wrong function",
		"code_anchors": []any{map[string]any{"path": "internal/p.go", "snippet": "func Parse() {}"}},
	}))
	newID := res["drawer"].(map[string]any)["id"].(string)

	// Filtered by drawer HERE, not in the call: am_list_anchors takes a wing, and
	// an unknown drawer_id argument would be silently ignored — the listing would
	// then return the ended record's carried anchor too and this check could not
	// fail on the thing it is about.
	anchors := h.JSON(t, h.MustCall(t, "am_list_anchors", map[string]any{"wing": "wing_anchor"}))
	rows, _ := anchors["anchors"].([]any)
	var onNew int
	for _, raw := range rows {
		a, _ := raw.(map[string]any)
		if id, _ := a["drawer_id"].(string); id == newID {
			onNew++
		}
	}
	if onNew == 0 {
		t.Fatalf("the correcting record %s carries no anchors; they were written to the record the "+
			"update ENDED. got: %v", newID, anchors)
	}
}

func TestInvalidateDrawerEndsWithNoSuccessor(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_gamma")
	out := h.MustCall(t, "am_add_drawer", map[string]any{"room": "r", "content": "we are not doing this after all"})
	id := h.JSON(t, out)["drawers"].([]any)[0].(map[string]any)["id"].(string)

	if refusal := h.MustRefuse(t, "am_invalidate_drawer", map[string]any{"id": id}); !strings.Contains(strings.ToLower(refusal), "reason") {
		t.Errorf("invalidate without a reason must say so: %s", refusal)
	}

	res := h.JSON(t, h.MustCall(t, "am_invalidate_drawer", map[string]any{
		"id": id, "reason": "the plan was dropped",
	}))
	if res["ended"] != id {
		t.Errorf("ended = %v; want %q", res["ended"], id)
	}

	// Off the default route, and the refusal names the way in — an agent reaches
	// this id by holding what the retraction just returned.
	refused := h.MustRefuse(t, "am_get_drawer", map[string]any{"id": id})
	if !strings.Contains(refused, "include_history") {
		t.Errorf("the refusal does not name the history route: %s", refused)
	}

	// The text survives. Ending is not deleting, and that is the whole point.
	d := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{"id": id, "include_history": true}))
	if !strings.Contains(d["content"].(string), "not doing this after all") {
		t.Errorf("the retracted text is gone; a retraction that erases is a delete wearing a new "+
			"name. got: %v", d)
	}
	if d["ended_reason"] != "the plan was dropped" {
		t.Errorf("ended_reason = %v; the reason is the only thing a tombstone cannot carry", d["ended_reason"])
	}
}
