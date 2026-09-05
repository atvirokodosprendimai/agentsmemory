# ADR-061 Tasks

Implementation tasks for ADR-061: The wake-up hands back the last turn. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The Stop hook writes the project's last-turn note | done | — | `go test ./clients/claude-code/ -run 'TestTheStopHookWritesTheLastTurnNote$\|TestTheLastTurnNoteIsOffWhenAsked$' …` |
| T2 | A `startup` or `resume` opens with the last-turn note and asks the checkpoint when the branch matches | pending | — | `go test ./clients/claude-code/ -run 'TestAColdStartOnTheSameBranchHandsBackTheLastTurn$\|TestAColdStartOnAnotherBranchKeepsCraft$' …` |
| T3 | `/am` and the bootstrap protocol read the wake-up before planning | pending | — | `go test ./clients/claude-code/ -run 'TestBothProtocolsReadTheWakeUp$' …` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | the note path, key and `key=value` format | T2 | T1 before T2 |

## Notes

- T2's S4 is a real restart in this checkout, recorded with `adr-verify --human`; a silent one is its Stop Condition.
- T1's Stop Condition is measured first: one real Stop payload must carry `transcript_path`.
