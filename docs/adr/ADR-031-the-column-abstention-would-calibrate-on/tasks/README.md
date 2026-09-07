# ADR-031 Tasks

Implementation tasks for ADR-031: Keep the one score that separates a recall that worked from one that did not. See the parent ADR for the decision.

**Source of truth:** the task file's headers. This README is a derived index.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| Task | Goal | Produces | Consumes | Status | Acceptance |
|------|------|----------|----------|--------|------------|
| T1 | Keep the separating score, and report it with an honest denominator | `search_events.top_rerank_score`; `AvgTopRerank` + `Reranked`; the two `am_recall_stats` fields | none | done | `go test ./internal/palace/ -run "^(TestRerankSignalIsReportedAndNotDilutedByUnrerankedRows)$"` |

## Not a task here

**The abstention threshold itself.** This ADR keeps the signal; spending it is ADR-001's T3, which stays BLOCKED on its own preflight — a corpus measuring 100% in-pool is saturated and the go/no-go cannot be taken there in either direction. What this ADR removes is a *second*, independent obstacle: even on a clean corpus, calibrating on `top_score` under `FUSION=rrf` would have been calibrating on a constant with a 16% dynamic range. Both had to be true for T3 to be answerable, and only one was known.
