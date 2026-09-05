# ADR-052 Tasks

Implementation tasks for ADR-052: One writer, many readers — make the writer
count a decision. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated.

## Execution Order

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3, T4 | T2 |
| 4 | T5 | T4 |
| 5 | T6 | T5 |

```
T1 ── T2 ─┬─ T3
          └─ T4 ── T5 ── T6
```

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Reproduce the lock upgrade failure as a red test | done | — | `go test ./cmd/server/ -run 'TestAReadThenWriteTransactionSurvivesConcurrentWriters$'` |
| T2 | One DSN per role, and a writer that takes its lock at BEGIN | done | — | `go test ./cmd/server/... -count=1` |
| T3 | The test harness opens the database we ship | done | — | `go test ./internal/mcptest/... ./cmd/server/ -count=1` |
| T4 | A read handle the read path cannot write through | done | — | `go test ./cmd/server/ -count=1` |
| T5 | Route internal/palace reads onto the read handle | done | — | `go test ./internal/palace/... ./cmd/server/... -count=1` |
| T6 | A gate that fails when the wiring is deleted | done | — | `go test ./cmd/server/ -run 'TestEveryServingHandleDeclaresItsRole$|TestNoServingOpenerAddsAWriteSerialisationPragma$|TestTheReadHandleCannotWrite$' -count=1` |

Status: `pending` | `partial` | `blocked` | `done`.

- `pending` — not started, or started and carrying no evidence yet.
- `partial` — genuinely part-done, with every landed claim checked as hard as a `done` one.
- `blocked` — waiting on something outside this repository, named in `**Blocked-on:**`.
- `done` — finished, with tool-written acceptance and mutation evidence to match.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `TestAReadThenWriteTransactionSurvivesConcurrentWriters` | T2 | T1 before T2 — T2's acceptance is this test going green |
| T2 | `openWriterDB` | T3, T4 | T2 before both — neither can name an opener that does not exist |
| T4 | `openReaderDB` | T5, T6 | T4 before T5 |
| T5 | `palace.NewRepo` | T6 | T5 before T6 — the gate asserts on the wired shape |

## Notes

- **T4 also ships `--db-reader-pool`**, decided by the owner on 2026-09-04 against the record's own first instinct. Its unit fence names `TestTheReaderPoolFlagReachesTheHandle` beside the two handle tests, and S4 is bound to `cmd/server/envreach_test.go`'s two existing env-documentation gates rather than to a reviewer noticing. The writer deliberately gets no equivalent knob — see the task's Invariants.
- **Rebase before wave 4.** T5 changes `palace.NewRepo`'s signature and touches every test that builds a `Repo`. Open ADR PRs on `internal/palace` collide with it; the record's Stop Condition requires rebasing onto `main` first.
- **T2 runs the whole `cmd/server` suite, not its own test alone**, because `foreign_keys(1)` can surface a latent constraint-ordering bug. A failure there is a finding, not noise.
- The measurements behind this ADR were taken with a throwaway module against the repository's pinned dependency graph, 2026-09-04. T1 is what puts them in the tree so they stop being a session's private evidence.
- **An earlier draft carried a seventh task adding `-race` to CI.** It was removed on 2026-09-04, before review, because the job already exists and is already required (`.github/workflows/build.yml:148`, added `11c7176` on 2026-08-30). The draft trusted ADR-042's open follow-up, which has been stale for five days, over the workflow file. The lesson is the one this repository already records: what you can observe now outranks a written record of it.
