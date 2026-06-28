package mcpserver

import (
	"context"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerGraph wires the navigable-graph tools: tunnels (cross-wing links),
// hallways (within-wing entity co-occurrence), the passive graph views (traverse,
// find_tunnels, graph_stats), and recompute_graph. All are tenant-scoped via admit.
func registerGraph(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	registerCreateTunnel(srv, drawers, usageSvc)
	registerDeleteTunnel(srv, drawers, usageSvc)
	registerListTunnels(srv, drawers, usageSvc)
	registerFindTunnels(srv, drawers, usageSvc)
	registerFollowTunnels(srv, drawers, usageSvc)
	registerListHallways(srv, drawers, usageSvc)
	registerDeleteHallway(srv, drawers, usageSvc)
	registerTraverse(srv, drawers, usageSvc)
	registerGraphStats(srv, drawers, usageSvc)
	registerRecomputeGraph(srv, drawers, usageSvc)
}

// endpointView is one side of a tunnel on the wire.
type endpointView struct {
	Wing     string `json:"wing"`
	Room     string `json:"room"`
	DrawerID string `json:"drawer_id,omitempty"`
}

// tunnelView is a tunnel's JSON shape, flattening the L7 dynamics fields.
type tunnelView struct {
	ID            string       `json:"id"`
	Source        endpointView `json:"source"`
	Target        endpointView `json:"target"`
	Label         string       `json:"label"`
	Kind          string       `json:"kind"`
	CreatedAt     string       `json:"created_at"`
	UpdatedAt     string       `json:"updated_at,omitempty"`
	Strength      float64      `json:"strength"`
	Stability     float64      `json:"stability"`
	LastActivated string       `json:"last_activated,omitempty"`
	AccessCount   int          `json:"access_count"`
}

func toTunnelView(t palace.Tunnel) tunnelView {
	return tunnelView{
		ID:     t.ID,
		Source: endpointView{Wing: t.Source.Wing, Room: t.Source.Room, DrawerID: t.Source.DrawerID},
		Target: endpointView{Wing: t.Target.Wing, Room: t.Target.Room, DrawerID: t.Target.DrawerID},
		Label:  t.Label, Kind: string(t.Kind), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		Strength: t.Strength, Stability: t.Stability, LastActivated: t.LastActivated, AccessCount: t.AccessCount,
	}
}

// hallwayView is a hallway's JSON shape, flattening the L7 dynamics fields.
type hallwayView struct {
	ID            string   `json:"id"`
	Wing          string   `json:"wing"`
	EntityA       string   `json:"entity_a"`
	EntityB       string   `json:"entity_b"`
	CoOccurrence  int      `json:"co_occurrence_count"`
	Rooms         []string `json:"rooms"`
	Label         string   `json:"label"`
	CreatedAt     string   `json:"created_at"`
	CreatedBy     string   `json:"created_by"`
	Strength      float64  `json:"strength"`
	Stability     float64  `json:"stability"`
	LastActivated string   `json:"last_activated,omitempty"`
	AccessCount   int      `json:"access_count"`
}

func toHallwayView(h palace.Hallway) hallwayView {
	return hallwayView{
		ID: h.ID, Wing: h.Wing, EntityA: h.EntityA, EntityB: h.EntityB,
		CoOccurrence: h.CoOccurrence, Rooms: h.Rooms, Label: h.Label,
		CreatedAt: h.CreatedAt, CreatedBy: h.CreatedBy,
		Strength: h.Strength, Stability: h.Stability, LastActivated: h.LastActivated, AccessCount: h.AccessCount,
	}
}

func registerCreateTunnel(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
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
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

func registerDeleteTunnel(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("delete_tunnel",
		mcp.WithDescription("Delete a tunnel by id."),
		mcp.WithString("tunnel_id", mcp.Required(), mcp.Description("The tunnel id to delete.")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("tunnel_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		deleted, err := drawers.DeleteTunnel(ctx, t.TeamID, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"deleted": deleted, "tunnel_id": id}), nil
	})
}

func registerListTunnels(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("list_tunnels",
		mcp.WithDescription("List explicit and derived tunnels, optionally filtered to those touching a wing."),
		mcp.WithString("wing", mcp.Description("Only tunnels with this wing as source or target.")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		tunnels, err := drawers.ListTunnels(ctx, t.TeamID, req.GetString("wing", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]tunnelView, len(tunnels))
		for i, tn := range tunnels {
			views[i] = toTunnelView(tn)
		}
		return jsonResult(map[string]any{"tunnels": views, "count": len(views)}), nil
	})
}

func registerFindTunnels(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("find_tunnels",
		mcp.WithDescription("Find rooms that span two or more wings (passive cross-wing connectors), optionally filtered by one or two wings."),
		mcp.WithString("wing_a", mcp.Description("Only rooms that also appear in this wing.")),
		mcp.WithString("wing_b", mcp.Description("Only rooms that also appear in this wing.")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		rooms, err := drawers.FindTunnels(ctx, t.TeamID, req.GetString("wing_a", ""), req.GetString("wing_b", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"rooms": rooms, "count": len(rooms)}), nil
	})
}

func registerFollowTunnels(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("follow_tunnels",
		mcp.WithDescription("Follow the tunnels leaving or entering a wing/room, with a preview of the drawer each pinned tunnel leads to."),
		mcp.WithString("wing", mcp.Required(), mcp.Description("The wing to follow tunnels from.")),
		mcp.WithString("room", mcp.Required(), mcp.Description("The room to follow tunnels from.")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		return jsonResult(map[string]any{"connections": conns, "count": len(conns)}), nil
	})
}

func registerListHallways(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("list_hallways",
		mcp.WithDescription("List within-wing hallways (entity-to-entity co-occurrence links), optionally filtered by wing."),
		mcp.WithString("wing", mcp.Description("Only hallways in this wing.")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		halls, err := drawers.ListHallways(ctx, t.TeamID, req.GetString("wing", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]hallwayView, len(halls))
		for i, h := range halls {
			views[i] = toHallwayView(h)
		}
		return jsonResult(map[string]any{"hallways": views, "count": len(views)}), nil
	})
}

func registerDeleteHallway(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("delete_hallway",
		mcp.WithDescription("Delete a hallway by id (it will return on the next am_recompute_graph if the co-occurrence still holds)."),
		mcp.WithString("hallway_id", mcp.Required(), mcp.Description("The hallway id to delete.")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("hallway_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		deleted, err := drawers.DeleteHallway(ctx, t.TeamID, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"deleted": deleted}), nil
	})
}

func registerTraverse(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("traverse",
		mcp.WithDescription("Walk the graph from a starting room across rooms that share a wing, out to max_hops."),
		mcp.WithString("start_room", mcp.Required(), mcp.Description("The room to start the walk from.")),
		mcp.WithNumber("max_hops", mcp.Description("How many hops to walk, 1-10 (default 2).")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		return jsonResult(map[string]any{"nodes": nodes, "count": len(nodes)}), nil
	})
}

func registerGraphStats(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("graph_stats",
		mcp.WithDescription("Return aggregate metrics about the team's graph: room totals, cross-wing connectors, edges, rooms-per-wing, and the top connectors."),
	)
	srv.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		stats, err := drawers.GraphStats(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(stats), nil
	})
}

func registerRecomputeGraph(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("recompute_graph",
		mcp.WithDescription("Rebuild the derived graph (hallways + entity tunnels) from current drawers. Run after mining or wing changes. No source files are read."),
		mcp.WithString("wing", mcp.Description("Only rebuild this wing (default: all wings).")),
		mcp.WithBoolean("prune_orphans", mcp.Description("Drop hallways for wings that no longer have drawers (default true).")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
