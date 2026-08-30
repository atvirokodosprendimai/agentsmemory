# ADR-034 Tasks

Implementation tasks for ADR-034: Record WHY the cross-encoder did not order a page. See the parent
ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | applyRerankWith returns WHY it did not rerank | done | — | `go test ./internal/palace/ -run 'TestTheReasonOnTheSpanIsTheReasonReturned'` + suite |
| T2 | persist the reason and report it | done | — | `go test ./internal/palace/ -run 'TestRecallStatsCountsWhyRerankingWasSkipped'` + `./internal/mcpserver/` + suite |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `applyRerankWith` returning a reason | T2 | T1 before T2 — T2's `recordSearch` write has nothing to persist otherwise |
