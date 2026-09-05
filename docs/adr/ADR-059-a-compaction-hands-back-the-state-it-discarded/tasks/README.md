# ADR-059 Tasks

Implementation tasks for ADR-059: A compaction hands back the state it discarded. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A PreCompact hook writes the session's state note before the context is summarised | pending | — | `go test ./clients/claude-code/ -run 'TestThePreCompactHookWritesTheStateNote$\|TestThePreCompactHookIsRegistered$' …` |
| T2 | The SessionStart recall on `source=compact` hands the note back and recalls the session's checkpoint | pending | — | `go test ./clients/claude-code/ -run 'TestACompactStartHandsBackTheStateNote$\|TestAColdStartDoesNotReadTheNote$' …` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | the note file and its `key=value` line format | T2 | T1 before T2 |

## Notes

- T2's fence is hermetic; its S4 live compaction is recorded in the sign-off, and a silent one is the Stop Condition, not a pass.
