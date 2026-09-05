# ADR-060 Tasks

Implementation tasks for ADR-060: A recall you can afford to page — `ids_only` on `am_search`. See the parent ADR for the decision.

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
| T1 | `am_search` returns a thin, truthful page when `ids_only` is true | done | — | `go test ./internal/mcptest/ -run 'TestAnIdsOnlyPageCarriesNoContentAndSaysSo$' …` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

None — one task.

## Notes

- Hermetic; the size ratio is asserted on the test's own fixture, and the production numbers (9–10x) are in the record's Context, dated.
