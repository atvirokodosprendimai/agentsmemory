# agentsmemory

> A multi-tenant **memory palace** for AI agents — served as a remote **MCP** server, backed by **Ollama** + **Qdrant** from day one.

`agentsmemory` is the Go SaaS rewrite of the original Python [`mempalace`](#provenance):
a semantic, long-term memory store that humans and AI agents read from and write
to. Where the Python tool was a single local user with no auth, this is built for
**teams**: each agent connects to a network MCP endpoint with a bearer token,
operates inside its team's isolated workspace, and can pull **centralised,
versioned skills** the team keeps up to date.

> **Status: early skeleton.** The tenancy, auth, skill registry, storage clients
> and MCP transport are wired and verified end-to-end, and the **core memory
> loop** (file a drawer → recall it semantically) now works end-to-end against
> Ollama + the vector store. Today the server exposes **36 of the planned 37 MCP
> tools** — the WRITE/FILE + SEARCH/RECALL families, the agent `diary`, the `am_mine`
> pipeline (text → chunked drawers + closet index), **hybrid** search (vector +
> BM25 + closet boost), the navigable **graph** (hallways + tunnels + traverse),
> the temporal **knowledge graph**, the skill-registry CRUD, and wing admin. Only
> the two single-user-local tools (`sync`, `hook_settings`) are intentionally not
> ported. See the [Roadmap](#roadmap).

---

## Why it exists

The "memory palace" metaphor *is* the data model:

| Concept | Meaning |
|---|---|
| **Wing** | a project / context namespace |
| **Room** | an aspect within a wing (e.g. `backend`, `decisions`) |
| **Drawer** | one **verbatim** memory chunk + rich metadata (never summarised) |
| **Closet** | a topic/quote pointer index used as a search rank-boost (never a gate) |
| **Hallway** | a within-wing link between entities that co-occur in drawers |
| **Tunnel** | a cross-wing link (author-made, or auto-generated from a shared topic) |
| **Knowledge Graph** | a separate temporal store of `subject → predicate → object` facts with validity windows |

Agents recall context with hybrid search (vector similarity + BM25 + closet
boost, fused), and file new memories that are embedded and indexed. The original
design notes live in the project's memory palace under the `agentsmemory` wing.

---

## Architecture

```
                       Authorization: Bearer <token>
   AI agent  ───────────────────────────────────────────►  POST /mcp
 (Claude, etc.)                                                 │
                                                                ▼
                                                   ┌────────────────────────┐
                                                   │  Streamable HTTP (MCP)  │  stateless
                                                   │  mark3labs/mcp-go       │
                                                   └────────────┬────────────┘
                                       HTTPContextFunc: token ──► Tenant on ctx
                                                                │ (fail closed if unresolved)
                                ┌───────────────────────────────┼───────────────────────────┐
                                ▼                               ▼                            ▼
                        internal/tenant                  internal/skill              internal/palace
                     teams · users · keys             load_skill registry         wings · rooms · drawers
                        · plans (price)              (centralised, versioned)        hallways · tunnels
                                │                            │                            │
                                ▼                            ▼                            ▼
                         SQLite (no-cgo)   ◄── relational source of truth ──►     Qdrant + Ollama
                       gorm + goose schema                                  collection-per-tenant · bge-m3
```

- **Stateless transport.** Every MCP request re-resolves its tenant from the
  bearer token, so there is no server-side session map and the service scales
  horizontally behind a load balancer.
- **One choke point for isolation.** A token becomes a `Tenant` in exactly one
  place (`tenant.Repo.ResolveToken`); every tool reads the tenant off the
  context and refuses to run without one.
- **Two stores, one source of truth.** SQLite holds tenancy, auth, plans and
  skills (the relational SoT). Qdrant holds vectors and is rebuildable from it.

---

## Multi-tenancy & plans

The unit of tenancy **and** billing is a **workspace** (the `teams` table):

- A workspace has a **kind** (`personal` | `enterprise`) and a **plan** (a price
  tier from the `plans` catalog, e.g. Personal `$0`, Enterprise `$50/mo`).
- A single user can own **several workspaces across plans** — a couple of cheap
  personal ones and one or more enterprise ones — and mint **multiple API keys**
  in each (one per agent or CI job, each independently revocable).
- Each workspace is **physically isolated**: it gets its own Qdrant collection,
  named `mempalace_<sha256(teamID)[:16]>_drawers`. A missing query filter can
  never leak across teams because the data is not even colocated.

```
user ──┬── workspace "personal"    (plan: Personal,  $0)   ── key… ── qdrant collection A
       ├── workspace "side-project"(plan: Personal,  $0)   ── key… ── qdrant collection B
       └── workspace "acme-corp"    (plan: Enterprise, $50) ── key… ── qdrant collection C
```

> Billing today is a `plan_id` column on the workspace. A dedicated
> `subscriptions` table is the planned evolution when payment lands.

---

## Authentication

Phase 1 is **per-agent bearer tokens**; the boundary is designed so OAuth 2.1
can slot in later without touching any tool.

- A user mints API keys from the (future) dashboard. Only `sha256(token)` is
  stored — the plaintext is shown once.
- An agent sends `Authorization: Bearer <token>` on its MCP connection. The
  token's workspace **is** the tenant scope for that session.
- Roles (`member` | `writer` | `admin`) gate writes to shared artifacts — e.g.
  updating a centralised skill requires `writer` or `admin`.

---

## Centralised skills (`am_load_skill`)

Instead of every developer copy-pasting local skill files, a team keeps **one
shared, versioned source of truth** and its agents pull from it:

- `am_load_skill(name)` → returns `{ id, name, version, description, content,
  updated_by, updated_at }` so the agent can drop the body straight into a skill
  slot. Read access for any team member; the lookup is a direct keyed query (no
  vector search).
- Skills are **relational, not memory drawers** — they are mutable, named,
  permissioned authored artifacts with an owner and an update workflow.
- `am_list_skills` (metadata for any member) and `am_update_skill` (version-bumping,
  writer/admin) complete the registry CRUD. A `/load-skill <name>` Claude command
  that calls the tool is the remaining nicety.

---

## MCP tools

Every tool is namespaced with the `am_` prefix (e.g. `am_status`, `am_search`)
so the server can run alongside other memory MCPs — notably mempalace, which
exposes same-named tools — without the client seeing two tools of the same name.

| Tool | Status | Description |
|---|---|---|
| `am_status` | ✅ | Liveness + the team this session is scoped to |
| `am_load_skill` | ✅ | Load a centralised, team-shared skill by name |
| `am_add_drawer` | ✅ | File a verbatim memory (chunked + embedded; idempotent by source) |
| `am_get_drawer` / `am_update_drawer` / `am_delete_drawer` | ✅ | Read, edit-in-place, or remove a drawer by id |
| `am_list_drawers` | ✅ | Paginate drawers, optionally filtered by wing/room |
| `am_search` | ✅ | Hybrid recall — vector candidates re-ranked by vector + BM25 + closet boost |
| `am_check_duplicate` | ✅ | Is content near-identical to an existing drawer? |
| `am_list_wings` / `am_list_rooms` / `am_get_taxonomy` | ✅ | Indexed wing/room aggregations of a team's memory |
| `am_get_aaak_spec` | ✅ | The AAAK compressed-memory dialect reference |
| `am_reconnect` | ✅ | Re-ready the workspace's vector store (stateless liveness probe) |
| `am_diary_write` / `am_diary_read` | ✅ | Append to / read an agent's append-only journal (timestamped, newest-first) |
| `am_mine` | ✅ | Mine a text payload into chunked drawers (entities + content date) + the closet index; idempotent by source |
| `am_list_hallways` / `am_delete_hallway` | ✅ | Within-wing entity co-occurrence links (derived from mined entities) |
| `am_create_tunnel` / `am_delete_tunnel` / `am_list_tunnels` / `am_find_tunnels` / `am_follow_tunnels` | ✅ | Cross-wing links — explicit (authored, symmetric) + derived (entity) |
| `am_traverse` / `am_graph_stats` / `am_recompute_graph` | ✅ | Walk the room↔wing graph, summarise it, rebuild hallways + entity tunnels |
| `am_kg_add` / `am_kg_invalidate` / `am_kg_query` / `am_kg_stats` / `am_kg_timeline` | ✅ | Temporal knowledge graph — subject→predicate→object facts with validity windows, queryable as-of a point in time |
| `am_list_skills` / `am_update_skill` | ✅ | List the team's centralised skills; create/version-bump a skill body (writer/admin) |
| `am_merge_wing` / `am_memories_filed_away` | ✅ | Fold wings together; summarise what the team has filed |
| `sync`, `hook_settings` | ⛔ | Not ported — single-user-local (on-disk source pruning / local hook config) with no multi-tenant meaning |

---

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go 1.25+ |
| HTTP router | `github.com/go-chi/chi/v5` |
| MCP server | `github.com/mark3labs/mcp-go` (Streamable HTTP, stateless) |
| Relational store | SQLite **no-cgo** via `gorm.io/gorm` + `github.com/glebarez/sqlite` |
| Migrations | `github.com/pressly/goose/v3` (embedded `.sql`) |
| Vector store | **Qdrant** (REST, no SDK) — collection per tenant |
| Embeddings | **Ollama** `bge-m3` (1024-dim) via `/api/embed` |
| CLI / flags | `github.com/urfave/cli/v3` |
| Auth (planned humans) | `github.com/markbates/goth` |
| Web UI (planned) | `templ` + [datastar](https://data-star.dev) |

---

## Quick start

**Prerequisites:** Go 1.25+. (Qdrant and Ollama are only needed once the memory
pipeline lands; the skeleton boots without them.)

```bash
# build
go build -o agentsmemory ./cmd/server

# run — migrates an embedded schema, seeds a demo workspace on first boot,
# and prints a one-time bearer token to the log
./agentsmemory --addr :8080 --db agentsmemory.db
```

On first run you'll see something like:

```
seeded demo team <team-id>
MCP bearer token (shown once): <64-hex-char token>
agentsmemory listening on :8080 (MCP at /mcp)
```

Call it like an MCP client would:

```bash
TOKEN=<paste the token>

# initialize
curl -s http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
       "params":{"protocolVersion":"2025-03-26","capabilities":{},
                 "clientInfo":{"name":"demo","version":"0"}}}'

# load the seeded "hello" skill
curl -s http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"am_load_skill","arguments":{"name":"hello"}}}'
```

A request without a valid token comes back as a fail-closed
`unauthenticated` tool error.

---

## Connect Claude Code (the `aiagentmemory` kit)

The `aiagentmemory` binary wires [Claude Code](https://claude.com/claude-code)
into your workspace — it installs the memory-grounded slash commands (`/M`,
`/am`) and the Stop hook, registers the agentsmemory MCP, and can wrap the Claude
CLI so each project runs against its own isolated configuration. It replaces the
old shell installer; everything ships in one downloadable binary.

Full reference: [`clients/claude-code/README.md`](clients/claude-code/README.md).

### Install in one line

```bash
curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
```

The bootstrap script detects your OS/arch, downloads the latest
`aiagentmemory-<os>-<arch>` from
[GitHub Releases](https://github.com/atvirokodosprendimai/agentsmemory/releases)
into `~/.local/bin`, then runs `aiagentmemory install`. Anything after `--` is
forwarded to `install`. Prefer to build it yourself?

```bash
go build -o aiagentmemory ./clients/claude-code
./aiagentmemory install
```

`install` prompts for your **workspace API token** (create a project in the
dashboard and copy or **Reveal** its key), then registers the agentsmemory MCP in
one shot. Supply it non-interactively with `--token <key>` or the
`AGENTSMEMORY_TOKEN` environment variable. Add `--recommended` to also install the
companion tools: the [codebase-memory](https://github.com/DeusData/codebase-memory-mcp)
MCP and the eidos and codex plugins. Preview any run with `--dry-run` — it prints
every file write and command without touching anything.

### Two ways to install

| Mode | Command | What it does |
|------|---------|--------------|
| **Global** | `aiagentmemory install` | Wires the MCP, commands, and Stop hook into the global `~/.claude`. Wraps the Claude you already run. |
| **Sandboxed** | `aiagentmemory install --sandbox <name>` | Installs a self-contained config under `~/.sandboxes/<name>`, isolated from every other project and from the global `~/.claude`. |

### Sandboxed installation (per-project isolation)

A **sandbox** is just a Claude config directory under `~/.sandboxes/<name>`.
Running Claude with `CLAUDE_CONFIG_DIR` pointed at it isolates that project's
slash commands, settings, MCP servers, and agentsmemory token from everything
else — so a client project and an internal project never share memory, tools, or
credentials. Set one up once, with or without the recommended tools:

```bash
aiagentmemory install --sandbox acme               # core: commands, hook, our MCP
aiagentmemory install --sandbox acme --recommended # + codebase-memory, eidos, codex
```

The installer writes into `~/.sandboxes/acme/` and runs every `claude`
registration with `CLAUDE_CONFIG_DIR` pinned there, so nothing leaks into your
global config. Sandbox names are plain identifiers (letters, digits, dash,
underscore).

### Run a sandbox without re-installing

Installing is a one-time setup. To **launch Claude against an existing sandbox**,
just name it — no re-install:

```bash
aiagentmemory run acme                     # open Claude in the acme sandbox
aiagentmemory run acme -p "summarise repo" # args after the name pass straight to claude
```

`run <name>` sets `CLAUDE_CONFIG_DIR=~/.sandboxes/<name>`, then exec-replaces the
process with the Claude CLI — inheriting your terminal and its exit code, so it
behaves exactly like running `claude`, only against that sandbox. It errors with a
hint if the sandbox hasn't been installed yet. The global counterpart is:

```bash
aiagentmemory wrap                         # run Claude against the global ~/.claude
```

The Claude CLI it drives is resolved from `AIAGENTMEMORY_CLAUDE_BIN`, then
`claude` on your `PATH`.

---

## Configuration

All flags have sensible local defaults:

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `:8080` | HTTP / MCP listen address |
| `--db` | `agentsmemory.db` | SQLite database path |
| `--qdrant-url` | `http://localhost:6333` | Qdrant base URL |
| `--qdrant-api-key` | *(empty)* | Qdrant API key (optional) |
| `--ollama-url` | `http://localhost:11434` | Ollama base URL |
| `--ollama-model` | `bge-m3` | Embedding model (1024-dim) |

---

## Migrating from mempalace

Bring an existing local Python `mempalace` into a workspace — every drawer, diary
entry, closet, knowledge-graph fact and explicit tunnel. The vehicle is a small
**read-only** CLI that reads your palace and streams it to the server's
`/import` endpoint with your project's API key; the server **re-embeds** each
memory with its own model (the bundle carries text, not vectors) and rebuilds the
derived graph (hallways/entity-tunnels) afterwards.

```bash
# Run where the mempalace package is installed. Inspect first:
python clients/migrate/mempalace_export.py --out palace.ndjson

# Then stream it into your workspace (token = the project's API key):
python clients/migrate/mempalace_export.py --push \
  --server https://your-host --token sk_live_xxx

# Or push a bundle exported earlier on another machine:
python clients/migrate/mempalace_export.py --file palace.ndjson --push \
  --server https://your-host --token sk_live_xxx
```

`POST /import` sits behind the same Bearer gate as `/mcp`, takes streaming NDJSON
(one record per line, `kind`-discriminated), and streams progress back. The
import is **idempotent** — drawer ids are recomputed under the target tenant, so
re-running a partial migration finishes it rather than duplicating. The project
page surfaces the exact command (with your host filled in) under *Bring your
mempalace*.

Full step-by-step guide, flag reference and troubleshooting:
[`clients/migrate/README.md`](clients/migrate/README.md).

---

## Data export & BDAR/GDPR compliance

A workspace member can download **everything the workspace holds** as a single,
self-contained **SQLite file** — the *right of access* and *data portability*
under **BDAR** (the Lithuanian implementation of the EU GDPR). The project page
surfaces it under *Download your data*; it maps to a membership-gated
`GET /projects/{teamID}/export`. It is the **outbound counterpart to `/import`**:
import brings a palace in, export takes your workspace out.

The archive is a standalone, valid SQLite database — open it with any SQLite
browser — built from the live source of truth:

- **Schema** is replayed **verbatim** from the source `sqlite_master`, so the
  export is byte-faithful to the running schema (no goose re-run, no drift).
- **Rows** are copied through an explicit, reviewed manifest, each **scoped to the
  requesting tenant** — workspace-owned memory (drawers, diary, closets, hallways,
  tunnels, knowledge-graph facts, vectors, skills, usage, subscriptions, merge
  jobs) by `team_id` / namespace, plus the requester's own identity rows (account,
  membership, API-key metadata). No other tenant's data can enter the archive.
- **Credentials are redacted**: the password hash is blanked, an API key's
  `token_hash` is replaced and `token_enc` blanked — the export carries *your
  data*, never usable secrets.

```bash
# From the browser: project page → "Download your data".
# Or with an authenticated session cookie:
curl -b session.jar https://your-host/projects/<teamID>/export \
  -o agentsmemory-<workspace>-<date>.sqlite
```

Implementation: [`internal/dataexport`](internal/dataexport/dataexport.go)
(scoping manifest + redaction) and `internal/web/export.go` (the download route).

---

## Project layout

```
cmd/server/            entrypoint: cli flags → migrate → seed → serve
db/                    embedded goose migrations (.sql)
internal/
  config/              runtime configuration
  tenant/              teams (workspaces) · users · memberships · api_keys · plans
  skill/               centralised skill registry (load_skill)
  store/qdrant/        Qdrant REST client, collection-per-tenant naming
  embed/ollama/        Ollama bge-m3 embedder
  auth/                bearer token → tenant context injection
  palace/              core memory domain types (wing/room/drawer/hallway/tunnel)
  mcpserver/           MCP tool wiring (status, load_skill, …)
  dataexport/          per-workspace SQLite data export (BDAR right of access)
  web/                 dashboard (templ + datastar): projects, keys, export
```

Bounded contexts are kept apart (DDD): `tenant` and `skill` share only tenancy
and auth, never storage internals; interfaces are declared at the consumer.

---

## Development

```bash
go build ./...     # compile everything
go vet ./...       # static checks
go test ./...      # unit tests (skill scoping + role gate, qdrant naming)
```

`goose` owns the schema; `gorm` is the query layer only (`AutoMigrate` is never
called). Schema changes are additive migrations under `db/migrations/`.

---

## Roadmap

- [x] Tenancy (workspaces, users, memberships, API keys) + plan/price tiers
- [x] Bearer-token auth → tenant resolution; fail-closed tools
- [x] `am_load_skill` centralised skill registry
- [x] Qdrant (collection-per-tenant) + Ollama (`bge-m3`) clients
- [x] Stateless Streamable-HTTP MCP server (`am_status`, `am_load_skill`)
- [x] Core memory loop — drawer CRUD + semantic recall + taxonomy (12 tools, vector-only search)
- [x] Agent diary — `am_diary_write` / `am_diary_read` (timestamped, append-only journal) (16 of 37)
- [x] Hybrid search — vector candidates re-ranked by vector + BM25 + closet boost (RRF-style convex blend)
- [x] Mining pipeline — `am_mine` text → chunked drawers (entities + content date) + closet index, idempotent by source (17 of 37)
- [x] Graph — hallways (entity co-occurrence) + tunnels (explicit + entity) + traverse/find/stats/recompute (10 tools, 27 of 37)
- [x] Knowledge graph — temporal subject→predicate→object facts with validity windows (5 tools, 32 of 37)
- [x] Skill registry CRUD — `am_list_skills` + `am_update_skill` (role-gated)
- [x] Admin — `am_merge_wing` + `am_memories_filed_away` (36 of 37; `sync`/`hook_settings` are single-user-local, not ported)
- [x] Web dashboard — local (`goth`) login, project create + one-time API key, monthly usage metering — `templ` + datastar
- [x] Web skill management — per-project list / create / edit (role-gated to writer/admin), membership-checked routes
- [x] Migration — read-only `mempalace` exporter + streaming `POST /import` (drawers, diary, closets, KG facts, tunnels; re-embedded, graph rebuilt)
- [x] Data export (BDAR/GDPR) — download a workspace's data as a self-contained SQLite file (`GET /projects/{teamID}/export`, membership-gated, tenant-scoped, secrets redacted)
- [ ] Web — API-key rotation/revoke + team/member management (invite, set role)
- [ ] A `/load-skill` Claude command (the client-side nicety over the `am_load_skill` tool)
- [ ] Subscriptions / billing

---

## Provenance

A faithful Go SaaS rewrite of the Python `mempalace` (frozen). The domain model
(wings/rooms/drawers/closets/hallways/tunnels/KG/AAAK dialect), the 37-tool MCP
contract, the hybrid ranking, and idempotent mining are ported; Chroma, local
ONNX embeddings, and the HNSW repair tooling are dropped in favour of Qdrant +
Ollama from the start. Reference Go stack patterns follow the sibling
`forumchat` project (chi · templ · datastar · Ollama · Qdrant · MCP · RRF).
