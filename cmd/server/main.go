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
	"github.com/atvirokodosprendimai/agentsmemory/internal/importer"
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

	// serveAction boots the HTTP server. It backs both the root command (so a
	// bare `agentsmemory`, and the Docker image, keep serving) and the explicit
	// `serve` subcommand — one behaviour, two entry points.
	serveAction := func(ctx context.Context, c *cli.Command) error {
		return run(ctx, configFromCmd(c, def))
	}

	// urfave/cli v3 models the program as a Command. The root keeps the serve
	// flags + action for backward compatibility; subcommands add an explicit
	// `serve` and the read-only `mcp` CLI. Flag builders return fresh slices so
	// the root and the `serve` subcommand never share mutable flag state.
	cmd := &cli.Command{
		Name:   "agentsmemory",
		Usage:  "Remote MCP memory server (Qdrant + Ollama, multi-tenant)",
		Flags:  serveFlags(def),
		Action: serveAction, // no subcommand → serve (bare run + Docker CMD)
		Commands: []*cli.Command{
			{
				Name:   "serve",
				Usage:  "Run the HTTP MCP server + dashboard (the default action)",
				Flags:  serveFlags(def),
				Action: serveAction,
			},
			mcpCommand(def),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// configFromCmd reads the storage/embed flags off a (sub)command into a Config.
// The mcp subcommand omits the addr flag, so c.String("addr") yields "" there —
// harmless, because only the serve path reads Addr.
func configFromCmd(c *cli.Command, def config.Config) config.Config {
	return config.Config{
		Addr:             c.String("addr"),
		DBPath:           c.String("db"),
		VectorBackend:    c.String("vector-backend"),
		QdrantURL:        c.String("qdrant-url"),
		QdrantAPIKey:     c.String("qdrant-api-key"),
		OllamaURL:        c.String("ollama-url"),
		OllamaEmbedModel: c.String("ollama-model"),
		HTTPTimeout:      def.HTTPTimeout,
		Debug:            c.Bool("debug"),
	}
}

// dataFlags are the storage + embedding flags shared by every entry point that
// opens the database (serve and the read-only mcp CLI). It returns a fresh slice
// per call so each command owns its own flag instances.
func dataFlags(def config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "db", Sources: cli.EnvVars("AGENTSMEMORY_DB"), Value: def.DBPath, Usage: "SQLite database path"},
		&cli.StringFlag{Name: "vector-backend", Sources: cli.EnvVars("VECTOR_BACKEND"), Value: def.VectorBackend, Usage: "search index: sqlite|qdrant (SQLite is always the source of truth)"},
		&cli.StringFlag{Name: "qdrant-url", Sources: cli.EnvVars("QDRANT_URL"), Value: def.QdrantURL, Usage: "Qdrant base URL"},
		&cli.StringFlag{Name: "qdrant-api-key", Sources: cli.EnvVars("QDRANT_API_KEY"), Value: def.QdrantAPIKey, Usage: "Qdrant API key (optional)"},
		&cli.StringFlag{Name: "ollama-url", Sources: cli.EnvVars("OLLAMA_URL"), Value: def.OllamaURL, Usage: "Ollama base URL"},
		&cli.StringFlag{Name: "ollama-model", Sources: cli.EnvVars("OLLAMA_EMBED_MODEL"), Value: def.OllamaEmbedModel, Usage: "Ollama embedding model"},
		&cli.BoolFlag{Name: "debug", Sources: cli.EnvVars("APP_DEBUG"), Value: def.Debug, Usage: "verbose logging: per-request HTTP access logs + gorm SQL"},
	}
}

// serveFlags are the flags the serving entry points expose: the listen address
// plus the shared storage/embed flags.
func serveFlags(def config.Config) []cli.Flag {
	return append([]cli.Flag{
		&cli.StringFlag{Name: "addr", Sources: cli.EnvVars("AGENTSMEMORY_ADDR"), Value: def.Addr, Usage: "HTTP listen address"},
	}, dataFlags(def)...)
}

// run opens the database, migrates, wires dependencies, and serves until error.
func run(ctx context.Context, cfg config.Config) error {
	if cfg.Debug {
		// Make the "why is it silent?" answer obvious on boot: echo the effective
		// wiring so a misread flag/env is visible before any request arrives.
		log.Printf("debug mode ON — request + SQL logging enabled")
		log.Printf("config: addr=%s db=%s vector_backend=%s ollama=%s/%s",
			cfg.Addr, cfg.DBPath, cfg.VectorBackend, cfg.OllamaURL, cfg.OllamaEmbedModel)
	}

	// Open + migrate + wire the bounded-context services. The same wiring backs
	// the read-only mcp CLI, so it lives in buildServices (the one place the two
	// driving adapters share). Serving additionally seeds and starts transports.
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	log.Printf("vector backend: %s (SQLite source of truth)", cfg.VectorBackend)
	tenants, skills, usageSvc, drawers := svc.tenants, svc.skills, svc.usage, svc.drawers

	// Seeding is serve-only: the read-only CLI must never create a demo team.
	if err := seedIfEmpty(ctx, svc.gdb, tenants, skill.NewRepo(svc.gdb), svc.vectors); err != nil {
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
	webSrv := web.New(tenants, usageSvc, skills, sessionKey())

	r := chi.NewRouter()
	// Logger before Recoverer so even a panicked request (recovered as a 500) is
	// still logged. Gated on Debug: the server is intentionally silent in
	// production, and APP_DEBUG=true is what surfaces per-request access logs.
	if cfg.Debug {
		r.Use(middleware.Logger)
	}
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

	// Bulk migration ingest: a user streams their exported mempalace (NDJSON) here
	// with the same Bearer token as /mcp. The gate resolves the tenant, then the
	// importer re-files every drawer/closet/fact/tunnel under it. Fronted by the
	// same gate so auth is identical to the agent surface.
	r.Handle("/import", authSrv.Gate(importer.Handler(drawers, usageSvc)))

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

// tokenSecret returns the secret that seals API keys at rest so the dashboard can
// reveal them. AGENTSMEMORY_TOKEN_KEY keeps sealed keys revealable across
// restarts; absent it, a random per-boot key is used and a warning is logged —
// reveal still works within a run, but keys minted before a restart become
// reveal-unavailable (the seal can't be opened with the new key). An empty
// secret here disables reveal entirely (tokens stay shown-once).
func tokenSecret() string {
	if s := os.Getenv("AGENTSMEMORY_TOKEN_KEY"); s != "" {
		// The seal key is SHA-256 of this string, so a short/low-entropy value is
		// guessable offline against a leaked token_enc (GCM confirms a correct
		// guess). Warn loudly; it should be 32+ random characters (hex/base64).
		if len(s) < 32 {
			log.Printf("warning: AGENTSMEMORY_TOKEN_KEY is shorter than 32 chars; use 32+ random bytes (hex/base64) so revealed keys resist offline guessing")
		}
		return s
	}
	log.Printf("warning: AGENTSMEMORY_TOKEN_KEY unset; using a random key (revealed keys reset on restart)")
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("entropy failure generating token key: %v", err)
	}
	return hex.EncodeToString(buf)
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

// services holds the wired domain collaborators shared by the serve and mcp
// entry points: both open the same SQLite source of truth and talk to the same
// palace/skill/usage services. Extracting the wiring keeps the two driving
// adapters — the HTTP MCP server and the read-only CLI — over one domain core.
type services struct {
	gdb     *gorm.DB
	vectors store.VectorStore
	tenants *tenant.Repo
	skills  *skill.Service
	usage   *usage.Service
	drawers *palace.Service
}

// buildServices opens and migrates the database, then wires the bounded-context
// services against it. It deliberately does NOT seed (the serve path seeds; a
// read-only CLI invocation must not create data) and starts no transport, so it
// is safe to call from both entry points.
func buildServices(cfg config.Config) (*services, error) {
	gdb, err := openDB(cfg.DBPath, cfg.Debug)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("sql handle: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Bounded contexts: tenant (auth + workspaces), skill (load_skill), and
	// usage (monthly request metering).
	tenants := tenant.NewRepo(gdb, tenant.WithTokenSecret(tokenSecret()))
	skills := skill.NewService(skill.NewRepo(gdb))
	usageSvc := usage.NewService(usage.NewRepo(gdb), tenants)

	// Vector storage: SQLite is always the source of truth; cfg.VectorBackend
	// selects whether it also serves search or Qdrant indexes it.
	vectors, err := buildVectorStore(cfg, gdb)
	if err != nil {
		return nil, fmt.Errorf("vector store: %w", err)
	}

	// The memory loop: Ollama embeds text, the store seam holds the vectors, and
	// the palace service ties them to drawer metadata.
	embedder := ollama.New(cfg.OllamaURL, cfg.OllamaEmbedModel, cfg.HTTPTimeout)
	drawers := palace.NewService(palace.NewRepo(gdb), embedder, vectors, defaultVectorDim)

	return &services{gdb: gdb, vectors: vectors, tenants: tenants, skills: skills, usage: usageSvc, drawers: drawers}, nil
}

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
// never called. By default the logger is silenced because expected "record not
// found" lookups (e.g. the create branch of an upsert) are control flow, not
// errors — real failures still surface through returned error values. In debug
// mode it logs every statement (logger.Info) so queries are visible during
// development.
//
// The logger writes to stderr, not gorm's stdout default, so a command whose
// stdout is data — the read-only mcp CLI emits JSON there — stays clean and
// pipeable even with APP_DEBUG=true; serve is unaffected (its stdout is not a
// data channel).
func openDB(path string, debug bool) (*gorm.DB, error) {
	level := logger.Silent
	if debug {
		level = logger.Info
	}
	gormLog := logger.New(
		log.New(os.Stderr, "\r\n", log.LstdFlags),
		logger.Config{LogLevel: level},
	)
	return gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormLog})
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
