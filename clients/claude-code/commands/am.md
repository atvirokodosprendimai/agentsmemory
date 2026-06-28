---
description: agentsmemory wake-up — call am_skillset, then follow the platform playbook it returns
argument-hint: [your task]
---

You have the **agentsmemory** MCP connected (tools prefixed `am_`) — your
team-shared, cross-session memory. Before acting, let the server tell you how to
use itself. This command is deliberately thin: all the real, current guidance
lives in `am_skillset`, which the platform keeps up to date — so this file rarely
changes even as the toolset grows.

## Task

$ARGUMENTS

> If the task is empty, stop after Step 1 and give a short briefing of what the
> memory already knows that is relevant — no plan, no code. Even then, finish with
> Step 3: a briefing-only run still ends with the diary write.

## Step 1 — Call `am_skillset` FIRST (before any other tool)

Call **`am_skillset`**. It returns, straight from the platform:

- **`preamble`** — the wake-up playbook: which `am_*` tools to call, in what
  order, and which centralised skills to load. Treat it as your instructions for
  this server.
- **`tools`** — the live catalogue of every `am_*` method (name + description).
  Treat it as the authoritative list of what you can call (it is generated from
  the running server, so it is never out of date).

The playbook is curated centrally and kept current, so it — not this file — is the
source of truth for *how* to use the memory.

## Step 2 — Do exactly what it says

Follow the preamble's order and act on its instructions literally. When it names a
skill to load, **call the tool** — e.g. "load `effective-go`" becomes
`am_load_skill(name="effective-go")`, then apply the returned body. The same goes
for `am_status`, `am_search "<your task>"`, and the rest of the loop it lays out.

## Step 3 — Persist before you stop (mandatory, every task)

The playbook also says how to write back what you learned — typically
`am_diary_write` (an AAAK session summary), `am_kg_add` (durable
subject→predicate→object facts), and `am_create_tunnel` (links across topics).

This is unconditional: do it on **every** task, not only the ones that changed
code. A read-only briefing, a plan you only printed, a question you answered — each
still ends with `am_diary_write`. Do not end your turn without it: a session that
recalls but never records leaves the next one cold.
