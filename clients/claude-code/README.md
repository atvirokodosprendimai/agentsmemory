# agentsmemory — Claude Code kit (`aiagentmemory`)

A single binary that wires [Claude Code](https://claude.com/claude-code) into
your **agentsmemory** workspace: it installs the memory-grounded slash commands
and the Stop hook, registers the agentsmemory MCP, and can optionally pull in the
recommended companion tools. It also wraps the Claude CLI so each project can run
against its own isolated configuration.

It replaces the old `install.sh` shell installer — everything now ships inside
one downloadable binary, `aiagentmemory`.

## Quick install

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
```

Bootstrap environment knobs: `AIAGENTMEMORY_VERSION` (pin a tag),
`AIAGENTMEMORY_BIN_DIR` (install dir, default `~/.local/bin`),
`AIAGENTMEMORY_NO_INSTALL` (download only).

## Two ways to install

| Mode | Command | What it does |
|------|---------|--------------|
| **Global** | `aiagentmemory install` | Wires our MCP + commands + Stop hook into the global `~/.claude`. Wraps the Claude you already run. |
| **Isolated** | `aiagentmemory install --sandbox <name>` | Installs a self-contained config under `~/.sandboxes/<name>`. Launch Claude against it with `aiagentmemory run <name>` — its commands, settings, MCP servers, and token stay isolated from every other project. |

Add `--recommended` to either mode to also install the ecosystem tools (see
below).

## What gets installed

**Core (always):**

- `commands/M.md` → the **`/M`** bootstrap command (mempalace + codebase-memory
  + eidos flavour).
- `commands/am.md` → the **`/am`** bootstrap command (agentsmemory-native `am_*`
  tools).
- `hooks/agentsmemory-stop-hook.sh` → the Stop hook, registered in
  `settings.json` (idempotent, with a timestamped backup; no `jq` needed).
- The **agentsmemory MCP** — the remote Streamable-HTTP server at
  `https://aiagentmemory.dev/mcp`, authed by your workspace token (see below).

> The legacy verbose `/agentsmemory` command has been retired — only `/M` and
> `/am` ship now.

**Recommended (`--recommended`):**

| Tool | How it is installed |
|------|---------------------|
| [codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp) | Upstream `curl \| bash` installer, then registered as the `codebasememory` stdio MCP. |
| [eidos](https://github.com/agenticnotetaking/eidos) plugin | `plugin marketplace add agenticnotetaking/eidos` + `plugin install eidos@eidos`. |
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
aiagentmemory install [flags]     install the kit (global, or --sandbox <name>)
aiagentmemory run <name> [args]   run Claude against sandbox ~/.sandboxes/<name>
aiagentmemory wrap [args]         run Claude against the global config
```

### `install` flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--sandbox <name>` | — | Install into `~/.sandboxes/<name>` (isolated mode). |
| `--recommended` | off | Also install codebase-memory, eidos, codex. |
| `--token <key>` | `$AGENTSMEMORY_TOKEN` | agentsmemory workspace token. |
| `--mcp-url <url>` | `https://aiagentmemory.dev/mcp` | agentsmemory MCP endpoint. |
| `--scope <scope>` | `user` | Claude MCP/plugin scope: `user`, `local`, `project`. |
| `--claude-bin <bin>` | `$TEISORA_CLAUDE_BIN` → `teisora-claude` → `claude` | Claude CLI to drive. |
| `--claude-dir <dir>` | `~/.claude` | Override the target config dir (ignored with `--sandbox`). |
| `--yes`, `-y` | off | Non-interactive: never prompt. |
| `--dry-run` | off | Print the full plan without writing files or running commands. |

`--dry-run` is the safe way to see exactly what will happen — every file write
and every Claude CLI call is printed.

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

The Claude CLI it drives is resolved from `TEISORA_CLAUDE_BIN`, then
`teisora-claude`, then `claude` on `PATH`.

## The Stop hook

On each turn end the hook reminds Claude to persist the session into team memory
(`am_diary_write`, `am_kg_add`, `am_add_drawer`). Control it with
`AGENTSMEMORY_STOP_HOOK`: `on` (default), `once` (first Stop only), or `off`.

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
rm ~/.claude/commands/M.md ~/.claude/commands/am.md
rm ~/.claude/hooks/agentsmemory-stop-hook.sh
# then remove the agentsmemory entry from the Stop array in ~/.claude/settings.json,
# and: claude mcp remove --scope user agentsmemory
```

Delete a whole sandbox with `rm -rf ~/.sandboxes/<name>`.
