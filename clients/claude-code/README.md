# agentsmemory — Claude Code kit

Drop-in [Claude Code](https://claude.com/claude-code) integration for the
**agentsmemory** MCP: a startup slash command that grounds Claude in your team
memory, and a Stop hook that reminds it to write back before each turn ends.

It mirrors the mempalace pattern, but talks to *your* agentsmemory workspace over
MCP — everything is scoped to your team by the bearer token, so you never pass a
team id.

## 1. Connect the MCP

agentsmemory serves a stateless Streamable-HTTP MCP endpoint at `/mcp`, authed by
a workspace API key (create a project in the dashboard, copy or **Reveal** its
key). Add it to your project's `.mcp.json` (or `~/.claude/.mcp.json` for all
projects):

```json
{
  "mcpServers": {
    "agentsmemory": {
      "type": "http",
      "url": "https://YOUR-HOST/mcp",
      "headers": { "Authorization": "Bearer YOUR-API-KEY" }
    }
  }
}
```

The server name you choose (`agentsmemory` here) becomes the tool prefix
`mcp__agentsmemory__<tool>`. Through the claude.ai connector the prefix instead
looks like `mcp__claude_ai_<label>__<tool>` — the bare tool names are identical.

## 2. Install the command + hook

```bash
./install.sh                 # installs into ~/.claude
# or: CLAUDE_DIR=/custom/.claude ./install.sh
```

This copies:

- `commands/am.md` → `~/.claude/commands/am.md` — the **`/am`** wake-up command
  (recommended). It is thin on purpose: it calls **`am_skillset`** and follows the
  platform-curated playbook it returns, so it stays correct as the toolset grows —
  you never re-install to get new guidance.
- `commands/agentsmemory.md` → `~/.claude/commands/agentsmemory.md` — the verbose
  `/agentsmemory` command: the full wake-up steps and tool table written inline,
  handy as an offline reference but hand-maintained.
- `hooks/agentsmemory-stop-hook.sh` → `~/.claude/hooks/…` and registers it as a
  **Stop** hook in `~/.claude/settings.json` (a timestamped backup is made; the
  entry is not duplicated on re-run).

Restart Claude Code (or `/reload`) to pick them up.

## 3. Use it

Run **`/am <your task>`** at the start of a session. Claude will:

1. **Call `am_skillset` first** — the global wakeup playbook (how to use the
   `am_*` tools, in what order, which skills to load) plus the live tool catalogue.
   The platform curates this centrally, so the guidance is always current.
2. **Follow it** — wake (`am_status`), recall (`am_search`), load named skills
   (`am_load_skill`), and work memory-first as the playbook directs.
3. **Persist before stopping** — `am_diary_write` (AAAK summary), `am_kg_add` (new
   facts as triples), `am_add_drawer` (notable decisions/code verbatim).

With no task, it gives a short briefing of what the memory already knows.

> Prefer `/am`. The verbose `/agentsmemory` predates `am_skillset` and lists the
> tools inline, so it can drift as the server changes; `/am` reads the live list
> from the server every time.

## The Stop hook

On each turn end it reminds Claude to persist the session (diary + KG triples +
drawers). Control it with the `AGENTSMEMORY_STOP_HOOK` environment variable:

| Value | Behavior |
|-------|----------|
| `on` (default) | Remind on every Stop, like mempalace. |
| `once` | Remind only on the first Stop of a session, then stay quiet. |
| `off` | Disabled. |

The reminder is advisory: persist, or say there was nothing worth remembering.

## Uninstall

Delete `~/.claude/commands/agentsmemory.md` and
`~/.claude/hooks/agentsmemory-stop-hook.sh`, and remove the agentsmemory entry
from the `Stop` array in `~/.claude/settings.json`.
