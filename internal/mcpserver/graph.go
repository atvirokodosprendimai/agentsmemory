package mcpserver

import (
	"context"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerGraph wires the navigable-graph tools: tunnels (cross-wing links),
// hallways (within-wing entity co-occurrence), the passive graph views (traverse,
// find_tunnels, graph_stats), and recompute_graph. All are tenant-scoped via admit.
func registerGraph(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	registerCreateTunnel(reg, drawers, usageSvc)
	registerListTunnels(reg, drawers, usageSvc, scopeSearchToWing)
	registerFindTunnels(reg, drawers, usageSvc)
	registerFollowTunnels(reg, drawers, usageSvc)
	registerListHallways(reg, drawers, usageSvc, scopeSearchToWing)
	registerTraverse(reg, drawers, usageSvc)
	registerGraphStats(reg, drawers, usageSvc)
	registerRecomputeGraph(reg, drawers, usageSvc)
}

// endpointView is one side of a tunnel on the wire.
type endpointView struct {
	Wing     string `json:"wing"`
	Room     string `json:"room"`
	DrawerID string `json:"drawer_id,omitempty"`
}

// tunnelView is a tunnel's JSON shape.
//
// It deliberately carries none of palace.Dynamics. ADR-048 retired strength,
// stability, last_activated and access_count from this surface: initDynamics
// stamps them once and nothing in the tree writes them again, so publishing them
// advertised a reinforcement layer the server does not implement. The owner's
// 2026-08-28 ruling rejects wiring one up, so this view stays a subset of the
// domain type rather than a mirror of it — which is why the view exists at all.
type tunnelView struct {
	ID        string       `json:"id"`
	Source    endpointView `json:"source"`
	Target    endpointView `json:"target"`
	Label     string       `json:"label"`
	Kind      string       `json:"kind"`
	CreatedAt string       `json:"created_at"`
	UpdatedAt string       `json:"updated_at,omitempty"`
}

func toTunnelView(t palace.Tunnel) tunnelView {
	return tunnelView{
		ID:     t.ID,
		Source: endpointView{Wing: t.Source.Wing, Room: t.Source.Room, DrawerID: t.Source.DrawerID},
		Target: endpointView{Wing: t.Target.Wing, Room: t.Target.Room, DrawerID: t.Target.DrawerID},
		Label:  t.Label, Kind: string(t.Kind), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

// hallwayView is a hallway's JSON shape.
//
// Like tunnelView it carries none of palace.Dynamics, per ADR-048. The measurement
// behind that: access_count > 0 held for 0 of 1,338 hallways on 2026-08-25, because
// initDynamics is the only writer in the tree — a fact internal/palace/hallway.go
// already records in prose. CreatedAt survives and LastActivated does not, which is
// the right way round: the hallway stamp repair still reads LastActivated
// internally, it is simply not a number a caller can learn anything from.
type hallwayView struct {
	ID           string   `json:"id"`
	Wing         string   `json:"wing"`
	EntityA      string   `json:"entity_a"`
	EntityB      string   `json:"entity_b"`
	CoOccurrence int      `json:"co_occurrence_count"`
	Rooms        []string `json:"rooms"`
	Label        string   `json:"label"`
	CreatedAt    string   `json:"created_at"`
	CreatedBy    string   `json:"created_by"`
}

func toHallwayView(h palace.Hallway) hallwayView {
	return hallwayView{
		ID: h.ID, Wing: h.Wing, EntityA: h.EntityA, EntityB: h.EntityB,
		CoOccurrence: h.CoOccurrence, Rooms: h.Rooms, Label: h.Label,
		CreatedAt: h.CreatedAt, CreatedBy: h.CreatedBy,
	}
}

// graphStatsView is graph_stats on the wire: every metric at the top level, plus
// the note explaining an empty derived graph. It embeds palace.GraphStats rather
// than re-listing its fields so the shape an agent already parses cannot drift
// from the one the palace computes.
type graphStatsView struct {
	palace.GraphStats
	Note string `json:"note,omitempty"`
}

func registerCreateTunnel(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("create_tunnel",
		mcp.WithDescription("Create or update an explicit cross-wing tunnel between two existing wing/room locations. Tunnels are symmetric — creating the reverse direction updates the same tunnel."),
		mcp.WithString("source_wing", mcp.Required(), mcp.Description("Source wing.")),
		mcp.WithString("source_room", mcp.Required(), mcp.Description("Source room (must already contain a drawer).")),
		mcp.WithString("target_wing", mcp.Required(), mcp.Description("Target wing.")),
		mcp.WithString("target_room", mcp.Required(), mcp.Description("Target room (must already contain a drawer).")),
		mcp.WithString("label", mcp.Description("Optional description of the link.")),
		mcp.WithString("source_drawer_id", mcp.Description("Optional drawer to pin the source endpoint to.")),
		mcp.WithString("target_drawer_id", mcp.Description("Optional drawer to pin the target endpoint to.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		sw, err := req.RequireString("source_wing")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sr, err := req.RequireString("source_room")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		tw, err := req.RequireString("target_wing")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		tr, err := req.RequireString("target_room")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		tun, err := drawers.CreateTunnel(ctx, t.TeamID, palace.TunnelInput{
			SourceWing: sw, SourceRoom: sr, TargetWing: tw, TargetRoom: tr,
			Label:          req.GetString("label", ""),
			SourceDrawerID: req.GetString("source_drawer_id", ""),
			TargetDrawerID: req.GetString("target_drawer_id", ""),
		}, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(toTunnelView(tun)), nil
	})
}

func registerListTunnels(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("list_tunnels",
		mcp.WithOutputSchema[tunnelsResult](),
		mcp.WithDescription("List explicit and derived tunnels, optionally filtered to those touching a wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise omission lists every wing. Pass \"*\" to list every wing deliberately. Each endpoint carries wing and room, plus drawer_id when that end is pinned to ONE specific memory rather than to the room as a whole — absent means the tunnel lands on the room, so follow it by recalling there."),
		mcp.WithString("wing", mcp.Description("Only tunnels with this wing as source or target. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise every wing. Pass \"*\" for every wing deliberately."), searchWingProperty()),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		// Resolved through searchWingFor, not taken raw. Audited 2026-08-20 by
		// RUNNING it against two projects in one workspace: naming no wing
		// enumerated every wing, so one project's tunnel labels — free text written by another project's session — were disclosed. am_search and
		// am_list_drawers resolve identically, and an enumeration that does not is
		// a hole in the scope those two enforce.
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		tunnels, err := drawers.ListTunnels(ctx, t.TeamID, wing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]tunnelView, len(tunnels))
		for i, tn := range tunnels {
			views[i] = toTunnelView(tn)
		}
		return jsonResult(tunnelsResult{Tunnels: views, Count: len(views)}), nil
	})
}

func registerFindTunnels(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("find_tunnels",
		mcp.WithOutputSchema[tunnelRoomsResult](),
		mcp.WithDescription("Find rooms that span two or more wings (passive cross-wing connectors), optionally filtered by one or two wings."),
		mcp.WithString("wing_a", mcp.Description("Only rooms that also appear in this wing.")),
		mcp.WithString("wing_b", mcp.Description("Only rooms that also appear in this wing.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		rooms, err := drawers.FindTunnels(ctx, t.TeamID, req.GetString("wing_a", ""), req.GetString("wing_b", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(tunnelRoomsResult{Rooms: rooms, Count: len(rooms)}), nil
	})
}

func registerFollowTunnels(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("follow_tunnels",
		mcp.WithOutputSchema[connectionsResult](),
		mcp.WithDescription("Follow the tunnels leaving or entering a wing/room, with a preview of the drawer each pinned tunnel leads to."),
		mcp.WithString("wing", mcp.Required(), mcp.Description("The wing to follow tunnels from.")),
		mcp.WithString("room", mcp.Required(), mcp.Description("The room to follow tunnels from.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		wing, err := req.RequireString("wing")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		room, err := req.RequireString("room")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		conns, err := drawers.FollowTunnels(ctx, t.TeamID, wing, room)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(connectionsResult{Connections: conns, Count: len(conns)}), nil
	})
}

func registerListHallways(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("list_hallways",
		mcp.WithDescription("List within-wing hallways (entity-to-entity co-occurrence links), optionally filtered by wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise omission lists every wing. Pass \"*\" to list every wing deliberately. A note appears when the list is empty because the derived graph does not exist yet — the state of every palace populated only through am_add_drawer — because that answer is otherwise byte-identical to a graph that genuinely holds no hallway."),
		mcp.WithString("wing", mcp.Description("Only hallways in this wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise every wing. Pass \"*\" for every wing deliberately."), searchWingProperty()),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		halls, err := drawers.ListHallways(ctx, t.TeamID, wing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]hallwayView, len(halls))
		for i, h := range halls {
			views[i] = toHallwayView(h)
		}
		out := map[string]any{"hallways": views, "count": len(views)}
		// An empty hallway list is byte-identical to a graph that can never hold
		// one — which is the state of every palace populated through am_add_drawer.
		// The lookup is skipped when there ARE hallways so the note never costs a
		// second read of a list this handler already has.
		if len(views) == 0 {
			if note := emptyGraphNote(ctx, drawers, t.TeamID, wing); note != "" {
				out["note"] = note
			}
		}
		return jsonResult(out), nil
	})
}

func registerTraverse(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("traverse",
		mcp.WithDescription("Walk the graph from a starting room across rooms that share a wing, out to max_hops. A note appears when the walk describes a palace whose derived graph does not exist yet — an empty answer for that reason is byte-identical to one from a graph that simply holds nothing, and the walk alone cannot say which it is."),
		mcp.WithString("start_room", mcp.Required(), mcp.Description("The room to start the walk from.")),
		mcp.WithNumber("max_hops", mcp.Description("How many hops to walk, 1-10 (default 2).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		start, err := req.RequireString("start_room")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		nodes, err := drawers.Traverse(ctx, t.TeamID, start, req.GetInt("max_hops", 2))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{"nodes": nodes, "count": len(nodes)}
		// A walk that crosses rooms but no entity still describes a palace whose
		// derived graph does not exist, and the walk alone does not say so. The
		// note names no wing: this tool answers for the whole palace.
		if note := emptyGraphNote(ctx, drawers, t.TeamID, ""); note != "" {
			out["note"] = note
		}
		return jsonResult(out), nil
	})
}

func registerGraphStats(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("graph_stats",
		mcp.WithOutputSchema[graphStatsView](),
		mcp.WithDescription("Return aggregate metrics about the team's graph: room totals, cross-wing connectors, edges, rooms-per-wing, and the top connectors. ⚠ITS ROOM TOTAL IS DELIBERATELY SMALLER THAN am_list_rooms AND am_memories_filed_away, AND THE DIFFERENCE IS A FILTER RATHER THAN A DEFECT: this counts NAVIGABLE rooms, so a room named \"general\" (am_mine's default) is excluded, as is any room with no wing, and a room whose every memory has been retracted is gone (ADR-055). Subtract those before comparing with am_list_rooms — three separate sessions have filed the gap as an undercount, because nothing here said so."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		stats, err := drawers.GraphStats(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// total_edges:0 reads as "nothing is connected" whether the graph is
		// young or structurally unreachable; the note separates the two.
		return jsonResult(graphStatsView{
			GraphStats: stats,
			Note:       emptyGraphNote(ctx, drawers, t.TeamID, ""),
		}), nil
	})
}

func registerRecomputeGraph(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("recompute_graph",
		mcp.WithDescription("Rebuild the derived graph (hallways + entity tunnels) from current drawers. Run after mining or wing changes. No source files are read."),
		mcp.WithString("wing", mcp.Description("Only rebuild this wing (default: all wings).")),
		mcp.WithBoolean("prune_orphans", mcp.Description("Drop hallways for wings that no longer have drawers (default true).")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		res, err := drawers.RecomputeGraph(ctx, t.TeamID, req.GetString("wing", ""), req.GetBool("prune_orphans", true))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(res), nil
	})
}
