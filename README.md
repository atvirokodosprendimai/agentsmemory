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
> Ollama + the vector store. Today the server exposes **16 of the planned 37 MCP
> tools** (`status`, `load_skill`, the WRITE/FILE + SEARCH/RECALL + STATUS/ADMIN
> families, plus the agent `diary`). Search is now **hybrid** — vector candidates
> re-ranked by a vector+BM25 blend (closet boost lands with mining). Mining and the
> graph/KG tool families are the next phases — see the [Roadmap](#roadmap).

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

## Centralised skills (`load_skill`)

Instead of every developer copy-pasting local skill files, a team keeps **one
shared, versioned source of truth** and its agents pull from it:

- `load_skill(name)` → returns `{ id, name, version, description, content,
  updated_by, updated_at }` so the agent can drop the body straight into a skill
  slot. Read access for any team member; the lookup is a direct keyed query (no
  vector search).
- Skills are **relational, not memory drawers** — they are mutable, named,
  permissioned authored artifacts with an owner and an update workflow.
- **Planned:** `list_skills` and `update_skill(id)` (version-bumping, role-gated)
  plus a `/load-skill <name>` Claude command that calls the tool.

---

## MCP tools

| Tool | Status | Description |
|---|---|---|
| `status` | ✅ | Liveness + the team this session is scoped to |
| `load_skill` | ✅ | Load a centralised, team-shared skill by name |
| `add_drawer` | ✅ | File a verbatim memory (chunked + embedded; idempotent by source) |
| `get_drawer` / `update_drawer` / `delete_drawer` | ✅ | Read, edit-in-place, or remove a drawer by id |
| `list_drawers` | ✅ | Paginate drawers, optionally filtered by wing/room |
| `search` | ✅ | Hybrid recall — vector candidates re-ranked by a vector+BM25 blend (closet boost with mining) |
| `check_duplicate` | ✅ | Is content near-identical to an existing drawer? |
| `list_wings` / `list_rooms` / `get_taxonomy` | ✅ | Indexed wing/room aggregations of a team's memory |
| `get_aaak_spec` | ✅ | The AAAK compressed-memory dialect reference |
| `reconnect` | ✅ | Re-ready the workspace's vector store (stateless liveness probe) |
| `diary_write` / `diary_read` | ✅ | Append to / read an agent's append-only journal (timestamped, newest-first) |
| `mine`, `create_tunnel`, `kg_add`, … | 🔜 | The remaining graph/KG/mining tools (21), ported from the Python contract |

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
       "params":{"name":"load_skill","arguments":{"name":"hello"}}}'
```

A request without a valid token comes back as a fail-closed
`unauthenticated` tool error.

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
- [x] `load_skill` centralised skill registry
- [x] Qdrant (collection-per-tenant) + Ollama (`bge-m3`) clients
- [x] Stateless Streamable-HTTP MCP server (`status`, `load_skill`)
- [x] Core memory loop — drawer CRUD + semantic recall + taxonomy (12 tools, vector-only search)
- [x] Agent diary — `diary_write` / `diary_read` (timestamped, append-only journal) (16 of 37)
- [x] Hybrid search — vector candidates re-ranked by a vector+BM25 convex blend (closet boost lands with mining)
- [ ] Mining pipeline (text → drawers, idempotent by source + chunk)
- [ ] Remaining MCP tools — graph/tunnels, KG, mining (21 of 37)
- [ ] `list_skills` + `update_skill` + a `/load-skill` Claude command
- [ ] Web dashboard (`goth` login, key & skill management) — `templ` + datastar
- [ ] Subscriptions / billing

---

## Provenance

A faithful Go SaaS rewrite of the Python `mempalace` (frozen). The domain model
(wings/rooms/drawers/closets/hallways/tunnels/KG/AAAK dialect), the 37-tool MCP
contract, the hybrid ranking, and idempotent mining are ported; Chroma, local
ONNX embeddings, and the HNSW repair tooling are dropped in favour of Qdrant +
Ollama from the start. Reference Go stack patterns follow the sibling
`forumchat` project (chi · templ · datastar · Ollama · Qdrant · MCP · RRF).
