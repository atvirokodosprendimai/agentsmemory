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

1. **`am_status`** — wake the palace: overview (wings/rooms/drawer counts), the
   **AAAK** dialect, and the storage backend. This is the on-wake-up call; it
   grounds you in the memory's shape before anything task-specific.
2. **`am_get_aaak_spec`** + **`am_get_taxonomy`** — learn the compressed write dialect
   and the wing/room taxonomy, so anything you file later lands in the right
   place and reads back cleanly.
3. **`am_list_skills`** → **`am_load_skill`** — see what shared skills the team
   publishes, then pull the bodies relevant to the task. Use a team skill instead
   of re-deriving one locally; it is versioned and shared.
4. **`am_search`** with the task keywords — recall prior decisions, learnings, and
   rationale **before** you act. This is not optional and not replaceable by
   grep or your own memory: the palace is the only source of cross-session *why*.

## Step 2 — Work memory-first

The task will pull you toward unfamiliar code or facts. **Ask the memory before
you grep or guess.**

- Before a broad search / file sweep over unfamiliar code, **`am_search`** the
  palace first; if it already explains that part, use the recalled memory.
- Use **`am_list_wings`** / **`am_list_rooms`** / **`am_get_taxonomy`** to scope a search
  to the right wing/room; **`am_get_drawer`** to read a specific memory verbatim.
- For relationships and history, query the **knowledge graph**: **`am_kg_query`**
  (current/as-of facts about an entity), **`am_kg_timeline`** (how a fact changed).
- For connections across topics, walk the graph: **`am_traverse`**,
  **`am_follow_tunnels`**, **`am_find_tunnels`**, **`am_list_hallways`**.
- Read your own prior journal with **`am_diary_read`** to thread context across
  sessions.
- Before writing a new memory, **`am_check_duplicate`** so you reinforce rather than
  duplicate.

## Step 3 — Persist before you stop (close the loop)

A learning that isn't written back is lost. Before the turn ends, write what you
learned **in AAAK** (compressed, entity-coded — see `am_get_aaak_spec`):

- **`am_diary_write`** — a session summary: what you built/decided/learned and any
  open thread. Use a stable agent name so the journal threads across sessions.
- **`am_kg_add`** — new durable facts as subject → predicate → object triples (with
  validity windows). Correct a changed fact with **`am_kg_invalidate`** then
  **`am_kg_add`** the new one — never silently overwrite.
- **`am_add_drawer`** — notable decisions, rationale, or code **verbatim** into the
  right wing + room (`am_update_drawer` / `am_delete_drawer` to amend).
- **Connect it**: weave a **`am_create_tunnel`** when this work links to another
  topic/wing (check **`am_find_tunnels`** first so you reinforce, not duplicate).
- **Skills**: if you produced a reusable procedure, publish it with
  **`am_update_skill`** (writer/admin) so the whole team gets it next time.

The included **Stop hook** will remind you of this at turn end; do it proactively
so you are never blocked.

## Tool reference

Every tool carries the `am_` prefix so it never collides with another memory MCP
(mempalace exposes same-named tools like `search` / `add_drawer` / `list_wings`),
which means both can be connected at once. The full name your client sees is
`mcp__<server>__am_<tool>` — e.g. `mcp__agentsmemory__am_status`, or
`mcp__claude_ai_agensmemory__am_status` through the claude.ai connector. Call them
by the `am_`-prefixed names below.

| Group | Tools |
|-------|-------|
| **Wake / taxonomy** | `am_status`, `am_get_aaak_spec`, `am_get_taxonomy`, `am_list_wings`, `am_list_rooms`, `am_reconnect` |
| **Drawers (memories)** | `am_add_drawer`, `am_get_drawer`, `am_update_drawer`, `am_delete_drawer`, `am_list_drawers`, `am_check_duplicate`, `am_search` |
| **Diary** | `am_diary_write`, `am_diary_read` |
| **Knowledge graph** | `am_kg_add`, `am_kg_query`, `am_kg_invalidate`, `am_kg_timeline`, `am_kg_stats` |
| **Graph (links)** | `am_traverse`, `am_create_tunnel`, `am_find_tunnels`, `am_follow_tunnels`, `am_list_tunnels`, `am_delete_tunnel`, `am_list_hallways`, `am_delete_hallway`, `am_graph_stats`, `am_recompute_graph` |
| **Mining** | `am_mine` (chunk longer text into drawers, idempotent by source) |
| **Skills** | `am_list_skills`, `am_load_skill`, `am_update_skill` |
| **Admin** | `am_merge_wing`, `am_memories_filed_away` |

Everything is scoped to your team automatically by the bearer token on the MCP
connection — you never pass a team id.
