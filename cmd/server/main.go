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
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/embed/ollama"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"
	"github.com/atvirokodosprendimai/agentsmemory/internal/oauth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web"

	"github.com/glebarez/sqlite"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/server"
	"github.com/pressly/goose/v3"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	// Load a .env file if present (best effort) so secrets and config can live
	// in a local file during development; real env vars still take precedence.
	_ = godotenv.Load()

	def := config.Default()

	// urfave/cli v3 models the program as a Command; flags default to the
	// config defaults so a bare `server` works on a local dev box.
	cmd := &cli.Command{
		Name:  "agentsmemory",
		Usage: "Remote MCP memory server (Qdrant + Ollama, multi-tenant)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "addr", Sources: cli.EnvVars("AGENTSMEMORY_ADDR"), Value: def.Addr, Usage: "HTTP listen address"},
			&cli.StringFlag{Name: "db", Sources: cli.EnvVars("AGENTSMEMORY_DB"), Value: def.DBPath, Usage: "SQLite database path"},
			&cli.StringFlag{Name: "vector-backend", Sources: cli.EnvVars("VECTOR_BACKEND"), Value: def.VectorBackend, Usage: "search index: sqlite|qdrant (SQLite is always the source of truth)"},
			&cli.StringFlag{Name: "qdrant-url", Sources: cli.EnvVars("QDRANT_URL"), Value: def.QdrantURL, Usage: "Qdrant base URL"},
			&cli.StringFlag{Name: "qdrant-api-key", Sources: cli.EnvVars("QDRANT_API_KEY"), Value: def.QdrantAPIKey, Usage: "Qdrant API key (optional)"},
			&cli.StringFlag{Name: "ollama-url", Sources: cli.EnvVars("OLLAMA_URL"), Value: def.OllamaURL, Usage: "Ollama base URL"},
			&cli.StringFlag{Name: "ollama-model", Sources: cli.EnvVars("OLLAMA_EMBED_MODEL"), Value: def.OllamaEmbedModel, Usage: "Ollama embedding model"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			cfg := config.Config{
				Addr:             c.String("addr"),
				DBPath:           c.String("db"),
				VectorBackend:    c.String("vector-backend"),
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

	// Bounded contexts: tenant (auth + workspaces), skill (load_skill), and
	// usage (monthly request metering).
	tenants := tenant.NewRepo(gdb)
	skills := skill.NewService(skill.NewRepo(gdb))
	usageSvc := usage.NewService(usage.NewRepo(gdb), tenants)

	// Vector storage: SQLite is always the source of truth; cfg.VectorBackend
	// selects whether it also serves search or Qdrant indexes it. The embedder
	// (Ollama) is wired in a later phase against this same seam.
	vectors, err := buildVectorStore(cfg, gdb)
	if err != nil {
		return fmt.Errorf("vector store: %w", err)
	}
	log.Printf("vector backend: %s (SQLite source of truth)", cfg.VectorBackend)

	// The memory loop: Ollama embeds text, the store seam holds the vectors, and
	// the palace service ties them to drawer metadata. The MCP drawer tools call
	// into this service, tenant-scoped per request.
	embedder := ollama.New(cfg.OllamaURL, cfg.OllamaEmbedModel, cfg.HTTPTimeout)
	drawers := palace.NewService(palace.NewRepo(gdb), embedder, vectors, defaultVectorDim)

	if err := seedIfEmpty(ctx, gdb, tenants, skill.NewRepo(gdb), vectors); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	// The MCP server, exposed over Streamable HTTP. The HTTP context func runs
	// per request, turning the Bearer token into a tenant on the context the
	// tools read — this is the only place auth touches the transport. Tools
	// meter each call against the workspace's monthly cap via usageSvc.
	mcpSrv := mcpserver.New(mcpserver.Deps{Skills: skills, Usage: usageSvc, Drawers: drawers})

	// OAuth 2.1 authorization server (stateless), validating client credentials
	// against our own api_keys (the merged authcounterapi role). It guards /mcp
	// and serves the discovery + token endpoints claude.ai's remote connector
	// needs. tenants satisfies both the client validator and the raw-token
	// resolver, so OAuth bearers and direct API tokens share one /mcp.
	sealer, err := oauth.NewSealer(oauthSecret())
	if err != nil {
		return fmt.Errorf("oauth sealer: %w", err)
	}
	issuer := oauthIssuer(cfg.Addr)
	authSrv := oauth.NewAuthServer(issuer, sealer, tenants, tenants)

	streamSrv := server.NewStreamableHTTPServer(
		mcpSrv,
		// The OAuth gate resolves the bearer and puts the tenant on the request
		// context; Bridge forwards it into the per-tool context.
		server.WithHTTPContextFunc(auth.Bridge),
		// Stateless: no server-side session map. Suits a multi-tenant remote
		// server and lets it scale horizontally behind a load balancer.
		server.WithStateLess(true),
	)

	// The human-facing dashboard (register/login/create project) shares the same
	// chi router and database; agents use /mcp, people use the web routes.
	webSrv := web.New(tenants, usageSvc, sessionKey())

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// OAuth discovery + endpoints for the claude.ai remote-connector handshake.
	r.Get("/.well-known/oauth-protected-resource", authSrv.ProtectedResourceMetadata)
	r.Get("/.well-known/oauth-authorization-server", authSrv.AuthorizationServerMetadata)
	r.Get("/authorize", authSrv.Authorize)
	r.Post("/token", authSrv.Token)

	// MCP endpoint, fronted by the OAuth gate: it 401-challenges unauthenticated
	// requests (so the connector starts OAuth) and lets resolved bearers (OAuth
	// or direct API token) through to the stateless MCP handler.
	r.Handle("/mcp", authSrv.Gate(streamSrv))

	// Dashboard + auth + static assets.
	webSrv.Routes(r)

	log.Printf("agentsmemory listening on %s (dashboard /, MCP /mcp, OAuth issuer %s)", cfg.Addr, issuer)
	return http.ListenAndServe(cfg.Addr, r)
}

// oauthSecret returns the key that seals OAuth tokens. OAUTH_SECRET_KEY keeps
// tokens valid across restarts; absent it, a random key is used (and every
// previously issued OAuth token becomes invalid on restart).
func oauthSecret() string {
	if s := os.Getenv("OAUTH_SECRET_KEY"); s != "" {
		return s
	}
	log.Printf("warning: OAUTH_SECRET_KEY unset; using a random key (OAuth tokens reset on restart)")
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("entropy failure generating OAuth secret: %v", err)
	}
	return hex.EncodeToString(buf)
}

// oauthIssuer is the public base URL advertised in OAuth metadata. In production
// set OAUTH_ISSUER to the external https URL (no trailing slash, no /mcp); for
// local dev it is derived from the listen address.
func oauthIssuer(addr string) string {
	if v := os.Getenv("OAUTH_ISSUER"); v != "" {
		return strings.TrimRight(v, "/")
	}
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}
	return "http://" + host
}

// sessionKey returns the cookie signing key. AGENTSMEMORY_SESSION_KEY (hex) keeps
// sessions valid across restarts in production; absent it, a random key is used
// and a warning is logged (dev convenience — sessions reset on restart).
func sessionKey() []byte {
	if hexKey := os.Getenv("AGENTSMEMORY_SESSION_KEY"); hexKey != "" {
		if raw, err := hex.DecodeString(hexKey); err == nil && len(raw) >= 32 {
			return raw
		}
		log.Printf("warning: AGENTSMEMORY_SESSION_KEY is not valid hex of >=32 bytes; using a random key")
	} else {
		log.Printf("warning: AGENTSMEMORY_SESSION_KEY unset; using a random session key (sessions reset on restart)")
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("entropy failure generating session key: %v", err)
	}
	return buf
}

// defaultVectorDim is the embedding dimension new vector namespaces are created
// with — bge-m3 (the default Ollama model) produces 1024-d vectors, matching the
// Qdrant collection size in internal/store/qdrant.
const defaultVectorDim = 1024

// buildVectorStore assembles the vector layer from cfg. SQLite is always the
// durable source of truth (sqlitevec); cfg.VectorBackend then decides whether it
// also serves search or whether Qdrant is layered on as the search index via
// store.Hybrid. This switch is the single swap point for the search backend.
func buildVectorStore(cfg config.Config, gdb *gorm.DB) (store.VectorStore, error) {
	sot := sqlitevec.New(gdb)
	switch cfg.VectorBackend {
	case config.VectorBackendSQLite:
		return sot, nil
	case config.VectorBackendQdrant:
		index := qdrant.New(cfg.QdrantURL, cfg.QdrantAPIKey, cfg.HTTPTimeout)
		return store.NewHybrid(sot, index), nil
	default:
		return nil, fmt.Errorf("unknown vector backend %q (want %q or %q)",
			cfg.VectorBackend, config.VectorBackendSQLite, config.VectorBackendQdrant)
	}
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
func seedIfEmpty(ctx context.Context, gdb *gorm.DB, tenants *tenant.Repo, skills *skill.Repo, vectors store.VectorStore) error {
	var teamCount int64
	if err := gdb.WithContext(ctx).Model(&tenant.Team{}).Count(&teamCount).Error; err != nil {
		return err
	}
	if teamCount > 0 {
		return nil
	}

	t, cred, err := tenants.SeedTeamWithKey(ctx, "Demo Team", "demo", "owner@demo.local")
	if err != nil {
		return err
	}
	// Ready the demo workspace's vector namespace so its first write/search has
	// somewhere to land — a no-op for the SQLite backend, a collection create
	// for Qdrant.
	if err := vectors.EnsureNamespace(ctx, t.TeamID, defaultVectorDim); err != nil {
		return fmt.Errorf("ensure demo vector namespace: %w", err)
	}
	if _, err := skills.Upsert(ctx, t.TeamID, "hello",
		"A starter skill proving load_skill works.",
		"# Hello Skill\n\nThis is a centralised, team-shared skill served by agentsmemory.\n",
		t.UserID); err != nil {
		return err
	}

	log.Printf("seeded demo team %s", t.TeamID)
	log.Printf("OAuth client_id (shown once): %s", cred.ClientKey)
	log.Printf("MCP bearer token / secret (shown once): %s", cred.Secret)
	log.Printf("try: curl -H 'Authorization: Bearer %s' ... http://%s/mcp", cred.Secret, "<addr>")
	return nil
}
