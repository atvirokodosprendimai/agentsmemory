# ADR-042 Tasks

Implementation tasks for ADR-042: Ask the payment provider, do not wait to be told. See the parent
ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | none |
| 4 | T4 | T2, T3 |
| 5 | T5 | T4 |

T1 is first despite depending on nothing, because it is the only task that changes what a user sees
today: it removes a button that cannot work and a sentence that is false. Putting the observable
slice first is deliberate — a human can check the direction before any of the machinery exists.

T2 and T3 are independent of each other and of T1; run them in either order, or in parallel.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Stop the dashboard promising a provider and a portal it does not have | done | — | `go test ./internal/web/...` over its 4 named tests + full build/vet/suite (see the task file for the exact fence) |
| T2 | Make a contribution name the workspace that started it | done | — | `go test ./internal/billing/` over its 6 named tests + full build/vet/suite (see the task file for the exact fence) |
| T3 | Read contributions from the authenticated Open Collective API | done | — | `go test ./internal/billing/ -run 'TestOCOrderSource...'` (4 tests) + full build/vet/suite |
| T4 | Turn an order into the plan change the webhook would have made | done | — | `go test ./internal/billing/ -run 'TestReconcile'` (8 tests) + full build/vet/suite |
| T5 | Wire the reconciler so something actually selects it | done | — | its 4 named tests + the config/doc gate family + gofmt + `go test ./...` (see the task file for the exact fence) |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T2 | `IntentRepo`, `intentTag(teamID)` | T4 | T2 before T4 |
| T3 | `orderSource`, `providerOrder`, `newOCOrderSource()` | T4, T5 | T3 before T4 and T5 |
| T4 | `Reconciler.ReconcileOnce(ctx)` | T5 | T4 before T5 |

## Notes

- **T4 is not hermetic and its gate cannot see why.** Its unit tests run on fixtures, but the ADR's
  central open question — whether a `tags` value set on the checkout URL arrives on `Order.tags` —
  can only be answered by one real contribution through the live hosted flow. T4's sign-off must
  record the Open Collective order id the tag was read back from and which channel attributed it.
  A green unit run is NOT that evidence.
- **T3 and T4 each ship a component nothing selects.** That is intended sequencing, not an
  oversight, and it is this repo's most-shipped defect class — so neither may be presented as
  "activation works". Only T5 makes the feature reachable, and only its gate proves it.
- Verified 2026-08-28 for allocation races, by unioning `origin/main`, every remote branch and all
  open PR heads: ADR number **042** and migration version **00034** are both free. Both are
  allocated at authoring, which the palace records as a recurring cross-branch collision risk —
  re-check both before merge.
- Production state at authoring: the OpenCollective provider is live, the upgrade button works, and
  `account(slug:"ai-agents-memory").orders` returns `totalCount: 0` — there is no paying customer
  yet, so nothing here is a migration of existing subscriptions.
