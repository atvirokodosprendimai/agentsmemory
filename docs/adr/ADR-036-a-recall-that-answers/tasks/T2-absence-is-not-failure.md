# Task ADR-036-T2: A lookup that distinguishes four outcomes, not two

**Depends-on:** none
**Covers:** F-12
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `kg.Resolution` (the four-state lookup outcome)
**Consumes:** none
**Data dependency:** hermetic

## Goal

A fact lookup reports WHICH of four things happened, so a caller can tell "nothing is filed" from "I could not look".

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/kg.go` | edit | return the resolution state beside the count |
| `internal/mcpserver/kg.go` | edit | render it — the line that makes it DISCOVERABLE to a caller |
| `internal/palace/recallanswers_spec_test.go` | edit | the red test |
| `internal/mcpserver/recallanswers_reach_test.go` | edit | the render-site proof |

## Ordered Steps

1. Confirm `TestAFactLookupDistinguishesAbsenceFromFailure` and `TestKGQueryResultRendersResolutionState` are RED.
2. Define the outcomes by STAGE so they are exhaustive and mutually exclusive, which "four outcomes" was not — "known term, no triples" and "lookup ran, no candidates" overlapped. Stage A, term resolution: `term_known` | `term_unknown`. Stage B, reached only when `term_known`: `matched` | `no_triples`. That yields exactly three renderable states: **matched**, **known_term_no_triples**, **unknown_term**.
3. Backend failure stays OUT-OF-BAND as it is today (`internal/mcpserver/kg.go` returns an error rather than a result), so it is not a fourth renderable state. What the test must prove is that an injected backend failure does NOT fail open into any of the three — today an unreachable backend and an unknown entity both read as `count: 0`.
4. Observed 2026-08-26: `am_kg_query` returned `count: 0` with no error for a nonexistent entity AND a nonexistent predicate — both collapse to outcome 2 today and are indistinguishable from outcome 1. Reproduce both.
5. Test the backend-failure state by INJECTING a failure, not by assuming errors already propagate.
6. Render the state in the tool result. A field the handler sets and no renderer emits is invisible to every agent, and no behavioural test can see that.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestAFactLookupDistinguishesAbsenceFromFailure|TestKGQueryResultRendersResolutionState' -count=1 2>&1 | tee /tmp/acc36t2.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t2.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR|TestAQuestionReachesTheFactThatAnswersIt|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestAnUnlocatableFactIsCountedNotDropped|TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAWingReportsItsOwnEntryPoint|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestSearchResultRendersFactsAndTheSiblingPointer|TestSearchResultRendersTheCorrectionMark|TestAddDrawerResultReportsItsEdge|TestEntryPointToolIsRegisteredAndDiscoverable|TestBootstrapToolIsRegisteredAndDiscoverable|TestADR036FixturesCarryNoPrivatePalaceContent' 2>&1 | tee /tmp/acc36t2b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T2 does not depend on: T2's own 2 and its
ancestors' 0 still run, so a regression in what T2 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAFactLookupDistinguishesAbsenceFromFailure` | `internal/palace/recallanswers_spec_test.go` | the three renderable states are exhaustive and mutually exclusive, and an injected backend failure surfaces as an error rather than collapsing into any of them | F-12 |
| `TestKGQueryResultRendersResolutionState` | `internal/mcpserver/recallanswers_reach_test.go` | the state reaches the rendered tool result, not only the Go struct | F-12 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the palace test |
| 2 — something selects it | the mcpserver render site; mutation: delete the render line — the palace test stays GREEN and the mcpserver test goes red, which is the whole reason it exists |
| 3 — the caller can discover it | the field appears in the tool RESULT |
| 4 — it is used | T3 consumes it; a sibling pointer built on a fail-open lookup cannot be trusted |

## Verification Log

- 2026-08-26 · d9af496 · exit 1 · `set -o pipefail …` · acceptance-sha256:bb1c80b4ed2fcbcfb4673cd182997ba6deaaa227f4e6d36b142647d62125a825
  ```
  --- FAIL: TestAFactLookupDistinguishesAbsenceFromFailure (0.00s)
      recallanswers_spec_test.go:170: F-12 not implemented: observed 2026-08-26 — am_kg_query returns count:0 with no error for a nonexistent entity AND a nonexistent predicate, so F-2's pointer cannot be trusted until absence and failure differ
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.023s
  --- FAIL: TestKGQueryResultRendersResolutionState (0.00s)
      recallanswers_reach_test.go:23: ADR-036 T2 not implemented: am_kg_query's rendered result distinguishes an unresolved entity or predicate from a real empty match
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.022s
  FAIL
  ```
- 2026-08-26 · d9af496* · exit 0 · `set -o pipefail …` · acceptance-sha256:bb1c80b4ed2fcbcfb4673cd182997ba6deaaa227f4e6d36b142647d62125a825

## Mutation Log

- 2026-08-26 · d9af496* · mutant killed · exit 1 · `internal/palace/kg.go` · collapse unknown_term into known_term_no_facts for an entity — the exact fail-open measured 2026-08-26 · acceptance-sha256:bb1c80b4ed2fcbcfb4673cd182997ba6deaaa227f4e6d36b142647d62125a825
- 2026-08-26 · d9af496* · mutant killed · exit 1 · `internal/mcpserver/kg.go` · render the state under a name no agent looks for; the palace test stays green and only the rung-3 render check sees it · acceptance-sha256:bb1c80b4ed2fcbcfb4673cd182997ba6deaaa227f4e6d36b142647d62125a825

## Invariants

- A real empty result still reports zero — this adds a signal, it does not change counts.
- Three renderable states, exhaustive and mutually exclusive, plus an out-of-band failure. Collapsing "unknown term" into "no match" is the defect being fixed; adding an overlapping fourth would be a different one.

## Risks

- Callers may read "unresolved" as an error and stop. It is not: the call still succeeds and the field is advisory.

## Out of Scope

- Validating entity spelling on write (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask if `am_kg_query` has callers beyond the MCP layer that a wider result shape would break.
