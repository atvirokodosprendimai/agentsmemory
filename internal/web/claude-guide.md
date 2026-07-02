# Install agentsmemory into Claude Code

> This guide is written for **Claude** (or any coding agent) to install the
> agentsmemory Claude Code kit and connect a workspace's shared memory. If you are
> a human, the same commands work — just paste your own token.

agentsmemory gives Claude Code a persistent, cross-session **team memory** (the
`am_*` MCP tools) plus a memory-first operating protocol that loads automatically
every session.

## 1. Get the workspace token (ask the user)

The MCP is authed by a per-workspace API token. **Ask the user to provide it** —
do not invent one. To get it, the user:

1. Signs in at {{BASE_URL}} and opens (or creates) a project/workspace.
2. Copies the project's API key — the **Reveal** button shows it.
3. Pastes it to you.

Treat the token as a **secret**: use it only in the install command below, never
echo it back into the chat, a file, or a log.

## 2. Install

The kit ships as a single binary, `aiagentmemory`. Install it globally
(`~/.claude`, wraps the user's existing Claude), non-interactively, with the token
passed as a flag.

**Download only, then run with flags** (matches "download the binary, then run
it"):

```bash
curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh \
  | AIAGENTMEMORY_NO_INSTALL=1 bash
~/.local/bin/aiagentmemory install --global --token <TOKEN>
```

**Or one shot** (download + install in a single command):

```bash
curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh \
  | bash -s -- --global --token <TOKEN>
```

Replace `<TOKEN>` with the value the user gave you. You can also pass it out of
band as the `AGENTSMEMORY_TOKEN` environment variable instead of `--token`.

**Isolated per-project sandbox** (instead of global — keeps this project's config,
MCP servers, and token separate from everything else):

```bash
~/.local/bin/aiagentmemory install --sandbox <name> --token <TOKEN> --recommended
aiagentmemory run <name>          # launch Claude against that sandbox
```

Add `--recommended` to either mode to also install the codebase-memory MCP and the
eidos and codex plugins. Preview any install without touching disk with
`--dry-run`.

## 3. What it installs

- The `/am` and `/M` bootstrap slash commands.
- A Stop hook that reminds Claude to persist the session into team memory.
- An auto-loaded `CLAUDE.md` (via `agentsmemory-bootstrap.md`) so the memory-first
  protocol applies **every session** — the user never has to type `/am`.
- The `agentsmemory` MCP (the `am_*` tools) at `https://aiagentmemory.dev/mcp`,
  authed with the token.

## 4. Verify

```bash
aiagentmemory install --global --dry-run    # prints the full plan, writes nothing
```

Then restart Claude Code (or `/reload`). The memory protocol loads automatically;
run `/am <task>` or `/M <task>` to trigger the full grounding sequence on demand.

## Flags

| Flag | Purpose |
|------|---------|
| `--global` | Install into `~/.claude` non-interactively (skips the mode prompt). |
| `--sandbox <name>` | Install an isolated config at `~/.sandboxes/<name>`. |
| `--token <key>` | Workspace API token (or `AGENTSMEMORY_TOKEN`). |
| `--recommended` | Also install codebase-memory + the eidos and codex plugins. |
| `--dry-run` | Print the plan without writing files or running commands. |
| `--yes`, `-y` | Never prompt (CI). |
