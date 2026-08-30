# ADR-037 — tasks

Implementation of [ADR-037: Carry the why with the code, and gate the citations](../ADR-037-the-why-travels-with-the-code.md).

**Source of truth:** the task files' headers. This README is a derived index — when the two
disagree, the task file is right and this file is stale.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Every ADR cited in Go source resolves | done | none — no spec | repohygiene citation test + vet |
