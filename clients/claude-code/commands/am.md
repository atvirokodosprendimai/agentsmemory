---
description: agentsmemory session bootstrap — load specs (eidos:spec), code reality (codebase-memory), and team memory (am_* MCP), then plan, work, and persist what you learned
argument-hint: [your question or task]
---

You are (re)starting a session. Ground yourself before acting: load the **specs**
(what the system should be), the **code reality** (what it is), and the **team
memory** (who did what, and why). Then plan, work, and — before you stop — write
back what you learned so the next session starts ahead of where this one did.

This command is **generic**: it wires up three sources — the **agentsmemory MCP**
(`am_*` tools), the **codebase-memory** code graph, and the **eidos:spec** skill —
and assumes no particular language, framework, or UI stack.

## Task

$ARGUMENTS

## Step 1 — Load context (specs, code, memory)

Fire these in parallel where you can; each answers a different question.

- **1a. Specs — `eidos:spec`.** Invoke the `eidos:spec` skill to load the
  project's source-of-truth specs (`eidos/*.md`): what the system is *supposed* to
  do. If the project has no specs, say so and move on.

- **1b. Code reality — codebase-memory.** Reindex first, then search — never
  search a stale graph:
  1. `index_repository(repo_path=<cwd>)` — refresh the code graph (it re-indexes
     incrementally; `index_status` / `detect_changes` show what moved).
  2. `mcp__codebase-memory-mcp__search_code(pattern=<task>, project=<repo>)` —
     locate the symbols, files, and routes the task touches. Reach for
     `get_architecture` or `trace_path` when you need structure or call paths.

- **1c. Team memory — `am_*` MCP.** Two calls, in order:
  1. `am_skillset` — the wake-up playbook: how to drive the `am_*` tools, in what
     order, and which skills to load. The platform curates this centrally, so the
     guidance stays current as the toolset grows — you never re-install to get it.
  2. `am_search(<task>)` — recall past decisions, learnings, and rationale for
     this work. This is the **only** source of cross-session *why*; don't
     reconstruct from code what memory already explains.

Reconcile the three. If the spec (1a), the code (1b), and past decisions (1c)
disagree, **surface the conflict** — that's a human decision, not one to make
silently.

## Memory-first — ask before you grep

When the task pulls you into unfamiliar code, **ask memory first**: `am_search`
for the symbol, subsystem, or behaviour; if the palace already explains it, use
that instead of reverse-engineering it. Grep only the gap.

The same reflex applies to **tools**: if you're unsure how to drive one (an `am_*`
tool, a codebase-memory call, a skill, a CLI flag), `am_search` for its usage
before guessing. Whatever you had to work out the hard way, write back (Step 4) so
the next session recalls it.

## Step 2 — Plan

Invoke **`eidos:plan`** to turn the loaded context into a structured, multi-step
plan grounded in the specs (1a) and the code graph (1b). Cite concrete
`file:line`. Surface unresolved conflicts as decision points, not silent choices.

## Step 2b — Todo list

Materialize the plan into a tracked todo list **before** you start changing code —
one concrete, verifiable action per item. Drive the work off it: exactly one item
in progress at a time, marked done the moment its check passes (test, build, lint,
runtime output). Add new work you discover; never do it off-book.

## Step 3 — Implement

Work the list. Make surgical changes, verify as you go, and keep the list in sync
with reality. Comment the **why** on non-obvious code, favour reuse over
repetition, and commit after each verified step — one logical change per commit,
pushed often. For changes that touch untrusted input, auth, or other high-stakes
surfaces, get an independent review before committing.

## Step 4 — Persist before you stop

Write back what this session produced so the next one recalls it:

- **`am_diary_write`** — an AAAK session summary: what you built, decided, or
  learned, plus any open thread. Use a stable `agent_name` so the diary threads
  across sessions.
- **`am_kg_add`** — new durable facts as subject → predicate → object triples.
- **`am_add_drawer`** — notable decisions or code, verbatim, into the right wing
  and room.

A verified change that isn't written back is memory lost. Skip only when the
session produced nothing worth recalling — and say so.

If `$ARGUMENTS` is empty, stop after Step 1 and give a short **briefing** instead:
what the specs cover, the current code shape, and the most relevant recalled
memories — no plan, no code.
