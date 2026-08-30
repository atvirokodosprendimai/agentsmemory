package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestReplaceWingHallwaysReportsBothLegs pins R1's delete-and-insert accounting:
// the write returns what the transaction actually landed on each leg, so a
// recompute can verify the insert leg against what it derived instead of trusting
// the computation. A driver that reports fewer rows than the batch it was handed
// is a wiring regression the recompute must catch, not blend.
func TestReplaceWingHallwaysReportsBothLegs(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-r1", "alpha"

	// Seed a previous set so the delete leg has rows to remove.
	seed := []Hallway{
		{ID: "h-old-1", TeamID: team, Wing: wing, EntityA: "A", EntityB: "B", CoOccurrence: 1},
		{ID: "h-old-2", TeamID: team, Wing: wing, EntityA: "C", EntityB: "D", CoOccurrence: 2},
	}
	if _, err := svc.repo.ReplaceWingHallways(ctx, team, wing, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Replace with a different set: the delete leg must report the old count,
	// the insert leg the new count.
	fresh := []Hallway{
		{ID: "h-new-1", TeamID: team, Wing: wing, EntityA: "E", EntityB: "F", CoOccurrence: 3},
		{ID: "h-new-2", TeamID: team, Wing: wing, EntityA: "G", EntityB: "H", CoOccurrence: 4},
		{ID: "h-new-3", TeamID: team, Wing: wing, EntityA: "I", EntityB: "J", CoOccurrence: 5},
	}
	stats, err := svc.repo.ReplaceWingHallways(ctx, team, wing, fresh)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if stats.Deleted != 2 {
		t.Fatalf("delete leg: expected 2 (the old set), got %d", stats.Deleted)
	}
	if stats.Inserted != 3 {
		t.Fatalf("insert leg: expected 3 (the fresh set), got %d", stats.Inserted)
	}
}

// TestSaveTunnelsReportsUpsertedRows pins R1's tunnel-side accounting: the write
// returns the rows the upsert landed, so the recompute can verify the tunnel
// count against the driver instead of trusting the derivation.
func TestSaveTunnelsReportsUpsertedRows(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-r1"

	// Clean slate: the recompute path deletes the derived kind first, so the
	// upsert is a fresh insert and the landed count is the full set.
	if err := svc.repo.DeleteTunnelsByKind(ctx, team, TunnelEntity); err != nil {
		t.Fatalf("clean: %v", err)
	}
	tunnels := make([]Tunnel, 4)
	for i := range tunnels {
		tunnels[i] = Tunnel{
			TeamID: team, ID: "t-r1-a" + strings.Repeat("x", i),
			Source: Endpoint{Wing: "alpha", Room: "db"},
			Target: Endpoint{Wing: "beta", Room: "db"},
			Kind:   TunnelEntity,
		}
	}
	landed, err := svc.repo.SaveTunnels(ctx, tunnels)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if landed != 4 {
		t.Fatalf("upsert: expected 4 rows landed, got %d", landed)
	}
}

// TestRecomputeGraphVerifiesWrittenCounts is the R1 gate: recompute reports the
// counts the write actually landed and errors when derived and landed diverge.
// The divergence path is pinned at the gate itself (a real sqlite driver reports
// honest batch counts, so the divergence is by construction unreachable on it),
// and the wiring is pinned end-to-end: the returned result equals the table
// counts, which is only true if RecomputeGraph propagates the repo's landed
// numbers rather than re-trusting the derivation.
func TestRecomputeGraphVerifiesWrittenCounts(t *testing.T) {
	ctx := context.Background()

	// Gate: a short write is rejected, naming the scope and both counts.
	err := verifyRecomputeCount("wing alpha", 3, 2)
	if err == nil {
		t.Fatal("gate: a short write must be rejected")
	}
	if !errors.Is(err, ErrRecomputeMismatch) {
		t.Fatalf("gate: want ErrRecomputeMismatch, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "wing alpha") || !strings.Contains(msg, "3") || !strings.Contains(msg, "2") {
		t.Fatalf("gate: error must name scope + expected/landed, got %q", msg)
	}
	if err := verifyRecomputeCount("wing alpha", 3, 3); err != nil {
		t.Fatalf("gate: an exact write must pass, got %v", err)
	}

	// Wiring: a full recompute returns numbers equal to the table, i.e. the
	// verified counts from the write, for both hallways and tunnels.
	svc := newTestService(t)
	const team = "team-r1"
	mineForGraph(t, svc, team, "alpha", "db", "Redis", "Postgres")
	mineForGraph(t, svc, team, "beta", "db", "Redis", "Mongo")

	res, err := svc.RecomputeGraph(ctx, team, "", true)
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	halls, err := svc.ListHallways(ctx, team, "")
	if err != nil {
		t.Fatalf("list hallways: %v", err)
	}
	if res.Hallways != len(halls) {
		t.Fatalf("hallways: result reports %d, table holds %d", res.Hallways, len(halls))
	}
	tuns, err := svc.ListTunnels(ctx, team, "")
	if err != nil {
		t.Fatalf("list tunnels: %v", err)
	}
	if res.EntityTunnels != len(tuns) {
		t.Fatalf("entity tunnels: result reports %d, table holds %d", res.EntityTunnels, len(tuns))
	}
}
