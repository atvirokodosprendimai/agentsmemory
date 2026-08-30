# Task ADR-004-T1: Score where the superseded version landed

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `EvalCase.Distractor`, `EvalCaseResult.DistractorRanks`, `EvalCaseResult.DistractorPoolRank`, `palace.SupersessionMetrics`
**Consumes:** none

## Goal

Make the eval record where the superseded drawer landed — per arm, and once per case in the shared pool — and turn those ranks into stale-above-current rates with intervals that do not lie at the boundaries.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | `EvalCase` carries the superseded drawer, `EvalCaseResult` carries its per-arm rank AND its one pool rank, each arm carries its scope, `EvalMetrics` carries the counts — the gold's rank alone cannot express "the stale version outranked its correction", and one rank cannot express it for arms scored over different populations |
| `internal/palace/evalstats.go` | edit | the rate and its Wilson interval — a proportion, not a mean of reciprocal ranks, so the percentile bootstrap next door is the wrong instrument |
| `internal/palace/eval_test.go` | edit | pin that the distractor is ranked from the same pool as the gold, that pool presence is recorded once per case, and that the two non-pool arms are labelled rather than folded in |
| `internal/palace/evalstats_test.go` | edit | pin the rate arithmetic on a set whose answer is known by hand, including the boundary values where a bootstrap would collapse |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestSupersessionRanksDistractorInSamePool` and `TestSupersessionRanksScopePerArm` in `internal/palace/eval_test.go`; `TestStaleAboveRateExcludesVacuous`, `TestStaleAboveRateCountsUnreachableCurrent` and `TestStaleAboveRateWilsonNotBootstrap` in `internal/palace/evalstats_test.go`. Commit them red.
2. Add `Distractor string` to `EvalCase` (json `distractor,omitempty`), and `DistractorRanks map[EvalArm]int` plus `DistractorPoolRank int` to `EvalCaseResult` in `internal/palace/eval.go`.
3. In `evalCase`, look the distractor up in the **same per-arm ordering already computed for the gold** — one pool, one ordering, two lookups. A second retrieval would break the shared-pool property the whole table rests on.

   **Resolve the distractor to a MEMORY id exactly as the gold is resolved.** Found by review before
   execution. The pool is keyed by `p.memory` (`internal/palace/eval.go:637-638`), and the gold gets
   there through a `repo.Get` plus `ParentID` into `goldSet` (`:595-605`). Rank a raw distractor
   drawer id against memory ids and every MULTI-CHUNK distractor scores as never-retrieved —
   `Vacuous` inflates, every stale-above rate is flattered, and nothing fails. Write the failing test
   for the multi-chunk case in step 1, not just the single-chunk one, or the bug ships behind a green
   suite.

   Note also that the lookup lands in more than one place: `rankOf` is called from the fusion-seam
   branch and again for the page-scoped and own-index arms (`:730`, `:745`, `:796`), so "two lookups"
   understates it. Whatever resolves the distractor must be shared, not repeated.
4. Record `DistractorPoolRank` once per case beside the gold's existing `poolRank`, from the same dense-distance ordering. Vacuity is a property of the case, not of an arm: two arms may rank a distractor differently, but they cannot disagree about whether it was retrievable, and reading each arm's own 0 as "outside the pool" is the mistake this task exists to not repeat.
5. Give each arm a `SupersessionScope`: pool for every arm that re-orders the shared pool; page for `ArmProduction`, which is scored over the ≤`DefaultSearchLimit` page `Search` returns after `DefaultMaxDistance` (`internal/palace/eval.go:562`); own-index for `ArmContextual`, which retrieves from its own namespace. A boolean would not do — the report has to name the population and the gate has to refuse the two that are not comparable, which is the same line `printPoolDiagnosis` already draws for the contextual arm.
6. Add `SupersessionMetrics{Scope, Cases, StaleAbove, StaleAboveReachable, StaleInPage, CurrentUnreachable, Vacuous}` to `EvalMetrics` and fill it per arm. The denominator follows the scope: a pool-scoped arm counts the non-vacuous cases, while a page-scoped or own-index arm counts EVERY verified pair, because it nominated its own candidates and a case vacuous in the shared pool may well have been on its page — `Search` fetches `limit * hybridCandidateMultiplier` raised to `rerankPool` (50 at the defaults, `internal/palace/service.go:649`), which is larger than the `--pool 20` this corpus has been run at. `Cases` is that denominator, so a reader can see which one a row used. Then, per case: `CurrentUnreachable` when the gold's rank is 0; `StaleAbove` when the distractor's rank is non-zero AND either the gold's rank is 0 or the distractor's is lower-numbered — the gold's 0 is a miss sentinel, so a bare `<` scores "stale retrieved, correction missing" as a success; `StaleAboveReachable` the same rate restricted to cases with a non-zero gold rank; `StaleInPage` when the distractor's rank is 5 or better — the cutoff `Recall5` uses, written as a number because the page the agent sees is `DefaultSearchLimit` and the eval never sets `SearchQuery.Limit`. On a page-scoped arm that page is already at most five long, so `StaleInPage` there means only "the distractor was on the page"; the scope printed beside it is what stops the two being read as one number.
7. Add `StaleAboveRate` and `WilsonInterval` to `internal/palace/evalstats.go`: the rate over non-vacuous cases, and its 95% score interval. Do NOT reuse `BootstrapMRR` — resampling a proportion by percentile returns `[0,0]` at a rate of 0 and `[1,1]` at 1, and 0 is the value this metric is most likely to take on a small corpus.
8. Run the acceptance command; the new tests green and no existing eval test moves.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestSupersessionRanks|TestStaleAboveRate" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSupersessionRanksDistractorInSamePool` | `internal/palace/eval_test.go` | the distractor is ranked from the gold's pool, and its pool rank is recorded once per case | — |
| `TestSupersessionRanksScopePerArm` | `internal/palace/eval_test.go` | pool-scoped arms report scope pool, `ArmProduction` reports page, `ArmContextual` reports own-index | — |
| `TestStaleAboveRateExcludesVacuous` | `internal/palace/evalstats_test.go` | a case whose distractor never entered the pool leaves both numerator and denominator | — |
| `TestStaleAboveRateCountsUnreachableCurrent` | `internal/palace/evalstats_test.go` | distractor retrieved with the gold at rank 0 counts as stale-above, not as a success, and appears in `CurrentUnreachable` | — |
| `TestStaleAboveRateWilsonNotBootstrap` | `internal/palace/evalstats_test.go` | a rate of 0 over 8 cases returns a non-degenerate interval whose upper bound exceeds 0.20 | — |

## Invariants

- Gold ranks are untouched: every arm's MRR is identical before and after this task.
- A case with no `Distractor` — every non-temporal case and every pre-existing case file — scores exactly as today.
- A case whose distractor id equals its gold id is rejected at load rather than scored; a drawer cannot supersede itself.
- Vacuity is decided once per case from the shared pool and never per arm.
- Vacuous cases are dropped from pool-scoped rates only; a page-scoped or own-index arm keeps every verified pair, because the pool it was excluded on is not the pool it used.
- A page-scoped or own-index arm's counts are recorded and labelled with their own denominator, never summed into a pool-scoped rate.

## Risks

- Counting a vacuous case as "not stale" would flatter every arm. Mitigation: vacuous cases are excluded from the denominator, not scored as successes, and the count is carried so the report can show it.
- Wilson is a normal approximation and this corpus is small. Mitigation: it is the interval recommended at exactly this size, it never collapses to zero width, and at n=30 Clopper–Pearson differs by one case at the `justified` edge — documented in the ADR as the stricter fallback, not silently swapped in.

## Stop Condition

Stop if the distractor cannot be ranked without re-running retrieval for it — a second pool per case breaks the one-shared-pool property every arm comparison depends on, and the design needs revisiting rather than patching.

## Out of Scope

- Generating or verifying the pairs — that is T2's job.
- Printing anything — T3 owns the report and T5 the verdict.
- Choosing which arm the gate reads, and the family-wise correction over the recency sweep — T5 owns both; this task only labels the scopes that make the choice possible.

## Verification Log
- 2026-08-20 · a962858* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestSupersessionRanks|TestStaleAboveRate" -count=1'`
- 2026-08-20 · 8a89e42 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestSupersessionRanks|TestStaleAboveRate" -count=1'`
- 2026-08-20 · 15d2f91 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestSupersessionRanks|TestStaleAboveRate" -count=1'`
- 2026-08-20 · a5f94cb · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestSupersessionRanks|TestStaleAboveRate" -count=1'`
- 2026-08-28 · 8864647 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestSupersessionRanks|TestStaleAboveRate" -count=1'` · acceptance-sha256:5c0c69b789adac4019f0c5b7f543f0cfa6f6321cf57364184b0feba8c1410005

## Mutation Log
- 2026-08-28 · 8727c86 · mutant killed · exit 1 · `internal/palace/evalstats.go` · stale-above must count the case where the current version is UNREACHABLE — dropping gold==0 silently reports the worst outcome as no finding · acceptance-sha256:5c0c69b789adac4019f0c5b7f543f0cfa6f6321cf57364184b0feba8c1410005
