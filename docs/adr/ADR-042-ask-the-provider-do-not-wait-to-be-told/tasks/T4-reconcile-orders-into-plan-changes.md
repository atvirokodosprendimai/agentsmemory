# Task ADR-042-T4: Turn an order into the plan change the webhook would have made

**Depends-on:** T2, T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `Reconciler`, `Reconciler.ReconcileOnce(ctx) (ReconcileReport, error)`
**Consumes:** `IntentRepo` + `intentTag(teamID)` (T2), `orderSource` + `providerOrder` (T3)
**Data dependency:** needs ONE real contribution through the live hosted checkout, to confirm the `tags` value set by T2 arrives on `Order.tags`. The unit gate below is hermetic and does NOT prove this; the sign-off line must record the Open Collective order id the tag was read back from, and which channel attributed it (tag or email).

## Goal

Map each incoming order onto the existing `providerEvent` vocabulary, attribute it to a workspace,
and apply it through the existing `applyActivated` / `applyCanceled` — never a second plan-flip path.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/billing/reconcile.go` | add | The mapper and the reconcile pass. |
| `internal/billing/billing.go` | edit | Expose `applyActivated`/`applyCanceled` to the reconciler within the package; add nothing public. |
| `internal/billing/reconcile_test.go` | add | Mapping, attribution, idempotency and refusal tests. |
| `db/migrations/00035_billing_applied_orders.sql` | add | The applied-order ledger (review B1). Version `00035` union-checked free across `origin/main`, every remote branch and all open PR heads. |
| `internal/billing/reconcile_idempotence_test.go` | add | The three reproductions of the PR #96 review findings. |
| `internal/billing/intent.go` | edit | `MatchByEmail` refuses an ambiguous match (review B2); the `intentTag` comment stops implying unforgeability (review A3). |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestReconcileMapsOrderStatusToEventKind`,
   `TestReconcileAttributesByTagOnlyWithAMatchingIntent`,
   `TestReconcileLeavesAnUnattributableOrderAlone`, `TestReconcileIsIdempotent`. Confirm RED.
2. Map status → kind, exhaustively over the 14 values read from the live enum 2026-08-28:
   `ACTIVE`, `PAID` → `eventActivated`; `CANCELLED`, `EXPIRED`, `REFUNDED`, `REJECTED`, `ERROR` →
   `eventCanceled`; `NEW`, `PENDING`, `PROCESSING`, `REQUIRE_CLIENT_CONFIRMATION`, `DISPUTED`,
   `IN_REVIEW`, `PAUSED` → `eventIgnored`. Write the mapping as a table keyed by the enum string and
   default UNKNOWN statuses to `eventIgnored` plus a log line — a status Open Collective adds later
   must never be silently read as a cancellation.
3. Resolve the plan code from the tier SLUG using the configured tier→plan map, not from the
   amount. An unknown tier is `eventIgnored` with a log line.
4. Attribute in this order, and stop at the first hit: (a) a `tags` value that matches a
   `billing_checkout_intents` row for that plan; (b) `FromAccountEmail` matching an intent row's
   email; (c) NOTHING — leave the order alone, log it once with its order id, and continue. A tag
   with no matching intent is NOT attribution.
5. Build the `providerEvent` and call the existing `applyActivated` / `applyCanceled`. Set
   `subscriptionID` from the order id so `applyCanceled`'s lookup key works, and populate
   `CurrentPeriodEnd` from `NextChargeDate`.
6. Return a `ReconcileReport` carrying counts (seen, activated, canceled, ignored, unattributed) so
   the caller can log a number rather than a silence.
7. Confirm GREEN.

## Acceptance

```bash
go test ./internal/billing/ -run 'TestReconcile|TestEmailFallbackRefusesAnAmbiguousMatch|TestApplyIsIdempotentWithoutTheLedger|TestLedgerRecordsOnlyDecisionsActuallyTaken|TestUnknownFrequencyIsNotTreatedAsRecurring' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t4-new.out && \
grep -q "^ok" /tmp/adr042-t4-new.out && \
go build ./... && go vet ./... && go test ./internal/billing/ ./internal/web/... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestReconcileMapsOrderStatusToEventKind` | `internal/billing/reconcile_test.go` | All 14 known statuses map as specified, and an unknown status maps to `eventIgnored` rather than to a cancellation | — |
| `TestReconcileAttributesByTagOnlyWithAMatchingIntent` | `internal/billing/reconcile_test.go` | A tag with no recorded intent does NOT upgrade the workspace it names; the same order DOES activate once the intent exists | — |
| `TestReconcileAttributesByEmailWhenTheTagIsAbsent` | `internal/billing/reconcile_test.go` | The email fallback attributes when no tag survived — the ADR's answer if `tags` does not round-trip | — |
| `TestReconcileLeavesAnUnattributableOrderAlone` | `internal/billing/reconcile_test.go` | An order with no tag and an unknown email changes no plan and is counted as unattributed | — |
| `TestReconcileIgnoresAContributionOutsideOurTiers` | `internal/billing/reconcile_test.go` | An ordinary donation (no tier) is not treated as a purchase even when it carries a valid tag | — |
| `TestReconcileIsIdempotent` | `internal/billing/reconcile_test.go` | Three passes leave one subscription row and one plan value, and `nextChargeDate` populates `CurrentPeriodEnd` | — |
| `TestReconcileDoesNotResurrectACanceledSubscription` | `internal/billing/reconcile_test.go` | A stale `ACTIVE` for an order already recorded canceled does not re-upgrade — the existing `applyActivated` guard holds on this path too | — |
| `TestReconcileReturnsTheReadError` | `internal/billing/reconcile_test.go` | A failing order source is an error, not a quiet pass with nothing to do | — |
| `TestReconcileDoesNotRevertAnOperatorDowngrade` | `internal/billing/reconcile_idempotence_test.go` | An operator's `set-plan` downgrade survives the next pass — the applied-order ledger, not the provider's unchanged state, is what makes a POLL idempotent | — |
| `TestReconcileDoesNotGrantARecurringPlanForAOneOff` | `internal/billing/reconcile_idempotence_test.go` | A ONETIME contribution to a recurring tier is ignored, not treated as a subscription that never expires | — |
| `TestEmailFallbackRefusesAnAmbiguousMatch` | `internal/billing/reconcile_idempotence_test.go` | Two workspaces sharing one email attribute to NEITHER, instead of to whichever clicked Upgrade last | — |
| `TestLedgerRecordsOnlyDecisionsActuallyTaken` | `internal/billing/reconcile_idempotence_test.go` | An activation the stale-re-delivery guard declines writes NO ledger row — the ledger records what the server did, not what it refused to do | — |
| `TestUnknownFrequencyIsNotTreatedAsRecurring` | `internal/billing/reconcile_idempotence_test.go` | An order whose recurrence the provider did not state is refused, not admitted — `Order.frequency` is nullable, so this is a real state | — |
| `TestApplyIsIdempotentWithoutTheLedger` | `internal/billing/reconcile_idempotence_test.go` | Repeated application of the SAME event converges — held with the ledger deliberately absent, because the ledger otherwise short-circuits the very passes this is about | — |

### Review findings from PR #96, reproduced before fixing

Ryouku requested changes with two defects, both reproduced by their own probe and then **independently
reproduced here** before anything was changed — a finding is a hypothesis, and this is payment code.
All three reproductions were red, and are the three tests added above.

**B1 — a poll is not a webhook, and the idempotence that was enough for one is not enough for the other.**
`set-plan` writes only `teams.plan_id`, and `applyActivated`'s only re-delivery guard is "the
subscription row says canceled". After an operator downgrades, the row still reads `active` and the
order is still `PAID` upstream, so every pass re-applied it — reverting the operator's rollback within
one interval, with a routine "1 activated" in the log. The ADR's own parenthetical ("a processed-event-id
ledger would generalise this") turned out to be load-bearing here in a way it was not for Stripe: a
webhook fires once, a poll fires forever. Fixed with migration `00035_billing_applied_orders` — an
`(order id, status)` ledger, so a genuine transition still applies and a repeat of the same state does
not. The same root cause made a ONETIME contribution grant Pro permanently, with `Frequency` decoded
and read by nothing; that is now an explicit refusal.

**B2 — the email fallback attributed by who clicked last, across every workspace.** `MatchByEmail` was
scoped to `(email, plan)` and ordered `created_at DESC` with nothing tying an intent to the payer, so
one person with a personal and a team workspace, clicking both and paying once, sent the money to
whichever they clicked last. Worse given registration performs no email verification. It now refuses an
ambiguous match — the file's own "when neither resolves, the answer is we do not know", applied to a
case it had been resolving by guessing. ⚠ Worth naming: this is the channel that carries everything if
the tag round-trip fails, so the "fallback" framing understated it.

### Self-review findings, same PR

Reviewing my own work after the round above — structurally the weakest kind of review, and it still
found two things.

**F2 — `TestReconcileIsIdempotent` stopped measuring idempotence.** Measured: pass 2 returns
`Ignored:1` and never reaches `applyActivated`, because the ledger short-circuits it. So its
three-pass loop asserts about ONE application. It does still go red under a broken-upsert mutant, but
via its incidental `CurrentPeriodEnd` assertion rather than the row count the test is named for —
the multi-pass structure is inert either way. `TestApplyIsIdempotentWithoutTheLedger` restores the
property directly, with the ledger absent, and asserts every pass genuinely reaches the apply so it
cannot quietly become a skip-test in turn.

**N1/N2 — the ledger recorded decisions that were not taken.** `applyActivated` now reports whether
it ACTED, and `applyCanceled` reports WHICH workspace it downgraded. A verified event the
stale-re-delivery guard declines, and a cancellation for an order belonging to somebody else's
integration, are both successes that changed nothing — neither is written to the ledger now, so no
false entry and no empty `team_id`. The webhook path discards both values, which is why it still
returns 200 on a delivery it deliberately ignored.

**Third-pass finding — a production guard had been weakened to fit its fixtures.** The recurrence
check read `o.Frequency != "" && …`, admitting an order whose recurrence the provider never stated.
That was not a decision: nine test fixtures omitted the field, and the guard had been written around
them. `Order.frequency` is NULLABLE in the published schema, so an absent value is a state the API
can really produce — and admitting it defeated the guard in exactly the case it exists for. Measured
against real staging data the same day: **38 of 40 contributions were `ONETIME`**, so the case the
guard rejects is the COMMON one and almost every fixture was implicitly recurring. The escape is
gone, the fixtures now carry realistic frequencies, and `TestUnknownFrequencyIsNotTreatedAsRecurring`
holds it.

**N3** — the migration comment now states the three behaviours an operator cannot guess from the
schema: rows are written only when something happened, the lookup fails OPEN, and deleting a row is
the supported way to force a re-apply.

**A1** — the `recover` sat at the function boundary, so a panic ended the LOOP, not the pass: the process
survived and activation was dead until redeploy. Moved inside the pass. **A2** — orders are now sorted
oldest-first so the newest state applies last, rather than the outcome depending on API page order.
**A3** — `intentTag`'s comment called it "one-way" inside a paragraph about authorization; it is a hash,
not a MAC, and the comment now says so and says why that is acceptable here.

**Deviation from the ADR's step 2, taken on the ADR's own advice:** `ERROR` is mapped to
`eventIgnored`, not `eventCanceled`. The ADR's Risks section flagged this exact call and said the
failure mode of ignoring (a workspace keeps Pro slightly too long) is strictly safer than the failure
mode of cancelling (a paying customer downgraded mid-retry, which is much harder to notice). Recorded
here because the ADR's Decision text still lists `REJECTED` and `ERROR` together.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above |
| 2 — something selects it | Nothing until T5 constructs and drives it. This task must not be read as "activation works" |
| 3 — the caller can discover it | `n/a: no declared interface` — package-internal |
| 4 — it is used | `ReconcileReport` counts, logged by T5 |

## Mutation Log

- 2026-08-28 · 366dd22* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Accepts a tag without requiring a recorded CheckoutIntent to corroborate it. The tag rides in a user-controlled URL, so this lets anyone credit a payment to any workspace. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · 366dd22* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Makes a status OpenCollective adds after this was written downgrade every workspace holding such an order — the silent mass-downgrade the default exists to prevent. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · 366dd22* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Stops requiring the contribution to name one of our sellable tiers, so a 5 EUR one-off donation would activate a 50 EUR/month plan. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · eec2269* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Accepts a tag without requiring a recorded CheckoutIntent to corroborate it, letting anyone credit a payment to any workspace. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · eec2269* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Makes a status OpenCollective adds later downgrade every workspace holding such an order. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · 66ebe2c* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Removes the applied-order ledger check, restoring the reviewed defect: a still-PAID order re-applies every pass, silently reverting an operators set-plan downgrade within one interval. · acceptance-sha256:fe873043ebf0b97330de17ca68cada502005fc9a2ec560378b72f5259496fe9d
- 2026-08-28 · 66ebe2c* · mutant killed · exit 1 · `internal/billing/intent.go` · Lets MatchByEmail resolve when several workspaces share the address, restoring the reviewed defect: the payment lands on whichever workspace clicked Upgrade last. · acceptance-sha256:fe873043ebf0b97330de17ca68cada502005fc9a2ec560378b72f5259496fe9d
- 2026-08-28 · 66ebe2c* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Drops the recurrence check so a ONETIME contribution to a monthly tier grants Pro — which upstream never transitions away from, so it would be permanent. · acceptance-sha256:fe873043ebf0b97330de17ca68cada502005fc9a2ec560378b72f5259496fe9d
- 2026-08-28 · 7d1ac1e* · mutant killed · exit 1 · `internal/billing/repo.go` · Breaks the subscription upserts convergence. This is the property TestReconcileIsIdempotent was named for and stopped measuring once the ledger short-circuited its later passes; TestApplyIsIdempotentWithoutTheLedger holds it directly. · acceptance-sha256:9c3f775c0d86d8b369ef1ea454c3d315a291a3e60b3435361bb395cee408844c
- 2026-08-28 · 7d1ac1e* · mutant survived · exit 0 · `internal/billing/reconcile.go` · Marks an activation the stale-re-delivery guard declined as applied, putting a false entry in the ledger — a record of a decision the server refused to take. · acceptance-sha256:9c3f775c0d86d8b369ef1ea454c3d315a291a3e60b3435361bb395cee408844c
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 7d1ac1e* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Marks an activation the stale-re-delivery guard declined as applied, putting a false entry in the ledger. Previously SURVIVED: the behaviour was fixed and nothing tested it. · acceptance-sha256:82bc092f9dab4720da8023160c4f3fa1935f38bbf55ca9c027307e57ccd222e4
- 2026-08-28 · 7d1ac1e* · mutant killed · exit 1 · `internal/billing/repo.go` · Breaks the subscription upserts convergence — the property TestReconcileIsIdempotent was named for and stopped measuring once the ledger short-circuited its later passes. · acceptance-sha256:82bc092f9dab4720da8023160c4f3fa1935f38bbf55ca9c027307e57ccd222e4
- 2026-08-28 · b1e94c3* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Restores the empty-frequency escape: an order whose recurrence the provider never stated activates a recurring plan. Order.frequency is nullable, so this is a real API state, and admitting it defeats the guard in the one case it exists for. · acceptance-sha256:d63f852f3f6f801e2994afb220eff0bff0121307c17dd40a9dd1906dbbd56fbe
- 2026-08-28 · b1e94c3* · mutant killed · exit 1 · `internal/billing/repo.go` · Breaks the subscription upserts convergence — the property the ledger short-circuited out of TestReconcileIsIdempotent. · acceptance-sha256:d63f852f3f6f801e2994afb220eff0bff0121307c17dd40a9dd1906dbbd56fbe

## Invariants

- The plan flip happens ONLY through `applyActivated` / `applyCanceled`. No second write path.
- A tag alone never activates; an intent row must corroborate it.
- An unknown or new order status never downgrades anyone.
- Reconciliation is idempotent: it runs every interval forever and must converge.

## Risks

- The status mapping is a judgement about someone else's state machine. `ERROR` → `eventCanceled` is
  the one to challenge: it is mapped as cancel because an errored order is not a paid one, but if it
  turns out to be a transient state a workspace would be downgraded mid-retry. Named here so the
  reviewer sees it; if unsure, move `ERROR` to `eventIgnored` — the failure mode of ignoring is a
  workspace that keeps Pro slightly too long, which is strictly safer than one that loses it wrongly.
- Email matching is weak if a contributor pays under a different email. It is the fallback, not the
  primary, and its failure lands in the unattributed bucket rather than on a wrong workspace.

## Stop Condition

If the real contribution shows `tags` absent AND `fromAccount` email unreadable with the token's
permission, stop: both attribution channels are gone and the ADR's Decision needs revisiting before
more code is written.

## Out of Scope

- The periodic driver and config — T5.
- Distinguishing `DISPUTED` / `IN_REVIEW` from `eventIgnored` (deferred: Follow-ups, ADR-042).

## Verification Log
- 2026-08-28 · 366dd22* · exit 0 · `go test ./internal/billing/ -run 'TestReconcile|TestEmailFallbackRefusesAnAmbiguousMatch|TestApplyIsIdempotentWithoutTheLedger|TestLedgerRecordsOnlyDecisionsActuallyTaken|TestUnknownFrequencyIsNotTreatedAsRecurring' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \ …` · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · eec2269* · exit 0 · `go test ./internal/billing/ -run 'TestReconcile|TestEmailFallbackRefusesAnAmbiguousMatch|TestApplyIsIdempotentWithoutTheLedger|TestLedgerRecordsOnlyDecisionsActuallyTaken|TestUnknownFrequencyIsNotTreatedAsRecurring' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \ …` · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · 66ebe2c* · exit 0 · `go test ./internal/billing/ -run 'TestReconcile|TestEmailFallbackRefusesAnAmbiguousMatch|TestApplyIsIdempotentWithoutTheLedger|TestLedgerRecordsOnlyDecisionsActuallyTaken|TestUnknownFrequencyIsNotTreatedAsRecurring' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \ …` · acceptance-sha256:fe873043ebf0b97330de17ca68cada502005fc9a2ec560378b72f5259496fe9d
- 2026-08-28 · 7d1ac1e* · exit 0 · `go test ./internal/billing/ -run 'TestReconcile|TestEmailFallbackRefusesAnAmbiguousMatch|TestApplyIsIdempotentWithoutTheLedger|TestLedgerRecordsOnlyDecisionsActuallyTaken|TestUnknownFrequencyIsNotTreatedAsRecurring' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \ …` · acceptance-sha256:9c3f775c0d86d8b369ef1ea454c3d315a291a3e60b3435361bb395cee408844c
- 2026-08-28 · 7d1ac1e* · exit 0 · `go test ./internal/billing/ -run 'TestReconcile|TestEmailFallbackRefusesAnAmbiguousMatch|TestApplyIsIdempotentWithoutTheLedger|TestLedgerRecordsOnlyDecisionsActuallyTaken|TestUnknownFrequencyIsNotTreatedAsRecurring' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \ …` · acceptance-sha256:82bc092f9dab4720da8023160c4f3fa1935f38bbf55ca9c027307e57ccd222e4
- 2026-08-28 · b1e94c3* · exit 0 · `go test ./internal/billing/ -run 'TestReconcile|TestEmailFallbackRefusesAnAmbiguousMatch|TestApplyIsIdempotentWithoutTheLedger|TestLedgerRecordsOnlyDecisionsActuallyTaken|TestUnknownFrequencyIsNotTreatedAsRecurring' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \ …` · acceptance-sha256:d63f852f3f6f801e2994afb220eff0bff0121307c17dd40a9dd1906dbbd56fbe
