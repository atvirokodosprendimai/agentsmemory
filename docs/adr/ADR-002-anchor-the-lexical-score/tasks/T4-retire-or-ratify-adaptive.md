# Task ADR-002-T4: Retire or ratify the adaptive lexical weighting, from the evidence

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `LEX_NORM` config key, `Service.WithLexNorm`, the shipping lexical-normaliser default, and (outcome i) the removal of the coverage machinery
**Consumes:** `anchorEvidence` (T3)


> **Amended 2026-08-20.** This task was written for a world where every fusion arm carried the
> closet prior and an unboosted `no-closet` family was added beside it as a control. ADR-003 T1
> made the prior opt-in by arm name and put closet variants of the sweep and adaptive arms
> permanently out of scope, so the ten anchored arms are one unboosted family and there is no
> `no-closet` counterpart to any of them — the confound the control existed for is gone rather
> than being controlled for. Read every `no-closet` reference below as "the anchored arms",
> and every count of four intervals or two regimes as two intervals over one regime. The bar per
> interval is unchanged. See the amendment note in the parent ADR's Decision.

## Goal

Execute the outcome the committed evidence selects — delete the adaptive machinery, ratify it, or record that it did not earn the default — and bind the code's disposition to the evidence with a test that recomputes the ADR's deterministic shipping rule and its four selection-free deletion intervals rather than trusting a written verdict.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/lexnorm_disposition_test.go` | add | recomputes the shipping rule and the deletion trigger from the committed evidence and asserts the code follows them |
| `internal/palace/service.go` | edit | `WithLexNorm` plus the ranking switch (`service.go:710-722`) selecting the normaliser; under outcome (i) the `bm25Auto` / `bm25IDF` branches go |
| `internal/palace/service_test.go` | edit | the behavioural knob test: `LEX_NORM` must change what `Search` returns |
| `internal/palace/rank.go` | edit | outcome (i): delete `adaptiveBM25Weight`, `LexicalCoverage`, `LexicalCoverageIDF`, `rankHybridAdaptive`, `rankHybridAdaptiveIDF`; otherwise re-point them at the shipped normaliser |
| `internal/palace/rank_test.go` | edit | outcome (i): delete the four coverage tests that keep the deleted functions compiling (`TestLexicalCoverage*`, `TestAdaptiveWeightCollapsesWithoutSignal`) |
| `internal/palace/eval.go` | edit | the arm registry drops whichever arms the disposition retires |
| `internal/config/config.go` | edit | add `LexNorm`; under outcome (i), `BM25Weight` stops accepting `auto` / `auto-idf` and defaults to a fixed weight |
| `cmd/server/main.go` | edit | `--lex-norm` flag and its `LEX_NORM` env source beside `--bm25-weight` (`main.go:180-184`) |

## Ordered Steps

1. Write the failing tests first (TDD red):
   - `TestLexNormDispositionFollowsTheEvidence` in `internal/palace/lexnorm_disposition_test.go`. It loads the T3 evidence and computes two things, exactly as the ADR pre-registers them.

     **(a) The shipping rule.** Per corpus, per normaliser ∈ {`page-max`, `ceiling`, `saturating`}, the highest-MRR fixed swept weight among the **boosted** arms. The shipped normaliser is the one whose best-of-family MRR is highest on **both** corpora; corpora disagreeing on the argmax → `page-max`; `ceiling` beats `saturating` when their best-of-family `PairedDelta` contains zero; `page-max` beats an anchored normaliser on a tie. The shipped `BM25_WEIGHT` is the argmax over {0.20, 0.40, 0.60, `auto`, `auto-idf`} under the shipped normaliser, agreeing on both corpora, else `auto`. The `no-closet` arms are not read by the shipping rule at all — production boosts, so the default is chosen on the arms that boost.

     **(b) The deletion trigger.** Four intervals. For each ordered pair of corpora (A→B, B→A) and each regime ∈ {boosted, `no-closet`}: select on corpus A the best anchored fixed swept weight `w*` and the stronger anchored adaptive arm `m*` by MRR, then compute `PairedDelta(B.ranks[w*], B.ranks[m*])`. Every one of the four must exclude zero in favour of the fixed arm. **Selection and interval must never come from the same corpus** — that mistake is what this trigger replaced, and a test that quietly computes both on one corpus reintroduces it while staying green.

     The test then asserts the shipping default and the live `evalArms` registry match the branch those computations select. Commit it red.
   - `TestLexNormTriggerRejectsSingleCorpusSelection` in the same file, and behavioural rather than structural. Hand the trigger computation a synthetic evidence pair built so that the single-corpus comparison (select and test on the same corpus) fires while the cross-corpus one does not, and assert the trigger returns "does not fire". A test that merely asserts the selection file and the interval file differ cannot fail against the implementation it sits beside — it is the same shape as the predecessor of `TestLexicalIDFChangesWhatSearchReturns`, which passed while the flag was read by nothing.
   - `TestLexNormChangesWhatSearchReturns` in `internal/palace/service_test.go`, built exactly like `TestLexicalIDFChangesWhatSearchReturns` (`service_test.go:583`): file a fixture whose candidates force the two normalisers apart, then require `WithLexNorm("page-max")` and `WithLexNorm("ceiling")` to return **different scores** through `Search`. A test that only asserts the field was set passes while nothing reads it, which is the exact defect that test's comment records.
2. Encode the three branches exactly as the ADR pre-registers them, with no fourth: (i) all four deletion intervals exclude zero in favour of the fixed arm **and** the shipped normaliser is anchored → the adaptive arms must be absent from `evalArms` and the default `BM25Weight` must parse as a fixed float; (ii) the trigger does not fire and the rule's argmax weight is adaptive → the adaptive arms stay and the default is the rule's `(normaliser, auto|auto-idf)`; (iii) the trigger does not fire and the rule's argmax weight is fixed → the adaptive arms stay, the default is the rule's `(normaliser, fixed weight)`, and the evidence records that the coverage machinery did not earn the default. The case where the intervals fire but the rule ships `page-max` is outcome (iii) with the discrepancy recorded in the test's failure message.
3. Read the branch off the test's own computation, then make the code satisfy it. Under outcome (i) the deletions are the work: the functions, their arms, their tests, and the two accepted `BM25_WEIGHT` values.
4. Add `LexNorm` to `config.Config` with `page-max` as the value that reproduces today, add `Service.WithLexNorm` mirroring `WithLexicalIDF` (`service.go:262`), wire `--lex-norm` / `LEX_NORM` in `cmd/server/main.go`, and validate it the way `--fusion` is validated (`main.go:820`) so a typo refuses startup instead of silently ranking differently.
5. Update the stale comments the change falsifies: `HybridScore.Fused`'s "0.6*vecSim + 0.4*bm25Norm + closetBoost" (`rank.go:240`), `rankHybrid`'s "min-max normalized within the set" (`rank.go:275`) — which is min-max against the *theoretical* minimum and the observed maximum, and should say so — and the `--bm25-weight` usage string.
6. Run the acceptance command. `go vet ./...` compiles the whole module, so any reference left behind by a deletion fails there.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'set -e; gofmt -l internal/palace internal/config cmd/server | grep -q . && exit 1; go vet ./...; go test ./internal/palace/ ./internal/config/ ./cmd/server/ -run "TestLexNorm|TestAnchorEvidence|TestSearch|TestRankHybrid|TestLexicalIDFChangesWhatSearchReturns" -count=1 2>&1 | tee /tmp/adr002-t4.out; if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr002-t4.out; then exit 1; fi'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestLexNormDispositionFollowsTheEvidence` | `internal/palace/lexnorm_disposition_test.go` | the shipping default and the arm registry match the branch the committed evidence selects under the deterministic rule and the four cross-corpus intervals | — |
| `TestLexNormTriggerRejectsSingleCorpusSelection` | `internal/palace/lexnorm_disposition_test.go` | on synthetic evidence where selecting and testing on one corpus would fire but cross-corpus transfer does not, the trigger does not fire — the guard that can actually fail if selection and interval are collapsed onto one corpus | — |
| `TestLexNormChangesWhatSearchReturns` | `internal/palace/service_test.go` | `LEX_NORM` reaches the ranking path: two normalisers produce different scores through `Search` | — |
| `TestLexNormConfigDefaultRoundTrips` | `internal/palace/lexnorm_disposition_test.go` | `config.Default()`'s lexical settings map to the `Service` the composition root builds, so the knob and the code cannot disagree | — |

## Invariants

- No orphaned helper: if an arm goes, the function behind it goes with it. `LexicalCoverage` and `LexicalCoverageIDF` are exported and referenced by four tests in `rank_test.go`, so a half-deletion fails to compile under the `go vet ./...` that leads the acceptance fence.
- `LEX_NORM=page-max` reproduces pre-ADR ranking byte-for-byte, whatever the default becomes.
- Retrieval is untouched: the candidate pool, its size, and the distance gate are the same before and after.
- The disposition test computes every interval with `PairedDelta` over committed ranks — no hand-entered MRR, no verdict read from prose.

## Risks

- Under outcome (i) the deletion is not env-revertable — it is a git revert. Mitigated by the trigger spanning both corpora, both directions and both boost regimes, and by the deletion landing as its own commit so the revert is one operation.
- The trigger may never fire at n=40 and n=30, and the honest response is outcome (iii). The temptation at this point in the chain is to relax it to the single-corpus comparison that would have fired; `TestLexNormTriggerRejectsSingleCorpusSelection` exists because that temptation arrives exactly when the code deletion is the only remaining work, and it is built to fail on the relaxed version rather than to describe the strict one.
- A default flip changes the `score` an agent sees on `am_search` (`internal/mcpserver/drawers.go:300`). Nothing consumes it as an absolute; the field's doc comment must be corrected in step 5 rather than left describing arithmetic that no longer runs.

## Stop Condition

Stop and ask if the deletion trigger's four intervals **split by regime** — firing in the boosted regime and not in the `no-closet` one, or the reverse. That means the closet prior, not the lexical weighting, is producing the result, which is ADR-003's question rather than this one's, and the two ADRs need to be sequenced in the open instead of one silently deleting code on the other's effect. Stop too if the T3 evidence does not carry two separable corpora, because the cross-corpus trigger then cannot be computed at all and the only thing available is the single-corpus comparison this ADR rejected.

## Out of Scope

- Global corpus-wide IDF (deferred: docs/adr/ADR-002-anchor-the-lexical-score.md)
- Recalibrating the closet boost against the rescaled fused range (deferred: docs/adr/ADR-002-anchor-the-lexical-score.md)
- A selection-aware bootstrap that would let the trigger be read on one corpus — this task uses cross-corpus transfer instead, and the bootstrap version is an ADR follow-up.
- Any change to the cross-encoder blend or the reranked arms.

## Verification Log

## Mutation Log
