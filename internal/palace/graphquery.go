package palace

import (
	"context"
	"fmt"
	"sort"
	"time"
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
	traverseMaxResults = 50
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
func (s *Service) Traverse(ctx context.Context, teamID, startRoom string, maxHops int) ([]TraverseNode, error) {
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
		via  []string
	}
	visited := map[string]struct{}{startRoom: {}}
	queue := []qitem{{room: startRoom, hop: 0}}
	var result []TraverseNode
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		n := nodes[cur.room]
		result = append(result, TraverseNode{Room: cur.room, Wings: n.Wings, Count: n.Count, Hop: cur.hop, ConnectedVia: cur.via})
		if cur.hop >= maxHops {
			continue
		}
		// Neighbours are rooms sharing at least one wing. Iterate in sorted order so
		// the walk (and thus which neighbour is discovered first) is deterministic.
		for _, other := range sortedRooms(nodes) {
			if _, seen := visited[other]; seen {
				continue
			}
			shared := intersectSorted(n.Wings, nodes[other].Wings)
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
}

// RecomputeGraph rebuilds the derived graph from current drawers, no source files
// read. It recomputes each target wing's hallways (all present wings, or the one
// given), then regenerates the entity tunnels globally from the full hallway set
// (delete-and-rebuild, so stale ones are pruned). With prune on a full recompute,
// hallways for wings that no longer have drawers are cleared. Topic tunnels are
// not generated (no topic registry yet).
func (s *Service) RecomputeGraph(ctx context.Context, teamID, wing string, prune bool) (RecomputeResult, error) {
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

	res := RecomputeResult{}
	for _, w := range targets {
		halls, err := s.computeHallwaysForWing(ctx, teamID, w, now)
		if err != nil {
			return RecomputeResult{}, err
		}
		if err := s.repo.ReplaceWingHallways(ctx, teamID, w, halls); err != nil {
			return RecomputeResult{}, err
		}
		res.WingsRebuilt = append(res.WingsRebuilt, w)
		res.Hallways += len(halls)
	}
	sort.Strings(res.WingsRebuilt)

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
			if err := s.repo.ReplaceWingHallways(ctx, teamID, w, nil); err != nil {
				return RecomputeResult{}, err
			}
			res.PrunedHallways++
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
	if err := s.repo.SaveTunnels(ctx, entityTunnels); err != nil {
		return RecomputeResult{}, err
	}
	res.EntityTunnels = len(entityTunnels)
	return res, nil
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
