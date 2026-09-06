# Installing agentsmemory

Two halves, and installing one without the other is the commonest way to end up
with something that looks wired and does nothing:

1. **A server** — the palace itself. Hosted, or self-hosted on your machine.
2. **The kit** — `aiagentmemory install`, which registers the MCP **and** installs
   the memory protocol, hooks and commands your agent needs in order to actually
   use it. A bare `claude mcp add` gives you the tools and none of the habit; see
   [The server is inert without the protocol](README.md#the-server-is-inert-without-the-protocol).

Updating an existing install is [UPDATE.md](UPDATE.md). When something is wired
but behaving oddly, [TROUBLESHOOTING.md](TROUBLESHOOTING.md) is the list of
failure modes we have actually hit, each with what it looks like from the outside.

---

## Pick your path

| You want | Server | Start at |
|---|---|---|
| The hosted service, nothing to run | none — it is ours | [3. Install the kit](#3-install-the-kit) |
| Your own palace, one binary, no Docker | `agentsmemory --local` | [1. Prerequisites](#1-prerequisites) |
| Your own palace in containers | Docker Compose | [1. Prerequisites](#1-prerequisites) |
| Highest recall quality, several machines | Compose + Qdrant + cross-encoder | [2c. Docker Compose](#2c-docker-compose) |
| Claude Desktop | needs a host binary to spawn | [Claude Desktop](#claude-desktop---agent-claude-desktop) |
| To wire an MCP client yourself | any | [Registering by hand](#registering-by-hand-and-over-a-unix-socket) |

Everything below works on **Linux, macOS and Windows**. Where a platform differs
it is called out at the step where it bites, not collected in a footnote — see
also [Platform notes](#platform-notes), which is the same information gathered
per-OS for people who prefer it that way.

---

## 1. Prerequisites

### An embedder (required for a self-hosted server)

agentsmemory never embeds text itself. It calls **your** Ollama, so nothing you
remember leaves your machine — and it means the memory loop does not work until
Ollama answers.

```bash
# 1. Install and run Ollama — https://ollama.com/download
#    macOS / Windows: the desktop app starts the server for you.
#    Linux:           curl -fsSL https://ollama.com/install.sh | sh
ollama --version

# 2. Pull the embedding model (bge-m3, 1024-dim, ~1.2 GB)
ollama pull bge-m3

# 3. Prove it answers on the endpoint the server will use
curl -s http://localhost:11434/api/embed \
  -d '{"model":"bge-m3","input":"hello"}' | head -c 120
```

**Do not skip step 3.** A JSON array of floats means the server will work, and it
fails here rather than on your first `am_add_drawer`. The model must be *pulled*,
not merely installed: Ollama does not fetch it on demand for `/api/embed`, and a
missing one comes back as `model "bge-m3" not found`. Nothing is lost when that
happens — writes return the embed error, and rows that came in through `/import`
sit in the embed queue and drain by themselves once the model is there.

⚠ **Choose the model before you fill the palace, not after.** `bge-m3` matches the
frozen Python palace at 1024 dimensions, so migrated and new memories share one
vector space. Swapping models changes that space: old and new vectors stop being
comparable and every drawer needs re-embedding. `--ollama-model` exists for a
fresh palace.

**macOS and Windows: install Ollama on the host, not in the stack.** A
containerised Ollama cannot reach Metal on macOS, and on both platforms Docker
runs its containers inside a VM. `docker-compose.ollama.yml` exists for hosts with
no Ollama at all; where you have one, the host install is faster by a wide margin.

**Running the server in Docker? `localhost` is not your machine.** Ollama binds
`127.0.0.1` by default and a container cannot reach the host's loopback. Bind it
wider and use the name compose maps for you:

```bash
# macOS
launchctl setenv OLLAMA_HOST 0.0.0.0     # then restart the Ollama app

# Linux (systemd)
sudo systemctl edit ollama               # add: Environment="OLLAMA_HOST=0.0.0.0"
sudo systemctl restart ollama

# Windows
setx OLLAMA_HOST 0.0.0.0                 # then restart the Ollama app
```

Then point the server at `http://host.docker.internal:11434`. On **Linux only**
you can skip the whole problem with the host-network overlay, where
`localhost:11434` inside the container really is your loopback. A GPU box
elsewhere works just as well — point `OLLAMA_URL` at it and pull `bge-m3` there.

### Go (only if you build from source)

Go 1.25+. Every route below has a download-a-binary alternative, so this is
optional.

---

## 2. Get a server

Skip this entirely if you are using the hosted service.

### 2a. The binary

Every release publishes `aiagentmemory-server-<os>-<arch>` for `linux` and
`darwin` on `amd64` and `arm64`, alongside the `aiagentmemory` CLI:

```bash
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); [ "$arch" = x86_64 ] && arch=amd64; [ "$arch" = aarch64 ] && arch=arm64
curl -fsSL -o agentsmemory \
  "https://github.com/atvirokodosprendimai/agentsmemory/releases/latest/download/aiagentmemory-server-${os}-${arch}"
chmod +x agentsmemory
```

**Windows, or want checksums:** the same releases carry
`agentsmemory_<os>_<arch>.tar.gz` / `.zip` archives — Windows included — with a
`SHA256SUMS.txt`. Building it yourself is `go build -o agentsmemory ./cmd/server`.

Run it in single-workspace mode:

```bash
./agentsmemory --local --db agentsmemory.db
# agentsmemory listening on 127.0.0.1:8080 (local mode: workspace "local", MCP /mcp, no token required, no dashboard)
```

`--local` collapses the multi-tenant SaaS shape to the simplest thing that still
runs every tool: exactly one workspace, no token, no dashboard, loopback only, and
the embedded `chromem` index so there is no vector service to run. All MCP tools
behave identically — they only ever see a resolved workspace, and local mode
injects one instead of resolving it from a credential.

⚠ **It refuses to start** if the database already holds a workspace that is not
`local`, including the `demo` workspace the multi-tenant path seeds on first boot.
Use a fresh `--db` file, or drop `--local`.

⚠ **The endpoint is unauthenticated, so reachability *is* authorization.** That is
why it binds loopback. The server also refuses any request whose `Host` or
`Origin` names something other than this machine (`403 forbidden: Host <name> does
not address this machine`) — a page you visit can point its own domain at
`127.0.0.1`, at which point the browser treats it as same-origin and CORS never
runs (ADR-049). To share a palace across a home network, use `--token`; to tighten
it below loopback, use `--socket`. Both are documented in the
[README](README.md#serving-a-home-network---token).

### 2b. Build from source

```bash
go build -o agentsmemory ./cmd/server
./agentsmemory --local --db agentsmemory.db
```

⚠ **Stamp the version when you build, or the server reports `dev`.** The
`.dockerignore` excludes `.git`, so there is no VCS fallback, and a `dev` build
passes every freshness check while telling you nothing about what is running
(issue #210):

```bash
go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags)" -o agentsmemory ./cmd/server
```

### 2c. Docker Compose

Five files, composed rather than copied — every stack is the base plus zero or
more overlays, so there is no variant to keep in sync with another.

| File | What it is | When |
|---|---|---|
| `docker-compose.yml` | the base: one container, embedded chromem index, one volume | one person, one machine. The default |
| `docker-compose.full.yml` | overlay: Qdrant + a cross-encoder | recall precision matters more than dependency count. **This is what we run** |
| `docker-compose.ollama.yml` | overlay: the embedder in the stack, and points the server at it | you have no Ollama on the host |
| `docker-compose.host.yml` | overlay: host networking — **Linux only** | Ollama on `127.0.0.1:11434` with no `OLLAMA_HOST=0.0.0.0`. Does nothing on macOS/Windows, where containers live in a VM |
| `docker-compose.prod.yml` | hosted multi-workspace mode: dashboard, OAuth, billing | you are running the SaaS shape |

The one-container stack:

```bash
cp .env.docker.example .env.docker   # point OLLAMA_URL at your Ollama
docker compose up -d
```

⚠ **Every `-f` flag, every time.** An overlay alone is not a complete stack, and
leaving one off does not fail — it starts a valid *different* stack, silently.
Either repeat the flags, or fix them once:

```bash
export COMPOSE_FILE=docker-compose.yml:docker-compose.full.yml
```

Putting that same line in a `.env` beside the compose files makes it the
directory's default, which is what stops the flags drifting between people.

⚠ **A Compose-only install leaves no host binary**, and `--agent claude-desktop`
needs one because it registers a `mcp-stdio` bridge (issue #199). The installer
fetches it for you: when no `aiagentmemory-server` is on PATH it downloads the
one the release publishes for your platform — the release the client came from,
or the newest for an unstamped client — verifies it runs, and places it under
the install's `bin/`. Offline, or on a platform the release does not build, it
refuses with the hand remedy, which still works:

```bash
go build -o ~/.local/bin/aiagentmemory-server ./cmd/server   # or: --server-bin <path>
```

⚠ **Capping the inference containers can turn reranking off silently.** At a
2-CPU quota the cross-encoder never finishes inside `RERANK_TIMEOUT`, so every
search returns the fused order while the reranker sits there installed, healthy
and contributing nothing. Measured: 4 CPUs for the reranker and 2 for Ollama is
the floor that works on a 10-core host. The constraint is a relationship, not a
core count — `RERANK_POOL` × per-document cost < `RERANK_TIMEOUT`.

---

## 3. Install the kit

The `aiagentmemory` binary wires [Claude Code](https://claude.com/claude-code),
[Codex](https://developers.openai.com/codex), [Cursor](https://cursor.com), Claude
Desktop or [pi](https://pi.dev) into your workspace.

```bash
curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
```

The bootstrap script detects your OS/arch, downloads the latest
`aiagentmemory-<os>-<arch>` into `~/.local/bin`, then runs `aiagentmemory
install`. Anything after `--` is forwarded. Building it yourself:

```bash
go build -o aiagentmemory ./clients/claude-code
./aiagentmemory install
```

### Against the hosted service

```bash
aiagentmemory install                    # prompts for your workspace API token
aiagentmemory install --token <key>      # or non-interactively
AGENTSMEMORY_TOKEN=<key> aiagentmemory install --yes
```

Create a project in the dashboard and copy or **Reveal** its key.

### Against your own server

```bash
aiagentmemory install --local                                # global, Claude, http://localhost:8080/mcp
aiagentmemory install --local --agent all                    # claude | codex | cursor | claude-desktop | pi
aiagentmemory install --local --sandbox acme                 # isolated config at ~/.sandboxes/acme
aiagentmemory install --local --mcp-url http://192.168.1.50:8080/mcp
```

With `--local` and no target named, the install goes global and skips the
interactive global-vs-sandbox prompt.

⚠ **Pass `--wing` unless you want every recall searching every project.** Without
it the registration carries no wing, `default_wing` is empty, and an unscoped
`am_search` competes your answer against every other project in the workspace —
unrelated projects do not remove the answer, they add competitors ahead of it:

```bash
aiagentmemory install --local --wing wing_acme
```

The installer uses whichever channel each client supports: an HTTP header for
Claude and Cursor, a URL query for Codex, a bridge flag for Desktop and socket
installs, an environment variable for pi.

Where a process manager supplies registration scope through the environment
instead, the `mcp-stdio` bridge reads its wing from there:

```bash
AGENTSMEMORY_WING=wing_acme
```

A tool call can still pass `wing: "*"` when it deliberately needs every project.

### Claude Desktop (`--agent claude-desktop`)

Desktop is the thinnest install there is, and the differences are worth knowing
before you start rather than after.

```bash
# 1. Desktop spawns a LOCAL PROCESS, so it needs a server binary on this machine.
#    A Docker-only install produces none (issue #199).
go build -o ~/.local/bin/aiagentmemory-server ./cmd/server

# 2. QUIT CLAUDE DESKTOP FIRST. It holds its config file open while running.
# 3. Then install.
aiagentmemory install --local --agent claude-desktop --wing wing_acme
```

**What it writes:** `claude_desktop_config.json`, with an `mcpServers` entry that
spawns the `mcp-stdio` bridge — the same file shape Cursor and Claude Code use.
The config directory is the one place that is not under `$HOME` in the usual way:

| | |
|---|---|
| macOS | `~/Library/Application Support/Claude` |
| Windows | `%AppData%\Roaming\Claude` |
| Linux | `~/.config/Claude` |

**Which binary it spawns:** the installer looks for `aiagentmemory-server` first,
then `agentsmemory`, on `PATH`. `--server-bin /path/to/agentsmemory` names one
explicitly. If neither is found the install **refuses** rather than writing a
command that is not there.

⚠ **`--dry-run` does not perform that check.** A rehearsal reports the first
candidate name whether or not any such file exists — it never looks — so a dry run
can show you `aiagentmemory-server` and the real install can then refuse. Read a
clean `--dry-run` as "the plan is well-formed", never as "the binary is there".
It is the same shape as the exit-0 trap below: the rehearsal reports success and
the authority is the run that actually resolves the path.

⚠ **Desktop gets the tools and no protocol file.** It has no commands directory,
no memory file, no rules file, no hooks and no subagent system — every one of
those is a measured absence, not an omission. The only channel for the rules is
the MCP `initialize` handshake, which the server answers with `instructions` for
exactly this reason (ADR-021). In practice that means Desktop **recalls** memory
and is never prompted to **write** it.

⚠ **`--sandbox` is refused for Desktop**, because it exposes no config-dir
variable to pin — the same reason it is refused for Cursor.

⚠ **A failed registration can still exit 0**, reporting a rename error rather than
"quit Claude Desktop" (issue #208). Do not trust the exit code here:

```bash
aiagentmemory doctor --agent claude-desktop   # ⚠ the --agent is what makes this check DESKTOP
```

Without `--agent claude-desktop` you get a clean report about **Claude Code** —
`doctor` defaults to `--agent claude`, and only the Desktop kit has a bridge
binary to judge. Verifying a Desktop install with the bare command is the shape
this whole page warns about: a green answer to a question nobody asked.

### Registering by hand, and over a Unix socket

**By hand, over HTTP.** Any MCP client can be pointed straight at a `--local`
server with no header, because a loopback server carries no token:

```bash
claude mcp add --transport http agentsmemory http://localhost:8080/mcp
```

⚠ **That gives you the tools and nothing else** — no protocol, no hooks, no
commands, no subagent definition. It is a registration, not an install; see
[the server is inert without the protocol](README.md#the-server-is-inert-without-the-protocol).
`aiagentmemory install` does this step *and* the rest.

**Over a Unix socket.** A port is a weak boundary for an unauthenticated
endpoint — every user and process on the machine can open `127.0.0.1:8080`. A
socket at mode `0600` narrows that to the account that started the server:

```bash
./agentsmemory --local --socket /tmp/agentsmemory.sock --db agentsmemory.db
aiagentmemory install --local --socket /tmp/agentsmemory.sock --wing wing_acme
```

A socket has no URL, so agents reach it through the `mcp-stdio` bridge shipped in
the same binary. The server prints the ready-made registration lines on startup
with its own absolute path filled in, so they can be copied out of the log:

```bash
claude mcp add agentsmemory -- /path/to/agentsmemory mcp-stdio --socket /tmp/agentsmemory.sock --wing wing_acme
codex  mcp add agentsmemory -- /path/to/agentsmemory mcp-stdio --socket /tmp/agentsmemory.sock --wing wing_acme
```

`--socket` requires `--local` — the bridge carries no token, so it only reaches a
self-hosted server. It works for Claude and Codex; **pi has no MCP client of its
own**, so install that one with `--mcp-url` against `--addr`.

⚠ **Socket paths are short** — near 104 bytes on macOS, 108 on Linux. A deeply
nested path fails to bind with a bare `invalid argument` that does not say so.

### Preview first

```bash
aiagentmemory install --dry-run     # prints every file write and command, touches nothing
```

### What each agent actually gets

The agents do not offer the same surfaces, and the kit installs what each one has
rather than pretending. The full matrix — config dirs, which hooks exist, which
agents get the *write* half of the loop — is in the
[README](README.md#what-each-agent-actually-gets) and in
[`clients/claude-code/README.md`](clients/claude-code/README.md).

Two that surprise people:

- **Cursor needs one manual step the installer deliberately does not take.**
  Cursor gates every MCP server behind an approval stored outside `mcp.json`, so a
  registered-but-unapproved server looks identical on disk to a working one. Run
  `cursor-agent mcp enable agentsmemory` once. The installer prints this every
  time, because a re-install cannot tell whether you have done it.
- **Only Claude and Codex get the write half.** The Stop checkpoint and the
  subagent hooks are what ask an agent to persist what it learned. Cursor and
  Claude Desktop recall memory and are never prompted to write it — ADR-017
  records why the advisory half of a loop does not happen on its own.

---

## 4. Verify

Three checks, in order of what they can tell you.

```bash
aiagentmemory doctor
```

`doctor` reads the registration your agent will actually use and reports three
states an install cannot: a hook installed and registered by no event, a hook
registered on an event whose stdout goes to the debug log, and a hook that will
not run. Against `--agent claude-desktop` it also judges the bridge binary that
kit registers — that it exists, can be spawned, and is the same build as the
server it bridges to. And for every kit it judges the server the install points
at: it reads the version out of the MCP handshake and fails on
`UNSTAMPED` — a server reporting the bare word `dev`, which is an image built
without `AGENTSMEMORY_VERSION` and cannot be told from a stale one (issue #210) —
and on `UNREACHABLE`. A `dev-<commit>` build is reported as `unreleased` and
passes, because the string names a commit you can compare against your checkout.

⚠ **`doctor` checks ONE kit per run, the one `--agent` names.** If you installed
for more than one agent, run it once per agent; a green report for `claude` says
nothing whatever about your Desktop or Cursor install.

⚠ **`doctor` cannot fail on silence, and that limit is deliberate.** Both shipped
injecting hooks are silent when healthy — the verify hook prints only on drift,
the recall hook only when the palace has something for your branch — so one run
cannot tell healthy silence from muteness. Each hook writes what it asked and what
came back to stderr, and `doctor` prints that verbatim so a human can judge.

Then ask the server who it thinks you are. From a session, `am_status` should
report the workspace you expect and a `default_wing` if you passed `--wing`. From
the shell:

```bash
aiagentmemory mcp am_status
```

⚠ **Check the `workspace` block, not just that the call succeeded.** A
registration carrying another project's token answers every probe happily. The
workspace slug is the identity check; `mode` only tells you whether that workspace
lives on the SaaS or on a server you run.

---

## Platform notes

The same information as above, gathered per-OS.

### macOS

- **Install Ollama on the host.** A containerised Ollama cannot reach Metal and
  runs on CPU.
- **`docker-compose.host.yml` does nothing here.** Host networking is Linux-only;
  Docker Desktop runs containers in a VM. Use `OLLAMA_HOST=0.0.0.0` plus
  `host.docker.internal`.
- **Unix socket paths are short** — the kernel caps them near 104 bytes (108 on
  Linux). A deeply nested `--socket` path fails to bind with a bare
  `invalid argument`.
- **`--shared-auth` is a no-op for Claude here.** macOS already shares the
  keychain across config directories; the flag matters for codex and pi.
- **Quit Claude Desktop before `--agent claude-desktop`.** A running Desktop holds
  its config file, and the install currently exits 0 after failing to register,
  reporting a rename error rather than saying so (issue #208). Verify with
  `aiagentmemory doctor --agent claude-desktop` rather than trusting the exit
  code — the bare command reports on Claude Code and can exit 0 just as happily.
- **Watch for two binaries on PATH.** If you use the quality-harness tools,
  `~/.claude/bin` can shadow `~/.local/bin`, and the shadow is what runs (issue
  #204). `command -v aiagentmemory` tells you which one you have.

### Windows

Windows is a supported platform — the releases ship `.zip` archives for it and the
kit installs. Four things behave differently, and all four are open issues rather
than settled design:

- ⚠ **The `SessionEnd` hook is not registered on Windows, by design.** Process
  creation costs about a second there and the hook needs ~3.2s, so it loses the
  teardown race and every exit reported `Hook cancelled` (issue #150). You lose
  the end-of-session report and nothing else; ask the server for the same numbers
  instead:

  ```bash
  curl -fsS "${AGENTSMEMORY_MCP_URL%/mcp}/stats?hours=2"
  ```

  The hooks read `AGENTSMEMORY_MCP_URL`. `AGENTSMEMORY_URL` is the proxy origin and
  is unset in an ordinary install. On macOS and Linux the hook completes well
  inside teardown and stays registered.
- ⚠ **One install run prints two contradictory lines about it** — both "SessionEnd
  hook NOT registered on Windows" and "SessionEnd hook already registered" (issue
  #184). The first is the true one. An existing registration from an earlier
  install is retired on upgrade.
- ⚠ **A Windows checkout is CRLF** (`core.autocrlf=true`, and this repository ships
  no `.gitattributes`), while the source-reading gates hard-code `\n`. This only
  affects you if you build and run the test suite: one gate false-alarms on a
  property that actually holds (issue #163).
- ⚠ **`go test ./...` fails around 40 tests in `TempDir` cleanup**, because two test
  helpers never close their SQLite handle — POSIX hides this, Windows does not
  (issue #162). The same cause is why `docs/architecture.md`'s own Gate command
  cannot pass on a Windows host. Nothing about the *server* is affected.

Everything else — install, MCP registration, protocol, recall, the Stop
checkpoint — works.

### Linux

- **`docker-compose.host.yml` is yours alone.** With it, `localhost:11434` inside
  the container is your loopback, and you need neither `OLLAMA_HOST=0.0.0.0` nor
  `host.docker.internal`.
- Socket paths cap near 108 bytes.

---

## Where to go next

- [UPDATE.md](UPDATE.md) — upgrading the server, the kit and the protocol, and what
  changes silently if you do it in the wrong order.
- [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — the failure modes we have hit, and what
  each looks like from the outside.
- [README](README.md) — what the system is, the tool catalogue, and the reference
  material for everything installed above.
