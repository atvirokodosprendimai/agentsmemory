# Task ADR-044-T5: Make a page report how many hits the budget made it withhold

**Depends-on:** T4
**Covers:** F-7, UC1-S4
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `withheld` page field
**Consumes:** `partialWithFetchID` (T4), `coveredRunes` (T3)
**Data dependency:** hermetic — driven by more fixture memories than a fixture budget can carry.

## Goal

Make a page cut short by the response budget say how many hits it dropped, so a short page is legible as short rather than read as an exhausted corpus.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | `overBudget` is already counted in the render loop at `:929` and discarded. The page-level number is its sibling: hits the budget prevented from appearing at all, as distinct from hits it trimmed. **The two must not be conflated** — a trimmed hit is on the page and marked (T4); a withheld hit is not on the page and is otherwise invisible |
| `internal/mcpserver/drawers.go` | edit | Add `withheld` to the `am_search` result and name it in the tool description at `:817`, without which the key is undiscoverable by construction and `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` fails |
| `internal/mcpserver/kg.go` | read only | `out["withheld"] = map[string]int64{res.WithheldStatus: res.Withheld}` at `:223` is the existing vocabulary for this idea. Reuse the shape rather than inventing a second one; if the shapes must differ, say why here |
| `internal/mcpserver/bootstrap.go` | edit or record | `am_bootstrap` already carries a truncation report of its own via `res.WireShape()`. Either extend it to the same vocabulary, or record why its report already satisfies F-7. The ADR names this as a required decision, not an optional one |
| `internal/mcpserver/readcost_spec_test.go` | edit | Turn `TestF7APageReportsWhatItWithheld` green. **Tag STAYS** — F-4 is still red in this file |

## Ordered Steps

1. Confirm `TestF7APageReportsWhatItWithheld` is red for the right reason. Verified 2026-08-29; the binding names the trap explicitly — *"counting hits dropped for relevance as withheld — the count is about the BUDGET, not about ranking, which this spec does not touch"*.
2. Separate the two counters in the render loop: trimmed (already `overBudget`) and withheld (new). Do not reuse one variable for both.
3. Emit `withheld` on the page, following `kg.go:223`'s shape.
4. Turn F-7 green, with the ranking confusion as an explicit negative case: hits that never entered the candidate pool, or were dropped by `limit`, are NOT withheld. Only the budget withholds.
5. Decide `am_bootstrap` — extend or record, in this file's prose.

## Acceptance

```bash
set -o pipefail
go test -tags readcostspec ./internal/mcpserver/ -run 'TestF7APageReportsWhatItWithheld' -count=1 2>&1 | tee /tmp/adr044-t5.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t5.out && go test -tags readcostspec ./internal/mcpserver/ -run 'TestF1CoverageCountsEveryDisclosedRange|TestF2NoHitIsSilentlyPartial' -count=1 && go vet ./... && go test ./... -count=1
```

⚠ **This fence cannot observe step 5.** As in T3: `TestF7…` passes whether or not `am_bootstrap` was
decided, so **the sign-off for this task must name the `am_bootstrap` decision** — extended to the
`withheld` vocabulary, or a written reason its own truncation report already satisfies F-7. The ADR
makes this a required decision rather than an option; without the sign-off requirement it is a
requirement no gate can see.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF7APageReportsWhatItWithheld` | `internal/mcpserver/readcost_spec_test.go` | A budget-shortened page reports its withheld count; an exhausted corpus reports zero | F-7, UC1-S4 |
| `TestF7APageReportsWhatItWithheld/hits_dropped_by_limit_are_not_withheld` | same | The ranking confusion the binding names, as a subtest inside the fence | F-7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF7APageReportsWhatItWithheld` |
| 2 — something selects it | The counter is emitted from `registerSearch`'s result assembly (`drawers.go:815`). Mutation: report a constant zero and watch the test go red |
| 3 — the caller can discover it | The `am_search` description must name `withheld`; `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` matches on a word boundary, so a mention inside another word does not count |
| 4 — it is used | With no cursor, the count is the ONLY evidence a withheld hit existed. Whether callers act on it is what T1's rule measures |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->

## Invariants

- `withheld` counts hits the BUDGET excluded. Never hits excluded by `limit`, by relevance, or by `survivorsFrom`'s history filter.
- Zero withheld on a page that was cut is a defect, not an empty value.
- No cursor or offset is added; the count is the whole contract.

## Risks

- Conflating trimmed and withheld would make the number meaningless while looking correct. Mitigated by keeping two counters and by the negative subtest.
- A second withheld vocabulary diverging from `kg.go`'s. Mitigated by reusing that shape or recording the difference.

## Stop Condition

Stop if the render loop cannot know how many hits it withheld — if the candidate set is truncated before the budget is applied, the count is unknowable and reporting a guess would be worse than reporting nothing. Then the loop's structure has to change, which is larger than this task.

**What would make this criterion impossible to fail:** a fixture corpus small enough that the budget is never reached produces zero withheld on every page and the assertion passes vacuously. The fixture must be large enough to force a cut.

## Out of Scope

- Adding a cursor so withheld hits are resumable (permanent: declined in the spec's Non-Goals at Grill Log 8 — a second resumption contract for the job `am_get_drawer` already does is the cost this record exists to avoid)
- Removing the build tag — T6 owns it

## Verification Log

<!-- Tool-written by `adr-verify`. -->
