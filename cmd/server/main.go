// Command server is the agentsmemory remote MCP server. It migrates its SQLite
// schema, wires the tenant/skill/store/embed collaborators, and serves the MCP
// tools over Streamable HTTP so a team's agents can connect with a Bearer token.
//
// This is the day-one skeleton: it boots, migrates, seeds a demo team on first
// run, and answers the status and load_skill tools. Mining and hybrid search
// land in later phases against the same wiring.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/glebarez/sqlite"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mark3labs/mcp-go/server"
	"github.com/pressly/goose/v3"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	def := config.Default()

	// urfave/cli v3 models the program as a Command; flags default to the
	// config defaults so a bare `server` works on a local dev box.
	cmd := &cli.Command{
		Name:  "agentsmemory",
		Usage: "Remote MCP memory server (Qdrant + Ollama, multi-tenant)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "addr", Value: def.Addr, Usage: "HTTP listen address"},
			&cli.StringFlag{Name: "db", Value: def.DBPath, Usage: "SQLite database path"},
			&cli.StringFlag{Name: "qdrant-url", Value: def.QdrantURL, Usage: "Qdrant base URL"},
			&cli.StringFlag{Name: "qdrant-api-key", Value: def.QdrantAPIKey, Usage: "Qdrant API key (optional)"},
			&cli.StringFlag{Name: "ollama-url", Value: def.OllamaURL, Usage: "Ollama base URL"},
			&cli.StringFlag{Name: "ollama-model", Value: def.OllamaEmbedModel, Usage: "Ollama embedding model"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg := config.Config{
				Addr:             c.String("addr"),
				DBPath:           c.String("db"),
				QdrantURL:        c.String("qdrant-url"),
				QdrantAPIKey:     c.String("qdrant-api-key"),
				OllamaURL:        c.String("ollama-url"),
				OllamaEmbedModel: c.String("ollama-model"),
				HTTPTimeout:      def.HTTPTimeout,
			}
			return run(ctx, cfg)
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// run opens the database, migrates, wires dependencies, and serves until error.
func run(ctx context.Context, cfg config.Config) error {
	gdb, err := openDB(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("sql handle: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Bounded contexts: tenant (auth) and skill (load_skill). The memory palace
	// and its Qdrant/Ollama clients are wired in later phases.
	tenants := tenant.NewRepo(gdb)
	skills := skill.NewService(skill.NewRepo(gdb))

	if err := seedIfEmpty(ctx, gdb, tenants, skill.NewRepo(gdb)); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	// The MCP server, exposed over Streamable HTTP. The HTTP context func runs
	// per request, turning the Bearer token into a tenant on the context the
	// tools read — this is the only place auth touches the transport.
	mcpSrv := mcpserver.New(mcpserver.Deps{Skills: skills})
	streamSrv := server.NewStreamableHTTPServer(
		mcpSrv,
		server.WithHTTPContextFunc(auth.HTTPContextFunc(tenants)),
		// Stateless: no server-side session map. Every POST re-resolves its
		// tenant from the Bearer token, which suits a multi-tenant remote
		// server and lets it scale horizontally behind a load balancer.
		server.WithStateLess(true),
	)

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Mount the MCP endpoint. The streamable server's default endpoint path is
	// "/mcp", which is exactly what chi routes here.
	r.Handle("/mcp", streamSrv)

	log.Printf("agentsmemory listening on %s (MCP at /mcp)", cfg.Addr)
	return http.ListenAndServe(cfg.Addr, r)
}

// openDB opens a pure-Go (no cgo) SQLite database through gorm's glebarez
// driver. gorm is the query layer; goose owns the schema, so AutoMigrate is
// never called. The logger is silenced because expected "record not found"
// lookups (e.g. the create branch of an upsert) are control flow, not errors —
// real failures still surface through returned error values.
func openDB(path string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

// migrate applies the embedded goose migrations to the open database.
func migrate(sqlDB *sql.DB) error {
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(sqlDB, "migrations")
}

// seedIfEmpty creates a demo team, owner, API key, and one example skill on a
// brand-new database, printing the one-time token so the operator can connect an
// agent immediately. On an already-seeded database it is a no-op.
func seedIfEmpty(ctx context.Context, gdb *gorm.DB, tenants *tenant.Repo, skills *skill.Repo) error {
	var teamCount int64
	if err := gdb.WithContext(ctx).Model(&tenant.Team{}).Count(&teamCount).Error; err != nil {
		return err
	}
	if teamCount > 0 {
		return nil
	}

	t, token, err := tenants.SeedTeamWithKey(ctx, "Demo Team", "demo", "owner@demo.local")
	if err != nil {
		return err
	}
	if _, err := skills.Upsert(ctx, t.TeamID, "hello",
		"A starter skill proving load_skill works.",
		"# Hello Skill\n\nThis is a centralised, team-shared skill served by agentsmemory.\n",
		t.UserID); err != nil {
		return err
	}

	log.Printf("seeded demo team %s", t.TeamID)
	log.Printf("MCP bearer token (shown once): %s", token)
	log.Printf("try: curl -H 'Authorization: Bearer %s' ... http://%s/mcp", token, "<addr>")
	return nil
}
