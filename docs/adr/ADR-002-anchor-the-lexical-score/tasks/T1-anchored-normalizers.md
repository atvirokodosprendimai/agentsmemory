# Task ADR-002-T1: Make the lexical normaliser a choice, and add the two anchored transforms

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file plus its test)
**Owner:** unassigned
**Produces:** `lexNorm` normaliser type, `lexNormPageMax` / `lexNormCeiling` / `lexNormSaturating`, `rankFused` taking a normaliser
**Consumes:** none

## Goal

Give `rankFused` an explicit lexical normaliser, with today's page-maximum as one named option and two anchored transforms beside it — no behaviour change on any existing path.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/rank.go` | edit | `rankFused` (`rank.go:419-444`) divides by `maxBM25`; the divisor becomes a named `lexNorm` and two anchored transforms are added |
| `internal/palace/rank_test.go` | edit | pin the defect, the *winner*-independence property, the corrected weight identity, and that `page-max` reproduces today exactly |

## Ordered Steps

1. Write the failing tests first (TDD red), in `internal/palace/rank_test.go`:
   - `TestLexNormPageMaxGivesTheWinnerFullWeight` — under `page-max` the top candidate's normalised lexical score is exactly 1.0 on a page whose best raw BM25 is weak; under `ceiling` it is not.
   - `TestLexNormAnchoredIgnoresWhichCandidateWon` — the narrow, true property (see step 2 for the fixture): under an anchored norm, changing *another* candidate so that it becomes the page's lexical winner leaves this candidate's normalised lexical score bit-identical, while under `page-max` it shrinks.
   - `TestLexNormCeilingEqualsPageMaxAtTheRescaledWeight` — over randomly generated boost-free pages, the `ceiling` ordering at `w` equals the `page-max` ordering at `w' = w·a/(1 − w + w·a)`, `a = maxBM25/C`, and does **not** in general equal it at `w' = w·a`. This is the identity the first version of the ADR got wrong; the test is what keeps it correct.
   - `TestLexNormBoostHasNoEquivalentPageMaxWeight` — over generated boosted pages, some page has NO `w'` on a fine grid over `[0,1]` reproducing the `ceiling` ordering at `w`, while every boost-free page has one.

     **Corrected during execution.** This step originally asked for the claim on a single hand-built fixture, and that claim is false: four candidates admit twenty-four orderings, so a page-max weight matches by coincidence on roughly two pages in three, and the first fixture written to this spec failed for that reason. The ADR's own evidence was always a population statistic — 36% disagreement over 4,000 pages, not 100% over one. What distinguishes the two normalisers is per-page EXISTENCE: without a boost a reproducing weight always exists and the previous test names it in closed form, and with a boost pages exist where none does. The test asserts both halves, so it fails if either stops being true.
   - `TestLexNormPageMaxIsTodaysArithmetic` — the `page-max` order equals the pre-change `rankHybridWeighted` order on a fixed fixture.

   Commit them red.
2. Build the winner-independence fixture so it isolates the winner and nothing else. `bm25Scores` computes df over query terms only and `avgdl` over all tokens (`rank.go:117-136`), so replacing one candidate's text with a variant that (a) contains the same set of query terms, (b) has the same token count, and (c) differs only in term *frequency* leaves `N`, every `df`, every `idf` and `avgdl` unchanged — so every other candidate's raw score is bit-identical and `C` is unchanged, while the edited candidate's raw score moves. Choose the frequencies so the variant actually becomes the page maximum, and **assert that it did** before asserting anything about the siblings; at `k1 = 1.5` a 1→2 bump may not be enough on its own.
3. Add `type lexNorm` and the three implementations to `rank.go`. `lexNormCeiling` divides by `C = (bm25K1+1) * Σ idf(t)` over the query terms, reusing the same smoothed IDF `bm25Scores` computes; `lexNormSaturating` returns `raw/(raw + 0.5*C)`.
4. Extract the query ceiling into one helper shared by both anchored transforms and by `bm25Scores`' IDF computation, so the two cannot drift apart.
5. Thread the normaliser through `rankFused`; every existing caller (`rankHybridWeighted`, `rankHybridAdaptive`, `rankHybridAdaptiveIDF`) passes `lexNormPageMax` and is behaviourally unchanged.
6. Guard the degenerate cases: `C == 0` must yield a zero lexical contribution, never a division by zero. The smoothed Lucene IDF is strictly positive even for a term in every candidate, so an empty query-term set (or an empty candidate set) is the only way `C` reaches zero — but the guard is written against `C` itself rather than against that reasoning, because the reasoning would not survive a change of IDF formula.
7. Document on `lexNormCeiling` what it does and does not buy: winner-independence, not candidate-set independence — `N`, `df`, `idf` and `avgdl` are all pool quantities, so dropping a sibling still moves both `raw` and `C`. A doc comment that claims more than the test proves is how the first version of this ADR went wrong.
8. Pin the two invariants no other test reaches: the saturating transform's own contract, and the degenerate-input guard on all three normalisers. Added during execution — `lexNormSaturating` is registered as an arm only in T2, so without a test of its own it would ship as code nothing asserts anything about, which is the defect class this repository is named after.
9. Prove the anchoring tests can fail: alias `lexNormCeiling` to `lexNormPageMax`, watch them go red, put it back. A test that passes under both normalisers is not testing the normaliser.
10. Run the acceptance command; the whole existing rank suite must be green alongside the new tests.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestLexNorm|TestRankHybrid|TestRankRRF|TestBM25Scores|TestLexicalCoverage|TestAdaptiveWeight" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestLexNormPageMaxGivesTheWinnerFullWeight` | `internal/palace/rank_test.go` | the defect: page-max hands the winner `norm == 1.0` however weak its raw score, and an anchored norm does not | — |
| `TestLexNormAnchoredIgnoresWhichCandidateWon` | `internal/palace/rank_test.go` | with pool statistics held fixed by construction, moving the lexical winner leaves other candidates' anchored contributions unchanged and shrinks their page-max ones | — |
| `TestLexNormCeilingEqualsPageMaxAtTheRescaledWeight` | `internal/palace/rank_test.go` | the corrected identity `w' = w·a/(1−w+w·a)` holds with no boost, and `w' = w·a` does not | — |
| `TestLexNormBoostHasNoEquivalentPageMaxWeight` | `internal/palace/rank_test.go` | boost-free pages always have a reproducing page-max weight; boosted pages sometimes have none | — |
| `TestLexNormSaturatingCompressesTheTop` | `internal/palace/rank_test.go` | the saturating transform's contract — half-way at `kappa·C`, strictly increasing, bounded below 1, and compressing strong matches relative to `ceiling` | — |
| `TestLexNormDegenerateInputsYieldZero` | `internal/palace/rank_test.go` | every normaliser contributes zero, never NaN, when there is no lexical signal, and the vector order stands | — |
| `TestLexNormPageMaxIsTodaysArithmetic` | `internal/palace/rank_test.go` | `page-max` reproduces the pre-change ordering exactly | — |

## Invariants

- Every pre-existing ranking test passes unchanged; `page-max` is the default on every caller after this task.
- No production code path selects an anchored normaliser yet — this task ships the option, not the choice.
- `C == 0` and `maxBM25 == 0` both yield a zero lexical term rather than a NaN or a panic.
- `a = maxBM25/C < 1` strictly whenever any query term has nonzero IDF, so `w' < w`: the anchored normalisers can only shrink the effective lexical weight.

## Risks

- The ceiling helper and `bm25Scores`' IDF can drift apart if duplicated; step 4 shares one helper, and `TestLexNormCeilingEqualsPageMaxAtTheRescaledWeight` fails loudly if the two disagree, because the identity is exact only when both use the same IDF.
- A sibling-independence test written the obvious way — drop a candidate, expect the others unchanged — will fail, and correctly: `N`, `df` and `avgdl` all move. Step 2's fixture is the narrower property that is actually true; do not "fix" the test by loosening it to an approximate comparison.

## Stop Condition

Stop and ask if `κ = 0.5` in the saturating transform turns out to need per-corpus tuning to be competitive — a second free constant defeats the point of anchoring, and that is a decision to take in the open rather than in a default value.

## Out of Scope

- Registering the anchored arms in the eval — that is T2's job.
- Any config key or default change — that is T4's job.
- Making `raw` or `C` independent of the candidate set, which needs corpus-wide term statistics (deferred: docs/adr/ADR-002-anchor-the-lexical-score.md)
## Verification Log
- 2026-08-20 · a2483fc* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestLexNorm|TestRankHybrid|TestRankRRF|TestBM25Scores|TestLexicalCoverage|TestAdaptiveWeight" -count=1'`
- 2026-08-20 · 9195d7c · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestLexNorm|TestRankHybrid|TestRankRRF|TestBM25Scores|TestLexicalCoverage|TestAdaptiveWeight" -count=1'`
- 2026-08-20 · 483911a · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestLexNorm|TestRankHybrid|TestRankRRF|TestBM25Scores|TestLexicalCoverage|TestAdaptiveWeight" -count=1'`
- 2026-08-20 · f044789 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestLexNorm|TestRankHybrid|TestRankRRF|TestBM25Scores|TestLexicalCoverage|TestAdaptiveWeight" -count=1'`
- 2026-08-20 · 013fa00 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestLexNorm|TestRankHybrid|TestRankRRF|TestBM25Scores|TestLexicalCoverage|TestAdaptiveWeight" -count=1'`
- 2026-08-28 · 312ca4d · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestLexNorm|TestRankHybrid|TestRankRRF|TestBM25Scores|TestLexicalCoverage|TestAdaptiveWeight" -count=1'` · acceptance-sha256:f3e87029844b74321b644ced772efe89267ee23d9f5e664e0690cacca7f84473

## Mutation Log
- 2026-08-28 · 090288c · mutant killed · exit 1 · `internal/palace/rank.go` · the anchor itself: without dividing by the query ceiling the ceiling transform is not anchored, and a candidate score again depends on which sibling won · acceptance-sha256:f3e87029844b74321b644ced772efe89267ee23d9f5e664e0690cacca7f84473
