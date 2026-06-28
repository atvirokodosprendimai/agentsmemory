package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerDrawers wires the core memory-loop tools — the WRITE/FILE, SEARCH/RECALL
// and the remaining STATUS/ADMIN families of the Python contract — onto the MCP
// server. Every handler shares the admit() preamble (auth + monthly metering) and
// is scoped to the resolved tenant's TeamID, so a token can only ever touch its
// own workspace's memories.
func registerDrawers(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	registerAddDrawer(reg, drawers, usageSvc)
	registerGetDrawer(reg, drawers, usageSvc)
	registerUpdateDrawer(reg, drawers, usageSvc)
	registerDeleteDrawer(reg, drawers, usageSvc)
	registerListDrawers(reg, drawers, usageSvc)
	registerSearch(reg, drawers, usageSvc)
	registerCheckDuplicate(reg, drawers, usageSvc)
	registerListWings(reg, drawers, usageSvc)
	registerListRooms(reg, drawers, usageSvc)
	registerGetTaxonomy(reg, drawers, usageSvc)
	registerGetAAAKSpec(reg, drawers, usageSvc)
	registerReconnect(reg, drawers, usageSvc)
}

// drawerView is the agent-facing JSON shape of a drawer. It omits TeamID (the
// caller already knows its own scope) and gives every field an explicit snake_case
// tag so the wire format is stable regardless of Go field names.
type drawerView struct {
	ID          string   `json:"id"`
	Wing        string   `json:"wing"`
	Room        string   `json:"room"`
	SourceFile  string   `json:"source_file"`
	ChunkIndex  int      `json:"chunk_index"`
	Content     string   `json:"content"`
	Entities    []string `json:"entities,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	FiledAt     string   `json:"filed_at"`
	ContentDate string   `json:"content_date,omitempty"`
}

// toView projects a domain Drawer onto its wire shape.
func toView(d palace.Drawer) drawerView {
	return drawerView{
		ID:          d.ID,
		Wing:        d.Wing,
		Room:        d.Room,
		SourceFile:  d.SourceFile,
		ChunkIndex:  d.ChunkIndex,
		Content:     d.Content,
		Entities:    d.Entities,
		ParentID:    d.ParentID,
		FiledAt:     d.FiledAt,
		ContentDate: d.ContentDate,
	}
}

// jsonResult marshals v into a text tool result. A marshal failure is an internal
// bug, not an agent error, so it is surfaced as a tool error rather than panicking.
func jsonResult(v any) *mcp.CallToolResult {
	out, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("internal: failed to encode result")
	}
	return mcp.NewToolResultText(string(out))
}

// registerAddDrawer: file a verbatim memory. Oversized content is chunked, each
// chunk embedded and stored; the response reports the drawers created.
func registerAddDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("add_drawer",
		mcp.WithDescription("File a verbatim memory (drawer) into a wing/room. Content over ~800 chars is chunked into multiple drawers; re-adding the same source is idempotent."),
		mcp.WithString("wing", mcp.Required(), mcp.Description("Project namespace the memory belongs to.")),
		mcp.WithString("room", mcp.Required(), mcp.Description("Aspect within the wing, e.g. \"backend\" or \"decisions\".")),
		mcp.WithString("content", mcp.Required(), mcp.Description("The verbatim text to remember — stored exactly, never summarised.")),
		mcp.WithString("source_file", mcp.Description("Optional provenance of the content (a path or label).")),
		mcp.WithString("content_date", mcp.Description("Optional date the memory is about (e.g. 2026-06-26).")),
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
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		created, err := drawers.Add(ctx, t.TeamID, palace.AddInput{
			Wing:        wing,
			Room:        room,
			Content:     content,
			SourceFile:  req.GetString("source_file", ""),
			ContentDate: req.GetString("content_date", ""),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]drawerView, len(created))
		for i, d := range created {
			views[i] = toView(d)
		}
		return jsonResult(map[string]any{"ok": true, "chunks": len(created), "drawers": views}), nil
	})
}

// registerGetDrawer: fetch one drawer by id.
func registerGetDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_drawer",
		mcp.WithDescription("Fetch a single drawer by its id."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The drawer id returned by am_add_drawer or am_search.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		d, err := drawers.Get(ctx, t.TeamID, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(toView(d)), nil
	})
}

// registerUpdateDrawer: edit a drawer's content/wing/room in place. Only the
// fields actually supplied are changed; a changed drawer is re-embedded.
func registerUpdateDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("update_drawer",
		mcp.WithDescription("Update a drawer's content, wing, or room in place (its id is unchanged). Only supplied fields are modified."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The drawer id to update.")),
		mcp.WithString("content", mcp.Description("New verbatim content (re-embedded on change).")),
		mcp.WithString("wing", mcp.Description("Move the drawer to this wing.")),
		mcp.WithString("room", mcp.Description("Move the drawer to this room.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Distinguish "field omitted" from "field set to empty" by presence in the
		// raw arguments, so update_drawer only touches what the agent actually sent.
		args := req.GetArguments()
		patch := palace.DrawerPatch{}
		if _, ok := args["content"]; ok {
			v := req.GetString("content", "")
			patch.Content = &v
		}
		if _, ok := args["wing"]; ok {
			v := req.GetString("wing", "")
			patch.Wing = &v
		}
		if _, ok := args["room"]; ok {
			v := req.GetString("room", "")
			patch.Room = &v
		}
		d, err := drawers.Update(ctx, t.TeamID, id, patch)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(toView(d)), nil
	})
}

// registerDeleteDrawer: remove a drawer (row + vector) by id.
func registerDeleteDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("delete_drawer",
		mcp.WithDescription("Delete a drawer by id (removes both its metadata and its embedding)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The drawer id to delete.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := drawers.Delete(ctx, t.TeamID, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"ok": true, "deleted": id}), nil
	})
}

// registerListDrawers: paginate a team's drawers, optionally filtered by wing/room.
func registerListDrawers(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("list_drawers",
		mcp.WithDescription("List drawers (newest first), optionally narrowed to a wing and/or room, with limit/offset paging."),
		mcp.WithString("wing", mcp.Description("Only drawers in this wing.")),
		mcp.WithString("room", mcp.Description("Only drawers in this room.")),
		mcp.WithNumber("limit", mcp.Description("Max drawers to return (default 50).")),
		mcp.WithNumber("offset", mcp.Description("Number of drawers to skip (default 0).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		list, err := drawers.List(ctx, t.TeamID,
			req.GetString("wing", ""), req.GetString("room", ""),
			req.GetInt("limit", 50), req.GetInt("offset", 0))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]drawerView, len(list))
		for i, d := range list {
			views[i] = toView(d)
		}
		return jsonResult(map[string]any{"drawers": views, "count": len(views)}), nil
	})
}

// searchHitView is one ranked search result: the drawer plus its scores.
type searchHitView struct {
	drawerView
	Score       float64 `json:"score"`        // fused hybrid rank (vector + BM25 + closet boost), higher is better
	BM25        float64 `json:"bm25_score"`   // raw lexical BM25 component, for transparency
	ClosetBoost float64 `json:"closet_boost"` // closet rank boost folded into score, for transparency
	Distance    float64 `json:"distance"`     // raw cosine distance in [0,2], lower is closer
}

// registerSearch: hybrid recall over a team's drawers — vector candidates
// re-ranked by a vector+BM25 blend (closet boost joins with the mining phase).
func registerSearch(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("search",
		mcp.WithDescription("Semantically recall drawers most similar to a query. Optionally filter by wing/room and a max cosine distance."),
		mcp.WithString("query", mcp.Required(), mcp.Description("What to recall (max 250 chars).")),
		mcp.WithNumber("limit", mcp.Description("Max results, 1-100 (default 5).")),
		mcp.WithString("wing", mcp.Description("Restrict to this wing.")),
		mcp.WithString("room", mcp.Description("Restrict to this room.")),
		mcp.WithNumber("max_distance", mcp.Description("Drop results farther than this cosine distance (0-2, default 1.5; 0 disables).")),
		mcp.WithString("context", mcp.Description("Optional background context (reserved for future re-ranking; not used yet).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		hits, err := drawers.Search(ctx, t.TeamID, palace.SearchQuery{
			Query:       query,
			Wing:        req.GetString("wing", ""),
			Room:        req.GetString("room", ""),
			Limit:       req.GetInt("limit", palace.DefaultSearchLimit),
			MaxDistance: req.GetFloat("max_distance", palace.DefaultMaxDistance),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]searchHitView, len(hits))
		for i, h := range hits {
			views[i] = searchHitView{drawerView: toView(h.Drawer), Score: h.Score, BM25: h.BM25, ClosetBoost: h.ClosetBoost, Distance: h.Distance}
		}
		return jsonResult(map[string]any{"hits": views, "count": len(views)}), nil
	})
}

// registerCheckDuplicate: is content near-identical to an existing drawer?
func registerCheckDuplicate(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("check_duplicate",
		mcp.WithDescription("Check whether content is near-identical to an existing drawer before filing it."),
		mcp.WithString("content", mcp.Required(), mcp.Description("The candidate content to test.")),
		mcp.WithNumber("threshold", mcp.Description("Cosine-similarity threshold for a duplicate (default 0.9).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := drawers.CheckDuplicate(ctx, t.TeamID, content, req.GetFloat("threshold", palace.DefaultDupThreshold))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{"is_duplicate": res.IsDuplicate, "similarity": res.Similarity}
		if res.Drawer != nil {
			out["drawer"] = toView(*res.Drawer)
		}
		return jsonResult(out), nil
	})
}

// registerListWings: per-wing drawer/room counts.
func registerListWings(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("list_wings",
		mcp.WithDescription("List the team's wings with how many drawers and distinct rooms each holds."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		wings, err := drawers.Wings(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"wings": wings, "count": len(wings)}), nil
	})
}

// registerListRooms: per-room drawer counts, optionally within one wing.
func registerListRooms(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("list_rooms",
		mcp.WithDescription("List the team's rooms with drawer counts, optionally restricted to one wing."),
		mcp.WithString("wing", mcp.Description("Only rooms within this wing.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		rooms, err := drawers.Rooms(ctx, t.TeamID, req.GetString("wing", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"rooms": rooms, "count": len(rooms)}), nil
	})
}

// registerGetTaxonomy: the wing -> rooms tree with counts.
func registerGetTaxonomy(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_taxonomy",
		mcp.WithDescription("Return the team's memory taxonomy: every wing with its rooms and counts."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		tax, err := drawers.GetTaxonomy(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(tax), nil
	})
}

// registerGetAAAKSpec: the static AAAK dialect reference. It needs no storage, so
// it still meters (to keep tool behaviour uniform) but reads nothing per-team.
func registerGetAAAKSpec(reg *registrar, _ *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_aaak_spec",
		mcp.WithDescription("Return the AAAK compressed-memory dialect spec agents use for diary and closet lines."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, errResult, ok := admit(ctx, usageSvc); !ok {
			return errResult, nil
		}
		return jsonResult(map[string]any{"spec": palace.AAAKSpec}), nil
	})
}

// registerReconnect: a liveness probe over the tenant's vector namespace. In this
// stateless server it has no cached client to drop (unlike the Python tool); it
// re-readies the namespace and confirms the backend is reachable.
func registerReconnect(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("reconnect",
		mcp.WithDescription("Re-ready the workspace's vector store and confirm the backend is reachable (a stateless liveness probe)."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		if err := drawers.Reconnect(ctx, t.TeamID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"ok": true, "note": "stateless server: namespace re-readied, backend reachable"}), nil
	})
}
