package palace

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestFactLookupIssuesABoundedNumberOfStatements measures what a single recall
// actually costs in SQL, on the search hot path.
//
// The number that matters is STATEMENTS, not entities. An earlier version of this
// test counted candidate entities and called them queries — true when factsFor
// issued one KGQuery per entity, and stale the moment the fetch was batched. A
// cost test whose unit drifts from the thing it measures reports a number nobody
// can act on.
//
// The review that prompted this counted correctly and beat my own measurement:
// one KGQuery costs several statements, and factsFor read the CANDIDATE POOL
// (limit*3 — 30 drawers at the shipped limit of 10) rather than the ranked page.
// Both are fixed; this pins the result.
func TestFactLookupIssuesABoundedNumberOfStatements(t *testing.T) {
	ctx := context.Background()
	const team = "t-sqlcost"
	svc := newTestService(t)

	// Ten drawers, each naming three things no other drawer names, so entity
	// dedup cannot flatter the count. Each noun is repeated within its drawer
	// because extractEntities is frequency-based and stamps nothing otherwise.
	vocab := [][3]string{
		{"Kestrel", "Meridian", "Foundry"}, {"Lantern", "Quarry", "Thicket"},
		{"Beacon", "Harrow", "Vellum"}, {"Cinder", "Marrow", "Pallet"},
		{"Dovetail", "Nimbus", "Rampart"}, {"Ember", "Orchard", "Sable"},
		{"Fathom", "Plinth", "Tessera"}, {"Girder", "Quill", "Umber"},
		{"Halyard", "Rookery", "Verdant"}, {"Ingot", "Sextant", "Willow"},
	}
	for _, v := range vocab {
		content := fmt.Sprintf("%s calls %s. %s stores in %s. %s reads %s. %s and %s share %s.",
			v[0], v[1], v[0], v[2], v[1], v[2], v[0], v[1], v[2])
		if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: content}); err != nil {
			t.Fatalf("add: %v", err)
		}
		if _, err := svc.KGAdd(ctx, team, v[0], "calls", v[1], "", "", "", "", ""); err != nil {
			t.Fatalf("kgadd: %v", err)
		}
	}
	if _, err := svc.BackfillEntityLabels(ctx, team); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// Record only the statements the fact lookup itself issues.
	rec := &sqlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	svc.repo.db = svc.repo.db.Session(&gorm.Session{Logger: rec})

	vec, err := svc.embed.EmbedOne(ctx, "what does Kestrel call")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	page := map[string]Drawer{}
	ids, _ := svc.repo.IDsBySource(ctx, team, "wing_acme", "decisions", "")
	ds, _ := svc.repo.DrawersByIDs(ctx, team, ids)
	for _, d := range ds {
		page[d.ID] = d
	}

	before := len(rec.statements())
	if _, err := svc.factsFor(ctx, team, "wing_acme", vec, page); err != nil {
		t.Fatalf("factsFor: %v", err)
	}
	cost := len(rec.statements()) - before

	graph := 0
	for _, sql := range rec.statements()[before:] {
		if strings.Contains(sql, "kg_triples") || strings.Contains(sql, "kg_entities") {
			graph++
		}
	}
	t.Logf("one recall over a %d-drawer page: %d statements total, %d of them graph reads", len(page), cost, graph)

	// The ceiling is what stops a later change reintroducing per-entity fan-out.
	// It is not a performance target — it is the line between "bounded by the
	// query shape" and "bounded by how chatty the extractor was".
	const ceiling = 12
	if cost > ceiling {
		t.Errorf("a single recall issued %d SQL statements (ceiling %d) on the search hot path; the lookup is fanning out per entity again", cost, ceiling)
	}
}
