---
description: agentsmemory — wake the team memory, load shared skills, work memory-first, and persist before you stop
argument-hint: [your task]
---

You have the **agentsmemory** MCP connected — a team-shared, multi-tenant memory
served over MCP: wings/rooms/**drawers** (verbatim memories), a temporal
**knowledge graph** (subject → predicate → object facts), an append-only agent
**diary**, a **graph** of hallways/tunnels, and centralised **skills** (shared
`SKILL.md` bodies your team pulls by name). This command grounds you in it
**before** you act, and reminds you to write back **after**.

Treat the memory as the source of truth for *who/what/why* across sessions:
**ask it before you guess, and tell it what you learned.**

## Task

$ARGUMENTS

> If the task is empty, stop after Step 1 and give a short briefing of what the
> memory already knows that is relevant — no plan, no code.

## Step 1 — Wake + load (do this FIRST, before any other tool)

Run these up front so you work from recalled memory, not a cold start:

1. **`status`** — wake the palace: overview (wings/rooms/drawer counts), the
   **AAAK** dialect, and the storage backend. This is the on-wake-up call; it
   grounds you in the memory's shape before anything task-specific.
2. **`get_aaak_spec`** + **`get_taxonomy`** — learn the compressed write dialect
   and the wing/room taxonomy, so anything you file later lands in the right
   place and reads back cleanly.
3. **`list_skills`** → **`load_skill`** — see what shared skills the team
   publishes, then pull the bodies relevant to the task. Use a team skill instead
   of re-deriving one locally; it is versioned and shared.
4. **`search`** with the task keywords — recall prior decisions, learnings, and
   rationale **before** you act. This is not optional and not replaceable by
   grep or your own memory: the palace is the only source of cross-session *why*.

## Step 2 — Work memory-first

The task will pull you toward unfamiliar code or facts. **Ask the memory before
you grep or guess.**

- Before a broad search / file sweep over unfamiliar code, **`search`** the
  palace first; if it already explains that part, use the recalled memory.
- Use **`list_wings`** / **`list_rooms`** / **`get_taxonomy`** to scope a search
  to the right wing/room; **`get_drawer`** to read a specific memory verbatim.
- For relationships and history, query the **knowledge graph**: **`kg_query`**
  (current/as-of facts about an entity), **`kg_timeline`** (how a fact changed).
- For connections across topics, walk the graph: **`traverse`**,
  **`follow_tunnels`**, **`find_tunnels`**, **`list_hallways`**.
- Read your own prior journal with **`diary_read`** to thread context across
  sessions.
- Before writing a new memory, **`check_duplicate`** so you reinforce rather than
  duplicate.

## Step 3 — Persist before you stop (close the loop)

A learning that isn't written back is lost. Before the turn ends, write what you
learned **in AAAK** (compressed, entity-coded — see `get_aaak_spec`):

- **`diary_write`** — a session summary: what you built/decided/learned and any
  open thread. Use a stable agent name so the journal threads across sessions.
- **`kg_add`** — new durable facts as subject → predicate → object triples (with
  validity windows). Correct a changed fact with **`kg_invalidate`** then
  **`kg_add`** the new one — never silently overwrite.
- **`add_drawer`** — notable decisions, rationale, or code **verbatim** into the
  right wing + room (`update_drawer` / `delete_drawer` to amend).
- **Connect it**: weave a **`create_tunnel`** when this work links to another
  topic/wing (check **`find_tunnels`** first so you reinforce, not duplicate).
- **Skills**: if you produced a reusable procedure, publish it with
  **`update_skill`** (writer/admin) so the whole team gets it next time.

The included **Stop hook** will remind you of this at turn end; do it proactively
so you are never blocked.

## Tool reference

Tools are exposed by your MCP server name as `mcp__<server>__<tool>` — e.g.
`mcp__agentsmemory__status`, or `mcp__claude_ai_agensmemory__status` when
connected through the claude.ai connector. Call them by the bare tool name below.

| Group | Tools |
|-------|-------|
| **Wake / taxonomy** | `status`, `get_aaak_spec`, `get_taxonomy`, `list_wings`, `list_rooms`, `reconnect` |
| **Drawers (memories)** | `add_drawer`, `get_drawer`, `update_drawer`, `delete_drawer`, `list_drawers`, `check_duplicate`, `search` |
| **Diary** | `diary_write`, `diary_read` |
| **Knowledge graph** | `kg_add`, `kg_query`, `kg_invalidate`, `kg_timeline`, `kg_stats` |
| **Graph (links)** | `traverse`, `create_tunnel`, `find_tunnels`, `follow_tunnels`, `list_tunnels`, `delete_tunnel`, `list_hallways`, `delete_hallway`, `graph_stats`, `recompute_graph` |
| **Mining** | `mine` (chunk longer text into drawers, idempotent by source) |
| **Skills** | `list_skills`, `load_skill`, `update_skill` |
| **Admin** | `merge_wing`, `memories_filed_away` |

Everything is scoped to your team automatically by the bearer token on the MCP
connection — you never pass a team id.
