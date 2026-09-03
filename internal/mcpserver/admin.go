package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerAnchors wires the two halves of staleness detection: list_anchors hands
// out the questions, mark_anchors takes back the answers.
//
// The split exists because the server usually cannot see the code it stores
// memories about — it runs in a container, the repository is on someone's laptop.
// Whoever CAN read the working tree (the `aiagentmemory verify` command) does the
// checking; the server only keeps score.
func registerAnchors(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	list := newTool("list_anchors",
		mcp.WithDescription("List code anchors — the (file, snippet) pairs memories are pinned to — so a client that can read the working tree can verify them. Filter by wing, repo label, or status (unchecked|verified|drifted|missing)."),
		mcp.WithString("wing", mcp.Description("Only anchors on drawers in this wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise every wing. Pass \"*\" for every wing deliberately."), searchWingProperty()),
		mcp.WithString("repo", mcp.Description("Only anchors carrying this repo label.")),
		mcp.WithString("status", mcp.Description("Only anchors in this state.")),
		mcp.WithNumber("limit", mcp.Description("Max anchors to return (default 500).")),
	)
	reg.add(list, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		// Resolved through searchWingFor, not taken raw. Audited 2026-08-20 by
		// RUNNING it against two projects in one workspace: naming no wing
		// enumerated every wing, so one project's anchors — and the verbatim source lines they carry — were handed to another. am_search and
		// am_list_drawers resolve identically, and an enumeration that does not is
		// a hole in the scope those two enforce.
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		anchors, err := drawers.ListAnchors(ctx, t.TeamID, palace.AnchorFilter{
			Wing:   wing,
			Repo:   req.GetString("repo", ""),
			Status: req.GetString("status", ""),
			Limit:  req.GetInt("limit", 0),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := make([]map[string]any, 0, len(anchors))
		for _, a := range anchors {
			out = append(out, map[string]any{
				"id": a.ID, "drawer_id": a.DrawerID, "repo": a.Repo, "path": a.Path,
				"snippet": a.Snippet, "status": a.Status, "line": a.Line, "checked_at": a.CheckedAt,
			})
		}
		return jsonResult(map[string]any{"anchors": out, "count": len(out)}), nil
	})

	registerMarkAnchors(reg, drawers, usageSvc)
}

// registerMarkAnchors: take back the verification verdicts list_anchors handed
// out. Split from registerAnchors because it WRITES and its sibling reads, and a
// registration that builds both cannot carry one classification honestly.
func registerMarkAnchors(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	mark := newTool("mark_anchors",
		mcp.WithDescription("Record verification verdicts for code anchors: [{\"id\":\"<anchor id>\",\"status\":\"verified|drifted|missing\",\"line\":123}]. Writes only the verdict, never the memory, so stamping never re-embeds anything."),
		mcp.WithArray("verdicts", mcp.Required(), mcp.Description("The results, one object per anchor checked.")),
	)
	reg.addWrite(mark, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		raw, ok := req.GetArguments()["verdicts"].([]any)
		if !ok {
			return mcp.NewToolResultError("verdicts must be an array of {id, status, line} objects"), nil
		}
		verdicts := make([]palace.AnchorVerdict, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			status, _ := m["status"].(string)
			line := 0
			if f, ok := m["line"].(float64); ok {
				line = int(f)
			}
			if id == "" || status == "" {
				continue
			}
			verdicts = append(verdicts, palace.AnchorVerdict{ID: id, Status: status, Line: line})
		}
		n, err := drawers.MarkAnchors(ctx, t.TeamID, verdicts)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"marked": n}), nil
	})
}

// registerRecallStats adds recall_stats: is the memory being used, and does it
// answer? Drawer counts say how much is remembered; this says whether remembering
// is working, which is the only question an operator can act on.
func registerRecallStats(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("recall_stats",
		mcp.WithDescription("How well memory is working, per wing: searches run, how many came back with something, drawers held, and the recent queries that found NOTHING (the memories the team looked for and does not have). Use it to see whether recall is earning its keep rather than guessing. Two team-level counts sit beside them: fetches, how many times a caller read a drawer while naming the recall that sent it there, and recalls_fetched, how many DISTINCT recalls those fetches name — the palace's only usage signal that grows with usage rather than with a labelling budget. They are raw counts and deliberately not a rate: the denominator would be recalls that were LOGGED, and a ratio needs the ranking profile beside it to mean anything."),
		mcp.WithString("wing", mcp.Description("Only report this wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise every wing. Pass \"*\" for every wing deliberately."), searchWingProperty()),
		mcp.WithNumber("hours", mcp.Description("Window to report on, in hours (default 24).")),
		mcp.WithNumber("unanswered", mcp.Description("How many unanswered queries to list (default 10).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		hours := req.GetInt("hours", 24)
		if hours <= 0 {
			hours = 24
		}
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		stats, err := drawers.RecallStats(ctx, t.TeamID, wing, time.Duration(hours)*time.Hour, req.GetInt("unanswered", 10))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		wings := make([]map[string]any, 0, len(stats.Wings))
		for _, w := range stats.Wings {
			wings = append(wings, map[string]any{
				"wing":          w.Wing,
				"searches":      w.Searches,
				"answered":      w.Answered,
				"answered_pct":  w.AnsweredPct(),
				"avg_top_score": w.AvgTop,
				// The fused score above is a rank encoding under FUSION=rrf, so it is
				// nearly constant and cannot say whether recall is working. This one can:
				// ADR-001 measured the cross-encoder separating answerable from
				// unanswerable by ~4.7 in median where cosine distance separated them by
				// 0.022. Reported with its own count, because averaging it over searches
				// no cross-encoder touched would divide real logits by a denominator of
				// not-measured zeros.
				"avg_top_rerank_score": w.AvgTopRerank,
				"reranked":             w.Reranked,
				// WHY the cross-encoder did not order a page, per reason. `reranked`
				// alone is 0 for a disabled reranker and for one failing on every
				// query, which is what this separates.
				//
				// Renders as null, not {}, when nothing was skipped in the window —
				// a nil map, verified against the live server 2026-08-26. That is the
				// honest encoding: null reads as "no skips recorded", and `searches`
				// and `reranked` beside it say whether that is because the window was
				// healthy or because it was empty.
				"rerank_skips": w.RerankSkips,
				"drawers":      w.Drawers,
				"last_used":    w.LastUsed,
				"last_filed":   w.LastFiled,
			})
		}
		// ADR-028 T3. Two RAW counts, never a rate, and never wing-scoped.
		//
		// A rate is withheld on purpose: the denominator is recalls THAT WERE
		// LOGGED — SkipTelemetry means some recalls write no search_events row at
		// all — and ADR-028's deferral puts any ratio behind `profile_id` on the
		// durable row, because "38% of recalls were followed by a fetch" is
		// uninterpretable without knowing which ranking profile produced them.
		// Publishing the counts is what makes the fetch join observable at all;
		// publishing a rate would be the population error ADR-007 exists to stop.
		//
		// Team-scoped rather than per-wing because a fetch names a DRAWER and the
		// wing would have to be joined back through it. Reported at the top level
		// so nobody reads it as a wing figure.
		fetches, recallsFetched, err := drawers.CountFetches(ctx, t.TeamID, time.Duration(hours)*time.Hour)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"window_hours":    hours,
			"since":           stats.Since,
			"searches":        stats.Searches,
			"answered":        stats.Answered,
			"answered_pct":    stats.AnsweredPct(),
			"writes":          stats.Writes,
			"fetches":         fetches,
			"recalls_fetched": recallsFetched,
			"wings":           wings,
			"unanswered":      stats.Unanswered,
			"suggestions":     stats.Suggestions,
			"hint":            "answered_pct climbing over weeks means the palace is learning the questions this team actually asks; a wing with drawers and no searches is written-to and never read. suggestions collapses the unanswered queries into a to-write list: each entry is one memory this team looked for and does not have, with how many times it was asked and which wing to file it in.",
		}), nil
	})
}

// registerAdmin wires the palace-maintenance tools: merge_wing (fold wings
// together) and memories_filed_away (a recent-activity summary). The frozen sync
// and hook_settings tools are deliberately not ported — both are single-user-local
// (on-disk source pruning / local Claude Code hook config) with no meaning on a
// multi-tenant server. All admin tools are tenant-scoped.
//
// delete_wing was registered here when local, and ADR-038 removed it. The argument
// for keeping it was that self-hosted, the agent and the operator are one person on
// one machine, so the alternative to an agent deleting a wing was nobody deleting
// it. That is not a boundary — it is the case where the boundary is ABSENT, which
// is exactly when a model's mistake is unrecoverable. Erasure moved to
// `agentsmemory wing delete`, which the same person runs and which no misread
// retraction can reach.
//
// merge_wing deliberately stays: it moves memories, it does not destroy them.
func registerAdmin(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	registerMergeWing(reg, drawers, usageSvc)
	registerMemoriesFiledAway(reg, drawers, usageSvc)
}

// wingMerger is the palace work merge_wing performs, declared here at the
// consumer so a test can drive the real handler against a recording double
// instead of a database. *palace.Service satisfies it, as does mergejob.Merger's
// implementation — the two interfaces are deliberately separate because each
// belongs to the package that calls it.
type wingMerger interface {
	MergeWing(ctx context.Context, teamID string, sources []string, target string) (palace.MergeWingResult, error)
	RecomputeGraph(ctx context.Context, teamID, wing string, prune bool) (palace.RecomputeResult, error)
}

// registerMergeWing: fold one or more source wings into a target, relabeling every
// drawer and closet, then rebuild the derived graph.
//
// Synchronous by decision (M, 2026-08-24), superseding the 2026-06-29 call that
// put merge+rebuild in a durable background job because the rebuild is slow.
// That decision still governs the DASHBOARD, which enqueues a mergejob and shows
// progress; an agent calling this tool has no such queue to watch, and a merge
// that returns before the graph is rebuilt hands back a palace whose hallways
// describe a layout that no longer holds.
func registerMergeWing(reg *registrar, drawers wingMerger, usageSvc *usage.Service) {
	tool := newTool("merge_wing",
		mcp.WithDescription("Merge one or more source wings into a target wing, relabeling every drawer and closet in place, then rebuild hallways/tunnels. If the graph rebuild fails after the relabel, re-run am_recompute_graph."),
		mcp.WithArray("sources", mcp.Required(),
			mcp.Description("The wing names to fold into the target."),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithString("target", mcp.Required(), mcp.Description("The wing to merge the sources into (created if new).")),
	)
	reg.addWrite(tool, mergeWingHandler(drawers, usageSvc))
}

// mergeWingHandler is the merge_wing body, named rather than inlined into the
// registration for the same reason writeGuard is: a test can then drive the real
// handler, and a re-implemented handler in a test proves the test.
//
// ⚠ORDER IS THE CONTRACT. MergeWing relabels the rows; RecomputeGraph derives
// hallways and tunnels FROM those rows. Rebuilding first and relabeling second
// compiles, passes a call-counting check, and leaves exactly the stale graph this
// pair exists to prevent — so the ordering is asserted behaviourally, not by
// counting calls in the source.
func mergeWingHandler(drawers wingMerger, usageSvc *usage.Service) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		target, err := req.RequireString("target")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sources, ok := stringSlice(req.GetArguments()["sources"])
		if !ok || len(sources) == 0 {
			return mcp.NewToolResultError("sources must be a non-empty array of wing-name strings"), nil
		}
		res, err := drawers.MergeWing(ctx, t.TeamID, sources, target)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if _, err := drawers.RecomputeGraph(ctx, t.TeamID, "", true); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"relabeled %d drawers / %d closets, but graph rebuild failed: %v — re-run recompute_graph",
				res.Drawers, res.Closets, err)), nil
		}
		return jsonResult(res), nil
	}
}

// registerMemoriesFiledAway: a quick summary of what the team has filed.
func registerMemoriesFiledAway(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("memories_filed_away",
		// The schema is GENERATED from the very type the handler returns, so the
		// two cannot disagree. That is the whole reason these three tools got one
		// first: their payload is already a named struct, where a hand-written
		// schema would be a second copy of a shape — and the copy nobody maintains
		// is the one that goes false.
		mcp.WithOutputSchema[palace.FiledAwayResult](),
		mcp.WithDescription("Summarise what the team CURRENTLY holds: memories, the rows they occupy, distinct wings and rooms, and the most recent filing. `count` is memories — a memory over the chunk threshold is several rows sharing a parent, and `drawers` is that row count, reported beside it rather than instead of it. Retracted records are excluded from every figure: they are history, not what is filed. ⚠ Both were wrong until 2026-09-03, when this counted every row ever written and called the total memories — 3460 against 1142 on the palace that found it."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		res, err := drawers.MemoriesFiledAway(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(res), nil
	})
}

// stringSlice coerces an MCP argument (a JSON array decoded to []any) into a
// []string. It returns ok=false if the value is not an array or any element is
// not a plain string, so a malformed `sources` is rejected outright rather than
// silently partially applied.
func stringSlice(v any) ([]string, bool) {
	raw, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
