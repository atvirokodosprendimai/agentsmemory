package palace

import (
	"context"
	"sort"
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

// fillTo pads s with entity-free lowercase filler until it is exactly n runes.
func fillTo(s string, n int) string {
	for len([]rune(s)) < n {
		s += " lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod "
	}
	return string([]rune(s)[:n])
}

// twoChunkMemory builds content longer than one chunk whose halves name
// different pairs, separated by an entity-free gap exactly as wide as
// ChunkOverlap — so the overlap cannot carry one half's names into the other
// half's drawer, and "entities are per chunk" becomes an assertion with teeth
// rather than a comment.
//
// Layout in runes: [0,1280) names a1/a2, [1280,1600) is filler, [1600,2800)
// names b1/b2. ChunkText's stride is ChunkSize-ChunkOverlap = 1280, so chunk 0
// is [0,1600) and chunk 1 is [1280,2800).
func twoChunkMemory(a1, a2, b1, b2 string) string {
	head := fillTo(strings.Repeat(a1+" powers it and "+a2+" backs it. ", 25), ChunkSize-ChunkOverlap)
	gap := fillTo("", ChunkOverlap)
	tail := fillTo(strings.Repeat(b1+" powers it and "+b2+" backs it. ", 25), 1200)
	return head + gap + tail
}

// TestHallwaysDeriveFromDrawersAnAgentFiled is the assertion nothing in this
// repository had ever made: that the path EVERY agent write takes feeds the
// derived graph.
//
// Every hallway test before this one populated its wings with svc.Mine, so the
// whole subsystem was thoroughly tested against the producer agents do not use
// and untested against the one they do — which is why a live palace of 366
// agent-filed drawers derived exactly zero hallways while the suite stayed green
// (ADR-016). Deleting the mining test in favour of this one would just swap the
// blind spot round; both producers have to be pinned.
func TestHallwaysDeriveFromDrawersAnAgentFiled(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// Two memories filed the way an agent files them, each naming the same pair.
	// hallwayMinCount is 2, so one drawer is not enough and the pair has to
	// recur across separate writes.
	for _, in := range []AddInput{
		{Wing: "wing_acme", Room: "db", Content: "Redis powers the queue and Postgres backs it. Redis is fast, Postgres is durable."},
		{Wing: "wing_acme", Room: "cache", Content: "Cutover notes: Redis fronts every read, Postgres owns the writes. Redis evicts, Postgres persists."},
		{Wing: "wing_alpha", Room: "db", Content: "Redis fronts the API and Mongo stores documents. Redis expires keys, Mongo shards them."},
		{Wing: "wing_alpha", Room: "cache", Content: "Redis holds the session, Mongo holds the profile. Redis is volatile, Mongo is not."},
	} {
		if _, err := svc.Add(ctx, team, in); err != nil {
			t.Fatalf("add %s/%s: %v", in.Wing, in.Room, err)
		}
	}

	res, err := svc.RecomputeGraph(ctx, team, "", true)
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if res.Hallways < 1 {
		t.Fatalf("drawers filed through Add must derive hallways, got %d", res.Hallways)
	}

	halls, err := svc.ListHallways(ctx, team, "wing_acme")
	if err != nil {
		t.Fatalf("list hallways: %v", err)
	}
	var found bool
	for _, h := range halls {
		if h.EntityA == "Postgres" && h.EntityB == "Redis" { // sorted pair
			found = true
			if h.CoOccurrence < hallwayMinCount {
				t.Fatalf("co-occurrence should be >= %d, got %d", hallwayMinCount, h.CoOccurrence)
			}
		}
	}
	if !found {
		t.Fatalf("expected a Postgres<->Redis hallway from Add-filed drawers, got %+v", halls)
	}

	// The derived half of tunnels shares the same input and was equally
	// unreachable, so it is asserted from the same producer.
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
		t.Fatalf("Redis is named in both wings, so Add-filed drawers must weave an entity tunnel, got %+v", tunnels)
	}
}

// TestAddExtractsEntitiesPerChunkNotPerMemory pins the locality half of the
// contract: a memory that spans chunks must not hand every chunk the whole
// memory's entities.
//
// Extracting from the full input instead of the chunk still derives hallways, so
// the test above passes happily under that mutation — and the graph then records
// connections the text never made, between things named a thousand characters
// apart. Mining already extracts per chunk; this is what keeps Add honest.
func TestAddExtractsEntitiesPerChunkNotPerMemory(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.Add(ctx, team, AddInput{
		Wing:    "wing_acme",
		Room:    "long",
		Content: twoChunkMemory("Redis", "Postgres", "Kafka", "Mongo"),
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Read back from storage, not from the returned structs: the column is what
	// RecomputeGraph reads, so the column is what has to be right.
	stored, err := svc.List(ctx, team, "wing_acme", "long", 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("fixture should be exactly two chunks, got %d", len(stored))
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].ChunkIndex < stored[j].ChunkIndex })

	for _, want := range []string{"Redis", "Postgres"} {
		if !has(stored[0].Entities, want) {
			t.Errorf("chunk 0 names %s, so it should carry it: %v", want, stored[0].Entities)
		}
		if has(stored[1].Entities, want) {
			t.Errorf("chunk 1 never names %s — entities must come from the chunk, not the memory: %v", want, stored[1].Entities)
		}
	}
	for _, want := range []string{"Kafka", "Mongo"} {
		if !has(stored[1].Entities, want) {
			t.Errorf("chunk 1 names %s, so it should carry it: %v", want, stored[1].Entities)
		}
		if has(stored[0].Entities, want) {
			t.Errorf("chunk 0 never names %s — entities must come from the chunk, not the memory: %v", want, stored[0].Entities)
		}
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

// TestUpdateRefreshesEntities is the third producer of the same defect, and the
// only one that writes a WRONG answer rather than no answer.
//
// Add and WriteDiary each built Drawer rows without Entities, so a memory filed
// through them was absent from the derived graph (ADR-016 T2 and T4). Update is
// worse in kind: it replaces the content and leaves the previous content's
// entities on the row, so the graph keeps asserting an edge the text no longer
// supports. An empty graph tells an agent to go and look; a graph that names the
// wrong pair tells it not to.
//
// Read back from storage rather than from the returned Drawer: the column is
// what RecomputeGraph reads, so the column is what has to be right.
func TestUpdateRefreshesEntities(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	added, err := svc.Add(ctx, team, AddInput{
		Wing:    "wing_acme",
		Room:    "decisions",
		Content: "We chose Redis over Postgres for the session cache. Redis wins on latency; Postgres wins on durability. Redis it is.",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) != 1 {
		t.Fatalf("fixture should be a single chunk, got %d", len(added.Drawers))
	}
	id := added.Drawers[0].ID

	replacement := "We reversed it and chose Kafka over Mongo for the event log. Kafka wins on ordering; Mongo wins on shape. Kafka it is."
	up, err := svc.Update(ctx, team, id, DrawerPatch{Content: &replacement, Reason: "we reversed the decision"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// The CORRECTING record, not the one it replaced: a correction mints a new row
	// and the old one keeps its own text and therefore its own entities.
	stored, err := svc.Get(ctx, team, up.Drawer.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, want := range []string{"Kafka", "Mongo"} {
		if !has(stored.Entities, want) {
			t.Errorf("the memory now names %s, so its row should carry it: %v", want, stored.Entities)
		}
	}
	for _, gone := range []string{"Redis", "Postgres"} {
		if has(stored.Entities, gone) {
			t.Errorf("the memory no longer names %s, so the graph must not still join on it: %v", gone, stored.Entities)
		}
	}
}
