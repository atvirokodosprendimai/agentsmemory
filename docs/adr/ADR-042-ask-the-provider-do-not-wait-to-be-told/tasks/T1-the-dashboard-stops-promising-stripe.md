# Task ADR-042-T1: Stop the dashboard promising a provider and a portal it does not have

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** none — corrects existing behaviour, adds no contract
**Consumes:** none
**Data dependency:** hermetic

## Goal

Under `BILLING_PROVIDER=opencollective`, stop rendering a "Manage your plan" button whose handler
can only fail, and stop telling the user their payment goes through Stripe and returns them to the
dashboard.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/handlers.go` | edit | `canManage` gated on plan alone; a workspace with no `subscriptions` row must not be offered a portal. This line is what SELECTS the ManageCard. |
| `internal/billing/billing.go` | edit | Adds `Service.HasRelationship`, applying the SAME test `ManageURL` applies, so the gate cannot disagree with the handler it guards. |
| `internal/web/views/project.templ` | edit | The hint promised "Secure checkout via Stripe … then land back here"; both clauses are false under OpenCollective. Doc comments on `UpgradeCard`/`ManageCard` corrected too. |
| `internal/web/views/project_templ.go` | regenerate | `templ generate` output — the served HTML, and the artifact the copy test actually renders. Never hand-edited. |
| `internal/web/billing_gate_test.go` | add | The gate test, DB-backed so it exercises the real `projectsForUser` path rather than a reimplementation of its logic. |
| `internal/web/views/upgrade_card_test.go` | add | The copy test. |

**Deviation from the ADR as authored, recorded rather than silently taken:** the ADR's step 2 called
for a `HasBillingRelationship` field on `ProjectVM`. Not done — the value is consumed one line later
in the same loop, so a local variable is the smaller diff and a view-model field nothing renders
would be a field with no reader. `Service.HasRelationship` (step 3) is unchanged and is where the
reuse lives. The gate test lives in a new `billing_gate_test.go` rather than `handlers_test.go`,
which does not exist in this package.

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCanManageRequiresARecordedSubscription` asserting a
   paid-plan workspace with no `subscriptions` row yields `CanManage == false`, and
   `TestUpgradeCardDoesNotNameAProviderItMayNotUse` asserting the rendered `UpgradeCard` contains no
   "Stripe" and makes no "land back here" promise. Confirm both are RED.
2. (Superseded during execution — see the deviation note above: no `ProjectVM` field was added, the
   value is a local in `projectsForUser`.)
3. Add the lookup to `Service` as a small, nil-safe method (`HasRelationship(ctx, teamID) bool`)
   rather than exposing `*Repo` to the web layer — the consumer keeps depending on the two methods
   it uses, per the existing `PlanStore` precedent.
4. Change `canManage` to `s.billing.Enabled() && isAdmin && !onFree && !isComped && hasRelationship`.
5. Rewrite the `UpgradeCard` hint to text true under BOTH providers and BOTH activation paths:
   name no provider, promise no redirect back, promise no timing.
6. Run `templ generate`; never edit `*_templ.go`.
7. Confirm both tests are GREEN and the full package suite still passes.

## Acceptance

```bash
go test ./internal/web/... -run 'TestCanManageRequiresARecordedSubscription|TestCanUpgradeIsUnaffectedByTheRelationshipGate|TestUpgradeCardDoesNotNameAProviderItMayNotUse|TestUpgradeCardStillExplainsTheHandoff' -count=1 2>&1 | tee /tmp/adr042-t1-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t1-new.out && \
grep -q "^ok" /tmp/adr042-t1-new.out && \
go build ./... && go vet ./... && go test ./internal/web/... ./internal/billing/ -count=1
```

The first command runs ONLY this task's four new tests, so the regression suites in the last command
cannot carry the verdict by themselves. The `grep -q "^ok"` is what makes this red today: with the tests
absent, `-run` matches nothing, Go prints `ok … [no tests to run]` and exits 0, so the exit code
alone would pass on an empty tree.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCanManageRequiresARecordedSubscription` | `internal/web/billing_gate_test.go` | A `pro_monthly` workspace with no `subscriptions` row gets `CanManage == false`; one WITH a row gets `true` | — |
| `TestCanUpgradeIsUnaffectedByTheRelationshipGate` | `internal/web/billing_gate_test.go` | The free-plan upgrade path is untouched — guards against "fixing" CanManage by suppressing both controls | — |
| `TestUpgradeCardDoesNotNameAProviderItMayNotUse` | `internal/web/views/upgrade_card_test.go` | Rendered `UpgradeCard` HTML names neither provider and makes no claim the user returns to the dashboard | — |
| `TestUpgradeCardStillExplainsTheHandoff` | `internal/web/views/upgrade_card_test.go` | The hint still exists — guards against passing the test above by deleting the copy | — |

Both mechanisms are proven falsifiable in the Mutation Log below. Note that the first mutant had to
be `hasRelationship := true` rather than deleting `&& hasRelationship`: the deletion leaves an unused
variable, so it does not compile, and `adr-verify` correctly graded that attempt `inconclusive`
rather than crediting a mutant that never ran.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestCanManageRequiresARecordedSubscription` |
| 2 — something selects it | the `canManage` expression in `projectsForUser` is itself the selection; the `hasRelationship := true` mutant proves the test reaches it |
| 3 — the caller can discover it | n/a: no declared interface — this removes a control rather than adding one |
| 4 — it is used | Observable as the absence of a failing flash; nothing measures this yet |

## Mutation Log

- 2026-08-28 · 71cfd56* · mutant inconclusive · exit 1 · `internal/web/handlers.go` · Removes the subscription-relationship condition, restoring the exact pre-ADR-042 gate that rendered a Manage button whose handler could only return ErrNoSubscription · acceptance-sha256:24f2adb30f737ebfda1b450193b08050cdf0e794c9356930e360ea8872541f4e
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-08-28 · 71cfd56* · mutant killed · exit 1 · `internal/web/handlers.go` · Makes the relationship lookup always answer yes — the exact pre-ADR-042 behaviour, since gating on the plan alone is equivalent to assuming a relationship exists. Chosen over deleting the condition because that mutant does not compile (unused variable) and so tests nothing. · acceptance-sha256:24f2adb30f737ebfda1b450193b08050cdf0e794c9356930e360ea8872541f4e
- 2026-08-28 · 71cfd56* · mutant killed · exit 1 · `internal/web/views/project_templ.go` · Restores the exact shipped falsehood in the GENERATED file, which is what compiles and what the test renders — editing project.templ alone would not reach the test without regeneration, so the mutant targets the artifact actually under test. · acceptance-sha256:24f2adb30f737ebfda1b450193b08050cdf0e794c9356930e360ea8872541f4e
- 2026-08-28 · 307bf33* · mutant killed · exit 1 · `internal/web/handlers.go` · Makes the relationship lookup always answer yes — the exact pre-ADR-042 behaviour, since gating on the plan alone assumes a relationship exists. Chosen over deleting the condition because that mutant does not compile (unused variable) and so tests nothing. · acceptance-sha256:d92110e33a3cdf6c3d54da3e9d2de192facbce9fe483e410b007e9c4c6329021
- 2026-08-28 · 307bf33* · mutant killed · exit 1 · `internal/web/views/project_templ.go` · Restores the exact shipped falsehood in the GENERATED file, which is what compiles and what the test renders. · acceptance-sha256:d92110e33a3cdf6c3d54da3e9d2de192facbce9fe483e410b007e9c4c6329021

## Invariants

- The Stripe flow is unchanged: a Stripe workspace with a real subscription still sees ManageCard.
- `*_templ.go` is only ever regenerated, never hand-edited.
- The copy must remain true after T5 makes activation automatic — so it states no timing and names
  no provider. A later task may make it MORE specific; it must never make it false again.

## Risks

- Rewriting the hint to describe manual activation would go stale the moment T5 lands. Mitigated by
  the invariant above: describe the destination, not today's mechanism.
- `HasRelationship` adds a query to the project list. It is one indexed lookup per team on a page
  that already does several; if it shows up, fold it into the existing subscription read.

## Stop Condition

If the reviewer judges that the ManageCard should instead be shown with a different action for
OpenCollective (linking straight to the project page without going through `ManageURL`), stop —
that is a product decision about what "manage" means for a donations platform, not an
implementation detail, and it changes this task's shape.

## Out of Scope

- Making activation automatic — that is T2–T5.
- The `getBillingSuccess` copy, which OpenCollective never reaches today and T5 revisits.

## Verification Log
- 2026-08-28 · 71cfd56* · exit 0 · `go test ./internal/web/... -run 'TestCanManageRequiresARecordedSubscription|TestCanUpgradeIsUnaffectedByTheRelationshipGate|TestUpgradeCardDoesNotNameAProviderItMayNotUse|TestUpgradeCardStillExplainsTheHandoff' -count=1 2>&1 | tee /tmp/adr042-t1-new.out && \ …` · acceptance-sha256:24f2adb30f737ebfda1b450193b08050cdf0e794c9356930e360ea8872541f4e
- 2026-08-28 · 307bf33* · exit 0 · `go test ./internal/web/... -run 'TestCanManageRequiresARecordedSubscription|TestCanUpgradeIsUnaffectedByTheRelationshipGate|TestUpgradeCardDoesNotNameAProviderItMayNotUse|TestUpgradeCardStillExplainsTheHandoff' -count=1 2>&1 | tee /tmp/adr042-t1-new.out && \ …` · acceptance-sha256:d92110e33a3cdf6c3d54da3e9d2de192facbce9fe483e410b007e9c4c6329021
