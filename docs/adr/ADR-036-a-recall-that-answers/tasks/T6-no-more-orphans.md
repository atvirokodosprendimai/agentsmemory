# Task ADR-036-T6: Every new drawer is REACHABLE, and derived edges say so

**Depends-on:** none
**Covers:** F-11, UC5-S1, UC5-S2
**Estimated scope:** L
**Owner:** unassigned
**Produces:** the derived-edge marker column, and the derived-edge contract
**Consumes:** none
**Data dependency:** hermetic

## Goal

A filed drawer is reachable BY TRAVERSAL from its wing's entry point, and a server-derived edge is distinguishable from an authored one.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00028_kg_triples_derived.sql` | add | nullable marker; `00027` is the highest on any branch, verified 2026-08-26 |
| `internal/palace/kg.go` | edit | carry the marker |
| `internal/palace/service.go` | edit | attach the edge at write time — the line that SELECTS it |
| `internal/mcpserver/drawers.go` | edit | report it on `am_add_drawer` — named explicitly, because this task previously promised the field while naming no MCP file |
| `internal/palace/recallanswers_spec_test.go` | edit | the red test |
| `internal/mcpserver/recallanswers_reach_test.go` | edit | the render-site proof |

## Ordered Steps

1. Confirm both tests are RED.
2. **Define the edge contract before writing it**, because "attach an edge" is satisfiable by a self-loop that makes nothing reachable: SUBJECT is the wing's room-or-entry node, PREDICATE is a fixed reserved verb, OBJECT is the new drawer's id, and the attachment root is the wing entry point T7 resolves. The drawer must end up as a triple OBJECT — 0 drawers are today, measured 2026-08-26.
3. Add migration `00028` with a `-- +goose Down`.
4. An authored edge always wins; a derived edge never overwrites one.
5. Assert REACHABILITY, not existence: walk from the entry point and require the new drawer to be found. An existence assertion passes on a self-loop.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAddDrawerResultReportsItsEdge' -count=1 2>&1 | tee /tmp/acc36t6.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t6.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR|TestAFactLookupDistinguishesAbsenceFromFailure|TestAQuestionReachesTheFactThatAnswersIt|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestAnUnlocatableFactIsCountedNotDropped|TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestAWingReportsItsOwnEntryPoint|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestKGQueryResultRendersResolutionState|TestSearchResultRendersFactsAndTheSiblingPointer|TestSearchResultRendersTheCorrectionMark|TestEntryPointToolIsRegisteredAndDiscoverable|TestBootstrapToolIsRegisteredAndDiscoverable|TestADR036FixturesCarryNoPrivatePalaceContent' 2>&1 | tee /tmp/acc36t6b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T6 does not depend on: T6's own 2 and its
ancestors' 0 still run, so a regression in what T6 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked` | `internal/palace/recallanswers_spec_test.go` | a drawer filed with no edge becomes reachable from the wing entry point and is marked derived; an authored edge is not overwritten | F-11, UC5-S1, UC5-S2 |
| `TestAddDrawerResultReportsItsEdge` | `internal/mcpserver/recallanswers_reach_test.go` | `am_add_drawer`'s rendered result says whether the drawer has an edge and whether it was derived | F-11 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the palace test |
| 2 — something selects it | the `Service.Add` call site; mutation: remove it and drawers file as orphans again |
| 3 — the caller can discover it | the mcpserver test |
| 4 — it is used | orphan rate per wing, expected to fall from the 97.1% measured 2026-08-26 — for drawers filed AFTER this lands |

## Verification Log

- 2026-08-26 · 1f282da · exit 1 · `set -o pipefail …` · acceptance-sha256:fd51152ec40ae6e3037bbdae87c5d067e6cc46569328c2ddcc0a87e53b50a545
  ```
  --- FAIL: TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked (0.00s)
      recallanswers_spec_test.go:175: F-11 not implemented: every drawer gets an edge at write time; a derived edge is marked as derived and never overwrites an authored one
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.018s
  --- FAIL: TestAddDrawerResultReportsItsEdge (0.00s)
      recallanswers_reach_test.go:92: ADR-036 T6 not implemented: am_add_drawer's rendered result says whether the drawer has an edge and whether it was derived
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.018s
  FAIL
  ```
- 2026-08-26 · 1f282da* · exit 0 · `set -o pipefail …` · acceptance-sha256:fd51152ec40ae6e3037bbdae87c5d067e6cc46569328c2ddcc0a87e53b50a545

## Mutation Log

- 2026-08-26 · 1f282da* · mutant killed · exit 1 · `internal/palace/kg.go` · turn the containment edge into a self-loop: the row still exists and is still marked derived, but nothing can traverse to the drawer · acceptance-sha256:fd51152ec40ae6e3037bbdae87c5d067e6cc46569328c2ddcc0a87e53b50a545
- 2026-08-26 · 1f282da* · mutant inconclusive · exit 1 · `internal/palace/kg.go` · stop deferring to an authored edge; a server guess would sit beside a human decision as though equivalent · acceptance-sha256:fd51152ec40ae6e3037bbdae87c5d067e6cc46569328c2ddcc0a87e53b50a545
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-08-26 · 1f282da* · mutant survived · exit 0 · `internal/palace/kg.go` · invert the deference: yield to a derived edge instead of an authored one, so a server guess overwrites a human decision · acceptance-sha256:fd51152ec40ae6e3037bbdae87c5d067e6cc46569328c2ddcc0a87e53b50a545
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-26 · 1f282da* · mutant killed · exit 1 · `internal/palace/kg.go` · invert the deference: yield to a derived edge instead of an authored one, so a server guess overwrites a human decision · acceptance-sha256:fd51152ec40ae6e3037bbdae87c5d067e6cc46569328c2ddcc0a87e53b50a545

## Invariants

- A derived edge is always distinguishable from an authored one — otherwise the noise it may introduce is unmeasurable and unremovable.
- The test proves TRAVERSAL, not the presence of a row.
- ADR-016's entity stamping is untouched.

## Risks

- **This task fixes the write path only.** The 1,928 existing orphans stay orphaned, so the live corpus remains ~97% unreachable after T6 completes. T7's dependency on T6 is therefore about the CONTRACT being defined before an entry point indexes against it, not about coverage — the coverage claim needs the backfill, which is deferred.
- Derived edges invent taxonomy the writer did not choose. The marker is what keeps that measurable and reversible.

## Out of Scope

- Backfilling edges for the 1,928 existing orphans (deferred: docs/adr/BACKLOG.md)
- Repairing the 16 dangling `source_drawer_id` pointers (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask before inventing the reserved predicate name if no existing vocabulary fits — a verb the whole corpus will carry is a product decision.
