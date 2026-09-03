# ADR-049 Tasks

Implementation tasks for ADR-049: Loopback is not a boundary a browser respects. See the
parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers`
headers. This README is a derived index — when it disagrees with a task file, the task
file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

One task. The change is a single seam and splitting the classifier from its wiring would
produce exactly the state this repository keeps shipping: a finished component that
nothing selects.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Refuse a request addressed elsewhere, wherever this machine is the boundary | done | — | `go test ./internal/auth/ ./cmd/server/ -run '…RebindGuard…'` then `go test ./...` |

Status: `pending` | `partial` | `blocked` | `done`.

Acceptance commands are abbreviated here; the task file carries the full fence, including
the `no tests to run` guard. The task file wins.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `The credential-free local endpoints refuse an off-machine Host or Origin` | — | none |

## Notes

- The fence runs the LOCAL Go toolchain rather than `golang:1.26-alpine` under docker,
  matching ADR-045's and ADR-046's tasks. The full `go test ./...` at the end of the
  fence is the same suite `scripts/redeploy.sh` gates a deploy on.
- The guard was verified against the RUNNING container as well as in tests, because a
  passing suite says nothing about the artifact that is serving — the same reason
  `redeploy.sh` greps the shipped binary. The live re-probe is recorded in the parent
  ADR's Context and in the Verification Log.
