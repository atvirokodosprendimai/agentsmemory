# Task ADR-044-T7: Make a correction leave exactly one current successor, atomically

**Depends-on:** T1
**Covers:** F-3, UC2-S1, UC2-S2
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** tag removal from `internal/palace/readcost_spec_test.go`
**Consumes:** the counting-rule artifact and its content identity (T1)
**Data dependency:** hermetic for the single-writer case. The CONCURRENT case needs two writers actually interleaving against a real store — if the test harness cannot produce that interleaving, say so rather than asserting coverage.

## Goal

Make an advertised correction end its predecessor and leave exactly one current record on that subject — including when the write fails part-way and when a second correction races it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/supersede.go` | edit | `supersedeInto` at `:84-124` writes the successor, then ends predecessor chunks one at a time, with no transaction and no compare-and-swap. That is what leaves two current records. Wrap the whole operation, and add the compare-and-swap so a racing correction is REFUSED rather than also succeeding |
| `internal/palace/supersede.go` | edit | The comment at `:84-87` documents the current order as deliberate — *"so a failure leaves the old memory current rather than leaving the team with nothing."* **That trade is rejected by ADR-044 §Decision.** Update the comment to record the new choice and why, or the code will carry a rationale for behaviour it no longer has |
| `internal/palace/readcost_spec_test.go` | edit | Turn `TestF3ACorrectionLeavesOneCurrentSuccessor` green **and remove `//go:build readcostspec`** — F-3 is the only binding in this file, so its tag comes off with it |
| `internal/mcpserver/drawers.go` | read only | `am_update_drawer` at `:490` is the advertised correction surface; its description at `:463` already promises the ending-and-linking behaviour. This task makes the promise true under failure and concurrency |

## Ordered Steps

1. Confirm `TestF3ACorrectionLeavesOneCurrentSuccessor` is red for the right reason. Verified 2026-08-29; the binding names three kill-cases — replacing supersession with a plain `Add`, skipping one predecessor chunk's ending, and racing two corrections into two current successors.
2. Enumerate the shapes the existing write path can produce, since this task operates on existing records: a single-chunk predecessor; a multi-chunk predecessor (the case `:84-124` iterates); a predecessor already ended by someone else; and a predecessor whose chunks were written by an older mint path. Decide each: handled, or refused with a named error.
3. Make the operation atomic. Then add the compare-and-swap on the predecessor's current state so the second of two racing corrections is refused rather than applied.
4. Turn F-3 green for the single-writer and partial-failure cases first. These are hermetic.
5. Attempt the concurrent case. **If the harness cannot actually interleave two corrections, do not assert it passes** — leave the mutant `survived` in the Mutation Log with a written reason, per the task template. An honest gap is worth more than a coverage claim the next reader cannot check.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ -run 'TestF3ACorrectionLeavesOneCurrentSuccessor' -count=1 2>&1 | tee /tmp/adr044-t7.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t7.out && go vet ./... && go test ./... -count=1
```

No `-tags readcostspec`: the fence is red until step 5 removes the tag, so it proves the removal too.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF3ACorrectionLeavesOneCurrentSuccessor` | `internal/palace/readcost_spec_test.go` | A correction ends its predecessor and links it; exactly one record on the subject is current | F-3, UC2-S1 |
| `TestF3ACorrectionLeavesOneCurrentSuccessor/a_part_way_failure_leaves_no_fork` | same | Neither both-current nor a half-ended predecessor, as a subtest inside the fence | F-3, UC2-S2 |
| `TestF3ACorrectionLeavesOneCurrentSuccessor/a_racing_correction_is_refused` | same | One writer wins, the other is refused — or the reason it cannot be driven is recorded | F-3, UC2-S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF3ACorrectionLeavesOneCurrentSuccessor` |
| 2 — something selects it | `supersedeInto` is reached from `am_update_drawer` (`drawers.go:490`), the advertised correction surface. Mutation: skip one predecessor chunk's ending and watch the test go red |
| 3 — the caller can discover it | `am_update_drawer`'s description already advertises ending-and-linking (`drawers.go:463`); no new key — `n/a: no new declared interface` |
| 4 — it is used | The four-corrections-zero-endings measurement of 2026-08-28 is the observation this closes; re-running that count is how usage is seen |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->

## Invariants

- Exactly one current record per corrected subject, after every outcome of the operation.
- Read-side history filtering is unchanged: `survivorsFrom` (`memory_search.go:70`) already excludes ended records, and this task does not touch it.
- **Nothing here touches ORDERING.** F-3 is a write-side invariant; ADR-004 issue #34's `justified` verdict still gates any ranking use, and this task may not introduce one.
- ADR-038's identity rules are untouched: ids stay opaque and minted once.

## Risks

- The rejected trade is real. An atomic correction that fails leaves the predecessor current and the correction unwritten, so the author must retry — where today a partial failure left the old record standing by design. Named in ADR-044 §Consequences as a knowing cost.
- A compare-and-swap on SQLite under concurrent writers may serialise rather than refuse, making the race untestable as written. Step 5 covers this honestly rather than by assertion.

## Stop Condition

Stop if atomicity cannot be achieved without holding a transaction across the embedding write — that would couple a correction's durability to the embedder's availability, which is a worse failure than the one being fixed. Bring it to the owner; the fix may be to end the predecessor first and accept a different partial state, which is a decision, not an implementation detail.

**What would make this criterion impossible to fail:** a test store that serialises every write cannot exhibit the race at all, so `a_racing_correction_is_refused` would pass without proving anything. Verify the harness can produce two genuinely concurrent writers before trusting that subtest.

## Out of Scope

- Any ordering or ranking effect (permanent: the spec makes this a Non-Goal and ADR-004 issue #34's verdict is still open — F-3 is write-side only)
- Amending ADR-038 (permanent: it owns identity, and Grill Log 9 places atomicity here so the fact stays with the evidence that motivates it)
- Retention or pruning of ended records (deferred: `docs/adr/BACKLOG.md` §"From ADR-044 (make a small read trustworthy)")

## Verification Log

<!-- Tool-written by `adr-verify`. -->
