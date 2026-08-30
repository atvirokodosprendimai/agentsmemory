package mcpserver_test

import (
	"strings"
	"testing"

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
