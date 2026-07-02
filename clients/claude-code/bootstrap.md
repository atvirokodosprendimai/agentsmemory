# agentsmemory — operating protocol (auto-loaded)

This file is imported into your memory automatically every session (via the
`@agentsmemory-bootstrap.md` line in `CLAUDE.md`), so this protocol applies
**without you typing anything**. The `/am` and `/M` slash commands run the same
grounding sequence scoped to a specific task (`/am <task>`); this file is the
always-on baseline.

Bias toward correctness, small diffs, and verified changes.

## Behavioral Rules

### Think Before Coding

- State assumptions. If requirements are ambiguous, ask before editing.
- If multiple interpretations exist, present them instead of choosing silently.
- Push back on overcomplicated or speculative work.
- For non-trivial changes, define success criteria and a short plan before implementation.

### Simplicity First

- Implement only what was requested.
- Do not add abstractions for single-use code.
- Do not add configurability, fallback paths, or defensive handling for impossible states.
- If a change grows large, stop and simplify before continuing.

### Surgical Changes

- Touch only files needed for the request.
- Do not refactor, reformat, or clean adjacent code unless required.
- Match existing style, even when a different style would be preferable.
- Remove only unused imports, variables, or functions created by your own change.
- Mention unrelated dead code or issues; do not fix them unless asked.

### Verified Execution

- Convert tasks into verifiable goals: reproduce bugs, add focused tests when useful, then make checks pass.
- For multi-step work, use: `step -> verify: check`.
- Do not claim completion without evidence from tests, lint, type-check, build, runtime output, or source tracing.

---

You are (re)starting a session. **First** load context from the sources below,
**then** plan, **then** code. Do not skip the bootstrap even if the task looks
trivial — the whole point is to ground the work in idiomatic code, polished
UX/UI, spec intent, code reality, and prior decisions. **Quality and UX/UI are
first-class citizens here, gated like correctness — never bolted on at the end.**

## Step 0 — Language idioms FIRST (hard gate, do not skip)

Before touching code, load the idioms for the language and stack this project
actually uses — whatever they are. If your setup provides an idiom skill for that
language (a linting/best-practices skill), your **very first action** is invoking
it via the Skill tool — an actual Skill call, before any other tool, before any
prose.

This is a gate, not a suggestion:

- The idiom Skill call is a **message on its own**. Do **not** batch it with the
  Step 1 memory calls, and do not read files or write analysis until it has
  loaded. Batching is how the gate gets skipped — keep it alone and first.
- After it loads, emit a literal audit line naming the skill (e.g.
  `<skill> loaded ✓`) so the load is visible.
- **Re-check before Step 3.** Before writing any code, confirm the idiom line
  appears earlier in this turn. If no such skill exists for your stack, say so in
  one line and follow the language's published conventions regardless.

## Step 0b — UX/UI idioms (hard gate when any UI is touched)

Quality and UX/UI are **first-class, not polish at the end**. The moment the task
touches a user-facing surface — an HTML template, a component, a page, CSS, a
client-side interaction, anything a human sees or clicks — load these idioms
**before** you write or change a line of markup.

This is a **hard gate, conditionally armed**:

- **Default armed.** Assume the change reaches UI unless it is *provably*
  backend-only (pure logic, no rendered output, no template, no handler that
  writes HTML). If backend-only, skip this step and say so in one line; never skip
  silently.
- When armed, load whatever design/UX skills your setup provides for your
  framework before writing any markup, and emit an audit line for each. If none
  exist, follow the platform's own UI conventions.
- **Re-check before Step 3.** Confirm the lines (or the explicit backend-only
  skip) appear earlier in this turn before writing any UI code.

UX/UI is held to the same bar as correctness: a feature that works but looks
templated, breaks on mobile, drops focus states, or fights your framework's idioms
is **not done**.

## Step 1 — Load memory (specs, code, why) — hard gate, do not skip

All three sources are **MUST**, not "run if convenient." Fire the independent
calls in parallel where you can; each answers a different question.

- **1a. Specs (intent) — `eidos:spec`.** Invoke the `eidos:spec` skill to load the
  project's source-of-truth specs (`eidos/*.md`): what the system is *supposed* to
  do. If the project has no specs, say so and move on. Emit `specs loaded ✓`.
- **1b. Code graph (reality) — codebase-memory.** Reindex first, then search —
  never search a stale graph:
  1. `index_repository(repo_path=<cwd>)` — refresh the code graph (it re-indexes
     incrementally; `index_status` / `detect_changes` show what moved).
  2. `search_code(pattern=<task>, project=<repo>)` — locate the symbols, files,
     and routes the task touches. Reach for `get_architecture` or `trace_path`
     when you need structure or call paths.

  Both calls are mandatory. Emit `code graph indexed + searched ✓`.
- **1c. Team memory (who + why) — `am_*` MCP.** Two calls, in order:
  - **Wake up first** — call `am_status` to load the palace overview + AAAK spec.
    It grounds you in identity and palace shape before task-specific recall. Emit
    `palace woken ✓`.
  - **Then search** — call `am_search(<task>)` to recall past decisions,
    learnings, and rationale. This is the **only** source of cross-session *why*;
    don't reconstruct from code what memory already explains. Emit
    `palace searched ✓`.

Reconcile the three. If the spec (1a), the code (1b), and past decisions (1c)
disagree, **surface the conflict** — that's a human decision, not one to make
silently.

## Memory-first exploration (don't re-derive what's remembered)

Before any broad `grep` / `search_code` / file sweep over unfamiliar code:

1. **Query `am_search` first** with the symbol, subsystem, or behaviour. If the
   palace already explains it, use the recalled memory instead of reconstructing
   it.
2. **Grep only the gap.** If the palace is silent or stale, sweep the code — but
   scope the grep to what memory didn't answer.
3. **Write back what you re-derived** (Step 4) so the next session recalls it.

## Memory-first tool use (recall how a tool works before you fumble it)

`am_search` is a mid-session reflex, not a startup-only call. The moment you reach
for a tool whose exact shape you're unsure of — an `am_*` tool, a codebase-memory
call, a skill, a CLI flag — `am_search` for its usage first (e.g.
*"am_create_tunnel params"*). If memory explains how to call it, use that instead
of guessing. Many tools load **deferred** (name only): recall that one
`ToolSearch: "select:<tool_name>"` call loads the schema before the tool is
callable. Write back any usage you worked out the hard way (Step 4).

## Step 2 — Plan

Invoke **`eidos:plan`** to turn the loaded context into a structured, multi-step
plan grounded in the specs (1a) and the code graph (1b). Cite concrete
`file:line`. Surface unresolved conflicts as decision points, not silent choices.
For user-facing work, carry explicit UX/UI steps (interaction, loading/empty/error
states, responsiveness, accessibility) as first-class items.

## Step 2b — Todo list (hard gate, ALWAYS, do not skip)

Materialize the plan into a live todo list with the **`TodoWrite`** tool **before**
you start changing code — one concrete, verifiable action per item. Emit
`todo list written ✓` after the first write. Drive the work off it: exactly one
item `in_progress` at a time, marked `completed` the moment its check passes (test,
build, lint, runtime output). Add new work you discover; never do it off-book.

## Step 3 — Implement

Work the list. Make surgical changes, verify as you go, and keep the list, plan,
and code in sync. When work reaches into unfamiliar code, apply **Memory-first
exploration** — query the palace before you grep.

- **UX/UI quality bar** — every user-facing change ships at production quality:
  accessible by default (semantic HTML, real labels, keyboard-reachable, visible
  focus, sufficient contrast), responsive (mobile → desktop, touch targets ≥
  44px), every interaction state designed (hover/focus/active/disabled/loading/
  empty/error), idiomatic for your framework, and distinctive rather than
  templated. Verify non-trivial UI in the browser — look at the pixels.
- **Comment the why** — doc comments on every exported identifier (start with the
  name), a package comment per package, and short *why* comments on non-obvious
  blocks (concurrency, error-handling choices, business rules, workarounds). Skip
  comments that parrot the code; keep comments in sync when you edit.
- **Favour reuse over repetition** — refactor duplicated logic into one shared
  unit; extract a small interface at the consumer when reuse spans call sites
  (accept interfaces, return structs). Flag the opportunity in the plan; don't
  silently widen scope.
- **Independent review on risky changes** — when a change touches untrusted input,
  auth, parsers, deserialization, concurrency, public APIs, or data migrations,
  get an independent review (a read-only review agent, if your setup has one)
  before committing. Fold confirmed findings back in and journal the notable ones.
- **Commit often, push often** — one logical change per commit, message says
  *why*, push at every natural stopping point. Never bundle unrelated changes.

## Step 4 — Persist before you stop

Write back what this session produced so the next one starts ahead:

- **`am_diary_write`** — an AAAK session summary (compressed, entity-coded,
  emotion-marked): what you built, decided, or learned, plus any open thread. Use a
  stable `agent_name` so the diary threads across sessions.
- **`am_kg_add`** — new durable facts as subject → predicate → object triples.
- **`am_add_drawer`** — notable decisions or code, verbatim, in the right wing and
  room.
- **`am_create_tunnel`** — when this work connects to another project/domain, weave
  a cross-wing tunnel (check `am_find_tunnels` / `am_follow_tunnels` first so you
  reinforce, not duplicate).

A verified change that isn't written back is memory lost. Skip only when the
session produced nothing worth recalling — and say so.
