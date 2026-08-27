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

A recorded number, with the sample size and window it was taken over, that later tasks are measured
against.

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
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./clients/claude-code/ -run "TestF3|TestTheBaselineRefusesAnUndersizedSample" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
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

## Invariants

- A rate is never reported without its sample size, window and classifier version.\n- An undersized sample reports insufficiency rather than a number.

## Risks

- The sample may be unrepresentative: sessions during this work are not ordinary sessions. Record\n  the window so a later reader can judge that rather than inherit it silently.

## Stop Condition

Stop if fewer than the minimum observations exist. A baseline taken on five sessions will be\nquoted for a year. ⚠ What would make this impossible to fail? Choosing the minimum AFTER seeing\nthe collected count. Fix the minimum in step 2, before collection.

## Out of Scope

- Interpreting the baseline (that is T3-T6's job, one at a time)\n- Backfilling from historical transcripts (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
