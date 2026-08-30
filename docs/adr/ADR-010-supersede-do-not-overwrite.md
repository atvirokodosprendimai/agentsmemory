# ADR-010: A memory is ended, not overwritten — and retraction is not erasure

> ## CLOSED 2026-08-27 — superseded by ADR-038
>
> **This decision was never wrong; it was never built.** Proposed 2026-08-20, 0 of 3 tasks, still
> pending seven days later while the store kept overwriting and deleting in place.
>
> It is absorbed **in full** into
> [`ADR-038: Refer by the id, dedupe on the content, end instead of overwrite`](ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md) —
> the validity window, retraction-versus-erasure, the required `reason`, current-only recall, the
> affordability argument and the pre-registered falsification all move there unchanged in substance.
> Its three task files are **frozen and must not be executed**: ADR-038 re-authors them as its T1, T4
> and T5, because composing the two records changed them.
>
> **What composing them found, and what neither record could see alone:** ADR-038's unique index on
> the content key is wrong unless it is scoped to CURRENT rows. The identity half does not know what
> "current" means; the lineage half does not know there is an index. Shipped separately they produce
> a store where text that was once superseded can never be filed again, and no gate in either record
> would have caught it. That interaction is why this is one decision now.
>
> **This closure is IN EFFECT — ADR-038 was Accepted on 2026-08-27**, and the two were accepted as
> one decision. Everything below is kept verbatim as the reasoning ADR-038 inherits — read it there,
> and execute it there. These task files stay frozen.

**Status:** Superseded by ADR-038
**Date:** 2026-08-20
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-004 (supersession in ranking — this is supersession in storage), ADR-001 (recall must be able to say "I don't know"; a superseded record changes what "current" means)
**Invalidates:** none — checked. It does REVISE work landed in this repository on 2026-08-20: `Service.Delete` was hardened to remove every chunk of a memory, and `Service.Update` to refuse a multi-chunk content edit. Both are correct within the current model and both become the wrong primitive under this one. Said plainly because an ADR that quietly reverses last week's fix is how a team stops trusting its own record.
**Served-path change:** Drawers gain a validity window and recall returns only what is current, so `am_search` stops surfacing memories the team has since retracted. Status is Proposed; 0 of 3.

## Context

The write path should end a record rather than overwrite it: an update sets `valid_to`, and the log keeps history with validity dates. The analogy that makes it concrete is a legal one. **A ruling is made under the laws valid on that day.** Delete the superseded law and the ruling stops being legible — the reasoning no longer connects to anything it was reasoning about.

This repository already holds that principle, and applies it to the wrong half of the store.

`db/migrations/00010_kg.sql`, on a knowledge-graph fact:

> A fact is CURRENT while its `valid_to` is empty; setting `valid_to` ends it (**it becomes historical but is never deleted**).

`db/migrations/00006_drawers.sql`, on a drawer: no `valid_to`, no `superseded_by`, no revision table. `Repo.Update` is an in-place `Updates()` — the prior content is gone the moment a correction lands. `Repo.Delete` removes the row and its vector.

So the temporal model covers **facts** and not **reasoning**. That is backwards. A fact — "service X deploys to host Y" — is cheap to re-derive from the running system. A Class-B record — why Kafka was replaced, what was tried first, what the constraint was — is the thing that exists nowhere else, and it is the one the store lets an agent overwrite or delete outright.

There is a second, sharper problem hiding under the word *delete*, and it is why this needs a decision rather than a schema patch. Two different operations are conflated:

- **Retraction** — an agent decides a memory is no longer true. The old text is *evidence*: "we used Kafka until March, then replaced it because rebalancing stalled" is a better record than either half alone, and the rejected alternative is what makes the current decision legible.
- **Erasure** — an operator decides data must not exist. A secret was filed, a customer asked, a retention policy applies. Here the old text must genuinely go, vectors included.

`am_delete_drawer` is exposed to **agents** and performs **erasure**. An agent doing the first gets the second, irreversibly, and the palace's own protocol tells it to correct memories that turn out wrong. It is not alone: five agent-facing tools destroy — `delete_drawer`, `delete_tunnel`, `delete_hallway`, `delete_wing`, `merge_wing`.

**And there is a third gap, which is the one that actually costs money.** Nothing anywhere records *why* something stopped applying. `am_kg_invalidate` takes `subject`, `predicate`, `object` and `ended` — a date, and no reason; the KG schema has `valid_to` and no column for one. So even the half of the store that keeps its history keeps only *that* a fact ended, never *why*.

That is the rediscovery tax wearing a different hat. A session that finds an ended record with no reason is in the same position as one that finds nothing: it re-derives, reaches the same idea, and re-litigates a decision the team already took. What is needed is a mechanism by which an invalidation also records that a decision was already taken not to apply something, and why. An invalidation is not an absence. It is a decision, and it is Class-B knowledge of precisely the kind §5 of the paper argues is irrecoverable.

## Existing Primitives Audit

- **KG validity windows** (`valid_from` / `valid_to`, `KGInvalidate`) — the model this ADR extends to drawers. Reuse the semantics verbatim rather than inventing a second vocabulary: a record is current while `valid_to` is empty, and ending it never deletes it.
- **`Drawer.ParentID`** — already expresses "these rows are one memory". Reshape: a supersession chain is the same shape one level up, and reusing it keeps chunking and versioning from becoming two competing notions of identity.
- **`content_date`** — the date a memory is ABOUT, already used by the recency reorder. Distinct from validity and must stay distinct; conflating "when this was true" with "when we believed it" is how temporal stores become unreadable.
- **`Service.Delete` / `Service.Update`** — both hardened on 2026-08-20 and both re-scoped here. Reshape, and say so.
- **`DeleteWing` / `MergeWing`** — operator-facing, already outside the agent surface. Reuse as the precedent for where erasure belongs.

## Decision

Drawers gain a validity window, and the two operations are separated at the tool surface.

**Retraction (agent-facing), and it carries a reason.** Two shapes, one mechanism:

- `am_invalidate_drawer(id, reason)` — this memory no longer applies, and here is why. Nothing replaces it.
- Correcting a memory writes a NEW record and ends the old one. `am_update_drawer`'s content edit becomes a supersede: the returned id is the new record's, and the response names the one it replaced.

**`reason` is required on both.** An invalidation without one records that something ended and destroys the only thing worth keeping about the ending. A required free-text field is a weak guarantee — an agent can write "obsolete" — but it is the difference between a field nobody fills and a field somebody can be asked about, and it costs one argument.

**The reason travels with the CURRENT record, not only with the ended one.** This corrects a mistake in the first draft of this ADR: it hid history behind an explicit flag and also expected retractions to prevent re-litigation, and those two cannot both be true. A session about to redo a rejected thing does not know to ask for history — that is precisely what it does not know. So the live record carries what it superseded and why, and the reason reaches the default recall path while the stale TEXT does not.

**Erasure (operator-facing).** Genuine removal — row and vector — moves behind the operator surface where `delete_wing` already lives. It remains possible, because a store that cannot forget a leaked secret is not deployable, but it stops being something a confused agent reaches for while trying to be helpful.

**Recall returns current records; the reason rides along.** Superseded TEXT does not compete with its correction — that failure is documented here already, where an update rewrote chunk 0 while chunk 1 stayed live with its own embedding, still answering with the retracted claim. But the current record names what it replaced and why, so a reader of the live memory learns the decision was taken and does not re-take it. Full history stays behind an explicit flag for the cases that want the whole chain.

**Why accumulation is affordable here, which is the load-bearing claim.** The position this ADR is built on is that everything the palace can accumulate should be kept, because the accumulation *is* the value. This ADR is what makes that position payable rather than merely principled.

Keeping everything has a cost, and it is not disk. It is **retrieval**: a larger, more heterogeneous corpus measurably retrieves worse, because unrelated records do not remove the answer — they add competitors ahead of it. That is already this project's own recorded finding, and it is why deletion exists at all.

**The evidence for that, named, because a load-bearing claim should not rest on a pointer to itself.** Two results are on record and they are not equally strong. The first is MRR 0.83 on a focused corpus against 0.34–0.39 on a large mixed one — which is *confounded*: different corpora AND different question sets, so it cannot separate "mixing hurts" from "those questions are harder", and it should not be quoted alone. The second is the one that carries the claim: **the closet prior, where one source lifting fifty unrelated drawers cost 0.10 MRR.** Same corpus, controlled intervention, and it measures the mechanism directly — promote unrelated records, watch the answer fall. Cited here because this paragraph is the argument the rest of the ADR stands on, and it previously asserted the finding without naming which measurement produced it.

One boundary on how far the claim generalises, added 2026-08-21 after a survey of the published record: the IR literature does *not* establish that widening retrieval scope degrades retrieval RANKING in general — selective search shows a well-chosen subset matches exhaustive search at far lower cost, which is a different statement. Degradation IS established at the ANSWER level (a router at 80.05 accuracy against 67.76 for all-source broadcast; a single related-but-non-answering passage costing up to 25 points). So the claim above holds for this system on this system's own measurement, and should not be restated as a general property of vector search. `am_delete_drawer` is a workaround for the absence of a validity window: with no way to mark a record ended, the only way to stop it competing in recall is to destroy it.

Give a record a validity window and that trade disappears. An ended record leaves the default recall path without leaving the store, so accumulation stops costing precision. **Deletion is not the price of good retrieval once ending is possible** — and that is the argument for keeping everything, stated so it can be checked.

**Storage and payload are separate budgets, and this ADR only spends the first.** The obvious objection to keeping everything is the context window: if the store holds the whole history, what reaches the model? The answer is that the payload has never been a function of corpus size. A default recall returns `DefaultSearchLimit` = 5 hits at `DefaultSnippetChars` = 400 characters each — a window centred on the match, not the memory — so roughly 2,000 characters, on the order of 500 tokens, whether the palace holds 200 drawers or 200,000. Ending records changes the candidate POOL, and the pool was already bounded by `limit × snippet_chars` before it reached anybody's context.

So "keep everything" costs disk and index, and costs the context window nothing. That is the whole reason the validity window makes accumulation affordable rather than merely principled.

**The one payload cost this ADR does add, and its bound.** Carrying "supersedes X, because Y" on the live record puts the reason on the default path — which is the point — and reasons are free text. Five hits each carrying a 120-character reason is ~600 characters, a 30% increase on a ~2,000-character page, which is real. So the reason is **truncated to 200 characters in a recall response**, with the full text reachable through `include_history` alongside the record it ended. A retraction whose reasoning needs more than 200 characters is a memory in its own right and should be filed as one.

**Pre-registered falsification — and a note on what it deliberately does not measure.**

The first draft proposed retracting the history chain if `include_history` were rarely called. That was wrong, and it is worth saying why rather than quietly replacing it: read frequency is a bad proxy for the value of an archive. A decision record's payoff is rare and large — the one time someone asks why an alternative was rejected and the answer exists. Measuring it by call count would retract a feature whose value model is low-frequency and high-consequence, which is the same error as cancelling insurance because no claim was filed. Struck.

What *is* falsifiable is the engineering claim this ADR actually makes:

> **Accumulation must not degrade recall of current records.** Measured on a corpus where superseded records outnumber current ones by at least 2:1, MRR over current-only cases must be within noise of the same case set measured before the ended records existed.

If that fails, the exclusion is not working and ended records are competing after all — which is a defect in this ADR's implementation, not a reason to start deleting. The remedy is to fix the filter, and the second remedy, only if the first is impossible, is to move ended records to a separate index.

The `reason` field gets the same correction. Its quality is measured — median length, and a sample read by a human — but the measurement **improves the prompting, it does not retract the field**. A reason that reads "obsolete" is a case for a better tool description, not an argument that recording why a decision changed was a mistake.

## Alternatives Considered

- **Leave it; agents can file a new drawer and delete the old.** Rejected: that is the current behaviour, and it destroys the rejected alternative — the specific thing that is irrecoverable at any price, because a rejected alternative leaves no trace in the artifact.
- **Soft-delete with a `deleted_at` tombstone.** Rejected as insufficient rather than wrong: it records THAT a record died and not what replaced it. "Kafka until March, then NATS, because rebalancing" needs the link, and a tombstone has nowhere to put it.
- **Full event sourcing — an append-only log as the source of truth, state as a projection.** Rejected for now, and the reason matters because this is the stronger version of the same idea: the store already has a working row model with vectors, chunking and anchors hanging off drawer identity, and rebuilding that as a projection is a rewrite whose risk is not justified by the benefit a validity window already delivers. A validity window IS the append-only property for the one thing that needs it. Revisit if a second consumer of the history appears.
- **Only supersede; no standalone invalidate verb.** Rejected: plenty of retractions replace nothing. "We are not doing this after all" has no successor record, and forcing one would make an agent invent a placeholder memory to express an absence.
- **Version everything, keep every revision.** Rejected: a typo fix would then create a revision, and the history that matters — a decision changing — would be buried in noise. Supersession is a deliberate act; a correction to spelling is not.

## Component / Boundary Impact

`internal/palace` keeps ownership of drawer identity and gains the validity window. `internal/mcpserver` moves one tool from the agent surface to the operator one. The vector index gains a rule — superseded records leave the default search — which is a change to what `Search` retrieves, not to how it ranks.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `drawers.valid_to`, `drawers.superseded_by` | add (migration) | `db/migrations` | `internal/palace` |
| `am_update_drawer` content edit | change — supersedes instead of overwriting; returns the new id and names the ended one | `internal/mcpserver/drawers.go` | every agent that corrects a memory |
| `am_invalidate_drawer(id, reason)` | add | `internal/mcpserver/drawers.go` | any agent retracting a memory |
| `drawers.ended_reason`, `drawers.ended_at` | add (migration) | `db/migrations` | recall, and the current record's provenance |
| recall response: `supersedes` + reason, truncated to 200 chars | add | `internal/mcpserver/drawers.go` | every recall — bounded so accumulation never grows the payload |
| `am_delete_drawer`, `am_delete_tunnel`, `am_delete_hallway` | change — leave the agent surface | `internal/mcpserver` | agents (removed), operators (retained) |
| `am_kg_invalidate` `reason` | add — required, mirroring the drawer verb | `internal/mcpserver/kg.go` | anyone reading why a fact ended |
| `am_search` / `am_list_drawers` | change — current records only, with an explicit history flag | `internal/mcpserver` | every recall |
| operator erasure path | add | `cmd/server` | operators, retention policy |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `valid_to` / `superseded_by` columns | T1 | T2, T3 | No — additive; empty `valid_to` is every existing row, which is correct |
| supersede semantics on Update | T2 | T3 | **Yes** — `am_update_drawer` returns a different id than the one it was given |
| current-only recall + history flag | T3 | T3 | Yes — search stops returning superseded records |

## Implementation

`tasks/README.md` — three tasks.

## Consequences

- **Positive:** the record that explains why a decision changed survives the change. "We used X until March, then Y, because Z" becomes expressible, and it is the shape a reader actually needs.
- **Negative:** the store grows monotonically, and a correction costs a row rather than reusing one. Superseded rows keep their vectors, so the index grows too. This is accepted as the intended trade rather than tolerated as a cost: the growth is what is being bought, and it is affordable only because ended records leave the default recall path.
- **Neutral:** `am_update_drawer` returns a new id. Any caller that assumed the id was stable across a content edit must be updated — inside this repository the only such caller is the tool itself.

## Out of Scope

- Full event sourcing of the whole store (deferred: docs/adr/BACKLOG.md — see Alternatives; revisit when a second consumer of history exists)
- Versioning wing/room moves (permanent: a move is not a claim about the world, so ending and re-filing would record noise as history)
- Retention or automatic pruning of superseded records (permanent: the position this ADR is built on is that accumulation is the value, and a pruner would spend engineering effort undoing it. Erasure stays available to an operator for the cases that require it — a leaked credential, a deletion request — which is a legal and safety path, not a housekeeping one)
- Applying the same model to diary entries (deferred: docs/adr/BACKLOG.md — a diary is already append-only by construction; nothing overwrites an entry)
- Removing `merge_wing` and `delete_wing` from the OPERATOR surface (permanent: they are the erasure path this ADR requires to exist; the decision is that agents cannot reach them, not that nobody can)
- Structured reasons — a taxonomy of why something ended (deferred: docs/adr/BACKLOG.md — free text first, because a taxonomy chosen before there are reasons to classify is a guess, and the falsification below will show what people actually write)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A required `reason` gets filled with "obsolete" and buys nothing | High | Med | Accepted and measured rather than designed around: T2 records reason length and the falsification below reads it. A taxonomy imposed now would be a guess about reasons nobody has written yet |
| Superseded records leak back into default recall | Med | High | T3's falsification: a superseded record must be unreachable by every default route — search, list, and get — checked from an end-to-end scenario rather than a unit test, since this exact failure shipped once already as a live chunk 1 |
| Agents cannot erase a wrongly-filed secret and file it anyway | Med | High | The operator erasure path lands in the same task, and the refusal text names it |
| The store grows without bound | High | Low | Accepted as the point, not tolerated as a cost. The falsification measures whether growth harms recall of CURRENT records; if it does the filter is broken, and the remedy is the filter |
| The migration mis-handles existing rows | Low | High | Empty `valid_to` means current, so every existing row is correct with no backfill; T1 asserts that on a copy of a real database |

## Rollback

The migration is additive: `valid_to` and `superseded_by` default empty, and every existing row reads as current. Reverting the code restores overwrite-and-delete, and the extra columns are ignored — no data is lost by rolling back, only by rolling forward and then erasing. Records superseded while the feature was live remain readable as ordinary drawers after a revert, since they differ only by a column nothing reads.

## Follow-ups
