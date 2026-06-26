// Package mcpserver wires the agentsmemory tools onto a mark3labs/mcp-go server
// exposed over Streamable HTTP, so remote agents connect to it as their memory
// MCP server. Every tool is tenant-scoped: it reads the tenant the auth layer
// placed on the context and fails closed if there is none.
//
// The skeleton registers two tools — status (liveness + tenant echo) and
// load_skill (the centralised-skill read path). The remaining 35 tools from the
// Python contract (search, add_drawer, mine, the graph/KG/diary families) slot
// in here the same way as later phases land.
package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Deps are the collaborators the tools need. Passing them in (rather than
// reaching for globals) keeps the server testable and the wiring explicit.
type Deps struct {
	Skills *skill.Service
}

// New builds the MCP server and registers all tools. The returned *MCPServer is
// handed to a transport (Streamable HTTP) in cmd/server.
func New(deps Deps) *server.MCPServer {
	srv := server.NewMCPServer(
		"agentsmemory",
		"0.1.0",
		server.WithToolCapabilities(true), // advertise the tools/list capability
	)

	registerStatus(srv)
	registerLoadSkill(srv, deps.Skills)
	return srv
}

// registerStatus adds the status tool: a cheap call an agent makes to confirm
// the connection is authenticated and to learn which team it is scoped to.
func registerStatus(srv *server.MCPServer) {
	tool := mcp.NewTool("status",
		mcp.WithDescription("Report server liveness and the team this MCP session is scoped to."),
	)
	srv.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, ok := auth.TenantFrom(ctx)
		if !ok {
			return mcp.NewToolResultError("unauthenticated: present a valid Bearer token"), nil
		}
		out, _ := json.Marshal(map[string]any{
			"ok":      true,
			"team_id": t.TeamID,
			"role":    string(t.Role),
		})
		return mcp.NewToolResultText(string(out)), nil
	})
}

// registerLoadSkill adds the load_skill tool: an agent passes a skill name and
// receives the centralised, team-shared skill body so it can load it into a
// skill slot. Read access is granted to any team member.
func registerLoadSkill(srv *server.MCPServer, skills *skill.Service) {
	tool := mcp.NewTool("load_skill",
		mcp.WithDescription("Load a centralised, team-shared skill by name. Returns the skill body and version so the calling agent can use it directly."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("The unique skill name within the team, e.g. \"effective-go\"."),
		),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, ok := auth.TenantFrom(ctx)
		if !ok {
			return mcp.NewToolResultError("unauthenticated: present a valid Bearer token"), nil
		}
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		res, err := skills.Load(ctx, t.TeamID, name)
		if err != nil {
			// A missing skill is a normal, recoverable outcome for the agent —
			// surface it as a tool error, not a transport failure.
			return mcp.NewToolResultError(err.Error()), nil
		}
		out, _ := json.Marshal(res)
		return mcp.NewToolResultText(string(out)), nil
	})
}
