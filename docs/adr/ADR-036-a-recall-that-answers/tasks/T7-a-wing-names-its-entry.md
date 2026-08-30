# Task ADR-036-T7: A wing reports its own entry point, resolved directly

**Depends-on:** T6, T2, T3
**Covers:** F-10, UC4-S1, UC4-S2
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `Service.EntryPoint`
**Consumes:** the derived-edge contract and marker column (T6), `kg.Resolution` (T2), `palace.WingPolicy` (T3)
**Data dependency:** hermetic

## Goal

Reaching a wing's taxonomy needs no id the server did not supply, and no graph walk.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/graphquery.go` | edit | resolve the entry point's edges directly |
| `internal/mcpserver/kg.go` | edit | register and expose it — the line that makes it DISCOVERABLE |
| `internal/palace/recallanswers_spec_test.go` | edit | two red tests |
| `internal/mcpserver/recallanswers_reach_test.go` | edit | the catalogue proof |

## Ordered Steps

1. Confirm all three tests are RED.
2. Resolve the entry record and its outgoing edges DIRECTLY. Do NOT use `am_traverse`: its `max_hops` is provably inert — `via` is an intersection carried forward, so hop >=2 can never add a node. Verified 2026-08-26 from the `wing_agentmemories` `llm_init` root (25 nodes, all hop <=1) and from a leaf drawer in the same room (10 nodes, all hop 1).
3. A wing with no entry point says so, distinguishably from an error — reuse T2's state vocabulary rather than inventing a second one.
4. Register the tool and assert it appears in the CATALOGUE with its arguments. A tool the handler serves and the catalogue omits is one no agent will ever call.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestAWingReportsItsOwnEntryPoint|TestEntryPointToolIsRegisteredAndDiscoverable' -count=1 2>&1 | tee /tmp/acc36t7.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t7.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestSearchResultRendersTheCorrectionMark|TestBootstrapToolIsRegisteredAndDiscoverable' 2>&1 | tee /tmp/acc36t7b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T7 does not depend on: T7's own 2 and its
ancestors' 14 still run, so a regression in what T7 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAWingReportsItsOwnEntryPoint` | `internal/palace/recallanswers_spec_test.go` | entry record and outgoing edges returned; a wing without one says so distinguishably | F-10, UC4-S1, UC4-S2 |
| `TestEntryPointToolIsRegisteredAndDiscoverable` | `internal/mcpserver/recallanswers_reach_test.go` | the tool is in the catalogue with its arguments | F-10 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two palace tests |
| 2 — something selects it | the mcpserver registration; mutation: unregister it — the palace tests stay green and the catalogue test goes red |
| 3 — the caller can discover it | the catalogue test IS this rung |
| 4 — it is used | whether any client kit stops hardcoding a root id — measured after T8, not here |

## Verification Log

- 2026-08-26 · fe0a696 · exit 1 · `set -o pipefail …` · acceptance-sha256:ae6a65a0229b000c2a2dd1b3ad4a29fab0e2df1b7779d61b3c99484464d2e4aa
  ```
  --- FAIL: TestAWingReportsItsOwnEntryPoint (0.00s)
      recallanswers_spec_test.go:449: F-10 not implemented: a wing must report its entry record and outgoing taxonomy edges, so reaching a taxonomy never needs an id the server did not supply
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.016s
  --- FAIL: TestEntryPointToolIsRegisteredAndDiscoverable (0.00s)
      recallanswers_reach_test.go:135: ADR-036 T7 not implemented: the entry-point tool is registered and appears in the catalogue with its arguments
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.016s
  FAIL
  ```
- 2026-08-26 · fe0a696* · exit 0 · `set -o pipefail …` · acceptance-sha256:ae6a65a0229b000c2a2dd1b3ad4a29fab0e2df1b7779d61b3c99484464d2e4aa

## Mutation Log

- 2026-08-26 · fe0a696* · mutant killed · exit 1 · `internal/mcpserver/kg.go` · declare the tool and never register it; the palace test stays green and only the catalogue check sees it · acceptance-sha256:ae6a65a0229b000c2a2dd1b3ad4a29fab0e2df1b7779d61b3c99484464d2e4aa
- 2026-08-26 · fe0a696* · mutant killed · exit 1 · `internal/palace/graphquery.go` · stop distinguishing a wing with no entry point from one whose entry point is merely empty · acceptance-sha256:ae6a65a0229b000c2a2dd1b3ad4a29fab0e2df1b7779d61b3c99484464d2e4aa

## Invariants

- No graph walk. A future reader must not "restore" traversal here without first deciding transitive-vs-confined.
- The absence vocabulary is T2's and the wing rule is T3's. Neither is reimplemented here.

## Risks

- T6 fixes the write path only, so on today's corpus the entry point still reaches almost nothing (97.1% orphans, measured 2026-08-26). T6 precedes this task so the edge CONTRACT is settled first; the coverage claim needs the deferred backfill and is not made here.

## Out of Scope

- Fixing `am_traverse` (deferred: docs/adr/BACKLOG.md)
- Defining the tier vocabulary (permanent: the server distinguishes eager from on-demand and does not bless particular names.)

## Stop Condition

Stop and ask if a wing turns out to need more than one entry point — that changes the shape of the result and of T8.
