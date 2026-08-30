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
| `internal/mcptest/regions_test.go` | add | **Not in this table at authoring, and the omission was the reachability trap this repo keeps falling into.** The F-1 binding lives in `package mcpserver`, which `mcptest` imports, so the binding cannot drive the real tool — it tests `coveredRunes` and would stay green if the CALL SITE were reverted to the window-only division. `TestScenarioCoverageCountsTheRegionsItRendered` drives the real `am_search` over the transport and reads the field an agent receives. It runs in the default lane and is inside this task's fence via its `go test ./...` leg |
| `internal/mcpserver/bootstrap.go` | edit or record | `am_bootstrap` renders through `res.WireShape()`, not `toView`, and carries its own truncation report. Either extend the same coverage arithmetic to it, or record in this task why its report already satisfies F-1. **Silence is not an option** — a member of the class left unmentioned reads as a member that does not exist |

## Ordered Steps

1. Confirm `TestF1CoverageCountsEveryDisclosedRange` is red for the right reason: verified 2026-08-29, *"Today `Coverage = len(views[i].Content) / len(fullContent)` (drawers.go:929) counts the window only"*.
2. Measure the current gap on a real memory before changing anything, so the fix has a before-figure that is not borrowed: the spec records **11–13% reported against 23–27% disclosed**, measured 2026-08-28 over 3,053–3,505-rune memories. Re-take it and date it. **Re-taken 2026-08-29** against a live 5,331-rune memory (`wing_agentmemories`/`llm_open_threads`, the session-continuation note) at `snippet_chars: 700`, reconstructed verbatim from the palace — its rune count matches the `content_length` the server reported, which is what makes it the same text and not a paraphrase:

   | Figure | Value |
   |--------|-------|
   | Memory | 5,331 runes |
   | Primary window, as rendered | 703 runes, in **two** ranges — `[0,119)` and `[1522,2101)` — the head-joined shape |
   | Regions rendered beside it | 7, 723 runes summed |
   | **Reported (window ÷ full)** | **0.1319 — 13.2%** |
   | Naive sum, window + regions | 0.2675 — 26.8% |
   | **Disclosed (union, de-duplicated)** | **0.2472 — 24.7%** |

   Two things this measurement settles that the borrowed figure could not. The gap reproduces on 2026-08-29 on a memory of a different size, so it is not an artifact of the three that were measured. And **the overlap is real, not hypothetical**: the naive sum exceeds the union by 108 runes, so a fix that summed instead of unioning would have over-reported on the very first real hit it saw.
3. Extract `coveredRunes(content string, regions []regionView, full string) float64` — **`full` is the memory, not its length, a deviation from the signature declared above.** The primary window arrives as rendered text and its offsets have to be recovered from it (the measurement above shows why: the real shape is two disjoint ranges, not one), so the union cannot be computed from a length alone. The signature moved rather than the arithmetic guessing. Union of disclosed ranges over the memory's rune length, clamped to 1. The clamp stays: join markers are now excluded by construction so it should never fire, and a clamp that never fires costs nothing while a removed one that should have fired reports coverage above 1.0.
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

## The `am_bootstrap` decision (step 5)

**Recorded, not extended, and the reason names the field.** `am_bootstrap` renders through
`BootstrapResult.WireShape()` (`internal/palace/bootstrap.go`), whose `eager` tier is
`Eager []Drawer` — rows appended verbatim from `DrawersByIDs`, with no snippet window, no
`SnippetRegions` call, and no per-record fraction anywhere in the struct. **F-1's defect has no
analogue there**: the arithmetic this task corrects is a division that counts the primary window and
ignores the regions rendered beside it, and `am_bootstrap` renders neither. Every eager record is
disclosed whole, so a `content_coverage` field on that surface would be the constant 1.0 — a number
that never varies, which this file's own `Truncated` comment already identifies as a field carrying
no information. Its `truncation` report answers the other question, *which records were left out
entirely*, unconditionally and with the call that fetches them; that is the shape T5's `withheld`
belongs to, not F-1's.

**The residual, named rather than left silent.** A `Drawer` row is a chunk, so an entry edge naming
a chunk of a multi-chunk memory inlines 100% of a row that is a fraction of a memory. That is a real
disclosure gap and it is **not F-1's** — it is F-4, *a memory is ONE UNIT to its caller*, which T6
owns. Routed there rather than absorbed here, because F-1's arithmetic cannot express it: there is no
window to add a region to.

## Stop Condition check

**It did not fire, and this line exists because a Stop Condition that silently does not fire looks
identical to one nobody checked.** The condition asks whether rendered region text can be mapped back
to offsets in the memory. It can: `palace.Region` carries `Start`, a rune offset, and `regionView`
carries it onto the wire as `start` (`drawers.go`, `regionView.Start`). Region text is a verbatim
slice with no join markers (`regions.go`, `out = append(out, Region{Text: string(runes[start:end])…})`),
and regions are non-overlapping among themselves. The union is computable without a wire change.

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
- 2026-08-29 · 93ee81c* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · summing disclosed ranges instead of unioning them double-counts a region that falls inside the primary window — the over-report that reads as completeness. F-1 kill-case: claiming more of the memory than was disclosed · acceptance-sha256:2f9fdaffe162ce7a9907fe01acbb5b081c5569f856ff3a6152b1a03de1d99c6a
- 2026-08-29 · 93ee81c* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · reporting a constant 1.0 claims the memory is fully disclosed while a region was withheld — a caller reads 1.0 as "there is nothing more to fetch". F-1 kill-case: claiming 1.0 while withholding a region · acceptance-sha256:2f9fdaffe162ce7a9907fe01acbb5b081c5569f856ff3a6152b1a03de1d99c6a
- 2026-08-29 · 93ee81c* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the REACHABILITY mutant: coveredRunes stays correct and the call site reverts to the window-only division, so the wire carries the old number while the unit test still passes. Killed by TestScenarioCoverageCountsTheRegionsItRendered over the real transport, not by the binding · acceptance-sha256:2f9fdaffe162ce7a9907fe01acbb5b081c5569f856ff3a6152b1a03de1d99c6a

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
- 2026-08-29 · 93ee81c* · exit 0 · `set -o pipefail …` · acceptance-sha256:2f9fdaffe162ce7a9907fe01acbb5b081c5569f856ff3a6152b1a03de1d99c6a
