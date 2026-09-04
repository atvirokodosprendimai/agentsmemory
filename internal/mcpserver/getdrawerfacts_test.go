package mcpserver_test

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestAFetchCarriesTheFactsAboutItsDrawer is ADR-053 T4's gate.
//
// am_search has rendered a facts block since ADR-036; the by-id fetch did not,
// and that is the wrong way round — the fetch is the call a caller makes AFTER
// committing to read a memory, which is exactly when the graph context is worth
// having.
func TestAFetchCarriesTheFactsAboutItsDrawer(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_eta")
	added := h.JSON(t, h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "the decision this fact is about",
	}))
	id := firstDrawerID(t, added)

	// The drawer id is an ENDPOINT of the fact, which is how a fact points at a
	// memory. ⚠ A fact that merely carries source_drawer_id — provenance, not a
	// reference — is a DIFFERENT lookup and this block does not do it; that is
	// recorded as deferred in T4's Out of Scope rather than left to be discovered.
	h.MustCall(t, "am_kg_add", map[string]any{
		"subject": "the deploy", "predicate": "was_decided_in", "object": id,
		"source_drawer_id": id,
	})

	res := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{"id": id}))
	facts, _ := res["facts"].([]any)
	if len(facts) == 0 {
		t.Errorf("no facts on the fetch of a drawer a fact names: %#v", res["facts"])
	}
}

// TestAFetchSurfacesAnIncomingCorrection is the half that would look complete
// while being wrong.
//
// ⚠ A CORRECTION ATTACHES AS AN INCOMING EDGE — it points AT the record it
// corrects — so an outgoing-only block omits exactly the fact start-here says
// every leaf fetch must check, and omits it silently. A caller then reads a
// superseded memory believing they checked. The containment assertion is here
// too: a fetch must not turn into a listing of the drawer's room.
func TestAFetchSurfacesAnIncomingCorrection(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_theta")
	added := h.JSON(t, h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "the claim that was later narrowed",
	}))
	id := firstDrawerID(t, added)

	h.MustCall(t, "am_kg_add", map[string]any{
		"subject": "a later finding", "predicate": "qualifies", "object": id,
		"source_drawer_id": id,
	})

	res := h.JSON(t, h.MustCall(t, "am_get_drawer", map[string]any{"id": id}))
	facts, _ := res["facts"].([]any)
	var sawCorrection bool
	for _, f := range facts {
		m, _ := f.(map[string]any)
		if p, _ := m["predicate"].(string); p == "qualifies" {
			sawCorrection = true
		}
		if p, _ := m["predicate"].(string); p == "holds" {
			t.Errorf("the fetch carried its room's listing: %#v", m)
		}
	}
	if !sawCorrection {
		t.Errorf("the qualifies edge pointing AT this drawer is missing — an outgoing-only block "+
			"looks complete and omits every correction: %#v", facts)
	}
}

// firstDrawerID pulls the id out of an am_add_drawer response.
func firstDrawerID(t *testing.T, added map[string]any) string {
	t.Helper()
	ds, _ := added["drawers"].([]any)
	if len(ds) == 0 {
		t.Fatalf("no drawer in the add response: %#v", added)
	}
	m, _ := ds[0].(map[string]any)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("no id on the added drawer: %#v", m)
	}
	return id
}
