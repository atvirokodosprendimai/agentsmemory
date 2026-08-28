# Task ADR-041-T2: Record the baseline rate before anything tries to move it

**Depends-on:** T1
**Covers:** F-3
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `recall baseline` — the pre-mechanism rate, with its sample size and window
**Consumes:** `recall observation record` (T1)
**Data dependency:** needs REAL SESSIONS — at least 20 observations from ordinary work, collected
over a window recorded in the sign-off. The acceptance fence below is hermetic and **cannot see this
requirement**; it proves a baseline was recorded and well-formed, not that it was taken against
enough real sessions. That is what the sign-off line is for.

## Goal

A recorded number, with the sample size, window AND CLASSIFIER PRECISION it was taken under, that
later tasks are measured against.

⚠ **Precision is not optional here, and T1's held-out run is why.** Measured 2026-08-27 over 46
transcripts: the classifier runs at roughly 50% precision, so half the denominator is not the
class. A bare rate would be quoted as though it meant one thing while meaning another. Report
the rate, the sample size, the window, the classifier version and the precision it was judged
at — or report nothing.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/recallrate.go` | edit | The reader that aggregates observations into a rate |
| `clients/claude-code/recallrate_spec_test.go` | edit | `TestF3…` turns green |
| `docs/adr/BACKLOG.md` | edit | The baseline is reported there whichever way it falls, per the ADR's Follow-ups |

## Ordered Steps

1. Confirm `TestF3NoMechanismShipsBeforeABaseline` is red.
2. Implement the aggregate reader: observations in, rate out, refusing to report a rate below the
   minimum sample size rather than reporting a noisy one.
3. **Collect real observations.** This is the step the fence cannot check.
4. Record the rate, the sample size, the window, and the classifier version in `BACKLOG.md`.
5. Record the sign-off with `adr-verify --human`, naming the sample size and window.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./clients/claude-code/ -run "^(TestF3NoMechanismShipsBeforeABaseline|TestTheBaselineRefusesAnUndersizedSample)$" -count=1 -v 2>&1 | tee /tmp/acc.out; grep -q "^=== RUN" /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
```

⚠ This fence proves the CODE half. The task is not done until the sign-off line records what the run
was taken against — `adr-verify --human "baseline N=<count> over <window>, classifier v<version>"`.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF3NoMechanismShipsBeforeABaseline` | `clients/claude-code/recallrate_spec_test.go` | a rate exists before any mechanism ships | F-3 |
| `TestTheBaselineRefusesAnUndersizedSample` | same | fewer observations than the minimum reports "insufficient", not a number | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the aggregate reader's unit tests |
| 2 — something selects it | the operator runs it; there is no automatic caller and that is deliberate |
| 3 — the caller can discover it | the command's `--help` names it |
| 4 — it is used | the recorded baseline in `BACKLOG.md` is the usage |

## Mutation Log

- 2026-08-28 · 1d3a11a* · mutant killed · exit 1 · `clients/claude-code/recallrate.go` · the undersized refusal: a rate from five sessions is quoted like a rate from five hundred · acceptance-sha256:d4e8e19eca8d12a155e517da2eb2da8fde9cf68d0815c8a5dd41143c6c51c879
- 2026-08-28 · 1d3a11a* · mutant killed · exit 1 · `clients/claude-code/recallrate.go` · the precision refusal: half the denominator is not the class at v2 · acceptance-sha256:d4e8e19eca8d12a155e517da2eb2da8fde9cf68d0815c8a5dd41143c6c51c879
- 2026-08-28 · 1d3a11a* · mutant killed · exit 1 · `clients/claude-code/recallrate.go` · the classifier-mixing refusal (F-16): rates under different classifiers are not comparable · acceptance-sha256:d4e8e19eca8d12a155e517da2eb2da8fde9cf68d0815c8a5dd41143c6c51c879
- 2026-08-28 · 38c99a8 · mutant inconclusive · exit 1 · `clients/claude-code/recallrate.go` · the floor below which no rate is reported is removed, so a one-session baseline is quoted like one from hundreds · acceptance-sha256:8cd2fd156fab8c9e93f0396faaf2ceb8c46304c36d979ecd4cad6dcf8efa27b9
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-08-28 · 34ecb42 · mutant killed · exit 1 · `clients/claude-code/recallrate.go` · the undersized-sample refusal never fires, so a rate from a handful of sessions is reported exactly like one from hundreds · acceptance-sha256:8cd2fd156fab8c9e93f0396faaf2ceb8c46304c36d979ecd4cad6dcf8efa27b9

## Invariants

- A rate is never reported without its sample size, window and classifier version.\n- An undersized sample reports insufficiency rather than a number.

## Risks

- The sample may be unrepresentative: sessions during this work are not ordinary sessions. Record\n  the window so a later reader can judge that rather than inherit it silently.

## Stop Condition

Stop if fewer than the minimum observations exist. A baseline taken on five sessions will be\nquoted for a year. ⚠ What would make this impossible to fail? Choosing the minimum AFTER seeing\nthe collected count. Fix the minimum in step 2, before collection.

## Out of Scope

- Interpreting the baseline (that is T3-T6's job, one at a time)\n- Backfilling from historical transcripts (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
- 2026-08-28 · 1d3a11a* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./clients/claude-code/ -run "^(TestF3NoMechanismShipsBeforeABaseline|TestTheBaselineRefusesAnUndersizedSample)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:d4e8e19eca8d12a155e517da2eb2da8fde9cf68d0815c8a5dd41143c6c51c879
  ```
  === RUN   TestF3NoMechanismShipsBeforeABaseline
      recallrate_spec_test.go:191: not built yet — F-3 (T2): a mechanism intended to raise the rate cannot ship until a baseline exists. Otherwise its effect is unfalsifiable in both directions
  --- FAIL: TestF3NoMechanismShipsBeforeABaseline (0.00s)
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	0.007s
  FAIL
  ```
- 2026-08-28 · 1d3a11a* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./clients/claude-code/ -run "^(TestF3NoMechanismShipsBeforeABaseline|TestTheBaselineRefusesAnUndersizedSample)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:d4e8e19eca8d12a155e517da2eb2da8fde9cf68d0815c8a5dd41143c6c51c879
- 2026-08-28 · human-observed · baseline 27.6% (61/221) over N=46 sessions, window 2026-08-01..2026-08-28, classifier v2, precision 48% hand-judged 12/25 — recorded in docs/adr/BACKLOG.md
- 2026-08-28 · human-observed · baseline RE-TAKEN under v3 (preceded = a recall since the last user turn, amended by the owner 2026-08-28): 7.6% (26/341) over 24 sessions carrying assertions, from 48 transcripts scanned, classifier v3 — supersedes the v2 figure of 27.6% (61/221), which is not comparable across counting rules per F-16; recorded in docs/adr/BACKLOG.md
- 2026-08-28 · 5e734b8 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./clients/claude-code/ -run "^(TestF3NoMechanismShipsBeforeABaseline|TestTheBaselineRefusesAnUndersizedSample)$" -count=1 -v 2>&1 | tee /tmp/acc.out; grep -q "^=== RUN" /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:8cd2fd156fab8c9e93f0396faaf2ceb8c46304c36d979ecd4cad6dcf8efa27b9
