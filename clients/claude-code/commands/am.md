---
description: Session-start bootstrap — load Go + UX/UI idioms + specs + code graph + memory palace, then plan, then write a tracked todo list and implement it with quality and UX/UI as first-class gates
argument-hint: [your question or task]
---

You are (re)starting a session. **First** load context from the sources below,
**then** plan, **then** code. Do not skip the bootstrap even if the task looks
trivial — the whole point is to ground the work in idiomatic Go, polished UX/UI,
spec intent, code reality, and prior decisions. **Quality and UX/UI are
first-class citizens here, gated like correctness — never bolted on at the end.**

## Task

$ARGUMENTS

## Step 0 — Go idioms FIRST (hard gate, do not skip)

Before anything else, your **very first action** must be invoking the
**`effective-go`** skill via the Skill tool. Not a mention, not a plan to do it
later — an actual Skill tool call, before any other tool, before any prose.

This is a gate, not a suggestion:

- The `effective-go` Skill call is a **message on its own**. Do **not** batch it
  with the Step 1 parallel MCP calls, do **not** read files, search the code
  graph, call MCP, or write a single word of analysis until it has loaded.
  Batching is how the gate gets skipped — keep it alone and first.
- After it loads, your next sentence must be the literal line
  `effective-go loaded ✓` so the load is visible and auditable. This audit line
  is **exempt from caveman compression** — emit it verbatim, exactly those three
  tokens, never abbreviated.
- **Re-check before Step 3.** Before writing any code, confirm `effective-go
  loaded ✓` actually appears earlier in this turn. If it does not, you skipped
  the gate — **stop, load it now**, emit the line, then continue.

Every line of Go you later read or write must stay idiomatic, so these
conventions load before you touch spec, code, or keyboard.

## Step 0b — UX/UI + hypermedia idioms (hard gate when any UI is touched)

Quality and UX/UI are **first-class, not polish at the end**. The moment the task
touches a user-facing surface — an HTML template, a component, a page, CSS, a
`data-*` attribute, an SSE/hypermedia fragment, anything a human sees or clicks —
these idioms load **before** you write or change a line of markup, exactly like
Step 0 does for Go.

This is a **hard gate, conditionally armed**:

- **Default armed.** This is a datastar hypermedia stack — assume the change
  reaches UI unless it is *provably* backend-only (pure logic, no rendered output,
  no template, no handler that writes HTML). If backend-only, skip this step and
  say so in one line; never skip silently.
- When armed, invoke all three skills before any markup (they may batch together,
  but must precede any template/CSS edit):
  - **`uxui`** — design intelligence: layout, color, typography, spacing,
    interaction states, accessibility, charts. Emit `uxui loaded ✓`.
  - **`datastar`** — idiomatic datastar / hypermedia conventions from the
    reference. Emit `datastar loaded ✓`.
  - **`frontend-design`** — distinctive, production-grade visual design that
    avoids generic AI aesthetics. Emit `frontend-design loaded ✓`.
- These audit lines (`uxui loaded ✓`, `datastar loaded ✓`,
  `frontend-design loaded ✓`) are **exempt from caveman compression** — emit them
  verbatim, never abbreviated.
- **Re-check before Step 3.** Before writing any UI code, confirm the three lines
  (or the explicit one-line backend-only skip) appear earlier in this turn. A
  missing line means you skipped the gate — **stop, load it now**, emit the line,
  then continue.

UX/UI is held to the same bar as Go correctness: a feature that works but looks
templated, breaks on mobile, drops focus states, or fights datastar idioms is
**not done**.

## Step 1 — Load memory (specs, code, why) — hard gate, do not skip

All three sources are **MUST**, not "run if convenient." Like Step 0, this is a
gate: 1b (code index + search) and 1c (palace wake-up + search) are the ones that
get silently skipped, so they are enforced with audit lines and a re-check —
**no plan, no code, no file reads beyond this step until every call has run and
its audit line appears in this turn.**

Steps 1b and 1c are independent MCP calls — fire them in the **same message, in
parallel** (1c is two calls: `am_status` to wake up, then
`am_search`). Step 1a is a Skill invocation.

- **1a. Specs (intent)** — invoke the `eidos:spec` skill to load the project's
  source-of-truth specs (`eidos/*.md`). These describe what the system *should*
  be. After it loads, emit the literal audit line `specs loaded ✓`.
- **1b. Code graph (reality) — MUST, do not skip.** **Reindex before any
  action.** First call `index_repository(repo_path=<cwd>)` to index/reindex this
  repo into the knowledge graph so the database is fresh — never search or act
  against a stale graph. (Already-indexed repos reindex incrementally;
  `index_status` / `detect_changes` confirm what moved.) Only **after** the
  reindex, call `mcp__codebase-memory-mcp__search_code` with the task as the
  query to locate the relevant symbols/files/routes in **this** repo. Both the
  `index_repository` call **and** the `search_code` call are mandatory — a
  reindex without a search, or a search without a reindex, does not satisfy the
  gate. After both return, emit the literal audit line `code graph indexed +
  searched ✓`.
- **1c. Memory palace (who + why) — MUST, do not skip.** Two calls, both
  required, in order:
  - **Wake up first** — call
    `am_status` to load the palace
    overview + AAAK spec. This is the MCP server's own **on-wake-up** call: it
    grounds you in identity and palace shape *before* any task-specific recall, so
    later searches read against a known structure. After it returns, emit the
    literal audit line `palace woken ✓`.
  - **Then search** — call
    `am_status` with the task to recall
    past decisions, learnings, and rationale across all projects. This is **not
    optional** and **not replaceable by grep or your own memory** — the palace is
    the only source of cross-project rationale. After it returns, emit the literal
    audit line `palace searched ✓`.

These four audit lines (`specs loaded ✓`, `code graph indexed + searched ✓`,
`palace woken ✓`, `palace searched ✓`) are **exempt from caveman compression** —
emit them verbatim, exactly as written, never abbreviated.

**Re-check before Step 2.** Before invoking `eidos:plan`, confirm all four
audit lines actually appear earlier in this turn. If any is missing, you skipped
that source — **stop, run it now**, emit its line, then continue. A plan built on
a skipped source is invalid.

Reconcile the sources. Call out explicitly any **conflict** between spec intent
(1a), code reality (1b), and past decisions (1c) — those are human decisions,
surface them before planning.

## Memory-first exploration (don't re-derive what's remembered)

The task will pull you toward code you haven't loaded — a subsystem, package, or
file outside the Step 1b/1c hits. **Ask the palace before you grep it.**
Reverse-engineering the same code every session is wasted work; the palace
already holds what that code does and why it's shaped that way.

Before any broad `grep` / `search_code` / file sweep over unfamiliar or
unrelated code:

1. **Query `am_search` first** with what you're about to look for — the
   symbol, the subsystem, the behavior. If the palace already explains that
   code/part, **use the recalled memory**; do not reconstruct it from scratch.
2. **Grep only the gap.** If the palace is silent or stale, sweep the code — but
   scope the grep to what memory didn't answer, not the whole tree.
3. **Write back what you re-derived** (Step 4). When the palace was empty and you
   had to reverse-engineer a part, journal it / file a drawer so the next session
   recalls it instead of re-deriving. The loop only pays off if you close it.

This is a standing rule, not a one-time Step 1 action: the moment Step 3 coding
makes you reach into unfamiliar code, pause and query the palace before the grep.

## Memory-first tool use (recall how a tool works before you fumble it)

`am_search` is **not a startup-only call** — it is a mid-session reflex.
The same way you ask the palace about unfamiliar *code*, ask it about unfamiliar
*tools* the moment you reach for one whose exact shape you're unsure of: which
tool does the job, its required params, the calling pattern, the gotcha that bit
you last time. The palace already holds these tool-usage notes; re-deriving them
by trial-and-error (a failed call, a re-read of the schema, a wrong param) is the
same wasted work as reverse-engineering remembered code.

This is a **standing rule**, armed every time you're about to use a tool you
don't already know cold — an MCP tool, a skill, a CLI invocation, a less-used
flag:

1. **Recall first.** Before the call, `am_search` for the tool / task —
   e.g. *"am_create_tunnel params"*, *"codebase-memory index_repository
   gotcha"*, *"playwright skill dev-server detect"*. If the palace explains how to
   call it, **use the recalled usage** — correct params, correct sequence — instead
   of guessing.
2. **Don't guess deferred-tool schemas.** Many tools here load **deferred** (name
   only, no schema — see the session-start reminder). Recall the activation
   pattern from memory: one `ToolSearch: "select:<tool_name>"` call loads the
   schema, *then* the tool is callable. Recalling this beats a failed direct call.
3. **Read the gap only.** If the palace is silent or stale on that tool, then open
   its schema / `--help` — but scope it to what memory didn't answer.
4. **Write back the usage you learned** (Step 4). When you had to work out a tool's
   correct params, sequence, or gotcha the hard way, journal it / file a drawer so
   the next session recalls *how to drive the tool*, not just what it did. Close
   the loop — that's what makes the next session faster.

The trigger is the moment of doubt: if you're about to call a tool and your hand
hesitates on the params, that hesitation **is** the cue to query the palace first.

## Step 2 — Plan

Invoke the **`eidos:plan`** skill to turn the loaded context into a structured,
multi-step plan grounded in spec intent and code reality. Cite concrete
`file:line` from the code graph in the plan steps. Surface unresolved conflicts
from Step 1 as decision points, not silent choices.

For any user-facing work, the plan **must** carry explicit UX/UI steps as
first-class items — interaction states (loading/empty/error included),
responsiveness, accessibility, and the datastar signal → server-fragment flow —
not as an afterthought tacked on after the logic lands.

## Step 2b — Todo list (hard gate, ALWAYS, do not skip)

The plan from Step 2 is prose; a prose plan is not trackable and gets dropped
mid-task. **Always** materialize it into a live todo list with the **`TodoWrite`**
tool — every time, even for a one-step task, even when the work "looks trivial."
There is no task too small for a todo list here.

This is a gate, not a suggestion:

- **Write the list before any Step 3 code.** Turn each plan step (logic *and* the
  first-class UX/UI items) into a discrete `TodoWrite` entry. One concrete,
  verifiable action per item — not a vague bucket. The list **is** the plan made
  executable.
- After the first `TodoWrite` call lands, emit the literal audit line
  `todo list written ✓`. This audit line is **exempt from caveman compression** —
  emit it verbatim, exactly those three tokens, never abbreviated.
- **Drive the whole of Step 3 off this list.** Exactly one item `in_progress` at a
  time; mark it `completed` the instant its check passes (test, build, lint,
  runtime output) — not in a batch at the end. New work you discover mid-task gets
  **added** to the list, never silently done off-book.
- **Re-check before you stop.** A turn that wrote code but left the todo list
  empty, stale, or with completed work still marked pending means you skipped the
  gate — fix the list before ending. The list is done when every item is
  `completed` (or explicitly removed with a stated reason).

A plan you didn't turn into a todo list is a plan you will half-forget — so the
list is mandatory, and it stays in sync with reality until the task lands.

## Step 3 — Code

Implement the plan **by working through the Step 2b todo list**, staying within
the `effective-go` idioms from Step 0 and the spec intent from Step 1a. Make
surgical changes, verify as you go, and keep the todo list, plan, and code in
sync — one item `in_progress`, mark it `completed` the moment its check passes. When the work reaches into code you haven't loaded, apply
**Memory-first exploration** above — query the palace before you grep.

**UX/UI quality bar (hard gate — first-class, not polish).** Every user-facing
change ships at production design quality, applying the Step 0b idioms. This is
enforced the same way correctness is — a working-but-ugly or inaccessible surface
does **not** satisfy Step 3:

- **Accessible by default** — semantic HTML, real labels, keyboard-reachable,
  visible focus states, sufficient contrast; `aria-*` only where semantics fall
  short. (per `uxui`)
- **Responsive** — works mobile → desktop; no fixed-width traps, no horizontal
  scroll, touch targets ≥ 44px.
- **Every interaction state designed** — hover, focus, active, disabled, loading,
  empty, and error are intentional, not defaulted. Loading/empty especially —
  datastar makes them cheap, so there's no excuse to skip them.
- **Idiomatic datastar / hypermedia** — drive UI from signals and server-rendered
  fragments per the `datastar` reference; no hand-rolled JS where a `data-*`
  attribute does the job. Handlers return small, focused HTML fragments.
- **Distinctive, not templated** — apply `frontend-design`: intentional type
  scale, spacing rhythm, and color; avoid the generic AI default look.
- **Verify it in the browser** — for non-trivial UI, drive it with the
  `playwright-skill` (or screenshot via `/run`) and *look*: confirm layout,
  states, and responsiveness actually render. Don't assume — UI bugs hide in the
  pixels, not the types.

**Comment comprehensively (hard gate).** Every Go file, type, function, and
non-obvious block you write or touch must carry comments that explain **why**, not
just restate the code. This is not optional — code without comments does not
satisfy Step 3:

- **Doc comments on every exported identifier** — package, type, func, const, var.
  Full sentences, start with the identifier name, per `effective-go` (Step 0).
  `// Parse reads …`, not `// parses`.
- **Package comment** — every package has one `// Package x …` doc comment stating
  what it does and why it exists.
- **Explain the why, not the what** — comment the intent, the tradeoff, the
  invariant, the gotcha. Skip comments that parrot the code (`i++ // increment i`).
- **Non-obvious blocks** — concurrency, error-handling choices, business rules,
  magic numbers, workarounds: a short comment on *why it is this way*. Tie it to
  the spec intent (Step 1a) or recalled decision (Step 1c) when one drove it.
- **Comment as you write**, not after — and keep comments in sync when you edit
  code. A stale comment is worse than none; fix or delete it.

We work **Agile** — favor reuse over repetition as you implement:

- **Spot duplication** — if the change repeats logic that already exists (or that
  you are about to write twice), stop and **refactor to a single shared
  unit** instead of copy-pasting. DRY beats a second copy.
- **Extract an interface** — when reuse is possible across more than one concrete
  type or call site, define a small Go **interface** at the consumer and depend on
  that, not the concrete type. Keep it minimal (accept interfaces, return structs)
  and idiomatic per Step 0.
- Surface the reuse/interface opportunity in the plan (Step 2) when you see it
  early; don't silently widen scope — flag it, then refactor surgically.

**Independent review on risky changes (Codex).** Don't trust your own pass alone
when a change reaches code that handles **user input / untrusted external input** —
parsers, validators, request handlers, auth, deserialization, anything a bad
payload or attacker touches. Before committing such a change, get an independent
Codex review of the diff:

- **Spawn the `codex:codex-rescue` agent** (Agent tool) with a **read-only** review
  prompt: point it at the working-tree diff and ask it to hunt correctness,
  security, and edge-case defects. Read-only means it reviews, it does not edit —
  don't let it apply patches (no `--write`).
- **Heavier native pass:** recommend the user run **`/codex:review`** (defect-focused)
  or **`/codex:adversarial-review`** (challenges the approach, assumptions, and
  tradeoffs). These are user-invoked slash commands — you can't fire them yourself,
  so surface the recommendation at the gate.
- This is a **sometimes** gate, not every step. Trigger on user-input and security
  surfaces and other high-stakes changes (concurrency, public APIs, data
  migrations). Skip it for trivial or internal-only edits.
- **Fold confirmed findings back in** before you commit; journal the notable ones
  in Step 4 so the next session recalls the failure mode.

**Commit often, push often.** Agile means small, verified increments land
continuously — not one giant end-of-task commit:

- **Commit after each verified step.** When a plan step is done and its check
  passes (test, build, lint, runtime output), make a focused commit. One logical
  change per commit; keep the diff small and the message clear about *why*.
- **Push after committing.** Don't let local work pile up — push so it's backed
  up, visible, and CI can run. Push at least at every natural stopping point.
- **Never bundle unrelated changes.** A refactor (the DRY/interface work above)
  and a feature change are separate commits.

This is **machine-enforced**: a `Stop` hook (`commit-guard.sh`) blocks the end of
your turn while the tree has uncommitted tracked changes or unpushed commits. So
commit and push *proactively* as you go — don't wait to be blocked, and never ask
the user to tell you to commit, merge, or push. If you intentionally leave
something uncommitted, state the reason before stopping.

## Step 4 — Before you stop, persist memory

The `Stop` hook is more than a commit gate — treat it as the **end-of-session
checkpoint** where you write what you learned back into the memory palace, so the
next session (Step 1c) recalls it. `am_search` is only the *read* side;
these are the *write* side, and they go beyond a one-line drawer:

- **Diary (what happened, why it mattered)** — call
  `am_diary_write` to journal this
  session: what you built/decided/learned and any open thread. Write in **AAAK**
  (compressed, entity-coded, emotion-marked), e.g.
  `SESSION:2026-06-22|added.diary+tunnel.step.to.M.md|why:stop.hook=natural.checkpoint|★★★`.
  Use a stable `agent_name` so the journal threads across sessions; read it back
  next time with `am_diary_read`.
- **Tool-usage notes (how to drive a tool, not just what it did)** — when this
  session you worked out a tool's correct params, calling sequence, deferred-load
  activation, or a gotcha the hard way, file it back (drawer or diary line) so
  **Memory-first tool use** recalls it next time instead of you re-fumbling it.
  Tag it by tool name so the recall query lands — e.g.
  `TOOL:create_tunnel|needs from_room+to_room+both wings|check find_tunnels first`.
- **Tunnels (link related memories across wings)** — when this session's work
  connects to another project/domain, weave a cross-wing tunnel with
  `am_create_tunnel` (e.g. an API design
  here ↔ the schema it depends on elsewhere). Before creating, check existing
  links with `am_find_tunnels` (which wings bridge) and
  `am_follow_tunnels` (what a room already connects to) so you reinforce,
  not duplicate.

Persist *before* you let the turn end — a verified change that isn't journaled or
linked is memory lost. Skip only when the turn produced nothing worth recalling;
say so if you skip.

If `$ARGUMENTS` is empty, stop after Step 1 and emit a concise **session-start
briefing** instead: what the specs cover, the current code shape, and the most
relevant recalled memories — no plan, no code.
