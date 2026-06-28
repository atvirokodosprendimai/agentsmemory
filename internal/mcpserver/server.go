// Package mcpserver wires the agentsmemory tools onto a mark3labs/mcp-go server
// exposed over Streamable HTTP, so remote agents connect to it as their memory
// MCP server. Every tool is tenant-scoped: it reads the tenant the auth layer
// placed on the context, meters the call against the workspace's monthly request
// cap, and fails closed when there is no tenant or the cap is exhausted.
//
// Registered so far: status (liveness + tenant echo), load_skill (the
// centralised-skill read path), the core memory loop (drawer CRUD, semantic
// recall, taxonomy), and the agent diary (diary_write/diary_read). The remaining
// Python-contract tools (mine and the graph/KG families) slot in here the same
// way as later phases land.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Deps are the collaborators the tools need. Passing them in (rather than
// reaching for globals) keeps the server testable and the wiring explicit.
type Deps struct {
	Skills  *skill.Service
	Usage   *usage.Service
	Drawers *palace.Service
}

// New builds the MCP server and registers all tools.
func New(deps Deps) *server.MCPServer {
	srv := server.NewMCPServer(
		"agentsmemory",
		"0.1.0",
		server.WithToolCapabilities(true), // advertise the tools/list capability
	)
	registerStatus(srv, deps.Usage)
	registerLoadSkill(srv, deps.Skills, deps.Usage)
	// Skill-registry management: list + update (write is role-gated).
	registerSkills(srv, deps.Skills, deps.Usage)
	// The core memory loop: drawer CRUD, semantic recall, and palace taxonomy.
	registerDrawers(srv, deps.Drawers, deps.Usage)
	// The agent diary: append-only journal entries (diary_write/diary_read).
	registerDiary(srv, deps.Drawers, deps.Usage)
	// Mining: text -> chunked drawers + closet index (mine).
	registerMine(srv, deps.Drawers, deps.Usage)
	// The navigable graph: hallways, tunnels, traverse, recompute_graph.
	registerGraph(srv, deps.Drawers, deps.Usage)
	// The temporal knowledge graph: kg_add/invalidate/query/stats/timeline.
	registerKG(srv, deps.Drawers, deps.Usage)
	// Palace maintenance: merge_wing, memories_filed_away.
	registerAdmin(srv, deps.Drawers, deps.Usage)
	return srv
}

// admit resolves the tenant and meters one request against the workspace's
// monthly cap. It returns the tenant on success, or a ready-to-return error
// result (and ok=false) when the caller is unauthenticated, the meter fails, or
// the cap is exhausted. Centralising this keeps every tool's preamble identical.
func admit(ctx context.Context, usageSvc *usage.Service) (tenant.Tenant, *mcp.CallToolResult, bool) {
	t, ok := auth.TenantFrom(ctx)
	if !ok {
		return tenant.Tenant{}, mcp.NewToolResultError("unauthenticated: present a valid Bearer token"), false
	}
	st, err := usageSvc.Allow(ctx, t.TeamID)
	if err != nil {
		return tenant.Tenant{}, mcp.NewToolResultError("usage metering failed"), false
	}
	if !st.Allowed {
		return tenant.Tenant{}, mcp.NewToolResultError(
			fmt.Sprintf("monthly request cap reached (%d/%d) — upgrade the project's plan", st.Used, st.Cap),
		), false
	}
	return t, nil, true
}

// registerStatus adds the status tool: a cheap, metered call confirming the
// session is authenticated and reporting the team and remaining quota.
func registerStatus(srv *server.MCPServer, usageSvc *usage.Service) {
	tool := mcp.NewTool("status",
		mcp.WithDescription("Report server liveness, the team this MCP session is scoped to, and remaining monthly quota."),
	)
	srv.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		st, _ := usageSvc.Snapshot(ctx, t.TeamID)
		out, _ := json.Marshal(map[string]any{
			"ok":             true,
			"team_id":        t.TeamID,
			"role":           string(t.Role),
			"used_this_month": st.Used,
			"monthly_cap":    st.Cap,
			"remaining":      st.Remaining(),
		})
		return mcp.NewToolResultText(string(out)), nil
	})
}

// registerLoadSkill adds the load_skill tool: an agent passes a skill name and
// receives the centralised, team-shared skill body. Read access for any member.
func registerLoadSkill(srv *server.MCPServer, skills *skill.Service, usageSvc *usage.Service) {
	tool := mcp.NewTool("load_skill",
		mcp.WithDescription("Load a centralised, team-shared skill by name. Returns the skill body and version so the calling agent can use it directly."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("The unique skill name within the team, e.g. \"effective-go\"."),
		),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := skills.Load(ctx, t.TeamID, name)
		if err != nil {
			// A missing skill is a normal outcome for the agent — surface it as
			// a tool error, not a transport failure.
			return mcp.NewToolResultError(err.Error()), nil
		}
		out, _ := json.Marshal(res)
		return mcp.NewToolResultText(string(out)), nil
	})
}
