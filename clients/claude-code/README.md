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

- `commands/agentsmemory.md` → `~/.claude/commands/agentsmemory.md` — the
  `/agentsmemory` slash command.
- `hooks/agentsmemory-stop-hook.sh` → `~/.claude/hooks/…` and registers it as a
  **Stop** hook in `~/.claude/settings.json` (a timestamped backup is made; the
  entry is not duplicated on re-run).

Restart Claude Code (or `/reload`) to pick them up.

## 3. Use it

Run **`/agentsmemory <your task>`** at the start of a session. Claude will:

1. **Wake + load** — `status`, `get_aaak_spec` / `get_taxonomy`, then
   `list_skills` → `load_skill`, then `search` the memory for prior decisions.
2. **Work memory-first** — query the palace / knowledge graph before grepping or
   guessing.
3. **Persist before stopping** — `diary_write` (AAAK summary), `kg_add` (new
   facts as triples), `add_drawer` (notable decisions/code verbatim).

With no task, it gives a short briefing of what the memory already knows.

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
