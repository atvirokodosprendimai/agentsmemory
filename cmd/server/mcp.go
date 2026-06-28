// mcp.go implements the `mcp` subcommand: a read-only CLI over the same palace
// domain services the HTTP MCP server (internal/mcpserver) exposes to remote
// agents. It is the second driving adapter onto one domain core, for operators
// and scripts that want to recall memory from the shell without an OAuth
// round-trip — e.g. `agentsmemory mcp search "auth bug"`.
//
// Read-only by construction: the dispatch table (readOnlyTools) lists only tools
// that read. A write tool name simply is not found, and the CLI says so. Two
// tenant paths mirror the agreed design: --token resolves an API key exactly
// like the HTTP gate and meters the call against the workspace cap (parity),
// while --team selects a team id for local admin reads with no metering (the
// operator owns the database).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"

	"github.com/urfave/cli/v3"
)

// mcpCommand builds the `mcp` subcommand. It reuses dataFlags (the storage/embed
// flags) so it opens the same database as serve, and adds the auth selectors
// (--token / --team) and the repeatable -a/--arg tool-argument flag.
func mcpCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:      "mcp",
		Usage:     "Invoke a read-only memory tool from the CLI (run with no tool to list them)",
		ArgsUsage: "[tool] [primary-arg]",
		Flags: append(dataFlags(def),
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars("AGENTSMEMORY_TOKEN"), Usage: "API key: resolves the tenant and meters the call (HTTP parity)"},
			&cli.StringFlag{Name: "team", Usage: "team id: local admin read, no metering (alternative to --token)"},
			&cli.StringSliceFlag{Name: "arg", Aliases: []string{"a"}, Usage: "tool argument as key=value (repeatable)"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return runMCP(ctx, c, def)
		},
	}
}

// runMCP dispatches one read-only tool call. With no tool name it prints the
// catalogue (like tools/list); otherwise it wires the services, resolves the
// tenant, meters when on the token path, runs the tool, and prints its result as
// indented JSON.
func runMCP(ctx context.Context, c *cli.Command, def config.Config) error {
	tools := readOnlyTools()

	// The am_ prefix the HTTP server adds is for client disambiguation; accept
	// it but don't require it on the CLI.
	name := strings.TrimPrefix(c.Args().First(), "am_")
	if name == "" {
		printToolList(tools)
		return nil
	}

	tool, ok := findTool(tools, name)
	if !ok {
		// A write tool (add_drawer, kg_add, …) lands here too: the CLI is
		// read-only on purpose, so it is simply not in the catalogue.
		return fmt.Errorf("unknown read-only tool %q; run `agentsmemory mcp` to list the available tools", name)
	}

	// Wire the same services serve uses. buildServices migrates but does not
	// seed, so a read never mutates team data.
	svc, err := buildServices(configFromCmd(c, def))
	if err != nil {
		return err
	}

	team, meter, err := resolveTenant(ctx, svc, c)
	if err != nil {
		return err
	}
	// The token path mirrors the HTTP admit(): meter one request against the
	// workspace cap and fail closed when exhausted. The --team admin path skips
	// metering — it is local operator access to the operator's own database.
	if meter {
		st, err := svc.usage.Allow(ctx, team)
		if err != nil {
			return fmt.Errorf("usage metering failed: %w", err)
		}
		if !st.Allowed {
			return fmt.Errorf("monthly request cap reached (%d/%d) — upgrade the project's plan", st.Used, st.Cap)
		}
	}

	// Fold the bare positional into the tool's primary argument, layered under
	// any explicit -a values (which win). The tool's run closure then reads its
	// inputs the same way the HTTP handler does.
	args := parseArgs(c.StringSlice("arg"), tailArgs(c), tool.primary)
	res, err := tool.run(ctx, svc, team, args)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// resolveTenant picks the tenant the call acts as. --token resolves an API key
// like the HTTP gate (and signals metering); --team trusts a team id for local
// admin reads (no metering). Exactly one is required.
func resolveTenant(ctx context.Context, svc *services, c *cli.Command) (team string, meter bool, err error) {
	if token := c.String("token"); token != "" {
		t, err := svc.tenants.ResolveToken(ctx, token)
		if err != nil {
			return "", false, fmt.Errorf("resolve --token: %w", err)
		}
		return t.TeamID, true, nil
	}
	if team := c.String("team"); team != "" {
		return team, false, nil
	}
	return "", false, fmt.Errorf("provide --token (or AGENTSMEMORY_TOKEN) for a metered, auth-parity read, or --team <id> for a local admin read")
}

// tailArgs returns the positional tokens after the tool name. parseArgs scans
// them so the hybrid syntax (`mcp search "q" -a limit=5`) works regardless of
// whether urfave/cli consumed the interspersed -a into its flag slice.
func tailArgs(c *cli.Command) []string {
	all := c.Args().Slice()
	if len(all) <= 1 {
		return nil
	}
	return all[1:]
}

// cliArgs carries the parsed tool arguments. The typed getters mirror mcp-go's
// req.GetString/GetInt/GetFloat so each handler reads its inputs the same way
// the HTTP tool does, defaulting when an argument is absent.
type cliArgs struct {
	m map[string]string
}

func (a cliArgs) str(key string) string { return a.m[key] }

func (a cliArgs) strOr(key, def string) string {
	if v, ok := a.m[key]; ok && v != "" {
		return v
	}
	return def
}

func (a cliArgs) intOr(key string, def int) int {
	if v, ok := a.m[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func (a cliArgs) floatOr(key string, def float64) float64 {
	if v, ok := a.m[key]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// parseArgs builds the tool arguments from the -a/--arg flag values plus the raw
// trailing tokens. It is robust to how urfave/cli splits flags vs positionals:
// it reads the cli-parsed --arg slice AND re-scans the tail, so a key=value lands
// in the map whichever way it arrived. The first plain token (not key=value, not
// an -a marker) becomes the primary positional, folded under primaryKey unless
// an explicit -a already set it.
func parseArgs(argFlags, rawTail []string, primaryKey string) cliArgs {
	m := map[string]string{}
	add := func(kv string) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[strings.TrimSpace(k)] = v
		}
	}
	for _, kv := range argFlags {
		add(kv)
	}

	var positional string
	for i := 0; i < len(rawTail); i++ {
		tok := rawTail[i]
		switch {
		case tok == "-a" || tok == "--arg":
			if i+1 < len(rawTail) {
				add(rawTail[i+1])
				i++
			}
		case strings.Contains(tok, "="):
			add(tok)
		case positional == "":
			positional = tok
		}
	}

	if positional != "" && primaryKey != "" {
		if _, exists := m[primaryKey]; !exists {
			m[primaryKey] = positional
		}
	}
	return cliArgs{m: m}
}

// mcpTool is one read-only CLI tool: its wire name (without the am_ prefix), the
// argument the bare positional fills under the hybrid syntax (empty when the
// tool takes no positional), a one-line usage string for the catalogue, and the
// run closure that calls the matching palace/skill service method and returns a
// JSON-marshalable result.
type mcpTool struct {
	name    string
	primary string
	usage   string
	run     func(ctx context.Context, svc *services, team string, a cliArgs) (any, error)
}

// readOnlyTools is the catalogue of read-only tools the CLI exposes — the read
// subset of the HTTP MCP surface, each delegating to the same palace/skill
// service method its HTTP counterpart calls. Nothing here mutates state.
func readOnlyTools() []mcpTool {
	return []mcpTool{
		{"status", "", "team + role, memory overview (wings→rooms), remaining quota",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				st, _ := svc.usage.Snapshot(ctx, team)
				tax, _ := svc.drawers.GetTaxonomy(ctx, team)
				total := 0
				for _, w := range tax.Wings {
					total += w.Drawers
				}
				return map[string]any{
					"team_id":       team,
					"total_drawers": total,
					"wings":         tax.Wings,
					"usage": map[string]any{
						"used_this_month": st.Used,
						"monthly_cap":     st.Cap,
						"remaining":       st.Remaining(),
					},
				}, nil
			}},
		{"search", "query", "semantic recall of drawers by query (args: wing, room, limit, max_distance)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.Search(ctx, team, palace.SearchQuery{
					Query:       a.str("query"),
					Wing:        a.str("wing"),
					Room:        a.str("room"),
					Limit:       a.intOr("limit", palace.DefaultSearchLimit),
					MaxDistance: a.floatOr("max_distance", palace.DefaultMaxDistance),
				})
			}},
		{"get_drawer", "id", "fetch one drawer by id",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.Get(ctx, team, a.str("id"))
			}},
		{"list_drawers", "wing", "list drawers, optionally filtered (args: wing, room, limit, offset)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.List(ctx, team, a.str("wing"), a.str("room"), a.intOr("limit", 50), a.intOr("offset", 0))
			}},
		{"check_duplicate", "content", "check whether content duplicates an existing drawer (arg: threshold)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.CheckDuplicate(ctx, team, a.str("content"), a.floatOr("threshold", palace.DefaultDupThreshold))
			}},
		{"get_taxonomy", "", "the team's wings→rooms taxonomy with counts",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.GetTaxonomy(ctx, team)
			}},
		{"list_wings", "", "list the team's wings with drawer counts",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.Wings(ctx, team)
			}},
		{"list_rooms", "wing", "list rooms, optionally within one wing",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.Rooms(ctx, team, a.str("wing"))
			}},
		{"get_aaak_spec", "", "the static AAAK write-dialect reference",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return map[string]any{"spec": palace.AAAKSpec}, nil
			}},
		{"list_skills", "", "list the team's centralised skills (no bodies)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.skills.List(ctx, team)
			}},
		{"load_skill", "name", "load a centralised skill body by name",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.skills.Load(ctx, team, a.str("name"))
			}},
		{"skillset", "", "the global wakeup playbook + the read-only tool catalogue",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				sk, found, err := svc.skillsets.Get(ctx)
				if err != nil {
					return nil, err
				}
				preamble, version := "", 0
				if found {
					preamble, version = sk.Content, sk.Version
				}
				// The CLI is a read-only adapter, so it advertises the read subset of
				// the surface (the full set is over MCP). Build it from this very
				// catalogue so it cannot drift from what the CLI actually exposes.
				tools := readOnlyTools()
				cat := make([]map[string]string, len(tools))
				for i, t := range tools {
					cat[i] = map[string]string{"name": "am_" + t.name, "usage": t.usage}
				}
				return map[string]any{
					"preamble":   preamble,
					"version":    version,
					"tools":      cat,
					"tool_count": len(cat),
				}, nil
			}},
		{"diary_read", "agent_name", "read an agent's diary entries (args: wing, last_n)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.ReadDiary(ctx, team, a.str("agent_name"), a.str("wing"), a.intOr("last_n", palace.DefaultDiaryReadN))
			}},
		{"list_tunnels", "wing", "list cross-wing tunnels, optionally within one wing",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.ListTunnels(ctx, team, a.str("wing"))
			}},
		{"find_tunnels", "wing_a", "find tunnels between two wings (arg: wing_b)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.FindTunnels(ctx, team, a.str("wing_a"), a.str("wing_b"))
			}},
		{"follow_tunnels", "wing", "follow tunnels out of a wing/room (arg: room)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.FollowTunnels(ctx, team, a.str("wing"), a.str("room"))
			}},
		{"list_hallways", "wing", "list within-wing hallways, optionally for one wing",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.ListHallways(ctx, team, a.str("wing"))
			}},
		{"traverse", "start_room", "traverse the graph from a start room (arg: max_hops)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.Traverse(ctx, team, a.str("start_room"), a.intOr("max_hops", 2))
			}},
		{"graph_stats", "", "graph overview: rooms, hallways, tunnels",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.GraphStats(ctx, team)
			}},
		{"kg_query", "entity", "query the knowledge graph for an entity's facts (args: as_of, direction)",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				facts, ent, err := svc.drawers.KGQuery(ctx, team, a.str("entity"), a.str("as_of"), a.strOr("direction", "both"))
				if err != nil {
					return nil, err
				}
				return map[string]any{"entity": ent, "facts": facts}, nil
			}},
		{"kg_stats", "", "knowledge-graph overview: entities, triples, types",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.KGStats(ctx, team)
			}},
		{"kg_timeline", "entity", "an entity's facts in temporal order",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				facts, label, err := svc.drawers.KGTimeline(ctx, team, a.str("entity"))
				if err != nil {
					return nil, err
				}
				return map[string]any{"entity": label, "facts": facts}, nil
			}},
		{"memories_filed_away", "", "summary of what the team has stored",
			func(ctx context.Context, svc *services, team string, a cliArgs) (any, error) {
				return svc.drawers.MemoriesFiledAway(ctx, team)
			}},
	}
}

// findTool looks up a tool by its bare name.
func findTool(tools []mcpTool, name string) (mcpTool, bool) {
	for _, t := range tools {
		if t.name == name {
			return t, true
		}
	}
	return mcpTool{}, false
}

// printToolList prints the read-only catalogue, sorted by name and column-
// aligned, so `agentsmemory mcp` is a usable discovery surface.
func printToolList(tools []mcpTool) {
	sorted := make([]mcpTool, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })

	width := 0
	for _, t := range sorted {
		if len(t.name) > width {
			width = len(t.name)
		}
	}

	fmt.Println("read-only memory tools — agentsmemory mcp <tool> [primary-arg] [-a key=value]")
	fmt.Println("auth: --token (or AGENTSMEMORY_TOKEN) meters like /mcp; --team <id> is a local admin read")
	fmt.Println()
	for _, t := range sorted {
		prim := ""
		if t.primary != "" {
			prim = "  [" + t.primary + "]"
		}
		fmt.Printf("  %-*s  %s%s\n", width, t.name, t.usage, prim)
	}
}
