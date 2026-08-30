# Task ADR-003-T1: Make closet use an explicit arm dimension

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `closetBoostsAt` — closet boosts at a caller-supplied scale; `armBoosts` — the arm → boosts classification every rank call in `evalCase` goes through; `ArmHybridRerank` — the closet-off reranked arm
**Consumes:** none

## Goal

Every eval arm carries the closet prior only if its name says so, and measures what its name says whatever the server is configured to serve.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/mine.go` | edit | `closetBoosts` returns nothing when the served scale is 0 (`mine.go:301`), which is right for `Search` and wrong for an arm named after the mechanism; split out `closetBoostsAt(ctx, teamID, vec, scale)` and leave `closetBoosts` delegating |
| `internal/palace/eval.go` | edit | `evalCase` builds ONE boosts slice (`eval.go:493`) and hands it to fourteen arm names — twelve of which never say `closet`; route every rank call through `armBoosts(arm, closet)`, add `ArmHybridRerank`, extract the arms list so it can be enumerated by a test, leave `ArmProduction` on the served scale |
| `internal/palace/eval_test.go` | edit | pin the classification arm by arm, pin that the closet arm still separates from `hybrid` at served scale 0, and pin that the production arm does not |
| `cmd/server/eval.go` | edit | the command description lists the arms an operator will read; a new arm that is not listed is the omission this repo already pays a gate to catch |
| `internal/palace/armreach_test.go` | edit | **added during execution.** `TestEveryDeclaredArmIsRegistered` parses `EvaluateWith` by name, so extracting the list into `evalArms` made it report all eleven arms as unreachable. The gate has to be pointed at the assembling function; step 4 warned the gate would lose sight of the list but the file was missing from this table |

## Ordered Steps

1. Write the failing tests first (TDD red) and commit them red:
   - `TestArmBoostsDimension` — build the arms list with the extracted `evalArms(...)` and assert `armBoosts` hands the closet slice to exactly `hybrid+closet` and `hybrid+closet+rerank`, and `nil` to every other arm, sweeps included.
   - `TestClosetArmMeasuresClosetsWhenServedPriorIsOff` — service built `WithClosetBoost(0)`, one mined source, a case whose gold shares that source: `hybrid+closet`'s ranks differ from `hybrid`'s.
   - `TestProductionArmFollowsServedClosetScale` — at served scale 0 the production arm's ranks match the unboosted order.
2. Add `closetBoostsAt(ctx context.Context, teamID string, vec []float32, scale float64) map[string]float64` in `mine.go`, holding the present body with `scale` where `s.closetBoostScale` is read — early return included, so a caller passing 0 still skips the closet vector search. Reduce `closetBoosts` to `return s.closetBoostsAt(ctx, teamID, vec, s.closetBoostScale)` so `Search` and every existing caller are untouched.
3. Add `armBoosts(arm EvalArm, closet []float64) []float64` in `eval.go`: `closet` for `ArmHybridCloset` and `ArmReranked`, `nil` for everything else. Put the reason at the definition — an arm whose name does not say `closet` must not carry one, or a decision about the lexical weight gets read off a table that was silently measuring a curation prior as well.
4. Extract the arms-list construction from `EvaluateWith` into `evalArms(opts EvalOptions, rerank bool) []EvalArm` and call it from both `EvaluateWith` and the test. `TestEveryDeclaredArmIsRegistered` (`armreach_test.go`) parses the function that assembles the list **by name**, so the extraction alone turns that gate red on every arm — repoint it at `evalArms` and keep the name in one named constant so the next move is one edit. The red run is the gate working: a check that silently followed the code would have proved nothing.
5. In `evalCase`, compute the closet slice once with `s.closetBoostsAt(ctx, teamID, vec, 1)` and pass every `rankHybrid` / `rankHybridWeighted` / `rankHybridAdaptive` / `rankHybridAdaptiveIDF` / `rankRRF` call through `armBoosts(arm, closet)`. The reranked arms need two fused pools now — one closet-off, one closet-on — and `RerankScoresFor` aligns its scores to the order it is handed (`service.go:822-826`), so each pool gets its own cross-encoder pass, exactly as `rrf+rerank` already does.
6. Register `ArmHybridRerank` (`"hybrid+rerank"`) beside `ArmReranked` in the rerank block of `evalArms`, and add it to the `eval` command description. After ADR-003's flip production is closet-OFF plus the cross-encoder, so without this arm the only reranked row in the table would be named after a configuration nobody runs.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestArmBoostsDimension|TestClosetArmMeasuresClosetsWhenServedPriorIsOff|TestProductionArmFollowsServedClosetScale|TestEveryDeclaredArmIsRegistered|TestSearchAppliesClosetBoost|TestRerankedArmsUseThePoolTheirNameClaims|TestEvalCaseFetchesOnlyThePoolsItsArmsRead" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestArmBoostsDimension` | `internal/palace/eval_test.go` | exactly the two closet-named arms receive closet boosts; every other registered arm receives `nil` | — |
| `TestClosetArmMeasuresClosetsWhenServedPriorIsOff` | `internal/palace/eval_test.go` | the `hybrid+closet` arm still applies closet boosts when the served scale is 0 | — |
| `TestProductionArmFollowsServedClosetScale` | `internal/palace/eval_test.go` | `ArmProduction` reflects the configured scale, not the arms' full-strength one | — |
| `TestEveryDeclaredArmIsRegistered` | `internal/palace/armreach_test.go` | the new `ArmHybridRerank` is actually appended to the arms list | — |
| `TestSearchAppliesClosetBoost` | `internal/palace/rank_test.go` | the search path is unchanged by the split | — |
| `TestEvalCaseFetchesOnlyThePoolsItsArmsRead` | `internal/palace/eval_test.go` | **added after review.** Counts cross-encoder passes in both directions — a pool no requested arm reads is not fetched, one that is read is. A skipped pass degrades silently to the fused order and still prints a row headed by the reranker's name | — |
| `TestRerankedArmsUseThePoolTheirNameClaims` | `internal/palace/eval_test.go` | **added after review, and it was blocking.** Each reranked arm reads the pool its name claims. Inverting that one condition, so every reranked arm read the wrong pool, previously turned zero tests red — no fixture in the package configured a reranker, so the branch was dead to the suite. The classifier had a test; its consumer did not | — |

## Invariants

- `Search` behaves identically at every scale: `closetBoosts` keeps its signature, its early return at 0, and its callers.
- `vector`, `hybrid` and `contextual chunks` score exactly as before — they already passed `nil` (`eval.go:555`, `eval.go:640`).
- Every other arm's numbers MOVE, and that is the point of the task rather than a regression: `rrf`, `rrf+rerank`, `fusion bm25=auto`, `fusion bm25=auto-idf`, the four `fusion bm25=<w>` sweeps and the four `rerank blend w=<w>` sweeps were all carrying the closet prior. Every number those arms produced before this task was taken closet-ON, including the ones ADR-002 quotes.
- `ArmProduction` still goes through `Search` and still reads the served scale.

## Risks

- One extra cross-encoder pass per case, for the second fused pool — and only when an arm that reads it was actually requested. Review found both passes firing unconditionally whenever a reranker was configured, so a caller asking for no reranked arms paid for two passes it never used; `evalCase` now fetches a pool only if a requested arm reads it. With `--pool 50` and the default `RERANK_POOL=50` both passes score the same 50 documents in a different order, so the cost is inference time rather than a different candidate set; on a corpus where the pool exceeds the rerank pool the two heads differ, which is information, not noise.
- Renaming what an arm measures without saying so in the table would leave two runs of the same arm name meaning different things. T2's provenance block records the commit each run was taken at, which is what makes an old table identifiable as pre-T1.

## Stop Condition

Stop if the closet arm cannot be made to differ from `hybrid` in a test without standing up a live embedding backend. `newTestService` mines and searches offline with `fakeEmbedder`, which is what `TestSearchAppliesClosetBoost` already relies on; if that fixture cannot separate the two arms, building one that can is a larger task than this one assumes.

## Out of Scope

- Flipping any default — T4's job, and only behind T3's table.
- Re-tuning `closetRankBoosts`, `closetDistanceCap` or `closetBoostStrength` (permanent: this ADR moves a default, not the formula.)
- Closet variants of the RRF, adaptive, IDF or sweep arms (permanent: the decision needs one closet-on/closet-off pair per production shape; a second dimension over ten arms is a table nobody reads.)

## Verification Log
- 2026-08-20 · 8a3ab6b* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestArmBoostsDimension|TestClosetArmMeasuresClosetsWhenServedPriorIsOff|TestProductionArmFollowsServedClosetScale|TestEveryDeclaredArmIsRegistered|TestSearchAppliesClosetBoost" -count=1'`
- 2026-08-20 · 13b92c8 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestArmBoostsDimension|TestClosetArmMeasuresClosetsWhenServedPriorIsOff|TestProductionArmFollowsServedClosetScale|TestEveryDeclaredArmIsRegistered|TestSearchAppliesClosetBoost" -count=1'`
- 2026-08-20 · 1ec4007 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestArmBoostsDimension|TestClosetArmMeasuresClosetsWhenServedPriorIsOff|TestProductionArmFollowsServedClosetScale|TestEveryDeclaredArmIsRegistered|TestSearchAppliesClosetBoost" -count=1'`
- 2026-08-20 · 195c901 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestArmBoostsDimension|TestClosetArmMeasuresClosetsWhenServedPriorIsOff|TestProductionArmFollowsServedClosetScale|TestEveryDeclaredArmIsRegistered|TestSearchAppliesClosetBoost|TestRerankedArmsUseThePoolTheirNameClaims|TestEvalCaseFetchesOnlyThePoolsItsArmsRead" -count=1'`
- 2026-08-20 · abe1665 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestArmBoostsDimension|TestClosetArmMeasuresClosetsWhenServedPriorIsOff|TestProductionArmFollowsServedClosetScale|TestEveryDeclaredArmIsRegistered|TestSearchAppliesClosetBoost|TestRerankedArmsUseThePoolTheirNameClaims|TestEvalCaseFetchesOnlyThePoolsItsArmsRead" -count=1'`
- 2026-08-20 · d29da6f · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestArmBoostsDimension|TestClosetArmMeasuresClosetsWhenServedPriorIsOff|TestProductionArmFollowsServedClosetScale|TestEveryDeclaredArmIsRegistered|TestSearchAppliesClosetBoost|TestRerankedArmsUseThePoolTheirNameClaims|TestEvalCaseFetchesOnlyThePoolsItsArmsRead" -count=1'`
- 2026-08-28 · ff70b93 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestArmBoostsDimension|TestClosetArmMeasuresClosetsWhenServedPriorIsOff|TestProductionArmFollowsServedClosetScale|TestEveryDeclaredArmIsRegistered|TestSearchAppliesClosetBoost|TestRerankedArmsUseThePoolTheirNameClaims|TestEvalCaseFetchesOnlyThePoolsItsArmsRead" -count=1'` · acceptance-sha256:d82a5d626d22b7b3747aeef2e705864d86e49f871c25fff61910a20f07e9f367

## Mutation Log
- 2026-08-28 · bbdff3f · mutant killed · exit 1 · `internal/palace/eval.go` · the arm dimension: every arm receiving the curation prior is the exact pre-T1 defect — twelve arms whose names promise a pure comparison quietly measuring a closet boost · acceptance-sha256:d82a5d626d22b7b3747aeef2e705864d86e49f871c25fff61910a20f07e9f367
