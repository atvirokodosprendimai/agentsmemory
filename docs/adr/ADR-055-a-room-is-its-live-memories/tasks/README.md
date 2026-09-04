# ADR-055 Tasks

Implementation tasks for ADR-055: A room is the set of its live memories, and every surface that lists rooms says so. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Every surface that lists or counts rooms reads live rows, and one test asks them all | pending | — | `go test ./internal/palace/ -run TestEveryRoomListingAgreesOnARetractedRoom …` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

None.

## Notes

- The enumeration command and its 2026-09-04 count live in the task; re-run it before editing.
