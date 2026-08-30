# Task ADR-036-T3: Facts reach the page, wing-resolved, in three states

**Depends-on:** T1, T2
**Covers:** F-1, F-2, F-8, F-9, F-18, UC1-S1, UC2-S1, UC2-S2
**Estimated scope:** L
**Owner:** unassigned
**Produces:** `Service.factsFor` (wing-resolved facts in three states) and `palace.WingPolicy` (the single authorization point)
**Consumes:** the fact-retrieval arm (T1), `kg.Resolution` (T2)
**Data dependency:** hermetic

## Goal

A question reaches a fact in its own wing, learns which OTHER wings hold matches it may query, and is told how many matches exist that cannot be placed at all.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | embed entity labels; resolve wing from provenance; build the three-state fact block |
| `internal/palace/palace.go` | edit | the fields carrying facts, the sibling pointer and the unlocatable count |
| `internal/palace/wingpolicy.go` | add | the ONE authorization point F-19 requires: given a viewer wing and a candidate, return LOCAL, FOREIGN or UNLOCATABLE. T5, T7 and T8 call it rather than filtering for themselves |
| `internal/palace/entityvectors.go` | add | the entity-label vector lifecycle: initial backfill, upsert on KG write, delete on KG delete — under its own namespace |
| `internal/mcpserver/drawers.go` | edit | render them — the line that makes them DISCOVERABLE |
| `internal/palace/recallanswers_spec_test.go` | edit | six red tests |
| `internal/mcpserver/recallanswers_reach_test.go` | edit | the render-site proof |

## Ordered Steps

1. Confirm all seven tests are RED.
2. Embed entity labels into the existing vector store under a DISTINCT namespace, and write the lifecycle explicitly: backfill existing entities once, upsert on `am_kg_add`, remove on delete. An index that is only ever written at backfill is stale by its second day.
3. Write `WingPolicy` as the single decision point and route the fact block through it. It resolves `source_drawer_id` into exactly three states — LOCAL, FOREIGN (name the wing), UNLOCATABLE (count it). Unresolvable provenance is never LOCAL. T5, T7 and T8 consume this rather than each writing a filter that agrees today and diverges later.
4. Return in-wing facts as a block BESIDE the drawer hits; name the derivable sibling wings; report the unlocatable count.
5. Assert drawer selection and order are byte-identical before and after.
6. Assert the POSITIVE case: a question that does not name the entity returns the in-wing fact. UC1-S1 was previously bound to a negative assertion that returning nothing satisfied.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestAQuestionReachesTheFactThatAnswersIt|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestAnUnlocatableFactIsCountedNotDropped|TestSearchResultRendersFactsAndTheSiblingPointer' -count=1 2>&1 | tee /tmp/acc36t3.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t3.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAWingReportsItsOwnEntryPoint|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestSearchResultRendersTheCorrectionMark|TestAddDrawerResultReportsItsEdge|TestEntryPointToolIsRegisteredAndDiscoverable|TestBootstrapToolIsRegisteredAndDiscoverable' 2>&1 | tee /tmp/acc36t3b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T3 does not depend on: T3's own 7 and its
ancestors' 5 still run, so a regression in what T3 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAQuestionReachesTheFactThatAnswersIt` | `internal/palace/recallanswers_spec_test.go` | a question not naming the entity returns the in-wing fact in a distinct block — the positive assertion UC1-S1 lacked | UC1-S1 |
| `TestAWingScopedRecallNeverReturnsAnotherWingsFact` | `internal/palace/recallanswers_spec_test.go` | no foreign wing's subject, predicate or object appears anywhere | F-1, UC2-S2 |
| `TestARecallNamesTheWingsThatHoldTheAnswer` | `internal/palace/recallanswers_spec_test.go` | every DERIVABLE sibling wing is named; omitting one that is derivable fails | F-2, UC2-S1 |
| `TestAFactsWingComesFromItsProvenance` | `internal/palace/recallanswers_spec_test.go` | the three states are distinguished; unresolvable is never local | F-8 |
| `TestAnUnlocatableFactIsCountedNotDropped` | `internal/palace/recallanswers_spec_test.go` | a match whose wing cannot be derived is counted, not silently dropped | F-18 |
| `TestReturningFactsDoesNotChangeDrawerRanking` | `internal/palace/recallanswers_spec_test.go` | drawer selection and order are unchanged | F-9 |
| `TestSearchResultRendersFactsAndTheSiblingPointer` | `internal/mcpserver/recallanswers_reach_test.go` | the block, the wings and the count reach the rendered result | F-2, F-18 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the six palace tests |
| 2 — something selects it | the `Search` call site; and the entity-vector lifecycle hook on `am_kg_add` — mutation: delete either and a test goes red |
| 3 — the caller can discover it | the mcpserver test; a palace test cannot observe a render site |
| 4 — it is used | T1's answerable-rate over the frozen corpus, whose baseline is 0% by construction |

## Verification Log

- 2026-08-26 · 12c5ad4 · exit 1 · `set -o pipefail …` · acceptance-sha256:7193b4e2232a1c0ccc7adee35e5233fa0a24511ac71abb45a8e5b26a9ddc92e5
  ```
      recallanswers_spec_test.go:380: UC1-S1 not implemented: a wing holding a current fact whose subject is semantically close to the question returns that fact in a distinct block beside the drawer hits, without the question naming the entity
  --- FAIL: TestAnUnlocatableFactIsCountedNotDropped (0.00s)
      recallanswers_spec_test.go:388: F-18 not implemented: a matching fact whose wing cannot be derived is reported as a count and attributed to no wing
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.022s
  --- FAIL: TestSearchResultRendersFactsAndTheSiblingPointer (0.00s)
      recallanswers_reach_test.go:81: ADR-036 T3 not implemented: am_search's rendered result carries the in-wing fact block, the derivable sibling wings, and the unlocatable count
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.019s
  FAIL
  ```
- 2026-08-26 · 12c5ad4* · exit 0 · `set -o pipefail …` · acceptance-sha256:7193b4e2232a1c0ccc7adee35e5233fa0a24511ac71abb45a8e5b26a9ddc92e5

## Mutation Log

- 2026-08-26 · 12c5ad4* · mutant killed · exit 1 · `internal/palace/wingpolicy.go` · claim a fact with no provenance for the searched wing — on this corpus that is the majority of facts, returned under the wrong project name · acceptance-sha256:7193b4e2232a1c0ccc7adee35e5233fa0a24511ac71abb45a8e5b26a9ddc92e5
- 2026-08-26 · 12c5ad4* · mutant killed · exit 1 · `internal/palace/factsfor.go` · silently drop facts that cannot be placed; silence is indistinguishable from nothing being filed · acceptance-sha256:7193b4e2232a1c0ccc7adee35e5233fa0a24511ac71abb45a8e5b26a9ddc92e5
- 2026-08-26 · 12c5ad4* · mutant killed · exit 1 · `internal/palace/wingpolicy.go` · let a sibling wings fact content cross the boundary instead of only its name · acceptance-sha256:7193b4e2232a1c0ccc7adee35e5233fa0a24511ac71abb45a8e5b26a9ddc92e5

## Invariants

- Ranking is untouched — F-9 is what stops this being confounded with a retrieval change.
- No foreign wing content, ever. The pointer names wings; it never carries facts.
- Three states, always. A fact is LOCAL, FOREIGN or UNLOCATABLE — never defaulted into the searched wing.

## Risks

- Provenance caps location at 46% (90 of 196 triples resolve, measured 2026-08-26), so most matches are UNLOCATABLE. That is why F-18 exists: the majority case must be reportable, not an edge case that gets dropped.
- The sibling pointer discloses which wings hold a match for a query. This workspace is one tenant, so that is inside the trust boundary — but it is a disclosure, and F-19 (T8) is where the single rule governing it lands.

## Out of Scope

- Adding a `wing` column to `kg_triples` (deferred: docs/adr/BACKLOG.md)
- Changing the reranker or fusion (permanent: ADR-030 — a blend that cannot tell confidence from noise — and ADR-034 own that area; both verified present 2026-08-26.)

## Stop Condition

Stop and ask if entity-label embeddings would need a different model from the drawer embedder — mixing embedding spaces in one namespace is a different decision.
