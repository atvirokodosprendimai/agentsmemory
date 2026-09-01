# ADR-046 Tasks

Implementation tasks for ADR-046: Serve the whole entry record, then stop refusing long
ones. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers`
headers. This README is a derived index — when it disagrees with a task file, the task
file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

Strictly sequential, and the order is the whole safety argument: T2 deletes the refusal
that currently prevents a truncated front door, so it is only safe once T1 has made
truncation impossible. Doing them in the other order ships the silent cut.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Serve every chunk of an eager bootstrap record | done | — | `go test ./internal/palace/ -run "TestBootstrapServesEveryChunkOfAnEntryRecord\|TestBootstrapLeavesAShortEntryRecordUnchanged"` then the package suite |
| T2 | Delete the entry-room chunk refusal | pending | — | `go test ./internal/palace/ -run "TestALongEntryRecordIsAcceptedAndServedWhole"` then `go test ./...` |

Status: `pending` | `partial` | `blocked` | `done`.

Acceptance commands are abbreviated here; the task files carry the full fences,
including the `no tests to run` guard. The task file wins.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `Bootstrap serves whole memories` | T2 | T1 before T2 — T2 is unsafe without it |

## Notes

- Fences run the LOCAL Go toolchain, not `golang:1.26-alpine` under docker, for the
  reason ADR-045's tasks record: docker is unavailable on the executing machine.
- T1's multi-chunk fixture reaches `llm_init` by MOVING a record there, because the
  refusal T2 deletes still blocks the direct write while T1 is being built. That the
  move is a way in at all is a hole ADR-045 opened, and it is why this ADR exists now
  rather than later.
