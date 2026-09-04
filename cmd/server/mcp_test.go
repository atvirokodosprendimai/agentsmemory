package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

func TestProductionMCPConstructionHasOneChokepoint(t *testing.T) {
	type calls struct {
		constructor int
		shared      int
	}
	byFunction := map[string]calls{}
	packages, err := parser.ParseDir(token.NewFileSet(), ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse cmd/server: %v", err)
	}
	for _, file := range packages["main"].Files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			found := byFunction[function.Name.Name]
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch target := call.Fun.(type) {
				case *ast.SelectorExpr:
					if pkg, ok := target.X.(*ast.Ident); ok && pkg.Name == "mcpserver" && target.Sel.Name == "Compose" {
						found.constructor++
					}
				case *ast.Ident:
					if target.Name == "productionMCPServer" {
						found.shared++
					}
				}
				return true
			})
			byFunction[function.Name.Name] = found
		}
	}

	constructors := 0
	for name, found := range byFunction {
		constructors += found.constructor
		if found.constructor > 0 && name != "productionMCPServer" {
			t.Errorf("%s calls mcpserver.Compose directly; HTTP and CLI must share productionMCPServer", name)
		}
	}
	if constructors != 1 {
		t.Errorf("mcpserver.Compose call sites = %d, want one production composition chokepoint", constructors)
	}
	for _, name := range []string{"run", "runMCP"} {
		if byFunction[name].shared == 0 {
			t.Errorf("%s does not call productionMCPServer", name)
		}
	}
}

func directMCPConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.DBPath = t.TempDir() + "/agentsmemory.db"
	cfg.VectorBackend = config.VectorBackendSQLite
	return cfg
}

func runDirectMCP(t *testing.T, cfg config.Config, args ...string) (string, error) {
	t.Helper()
	cmd := mcpCommand(cfg)
	var out bytes.Buffer
	cmd.Writer = &out
	cmd.ErrWriter = &out
	returnOutput := append([]string{"mcp"}, args...)
	err := cmd.Run(t.Context(), returnOutput)
	return out.String(), err
}

func TestDirectCLIAdvertisesRawMode(t *testing.T) {
	out, _ := runDirectMCP(t, config.Default(), "--help")
	if !strings.Contains(out, "--raw") {
		t.Fatalf("direct CLI help does not offer --raw, but PrintTools advertises it:\n%s", out)
	}
}

func seedDirectDrawers(t *testing.T, cfg config.Config, teamID string) *services {
	t.Helper()
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatalf("build services: %v", err)
	}
	sqlDB, err := svc.gdb.DB()
	if err != nil {
		t.Fatalf("SQL handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	drawers := []palace.Drawer{
		{ID: "alpha-id", TeamID: teamID, Wing: "wing_alpha", Room: "decisions", SourceFile: "alpha.md", Content: "alpha", FiledAt: "2026-08-24T08:00:00Z"},
		{ID: "beta-id", TeamID: teamID, Wing: "wing_beta", Room: "decisions", SourceFile: "beta.md", Content: "beta", FiledAt: "2026-08-24T08:01:00Z"},
	}
	if err := palace.NewRepo(svc.gdb, svc.gdb).Save(t.Context(), drawers); err != nil {
		t.Fatalf("seed drawers: %v", err)
	}
	return svc
}

func TestDirectCLIReadSurfaceComesFromLiveAnnotations(t *testing.T) {
	t.Setenv("AGENTSMEMORY_TOKEN", "")
	cfg := directMCPConfig(t)
	definitions, err := listMCPTools(t.Context(), productionMCPServer(nil, cfg, true))
	if err != nil {
		t.Fatal(err)
	}

	// A destructive verb used to stand here: delete_wing, checked for being present
	// on the local surface and NOT annotated read-only. ADR-038 removed it, so the
	// same property is checked on the verb that replaced it — a write must never be
	// advertised as a read, or a client that trusts the annotation calls it freely.
	invalidate, ok := mcpcli.FindTool(definitions, "invalidate_drawer")
	if !ok {
		t.Fatal("local production surface does not expose am_invalidate_drawer")
	}
	if mcpcli.IsReadOnly(invalidate) {
		t.Fatal("am_invalidate_drawer is classified read-only; it ends a memory")
	}
	if _, gone := mcpcli.FindTool(definitions, "delete_wing"); gone {
		t.Error("am_delete_wing is back on the local production surface — local is where the " +
			"operator boundary is absent, which is when an agent's mistake is unrecoverable")
	}

	out, err := runDirectMCP(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"recall_stats", "list_anchors"} {
		if !strings.Contains(out, name) {
			t.Errorf("live CLI catalogue omits %s:\n%s", name, out)
		}
		called, err := runDirectMCP(t, cfg, name, "--team", "team-a")
		if err != nil {
			t.Errorf("live read tool %s is listed but not callable: %v\n%s", name, err, called)
		}
	}
	if strings.Contains(out, "invalidate_drawer") {
		t.Errorf("read-only CLI catalogue includes invalidate_drawer:\n%s", out)
	}

	_, err = runDirectMCP(t, cfg, "invalidate_drawer", "--team", "team-a")
	if err == nil || !strings.Contains(err.Error(), "writes to the palace") {
		t.Fatalf("invalidate_drawer error = %v, want a write refusal from the live annotation", err)
	}
}

func TestDirectCLIWingSemanticsUseProductionResolver(t *testing.T) {
	t.Setenv("AGENTSMEMORY_TOKEN", "")
	const teamID = "team-wing"
	cfg := directMCPConfig(t)
	seedDirectDrawers(t, cfg, teamID)

	wings := func(t *testing.T, cfg config.Config, args ...string) []string {
		t.Helper()
		out, err := runDirectMCP(t, cfg, args...)
		if err != nil {
			t.Fatalf("run direct MCP: %v\n%s", err, out)
		}
		var payload struct {
			Drawers []struct {
				Wing string `json:"wing"`
			} `json:"drawers"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("decode list_drawers: %v\n%s", err, out)
		}
		got := make([]string, 0, len(payload.Drawers))
		for _, drawer := range payload.Drawers {
			got = append(got, drawer.Wing)
		}
		sort.Strings(got)
		return got
	}
	assertWings := func(t *testing.T, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("wings = %v, want %v", got, want)
		}
	}

	t.Run("registration default", func(t *testing.T) {
		got := wings(t, cfg, "list_drawers", "--team", teamID, "--wing", "wing_alpha")
		assertWings(t, got, "wing_alpha")
	})
	t.Run("explicit argument wins", func(t *testing.T) {
		got := wings(t, cfg, "list_drawers", "--team", teamID, "--wing", "wing_alpha", "-a", "wing=wing_beta")
		assertWings(t, got, "wing_beta")
	})
	t.Run("star is cross-wing, not a literal filter", func(t *testing.T) {
		got := wings(t, cfg, "list_drawers", "--team", teamID, "--wing", "wing_alpha", "-a", "wing=*")
		assertWings(t, got, "wing_alpha", "wing_beta")
	})
	t.Run("star registration default is cross-wing", func(t *testing.T) {
		got := wings(t, cfg, "list_drawers", "--team", teamID, "--wing", "*")
		assertWings(t, got, "wing_alpha", "wing_beta")
	})
	t.Run("workspace scope can widen an omitted argument", func(t *testing.T) {
		workspace := cfg
		workspace.SearchScope = "workspace"
		got := wings(t, workspace, "list_drawers", "--team", teamID, "--wing", "wing_alpha")
		assertWings(t, got, "wing_alpha", "wing_beta")
	})
}

func TestDirectCLIUsesProductionProjectionAndTypedArguments(t *testing.T) {
	t.Setenv("AGENTSMEMORY_TOKEN", "")
	const teamID = "team-projection"
	cfg := directMCPConfig(t)
	seedDirectDrawers(t, cfg, teamID)

	whole, err := runDirectMCP(t, cfg, "get_drawer", "alpha-id", "--team", teamID, "-a", "whole=true")
	if err != nil {
		t.Fatalf("get whole drawer: %v\n%s", err, whole)
	}
	var memory struct {
		Count  int `json:"count"`
		Chunks []struct {
			ID string `json:"id"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal([]byte(whole), &memory); err != nil {
		t.Fatalf("decode whole drawer: %v\n%s", err, whole)
	}
	if memory.Count != 1 || len(memory.Chunks) != 1 || memory.Chunks[0].ID != "alpha-id" {
		t.Fatalf("whole projection = %#v, want one canonical chunks result", memory)
	}

	limited, err := runDirectMCP(t, cfg, "list_drawers", "--team", teamID, "-a", "wing=*", "-a", "limit=1")
	if err != nil {
		t.Fatalf("limited list: %v\n%s", err, limited)
	}
	var page struct {
		Count   int   `json:"count"`
		Drawers []any `json:"drawers"`
	}
	if err := json.Unmarshal([]byte(limited), &page); err != nil {
		t.Fatalf("decode limited list: %v\n%s", err, limited)
	}
	if page.Count != 1 || len(page.Drawers) != 1 {
		t.Fatalf("limited projection = %#v, want exactly one drawer from numeric limit=1", page)
	}
}

func TestDirectCLIAdmissionModes(t *testing.T) {
	t.Setenv("AGENTSMEMORY_TOKEN", "")
	cfg := directMCPConfig(t)
	svc, err := buildServices(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := svc.gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	resolved, credential, err := svc.tenants.SeedTeamWithKey(t.Context(), "CLI Test", "cli-test", "cli@example.test")
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	tokenOut, err := runDirectMCP(t, cfg, "status", "--token", credential.Secret)
	if err != nil {
		t.Fatalf("token status: %v\n%s", err, tokenOut)
	}
	var tokenStatus struct {
		Role  string `json:"role"`
		Mode  string `json:"mode"`
		Usage struct {
			Used int `json:"used_this_month"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(tokenOut), &tokenStatus); err != nil {
		t.Fatalf("decode token status: %v\n%s", err, tokenOut)
	}
	if tokenStatus.Role != "admin" || tokenStatus.Mode != "local" || tokenStatus.Usage.Used != 1 {
		t.Fatalf("token status = %#v, want admin/local and exactly one metered call", tokenStatus)
	}
	before, err := svc.usage.Snapshot(t.Context(), resolved.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Used != 1 {
		t.Fatalf("token usage = %d, want 1 (the production handler must meter once, not twice)", before.Used)
	}

	teamOut, err := runDirectMCP(t, cfg, "status", "--team", resolved.TeamID)
	if err != nil {
		t.Fatalf("team status: %v\n%s", err, teamOut)
	}
	var teamStatus struct {
		Role string `json:"role"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(teamOut), &teamStatus); err != nil {
		t.Fatalf("decode team status: %v\n%s", err, teamOut)
	}
	if teamStatus.Role != "admin" || teamStatus.Mode != "local" {
		t.Fatalf("team status = %#v, want trusted admin/local identity", teamStatus)
	}
	after, err := svc.usage.Snapshot(t.Context(), resolved.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Used != before.Used {
		t.Fatalf("--team changed usage from %d to %d; trusted local reads must stay unmetered", before.Used, after.Used)
	}

	t.Setenv("AGENTSMEMORY_TOKEN", credential.Secret)
	envTeamOut, err := runDirectMCP(t, cfg, "status", "--team", resolved.TeamID)
	if err != nil {
		t.Fatalf("--team with AGENTSMEMORY_TOKEN in the environment: %v\n%s", err, envTeamOut)
	}
	var envTeamStatus struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(envTeamOut), &envTeamStatus); err != nil {
		t.Fatalf("decode env+team status: %v\n%s", err, envTeamOut)
	}
	if envTeamStatus.Mode != "local" {
		t.Fatalf("env+team status mode = %q, want local; the client-kit token env must not collide with --team", envTeamStatus.Mode)
	}
	still, err := svc.usage.Snapshot(t.Context(), resolved.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Used != after.Used {
		t.Fatalf("AGENTSMEMORY_TOKEN plus --team metered the call (%d → %d); --team must stay the unmetered local operator", after.Used, still.Used)
	}
}
