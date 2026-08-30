# Task ADR-004-T4: Add the recency arm — the cheap fix the graph must beat

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file plus its test)
**Owner:** unassigned
**Produces:** `palace.ArmRecency`
**Consumes:** none

## Goal

Measure whether a content-date preference alone closes the supersession gap, so a knowledge graph is only ever justified by a gap that survives the cheapest available fix — and measure what that preference costs on the questions it was not built for.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | a swept eval-only arm that reorders the fused order by content date within a score band — the null hypothesis for every graph claim in ADR-004 |
| `internal/palace/eval_test.go` | edit | pin that the newer drawer wins inside the band, that undated candidates are left where fusion put them, and that no other arm moves |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestRecencyArmPrefersNewerWithinBand` in `internal/palace/eval_test.go`, over a pool where two candidates score within the band and differ only in `content_date`, asserting the recency arm ranks the newer one first while `ArmHybridCloset` does not; and `TestRecencyArmLeavesUndatedInPlace`, asserting a candidate whose date does not parse keeps its fused position. Commit them red.
2. Add `ArmRecency EvalArm = "fusion+recency"` and a `recencySweep` of band widths, named through a helper like the existing `bm25Arm` / `rerankArm`, because picking one band by hand is the constant-nobody-measured mistake this repo already sweeps its way out of twice. The sweep is a FIXED list declared in source, never derived from a run: T5 corrects its interval family-wise over the number of bands, and a k that depends on the data is not a k anyone can pre-register.
3. Implement the arm as the **unboosted `ArmHybrid`** ordering with a stable reorder: within a band of fused score, prefer the newer `findDate(ContentDate)`. An unparseable or empty date is never promoted and never demoted — absence of a date is not evidence of being old.

   **Amended before execution — this step was written for the pre-ADR-003 world.** It originally
   said `ArmHybridCloset`. ADR-003 T1 made the closet prior opt-in by arm name, and
   `TestArmBoostsDimension` now *errors* when any arm outside `{ArmHybridCloset, ArmReranked}`
   receives boosts — so building the recency arm on the closet ordering is not stale prose, it is a
   guaranteed red test. The unboosted baseline is also what ADR-004's own body calls for, and it is
   the shape production serves after ADR-003's flip.

   **The arm cannot go through the fusion seam as it stands.** `fusionRankerFor`'s signature is
   `(query, docs, dists, boosts)` with no date input, and the `candidate` struct `evalCase` builds
   carries no `ContentDate`. Thread the date onto the candidate and reorder after the fused call
   rather than widening the seam for one arm — the seam exists so an arm cannot be scored by a ranker
   no test can reach, and a date-shaped hole in it would weaken that for every other arm.
4. Keep the whole reorder inside `eval.go`: production ranking must be unable to inherit it by accident, and the ADR puts a production recency prior explicitly out of scope.
5. Run the acceptance command; both tests green and the existing arm tests unmoved.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestRecencyArm" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRecencyArmPrefersNewerWithinBand` | `internal/palace/eval_test.go` | inside the band the newer dated drawer outranks the older one, and the baseline arm does not | — |
| `TestRecencyArmReordersThroughEvalCase` | `internal/palace/eval_test.go` | **added during execution.** The ARM reorders, not just the helper. Collapsing the band to zero inside the dispatch made the arm a byte-identical copy of hybrid under a different name and left the whole suite green — the helper's own tests pass whether or not evalCase calls it | — |
| `TestRecencyArmLeavesUndatedInPlace` | `internal/palace/eval_test.go` | an undated candidate is neither promoted nor demoted | — |

## Invariants

- No existing arm's ranks change; the recency arms are additive rows in the table.
- `internal/palace/rank.go` and `service.go` are untouched — the arm is a measurement, not a ranking change.
- Dates are compared through `findDate`, never lexicographically on the raw field, matching `OlderNeighbor`.
- Every recency arm scores the NON-temporal cases too, and its per-case ranks reach `EvalMetrics.Ranks` like any other arm's. T5's veto turns on the paired MRR delta over that population, so an arm that only ever ran on temporal cases could not be shown to be free.
- `recencySweep` has a fixed length known before the run.

## Risks

- A band wide enough to help temporal cases may scramble single-hop ranking. Mitigation: the sweep makes that visible per band in the headline table, no band is promoted to production by this ADR, and T5 refuses to let a band stand for "the cheap fix" unless its non-temporal MRR is non-inferior to the gated arm's.
- The baseline this arm reorders is a moving target: ADR-002 rescales the lexical half of the fusion and ADR-003 may retire the closet prior. Mitigation: define the arm as "the production fused order plus a date preference" rather than as a fixed arm name, so it follows whichever ordering production actually ships; a recency arm measured against a retired baseline answers a question nobody asked.

## Stop Condition

Stop if the arm cannot be expressed without a tuned constant that the sweep does not cover — a hand-picked recency weight is the same inherited-folklore failure the ADR is arguing against, and it needs a decision rather than a default.

## Out of Scope

- Shipping any of this in production ranking (permanent: ADR-004 measures; a ranking change is its own ADR with its own cost on non-temporal cases)
- Recency signals other than `content_date`, such as file time or last access (deferred: docs/adr/BACKLOG.md)

## Verification Log
- 2026-08-20 · b5d8df1 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestRecencyArm" -count=1'`
- 2026-08-20 · 598b21c · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestRecencyArm" -count=1'`
- 2026-08-20 · d080f2f · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestRecencyArm" -count=1'`
- 2026-08-28 · 282ebde · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestRecencyArm" -count=1'` · acceptance-sha256:fb971f709459cd10031f9b3f51f7919449486cb59eb552eaa31a0d81e442c37e

## Mutation Log
- 2026-08-28 · ee8401b · mutant killed · exit 1 · `internal/palace/eval.go` · a swept band that is declared and never registered appears in no table — the reachability defect the sweep exists to avoid · acceptance-sha256:fb971f709459cd10031f9b3f51f7919449486cb59eb552eaa31a0d81e442c37e
