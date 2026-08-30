package mcptest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestKgAddProvenanceIsCheckedAtTheToolSurface is rung 2 for the provenance
// refusal: it proves the check is SELECTED, not merely present.
//
// The unit tests for it all call Service.KGAdd directly, which is the shape
// AGENTS.md names as this repository's characteristic defect — the component
// exercised instead of the selection. Two mutants survive those tests untouched
// and are killed here:
//
//   - the MCP handler forwarding "" instead of the caller's source_drawer_id. The
//     check then never runs, and the fact is stored without the provenance the
//     agent supplied. Killed by the ACCEPT case below, which requires the stored
//     fact to carry the id back.
//   - `team_id = ?` dropped from Repo.DrawerExists. A drawer belonging to another
//     workspace then satisfies the check, so one tenant's fact cites another
//     tenant's memory. Killed by the cross-team case, which needs the hosted
//     harness: the ordinary scenario harness has one workspace and cannot express
//     two.
func TestKgAddProvenanceIsCheckedAtTheToolSurface(t *testing.T) {
	hosted := mcptest.NewHosted(t)
	ctx := context.Background()

	_, credAlpha, err := hosted.Tenants.SeedTeamWithKey(ctx, "Alpha", "alpha-prov", "alpha-prov@example.test")
	if err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	_, credBeta, err := hosted.Tenants.SeedTeamWithKey(ctx, "Beta", "beta-prov", "beta-prov@example.test")
	if err != nil {
		t.Fatalf("seed beta: %v", err)
	}
	alpha := hosted.Client(t, "wing_alpha", credAlpha.Secret)
	beta := hosted.Client(t, "wing_beta", credBeta.Secret)

	alphaDrawerID := firstDrawerID(t, alpha, alpha.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_alpha", "room": "decisions",
		"content": "Alpha decided to keep the reranker off until the pool is measured",
	}))

	// ACCEPT, and prove the id survived the handler. A handler that dropped the
	// argument would also pass the refusal cases below — nothing to check means
	// nothing to refuse — so this is the half that kills that mutant.
	alpha.MustCall(t, "am_kg_add", map[string]any{
		"subject": "alpha", "predicate": "decided", "object": "reranker off",
		"source_drawer_id": alphaDrawerID,
	})
	facts := alpha.MustCall(t, "am_kg_query", map[string]any{"entity": "alpha"})
	if !strings.Contains(facts, alphaDrawerID) {
		t.Errorf("the stored fact does not carry the source_drawer_id the caller passed; "+
			"the handler is not forwarding it, and the provenance check therefore never "+
			"runs on anything. query returned: %s", facts)
	}

	// REFUSE a drawer that belongs to ANOTHER workspace. It exists — so a lookup
	// without the tenant predicate finds it — and citing it would let one team's
	// graph point into another team's memory.
	refusal := beta.MustRefuse(t, "am_kg_add", map[string]any{
		"subject": "beta", "predicate": "cites", "object": "someone else's memory",
		"source_drawer_id": alphaDrawerID,
	})
	if !strings.Contains(strings.ToLower(refusal), "no drawer") {
		t.Errorf("the cross-team refusal does not say the drawer is not there: %s", refusal)
	}

	// REFUSE an id that names nothing anywhere.
	beta.MustRefuse(t, "am_kg_add", map[string]any{
		"subject": "beta", "predicate": "cites", "object": "nothing",
		"source_drawer_id": strings.Repeat("b", 64),
	})
}
