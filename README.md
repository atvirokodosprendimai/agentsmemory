# agentsmemory

[![Support on Open Collective](https://opencollective.com/it-uoga/tiers/badge.svg)](https://opencollective.com/it-uoga/projects/ai-agents-memory)

> A multi-tenant **memory palace** for AI agents — served as a remote **MCP** server, backed by **Ollama** and a swappable vector index (**Qdrant** for the SaaS, an **embedded** one for self-hosted).

`agentsmemory` is the Go SaaS rewrite of the original Python [`mempalace`](#provenance):
a semantic, long-term memory store that humans and AI agents read from and write
to. Where the Python tool was a single local user with no auth, this is built for
**teams**: each agent connects to a network MCP endpoint with a bearer token,
operates inside its team's isolated workspace, and can pull **centralised,
versioned skills** the team keeps up to date.

> **Status: early skeleton.** The tenancy, auth, skill registry, storage clients
> and MCP transport are wired and verified end-to-end, and the **core memory
> loop** (file a drawer → recall it semantically) now works end-to-end against
> Ollama + the vector store. Today the server exposes **41 MCP
> tools**, the same 41 whether hosted or self-hosted — the WRITE/FILE + SEARCH/RECALL families, the agent `diary`, the `am_mine`
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
  skills (the relational SoT) *and* every vector. The search index — Qdrant, or
  the embedded chromem index a self-hosted install defaults to — is derived from
  it and rebuildable without re-embedding.

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
  writer/admin) complete the registry CRUD. The **`/load-skill <name>`** Claude
  command is the client-side nicety over the tool: it fetches a skill by name and
  uses its body directly in the session — no file written, always the live
  version (with no name, it lists what's available). Shipped by the
  `aiagentmemory` installer.

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
| `am_get_drawer` / `am_update_drawer` / `am_invalidate_drawer` | ✅ | Read a memory; correct it (sending `content` supersedes — a new record, the old one ended with your `reason`, the two linked) or move it (wing/room keeps the id); retract one that nothing replaces. **No agent-reachable tool destroys a record** — erasure is `agentsmemory drawer erase`, which needs the database file |
| `am_list_drawers` | ✅ | Paginate drawers, optionally filtered by wing/room |
| `am_search` | ✅ | Hybrid recall — vector candidates re-ranked by vector + BM25 + closet boost, then optionally by a TEI cross-encoder (`RERANK_URL`) |
| `am_check_duplicate` | ✅ | Is content near-identical to an existing drawer? |
| `am_list_wings` / `am_list_rooms` / `am_get_taxonomy` | ✅ | Indexed wing/room aggregations of a team's memory |
| `am_get_aaak_spec` | ✅ | The AAAK compressed-memory dialect reference |
| `am_reconnect` | ✅ | Ensure the workspace's vector namespace exists; write-gated because this may create backend state |
| `am_diary_write` / `am_diary_read` | ✅ | Append to / read an agent's append-only journal (timestamped, newest-first) |
| `am_mine` | ✅ | Mine a text payload into chunked drawers (entities + content date) + the closet index; idempotent by source |
| `am_list_hallways` | ✅ | Within-wing entity co-occurrence links (derived from mined entities). Rebuilt by `am_recompute_graph`, which is also how one is removed |
| `am_create_tunnel` / `am_list_tunnels` / `am_find_tunnels` / `am_follow_tunnels` | ✅ | Cross-wing links — explicit (authored, symmetric) + derived (entity) |
| `am_traverse` / `am_graph_stats` / `am_recompute_graph` | ✅ | Walk the room↔wing graph, summarise it, rebuild hallways + entity tunnels |
| `am_kg_add` / `am_kg_invalidate` / `am_kg_supersede` / `am_kg_query` / `am_kg_stats` / `am_kg_timeline` | ✅ | Temporal knowledge graph — subject→predicate→object facts with validity windows, queryable as-of a point in time. `am_kg_invalidate` retracts and requires a `reason`; `am_kg_supersede` REPLACES a value in one transaction, ending the old window and starting the new one at the same instant — hand-rolling invalidate-then-add leaves both values in effect until the end of the day |
| `am_list_skills` / `am_update_skill` | ✅ | List the team's centralised skills; create/version-bump a skill body (writer/admin) |
| `am_bootstrap` | ✅ | Start a session in one call: a wing's entry node, its first records inlined, pointers to the rest, and the corrections attached to any of them. Replaces a hand-executed multi-call traversal. **Returns `resolution: "unknown_term"` on a wing whose `llm_init` drawers were filed before the derived room edges shipped** — those edges are written when a drawer is written, and existing corpora are not backfilled |
| `am_entry_point` | ✅ | Where to START in a wing: the entry node and what it points at. Edges naming a record in another wing are dropped and counted in `refused`, never listed. Same `unknown_term` condition as `am_bootstrap` |
| `am_list_anchors` / `am_mark_anchors` | ✅ | Code anchors pinned to a memory — list them, or re-check them against the tree and mark the drawers whose code has since changed |
| `am_recall_stats` | ✅ | What recall actually did: counts and score distributions over recorded searches, including why a cross-encoder did not order a page |
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
| Vector store | **Qdrant** (REST, no SDK) — collection per tenant · or embedded **`chromem-go`** · or SQLite itself |
| Embeddings | **Ollama** `bge-m3` (1024-dim) via `/api/embed` |
| CLI / flags | `github.com/urfave/cli/v3` |
| Auth (planned humans) | `github.com/markbates/goth` |
| Web UI (planned) | `templ` + [datastar](https://data-star.dev) |

---

## Quick start

**Prerequisites:** Go 1.25+ and an **Ollama** with `bge-m3` pulled — every drawer
is embedded on the way in, so the memory loop needs it (see [Preparing Ollama
(embeddings)](#preparing-ollama-embeddings) below). A vector *service* is not a
prerequisite: searches are indexed in-process unless you ask for Qdrant.

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

### Preparing Ollama (embeddings)

agentsmemory never embeds text itself — it calls **your** Ollama, so nothing you
remember leaves your machine. One install, one model pull:

```bash
# 1. Install and run Ollama — https://ollama.com/download
#    macOS/Windows: the app starts the server.
#    Linux:  curl -fsSL https://ollama.com/install.sh | sh
ollama --version

# 2. Pull the embedding model (bge-m3, 1024-dim, ~1.2 GB)
ollama pull bge-m3

# 3. Prove it answers on the endpoint the server will use
curl -s http://localhost:11434/api/embed \
  -d '{"model":"bge-m3","input":"hello"}' | head -c 120
```

Step 3 is the one worth running: a JSON array of floats means the server will
work, and it fails loudly here rather than on your first `am_add_drawer`. The
model must be *pulled*, not merely installed — Ollama does not fetch it on
demand for `/api/embed`; a missing one comes back as `model "bge-m3" not found`.
When that happens nothing is lost: writes return the embed error, and rows that
came through `/import` sit in the embed queue and drain by themselves once the
model is there.

**Why `bge-m3`, and why not to change it casually.** It matches the frozen Python
palace (1024-dim), so migrated memories and new ones share one vector space.
Swapping the model changes that space: old and new vectors stop being comparable
and every drawer needs re-embedding. Pick it before you fill the palace, not
after — `--ollama-model` exists for a fresh one.

**Running the server in Docker? `localhost` is not your machine.** Ollama binds
`127.0.0.1` by default, and a container cannot reach the host's loopback. Either
bind it wider and use the name compose maps for you (`OLLAMA_URL=http://host.docker.internal:11434`):

```bash
# macOS
launchctl setenv OLLAMA_HOST 0.0.0.0     # then restart the Ollama app

# Linux (systemd)
sudo systemctl edit ollama               # add: Environment="OLLAMA_HOST=0.0.0.0"
sudo systemctl restart ollama
```

…or, on Linux, skip the problem entirely with the host-network override below,
where `localhost:11434` inside the container *is* your machine's loopback.

A GPU box elsewhere works just as well — point `OLLAMA_URL` at it
(`http://192.168.1.50:11434`) and pull `bge-m3` there instead.

### Self-hosted single-workspace mode (`--local`)

Everything above is the multi-tenant SaaS shape: many workspaces, each behind a
token. If you are running this on your own machine for yourself, `--local`
collapses it to the simplest thing that still runs every tool.

Grab the server binary for your platform — every release publishes
`aiagentmemory-server-<os>-<arch>` for `linux` and `darwin` on `amd64` and
`arm64`, alongside the `aiagentmemory` CLI:

```bash
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); [ "$arch" = x86_64 ] && arch=amd64; [ "$arch" = aarch64 ] && arch=arm64
curl -fsSL -o agentsmemory \
  "https://github.com/atvirokodosprendimai/agentsmemory/releases/latest/download/aiagentmemory-server-${os}-${arch}"
chmod +x agentsmemory
```

Windows, or want checksums? The same releases carry
`agentsmemory_<os>_<arch>.tar.gz` / `.zip` archives (Windows included) with a
`SHA256SUMS.txt`. Prefer to build it yourself? `go build -o agentsmemory
./cmd/server`. Either way, run it:

```bash
./agentsmemory --local --db agentsmemory.db
# agentsmemory listening on 127.0.0.1:8080 (local mode: workspace "local", MCP /mcp, no token required, no dashboard)
```

Then point your agent at *that* server. It is the same kit as the hosted
service; `--local` aims it at `http://localhost:8080/mcp` and never *asks* for a
token, because a loopback server has none (pass `--token` only if you started the
server with one — see [serving a home
network](#serving-a-home-network---token)):

```bash
aiagentmemory install --local                                # global, Claude (~/.claude) — the default agent
aiagentmemory install --local --agent all                    # claude | codex | pi | both | all
aiagentmemory install --local --sandbox acme                 # isolated config at ~/.sandboxes/acme
aiagentmemory install --local --sandbox acme --agent codex   # …and choose the agent inside it
```

With `--local` and no target named, the install goes global and skips the
interactive global-vs-sandbox prompt — a self-hoster setting up their own machine
has an obvious answer to that question. Naming `--sandbox <name>` still wins (the
name is required), and `--mcp-url` overrides the endpoint for a server on another
port or host. Registering the MCP is only half the job, though: what makes an
agent actually *use* the tools is the protocol the kit installs alongside it —
see [the server is inert without the
protocol](#the-server-is-inert-without-the-protocol).

⚠ **Re-run `install` the same way you ran it the first time.** Without `--local`
(or `--mcp-url`) the endpoint falls back to the hosted default, and the default
wins over what is already configured — so a bare `aiagentmemory install` on a
machine set up with `--local` repoints every installed hook at the hosted
service. The hooks then talk to a server they hold no credential for, and a hook
that cannot reach its palace goes **silent** rather than failing loudly, so
nothing looks broken. The installer now says so before it writes:

```
[!!] this install REPOINTS your hooks: they currently talk to
     http://localhost:8080/mcp, and will now talk to https://aiagentmemory.dev/mcp.
```

To see where your hooks point today, grep the endpoint out of your agent's hooks
file — for Claude, `AGENTSMEMORY_MCP_URL` is the first thing in each command:

```bash
grep -o "AGENTSMEMORY_MCP_URL='[^']*'" ~/.claude/settings.json | sort -u
```

What changes:

| | default | `--local` |
|---|---|---|
| Workspaces | many, created from the dashboard | exactly one, slug `local`, provisioned on first boot |
| `/mcp` auth | Bearer token or OAuth | **none** — every request is the local workspace (unless [`--token`](#serving-a-home-network---token)) |
| API keys | minted per member | none exist; none are stored |
| Quota | per plan | uncapped (`plan_unlimited`) |
| Dashboard, OAuth, billing webhooks | mounted | **not registered** (404) |
| Listen address | `:8080` (all interfaces) | `127.0.0.1:8080` |
| Search index | `sqlite` (the source of truth scans itself) | **`chromem`** — embedded, no service to run |

All 41 MCP tools behave identically — they only ever see a resolved workspace,
and local mode injects one instead of resolving it from a credential.

Point any MCP client straight at it, with no header:

```bash
claude mcp add --transport http agentsmemory http://localhost:8080/mcp
```

Two guardrails worth knowing:

- **The endpoint is unauthenticated by default**, so reachability *is*
  authorization. That is why the default binds loopback. Overriding `--addr` to a
  routable interface still works but logs a loud warning — anyone who can reach
  the port owns every memory in the file. Two things tighten it:
  [`--token`](#serving-a-home-network---token) puts a shared secret in front of
  the port, and
  [`--socket`](#unix-socket-and-stdio-mcp---socket--mcp-stdio) goes further than
  loopback can.
- **A browser cannot reach it by renaming your machine.** Because reachability is
  authorization, the endpoint refuses any request whose `Host` or `Origin` names
  something other than this machine, answering
  `403 forbidden: Host <name> does not address this machine`. That is not
  theoretical tidiness: a page you visit
  can point its own domain at `127.0.0.1`, at which point your browser treats it
  as same-origin, skips the preflight, and CORS never runs — so without this
  check any site you open could read and rewrite every memory in the file. The
  check is on wherever the boundary is this machine (loopback, `--socket`, or a
  container publishing to the host loopback) and off when you have deliberately
  bound a routable address, where `--token` is the boundary instead.
- **It refuses to start** if the database already holds a workspace that is not
  `local` — including the `demo` workspace the multi-tenant path seeds on first
  boot. Use a fresh `--db` file, or drop `--local`.

#### Serving a home network (`--token`)

Loopback is the right default for one machine, and the wrong one the moment you
want the laptop in the other room to share the same memory. `--token` is what
makes that safe: local mode then requires `Authorization: Bearer <token>` on
`/mcp` and `/import`, so the shared secret takes over the job the loopback
boundary was doing.

```bash
export AGENTSMEMORY_LOCAL_TOKEN="$(openssl rand -hex 32)"
./agentsmemory --local --token "$AGENTSMEMORY_LOCAL_TOKEN" --addr 0.0.0.0:8080 --db agentsmemory.db
```

`0.0.0.0` binds every interface, which is what makes the server reachable from
another machine. Then install the kit on each of those machines, pointing at the
server's LAN address — one exported variable configures both halves, because the
server and the installer read the same one:

```bash
export AGENTSMEMORY_LOCAL_TOKEN="…the same value…"
aiagentmemory install --local --token "$AGENTSMEMORY_LOCAL_TOKEN" \
  --mcp-url http://192.168.1.50:8080/mcp
```

Worth knowing:

- **The token authenticates, it does not identify.** There is still exactly one
  workspace, so everyone holding the token shares one memory palace and has full
  read and write access to it. That is usually the point on a home network; it is
  not a way to separate two people's memories.
- **No token is stored server-side**, so there is nothing to rotate but the flag —
  restart with a new value and re-run the installer.
- **`/healthz` stays open** so container health checks keep working. It reveals
  nothing but liveness.
- **Without `--token`, nothing changes.** A loopback or `--socket` install carries
  no credential, and a stray `Authorization` header left in an agent's config is
  still ignored rather than rejected.
- **Anything routable deserves TLS.** The token crosses the network in a header,
  so on anything less trusted than your own LAN, put a reverse proxy with HTTPS in
  front of it rather than exposing the port directly.

Local mode also picks its own search index: **chromem**, a vector database that
runs inside the server process. It keeps the vectors in memory and persists them
next to the database — `agentsmemory.db` gets `agentsmemory.chromem/` beside it,
one directory per workspace inside — so a self-hosted install is one binary, one
file and one folder, with no service to install, start or monitor. `sqlite` and
`qdrant` remain one `--vector-backend` away ([choosing the index](#choosing-the-index)).

### Unix socket and stdio MCP (`--socket` / `mcp-stdio`)

HTTP on a port stays the default and nothing about it changed. But a port is a
weak boundary for an endpoint with no authentication: *every* user and process on
the machine can open `127.0.0.1:8080`. `--socket` replaces it with a Unix socket
created at mode `0600`, so the operating system restricts the server to the
account that started it:

```bash
./agentsmemory --local --socket /tmp/agentsmemory.sock --db agentsmemory.db
# agentsmemory listening on unix:/tmp/agentsmemory.sock (local mode: workspace "local", …)
```

A socket has no URL, so agents reach it through `mcp-stdio` — a bridge shipped in
the same binary that speaks MCP on stdin/stdout and forwards to the server:

```bash
claude mcp add agentsmemory -- /path/to/agentsmemory mcp-stdio --socket /tmp/agentsmemory.sock --wing wing_acme
codex  mcp add agentsmemory -- /path/to/agentsmemory mcp-stdio --socket /tmp/agentsmemory.sock --wing wing_acme
```

The server prints both lines on startup with its own absolute path filled in, so
they can be copied straight out of the log. The installer can wire it for you
instead — it registers the same bridge and installs the memory protocol alongside
it:

```bash
aiagentmemory install --local --socket /tmp/agentsmemory.sock
```

`--socket` requires `--local` (the bridge carries no token, so it only reaches a
self-hosted server) and finds the server binary on `PATH`, or takes
`--server-bin /path/to/agentsmemory`. It works for Claude and codex; pi has no MCP
client of its own, so install that one with `--mcp-url` against `--addr`.

Worth knowing:

- **It is a raw JSON-RPC passthrough.** The bridge carries no tool catalogue, so
  a tool added to the server works over stdio the moment the server restarts —
  there is nothing to regenerate and no proxy to rebuild.
- **One server, many agents.** Each agent spawns its own bridge process, but they
  all share the one server — and therefore one SQLite writer and one embedding
  queue, rather than each opening the database itself.
- **It works over HTTP too.** `mcp-stdio --url http://host:8080/mcp --wing
  wing_acme` (with `--token` for a multi-tenant server) bridges any endpoint,
  which is the escape hatch for a client that only supports stdio transport.
- **The registration can be project-scoped.** `--wing` becomes
  `X-Agentsmemory-Wing` on every forwarded request, over HTTP or a socket. A tool
  call can still pass `wing: "*"` when it deliberately needs every project.
- **`AGENTSMEMORY_SOCKET` configures both halves** — the server's listen path and
  the bridge's dial path — so the pair cannot drift apart.
- **`AGENTSMEMORY_WING` configures the bridge's `--wing` value** when a process
  manager supplies registration scope through the environment:

  ```bash
  AGENTSMEMORY_WING=wing_acme
  ```
- **Socket paths are short.** The kernel caps them near 104 bytes (macOS) or 108
  (Linux); a deeply nested path fails to bind with a bare `invalid argument`.

### The compose files, and which one you want

Five files, composed rather than copied: every stack is the base plus zero or
more overlays, so there is no variant to keep in sync with another.

| File | What it is | When |
|------|-----------|------|
| `docker-compose.yml` | the base — **one** container, embedded chromem index, one volume | one person, one machine. The default, and it stays the default |
| `docker-compose.full.yml` | overlay: Qdrant + a cross-encoder | recall precision matters more than dependency count, or several machines share an index. **This is what we run** |
| `docker-compose.ollama.yml` | overlay: the embedder in the stack, *and* points the server at it | you have no Ollama on the host. A profile could start the container but not repoint the server, which is why it is a file |
| `docker-compose.host.yml` | overlay: host networking — **Linux only** | Ollama on `127.0.0.1:11434` with no `OLLAMA_HOST=0.0.0.0` and no `host.docker.internal`. Does nothing on macOS/Windows, where containers live in a VM |
| `docker-compose.prod.yml` | hosted multi-workspace mode: dashboard, OAuth, billing | you are running the SaaS shape rather than a personal palace |

Overlays stack. `-f docker-compose.yml -f docker-compose.full.yml -f
docker-compose.ollama.yml` is a legitimate combination and means all three.

**Every `-f` flag, every time.** An overlay alone is not a complete stack, and
leaving one off does not fail — it starts a valid *different* stack. Either repeat
the flags, or fix them once per shell with
`export COMPOSE_FILE=docker-compose.yml:docker-compose.full.yml`, or put that same
line in a `.env` beside the compose files to make it this directory's default.

The image every one of them builds comes from the `Dockerfile` at the repo root.

### Docker Compose (one container)

```bash
cp .env.docker.example .env.docker   # point OLLAMA_URL at your Ollama
docker compose up -d

# then point your agents at it — the kit does the whole wiring, not just the MCP
aiagentmemory install --local --agent all     # or: claude | codex | cursor | pi
```

`--local` is what tells the kit the server is yours: it registers
`http://localhost:8080/mcp`, prompts for no token, and installs globally unless
`--sandbox`/`--config-dir` says otherwise. Registering the MCP by hand
(`claude mcp add --transport http agentsmemory http://localhost:8080/mcp`) still
works and gives you the tools — but not the protocol, the hooks or the subagent
definition, which is [the difference between a server and a habit](#the-server-is-inert-without-the-protocol).

Brings up `--local` with the embedded chromem index, so the whole stack is **one
container and one volume** — `/data/agentsmemory.db` (truth) and
`/data/agentsmemory.chromem` (index) live side by side in it. Ollama is
deliberately **not** a service: most people already run one, and a second copy
would re-download gigabytes of models — so `.env.docker.example` covers both
`host.docker.internal` (Ollama on your machine) and a URL for a remote box.
Both of the things that bite are on the Ollama side, and both are handled in
[Preparing Ollama](#preparing-ollama-embeddings): it binds `127.0.0.1` by
default, which a container cannot reach, and the model must be pulled first.

The port is published as `127.0.0.1:8080:8080`, and the loopback prefix is
load-bearing for the same reason as above — plain `8080:8080` would offer an
unauthenticated memory server to your whole network. Inside the container the
process binds `:8080` (a published port cannot reach a loopback-bound process),
so it logs the non-loopback warning on boot; there, the published interface is
the boundary, and the warning is expected.

### The stack this project runs on

Everything above is a menu. This is the setup the maintainers actually run, and
the one every measurement in `docs/adr/` was taken against — the base file plus
the full-quality overlay, with Ollama on the host:

```bash
# 0. once: an embedder on the host, and the model pulled (see Preparing Ollama)
ollama pull bge-m3

# 1. the stack
cp .env.docker.example .env.docker      # point OLLAMA_URL at your Ollama
docker compose -f docker-compose.yml -f docker-compose.full.yml up -d

# 2. your agents — all four, or name one
aiagentmemory install --local --agent all
```

Three containers: the server, Qdrant, and the cross-encoder. First boot pulls the
reranker model into a volume, so give it a few minutes and watch
`docker compose ... logs -f reranker` if you are impatient.

**Verify it, rather than assume it.** The server prints what it actually resolved,
which is the only thing that says the overlay applied:

```bash
docker compose -f docker-compose.yml -f docker-compose.full.yml logs agentsmemory | grep 'ranking:'
# ranking: fusion=rrf lex-weight=n/a lex-norm=n/a closet-boost=0.00 rerank=on(pool=10,weight=0.50) unit=memory evidence=lexical
```

`rerank=on(...)` is the line to look for. If it says `rerank=off`, the overlay did
not apply — you almost certainly dropped one of the two `-f` flags, which starts
a *valid* base stack rather than failing. `unit=memory` is the served ranking
unit. `evidence=lexical|semantic` is the resolved `MEMORY_EVIDENCE_SELECTOR`.

**Redeploying after a change**, including proving the running binary carries it:

```bash
scripts/redeploy.sh
```

It refuses to report success on a build it cannot verify: it runs the suite before
building, greps the binary *inside the running container* for strings the change
introduced, compares its digest against the image just built, runs one real search
through `/mcp`, and checks the installed client kit against the checkout. A build's
success is a claim about the build; this reads the artifact that is serving.

### Docker Compose (the full-quality stack)

When recall quality matters more than dependency count, a second overlay swaps
the in-process index for Qdrant and adds a cross-encoder in front of the results:

```bash
docker compose -f docker-compose.yml -f docker-compose.full.yml up -d

# switching an existing install: replay the stored vectors into Qdrant once.
# Nothing is re-embedded — SQLite is the source of truth.
docker compose -f docker-compose.yml -f docker-compose.full.yml run --rm agentsmemory sync
```

That is three services: the server, **Qdrant** as the search index, and a
**cross-encoder** (`bge-reranker-v2-m3`, served by llama.cpp) that rescores the
top `RERANK_POOL` candidates of every search before the page is cut — **10** in
this overlay, not the flag's own default of 50. Cost is linear: the cross-encoder
scores every pair in a full forward pass, measured on this stack at ~435ms per
document, so 50 candidates is ~22s and MCP clients give up long before the
server's budget does. (This sentence said 50 while the overlay shipped 10, which
is the drift `TestDocumentedEnvVarsAreRead` exists to catch one layer down.) The embedder scores a
drawer against a query it never saw; the cross-encoder reads the pair together,
which is the sharper judgement — and it only runs over what hybrid ranking
already surfaced, so the cost is bounded per search rather than per palace. If it
is down, search falls back to the fused vector+BM25 order instead of failing.

#### No Ollama on the host (`docker-compose.ollama.yml`)

If you have no Ollama at all, a third file adds it — and, unlike a profile,
points the server at it:

```bash
docker compose -f docker-compose.yml -f docker-compose.full.yml \
               -f docker-compose.ollama.yml up -d
```

It pulls the embedding model on first boot and reports healthy only once that
model exists, so `up -d` really is the whole setup and nothing starts before it
can embed. On macOS and Windows prefer a host install where you have one: a
containerised Ollama cannot reach Metal and runs on CPU.

Going back is dropping the second `-f` — the chromem index is still on the
volume, and the server refills it from SQLite if it is not.

**On Linux**, an override removes the Ollama friction entirely:

```bash
# start (both -f flags, every time — the override alone is not a complete stack)
docker compose -f docker-compose.yml -f docker-compose.host.yml up -d

# follow the logs / stop
docker compose -f docker-compose.yml -f docker-compose.host.yml logs -f agentsmemory
docker compose -f docker-compose.yml -f docker-compose.host.yml down
```

Repeating both files gets tedious, so either export it once per shell —
`export COMPOSE_FILE=docker-compose.yml:docker-compose.host.yml`, after which
plain `docker compose up -d` uses both — or put that same line in a `.env` beside
the compose files to make it the permanent default for this directory.

Without compose at all, the equivalent single container (the embedded index, so
nothing else needs to run):

```bash
docker build -t agentsmemory:local .
docker run -d --name agentsmemory --network host --restart unless-stopped \
  -v agentsmemory-data:/data \
  -e VECTOR_BACKEND=chromem -e OLLAMA_URL=http://localhost:11434 \
  agentsmemory:local serve --local --addr 127.0.0.1:8080 --db /data/agentsmemory.db
```

`network_mode: host` puts the container in the host's network namespace, so
`localhost:11434` inside it *is* your machine's loopback — Ollama works on its
default `127.0.0.1` bind, with no `OLLAMA_HOST=0.0.0.0` and no
`host.docker.internal`. It is Linux-only: Docker Desktop on macOS and Windows
runs containers in a VM, where host networking is an opt-in feature (4.34+)
rather than the default. Note that host networking **ignores `ports:`**, so the
loopback publish stops protecting anything and the server's own `--addr
127.0.0.1:8080` becomes the boundary — which is exactly what the override pins.
The embedded index needs no network at all, so nothing else changes; a Qdrant
service, if you uncomment one, stays on the bridge network and is reached through
its published loopback port.

### Hosted mode & billing (`docker-compose.prod.yml`)

The stack above is the self-hosted `--local` mode. The hosted service — the
multi-tenant shape behind aiagentmemory.dev, with dashboard, auth and billing —
is the same image running its default `serve` command, one persistent volume,
and the full `.env.example` configuration behind a TLS reverse proxy:

```bash
cp .env.example .env.prod          # session key, OAuth, billing — all required
docker compose -f docker-compose.prod.yml up -d
```

The port is published on the host loopback only; put Caddy/nginx/Traefik in
front and forward 443 → 127.0.0.1:8080. Billing is single-provider per
deployment and inert until configured. With OpenCollective — a donations
platform, so the checkout is a hosted contribution page:

```bash
BILLING_PROVIDER=opencollective
OPENCOLLECTIVE_CHECKOUT_PRO_MONTHLY=https://opencollective.com/it-uoga/projects/ai-agents-memory/contribute/pro-monthly-104934/checkout
OPENCOLLECTIVE_CHECKOUT_PRO_ANNUAL=https://opencollective.com/it-uoga/projects/ai-agents-memory/contribute/pro-yearly-104935/checkout
OPENCOLLECTIVE_PROJECT_URL=https://opencollective.com/it-uoga/projects/ai-agents-memory
# Activation: OpenCollective does not SIGN its webhooks, so the server asks
# instead of waiting to be told — it polls the authenticated GraphQL API and
# reconciles contributions into plan changes (ADR-042).
OPENCOLLECTIVE_PERSONAL_TOKEN=oc_pat_...          # read-only; this is the switch
OPENCOLLECTIVE_COLLECTIVE_SLUG=ai-agents-memory
OPENCOLLECTIVE_RECONCILE_INTERVAL=15m             # optional, this is the default
```

A paid contribution started from the dashboard's Upgrade button is attributed
back to its workspace and activated within one interval. Two cases still need a
human, by design rather than omission: a contribution made outside that button
carries no attribution, and one whose payer cannot be matched is counted and
logged rather than guessed at. Both — and any deployment that leaves
`OPENCOLLECTIVE_PERSONAL_TOKEN` unset, which turns polling off entirely — are
settled with:

```bash
agentsmemory set-plan --slug <workspace> --plan pro_monthly
```

**CI deploy (opt-in).** Tagging `vX.Y.Z` already builds binaries and the GHCR
image; a third workflow (`.github/workflows/deploy.yml`) additionally rolls
that exact image to a host over SSH. Enable it by setting the repository
variable `DEPLOY_ENABLED=true` and the secrets `DEPLOY_HOST`,
`DEPLOY_USER`, `DEPLOY_SSH_KEY` (optional `DEPLOY_PORT`, default 22;
`DEPLOY_DIR`, default `/opt/agentsmemory`). The host needs Docker and a
one-time `docker-compose.prod.yml` + `.env.prod` — the workflow fetches the
compose file from this repo if missing, and `.env.prod` (session key, OAuth,
`BILLING_PROVIDER` + `OPENCOLLECTIVE_*`) stays operator-managed. The rollout
waits for the tag's image to finish building (it publishes in the parallel
release workflow), then swaps the container, probes `/healthz`, and rolls back
to the previously-running tag if the new one never answers. Until enabled, the
workflow is skipped, never failed.

**PR test images.** `.github/workflows/pr-image.yml` builds same-repository PRs
automatically without moving a release tag or `latest`. A maintainer can also
run it manually with a PR number and its reviewed 40-character head SHA,
including for a fork PR; the run fails if that PR head moved. Preview versions
live in the separate `agentsmemory-pr` GHCR package. The run summary reports
both a readable tag of the form
`pr-24-sha256-<64-hex-container-digest>` and the canonical
`ghcr.io/atvirokodosprendimai/agentsmemory-pr@sha256:<digest>` reference.
Deploy the canonical digest so the tested bytes cannot move underneath the host:

```bash
AGENTSMEMORY_IMAGE='ghcr.io/atvirokodosprendimai/agentsmemory-pr@sha256:<digest>' \
  docker compose -f docker-compose.prod.yml up -d
```

`AGENTSMEMORY_IMAGE` is a one-command override; omit it to return to the normal
`AGENTSMEMORY_IMAGE_TAG`/`latest` path. A daily cleanup deletes GHCR versions
whose complete tag set consists only of PR digest tags and whose creation time
is more than seven days old. Because cleanup runs daily, practical retention is
about seven to eight days. Release-tagged and `latest` versions do not match the
cleanup filter.

### Choosing the index

`VECTOR_BACKEND` (or `--vector-backend`) picks what answers searches. SQLite is
written either way, so this is never a decision about your data — switching costs
an index rebuild, never a re-embedding:

| Value | What runs | Choose it when |
|---|---|---|
| `chromem` *(default with `--local`)* | nothing extra — an in-process index, held in memory, persisted to `<db>.chromem/` | self-hosted on one machine |
| `sqlite` *(default otherwise)* | nothing at all — the source of truth scans its own vectors per query | you want the smallest possible footprint |
| `qdrant` | a separate Qdrant service | the palace outgrows memory, or several machines share one index |

The chromem index is derived and disposable: delete the directory and the server
refills it from SQLite on the next boot, logging `rebuilt namespace … from the
SQLite source of truth`. Switching *to* Qdrant is the one case that needs a
command — `agentsmemory sync` — because refilling a remote index at boot would
mean blocking startup on a service that may be down.

### Backing up the SQLite volume

**Back up SQLite; ignore the index.** SQLite is the source of truth and every
index is rebuildable from it — a chromem directory refills itself on the next
boot, and `agentsmemory sync --recreate` replays every vector into Qdrant without
re-embedding. Losing an index costs a restart or one command, not your memory.

The volume name is `<project>_agentsmemory-data`, where the project is `name:` in
`docker-compose.yml` — so `agentsmemory_agentsmemory-data` by default. Confirm
with `docker volume ls`.

**Stop and copy** — simplest and unconditionally consistent. A single-user server
will not miss two seconds:

```bash
docker compose stop agentsmemory
docker run --rm -v agentsmemory_agentsmemory-data:/data -v "$PWD:/backup" \
  alpine:3.20 tar czf "/backup/agentsmemory-$(date +%F).tar.gz" -C /data .
docker compose start agentsmemory
```

**Hot backup** — no downtime, using SQLite's online backup API. The runtime image
is bare Alpine with no `sqlite3`, so borrow one:

```bash
docker run --rm -v agentsmemory_agentsmemory-data:/data -v "$PWD:/backup" alpine:3.20 \
  sh -c 'apk add --no-cache sqlite >/dev/null &&
         sqlite3 /data/agentsmemory.db ".backup /backup/agentsmemory-$(date +%F).db"'
```

Do **not** just `cp` the database file while the server is running. `.backup`
(and `VACUUM INTO '/backup/out.db'`, which additionally compacts) coordinate with
writers; a plain copy can catch a write mid-transaction and hand you a file that
only fails later, when you need it.

**Verify** the backup before you trust it — an unreadable backup discovered at
restore time is not a backup:

```bash
sqlite3 agentsmemory-2026-08-16.db "PRAGMA integrity_check; SELECT count(*) FROM drawers;"
```

**Restore** into a fresh volume:

```bash
docker compose down
docker volume rm agentsmemory_agentsmemory-data
docker volume create agentsmemory_agentsmemory-data
docker run --rm -v agentsmemory_agentsmemory-data:/data -v "$PWD:/backup" alpine:3.20 \
  sh -c 'tar xzf /backup/agentsmemory-2026-08-16.tar.gz -C /data && chown -R 10001:10001 /data'
docker compose up -d
docker compose exec agentsmemory agentsmemory sync --recreate   # only if using Qdrant
```

⚠ The `chown` is not optional. The image runs as uid **10001**, while the restore
container writes as root — skip it and the server starts, then fails on the first
write to a database it cannot open. Restoring a single `.db` file instead of the
tarball needs the same treatment.

### The server is inert without the protocol

Connecting the MCP gives your agent 41 tools and **no reason to call any of
them**. Nothing about a tool catalogue tells an agent to recall before it acts or
to write down what it learned; without that instruction the memory simply never
gets opened. Delegation comes in three layers, and self-hosting only gets you the
first one for free:

| Layer | What it does | How you get it |
|---|---|---|
| `am_skillset` | Server-side wakeup playbook — which tool, in what order — returned over MCP itself | **Automatic.** Seeded on first boot, including `--local` |
| `CLAUDE.md` / `AGENTS.md` | The always-on protocol: recall at session start, persist before stopping | `aiagentmemory install` writes `agentsmemory-bootstrap.md` and merges an import into your memory file |
| `/am`, `/load-skill` + Claude's six hooks | Task-scoped grounding, the end-of-turn checkpoint that stops memory being lost, a `UserPromptSubmit` recall that asks the palace about each task in the user's own words, and the two that make a Claude SUBAGENT a session: `SubagentStart` puts the recall instruction next to its task, `SubagentStop` asks it for what it found. Codex currently installs only the proven `Stop` hook. | Same installer |

So after `docker compose up`, run the kit as well — `--local` wires it to your
own server:

```bash
aiagentmemory install --local
```

That points the MCP at `http://localhost:8080/mcp`, never asks for a token (and
drops any `AGENTSMEMORY_TOKEN` it finds in your environment, rather than writing
a credential into a config where it would imply the server checks one), and
installs globally without the interactive global-vs-sandbox prompt — a
self-hoster is setting up their own machine, so that question has an obvious
answer. An explicit `--sandbox <name>` still wins if you want a local server in
an isolated config, and `--mcp-url` overrides the endpoint for a server on
another port or host.

The registration it writes carries no `Authorization` header at all, on all three
agents: Claude stores a bare `{"type":"http","url":...}`, codex registers the URL
with no bearer-token variable and no token file, and pi's bridge extension gets
`AGENTSMEMORY_LOCAL=1` so it treats the missing token as intentional and connects
anyway instead of reporting "memory tools are off".

---

## Connect Claude Code, Codex, Cursor or pi (the `aiagentmemory` kit)

The `aiagentmemory` binary wires [Claude Code](https://claude.com/claude-code),
[Codex](https://developers.openai.com/codex), [Cursor](https://cursor.com) or
[pi](https://pi.dev) into your workspace: it registers the agentsmemory MCP,
installs the always-on memory protocol, and adds whatever else that agent can
take — slash commands, lifecycle hooks, a subagent definition whose tool
allowlist names the `am_*` tools. It replaces the old shell installer; everything
ships in one downloadable binary.

```bash
aiagentmemory install                          # claude, the default
aiagentmemory install --agent claude           # the same, said out loud
aiagentmemory install --agent codex
aiagentmemory install --agent cursor
aiagentmemory install --agent claude-desktop
aiagentmemory install --agent pi
aiagentmemory install --agent both             # claude + codex
aiagentmemory install --agent all              # all five
```

`--agent claude-desktop` needs the **server binary on this machine** — it registers
a `mcp-stdio` bridge, and a Docker-only install produces no host binary. The
install refuses rather than writing a command that is not there:

```bash
go build -o ~/.local/bin/aiagentmemory-server ./cmd/server
```

### What each agent actually gets

The agents do not offer the same surfaces, and the kit installs what each one has
rather than pretending. Everything below is measured against a real install, not
inferred from documentation.

| | **claude** | **codex** | **cursor** | **claude-desktop** | **pi** |
|---|---|---|---|---|---|
| config dir | `~/.claude` | `~/.codex` | `~/.cursor` | `~/Library/Application Support/Claude` | `~/.pi/agent` |
| MCP registration | `claude mcp add` | `codex mcp add` | **writes `mcp.json`** — Cursor ships no `mcp add` | **writes `claude_desktop_config.json`**, spawning `mcp-stdio` | bridge extension |
| memory protocol | `CLAUDE.md` + `@import` | inlined in `AGENTS.md` | `rules/agentsmemory.mdc`, `alwaysApply: true` | **the MCP handshake** — it can hold no file | inlined in `AGENTS.md` |
| slash commands | `/M`, `/am`, `/load-skill` | `/prompts:M`, … | **none** — no commands dir | **none** | `/M`, … |
| Stop checkpoint | ✅ | ✅ — native TOML in `config.toml` | ❌ hook shape not established | ❌ | in the extension |
| `SessionStart` / `SessionEnd` | ✅ — two hooks share `SessionStart`: one verifies anchored memories, one performs a recall for the branch and injects it so a fresh context does not start blind (ADR-041). ⚠ `SessionEnd` is **not registered on Windows**: process creation costs ~1s there, the hook needs ~3.2s, and it loses the teardown race, so every exit reported `Hook cancelled` (#150). Ask the server for the same numbers instead — `curl -fsS "${AGENTSMEMORY_MCP_URL%/mcp}/stats?hours=2"` (the hooks read `AGENTSMEMORY_MCP_URL`; `AGENTSMEMORY_URL` is the proxy origin and is unset in an ordinary install). An existing registration from an earlier install is retired on upgrade. On macOS and Linux the hook completes well inside teardown and stays registered; `AGENTSMEMORY_STATS=off` turns the end-of-session report off there without touching the other hooks (the 3,210ms → 634ms figures are from the Windows host that reported #150) | ❌ not registered; not part of the Codex subagent audit | ❌ | ❌ | ❌ |
| `SubagentStart` / `SubagentStop` | ✅ | ❌ events exist; [payload, feedback, and retry contracts remain to measure](docs/adr/BACKLOG.md) | ❌ | ❌ | ❌ |
| subagent definition | `agents/*.md` | `agents/*.toml` | `agents/*.md` | ❌ | ❌ no subagent system |
| `--wing` registration scope | ✅ header | ✅ URL query | ✅ header | ✅ `mcp-stdio --wing` | ✅ bridge env |
| `--sandbox` isolation | ✅ | ✅ | **refused** — no config-dir variable | **refused** — same reason | ✅ |
| needs a host server binary | ❌ | ❌ | ❌ | **✅ — the stdio bridge** | ❌ |

Two things worth reading twice:

- **Cursor needs one manual step the installer deliberately does not take.**
  Cursor gates every MCP server behind an approval that is stored outside
  `mcp.json`, so a registered-but-unapproved server looks identical on disk to a
  working one. Run `cursor-agent mcp enable agentsmemory` once. The install prints
  this line every time, because a re-install cannot tell whether you have done it.
- **Claude and Codex get the write half.** The Stop checkpoint and the subagent hooks
  are what ask an agent to persist what it learned. Codex gets the Stop checkpoint
  as a native `config.toml` hook; Cursor and Claude Desktop get none. Those agents
  recall memory and are never prompted to write it — see
  [ADR-017](docs/adr/ADR-017-a-subagent-is-a-session.md) for why the advisory half
  of a loop does not happen on its own.
- **Every client is told the rules on connection**, whether or not a kit could
  install a protocol file for it. The server returns `instructions` in the MCP
  `initialize` response — recall before acting, check `am_status` once, inherit a
  named `default_wing`, and use `wing: "*"` only for a deliberate cross-project
  search. That rule is there because a client without it invented an empty
  namespace and proposed searching every project on every recall
  ([ADR-021](docs/adr/ADR-021-the-handshake-carries-the-protocol.md)).

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
MCP and the codex review plugin. Preview any run with `--dry-run` — it prints
every file write and command without touching anything.

### Two ways to install

| Mode | Command | What it does |
|------|---------|--------------|
| **Global** | `aiagentmemory install` | Wires the MCP, commands, all five hooks and the shipped subagent definition into the global `~/.claude`. Wraps the Claude you already run. |
| **Sandboxed** | `aiagentmemory install --sandbox <name>` | Installs a self-contained config under `~/.sandboxes/<name>`, isolated from every other project and from the global `~/.claude`. |

Sandboxing works by pinning the agent's own config-dir variable
(`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PI_CODING_AGENT_DIR`) at launch. **Cursor
exposes no such variable**, so `--agent cursor --sandbox x` is refused rather than
writing a complete kit into a directory Cursor will never open.

### Command options

Every flag `install` takes. `aiagentmemory install --help` is the authority — this
table is checked against it.

| Flag | What it does |
|------|--------------|
| `--agent <name>` | `claude` (default) · `codex` · `cursor` · `claude-desktop` · `pi` · `both` (claude+codex) · `all` |
| `--global` | install into the agent's global config dir without the mode prompt |
| `--sandbox <name>` | install into an isolated config at `~/.sandboxes/<name>` |
| `--config-dir <dir>` | install into an explicit directory instead |
| `--local` | wire up a self-hosted `agentsmemory --local` server (`http://localhost:8080/mcp`); no token is prompted for |
| `--token <key>` | bearer token to present — hosted workspace key, or the one `--local` was started with. `$AGENTSMEMORY_TOKEN`, `$AGENTSMEMORY_LOCAL_TOKEN` |
| `--mcp-url <url>` | the MCP endpoint (default the hosted service) |
| `--socket <path>` | register over stdio against a `--local` server on a Unix socket; requires `--local` |
| `--server-bin <path>` | server binary the `--socket` stdio bridge spawns |
| `--wing <name>` | scope this registration to one project on every MCP call. The installer uses each client's supported channel: HTTP header (Claude/Cursor), URL query (Codex), bridge flag (Desktop/socket), or pi environment. `wing: "*"` remains an explicit per-call cross-project opt-in |
| `--scope <scope>` | Claude MCP/plugin scope: `user` (default) · `local` · `project` |
| `--recommended` | also install codebase-memory MCP and the codex review plugin |
| `--copy` | seed a sandbox from the agent's global config (logins, MCP servers, plugins, settings). Needs `--sandbox`/`--config-dir` |
| `--shared-auth` | link the sandbox's credential files to the global config, so one login serves every sandbox |
| `--claude-bin` / `--codex-bin` / `--pi-bin` | override the agent CLI to drive |
| `--dry-run` | print every file write and command without touching anything |
| `--yes`, `-y` | never prompt |

Other subcommands: `verify` (check memories still match the code they describe),
`mine-claude`, `update` (the binary), `update-skill` (the protocol and commands),
`init` / `load` (record and launch a project's agent), `run` / `wrap` (drive an
agent against a sandbox or the global config), `mcp` (call a read-only memory tool
from the shell).

### How memory arrives: three layers, three fill mechanisms

The palace has three layers, and **they do not fill the same way**. Nothing
breaks if you do not know that, but a seeded palace will answer recall questions
well and graph questions with nothing, which reads as the graph being broken when
it is simply empty.

| Layer | Filled by | Automatic? |
|---|---|---|
| Drawers + closets (recall) | every write: `am_add_drawer`, `am_diary_write`, `mine-claude` | ✅ |
| Hallways and tunnels (navigation) | derived on write from rooms and entities | ✅ |
| **Knowledge graph** (subject→predicate→object) | **`am_kg_add` by an agent, or `agentsmemory kg-extract`** | ❌ **never automatic** |

**Mining does not produce facts.** `mine-claude` files transcripts as drawers; it
writes no triples. An agent following the memory protocol calls `am_kg_add` as it
works, which is the ordinary way the graph fills. To extract facts from a corpus
you already mined, run `agentsmemory kg-extract --wing <wing>`.

⚠ **`kg-extract` needs a GENERATIVE model, and the compose overlay does not
provision one.** `docker-compose.ollama.yml` pulls the embedder (`bge-m3`) and
nothing else — an embedder cannot answer `/api/generate`, so `kg-extract` fails
every source with `model not found` until you supply one:

```bash
docker compose exec ollama ollama pull qwen2.5-coder:7b   # multi-GB, one time
docker compose exec agentsmemory agentsmemory kg-extract --wing wing_acme
```

Use `--gen-model` (or `EVAL_GEN_MODEL`) to name a model you already have. The
same model serves `agentsmemory eval` and `agentsmemory longmemeval`.

### `agentsmemory longmemeval` — does our writing advice actually help?

Every other eval here scores ranking against questions generated from our own
drawers. `longmemeval` scores **judged answer accuracy** over a (write-policy ×
query-policy) grid on [LongMemEval-S](https://github.com/xiaowu0162/LongMemEval),
a corpus written by people who have never seen this codebase — which is the
property a self-derived corpus can never have.

```bash
# the dataset is third-party data with its own licence; fetch it yourself.
# upstream ships it as `longmemeval_s`, with no .json extension.
agentsmemory longmemeval --data longmemeval_s \
  --write verbatim,question-first \
  --query verbatim,named-thing \
  --n 3 --out smoke.cells.json
```

That is a **smoke test** — four cells at `--n 3`, about 50 minutes. It answers
"is my stack wired up", not "which policy wins". Size a real run from the table
below before you start one, because the honest ones are measured in hours.

It needs the **same generative model** as `kg-extract` and `eval`, for the reader
and the judge alike, and it writes into throwaway scratch scopes — one per
(cell, question), so no question can retrieve another's history.

#### What the corpus is

Measured over the published file, not quoted from the paper:

| | |
|---|---|
| Questions | 500, across 6 question types |
| Largest type / smallest | `multi-session` and `temporal-reasoning` (133 each) / `single-session-preference` (30) |
| Sessions in the haystack | 25,112 total — median **50** per question (39–66) |
| Session length | median **10,012** characters, p90 16,792, max 78,117 |
| Sessions holding the answer | median **2** per question (max 6) |

The session length is why `--context-runes` defaults to 24,000 and why you should
not lower it casually: a budget under the median cannot fit one whole session, so
the verbatim baseline assembles nothing and scores 0 **by construction** — every
other policy then looks good against a baseline that could not play.

#### What a run costs, and how to pick `--n`

One (cell × question) takes **≈ 4m15s**, measured 2026-09-03 against a remote
Ollama (`bge-m3` embedding, `gpt-oss:20b` as reader and judge) with the
cross-encoder on. **About 85% of that is ingest** — 48 `Add` calls totalling 212s
for one question's haystack — so the cost tracks the *number of cells*, and the
model only sets the remaining ~40s.

Total ≈ `write policies × query policies × n × 4 minutes`:

| grid | `--n 3` | `--n 12` | `--n 50` |
|---|---|---|---|
| 2 × 2 (4 cells) | 50 min | 3.4 h | 14 h |
| 4 × 2 (8 cells) | 1.7 h | 6.8 h | 28 h |

⚠ **There is no fast configuration of this, and that is a property of the
benchmark.** Each question carries its own ~50-session haystack, which must be
embedded before anything can be retrieved from it, and isolation is per
(cell, question) so the same haystack is re-embedded for every cell. Concurrency
buys roughly 2× on a saturated GPU and nothing on a busy one — measured: 16
embed requests took 23.5s at 16 workers against ~50s sequential.

**So pick `--n` from what you are asking, not from your patience.** A run meant
to *decide* something needs the held-out split ADR-047 pre-registers, which means
n in the low hundreds and an overnight job; anything at `--n 20` or below is a
smoke test whatever its table says.

⚠ **A pilot run decides nothing.** At small `--n` a paired interval spans zero for
almost any real effect, so a neutral result is what the instrument says at that
size whether the writing rules work or not. ADR-047 records this as a property
rather than a caveat: no rule may be promoted *or retired* from a pilot.

⚠ **The shared context budget is counted in RUNES, not tokens** (`--context-runes`),
because this repository has no tokenizer. Each cell records the endpoint's own
reported prompt-token count beside it where the endpoint supplies one, so the
approximation is measured rather than assumed.

### Which ingest path do I want?

Both exist, they behave oppositely, and each is wrong for the other's job:

| | Stop hook (automatic) | `mine-claude` (manual) |
|---|---|---|
| Runs | at every session end | when you run it |
| Files | what the agent decided was worth keeping | the raw transcript text |
| Feeds the knowledge graph | ✅ the agent calls `am_kg_add` | ❌ drawers only |
| Cost | negligible | one embed per changed chunk |

The Stop hook is what keeps a palace current. `mine-claude` is a **backfill tool**
for history that predates the install — run it once over your transcripts, not on
a schedule. Re-running it is cheap for unchanged text (only changed chunks are
re-embedded) but it still does not write facts.

### Sandboxed installation (per-project isolation)

A **sandbox** is just a Claude config directory under `~/.sandboxes/<name>`.
Running Claude with `CLAUDE_CONFIG_DIR` pointed at it isolates that project's
slash commands, settings, MCP servers, and agentsmemory token from everything
else — so a client project and an internal project never share memory, tools, or
credentials. Set one up once, with or without the recommended tools:

```bash
aiagentmemory install --sandbox acme               # core: commands, hook, our MCP
aiagentmemory install --sandbox acme --recommended # + codebase-memory, codex review
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

### Read your memory from the shell

`aiagentmemory mcp` calls the memory tools yourself — same endpoint, same token,
same transport your agents use — so you can see exactly what a tool returns
without asking an agent to relay it:

```bash
aiagentmemory mcp                          # the tools you can call
aiagentmemory mcp status                   # workspace, wings, quota
aiagentmemory mcp search "auth bug" -a limit=3
aiagentmemory mcp search "auth bug" | jq '.hits[].room'
```

The bare positional fills the tool's first required argument; everything else is
`-a key=value`. Output is indented JSON on stdout (notes go to stderr, so it
pipes), and the workspace token is read from an install already on this machine —
`--sandbox <name>` picks one. It is **read-only**: the write tools exist on the
endpoint but the CLI refuses them, so a mistyped command can never mutate team
memory. Full flag reference in [`clients/claude-code/README.md`](clients/claude-code/README.md).

### Codex (`--agent codex`)

Codex is configured the same way Claude is, under different names, so the kit is
the same content in different places:

```bash
aiagentmemory install --agent codex                  # into ~/.codex
aiagentmemory install --agent both --sandbox acme    # one sandbox, both agents
aiagentmemory run --agent codex acme                 # launch codex with CODEX_HOME pinned
```

| | Claude Code | Codex |
|---|---|---|
| Config dir | `~/.claude` (`CLAUDE_CONFIG_DIR`) | `~/.codex` (`CODEX_HOME`) |
| Slash commands | `commands/*.md` → `/M`, `/am` | `prompts/*.md` → `/prompts:M`, `/prompts:am` |
| Always-on memory | `CLAUDE.md` + managed `@import` | `AGENTS.md` with the protocol inlined — codex has no `@import` |
| Stop hook | `settings.json` | native TOML in `config.toml`; an install retires its old `hooks.json` entry |
| MCP auth | `Authorization: Bearer <token>` header | `bearer_token_env_var = "AGENTSMEMORY_TOKEN"` |

On upgrade, the installer first lands the native TOML hook, then removes only
agentsmemory's entry from its previous `hooks.json` representation. Codex
supports both files but merges them and warns when one config layer uses both,
so the installer keeps a single representation for its own hook. It deletes the
JSON file when nothing else remains; foreign hooks are preserved with a warning,
so migration never erases configuration it does not own.

One thing codex needs that Claude does not is **the token in the environment** — it is written to
`<CODEX_HOME>/agentsmemory.env` (`0600`) and exported for you by
`aiagentmemory run --agent codex …`; for plain `codex`, source it from your shell
rc. A codex sandbox is a whole `CODEX_HOME`, so it also needs its own login:
`CODEX_HOME=~/.sandboxes/acme codex login`.

### Inheriting your global setup (`--copy`)

A new sandbox starts signed out, with no MCP servers, plugins or skills. `--copy`
seeds it from that agent's global config dir first:

```bash
aiagentmemory install --agent pi --sandbox acme --copy
```

Credentials, settings, `.claude.json` (Claude's MCP servers), plugins, skills,
extensions and prompts travel; conversation history, logs, `*.sqlite*` stores and
caches stay behind. Existing files in the target are never overwritten, modes are
preserved (`auth.json` stays `0600`) — and note the consequence: **the sandbox can
act as you** until you sign it out.

### Sharing one login (`--shared-auth`)

`--copy` snapshots credentials; `--shared-auth` links them, so a login in any
sandbox is a login everywhere:

```bash
aiagentmemory install --agent pi --sandbox acme --shared-auth
```

Claude on macOS already shares its keychain, so the flag is a no-op there; codex
links `auth.json`, pi links `auth.json` and `models-store.json`. If an agent ever
replaces the link with a real file, `aiagentmemory run` says so at launch and
prints the command that re-shares it.

### pi (`--agent pi`)

pi looks like codex — `prompts/` for commands, `AGENTS.md` for memory — except
that it ships **no MCP client and no hooks**, both by design. So the installer
writes a bridge extension into `<config dir>/extensions/agentsmemory.ts`: at
startup it handshakes with your workspace MCP, lists the tools, and re-registers
each one as a native pi tool, so `am_*` calls work unchanged. The same extension
fires the end-of-turn memory checkpoint that the Stop hook fires elsewhere.

```bash
aiagentmemory install --agent pi                   # into ~/.pi/agent
aiagentmemory install --agent all --sandbox acme   # one sandbox, all three agents
aiagentmemory run --agent pi acme                  # launch pi with PI_CODING_AGENT_DIR pinned
```

| | Codex | pi |
|---|---|---|
| Config dir | `~/.codex` (`CODEX_HOME`) | `~/.pi/agent` (`PI_CODING_AGENT_DIR`) |
| Slash commands | `prompts/*.md` → `/prompts:M` | `prompts/*.md` → `/M` |
| Stop hook | `config.toml` | none — the checkpoint ships in the extension |
| MCP | native, `--bearer-token-env-var` | bridged by the extension |

The token and endpoint are written to `<config dir>/agentsmemory.env` (`0600`)
and exported by `aiagentmemory run --agent pi …`. A pi sandbox is the whole agent
dir including `auth.json`, so it starts with no provider credentials.
`--recommended` adds nothing for pi: codebase-memory is a stdio MCP and the
codex review plugin is a Claude marketplace.

---

## Configuration

All flags have sensible local defaults:

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `:8080` (`127.0.0.1:8080` with `--local`) | HTTP / MCP listen address |
| `--local` | `false` | Self-hosted single-workspace mode: one `local` workspace, unauthenticated `/mcp`, no dashboard |
| `--token` | *(empty)* | `--local` only (`AGENTSMEMORY_LOCAL_TOKEN`): require this bearer on `/mcp` and `/import`, so the server can safely bind a LAN address |
| `--db` | `agentsmemory.db` | SQLite database path |
| `--vector-backend` | `sqlite` (`chromem` with `--local`) | Search index: `sqlite` \| `chromem` \| `qdrant` — SQLite is always the source of truth |
| `--qdrant-url` | `http://localhost:6333` | Qdrant base URL |
| `--qdrant-api-key` | *(empty)* | Qdrant API key (optional) |
| `--ollama-url` | `http://localhost:11434` | Ollama base URL |
| `--ollama-model` | `bge-m3` | Embedding model (1024-dim) |
| `--rerank-url` | *(empty)* | `RERANK_URL` — TEI base URL for cross-encoder re-ranking. Empty disables it |
| `--rerank-pool` | `50` | `RERANK_POOL` — candidates cross-encoded per search (ignored without `--rerank-url`) |
| `--retrieve-k` | `0` | `RETRIEVE_K` — floor on how many distinct memories Search retrieves before ranking, independent of the page. `0` (default) uses the formula: `limit×3`, raised to `--rerank-pool` when a cross-encoder will run. Does not change the page size |
| `--memory-evidence-selector` | `lexical` | `MEMORY_EVIDENCE_SELECTOR` — bounded reranker evidence: literal query coverage or query-time semantic passage selection |
| `--otel-endpoint` | *(empty)* | `AGENTSMEMORY_OTEL_ENDPOINT` — OpenTelemetry export: empty=off, `stdout` prints a compact stage tree to stderr, otherwise an OTLP HTTP collector URL (`http://localhost:4318`). Does not change search results |

### Observability

Runtime execution is OpenTelemetry (ADR-025): one parent span per `am_search` (`am.search`) with child stages `embed`, `retrieve` (`hydrate` nested), `collapse`, `closet`, `fusion`, `recency`, `rerank` (`evidence` nested when a reranker is configured), `record`. Each stage reports `ran` / `bypassed` / `failed_open` / `failed_closed`. MCP tools emit `am.tool`. Outbound HTTP (embed, rerank, Qdrant) and inbound `/mcp` are wrapped. Eval wraps each question in `am.eval.case` and each ranking arm in `am.eval.arm` so an ablation is a tree, not a forest.

The dump is a debug tool, not a JSON firehose. Every span carries `am.code.file` / `am.code.line` / `am.code.func` (the `Start` call site). A bypassed or failed stage carries a closed `am.reason` (`scale_zero`, `weight_zero`, `band_zero`, `no_reranker`, `skip_sqlite`, `empty`, `lexical`, `error`, …). Retrieve emits a `widen` event per doubling round (`k`, `hits`, `distinct`, `stop`). The `am.search` parent repeats the resolved knobs (`am.fusion`, `am.closet_scale`, `am.rerank_configured`, …) beside `am.profile_id`, so you can read a tree against `RankingProfile()` / `am_status` and see whether a stage was eligible, whether it ran, and which line of code started it.

`--otel-endpoint stdout` prints that tree on stderr:

```
am.search  5040ms  ran  profile_id=fusion=rrf… closet_scale=0 rerank_configured=true  internal/palace/service.go:961
  am.search.embed  153ms  ran  dim=1024  internal/palace/service.go:964
  am.search.retrieve  42ms  ran  reason=exhausted  k=50 rounds=1  internal/palace/memory_search.go:69
    · widen k=50 hits=3 distinct=1 stop=exhausted
    am.search.hydrate  12ms  ran  count=3  internal/palace/memory_search.go:70
  am.search.closet  0ms  bypassed  reason=scale_zero  scale=0  internal/palace/mine.go:320
  am.search.record  0ms  bypassed  reason=skip_sqlite
```

A closet span that says `ran` while `closet_scale=0` (or `bypassed` with no `am.reason`) means the stage is not wired. OTLP to a collector keeps the same attributes for Jaeger; stdout is the local join to source.

`search_id` is the SQLite `search_events.id` so a sampled trace can join a durable relevance row. Wing and room appear as booleans (`am.has_wing`, `am.has_room`), never names. Raw queries, memory content and tenant ids are not metric labels.

A collector that is down drops observability, not search. `SkipTelemetry` still skips the SQLite log (eval); OTEL spans are always created and hit the noop provider when `--otel-endpoint` is empty. Each stage also increments unsampled `eligible` / `selected` / `effect` / `fallback` counters.

### Memory-level ranking

Vector candidate capacity, BM25 and the optional cross-encoder operate once per
logical memory. Chunk storage and chunk embeddings remain unchanged: the best
passage nominates a memory, the vector prefix widens until it holds the target
number of distinct memories, and ranking uses reassembled memory evidence.

`MEMORY_EVIDENCE_SELECTOR=lexical` is the default control. `semantic` reuses the
raw query embedding, embeds overlapping windows from the whole reassembled long
memory, and selects up to four distant passages within the same 1600-rune
cross-encoder budget. Short memories pass through unchanged. Any
passage-embedding failure falls back to lexical evidence for the whole
shortlist. Passage requests stay bounded at 128 inputs. With the TEI embedding
backend, the client reads `/info.max_client_batch_size` (caching only a
successful probe, and retrying after a backoff otherwise) and uses the server's
real limit up to that bound; if `/info` is unavailable it retains TEI's safe
32-input default. The semantic arm adds embedding latency but no migration;
roll it back by setting the selector to `lexical`. It is inert unless
`RERANK_URL` is set. The resolved profile reports `unit=memory` and
`evidence=lexical|semantic`. The process environment can override an `.env`
file, so that profile—not the file—is the authority.

### Cross-encoder re-ranking (optional)

`am_search` fuses vector similarity, BM25 and the closet boost. All three are
*proxies* — they score the query and the drawer separately and combine the
numbers. A cross-encoder reads both together, so it judges relevance far better;
it is also far slower, which is why it only ever sees a shortlist.

There is deliberately no `RERANK_MODEL` service setting. TEI fixes the model
when its own container starts, and its `/rerank` request carries no model field;
set the model on that container rather than advertising an inert app variable.

Set `RERANK_URL` and search gains a fourth stage: the top `RERANK_POOL` fused
candidates are cross-encoded and reordered, and the cross-encoder's score — not
the fused score — decides the page. Both are reported (`rerank_score` beside
`score`, `bm25_score` and `closet_boost`), so you can see when they disagree.
Widening the pool is most of the win: without it the ranker only ever sees
`limit × 3` candidates, so a drawer buried at rank 40 can never surface.

It **fails open**. A reranker that is down, slow or returning nonsense costs
ordering quality, never recall — search falls back to the hybrid order and logs a
warning. Nothing else changes, so leaving `RERANK_URL` unset keeps the exact
behaviour every deployment had before.

`RERANK_POOL` is independent of TEI's own `--max-client-batch-size` (32 by
default): the pool is split into batches automatically, so you can set it to
whatever search quality needs without reconfiguring the server. Scoring each
pair is independent, so batching cannot change a score.

Run one with [TEI](https://github.com/huggingface/text-embeddings-inference):

```bash
docker run -d --name reranker -p 12434:80 -v $PWD/tei-data:/data --pull always \
  ghcr.io/huggingface/text-embeddings-inference:cpu-1.9 \
  --model-id BAAI/bge-reranker-v2-m3
# CUDA host: swap cpu-1.9 -> cuda-1.9 and add --gpus all
# TEI listens on port 80 INSIDE the container — map to 80, not 8080.

export RERANK_URL=http://localhost:12434
```

> **Ollama cannot do this job.** It exposes only a model's embedding layer, never
> the cross-encoder classification head, so it has no rerank endpoint
> ([ollama/ollama#10467](https://github.com/ollama/ollama/issues/10467)) — pulling
> a `bge-reranker` tag into Ollama gets you embeddings, not relevance scores.
> Keep Ollama for `bge-m3` embeddings and run TEI alongside it.

`am_search`'s optional `context` argument feeds this stage: it sharpens the
re-ranking without changing which drawers are retrieved.

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

## Moving a single wing (`wing export` / `wing import`)

The whole-workspace export above takes *everything*. To move **one wing** —
restore a backup, seed a new workspace, fork a wing under a second name, or lift a
project's memory out of a self-hosted install and into the hosted service — use a
**wing bundle**.

The defining property is that **a bundle carries no wing name**. Not on a record,
not in a header, nowhere. Exporting is "take this wing's *contents*", and the
destination is named on the way **in**:

```bash
# Self-hosted: straight against the database, no server and no token needed.
agentsmemory wing export --db ~/.agentsmemory/db.sqlite --wing wing_acme --out acme.ndjson
agentsmemory wing import --db ~/.agentsmemory/db.sqlite --file acme.ndjson --as wing_abc
```

`--as` is **required**. A bundle names no wing, so importing without a
destination would file every memory into an unnamed wing — an import that looks
like it worked and leaves the memories where nobody looks.

On a multi-workspace database add `--project <slug>` (it defaults to the single
`local` workspace). The same bundle works over HTTP:

```bash
# Agents / scripts: the same endpoint the mempalace migration uses, plus ?as=
curl -X POST -H "Authorization: Bearer $KEY" --data-binary @acme.ndjson \
  "https://your-host/import?as=wing_abc&recompute=1"

# Browser: project page → "Move a wing" (download a wing, upload a bundle).
```

**What travels, and what deliberately does not:**

| Carried | Left behind |
|---|---|
| Drawers, including diary (agent + topic preserved) | **Vectors** — the destination re-embeds |
| Closets (the mined pointer index) | **Knowledge-graph facts** — team-global, not wing-scoped |
| Explicit tunnels with **both** ends inside the wing | Hallways and derived tunnels — recomputed |

Both omissions are deliberate. Vectors would multiply the file size *and*
silently corrupt search if the destination runs a different embedding model or
dimension, so a bundle stays text and the background worker indexes it on
arrival. KG facts belong to the whole team rather than to any wing, so shipping
them with "one wing" would sweep every *other* wing's facts along with it.

A tunnel with one endpoint outside the wing is dropped because the importer
requires each endpoint room to already hold a drawer — the far end simply isn't
in the bundle. Since an explicit tunnel exists to link two *different* wings, a
single-wing bundle usually carries none; the CLI says so rather than leaving you
to wonder.

Every record id is deterministic, so importing the same bundle twice **upserts
rather than duplicates**, and importing into an existing wing merges into it.
Exporting a wing that does not exist fails and lists the wings that do — an
export must never produce a valid, empty file.

Implementation: [`internal/wingbundle`](internal/wingbundle/wingbundle.go) (the
format + exporter), `internal/importer` (`?as=`), `cmd/server/wing.go` (the CLI)
and `internal/web/wing.go` (the dashboard routes).

---

## Teaching the palace about a project's data (`import`)

A project's reference and seed data usually ship as JSONL beside the code that
loads them. The rows end up in a database, which is the right home for rows — it
answers *"which invoices are overdue"* better than any vector search will.

What no store answers is what the data **means**: why every seeded date falls in
one quarter, which of twelve status values the data actually exercises, that
`amount` is minor units. An agent opening the repository can read the rows and
still not know any of it.

`agentsmemory import` files one memory per dataset, and the memory is two halves:

```bash
agentsmemory import --config agentsmemory-import.toml --out project.ndjson
agentsmemory wing import --db ~/.agentsmemory/db.sqlite --file project.ndjson --as wing_acme

# Or straight into a running server, self-hosted or hosted:
agentsmemory import --config agentsmemory-import.toml \
  --push https://your-host/import --as wing_acme --token "$AGENTSMEMORY_TOKEN"
```

The mapping file is committed **in the project's own repository**, so a change to
a dataset and the description of that dataset are reviewed in the same pull
request:

```toml
wing = "wing_acme"                       # a default; --as still names it on the way in

[[dataset]]
file  = "data/invoices.jsonl"            # relative to THIS file, not to the shell
room  = "schema"
title = "Invoice seed data"
why   = """
Seeded from one anonymised quarter, which is why every due date lands in Q1.
`amount` is MINOR UNITS — 1200 is twelve euros, not twelve hundred."""
show_values = ["status", "currency"]     # the ONLY fields whose values are quoted
```

`why` is required. A dataset drawer carrying only a profile records what a reader
could have derived from the file, and filing it would spend recall on nothing.

### `show_values` — the only values that leave the file

Every field is measured the same way whether or not you name it: type, how many
rows carry it, how many distinct values it took, and its date range. But the
**values themselves are quoted only for the fields listed in `show_values`.**

A drawer is recalled by every agent in the wing, and the server embeds and
indexes it on arrival. So `status` and `country` and `manager_email` all look
alike from inside a profiler — twelve distinct strings — and only the person who
wrote the dataset knows which of them may be published. Naming them is that
person saying so.

It is an **allowlist, not a list of exclusions**, because the two fail in
opposite directions and only one failure is recoverable. A column added to the
dataset next month is merely *absent* from the next memory here; an exclusion
list written before that column existed *publishes* it. A re-import replaces the
drawer, but nothing un-embeds what the first one already filed.

The cost is that the omission could be silent, so it isn't: an unnamed field
still reports its distinct **count**, and the memory says in as many words that
values were withheld.

```
· status (string), takes 2 value(s) here: open, paid
· email (string), 148 distinct value(s), not listed

⚠2 field(s) above were COUNTED AND NOT QUOTED. …
```

**What lands, and what deliberately does not:**

| Carried | Left behind |
|---|---|
| The `why` you wrote, verbatim and first | **The rows.** They are already in the database this same JSONL builds |
| A profile **measured** on every run: fields, types, row count, distinct counts, date ranges | **The values of any field `show_values` does not name** — counted, never quoted |
| The value sets of the fields you named in `show_values` | **Vectors** — the bundle is text, the server embeds |
| A count of the fields whose values were withheld, so the gap is visible | Anything below the first level of a nested object |

The measured half cannot drift from the data, because it is re-derived rather
than remembered. The written half is the part no tool can infer, which is why it
goes first: recall returns a *window* around a match, and the half that cannot be
re-derived has to be the half a snippet finds.

Rows are refused on evidence rather than taste: a larger, more heterogeneous
corpus retrieves measurably worse, because unrelated entries do not remove the
answer — they add competitors ahead of it.

### Re-running it

A drawer's id is a hash of where it goes and what it says, and the memory's text
is a pure function of the dataset — the measurement date rides along as
`content_date` rather than inside the text. So **re-importing an unchanged file
is a no-op**, however often it runs: same bytes, same id, one row. That is what
makes a committed mapping file worth committing and a scheduled re-import safe.

**A changed dataset is a different matter.** The import path absorbs and never
purges by source — it has to, because a batched migration would otherwise delete
the earlier batches of the source it is still uploading — so a new profile is
filed *beside* the old one and yesterday's numbers stay recallable. Delete the
superseded drawer with `am_invalidate_drawer` after a real change — its text stays
readable with your reason attached — until the
[backlog item](docs/adr/BACKLOG.md) that closes this lands.

Pushing straight to a server takes `--as`, and the CLI refuses the push without
it: the bundle carries no wing, and `/import` **skips** a record it cannot
address while still answering 200. The push then reads the endpoint's summary
rather than its status code, for the same reason — a storage failure is reported
inside a 200 — and asks it to rebuild the derived graph on the way out.

Implementation: [`internal/datasetdoc`](internal/datasetdoc/) (the profiler,
the mapping file and the bundle) and `cmd/server/importdata.go` (the CLI).

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
  store/chromemvec/    embedded chromem-go index (the --local default)
  store/sqlitevec/     SQLite vector source of truth
  embed/ollama/        Ollama bge-m3 embedder
  auth/                bearer token → tenant context injection
  palace/              core memory domain types (wing/room/drawer/hallway/tunnel)
  mcpserver/           MCP tool wiring (status, load_skill, …)
  dataexport/          per-workspace SQLite data export (BDAR right of access)
  wingbundle/          portable single-wing bundle format (carries no wing name)
  importer/            POST /import — bundle ingest, ?as= names the target wing
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

**Migration numbers are allocated at merge, never at authoring**, because a
per-branch uniqueness check cannot see another branch. Plain `goose.Up` refuses a
pending migration sitting below the database's maximum applied version, so when
two branches both add one, whichever merges SECOND renumbers.

That renumber has a cost, and it lands on you rather than on production: if you
ran the branch locally, your database already applied that file under its OLD
number, so goose sees the new number as unapplied and re-runs the same SQL. For
an `ADD COLUMN` that is `duplicate column name`, and the server exits on a
migrate error — a crash loop on the next start. A fresh or production database
never sees it, and no test can, because tests migrate from empty.

The repair is to record the new version as applied, after checking your schema
already holds what it would create:

```bash
sqlite3 agentsmemory.db \
  "INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (<new>, 1, datetime('now'));"
```

Numbering therefore has deliberate gaps — `00027` is one, freed when ADR-034's
migration became `00029` at merge. A gap is a record that a renumber happened,
not an error.

---

## Support

AI Agent Memory is open source and free to self-host. If the hosted service
helps you, you can support development on
[Open Collective](https://opencollective.com/it-uoga/projects/ai-agents-memory)
— contributions fund the always-on GPU the hosted recall runs on. The Pro plan's
checkout is the project's €50/month contribution tier, so paying subscribers and
donors land on the same page.

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
- [x] Admin — `am_merge_wing` + `am_memories_filed_away` (`sync`/`hook_settings` stay single-user-local, not ported)
- [x] Web dashboard — local (`goth`) login, project create + one-time API key, monthly usage metering — `templ` + datastar
- [x] Web skill management — per-project list / create / edit (role-gated to writer/admin), membership-checked routes
- [x] Migration — read-only `mempalace` exporter + streaming `POST /import` (drawers, diary, closets, KG facts, tunnels; re-embedded, graph rebuilt)
- [x] Data export (BDAR/GDPR) — download a workspace's data as a self-contained SQLite file (`GET /projects/{teamID}/export`, membership-gated, tenant-scoped, secrets redacted)
- [x] Web — per-member API-key reveal + rotation (each member reveals/rotates their own key — scoped to `(team, user)`, secret shown once, destructive-confirm flow)
- [x] Subscriptions / billing — provider-agnostic (Stripe + OpenCollective): hosted checkout, signature-verified webhooks (Stripe) / operator-activated contributions (OpenCollective), self-service customer portal, FREE + PRO monthly/annual ladder
- [x] 2FA — per-user TOTP (Google-Authenticator compatible) + one-time recovery codes; enforced on password *and* social login
- [x] Passwordless — WebAuthn passkeys (passwordless primary login + passkey as a 2nd factor)
- [x] Operator plan override — unlimited (`-1` cap) plan + superadmin `set-plan` CLI
- [x] `/load-skill` Claude command — client-side wrapper over `am_load_skill`: fetch a team-shared skill by name and install it as a local `.claude/skills/<name>/SKILL.md` (shipped in the `aiagentmemory` installer)
- [x] Web — team/member management with per-member API keys: add a registered user by email (admin-gated) to mint them their own token, set roles (member/writer/admin) with a last-admin guard, and remove a member to revoke their keys in the same transaction (they can no longer connect)

---

## Provenance

A faithful Go SaaS rewrite of the original single-user Python
[`mempalace`](https://github.com/MemPalace/mempalace) (frozen) — that repository
is the upstream source this project is derived from. The domain model
(wings/rooms/drawers/closets/hallways/tunnels/KG/AAAK dialect), the 37-tool MCP
contract, the hybrid ranking, and idempotent mining are ported; Chroma, local
ONNX embeddings, and the HNSW repair tooling are dropped in favour of Qdrant +
Ollama from the start. Reference Go stack patterns follow a sibling
project on the same stack (chi · templ · datastar · Ollama · Qdrant · MCP · RRF).
