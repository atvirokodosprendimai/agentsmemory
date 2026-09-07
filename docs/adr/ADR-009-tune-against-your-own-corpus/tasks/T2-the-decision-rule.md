# Task ADR-009-T2: A decision rule that refuses more often than it moves

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `TuneResult` and the held-out, both-modes decision rule
**Consumes:** the two tables (T1)
**Data dependency:** hermetic — the rule is driven by synthetic per-case ranks in tests; T1's real tables exercise it once

## Goal

Given two tables, the rule picks a configuration only when a held-out paired interval excludes zero in both query modes, and records what it refused.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/tune.go` | add | the rule, beside the statistics it uses |
| `internal/palace/tune_test.go` | add | the rule's behaviour, driven by constructed rank vectors |
| `internal/palace/evalstats.go` | edit | expose the held-out split if `PairedDelta` cannot take one today |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestTuneRefusesOnATie`, `TestTuneRefusesWhenModesDisagree`, `TestTuneMovesOnAConsistentMargin`. Commit them red.
2. Split cases deterministically — seeded from the case-set id, never from wall-clock — so a tuning run is replayable and two runs on one corpus agree.
3. Argmax on the selection half; confirm on the held-out half. The interval that decides must be computed on cases that did not choose the winner.
4. Optimise the WORSE of the two modes, not the mean. An operator notices the query class that stopped working, not the average.
5. Every refusal is recorded with its reason and its interval. A tuner that only reports what it changed teaches nothing about what it considered.
6. Falsify each: decide on the selection half; take the mean of the modes; drop the interval and use the point estimate; seed the split from the clock.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true;
  set -e
  git config --global --add safe.directory /src
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestTune" -count=1 -v 2>&1 | tee /tmp/t2.out
  grep -q -- "--- PASS: TestTuneRefusesOnATie" /tmp/t2.out
  grep -q -- "--- PASS: TestTuneRefusesWhenModesDisagree" /tmp/t2.out
  grep -q -- "--- PASS: TestTuneMovesOnAConsistentMargin" /tmp/t2.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t2.out; then exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTuneRefusesOnATie` | `internal/palace/tune_test.go` | an interval containing zero keeps the incumbent and records the tie | — |
| `TestTuneRefusesWhenModesDisagree` | `internal/palace/tune_test.go` | a configuration winning one mode and losing the other is not adopted | — |
| `TestTuneMovesOnAConsistentMargin` | `internal/palace/tune_test.go` | a clear, consistent, held-out win does move the knob | — |
| `TestTuneSplitIsDeterministic` | `internal/palace/tune_test.go` | two runs over one case set agree, so a tuning result is replayable | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| decide on the selection half instead of the held-out half | yes | `TestTuneMovesOnAConsistentMargin` (a planted selection-only win must not move it) |
| take the mean of the two modes | yes | `TestTuneRefusesWhenModesDisagree` |
| compare point estimates instead of intervals | yes | `TestTuneRefusesOnATie` |
| seed the split from wall-clock | yes | `TestTuneSplitIsDeterministic` |

## Out of Scope

- Writing anything to disk (permanent: T3 owns the file and its precedence; a rule that also persists is two decisions)
- Tuning knobs the eval does not measure (permanent: a tuner that guesses is worse than a default somebody chose)

## Invariants

- The interval that decides is never computed on the cases that selected the winner.
- A refusal is as loud as a change.

## Risks

- A held-out split at small n leaves both halves underpowered and the rule never moves. Accepted: never moving is the correct behaviour when the evidence cannot separate, and the recorded refusal says so.

## Stop Condition

Stop and ask if the rule never moves on a large corpus — that is the parent ADR's pre-registered falsification, and it means a global constant suffices.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>

## Mutation Log
