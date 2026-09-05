package mcpserver

import (
	"context"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerDiary wires the agent-diary tools — diary_write and diary_read — onto
// the MCP server. A diary is an agent's append-only journal: entries accumulate
// in the "diary" room of the agent's wing and are read back newest-first. Both
// handlers share the admit() preamble (auth + monthly metering) and are scoped to
// the resolved tenant's TeamID, so an agent's journal never crosses workspaces.
func registerDiary(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	registerDiaryWrite(reg, drawers, usageSvc)
	registerDiaryRead(reg, drawers, usageSvc)
}

// registerDiaryWrite: append a journal entry for an agent. The entry is filed as
// a drawer in the agent's diary room (chunked + embedded like any memory) but is
// timestamp-unique, so re-journaling identical text keeps both entries.
//
// The description asks for the wrong assumption and the failed approach by name.
// That is deliberate: a repository records only what worked — the dead ends are
// what a later session repeats, and they exist nowhere but here. It is carried in
// the tool description rather than only in the wakeup playbook or a client's Stop
// hook because this text is the one surface every client sees, unconditionally,
// at the moment of the call; the hook exists only on some installs. It also names
// a cheap escape ("say so briefly"), because a prompt that demands a lesson from
// a session that had none gets an invented one — and drawers are verbatim and
// immutable, so a confabulated lesson is recalled forever.
func registerDiaryWrite(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("diary_write",
		mcp.WithDescription("Append a diary entry to an agent's journal (in AAAK format). Entries are timestamped and accumulate in the agent's diary room over time. Record what the next session cannot recover from the code: what you decided and why, which assumption turned out wrong, and which approach you tried that failed — a repository keeps only what worked, so dead ends are lost unless they are written here. When nothing went wrong, say so briefly rather than inventing a lesson. Use a stable agent_name so entries thread across sessions. Fields that appear only when they apply: chunk_ids, the ids of every chunk a long entry was split into, so you can address one directly; and pending_embedding with warning when the entry is stored but not yet searchable, which looks identical to a healthy write from here and only an operator can fix."),
		mcp.WithString("agent_name", mcp.Required(), mcp.Description("Whose journal to write to (case-insensitive; stored lowercased).")),
		mcp.WithString("entry", mcp.Description("The diary entry text, ideally in AAAK. Either entry or content is required; entry wins if both are given.")),
		mcp.WithString("content", mcp.Description("Alias for entry — accepted because am_add_drawer uses \"content\".")),
		mcp.WithString("topic", mcp.Description("Optional tag grouping entries (default \"general\").")),
		mcp.WithString("wing", mcp.Description("Optional journal destination. Omitted, uses this MCP registration's default_wing when configured; otherwise wing_<agent_name>.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		agent, err := req.RequireString("agent_name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// entry is the canonical field; content is an accepted alias (callers used
		// to add_drawer reach for "content"). entry wins whenever it is PRESENT —
		// even if explicitly empty — matching the frozen tool's precedence; content
		// is consulted only when entry was not supplied at all. Presence, not
		// emptiness, is the test, so an explicit empty entry is not silently
		// replaced by content. Service-level SanitizeContent rejects an empty result.
		args := req.GetArguments()
		entry := req.GetString("entry", "")
		if _, ok := args["entry"]; !ok {
			entry = req.GetString("content", "")
		}
		res, err := drawers.WriteDiary(ctx, t.TeamID, palace.DiaryWriteInput{
			Agent: agent,
			Entry: entry,
			Topic: req.GetString("topic", ""),
			// An explicit wing wins; otherwise the project this registration
			// belongs to; otherwise the palace's own wing_<agent> default. A diary
			// entry written while working on a project belongs to that project —
			// filing every project's journal under the agent's name is exactly the
			// mixing this default exists to stop.
			Wing: firstWing(req.GetString("wing", ""), auth.DefaultWingFrom(ctx)),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{
			"success":   true,
			"entry_id":  res.EntryID,
			"agent":     res.Agent,
			"topic":     res.Topic,
			"timestamp": res.Timestamp,
			"chunks":    res.Chunks,
		}
		if len(res.ChunkIDs) > 0 {
			out["chunk_ids"] = res.ChunkIDs
		}
		if res.PendingEmbedding {
			out["pending_embedding"] = true
			out["warning"] = pendingEmbeddingWarning
		}
		return jsonResult(out), nil
	})
}

// firstWing returns the first non-blank wing of those given, or "" when there is
// none — which the palace reads as "use the agent's own wing".
func firstWing(wings ...string) string {
	for _, w := range wings {
		if w = strings.TrimSpace(w); w != "" {
			return w
		}
	}
	return ""
}

// registerDiaryRead: return an agent's most recent diary entries, newest first.
// An omitted wing reads across every wing the agent has journaled in.
func registerDiaryRead(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("diary_read",
		mcp.WithDescription("Read an agent's recent diary entries, newest first. With no wing, returns entries from every wing the agent has written to."),
		mcp.WithString("agent_name", mcp.Required(), mcp.Description("Whose journal to read (case-insensitive).")),
		mcp.WithNumber("last_n", mcp.Description("How many recent entries to return, 1-100 (default 10).")),
		mcp.WithString("wing", mcp.Description("Optional wing to restrict to (default: all wings for this agent).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		agent, err := req.RequireString("agent_name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := drawers.ReadDiary(ctx, t.TeamID,
			agent, req.GetString("wing", ""), req.GetInt("last_n", palace.DefaultDiaryReadN))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(res), nil
	})
}
