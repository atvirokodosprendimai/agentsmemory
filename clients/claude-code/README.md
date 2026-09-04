# agentsmemory — agent kit (`aiagentmemory`)

A single binary that wires [Claude Code](https://claude.com/claude-code),
[Codex](https://developers.openai.com/codex), [Cursor](https://cursor.com) or
[pi](https://pi.dev) into your
**agentsmemory** workspace: it installs the memory-grounded slash commands and the
Stop hook, registers the agentsmemory MCP, and can optionally pull in the
recommended companion tools. It also wraps the agent CLI so each project can run
against its own isolated configuration.

`--agent claude` is the default; `--agent codex`, `--agent cursor`,
`--agent claude-desktop` and `--agent pi` install into those tools' own layouts,
`--agent both` does Claude + codex (what it always meant) and `--agent all` does
all five.
Everything below describes Claude unless a per-agent column or the
[Codex](#codex) / [pi](#pi) section says otherwise.

**The agents do not offer the same surfaces**, and the kit installs what each one
has rather than pretending:

| | claude | codex | cursor | claude-desktop | pi |
|---|---|---|---|---|---|
| config dir | `~/.claude` | `~/.codex` | `~/.cursor` | `~/Library/Application Support/Claude` | `~/.pi/agent` |
| MCP registered by | `claude mcp add` | `codex mcp add` | writing `mcp.json` — no `mcp add` | writing `claude_desktop_config.json` | bridge extension |
| protocol lands in | `CLAUDE.md` + `@import` | `AGENTS.md` (inlined) | `rules/agentsmemory.mdc` (`alwaysApply: true`) | the MCP handshake — it holds no file | `AGENTS.md` (inlined) |
| slash commands | `commands/` | `prompts/` | none — no commands dir | none | `prompts/` |
| lifecycle hooks | all five | `Stop` — native TOML in `config.toml` | none — hook shape not established | none | in the extension |
| subagent definition | `agents/*.md` | `agents/*.toml` | `agents/*.md` | none | none |
| `--wing` registration scope | header | URL query | header | `mcp-stdio --wing` | bridge environment |
| `--sandbox` | ✅ | ✅ | refused — no config-dir variable | refused — same reason | ✅ |
| needs a host server binary | — | — | — | ✅ the `mcp-stdio` bridge | — |

**`--agent claude-desktop` is the thinnest kit**: an MCP registration and nothing
else, because Desktop has nowhere to put anything else. Its entry spawns the
bridge the server binary already ships:

```json
"agentsmemory": {
  "command": "~/.local/bin/aiagentmemory-server",
  "args": ["mcp-stdio", "--url", "http://localhost:8080/mcp", "--wing", "wing_acme"]
}
```

So it needs that binary on the host — `go build -o ~/.local/bin/aiagentmemory-server
./cmd/server` — and the install REFUSES rather than writing a command that is not
there, because a Docker-only deployment produces none. Restart Claude Desktop
afterwards; it reads the file only at launch.

Cursor needs one manual step afterwards, and the install prints it every time:
`cursor-agent mcp enable agentsmemory`. Cursor gates every MCP server behind an
approval stored outside `mcp.json`, so a registered-but-unapproved server is
byte-identical on disk to a working one — and an installer that approved its own
server would defeat the gate.

It replaces the old `install.sh` shell installer — everything now ships inside
one downloadable binary, `aiagentmemory`.

## Quick install

> Installing for the first time? [INSTALL.md](../../INSTALL.md) is the
> start-to-finish guide, including what differs on macOS, Windows and Linux.
> Upgrading? [UPDATE.md](../../UPDATE.md). Something wired but misbehaving?
> [TROUBLESHOOTING.md](../../TROUBLESHOOTING.md). This file is the kit's own
> reference — every flag, every agent surface, and what lands where.

```bash
curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
```

That bootstrap script downloads the latest `aiagentmemory` binary for your
OS/arch from [GitHub Releases](https://github.com/atvirokodosprendimai/agentsmemory/releases)
into `~/.local/bin`, then runs `aiagentmemory install`. Arguments after `--` are
forwarded to `install`:

```bash
# Isolated install for one project, with all recommended tools:
curl -fsSL <url>/install.sh | bash -s -- --sandbox myproject --recommended

# Non-interactive global install with the token in an env var (no prompts):
curl -fsSL <url>/install.sh | AGENTSMEMORY_TOKEN="<token>" bash -s -- --global
```

For an agent-followable version of these steps, the server also publishes an
install guide **for Claude** at `/claude-guide` (raw Markdown) — it tells the
agent to ask you for the workspace token, then runs the commands above.

Bootstrap environment knobs: `AIAGENTMEMORY_VERSION` (pin a tag),
`AIAGENTMEMORY_BIN_DIR` (install dir, default `~/.local/bin`),
`AIAGENTMEMORY_NO_INSTALL` (download only).

## Two ways to install

| Mode | Command | What it does |
|------|---------|--------------|
| **Global** | `aiagentmemory install` | Wires our MCP + commands + Stop hook into the global `~/.claude` (or `~/.codex` with `--agent codex`). Wraps the agent you already run. |
| **Isolated** | `aiagentmemory install --sandbox <name>` | Installs a self-contained config under `~/.sandboxes/<name>`. Launch the agent against it with `aiagentmemory run <name>` — its commands, settings, MCP servers, and token stay isolated from every other project. |

Add `--recommended` to either mode to also install the ecosystem tools (see
below), and `--agent codex` / `--agent both` to install for codex as well (see
[Codex](#codex)).

Isolation works by pinning the agent's own config-dir variable at launch —
`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PI_CODING_AGENT_DIR`. **Cursor exposes none**,
so `--agent cursor` with `--sandbox` or `--config-dir` is refused rather than
writing a complete, correct kit into a directory Cursor will never open and
reporting success.

## What gets installed

**Core (always):**

- ⚠ `commands/M.md` and the **`/M`** command were RETIRED — a second grounding
  sequence beside `/am` is a second copy of one protocol, and the installer now
  removes it from config dirs that still carry it. What it used to be (language/UI idioms,
  codebase-memory, and memory-palace grounding).
- `commands/am.md` → the **`/am`** bootstrap command (agentsmemory-native `am_*`
  tools).
- `agentsmemory-stop-hook.sh` → registered in `settings.json` for **two** events
  (idempotent, with a timestamped backup; no `jq` needed). On `Stop` it is the
  end-of-turn checkpoint; on `UserPromptSubmit` a second recall hook asks the
  palace about the task the user just described, in their own words, and injects
  what it finds before the turn starts; on `SubagentStop` it asks a finishing subagent for what
  it FOUND — a drawer and a fact, not a session summary. One script, branching on
  the event, because the two nudges differ in text and not in machinery. It sits
  flat in the config dir, not under `hooks/`: a sandbox can be shared with pi,
  which halts its launch on any `hooks/` directory it finds.
- `agentsmemory-anchor-cue-hook.sh` → registered on **`PreToolUse`**. When a tool is
  about to open a file, it lists the memories PINNED to exactly that path and puts
  them in front of the model, without anything being asked. It issues no query: a
  code anchor is an exact pin, so the lookup is a join on a path the tool call
  already names. It prints nothing for a path no memory pins, which is the common
  case. Registered matcher-less like every plan here — the script exits silently
  when the event carries no `file_path`, which is the guard a matcher would only
  duplicate.
- `agentsmemory-touched-hook.sh` → registered on **`PostToolUse`**, write tools only.
  It appends each edited path to a session-scoped list and says nothing to the
  model; the `Stop` hook reads that list and NAMES the files, which turns the
  end-of-turn nudge from "persist something" into a question with an answer in it.
  It is not the `PostToolUse` audit ADR-041 rejected — it delivers no verdict, so
  there is nothing to report late.
- The task-recall script is registered on **`UserPromptSubmit`** and
  **`UserPromptExpansion`**, branching on `hook_event_name`. The submit branch
  REFUSES a slash command on purpose — `/am` is a command name, and recalling
  against it retrieves whatever is nearest to one — so the expansion branch is
  what gives those turns a recall at all, asked against the text the command
  expanded into. One script, because the two differ in which text they ask with
  and not in machinery.
- `agentsmemory-verify-hook.sh` → the `SessionStart` hook: before a session acts
  on anything, it checks that memories carrying code anchors still match the code.
  Detection that arrives after the wrong decision is not detection.
- `agentsmemory-session-end-hook.sh` → the `SessionEnd` hook: what recall actually
  did across the whole session. It is the only one of the hooks that can report
  that, because at `Stop` the session has barely begun.
  ⚠ **Not registered on Windows**, and retired there on upgrade: process creation
  costs ~1s per spawn, the hook needs ~3.2s, and it loses the teardown race, so
  every exit reported `Hook cancelled` (#150). Ask the server instead —
  `curl -fsS "${AGENTSMEMORY_MCP_URL%/mcp}/stats?hours=2"`.
- `agentsmemory-recall-hook.sh` → a second `SessionStart` hook: it PERFORMS a
  recall for the current branch and injects the result, so a fresh context does
  not start blind. It is the one mechanism here that asks nothing of the agent —
  ADR-017 named it in 2026-08 ("a subagent cannot skip a recall that already
  happened") and left it unbuilt pending measurement, which ADR-041 supplied. It
  prints nothing when the recall returns nothing; `AGENTSMEMORY_RECALL=off`
  disables it.

  It shipped first on `PreCompact` and could not work there: Claude Code adds a
  hook's plain stdout to the model's context for `SessionStart`,
  `UserPromptSubmit`, `UserPromptExpansion` and `PostModelSwitch` only, and writes
  every other event's stdout to the debug log. A hook on any other event can still
  reach the model by returning `hookSpecificOutput.additionalContext` instead of
  plain text — that is how the `SubagentStart` and `PreToolUse` hooks here work. The recall ran and was discarded.
  `SessionStart` is also the correct side of a compaction — output injected
  before one is part of the context being compacted. `TestEveryInjectingHookIsOnAnInjectingEvent`
  is what keeps that a gate rather than a paragraph.
- `agentsmemory-bootstrap.md` → the always-on operating protocol, pulled into
  the config dir's `CLAUDE.md` via a managed `@agentsmemory-bootstrap.md` import.
  Claude Code loads `$CLAUDE_CONFIG_DIR/CLAUDE.md` as user memory, so the
  memory-first workflow applies **every session** — you never have to type `/am`.
  Subagents too: a `SubagentStart` hook puts the recall instruction next to the
  dispatched task. That is not redundant with the protocol above it — measured on
  a 449-memory palace, subagents receiving the full protocol and nothing else
  recalled in **0 of 5** dispatches, and **5 of 5** with the injection.
  A `SubagentStop` hook closes the other half: a subagent is asked for what it
  FOUND — a drawer, a fact — not for a session summary, which its dispatcher
  writes. Set `AGENTSMEMORY_SUBAGENT_STOP_HOOK=off` to keep the session
  checkpoint and drop the subagent one; a wide fan-out pays one extra turn per
  branch.
- `agents/agentsmemory-researcher.md` → a read-only research subagent whose
  `tools:` allowlist names the `am_*` tools. An agent definition that restricts
  tools can call only what it lists, so one defined without them cannot recall
  however it is instructed.
  The import is merged idempotently: an existing `CLAUDE.md` is preserved and
  backed up, and only the one managed block is added or updated.
- The **agentsmemory MCP** — the remote Streamable-HTTP server at
  `https://aiagentmemory.dev/mcp`, authed by your workspace token (see below).

> The legacy verbose `/agentsmemory` command has been retired, and `/M` with it — only
> `/am` ship now.

**Recommended (`--recommended`):**

| Tool | How it is installed |
|------|---------------------|
| [codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp) | Upstream `curl \| bash` installer, then registered as the `codebasememory` stdio MCP. |
| [codex](https://github.com/openai/codex-plugin-cc) plugin | `plugin marketplace add openai/codex-plugin-cc` + `plugin install codex@openai-codex`. |

Recommended steps are best-effort: a plugin that is already installed or a
network hiccup is reported but does not abort the install.

## The MCP token

The agentsmemory MCP is authed by a per-workspace API key (create a project in
the dashboard and copy or **Reveal** its key). `install` resolves it from, in
order:

1. `--token <key>` flag, or the `AGENTSMEMORY_TOKEN` environment variable;
2. an interactive prompt (works even through `curl | bash`, which reads
   `/dev/tty`).

With no token and no terminal (CI), the MCP step is skipped with a copy-paste
hint so you can add it later.

## Commands

```text
aiagentmemory install [flags]                  install the kit (global, or --sandbox <name>)
aiagentmemory install --agent codex [flags]    same, into ~/.codex (or --agent both)
aiagentmemory install --agent pi [flags]       same, into ~/.pi/agent (or --agent all)
aiagentmemory update [flags]                   replace the binary in place (configs untouched)
aiagentmemory update-skill [flags]             refresh the protocol + slash commands from GitHub
aiagentmemory init --sandbox <name> [-- args]  record how THIS project launches
aiagentmemory load [-- extra args]             launch this project's agent + sandbox + flags
aiagentmemory run <name> [args]                run Claude against sandbox ~/.sandboxes/<name>
aiagentmemory run --agent codex <name> [args]  run codex against that sandbox (pins CODEX_HOME)
aiagentmemory run --agent pi <name> [args]     run pi against that sandbox (pins PI_CODING_AGENT_DIR)
aiagentmemory run claude [args]                no such sandbox → run Claude against the global config
aiagentmemory wrap [args]                      run Claude against the global config
aiagentmemory wrap --agent codex [args]        run codex against ~/.codex
aiagentmemory mcp                              list the memory tools you can call
aiagentmemory mcp <tool> [arg] [-a k=v]        call one and print what it returns
aiagentmemory doctor [--agent <a>]             can the installed hooks reach a session?
```

`--agent` is only read in the leading position of `run`/`wrap` — everything after
the sandbox name is forwarded to the agent untouched.

### `doctor` — are the hooks actually wired?

A hook is two things: a script, and a line in `settings.json` selecting it. The
script half is easy to check and the registration half is where installs break —
hand-edited settings, a config dir inherited with `--copy`, or an older install that
registered a hook on an event Claude Code no longer injects.

```console
$ aiagentmemory doctor
config:  ~/.claude
project: ~/code/your-repo

  agentsmemory-recall-hook.sh            SessionStart   speaks       3909 bytes
      | agentsmemory-recall: query=… room=diary max_distance=0.42 count=1
  agentsmemory-verify-hook.sh            SessionStart   silent       no output; see its stderr for what it asked

  all 2 injecting hook(s) are registered on an injecting event and ran
```

It exits non-zero on three states, and only these three:

| verdict | what it means |
|---|---|
| `UNREGISTERED` | the script is installed and no event runs it — re-run `install` |
| `DISCARDED` | registered on an event whose PLAIN stdout goes to the debug log; only `SessionStart`, `UserPromptSubmit`, `UserPromptExpansion` and `PostModelSwitch` reach the model that way. A hook declaring a structured channel is not judged here — it injects via `additionalContext` instead |
| `FAILED` | it exited non-zero; the indented line under it is the hook's own stderr |

**`silent` is not a failure.** Both of these hooks are silent when everything is
fine — the verify hook prints only when a memory drifted, the recall hook only when
the palace has something for your branch. Nothing can tell that apart from a broken
hook in one run, so `doctor` prints what each hook wrote to stderr (which no event
injects, and which the model therefore never sees) and lets you read it.

### `install` flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--agent <name>` | `claude` | Agent to install for: `claude`, `codex`, `pi`, `both` (claude+codex) or `all`. |
| `--global` | — | Install into the agent's global config dir non-interactively (skips the mode prompt); mutually exclusive with `--sandbox`/`--claude-dir`. |
| `--sandbox <name>` | — | Install into `~/.sandboxes/<name>` (isolated mode). |
| `--copy` | off | Seed the target from the agent's global config — logins, MCP servers, plugins, skills, settings. Needs `--sandbox`/`--config-dir`. |
| `--shared-auth` | off | Link the target's credential files to the global config, so one login serves every sandbox. |
| `--recommended` | off | Also install codebase-memory and the Claude-only codex review plugin. |
| `--token <key>` | `$AGENTSMEMORY_TOKEN` | agentsmemory workspace token. |
| `--mcp-url <url>` | `https://aiagentmemory.dev/mcp` | agentsmemory MCP endpoint. |
| `--wing <name>` | — | Scope every MCP call from this registration to one project; a tool call can still pass `wing: "*"` for deliberate cross-project recall. |
| `--scope <scope>` | `user` | Claude MCP/plugin scope: `user`, `local`, `project` (Claude only — codex has no scopes). |
| `--claude-bin <bin>` | `$AIAGENTMEMORY_CLAUDE_BIN` → `claude` | Claude CLI to drive. |
| `--codex-bin <bin>` | `$AIAGENTMEMORY_CODEX_BIN` → `codex` | codex CLI to drive. |
| `--pi-bin <bin>` | `$AIAGENTMEMORY_PI_BIN` → `pi` | pi CLI to drive. |
| `--claude-dir <dir>` | the agent's global dir | Override the target config dir (ignored with `--sandbox`). |
| `--yes`, `-y` | off | Non-interactive: never prompt. |
| `--dry-run` | off | Print the full plan without writing files or running commands. |

`--dry-run` is the safe way to see exactly what will happen — every file write
and every agent CLI call is printed.

## Reading your memory from the shell (`mcp`)

`aiagentmemory mcp` calls the memory tools yourself, against the same endpoint,
with the same token, over the same transport your agents use — so what you see is
what the agent sees. It is the way to check what a tool actually returns without
asking an agent to relay it.

```bash
aiagentmemory mcp                                  # the tools you can call
aiagentmemory mcp status                           # workspace, wings, quota
aiagentmemory mcp search "auth bug"                # semantic recall
aiagentmemory mcp search "auth bug" -a limit=3 -a wing=wing_api
aiagentmemory mcp get_drawer <id>
aiagentmemory mcp search "auth bug" | jq '.hits[].room'
```

- **The bare positional fills the tool's first required argument**, so
  `mcp search "x"` means `-a query=x`. Everything else goes in as `-a key=value`
  (repeatable). Values are typed from the tool's own schema — `-a limit=3` crosses
  the wire as the number `3`.
- **Tool names work with or without the `am_` prefix**: `mcp search` = `mcp
  am_search`.
- **Output is JSON on stdout**, indented and pipeable; notes and errors go to
  stderr. `--raw` prints the whole MCP envelope instead, and `mcp --raw` prints
  the full catalogue including every tool's input schema.
- **It is read-only.** The endpoint exposes write tools, but the CLI refuses them
  — a mistyped shell command must never mutate team memory. Ask your agent for
  writes.
- **The token is found for you**, in this order: `--token` /
  `$AGENTSMEMORY_TOKEN`; then an install on this machine — `agentsmemory.env`
  (codex, pi) or the `agentsmemory` MCP registration in `.claude.json` (Claude).
  `--sandbox <name>` reads one sandbox's install, `--config-dir <dir>` any other.
  With neither, `$HOME` and the three global config dirs are searched. The line on
  stderr says which file the token came from.

| Flag | Default | Purpose |
|------|---------|---------|
| `-a`, `--arg <k=v>` | — | Tool argument, repeatable. |
| `--token <key>` | `$AGENTSMEMORY_TOKEN` | Workspace token (default: read from an install). |
| `--sandbox <name>` | — | Take the token from `~/.sandboxes/<name>`. |
| `--config-dir <dir>` | — | Take the token from an install in this dir. |
| `--mcp-url <url>` | `$AGENTSMEMORY_MCP_URL` → `https://aiagentmemory.dev/mcp` | Endpoint to call. |
| `--raw` | off | Print the MCP envelope (content blocks, `isError`) instead of the result. |
| `--timeout <dur>` | `60s` | Give up on the endpoint after this long. |

## Codex

`--agent codex` installs the same kit into codex's own layout. Codex is
configured the same way Claude is, with different filenames, so the kit is the
same content in different places:

| | Claude Code | Codex |
|---|---|---|
| Config dir | `~/.claude`, relocated by `CLAUDE_CONFIG_DIR` | `~/.codex`, relocated by `CODEX_HOME` |
| Slash commands | `commands/*.md` → `/am` | `prompts/*.md` → `/prompts:am` |
| Always-on memory | `CLAUDE.md` + a managed `@agentsmemory-bootstrap.md` import | `AGENTS.md` with the protocol **inlined** in the managed block — codex has no `@import` |
| Stop hook | `settings.json` | native TOML in `config.toml` (same `Stop` event and `stop_hook_active` loop guard) |
| MCP auth/scope | `--header "Authorization: Bearer <token>"` plus `X-Agentsmemory-Wing` | `--bearer-token-env-var AGENTSMEMORY_TOKEN`; `--wing` is encoded in the registered URL because Codex has no arbitrary-header flag |
| Recommended | codebase-memory + codex review plugin | codebase-memory only (the review plugin is for Claude) |

On upgrade, the installer writes the native TOML registration first and then
removes only agentsmemory's command from its previous `hooks.json`
representation. Codex supports and merges both forms, but warns when the same
config layer uses both. If another hook still lives in JSON, the installer
preserves the file and warns that Codex may keep reporting two representations.

Codex 0.144.5 also exposes `SubagentStart` and `SubagentStop`, but the installer
does not register those yet. Event names alone do not prove the contract our
Claude scripts depend on: a live Codex dispatch still has to capture the input
fields, stdout feedback envelope, and exit-2 single-retry behaviour. See
[ADR-017's amended deferral](../../docs/adr/ADR-017-a-subagent-is-a-session.md).

One codex-specific step remains for launches outside the wrapper:

1. **Have the token in the environment.** The install writes it to
   `<CODEX_HOME>/agentsmemory.env` (mode `0600`) and
   `aiagentmemory run --agent codex …` / `wrap --agent codex …` export it for
   you. To launch plain `codex`, source it from your shell rc:

   ```bash
   set -a; . ~/.codex/agentsmemory.env; set +a
   ```

A codex **sandbox** is a whole `CODEX_HOME`, and codex keeps `auth.json` there —
so a fresh sandbox starts logged out. Log it in once:

```bash
aiagentmemory install --agent codex --sandbox myproject
CODEX_HOME=~/.sandboxes/myproject codex login
aiagentmemory run --agent codex myproject
```

Both agents can share one sandbox (`--agent both --sandbox myproject`): they
never collide on a filename, so `CLAUDE_CONFIG_DIR` and `CODEX_HOME` can point at
the same directory.

## pi

`--agent pi` installs the same kit into [pi](https://pi.dev). Structurally pi
looks like codex — `prompts/` for slash commands, `AGENTS.md` for always-on
memory — with one difference that changes how the MCP is wired: **pi ships no MCP
client and no hook system**, both deliberately ("intentionally does not include
built-in MCP", pi's own docs). So the kit brings its own.

| | Codex | pi |
|---|---|---|
| Config dir | `~/.codex`, relocated by `CODEX_HOME` | `~/.pi/agent`, relocated by `PI_CODING_AGENT_DIR` |
| Slash commands | `prompts/*.md` → `/prompts:am` | `prompts/*.md` → `/am` (no namespace) |
| Always-on memory | `AGENTS.md`, protocol inlined | `AGENTS.md`, protocol inlined (pi has no `@import` either) |
| Stop hook | `config.toml` | **none** — pi renamed `hooks/` to extensions |
| Our MCP | `codex mcp add --bearer-token-env-var` | **bridged** by `extensions/agentsmemory.ts` |

The bridge extension is written to `<config dir>/extensions/agentsmemory.ts`,
where pi auto-discovers it. On startup it performs the MCP handshake against your
workspace endpoint, lists the tools, and re-registers each one as a native pi
tool — so `am_search`, `am_diary_write` and the rest are callable exactly as they
are in Claude and codex. The same extension fires the end-of-turn memory
checkpoint the Stop hook provides on the other two agents (`AGENTSMEMORY_STOP_HOOK=off`
or `=once` still applies).

Two pi-specific notes:

1. **The token travels in the environment.** The install writes it plus the
   endpoint to `<config dir>/agentsmemory.env` (mode `0600`), and
   `aiagentmemory run --agent pi …` exports both. For plain `pi`, source it:

   ```bash
   set -a; . ~/.pi/agent/agentsmemory.env; set +a
   ```

2. **`--recommended` adds nothing.** codebase-memory is a stdio MCP server and
   the codex review plugin is a Claude marketplace; pi takes neither. The installer
   says so rather than pretending.

pi also **halts its launch on any `hooks/` directory** in the config dir — it
reads one as its own deprecated layout and waits for a keypress. That is why the
Claude/codex Stop hook installs flat as `<config dir>/agentsmemory-stop-hook.sh`;
a config dir written by an older release still has `hooks/`, and re-running the
Claude or codex install relocates the script and prunes the stale registration. A
pi-only install will not delete it — the script belongs to another agent, whose
registration would then point at nothing — it prints the command that will.

One more shared-sandbox caveat: pi renames `commands/` to `prompts/` on startup
**if `prompts/` does not exist**. Installing the pi (or codex) kit creates
`prompts/`, so the rename never fires; but pointing pi at a Claude-only config dir
will move Claude's slash commands out from under it. Install the kit for every
agent that will open the sandbox.

A pi **sandbox** is the whole agent dir, `auth.json` included, so it starts with
no provider credentials:

```bash
aiagentmemory install --agent pi --sandbox myproject
aiagentmemory run --agent pi myproject      # sign in inside it, or pass --api-key
```

All three agents can share one sandbox (`--agent all --sandbox myproject`) — no
two of them collide on a filename.

## Inheriting your global setup (`--copy`)

A fresh sandbox starts empty: signed out, no MCP servers, no plugins, none of
your skills. `--copy` seeds it from the agent's own global config dir before the
kit is installed:

```bash
aiagentmemory install --agent pi --sandbox acme --copy       # from ~/.pi/agent
aiagentmemory install --agent all --sandbox acme --copy      # each agent from its own global dir
```

**What travels:** credentials (`auth.json`, `models-store.json` — so pi arrives
with your providers already logged in), `settings.json` / `config.toml`,
`.claude.json` (which is where Claude keeps its MCP servers), `plugins/`,
`skills/`, `extensions/`, `themes/`, `prompts/` and `commands/`.

**What stays behind:** conversation and project state (`projects/`, `sessions/`,
`history.jsonl`), logs and `*.sqlite*` stores, caches, `bin/` and other extracted
binaries, `.bak` files. That exclusion is what makes the copy usable — a global
`~/.codex` here is 795 MB, of which ~440 MB is runtime state.

It is still not small: with plugins, expect roughly 230 MB (Claude) or 350 MB
(codex) per sandbox. The installer prints the byte count it copied.

Two rules the copy follows:

- **It never overwrites.** Anything already in the target wins, so `--copy` on an
  existing sandbox fills gaps rather than reverting your changes, and the kit's
  own files are written afterwards, on top.
- **Modes are preserved.** `auth.json` stays `0600`; a copied credential is never
  widened. Note what that implies — **the sandbox can act as you** until you sign
  it out. Copy your own config, not someone else's.

A Stop hook registration inherited from the source config dir is retired
automatically: it points at *that* dir's script, so left alone it would fire the
memory checkpoint twice per stop.

## Sharing one login across sandboxes (`--shared-auth`)

`--copy` gives a sandbox a *snapshot* of your credentials; when a token expires
you re-authenticate in each sandbox separately. `--shared-auth` links them
instead:

```bash
aiagentmemory install --agent pi --sandbox acme --shared-auth
# ~/.sandboxes/acme/auth.json         -> ~/.pi/agent/auth.json
# ~/.sandboxes/acme/models-store.json -> ~/.pi/agent/models-store.json
```

Log in anywhere — the global agent or any sandbox — and every sandbox sees it at
once. What gets linked is per agent:

| Agent | Credential files | Note |
|---|---|---|
| Claude Code | `.credentials.json` | On macOS credentials live in the login **keychain**, which every config dir already shares — the flag reports there is nothing to link. |
| Codex | `auth.json` | |
| pi | `auth.json`, `models-store.json` | The model store rides along, or a sandbox is authenticated for models it does not list. |

An existing credential file in the target is moved aside (`.bak.<ts>`) before the
link replaces it, and a link that is already correct is left alone, so re-running
the install is a no-op.

**The one failure mode**, and how you find out: an agent that rewrites
credentials by replacing the file (write a temp file, rename over the target)
destroys the link, and the sandbox silently stops sharing. pi writes in place
(`writeFileSync`), so it writes *through* the link — verified. For any agent that
does not, `aiagentmemory run` checks the link on every launch and tells you:

```
aiagentmemory: auth.json no longer shared with the global config (the agent replaced the link)
  re-share with: aiagentmemory install --agent pi --config-dir ~/.sandboxes/acme --shared-auth --yes
```

Nothing is repaired automatically — which side holds the credential you want is
your call, not ours.

## Updating

Upgrading the CLI is **binary-only** — you do not re-run `install`, so nothing
under `~/.claude` or `~/.sandboxes` is rewritten and your MCP registration,
slash commands, Stop hook and workspace token stay exactly as they are:

```bash
aiagentmemory update              # upgrade to the latest release
aiagentmemory update --check      # just report installed vs latest
aiagentmemory update --version v0.0.46   # pin, or roll back
```

macOS note: if the binary lives somewhere you do not own (e.g. `/usr/local/bin`)
the swap needs `sudo aiagentmemory update`; the default `~/.local/bin` install
never does. Update a copy elsewhere with `--bin <path>`.

How it works: the new asset is downloaded next to the current binary, run once
with `--version` to prove it is intact and the right architecture, and only then
renamed over the old file — an atomic swap, so a failed or interrupted download
leaves the working binary in place. Replacing a running binary is safe on macOS
and Linux; an already-open session keeps running the old image.

### Refreshing the protocol and commands (`update-skill`)

`update` deliberately leaves your config dir alone, which means the memory
protocol and slash commands stay at whatever version was installed the day the
kit went in. `update-skill` is the other half — it fetches
`agentsmemory-bootstrap.md` and the `/am` and `/load-skill` commands from
GitHub and writes them into a config dir:

```bash
aiagentmemory update-skill                      # the global install
aiagentmemory update-skill --sandbox aks        # an isolated sandbox
aiagentmemory update-skill --sandbox aks --agent all   # claude + codex + pi in it
aiagentmemory update-skill --check              # what would change, writes nothing
aiagentmemory update-skill --ref main           # track a branch instead of a release
```

It is as narrow as `update` is, from the other side: the binary, your MCP
registration, workspace token and Stop hook are all left untouched. The Stop
hook and the pi bridge extension are excluded on purpose even though they are
kit assets too — both are executable code, and quietly downloading a shell
script over an existing one is a bigger act than refreshing documentation. Run
`install` when you want those.

The whole kit is downloaded before anything is written, so a failed fetch leaves
your config dir exactly as it was rather than half-updated. `--ref` defaults to
the latest release tag, so `update-skill` and `update` track the same version by
default; the files come from the repository tree, since a release publishes
binaries only. On codex and pi the protocol is inlined into `AGENTS.md` and on
Claude it is imported from `CLAUDE.md`, exactly as `install` does it — your own
content outside the managed block is preserved, and the file is backed up before
it changes.

## Updating a binary too old to have `update`

Releases before `update` existed have no self-update. Re-download it without
running the installer — same result, no config touched:

```bash
AIAGENTMEMORY_NO_INSTALL=1 curl -fsSL \
  https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
```

`AIAGENTMEMORY_NO_INSTALL` is what makes it download-only; `AIAGENTMEMORY_VERSION`
pins a tag and `AIAGENTMEMORY_BIN_DIR` changes the destination.

## Sandboxes

A sandbox is just a Claude config directory under `~/.sandboxes/<name>`. Running
Claude with `CLAUDE_CONFIG_DIR` pointed at it isolates that project's commands,
settings, MCP servers, and agentsmemory token from everything else. `run` does
exactly that and then execs Claude, inheriting your terminal and its exit code:

```bash
aiagentmemory install --sandbox acme --recommended   # set up once
aiagentmemory run acme                                # launch Claude in it
aiagentmemory run acme -p "summarise the repo"        # args pass through to claude
```

`wrap` is the global counterpart — it runs Claude against `~/.claude` with no
override.

The Claude CLI it drives is resolved from `AIAGENTMEMORY_CLAUDE_BIN`, then
`claude` on `PATH`.

### `run` with an agent name

`<name>` is a sandbox first. If no `~/.sandboxes/<name>` exists and the name is a
known agent CLI (`claude`, `codex`, `gemini`), `run` launches that agent against
the global config instead of erroring — so the obvious line just works:

```bash
aiagentmemory run claude              # no sandbox called "claude" → global config
aiagentmemory run claude -p "hi"      # args still pass through
```

Any other unknown name keeps the old behaviour and points you at
`install --sandbox <name>`. A sandbox always wins: create `~/.sandboxes/claude`
and `run claude` means that sandbox.

### Environment variables

The agent replaces this process (`exec`) and inherits your **full environment**,
so anything you export or prefix reaches Claude unchanged:

```bash
SET_NEW_ENV=1 aiagentmemory run acme          # SET_NEW_ENV=1 is visible to claude
ANTHROPIC_MODEL=... aiagentmemory run claude  # so is this
```

The only variable `run` adds is `CLAUDE_CONFIG_DIR`, set to the sandbox dir (and
left alone in global mode).

## Per-project launch (`init` / `load`)

`run acme --model opus --append-system-prompt "..."` is a lot to retype, and the
sandbox name in it is **personal** — your teammate's sandbox for the same repo is
called something else. `init` records the launch once, `load` performs it:

```bash
cd ~/code/myproj
aiagentmemory init --sandbox acme --agent claude -- --model opus
aiagentmemory load          # → claude, in sandbox acme, with --model opus
```

Everything after `--` is stored verbatim and handed to the agent by `load`.

### What is written where

The two halves of a launch have different owners, so they live in different files:

| File | Holds | Commit it? |
|------|-------|------------|
| `<project>/.aiagentmemory` | `agent=` and `args=` — the team-wide choice | **yes** |
| `~/.sandboxes/agents` | `<project dir>=<sandbox>`, one line per project | no — machine-local |
| `<project>/.aiagentmemory.local` | optional personal override, same format | no — git-ignore it |

The sandbox name is deliberately **absent** from the committed file. If it were
there, every teammate who named their sandbox differently would launch the wrong
config — or nothing at all. They run `aiagentmemory init --sandbox <their-name>`
once and the shared file never changes.

### Which sandbox wins

`load` resolves the sandbox from the first layer that has one, most specific
first, and prints which layer it used:

```text
--sandbox  >  $AIAGENTMEMORY_SANDBOX  >  ~/.sandboxes/agents  >  .aiagentmemory.local  >  .aiagentmemory
```

```bash
aiagentmemory load --sandbox other            # one-off, this launch only
AIAGENTMEMORY_SANDBOX=other aiagentmemory load # same, from the environment
aiagentmemory load -- --verbose                # extra args appended after the recorded ones
```

`--agent` and the recorded `args` follow the same ladder minus the env var; a
personal `args=` replaces the committed list whole rather than merging into it.
With no sandbox on any layer, `load` fails and tells you to run `init` — it never
falls back to the global config, since launching unpinned would defeat the point
while looking like success.

### One entry can cover a whole tree

The registry is matched by **nearest ancestor**, so a single line pins every
repository beneath a directory, and a per-repo line still overrides it:

```text
# ~/.sandboxes/agents
/Users/me/code=work            # every repo under ~/code launches in "work"
/Users/me/code/myproj=acme     # …except myproj, which launches in "acme"
```

Both lookups walk upward, so `load` behaves identically from the repository root
and from a directory deep inside it — it finds the nearest `.aiagentmemory` and
names the file it used when that is not your current directory.

### `init` flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--sandbox <name>` | — | Sandbox this project launches with. Recorded in `~/.sandboxes/agents`, never in the committed file. Warns (does not fail) if the sandbox does not exist yet. |
| `--agent <name>` | `claude` | Agent to launch: `claude`, `codex` or `pi`. Recorded in `.aiagentmemory`. |

`init` is declarative — it rewrites `.aiagentmemory` from the flags you give it,
so re-running it without `--` args clears any previously recorded agent flags.

## The Stop hook

On each turn end the hook reminds Claude to persist the session into team memory
(`am_diary_write`, `am_kg_add`, `am_add_drawer`). Control it with
`AGENTSMEMORY_STOP_HOOK`: `once` (default — first Stop of a session only), `on`
(every Stop), or `off`.

## Build from source

```bash
go build -o aiagentmemory ./clients/claude-code
./aiagentmemory install --help
```

Releases are cross-compiled for linux/darwin on amd64/arm64 by the `release`
GitHub workflow on every `vX.Y.Z` tag.

## Uninstall

Remove the installed pieces from the target config dir (`~/.claude` or
`~/.sandboxes/<name>`):

```bash
rm ~/.claude/commands/am.md
rm ~/.claude/agentsmemory-stop-hook.sh
rm ~/.claude/agentsmemory-bootstrap.md
# then, in ~/.claude/CLAUDE.md, delete the managed block between
#   <!-- BEGIN agentsmemory ... -->  and  <!-- END agentsmemory -->
# remove the agentsmemory entry from the Stop array in ~/.claude/settings.json,
# and: claude mcp remove --scope user agentsmemory
```

Delete a whole sandbox with `rm -rf ~/.sandboxes/<name>`.
