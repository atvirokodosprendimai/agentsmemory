# Task ADR-036-T1: The instrument: a fact answerable-rate with a 0% baseline

**Depends-on:** none
**Covers:** F-5, F-6
**Estimated scope:** M
**Owner:** unassigned
**Produces:** the fact-retrieval eval arm, a SYNTHETIC committed fixture, and `testdata/factcases-manifest-2026-08-26.json` (the redacted manifest)
**Consumes:** none
**Data dependency:** **Needs real data, and must not commit it.** The real case set is built from the live palace and stays UNTRACKED. What the repo carries is a synthetic fixture plus a redacted manifest — counts, hashes, provenance, date — and the fence runs against those. ADR-003 T2 closed this boundary permanently: case files carry queries and drawer ids from a private palace. An earlier draft of this task proposed committing the real corpus, which is how a `permanent` gets walked through — nothing sweeps one, so nothing would have resurfaced it.

## Goal

Fact retrieval becomes measurable against a frozen, dated corpus, so that no later task can report an improvement without an instrument — and so the instrument itself cannot drift.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | register the arm — the line that SELECTS it; an arm nothing registers appears in no table |
| `internal/palace/factcases.go` | add | load the frozen case set |
| `internal/palace/testdata/factcases-synthetic.jsonl` | add | the committed fixture: invented questions over invented triples, carrying no palace content |
| `internal/palace/testdata/factcases-manifest-2026-08-26.json` | add | the redacted record of the REAL run: case count, corpus hash, provenance, date — no case text, no ids |
| `.gitignore` | edit | keep the real case set untracked — the line that makes the privacy boundary mechanical rather than remembered |
| `internal/palace/eval_ssot_test.go` | edit | add the arm to the serviceForArm-nil set — it retrieves on its own path, since the shared pool holds drawers and this gold is a triple |
| `internal/palace/eval_test.go` | edit | add the arm to the not-fusion exception list; without it the arm falls through to the rerank branch and is scored under a name that does not describe it |
| `internal/repohygiene/adr036_fixtures_test.go` | edit | the privacy gate |
| `internal/palace/recallanswers_spec_test.go` | edit | the two red tests |

## Ordered Steps

1. Confirm `TestFactAnswerableRateIsMeasured` and `TestFactsOnThePageAreScoredByMRR` are RED.
2. Build the real case set from the live palace's 196 triples, drawing question phrasing from real `search_events` rows rather than the triples' own words — a case set written from the text it scores is circular. **Keep it untracked.**
3. Commit two things instead: a SYNTHETIC fixture the fence runs against, and a redacted manifest recording the real run's case count, corpus hash, provenance and date. The manifest is what makes the real measurement auditable without publishing it.
4. Make the boundary mechanical: `TestADR036FixturesCarryNoPrivatePalaceContent` fails if any tracked ADR-036 fixture carries case text, a drawer id or a triple id. Prose asking a future author to be careful is not a gate.
5. Register the arm in `evalArms`. Add the check that fails when that one line is deleted.
6. Report the answerable-rate as a fraction WITH its denominator. `12/30` and `0.40` are not the same claim when the corpus can change.
7. Assert the frozen file's recorded count matches the rows actually present, so a truncated corpus fails loudly instead of quietly reporting a rate over fewer cases.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/repohygiene/ -run 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR|TestADR036FixturesCarryNoPrivatePalaceContent' -count=1 2>&1 | tee /tmp/acc36t1.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t1.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestAFactLookupDistinguishesAbsenceFromFailure|TestAQuestionReachesTheFactThatAnswersIt|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestAnUnlocatableFactIsCountedNotDropped|TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAWingReportsItsOwnEntryPoint|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestKGQueryResultRendersResolutionState|TestSearchResultRendersFactsAndTheSiblingPointer|TestSearchResultRendersTheCorrectionMark|TestAddDrawerResultReportsItsEdge|TestEntryPointToolIsRegisteredAndDiscoverable|TestBootstrapToolIsRegisteredAndDiscoverable' 2>&1 | tee /tmp/acc36t1b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T1 does not depend on: T1's own 3 and its
ancestors' 0 still run, so a regression in what T1 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestFactAnswerableRateIsMeasured` | `internal/palace/recallanswers_spec_test.go` | the arm exists, is registered, and reports a fraction with its denominator over the frozen corpus | F-5 |
| `TestFactsOnThePageAreScoredByMRR` | `internal/palace/recallanswers_spec_test.go` | the arm scores ordering by MRR on the same paired bootstrap as every other arm | F-6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests |
| 2 — something selects it | the `evalArms` registration; mutation: delete it and the arm vanishes from every table |
| 3 — the caller can discover it | `eval --arms` lists it in `--help` |
| 4 — it is used | this task IS the rung-4 instrument for the fact-retrieval work in this ADR |

## Verification Log

- 2026-08-26 · 1a0635f · exit 1 · `set -o pipefail …` · acceptance-sha256:5b0379fe2aa32870d7f169bb96526f3e6f63a6e137df00d96b039cc8b42b489c
  ```
      recallanswers_spec_test.go:29: F-5 not implemented: a case set whose gold answers are kg_triples, and an arm reporting the fraction that reached the response. Baseline is 0% by construction
  --- FAIL: TestFactsOnThePageAreScoredByMRR (0.00s)
      recallanswers_spec_test.go:33: F-6 not implemented: once facts share the page, ordering is scored on the same paired bootstrap as every other arm
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.017s
  --- FAIL: TestADR036FixturesCarryNoPrivatePalaceContent (0.00s)
      adr036_fixtures_test.go:22: ADR-036 T1 not implemented: tracked testdata fixtures carry only redacted aggregates — counts, hashes, provenance, tokenizer — and no case text, drawer id or triple id
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/repohygiene	0.005s
  FAIL
  ```
- 2026-08-26 · 1a0635f* · exit 1 · `set -o pipefail …` · acceptance-sha256:5b0379fe2aa32870d7f169bb96526f3e6f63a6e137df00d96b039cc8b42b489c
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.029s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	0.423s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.013s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.019s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.487s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.024s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.034s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.027s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.023s
  FAIL
  ```
- 2026-08-26 · 1a0635f* · exit 0 · `set -o pipefail …` · acceptance-sha256:5b0379fe2aa32870d7f169bb96526f3e6f63a6e137df00d96b039cc8b42b489c

## Mutation Log

- 2026-08-26 · 1a0635f* · mutant killed · exit 1 · `internal/palace/eval.go` · delete the one line that registers the arm; without it the arm is declared, documented and appears in no table · acceptance-sha256:5b0379fe2aa32870d7f169bb96526f3e6f63a6e137df00d96b039cc8b42b489c
- 2026-08-26 · 1a0635f* · mutant survived · exit 0 · `internal/palace/testdata/factcases-synthetic.jsonl` · plant a palace-shaped identifier in a committed fixture; the privacy gate must refuse it · acceptance-sha256:5b0379fe2aa32870d7f169bb96526f3e6f63a6e137df00d96b039cc8b42b489c
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-26 · 1a0635f* · mutant killed · exit 1 · `internal/palace/testdata/factcases-synthetic.jsonl` · plant a real-shaped 64-hex palace identifier in a committed fixture; the privacy gate must refuse it · acceptance-sha256:5b0379fe2aa32870d7f169bb96526f3e6f63a6e137df00d96b039cc8b42b489c

## Invariants

- Baseline is 0% and stays stated — a non-zero result is only meaningful against it.
- The arm does not alter any existing arm's score.
- The corpus is dated and hashed. A rate quoted without its denominator is not a result.
- No palace content is ever committed. The repo carries synthetic fixtures and redacted aggregates; the real corpus stays untracked.

## Risks

- A case set built from the same triples it scores is circular; question phrasing comes from real `search_events` rows to break that.
- F-6 was originally worded "once facts share the page…", which presupposed T3 — a task cannot green a fact that depends on a later task. It now asserts a property of the instrument alone.
- A synthetic fixture cannot prove the real corpus behaves the same way. That is the cost of the privacy boundary and it is accepted deliberately: the manifest records the real run so the two can be compared by anyone with palace access.

## Out of Scope

- Improving the rate (deferred: this ADR's T3)
- Abstention (permanent: ADR-001 owns it and is Accepted with six pending tasks.)

## Stop Condition

Stop and ask if fewer than ~30 triples yield answerable questions. That floor is a judgement, not a power calculation: below it a single case moves the rate by more than 3 points, which exceeds the 0.01 MRR noise floor measured 2026-08-26 between two provably identical arms.
