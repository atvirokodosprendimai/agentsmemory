# ADR-036 — tasks

Implementation of [ADR-036: put the knowledge graph on the read path](../ADR-036-a-recall-that-answers.md),
which inherits its Contracts, Non-Goals and Risks from
[the spec](../../specs/2026-08-26-a-recall-that-answers.md) by reference.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers. This
README is a derived index — when the two disagree, the task file is right and this file is stale.

## Execution Order

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1, T2, T6 | none |
| 2 | T3 | T1, T2 |
| 3 | T4, T5, T7 | T3 (T4, T5) · T6, T2, T3 (T7) |
| 4 | T8 | T7, T3, T5 |

**Wave 1 is the floor, and its shape is the point.** T1 builds the instrument before any
FACT-RETRIEVAL task can claim an improvement — there is no eval arm for fact retrieval today, so
that capability is unmeasurable and therefore unimprovable. The claim is scoped deliberately: T2's
four-state lookup and T6's write-path edges are correctness work whose proof is a test, not a score,
so they do not wait on a measurement that would say nothing about them. T2 makes absence
distinguishable from failure, without which T3's sibling pointer rests on a lookup that fails open.
T6 settles the derived-edge CONTRACT before T7 indexes against it.

**T7 moved to wave 3** because it consumes `palace.WingPolicy`. An entry point returns outgoing edges,
and an edge can point into another wing — a crossing no fact-content check sees. It also reuses T2's
absence vocabulary, which was prose in its Ordered Steps and is now a declared dependency.

**What wave 1 does NOT buy:** T6 fixes the write path only. The 1,928 existing orphans stay
orphaned, so the live corpus is still ~97% unreachable when T7 ships (57 of 1,985 drawers carry any
edge, measured 2026-08-26). The backfill is deferred with a receipt in `BACKLOG.md`, and no task
here claims live-corpus coverage it does not deliver.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The instrument: a fact answerable-rate with a 0% baseline | done | F-5, F-6 | 3 tests (2 palace + privacy gate) + suite |
| T2 | A lookup that distinguishes four outcomes, not two | done | F-12 | 2 tests (palace + mcpserver) + suite |
| T6 | Every new drawer is REACHABLE, and derived edges say so | done | F-11, UC5-S1, UC5-S2 | 2 tests (palace + mcpserver) + suite |
| T3 | Facts reach the page, wing-resolved, in three states | done | F-1, F-2, F-8, F-9, F-18, UC1-S1, UC2-S1, UC2-S2 | 7 tests (6 palace + mcpserver) + suite |
| T7 | A wing reports its own entry point, resolved directly | done | F-10, UC4-S1, UC4-S2 | 2 tests (palace + catalogue) + suite |
| T4 | Both entity vocabularies, and an ended fact is never current | done | F-4, F-7, UC1-S2 | 2 tests + suite |
| T5 | A corrected record arrives carrying its correction | done | F-3, UC3-S1, UC3-S2 | 2 tests (palace + mcpserver) + suite |
| T8 | The protocol becomes an API, and proves it costs less for the same meaning | done | F-13, F-14, F-15, F-16, F-17, F-19, UC6-S1, UC6-S2, UC6-S3 | 7 tests (6 palace + catalogue) + suite |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

**Every fence carries `set -o pipefail`, an explicit `$rc` check, and a `-skip` list.** All three are
load-bearing:

- **`pipefail` + `$rc`** — `go test | tee` discards the test binary's exit status, so a fence that
  judges by grepping output alone passes a run that failed without printing a matched `FAIL` line.
  The grep now only catches the empty-filter case; the exit code decides.
- **`-skip`** — all 27 ADR-036 stubs are committed failing (20 in `internal/palace`, 6 in
  `internal/mcpserver`, 1 in `internal/repohygiene`, verified 2026-08-26), so an unskipped `go test ./...` stays red until the
  last task lands. Every earlier task would be structurally unable to record an exit-0 `adr-verify`
  entry, and `adr-lint` refuses `done` without one. A fence that cannot pass blocks its wave as
  surely as one that cannot fail. Each list skips exactly the stubs owned by tasks it does not
  depend on, so a fence still runs its ancestors' tests: T1 3 · T2 2 · T6 2 · T3 12 · T4 14 · T5 14 ·
  T7 16 · T8 25.

Proven two-sided 2026-08-26: T1's fence exits 1 today and exits 0 with only T1's own stubs
neutralised, while T3's fence still exits 1 in that same state. **No single fence runs all 27** — T8
skips T4's two, because T4 and T5 share wave 3 and T4 is not guaranteed done when T8 runs. The full
suite green is proven by CI on the merged branch, not by any one task's gate.

**Seven of the 27 stubs live outside `internal/palace`, and that is deliberate.** No test in
`package palace` can observe an MCP render site or a tool registration — it calls the service
directly, which is exactly what a caller that was never wired also does. Five tasks claim "delete
the render line and the test goes red"; without a test in the serving package that claim is false.
These sit beside `catalog_test.go` and `hitview_test.go`, which exist for the same reason. The
twenty-seventh is in `internal/repohygiene`: T1's fixtures must carry no palace content, and that is
a boundary ADR-003 T2 closed permanently — mechanical enforcement, not a note asking the next author
to remember.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | the fact-retrieval arm + frozen corpus | T3, T4, T8 | T1 before any task claiming a fact-retrieval improvement |
| T2 | `kg.Resolution` (term/triple states) | T3, T7 | T2 before T3 — the pointer rests on it; T7 reuses its absence vocabulary, now declared |
| T3 | `Service.factsFor` (three-state facts) | T4, T5, T8 | T3 before T4, T5, T8 |
| T3 | `palace.WingPolicy` (the single authorization point) | T5, T7, T8 | T3 before all three — F-19 requires one rule, and four filters that agree today diverge on the path nobody tested |
| T5 | `kg.CorrectionsFor` (the incoming sweep) | T8 | T5 before T8 — one sweep, not two that can disagree |
| T6 | derived-edge contract + marker column | T7, T8 | T6 before T7 — the contract must be settled before an entry point indexes against it |
| T7 | `Service.EntryPoint` | T8 | T7 before T8 |
