# Task ADR-042-T2: Make a contribution name the workspace that started it

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `IntentRepo` (Record/MatchByTag/MatchByEmail), `intentTag(teamID) string`, migration `00034_billing_checkout_intents`
**Consumes:** none
**Data dependency:** hermetic

## Goal

Record that a workspace asked to buy a plan, and carry that workspace's tag on the Open Collective
checkout URL, so a later contribution can be attributed to it instead of guessed at.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00034_billing_checkout_intents.sql` | add | New table. Version `00034` is the first free one — verified 2026-08-28 by unioning `db/migrations/` across `origin/main`, every remote branch and all open PR heads (highest allocated anywhere: `00033`). Goose owns the schema; `AutoMigrate` is never called. |
| `internal/billing/intent.go` | add | `IntentRepo` + `intentTag`. |
| `internal/billing/opencollective.go` | edit | `createCheckout` currently drops `TeamID`, `CustomerEmail` and `SuccessURL` (lines 50-56) and returns a bare URL. It must append `tags`, `email` and `redirect`. **This is the line that SELECTS the whole attribution mechanism** — without it every checkout is anonymous again. |
| `internal/billing/billing.go` | edit | `StartCheckout` records the intent before handing back the URL; `Service` gains the `intents` dependency. |
| `internal/billing/intent_test.go` | add | Repo round-trip + tag derivation tests. |
| `internal/billing/opencollective_test.go` | edit | URL-construction tests. |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestOpenCollectiveCheckoutCarriesAttribution` (the
   returned URL carries `tags`, `email` and `redirect`, and the tier path is untouched) and
   `TestCheckoutIntentRoundTrips` (a recorded intent is found by tag and by email). Confirm RED.
2. Write migration `00034` with both `-- +goose Up` and `-- +goose Down`: `id`, `team_id`,
   `plan_code`, `tag`, `email`, `created_at`, index on `tag` and on `email`.
3. Implement `intentTag(teamID)` as a short, stable, URL-safe token derived from the team id. It is
   an ATTRIBUTION HINT, never an authorization — document that in the doc comment, because the next
   reader's first instinct will be to treat a matching tag as proof of purchase.
4. Implement `IntentRepo.Record` / `MatchByTag` / `MatchByEmail`.
5. Change `createCheckout` to parse the configured tier URL and append the three query parameters,
   preserving any parameters the operator already put in the configured URL.
6. Have `StartCheckout` record the intent before returning; a record failure logs and does NOT block
   the checkout — a customer must never be stopped from paying because our bookkeeping failed.
7. Confirm GREEN and run the package suite.

## Acceptance

```bash
go test ./internal/billing/ -run 'TestOpenCollectiveCheckoutCarriesAttribution|TestOpenCollectiveCheckoutPreservesConfiguredQuery|TestIntentTagIsStableAndURLSafe|TestCheckoutIntentRoundTrips|TestUnknownIntentIsNotFoundRatherThanZero|TestStartCheckoutSurvivesIntentWriteFailure' -count=1 2>&1 | tee /tmp/adr042-t2-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t2-new.out && \
grep -q "^ok" /tmp/adr042-t2-new.out && \
go build ./... && go vet ./... && go test ./internal/billing/ ./internal/web/... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOpenCollectiveCheckoutCarriesAttribution` | `internal/billing/opencollective_test.go` | Returned URL keeps the configured tier page and gains `tags`, `email`, `redirect` | — |
| `TestOpenCollectiveCheckoutPreservesConfiguredQuery` | `internal/billing/opencollective_test.go` | A configured URL that already has a query keeps it — appending must not clobber | — |
| `TestIntentTagIsStableAndURLSafe` | `internal/billing/intent_test.go` | The tag is stable per workspace, collision-free across workspaces, needs no URL escaping, and does not leak the raw team id into a public record | — |
| `TestCheckoutIntentRoundTrips` | `internal/billing/intent_test.go` | A recorded intent is found by both attribution channels, tag and email | — |
| `TestUnknownIntentIsNotFoundRatherThanZero` | `internal/billing/intent_test.go` | A miss is `ErrRecordNotFound`, never a zero-valued intent whose empty TeamID would read as a match; an EMPTY tag or email matches nothing even when rows exist | — |
| `TestStartCheckoutSurvivesIntentWriteFailure` | `internal/billing/intent_test.go` | A failing intent store still yields a checkout URL — bookkeeping never blocks a payment | — |

**Pre-existing test amended, not deleted:** `TestOpenCollectiveStartCheckout_ReturnsStaticURL`
asserted byte equality with the configured URL, which this task deliberately changes. It now asserts
the tier page itself (scheme, host, path) is unchanged, which is the property that still matters —
the query is covered by the attribution test above.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestCheckoutIntentRoundTrips` |
| 2 — something selects it | `StartCheckout` records the intent and `createCheckout` appends the tag; the mutation that drops the `tags` parameter must turn `TestOpenCollectiveCheckoutCarriesAttribution` red |
| 3 — the caller can discover it | The tag is carried in the URL the user is redirected to — observable in the browser address bar and in `Order.tags` |
| 4 — it is used | T4 consumes it; until then nothing measures this |

## Mutation Log

- 2026-08-28 · 9ffc0d9* · mutant killed · exit 1 · `internal/billing/opencollective.go` · Drops the attribution tag from the checkout URL while keeping the call, so it still compiles. This is the pre-ADR-042 anonymous checkout: every buyer of a plan gets an identical link and no landed contribution can be attributed. · acceptance-sha256:12bb563b974697c0e0ba2b13bfc2281daa741d8fa4ee9b0158e8ff04c7ab62d8
- 2026-08-28 · 9ffc0d9* · mutant survived · exit 0 · `internal/billing/intent.go` · Disables the empty-tag guard so an untagged order would match any row holding the empty string, attributing a strangers payment to an arbitrary workspace. Rewritten rather than deleted so the function still compiles and the guard branch remains reachable. · acceptance-sha256:12bb563b974697c0e0ba2b13bfc2281daa741d8fa4ee9b0158e8ff04c7ab62d8
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 9ffc0d9* · mutant killed · exit 1 · `internal/billing/intent.go` · Disables the empty-tag guard. Previously SURVIVED because the test seeded no empty-tag row; the test now seeds one production really creates (CustomerEmail is optional), so the guard is load-bearing. · acceptance-sha256:12bb563b974697c0e0ba2b13bfc2281daa741d8fa4ee9b0158e8ff04c7ab62d8
- 2026-08-28 · 9ffc0d9* · mutant killed · exit 1 · `internal/billing/intent.go` · Disables the empty-email guard. This is the leg production can actually reach: CustomerEmail is optional, so StartCheckout records intents with email = "" and an order whose contributor email is unreadable would otherwise attribute to one of them. · acceptance-sha256:12bb563b974697c0e0ba2b13bfc2281daa741d8fa4ee9b0158e8ff04c7ab62d8

## Invariants

- A bookkeeping failure never blocks a payment.
- The tag is a hint, never an authorization: T4 must still require a matching intent row.
- The configured `OPENCOLLECTIVE_CHECKOUT_*` URL stays the source of the tier path; this task only
  appends parameters.
- Goose owns the schema. No `AutoMigrate`.

## Risks

- The hosted flow may ignore or strip `tags` — that is the ADR's named open risk, resolved by T4's
  data dependency, not here. This task is correct either way: it also records the intent and
  prefills the email, which is the fallback channel.
- **A mutation caught a real hole during execution, recorded because the fix is the lesson.** The
  first empty-key mutant SURVIVED: `TestUnknownIntentIsNotFoundRatherThanZero` seeded only a row with
  a non-empty tag, so removing the guard still returned not-found and the assertion proved nothing.
  The corpus could not exhibit the defect the test named. It now seeds a row with an empty tag AND an
  empty email — which production really creates, because `CustomerEmail` is optional on
  `CheckoutRequest` — and both guards are now killed by mutation. The email leg is the one that is
  live rather than defensive.
- `redirect` is validated by Open Collective's `isValidExternalRedirect`; if our URL is rejected the
  user simply stays on Open Collective, which is today's behaviour. No regression.

## Stop Condition

If `intentTag` cannot be made stable and URL-safe without exposing the raw team id, stop and get a
decision — leaking a workspace identifier into a public contribution record is a privacy call, not
an implementation one.

## Out of Scope

- Reading the tag back — T4.
- Any UI showing the intent log.

## Verification Log
- 2026-08-28 · 9ffc0d9* · exit 0 · `go test ./internal/billing/ -run 'TestOpenCollectiveCheckoutCarriesAttribution|TestOpenCollectiveCheckoutPreservesConfiguredQuery|TestIntentTagIsStableAndURLSafe|TestCheckoutIntentRoundTrips|TestUnknownIntentIsNotFoundRatherThanZero|TestStartCheckoutSurvivesIntentWriteFailure' -count=1 2>&1 | tee /tmp/adr042-t2-new.out && \ …` · acceptance-sha256:12bb563b974697c0e0ba2b13bfc2281daa741d8fa4ee9b0158e8ff04c7ab62d8
