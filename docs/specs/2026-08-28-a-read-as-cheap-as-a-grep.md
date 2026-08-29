# Spec: Make a palace read as cheap to justify as a grep

> **Date:** 2026-08-28 · **Status:** Ready-for-ADR
> **Owner:** Zy · **Becomes:** ADR-NNN (allocate at merge)
> **Gate:** Status may become Ready-for-ADR only after `spec-verify --spec docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` exits 0.
> **Cross-references:** `docs/adr/ADR-041-the-recall-that-does-not-depend-on-remembering.md` (owns pushed recall; its instrument's limits are inherited below), `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md` (ended records already excluded from default reads), `docs/adr/ADR-010-supersede-do-not-overwrite.md`, `docs/adr/ADR-017-a-subagent-is-a-session.md` (prose is the weakest lever), `internal/palace/chunk.go`, `internal/palace/memory_search.go`, `internal/mcpserver/drawers.go`

## Problem

Measured on session `ee8f1fc1`, one long working session in this repository: **7,521 Bash calls against 369 palace calls**, and of those 369, **226 writes against 143 reads**. The agent writes to the palace more than it reads from it. `am_search` ran 52 times in 8,256 tool calls.

The palace's competitor is not "no memory" — it is `grep`, which is faster and reports what the code *does* rather than what someone once said about it. Three measured properties make a read expensive to justify: a hit's reported coverage UNDER-COUNTS what it disclosed, so the "do I need a second call?" decision is made on a wrong number (M-3); memories fragment at a 1,600-character boundary (M-4); and the cheap way to file a correction leaves the record it corrects CURRENT, so one page can carry four competing framings of one finding (M-5, M-6).

⚠ Two earlier versions of this paragraph are retracted and the retractions are below: it claimed a hit discloses **~3%** (M-3b — that was the caller's own `snippet_chars: 90` read back; real disclosure is 23–27%) and that **a superseded record can outrank its correction** (M-5b — distance does not decide order, and the correct record in fact came back first). They are named here rather than quietly deleted because this document's own argument is that a correction must be as reachable as the thing it corrects.

⚠ **AMENDED 2026-08-28, after the questions above were resolved: COST WAS NEVER THE BINDING CONSTRAINT, and this section's framing said it was.** ESTIMATED the same evening in output tokens — the currency an agent actually spends, as distinct from context, which is what a result consumes. An `am_search` emission costs ~30 output tokens, an `am_get_drawer` ~45, a content-bearing drawer write ~400, a diary entry ~525. ⚠ **These are BPE arithmetic, not a counter reading: no instrument reports a turn's output-token count back to the model that emitted it, so treat them as ±20% and take the ORDERING as the claim** — a read sits one to two orders of magnitude below a write, and that gap survives an error far larger than 20%. The word in the first draft of this paragraph was "Measured", which is the overclaim this document retracts M-3b for. A read is one of the cheapest actions available; the mandatory write side is roughly **10×** the entire read-side protocol. And in the very session that produced this amendment — under an explicit instruction to recall more — the ratio measured **6 searches against 18 writes, 3.0 writes per read**, worse than the 1.9 in M-2. Reads are not rare because they are expensive.

**What M-1..M-10 actually establish is LEGIBILITY, not price.** A `grep`'s advantage is not that it is cheap; it is that its output is complete and self-evidently complete — you never wonder what it withheld. Every fact below is that property: coverage under-counts what was disclosed (F-1), a hit can be silently partial (F-2), a page can be silently short (F-7), one page can carry several current framings (F-3). The heading is kept as written because this document's argument is that a correction must be as reachable as the thing it corrects; the ADR this becomes should take its title from this paragraph rather than from the heading — the number is allocated at merge, per the header, so it is deliberately not written here.

**And the two are causally linked, which is the reason this amendment is not merely a rewording.** Because a small read cannot be trusted today — coverage under-reports, a partial hit does not say so, a short page does not say so — the rational defensive read is `snippet_chars: 0` or `whole: true`, which is the EXPENSIVE one. So the facts below do not make reading cheaper; they make a SMALL read trustworthy, which is what turns "read freely" from advice into a strategy an agent can run. Cheapness follows from legibility here, never the other way round.

### Evidence

Measured on session `ee8f1fc1` against the live palace and this tree, 2026-08-28. These are
observations, not requirements — they motivate the Facts below and are cited by them.

| ID | Observation | Evidence |
|----|-------------|----------|
| M-1 | An agent reaches for Bash roughly 20× more often than the palace. | session `ee8f1fc1` — 7,521 Bash calls against 369 palace calls |
| M-2 | An agent writes to the palace more than it reads from it. | same session — 226 writes, 143 reads; `am_search` 52 of 8,256 tool calls |
| M-3 | `content_coverage` counts only the primary window, not the `regions` also disclosed, so a caller deciding whether it needs a second call decides on an under-reported number. | `internal/mcpserver/drawers.go:929`; regions rendered separately at `:859` |
| M-3b | Disclosure is 23–27%, not the ~3% this spec first reported. The earlier figure came from a query passing `snippet_chars: 90` — it measured the caller's own parameter. | re-measured 2026-08-28: 401–404 content runes + 403–408 region runes over 3,053–3,505-rune memories; matches ADR-019's measured median of 25% |
| M-4 | Memories fragment at a fixed boundary sized for the embedder. | `internal/palace/chunk.go:20` — `ChunkSize = 1600` (~400 bge-m3 tokens) |
| M-5 | Four unlinked records about one subject were all CURRENT and all returned on one page, so a reader met three superseded framings beside the correct one. | one query, 2026-08-28; none had been ended, so none was filtered |
| M-5b | ⚠ **RETRACTED — no rank inversion was demonstrated.** An earlier version claimed the wrong record outranked its correction, citing distances 0.334 against 0.355. Distance does not decide order, and on re-reading the same response the CORRECT framing came back **first**. M-5 stands; the ordering claim does not. | ADR-028:17 — *"the score that decides the order is not the score that is shown"*; the cited response, re-read |
| M-6 | Filing a correction with `am_add_drawer` leaves the incorrect record CURRENT, while `am_update_drawer` ends and links it. The cheap path produces the competing corpus. | 4 corrections filed to one finding, 0 records ended; `am_update_drawer` advertises the correct behaviour at `internal/mcpserver/drawers.go:463` |
| M-7 | An ended record is ALREADY absent from a default page, not merely outranked. | `include_history` defaults false at `internal/mcpserver/drawers.go:431`; `survivorsFrom` at `internal/palace/memory_search.go:70`; ADR-038 |
| M-8 | Corrections are already ATTACHED to hits — retracts/supersedes/qualifies edges reach the reader today. | `internal/palace/memory_search.go:275`; wire field at `internal/mcpserver/drawers.go:761` |
| M-9 | `supersedeInto` writes the successor then ends predecessor chunks one at a time, without atomicity or a compare-and-swap. | `internal/palace/supersede.go:84-124` |
| M-10 | `am_search` has `limit` but no offset or cursor, so a withheld hit cannot be resumed by paging. | `internal/mcpserver/drawers.go:786-800` |

## Goal

An agent reads from the palace without first deciding whether the read is worth it: a hit is actionable on arrival, and what it returns is current.

## Actors

| Actor | Kind | Goal |
|-------|------|------|
| Working agent | system | Answer "what was decided / what was corrected" without a second round trip |
| Record author | system | File a correction that supersedes the record it corrects, not one that competes with it |
| Measurement owner | human role | Know whether a mechanism moved read behaviour, before spending a window on it |
| Concurrent correction author | system | Correct a record without racing another session into two competing current claims |
| External MCP client | external service | Read hits without depending on chunk-level fields |

## Use Cases

### UC-1: Working agent recalls a decision mid-task

- **Trigger:** the agent is about to assert something about this repository whose subject resolves in the working tree · **Preconditions:** the palace holds at least one relevant memory
- **Main flow:**
  1. The agent issues one recall.
  2. Each hit either carries its whole memory, or reports that it does not, with its full length and the id that fetches the rest.
  3. Each hit reports every range it disclosed, so the "do I need a second call?" decision is made on the truth rather than an under-count.
  4. The agent acts without a second call, or knows exactly what a second call would get.
- **Failure paths:** a. at step 2, a memory does not fit the response budget → the hit is explicitly partial, carrying its full length and its fetch id, rather than silently fragmentary (F-2); b. at step 3, a hit disclosed more than its primary window → coverage counts every disclosed range, not just the first (F-1); c. at step 1, the budget forced hits off the page → the page says how many it withheld, so a short page is legible as short (F-7)

  ⚠ **Ordering is deliberately absent from this use case.** An earlier draft had step 3 demote a superseded hit below its correction, on a measurement now RETRACTED (M-5b). Marking already ships and is unchanged (M-8); an ended record is already absent from a default page (M-7). Any *ordering* effect stays behind ADR-004's open issue #34, whose `justified` verdict is still open — confirmed 2026-08-28 against ADR-004's amendment of 2026-08-26, which narrowed the categorical wiring bar but expressly kept the gate on *any RANKING use of a graph read*. Resolved as a Non-Goal rather than left open (Grill Log 10).
- **Postconditions:** the agent has acted on whole, current content, or knows exactly what was withheld.

### UC-2: Record author files a correction

- **Trigger:** an author has found a filed record to be wrong · **Preconditions:** the record exists and is current
- **Main flow:**
  1. The author files the correction against the record it corrects.
  2. The corrected record is ended and linked to its successor.
  3. Exactly one current record remains on that subject.
- **Failure paths:** a. at step 2, the write fails part-way → no fork is left: either the predecessor is still current alone, or the successor is current alone, never both; b. a concurrent author corrects the same record → one of them wins and the other is refused, rather than both succeeding into two current claims
- **Postconditions:** a reader of a default page meets one framing of the subject, not several.

  ⚠ **This use case is why UC2-S1 and UC2-S2 sit here.** They were originally filed under the
  measurement use case, whose flow they do not describe, while the `Record author` actor had no use
  case at all. The scenarios did not move; the heading did.

### UC-3: Measurement owner establishes a baseline and quotes a rate

- **Trigger:** a mechanism intended to change read behaviour is proposed, or a read rate is about to be quoted · **Preconditions:** none for the baseline; a recorded baseline before any quote
- **Main flow:**
  1. The counting rule is written down as an artifact — what a read is, and the window it is attributed to.
  2. The baseline is collected under that published rule, naming it by content.
  3. Only then may a mechanism ship; and any later rate is quoted with the rule it was measured under, after comparing that rule against the baseline's.
- **Failure paths:** a. a mechanism is marked done with no baseline recorded → the gate fails; b. the counting rule's content differs from the baseline's → the baseline is invalidated, the comparison is refused, and the change is named
- **Postconditions:** every quoted rate names the rule it was measured under, and no rate is quoted across a rule change.

  ⚠ **One use case rather than two, deliberately.** Establishing a baseline and quoting a rate were
  split, which left each half with a single scenario and neither with both a happy and a failure
  path — the split was in the prose only, since F-5 and F-6 are two halves of one obligation.

## Scenarios

### UC1-S1 [happy] A hit reports every range it disclosed [@spec] → `internal/mcpserver/readcost_spec_test.go::TestF1CoverageCountsEveryDisclosedRange`

```gherkin
Given a memory long enough to be disclosed as a window plus regions
When a caller issues one recall that matches text in several places
Then the reported coverage counts the primary window and every region returned
And a caller comparing it against a threshold is comparing against the truth
```

### UC1-S2 [failure] A partial hit says so, and says how to complete it [@spec] → `internal/mcpserver/readcost_spec_test.go::TestF2NoHitIsSilentlyPartial`

```gherkin
Given a memory larger than the response budget allows to be disclosed whole
When a caller issues one recall that matches it
Then the hit is marked partial, reports the memory's full length
And carries the id that fetches the remainder
```

### UC1-S3 [happy] A caller never joins chunks [@spec] → `internal/mcpserver/readcost_spec_test.go::TestF4ChunkingCreatesNoReassemblyObligation`

```gherkin
Given a memory several times longer than the chunk size
When a recall matches text inside its last chunk
Then one hit is returned whose content is the memory's content
And no caller-side reassembly is required to obtain it
```

### UC1-S4 [failure] A short page says it is short [@spec] → `internal/mcpserver/readcost_spec_test.go::TestF7APageReportsWhatItWithheld`

```gherkin
Given more matching memories than the response budget can carry whole
When a caller issues one recall
Then the page reports how many hits it withheld
And a caller can tell a short page from an exhausted corpus without a second query
```

### UC2-S1 [happy] A correction leaves one current successor [@spec] → `internal/palace/readcost_spec_test.go::TestF3ACorrectionLeavesOneCurrentSuccessor`

```gherkin
Given a memory that a later record corrects
When the correction is written through the advertised correction operation
Then exactly one record about that subject is current
And it is linked to the ended predecessor
```

### UC2-S2 [failure] A correction that fails part-way leaves no fork [@spec] → `internal/palace/readcost_spec_test.go::TestF3ACorrectionLeavesOneCurrentSuccessor`

```gherkin
Given a correction whose predecessor spans several chunks
When ending one of those chunks fails, or a second correction races it
Then the operation does not leave two competing current records
And the failure is reported rather than half-applied
```

### UC3-S1 [happy] A baseline names the rule it was measured under [@implemented] → `internal/repohygiene/readrule_spec_test.go::TestF5ABaselineNamesItsCountingRule`

```gherkin
Given a counting rule committed as an artifact
When a baseline is recorded
Then the baseline names that rule by its content, not by description
```

### UC3-S2 [failure] A rate quoted across a rule change is refused [@spec] → `internal/repohygiene/readrule_spec_test.go::TestF6ARuleChangeInvalidatesItsBaselines`

```gherkin
Given a baseline recorded under one counting rule
When the counting rule's content changes
Then that baseline is invalid and a rate quoted from it is a defect
And the gate names the rule change rather than reporting a comparison
```

## Facts

| ID | Assertion (invariant / behavior) | Test (`path::name`) | Tag | Cmd (optional) |
|----|----------------------------------|---------------------|-----|----------------|
| F-1 | A hit's reported coverage counts every disclosed range — the primary window and every returned region — so a caller deciding whether it needs a second call decides on the truth. | `internal/mcpserver/readcost_spec_test.go::TestF1CoverageCountsEveryDisclosedRange` | @spec | |
| F-2 | No hit is silently partial: a hit that does not carry its whole memory reports that, its full length, and the id that fetches the rest. A memory larger than the response budget is ALWAYS partial-with-fetch-id — never returned whole by growing the budget for it — and the completion path is `am_get_drawer`, never paging: `am_search` gains no cursor (resolved 2026-08-28, Grill Log 7 and 8). | `internal/mcpserver/readcost_spec_test.go::TestF2NoHitIsSilentlyPartial` | @spec | |
| F-3 | An advertised correction leaves exactly ONE current successor, linked to the ended predecessor — including under partial failure and concurrent correction. ⚠ This constrains past a deliberate choice: `supersede.go:84-87` writes the successor FIRST *"so a failure leaves the old memory current rather than leaving the team with nothing"*. That trade is the two-current-records state this fact forbids; the ADR has to say which it wants, not assume. ⚠ The atomicity requirement is OWNED HERE and does not amend ADR-038, which owns identity rather than the write's atomicity (resolved 2026-08-28, Grill Log 9). And it is a WRITE-SIDE invariant only: nothing in this spec touches ordering, which stays behind ADR-004 issue #34's still-open `justified` verdict (Grill Log 10). | `internal/palace/readcost_spec_test.go::TestF3ACorrectionLeavesOneCurrentSuccessor` | @spec | |
| F-4 | Chunking creates no reassembly obligation: a caller never has to join chunks to obtain a memory's content. Chunk metadata may remain as diagnostics. | `internal/mcpserver/readcost_spec_test.go::TestF4ChunkingCreatesNoReassemblyObligation` | @spec | |
| F-5 | No mechanism ships before a baseline is recorded, and the baseline names the counting rule it was measured under by content, not by description. ⚠ The rule counts **reads ACTED ON WITHOUT A SECOND CALL**, not read FREQUENCY — fixed 2026-08-28, before any collection, per this fact's own requirement and Grill Log 13. Frequency is what the ADR-041 instrument already counts, it is not what F-1/F-2/F-7 deliver, and a mechanism that made every hit trustworthy could leave it unmoved. | `internal/repohygiene/readrule_spec_test.go::TestF5ABaselineNamesItsCountingRule` | @implemented | `go test -tags readcostspec ./internal/repohygiene/ -run TestF5ABaselineNamesItsCountingRule -count=1` |
| F-6 | Changing the counting rule invalidates every baseline taken under the previous one; a rate quoted across a rule change is a defect. | `internal/repohygiene/readrule_spec_test.go::TestF6ARuleChangeInvalidatesItsBaselines` | @spec | |
| F-7 | A page reports how many hits it withheld. With no cursor (M-10) a withheld hit is unresumable, so the count is the only evidence it existed — a short page must be legible as short rather than read as "that is all there is". | `internal/mcpserver/readcost_spec_test.go::TestF7APageReportsWhatItWithheld` | @spec | |

## Domain

**Memory** — one record with an identity, a wing, a room and content. **Chunk** — an embedding-time division of a memory; not a unit a caller addresses. **Coverage** — the fraction of a memory a hit discloses. **Supersession** — a directed relation from a correcting record to the record it corrects. **Counting rule** — the published definition under which a read rate is measured.

## Contracts Touched

| Surface | Change | Consumers |
|---------|--------|-----------|
| `am_search` hit shape (`content_truncated`, `content_coverage`) | modify | every MCP client; the bootstrap protocol |
| `chunk_index`, `parent_id` | retain | **every recall caller, not only `am_get_drawer`.** Declared on `drawerView` (`drawers.go:110`, `:113`) and reaching the wire on BOTH shapes, because `searchHitView` EMBEDS `drawerView` bare (`:684`) and Go promotes an embedded struct's JSON fields. `ChunkIndex` carries no `omitempty`, so every `am_search` hit already has it; `internal/mcptest/regions_test.go:193-199` reads `hit["chunk_index"]` over the real MCP transport |
| `am_get_drawer` (`whole` parameter) | modify | agents following `AGENTS.md`'s read guidance |
| The counting rule artifact | add | whoever quotes a read rate |
| `am_update_drawer` / `supersedeInto` atomicity | modify | any writer correcting a memory |
| `am_list_drawers`, `am_bootstrap` | modify | same disclosure reporting as `am_search` |
| `snippet_chars`, `regions`, `memory_id`, `chunks_matched` | retain | ADR-024 compatibility — F-4 does not remove them |

## Non-Goals

- **Retrieval ranking quality.** ADR-001/002/003 own it, and ADR-041 established the answer was already at rank 1 when it was not asked for. This spec is about access cost and trust, not about what comes back first.
- **Making hooks louder in the common case.** ADR-041 F-6's scarcity rule stands.
- **Deciding which entry-point layer is canonical.** Filed at `BACKLOG.md`, "Four spellings of one entry point"; an ADR-level decision. This spec proceeds independently — F-1..F-6 hold whichever layer wins. If that decision materialises a new read path, F-1 and F-2 extend to it by amendment.
- **Raising any number the ADR-041 instrument reports.** That instrument latches once per session and cannot see what a proximity mechanism changes; see `BACKLOG.md`.
- **Changing what matches.** Chunking may remain the embedding and matching unit; F-4 constrains what a caller receives, not how retrieval finds it.
- **A cursor or offset on `am_search`.** Resolved 2026-08-28: `am_get_drawer` is the only completion path, so F-2's "the id that fetches the rest" is the whole contract and F-7's withheld count is the only thing a caller learns about hits it did not get. A second resumption contract for the same job is the cost this spec exists to avoid.
- **ORDERING, in any form.** Resolved 2026-08-28 and stated as a boundary rather than an omission: ADR-004's gate on *"any RANKING use of a graph read"* survived its 2026-08-26 amendment intact, and issue #34's `justified` verdict is still open. F-3 is a write-side invariant; F-1, F-2 and F-7 are disclosure. Nothing here demotes, promotes or reorders anything, and the ADR this spec becomes may not introduce ordering without that verdict.
- **Landing the bindings green in the same PR as the ADR.** Resolved 2026-08-28: the red lane stays behind `-tags readcostspec`, collected with `go test -tags readcostspec ./...`. CI runs `go test ./...` on every push to main, and a deliberately-red binding in the default lane makes the tree's own signal unreadable — the failure this repository has already paid for. The tag comes off in the commit that turns each binding green.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Returning whole memories shrinks a page from ten hits to three | High | Med | F-2 makes each hit's completeness explicit, so a short page is legible rather than silently narrow. F-7 now carries the page-level half — old F-2's withheld-count obligation, dropped when F-2 was recast around partial-marking and restored 2026-08-28 as its own fact rather than smuggled back into F-2 |
| Returning whole memories inflates response size for long records | Med | Med | The requirement is on disclosure per hit, not on hit count; the budget still bounds the response |
| A chain of corrections, each superseding the last, leaves an ambiguous head | Low | Med | F-3 constrains the write side only — exactly one current successor per correction — so a chain resolves to one head by construction. It says nothing about order, and after M-5b's retraction this spec makes no ordering claim at all |
| The new counting rule is itself insensitive, repeating ADR-041's failure | Med | High | F-5 requires the rule to be an artifact fixed before collection, and a stated demonstration that a relevance-improving mechanism moves it |
| Callers depend on `chunk_index` / `parent_id` today | Med | Med | F-4 retains them as diagnostics; a repo-wide search found no production consumer, but external clients cannot be ruled out |
| A memory larger than the response budget can never be whole | High | Med | F-2 makes it explicitly partial with its full length and fetch id, rather than silently fragmentary. `am_search` has no cursor, so completion is `am_get_drawer`, not paging |
| F-3's atomicity requirement is larger than it looks | Med | High | `supersedeInto` is not atomic and has no compare-and-swap; the fact names partial failure and concurrency deliberately so the ADR cannot scope them away |

## Open Questions

<!-- All six resolved by Zy, 2026-08-28. Each is recorded in Grill Log rows 7-12 with the Fact or Non-Goal that now carries it; none was closed by deletion. -->

## Verify

```bash
spec-verify --spec docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md
```

## Grill Log (appendix)

| # | Question | Fact | Decision |
|---|----------|------|----------|
| 0 | Scouted measurements presented as one batch for veto | non-behavioral | Accepted without amendment; they are observations, recorded under Problem > Evidence as M-1..M-7 rather than as Facts requiring bindings |
| 1 | Must a hit be actionable without a second call? | F-1 | Accept — partial content was acted on twice in one session, and `content_coverage` under-reports what was disclosed (M-3). ⚠ The "3% coverage" figure this row was accepted on is RETRACTED (M-3b): it was the caller's own `snippet_chars: 90` read back. Real disclosure is 23–27%. The under-count is the fact; the headline number was wrong |
| 2 | Under a constrained budget, fewer whole or more fragments? | F-2 | Accept — fewer whole; a fragment that cannot be acted on has negative value. ⚠ Accepted as "plus a withheld count", which recasting F-2 around partial-marking dropped: nothing carries that obligation now, and it is an Open Question rather than a silent loss |
| 3 | Must a superseded memory be marked and prevented from outranking its correction? | F-3 | Accept as asked, then AMENDED after review. The ranking half rested on M-5b, now RETRACTED — distance does not decide order and the correct record in fact came back first. Marking already ships (M-8) and an ended record is already absent (M-7), so neither needed deciding. What survived is M-6: the cheap correction path leaves the wrong record CURRENT. F-3 was re-pointed at that — a write-side invariant, no ordering — and the ordering question is deferred to ADR-004 #34 |
| 4 | Is a memory one unit to its caller, or N chunks? | F-4 | Accept — chunking is an embedding-time detail; it may remain the matching unit |
| 5 | What is the success criterion, and what stops it being insensitive? | F-5, F-6 | Accept — the counting rule is an artifact fixed before collection, and changing it invalidates the baseline |
| 6 | Does this spec wait on the entry-point decision? | non-behavioral | Proceed independently; recorded in Non-Goals, with amendment named if a new read path lands |
| 7 | Should a memory larger than the response budget be returnable at all, or always partial-with-fetch-id? | F-2 | ALWAYS partial-with-fetch-id, resolved 2026-08-28. Growing the budget for one long record makes the partial flag conditional on record size, which is the same "is this all of it?" question F-2 exists to remove. Written into F-2 rather than left as a gloss |
| 8 | Is `am_search` gaining a cursor, or is `am_get_drawer` the only completion path? | F-2 | `am_get_drawer` only. A cursor is a second resumption contract for a job F-2's fetch id already does, and M-10 records that there is no offset today — so this is declining new surface, not preserving old. Recorded as a Non-Goal; the consequence is that a withheld hit is unresumable, which is exactly why row 11 lands |
| 9 | Does F-3's atomicity requirement belong here or as an ADR-038 amendment? | F-3 | Here. ADR-038 owns IDENTITY — the opaque id, `content_key`, ended-not-overwritten. `supersedeInto`'s lack of atomicity and compare-and-swap (M-9) is a read-cost defect: it is what lets one page carry two current framings of one subject (M-5, M-6). Amending ADR-038 would move the fact away from the evidence that motivates it |
| 10 | Does anything here touch ORDERING? | F-3 | No, and it is now a stated Non-Goal rather than an open question. Checked 2026-08-28 against ADR-004's 2026-08-26 amendment (owner sign-off, M: *"P1 - i agree with change"*): the bar was narrowed to permit read-path ANNOTATION and expressly KEPT on *"any RANKING use of a graph read"*, with issue #34's verdict still gating it. F-3 is write-side; F-1/F-2/F-7 are disclosure. The ADR may not reintroduce ordering without #34 |
| 11 | Should a page report how many hits it withheld? | F-7 | Yes — as its own fact. It is a NEW obligation, not a restatement, so folding it back into F-2 would have hidden a scope increase inside an existing binding. With no cursor (row 8) the count is the only evidence a withheld hit existed, and without it a short page reads as an exhausted corpus |
| 13 | What quantity does F-5's counting rule count? | F-5 | Reads ACTED ON WITHOUT A SECOND CALL. Fixed 2026-08-28 before collection, which is F-5's own requirement and the only moment it is cheap — F-6 makes every baseline taken under a superseded rule void. Read FREQUENCY was the obvious choice and is rejected on evidence: reads cost ~30 ESTIMATED output tokens against ~400-525 for a write — BPE arithmetic at ±20%, so the ordering is the claim and not the magnitude — meaning price is not why they are rare; and `am_recall_stats` MEASURED 6 searches against 18 writes, 3.0 per read, over a two-hour window under an explicit instruction to read more. The second figure is a counter reading and the first is not, and they are labelled differently on purpose. A frequency rule would repeat ADR-041's failure in a new location — counting the quantity that is easy to count rather than the one being claimed |
| 12 | Does the red-binding lane stay behind `-tags readcostspec`? | non-behavioral | Stays behind the tag. CI runs `go test ./...` on every push to main; a deliberately-red binding in the default lane makes the tree's own signal unreadable, and this repository has already paid for a green-looking gate. The tag comes off per binding, in the commit that turns it green |
