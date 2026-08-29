# Task ADR-044-T4: Make every incomplete hit say so, with its full length and fetch id

**Depends-on:** T3
**Covers:** F-2, UC1-S2
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `partialWithFetchID` — the marking a hit carries when it is not whole
**Consumes:** `coveredRunes` (T3)
**Data dependency:** hermetic — driven by memories constructed larger than a fixture budget.

## Goal

Make a hit that does not carry its whole memory report that fact, its full rune length, and the id that fetches the rest — never a fragment a caller cannot tell is a fragment.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | Two paths already set `Truncated` + `FullLength` (`:926-928` for the over-budget fallback, `:937-940` for the head bound). Unify them into one marking that also carries the fetch id, and extend it to the case neither covers: a memory larger than the response budget, which is **always** partial rather than made whole by growing the budget for it |
| `internal/mcpserver/drawers.go` | edit | The `am_search` description at `:817` names `content_truncated` and `content_length` as fields that *"appear only when they apply"*. If the fetch id becomes a new `omitempty` key it must be described here, or `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` fails — which is the gate working |
| `internal/mcpserver/readcost_spec_test.go` | edit | Turn `TestF2NoHitIsSilentlyPartial` green. **Tag STAYS** — F-4 and F-7 are still red in this file |

## Ordered Steps

1. Confirm `TestF2NoHitIsSilentlyPartial` is red for the right reason. Verified 2026-08-29: the binding names its own kill-cases — *"restoring a silent fragment, or an off-by-one in the reported length"*.
2. Enumerate the shapes the render path can already produce before writing anything, because this task operates on an existing path: a hit whole and untrimmed; trimmed by the head bound; falling back to a window because the whole memory exceeded the remaining budget; a memory larger than the ENTIRE budget so no window choice helps; and `snippet_chars` supplied by the caller, which is unclamped and can itself be smaller than the memory.
3. Introduce the single marking. The fetch id is the memory's id — `am_get_drawer(id, whole: true)` is the completion path, and F-2's contract is that this is the ONLY one: `am_search` gains no cursor (spec Non-Goals, Grill Log 8).
4. Assert the reported length is the memory's full rune length, not the chunk's and not the rendered window's. The off-by-one is the binding's named kill-case.
5. Confirm the description names any new key.

## Acceptance

```bash
set -o pipefail
go test -tags readcostspec ./internal/mcpserver/ -run 'TestF2NoHitIsSilentlyPartial' -count=1 2>&1 | tee /tmp/adr044-t4.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t4.out && go test -tags readcostspec ./internal/mcpserver/ -run 'TestF1CoverageCountsEveryDisclosedRange' -count=1 && go vet ./... && go test ./... -count=1
```

T3's binding is re-run separately rather than folded into one filter: a fence naming both could be
satisfied by the already-green one alone, which is the aggregate-gate hole the task template records.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF2NoHitIsSilentlyPartial` | `internal/mcpserver/readcost_spec_test.go` | A hit that is not whole is marked, reports full rune length, and carries the fetch id | F-2, UC1-S2 |
| `TestF2NoHitIsSilentlyPartial/a_memory_larger_than_the_whole_budget_is_still_marked` | same | The case no existing path covers, as a subtest inside the fence | F-2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF2NoHitIsSilentlyPartial` |
| 2 — something selects it | The marking is applied in the render loop every `toView` call site passes through. Mutation: skip the marking on the over-budget branch and watch the test go red |
| 3 — the caller can discover it | The `am_search` tool description must name the fetch-id key; `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` fails otherwise. This is the rung that is normally missed |
| 4 — it is used | T1's counting rule counts reads acted on without a second call — a correctly marked partial is precisely a read that needs one |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->

## Invariants

- A memory larger than the response budget is ALWAYS partial-with-fetch-id. The flag is never conditional on record size.
- The completion path is `am_get_drawer`. No cursor or offset is added to `am_search`.
- `content` itself is unchanged in meaning — ADR-019's decision stands.

## Risks

- Growing the number of marked hits makes pages look worse than before. That is the point: the pages were already this short and did not say so. F-7 (T5) supplies the page-level half.
- A new `omitempty` key is invisible to a caller who never hits the case. Mitigated by the description requirement, which an existing gate enforces on a word boundary.

## Stop Condition

Stop if the fetch id available at render time does not address the whole memory — if it names a chunk rather than the memory, `am_get_drawer(id, whole: true)` would return the wrong thing and F-2's contract would be a pointer to the wrong object. ADR-038 made the id opaque and minted once, so verify against `toView` before building on it.

## Out of Scope

- The page-level withheld count, which is T5
- Whether chunking is visible at all, which is T6
- Removing the build tag — T6 owns it

## Verification Log

<!-- Tool-written by `adr-verify`. -->
