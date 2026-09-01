---
description: agentsmemory session bootstrap — load project intent, code reality (codebase-memory), and team memory (am_* MCP), then plan, work, and persist what you learned
argument-hint: [your question or task]
---

You are (re)starting a session. Ground yourself before acting: load the **project
intent** (what the system should be), the **code reality** (what it is), and the **team
memory** (who did what, and why). Then plan, work, and — before you stop — write
back what you learned so the next session starts ahead of where this one did.

This command is **generic**: it combines the repository's own intent sources,
the **agentsmemory MCP** (`am_*` tools), and code-reality discovery using
codebase-memory when available. It assumes no particular documentation shape,
language, framework, or UI stack.

## Task

$ARGUMENTS

## Step 1 — Load context (intent, code, memory)

Fire these in parallel where you can; each answers a different question.

- **1a. Project intent — use the repository's own sources.** Discover and read
  what this project actually treats as authoritative: repository instructions,
  `docs/specs/`, ADRs, architecture docs, OpenAPI or schema contracts,
  product/business rules, or task acceptance criteria. Load only what bears on
  the task, name the exact sources, and do not assume a directory shape or a
  third-party skill. If none exists, say `no explicit intent source found` and
  carry that uncertainty into the plan.

- **1b. Code reality — codebase-memory. Three calls, in order:**
  1. ⚠ **`index_repository(repo_path=<cwd>)` FIRST, every task, no exceptions.**
     The graph does **not** index automatically. It holds whatever was true the
     last time somebody indexed — which may be another branch, another day, or
     never — so at the start of any task the index is **stale by construction**.
     This is the one call that is easy to skip and expensive to skip, because a
     stale graph does not error: **it answers**, confidently, about code that has
     moved. That is worse than having no graph at all, and it is why this is a
     numbered step rather than a sentence.
  2. `search_code(pattern=<task>, project=<repo>)` — locate the symbols, files
     and routes the task touches. Reach for `get_architecture` or `trace_path`
     when structure or call paths matter.
  3. Emit `code reality: indexed + searched ✓` so the order is visible.

  **If it is not reachable, SAY SO IN ONE LINE and carry on** — e.g.
  `⚠ codebase-memory unreachable (CONNECTION_CLOSED) — targeted source search only`.
  Then use targeted search over the paths, symbols, docs and tests the task
  names, and name what you inspected. Do not block on an optional integration.

  ⚠ **The announcement is not politeness, it is the finding.** A dead server and
  a skipped step produce identical output — silence — so an unannounced absence
  gets diagnosed for weeks as an agent that keeps forgetting. Measured 2026-08-30:
  three sessions across two repositories were all read as "forgot Step 1b" while
  `claude mcp list` reported `codebase-memory-mcp ✘ CONNECTION_CLOSED` on the
  machine running them. Check it before you conclude anything about your own
  discipline: `claude mcp list | grep codebase`.

- **1c. Team memory — `am_*` MCP.** Four calls, in order:
  1. `am_status` — which palace answered, your wing, what is waiting, and the
     **`entry_protocol`** block. Read that block first if it is present.
  2. ⚠ **`am_load_skill("start-here")` — do this NOW if `am_status` named an entry
     protocol.** It is **one call** with a known target, and it outranks this
     command on anything specific to that team. **Do not defer it because your
     task looks read-only** — it is what tells you which reads are cheap and which
     answers are already written down.
     ⚠ Measured 2026-08-30: this instruction used to live in call 4 below, as
     "load `start-here` if it exists". Three sessions in three repositories read
     it and **none** of them loaded the skill — because it was conditional on a
     catalogue call they pruned as preparation for work they were told not to do.
     It is call 2 now for that reason. If `am_status` named no entry protocol,
     skip it and say so.
  3. `am_search(<task>)` — recall past decisions, learnings, and rationale for
     this work. This is the **only** source of cross-session *why*; don't
     reconstruct from code what memory already explains.
  4. `am_list_skills` → `am_load_skill(<name>)` — load the team's other
     **centralised** skills (`effective-go`, and whatever else bears on the task).
     These are authored once and shared by every agent, so they outrank
     conventions you would otherwise infer. Check here before concluding a skill
     doesn't exist: a skill missing from your local list is usually centralised
     here instead.

Reconcile the three. If project intent (1a), the code (1b), and past decisions (1c)
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

Build a structured, multi-step plan directly from the loaded context using the
harness's native plan/todo tool. Ground it in project intent (1a) and code
reality (1b). Cite concrete `file:line`. Surface unresolved conflicts as decision
points, not silent choices.

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
- **`am_kg_add`** — **MUST, not optional.** New durable facts as subject →
  predicate → object triples. A drawer with no edge is an orphan: reachable by
  search, invisible to traversal, and it still surfaces in your OWN search, which
  is why authors believe it is reachable. If this session established no durable
  fact, say that in one line rather than skipping silently.
- **`am_add_drawer`** — notable decisions or code, verbatim, into the right wing
  and room.
- ⚠ **If this wing has no entry point, give it one.** `am_status` reports it: no
  `entry_protocol` block, or `am_entry_point(wing)` answering `unknown_term`,
  means a session waking here has no front door and must fall back to guessing
  which rooms to read. Fix it by filing ONE drawer into room `llm_init` whose
  content OPENS with the words `WHAT MUST I LOAD AT THE START OF A SESSION?` —
  the server mints the wing's by-name root from that write, so the address
  resolves for every session afterwards.
  ⚠ **Keep it under 1600 runes**: the entry tier is served one chunk at a time, so
  a longer record arrives cut with nothing marking it partial. And put in it only
  what a session cannot notice it needed until after it has broken something —
  that judgement is the whole of the tier and no tool can make it for you.

A verified change that isn't written back is memory lost. Skip only when the
session produced nothing worth recalling — and say so.

If `$ARGUMENTS` is empty, stop after Step 1 and give a short **briefing** instead:
what the intent sources establish, the current code shape, and the most relevant recalled
memories — no plan, no code.
