# ADR-008 Tasks

Implementation tasks for ADR-008: Every tool the palace exposes must be exercised end to end, or the
build fails. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T4 | T1 |
| 4 | T3 | T2 |

T2 lands red on purpose — it reports the 39 tools with no scenario, which is the measurement this
ADR opens with. T4 needs only the harness, so it runs beside T2 rather than behind it. T3 is last
because it is the widest, and because its three regression scenarios are the adoption bar: if the
gate cannot fail on the defects it was designed after, the ADR is re-planned rather than shipped.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-the-harness.md) | Stand a real server up in a test and prove one round trip | `mcptest.Harness` | none | done |
| [T2](T2-every-tool-or-the-build-fails.md) | Every registered tool has a scenario, or the build fails | the scenario registry + gate | `mcptest.Harness` | done |
| [T3](T3-lifecycle-round-trips.md) | Create, read, update, delete — proven by reading back | one-party scenarios + 3 regressions | the scenario registry | done |
| [T4](T4-two-and-three-party.md) | Two parties see what they should, three prove isolation | multi-party scenarios | `mcptest.Harness` | done |
