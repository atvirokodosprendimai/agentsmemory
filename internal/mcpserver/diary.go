package mcpserver

import (
	"context"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerDiary wires the agent-diary tools — diary_write and diary_read — onto
// the MCP server. A diary is an agent's append-only journal: entries accumulate
// in the "diary" room of the agent's wing and are read back newest-first. Both
// handlers share the admit() preamble (auth + monthly metering) and are scoped to
// the resolved tenant's TeamID, so an agent's journal never crosses workspaces.
func registerDiary(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	registerDiaryWrite(srv, drawers, usageSvc)
	registerDiaryRead(srv, drawers, usageSvc)
}

// registerDiaryWrite: append a journal entry for an agent. The entry is filed as
// a drawer in the agent's diary room (chunked + embedded like any memory) but is
// timestamp-unique, so re-journaling identical text keeps both entries.
func registerDiaryWrite(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("diary_write",
		mcp.WithDescription("Append a diary entry to an agent's journal (in AAAK format). Entries are timestamped and accumulate in the agent's diary room over time."),
		mcp.WithString("agent_name", mcp.Required(), mcp.Description("Whose journal to write to (case-insensitive; stored lowercased).")),
		mcp.WithString("entry", mcp.Description("The diary entry text, ideally in AAAK. Either entry or content is required; entry wins if both are given.")),
		mcp.WithString("content", mcp.Description("Alias for entry — accepted because am_add_drawer uses \"content\".")),
		mcp.WithString("topic", mcp.Description("Optional tag grouping entries (default \"general\").")),
		mcp.WithString("wing", mcp.Description("Optional target wing (default wing_<agent_name>).")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
			Wing:  req.GetString("wing", ""),
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
		return jsonResult(out), nil
	})
}

// registerDiaryRead: return an agent's most recent diary entries, newest first.
// An omitted wing reads across every wing the agent has journaled in.
func registerDiaryRead(srv *server.MCPServer, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("diary_read",
		mcp.WithDescription("Read an agent's recent diary entries, newest first. With no wing, returns entries from every wing the agent has written to."),
		mcp.WithString("agent_name", mcp.Required(), mcp.Description("Whose journal to read (case-insensitive).")),
		mcp.WithNumber("last_n", mcp.Description("How many recent entries to return, 1-100 (default 10).")),
		mcp.WithString("wing", mcp.Description("Optional wing to restrict to (default: all wings for this agent).")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
