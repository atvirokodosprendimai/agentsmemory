# Task ADR-042-T5: Wire the reconciler so something actually selects it

**Depends-on:** T4
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — composition root, config, docs)
**Owner:** unassigned
**Produces:** `TestOpenCollectiveActivationIsReachable` (the reachability gate named in ADR-042's `Enforced-by`)
**Consumes:** `Reconciler.ReconcileOnce(ctx)` (T4), `newOCOrderSource()` (T3)
**Data dependency:** hermetic

## Goal

Construct the reconciler from configuration and drive it on a schedule, so a paid contribution
actually reaches the plan flip — and add the gate that fails when that wiring is deleted.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/main.go` | edit | `billingConfig()` reads the four new env vars; the composition root constructs the reconciler and starts its loop. **This is the SELECTION line the whole ADR depends on** — every prior task is unreachable without it. |
| `internal/billing/billing.go` | edit | `Config` gains `OpenCollectivePersonalToken`, `OpenCollectiveSlug`, `OpenCollectiveAPIURL`, `ReconcileInterval`; `NewService` exposes a constructor for the reconciler. |
| `.env.example` | edit | Document all four. `TestReadEnvVarsAreDocumented` fails otherwise. |
| `.env.docker.example` | edit | Same, for the compose stack. |
| `docker-compose.prod.yml` | edit | Pass the new variables through. `TestDocumentedEnvVarsAreRead` binds this in the other direction. |
| `README.md` | edit | The hosted-billing block must stop saying activation is `set-plan` once it is not. |
| `cmd/server/ocreconcile.go` | add | The wiring: preconditions, construction, and the goroutine. Kept beside main.go so the composition root stays one line. |
| `cmd/server/ocreconcile_test.go` | add | The reachability gate and the config tests. |
| `internal/billing/reconcile.go` | edit | `Run` — the loop, its recover, and its per-pass log line. |
| `internal/billing/ocorders.go` | edit | Exports `NewOCOrderSource` plus the two defaults the composition root needs. |

## Ordered Steps

1. Write the failing test first (TDD red): `TestOpenCollectiveActivationIsReachable`, which parses
   `cmd/server/main.go` and fails when no path constructs the reconciler and starts it. Derive its
   universe from the source (like `TestEveryKnobIsSweptOrNamed` does) rather than hardcoding a
   symbol list, so a rename joins the check on the same commit. Confirm RED.
2. Add the `Config` fields and read them in `billingConfig()`. Every one must be BOTH assigned from
   the environment AND read by something, or `TestEveryConfigFieldIsPopulatedAndRead` fails — ADR-006
   binds here, and its stronger question applies: each must be read in the mode that is running, so
   they are read on the OpenCollective branch of the provider switch, not unconditionally.
3. Default `OpenCollectiveAPIURL` to `https://api.opencollective.com/graphql/v2` and
   `ReconcileInterval` to `15m`. One poll per interval is far under the measured 100 req/min
   authenticated limit.
4. Construct the reconciler ONLY when provider is `opencollective` AND the token and slug are set.
   Otherwise log which one is missing and leave activation manual — the existing boot-log block
   already has this shape; extend it rather than adding a second one.
5. Start the loop with the server's lifecycle context so shutdown stops it. Log every pass with the
   `ReconcileReport` counts, so "0 orders" and "the call failed" are never the same line.
6. A reconcile error logs and retries next interval. It is NEVER fatal — a payment provider being
   down must not take the server with it.
7. Update `.env.example`, `.env.docker.example`, `docker-compose.prod.yml` and the README block in
   the SAME commit; documentation is load-bearing here and gated in both directions.
8. Confirm GREEN, then run the repo's full gate.

## Acceptance

```bash
{ go test ./cmd/server/ -run 'TestOpenCollectiveActivationIsReachable|TestBillingConfigReadsOpenCollectiveReconcileVars|TestBillingConfigDefaultsTheReconcileKnobs' -count=1 && go test -race ./internal/billing/ -run 'TestReconcileLoopStopsOnContextCancel|TestEndToEndOpenCollectiveActivation' -count=1 ; } 2>&1 | tee /tmp/adr042-t5-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t5-new.out && \
grep -q "^ok" /tmp/adr042-t5-new.out && \
go test ./cmd/server/ -run 'TestEveryConfigFieldIsPopulatedAndRead|TestEveryFlagIsRead|TestDocumentedEnvVarsAreRead|TestReadEnvVarsAreDocumented|TestNotOperatorFacingIsJustified' -count=1 && \
gofmt -l . | grep -v '_templ.go' | tee /tmp/adr042-t5-fmt.out && [ ! -s /tmp/adr042-t5-fmt.out ] && \
go build ./... && go vet ./... && go test ./... -count=1
```

The middle command is the repo's existing reachability and documentation family, run explicitly
because this task is exactly the kind of change they exist to catch.

⚠ **`-race` on the loop tests, and only here, because this task introduces the goroutine.** Nothing
in this repository runs the race detector — not CI (`.github/workflows/*.yml` all run a bare
`go test ./...`), not any other ADR fence, not a Makefile. The whole suite is race-clean today,
verified with `go test -race ./...` on 2026-08-28, so this is a gap in ENFORCEMENT rather than a
defect. Binding it to this task's fence covers the goroutine this task adds; making it repo-wide is
a policy change that belongs to its own record, not to a payments PR.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOpenCollectiveActivationIsReachable` | `cmd/server/ocreconcile_test.go` | Deleting the reconciler construction, its loop start, or the composition root's call to the wiring each turns this red | — |
| `TestBillingConfigReadsOpenCollectiveReconcileVars` | `cmd/server/ocreconcile_test.go` | All four env vars land on `Config` | — |
| `TestBillingConfigDefaultsTheReconcileKnobs` | `cmd/server/ocreconcile_test.go` | An unset API URL and interval default rather than yielding an empty URL and a zero period that would spin the loop | — |
| `TestReconcileLoopStopsOnContextCancel` | `internal/billing/reconcile_test.go` | The goroutine exits on shutdown rather than leaking a poller past the process's intent to run | — |
| `TestEndToEndOpenCollectiveActivation` | `internal/billing/e2e_opencollective_test.go` | The WHOLE chain against a fake Open Collective: upgrade click → attributed checkout URL → the contribution appearing in the API with that tag echoed back → one reconcile pass → the workspace on Pro, with the order id and period end recorded and `HasRelationship` true. Nothing is stubbed except the provider | — |

**Why the end-to-end test is here and not in T4.** Each of T2–T4 proves its own unit and none of them
proves a payment activates anything; that is exactly the gap this ADR exists to close, so the test
that walks the whole chain belongs to the task that makes the chain reachable. It is also the only
test that would catch the seams being individually correct and jointly wrong — a tag written in one
format and matched in another, say, which every unit test would happily pass.

Existing gates that must stay green and are load-bearing here:
`TestEveryConfigFieldIsPopulatedAndRead`, `TestDocumentedEnvVarsAreRead`,
`TestReadEnvVarsAreDocumented`.

**The reachability gate had to be rewritten mid-task, and the reason is the finding.** Its first
draft asked "is any `.Run(` called in this package?" — which passed while the reconciler was
constructed and never driven, because `cmd/server` already contains four unrelated `Run` calls
(`rootCommand().Run`, `mcpcli.Run`, and the `embedworker` and `mergejob` background loops). The
mutant that removes `go rec.Run(...)` SURVIVED against it. The gate now derives the reconciler's
variable name from its assignment and requires `Run` on THAT identifier, and the mutant is killed.
A gate can be green because it is looking at the wrong thing.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestBillingConfigReadsOpenCollectiveReconcileVars` |
| 2 — something selects it | `TestOpenCollectiveActivationIsReachable` — the mutant deletes the construction line and the gate must go red. This is the rung the whole ADR turns on |
| 3 — the caller can discover it | `.env.example` + README + compose, bound by `TestDocumentedEnvVarsAreRead` and `TestReadEnvVarsAreDocumented` in both directions |
| 4 — it is used | The per-pass log line carries the `ReconcileReport` counts; that is the first thing that measures whether activation ever happens |

## Mutation Log

- 2026-08-28 · 04543c8* · mutant killed · exit 1 · `cmd/server/main.go` · Removes the single call that makes the whole decision reachable. Every component below it — intent store, order source, reconciler — keeps its own tests green while no payment activates anything. This is this repo signature defect and the reason the gate exists. · acceptance-sha256:2afd7df0a0f4cb3d9b5e55c54ccd038753798339946845fa6d4aaf17a722e870
- 2026-08-28 · 04543c8* · mutant survived · exit 0 · `cmd/server/ocreconcile.go` · Constructs the reconciler and never drives it. This is the subtler half of the reachability defect: the component is built, the log line still says reconciliation is ON, and nothing ever polls. · acceptance-sha256:2afd7df0a0f4cb3d9b5e55c54ccd038753798339946845fa6d4aaf17a722e870
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 04543c8* · mutant killed · exit 1 · `cmd/server/ocreconcile.go` · Constructs the reconciler and never drives it — the subtler half of the reachability defect: the component is built, the boot log still says reconciliation is ON, and nothing polls. Previously SURVIVED because the gate matched ANY .Run( and this package already has four unrelated ones. · acceptance-sha256:2afd7df0a0f4cb3d9b5e55c54ccd038753798339946845fa6d4aaf17a722e870
- 2026-08-28 · 04543c8* · mutant killed · exit 1 · `cmd/server/main.go` · Removes the single call that makes the whole decision reachable: every component below it keeps its tests green while no payment activates anything. · acceptance-sha256:d62824b69bdc3b15b92c8c4eac787e38df7d0615c8d06a6e35c6d8e2ce659e9b
- 2026-08-28 · 04543c8* · mutant killed · exit 1 · `cmd/server/ocreconcile.go` · Constructs the reconciler and never drives it. Previously SURVIVED because the gate matched ANY .Run( and this package has four unrelated ones; the gate now binds Run to the reconciler variable. · acceptance-sha256:d62824b69bdc3b15b92c8c4eac787e38df7d0615c8d06a6e35c6d8e2ce659e9b
- 2026-08-28 · 04543c8* · mutant killed · exit 1 · `cmd/server/main.go` · Removes the single call that makes the whole decision reachable: every component below it keeps its tests green while no payment activates anything. · acceptance-sha256:6c2c5eb0de2031e39f2f323705e7016c85da5f5830e5b4e5920f16988c0e7757
- 2026-08-28 · 04543c8* · mutant killed · exit 1 · `cmd/server/ocreconcile.go` · Constructs the reconciler and never drives it. Previously SURVIVED because the gate matched ANY .Run( and this package has four unrelated ones; the gate now binds Run to the reconciler variable. · acceptance-sha256:6c2c5eb0de2031e39f2f323705e7016c85da5f5830e5b4e5920f16988c0e7757
- 2026-08-28 · 3a40b20* · mutant killed · exit 1 · `cmd/server/main.go` · Removes the single call that makes the whole decision reachable: every component below it keeps its tests green while no payment activates anything. · acceptance-sha256:ea592223a4cbebd775faa1600ce4d7f2a8ec2570ce3e33a6808eddff9b29ef01
- 2026-08-28 · 3a40b20* · mutant killed · exit 1 · `cmd/server/ocreconcile.go` · Constructs the reconciler and never drives it — the subtler half of the reachability defect. · acceptance-sha256:ea592223a4cbebd775faa1600ce4d7f2a8ec2570ce3e33a6808eddff9b29ef01
- 2026-08-28 · 3a40b20* · mutant killed · exit 1 · `internal/billing/opencollective.go` · Writes a tag the reconciler cannot match back, simulating the two seams being individually correct and jointly wrong. Only the end-to-end test can see this: every unit test still passes because each side is internally consistent. · acceptance-sha256:ea592223a4cbebd775faa1600ce4d7f2a8ec2570ce3e33a6808eddff9b29ef01
- 2026-08-28 · eec2269* · mutant killed · exit 1 · `cmd/server/main.go` · Removes the single call that makes the whole decision reachable. · acceptance-sha256:ea592223a4cbebd775faa1600ce4d7f2a8ec2570ce3e33a6808eddff9b29ef01
- 2026-08-28 · eec2269* · mutant killed · exit 1 · `cmd/server/ocreconcile.go` · Typos the tier slug in the composition roots plan map — the one place this code must agree with the operators configured checkout URL. Every unit test still passes because they use their own map; only the end-to-end run notices that a real contribution stops matching a plan. · acceptance-sha256:ea592223a4cbebd775faa1600ce4d7f2a8ec2570ce3e33a6808eddff9b29ef01
- 2026-08-28 · b1e94c3* · mutant killed · exit 1 · `cmd/server/main.go` · Removes the single call that makes the whole decision reachable. · acceptance-sha256:64110764557c688ca38d803b0229c33c82f7cffc8220e61d59a499ac3c0707a5
- 2026-08-28 · b1e94c3* · mutant killed · exit 1 · `cmd/server/ocreconcile.go` · Constructs the reconciler and never drives it. · acceptance-sha256:64110764557c688ca38d803b0229c33c82f7cffc8220e61d59a499ac3c0707a5

## Invariants

- With no token configured, behaviour is exactly today's: no goroutine, no outbound call, manual
  `set-plan`.
- A reconcile failure never kills the server.
- The loop honours shutdown.
- Every new config field is both populated and read, on the branch that runs.

## Risks

- A panicking loop must not take the process down — recovered at the loop boundary and logged.
  (This is NOT the server's first background goroutine, as this task first claimed: `embedworker`
  and `mergejob` already run as loops from `main.go:315` and `:321`, and their shape is the
  precedent this one follows. The authoring grep looked for `time.NewTicker`/cron and embedworker
  sleeps instead — the grep was right about tickers and wrong as a proxy for "no background loops".)
- The README currently tells operators activation is manual. Leaving that true after this lands
  would be the same defect T1 fixes on the dashboard, one document over.

## Stop Condition

If `TestOpenCollectiveActivationIsReachable` cannot be written so that deleting the wiring makes it
fail — for example because the construction is indirect enough that a source parse cannot see it —
stop. A gate that cannot fail is worse than none, and the shape of the wiring should change rather
than the gate being weakened.

## Out of Scope

- Webhook-as-doorbell for lower latency (deferred: Follow-ups, ADR-042).
- An operator UI for unattributed orders (deferred: Follow-ups, ADR-042).

## Verification Log
<!-- One tool-written entry was removed here by hand, which this log otherwise forbids, so the reason
     is recorded rather than left to be discovered. adr-verify's truncation kept the first TWO lines
     of a multi-line fence before its ` …` marker, so the entry contained a newline and no longer
     matched the one-line grammar adr-lint enforces — a malformed row that blocked the lint
     permanently. It was a stale PASS for a fence that no longer exists (digest 2afd7df0…, superseded
     twice since), never a verdict being hidden: no failing run and no mutant was touched, and the
     fence has been restructured so its recorded command stays on one line. -->

- 2026-08-28 · 04543c8* · exit 0 · `go test ./cmd/server/ -run 'TestOpenCollectiveActivationIsReachable|TestBillingConfigReadsOpenCollectiveReconcileVars|TestBillingConfigDefaultsTheReconcileKnobs' -count=1 2>&1 | tee /tmp/adr042-t5-new.out && \ …` · acceptance-sha256:d62824b69bdc3b15b92c8c4eac787e38df7d0615c8d06a6e35c6d8e2ce659e9b
- 2026-08-28 · 04543c8* · exit 0 · `{ go test ./cmd/server/ -run 'TestOpenCollectiveActivationIsReachable|TestBillingConfigReadsOpenCollectiveReconcileVars|TestBillingConfigDefaultsTheReconcileKnobs' -count=1 && go test -race ./internal/billing/ -run 'TestReconcileLoopStopsOnContextCancel|TestEndToEndOpenCollectiveActivation' -count=1 ; } 2>&1 | tee /tmp/adr042-t5-new.out && \ …` · acceptance-sha256:6c2c5eb0de2031e39f2f323705e7016c85da5f5830e5b4e5920f16988c0e7757
- 2026-08-28 · 3a40b20* · exit 0 · `{ go test ./cmd/server/ -run 'TestOpenCollectiveActivationIsReachable|TestBillingConfigReadsOpenCollectiveReconcileVars|TestBillingConfigDefaultsTheReconcileKnobs' -count=1 && go test -race ./internal/billing/ -run 'TestReconcileLoopStopsOnContextCancel|TestEndToEndOpenCollectiveActivation' -count=1 ; } 2>&1 | tee /tmp/adr042-t5-new.out && \ …` · acceptance-sha256:ea592223a4cbebd775faa1600ce4d7f2a8ec2570ce3e33a6808eddff9b29ef01
- 2026-08-28 · eec2269* · exit 0 · `{ go test ./cmd/server/ -run 'TestOpenCollectiveActivationIsReachable|TestBillingConfigReadsOpenCollectiveReconcileVars|TestBillingConfigDefaultsTheReconcileKnobs' -count=1 && go test -race ./internal/billing/ -run 'TestReconcileLoopStopsOnContextCancel|TestEndToEndOpenCollectiveActivation' -count=1 ; } 2>&1 | tee /tmp/adr042-t5-new.out && \ …` · acceptance-sha256:ea592223a4cbebd775faa1600ce4d7f2a8ec2570ce3e33a6808eddff9b29ef01
- 2026-08-28 · b1e94c3* · exit 0 · `{ go test ./cmd/server/ -run 'TestOpenCollectiveActivationIsReachable|TestBillingConfigReadsOpenCollectiveReconcileVars|TestBillingConfigDefaultsTheReconcileKnobs' -count=1 && go test -race ./internal/billing/ -run 'TestReconcileLoopStopsOnContextCancel|TestEndToEndOpenCollectiveActivation' -count=1 ; } 2>&1 | tee /tmp/adr042-t5-new.out && \ …` · acceptance-sha256:64110764557c688ca38d803b0229c33c82f7cffc8220e61d59a499ac3c0707a5
