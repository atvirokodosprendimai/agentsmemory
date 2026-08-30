# Task ADR-036-T4: Both entity vocabularies, and an ended fact is never current

**Depends-on:** T3
**Covers:** F-4, F-7, UC1-S2
**Estimated scope:** M
**Owner:** unassigned
**Produces:** none
**Consumes:** `Service.factsFor` (T3)
**Data dependency:** **Needs real data** for the stop condition only: the answerable-rate comparison runs against T1's frozen corpus. The fence is hermetic.

## Goal

A fact is reachable through an extracted term as well as a KG entity, and an ended fact is never presented as current.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | match the query against `drawers.entities` as well as `kg_entities` |
| `internal/palace/recallanswers_spec_test.go` | edit | two red tests |

## Ordered Steps

1. Confirm both tests are RED.
2. Match against both vocabularies at query time, read-only. No schema change, no write-path change.
3. Filter `valid_to`. Precedent: `am_kg_query` already defaults to `status=current` via `kgQueryDefaultStatus`, so this extends an existing default rather than inventing one.
4. Run T1's arm with the second vocabulary on and off, and RECORD both rates with their denominator in the sign-off. The stop condition below is only checkable if that number exists.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ -run 'TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent' -count=1 2>&1 | tee /tmp/acc36t4.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t4.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestACorrectedRecordArrivesCarryingItsCorrection|TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAWingReportsItsOwnEntryPoint|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestSearchResultRendersTheCorrectionMark|TestAddDrawerResultReportsItsEdge|TestEntryPointToolIsRegisteredAndDiscoverable|TestBootstrapToolIsRegisteredAndDiscoverable' 2>&1 | tee /tmp/acc36t4b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T4 does not depend on: T4's own 2 and its
ancestors' 12 still run, so a regression in what T4 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestFactLookupMatchesBothEntityVocabularies` | `internal/palace/recallanswers_spec_test.go` | a fact whose subject appears only in `drawers.entities` is reachable by a question naming that term | F-4 |
| `TestAnEndedFactIsNeverPresentedAsCurrent` | `internal/palace/recallanswers_spec_test.go` | a fact with non-empty `valid_to` is not in the current block | F-7, UC1-S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests |
| 2 — something selects it | the second-vocabulary match; mutation: remove it and the extracted-term test goes red |
| 3 — the caller can discover it | n/a: no declared interface — the behaviour is inside an existing call, and this is stated rather than left blank |
| 4 — it is used | T1's answerable-rate, split by which vocabulary matched |

## Verification Log

- 2026-08-26 · 7a2bbc5 · exit 1 · `set -o pipefail …` · acceptance-sha256:ef1ef68a293acc1d7f1145880433584ab732f4e9929be7ca01e70b04925822c9
  ```
  --- FAIL: TestFactLookupMatchesBothEntityVocabularies (0.00s)
      recallanswers_spec_test.go:66: F-4 not implemented: fact lookup must match a query against kg_entities AND drawers.entities, read-only
  --- FAIL: TestAnEndedFactIsNeverPresentedAsCurrent (0.00s)
      recallanswers_spec_test.go:186: F-7 not implemented: a fact with a non-empty valid_to must not be presented as current
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.019s
  FAIL
  ```
- 2026-08-26 · 7a2bbc5* · exit 0 · `set -o pipefail …` · acceptance-sha256:ef1ef68a293acc1d7f1145880433584ab732f4e9929be7ca01e70b04925822c9

## Mutation Log

- 2026-08-26 · 7a2bbc5* · mutant inconclusive · exit 1 · `internal/palace/factsfor.go` · stop joining the extracted vocabulary; a fact reachable only through a term the extractor found becomes unreachable · acceptance-sha256:ef1ef68a293acc1d7f1145880433584ab732f4e9929be7ca01e70b04925822c9
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-08-26 · 7a2bbc5* · mutant killed · exit 1 · `internal/palace/factsfor.go` · present ended facts as current; 14 already-expired facts on the live palace would be served as live answers · acceptance-sha256:ef1ef68a293acc1d7f1145880433584ab732f4e9929be7ca01e70b04925822c9
- 2026-08-26 · 7a2bbc5* · mutant killed · exit 1 · `internal/palace/factsfor.go` · stop joining the extracted vocabulary; a fact reachable only through a term the extractor found becomes unreachable · acceptance-sha256:ef1ef68a293acc1d7f1145880433584ab732f4e9929be7ca01e70b04925822c9

## Invariants

- The two vocabularies are NOT merged. This is a read-time join only.
- 945 of 1,985 drawers carry entities (47.6%, measured 2026-08-26), so the second vocabulary has material to work with.

## Risks

- Frequency-extracted terms are noisier than authored names and may pull irrelevant facts. T1 measures whether they help or hurt — which is why the on/off comparison is a required step, not an optional one.

## Out of Scope

- Unifying the vocabularies at the write path (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask if the recorded on/off comparison shows the join LOWERS the answerable-rate by more than the corpus's single-case granularity — that would mean it costs more than it buys.
