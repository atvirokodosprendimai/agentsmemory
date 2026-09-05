# Troubleshooting

Organised by **what you see**, because most of these look like nothing at all.

The recurring shape is worth stating once: almost every failure below is
**silent**. A hook that cannot reach its palace goes quiet rather than erroring; a
reranker that times out returns the unranked order; an unread environment variable
cannot warn you; a stale binary answers confidently. So the first question is
rarely "what is the error" — it is "which of these is quietly true right now".

Two commands answer most of it:

```bash
aiagentmemory doctor            # registrations, hook events, the binary a bridge spawns, the server's version
aiagentmemory mcp am_status     # which palace answered, which workspace, which version
```

---

## Recall is worse than it used to be, or answers come from other projects

**Most likely: the registration lost its wing.** With no wing on the registration
`default_wing` is empty, and an unscoped `am_search` searches *every* wing in the
workspace. Unrelated projects do not remove your answer — they add competitors
ahead of it, so recall degrades rather than breaking.

```bash
aiagentmemory mcp am_status     # look at default_wing
```

Empty when you expected a project? Re-run the install **with the same flags as the
first time**, plus `--wing`:

```bash
aiagentmemory install --local --wing wing_acme
```

A `--global` install registers at user scope with no `X-Agentsmemory-Wing` header,
which is the usual way this happens.

**Also possible: the cross-encoder is timing out.** A rerank that fails open
returns the fused order and reports it rather than erroring. `am_recall_stats`
names the reason, and `failed_open reason=timeout` is distinct from
`reason=error`. On a CPU-capped stack this is the answer: at a 2-CPU quota the
cross-encoder never finishes inside `RERANK_TIMEOUT`. Measured floor on a 10-core
host is 4 CPUs for the reranker and 2 for Ollama. The constraint is
`RERANK_POOL` × per-document cost < `RERANK_TIMEOUT` — a relationship, not a core
count.

---

## I created a room by mistake

A room is its live memories, and nothing else (ADR-055). There is no create and no
delete: filing a memory into a name is what makes the room exist, and the room is
gone from `am_list_rooms`, `am_list_wings` and `am_graph_stats` the moment its
last live memory is not there. Two verbs do that:

- **retract** the memory — `am_invalidate_drawer(id, reason)` — when it should
  not have been filed at all;
- **relocate** it — `am_update_drawer(id, room: "<the right room>")` — when the
  memory is right and the room name was the typo. The id is kept.

A room whose memories are all ended stays readable by id (history is never
deleted) and is listed by nothing.

## Nothing is being written to memory any more

**Most likely: an install repointed your hooks.** A bare `aiagentmemory install`
on a machine set up with `--local` falls back to the hosted default, and the
default wins over what is already configured. The hooks then talk to a server they
hold no credential for and **go silent rather than failing**.

```bash
grep -o "AGENTSMEMORY_MCP_URL='[^']*'" ~/.claude/settings.json | sort -u
```

Fix by re-running install the way you originally did — see
[UPDATE.md](UPDATE.md#the-trap-that-catches-everyone-re-running-install).

**Also possible: the agent has no write half.** Only Claude and Codex get the Stop
checkpoint and the subagent hooks. Cursor and Claude Desktop recall memory and are
never prompted to write it. That is design, not breakage (ADR-017).

---

## The Stop nudge says nothing was touched, but I edited files

The `PostToolUse` "touched" hook records `Edit`, `Write`, `NotebookEdit` and
`MultiEdit` only. A session that edits through Bash — `sed`, a heredoc, or a
multi-file tool — leaves that list **empty**, and an empty list reads as "this
session changed nothing" rather than "this session used a tool the hook does not
watch". The two are indistinguishable from the nudge.

`git status --porcelain` is the truth. The hook is not broken; the inference from
an empty list is.

---

## `am_status` reports version `dev`

The build was not stamped. `.dockerignore` excludes `.git`, so there is no VCS
fallback, and a `dev` build passes every freshness check while telling you nothing
(issue #210). Rebuild with the stamp:

```bash
go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags)" -o agentsmemory ./cmd/server
```

`AGENTSMEMORY_VERSION` is per-build, not sticky — the next rebuild without it
resets to `dev`.

---

## I updated the binary and nothing changed

**Check which binary actually runs.** Two copies on `PATH` is common: `~/.claude/bin`
(the quality-harness tools directory) can shadow `~/.local/bin`, and the shadow
wins. `redeploy.sh` names the path it read and warns when the remedy targets a
directory the shadow hides (issue #204); make the shadow a symlink into
`~/.local/bin` rather than a copy, and the same for `aiagentmemory-server`.

```bash
command -v aiagentmemory       # the one that runs
ls -l ~/.claude/bin/aiagentmemory ~/.local/bin/aiagentmemory 2>/dev/null
```

Make the shadow a symlink to the real one, or remove it.

**And check the server separately.** A current kit against a stale server looks
identical to a current stack. `am_status`'s `version` field is the only thing that
distinguishes them.

---

## A tool call returns another project's memories

You are talking to a different workspace than you think. A registration carrying
another project's token answers every probe happily — a successful call proves
nothing about scope.

```bash
aiagentmemory mcp am_status     # read the workspace block: slug and name
```

`mode` (`local` / `hosted`) says only whether that workspace lives on the SaaS or
on a server you run. **The workspace slug is the identity check.**

A *wing* you do not recognise is a different matter and usually benign — a wing
comes into existence when something is first written to it, so a missing one means
nobody has written there yet.

---

## The server will not start

- **`refuses to start: workspace is not local`** — the database already holds a
  workspace that is not `local`, including the `demo` workspace the multi-tenant
  path seeds on first boot. Use a fresh `--db` file, or drop `--local`.
- **A goose migration error on start** — usually a pending migration numbered
  *below* the database's maximum applied version, which happens when two branches
  allocated the same number. Migration numbers are allocated at merge for exactly
  this reason. A fresh database never sees it.
- **`invalid argument` binding a socket** — the path is too long. The kernel caps
  Unix socket paths near 104 bytes on macOS and 108 on Linux, and the failure
  message does not say so.

---

## `403 forbidden: Host <name> does not address this machine`

Working as intended. Because the local endpoint is unauthenticated, reachability
*is* authorization, so the server refuses any request whose `Host` or `Origin`
names something other than this machine. Without that check, a page you visit
could point its own domain at `127.0.0.1`, at which point the browser treats it as
same-origin, skips the preflight, and CORS never runs (ADR-049).

If you deliberately bound a routable address, the check is off and `--token` is
the boundary instead.

---

## Claude Desktop: the install said it worked and the tools are not there

Two separate causes:

- **Desktop was running.** It holds the bridge it spawned open, so placing the
  new binary fails; the install now exits non-zero and says so (issue #208). Quit
  Claude Desktop and re-run.
- **There is no host server binary and the download failed.** `--agent
  claude-desktop` registers an `mcp-stdio` bridge; a Docker-only install produces
  no host binary, so the installer downloads the release's one for your platform
  (issue #199). Offline, or on a platform the release does not build, build it:

  ```bash
  go build -o ~/.local/bin/aiagentmemory-server ./cmd/server   # or: --server-bin <path>
  ```

Either way, `aiagentmemory doctor` judges the binary the bridge spawns — trust it
over the installer's exit code.

---

## Cursor: registered, but the tools never appear

Run it once, by hand:

```bash
cursor-agent mcp enable agentsmemory
```

Cursor gates every MCP server behind an approval stored **outside** `mcp.json`, so
a registered-but-unapproved server looks identical on disk to a working one. The
installer prints this line every time because a re-install cannot tell whether you
have done it.

---

## Windows: `Hook cancelled` at the end of every session

Expected, and already handled: the `SessionEnd` hook is **not registered on
Windows** by design. Process creation costs about a second there and the hook needs
~3.2s, so it loses the teardown race (issue #150). An existing registration from an
earlier install is retired on upgrade.

You lose the end-of-session report and nothing else. Ask the server for the same
numbers:

```bash
curl -fsS "${AGENTSMEMORY_MCP_URL%/mcp}/stats?hours=2"
```

The hooks read `AGENTSMEMORY_MCP_URL`. `AGENTSMEMORY_URL` is the proxy origin and
is unset in an ordinary install.

⚠ If one install run prints **both** "SessionEnd hook NOT registered on Windows"
and "SessionEnd hook already registered", the first is the true one (issue #184).

---

## Windows: `go test ./...` fails

Two known causes, both in the test suite rather than the server:

- **~40 failures in `TempDir` cleanup** — two test helpers never close their SQLite
  handle. POSIX allows unlinking an open file; Windows does not (issue #162). The
  same cause is why `docs/architecture.md`'s own Gate command cannot pass on a
  Windows host.
- **A source-reading gate false-alarms** — a Windows checkout is CRLF
  (`core.autocrlf=true`, and this repository ships no `.gitattributes`) while those
  gates hard-code `\n`, so a property that actually holds is reported as broken
  (issue #163).

Neither affects a running server or an installed kit.

---

## macOS: embedding is slow

Ollama is probably in a container. It cannot reach Metal there and runs on CPU.
Install Ollama on the host and point the server at
`http://host.docker.internal:11434` — `docker-compose.ollama.yml` exists for hosts
with no Ollama at all, not as the preferred setup.

Related: `docker-compose.host.yml` does nothing on macOS or Windows. Host
networking is Linux-only; Docker Desktop runs containers in a VM.

---

## Compose: a setting I configured has no effect

- **Check every `-f` flag is present.** An overlay alone is not a complete stack,
  and leaving one off starts a valid *different* stack rather than failing. Fix it
  once with `COMPOSE_FILE`, ideally in a `.env` beside the compose files.
- **Check `scripts/redeploy.sh` deploys the same chain.** It takes the chain from
  `COMPOSE_FILE` in the ENVIRONMENT first — it does not read a `.env`, so export
  it for this script — else from the running container's `config_files` label,
  so it follows the overlay set that is up; it prints the chain it resolved on
  its first line. Releases before that hardcoded two files and silently reverted
  `RERANK_URL` on a four-file setup (issue #209).
- **Check the variable is actually read.** A variable documented and read by
  nothing is the failure `TestDocumentedEnvVarsAreRead` exists to catch — it found
  a shipped compose file advertising a rerank pool the server never read. If you
  are on a release where that is still true, the setting does nothing whatever you
  put in it.

---

## `doctor` prints nothing and I do not know if that is good

Silence from a healthy install is normal: the verify hook prints only on drift and
the recall hook only when the palace has something for your branch.

`doctor` **deliberately does not fail on silence** — one run cannot tell healthy
silence from a mute hook, and resolving that in an exit code would be a guess
wearing a check. What it does instead costs nothing: each hook writes what it asked
and what came back to **stderr**, which no event injects, and `doctor` prints that
verbatim. Read those lines and judge.

---

## A memory search says something about code that is no longer true

The memory is pinned to code that has since changed. Search flags this: a hit
carries `stale` when its code anchors no longer match the tree.

```bash
aiagentmemory verify        # re-check anchored memories against this checkout
```

Re-point or correct the flagged memories rather than acting on them. Note that an
anchor on a **superseded** record cannot be repaired — correcting a record mints a
new one, and the old one keeps the text it was written with (ADR-038), so its pins
are drifted by construction and are excluded from the verifier feed by default.

---

## Still stuck

- [INSTALL.md](INSTALL.md) — the full install path, per platform.
- [UPDATE.md](UPDATE.md) — what updates independently of what.
- [`docs/adr/`](docs/adr/) — why a thing is the way it is, when the behaviour looks
  deliberate and you want to know what it was traded against.
- [Issues](https://github.com/atvirokodosprendimai/agentsmemory/issues) — several
  of the defects named above are open, with the measurements attached.
