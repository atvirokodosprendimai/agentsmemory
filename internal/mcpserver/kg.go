package mcpserver

import (
	"context"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerKG wires the temporal knowledge-graph tools: kg_add / kg_invalidate
// (write facts and end them), kg_query / kg_timeline (read, optionally as-of a
// point in time), and kg_stats. All are tenant-scoped via admit.
func registerKG(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	registerKGAdd(reg, drawers, usageSvc)
	registerKGInvalidate(reg, drawers, usageSvc)
	registerKGQuery(reg, drawers, usageSvc)
	registerKGStats(reg, drawers, usageSvc)
	registerKGTimeline(reg, drawers, usageSvc)
}

func registerKGAdd(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_add",
		mcp.WithDescription("Add a fact (subject → predicate → object) to the temporal knowledge graph, optionally with a validity window. Re-adding an identical current fact is a no-op; to replace a fact, invalidate the old one first."),
		mcp.WithString("subject", mcp.Required(), mcp.Description("The fact's subject entity.")),
		mcp.WithString("predicate", mcp.Required(), mcp.Description("The relationship (e.g. \"works_at\").")),
		mcp.WithString("object", mcp.Required(), mcp.Description("The fact's object entity.")),
		mcp.WithString("valid_from", mcp.Description("Optional start of validity (YYYY-MM-DD or YYYY-MM-DDTHH:MM:SSZ).")),
		mcp.WithString("valid_to", mcp.Description("Optional end of validity; omit while the fact is current.")),
		mcp.WithString("source_closet", mcp.Description("Optional closet id this fact came from.")),
		mcp.WithString("source_file", mcp.Description("Optional source label.")),
		mcp.WithString("source_drawer_id", mcp.Description("Optional drawer id this fact was extracted from.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		subject, err := req.RequireString("subject")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		predicate, err := req.RequireString("predicate")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		object, err := req.RequireString("object")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := drawers.KGAdd(ctx, t.TeamID, subject, predicate, object,
			req.GetString("valid_from", ""), req.GetString("valid_to", ""),
			req.GetString("source_closet", ""), req.GetString("source_file", ""), req.GetString("source_drawer_id", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"success": true, "triple_id": res.TripleID, "fact": res.Fact}), nil
	})
}

func registerKGInvalidate(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_invalidate",
		mcp.WithDescription("Mark a current fact as no longer true by ending its validity window. The fact is kept (queryable as-of an earlier time), not deleted."),
		mcp.WithString("subject", mcp.Required(), mcp.Description("The fact's subject entity.")),
		mcp.WithString("predicate", mcp.Required(), mcp.Description("The relationship.")),
		mcp.WithString("object", mcp.Required(), mcp.Description("The fact's object entity.")),
		mcp.WithString("ended", mcp.Description("When it stopped being true (YYYY-MM-DD or datetime; default today).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		subject, err := req.RequireString("subject")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		predicate, err := req.RequireString("predicate")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		object, err := req.RequireString("object")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		fact, ended, err := drawers.KGInvalidate(ctx, t.TeamID, subject, predicate, object, req.GetString("ended", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"success": true, "fact": fact, "ended": ended}), nil
	})
}

func registerKGQuery(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_query",
		mcp.WithDescription("Query an entity's relationships in the knowledge graph, optionally as of a point in time and in a chosen direction."),
		mcp.WithString("entity", mcp.Required(), mcp.Description("The entity to look up.")),
		mcp.WithString("as_of", mcp.Description("Only facts in effect at this instant (YYYY-MM-DD or datetime).")),
		mcp.WithString("direction", mcp.Description("\"outgoing\", \"incoming\", or \"both\" (default).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		entity, err := req.RequireString("entity")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		asOf := req.GetString("as_of", "")
		facts, ent, err := drawers.KGQuery(ctx, t.TeamID, entity, asOf, req.GetString("direction", "both"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{"entity": ent, "facts": facts, "count": len(facts)}
		if asOf != "" {
			out["as_of"] = asOf
		}
		return jsonResult(out), nil
	})
}

func registerKGStats(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_stats",
		mcp.WithDescription("Knowledge-graph overview: entity and fact totals, current vs expired facts, and the relationship types in use."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		stats, err := drawers.KGStats(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(stats), nil
	})
}

func registerKGTimeline(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_timeline",
		mcp.WithDescription("Chronological timeline of facts (validity start order), for one entity or — with no entity — across the whole graph."),
		mcp.WithString("entity", mcp.Description("Restrict to facts touching this entity (default: all).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		facts, label, err := drawers.KGTimeline(ctx, t.TeamID, req.GetString("entity", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"entity": label, "timeline": facts, "count": len(facts)}), nil
	})
}
