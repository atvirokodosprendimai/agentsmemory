package palace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// This file holds the read side of the graph (build/traverse/find_tunnels/
// graph_stats) and recompute_graph, which rebuilds the derived graph from current
// drawers. The graph itself is not stored: build folds the drawers' (room, wing)
// pairings into rooms and the wings they span, exactly as the frozen build_graph
// did over ChromaDB metadata. Rooms that span two or more wings are the
// cross-wing connectors the tools surface.

// traverseMaxResults / traverseDefaultHops / traverseMaxHops bound a walk (frozen
// caps at 50 results, default 2 hops, 1-10 range).
const (
	traverseMaxResults  = 50
	traverseDefaultHops = 2
	traverseMaxHops     = 10
	graphTopTunnels     = 10
)

// GraphNode is one room in the built graph: the wings it spans, how many drawers
// back it, and the most recent content date seen.
type GraphNode struct {
	Room   string   `json:"room"`
	Wings  []string `json:"wings"`
	Count  int      `json:"count"`
	Recent string   `json:"recent,omitempty"`
}

// buildGraph folds a team's (room, wing) drawer pairings into a room -> node map,
// each node carrying its spanning wings (sorted), total drawer count and most
// recent date. It is the shared substrate of traverse / find_tunnels / graph_stats.
func (s *Service) buildGraph(ctx context.Context, teamID string) (map[string]GraphNode, error) {
	rows, err := s.repo.GraphRoomWings(ctx, teamID)
	if err != nil {
		return nil, err
	}
	type acc struct {
		wings  map[string]struct{}
		count  int
		recent string
	}
	byRoom := map[string]*acc{}
	for _, rw := range rows {
		a := byRoom[rw.Room]
		if a == nil {
			a = &acc{wings: map[string]struct{}{}}
			byRoom[rw.Room] = a
		}
		a.wings[rw.Wing] = struct{}{}
		a.count += rw.Count
		if rw.Recent > a.recent {
			a.recent = rw.Recent
		}
	}
	out := make(map[string]GraphNode, len(byRoom))
	for room, a := range byRoom {
		out[room] = GraphNode{Room: room, Wings: sortedSet(a.wings), Count: a.count, Recent: a.recent}
	}
	return out, nil
}

// TraverseNode is one room reached by a walk: its node data plus the hop distance
// from the start and the wings it shares with the prior hop.
type TraverseNode struct {
	Room         string   `json:"room"`
	Wings        []string `json:"wings"`
	Count        int      `json:"count"`
	Hop          int      `json:"hop"`
	ConnectedVia []string `json:"connected_via,omitempty"`
}

// Traverse walks the graph breadth-first from startRoom, treating two rooms as
// adjacent when they share a wing, out to maxHops (clamped to [1, traverseMaxHops]).
// Results are ordered by hop then descending count and capped, matching the frozen
// traverse. An unknown start room is reported as an error the tool surfaces.
//
// A walk carries the wings it is travelling through and may only step onward
// through those, which is what keeps it a walk rather than a flood. A room node is
// GLOBAL — one node per room NAME, carrying every wing that uses it — so "diary"
// is a single node standing for eleven unrelated wings. Matching a neighbour
// against the current room's full wing set therefore let a walk enter diary from
// one project and leave it into any of the other ten: not a link between related
// memories but a NAME COLLISION presented as one, and it silently crossed the wing
// boundary the rest of the protocol is built on. Measured before this changed, a
// two-hop walk from a single-wing room returned 36 of the palace's 36 rooms —
// every room in every project, at zero selectivity.
//
// Intersecting against the carried wings instead confines a walk to the wings its
// start room actually belongs to, narrowing as it goes. Reaching a genuinely
// unrelated project is then the job of an explicit tunnel, which is authored and
// says what it means.
func (s *Service) Traverse(ctx context.Context, teamID, startRoom string, maxHops int) (result []TraverseNode, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageTraverse, attribute.Int("am.max_hops", maxHops))
	defer func() { endStage(sp, err, attribute.Int("am.count", len(result))) }()
	if maxHops <= 0 {
		maxHops = traverseDefaultHops
	}
	if maxHops > traverseMaxHops {
		maxHops = traverseMaxHops
	}
	nodes, err := s.buildGraph(ctx, teamID)
	if err != nil {
		return nil, err
	}
	if _, ok := nodes[startRoom]; !ok {
		return nil, fmt.Errorf("%w: room %q not found", ErrNotFound, startRoom)
	}

	type qitem struct {
		room string
		hop  int
		// via is the set of wings this walk is confined to, not merely a label for
		// the hop that produced it: the next step is matched against via, so it
		// narrows along the path and a walk can never leave through a wing it did
		// not arrive by.
		via []string
	}
	// Sort the room names once, not per BFS pop: the neighbour scan reuses this
	// stable order so the walk is deterministic without re-sorting each level.
	roomOrder := sortedRooms(nodes)
	visited := map[string]struct{}{startRoom: {}}
	queue := []qitem{{room: startRoom, hop: 0, via: nodes[startRoom].Wings}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		n := nodes[cur.room]
		node := TraverseNode{Room: cur.room, Wings: n.Wings, Count: n.Count, Hop: cur.hop}
		// The start room was not reached through anything, so it reports no
		// connecting wing even though the walk starts out carrying its own.
		if cur.hop > 0 {
			node.ConnectedVia = cur.via
		}
		result = append(result, node)
		if cur.hop >= maxHops {
			continue
		}
		// Neighbours are rooms sharing a wing the walk is ALREADY in, scanned in the
		// stable order.
		for _, other := range roomOrder {
			if _, seen := visited[other]; seen {
				continue
			}
			shared := intersectSorted(cur.via, nodes[other].Wings)
			if len(shared) == 0 {
				continue
			}
			visited[other] = struct{}{}
			queue = append(queue, qitem{room: other, hop: cur.hop + 1, via: shared})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Hop != result[j].Hop {
			return result[i].Hop < result[j].Hop
		}
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Room < result[j].Room
	})
	if len(result) > traverseMaxResults {
		result = result[:traverseMaxResults]
	}
	return result, nil
}

// FindTunnels returns the rooms that span two or more wings — the passive
// cross-wing connectors — optionally narrowed to those touching wingA and/or
// wingB. Ordered by descending drawer count and capped, matching the frozen tool.
func (s *Service) FindTunnels(ctx context.Context, teamID, wingA, wingB string) ([]GraphNode, error) {
	nodes, err := s.buildGraph(ctx, teamID)
	if err != nil {
		return nil, err
	}
	var out []GraphNode
	for _, n := range nodes {
		if len(n.Wings) < 2 {
			continue
		}
		if wingA != "" && !contains(n.Wings, wingA) {
			continue
		}
		if wingB != "" && !contains(n.Wings, wingB) {
			continue
		}
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Room < out[j].Room
	})
	if len(out) > traverseMaxResults {
		out = out[:traverseMaxResults]
	}
	return out, nil
}

// GraphStats is the graph_stats summary: totals, per-wing room counts, and the
// most-connected rooms.
type GraphStats struct {
	TotalRooms   int            `json:"total_rooms"`
	TunnelRooms  int            `json:"tunnel_rooms"`
	TotalEdges   int            `json:"total_edges"`
	RoomsPerWing map[string]int `json:"rooms_per_wing"`
	TopTunnels   []GraphNode    `json:"top_tunnels"`
}

// GraphStats computes the aggregate graph metrics: total rooms, how many span two+
// wings (tunnel rooms), the number of cross-wing edges (a wing-pair per multi-wing
// room), rooms-per-wing, and the top connectors by wing span.
func (s *Service) GraphStats(ctx context.Context, teamID string) (GraphStats, error) {
	nodes, err := s.buildGraph(ctx, teamID)
	if err != nil {
		return GraphStats{}, err
	}
	stats := GraphStats{RoomsPerWing: map[string]int{}}
	stats.TotalRooms = len(nodes)
	var multi []GraphNode
	for _, n := range nodes {
		for _, w := range n.Wings {
			stats.RoomsPerWing[w]++
		}
		if len(n.Wings) >= 2 {
			stats.TunnelRooms++
			// A multi-wing room contributes one edge per unordered wing pair.
			stats.TotalEdges += len(n.Wings) * (len(n.Wings) - 1) / 2
			multi = append(multi, n)
		}
	}
	sort.SliceStable(multi, func(i, j int) bool {
		if len(multi[i].Wings) != len(multi[j].Wings) {
			return len(multi[i].Wings) > len(multi[j].Wings)
		}
		return multi[i].Count > multi[j].Count
	})
	if len(multi) > graphTopTunnels {
		multi = multi[:graphTopTunnels]
	}
	stats.TopTunnels = multi
	return stats, nil
}

// RecomputeResult reports what recompute_graph rebuilt.
type RecomputeResult struct {
	WingsRebuilt   []string `json:"wings_rebuilt"`
	Hallways       int      `json:"hallways"`
	EntityTunnels  int      `json:"entity_tunnels"`
	PrunedHallways int      `json:"pruned_hallways"`
	// EntityLabelsIndexed is how many KG entity labels were re-embedded, so an
	// operator can see the fact-lookup index was rebuilt and not merely the
	// hallways.
	EntityLabelsIndexed int `json:"entity_labels_indexed"`
}

// ErrRecomputeMismatch is returned by RecomputeGraph when a write landed fewer
// rows than the recompute derived. Both writes are single batch transactions, so
// any divergence is driver-level silent row loss — a wiring regression, not a
// data difference.
var ErrRecomputeMismatch = errors.New("recompute mismatch")

// verifyRecomputeCount is R1's gate: the recompute's written count must equal
// what it derived, per leg. Named so the gate is testable without simulating a
// driver that lies (a real sqlite driver reports honest batch counts, so the
// divergence path is by construction unreachable on it — the gate is the net,
// and the net's mesh is what is pinned here).
func verifyRecomputeCount(scope string, expected, landed int) error {
	if landed != expected {
		return fmt.Errorf("%w: %s: expected %d rows, write landed %d", ErrRecomputeMismatch, scope, expected, landed)
	}
	return nil
}

// RecomputeGraph rebuilds the derived graph from current drawers, no source files
// read. It recomputes each target wing's hallways (all present wings, or the one
// given), then regenerates the entity tunnels globally from the full hallway set
// (delete-and-rebuild, so stale ones are pruned). With prune on a full recompute,
// hallways for wings that no longer have drawers are cleared. Topic tunnels are
// not generated (no topic registry yet). The returned counts are verified: they
// come from what the writes actually landed, and a write that landed fewer rows
// than derived is an error (ErrRecomputeMismatch), not a blended number.
func (s *Service) RecomputeGraph(ctx context.Context, teamID, wing string, prune bool) (result RecomputeResult, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageRecompute, attribute.Bool("am.prune", prune), attribute.Bool("am.has_wing", wing != ""))
	defer func() {
		endStage(sp, err,
			attribute.Int("am.hallways", result.Hallways),
			attribute.Int("am.entity_tunnels", result.EntityTunnels),
			attribute.Int("am.wings", len(result.WingsRebuilt)),
		)
	}()
	// Serialize a team's recomputes so two cannot interleave the hallway replace
	// and entity-tunnel delete-and-rebuild and leave a stale graph.
	unlock := s.graphLocks.lock(teamID)
	defer unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	present, err := s.repo.WingsWithDrawers(ctx, teamID)
	if err != nil {
		return RecomputeResult{}, err
	}
	presentSet := map[string]struct{}{}
	for _, w := range present {
		presentSet[w] = struct{}{}
	}

	var targets []string
	full := wing == ""
	if full {
		targets = present
	} else {
		if _, ok := presentSet[wing]; !ok {
			return RecomputeResult{}, fmt.Errorf("%w: wing %q has no drawers", ErrInvalidInput, wing)
		}
		targets = []string{wing}
	}

	for _, w := range targets {
		halls, err := s.computeHallwaysForWing(ctx, teamID, w, now)
		if err != nil {
			return RecomputeResult{}, err
		}
		stats, err := s.repo.ReplaceWingHallways(ctx, teamID, w, halls)
		if err != nil {
			return RecomputeResult{}, err
		}
		// R1: verify the insert leg — the delete leg exists only to be excluded.
		// Hallways name the wing; tunnels are team-wide and name the scope instead.
		if err := verifyRecomputeCount("wing "+w, len(halls), stats.Inserted); err != nil {
			return RecomputeResult{}, err
		}
		result.WingsRebuilt = append(result.WingsRebuilt, w)
		result.Hallways += stats.Inserted
	}
	sort.Strings(result.WingsRebuilt)

	// Prune hallways for wings that no longer have drawers (full recompute only).
	if prune && full {
		all, err := s.repo.ListHallways(ctx, teamID, "")
		if err != nil {
			return RecomputeResult{}, err
		}
		stale := map[string]struct{}{}
		for _, h := range all {
			if _, ok := presentSet[h.Wing]; !ok {
				stale[h.Wing] = struct{}{}
			}
		}
		for w := range stale {
			stats, err := s.repo.ReplaceWingHallways(ctx, teamID, w, nil)
			if err != nil {
				return RecomputeResult{}, err
			}
			if err := verifyRecomputeCount("wing "+w, 0, stats.Inserted); err != nil {
				return RecomputeResult{}, err
			}
			result.PrunedHallways++
		}
	}

	// Regenerate entity tunnels globally from the current hallway set: drop the old
	// derived ones, rebuild from every wing's hallways (deduped by canonical id).
	if err := s.repo.DeleteTunnelsByKind(ctx, teamID, TunnelEntity); err != nil {
		return RecomputeResult{}, err
	}
	allHalls, err := s.repo.ListHallways(ctx, teamID, "")
	if err != nil {
		return RecomputeResult{}, err
	}
	hallwayWings := map[string]struct{}{}
	for _, h := range allHalls {
		hallwayWings[h.Wing] = struct{}{}
	}
	seen := map[string]struct{}{}
	var entityTunnels []Tunnel
	for w := range hallwayWings {
		for _, t := range entityTunnelsForWing(teamID, w, allHalls, now) {
			if _, dup := seen[t.ID]; dup {
				continue
			}
			seen[t.ID] = struct{}{}
			entityTunnels = append(entityTunnels, t)
		}
	}
	landed, err := s.repo.SaveTunnels(ctx, entityTunnels)
	if err != nil {
		return RecomputeResult{}, err
	}
	if err := verifyRecomputeCount("entity tunnels", len(entityTunnels), landed); err != nil {
		return RecomputeResult{}, err
	}
	// The VERIFIED count, not len(entityTunnels): ADR-027's R1 gate exists
	// because a write can land fewer rows than the recompute derived, and
	// reporting the derived number would describe the intent rather than the
	// database.
	result.EntityTunnels = landed

	// The entity-label index is derived structure too, so it is rebuilt here
	// rather than living only in a test. BackfillEntityLabels had NO production
	// caller, so entities that existed before ADR-036 never entered the vector
	// namespace and no question could reach their facts — a capability complete
	// and unreachable, which is this repository's named defect.
	//
	// Non-fatal: a recompute that rebuilt the graph should not report failure
	// because the label index could not be refreshed.
	if n, err := s.BackfillEntityLabels(ctx, teamID); err != nil {
		slog.WarnContext(ctx, "entity labels not reindexed; facts about pre-existing entities stay unreachable by question", "err", err)
	} else {
		result.EntityLabelsIndexed = n
	}
	return result, nil
}

// --- small set helpers ----------------------------------------------------

func sortedRooms(nodes map[string]GraphNode) []string {
	out := make([]string, 0, len(nodes))
	for r := range nodes {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func contains(items []string, target string) bool {
	for _, it := range items {
		if it == target {
			return true
		}
	}
	return false
}

// intersectSorted returns the common members of two sorted string slices.
func intersectSorted(a, b []string) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// EntryRoom is the room a wing's entry point lives in by convention.
//
// A convention rather than a schema column: which room a team calls its front
// door is a team decision, and the server's job is to RESOLVE it, not to bless
// it. The name matches what this workspace's own operating skill already uses,
// so an existing palace answers without being rebuilt.
const EntryRoom = "llm_init"

// EntryPointResult is a wing's front door: the node an agent starts from and
// what that node points at.
type EntryPointResult struct {
	// Wing is the wing this answers for, echoed so a caller cannot mistake whose
	// entry point it is holding.
	Wing string `json:"wing"`
	// Node is the entry node's identifier, empty when the wing has none.
	Node string `json:"node,omitempty"`
	// Edges are the entry node's OUTGOING edges — what it holds.
	Edges []KGFact `json:"edges,omitempty"`
	// Resolution reuses T2's vocabulary rather than inventing a second way to say
	// "nothing here". unknown_term means this wing has no entry point at all,
	// which is a fact about the wing and not an error; known_term_no_facts means
	// it has one that points at nothing yet.
	Resolution KGResolution `json:"resolution"`
	// Refused counts entry edges dropped because their target is not readable
	// from this wing. A count discloses no identifier, so it is the one thing
	// WingPolicy permits saying about a refused edge — and without it the drop
	// is silent: the caller sees a shorter edge list and cannot distinguish a
	// smaller front door from a filtered one.
	Refused int `json:"refused,omitempty"`
}

// EntryPoint resolves a wing's entry node and its outgoing edges DIRECTLY.
//
// Deliberately not via Traverse. `am_traverse`'s max_hops is provably inert: the
// `via` set is an intersection carried forward, so a node at hop >= 2 can only be
// admitted if it shares a wing with everything on the path already — which the
// hop-1 neighbours have already satisfied. Verified 2026-08-26 from this
// workspace's own llm_init root (25 nodes, all hop <= 1) and from a leaf drawer in
// the same room (10 nodes, all hop 1). Building a front door on a walk that
// silently returns only hop 1 would look like it worked.
//
// Fixing traverse is out of scope and stays that way for a reason: whether
// traversal should be transitive across wings or confined to the start node's own
// is an unmade product decision, and those are different products.
func (s *Service) EntryPoint(ctx context.Context, teamID, wing string) (EntryPointResult, error) {
	// A wing is REQUIRED here, and "required" in a tool schema only means the key
	// is present — an empty string satisfies it. Left unchecked, WingPolicy reads
	// an empty viewer as "unscoped" and treats every resolvable record as local,
	// which turns a missing argument into a cross-wing read.
	if strings.TrimSpace(wing) == "" {
		return EntryPointResult{}, fmt.Errorf("%w: entry_point needs a wing", ErrInvalidInput)
	}
	out := EntryPointResult{Wing: wing}
	node := DerivedEdgeSubject(wing, EntryRoom)

	q, err := s.KGQuery(ctx, teamID, KGQueryInput{
		Entity: node, Direction: "outgoing", Status: KGStatusCurrent,
	})
	if err != nil {
		return EntryPointResult{}, err
	}
	out.Resolution = q.Resolution
	if q.Resolution == KGResolutionUnknownTerm {
		// The wing has no entry point. That is a fact about the wing, reported
		// distinguishably from an error and from an entry point that is merely
		// empty — three different situations a bare count of zero would merge.
		return out, nil
	}
	out.Node = node

	// Every edge is authorized on the identifier it EXPOSES — f.Object, the
	// record the edge names — and not on f.SourceDrawerID, which is where the
	// edge came from.
	//
	// The two are independent: provenance is optional and describes the drawer a
	// fact was extracted from, while the object is the drawer being pointed at.
	// An edge whose provenance is local and whose target is foreign passed a
	// provenance check and disclosed the foreign id, and the comment that used to
	// sit here named that exact threat while the code beneath it checked the
	// other identifier.
	policy := s.wingPolicyFor(ctx, teamID, wing)
	for _, f := range q.Facts {
		placement, _ := policy.Place(ctx, f.Object)
		if policy.MayReturnContent(placement) {
			out.Edges = append(out.Edges, f)
			continue
		}
		out.Refused++
	}
	return out, nil
}
