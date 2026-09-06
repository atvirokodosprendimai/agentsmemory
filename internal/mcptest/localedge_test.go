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
// first one mean something, and it needs a caller CLAIMING a different workspace
// to say anything at all.
//
// ⚠ ITS FIRST VERSION WAS A DUPLICATE OF THE SCENARIO ABOVE WEARING A STRONGER
// NAME, and review of PR #322 proved it by building the middleware it was written
// against — one that passes the inbound credential through instead of injecting
// the fixed administrator — and watching both scenarios stay green over it. The
// reason is that `NewLocalWithWing` dials as `TeamID` and the injected tenant IS
// `TeamID`, so "passed through" and "injected" are byte-identical from the wire.
// Asserting local mode and a readable write does not separate them; only a
// mismatched claim does.
//
// Ignoring an inbound credential is a real property of this edge — it is what
// makes a stray bearer left in an agent's config harmless — so it is worth the
// one constructor rather than a rename.
func TestScenarioTheLocalEdgeIgnoresAnInboundCredential(t *testing.T) {
	h := mcptest.NewLocalAs(t, "wing_local", mcptest.OtherTeamID)

	status := h.MustCall(t, "am_status", map[string]any{})
	if strings.Contains(status, mcptest.OtherTeamID) {
		t.Errorf("the local edge answered as the workspace the CALLER claimed (%s):\n  %s\n"+
			"  It is documented to ignore inbound credentials and serve its fixed administrator, "+
			"and an edge that honours them makes a stray bearer in an agent config decide which "+
			"palace answers.", mcptest.OtherTeamID, status)
	}
	if !strings.Contains(status, mcptest.TeamID) {
		t.Errorf("the local edge did not answer as the workspace it injects (%s):\n  %s",
			mcptest.TeamID, status)
	}

	// And the injected identity is the one that WRITES, not merely the one the
	// status reports: a memory filed by the mismatched caller must be readable
	// back through the same edge, which is where it lands only if both calls were
	// served as the same injected tenant.
	h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "filed by a caller claiming another workspace",
	})
	out := h.MustCall(t, "am_search", map[string]any{"query": "claiming another workspace"})
	if strings.Contains(out, `"count":0`) {
		t.Errorf("the memory filed a moment ago through this edge is not visible to a search "+
			"through it:\n  %s", out)
	}
}
