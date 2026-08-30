# Task ADR-004-T3: Keep supersession out of the headline and give it its own table

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** supersession report table, headline exclusion of temporal cases
**Consumes:** `palace.SupersessionMetrics`

## Goal

Stop the headline average from absorbing supersession failures, and print the supersession numbers where a reader will actually see them.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | headline `Cases`, `Recall1`, `Recall5`, `MRR`, `Ranks` and `PoolRanks` cover the non-temporal population; temporal keeps its `ByCategory` record |
| `cmd/server/eval.go` | edit | the supersession table (per arm: scope, cases, vacuous, current-unreachable, stale-above with its Wilson interval, the reachable-only rate, stale-in-page) and the excluded count printed beside the headline |
| `internal/palace/eval_test.go` | edit | pin the exclusion arithmetic — the assertion a reader will doubt |
| `cmd/server/eval_test.go` | edit | pin that the table prints per arm with its scope, that page-scoped arms are visibly not comparable, and that the headline names the exclusion |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestHeadlineExcludesTemporalCases` in `internal/palace/eval_test.go`, asserting a mixed case file's headline MRR equals the MRR over its non-temporal cases alone while the temporal cases still appear under `ByCategory`; and `TestSupersessionTablePrintsPerArm` plus `TestSupersessionTableSeparatesPageScopedArms` in `internal/palace/eval_test.go` (the printer lives in `internal/palace/evalstats.go`; the task originally named cmd/server). Commit them red.
2. Split the accumulation in `EvalMetrics` so temporal cases feed `ByCategory` and the supersession counts but not the headline totals.
3. Keep every derived denominator aligned with the headline population: `EvalMetrics.Ranks`, because `BootstrapMRR` and `PairedDelta` resample it and an interval over a different population than its point estimate is worse than no interval; and `EvalReport.PoolRanks` with `printPoolDiagnosis`, which divides its miss count by `report.Arms[0].Cases` and would otherwise report misses from the full case set against the shrunken headline count.
4. Print the supersession table after the category breakdown, one row per arm, with the Wilson interval beside every rate, and vacuous and current-unreachable in their own columns. Two rates per row: the headline one, which counts a retrieved distractor with an unretrieved correction as a failure, and the reachable-only one over the cases that arm actually answered. Where they disagree the reader should see it here, before T5 turns it into a verdict.
5. Group the table by scope and label it, denominator included — the pool-scoped block counts non-vacuous cases, the page-scoped and own-index rows count every verified pair. Pool-scoped arms first, then `ArmProduction` (page) and `ArmContextual` (own-index) under a line saying their zeros mean "off the page" and "outside its own index" rather than "outside the pool", and that they keep the cases the pool-scoped block drops — the distinction `printPoolDiagnosis` already makes for the contextual arm's misses, applied to the metric a reader is most likely to quote. A single ranked list across scopes would invite exactly the cross-population comparison the gate refuses.
6. Print the excluded temporal count beside the headline with one line saying the number moved because the population changed, not because ranking did.
7. Run the acceptance command; the three tests green.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestHeadlineExcludesTemporal|TestSupersessionTable" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHeadlineExcludesTemporalCases` | `internal/palace/eval_test.go` | headline MRR equals MRR over non-temporal cases; temporal still recorded per category | — |
| `TestSupersessionTablePrintsPerArm` | `internal/palace/eval_test.go` | every arm's stale-above rate, interval, vacuous and current-unreachable counts reach the report | — |
| `TestSupersessionTableSeparatesPageScopedArms` | `internal/palace/eval_test.go` | `ArmProduction` and `ArmContextual` print under their own scope with the caveat line, never mixed into the pool-scoped block | — |

## Invariants

- A case file with no temporal cases produces the same headline numbers as today and prints no supersession table.
- No arm's per-case ranks change; only which cases the headline sums over.
- No printed rate mixes arms of different scopes into one number, and every row prints the denominator it used.

## Risks

- The moved headline reads as a regression to anyone comparing against an older run. Mitigation: the excluded count and its one-line explanation print next to the number, not in a footnote.

## Stop Condition

Stop if excluding temporal cases leaves any arm with too few headline cases to interval at all — that would mean the case file is mostly temporal, and the report needs a different shape rather than a subtraction.

## Out of Scope

- The verdict and its bar — that is T5's job.
- Category handling for `absent` and `real` cases, which keep today's treatment.

<!-- Corrected during execution: the Tests table named cmd/server/eval_test.go for the two table
tests, but the printer lives in internal/palace/evalstats.go and the tests with it. adr-lint's
tests-exist check caught the mismatch — it reads the real files, which is the whole point of it. -->

## Verification Log
- 2026-08-20 · e743a9d · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestHeadlineExcludesTemporal|TestSupersessionTable" -count=1'`
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.054s
  ok  	github.com/atvirokodosprendimai/agentsmemory/cmd/server	0.006s [no tests to run]
  ```
- 2026-08-20 · 0663646* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestHeadlineExcludesTemporal|TestSupersessionTable" -count=1'`
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.058s
  ok  	github.com/atvirokodosprendimai/agentsmemory/cmd/server	0.006s [no tests to run]
  ```
- 2026-08-20 · 0663646* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestHeadlineExcludesTemporal|TestSupersessionTable" -count=1'`
- 2026-08-20 · 85ffbef · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestHeadlineExcludesTemporal|TestSupersessionTable" -count=1'`
- 2026-08-20 · ab9f402 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestHeadlineExcludesTemporal|TestSupersessionTable" -count=1'`
- 2026-08-20 · 78c9360 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestHeadlineExcludesTemporal|TestSupersessionTable" -count=1'`
- 2026-08-28 · 35c51ee · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestHeadlineExcludesTemporal|TestSupersessionTable" -count=1'` · acceptance-sha256:adc834cc9925e67cd962451d44f72f64fe79530b2b9590933c312443f71d6821

## Mutation Log
- 2026-08-28 · f1e435d · mutant killed · exit 1 · `internal/palace/eval.go` · a temporal case asks a different question — folding it into the headline recall makes the number mean two things at once, which is the whole point of the task · acceptance-sha256:adc834cc9925e67cd962451d44f72f64fe79530b2b9590933c312443f71d6821
