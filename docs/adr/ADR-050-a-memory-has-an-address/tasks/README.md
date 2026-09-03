# ADR-050 Tasks

Implementation tasks for ADR-050: A memory has an address. See the parent ADR for the
decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers`
headers. This README is a derived index — when it disagrees with a task file, the task
file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

One task. The handler, the capability declaration and the `uri` on every hit are three
halves of one thing: a resource nothing advertises is unreachable, and an address no
response hands out is one a caller has to compose from a scheme it was never told. Split
across tasks, any one of them could merge alone and look finished.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Serve one memory by URI, advertise the template, and hand the address out with every hit | done | — | `go test ./internal/mcpserver/ -run '…Resource…'` then `go test ./...` |

Status: `pending` | `partial` | `blocked` | `done`.

Acceptance commands are abbreviated here; the task file carries the full fence, including
the `no tests to run` guard. The task file wins.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `A memory is addressable at agentsmemory://wing/{wing}/room/{room}/drawer/{id}` | — | none |

## Notes

- The fence runs the LOCAL Go toolchain, matching ADR-045, ADR-046 and ADR-049. The full
  `go test ./...` at the end is the same suite `scripts/redeploy.sh` gates a deploy on.
- Verified against a RUNNING server over HTTP as well as in tests, because a passing suite
  says nothing about the artifact that is serving. The live probe is in the Verification
  Log.
