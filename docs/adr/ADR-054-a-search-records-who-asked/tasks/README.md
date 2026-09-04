# ADR-054 Tasks

Implementation tasks for ADR-054: A search records who asked, so a to-write list holds only questions. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T1, T2 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A search records the origin its request carried | pending | — | `go test ./internal/palace/ -run TestASearchRecordsTheOriginItsContextCarries …` |
| T2 | The kit sends the origin, and every hook declares what it is | pending | — | `go test ./clients/claude-code/ -run 'TestEveryRecallHookDeclaresItsOrigin|TestMCPCallSendsTheOriginHeaderFromTheEnvironment' …` |
| T3 | The to-write list is built from the searches nobody's hook made | pending | — | `go test ./internal/palace/ -run 'TestSuggestionsHoldNoHookRecalls|TestHookSearchesAreCountedPerWing' …` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `mcpprotocol.OriginHeader`, `mcpprotocol.OriginEnvVar`, `auth.OriginFrom` | T2, T3 | T1 before both |
| T1 | `search_events.origin` | T3 | T1 before T3 |
| T2 | hooks declare `hook:<basename>` | T3 | T3's live re-measurement needs T2 deployed |

## Notes

- T3's fence is hermetic; its S4 re-measurement needs the live local palace after a deploy carrying all three tasks, over a window that begins at that deploy.
- The migration number is allocated at merge (UPDATE.md §Schema migrations); `00037` in T1 is a placeholder for whatever is next then.
