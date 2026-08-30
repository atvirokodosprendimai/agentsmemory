# Task ADR-036-T5: A corrected record arrives carrying its correction

**Depends-on:** T3
**Covers:** F-3, UC3-S1, UC3-S2
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `kg.CorrectionsFor` (the incoming three-predicate sweep)
**Consumes:** `Service.factsFor` and `palace.WingPolicy` (T3)
**Data dependency:** hermetic

## Goal

A record that has been retracted, superseded or qualified is returned WITH that correction — through one sweep that the bootstrap will reuse rather than reimplement.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/kg.go` | add | `CorrectionsFor` — the ONE incoming sweep, so T8 consumes it instead of writing a second one that can disagree |
| `internal/palace/memory_search.go` | edit | call it at collapse time |
| `internal/mcpserver/drawers.go` | edit | render the mark — the line that makes it DISCOVERABLE |
| `internal/palace/recallanswers_spec_test.go` | edit | the red test |
| `internal/mcpserver/recallanswers_reach_test.go` | edit | the render-site proof |

## Ordered Steps

1. Confirm both tests are RED.
2. Write `CorrectionsFor` as the single resolver, reading `retracts`, `supersedes` and `qualifies` INCOMING. Outgoing traversal structurally cannot see a correction.
3. Assert all THREE predicates in a table-driven test. Naming three and asserting one is how `qualifies` was missed on 2026-08-25, when a session that ran only `retracts` shipped a pointer to an ADR that was not on `main`.
4. Return the record in its normal rank position, carrying the edge and the replacement id. Marking, not hiding — a retraction can itself be wrong.
5. Route the replacement id through `WingPolicy` before rendering it. A correction target in another wing is a leak that no subject/predicate/object check would catch.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestACorrectedRecordArrivesCarryingItsCorrection|TestSearchResultRendersTheCorrectionMark' -count=1 2>&1 | tee /tmp/acc36t5.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t5.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAWingReportsItsOwnEntryPoint|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestAddDrawerResultReportsItsEdge|TestEntryPointToolIsRegisteredAndDiscoverable|TestBootstrapToolIsRegisteredAndDiscoverable' 2>&1 | tee /tmp/acc36t5b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T5 does not depend on: T5's own 2 and its
ancestors' 12 still run, so a regression in what T5 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestACorrectedRecordArrivesCarryingItsCorrection` | `internal/palace/recallanswers_spec_test.go` | table-driven over all three predicates: each marks the record with its edge and replacement id, read incoming | F-3, UC3-S1, UC3-S2 |
| `TestSearchResultRendersTheCorrectionMark` | `internal/mcpserver/recallanswers_reach_test.go` | the mark reaches the rendered hit | F-3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the palace test |
| 2 — something selects it | the collapse-time call; mutation: remove it and corrections stop travelling |
| 3 — the caller can discover it | the mcpserver test |
| 4 — it is used | how many recalls return a marked record — currently unmeasurable because nothing marks |

## Verification Log

- 2026-08-26 · ffc1d01 · exit 1 · `set -o pipefail …` · acceptance-sha256:308880c7faa47cf66150c6718ec3c639bd1bfb52ece3a5f3798a2a2c488aebeb
  ```
  --- FAIL: TestACorrectedRecordArrivesCarryingItsCorrection (0.00s)
      recallanswers_spec_test.go:62: F-3 not implemented: a hit that is the object of retracts/supersedes/qualifies must carry that edge and the replacement id — marked, never hidden
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.016s
  --- FAIL: TestSearchResultRendersTheCorrectionMark (0.00s)
      recallanswers_reach_test.go:91: ADR-036 T5 not implemented: a superseded record's correction edge and replacement id appear in the rendered hit
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.017s
  FAIL
  ```
- 2026-08-26 · ffc1d01* · exit 0 · `set -o pipefail …` · acceptance-sha256:308880c7faa47cf66150c6718ec3c639bd1bfb52ece3a5f3798a2a2c488aebeb

## Mutation Log

- 2026-08-26 · ffc1d01* · mutant survived · exit 0 · `internal/palace/kg.go` · read the sweep outgoing: a correction attaches to the record it corrects as an INCOMING edge, so this direction can never see one · acceptance-sha256:308880c7faa47cf66150c6718ec3c639bd1bfb52ece3a5f3798a2a2c488aebeb
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-26 · ffc1d01* · mutant survived · exit 0 · `internal/palace/kg.go` · sweep only retracts; on 2026-08-25 the edge that mattered was a qualifies and a session that ran only retracts shipped a bad pointer · acceptance-sha256:308880c7faa47cf66150c6718ec3c639bd1bfb52ece3a5f3798a2a2c488aebeb
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-26 · ffc1d01* · mutant killed · exit 1 · `internal/palace/kg.go` · attach the correction to the record doing the correcting instead of the one being corrected · acceptance-sha256:308880c7faa47cf66150c6718ec3c639bd1bfb52ece3a5f3798a2a2c488aebeb
- 2026-08-26 · ffc1d01* · mutant killed · exit 1 · `internal/palace/kg.go` · sweep only retracts; on 2026-08-25 the edge that mattered was a qualifies · acceptance-sha256:308880c7faa47cf66150c6718ec3c639bd1bfb52ece3a5f3798a2a2c488aebeb

## Invariants

- Nothing is hidden and nothing is demoted. Rank is unchanged, which keeps this separable from F-9.
- ONE sweep. T8 consumes `CorrectionsFor`; two implementations of the same rule diverge on the path nobody tested.

## Risks

- A live specimen exists: `drawers.entities is_written_only_by am_mine (retired)` was contradicted by a later fact while both read `current: true`. Use that pair as the fixture.

## Out of Scope

- Demoting or excluding superseded records (permanent: a retraction can itself be wrong, and a ranking input is a signal rather than a gate.)

## Stop Condition

Stop and ask if a record carries two incoming corrections with conflicting replacements — that ordering question is not decided here.
