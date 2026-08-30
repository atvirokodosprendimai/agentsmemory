# Task ADR-043-T2: An entry point that resolves reaches the mandatory tier

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `Bootstrap` returning `must.*` targets that live outside the root room
**Consumes:** none
**Data dependency:** hermetic

## Goal

`am_bootstrap` on a wing whose root carries `must.*` edges returns the drawers those edges name, so a caller cannot mistake the root room's own contents for the mandatory tier.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/bootstrap.go` | edit | Takes outgoing edges from the derived containment node and never examines `must.*` (ADR-036 T8, deliberate). That scoping is what makes a containment-edge backfill produce false reachability, and this task amends it |
| `internal/palace/bootstrap_musttier_test.go` | add | The failing test, fixture-driven: a root with `must.*` targets in OTHER rooms |
| `internal/mcpserver/drawers.go` | edit | The wire shape for the tier and its truncation report — a field a caller cannot discover is unreachable even when it is emitted, so the tool description names it |
| `internal/palace/graphquery.go` | none — read only | `EntryRoom` and `EntryPoint` are unchanged; this task changes what happens after the entry node resolves |

The line that SELECTS this is `Bootstrap`'s own edge walk: today it stops at containment. The mutation
below severs the `must.*` branch and the new test must go red — if it stays green, the test is
asserting on the root room's drawers, which is exactly the false reachability this task exists to
forbid.

## Ordered Steps

1. Write `internal/palace/bootstrap_musttier_test.go` and run it red: build a fixture wing with a root drawer in `EntryRoom`, three drawers in OTHER rooms, and `must.*` facts from the root's own drawer id to each. Assert `Bootstrap` returns all three. It fails today by construction, because `Bootstrap` never reads `must.*`.
2. Add a second red case in the same file for the trap: a wing whose derived containment edge exists but whose root carries NO `must.*` edges must not be reported as a complete bootstrap. Whatever field says so is the one a caller branches on.
3. Extend `Bootstrap` to follow `must.*` edges from the resolved root drawer's id, fetching each target by id. `ref.*` stays on demand — that is the ADR's Out of Scope, and making it eager reintroduces the response-size problem ADR-036 T8 measured.
4. Bound the tier against the response budget and populate the existing truncation report rather than silently cutting it: a tier that is quietly short is the same defect as a hit that is quietly partial.
5. Name the tier's field in the `am_bootstrap` tool description in `internal/mcpserver/drawers.go`, on a word boundary, so `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` can see it.
6. Re-run the fence green; keep step 1's red run in the Verification Log.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestBootstrapReachesTheMandatoryTier' -count=1 2>&1 | tee /tmp/adr043-t2.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr043-t2.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1
```

The new test runs alone first; the three suites that could otherwise carry the verdict are chained
after it. `internal/mcptest` is in the regression half deliberately — it drives the real MCP
transport, and a tier that exists in `palace` but never reaches the wire is this repository's most
shipped defect.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestBootstrapReachesTheMandatoryTier` | `internal/palace/bootstrap_musttier_test.go` | A root carrying `must.*` edges to drawers in other rooms bootstraps into all of them | — |
| `TestBootstrapReachesTheMandatoryTier/aContainmentEdgeAloneIsNotACompleteBootstrap` | `internal/palace/bootstrap_musttier_test.go` | A wing whose containment edge resolves but whose root has no `must.*` edges is reported distinguishably, not as `matched` with a short answer | — |
| `TestBootstrapReachesTheMandatoryTier/theTierIsBoundedAndSaysSo` | `internal/palace/bootstrap_musttier_test.go` | A tier larger than the response budget populates the truncation report rather than being silently cut | — |

Shapes the existing creation path can already produce, enumerated before writing assertions: a
`must.*` target whose drawer has been ended (ADR-038 — provenance is historical, so the tier must
report it rather than drop it silently); a target id that names no row at all; a target in a wing the
caller cannot read, which `am_entry_point` already drops into `refused` rather than listing; a
multi-chunk target, where returning one chunk is the partial-read defect one layer down; and a root
with `must.*` edges to LABELS rather than drawer ids, which is the shape this repository's own corpus
holds today and which T3 migrates.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestBootstrapReachesTheMandatoryTier` |
| 2 — something selects it | `Bootstrap`'s edge walk; the mutation severs the `must.*` branch and the test must go red |
| 3 — the caller can discover it | The tier's field is named in the `am_bootstrap` tool description, covered by `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` |
| 4 — it is used | Nothing measures this yet. `am_bootstrap` returns `unknown_term` on every wing of the local palace today; T3 is what makes a real call observable, and whether agents then use it is unmeasured |

## Mutation Log

## Invariants

- A wing with no `must.*` edges bootstraps exactly as it does today. This task is additive; it must not change the answer for any wing that has no mandatory tier.
- `ref.*` is not fetched eagerly.
- `EntryRoom` and `EntryPoint` are unchanged.
- An ended target is reported as ended, never dropped — ADR-038's rule that provenance is historical.

## Risks

- The tier inflates the response past the budget ADR-036 T8 measured. Mitigated by step 4 and its test; the measurement goes in the ADR's Follow-ups so `ref.*` can be decided on a number.
- The fixture cannot exhibit the defect, so the assertions are unfalsifiable however they are worded. Mitigated by step 1's red run being recorded before any code changes — a test that was never red proves nothing about a mechanism it was written beside.

## Stop Condition

Stop if extending `Bootstrap` requires `am_entry_point` to change its own contract — ADR-036 owns that
API and this record amends only T8's `must.*` scoping. A change to what `EntryPoint` returns is a
different decision and needs the owner.

## Out of Scope

- Correcting the served document — T1.
- Migrating this repository's corpus — T3.
- Following `ref.*` (deferred: Follow-ups, ADR-043 — decide once T2 has a measured response size for `must.*` alone).

## Verification Log
