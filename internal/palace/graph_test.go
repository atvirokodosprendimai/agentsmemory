package palace

import (
	"context"
	"strings"
	"testing"
)

// mineForGraph mines a block rich in two recurring entities into a wing/room so
// the pair forms a hallway and the room becomes a graph node.
func mineForGraph(t *testing.T, svc *Service, team, wing, room, a, b string) {
	t.Helper()
	// Each sentence names both entities twice so every chunk extracts both, making
	// them co-occur in every drawer of the source.
	content := strings.Repeat(a+" powers it and "+b+" backs it. "+a+" is fast, "+b+" is durable. ", 60)
	if _, err := svc.Mine(context.Background(), team, MineInput{Content: content, Wing: wing, Room: room, Source: wing + "-" + room}); err != nil {
		t.Fatalf("mine %s/%s: %v", wing, room, err)
	}
}

// TestGraphHallwaysAndEntityTunnels: mining two wings that share an entity, then
// recomputing, derives within-wing hallways and a cross-wing entity tunnel.
func TestGraphHallwaysAndEntityTunnels(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mineForGraph(t, svc, team, "alpha", "db", "Redis", "Postgres")
	mineForGraph(t, svc, team, "beta", "db", "Redis", "Mongo")

	res, err := svc.RecomputeGraph(ctx, team, "", true)
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if res.Hallways < 2 {
		t.Fatalf("expected hallways in both wings, got %d", res.Hallways)
	}
	if res.EntityTunnels < 1 {
		t.Fatalf("shared entity Redis should make an entity tunnel, got %d", res.EntityTunnels)
	}

	halls, err := svc.ListHallways(ctx, team, "alpha")
	if err != nil {
		t.Fatalf("list hallways: %v", err)
	}
	var found bool
	for _, h := range halls {
		if h.EntityA == "Postgres" && h.EntityB == "Redis" { // sorted pair
			found = true
			if h.CoOccurrence < 2 {
				t.Fatalf("co-occurrence should be >= 2, got %d", h.CoOccurrence)
			}
		}
	}
	if !found {
		t.Fatalf("expected a Postgres<->Redis hallway in alpha, got %+v", halls)
	}

	tunnels, err := svc.ListTunnels(ctx, team, "")
	if err != nil {
		t.Fatalf("list tunnels: %v", err)
	}
	var entityTunnel bool
	for _, tn := range tunnels {
		if tn.Kind == TunnelEntity && strings.Contains(tn.Label, "Redis") {
			entityTunnel = true
		}
	}
	if !entityTunnel {
		t.Fatalf("expected a Redis entity tunnel across wings, got %+v", tunnels)
	}
}

// TestGraphTraverseFindStats exercises the passive graph views over a room that
// spans two wings.
func TestGraphTraverseFindStats(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mineForGraph(t, svc, team, "alpha", "db", "Redis", "Postgres")
	mineForGraph(t, svc, team, "beta", "db", "Redis", "Mongo")

	// find_tunnels: room "db" spans alpha + beta.
	rooms, err := svc.FindTunnels(ctx, team, "", "")
	if err != nil {
		t.Fatalf("find_tunnels: %v", err)
	}
	var dbSpans bool
	for _, r := range rooms {
		if r.Room == "db" && len(r.Wings) == 2 {
			dbSpans = true
		}
	}
	if !dbSpans {
		t.Fatalf("room db should span two wings, got %+v", rooms)
	}

	// traverse from db returns db at hop 0.
	nodes, err := svc.Traverse(ctx, team, "db", 2)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(nodes) == 0 || nodes[0].Room != "db" || nodes[0].Hop != 0 {
		t.Fatalf("traverse should start at db hop 0, got %+v", nodes)
	}

	// unknown start room is an error.
	if _, err := svc.Traverse(ctx, team, "nonexistent", 2); err == nil {
		t.Fatal("traverse of unknown room should error")
	}

	stats, err := svc.GraphStats(ctx, team)
	if err != nil {
		t.Fatalf("graph_stats: %v", err)
	}
	if stats.TunnelRooms < 1 || stats.TotalEdges < 1 {
		t.Fatalf("stats should report the cross-wing db room: %+v", stats)
	}
}

// TestCreateAndFollowTunnel covers explicit tunnel CRUD: validation, symmetry,
// follow, and delete.
func TestCreateAndFollowTunnel(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"
	const now = "2026-06-28T00:00:00Z"

	mineForGraph(t, svc, team, "alpha", "cache", "Redis", "Postgres")
	mineForGraph(t, svc, team, "beta", "store", "Redis", "Mongo")

	// A tunnel to a non-existent room is rejected.
	if _, err := svc.CreateTunnel(ctx, team, TunnelInput{SourceWing: "alpha", SourceRoom: "cache", TargetWing: "beta", TargetRoom: "ghost"}, now); err == nil {
		t.Fatal("tunnel to a non-existent room should be rejected")
	}

	tun, err := svc.CreateTunnel(ctx, team, TunnelInput{SourceWing: "alpha", SourceRoom: "cache", TargetWing: "beta", TargetRoom: "store", Label: "cache depends on store"}, now)
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	// Symmetry: creating the reverse direction updates the SAME tunnel id.
	rev, err := svc.CreateTunnel(ctx, team, TunnelInput{SourceWing: "beta", SourceRoom: "store", TargetWing: "alpha", TargetRoom: "cache", Label: "updated"}, now)
	if err != nil {
		t.Fatalf("reverse create: %v", err)
	}
	if rev.ID != tun.ID {
		t.Fatalf("reverse tunnel should share the id (symmetric): %s vs %s", rev.ID, tun.ID)
	}

	// follow from alpha/cache finds the outgoing tunnel.
	conns, err := svc.FollowTunnels(ctx, team, "alpha", "cache")
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	if len(conns) != 1 || conns[0].ConnectedWing != "beta" || conns[0].ConnectedRoom != "store" {
		t.Fatalf("follow should find the beta/store connection, got %+v", conns)
	}

	// delete removes it.
	deleted, err := svc.DeleteTunnel(ctx, team, tun.ID)
	if err != nil || !deleted {
		t.Fatalf("delete tunnel: deleted=%v err=%v", deleted, err)
	}
	if _, err := svc.DeleteTunnel(ctx, team, tun.ID); err != nil {
		t.Fatalf("second delete should be a clean no-op, got %v", err)
	}
}
