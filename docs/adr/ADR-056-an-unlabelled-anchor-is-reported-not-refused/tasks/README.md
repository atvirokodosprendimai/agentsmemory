# ADR-056 Tasks

Implementation tasks for ADR-056: An anchor filed without a repository label is reported, not refused. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Both write tools report an anchor accepted without a label | done | — | `go test ./internal/mcptest/... -run 'TestAnUnlabelledAnchorIsReportedAtWrite$' …` |
| T2 | `doctor --corpus` reports unlabelled anchors as a population, not a verdict | done | — | `go test ./cmd/server/ -run 'TestDoctorCorpusReportsUnlabelledAnchors$' …` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

None — the tasks are independent.

## Notes

- Both tasks are hermetic. The seven unlabelled anchors measured on the local palace on 2026-09-04
  were labelled by hand that day, so a `doctor --corpus` run against it after T2 is expected to
  print zero for the population; a non-zero reading there is a new finding, not a test of T2, and
  it does not change the exit code either way.
