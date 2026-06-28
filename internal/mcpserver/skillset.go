package mcpserver

import (
	"context"

	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerSkillset wires am_skillset: the wake-up tool. A waking agent calls it
// first to learn how to drive this server — the superadmin-authored playbook
// (which tools to call, in what order, which centralised skills to load) plus the
// LIVE catalogue of every registered tool. It takes no arguments and any
// authenticated tenant may call it, because the global playbook is identical for
// everyone (only a platform superadmin edits it, via the dashboard).
//
// It closes over reg so the returned catalogue is read at call time from the
// registrar's live list — never a stale snapshot. Registered last (see New), so
// reg.catalog already holds every other tool plus am_skillset itself.
func registerSkillset(reg *registrar, skillsets *skillset.Service, usageSvc *usage.Service) {
	tool := newTool("skillset",
		mcp.WithDescription("Wake-up playbook for this memory server: how to use the am_* tools — which to call, in what order, and which centralised skills to load — plus the live catalogue of every available tool. Call this FIRST in a new session."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Meter like every other tool; the tenant scope itself is irrelevant here
		// (the playbook is global), but the call still counts against the cap.
		if _, errResult, ok := admit(ctx, usageSvc); !ok {
			return errResult, nil
		}

		// The superadmin-authored preamble. "Not set yet" is a normal state on a
		// brand-new database (before the seed or any edit), so an empty preamble is
		// returned rather than an error — the catalogue below is still useful alone.
		sk, found, err := skillsets.Get(ctx)
		if err != nil {
			return mcp.NewToolResultError("could not load the skillset playbook"), nil
		}
		preamble, version := "", 0
		if found {
			preamble, version = sk.Content, sk.Version
		}

		return jsonResult(map[string]any{
			"preamble":   preamble,    // the curated "when/which/how" wakeup guidance
			"version":    version,     // playbook version (0 when unset), for cache/staleness
			"tools":      reg.catalog, // live [{name, description}] — the real tool surface
			"tool_count": len(reg.catalog),
			"hint":       "Follow the preamble's wake-up order; load named skills with am_load_skill.",
		}), nil
	})
}
