# ADR-045 Tasks

Implementation tasks for ADR-045: Move a memory, not a row. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T1 |

T2 and T3 both depend only on T1 and touch different files, so they may run in either
order once T1 lands. T1 is first because it is the served-path change: after it, a
relocation of any size works, which is the thing a human can test.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Relocate every chunk of a memory in one transaction | done | — | `go vet ./... && go test ./internal/palace/ -run "TestAMoveRelocatesEveryChunkOfAMemory\|TestAMoveThatCollidesOnAnyChunkRelocatesNone"` then the package suite |
| T2 | Make a relocation carry its derived edges | done | — | `go vet ./... && go test ./internal/palace/ -run "TestAMoveEndsTheOldRoomsEdgeAndAttachesTheNew"` then the package suite |
| T3 | Retire the one-way-door claim, and gate it | done | — | `go vet ./... && go test ./internal/mcpserver/ -run "TestNoToolDescriptionClaimsALongMemoryCannotBeMoved"` then `go test ./...` |

Status: `pending` | `partial` | `blocked` | `done`.

Acceptance commands are abbreviated here; the task files carry the full fences,
including the docker invocation and the `no tests to run` guard. The task file wins.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `Service.moveMemory()` | T2, T3 | T1 before both |

## Notes

- Every fence runs the LOCAL Go toolchain rather than `golang:1.26-alpine` under
  docker as the rest of this corpus does. Amended 2026-09-01 during execution:
  docker is unavailable on the executing machine, and a fence that cannot run is
  not a gate. T1 carries the full reasoning.
- Each fence runs its NEW test alone first, so the regression suite chained after it
  cannot carry the verdict, and guards against `no tests to run` — a bare
  `go test -run <no match>` exits 0, which would make every fence green at authoring.
- T2's single-chunk subtest fails against today's code, before T1: the missing derived
  edge on a relocation is a pre-existing defect, not one this ADR introduces.
