# ADR-057 Tasks

Implementation tasks for ADR-057: codebase-memory is a checked peer of the kit, not an unwatched one. See the parent ADR for the decision.

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
| T1 | `doctor` reports the codebase-memory peer: ok, absent, DUPLICATE or BROKEN | pending | — | `go test ./clients/claude-code/ -run 'TestDoctorReportsTheCodebaseMemoryPeer$' …` |
| T2 | the installer registers the peer under one name and removes duplicate hook entries | pending | — | `go test ./clients/claude-code/ -run 'TestEveryInstallRemovesDuplicateHookEntries$|TestRecommendedRegistersThePeerOnceUnderUpstreamsName$' …` |

Status: `pending` | `partial` | `blocked` | `done`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | the `codebase-memory` doctor row | T2 | T1 before T2: T2's real-machine check reads the row |

## Notes

- Both tasks are hermetic. The real fixture — the owner's `~/.claude/settings.json.bak-20260905-*` with four `cbm-session-reminder` entries — is what the tests reproduce; a doctor run over the live file after T1 is the sign-off worth recording in the PR.
