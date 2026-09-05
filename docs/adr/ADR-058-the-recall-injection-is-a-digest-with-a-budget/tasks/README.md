# ADR-058 Tasks

Implementation tasks for ADR-058: The recall injection is a digest with a budget, not a dump. See the parent ADR for the decision.

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
| T1 | `mcp search --digest <chars>` renders a bounded plain-text digest of the page | done | — | `go test ./internal/mcpcli/ -run 'TestTheDigestFitsItsBudget$' …` |
| T2 | both recall hooks use the digest, carry the installed wing, and say "could not look" on both channels | pending | — | `go test ./clients/claude-code/ -run 'TestTheRecallHookCarriesTheInstalledWing$|TestARecallThatCouldNotLookSaysSoOnBothChannels$|TestTheHookPrefixCarriesTheWing$' …` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `--digest` on `mcp search` | T2 | T1 before T2 |

## Notes

- Both tasks are hermetic; T2's S6 re-measurement is a human step on a real install and is recorded in the ADR, not in the fence.
