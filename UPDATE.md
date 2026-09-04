# Updating agentsmemory

**Three things update independently, and updating one does not update the
others.** Most surprises after an upgrade come from assuming otherwise.

| Part | Command | What it touches |
|---|---|---|
| The **kit binary** (`aiagentmemory`) | `aiagentmemory update` | the binary only — no config is rewritten |
| The **protocol and commands** in your config dir | `aiagentmemory update-skill` | `agentsmemory-bootstrap.md`, `/am`, `/load-skill` |
| The **server** | rebuild / `docker compose up -d` / `scripts/redeploy.sh` | the palace itself |

The kit binary and the protocol are deliberately separate: `update` leaves your
config dir alone so an upgrade cannot silently rewrite your registration, and
`update-skill` refreshes documentation without touching executable code. Neither
is a superset of the other, and a machine can sit for weeks with a current binary
and a months-old protocol without anything saying so.

New install? [INSTALL.md](INSTALL.md). Something wired but misbehaving?
[TROUBLESHOOTING.md](TROUBLESHOOTING.md).

---

## 1. The kit binary

```bash
aiagentmemory update                     # upgrade to the latest release
aiagentmemory update --check             # report installed vs latest, change nothing
aiagentmemory update --version v0.0.46   # pin, or roll back
aiagentmemory update --bin <path>        # update a copy somewhere else
```

The new asset is downloaded next to the current binary, run once with `--version`
to prove it is intact and the right architecture, and only then renamed over the
old file — an atomic swap, so a failed or interrupted download leaves the working
binary in place.

**macOS / Linux:** replacing a running binary is safe; an already-open session
keeps running the old image. If the binary lives somewhere you do not own
(`/usr/local/bin`), the swap needs `sudo`; the default `~/.local/bin` install
never does.

**Windows:** a running process holds its image, so close any session using the
binary before updating.

⚠ **Check which binary you are updating.** If two copies are on `PATH` — this
happens when `~/.claude/bin` shadows `~/.local/bin`, as it does on a machine with
the quality-harness tools — the one that runs and the one an update writes can be
different files (issue #204). Confirm before and after:

```bash
command -v aiagentmemory
aiagentmemory --version
```

⚠ **`--version` cannot tell a fresh kit from a stale one when the kit was built
locally.** A plain `go build` leaves the version at `dev`, so a locally-built kit
self-reports `dev` however current it is. Trust the release tag or the git SHA you
built from, not the string.

### A binary too old to have `update`

Releases before `update` existed have no self-update. Re-download without running
the installer — same result, no config touched:

```bash
AIAGENTMEMORY_NO_INSTALL=1 curl -fsSL \
  https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
```

`AIAGENTMEMORY_NO_INSTALL` makes it download-only; `AIAGENTMEMORY_VERSION` pins a
tag and `AIAGENTMEMORY_BIN_DIR` changes the destination.

---

## 2. The protocol and commands

```bash
aiagentmemory update-skill                             # the global install
aiagentmemory update-skill --check                     # what would change, writes nothing
aiagentmemory update-skill --sandbox acme              # an isolated sandbox
aiagentmemory update-skill --sandbox acme --agent all  # every agent inside it
aiagentmemory update-skill --ref main                  # track a branch instead of a release
```

It fetches `agentsmemory-bootstrap.md` and the `/am` and `/load-skill` commands
from the repository tree and writes them into a config dir. Your binary, MCP
registration, workspace token and Stop hook are all left untouched.

The whole kit is downloaded before anything is written, so a failed fetch leaves
your config dir exactly as it was rather than half-updated. On Codex and pi the
protocol is inlined into `AGENTS.md`; on Claude it is imported from `CLAUDE.md`.
Your own content outside the managed block is preserved and the file is backed up
before it changes.

⚠ **The Stop hook and the pi bridge extension are excluded on purpose**, even
though they are kit assets. Both are executable code, and quietly downloading a
shell script over an existing one is a bigger act than refreshing documentation.
Run `install` when you want those.

`--ref` defaults to the latest release tag, so `update-skill` and `update` track
the same version by default. The files come from the repository tree, because a
release publishes binaries only.

---

## 3. The server

### A binary install

Download or rebuild, then restart. ⚠ **Stamp the version** — a build without it
serves `dev` with every check green, which destroys the only comparison that can
tell a stale server from a current one (issue #210):

```bash
go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags)" -o agentsmemory ./cmd/server
```

`AGENTSMEMORY_VERSION` is **per-build, not sticky**: a later rebuild without it
resets the served version to `dev`.

### Docker Compose

```bash
export COMPOSE_FILE=docker-compose.yml:docker-compose.full.yml   # your stack, whatever it is
docker compose pull && docker compose up -d
```

⚠ **Every `-f` flag, every time** — the same rule as installing. Bringing the
stack up with fewer overlays than you started it with does not fail; it starts a
valid *different* stack, and the difference is silent.

⚠ **`scripts/redeploy.sh` reads its compose chain from the running project** —
the container's `config_files` label, by basename, resolved in your checkout —
or from `COMPOSE_FILE` when that is set, and falls back to the two-file chain only
when nothing is running. It used to hardcode two files, which on the documented
four-file local setup silently reverted `RERANK_URL` (issue #209). It also refuses
to build without `AGENTSMEMORY_VERSION` and fails when the served version is not
the stamp (issue #210), and its kit check judges the Claude Desktop bridge binary
alongside the CLI.

What `redeploy.sh` is for is worth keeping even so: it refuses to build over a red
suite, and then **reads the running artifact** to prove the change is live. That
exists because the server once ran a 17-hour-old binary through an entire day of
work and nothing noticed — a build's success is a claim about the build, not about
what is serving.

⚠ **Never read its exit code through a pipe.** `scripts/redeploy.sh | tail`
returns `tail`'s status, so a refused deploy reports success.

### Schema migrations

`goose` owns the schema and migrations are additive `.sql` files embedded in the
binary; they run on start. `gorm` is the query layer only — `AutoMigrate` is never
called.

⚠ **Migration numbers are allocated at merge, never at authoring.** Two branches
authored in parallel otherwise both claim the same number, and a pending migration
sitting *below* the database's maximum applied version makes goose error on the
next start — a crash loop. A fresh or production database never sees it, and no
test can, because tests migrate from empty.

---

## The trap that catches everyone: re-running `install`

⚠ **Re-run `install` the same way you ran it the first time.**

Without `--local` (or `--mcp-url`) the endpoint falls back to the hosted default,
**and the default wins over what is already configured** — so a bare
`aiagentmemory install` on a machine set up with `--local` repoints every
installed hook at the hosted service. The hooks then talk to a server they hold no
credential for, and **a hook that cannot reach its palace goes silent rather than
failing loudly**, so nothing looks broken.

The installer warns before it writes:

```
[!!] this install REPOINTS your hooks: they currently talk to
     http://localhost:8080/mcp, and will now talk to https://aiagentmemory.dev/mcp.
```

To see where your hooks point today:

```bash
grep -o "AGENTSMEMORY_MCP_URL='[^']*'" ~/.claude/settings.json | sort -u
```

The same applies to `--wing`. An install that omits it registers with no wing
header, `default_wing` comes back empty, and every unscoped recall silently widens
to every project in the workspace. Nothing errors; recall just gets worse.

---

## Verify after updating

```bash
aiagentmemory doctor            # registrations, hook events, the Desktop bridge binary
aiagentmemory mcp am_status     # which palace answered, and at what version
```

`am_status` reports `version`: a release tag like `v0.0.113`, or `dev-<commit>` for
an unreleased build. **That is the field that tells a stale palace from a current
one, and nothing else here can.** Compare it against what you just deployed —
if you stamped the build, they match; if it says `dev`, the stamp was missed.

Also worth one look after a server upgrade:

```bash
aiagentmemory mcp am_status     # check the workspace slug, not just that it answered
```

A registration carrying another project's token answers every probe happily. The
workspace slug is the identity check; `mode` only says whether that workspace lives
on the SaaS or on a server you run.

---

## Reading a release before you take it

`CHANGELOG.md` leads each release section with **what the release got wrong
first**, because that is the transferable part. Sections flag three things
explicitly:

- **Changed defaults** — a default that changes owes you a sentence before you
  discover it.
- **Removed environment variables.** These are the ones that go *silent*: an
  unread variable cannot warn you, so anything you set that was retired simply
  stops doing something.
- **Migrations**, and whether rollback is possible.

---

## Platform notes

### macOS
- `sudo` only if the binary lives outside a directory you own.
- Replacing a running binary is safe; open sessions keep the old image.
- Quit Claude Desktop before re-running `--agent claude-desktop` (issue #208).

### Windows
- Close sessions holding the binary before `update` — a running process holds its
  image.
- An earlier `SessionEnd` registration is **retired on upgrade**, deliberately
  (issue #150). If you see both "NOT registered on Windows" and "already
  registered" in one run, the first is the true one (issue #184).

### Linux
- Nothing platform-specific. `sudo` only for a binary outside your own directories.
