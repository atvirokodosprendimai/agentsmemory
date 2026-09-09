package mcpserver

import (
	"context"
	"fmt"

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

// registerUpdateSkill: create or replace a skill's body, or edit part of one in
// place, bumping its version either way. Requires the writer or admin role; a
// member is refused.
func registerUpdateSkill(reg *registrar, skills *skill.Service, usageSvc *usage.Service) {
	tool := newTool("update_skill",
		mcp.WithDescription("Create or update a centralised, team-shared skill by name, bumping its version. Requires the writer or admin role. "+
			"TWO WAYS TO WRITE, and exactly one of them per call. `content` replaces the WHOLE body — the only way to create a skill that does not exist yet, "+
			"and the right one when you are rewriting it. `edits` changes PART of an existing body without resending it: a list of "+
			"[{\"old\":\"<exact text to replace>\",\"new\":\"<what replaces it>\"}], where `old` is copied verbatim out of the body am_load_skill returned. "+
			"Prefer `edits` for anything short of a rewrite — a body may be up to 1,000,000 characters and you can only resend one by reproducing it from "+
			"context, so a whole-body write costs the size of the SKILL rather than the size of the CHANGE and every resend is a chance to corrupt text "+
			"nobody meant to touch. "+
			"⚠ AN ANCHOR MUST MATCH EXACTLY ONCE. Zero matches or more than one refuses the whole call and names which edit and how many times it matched — "+
			"extend the anchor until it is unique, or pass `count` to say how many occurrences you mean and replace all of them. `new` may be empty; an edit "+
			"that deletes is an ordinary edit. Anchors are located in the body as STORED and every replacement is then made together, so you write the whole "+
			"list against the text you read rather than against an intermediate nobody has seen — and nothing is written unless every edit applies. "+
			"`edits` leaves the description alone; `expected_version` refuses the write when the stored version is not the one you loaded, which is the guard "+
			"a partial edit needs and a whole-body replace does not. "+
			"The answer says which path ran — `wrote` is \"content\" or \"edits\" — alongside the new `version` and `content_length`, and on the edits path an "+
			"`edits_applied` count echoing how many you sent. Read `wrote`: a call that meant to patch and fell through to a replace would otherwise look "+
			"identical to one that patched."),
		mcp.WithString("name", mcp.Required(), mcp.Description("The unique skill name within the team.")),
		mcp.WithString("content", mcp.Description("The WHOLE new skill body — a full replace. Required to create a skill that does not exist yet; mutually exclusive with `edits`.")),
		mcp.WithArray("edits", mcp.Description(
			"Change part of an existing body instead of resending it: [{\"old\":\"…\",\"new\":\"…\",\"count\":<optional>}]. "+
				"`old` is exact text copied out of what am_load_skill returned — NOT a line number, because the load carries none and a stale number "+
				"addresses a different line and applies there silently, while a stale anchor can only fail to match. `old` must appear exactly once "+
				"unless `count` says how many occurrences to expect, and all of them are then replaced. Mutually exclusive with `content`, and the "+
				"skill must already exist. A malformed list is refused rather than treated as no edits, because 'nothing matched' and 'nothing was "+
				"sent' are different answers and only one of them is yours.")),
		mcp.WithNumber("expected_version", mcp.Description(
			"Optional guard for `edits`: the version am_load_skill reported when you read the body. The write is refused, naming both versions, when the "+
				"stored skill has moved on — an edit list was authored against one particular body and means nothing against another. Omit it and the "+
				"edits apply to whatever is stored now.")),
		mcp.WithString("description", mcp.Description("Optional short description of the skill. It applies to a `content` write; an `edits` write leaves the stored description untouched.")),
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
		rawEdits, editsSent := req.GetArguments()["edits"]
		content := req.GetString("content", "")
		// Refuse both and neither rather than picking one. A call carrying both
		// expresses two different intentions for one body, and a call carrying
		// neither is the shape a caller lands in when its edit list failed to
		// serialise — answering that with "content must not be empty" sends them
		// to the wrong argument.
		if editsSent && content != "" {
			return mcp.NewToolResultError("pass content (a whole-body replace) or edits (a partial change), not both"), nil
		}
		if !editsSent {
			if content == "" {
				return mcp.NewToolResultError("nothing to write: pass content for a whole-body replace, or edits to change part of an existing skill"), nil
			}
			sk, err := skills.Update(ctx, skillCaller{t}, name, req.GetString("description", ""), content)
			if err != nil {
				// A role refusal (ErrForbidden) and any other error are both normal
				// tool-level outcomes here — surface the message, not a transport failure.
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(skillWriteResult(sk, "content", 0)), nil
		}
		edits, err := parseEdits(rawEdits)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sk, err := skills.Patch(ctx, skillCaller{t}, name, edits, req.GetInt("expected_version", 0))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(skillWriteResult(sk, "edits", len(edits))), nil
	})
}

// skillWriteResult is the one shape both write paths answer in, so a caller
// reading the response does not have to know which argument it sent.
//
// It reports `wrote` — "content" or "edits" — because the two paths differ in
// what they leave alone (an edits write preserves the description) and a caller
// that meant to patch and fell through to a replace would otherwise see an
// identical success. edits_applied appears only on the patch path, where the
// count is the caller's own assertion echoed back.
func skillWriteResult(sk skill.Skill, wrote string, editsApplied int) map[string]any {
	out := map[string]any{
		"ok": true, "name": sk.Name, "version": sk.Version,
		"updated_by": sk.UpdatedBy, "updated_at": sk.UpdatedAt,
		"wrote": wrote, "content_length": len([]rune(sk.Content)),
	}
	if wrote == "edits" {
		out["edits_applied"] = editsApplied
	}
	return out
}

// parseEdits reads the edits argument STRICTLY: a list of {old, new, count}
// objects, refusing anything it cannot read.
//
// ⚠ IT IS DELIBERATELY NOT TOLERANT, AND parseAnchorList NEXT DOOR RECORDS WHY.
// Tolerance is right where an unreadable entry means "no anchors added" and the
// memory is worth more than its anchor. Here an unreadable entry means "this
// change did not happen", and silently applying the entries that did parse would
// write a body the caller never described — a partial edit nobody can predict,
// reported as success. `edits: {…}` instead of `[{…}]` is an ordinary mistake for
// an LLM caller, and it must come back as a refusal naming the argument.
func parseEdits(raw any) ([]skill.Edit, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("edits must be a LIST of {old, new} objects, e.g. [{\"old\":\"…\",\"new\":\"…\"}]")
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("edits is empty: send at least one {old, new} object, or pass content instead")
	}
	out := make([]skill.Edit, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edit %d is not an object: each edit is {\"old\":\"…\",\"new\":\"…\"}", i+1)
		}
		old, ok := m["old"].(string)
		if !ok {
			return nil, fmt.Errorf("edit %d has no string \"old\": that is the exact text to replace", i+1)
		}
		// "new" is optional and may be empty — an edit that deletes text is an
		// ordinary edit — but a non-string is a mistake worth naming.
		next := ""
		if v, present := m["new"]; present {
			if next, ok = v.(string); !ok {
				return nil, fmt.Errorf("edit %d has a non-string \"new\"", i+1)
			}
		}
		count := 0
		if v, present := m["count"]; present {
			n, ok := v.(float64) // JSON numbers arrive as float64
			if !ok {
				return nil, fmt.Errorf("edit %d has a non-numeric \"count\"", i+1)
			}
			count = int(n)
		}
		out = append(out, skill.Edit{Old: old, New: next, Count: count})
	}
	return out, nil
}
