package main

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// Issue #71, the residue its own re-ranking left: authored edges are deferred to a
// human on delete, deliberately, and nothing tells the human. `doctor --corpus`
// reads source_drawer_id and only that, so neither end of an authored edge is
// checked — a leaf in the must./ref. tier carries the drawer id as its OBJECT, and
// a wing root carries it as the SUBJECT of everything under it. In the incident
// behind that issue the edge sat current, pointing at nothing, the bootstrap
// traversal loaded one record fewer, and a human found it a day later by reading
// wake-up output.
//
// These tests are built around the false-alarm direction as much as the finding.
// A check over subject and object is only worth having if it stays quiet over the
// ordinary row, and the ordinary row in a schemaless graph is a LABEL that names
// no drawer and never should.

// seedDeadEdge files a drawer, remembers its id, deletes it, and returns the id —
// a drawer id that is genuinely gone rather than one this test made up, because a
// fabricated id proves the walk can compare strings and nothing more.
func seedDeadEdge(t *testing.T, svc *services, teamID string) string {
	t.Helper()
	ctx := context.Background()
	added, err := svc.drawers.Add(ctx, teamID, palace.AddInput{
		Wing: "wing_alpha", Room: "decisions", Content: "the record a tier leaf points at",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := added.Drawers[0].ID
	if _, err := svc.drawers.Delete(ctx, teamID, id); err != nil {
		t.Fatalf("delete the drawer the edge names: %v", err)
	}
	return id
}

// TestDoctorCorpusReportsAnEdgeWhoseTargetWasDeleted drives the real delete path
// rather than deleting the row behind the service's back, because the point of
// issue #71 is what Delete leaves BEHIND: DropDerivedEdgesFor filters on
// derived = true, so an authored edge survives its target by design and the audit
// is the only thing that can say so.
func TestDoctorCorpusReportsAnEdgeWhoseTargetWasDeleted(t *testing.T) {
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	ctx := context.Background()
	dead := seedDeadEdge(t, svc, teamID)

	// A LEAF edge as the protocol writes one: the path is the entity, the leaf
	// name is the predicate, the drawer id is the object. Provenance is left empty
	// on purpose so that a finding here can only have come from the object leg —
	// with source_drawer_id set, LostFacts would fire too and the test would pass
	// whichever leg was working.
	if _, err := svc.drawers.KGAdd(ctx, teamID,
		"wing_alpha.root.must.decisions", "the-permissions-record", dead,
		"", "", "", "", ""); err != nil {
		t.Fatalf("seed leaf edge: %v", err)
	}

	f, err := walkCorpus(ctx, svc, teamID)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(f.LostEdges) != 1 || f.LostEdges[0] != dead {
		t.Errorf("lost edges = %v; want exactly [%s]. A tier leaf naming a deleted drawer is "+
			"loaded as one record fewer, with no error and no count", f.LostEdges, dead)
	}
	if len(f.LostFacts) != 0 {
		t.Errorf("lost facts = %v; this edge carries no provenance, so a finding there means the "+
			"two legs are not measuring what their names say", f.LostFacts)
	}
	if f.clean() {
		t.Error("the walk reported clean with a current edge pointing at a deleted drawer")
	}
}

// TestDoctorCorpusReportsAnEdgeWhoseSUBJECTWasDeleted covers the half the issue's
// own table names and no query in the tree has ever checked: deleting a record
// that other edges hang OFF orphans its outgoing edges, not its incoming ones.
func TestDoctorCorpusReportsAnEdgeWhoseSUBJECTWasDeleted(t *testing.T) {
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	ctx := context.Background()
	dead := seedDeadEdge(t, svc, teamID)

	if _, err := svc.drawers.KGAdd(ctx, teamID,
		dead, "corrects", "wing_alpha.root.must.decisions",
		"", "", "", "", ""); err != nil {
		t.Fatalf("seed correction edge: %v", err)
	}

	f, err := walkCorpus(ctx, svc, teamID)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(f.LostEdges) != 1 || f.LostEdges[0] != dead {
		t.Errorf("lost edges = %v; want exactly [%s] from the SUBJECT position", f.LostEdges, dead)
	}
}

// TestAnOrdinaryEntityLabelIsNotAnEdgeFinding is the assertion that decides
// whether this check survives contact with a real palace.
//
// subject and object are free labels: a wing root, a record number, a service
// name. None of them names a drawer and none of them should, so a check that
// asked "does this resolve to a row" of every endpoint would report a whole corpus
// as broken on its first run and be switched off by its second. Only an id-shaped
// value is a pointer, and only a pointer can dangle.
func TestAnOrdinaryEntityLabelIsNotAnEdgeFinding(t *testing.T) {
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	ctx := context.Background()
	if _, err := svc.drawers.Add(ctx, teamID, palace.AddInput{
		Wing: "wing_alpha", Room: "decisions", Content: "an ordinary memory",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, e := range [][3]string{
		{"wing_alpha.root", "must", "wing_alpha.root.must"},
		{"ADR-052", "shipped_in", "v0.0.114"},
		{"agentsmemory", "uses", "sqlite"},
		// 63 hex characters: the near miss. A value one character short of the
		// shape is a label, and treating it as a pointer would put every short
		// hash-like token into the report.
		{strings.Repeat("a", 63), "names", "something"},
	} {
		if _, err := svc.drawers.KGAdd(ctx, teamID, e[0], e[1], e[2], "", "", "", "", ""); err != nil {
			t.Fatalf("seed %q: %v", e[0], err)
		}
	}

	f, err := walkCorpus(ctx, svc, teamID)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(f.LostEdges) != 0 {
		t.Errorf("lost edges = %v over a corpus whose every edge endpoint is an ordinary label; "+
			"a check that flags the normal case is one an operator turns off", f.LostEdges)
	}
	if !f.clean() {
		t.Errorf("a corpus with ordinary labelled edges reported a finding: %+v", f)
	}
}

// TestDoctorCorpusCountsAnEdgeNamingAnEndedDrawerSeparately keeps the three states
// apart at the new leg too. An edge naming a RETRACTED drawer is not the silent
// failure this check exists for: reading an ended id errors and names its
// successor, so it announces itself the moment anything follows it. Folding it
// into the finding would turn every ordinary correction into a defect.
func TestDoctorCorpusCountsAnEdgeNamingAnEndedDrawerSeparately(t *testing.T) {
	cfg, teamID := eraseTestWorkspace(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	ctx := context.Background()
	added, err := svc.drawers.Add(ctx, teamID, palace.AddInput{
		Wing: "wing_alpha", Room: "decisions", Content: "a record a tier names and a session retracts",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := added.Drawers[0].ID
	if _, err := svc.drawers.KGAdd(ctx, teamID,
		"wing_alpha.root.must.decisions", "the-record", id, "", "", "", "", ""); err != nil {
		t.Fatalf("seed leaf edge: %v", err)
	}
	if err := svc.drawers.InvalidateDrawer(ctx, teamID, id, "the decision was reversed"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	f, err := walkCorpus(ctx, svc, teamID)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if f.EndedEdgeTargets < 1 {
		t.Errorf("EndedEdgeTargets = %d; want at least 1", f.EndedEdgeTargets)
	}
	if len(f.LostEdges) != 0 {
		t.Errorf("lost edges = %v; an edge naming a RETRACTED drawer fails loudly when read, and "+
			"reporting it as lost makes every correction look like damage", f.LostEdges)
	}
	if !f.clean() {
		t.Errorf("a corpus whose only unusual state is a deliberate retraction reported a "+
			"finding: %+v", f)
	}
}
