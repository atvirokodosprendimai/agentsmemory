package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// kgQueryDefaultStatus is the endedness kg_query applies when the caller names
// none. It is ONE literal in ONE place on purpose: ADR-026 flips it from "all" to
// "current" as a separate, revertible commit, and reverting must be this line and
// nothing else. Anything that needs to branch on the default is a sign the filter
// logic leaked out of palace.KGQuery, where it belongs.
const kgQueryDefaultStatus = palace.KGStatusCurrent

// kgStatusParamDescription is BUILT from the palace constants rather than
// restating them, so a status the service accepts can never drift from the list
// the agent is told about.
var kgStatusParamDescription = fmt.Sprintf(
	"Which half of a fact's life to return: %q (open-ended, not retracted), %q (retracted — the audit direction), or %q. Default %q. This is filtered SERVER-SIDE, so what it removes never reaches your context; it selects on whether a fact was ever ended, which is a different question from as_of's \"was it in effect at that moment\", and the two compose.",
	palace.KGStatusCurrent, palace.KGStatusEnded, palace.KGStatusAll, kgQueryDefaultStatus)

// registerKG wires the temporal knowledge-graph tools: kg_add / kg_invalidate /
// kg_supersede (write facts, retract them, and replace them atomically),
// kg_query / kg_timeline (read, optionally as-of a point in time), and kg_stats.
// All are tenant-scoped via admit.
func registerKG(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	registerKGAdd(reg, drawers, usageSvc)
	registerKGInvalidate(reg, drawers, usageSvc)
	registerKGSupersede(reg, drawers, usageSvc)
	registerKGQuery(reg, drawers, usageSvc)
	registerKGStats(reg, drawers, usageSvc)
	registerKGTimeline(reg, drawers, usageSvc)
	registerEntryPoint(reg, drawers, usageSvc)
}

func registerKGAdd(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_add",
		// The derivation renders this "Knowledge-graph add", which names the noun and
		// not the act. An explicit title wins over the derived one, which is what the
		// override in newTool exists for.
		mcp.WithToolTitle("Add knowledge-graph fact"),
		mcp.WithDescription("Add a fact (subject → predicate → object) to the temporal knowledge graph, optionally with a validity window. THIS IS A REQUIRED PART OF PERSISTING A SESSION, not an optional extra: a drawer with no edge is an orphan — reachable by search, invisible to traversal, and it still surfaces in the AUTHOR's own search, which is why authors believe it is reachable. The graph is also what still answers in a dormant wing, where search is weak because you cannot retrieve what you do not know to ask for. When a session established no durable fact, say so rather than skipping silently. Re-adding an identical current fact is a no-op; to replace a fact, invalidate the old one first. SCOPE: graph facts are WORKSPACE-wide, not scoped to a wing — unlike drawers, anchors and search, a fact filed from one project is returned to every project in this workspace. File a fact here when it is true of the workspace; put project-specific detail in a drawer, which is wing-scoped."),
		mcp.WithString("subject", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's subject entity. A SHORT LABEL (max %d characters), not a sentence — the entity is a node the graph is queried by, so put explanation in a drawer and point at it with source_drawer_id.", palace.MaxKGValueLen))),
		mcp.WithString("predicate", mcp.Required(), mcp.Description(fmt.Sprintf("The relationship (e.g. \"works_at\"). A safe name: max %d characters, and no \"/\", \"\\\\\" or \"..\" — it is validated like a name, not stored like a value, so \"uses/abuses\" is rejected.", palace.MaxNameLength))),
		mcp.WithString("object", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's object entity. A SHORT LABEL (max %d characters), not a sentence — evidence, commit ids and repro steps belong in a drawer referenced by source_drawer_id, never smuggled in here.", palace.MaxKGValueLen))),
		mcp.WithString("valid_from", mcp.Description("Optional start of validity (YYYY-MM-DD or YYYY-MM-DDTHH:MM:SSZ).")),
		mcp.WithString("valid_to", mcp.Description("Optional end of validity; omit while the fact is current.")),
		mcp.WithString("source_closet", mcp.Description("Optional closet id this fact came from.")),
		mcp.WithString("source_file", mcp.Description("Optional source label.")),
		mcp.WithString("source_drawer_id", mcp.Description("Optional drawer id this fact was extracted from. It is CHECKED: an id naming no drawer in this team is refused rather than stored, because provenance that resolves to nothing is worse than none — it reads as evidence. Pass the full id exactly as am_add_drawer or am_search returned it — a shortened one is refused here rather than stored. ⚠The CHECK IS ON PROVENANCE ONLY: subject and object are entity labels in a schemaless graph and are never checked, so a mistyped one still mints a NEW node silently. An id whose drawer was later RETRACTED or superseded is accepted: a fact records what was believed then, and a correction does not withdraw it.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		mcp.WithDescription("Retract a current fact that nothing replaces, by ending its validity window and recording WHY. The fact is kept (queryable as-of an earlier time), never deleted. When something does replace it, use kg_supersede — hand-rolling invalidate-then-add leaves both values in effect between the two calls. Returns ended_facts: how many CURRENT rows this ended — one triple can match several. REFUSES when nothing matched, rather than reporting success for a fact it never touched: either it was never filed, or it is already ended."),
		mcp.WithString("subject", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's subject entity. A SHORT LABEL (max %d characters), not a sentence — the entity is a node the graph is queried by, so put explanation in a drawer and point at it with source_drawer_id.", palace.MaxKGValueLen))),
		mcp.WithString("predicate", mcp.Required(), mcp.Description("The relationship.")),
		mcp.WithString("object", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's object entity. A SHORT LABEL (max %d characters), not a sentence — evidence, commit ids and repro steps belong in a drawer referenced by source_drawer_id, never smuggled in here.", palace.MaxKGValueLen))),
		mcp.WithString("ended", mcp.Description("When it stopped being true (YYYY-MM-DD or datetime). Omitted, it defaults to the INSTANT of the call, not to today's date, and the response echoes what was stored. ⚠A date you pass EXPLICITLY is date-granular on the read side: a bare date is treated as the END of that day, so the fact stays visible to as_of for the rest of it while status:\"current\" drops it immediately. Pass a datetime, or omit this, whenever the two answers agreeing matters. ⚠The boundary itself is INCLUSIVE either way: a fact whose valid_to equals the as_of you ask with is still in effect, while status:\"current\" has already dropped it — so the two agree everywhere EXCEPT that one instant, which is the instant kg_supersede returns.")),
		mcp.WithString("reason", mcp.Required(), mcp.Description("Why it stopped being true. valid_to already records THAT the fact ended; the reason is the half a later reader cannot reconstruct from the row, and it had nowhere to land until now. If something REPLACES this fact, use kg_supersede instead — it does both ends in one transaction.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		reason, err := req.RequireString("reason")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		n, fact, ended, err := drawers.KGInvalidate(ctx, t.TeamID, subject, predicate, object, req.GetString("ended", ""), reason)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// ended_facts is returned rather than implied. One (subject, predicate,
		// object) can match several CURRENT rows — the same fact re-asserted with a
		// different valid_from — so "it worked" and "three facts ended" are
		// different answers, and only the second one is checkable.
		return jsonResult(map[string]any{"success": true, "fact": fact, "ended": ended, "ended_facts": n}), nil
	})
}

// registerKGSupersede is the line that makes the atomic replacement reachable.
// Without it Service.KGSupersede is a correct function no agent can call, and the
// hand-rolled invalidate-then-add stays the only expressible replacement — which
// is the sequence issue #74 reproduced a day-scale overlap in.
func registerKGSupersede(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_supersede",
		mcp.WithDescription("Replace a fact with a new value in ONE transaction: the old value's window ends and the new one starts at the SAME instant, so no query sees both values or neither. Use this instead of kg_invalidate followed by kg_add — that sequence ends the old fact at day precision, which leaves both values in effect for the rest of the day, and leaves the graph with zero current values if the session dies between the two calls."),
		mcp.WithString("subject", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's subject entity. A SHORT LABEL (max %d characters), not a sentence.", palace.MaxKGValueLen))),
		mcp.WithString("predicate", mcp.Required(), mcp.Description("The relationship. Unchanged by a supersede — this replaces the OBJECT.")),
		mcp.WithString("old_object", mcp.Required(), mcp.Description("The value that stopped being true. Must match a CURRENT fact; the call is refused and changes nothing if it does not.")),
		mcp.WithString("new_object", mcp.Required(), mcp.Description(fmt.Sprintf("The value that replaces it. A SHORT LABEL (max %d characters).", palace.MaxKGValueLen))),
		mcp.WithString("reason", mcp.Required(), mcp.Description("Why the old value stopped being true. The row already records THAT it ended; only you can say why.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		var args [5]string
		for i, name := range []string{"subject", "predicate", "old_object", "new_object", "reason"} {
			v, err := req.RequireString(name)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			args[i] = v
		}
		boundary, err := drawers.KGSupersede(ctx, t.TeamID, args[0], args[1], args[2], args[3], args[4])
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// The boundary is returned rather than implied: it is the single instant
		// both windows share, and an agent that wants to query either side of the
		// replacement needs it.
		return jsonResult(map[string]any{
			"success": true, "boundary": boundary,
			"fact": args[0] + " → " + args[1] + " → " + args[3], "replaced": args[2],
		}), nil
	})
}

func registerKGQuery(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_query",
		mcp.WithDescription("Query the knowledge graph by entity, by predicate, or both — optionally as of a point in time, in a chosen direction, and restricted to facts that are still current. Give at least one of entity/predicate. Facts are workspace-wide: this returns facts filed by any project in the workspace, not only this registration's. ⚠READ `resolution` BEFORE CONCLUDING ANYTHING FROM count:0 — it is three answers, not one: `matched`, `known_term_no_facts` (the graph knows the term and has nothing filed) and `unknown_term` (it has never heard of it, so check your spelling; `unresolved` names which of entity/predicate was the unknown one). Reporting \"nothing is filed\" on an unknown_term is how a pointer to nowhere becomes a finding."),
		mcp.WithString("entity", mcp.Description("The entity to look up. Optional when predicate is given.")),
		mcp.WithString("predicate", mcp.Description("Only facts with this relation. Given WITHOUT an entity it is an entry point in its own right, answering \"every fact of this relation\" — how you audit a whole relation type, e.g. every retracts edge. Given WITH an entity it narrows that entity's facts.")),
		mcp.WithString("as_of", mcp.Description("Only facts in effect at this instant (YYYY-MM-DD or datetime). ⚠It is DATE-GRANULAR on the retraction end, so it can lag status:\"current\" by up to a day: a fact whose valid_to is a bare date is in effect through the END of that day, while status:\"current\" excludes it the moment it is retracted. Retractions made through this server now stamp an instant, so the lag is confined to a date-only end that a caller passed explicitly or that predates that change — ask with a datetime when the exact boundary matters. ⚠Even then the end is INCLUSIVE: a fact whose valid_to equals your as_of is still in effect here while status:\"current\" excludes it, so the disagreement narrows to that single instant rather than vanishing.")),
		mcp.WithString("direction", mcp.Description("\"outgoing\", \"incoming\", or \"both\" (default). Ignored without an entity: with predicate alone there is no queried endpoint for a fact to be incoming or outgoing of.")),
		mcp.WithString("status", mcp.Description(kgStatusParamDescription)),
		mcp.WithNumber("limit", mcp.Description("How many facts this page carries (default 100, max 1000). A page is ALSO cut when it would exceed the response budget, whatever limit you ask for — so a large limit does not buy a larger answer, it only stops the budget being the thing that decides.")),
		mcp.WithString("cursor", mcp.Description("Continue a cut page: pass back the `next_cursor` a previous call returned, VERBATIM. It is opaque and one-way — it names the entry point as well as the position, because a both-directions query is two index scans and a bare row id cannot say which to resume. Assembling one yourself is refused rather than silently mis-paged.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		asOf := req.GetString("as_of", "")
		res, err := drawers.KGQuery(ctx, t.TeamID, palace.KGQueryInput{
			Entity:    req.GetString("entity", ""),
			Predicate: req.GetString("predicate", ""),
			AsOf:      asOf,
			Direction: req.GetString("direction", "both"),
			Status:    req.GetString("status", kgQueryDefaultStatus),
			Limit:     req.GetInt("limit", 0),
			Cursor:    req.GetString("cursor", ""),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// ⚠ THE BUDGET IS SPENT BEFORE THE COUNT IS TAKEN. A page cut here and a
		// count taken before it would disagree, and the count is what a caller
		// reads to decide whether the answer was complete.
		facts, cut, cursor := boundGraphPage(res.Facts, res.NextCursor)
		out := map[string]any{
			"facts": facts, "count": len(facts), "status": res.Status,
			// resolution is what separates "nothing is filed about this" from
			// "you asked about something this graph has never heard of". Both
			// used to arrive as count:0 with no error, so a caller could not act
			// on the difference and a pointer built on the second pointed nowhere.
			// It is rendered here, not merely set on the Go struct: a field no
			// handler emits is invisible to every agent, and no behavioural test
			// can see that.
			"resolution": string(res.Resolution),
		}
		// Named only when something actually failed to resolve, so the key's
		// presence is itself the signal rather than an empty string every caller
		// has to compare against.
		if res.Unresolved != "" {
			out["unresolved"] = res.Unresolved
		}
		// Each entry point is echoed only when it was used, so the response says
		// which question was asked rather than carrying an empty key for the one
		// that was not.
		if res.Entity != "" {
			out["entity"] = res.Entity
		}
		if res.Predicate != "" {
			out["predicate"] = res.Predicate
		}
		if asOf != "" {
			out["as_of"] = asOf
		}
		// A filtered page must say what it filtered rather than presenting itself
		// as the whole. ADR-010's argument is the reason this is never silent: a
		// session about to redo a rejected thing does not know to ask for history
		// — that is precisely what it does not know. So the keys appear only when
		// something was actually removed, which makes their presence informative,
		// and the hint names the parameter that brings it back.
		withheld := map[string]int64{}
		for cause, n := range res.Withheld {
			if n > 0 {
				withheld[cause] = n
			}
		}
		if cut > 0 {
			withheld[palace.KGWithheldBudget] = int64(cut)
		}
		if len(withheld) > 0 {
			out["withheld"] = withheld
			var hints []string
			for _, st := range []string{palace.KGStatusEnded, palace.KGStatusCurrent} {
				if n := withheld[st]; n > 0 {
					hints = append(hints, fmt.Sprintf(
						"%d %s fact(s) not shown — pass status:%q to see them, or status:%q for both.",
						n, st, st, palace.KGStatusAll))
				}
			}
			if n := withheld[palace.KGWithheldBudget]; n > 0 {
				hints = append(hints, fmt.Sprintf(
					"%d fact(s) did not fit this response — pass cursor:%q to continue, or descend to a "+
						"narrower entity instead: a node this wide is usually one that wants splitting by topic.",
					n, cursor))
			}
			out["hint"] = strings.Join(hints, " ")
		}
		if cursor != "" {
			out["next_cursor"] = cursor
		}
		return jsonResult(out), nil
	})
}

func registerKGStats(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_stats",
		mcp.WithOutputSchema[palace.KGStatsResult](),
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
		mcp.WithDescription("Chronological timeline of facts (validity start order), for one entity or — with no entity — across the whole graph. Facts are workspace-wide, so a timeline may include facts filed by another project in this workspace."),
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

// registerEntryPoint exposes a wing's front door.
//
// The reg.add call is the line that makes it REACHABLE, and the catalogue entry
// that call produces is what makes it DISCOVERABLE — an agent consults the
// catalogue, and a tool the handler serves but the catalogue omits is one nobody
// will ever call.
func registerEntryPoint(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("entry_point",
		mcp.WithDescription("Where to START in a wing. Returns the wing's entry node and what it points at, so a session needs no id from a skill file and no multi-hop walk to begin. Edges whose target is not readable from this wing are dropped and counted in refused, never listed. A wing with no entry point says so, distinguishably from an error."),
		mcp.WithString("wing", mcp.Required(), mcp.Description("The wing whose entry point to resolve.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		res, err := drawers.EntryPoint(ctx, t.TeamID, req.GetString("wing", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{
			"wing": res.Wing, "node": res.Node, "edges": res.Edges,
			"resolution": string(res.Resolution),
		}
		// A refusal count of zero is the normal case and stays out of the
		// response; when present it says "the front door holds more than you
		// were shown", which is the one fact a filtered listing owes its reader.
		if res.Refused > 0 {
			out["refused"] = res.Refused
		}
		return jsonResult(out), nil
	})
}

// boundGraphPage fills a graph answer until the response budget is spent, and
// reports how many facts it could not carry plus the cursor that continues them.
//
// ⚠ THE GRAPH WAS THE ONE AGENT-FACING READ WITH NO BOUND (ADR-053). am_search
// and am_list_drawers have spent ResponseBudget since the page-size work; this
// query spent nothing, and measured on the live corpus 2026-09-04 a single
// entity rendered ~106,000 runes against a 40,000-rune budget. A tool result
// that large does not arrive smaller — it does not arrive at all, and an agent
// reads the empty result as "the graph holds nothing about this".
//
// The budget is checked BEFORE each fact is appended as well as after the last
// one, because a budget consulted only up front is not a bound — the same defect
// headWithin was written to fix on the drawer side.
//
// It is a BACKSTOP behind the caller's limit rather than the primary mechanism.
// A fan-out has no ranking, so a page cut with no way to continue is an
// arbitrary subset the caller cannot complete: the cursor is what makes the cut
// honest, and it is why this returns one for a page the budget shortened even
// when the store had no more rows to give.
func boundGraphPage(facts []palace.KGFact, storeCursor string) (kept []palace.KGFact, cut int, cursor string) {
	spent := graphEnvelopeReserve
	for i, f := range facts {
		b, err := json.Marshal(f)
		if err != nil {
			// A fact that cannot be rendered is not one to silently drop: keep it
			// and let the encoder report, rather than reporting it as withheld by
			// a budget that never saw it.
			kept = append(kept, f)
			continue
		}
		n := len([]rune(string(b)))
		if spent+n > ResponseBudget && i > 0 {
			// i > 0 so a single oversized fact is still returned rather than an
			// empty page: one fact past the budget is a page a client may still
			// truncate, while zero facts with a cursor is an answer that says
			// nothing and asks the caller to try again for the same result.
			return kept, len(facts) - i, kgPageCursor(facts[i-1])
		}
		spent += n
		kept = append(kept, f)
	}
	return kept, 0, storeCursor
}

// graphEnvelopeReserve is what a graph response costs BEFORE its facts: the
// entity, predicate, status, resolution, withheld map, hint and next_cursor,
// plus the JSON escaping the MCP layer applies when it puts this payload inside
// a text content — every quote in every fact becomes two runes there.
//
// ⚠ IT EXISTS BECAUSE A BUDGET SPENT ONLY ON CONTENT IS NOT A BUDGET ON THE
// RESPONSE. Measured 2026-09-04 with no reserve: a page filled exactly to
// ResponseBudget rendered 40,324 runes, so the bound was 324 runes short of the
// thing it claims to bound. The reserve is deliberately larger than that
// measurement rather than fitted to it — the overshoot scales with how many
// quotes the facts carry, and a constant tuned to one corpus is one that goes
// wrong on the next.
const graphEnvelopeReserve = 2_000

// kgPageCursor rebuilds the store's cursor for a fact the budget stopped at. The
// encoding lives in internal/palace; this reads the two fields it is made of
// rather than duplicating the format, so a change there cannot leave this half
// minting cursors the store refuses.
func kgPageCursor(last palace.KGFact) string {
	return palace.KGCursorFor(last)
}
