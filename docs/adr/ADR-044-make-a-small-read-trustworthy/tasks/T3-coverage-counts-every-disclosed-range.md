# Task ADR-044-T3: Count every disclosed range in `content_coverage`

**Depends-on:** T1
**Covers:** F-1, UC1-S1
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `coveredRunes` — coverage arithmetic over the primary window plus every region
**Consumes:** the counting-rule artifact and its content identity (T1)
**Data dependency:** hermetic — the arithmetic is driven by constructed hits; no live palace required.

## Goal

Make `content_coverage` report the fraction of a memory a caller actually received, counting the primary window and every region returned, so the "do I need a second call?" decision is made on the truth.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | `Coverage` is computed at `:958` as `len(views[i].Content) / full`, counting the window only, while regions are rendered separately at `:859`. Replace with `coveredRunes`, summing distinct disclosed ranges. **Ranges must be de-duplicated** — a region overlapping the primary window would otherwise be counted twice and push coverage above the truth, which is the same defect inverted |
| `internal/mcpserver/drawers.go` | edit | The `am_search` tool description at `:817` says *"content_coverage is always present and reports how much of the memory you are seeing"*. That sentence becomes true rather than aspirational; if the wording needs to change, it changes here, because `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` already gates description drift |
| `internal/mcpserver/readcost_spec_test.go` | edit | Turn `TestF1CoverageCountsEveryDisclosedRange` green. **The `//go:build readcostspec` tag STAYS** — F-2, F-4 and F-7 are still red in this file and T6 is its last task |
| `internal/mcpserver/bootstrap.go` | edit or record | `am_bootstrap` renders through `res.WireShape()`, not `toView`, and carries its own truncation report. Either extend the same coverage arithmetic to it, or record in this task why its report already satisfies F-1. **Silence is not an option** — a member of the class left unmentioned reads as a member that does not exist |

## Ordered Steps

1. Confirm `TestF1CoverageCountsEveryDisclosedRange` is red for the right reason: verified 2026-08-29, *"Today `Coverage = len(views[i].Content) / len(fullContent)` (drawers.go:929) counts the window only"*.
2. Measure the current gap on a real memory before changing anything, so the fix has a before-figure that is not borrowed: the spec records **11–13% reported against 23–27% disclosed**, measured 2026-08-28 over 3,053–3,505-rune memories. Re-take it and date it.
3. Extract `coveredRunes(content string, regions []region, full int) float64` — union of disclosed ranges over the memory's rune length, clamped to 1. The existing clamp at `:959-960` stays; its comment says the head join adds runes the memory does not have, and that is still true.
4. Turn F-1 green, including the two kill-cases the binding names: reporting window-only coverage, and claiming 1.0 while withholding a region.
5. Decide `am_bootstrap` — extend or record. Write the decision into this file's prose either way.

## Acceptance

```bash
set -o pipefail
go test -tags readcostspec ./internal/mcpserver/ -run 'TestF1CoverageCountsEveryDisclosedRange' -count=1 2>&1 | tee /tmp/adr044-t3.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t3.out && go vet ./... && go test ./... -count=1
```

⚠ **This fence cannot observe step 5, and that is the one obligation here with no mechanical
backstop.** `TestF1…` passes whether or not `am_bootstrap` was decided, so the requirement is
recorded where a human reads it: **the sign-off for this task must name the `am_bootstrap` decision —
"extended" or "records why its own report suffices" — and a sign-off that does not name it is not a
sign-off for this task.** Written down rather than assumed, because a requirement the gate cannot see
is exactly how a task goes green with its actual work unmet.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF1CoverageCountsEveryDisclosedRange` | `internal/mcpserver/readcost_spec_test.go` | Coverage counts the window and every region; overlapping ranges are counted once | F-1, UC1-S1 |
| `TestF1CoverageCountsEveryDisclosedRange/an_overlapping_region_is_not_double_counted` | same | The inverse defect, as a subtest so it is inside the fence | F-1 |

Shapes the existing render path can already produce, enumerated before writing the table: a hit with
no regions (coverage = window only, unchanged); a hit whose regions overlap the window; a hit with
`snippet_chars: 0` where the whole memory is the window (coverage must be 1, and `:954`'s comment
says coverage is set for EVERY hit including this one); and a hit trimmed by the budget after
regions were computed.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF1CoverageCountsEveryDisclosedRange` |
| 2 — something selects it | `coveredRunes` is called from the single assignment at `drawers.go:958`, on the path all seven `toView` call sites share. The mutation: restore the window-only division and watch the test go red |
| 3 — the caller can discover it | The `am_search` description already names `content_coverage`; this task keeps that sentence true. `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` is the check |
| 4 — it is used | The counting rule from T1 depends on callers acting without a second call; whether coverage changes that is what the baseline measures |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->

## Invariants

- `content_coverage` is present on every hit, including `snippet_chars: 0` — unchanged from `:954`.
- Coverage never exceeds 1.
- The field's name and wire key do not change; only its arithmetic does. ADR-019's `content` and `regions` are untouched.

## Risks

- A client threshold calibrated against the old under-reported number will behave differently. Accepted and named in the ADR's `Invalidates:` — the number moves, the field does not.
- Double-counting overlaps would over-report, which is worse than the current under-report because it reads as completeness. Mitigated by the subtest.

## Stop Condition

Stop if regions can overlap in a way the render path does not make recoverable — if the rendered region text cannot be mapped back to offsets in the memory, the union cannot be computed and coverage would be a guess. Then the shape of `region` needs a position field, which is a wire change this task did not budget for.

## Out of Scope

- Marking a hit partial, which is T4
- The page-level withheld count, which is T5
- Removing the build tag from this file — T6 owns it

## Verification Log

<!-- Tool-written by `adr-verify`. -->
