# ADR-044: Make a small read trustworthy enough to act on

**Status:** Accepted
**Date:** 2026-08-29
**Owner:** Zy
**Spec:** docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md
**Cross-references:** `docs/adr/ADR-013-a-page-of-memories-not-chunks.md` (a short page is honest — F-7 is the half it left unsaid), `docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md` (owns `regions` and the `content_coverage` field this record redefines), `docs/adr/ADR-024-rank-memories-not-chunks.md` (retains `memory_id` and `chunks_matched`, which F-4 does not remove), `docs/adr/ADR-028-a-recall-you-can-judge.md` (its T4 ratio is the instrument F-5's rule is derived from), `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md` (owns identity; explicitly NOT amended here), `docs/adr/ADR-041-the-recall-that-does-not-depend-on-remembering.md` (owns pushed recall and the no-mechanism-before-baseline rule this record inherits), `internal/mcpserver/drawers.go`, `internal/mcpserver/bootstrap.go`, `internal/palace/supersede.go`, `internal/palace/memory_search.go`
**Governs:** `internal/mcpserver/drawers.go`, `internal/mcpserver/bootstrap.go`, `internal/palace/supersede.go`, `internal/repohygiene/**`

The class is **every read path that returns memory content to a caller**, not `am_search` alone —
the spec's Contracts Touched extends the same disclosure obligation to `am_list_drawers` and
`am_bootstrap`. Enumerated by command rather than from memory, 2026-08-29:

    grep -rln "toView(\|newSearchHitView(" --include="*.go" internal/mcpserver/ | grep -v _test

**One file, seven call sites**: `drawers.go:241` (`am_list_drawers`), `:461` and `:480`
(`am_get_drawer`), `:561` (`am_update_drawer`), `:661` (`am_list_drawers` scoped), `:870`
(`am_search`, via `newSearchHitView` at `:694`), `:1074` (`am_check_duplicate`). Every one of them
renders through `toView` (`:140`), which is why the disclosure fields are fixed in one place.

**`am_bootstrap` is a member of the class and is NOT on that list**, because it renders through
`res.WireShape()` in `palace` instead of `toView` (`internal/mcpserver/bootstrap.go:37`). It already
carries a truncation report of its own. It is named here rather than left silent: T3 and T5 must
either extend it or record why its own report satisfies the same obligation. A member left
unmentioned reads as a member that does not exist, which is this repository's signature defect.

**Deliberately excluded:** `am_mine` and `am_diary_read`, which return content assembled by their
own paths and are not recall surfaces; and `am_kg_query`, which returns facts rather than memory
content. If any of them later renders a memory through `toView`, it joins the class on that commit.

**Enforced-by:** `internal/mcpserver/readcost_spec_test.go::TestF1CoverageCountsEveryDisclosedRange`

<!-- The strongest available form here is a test id, not a mutation label: this corpus has no
mutation campaign, and ADR-003's rule is that a test id proves the check exists rather than that it
can fail. Each task carries its own mutant, graded by `adr-verify --mutant`. -->

**Invalidates:** ADR-019 — its `content_coverage` field keeps its name and changes its meaning, from *the fraction of the memory the primary window shows* to *the fraction every disclosed range shows*. ADR-019's decision that `content` is unchanged and that regions sit beside it is untouched; only the arithmetic reporting how much of the memory a caller received is redefined. ADR-038 is deliberately NOT invalidated: it owns identity — the opaque id, `content_key`, ended-not-overwritten — and F-3's atomicity is a property of the write, not of identity (spec Grill Log 9).

**Served-path change:** Every `am_search`, `am_get_drawer` and `am_list_drawers` response changes shape — `content_coverage` starts counting regions, a hit that is not whole says so with its full length and fetch id, a page says how many hits the budget made it withhold, and a correction filed through `am_update_drawer` can no longer leave two current records on one subject.

## Context

The spec's Problem and Goal are the source; only the decision-relevant part is repeated here.

The framing that motivated this work — that reads are expensive — **was retracted by its own author
before this record was written**, and the correction is the reason the ADR exists in this shape.
Estimated 2026-08-28 in output tokens: an `am_search` emission costs ~30, an `am_get_drawer` ~45, a
content-bearing drawer write ~400, a diary entry ~525. These are BPE arithmetic at ±20%, not a
counter reading, and the **ordering is the claim** — a read sits one to two orders of magnitude
below a write. Reading is among the cheapest things an agent does.

What the ten measurements actually establish is **legibility**. A `grep`'s advantage is not price;
it is that its output is complete and self-evidently complete — you never wonder what it withheld.
Four properties break that for a palace read, each measured 2026-08-28 on session `ee8f1fc1` against
the live palace and this tree:

- `content_coverage` counts only the primary window while regions are rendered separately, so the
  reported figure is **11–13% where 23–27% was actually disclosed** (`drawers.go:774` declares the
  field, `:958` computes it, `:859` renders the regions it ignores).
- A hit trimmed by the response budget is marked, but a memory larger than the budget can never be
  whole, and nothing says which case a caller is looking at.
- A page cut short by the budget is indistinguishable from an exhausted corpus: `am_search` has
  `limit` and no cursor (`drawers.go:786-800`).
- The cheap way to file a correction leaves the corrected record CURRENT — 4 corrections filed to one
  finding, 0 records ended — so one page carried four competing framings of one subject.

**The two halves are causally linked, and that is why this is one record rather than four fixes.**
Because a small read cannot be trusted today, the rational defensive read is `snippet_chars: 0` or
`whole: true` — the expensive one. The facts below do not make reading cheaper; they make a SMALL
read trustworthy, and cheapness follows from that, never the other way round.

**Debt swept at authoring, per the skill's contract.** `adr-debt docs/adr` on 2026-08-29 before this
record: **213 deferred · 45 open follow-ups · 15 broken pointers · 0 unreceipted**, exit 1. One swept
item bears on this decision and is handled rather than carried: ADR-028 T3 and T4 both defer
retention for `drawer_fetches` and `search_events`, and T7 creates the same question for ended
records — so T7's deferral is filed alongside theirs, with the note that the three are one question.
Nothing else in the 213 or the 45 touches this record. The 15 broken pointers are
**not this corpus's defect**: reproduced on a two-line fixture the same day, `adr-debt` reports a
false `BROKEN [malformed]` for any `(deferred: …)` followed by a trailing full stop. Confirmed as a
tool defect by the quality-harness maintainer the same day and fixed for v2.31.2; the corpus is
correct and is deliberately left unedited. This record's own exit code must be read against that
pre-existing 15.

## Existing Primitives Audit

Reuse in every case; nothing here is a new subsystem.

| Primitive | Where | Disposition |
|-----------|-------|-------------|
| `toView` — the single renderer for every drawer on the wire | `internal/mcpserver/drawers.go:140` | **Reuse.** Seven call sites share it, so the disclosure fields are fixed once rather than per tool |
| `Coverage` field, already always present on a hit | `drawers.go:774` | **Reshape.** The field, its name and its wire key survive; only its arithmetic changes (`:958`) |
| `Truncated` / `FullLength`, already set on a budget trim | `drawers.go:926-928` | **Reuse.** F-2 extends the same marking to the case the budget can never satisfy, rather than adding a second partial flag |
| `overBudget` — a count of hits the budget trimmed, computed and discarded | `drawers.go:929` | **Reshape.** F-7's number is a sibling of this counter; the page-level count is computed in the same loop and currently thrown away |
| A `withheld` map already on the wire for a filtered response | `internal/mcpserver/kg.go:223` | **Reuse the shape.** `out["withheld"] = map[string]int64{...}` is the precedent; F-7 should not invent a second vocabulary for the same idea |
| `survivorsFrom` — ended records already absent from a default page | `internal/palace/memory_search.go:70` | **Reuse unchanged.** This is why F-3 is a write-side fact: the read side already filters correctly |
| `supersedeInto` — the advertised correction path | `internal/palace/supersede.go:84-124` | **Reshape.** Writes the successor then ends predecessor chunks one at a time, with no atomicity and no compare-and-swap |
| ADR-028 T4's fetch ratio over `drawer_fetches` (migration 00036) | `internal/palace/fetchlog.go` | **Reuse as the instrument.** See Decision §3 — F-5's rule is derived from it rather than measured by a second one |
| `-tags contractaxis`, the repository's existing red-lane shape | `internal/palace` | **Reuse.** `readcostspec` follows it exactly |

## Decision

**A hit tells the truth about itself, a page tells the truth about itself, and a correction leaves
one record standing.** Seven facts, in three groups, and the groups are not independent.

**1. Disclosure (F-1, F-2, F-7, F-4) — what a caller receives says what it is.**
`content_coverage` counts the primary window *and* every region returned, so a caller comparing it
against a threshold compares against the truth. A hit that does not carry its whole memory says so,
reports the memory's full length, and carries the id that fetches the rest — and a memory larger
than the response budget is **always** partial-with-fetch-id, never made whole by growing the budget
for it, because a partial flag conditional on record size restores the question it exists to remove.
A page reports how many hits the budget made it withhold. A caller never joins chunks to obtain a
memory's content; chunk metadata stays as diagnostics.

**2. Correction (F-3) — the write side, and this record chooses against a documented trade.**
`supersedeInto` writes the successor first, deliberately, *"so a failure leaves the old memory
current rather than leaving the team with nothing"* (`supersede.go:84-87`). **That trade is
rejected here.** The state it protects — predecessor current, successor also current — is precisely
the two-competing-records state that produced four framings of one finding on one page. An advertised
correction must leave exactly ONE current successor linked to the ended predecessor, including under
partial failure and under a concurrent correction of the same record. Saying which side of that
trade we want is the decision; the spec named it as something the ADR may not scope away.

**3. Measurement (F-5, F-6) — the rule is an artifact, and it ships first.**
The counting rule is a committed file with an identity, fixed before any collection. It counts
**reads acted on without a second call**, not read frequency — frequency is what the ADR-041
instrument already counts, and a mechanism that made every hit trustworthy could leave it unmoved.
Changing the rule's content invalidates every baseline taken under it, and a rate quoted across a
rule change is a defect the gate names rather than a comparison it reports.

**What would make this fail, and whether that data exists today.** F-5's criterion is falsifiable
only if a baseline can be *low* — and it can: `am_recall_stats` measured 6 searches against 18
writes over a two-hour window on 2026-08-28, under an explicit instruction to read more. The
population is one session, so the baseline task's sign-off must record the window and the sample
size; a rate over one session is not a corpus rate. F-1's criterion is falsifiable today — the
11–13% against 23–27% gap is measurable on the current tree before any change. F-3's is falsifiable
by racing two corrections, which requires a test that can actually interleave them; if it cannot,
the task says so rather than claiming coverage.

**The counting rule is derived from ADR-028 T4, not measured independently.** T4 `Produces:` *"a
fetch ratio reported with its population"* over `drawer_fetches` (migration 00036, landed). A recall
followed by an `am_get_drawer` is a read that NEEDED a second call; the complement of T4's ratio is
this record's quantity. T1 therefore consumes T4's instrument rather than building a second one, and
because T4 is **pending**, T1 either waits on it or records the rule against the raw
`drawer_fetches` rows and names T4 as the reporting half. This is the single largest dependency in
the record and it crosses ADRs.

## Alternatives Considered

- **Grow the response budget so long memories come back whole.** Rejected: it makes the partial flag
  conditional on record size, so "is this all of it?" returns as a question about which memory you
  happened to ask for. Resolved in the spec at Grill Log 7.
- **Add a cursor or offset to `am_search` so withheld hits are resumable.** Rejected: `am_get_drawer`
  already completes a partial hit, and a second resumption contract for one job is the cost this
  record exists to avoid. The consequence is accepted openly — a withheld hit is unresumable, which
  is exactly why F-7's count is load-bearing rather than cosmetic. Grill Log 8.
- **Demote a superseded record below its correction.** Rejected on evidence, and this is the entry
  worth reading twice: the earlier draft justified it with a measured rank inversion that **was
  retracted** — distance does not decide order, and on re-reading the same response the correct
  framing came back first. Ordering also sits behind ADR-004 issue #34, whose `justified` verdict is
  still open. What survived the retraction is a write-side fact, which is what F-3 became.
- **Amend ADR-038 with the atomicity requirement instead of deciding it here.** Rejected: ADR-038
  owns identity, and moving the fact there separates it from the read-cost evidence that motivates
  it. Grill Log 9.
- **Count read FREQUENCY as the success metric.** Rejected: it was the obvious choice and it repeats
  ADR-041's failure — counting the quantity that is easy to count rather than the one being claimed.
  Reads are not rare because they are expensive. Grill Log 13.
- **Fix the four disclosure defects as four small PRs with no record.** Rejected: F-1, F-2 and F-7
  are one contract change to one struct, and F-5 makes the whole set unshippable before a baseline
  exists. Four PRs would ship the mechanisms and skip the constraint.

## Component / Boundary Impact

`internal/mcpserver` keeps ownership of how much fits on the wire; `internal/palace` keeps ownership
of what a memory and a supersession ARE. That split is ADR-019's and is preserved: F-1 and F-7 are
transport arithmetic and land in `mcpserver`; F-3 is a domain invariant and lands in `palace`; F-5
and F-6 are repository hygiene and land in `internal/repohygiene`, which already hosts gates of this
shape. No module is added, moved or renamed, so `docs/architecture.md`'s Module Map is unchanged.

## Wiring & Contract Changes

Inherited from `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` §Contracts Touched; delta:

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `withheld` on an `am_search` page | add | `registerSearch` (`drawers.go:815`) | every MCP client; the tool description must name it, or the field is undiscoverable by construction |
| `am_bootstrap`'s existing truncation report | reconcile | `palace` `WireShape()` | either extended to the same vocabulary as `withheld`, or its difference recorded — decided in T5, not assumed here |
| The counting-rule artifact and its identity | add | `internal/repohygiene` | whoever quotes a read rate; `adr-verify` sign-off lines |

`TestEveryOmitemptyWireKeyInThisPackageIsDescribed` already exists in this package and will fail on
an `omitempty` field absent from every tool description — so F-7's new key is caught by a gate this
repository already runs, without anyone remembering to check.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| The counting-rule artifact and its content identity | T1 | T2, T3, T7 | No — additive file. T4, T5 and T6 inherit the constraint transitively through T3 rather than declaring it again; adding the edge to each would duplicate what the wave table already orders |
| `coveredRunes` — coverage arithmetic over window + regions | T3 | T4, T5 | No — internal to `drawers.go` |
| `partialWithFetchID` — the marking a hit carries when it is not whole | T4 | T5 | No — extends existing `Truncated`/`FullLength` |
| `withheld` page field | T5 | T6 | No — additive wire key |
| Tag removal from `internal/mcpserver/readcost_spec_test.go` | T6 | none | **Yes for the file** — T3, T4 and T5 must run their fences with `-tags readcostspec`; T6 removes it |
| Tag removal from `internal/repohygiene/readrule_spec_test.go` | T2 | none | **Yes for the file** — T1's fence carries the tag; T2 removes it |
| Tag removal from `internal/palace/readcost_spec_test.go` | T7 | none | No — T7 is the only task in that file |

**The build tag is a shared resource and the spec's plan does not survive contact with it.** The
Non-Goal says *"the tag comes off in the commit that turns each binding green"*, which is impossible
where one file holds four bindings: removing the tag at T3 would put F-2, F-4 and F-7 — still red —
into the default lane that CI runs on every push. Decided here: **the tag comes off in the LAST task
of each file**, and every earlier task in that file runs its acceptance fence with
`-tags readcostspec`. Three files, three removal points: T2, T6, T7.

## Implementation

See `docs/adr/ADR-044-make-a-small-read-trustworthy/tasks/README.md`. Seven tasks, five waves.

## Consequences

- **Positive:** a caller can act on a hit without a second call, or knows exactly what a second call
  would return. A short page is legible as short. One subject yields one current record.
- **Positive:** the ADR-041 failure mode — a mechanism shipped against an instrument that cannot see
  it — is structurally prevented rather than remembered, because every mechanism task depends on T1.
- **Negative:** whole-memory disclosure shrinks a page. A page of ten hits may become three, and F-7's
  count is what keeps that legible rather than silent. The budget still bounds the response.
- **Negative:** F-3 removes a deliberate safety property. Where `supersedeInto` today prefers "old
  record survives" over "nothing survives", an atomic correction can now fail leaving the predecessor
  current and the correction unwritten — the author must retry. That is the trade, taken knowingly.
- **Negative:** the record's completion depends on a task in another ADR (ADR-028 T4), which is
  pending and not owned here.
- **Neutral:** `chunk_index` and `parent_id` stay on the wire as diagnostics. A repo-wide search found
  no production consumer, but `internal/mcptest/regions_test.go:193-199` reads `hit["chunk_index"]`
  over the real transport, so they are load-bearing for tests even where no client needs them.

## Out of Scope

Inherited from `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` §Non-Goals; delta:

- Editing the 15 deferral bullets that `adr-debt` currently misreports as `BROKEN [malformed]` (permanent: they are well-formed — reproduced on a fixture 2026-08-29 and confirmed by the quality-harness maintainer as a tool defect fixed for v2.31.2, so changing this corpus to satisfy a broken parser would be the wrong repair)
- Backfilling `content_coverage`'s new meaning into recorded `search_events` rows (permanent: coverage is computed at render time and never stored, so there is nothing to backfill — verified against `drawers.go:954-960`, which sets it on the view rather than on the row)
- Taking ADR-021 T3's Claude Desktop measurement, which this record's read paths would also travel (deferred: `docs/adr/BACKLOG.md` §"From ADR-044 (make a small read trustworthy)")

## Risks

Inherited from `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` §Risks; delta:

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| T1 blocks on ADR-028 T4, which this record does not own, and the whole DAG stalls behind it | Med | High | T1 has an explicit fallback: record the rule against raw `drawer_fetches` rows and name T4 as the reporting half. The Stop Condition fires only if `drawer_fetches` itself proves insufficient |
| The tag is removed early and four red bindings enter CI's default lane | Med | High | Fixed in Inter-task Contracts: removal happens only in T2, T6 and T7. Every earlier fence in a shared file carries `-tags readcostspec` |
| F-3's concurrency half is untestable in practice, and the task claims coverage anyway | Med | High | T7's Stop Condition requires an interleaving the test can actually produce; if it cannot, the survived mutant stays in the log with a written reason rather than being papered over |
| The baseline is taken over one session and quoted as a corpus rate | High | Med | T1's `Data dependency` is not hermetic and its sign-off line must record window and sample size. The one figure available today, 6 searches against 18 writes, is explicitly n=1 |
| `am_bootstrap` is silently left out because it does not use `toView` | Med | Med | Named in `Governs:` above and made a required decision in T3 and T5 rather than an omission |

## Rollback

Required — this changes a wire contract and a persistence-adjacent write path.

- **F-1 (coverage arithmetic):** revert `drawers.go`. The field's name and presence are unchanged, so
  a client written against either meaning keeps parsing; only the number moves.
- **F-2 / F-7 (partial marking, withheld count):** both are additive keys. Reverting removes them;
  no client can have depended on a field that did not exist.
- **F-3 (atomic correction):** the highest-risk revert. If the atomic path is backed out, records
  corrected while it was live remain correctly ended — the invariant is on the write, not on stored
  shape, so no data migration is needed either way. **No migration is reserved by this ADR**; the
  counting rule is a committed file checked by a test, not a table, so migration 00037 stays free.
- **F-5 / F-6 (the rule and its gate):** delete the artifact and its test. Any baseline recorded
  under it becomes uncited, which F-6 already treats as invalid — the failure mode is refusal to
  quote, not a wrong number.

## Follow-ups

- [x] Re-run `adr-debt docs/adr` once quality-harness v2.31.2 ships — **done 2026-08-29, and 13 of the 15 cleared with no corpus edit** (`215 deferred · 15 broken` under 2.31.1 → `228 deferred · 2 broken` under 2.31.2; the 13 moved from broken to deferred, which is the arithmetic that shows they were always well-formed). Two remain and are a DIFFERENT class, reported to the maintainer rather than fixed here: `ADR-027-a-maintained-document-is-a-set-of-records.md:182`, whose disposition wraps across a line break, and `ADR-028-a-recall-you-can-judge.md:97`, whose pointer leads with a resolvable ADR id followed by prose and is resolved as one path
- [ ] Reconcile `am_bootstrap`'s truncation report with `withheld` if T5 records a difference rather than extending it
- [ ] Take ADR-021 T3's Claude Desktop measurement — still pending since 2026-08-22, and it decides whether protocol text reaches a client that has no hooks and no skills
