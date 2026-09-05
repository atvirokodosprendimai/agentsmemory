# Task ADR-056-T1: Both write tools report an anchor accepted without a label

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `anchors_unlabelled` and `anchors_advice` on the `am_add_drawer` and `am_update_drawer` responses
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the unlabelled count from parseAnchorList`, `the keys are absent when every anchor is labelled`

## Goal

A caller that files or replaces anchors and omits `repo` on any of them learns so in the same response, with the one call that labels them, and a caller that labels every anchor sees nothing new.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | `parseAnchorList` also returns how many accepted entries carried an empty `repo`; the `am_add_drawer` handler and the `am_update_drawer` `code_anchors` branch put `anchors_unlabelled` and `anchors_advice` on the response when the count is non-zero |
| `internal/mcpserver/drawers.go` | edit | both tool descriptions name the two keys, because `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` (`internal/mcpserver/wirekeys_test.go`) refuses an omitempty key no description mentions — a field a caller cannot discover is unreachable even when it is emitted (AGENTS.md §Reachability) |
| `internal/mcptest/anchorlabel_test.go` | add | `TestAnUnlabelledAnchorIsReportedAtWrite` through the real tool registry, so the assertion is on the wire shape the caller sees rather than on the helper |

The class this task governs was enumerated with `grep -rn 'AnchorInput{' --include='*.go' internal cmd clients | grep -v _test` on 2026-09-05 at 3a46c81: `internal/mcpserver/drawers.go` (`parseAnchorList`, the only builder fed from a request) and `internal/palace/supersede.go` (`carryAnchors`, which copies an existing record's anchors forward and is Out of Scope in the ADR). Both external write paths — `AddAnchors` from `am_add_drawer` and `ReplaceAnchors` from `am_update_drawer` — go through `parseAnchorList`, which is why the count is taken there and nowhere else.

## Ordered Steps

1. [S1] Write `TestAnUnlabelledAnchorIsReportedAtWrite` and run it red: through `mcptest.New`, call `am_add_drawer` with two anchors, one carrying `repo` and one without, and assert the response carries `anchors_unlabelled: 1` and an `anchors_advice` naming `am_update_drawer` and `repo`; then `am_update_drawer` with `code_anchors` of one unlabelled entry and assert the same keys; then the three negative cases — every anchor labelled, `code_anchors: []`, and the field omitted — and assert both keys are ABSENT from each. Today every positive assertion fails because nothing emits the keys.
2. [S2] Make `parseAnchorList` return the unlabelled count beside the inputs; wire the count into both handlers; the advice sentence names `am_update_drawer(id, code_anchors: [...])` with `repo` set on every entry. `[proof: mutation]`
3. [S3] Name both keys in both tool descriptions, in the sentence that already tells the caller to ALWAYS send `repo`; run the fence green, which includes the wire-key gate. `[proof: acceptance]`

## Acceptance

```bash
set -o pipefail
go test ./internal/mcptest/ -run 'TestAnUnlabelledAnchorIsReportedAtWrite$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./internal/mcpserver/ -run 'TestEveryOmitemptyWireKeyInThisPackageIsDescribed$' -count=1 2>&1 | tee /tmp/acc2.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc2.out && \
go test ./internal/mcpserver/ ./internal/mcptest/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnUnlabelledAnchorIsReportedAtWrite` | `internal/mcptest/anchorlabel_test.go` | both write tools report the count and the advice when an anchor lacks `repo`, and emit neither key when every anchor is labelled, when the list is empty, and when the field is omitted | — | S1, S2 |
| `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` | `internal/mcpserver/wirekeys_test.go` | the two new omitempty keys are named in a tool description, so a caller can learn they exist before hitting the case that emits them | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the count in `parseAnchorList` |
| 2 — something selects it | the two handlers put it on the response; the mutant is dropping the count (returning 0 unconditionally), which turns the scenario red on both positive cases |
| 3 — the caller can discover it | the descriptions name both keys, gated by `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` |
| 4 — it is used | not measured here; the first agent session that files an anchor without `repo` after the release will read it, and `doctor --corpus` (T2) measures whether the population then stops growing |

## Mutation Log

## Invariants

- No write is refused for a missing label; `AddAnchors` and `ReplaceAnchors` store exactly what they store today.
- The keys are absent, not zero, when nothing is unlabelled — a caller that checks for presence must not see `anchors_unlabelled: 0`.
- `parseAnchors`' existing rule stands: an entry with no `path` or no `snippet` is skipped, and a skipped entry is not counted as unlabelled.

## Risks

- The advice sentence goes stale when `am_update_drawer`'s anchor argument is renamed — mitigated by the scenario asserting the advice names the tool and the field, so a rename turns it red.

## Stop Condition

Stop if `am_update_drawer`'s `code_anchors` branch turns out not to run through `parseAnchorList` — then the count has two sources and the ADR's "taken in one place" claim is false and needs amending before the code is written.

## Out of Scope

- The corpus check — T2's job.
- Anchors carried forward by `carryAnchors` — the ADR's Out of Scope, permanent.

## Verification Log
