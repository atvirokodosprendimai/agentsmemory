# ADR-054: A search records who asked, so a to-write list holds only questions

**Status:** Proposed
**Date:** 2026-09-04
**Owner:** unassigned
**Spec:** None — no spec stage. The requirement is one sentence and the inbox finding that carries it is quoted in Context.
**Cross-references:** `docs/adr/ADR-001-recall-answers-or-abstains.md` (calibrates on `search_events` rows and must keep every row), `docs/adr/ADR-028-a-recall-you-can-judge.md` (the fetch log that made `recalls_fetched` a usage signal), `internal/palace/recallstats.go`, `clients/claude-code/mcpcall.go`, `internal/auth/auth.go`
**Governs:** None — declared by its tasks
**Enforced-by:** None — no gate exists at authoring time. T3 produces `internal/palace/recallstats_origin_test.go::TestSuggestionsHoldNoHookRecalls`, which fails when a `hook:` row reaches `suggestions`; naming it here before it exists is the rot the header warns about.
**Invalidates:** none — checked. ADR-001 reads every `search_events` row for calibration and continues to; this record filters a REPORT, not the table. ADR-028 T3's fetch log is untouched.
**Served-path change:** `am_recall_stats` stops listing a hook's automatic recalls under `unanswered` and `suggestions`, and reports per wing how many searches were hooks' — an agent following the to-write list writes memories for questions people asked instead of for strings of filenames nobody will ever search for.

## Context

`am_recall_stats` describes its `suggestions` list as *"one memory this team looked for and does not
have … and which wing to file it in"* — a to-write list. Measured 2026-09-04 over a 336-hour window
on the LOCAL palace this repository's sessions use (`am_status`: `mode: local`, workspace slug
`local`; 1,461 searches, 93% answered): seven of the ten `suggestions` are machine-shaped — a
branch name concatenated with changed filenames, or a run of merge-commit subjects — and every one of
the ten `unanswered` entries is a sentence the owner typed to the agent (*"am I asked anywhere to
stop, ever?"*), relayed verbatim by the task-recall hook. The `(unscoped)` pseudo-wing carries 848
of those 1,461 searches against zero drawers, because the hooks search with no wing.

**The magnitude is instance-specific; the class is not.** A reviewer re-ran the same window on
2026-09-04 against the HOSTED palace (workspace slug `atvirokodosprendimai-498ccd`): 97 `(unscoped)`
searches of 861, four `unanswered` entries — agent-composed inbox queries, last asked 2026-08-24, not
chat prompts — and none of the four suggestions machine-shaped. `(unscoped)` still carries searches
against zero drawers there, at a tenth of the volume and a different shape. A reader registered to
either instance who re-runs the command will get that instance's numbers, and the case for recording
the origin rests on the class of the defect — a hook's recall and a person's question share one
door — not on 848. The same shape was reported to this wing's inbox on 2026-09-02 and re-confirmed
2026-09-03; both reports named the root cause and stopped, correctly, because the remedy is a
decision.

The root cause is at the data layer. `search_events` (`db/migrations/00021_search_events.sql`)
records `id, team_id, wing, room, query, candidates, hits, top_score, reranked, created_at`, plus
`top_rerank_score` and `rerank_skip_reason` from later migrations. Nothing records WHO issued the
search. `Service.SearchPage` builds the row at `internal/palace/service.go` beside `s.repo.recordSearch(ctx, ev)`
from the query and the ranking outcome alone. The suggestion builder in `recallstats.go` is not
ignoring a distinction — the distinction does not exist.

Two populations write to that table through one door. A person, or an agent acting on its own
judgement, calls `am_search` because it wants an answer. A hook — `agentsmemory-recall-hook.sh` on
`SessionStart`, `agentsmemory-task-recall-hook.sh` on `UserPromptSubmit` — calls
`aiagentmemory mcp search` with whatever text it was handed: a branch name plus changed files, the
last commit subjects, the user's prompt. A hook's recall that finds nothing is a fact about the
palace worth counting; it is not a memory anyone should go and write. PR #169 gave the recall hook's
commit-subject fallback `--no-merges` and removed the single largest contributor (six repeats of one
merge-commit query), which narrowed the population without changing the class: the next-largest
entries are branch-plus-filenames and they are still there.

## Existing Primitives Audit

- **The wing header.** `X-Agentsmemory-Wing` (`internal/mcpprotocol/constants.go`) rides on every
  HTTP call from a registration; `auth.Bridge` lifts it into the context at the one place per request
  where HTTP is still visible (`internal/auth/auth.go`, `requestWing`), and `cmd/server/mcp.go` sets
  the same context value for the CLI's own `mcp` subcommand. **Reused as the shape:** the origin
  travels the same route, so nothing downstream learns a second way to receive a per-connection fact.
- **`aiagentmemory mcp`** (`clients/claude-code/mcpcall.go`) is the single client every shipped hook
  goes through — `set -- mcp search "$QUERY" …` in both recall hooks. It already builds the request
  headers from the registration. **Reused:** it is the one place the kit's machine callers can be
  told apart from a person, without touching any tool schema.
- **`searchEventRow` / `recordSearch`** (`internal/palace/recallstats.go`). **Reshaped:** one more
  column, written where the row is already built.
- **`RecallStats`** and `groupSuggestions` (`internal/palace/recallstats.go`). **Reshaped:** the
  unanswered scan gains a predicate; the per-wing struct gains one count.
- **Rejected as a primitive:** an `am_search` tool argument. The origin describes the CALLER, not
  the query; an argument would enter the tool schema every agent reads, be set by whoever chose to,
  and be omitted by whoever forgot. The header is set by the kit from the hook's environment and an
  agent never sees it.

## Decision

**Every search records its origin, supplied by the caller through the channel the wing already
travels, and the to-write list is built from the searches that had none.**

Concretely: `search_events` gains an `origin TEXT NOT NULL DEFAULT ''` column (additive migration).
`auth.Bridge` lifts a new `X-Agentsmemory-Origin` header into the context beside the wing;
`cmd/server/mcp.go` does the same for the CLI's in-process path. `Service.SearchPage` writes the
context's origin into the row. The kit's `aiagentmemory mcp` sends the header when
`AGENTSMEMORY_ORIGIN` is set, and each shipped hook exports `AGENTSMEMORY_ORIGIN=hook:<its own
basename>` before calling it; the eval's replay sets `eval`. An empty origin means a person or an
agent's own call — the population `unanswered` and `suggestions` exist to serve — and `RecallStats`
builds both lists over rows whose origin does not start with `hook:`, while every per-wing count
keeps every row and gains `hook_searches` so the split is visible rather than silent.

**What would make this fail, and whether the data exists today.** The criterion is the re-measurement
T3's sign-off records: over a window that begins at the deploy, no `suggestions` entry may carry a
`hook:` origin, and the top entries must read as questions. The data that could produce that failure
exists on this palace every session — both hooks fire on every session start and every prompt. Rows
written BEFORE the column exists carry `''` and are counted as a person's, so the lists clean up as
the window rolls past the deploy date rather than by rewriting history; the acceptance measurement is
taken over rows after it, and the sign-off says which window. Valid for the two deployments this
project runs — the local stack and the hosted workspace — because both serve the kit whose hooks set
the variable; a third-party client that never sets it is a person by construction, which is the
right default and the only one that does not require it to know about us.

## Alternatives Considered

- **(b) Filter on query SHAPE at suggestion time** — treat a query that is mostly filenames or
  commit subjects as not-a-question. Rejected because it is the heuristic-that-eats-real-questions
  this project has overturned before, and today's data shows why: *"inbox findings handed over from
  another project"* (asked three times, `wing_agentmemories`) is a real question a shape rule keeps,
  while *"so can we do a local triage now? test this mrw tool…"* is a person's sentence a
  filename-density rule might also keep — and *"Assert the flush actually runs Only ErrNotExist is
  absence…"* is three commit subjects that read as prose. The distinction is who asked, and only the
  caller knows that. Kept as a fallback if a hook ever cannot set the variable, and only after a
  measurement over the real corpus that names its false-negative rate.
- **A tool argument on `am_search`** (`origin: "hook"`). Rejected: puts the caller's identity in the
  query's schema, where every agent reads it and may set it; see the primitives audit.
- **Read the MCP `initialize` clientInfo name.** Rejected: the shipped hooks and a person's agent
  both connect through the same client (`aiagentmemory mcp` or Claude Code itself), so the client
  name does not separate them; and the transport is mounted stateless, so no session carries it to a
  later tool call.
- **Do nothing; let PR #169's narrowing stand.** Rejected: it removed one query, not the class, and
  the list's own description still promises a to-write list.

## Component / Boundary Impact

`internal/palace` (row, recorder, stats — one reason to change: how a recall is measured),
`internal/auth` (one more per-request fact lifted at the bridge), `cmd/server` (the CLI path sets the
same context value), `clients/claude-code` (the kit sends what it knows; hooks declare what they are),
`db/migrations` (one additive column). Ownership unchanged; `internal/palace` still imports no
surface (architecture contract D2). The architecture map's Module Map needs no delta — no module is
added or moved.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `X-Agentsmemory-Origin` HTTP header (`mcpprotocol.OriginHeader`) | new, optional | `clients/claude-code/mcpcall.go` from `AGENTSMEMORY_ORIGIN` | `internal/auth` `Bridge` |
| `AGENTSMEMORY_ORIGIN` env var | new, read by the kit CLI | each shipped hook exports it | `clients/claude-code/mcpcall.go` |
| `search_events.origin` column | new, additive, `''` default | `internal/palace` `SearchPage` | `RecallStats`, ADR-001 calibration (reads it as any other column) |
| `am_recall_stats` response: per-wing `hook_searches` | new `omitempty` key, named in the tool description | `internal/mcpserver/admin.go` | agents, the Stop hook's stats line |
| `am_recall_stats` response: `unanswered`, `suggestions` | semantics narrowed to origin-less rows | same | same |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `mcpprotocol.OriginHeader` and `auth.OriginFrom(ctx)` | T1 | T2, T3 | No — additive |
| `search_events.origin` column and `searchEventRow.Origin` | T1 | T3 | No — additive |
| `AGENTSMEMORY_ORIGIN` env var name (`mcpprotocol.OriginEnvVar`) | T1 | T2 | No — additive |

## Implementation

See `tasks/README.md`. Three tasks: T1 the column and the context (server), T2 the kit sends it and
the hooks declare it (client), T3 the report reads it and the live re-measurement (server + sign-off).

## Consequences

- **Positive:** the to-write list holds questions; a hook's failed recall is still counted, now
  visibly, as `hook_searches`; ADR-001's calibration population gains a column it can split on later.
- **Negative:** three surfaces learn one more per-connection fact; a hook on an older kit sends no
  origin and its recalls count as a person's until the kit is updated — `hook_searches: 0` beside a
  polluted list is the tell.
- **Neutral:** rows from before the column carry `''` and age out of the default 336-hour window two
  weeks after the deploy; nothing rewrites them.

## Out of Scope

- Splitting ADR-001's calibration by origin (permanent: boundary: that record decides what it
  calibrates on; this one only makes the column available to it)
- A shape heuristic over query text (deferred: this record's Alternatives — revisit only with a
  measured false-negative rate over the real corpus, filed against `docs/adr/BACKLOG.md` under
  "A shape heuristic for machine recalls needs its false-negative rate first")
- Origins other than `hook:<name>` and `eval` (permanent: boundary: the vocabulary is the kit's; a
  third-party client that sets nothing is a person, which is the right default)
- Session identity in `search_events` (permanent: fact: the MCP transport is mounted stateless, so no
  session id reaches a tool call; citation: file `internal/mcpserver/server.go:1`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A hook forgets to export the variable and its recalls pollute the list again | Med | Low | T2's gate reads every hook in `clients/claude-code/hooks/` and fails when one calls `mcp search` without exporting `AGENTSMEMORY_ORIGIN` |
| `hook_searches` is emitted and never discovered | Low | Med | named in the tool description; `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` covers it |
| The header is set by a person's registration by mistake and their questions vanish from the list | Low | Low | only the kit's CLI sends it, and only from the env var; a registration written by `install` never carries it |
| Old rows keep polluting for two weeks after deploy | High | Low | stated in the Decision; the sign-off names its window |

## Rollback

Additive everywhere. Stop sending the header (unset the variable in the hooks); the column stays
and reads `''`; `RecallStats` with no `hook:` rows behaves exactly as today. No migration to reverse.

## Follow-ups

- [ ] After two default windows have passed on the local palace, re-measure `suggestions` and record
  in this ADR whether a shape rule is still wanted for what the hooks could not label.
