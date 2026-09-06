# ADR-042: Ask the payment provider, do not wait to be told

**Status:** Accepted
**Date:** 2026-08-28
**Accepted:** 2026-08-28 by M — "make it fully automatic, by docs, change adr if needed because currently is completely broken so... nothing to lose?"
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** docs/adr/ADR-006-knobs-that-do-nothing.md, docs/ARCHITECTURE.md
**Governs:**
- type: path
  pattern: "internal/billing/**"
- type: path
  pattern: "cmd/server/plan.go"

<!-- Class: every code path that decides which plan a workspace is on, plus every path that
tells a user what their plan will do. Enumerated 2026-08-28 with
`grep -rn "SetTeamPlan" --include="*.go" internal/ cmd/ | grep -v _test.go` → 3 call sites
(internal/billing/billing.go:229, :258, cmd/server/plan.go:63) and
`grep -rn "\.Upsert(" internal/billing` → 2 writers (billing.go:232, :263).
internal/web/handlers.go and internal/web/views/project.templ are members of the second half of the
class and are edited by T5, but are NOT listed in Governs: they are owned by the web component and
this decision is not authoritative over the dashboard as a whole. -->

**Enforced-by:** None — no gate exists at authoring time, and naming one that does not resolve is the rot this header exists to prevent. T4 produces `TestOpenCollectiveActivationIsReachable`, which fails when nothing in the composition root selects the reconciler (AGENTS.md §Reachability, this repo's most-shipped defect class); this header is updated to name it when T4 lands.
**Invalidates:** none — checked. Grepped every Accepted ADR for `billing`, `Stripe`, `plan`, `subscription`, `webhook`; ADR-006 constrains this decision (a config field must be read in the mode that is running) but is not changed by it.
**Served-path change:** A workspace that pays on Open Collective is moved onto its Pro plan within one reconcile interval, instead of staying on Free until an operator notices an email and runs `set-plan` by hand.

## Context

Open Collective replaced Polar as the payment provider on 2026-08-17 (commit `0227bb1`). The
replacement is deliberate and its no-webhook design is documented in the README, `.env.example`, the
package comment and the boot log. **The provider integration is live in production and the upgrade
button works; there is not yet a paying customer.** Confirmed 2026-08-28 against
`api.opencollective.com/graphql/v2`: `account(slug:"ai-agents-memory").orders(filter: INCOMING)`
returns `totalCount: 0`.

What the swap left behind is a payment nothing can observe. Read at `main` `312ca4d`
(billing paths byte-identical to `origin/main` `0946d0d`; `go build`, `go vet`, `gofmt`,
`go test ./...` all clean, so none of this is a broken tree):

- The `subscriptions` table has exactly two writers — `applyActivated`
  (`internal/billing/billing.go:232`) and `applyCanceled` (`:263`) — and both are reachable only
  from `HandleWebhook` (`:189`).
- `openCollectiveProvider.parseWebhook` returns an error on every call
  (`internal/billing/opencollective.go:77-79`), so neither writer can ever run under this provider.
- `set-plan` writes only `teams.plan_id` (`cmd/server/plan.go:63`); `plan.go` does not import
  `billing`.

So **no Open Collective deployment can hold a `subscriptions` row**, and three consequences follow
that were not intended by the swap:

1. `canManage` is gated on the plan alone (`internal/web/handlers.go:231`), so an activated
   workspace renders a "Manage your plan" card whose handler can only ever return
   `ErrNoSubscription`. The author excluded the comped tier for exactly this reason at
   `handlers.go:221-225` and the same argument covers every Open Collective workspace.
2. `CurrentPeriodEnd` is declared (`internal/billing/repo.go:24`) and assigned nowhere, and there is
   no scheduled job in the server at all, so a cancelled contribution never downgrades anyone.
3. The checkout hand-off still promises Stripe to the user — "Secure checkout via Stripe … then land
   back here" (`internal/web/views/project.templ:280`, present in the generated
   `project_templ.go`, so it ships). Commit `c326817` ("de-Stripe the billing copy (QA)") touched
   only `internal/web/billing.go` and missed this line.

**The premise that made this design necessary is narrower than the code comment states.** The
comment at `opencollective.go:4-6` says Open Collective's webhooks are "an outgoing Slack/Discord-style
notification channel". Read 2026-08-28, Open Collective's own documentation says a collective may
send its webhook to any URL and that Slack/Discord are auto-detected special cases, not the whole
feature. What is genuinely missing is a **signature** — the documentation describes no signing or
authentication on delivery — which is what makes the "never flip a plan from this" conclusion right
even though the stated reason is wrong.

Measured 2026-08-28 against the live GraphQL API, the data needed to activate a plan is all
available and authenticated:

| Fact | Value observed |
|------|----------------|
| Endpoint / auth | `https://api.opencollective.com/graphql/v2`, header `Personal-Token: <token>` |
| Rate limit | 10 req/min unauthenticated, 100 req/min authenticated |
| Project | `ai-agents-memory`, id `acc_l4jGT5x9gtCEz5Ry6rFTK`, type `PROJECT` |
| Tiers | `pro-monthly` `legacyId 104934` €50/month, `pro-yearly` `legacyId 104935` €500/year — the same ids already embedded in `OPENCOLLECTIVE_CHECKOUT_*` |
| `Order` fields | `legacyId publicId status frequency amount tier fromAccount tags customData memo nextChargeDate lastChargedAt createdAt processedAt` |
| `OrderStatus` enum | `NEW REQUIRE_CLIENT_CONFIRMATION PAID ERROR PROCESSING REJECTED ACTIVE CANCELLED PENDING EXPIRED DISPUTED REFUNDED PAUSED IN_REVIEW` |
| Schema check falsifiable? | Yes — `thisFieldDoesNotExist` returns `GRAPHQL_VALIDATION_FAILED`, so the field list above is a real schema read and not a silently-ignored query |

And the contribution flow accepts URL parameters `amount`, `interval`, `contributeAs`, `email`,
`name`, `legalName`, `paymentMethod`, `tags` and `redirect` (read 2026-08-28 from
`opencollective-frontend/components/contribution-flow/index.js`), which is the attribution channel
the static URL currently lacks — `createCheckout` receives `TeamID`, `CustomerEmail` and
`SuccessURL` and uses only `in.PlanCode` (`opencollective.go:50-56`), so every buyer of a plan gets
a byte-identical link.

## Existing Primitives Audit

- **`checkoutAPI` / `webhookParser` / `portalAPI` seam** (`internal/billing/provider.go`) — REUSE
  unchanged. The seam is the right shape; this ADR adds a fourth, narrower seam beside it rather
  than widening any of the three.
- **`providerEvent` + `eventKind`** (`provider.go:43-71`) — REUSE as the normalized vocabulary. An
  Open Collective order maps onto exactly the existing three kinds; nothing new is needed.
- **`applyActivated` / `applyCanceled`** (`billing.go:208-264`) — REUSE. These already carry the
  idempotency, the stale-re-delivery guard and the empty-subscription-id guard that a reconciler
  needs. The reconciler must feed them, never re-implement the plan flip; a second plan-flip path
  is how the two would drift.
- **`Repo.Upsert`** (`repo.go:47`) — REUSE. Already an atomic `INSERT … ON CONFLICT(team_id)`,
  which is what a repeatedly-running reconciler requires.
- **`parseWebhook` failing closed** — KEEP. This ADR does not make the webhook trusted; it removes
  the need for one.
- **No HTTP client helper for JSON POST exists in `internal/billing` today** — the Polar
  `postJSON` helper was deleted with `polar.go` in `0227bb1`. T2 writes a small one; there is
  nothing to reuse.
- **No scheduler exists anywhere in the server** — verified 2026-08-28, zero `time.NewTicker` or
  cron outside a `doctor` doc comment. T4 introduces the first one, which is why its ownership and
  shutdown behaviour are called out rather than assumed.

## Decision

Learn about a payment by **asking Open Collective's authenticated GraphQL API**, on a schedule, and
feed what it returns through the plan-flip logic that already exists. Do not consume the webhook.

Concretely:

1. **Attribution at checkout.** `createCheckout` stops returning a bare static URL and appends
   `tags=<workspace tag>`, `email=<customer email>` and `redirect=<success URL>` to the configured
   tier URL. A `billing_checkout_intents` row records that workspace W asked to buy plan P at time
   T, so a contribution can be attributed by tag and corroborated by an intent that actually exists.
2. **A read-only order source.** A new one-method seam, `orderSource.listOrders`, implemented by a
   GraphQL client against `account(slug).orders(filter: INCOMING)`, authenticated with a personal
   token.
3. **Reconciliation, not events.** A reconciler maps each order to the existing `providerEvent` —
   `ACTIVE`/`PAID` → `eventActivated`, `CANCELLED`/`EXPIRED`/`REFUNDED`/`REJECTED` →
   `eventCanceled`, everything else including `ERROR` → `eventIgnored` — and calls the existing
   `applyActivated` / `applyCanceled`. `Order.legacyId` becomes the stable `subscriptionID`,
   `tier.legacyId` resolves the plan code, and `nextChargeDate` finally populates
   `CurrentPeriodEnd`. An UNKNOWN status — one Open Collective adds after this was written — is
   ignored and logged, never cancelled: a new state name must not be able to downgrade every paying
   workspace at once. (`ERROR` was moved from the cancel group to the ignore group during execution,
   on the reasoning in Risks below; recorded in T4.)
4. **A periodic driver**, off unless configured, gated on a personal token and a collective slug.
5. **A ledger of what was already applied.** Every order this server acts on is recorded as
   `(order id, status)`, and an order whose status has not changed since it was applied is skipped.
   This is what makes a POLL idempotent, as opposed to idempotent within one pass — and it is the
   part that was missing from the first version of this decision. A webhook fires once per event, so
   "apply what the event says" is safe. A poll sees the same order every interval forever, and
   without a record of what was already done the only remaining invariant is "the provider's state
   has not changed" — which is not idempotence at all, it is re-asserting a past decision over any
   local change made since. Concretely, it reverted an operator's `set-plan` downgrade within one
   interval (found in review; see Consequences). A row is written only when something actually
   happened, so an unattributable order is re-examined next pass rather than skipped forever.

**What would make this fail, and whether that data can exist.** The design rests on one claim that
is *not* yet verified: that a `tags` value placed on the contribution URL survives to
`Order.tags` on the created order. It could not be verified on 2026-08-28 because the project has
zero orders, and no amount of schema reading settles it — `tags` is confirmed present on both
`OrderCreateInput` and `Order`, which proves the field exists on the API path, not that the hosted
contribution flow forwards a URL parameter into it. **T4 therefore carries a data dependency on one
real contribution**, and its sign-off must record the order id it read the tag back from. If the tag
does not round-trip, this is not a redesign: T4 specifies email-to-intent matching as the fallback
in the same breath, and an order matching neither is left unattributed and reported rather than
guessed. The criterion is valid for the `opencollective.com` hosted flow as it behaves on the
sign-off date, and for no other checkout.

## Alternatives Considered

- **Consume the Open Collective webhook.** It exists and can point at an arbitrary URL, so this is
  the obvious design and it is what the current code's comment implies is impossible. Rejected on
  two independent grounds, both checked: the documentation describes **no signature or
  authentication** on delivery, so accepting one means flipping a paid plan on an unauthenticated
  POST anyone can forge; and delivery is not dependable — `opencollective/opencollective#7892`
  ("Webhooks: New financial contribution event not triggered", opened 2025-03-12, since closed)
  reports contributions not firing webhooks at all. A payment channel whose failure mode is silence
  cannot be the only thing that grants a plan.
- **Webhook as a doorbell, API as the source of truth.** Take the unsigned webhook as an untrusted
  "something happened" hint and immediately verify by querying the API. This is a real design and it
  is strictly better on latency. Rejected **for now** as pure addition: it needs every part of the
  polling path anyway, and it buys latency on a €50/month product where minutes do not matter. It
  is recorded as a follow-up rather than an alternative that lost on merit — if activation latency
  ever becomes a complaint, this is the answer, and nothing here forecloses it.
- **Keep manual `set-plan`, fix only the copy and the Manage button.** Cheapest, and it is what the
  current record implies is the intended steady state. Rejected because the manual path is not
  merely slow, it is *unattributable*: the operator receives "someone contributed €50" and every
  buyer's checkout URL is identical, so there is no reliable way to know which workspace to credit.
  Automating the attribution is most of this ADR's work, and once a workspace can be attributed
  there is no reason to leave the flip manual.
- **Poll the public REST endpoint** (`opencollective.com/<slug>/members.json` and similar).
  Rejected: it exposes contributors rather than orders, carries no order status, and is
  unauthenticated at 10 req/min. It cannot express a cancellation.
- **Ask contributors to email a receipt.** Rejected: it makes the customer do the reconciliation,
  and it is not checkable.

## Component / Boundary Impact

| Component | Ownership after change | One reason to change? |
|-----------|------------------------|-----------------------|
| `internal/billing` | Unchanged owner: the payments bounded context. Gains a read seam (`orderSource`) and a reconciler beside the existing write seams. | Yes — it changes when the payments contract changes. |
| `cmd/server` (composition root) | Unchanged. Gains the reconciler's construction, its config, and the goroutine that drives it. | Yes — it changes when wiring changes. |
| `internal/web` | Unchanged owner: the dashboard. T5 edits one gate expression and one copy string; no new responsibility. | Yes. |
| `internal/tenant` | Untouched. `teams.plan_id` remains the effective-plan source of truth and is still written only through `PlanStore.SetTeamPlan`. | Yes. |

No new bounded context, no module moves, so `docs/ARCHITECTURE.md`'s Module Map is inherited
unchanged — delta: none.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `OPENCOLLECTIVE_PERSONAL_TOKEN` (env) | new — enables reconciliation; unset keeps it off | operator | `billingConfig()` → `Config.OpenCollectivePersonalToken` |
| `OPENCOLLECTIVE_COLLECTIVE_SLUG` (env) | new — which account's orders to read (e.g. `ai-agents-memory`) | operator | `billingConfig()` → `Config.OpenCollectiveSlug` |
| `OPENCOLLECTIVE_RECONCILE_INTERVAL` (env) | new — poll period, default `15m` | operator | `billingConfig()` → reconciler driver |
| `OPENCOLLECTIVE_API_URL` (env) | new — override for tests/staging, default `https://api.opencollective.com/graphql/v2` | operator | `ocOrderSource` |
| `billing_checkout_intents` (table) | new — migration `00034` | T1 | reconciler (T3), attribution fallback |
| `billing_applied_orders` (table) | new — migration `00035`; the ledger that keeps a poll idempotent | T4 | reconciler (T4) |
| `subscriptions` rows under OpenCollective | behaviour change — rows now exist where none could before | reconciler (T3) | `ManageURL`, `canManage` (T5) |
| Contribution checkout URL | now carries `tags`, `email`, `redirect` query parameters | `createCheckout` (T1) | Open Collective hosted flow |
| `Config.Provider == "opencollective"` | unchanged | operator | unchanged |

The `checkoutAPI`, `webhookParser` and `portalAPI` interfaces are **unchanged**; `parseWebhook`
keeps failing closed.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `IntentRepo` + `intentTag(teamID)` (T2) | T2 | T4 | No — new symbols, no existing caller |
| `orderSource` + `providerOrder` + `newOCOrderSource()` (T3) | T3 | T4, T5 | No — new seam beside the existing three |
| `Reconciler.ReconcileOnce(ctx)` (T4) | T4 | T5 | No — new symbol |

T1 is deliberately outside this graph: it corrects a live user-facing defect and is correct whether
or not any other task lands, so it is ordered first to put an observable fix in front of a human
early. The config fields in the Wiring table are added and read entirely within T5, the composition
root, so they are not an inter-task contract — T3 and T4 take their dependencies as constructor
arguments rather than reading `Config`, which is what keeps that edge pointing forwards.

## Implementation

See `docs/adr/ADR-042-ask-the-provider-do-not-wait-to-be-told/tasks/README.md`. Five tasks,
sequential.

## Consequences

- **Positive:** a paying customer is upgraded without anyone watching an inbox, and the upgrade is
  attributable to a specific workspace rather than guessed.
- **Positive:** cancellation, refund and expiry finally have a path — `applyCanceled` becomes
  reachable under Open Collective, and `CurrentPeriodEnd` stops being a column nothing writes.
- **Positive:** the plan-flip logic stays in one place. The reconciler produces `providerEvent` and
  calls the same two functions the Stripe webhook does, so there is no second implementation to
  drift.
- **Negative:** the server acquires an outbound dependency on a third-party payment API. A reconcile
  failure must be logged and retried, never fatal.
  <!-- CORRECTED during execution: this bullet first read "its first background goroutine". False —
  cmd/server/main.go already starts embedworker and mergejob as background loops (main.go:315, :321).
  The authoring grep looked for time.NewTicker/cron and embedworker sleeps instead, so the grep was
  right about tickers and wrong as a proxy for "no background loops". Those two are the precedent
  this loop's shape follows rather than a novelty it introduces. -->
- **Negative:** a bounded but real risk that the poll interval delays activation; see below.
- **Negative:** activation is now eventually-consistent with a worst case of one interval (default
  15 minutes), where a signed webhook would be seconds. Accepted: see Alternatives.
- **Negative:** a personal token becomes a production secret with read access to the collective's
  financial data. It is read-only and scoped to one account, but it is a new secret to hold.
- **Neutral:** the manual `set-plan` path is untouched and remains the operator override, including
  for a contribution that arrives with no usable attribution.
- **Corrected in review, and the correction is the most useful thing in this record.** The first
  version of this decision had reconciliation feed the existing `applyActivated`/`applyCanceled`
  and treated their webhook-era idempotence as sufficient. It was not: `set-plan` writes only
  `teams.plan_id`, so the subscription row still read `active`, the guard did not fire, the order was
  still `PAID` upstream, and every pass put the workspace back on Pro with a routine "1 activated" in
  the log — the operator's documented rollback and the reconciler fought, and the reconciler won
  fifteen minutes later. The same root cause let a `ONETIME` contribution grant a recurring plan
  permanently, with `Frequency` decoded and read by nothing. Both are closed by decision point 5 and
  an explicit recurrence check. ★The general lesson, worth more than the fix: **when a pull replaces
  a push, ask what made the old path safe. If the answer is "it only ran once", that property is gone
  and nothing tells you.**

## Out of Scope

- Trusting the Open Collective webhook to grant a plan (permanent: its documentation describes no signature on delivery, read 2026-08-28 at `docs.opencollective.com/help/collectives/collective-settings/integrations.md`, so an accepted delivery is an unauthenticated POST — a boundary this ADR chooses, not a claim a signature can never exist)
- Any change to the Stripe provider or its webhook route (permanent: `BILLING_PROVIDER` selects one provider per deployment and this ADR is about the Open Collective one)
- Using the webhook as a low-latency doorbell in front of the API (deferred: Follow-ups, this ADR)
- Handling `DISPUTED` and `IN_REVIEW` as states distinct from `eventIgnored`, which is how T4 maps them (deferred: Follow-ups, this ADR)
- Multi-currency or non-EUR tiers (permanent: both tiers are EUR, verified 2026-08-28 against the live API, and a second currency is a catalog decision rather than a reconciliation one)
- A dashboard surface for reviewing unattributed contributions (deferred: Follow-ups, this ADR)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| `tags` does not survive the hosted checkout into `Order.tags` | Med | High — the primary attribution channel | T4 carries the data dependency on one real contribution and must record the order id it read back. Email-to-intent matching is specified in T4 as the fallback and needs no redesign. |
| A contributor pays without using our button, so there is no tag and no intent | High | Low | T4 leaves such an order unattributed, logs it once with its order id, and takes no action. The operator uses `set-plan`. Never guess. |
| A forged `tags` value credits the wrong workspace | Low | Med | The tag alone never activates: T4 requires a matching `billing_checkout_intents` row for that workspace and plan. A payer can still only harm themselves by paying for someone else. |
| Personal token leaks | Low | High | Read-only scope, one account, held only in `.env.prod` like the session key. Never logged — T3's error paths must not echo the header. |
| Open Collective API shape changes under us | Low | Med | T3 keeps the query in one place and unit-tests the decoder against recorded fixtures; a shape change fails loudly at decode rather than silently returning zero orders. |
| A reconcile bug repeatedly flips plans | Low | High | The reconciler only ever calls the existing idempotent `applyActivated`/`applyCanceled`; `Repo.Upsert` is `ON CONFLICT(team_id) DO UPDATE`, and the existing canceled-subscription guard (`billing.go:218-223`) already refuses a late re-activation. |
| Zero orders reads the same as a broken query | Med | Med | T2 distinguishes a successful empty page from an error, and T4 logs the reconcile outcome with a count, so "0 orders" and "the call failed" are never the same log line. This is the repo's own empty-result-is-not-an-answer rule. |

## Rollback

Required — this adds persistent state, an external integration and a new secret.

1. Unset `OPENCOLLECTIVE_PERSONAL_TOKEN`. The reconciler is not constructed, the goroutine does not
   start, and activation returns to manual `set-plan`. No code change, no redeploy beyond the env.
2. The two tables this decision adds each have a goose `-- +goose Down`: `00034` drops
   `billing_checkout_intents` and `00035` drops `billing_applied_orders`. Nothing else reads either.
   ⚠ Dropping `00035` alone, while reconciliation is still running, reverts to the behaviour that
   made it necessary: every pass re-applies the provider's current state, over any manual change an
   operator has made since. Stop the reconciler (step 1) before dropping it, or drop both.
3. Rows the reconciler already wrote into `subscriptions` are correct records of real payments and
   are deliberately **not** rolled back. `teams.plan_id` values it set are reversible with
   `set-plan --slug <s> --plan personal`.
4. T1's checkout URL parameters are additive query strings; reverting T1 restores the bare static
   URL and the hosted flow ignores the absence.

## Follow-ups

- [ ] Use the unsigned webhook as a doorbell that triggers an immediate reconcile, keeping the API
      as the source of truth (from Out of Scope).
- [ ] Decide whether `DISPUTED` / `IN_REVIEW` should suspend a plan rather than be ignored (from
      Out of Scope).
- [ ] An operator view listing contributions the reconciler could not attribute (from Out of Scope).
- [ ] Re-read the webhook signature question if Open Collective ships signed deliveries; that would
      supersede this record's central premise.
- [ ] **Run the test suite under `-race` in CI.** Nothing in this repository does: every workflow
      runs a bare `go test ./...`, no other ADR fence asks for it, and there is no Makefile target.
      The suite is race-clean today — verified with `go test -race ./...` on 2026-08-28, exit 0 — so
      this is a gap in ENFORCEMENT rather than a defect, and it went unnoticed until this ADR added
      the first goroutine anyone had reason to ask about. T5's fence now runs `-race` over the loop
      tests it owns; making it repo-wide is a policy change and belongs to its own record rather
      than to a payments PR.
