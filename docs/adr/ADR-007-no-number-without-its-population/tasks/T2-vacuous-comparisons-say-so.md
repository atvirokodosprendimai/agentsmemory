# Task ADR-007-T2: A comparison whose mechanism had no input reports `not measured`

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `ClosetCell` measurement status — `measured` / `no effect` / `not measured`
**Consumes:** none
**Data dependency:** hermetic
**Rests-on:** `the status distinguishes an absent input from a real null`, `a genuine null keeps its number and its interval`, `the status reaches the printed cell`

## Goal

The closet cell distinguishes "the prior changed nothing" from "there was no prior to apply", and prints the second as `not measured` with the missing input named.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/evalstats.go` | edit | `ClosetCell` gains a status; `ClosetDelta` takes the corpus's closet count |
| `internal/palace/evalstats_test.go` | edit | the two cases must be distinguishable, which is this task's whole claim |
| `cmd/server/eval.go` | edit | **the selection**: pass the closet count and print the status. A status computed and not printed is the same defect one level down |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestVacuousClosetComparisonIsNotMeasured` and `TestGenuineNullIsStillReported`. Commit them red.
2. `ClosetDelta` takes the corpus closet count. Zero closets with `moved == 0` is `not measured`, naming the missing input. Non-zero closets with `moved == 0` is `no effect` and keeps its interval — that is a real null and must survive.
3. **The pre-registered falsification.** If `moved == 0` cannot be split by that check — for instance because closets exist but none fell inside `closetDistanceCap` — the rule as written converts a real null into a non-answer and must be withdrawn rather than shipped. `TestGenuineNullIsStillReported` is that check: closets present, none within the cap, `moved == 0`, and the cell must read `no effect`, not `not measured`.
4. Print the status. Six tables printed `Δ +0.000` from an experiment that never ran; a status nobody sees changes none of them.
5. Falsify: return `measured` unconditionally; ignore the closet count; compute the status and do not print it.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true;
  set -e
  git config --global --add safe.directory /src
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ ./cmd/server/ -run "TestVacuousClosetComparisonIsNotMeasured|TestGenuineNullIsStillReported|TestClosetStatusReachesTheTable" -count=1 -v 2>&1 | tee /tmp/a2.out
  grep -q -- "--- PASS: TestVacuousClosetComparisonIsNotMeasured" /tmp/a2.out
  grep -q -- "--- PASS: TestGenuineNullIsStillReported" /tmp/a2.out
  grep -q -- "--- PASS: TestClosetStatusReachesTheTable" /tmp/a2.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a2.out; then exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestVacuousClosetComparisonIsNotMeasured` | `internal/palace/evalstats_test.go` | zero closets + moved 0 reports `not measured` and names the missing input | — |
| `TestGenuineNullIsStillReported` | `internal/palace/evalstats_test.go` | closets present but none within the cap still reports `no effect` with its interval | — |
| `TestClosetStatusReachesTheTable` | `cmd/server/eval_test.go` | the status is printed, not merely computed | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestVacuousClosetComparisonIsNotMeasured` and `TestGenuineNullIsStillReported` |
| 2 — something selects it | `TestClosetStatusReachesTheTable` — a status computed and not printed changes nothing |
| 3 — the caller can discover it | the printed cell IS the interface; a reader who sees `Δ +0.000` with no status cannot tell a null from an unrun experiment |
| 4 — it is used | the closet row has been printed on seven tables and read as a null every time — that is the usage this task changes |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| return `measured` unconditionally | yes | `TestVacuousClosetComparisonIsNotMeasured` |
| treat every `moved == 0` as `not measured` | yes | `TestGenuineNullIsStillReported` |
| compute the status and omit it from the printed cell | yes | `TestClosetStatusReachesTheTable` |

## Out of Scope

- Populating closets (deferred: docs/adr/BACKLOG.md)
- Applying the same status to other preselected contrasts (deferred: docs/adr/BACKLOG.md — the closet cell is the only preselected contrast today; generalise when a second exists)

## Invariants

- A genuine null keeps its number and its interval.
- The status is derived from the corpus, never passed in by a caller's opinion.

## Risks

- ADR-003's truth table reads this cell and changes meaning. Accepted: it is unreadable today for a worse reason, and ADR-003's Follow-ups already require a re-read.

## Stop Condition

Stop and report if step 3's falsification fires — the rule is then withdrawn, and that outcome is a finding worth recording rather than a task to force through.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>

## Mutation Log
