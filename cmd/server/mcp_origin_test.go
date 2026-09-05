package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestTheCLIPathSetsTheOriginFromTheEnvironment is ADR-054 T1's in-process half:
// the server's own `mcp` subcommand never goes through HTTP, so auth.Bridge
// never sees it; it sets the same context value from AGENTSMEMORY_ORIGIN,
// beside the wing. The shipped hooks do NOT use this path — they call the kit's
// `aiagentmemory mcp`, which speaks HTTP (T2) — so this pins the operator's
// direct route, not the hooks'.
func TestTheCLIPathSetsTheOriginFromTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DBPath = filepath.Join(dir, "palace.db")
	t.Setenv(mcpprotocol.OriginEnvVar, "hook:t")
	t.Setenv("AGENTSMEMORY_DB", cfg.DBPath)

	// HERMETIC: a search embeds its query, and `mcp` builds the production
	// embedder from --ollama-url. The first version of this test passed only on
	// a machine with Ollama on 11434 and failed in CI (review of #250); a
	// fake speaking Ollama's /api/embed answers a fixed vector instead, so the
	// row this test reads exists whatever is listening on the host.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3,0.4]]}`))
	}))
	t.Cleanup(fake.Close)
	t.Setenv("OLLAMA_URL", fake.URL)
	cfg.OllamaURL = fake.URL

	// `--team` names a trusted local identity and creates no teams row, and
	// search_events.team_id REFERENCES teams(id) — so on a fresh file the row's
	// insert fails the foreign key and recordSearch swallows it (measured while
	// writing this test). The team is seeded the way the mcptest harness seeds
	// its own, so the row this test reads can exist at all.
	seed, err := buildServices(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.gdb.Create(&tenant.Team{ID: "t-origin", Name: "t-origin", Slug: "t-origin", Kind: "personal", CreatedAt: "2026-01-01T00:00:00Z"}).Error; err != nil {
		t.Fatal(err)
	}
	seed.Close()

	var out bytes.Buffer
	root := rootCommand(config.Default())
	root.Writer = &out
	if err := root.Run(context.Background(), []string{
		"agentsmemory", "mcp", "--db", cfg.DBPath, "--ollama-url", fake.URL, "--team", "t-origin", "search", "-a", "query=anything at all", "-a", "wing=wing_acme",
	}); err != nil {
		t.Fatalf("mcp search: %v\n%s", err, out.String())
	}

	svc, err := buildServicesWith(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	var n int64
	if err := svc.gdb.Raw("SELECT COUNT(*) FROM search_events").Scan(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n == 0 {
		t.Fatalf("`mcp search` recorded no search_events row at all, so there is nothing to carry an origin:\n%s", out.String())
	}
	var origin string
	if err := svc.gdb.Raw("SELECT origin FROM search_events ORDER BY created_at DESC LIMIT 1").Scan(&origin).Error; err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if origin != "hook:t" {
		t.Fatalf("search_events.origin = %q after `mcp search` with %s=hook:t; the CLI path does not set the origin", origin, mcpprotocol.OriginEnvVar)
	}
}
