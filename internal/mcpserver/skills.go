package mcpserver

import (
	"context"

	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerSkills wires the skill-registry management tools that pair with
// load_skill: list_skills (discover what a team shares) and update_skill (a
// writer/admin edits a skill, bumping its version). Both are tenant-scoped.
func registerSkills(reg *registrar, skills *skill.Service, usageSvc *usage.Service) {
	registerListSkills(reg, skills, usageSvc)
	registerUpdateSkill(reg, skills, usageSvc)
}

// skillCaller adapts a resolved tenant to the skill package's RoleHolder, so the
// skill context authorizes against the role without importing the tenant type.
type skillCaller struct{ t tenant.Tenant }

func (c skillCaller) Team() string { return c.t.TeamID }
func (c skillCaller) User() string { return c.t.UserID }

// CanWrite defers to the one definition the whole MCP surface uses, so the skill
// service and the registration cannot come to different conclusions about the
// same role. It was the only role check in this package for a long time — the
// predicate was right and it had one consumer.
func (c skillCaller) CanWrite() bool { return tenant.CanWrite(c.t.Role) }

// registerListSkills: list the team's centralised skills as metadata (no bodies),
// so an agent can see what is available before loading one.
func registerListSkills(reg *registrar, skills *skill.Service, usageSvc *usage.Service) {
	tool := newTool("list_skills",
		mcp.WithOutputSchema[skillsResult](),
		mcp.WithDescription("List the team's centralised skills (name, description, version) without their bodies. Load a body with am_load_skill."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		list, err := skills.List(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(skillsResult{Skills: list, Count: len(list)}), nil
	})
}

// registerUpdateSkill: create or replace a skill's body, bumping its version.
// Requires the writer or admin role; a member is refused.
func registerUpdateSkill(reg *registrar, skills *skill.Service, usageSvc *usage.Service) {
	tool := newTool("update_skill",
		mcp.WithDescription("Create or update a centralised, team-shared skill by name, bumping its version. Requires the writer or admin role."),
		mcp.WithString("name", mcp.Required(), mcp.Description("The unique skill name within the team.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("The new skill body.")),
		mcp.WithString("description", mcp.Description("Optional short description of the skill.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sk, err := skills.Update(ctx, skillCaller{t}, name, req.GetString("description", ""), content)
		if err != nil {
			// A role refusal (ErrForbidden) and any other error are both normal
			// tool-level outcomes here — surface the message, not a transport failure.
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"ok": true, "name": sk.Name, "version": sk.Version, "updated_by": sk.UpdatedBy, "updated_at": sk.UpdatedAt,
		}), nil
	})
}
