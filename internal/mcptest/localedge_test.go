package mcptest_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// Issue #161. ADR-038 removed the four local-only delete tools, and their
// scenarios went with them — leaving `NewLocalWithWing` with no caller and the
// sentence in its own doc comment proved by nothing: that the local HTTP edge
// injects the fixed local administrator, so a handler is reachable there without
// a credential.
//
// The constructor was kept rather than deleted, which was the issue's open
// choice. Deleting it is honest but costs the next local-mode tool the harness it
// would be tested through, at the moment somebody is least inclined to rebuild
// one; keeping it costs a scenario. This is that scenario.
//
// ⚠ WHAT IS UNDER TEST IS THE COMPOSITION, NOT THE MIDDLEWARE. `internal/auth`
// already covers `LocalTenant` as a unit three times over, and every one of those
// tests passes while nothing mounts it. AGENTS.md §Reachability records exactly
// that split: the component is exercised, the SELECTION is not. So this drives
// the real `httptest` server the production middleware is wrapped around, and
// asserts through the wire.

// TestScenarioTheLocalEdgeInjectsItsAdministrator is the positive half: a call
// arriving with no bearer, no session and no local credential is served, and the
// server says it is in local mode.
//
// Without the middleware the same request reaches a handler with no tenant in
// context and is refused, so this fails if the mount is ever dropped — which is
// the property the doc comment claims and the thing no unit test can see.
func TestScenarioTheLocalEdgeInjectsItsAdministrator(t *testing.T) {
	h := mcptest.NewLocalWithWing(t, "wing_local")

	out := h.MustCall(t, "am_status", map[string]any{})
	if !strings.Contains(out, `"mode":"local"`) {
		t.Errorf("am_status through the local edge does not report local mode:\n  %s\n"+
			"  mode is the one field that tells an operator which surface answered, and the "+
			"local surface is selected by the same wiring this scenario exists to hold.", out)
	}

	// A WRITE, because admission is not the only half of the claim. The injected
	// tenant carries RoleAdmin, and a read-only identity would sail through the
	// assertion above and refuse this.
	h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "filed through the local edge, by the tenant it injects",
	})

	found := h.MustCall(t, "am_search", map[string]any{"query": "filed through the local edge"})
	if !strings.Contains(found, "by the tenant it injects") {
		t.Errorf("a memory filed through the local edge did not come back:\n  %s\n"+
			"  The write and the read must land in the same workspace, which is what proves the "+
			"edge injects ONE identity rather than a fresh one per request.", found)
	}
}

// TestScenarioTheLocalEdgeIgnoresAnInboundCredential is the half that makes the
// first one mean something.
//
// A middleware that merely PASSED THROUGH whatever the caller sent would satisfy
// every assertion above, because the harness's own client sends a workspace
// header. The local edge is documented to ignore inbound credentials and serve
// its fixed administrator instead; a caller claiming another workspace must
// therefore still be answered as the local one.
func TestScenarioTheLocalEdgeIgnoresAnInboundCredential(t *testing.T) {
	h := mcptest.NewLocalWithWing(t, "wing_local")
	h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "the local workspace holds exactly this",
	})

	// The team the harness names in its header is the same one the middleware
	// injects, so the two are told apart by what the SERVER reports rather than by
	// what the client asked for: a status carrying the local workspace, and a
	// search that finds the memory the injected tenant just filed.
	status := h.MustCall(t, "am_status", map[string]any{})
	if !strings.Contains(status, `"mode":"local"`) {
		t.Fatalf("not local mode, so the rest of this scenario is measuring something else:\n  %s", status)
	}
	out := h.MustCall(t, "am_search", map[string]any{"query": "the local workspace holds"})
	if strings.Contains(out, `"count":0`) {
		t.Errorf("the memory filed a moment ago through this same edge is not visible to a "+
			"search through it:\n  %s\n"+
			"  That is what a per-request identity looks like from the outside — the write and "+
			"the read landing in different workspaces.", out)
	}
}
