// Command server is the agentsmemory remote MCP server. It migrates its SQLite
// schema, wires the tenant/skill/store/embed collaborators, and serves the MCP
// tools over Streamable HTTP so a team's agents can connect with a Bearer token.
//
// This is the day-one skeleton: it boots, migrates, seeds a demo team on first
// run, and answers the status and load_skill tools. Mining and hybrid search
// land in later phases against the same wiring.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/billing"
	"github.com/atvirokodosprendimai/agentsmemory/internal/buildinfo"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/dataexport"
	"github.com/atvirokodosprendimai/agentsmemory/internal/embed/ollama"
	"github.com/atvirokodosprendimai/agentsmemory/internal/embed/teiembed"
	"github.com/atvirokodosprendimai/agentsmemory/internal/embedworker"
	"github.com/atvirokodosprendimai/agentsmemory/internal/importer"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mergejob"
	"github.com/atvirokodosprendimai/agentsmemory/internal/oauth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/passkey"
	"github.com/atvirokodosprendimai/agentsmemory/internal/rerank/tei"
	"github.com/atvirokodosprendimai/agentsmemory/internal/share"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/chromemvec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"
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

	// signal.NotifyContext, so SIGINT and SIGTERM CANCEL rather than kill. That
	// is what lets the serving action return through withTelemetry's deferred
	// flush: the OTLP path batches on a 2s timer, and a process killed outright
	// drops whatever accumulated since the last one — silently, on the ordinary
	// container and systemd stop path, in the process that emits by far the most
	// spans (issue #140).
	//
	// ⚠ THE SECOND SIGNAL MUST STILL KILL, AND THAT NEEDS THE GOROUTINE. A
	// deferred stop() alone runs only when main returns, so the handler stays
	// installed for the whole drain and a second SIGTERM is SWALLOWED — an
	// operator who sends it because the first looked ignored gets nothing, from a
	// process that has decided its shutdown matters more than their instruction.
	// Calling stop() as soon as the context is cancelled restores the default
	// disposition, so the next signal ends the process outright.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	if err := rootCommand(config.Default()).Run(ctx, os.Args); err != nil {
		log.Fatal(err)
	}
}

// rootCommand builds the whole CLI.
//
// Extracted from main so that WHICH subcommands exist is a question a test can
// ask. A command that is written, correct and never added to this list is this
// repository's characteristic defect, and until now the list lived inside a
// function nothing could call.
func rootCommand(def config.Config) *cli.Command {
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
		Name:  "agentsmemory",
		Usage: "Remote MCP memory server (Qdrant + Ollama, multi-tenant)",
		// buildinfo.Effective, not the raw variable: an unstamped binary has an
		// empty (or "dev") version symbol, and printing that tells an operator
		// nothing about WHICH unstamped build they are holding. The resolver
		// falls back to dev-<commit> from the embedded VCS stamp, and it is the
		// same call productionMCPServer makes, so --version, the MCP handshake
		// and am_status can never name three different builds (issue #70).
		Version: buildinfo.Effective(version),
		Flags:   meteredServeFlags(def),
		Action:  serveAction, // no subcommand → serve (bare run + Docker CMD)
		Commands: []*cli.Command{
			{
				Name:   "serve",
				Usage:  "Run the HTTP MCP server + dashboard (the default action)",
				Flags:  meteredServeFlags(def),
				Action: serveAction,
			},
			mcpCommand(def),
			stdioCommand(def),
			syncCommand(def),
			wingCommand(def),
			drawerCommand(def),
			importCommand(),
			evalCommand(def),
			longmemevalCommand(def),
			kgExtractCommand(def),
			shareCommand(def),
			setPlanCommand(def),
			projectsCommand(def),
			inspectCommand(def),
			doctorCommand(def),
			playbookCommand(def),
		},
	}

	// Every action, once, at the one place that knows the whole tree. See
	// telemetry.go: --otel-endpoint used to install a provider on two entry
	// points out of the twelve that offer it (issue #53).
	wrapTelemetry(cmd, def)

	return cmd
}

// configFromCmd reads the storage/embed flags off a (sub)command into a Config.
// The mcp subcommand omits the addr and socket flags, so c.String yields "" for
// both there — harmless, because only the serve path reads them.
func configFromCmd(c *cli.Command, def config.Config) config.Config {
	cfg := config.Config{
		Addr:                   c.String("addr"),
		SocketPath:             c.String("socket"),
		DBPath:                 c.String("db"),
		VectorBackend:          c.String("vector-backend"),
		QdrantURL:              c.String("qdrant-url"),
		QdrantAPIKey:           c.String("qdrant-api-key"),
		OllamaURL:              c.String("ollama-url"),
		OllamaEmbedModel:       c.String("ollama-model"),
		OllamaNumThread:        c.Int("ollama-num-thread"),
		OllamaCPUQuota:         c.String("ollama-cpu-quota"),
		RerankURL:              strings.TrimSpace(c.String("rerank-url")),
		RerankPool:             c.Int("rerank-pool"),
		DBReaderPool:           c.Int("db-reader-pool"),
		BM25Weight:             strings.TrimSpace(c.String("bm25-weight")),
		EmbedBackend:           strings.TrimSpace(c.String("embed-backend")),
		SearchScope:            strings.TrimSpace(c.String("search-scope")),
		EmbedURL:               strings.TrimSpace(c.String("embed-url")),
		ClosetBoost:            c.Float("closet-boost"),
		RetrieveK:              c.Int("retrieve-k"),
		Fusion:                 strings.TrimSpace(c.String("fusion")),
		MemoryEvidenceSelector: strings.TrimSpace(c.String("memory-evidence-selector")),
		LexNorm:                strings.TrimSpace(c.String("lex-norm")),
		RerankWeight:           c.Float("rerank-weight"),
		RerankNorm:             c.String("rerank-norm"),
		RerankTimeout:          c.Duration("rerank-timeout"),
		HTTPTimeout:            c.Duration("http-timeout"),
		EmbedTimeout:           c.Duration("embed-timeout"),
		OTELEndpoint:           strings.TrimSpace(c.String("otel-endpoint")),
		MonthlyRequestCap:      c.Int("monthly-request-cap"),
		Debug:                  c.Bool("debug"),
		Local:                  c.Bool("local"),
		// Trimmed because the presented credential is: auth.bearerToken strips the
		// space around the value it parses out of the header, so a configured token
		// with a stray newline or trailing space — which a .env file or a copy-paste
		// produces easily — could never be matched by any client, and would 401
		// every request with nothing in the logs to explain it.
		LocalToken: strings.TrimSpace(c.String("token")),
		// Platform-superadmin allowlist (serve only). On the mcp CLI the flag is
		// undefined so c.String returns "" → an empty allowlist, which is correct:
		// the read-only CLI never edits the global skillset.
		SuperAdminEmails: config.ParseSuperAdminEmails(c.String("superadmin-emails")),
	}
	// Local mode serves an UNAUTHENTICATED /mcp, so it defaults to loopback rather
	// than the multi-tenant ":8080" (every interface). An explicit --addr or
	// AGENTSMEMORY_ADDR still wins — serveLocal warns when that choice reaches the
	// network — but the default must never be the exposed one.
	if cfg.Local && !c.IsSet("addr") {
		cfg.Addr = config.LocalAddr
	}
	// Local mode is "one machine, one process", so its default search index must
	// not be a service someone has to run: chromem indexes in-process and stores
	// its files beside the database. The multi-tenant default stays sqlite (see
	// config.Default) — this only moves the floor for self-hosted installs, and
	// an explicit --vector-backend or VECTOR_BACKEND still wins, which is how the
	// Docker stack pins the choice out loud.
	if cfg.Local && !c.IsSet("vector-backend") {
		cfg.VectorBackend = config.VectorBackendChromem
	}
	return cfg
}

// dataFlags are the storage + embedding flags shared by every entry point that
// opens the database (serve and the read-only mcp CLI). It returns a fresh slice
// per call so each command owns its own flag instances.
func dataFlags(def config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "db", Sources: cli.EnvVars("AGENTSMEMORY_DB"), Value: def.DBPath, Usage: "SQLite database path"},
		&cli.StringFlag{Name: "vector-backend", Sources: cli.EnvVars("VECTOR_BACKEND"), Value: def.VectorBackend, Usage: "search index: sqlite|chromem|qdrant (SQLite is always the source of truth; --local defaults to chromem)"},
		&cli.StringFlag{Name: "qdrant-url", Sources: cli.EnvVars("QDRANT_URL"), Value: def.QdrantURL, Usage: "Qdrant base URL"},
		&cli.StringFlag{Name: "qdrant-api-key", Sources: cli.EnvVars("QDRANT_API_KEY"), Value: def.QdrantAPIKey, Usage: "Qdrant API key (optional)"},
		&cli.StringFlag{Name: "ollama-url", Sources: cli.EnvVars("OLLAMA_URL"), Value: def.OllamaURL, Usage: "Ollama base URL"},
		&cli.StringFlag{Name: "ollama-model", Sources: cli.EnvVars("OLLAMA_EMBED_MODEL"), Value: def.OllamaEmbedModel, Usage: "Ollama embedding model"},
		&cli.IntFlag{Name: "ollama-num-thread", Sources: cli.EnvVars("OLLAMA_NUM_THREAD"), Value: def.OllamaNumThread, Usage: "Threads Ollama may use per embed request (options.num_thread); 0 lets it choose. Set it to the container's CPU limit when Ollama runs under one — llama.cpp sizes its pool from the HOST's cores and a quota alone leaves it throttled, so a LOWER limit is slower, not lighter"},
		&cli.StringFlag{Name: "ollama-cpu-quota", Sources: cli.EnvVars("AGENTSMEMORY_OLLAMA_CPUS"), Value: def.OllamaCPUQuota, Usage: "The CPU limit Ollama's container runs under, as Docker spells it (fractions allowed). Used ONLY when --ollama-num-thread is unset, to size the thread pool to the quota so the two cannot drift; the compose overlay passes the limit it already applies"},
		&cli.StringFlag{Name: "rerank-url", Sources: cli.EnvVars("RERANK_URL"), Value: def.RerankURL, Usage: "cross-encoder base URL for re-ranking search results (TEI, or llama.cpp's server; empty disables re-ranking)"},
		&cli.IntFlag{Name: "rerank-pool", Sources: cli.EnvVars("RERANK_POOL"), Value: def.RerankPool, Usage: "how many candidates to cross-encode per search (ignored without --rerank-url)"},
		&cli.IntFlag{Name: "db-reader-pool", Sources: cli.EnvVars("DB_READER_POOL"), Value: def.DBReaderPool, Usage: "how many connections the read-only database handle may open at once; 0 derives max(4, NumCPU()). The writer has no such knob: one writer connection is ADR-052's decision, not a default"},
		&cli.StringFlag{Name: "bm25-weight", Sources: cli.EnvVars("BM25_WEIGHT"), Value: def.BM25Weight, Usage: "lexical fusion weight: 'auto' scales per query by measured lexical signal (default), 'auto-idf' weights each query term by how much it discriminates (ahead on every table measured so far), or a fixed 0..1. DOES NOTHING when --fusion=rrf: rank fusion combines positions rather than magnitudes, so there is no weight to apply"},
		&cli.StringFlag{Name: "embed-backend", Sources: cli.EnvVars("EMBED_BACKEND"), Value: def.EmbedBackend, Usage: "what embeds text: ollama (default) or tei (text-embeddings-inference — the only path to bge-m3's sparse and multi-vector output)"},
		&cli.StringFlag{Name: "search-scope", Sources: cli.EnvVars("SEARCH_SCOPE"), Value: def.SearchScope, Usage: "what a recall naming no wing searches: wing (default, the project this MCP was registered for) or workspace (every wing)"},
		&cli.StringFlag{Name: "embed-url", Sources: cli.EnvVars("EMBED_URL"), Value: def.EmbedURL, Usage: "embedding server base URL when --embed-backend=tei"},
		&cli.FloatFlag{Name: "closet-boost", Sources: cli.EnvVars("CLOSET_BOOST"), Value: def.ClosetBoost, Usage: "closet curation-prior strength 0..1: 0 off (default), 1 full boost — measured to hurt on mined-transcript corpora and help on curated ones"},
		&cli.IntFlag{Name: "retrieve-k", Sources: cli.EnvVars("RETRIEVE_K"), Value: def.RetrieveK, Usage: "floor on how many distinct memories Search retrieves before ranking, independent of the page size. 0 (default) uses the formula: limit×3, raised to --rerank-pool when a cross-encoder will run. Does not change the page size"},
		&cli.StringFlag{Name: "lex-norm", Sources: cli.EnvVars("LEX_NORM"), Value: def.LexNorm, Usage: "how raw lexical scores are normalised before fusion: 'page-max' (default — scale so the page's best lexical match reads 1.0), 'ceiling' or 'saturating' (measure against what the QUERY could have attained, so the lexical channel stays quiet when nothing in the page matches well). DOES NOTHING when --fusion=rrf: rank fusion combines positions rather than magnitudes, so there is no lexical magnitude to normalise, and DOES NOTHING when --bm25-weight=0: at zero lexical weight there is no lexical contribution to scale"},
		&cli.StringFlag{Name: "fusion", Sources: cli.EnvVars("FUSION"), Value: def.Fusion, Usage: "how vector and lexical evidence combine: 'rrf' (default) fuses the two RANKINGS by reciprocal rank, so neither score's scale can drown the other; 'linear' blends the two SCORES weighted by --bm25-weight. Under rrf both --bm25-weight and --lex-norm are inert, because rank fusion combines positions rather than magnitudes"},
		&cli.StringFlag{Name: "memory-evidence-selector", Sources: cli.EnvVars("MEMORY_EVIDENCE_SELECTOR"), Value: def.MemoryEvidenceSelector, Usage: "bounded evidence sent to the cross-encoder: lexical (default/control) or semantic (query-time passage embeddings across the whole memory). Inert without --rerank-url"},
		&cli.DurationFlag{Name: "http-timeout", Sources: cli.EnvVars("HTTP_TIMEOUT"), Value: def.HTTPTimeout, Usage: "budget for outbound calls to the vector store and the embedder — raise it for a slow or cold embedder, which is the case an operator hits first"},
		&cli.StringFlag{Name: "otel-endpoint", Sources: cli.EnvVars("AGENTSMEMORY_OTEL_ENDPOINT"), Value: def.OTELEndpoint, Usage: "OpenTelemetry export: empty=off, 'stdout' prints a compact stage tree to stderr (file:line, outcome, reason), otherwise an OTLP HTTP collector URL (http://localhost:4318). Does not change search results"},
		&cli.FloatFlag{Name: "rerank-weight", Sources: cli.EnvVars("RERANK_WEIGHT"), Value: def.RerankWeight, Usage: "how much the cross-encoder decides the order, 0..1 (1 = it overrides the hybrid score entirely)"},
		&cli.StringFlag{Name: "rerank-norm", Sources: cli.EnvVars("RERANK_NORM"), Value: def.RerankNorm, Usage: "how a raw cross-encoder score is scaled before blending: sigmoid (preserves confidence; the default), minmax (the original — scale-free, and on a small pool at weight 0.5 it ties and discards the cross-encoder), or rank (position only)"},
		&cli.DurationFlag{Name: "rerank-timeout", Sources: cli.EnvVars("RERANK_TIMEOUT"), Value: def.RerankTimeout, Usage: "budget for a rerank call; it does real inference, unlike the other outbound calls"},
		&cli.DurationFlag{Name: "embed-timeout", Sources: cli.EnvVars("EMBED_TIMEOUT"), Value: def.EmbedTimeout, Usage: "budget for ONE embed call, separate from --http-timeout because embedding is inference and a bulk batch is not interactive: measured 121s for a batch of 64 on a CPU-only host, against a 30s http-timeout that killed the seeding run"},
		&cli.BoolFlag{Name: "debug", Sources: cli.EnvVars("APP_DEBUG"), Value: def.Debug, Usage: "verbose logging: per-request HTTP access logs + gorm SQL"},
	}
}

// meteringFlags are the flags that change how requests are METERED. They are
// separate from dataFlags because dataFlags is shared by every command that
// opens the database, and most of those never meter anything: doctor, wing
// export and set-plan among them accepted --monthly-request-cap and it changed
// no result they produce. `set-plan --monthly-request-cap=...` was the worst of
// them — it reports a durable plan-cap change while the process override it
// accepted is neither used by that operation nor persisted.
//
// That is this repo's documented reachability failure in its config form: a flag
// parsed into a Config field is not a flag that has an EFFECT in the mode that is
// running (ADR-006). Only the serving paths and the direct `mcp` CLI meter, and
// TestTheCapOverrideIsOnlyDeclaredWhereItIsEnforced pins that set against the real
// command tree.
func meteringFlags(def config.Config) []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{Name: "monthly-request-cap", Sources: cli.EnvVars("AGENTSMEMORY_MONTHLY_REQUEST_CAP"), Value: def.MonthlyRequestCap, Usage: "override the monthly metered-request cap for EVERY workspace this process serves: 0 (default) leaves the workspace's plan deciding, a positive number caps every workspace there, and a negative number uncaps them. For a self-hosted install with no billing, where the seeded Free plan's 10000/month prices a service nobody is selling. Refused alongside configured billing, which sells a cap this would override"},
	}
}

// threadsFromQuota turns a Docker CPU quota into a thread count.
//
// ⚠ IT PARSES A DECIMAL BECAUSE THE VALUE IT IS HANDED IS A CPU QUOTA. The
// compose overlay derives this from AGENTSMEMORY_OLLAMA_CPUS so the pool cannot
// drift from the limit, and Docker's quota is fractional — its own error names a
// range "from 0.01 to N.00". An integer flag therefore refused to start the
// server on a perfectly valid `cpus: 0.5`, turning a fix for slow embedding into
// a boot failure. Caught in review before it shipped.
//
// A fraction floors to 1 rather than to 0: a container with half a core still
// runs one thread, and 0 means "let llama.cpp choose", which is the opposite of
// what a small quota is asking for. Anything unparseable or negative yields 0 —
// the same "unset" the flag documents — because a thread count is an
// optimisation, and refusing to boot over one would trade a slow server for no
// server.
func threadsFromQuota(v string) int {
	q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || q <= 0 {
		return 0
	}
	if q < 1 {
		return 1
	}
	return int(q)
}

// threadsFor resolves the two knobs into the one number the embedder needs.
//
// An explicit --ollama-num-thread wins and the quota is only a fallback, because
// the quota is DERIVED — the compose overlay passes the limit it already applies
// so an operator need not repeat it — while a thread count in .env.docker is
// something an operator chose. Letting the overlay outrank that file is exactly
// the precedence defect #154 was about.
func threadsFor(cfg config.Config) int {
	if cfg.OllamaNumThread != 0 {
		return cfg.OllamaNumThread
	}
	return threadsFromQuota(cfg.OllamaCPUQuota)
}

// serveFlags are the flags the serving entry points expose: the listen address
// or socket, plus the shared storage/embed flags.
func serveFlags(def config.Config) []cli.Flag {
	return append([]cli.Flag{
		&cli.StringFlag{Name: "addr", Sources: cli.EnvVars("AGENTSMEMORY_ADDR"), Value: def.Addr, Usage: "HTTP listen address"},
		// AGENTSMEMORY_SOCKET is shared with the mcp-stdio proxy on purpose: one
		// exported variable points the server at a socket and tells the proxy
		// where to dial, so the pair cannot drift apart.
		&cli.StringFlag{Name: "socket", Sources: cli.EnvVars(mcpprotocol.SocketEnvVar), Value: def.SocketPath, Usage: "listen on this Unix socket (mode 0600) instead of --addr; pair it with 'mcp-stdio --socket' to reach the server over stdio"},
		&cli.BoolFlag{Name: "local", Sources: cli.EnvVars(mcpprotocol.LocalEnvVar), Value: def.Local, Usage: "self-hosted single-workspace mode: one \"local\" workspace, unauthenticated /mcp, no dashboard (defaults to " + config.LocalAddr + ")"},
		&cli.StringFlag{Name: "token", Sources: cli.EnvVars(mcpprotocol.LocalTokenEnvVar), Usage: "require this bearer token on --local's /mcp and /import, so the server can safely bind a LAN address (e.g. --addr 0.0.0.0:8080); omit for a credential-free loopback or --socket install"},
		&cli.StringFlag{Name: "superadmin-emails", Sources: cli.EnvVars("SUPERADMIN_EMAILS"), Usage: "comma-separated emails allowed to edit the global am_skillset playbook"},
	}, dataFlags(def)...)
}

// meteredServeFlags are serveFlags plus the metering policy: what the two serving
// entry points expose.
//
// Separate from serveFlags because `eval` reuses that builder and meters nothing
// — it opens the database and runs offline comparisons, so an accepted cap
// override would change no number it prints. The gate that caught exactly that is
// TestTheCapOverrideIsOnlyDeclaredWhereItIsEnforced, on the commit that moved the
// flag out of dataFlags.
func meteredServeFlags(def config.Config) []cli.Flag {
	return append(serveFlags(def), meteringFlags(def)...)
}

// productionMCPServer is the one composition seam for every in-process MCP
// surface. The HTTP server and the direct CLI both call it, so neither can
// silently omit a service, change the search-scope policy, or construct a
// different set of handlers. A nil services value is registration-only: it is
// used by the CLI to inspect tools/list without opening or migrating a database.
func productionMCPServer(svc *services, cfg config.Config, local bool) *server.MCPServer {
	deps := mcpserver.Deps{
		Local:             local,
		ScopeSearchToWing: cfg.ScopeSearchToWing(),
		// The same resolver the --version flag reads, so the handshake, am_status
		// and the CLI can never name three different builds.
		Version: buildinfo.Effective(version),
	}
	if svc != nil {
		deps.Skills = svc.skills
		deps.Skillset = svc.skillsets
		deps.Usage = svc.usage
		deps.Drawers = svc.drawers
		deps.Workspaces = svc.tenants
	}
	return mcpserver.Compose(deps)
}

// run opens the database, migrates, wires dependencies, and serves until error.
func run(ctx context.Context, cfg config.Config) error {
	// --token is a local-mode concept: the multi-tenant path resolves real
	// per-workspace API keys, so a single shared secret there would authenticate
	// nothing and silently ignoring it would let an operator believe the server
	// was locked down when it was not. Fail loudly instead, before the database
	// is even opened.
	if cfg.LocalToken != "" && !cfg.Local {
		return fmt.Errorf("--token requires --local: multi-tenant mode authenticates with per-workspace API keys, not a shared token")
	}

	if cfg.Debug {
		// Make the "why is it silent?" answer obvious on boot: echo the effective
		// wiring so a misread flag/env is visible before any request arrives.
		log.Printf("debug mode ON — request + SQL logging enabled")
		log.Printf("config: addr=%s db=%s vector_backend=%s ollama=%s/%s rerank=%s otel=%s",
			cfg.Addr, cfg.DBPath, cfg.VectorBackend, cfg.OllamaURL, cfg.OllamaEmbedModel,
			cmp.Or(cfg.RerankURL, "off"), cmp.Or(cfg.OTELEndpoint, "off"))
	}

	// Claim the database before opening it. Only one server may serve a given
	// database; a second would orphan the first — silently, since the loser keeps
	// running and logs nothing. See lock.go for why the database and not the
	// listener is the thing being guarded.
	//
	// This belongs to serving, not to buildServices: inspect, mcp, plan and share
	// open the same database as readers and must keep working while a server
	// runs, which is exactly what the WAL journal mode enables.
	lock, err := lockDB(cfg.DBPath)
	if err != nil {
		return err
	}
	defer lock.Close()

	// Open + migrate + wire the bounded-context services. The same wiring backs
	// the read-only mcp CLI, so it lives in buildServices (the one place the two
	// driving adapters share). Serving additionally seeds and starts transports.
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	defer svc.Close()
	log.Printf("vector backend: %s (SQLite source of truth)", cfg.VectorBackend)
	warnIfPayloadMissing(ctx, cfg, svc)
	tenants, skills, usageSvc, drawers := svc.tenants, svc.skills, svc.usage, svc.drawers

	// Background embedder: drains rows that /import absorbed (text written, vector
	// deferred) so a large migration never blocks on the embedder or a proxy read
	// timeout. It runs for the process lifetime; ctx cancels it on shutdown, and the
	// embedded_at queue is durable so a restart simply resumes. Defaults suffice.
	go embedworker.New(drawers, 0, 0, nil).Run(ctx)

	// Background merge worker: drains the durable merge_jobs queue (a GUI enqueues
	// a wing merge; this relabels + rebuilds the graph off the request path). Like
	// the embedder it runs for the process lifetime, resumes from the queue on
	// restart, and stops on ctx cancel.
	go mergejob.New(mergejob.NewRepo(svc.gdb), drawers, nil).Run(ctx)

	// The MCP server, exposed over Streamable HTTP. The HTTP context func runs
	// per request, turning the Bearer token into a tenant on the context the
	// tools read — this is the only place auth touches the transport. Tools
	// meter each call against the workspace's monthly cap via usageSvc.
	mcpSrv := productionMCPServer(svc, cfg, cfg.Local)

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

	streamSrv := mcpserver.StreamHTTP(mcpSrv)

	// The base router both modes share: logging (debug only), panic recovery, and
	// the liveness probe. Everything mounted after this point differs per mode.
	r := chi.NewRouter()
	// First in the chain: behind a reverse proxy (a cloudflared sidecar, nginx)
	// the peer is the proxy, so every log line would otherwise read as the
	// container network. realIP restores the client address for everything that
	// runs after it — Logger included. It trusts the forwarding headers only
	// when the peer is local/private, so a direct public request cannot spoof it.
	r.Use(realIP)
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

	// Self-hosted mode forks here: it serves the agent surfaces only, so none of
	// the multi-tenant machinery below (seeding, OAuth, billing, passkeys, the
	// dashboard) is constructed — let alone mounted.
	if cfg.Local {
		return serveLocal(ctx, cfg, svc, r, streamSrv)
	}

	// Seeding is serve-only: the read-only CLI must never create a demo team. The
	// global skillset is seeded here too (via its repo, bypassing the superadmin
	// gate) so am_skillset is useful on a fresh database before any edit.
	if err := seedIfEmpty(ctx, svc.gdb, tenants, skill.NewRepo(svc.gdb), skillset.NewRepo(svc.gdb), svc.vectors); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	// Billing (hosted checkout + webhook; Stripe or OpenCollective per
	// BILLING_PROVIDER). Always constructed so the dashboard wiring stays simple; it
	// is inert until the active provider's env is set (billingSrv.Enabled() gates
	// the upgrade UI). It flips teams.plan_id, so it reuses tenants as its PlanStore.
	//
	// OpenCollective sends no SIGNED webhook, so a payment is learned by asking its
	// API on a schedule rather than by being told (ADR-042). The intent store is what
	// lets a landed contribution be attributed to the workspace that started it.
	billingCfg := billingConfig()
	billingSrv := billing.NewService(billingCfg, tenants, billing.NewRepo(svc.gdb)).
		WithIntents(billing.NewIntentRepo(svc.gdb))

	// A process-wide cap override and a configured checkout cannot both be true,
	// so refuse the combination at startup rather than serving a contradiction.
	// capLookupFor returns FixedCap for every nonzero override, which means
	// teams.plan_id no longer decides anything — while billing exists precisely to
	// change teams.plan_id in exchange for money. A user could pay, the plan could
	// flip successfully, and the enforced cap would not move. A negative override
	// is the same defect wearing the opposite sign: the dashboard would offer a
	// paid lift from a cap that is already unlimited.
	//
	// Loud at boot, because both of the alternatives are silent. Suppressing the
	// upgrade UI alone leaves an operator believing they sell a plan they do not,
	// and letting checkout run takes money for nothing.
	if err := refuseCapOverrideWithBilling(cfg.MonthlyRequestCap, billingSrv.Enabled()); err != nil {
		return err
	}
	startOpenCollectiveReconciler(ctx, billingCfg, billingSrv, svc.gdb)

	// Per-workspace data export (BDAR right of access): builds a scoped SQLite
	// archive of a tenant's data from the same source-of-truth database.
	exporter := dataexport.New(svc.gdb)

	// Passkey (WebAuthn) service: registers and verifies device credentials for
	// passwordless login and as a second factor. The Relying Party config derives
	// from PUBLIC_BASE_URL — the same public origin the OAuth callbacks use — so
	// the RPID/origin match the domain the browser is on (a mismatch is the classic
	// passkey failure). A bad config is a fatal startup error, never a silent one.
	baseURL := os.Getenv("PUBLIC_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	passkeys, err := passkey.NewService(passkey.ConfigFromBaseURL(baseURL, "AI Agent Memory"), passkey.NewRepo(svc.gdb))
	if err != nil {
		return fmt.Errorf("passkey service: %w", err)
	}

	// The human-facing dashboard (register/login/create project) shares the same
	// chi router and database; agents use /mcp, people use the web routes.
	webSrv := web.New(tenants, usageSvc, skills, svc.skillsets, svc.shares, svc.merges, billingSrv, exporter, svc.drawers, passkeys, cfg.SuperAdminEmails, sessionKey())

	// OAuth discovery + endpoints for the claude.ai remote-connector handshake.
	r.Get("/.well-known/oauth-protected-resource", authSrv.ProtectedResourceMetadata)
	r.Get("/.well-known/oauth-authorization-server", authSrv.AuthorizationServerMetadata)
	r.Get("/authorize", authSrv.Authorize)
	r.Post("/token", authSrv.Token)

	// Payment webhook: PUBLIC and unauthenticated by design — the provider calls it
	// server-to-server and the signature (verified inside HandleWebhook) IS the
	// authentication. It must see the RAW request body because the signature is
	// computed over the exact bytes, so it reads the body itself rather than relying
	// on any body-parsing middleware. Only Stripe registers one (OpenCollective
	// sends no signed webhook, so nothing calls it). A non-nil error is returned as
	// 400 so the provider retries (bad signature or a transient processing failure);
	// a verified-but-unhandled event already returned nil → 200.
	webhookHandler := func(w http.ResponseWriter, req *http.Request) {
		payload, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 1<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		if err := billingSrv.HandleWebhook(req.Context(), payload, req.Header); err != nil {
			log.Printf("billing webhook: %v", err)
			http.Error(w, "webhook error", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
	r.Post("/webhooks/stripe", webhookHandler)

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

	ln, err := listenerFor(cfg)
	if err != nil {
		return err
	}
	defer ln.Close()

	log.Printf("agentsmemory listening on %s (dashboard /, MCP /mcp, OAuth issuer %s)", listenDescription(cfg), issuer)
	return serveHTTP(ctx, ln, r)
}

// seedGlobalSkillset writes the default wakeup playbook when the database holds
// none yet. BOTH serving modes need it: the am_* tools are a catalogue of verbs
// with no instruction to use them, and the playbook is the "call this first, in
// this order, before you act" guidance that makes an agent reach for them at all.
// A server answering 37 tools with an empty preamble is a memory the agent never
// opens.
//
// Written via the repo rather than the gated Service because this is system
// seeding, not a superadmin edit; updated_by stays empty to mark it as the seeded
// default rather than an authored version. Seeding only when unset means an
// operator's edits are never overwritten on restart.
func seedGlobalSkillset(ctx context.Context, skillsets *skillset.Repo) error {
	if _, err := skillsets.Get(ctx); err == nil {
		return nil // already authored or seeded — leave it alone
	} else if !errors.Is(err, skillset.ErrNotSet) {
		return fmt.Errorf("read global skillset: %w", err)
	}
	if _, err := skillsets.Set(ctx, skillset.DefaultPlaybook, ""); err != nil {
		return fmt.Errorf("seed global skillset: %w", err)
	}
	return nil
}

// serveLocal runs the self-hosted, single-workspace server and blocks. It
// resolves (and on a fresh database provisions) the one "local" workspace, then
// mounts only the agent surfaces — /mcp and /import — behind a credential-free
// tenant middleware.
//
// Nothing else is registered. The dashboard, the OAuth handshake and the billing
// webhooks all exist to tell tenants apart, let a human manage their keys, or
// charge them; with one workspace, no token and no billing relationship, each
// would be a surface with nothing behind it.
//
// The trade this mode makes is explicit: authentication is replaced by network
// reachability. That is sound on a machine you own and wrong the moment the port
// is routable, which is what the non-loopback warning is for.
func serveLocal(ctx context.Context, cfg config.Config, svc *services, r chi.Router, mcpHandler http.Handler) error {
	t, err := svc.tenants.EnsureLocalWorkspace(ctx)
	if err != nil {
		// A database holding someone else's workspaces must never be served
		// through an open endpoint, so this is fatal with an actionable message
		// rather than a fallback to "pick the first team".
		if errors.Is(err, tenant.ErrForeignWorkspace) {
			return fmt.Errorf("%w — --local serves exactly one workspace; point --db at a fresh file, or drop --local to run multi-tenant", err)
		}
		return fmt.Errorf("local workspace: %w", err)
	}

	// Ready the workspace's vector namespace so its first write/search has
	// somewhere to land — a no-op for the SQLite backend, a collection create for
	// Qdrant. Mirrors what seeding does on the multi-tenant path.
	if err := svc.vectors.EnsureNamespace(ctx, t.TeamID, defaultVectorDim); err != nil {
		return fmt.Errorf("ensure local vector namespace: %w", err)
	}

	// The wakeup playbook is seeded here as well as on the multi-tenant path.
	// Local mode skips seedIfEmpty (it must never create a demo workspace), but
	// skipping the playbook with it would leave am_skillset returning 37 tools and
	// no guidance — a server the agent can call but never thinks to.
	if err := seedGlobalSkillset(ctx, skillset.NewRepo(svc.gdb)); err != nil {
		return err
	}

	// One middleware stands in for the entire auth stack: every request already
	// belongs to the only workspace there is. With --token it also gates entry, so
	// both agent surfaces are covered by construction — /healthz stays open, since
	// a liveness probe reveals nothing and a container health check has no token.
	local := auth.LocalTenant(t, cfg.LocalToken, localBoundary(cfg))
	r.Handle("/mcp", local(mcpHandler))
	r.Handle("/import", local(importer.Handler(svc.drawers, svc.usage)))
	// A plain-text recall report, for things that are not MCP clients — the Stop
	// hook prints it at the end of a session. It exists as its own endpoint
	// because the alternative is asking a bash script to speak JSON-RPC over a
	// streamable-HTTP transport to read six numbers.
	r.Handle("/stats", local(recallStatsHandler(svc.drawers, t.TeamID)))

	// The exposure warning is about an unauthenticated TCP port, so both escapes
	// silence it. A socket is bound at 0600, so the operating system already
	// restricts the endpoint to this user — tighter than any loopback port, which
	// every process on the machine may open. A token replaces the network boundary
	// with a credential, which is the whole point of --token. Warning in either
	// case would train operators to ignore it.
	switch {
	case cfg.SocketPath != "", config.IsLoopback(cfg.Addr):
	case publishedLoopback():
		// Docker publishes 127.0.0.1:8080 to the host and hands the container a
		// non-loopback bind, because a published port cannot reach a
		// loopback-bound process. The process cannot see that boundary from
		// inside, so the compose file states it (AGENTSMEMORY_PUBLISHED_LOOPBACK)
		// and we trust the operator's own file over a guess we cannot make.
		// Warning on every boot of the DEFAULT shipped path is how operators learn
		// to scroll past warnings that matter.
		log.Printf("--local is bound to %s inside this container; the compose file publishes it on the host loopback only.", cfg.Addr)
	case cfg.LocalToken != "":
		log.Printf("--local is bound to %s (beyond this machine) and is protected by --token: agents must send \"Authorization: Bearer <token>\". Anyone holding that token has full read and write access to every memory in %s.",
			cfg.Addr, cfg.DBPath)
	default:
		log.Printf("WARNING: --local serves an UNAUTHENTICATED /mcp, and %s is not a loopback address — anyone who can reach this port has full read and write access to every memory in %s. Set --token, bind %s, use --socket, or run multi-tenant.",
			cfg.Addr, cfg.DBPath, config.LocalAddr)
	}

	ln, err := listenerFor(cfg)
	if err != nil {
		return err
	}
	defer ln.Close()

	credential := "no token required"
	if cfg.LocalToken != "" {
		credential = "bearer token required"
	}
	log.Printf("agentsmemory listening on %s (local mode: workspace %q, MCP /mcp, %s, no dashboard)", listenDescription(cfg), tenant.LocalSlug, credential)
	// Registering the server is only half the job: an MCP endpoint is a catalogue
	// of verbs, and an agent calls none of them unless something instructs it to.
	// So point at both halves — the connection AND the protocol that drives it —
	// because a local operator has no dashboard to read this from.
	//
	// A socket has no URL to hand an agent, so the hint switches to the stdio
	// proxy. That is the whole point of shipping the proxy in this binary: the
	// same executable that is already running is also the command the agent
	// spawns, so there is nothing further to install.
	install := "aiagentmemory install --local"
	if cfg.SocketPath == "" {
		// The token has to appear in the hint, not just in the docs: the agent config
		// is written once and a 401 later reads as "the server is broken". It appears
		// as the ENV VAR rather than the value — this log ends up in `docker logs` and
		// the systemd journal, which are exactly what people paste when asking for
		// help. Naming the variable keeps the line copy-pasteable (a shell that
		// exported it substitutes the real value) without writing the secret down.
		header := ""
		if cfg.LocalToken != "" {
			header = ` --header "Authorization: Bearer $` + mcpprotocol.LocalTokenEnvVar + `"`
			install += ` --token "$` + mcpprotocol.LocalTokenEnvVar + `"`
		}
		log.Printf("connect an agent:  claude mcp add --transport http agentsmemory %s%s", agentEndpoint(cfg.Addr), header)
	} else {
		log.Printf("connect an agent:  claude mcp add agentsmemory -- %s mcp-stdio --socket %s", executableName(), cfg.SocketPath)
		log.Printf("           codex:  codex mcp add agentsmemory -- %s mcp-stdio --socket %s", executableName(), cfg.SocketPath)
		install += " --socket " + cfg.SocketPath
	}
	log.Printf("then install the memory protocol (CLAUDE.md + /M, /am commands + the end-of-turn hook), or the tools sit unused:  %s", install)
	return serveHTTP(ctx, ln, r)
}

// serveHTTP serves h on ln with inbound OpenTelemetry spans. This is the one
// place HTTP requests become traces; skipping it would leave /mcp and the
// dashboard invisible while Search still emitted children with no parent.
//
// It is also where the update check is launched, and the placement is the point.
// Issue #115 requires the notice to appear AFTER the listening line, and both
// serving paths reach here only once listenerFor has succeeded and that line has
// been printed — so a startup that fails earlier announces nothing, and a fast
// answer from GitHub cannot overtake the line an operator is waiting for. Doing
// it at the top of run instead satisfies neither, which is what
// TestTheUpdateCheckLaunchesFromTheListeningSeam exists to keep true. In a
// goroutine because nothing may wait on GitHub.
func serveHTTP(ctx context.Context, ln net.Listener, h http.Handler) error {
	go announceUpdate(ctx, buildinfo.Effective(version))

	// An *http.Server rather than http.Serve, because http.Serve has no way to be
	// told to stop: it blocks until the listener breaks, ignores the context it
	// was handed, and the only thing that ended it was the signal killing the
	// process — which is the whole of issue #140.
	srv := &http.Server{Handler: telemetry.HTTPHandler(h)}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	select {
	case err := <-served:
		// A listener that broke on its own. ErrServerClosed cannot arrive here —
		// nothing called Shutdown on this path — but it is cheap to be explicit
		// rather than to rely on that staying true.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Printf("agentsmemory: signal received, draining for up to %s", shutdownGrace)
		// ⚠ FROM Background, NOT from ctx, and for the same reason the telemetry
		// flush is: ctx is ALREADY CANCELLED — that is why we are here — so a
		// Shutdown deriving from it would return instantly, drop every in-flight
		// request, and report a graceful stop it did not perform.
		drain, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(drain); err != nil {
			// Say so rather than swallow it: a deadline reached here means
			// requests WERE cut off, and an operator reading a silent exit would
			// conclude the drain succeeded.
			return fmt.Errorf("draining connections: %w", err)
		}
		return nil
	}
}

// shutdownGrace bounds the drain. Long enough for an ordinary MCP call to finish
// — a search with a reranker configured is the slow one — and comfortably inside
// Docker's default 10s between SIGTERM and SIGKILL, because a grace period the
// supervisor will not wait for is a number that only looks like a promise.
const shutdownGrace = 5 * time.Second

// agentEndpoint renders the /mcp URL to hand an agent for a given listen
// address.
//
// The wildcard forms — "0.0.0.0:8080", ":8080", "[::]:8080" — name every
// interface, which is precisely the bind a home-network install uses and
// precisely the one that is useless as a URL: a second machine cannot dial
// 0.0.0.0. Rather than print a copy-pasteable line that silently fails, those
// yield an obvious placeholder for the operator to substitute. A concrete host
// is printed as-is, because it is already the right answer.
func agentEndpoint(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// Not a host:port; echo it rather than inventing structure.
		return "http://" + addr + "/mcp"
	}
	// net.ParseIP("") is nil, so the bare ":8080" form falls through to the
	// placeholder too — it binds every interface exactly like 0.0.0.0.
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		// A container publishing to the host's loopback is the exception: the
		// wildcard bind is then an implementation detail of the port mapping, and
		// the URL that actually works is localhost. The operator states that in
		// the compose file, and a hint nobody can paste is worse than none.
		if publishedLoopback() {
			return "http://localhost:" + port + "/mcp"
		}
		return "http://<this-machine-lan-ip>:" + port + "/mcp"
	}
	return "http://" + net.JoinHostPort(host, port) + "/mcp"
}

// listenDescription renders the bound address for the startup logs: the socket
// path when one is in use, the TCP address otherwise. It exists so both serving
// modes report where they actually are rather than echoing an Addr that a socket
// listener never bound.
func listenDescription(cfg config.Config) string {
	if cfg.SocketPath != "" {
		return "unix:" + cfg.SocketPath
	}
	return cfg.Addr
}

// executableName returns this binary's path for the copy-paste "connect an
// agent" hints. The agent spawns the proxy itself, so the hint has to name a
// command that will still resolve in that context: an absolute path always
// does, where the bare name only works if the binary is on PATH. It falls back
// to the plain name if the path cannot be determined.
func executableName() string {
	exe, err := os.Executable()
	if err != nil {
		return "aiagentmemory-server"
	}
	return exe
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

// billingConfig reads the billing wiring from the environment. Billing is optional
// and single-provider per deployment: BILLING_PROVIDER selects "stripe" (the
// back-compatible default) or "opencollective", and only that provider's settings
// need to be set. The backing ids are environment-specific (test vs live) and
// differ per provider — Stripe price ids vs OpenCollective contribution-checkout
// URLs — so they are configured here rather than seeded into the plan catalog.
// Empty price entries are dropped so a half-configured environment treats that
// plan as "not sellable" instead of priced "".
func billingConfig() billing.Config {
	provider := os.Getenv("BILLING_PROVIDER")
	if provider == "" {
		provider = billing.ProviderStripe
	}
	cfg := billing.Config{
		Provider:                 provider,
		StripeSecretKey:          os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret:      os.Getenv("STRIPE_WEBHOOK_SECRET"),
		OpenCollectiveProjectURL: os.Getenv("OPENCOLLECTIVE_PROJECT_URL"),
	}
	// PriceByPlanCode carries the *active* provider's ids, keyed by our sellable plan
	// codes: Stripe price ids or OpenCollective contribution-checkout URLs.
	switch provider {
	case billing.ProviderOpenCollective:
		cfg.PriceByPlanCode = map[string]string{
			"pro_monthly": os.Getenv("OPENCOLLECTIVE_CHECKOUT_PRO_MONTHLY"),
			"pro_annual":  os.Getenv("OPENCOLLECTIVE_CHECKOUT_PRO_ANNUAL"),
		}
		// Reconciliation wiring, read only on this branch because it is only read in
		// this mode (ADR-006). An unset personal token leaves reconciliation off and
		// activation manual, which is the documented rollback.
		cfg.OpenCollectivePersonalToken = os.Getenv("OPENCOLLECTIVE_PERSONAL_TOKEN")
		cfg.OpenCollectiveSlug = os.Getenv("OPENCOLLECTIVE_COLLECTIVE_SLUG")
		cfg.OpenCollectiveAPIURL = strings.TrimSpace(os.Getenv("OPENCOLLECTIVE_API_URL"))
		if cfg.OpenCollectiveAPIURL == "" {
			cfg.OpenCollectiveAPIURL = billing.DefaultOpenCollectiveAPIURL
		}
		// A zero interval would spin the loop, so an unset or unparseable value falls
		// back to the default rather than being taken literally.
		cfg.ReconcileInterval = billing.DefaultReconcileInterval
		if raw := strings.TrimSpace(os.Getenv("OPENCOLLECTIVE_RECONCILE_INTERVAL")); raw != "" {
			if d, err := time.ParseDuration(raw); err != nil {
				log.Printf("warning: OPENCOLLECTIVE_RECONCILE_INTERVAL=%q is not a duration; using %s", raw, billing.DefaultReconcileInterval)
			} else if d <= 0 {
				log.Printf("warning: OPENCOLLECTIVE_RECONCILE_INTERVAL=%q is not positive; using %s", raw, billing.DefaultReconcileInterval)
			} else {
				cfg.ReconcileInterval = d
			}
		}
	default:
		cfg.PriceByPlanCode = map[string]string{
			"pro_monthly": os.Getenv("STRIPE_PRICE_PRO_MONTHLY"),
			"pro_annual":  os.Getenv("STRIPE_PRICE_PRO_ANNUAL"),
		}
	}
	for code, id := range cfg.PriceByPlanCode {
		if id == "" {
			delete(cfg.PriceByPlanCode, code)
		}
	}

	// Surface the ways billing silently won't work. Stripe needs its secret key to
	// open checkouts and its webhook secret for the flip; OpenCollective needs only
	// the checkout URLs (no credentials exist), but it sends no signed webhook, so
	// activation after payment is an operator action — say so loudly at boot.
	switch provider {
	case billing.ProviderOpenCollective:
		if len(cfg.PriceByPlanCode) == 0 {
			log.Printf("billing disabled: opencollective checkout URLs unset (no upgrade-to-Pro button)")
		} else {
			log.Printf("billing: opencollective checkouts enabled; plan activation is manual (set-plan CLI) — OpenCollective sends no signed webhook")
		}
	default:
		if cfg.StripeSecretKey == "" {
			log.Printf("billing disabled: stripe credentials unset (no upgrade-to-Pro button)")
		} else if cfg.StripeWebhookSecret == "" {
			// Checkout can start, but the webhook fails closed without a secret — so a
			// completed payment would never flip the plan. Surface the misconfiguration.
			log.Printf("warning: stripe webhook secret unset; webhooks will reject all events and upgrades will not take effect")
		}
	}
	return cfg
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
	gdb *gorm.DB
	// rdb is the read model's handle: query_only at the driver, pooled wide,
	// opened beside gdb on the serving path and nil on the inspection path.
	// Nothing reads through it until ADR-052 T5 routes internal/palace onto
	// it; it is held HERE because the composition root is where the split is
	// chosen rather than assumed.
	rdb       *gorm.DB
	vectors   store.VectorStore
	tenants   *tenant.Repo
	skills    *skill.Service
	skillsets *skillset.Service // the global wakeup-playbook use-cases (am_skillset)
	usage     *usage.Service
	drawers   *palace.Service
	shares    *share.Service    // cross-workspace wing-share handshake (GUI consent flow)
	merges    *mergejob.Service // background wing-merge queue (GUI enqueue/list/detect)
}

// Close releases both database handles, reader first.
//
// ADR-052 T4: the serve path holds two handles on one file, and closing only
// the writer would leave the reader's pooled connections open across
// shutdown — which pins the WAL sidecar and reads as a leaked descriptor in a
// test. The reader is nil on the inspection path, where doctor opens its own
// query_only handle and there is nothing extra to release.
func (s *services) Close() error {
	var errs []error
	for _, h := range []*gorm.DB{s.rdb, s.gdb} {
		if h == nil {
			continue
		}
		sqlDB, err := h.DB()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// configureRanking applies the search-ranking settings to svc and returns the
// lines describing what it resolved.
//
// It is a function rather than a block inside buildServices so a test can drive
// flag values through to behaviour without standing a server up: ADR-006 T2
// sweeps these knobs to discover which are inert under which mode, and a block
// reachable only from the composition root cannot be swept. newReranker is the
// cross-encoder factory — tei.New in production — so the wiring is exercisable
// with no network.
//
// The returned lines are the ONLY observable of this wiring; each setter emits
// one. That is what lets an extraction be checked as a move rather than trusted
// as one.
// defaultFusion and defaultClosetBoost mirror config.Default() so the startup
// lines report a DEPARTURE from what ships rather than from a literal that
// stopped being the default — which is what `cfg.ClosetBoost != 1` had become.
//
// Mirroring is a duplication, so it is gated: TestConfiguredDefaultsMatchConfig
// fails when either drifts from config.Default(). An earlier version of this
// comment cited that test before it existed, which is the same defect the tests
// around it exist to catch.
const (
	defaultFusion      = "rrf"
	defaultClosetBoost = 0.0
)

func configureRanking(svc *palace.Service, cfg config.Config,
	newReranker func(url string, timeout time.Duration) palace.Reranker) (*palace.Service, []string) {

	var lines []string
	say := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	drawers := svc
	requestedEvidence := strings.TrimSpace(cfg.MemoryEvidenceSelector)
	beforeEvidence := drawers.MemoryEvidenceSelectorName()
	drawers = drawers.WithMemoryEvidenceSelector(requestedEvidence)
	if requestedEvidence != "" && !strings.EqualFold(requestedEvidence, drawers.MemoryEvidenceSelectorName()) {
		say("memory evidence selector: %q is not 'lexical' or 'semantic'; keeping %s", requestedEvidence, beforeEvidence)
	}

	// Applied unconditionally: the service's own zero value is the FULL prior, so
	// a config default of 0 that was only applied "when it differs from 1" would
	// have shipped the prior it is meant to retire. The announcement is the delta —
	// silence means the shipped default, which the resolved-profile line at the end
	// states in full either way.
	drawers = drawers.WithClosetBoost(cfg.ClosetBoost)
	if cfg.ClosetBoost != defaultClosetBoost {
		say("closet boost: scaled to %.2f (%.2f is the shipped default; 1.00 is the full curation prior)",
			cfg.ClosetBoost, defaultClosetBoost)
	}
	drawers = drawers.WithRetrieveK(cfg.RetrieveK)
	if cfg.RetrieveK > 0 {
		say("retrieve-k: floor %d (0 is formula-only — limit×3, raised to rerank-pool when a cross-encoder will run)",
			cfg.RetrieveK)
	}
	// An unrecognized value is reported rather than silently ignored, the same way
	// --bm25-weight reports one below. Fusion is chosen by an operator who ran the
	// eval and decided rrf wins on their corpus; if a typo (FUSION=rff) quietly
	// served the linear blend instead, they would read the eval's rrf column and
	// their production ordering as the same configuration when they are not.
	rrf := false
	if f := strings.TrimSpace(cfg.Fusion); f != "" && !strings.EqualFold(f, "linear") {
		if strings.EqualFold(f, "rrf") {
			drawers = drawers.WithFusion("rrf")
			rrf = true
			// Announced even when rrf is the SHIPPED default. Every other line here
			// reports a departure, and this one deliberately does not: an operator
			// who sets --bm25-weight or --lex-norm under rrf gets no behaviour and
			// no error, and staying quiet because "that is just the default" is how
			// they would find out from a table instead of from their own logs.
			say("fusion: reciprocal-rank (bm25 weight and lex-norm do not apply)")
		} else {
			say("fusion: %q is not 'linear' or 'rrf'; keeping linear", f)
		}
	}
	// Under rrf the weight is inert and the line above has just said so; reporting
	// one anyway leaves two adjacent lines disagreeing, and a reader believes
	// whichever they read second. The sweep discovers this pair by running it, so
	// the startup output and the measurement now agree.
	//
	// This guards the BM25 block ONLY. The first version returned here instead,
	// which silently took the reranker with it: rrf and reranking COMPOSE — Search
	// fuses first and reranks the fused order, and rrf+rerank is an eval arm an
	// operator reads before choosing it. Suppressing a contradictory line is worth
	// one condition, never an early exit past wiring that is still wanted.
	if !rrf {
		// The anchored normalisers were built, tested and compared in the eval and
		// production could select none of them until this line existed.
		if n := strings.TrimSpace(cfg.LexNorm); n != "" && n != palace.DefaultLexNorm {
			before := drawers.LexNormName()
			drawers = drawers.WithLexNorm(n)
			if drawers.LexNormName() == before {
				say("lex norm: %q is not one of %v; keeping %s", n, palace.LexNormNames(), before)
			} else {
				say("lex norm: %s (default is %s)", drawers.LexNormName(), palace.DefaultLexNorm)
			}
		}
		if strings.EqualFold(strings.TrimSpace(cfg.BM25Weight), "auto-idf") {
			drawers = drawers.WithLexicalIDF(true)
			say("bm25 weight: auto (IDF-weighted coverage)")
		} else if w := cfg.BM25Weight; w != "" && !strings.EqualFold(w, "auto") {
			if fixed, err := strconv.ParseFloat(w, 64); err == nil {
				drawers = drawers.WithBM25Weight(false, fixed)
				say("bm25 weight: fixed %.2f (auto is the measured default)", fixed)
			} else {
				say("bm25 weight: %q is not 'auto', 'auto-idf' or a number; keeping auto", w)
			}
		}
	}
	if cfg.RerankURL != "" {
		// A rerank call does real inference, unlike the millisecond calls
		// HTTPTimeout was sized for, so it gets its own budget.
		timeout := cfg.RerankTimeout
		if timeout <= 0 {
			timeout = cfg.HTTPTimeout
		}
		drawers = drawers.
			WithReranker(newReranker(cfg.RerankURL, timeout), cfg.RerankPool).
			WithRerankWeight(cfg.RerankWeight).
			WithRerankNorm(cfg.RerankNorm)
		say("reranker: %s (pool %d, weight %.2f, timeout %s)",
			cfg.RerankURL, cfg.RerankPool, cfg.RerankWeight, timeout)
	}

	// The resolved profile is announced ALWAYS, not as a delta. Everything above
	// reports what changed; this reports what will act. A server whose startup said
	// nothing was indistinguishable from one whose operator had set every value to
	// its default, and neither could be matched against a row in an eval table.
	lines = append(lines, "ranking: "+drawers.RankingProfile())

	return drawers, lines
}

// buildServices opens and migrates the database, then wires the bounded-context
// services against it. It deliberately does NOT seed (the serve path seeds; a
// read-only CLI invocation must not create data) and starts no transport, so it
// is safe to call from both entry points.
func buildServices(cfg config.Config) (*services, error) { return buildServicesWith(cfg, true) }

// inspectServices wires the services against the palace exactly as it exists.
//
// A checker must neither migrate the database nor reconcile its derived index:
// either write can repair the evidence before doctor reports on it. Serving and
// ordinary CLI paths still prepare the stores through buildServices.
func inspectServices(cfg config.Config) (*services, error) { return buildServicesWith(cfg, false) }

// inspectDatabaseServices wires a diagnostic whose question is answered wholly
// by SQLite. It deliberately ignores the configured search backend: a missing
// or stale derived index must not suppress an unrelated database report.
func inspectDatabaseServices(cfg config.Config) (*services, error) {
	cfg.VectorBackend = config.VectorBackendSQLite
	return inspectServices(cfg)
}

// buildServicesWith holds the shared composition. prepare applies migrations
// and reconciles the selected vector backend; false leaves both stores alone.
func buildServicesWith(cfg config.Config, prepare bool) (*services, error) {
	opener := openWriterDB
	if !prepare {
		opener = openInspectionDB
	}
	gdb, err := opener(cfg.DBPath, cfg.Debug)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if prepare {
		sqlDB, err := gdb.DB()
		if err != nil {
			return nil, fmt.Errorf("sql handle: %w", err)
		}
		if err := migrate(sqlDB); err != nil {
			return nil, fmt.Errorf("migrate: %w", err)
		}
		// ADR-038 T2: stamp the content key on any row that has none.
		//
		// Beside migrate, inside `prepare`, so the read-only inspection path
		// (doctor, which opens the database with query_only(1)) never reaches a
		// write. It runs on EVERY prepared boot, gated on rows-still-empty rather
		// than on the goose version: goose records a migration's version the first
		// time its SQL runs and never runs it again, so a backfill expressed as
		// "runs once" would never resume after an abort and the corpus would sit
		// permanently half-keyed with nothing reporting it. On a fully-keyed
		// palace this costs one bounded SELECT.
		//
		// A collision FAILS THE BOOT rather than being logged and skipped: two
		// current rows hashing to one key is a corpus fact somebody must look at,
		// and a server that starts anyway serves a store whose dedup is silently
		// wrong for exactly the rows that collided.
		if err := palace.NewRepo(gdb, gdb).BackfillContentKeys(context.Background()); err != nil {
			return nil, fmt.Errorf("backfill content keys: %w", err)
		}
		// Give a name to every entry room that has none.
		//
		// ⚠ THE WRITE-TIME MINT CANNOT REACH A WING THAT STOPPED WRITING. It fires
		// when a drawer lands in the entry room, so a wing whose entry records
		// predate it answers unknown_term to the first call the entry protocol
		// prescribes — measured on this project's own palace, where forty minutes
		// separated the wings that got a root from the one that did not.
		//
		// Inside `prepare` with the other backfill, so doctor's read-only path
		// (query_only(1)) never mints: a checker that repaired the corpus would be
		// reporting on a palace it had just changed.
		//
		// ⚠ `prepare` IS NOT THE SAME SET AS "COMMANDS THAT DO NOT ADVERTISE
		// READ-ONLY", and this comment used to imply it was. `inspect`, `projects`
		// and `mcp` call buildServices, so they prepare — and inspect's own header
		// calls itself "strictly read-only". That predates this backfill: the same
		// path already migrates the database and runs BackfillContentKeys, so those
		// commands have always written. Raised by review 2026-08-31 and recorded
		// rather than widened into this change, because the obvious remedy is
		// wrong for one of them: `mcp` invokes real tools, and a tool call records
		// telemetry, so it cannot run against query_only(1).
		if minted, err := palace.NewRepo(gdb, gdb).BackfillWingRoots(context.Background()); err != nil {
			return nil, fmt.Errorf("backfill wing roots: %w", err)
		} else if minted > 0 {
			log.Printf("minted %d wing root(s) whose entry room predated the by-name address", minted)
		}
	}

	// ADR-052 T4: the read model's handle, opened only on the prepared
	// (serving) path — the inspection path is query_only end to end already,
	// and a second read-only handle there would be a second copy of the same
	// decision. It is opened at the composition root because that is where
	// the split is chosen; TestTheServePathOpensBothHandles fails when this
	// call is deleted, which is the rung nothing else could prove while no
	// read is routed through it yet (T5).
	var rdb *gorm.DB
	if prepare {
		if rdb, err = openReaderDB(cfg.DBPath, cfg.Debug, cfg.DBReaderPool); err != nil {
			return nil, fmt.Errorf("open reader db: %w", err)
		}
	}

	// Bounded contexts: tenant (auth + workspaces), skill (load_skill), and
	// usage (monthly request metering).
	tenants := tenant.NewRepo(gdb, tenant.WithTokenSecret(tokenSecret()))
	skills := skill.NewService(skill.NewRepo(gdb))
	skillsets := skillset.NewService(skillset.NewRepo(gdb))
	usageSvc := usage.NewService(usage.NewRepo(gdb), capLookupFor(cfg, tenants))

	// Vector storage: SQLite is always the source of truth; cfg.VectorBackend
	// selects whether it also serves search or Qdrant indexes it.
	vectors, err := buildVectorStoreWith(cfg, gdb, prepare)
	if err != nil {
		return nil, fmt.Errorf("vector store: %w", err)
	}

	// The memory loop: Ollama embeds text, the store seam holds the vectors, and
	// the palace service ties them to drawer metadata.
	embedder, err := buildEmbedder(cfg)
	if err != nil {
		return nil, err
	}

	// The cross-encoder is optional and additive: configured, it rescores the top
	// candidates of every search; unconfigured, search is exactly the hybrid
	// vector+BM25 fusion it has always been. Building it here keeps the
	// composition root the only place that knows which rerank server is deployed.
	// ADR-052 T5: the line that SELECTS the split. On the inspection path there
	// is no separate reader — gdb is already query_only end to end — so the one
	// handle serves both roles rather than a nil deref on the first read.
	reader := rdb
	if reader == nil {
		reader = gdb
	}
	drawers := palace.NewService(palace.NewRepo(reader, gdb), embedder, vectors, defaultVectorDim)
	drawers, rankingLines := configureRanking(drawers, cfg, func(url string, timeout time.Duration) palace.Reranker {
		return tei.New(url, timeout)
	})
	for _, line := range rankingLines {
		log.Printf("%s", line)
	}

	// The wing-share handshake bridges the two contexts it sits over: tenant
	// (resolve the destination slug, read roles) and palace (list + copy wings).
	shares := share.NewService(share.NewRepo(gdb), tenants, drawers)

	// The wing-merge queue's web side: enqueue/list jobs and detect duplicates.
	// The background worker that drains it is started in run() (serve-only).
	merges := mergejob.NewService(mergejob.NewRepo(gdb), tenants, drawers)

	return &services{gdb: gdb, rdb: rdb, vectors: vectors, tenants: tenants, skills: skills, skillsets: skillsets, usage: usageSvc, drawers: drawers, shares: shares, merges: merges}, nil
}

// buildVectorStore assembles the vector layer from cfg. SQLite is always the
// durable source of truth (sqlitevec); cfg.VectorBackend then decides whether it
// also serves search or whether an index is layered on via store.Hybrid — either
// an embedded chromem directory or a Qdrant service. This switch is the single
// swap point for the search backend.
// buildEmbedder picks what turns text into vectors. Ollama stays the default so
// every existing deployment is unaffected; EMBED_BACKEND=tei selects the
// text-embeddings-inference client instead.
//
// This selector existed as a sentence in teiembed's package comment for a while
// before it existed as code: the client was written, unit-tested and given a
// live test, and nothing ever read the variable it documented. A backend nobody
// can select is not a backend, which is the same defect the eval's production
// arm and the IDF coverage each shipped with — worth naming here because it is
// evidently this codebase's favourite way to be wrong.

// buildEmbedder selects the embedding backend, or reports why it cannot.
//
// It returns an error rather than exiting because it runs inside buildServices,
// whose four preceding failure paths all return wrapped errors and by which
// point the database is open and migrated and the vector store is built.
// log.Fatalf here exited without unwinding any of that, and routed one config
// mistake differently from every other config mistake.
func buildEmbedder(cfg config.Config) (palace.Embedder, error) {
	if !strings.EqualFold(strings.TrimSpace(cfg.EmbedBackend), "tei") {
		return ollama.New(cfg.OllamaURL, cfg.OllamaEmbedModel, cfg.EmbedTimeout, threadsFor(cfg)), nil
	}
	url := strings.TrimSpace(cfg.EmbedURL)
	if url == "" {
		// No silent fallback to Ollama: an operator who asked for TEI and gets
		// Ollama has a palace embedded by a model they did not choose, and
		// vectors from two models in one index are not comparable.
		return nil, fmt.Errorf("EMBED_BACKEND=tei needs EMBED_URL (the text-embeddings-inference base URL)")
	}
	log.Printf("embeddings: text-embeddings-inference at %s", url)
	return teiembed.New(url, cfg.EmbedTimeout), nil
}

func buildVectorStore(cfg config.Config, gdb *gorm.DB) (store.VectorStore, error) {
	return buildVectorStoreWith(cfg, gdb, true)
}

// refuseCapOverrideWithBilling rejects the one configuration whose two halves
// contradict each other: a process-wide cap override and a checkout that sells a
// cap.
//
// capLookupFor returns usage.FixedCap for every nonzero override, so teams.plan_id
// stops deciding the enforced cap — while billing exists precisely to change
// teams.plan_id in exchange for money. A user could pay, the plan could flip
// successfully, and the enforced cap would not move. A negative override is the
// same defect with the opposite sign: the dashboard would offer a paid lift from a
// cap that is already unlimited.
//
// Loud at boot, because both alternatives are silent. Hiding the upgrade control
// alone leaves an operator believing they sell a plan they do not; letting
// checkout run takes money for nothing.
//
// A named function rather than three lines inside run so the decision is testable
// without a database, a migrated schema and a live provider — the same reason
// capLookupFor is one.
func refuseCapOverrideWithBilling(cap int, billingEnabled bool) error {
	if cap == 0 || !billingEnabled {
		return nil
	}
	return fmt.Errorf("--monthly-request-cap (%d) cannot be combined with configured billing: the "+
		"override fixes the cap for every workspace this process serves, so a purchase would flip "+
		"the plan and change no enforced cap. Unset the override, or unset the provider's price "+
		"configuration", cap)
}

// capLookupFor decides what prices a workspace's monthly request cap: the
// workspace's own plan, or one process-wide override an operator configured.
//
// It is a named function rather than three lines at the call site so the choice
// is testable without a database — the call site opens one — and so this repo's
// reachability rule has something to pin: a test that asserts the override is
// consulted must fail when the wiring goes, and it cannot see an inline branch
// inside buildServicesWith.
//
// The zero value returns the plan lookup unchanged, which is what keeps an
// operator who configures nothing on exactly today's behaviour rather than on a
// new default nobody chose.
func capLookupFor(cfg config.Config, plans usage.CapLookup) usage.CapLookup {
	if cfg.MonthlyRequestCap == 0 {
		return plans
	}
	return usage.FixedCap(cfg.MonthlyRequestCap)
}

// buildVectorStoreWith is buildVectorStore with preparation made optional for
// doctor. A non-preparing Chromem open also refuses to initialize or replace an
// index layout; merely disabling reconciliation would still destroy stale
// evidence in chromemvec.New. See inspectServices.
func buildVectorStoreWith(cfg config.Config, gdb *gorm.DB, reconcile bool) (store.VectorStore, error) {
	sot := sqlitevec.New(gdb)
	switch cfg.VectorBackend {
	case config.VectorBackendSQLite:
		return sot, nil
	case config.VectorBackendChromem:
		dir := config.ChromemPath(cfg.DBPath)
		var index *chromemvec.Index
		var err error
		if reconcile {
			index, err = chromemvec.New(dir)
		} else {
			index, err = chromemvec.OpenExisting(dir)
		}
		if err != nil {
			return nil, err
		}
		hybrid := store.NewHybrid(sot, index)
		if reconcile {
			report, err := reconcileChromem(context.Background(), sot, index, hybrid)
			if err != nil {
				return nil, err
			}
			log.Print(report.String())
		}
		log.Printf("chromem index: %s", dir)
		return hybrid, nil
	case config.VectorBackendQdrant:
		index := qdrant.New(cfg.QdrantURL, cfg.QdrantAPIKey, cfg.HTTPTimeout)
		return store.NewHybrid(sot, index), nil
	default:
		return nil, fmt.Errorf("unknown vector backend %q (want %q, %q or %q)",
			cfg.VectorBackend, config.VectorBackendSQLite, config.VectorBackendChromem, config.VectorBackendQdrant)
	}
}

// warnIfPayloadMissing checks that the search index actually carries the labels
// scoped search filters on, and says so loudly when it does not.
//
// Pushing the wing/room filter into the index made an assumption that is true for
// every point written since, and false for every point written before: that the
// payload is there. A palace missing it answers every scoped search with NOTHING
// and looks like an empty wing — which is the worst way for a memory system to
// fail, because "I have no memory of that" is a plausible answer and nobody
// investigates it.
//
// It samples rather than counts: this runs at boot against a collection that may
// hold millions of points, and a hundred settle the question.
func warnIfPayloadMissing(ctx context.Context, cfg config.Config, svc *services) {
	if cfg.VectorBackend != config.VectorBackendQdrant {
		return // only the Qdrant path filters server-side against stored payload
	}
	client := qdrant.New(cfg.QdrantURL, cfg.QdrantAPIKey, cfg.HTTPTimeout)
	// The namespace list comes from the source of truth, which is the one thing
	// guaranteed to know every tenant regardless of what the index holds.
	lister, ok := svc.vectors.(interface {
		Namespaces(context.Context) ([]string, error)
	})
	if !ok {
		return
	}
	namespaces, err := lister.Namespaces(ctx)
	if err != nil || len(namespaces) == 0 {
		return
	}
	reportPayloadGaps(ctx, client, namespaces, log.Printf)
}

// payloadSampler reports how many of a sample of a namespace's points carry every
// key. *qdrant.Client satisfies it.
//
// An interface at the consumer, one method wide, because the alternative is a
// check that can only be exercised by standing up Qdrant — and this check went
// out with no test at all, which is how it shipped warning about a namespace it
// should never have sampled.
type payloadSampler interface {
	SamplePayloadCoverage(ctx context.Context, namespace string, keys []string, sample int) (withKeys, sampled int, err error)
}

// reportPayloadGaps is warnIfPayloadMissing's loop, over namespaces the caller
// has already listed and through a warn function the caller supplies.
//
// Split out so a test can drive the real loop rather than a copy of it. Asserting
// the skip on a predicate alone would leave the branch that USES the predicate
// unpinned, and this repo's characteristic defect is exactly that: a component
// that works, with nothing exercising its selection.
func reportPayloadGaps(ctx context.Context, sampler payloadSampler, namespaces []string, warnf func(string, ...any)) {
	for _, ns := range namespaces {
		// Entity points carry a label and no wing/room, by design, and
		// entityMatches searches them with a nil filter — so the sentence below
		// would be false about them in a way an operator cannot act on, and its
		// advice names a repair driven by drawer rows they do not have (#164).
		if palace.IsEntityNamespace(ns) {
			continue
		}
		withKeys, sampled, err := sampler.SamplePayloadCoverage(ctx, ns, qdrant.FilterKeys(), 100)
		if err != nil || sampled == 0 {
			continue // an unreachable or empty collection is not this check's business
		}
		if withKeys == sampled {
			continue
		}
		warnf("WARNING: %d of %d sampled points in the search index carry no wing/room label, "+
			"so every wing-scoped search will silently return NOTHING for them — they will look like an empty wing. "+
			"Repair without re-embedding: agentsmemory sync --repair-payload", sampled-withKeys, sampled)
	}
}

// recallStatsHandler serves the recall report as plain text: GET /stats?hours=1.
//
// Text, not JSON, because its only readers are a human and a Stop hook echoing it
// into a terminal. A report nobody can read at a glance is a report nobody reads.
func recallStatsHandler(drawers *palace.Service, teamID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// minutes wins over hours: a hook that knows exactly how long this session
		// has been running should be able to ask for exactly that window, rather
		// than rounding a 40-minute session up to "the last hour" and reporting
		// the previous session's work as if it were this one's.
		window := time.Hour
		if v := r.URL.Query().Get("hours"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				window = time.Duration(n) * time.Hour
			}
		}
		if v := r.URL.Query().Get("minutes"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				window = time.Duration(n) * time.Minute
			}
		}
		stats, err := drawers.RecallStats(r.Context(), teamID, "", window, 5)
		if err != nil {
			http.Error(w, "recall stats: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		label := windowLabel(window, r.URL.Query().Get("label"))

		var b strings.Builder
		if stats.Searches == 0 && stats.Writes == 0 {
			fmt.Fprintf(&b, "memory, %s: nothing recalled or filed\n", label)
			_, _ = io.WriteString(w, b.String())
			return
		}
		fmt.Fprintf(&b, "memory, %s: %s recalled, %d answered (%d%%), %s filed\n",
			label, plural(stats.Searches, "search", "searches"), stats.Answered, stats.AnsweredPct(),
			plural(stats.Writes, "memory", "memories"))
		for _, wing := range stats.Wings {
			if wing.Searches == 0 && wing.Drawers == 0 {
				continue
			}
			fmt.Fprintf(&b, "  %-24s %d/%d answered", wing.Wing, wing.Answered, wing.Searches)
			if wing.Searches > 0 {
				fmt.Fprintf(&b, " (%d%%)", wing.AnsweredPct())
			}
			fmt.Fprintf(&b, ", %s\n", plural(wing.Drawers, "drawer", "drawers"))
		}
		if len(stats.Unanswered) > 0 {
			// The most useful line in the report: each of these is a memory the
			// team went looking for and does not have.
			b.WriteString("  found nothing for: ")
			b.WriteString(strings.Join(stats.Unanswered, " | "))
			b.WriteString("\n")
		}
		// The same gaps, deduplicated and counted — what to DO about them. The
		// "  write: " prefix is a grep contract with the Stop hook; see
		// RecallStats.SuggestionLines.
		for _, line := range stats.SuggestionLines(3) {
			b.WriteString(line)
			b.WriteString("\n")
		}
		// A trailing newline: the hook pipes this straight to a terminal, and
		// without it the next line of output starts mid-sentence.
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		_, _ = io.WriteString(w, b.String())
	})
}

// windowLabel names the period the report covers. A caller that knows what the
// window MEANS (a hook that measured this session) passes its own label, because
// "this session" tells a reader something "the last 43m" does not.
func windowLabel(window time.Duration, custom string) string {
	if custom = strings.TrimSpace(custom); custom != "" {
		if len(custom) > 40 {
			custom = custom[:40]
		}
		return custom
	}
	if window < 90*time.Minute {
		return fmt.Sprintf("last %dm", int(window.Minutes()))
	}
	return fmt.Sprintf("last %dh", int(window.Hours()))
}

// plural renders a count with the right noun, because this report is read by a
// human at the end of a session and "1 searches" is the kind of detail that makes
// a tool feel unowned.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// publishedLoopback reports whether the operator declared that this process's
// port is published on the host's loopback interface only — the shape every
// container-with-published-port has, where the container's own bind address says
// nothing about who can reach it.
//
// It is deliberately an assertion the operator makes (in the compose file that
// creates the boundary), not something inferred: a process cannot see its own
// port mapping, and guessing wrong in either direction is worse than asking.
func publishedLoopback() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTSMEMORY_PUBLISHED_LOOPBACK"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// localBoundary reports whether the operator's boundary is this machine, which
// is the condition under which the credential-free local endpoint must refuse a
// request addressed elsewhere (auth.OffMachineAddressing, ADR-049).
//
// It is deliberately the same three conditions the exposure-warning switch in
// serveLocal treats as bounded — a unix socket, a loopback bind, or a container
// whose compose file publishes to the host's loopback. They are one predicate
// here rather than two copies because a guard that disagreed with the warning
// would either refuse a deployment the operator was told was fine, or trust one
// they were warned about. TestTheGuardAgreesWithTheExposureWarning holds them
// together; the switch keeps its own cases because it must tell those three
// apart to say the right thing, while the guard only asks whether any holds.
func localBoundary(cfg config.Config) bool {
	return cfg.SocketPath != "" || config.IsLoopback(cfg.Addr) || publishedLoopback()
}

// reconcileChromem fills an empty chromem index from the SQLite source of truth,
// namespace by namespace, before the server starts answering searches.
//
// It exists because the index is a directory that can legitimately be absent
// while the database is full: the first boot after an install switches to this
// backend, a restore that brought back only the .db file, or a manually deleted
// index directory. Without the replay, search would return nothing at all and
// look like data loss even though every vector is safe in SQLite. The replay is
// cheap — it reuses the stored vectors, so nothing is re-embedded and Ollama is
// not called.
//
// Only wholly empty namespaces are replayed. An index that merely fell behind is
// a different problem (rebuild it deliberately by deleting the directory), and
// re-writing every vector on every boot would make startup scale with the palace.
// Partial fall-behind is not repaired here — it is NAMED here, because an index
// at 800 of 1000 points boots clean today and nothing reports the 200 missing
// (ADR-033 R2's population check catches it at search time instead).
func reconcileChromem(ctx context.Context, sot store.SourceOfTruth, index *chromemvec.Index, hybrid *store.Hybrid) (ReconcileReport, error) {
	var report ReconcileReport
	namespaces, err := sot.Namespaces(ctx)
	if err != nil {
		return report, fmt.Errorf("list source-of-truth namespaces: %w", err)
	}
	for _, ns := range namespaces {
		indexed, err := index.Count(ctx, ns)
		if err != nil {
			return report, fmt.Errorf("count chromem namespace %q: %w", ns, err)
		}
		expected, err := sot.Count(ctx, ns)
		if err != nil {
			return report, fmt.Errorf("count source-of-truth namespace %q: %w", ns, err)
		}
		if indexed == 0 {
			if err := hybrid.Rebuild(ctx, ns); err != nil {
				return report, fmt.Errorf("rebuild chromem namespace %q: %w", ns, err)
			}
			report.Rebuilt = append(report.Rebuilt, ns)
			continue
		}
		switch {
		case indexed < expected:
			if report.Under == nil {
				report.Under = map[string]int{}
			}
			report.Under[ns] = expected - indexed
		case indexed > expected:
			if report.Over == nil {
				report.Over = map[string]int{}
			}
			report.Over[ns] = indexed - expected
		}
	}
	return report, nil
}

// ReconcileReport names what a boot reconcile found per namespace. The empty
// case (rebuilt) is the repair; the under and over cases are what used to be
// invisible. indexed > expected is not necessarily corruption — the embed
// worker upserts a vector before stamping embedded_at, so during a normal async
// batch the index briefly holds points whose rows are still pending — but it is
// always worth a line at boot.
type ReconcileReport struct {
	Rebuilt []string       // wholly-empty namespaces replayed from the source of truth
	Under   map[string]int // partial fall-behind: namespace -> points missing from the index
	Over    map[string]int // namespace -> points the index holds beyond the source of truth
}

// String renders the report the way an operator reads a boot log: one line when
// nothing was found, one clause per namespace when it was.
func (r ReconcileReport) String() string {
	if len(r.Rebuilt) == 0 && len(r.Under) == 0 && len(r.Over) == 0 {
		return "chromem index: every namespace already holds a point"
	}
	var parts []string
	if len(r.Rebuilt) > 0 {
		parts = append(parts, fmt.Sprintf("rebuilt %d empty namespace(s): %s", len(r.Rebuilt), strings.Join(r.Rebuilt, ", ")))
	}
	for _, ns := range sortedReportNamespaces(r.Under) {
		parts = append(parts, fmt.Sprintf("namespace %q is %d point(s) behind the source of truth", ns, r.Under[ns]))
	}
	for _, ns := range sortedReportNamespaces(r.Over) {
		parts = append(parts, fmt.Sprintf("namespace %q holds %d point(s) the source of truth does not", ns, r.Over[ns]))
	}
	return "chromem index reconcile: " + strings.Join(parts, "; ")
}

// sortedReportNamespaces returns the keys of m in sorted order, so the boot
// log's namespace clauses render identically run to run — a random map order
// would make the same report diff differently between boots.
func sortedReportNamespaces(m map[string]int) []string {
	ns := make([]string, 0, len(m))
	for n := range m {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return ns
}

// openWriterDB opens the one handle every write goes through, capped at a
// single connection. It is a pure-Go (no cgo) SQLite database through gorm's
// glebarez driver. gorm is the query layer; goose owns the schema, so
// AutoMigrate is never called. By default the logger is silenced because
// expected "record not found" lookups (e.g. the create branch of an upsert) are
// control flow, not errors — real failures still surface through returned error
// values. In debug mode it logs every statement (logger.Info) so queries are
// visible during development.
//
// ADR-052: one writer, so that no lock is needed. Every write, AND every read a
// write depends on, belongs on this handle inside its own transaction — a read
// taken on a reader handle is a different snapshot, so a check made there is not
// binding on the write that follows it, and nothing in review distinguishes that
// from a correct one.
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
//
// The DSN carries the pragmas described on db.WriterPragmas, because more than one
// process legitimately opens this file: serve holds it open for its lifetime
// while inspect, mcp, plan, share and an export all read it.
func openWriterDB(path string, debug bool) (*gorm.DB, error) {
	gdb, err := openDBWithPragmas(path, debug, db.WriterPragmas)
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	// ⚠ THIS CAP IS THE WRITE SERIALISATION. There is no lock, no mutex, no
	// retry and no _txlock pragma standing behind it — ADR-052 decided one
	// writer so that no wall is needed, and measured that a wall in front of a
	// single-file door changes nothing (0 of 320 either way). RAISE this cap and
	// read-then-write transactions start failing again: measured 280, 292 and 293
	// of 320 on three runs, all "database is locked", because a
	// deferred transaction that reads first must UPGRADE its lock and SQLite
	// returns SQLITE_BUSY immediately on an upgrade conflict rather than
	// invoking the busy handler. busy_timeout cannot cover that however large.
	//
	// ⚠ RAISE it, do not DELETE it, if you are falsifying this: deleting the call
	// leaves sqlDB declared and unused, so the package does not build — and a
	// build failure is an INCONCLUSIVE mutant wearing a kill's exit code, not the
	// 280 failures this comment promises. Reported by review, who tried the
	// instruction as first written.
	//
	// It is deliberately NOT a flag. The reader pool is one (--db-reader-pool),
	// because the right number there is a property of the host; one writer is
	// not a tuning parameter, it is the decision, and a knob that can raise it
	// is a knob that can silently reintroduce the defect with every test green.
	sqlDB.SetMaxOpenConns(1)
	return gdb, nil
}

// openReaderDB opens a handle SQLite itself refuses writes on, pooled wide.
//
// ADR-052: the read path cannot write through the handle it holds, and that
// is enforced by the driver — query_only(1) in readerDBPragmas — rather than
// by review. The pool is many where the writer's is one because readers never
// take the write lock and WAL lets any number of them run beside the writer.
// It is a flag (--db-reader-pool) because the right number is a property of
// the host; pool <= 0 derives max(4, NumCPU()), the treatment RerankPool gives
// its zero. No such knob exists for the writer: raising that cap silently
// reintroduces the lock-upgrade failure the record measured (280 of 320), so
// one writer is the decision, and openWriterDB keeps it as a literal.
func openReaderDB(path string, debug bool, pool int) (*gorm.DB, error) {
	gdb, err := openDBWithPragmas(path, debug, readerDBPragmas)
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(resolveReaderPool(pool))
	return gdb, nil
}

// resolveReaderPool turns the configured reader pool into the number the
// handle is opened with: the value when positive, otherwise max(4, NumCPU()).
//
// Four is a floor so a small host still serves parallel reads; one per core
// above that. Each connection carries its own page cache, so a large host
// pays memory rather than correctness for the derived default — and the
// measurement that would justify a better default is the follow-up ADR-052
// records, not a number this function pretends to know.
func resolveReaderPool(n int) int {
	if n > 0 {
		return n
	}
	return max(4, runtime.NumCPU())
}

// openInspectionDB opens SQLite with query_only enabled and without changing
// its journal mode. It is the doctor connection: even if a future diagnostic
// accidentally selects a write through one of the repositories, SQLite refuses
// it instead of altering the evidence being inspected.
func openInspectionDB(path string, debug bool) (*gorm.DB, error) {
	return openDBWithPragmas(path, debug, inspectionDBPragmas)
}

func openDBWithPragmas(path string, debug bool, pragmas string) (*gorm.DB, error) {
	level := logger.Silent
	if debug {
		level = logger.Info
	}
	gormLog := logger.New(
		log.New(os.Stderr, "\r\n", log.LstdFlags),
		logger.Config{LogLevel: level},
	)
	return gorm.Open(sqlite.Open(path+pragmas), &gorm.Config{Logger: gormLog})
}

// readerDBPragmas are the writer's pragmas plus query_only, for handles that
// serve the read model.
//
// ADR-052: the read path cannot write through the handle it holds, and that is
// enforced by SQLite rather than by review — a write through such a handle
// returns "attempt to write a readonly database (8)". journal_mode is present
// where openInspectionDB deliberately omits it, because these connections are
// serving the same live database the writer is, not inspecting evidence.
//
// ⚠ It never carries _txlock=immediate. A serialisation pragma on a handle that
// cannot write is meaningless, and on the WRITER it would mean the writer count
// had stopped being one — which is the defect to fix rather than to paper over.
const readerDBPragmas = db.WriterPragmas + "&_pragma=query_only(1)"

// inspectionDBPragmas enforce doctor's no-write boundary at SQLite itself.
// busy_timeout remains useful when doctor reads a live palace; journal_mode is
// deliberately absent because changing it would itself mutate the database.
const inspectionDBPragmas = "?_pragma=query_only(1)&_pragma=busy_timeout(5000)"

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
// agent immediately. It also seeds the global wakeup playbook so am_skillset is
// useful from the first boot. On an already-seeded database it is a no-op.
func seedIfEmpty(ctx context.Context, gdb *gorm.DB, tenants *tenant.Repo, skills *skill.Repo, skillsets *skillset.Repo, vectors store.VectorStore) error {
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

	if err := seedGlobalSkillset(ctx, skillsets); err != nil {
		return err
	}

	log.Printf("seeded demo team %s", t.TeamID)
	log.Printf("OAuth client_id (shown once): %s", cred.ClientKey)
	log.Printf("MCP bearer token / secret (shown once): %s", cred.Secret)
	log.Printf("try: curl -H 'Authorization: Bearer %s' ... http://%s/mcp", cred.Secret, "<addr>")
	return nil
}
