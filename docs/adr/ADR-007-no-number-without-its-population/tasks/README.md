# ADR-007 Tasks

Implementation tasks for ADR-007: The eval may not print a number that means something other than
what it says. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T2 | none |
| 2 | T3 | none |
| 3 | T1 | none |

All three are independent — they fix three different numbers. T2 is first because ADR-003 is
currently blocked on a cell that cannot be read, and T3 second because tables are being generated
against this eval now, so every run taken before it lands is one nobody can place later.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-aggregate-within-one-population.md) | No statistic combines arms measuring different populations | scope-partitioned aggregation | none | done |
| [T2](T2-vacuous-comparisons-say-so.md) | A comparison whose mechanism had no input reports `not measured` | `ClosetCell` status | none | todo |
| [T3](T3-a-run-states-what-it-measured.md) | A run states its case set, and `BEST` states what it is best over | `CaseSetID`, `CaseSetOrigin`, `ranking` | none | done |
