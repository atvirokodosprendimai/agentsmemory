# Task ADR-003-T2: Print, persist and de-bias the evidence the flip is gated on

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `palace.ClosetDelta` — the preselected per-category paired statistic; `EvalCaseResult.PoolRank`; a `CandidateUnion` that pools the closet-ON head as well; the `<stem>.cells.json` run record
**Consumes:** `armBoosts` / `ArmHybridRerank` (T1)

## Goal

The run that decides this ADR prints the statistic the ADR gates on, records what produced it, and judges its real-query gold from a pool that is blind to the decision being taken.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | `EvalCaseResult` gains `PoolRank` — `Details` is appended for every case while `PoolRanks` skips absent ones (`eval.go:330`, `eval.go:368`), so today the two cannot be aligned by index and an unreachable case cannot be excluded from a paired statistic; and `CandidateUnion` (`eval.go:756`) pools four rankers that are all closet-OFF (`eval.go:820-831`), so it gains the closet-ON fused head |
| `internal/palace/evalstats.go` | edit | add `ClosetDelta(report, category)` — the preselected `hybrid+closet` minus `hybrid` comparison over one category, reusing `PairedDelta`; every verdict the table prints today is against its own best arm, which is selected from the same data |
| `internal/palace/evalstats_test.go` | edit | pin the admission rules on a synthetic report |
| `internal/palace/eval_test.go` | edit | pin that the judged pool contains what the closet-on head alone surfaces — `CandidateUnion` has no test today |
| `cmd/server/eval.go` | edit | print the closet block, carry the case file's provenance through `readCases` (`eval.go:830`, which drops the meta line), and write `<stem>.cells.json` — `writeResults` records created/pool/wing/cases/warnings/arms/details and nothing about the code or the ranking config it ran under (`eval.go:185-193`) |
| `cmd/server/eval_test.go` | edit | pin the printed block, the run record's fields, and that the record carries no case text |
| `cmd/server/evalreach_test.go` | add | **added during execution.** Every producer of run output must be CALLED from the command. All four functions this task adds were written, unit-tested and green while none of them was reachable from `runEval` — the block would never have printed and the evidence directory would have stayed empty. A unit test cannot catch that: it calls the function directly, which is precisely what the command was not doing |

## Ordered Steps

1. Write the failing tests first (TDD red) and commit them red: `TestClosetDeltaExcludesUnreachableAndAbsentCases`, `TestClosetDeltaIsScopedToOneCategory`, `TestCandidateUnionPoolsTheClosetHead`, `TestEvalPrintsPreselectedClosetDelta`, `TestRunRecordCarriesProvenanceAndNoCaseText`, `TestReadCasesKeepsProvenance`.
2. Add `PoolRank` to `EvalCaseResult` and populate it in `EvaluateWith` from the value `evalCase` already returns. Leave `EvalReport.PoolRanks` alone — the retrieval-ceiling printer reads it, and its exclusion of absent cases is correct there.
3. Add `ClosetDelta(report EvalReport, category string) ClosetCell` to `evalstats.go`. A case is admitted when its `Category` matches, its `PoolRank > 0`, and both arms scored it; the cell reports `Admitted`, `Unreachable`, `NoGold`, the point `DeltaMRR`, the `PairedDelta` interval, `DeltaRecall1`, and `Moved` — how many admitted cases the two arms ranked differently. Sign convention at the definition: Δ = closet minus no-closet, negative means the prior costs. The exclusions are the ADR's, fixed before the run; the counts are printed so no exclusion happens quietly.
4. Print the block from `printEvalTable`, one row per category present, with the admitted count, the exclusions, ΔMRR, the 95% paired interval, Δrecall@1 and `moved`. The caption says this comparison is preselected, unlike the `vs best` column whose baseline is chosen from the same table.
5. Pool the closet-ON fused head in `CandidateUnion`: `rankHybrid(query, docs, dists, closet)` with the boosts taken at full strength through T1's `closetBoostsAt(…, 1)`, added as one more `take(...)` beside the existing four. Today every pooled ranker passes `nil`, so a memory that only the closet prior would surface can never be judged relevant — which biases the `real` qrels in favour of the very conclusion this ADR is arguing for. Keep the id sort: the judge must not be able to infer which ranker proposed a candidate.
6. Replace `readCases` with `readCasesWithMeta`, returning the `caseFileMeta` line it currently drops, so a replayed run still records which generator wrote its questions. `loadOrGenerateCases` grows a fourth return value to carry it out to the run record; the generate path stamps `generatedMeta(c)` from the flags, which is what the generators already write into the case file. `readCases` is then unused and removed.
7. Write `<stem>.cells.json` beside the results file (`resultsPath` already derives the stem from `--cases`): commit sha and dirty flag from `runtime/debug.ReadBuildInfo` (`vcs.revision`, `vcs.modified`, recorded as `unknown` when the binary carries no VCS stamp), style, wing, corpus label, generator, case count, pool, served closet scale, BM25 weight, fusion mode, rerank configured/weight/pool, and the `ClosetDelta` cells. Queries and drawer ids stay out of it: this is the file the ADR's evidence directory holds, and the palace it is measured on is private.
8. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestClosetDeltaExcludesUnreachableAndAbsentCases|TestClosetDeltaIsScopedToOneCategory|TestCandidateUnionPoolsTheClosetHead|TestEvalPrintsPreselectedClosetDelta|TestRunRecordCarriesProvenanceAndNoCaseText|TestReadCasesKeepsProvenance" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestClosetDeltaExcludesUnreachableAndAbsentCases` | `internal/palace/evalstats_test.go` | a case with `PoolRank == 0` and an absent case contribute no delta, and both are counted in the printed exclusions | — |
| `TestClosetDeltaIsScopedToOneCategory` | `internal/palace/evalstats_test.go` | the statistic is computed over one category's cases only, never the whole run | — |
| `TestCandidateUnionPoolsTheClosetHead` | `internal/palace/eval_test.go` | a drawer that only the closet-boosted ordering promotes into the head is in the judged pool, exactly once | — |
| `TestEvalPrintsPreselectedClosetDelta` | `cmd/server/eval_test.go` | the printed block names the arm pair, the admitted count, the interval and the exclusions, per category | — |
| `TestRunRecordCarriesProvenanceAndNoCaseText` | `cmd/server/eval_test.go` | the cells file carries commit, closet scale, style, generator and the ranking config, and none of the case queries | — |
| `TestReadCasesKeepsProvenance` | `cmd/server/eval_test.go` | a replayed case file's generator and style survive into the run record | — |

## Invariants

- Nothing here changes ranking. `Search` is untouched and the arms score exactly as T1 left them.
- The exclusions are declared by the ADR, not by the run: `ClosetDelta` has no thresholds, no minimum case count, and no branch that depends on the sign of its own output.
- The cells file is derived, never hand-written. Every number the evidence directory quotes comes from a file the binary wrote.
- The judged pool stays blind: sorted by id, with no field saying which ranker proposed a candidate.

## Risks

- Pooling a fifth ranker adds judge calls for `--style real` — at most `perArm` more candidates per query before dedup, on the slowest generation path. Report the pooled count in the progress line so a longer run is legible rather than suspicious.
- Blind pooling removes the qrels' preference between the two closet settings, not their dependence on dense retrieval: a memory no ranker surfaces is still invisible to the gold. That limit is inherent to a judged eval and stays in the provenance, where `CandidateUnion`'s doc comment already puts it.
- `runtime/debug.ReadBuildInfo` carries no VCS stamp under `go run` or a test binary. The field records `unknown` rather than a guess, and T3 refuses a run recorded that way.

## Stop Condition

Stop if `ClosetDelta` cannot be computed because `hybrid` or `hybrid+closet` is missing from a report — that means T1 did not land, and a statistic assembled from whichever arms happen to be present is the failure this ADR exists to correct.

## Out of Scope

- Changing which arms exist or what they measure — T1 owns that.
- Any default value — T4 owns that, behind T3's table.
- A general preselected-comparison framework for other arm pairs (deferred: docs/adr/BACKLOG.md)
- Committing case files or full results JSON to the repo (permanent: they carry queries and drawer ids from a private palace; the cells file is the redacted record the evidence directory holds.)

## Verification Log
- 2026-08-20 · cf7584b* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestClosetDeltaExcludesUnreachableAndAbsentCases|TestClosetDeltaIsScopedToOneCategory|TestCandidateUnionPoolsTheClosetHead|TestEvalPrintsPreselectedClosetDelta|TestRunRecordCarriesProvenanceAndNoCaseText|TestReadCasesKeepsProvenance" -count=1'`
- 2026-08-20 · c845d96 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestClosetDeltaExcludesUnreachableAndAbsentCases|TestClosetDeltaIsScopedToOneCategory|TestCandidateUnionPoolsTheClosetHead|TestEvalPrintsPreselectedClosetDelta|TestRunRecordCarriesProvenanceAndNoCaseText|TestReadCasesKeepsProvenance" -count=1'`
- 2026-08-20 · 504c193 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestClosetDeltaExcludesUnreachableAndAbsentCases|TestClosetDeltaIsScopedToOneCategory|TestCandidateUnionPoolsTheClosetHead|TestEvalPrintsPreselectedClosetDelta|TestRunRecordCarriesProvenanceAndNoCaseText|TestReadCasesKeepsProvenance" -count=1'`
- 2026-08-20 · b94d68a · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestClosetDeltaExcludesUnreachableAndAbsentCases|TestClosetDeltaIsScopedToOneCategory|TestCandidateUnionPoolsTheClosetHead|TestEvalPrintsPreselectedClosetDelta|TestRunRecordCarriesProvenanceAndNoCaseText|TestReadCasesKeepsProvenance" -count=1'`
- 2026-08-28 · b0c758a · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestClosetDeltaExcludesUnreachableAndAbsentCases|TestClosetDeltaIsScopedToOneCategory|TestCandidateUnionPoolsTheClosetHead|TestEvalPrintsPreselectedClosetDelta|TestRunRecordCarriesProvenanceAndNoCaseText|TestReadCasesKeepsProvenance" -count=1'` · acceptance-sha256:1415dd0b5a5a94970be5ce7a0d970d5621f6dedd6f32c9b1b8350fa6c149d56a

## Mutation Log
- 2026-08-28 · eb5e779 · mutant survived · exit 0 · `internal/palace/evalstats.go` · an absent case has no gold to rank, so counting it turns an undefined delta into a zero and biases the statistic the ADR is gated on · acceptance-sha256:1415dd0b5a5a94970be5ce7a0d970d5621f6dedd6f32c9b1b8350fa6c149d56a
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · eb5e779* · mutant killed · exit 1 · `internal/palace/evalstats.go` · a case whose gold never made the pool must be excluded from the delta, not admitted with an undefined rank — the exclusion the cell reports as Unreachable · acceptance-sha256:1415dd0b5a5a94970be5ce7a0d970d5621f6dedd6f32c9b1b8350fa6c149d56a
