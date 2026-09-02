// mcp.go implements the `mcp` subcommand: a read-only in-process client of the
// production MCP server. Operators and scripts can inspect memory from the
// shell without an HTTP round-trip — e.g. `agentsmemory mcp search "auth bug"`
// — while exercising the exact handlers, schemas, projections, wing rules, and
// admission code remote agents use.
//
// Read-only is derived from each live tools/list entry. A missing or false
// readOnlyHint fails closed, so this adapter has no parallel tool registry to
// drift. --token resolves a real tenant and meters in the production handler;
// --team is trusted local-operator access to the operator's own database and is
// deliberately unmetered.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcptransport "github.com/mark3labs/mcp-go/server"
	"github.com/urfave/cli/v3"
)

// mcpCommand builds the `mcp` subcommand. It reuses dataFlags (the storage/embed
// flags) so it opens the same database as serve, and adds the auth selectors
// (--token / --team) and the repeatable -a/--arg tool-argument flag.
//
// It also takes meteringFlags, and unlike every other dataFlags command it earns
// them: --token here resolves a tenant and METERS the call for HTTP parity, so
// the cap override changes what this command does. That is the test for offering
// the flag at all — parsing it into a Config field is not having an effect.
func mcpCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:      "mcp",
		Usage:     "Invoke a memory tool from the CLI (run with no tool to list them; writes need --write)",
		ArgsUsage: "[tool] [primary-arg|file.md]",
		Flags: append(append(dataFlags(def), meteringFlags(def)...),
			&cli.StringFlag{Name: "token", Usage: "API key: resolves the tenant and meters the call (HTTP parity). AGENTSMEMORY_TOKEN is used only when neither --token nor --team is set"},
			&cli.StringFlag{Name: "team", Usage: "team id: trusted local admin read, no metering (alternative to --token)"},
			&cli.StringSliceFlag{Name: "arg", Aliases: []string{"a"}, Usage: "tool argument as key=value (repeatable)"},
			&cli.StringFlag{Name: "wing", Usage: "default wing for this call, like a per-project MCP registration; explicit -a wing= wins and \"*\" searches every wing"},
			&cli.BoolFlag{Name: "raw", Usage: "print the whole MCP envelope (content blocks, isError) instead of just the result"},
			&cli.BoolFlag{Name: "write", Usage: "allow a tool that writes to the palace (refused without this)"},
			&cli.BoolFlag{Name: "schema", Usage: "describe the tool's arguments and print a markdown template, instead of calling it"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return runMCP(ctx, c, def)
		},
	}
}

// runMCP performs one direct CLI invocation through the production MCP server.
// Listing the catalogue is registration-only and does not open the database;
// an actual call wires services, authenticates, and invokes the live handler.
func runMCP(ctx context.Context, c *cli.Command, def config.Config) error {
	cfg := configFromCmd(c, def)
	// Direct CLI always talks to this process's palace. --token meters a
	// tenant against the local SQLite file; it does not make the process a
	// hosted deployment. AGENTS.md uses mode to prove which palace opened.
	const local = true
	endpoint := mcpcli.Endpoint{
		ListTools: func(callCtx context.Context) ([]mcp.Tool, error) {
			return listMCPTools(callCtx, productionMCPServer(nil, cfg, local))
		},
		CallTool: func(callCtx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			svc, err := buildServices(cfg)
			if err != nil {
				return nil, err
			}
			sqlDB, err := svc.gdb.DB()
			if err != nil {
				return nil, fmt.Errorf("open SQL handle: %w", err)
			}
			defer sqlDB.Close()

			resolved, unmetered, err := resolveTenant(callCtx, svc, c)
			if err != nil {
				return nil, err
			}
			callCtx = auth.WithTenant(callCtx, resolved)
			if wing := c.String("wing"); wing != "" {
				callCtx = auth.WithDefaultWing(callCtx, wing)
			}
			if unmetered {
				callCtx = mcpserver.WithUnmeteredLocalOperator(callCtx)
			}

			session, err := newInProcessMCPClient(callCtx, productionMCPServer(svc, cfg, local))
			if err != nil {
				return nil, err
			}
			defer session.Close()
			return session.CallTool(callCtx, req)
		},
	}
	return mcpcli.Run(ctx, c.Writer, endpoint, mcpcli.Invocation{
		Tool:        c.Args().First(),
		ArgFlags:    c.StringSlice("arg"),
		Tail:        mcpcli.TailArgs(c.Args().Slice()),
		Raw:         c.Bool("raw"),
		AllowWrites: c.Bool("write"),
		Schema:      c.Bool("schema"),
		Log:         os.Stderr,
	})
}

// resolveTenant picks the tenant the call acts as. --token resolves the full
// production identity and role; --team constructs a local admin identity. The
// boolean reports whether production admission should skip hosted metering.
func resolveTenant(ctx context.Context, svc *services, c *cli.Command) (tenant.Tenant, bool, error) {
	token, team := c.String("token"), c.String("team")
	if token == "" && team == "" {
		// Client-kit docs tell operators to export AGENTSMEMORY_TOKEN. Binding
		// that env onto --token made `mcp status --team …` a hard error in any
		// shell that already had the variable. Env is the fallback when neither
		// identity flag is set, not a second identity that collides with --team.
		token = os.Getenv(mcpprotocol.TokenEnvVar)
	}
	if token != "" && team != "" {
		return tenant.Tenant{}, false, errors.New("provide exactly one of --token and --team, not both")
	}
	if token != "" {
		t, err := svc.tenants.ResolveToken(ctx, token)
		if err != nil {
			return tenant.Tenant{}, false, fmt.Errorf("resolve --token: %w", err)
		}
		return t, false, nil
	}
	if team != "" {
		return tenant.Tenant{TeamID: team, Role: tenant.RoleAdmin}, true, nil
	}
	return tenant.Tenant{}, false, errors.New("provide --token (or AGENTSMEMORY_TOKEN) for a metered, auth-parity read, or --team <id> for a trusted local admin read")
}

// newInProcessMCPClient starts and initializes an MCP client against srv. This
// is a protocol client, not a handler lookup: calls still cross the mcp-go
// dispatch boundary and consume the same tool definitions as HTTP clients.
func newInProcessMCPClient(ctx context.Context, srv *mcptransport.MCPServer) (*client.Client, error) {
	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		return nil, fmt.Errorf("create in-process MCP client: %w", err)
	}
	if err := cli.Start(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("start in-process MCP client: %w", err)
	}
	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("initialize in-process MCP client: %w", err)
	}
	return cli, nil
}

// listMCPTools returns the definitions a protocol client receives from
// tools/list, rather than a package-private registration slice.
func listMCPTools(ctx context.Context, srv *mcptransport.MCPServer) ([]mcp.Tool, error) {
	cli, err := newInProcessMCPClient(ctx, srv)
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	res, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list MCP tools: %w", err)
	}
	return res.Tools, nil
}
