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

## Deviations recorded during execution

**1. `Add` HAD TO BE SPLIT, and that is the whole reason this task was L.** Atomicity means the
successor's rows and the predecessor's endings commit together, and `Add` cannot simply be wrapped:
it calls the embedder, and `KGSupersede`'s comment already records what holding a network call
inside SQLite's single write transaction costs — *"a slow embedder becomes a locked database"*.
`Add` is now `prepareWrite` (chunk, embed, resolve ids, build rows — writes nothing) plus
`persistWrite` (vectors, then rows). `persistRows` is the row half, and **the repo is a parameter**
so a caller can pass one bound to a transaction, exactly as `KGSupersede` does with `&Repo{db: tx}`.
`purgeSource` became `purgeSourceOn` for the same reason. Behaviour-preserving: the whole `palace`
suite passed before the correction path was changed at all.

**2. VECTORS STAY OUTSIDE THE TRANSACTION, and this is not an optimisation.** `s.vectors` was
constructed with the service's own `*gorm.DB` and `sqlitevec` shares that handle, so writing
through it inside the transaction opens a SECOND connection to the file the transaction already
holds the write lock on — the same deadlock arriving by a different door. Vectors are written
first, which keeps `Add`'s invariant that a row never exists without its embedding.

⚠ **"The inverse orphan is harmless" was too broad, and review narrowed it. The accurate claim:
harmless to SERVING, visible as an over-count in coverage, permanently, with no reclaim path.**
Serving is genuinely unaffected — `am.dropped_orphan` in the search path is exactly the skip that
makes it so. But `DriftReport` counts an orphan as `indexed > expected` in the coverage block, so
it is a REPORTED signal rather than nothing; and this task changes orphans from a transient
upsert-before-stamp window into the permanent residue of an EXPECTED failure path, because
`ErrConcurrentCorrection` is a designed outcome and a retry mints new ids, so the losing attempt's
vectors stay forever. `doctor --index` does not go red on them — `Clean()` is `Total == 0` and the
drift walk is row-driven, so a point with no row never enters `Drifted` — which is what keeps this
a claim to narrow rather than a defect to fix. No reclaim path exists; that is a follow-up nobody
has taken.

**3. THE ENDINGS RUN BEFORE `persistRows` INSIDE THE TRANSACTION, and the order is load-bearing in a
way that is not obvious.** `persistRows` re-files under the predecessor's SOURCE, and a re-file ends
every current row of that source whose content key left it — which is every chunk of the
predecessor. Running it first would end them with the generic re-file reason and the swap would
then find nothing current and report a race that never happened.

**4a. THE POST-COMMIT WORK FAILS OPEN, both halves, and the first draft got this wrong.** It
called `carryAnchors` and the derived edge "repairable follow-ups" in one sentence and then made
the anchors a HARD ERROR returned after the transaction had already committed — the comment
describing the code the author meant to write. A transient anchor failure reported a correction
that succeeded as one that failed, and a caller doing the obvious thing (retry) landed on the
already-ended refusal, recovering from something that had already worked. Both now annotate and
continue, which is the choice `am_get_drawer`'s `MemorySize` lookup already makes — and it applies
with more force here, because there the read was still in flight while here the write is durable.
Found in review.

**4. THE `:84-87` COMMENT IS REWRITTEN, and half of it survives.** The old rationale — successor
first, *"so a failure leaves the old memory current rather than leaving the team with nothing"* — is
rejected by §Decision as to its CONCLUSION. Its PREMISE stands: an ending with no successor is
genuinely bad. What changed is that the choice is no longer forced; both halves commit together, so
a failure leaves the predecessor current and nothing else, which is the pre-correction state rather
than a fork.

**5. STEP 5's HONEST OUTCOME: THE COMPARE-AND-SWAP MUTANT SURVIVES, and here is exactly why.** The
task says to record this rather than claim coverage, and the reason is more specific than "the
harness cannot interleave" — it can. Measured 2026-08-29, 20 iterations x 8 concurrent writers with
distinct content per writer: **20 wins, 4 refusals from the compare-and-swap, 136 from the
pre-flight already-ended check, 0 other.** So the swap IS reached, at roughly 2.5% of refusals —
real, and far too rare to assert on without a flaky gate.

⚠ **AND THERE IS A SECOND REASON, which is the more interesting one: for a memory filed under a
NAMED SOURCE, something else upholds the invariant.** With the swap severed, extra writers succeed
(wins rose 20 → 25) and yet exactly one memory is still current, because each winner's re-file ends
the previous winner's rows through `purgeSourceOn`. The mutant is killed by the wrong mechanism, so
a test built on a source-filed fixture cannot see it. The swap's real job is the SOURCELESS memory,
where no re-file cleans up behind a lost race.

Both facts were found by varying the fixture — first identical content across writers (which
collapses to one row by content key and hid the fork entirely), then a named source. Neither was
visible from reading the code.

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
- 2026-08-29 · a52a567 · mutant killed · exit 1 · `internal/palace/supersede.go` · the ended predecessor keeps no link to its successor — a dead end rather than a correction, which is what a reader following the chain hits · acceptance-sha256:de9933f99d83b93d2c514f40b10bebdad43c95bca41104bb96cf2935ee4f49fa
- 2026-08-29 · a52a567* · mutant killed · exit 1 · `internal/palace/supersede.go` · end only the HEAD chunk, leaving the rest of a multi-chunk predecessor current and still answering with the claim the correction withdrew — the binding second kill-case · acceptance-sha256:de9933f99d83b93d2c514f40b10bebdad43c95bca41104bb96cf2935ee4f49fa
- 2026-08-29 · a52a567* · mutant survived · exit 0 · `internal/palace/supersede.go` · sever the compare-and-swap, so a second correction that arrives after the first has ended the chunks is applied rather than refused · acceptance-sha256:de9933f99d83b93d2c514f40b10bebdad43c95bca41104bb96cf2935ee4f49fa
  ```
  the fence passed with the mechanism broken
  ```

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
- 2026-08-29 · a52a567* · exit 0 · `set -o pipefail …` · acceptance-sha256:de9933f99d83b93d2c514f40b10bebdad43c95bca41104bb96cf2935ee4f49fa
