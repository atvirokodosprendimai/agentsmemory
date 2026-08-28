# ADR-038: Refer by the id, dedupe on the content, end instead of overwrite

**Status:** Accepted
**Date:** 2026-08-27
**Owner:** M
**Spec:** None — no spec stage; grounded in a recomputation of every drawer id in the live palace against `DrawerID`'s own recipe on 2026-08-27, and in three prior records that each deferred to the primitive this ADR adds. Numbers recorded inline.
**Supersedes:** **ADR-010, "A memory is ended, not overwritten — and retraction is not erasure"** (Proposed 2026-08-20, 0 of 3 tasks). Its decision is absorbed here in full — validity window, retraction-versus-erasure, the required `reason`, current-only recall — and its record is closed pointing at this one. **ADR-010's closure is now IN EFFECT** — this record was Accepted 2026-08-27, and the two were accepted as one decision.
**Cross-references:** ADR-004 (supersession in ranking — this is supersession in storage, inherited from ADR-010), ADR-013 (a page of memories, not chunks), ADR-015 (a wing merge must correct the index it invalidates — **this ADR closes its deferral**), ADR-019 (the agent sees a quarter of the memory — rejected re-chunking on the same id grounds), ADR-024 (rank memories, not chunks), ADR-027 (a maintained document is a set of records — **this ADR unblocks its rejected alternative**), ADR-036 (the knowledge graph on the read path — `kg_triples.source_drawer_id` is a consumer of drawer identity), `internal/palace/chunk.go:148` (`DrawerID`), `:164` (`diaryEntryID`), `internal/palace/service.go:660` (the mint), `:677` (`purgeSource`), `internal/palace/repo.go:85` (`OnConflict{UpdateAll: true}`), `:377` (the id-is-stable contract), `internal/palace/admin.go:306` (`MergeWing` relabels the wing in place), `internal/palace/import.go:21` (import idempotency rests on the recomputed id), `internal/store/qdrant/vector.go:29` (`pointID` = UUID5 of the drawer id), issue #39 part 2
**Governs:** `internal/palace/chunk.go`, `internal/palace/repo.go`, `internal/palace/service.go`, `internal/palace/import.go`, `internal/palace/mine.go`, `internal/palace/copywing.go`, `internal/palace/admin.go`, `internal/palace/anchors.go`, `internal/mcpserver/drawers.go`, `internal/mcpserver/kg.go`, `db/migrations/*_drawers_validity_window.sql`, `db/migrations/*_drawers_content_key.sql`
**Invalidates:** **ADR-010, which this record supersedes and closes** — see the Supersedes header; its three task files are frozen and must not be executed, because the tasks below re-author them for the interaction with the content key. Otherwise none — checked. Grepped ADR-001..037 for `DrawerID`, `drawer id`, `content hash`, `idempotent`, `re-chunk` and `new ids`: no accepted ADR pins the id to its content, and the two records that touch it (ADR-015, ADR-027) both **defer** to this decision rather than depend on the current shape. It **closes** ADR-015's deferral and the id half of ADR-027's; the remainder of ADR-027's is re-pointed, not silently absorbed.
**Served-path change:** **Yes, on both halves.** *Identity:* re-filing a memory that has since been edited stops silently reverting the edit, re-filing the edited text stops creating a duplicate row, and re-filing a named source stops stripping the anchors of chunks it did not change. *Lineage:* a drawer gains a validity window, `am_update_drawer`'s content edit supersedes instead of overwriting and returns the new record's id, `am_invalidate_drawer(id, reason)` appears, recall returns only current records with the superseding reason carried on the live one, and `am_delete_drawer` / `am_delete_tunnel` / `am_delete_hallway` leave the agent surface for the operator surface. The CLI gains `doctor --corpus`.

## Context

**The measurement, taken 2026-08-27 by recomputing `DrawerID(team_id, wing, room, source_file, chunk_index, content)` for every row in the live palace and comparing it to the stored primary key.** Not by reading the code and reasoning about it — by hashing 2,013 rows.

| population | rows | note |
|---|---|---|
| all drawers | 2,013 | the count when the ids were recomputed, early on 2026-08-27. **Later paragraphs say 2,024 and `am_status` said 2,029 the same afternoon** — the corpus grew during the session that wrote this record, mostly from its own filings. Stated rather than silently reconciled, because 1,705 below is a denominator. |
| diary rows, excluded | 308 | `diaryEntryID` is a **different function** — it folds agent, topic and an unstored random seed (`service.go:2100`), so a diary id is **permanently non-derivable by construction**. Including them would report 100% drift and be a false alarm. |
| non-diary, checked | 1,705 | |
| id **matches** the hash of its own row | 1,678 | |
| id **no longer describes its row** | **27** | 1.6%, across 8 wings |

Of those 27: **5** are explained by a wing move, **1** by a room move, and **21** are unattributed — an *upper bound* on in-place content edits, not a count of them, because a merge whose source wing no longer holds any drawer is undetectable by the substitution method used.

**Three shipped paths mutate the hashed tuple while keeping the id**, and two of them are deliberate and accepted:

- `Service.Update` rewrites content, wing or room and keeps the id. `repo.go:377` says so outright: *"the id is stable — it is not recomputed from the new wing/room."*
- `MergeWing` issues `Update("wing", target)` (`admin.go:306`) and keeps the id. That is ADR-015, accepted and shipped.
- `WriteDiary` mints from a seeded function that no lookup can reproduce.

So the palace already decided, in three places, that an id **is a reference, not a description**. It just never said so, and one value is still doing both jobs.

**The cost of not saying so — two failure modes, one live, one latent.**

A source-less `am_add_drawer` skips `purgeSource` (`service.go:679`) and relies on the content-hash id colliding with the stored row under `OnConflict{UpdateAll: true}` (`repo.go:85`). For a drawer that has since been edited in place:

- re-filing the **original** text mints the id the row still carries, and the edit is **silently reverted**;
- re-filing the **edited** text mints a different id, and a **duplicate row** with identical content is created.

Measured on the same corpus: **0 of the 27 drifted rows have `source_file = ''`**, so this is a mechanism with no shipped instances today — reported as a mechanism, not an incident. The live half is the other one: all 27 carry a named source, and `purgeSource` **hard-deletes** every drawer under a `(wing, room, source_file)` triple before inserting the new set (`service.go:844` — vectors, derived edges and rows), so **each of those 27 in-place edits is destroyed by the next re-file of its source**, across 19 distinct source triples.

**How likely that re-file is cannot be measured from this corpus, and an earlier draft of this record implied otherwise.** The obvious test — does a source triple carry more than one distinct `filed_at` — returns 0 for all 27, and that number is worthless: `purgeSource` deletes its predecessor, so a re-filed source leaves no trace of having been re-filed. The check cannot produce a non-zero answer for a named source, which makes it a gate that cannot fail. What is certain is the mechanism and the 27 rows exposed to it; the rate is unknown.

**A second live loss vector, found while answering "are we losing memory?" on 2026-08-27.**
`purgeSource` calls `DeleteBySource`, and `DeleteBySource` deletes the **anchors** of every drawer
under the triple first (`repo.go:225`, *"Anchors first, while the drawers that name them are still
queryable"*). Because ids are deterministic today, an unchanged chunk comes straight back with the
**same id and no anchors**. The drawer survives; its pin to the code it explains does not, and
nothing reports it.

Measured 2026-08-27 against the live palace: **65 anchors on 41 drawers, and 39 of those drawers sit
under a named source** across 39 source triples. So 95% of every anchor in the palace is destroyed by
the next re-file of its source. As with the drift rate above, **how often that happens cannot be
measured** — a re-file leaves no trace of its predecessor — so this is an exposure, not an incident
count.

## The other half, absorbed from ADR-010

Everything above is a loss the store cannot undo, and the reason is one sentence: **the temporal
model covers facts and not reasoning.** `db/migrations/00010_kg.sql` says of a knowledge-graph fact
that it *"becomes historical but is never deleted"*. `00006_drawers.sql` gives a drawer no `valid_to`,
no `superseded_by` and no revision table. That is backwards. A fact — *"service X deploys to host Y"*
— is cheap to re-derive from the running system. Why an approach was abandoned, what was tried
first, what the constraint was: that exists nowhere else, and it is the half the store lets an agent
overwrite or delete outright.

ADR-010 made the analogy concrete and it is worth keeping verbatim: **a ruling is made under the
laws valid on that day.** Delete the superseded law and the ruling stops being legible.

**And two operations are conflated under one word.** *Retraction* — an agent decides a memory is no
longer true, where the old text is evidence: *"we used Kafka until March, then replaced it because
rebalancing stalled"* is a better record than either half alone. *Erasure* — an operator decides
data must not exist: a leaked secret, a deletion request, a retention policy. `am_delete_drawer` is
exposed to **agents** and performs **erasure**, and the palace's own protocol tells agents to correct
memories that turn out wrong. Agent-facing tools that destroy: `delete_drawer`, `delete_tunnel`,
`delete_hallway` and `merge_wing` unconditionally, plus `delete_wing` whenever the server runs
self-hosted (`registerDeleteWing` is gated on `local`, `admin.go:198`). ADR-010 said the last two
were *"already outside the agent surface"*; **that was wrong, and this record inherited the error
until review caught it on 2026-08-27.**

**The third gap is the one that costs most.** Nothing records *why* something stopped applying.
`am_kg_invalidate` takes a date and no reason; the schema has `valid_to` and no column for one. A
session that finds an ended record with no reason is in the same position as one that finds nothing:
it re-derives, reaches the same idea, and re-litigates a decision the team already took.

**Why the two halves are one record and not two.** They were two, until composing them surfaced an
interaction neither could see alone — see the Decision's point 2. A unique index on the content key
is wrong unless it is scoped to CURRENT rows, and nothing in the identity half knows what "current"
means while nothing in the lineage half knows there is an index. Keeping them apart would have
shipped a store where text that was once superseded can never be filed again.

**A live instance, found in review 2026-08-27 while this record was open.** An agent executing this
repository's own wake-up protocol reported:

> ⚠ One conflict to flag: the wing root's `must.decisions.permissions` edge points at drawer
> `bf1ed1c3…` which no longer exists — a dangling `must.*` edge. Recovering that decision by search
> instead.

That is this decision's failure mode in the one place the protocol says an agent must read
**everything**: `AGENTS.md` instructs every session to fetch every `must.*` edge, warning that
skipping is silent because *"nothing reports the drawer you did not read"*. A deleted target makes
the skip involuntary. The agent behaved correctly — it noticed and fell back to search — but it
noticed by producing a human-readable warning, and **nothing in this tree makes that detection
systematic**, which is what T6's `doctor --corpus` is for.

**Reported from another palace, and NOT reproduced here — said plainly rather than absorbed as
though measured.** This palace's `must_*` edges point at room and skill LABELS (`llm_index`,
`effective-go`), not drawer ids, and `bf1ed1c3…` exists in no row here. What is measured here is the
same class:

| pointer | dangling |
|---|---|
| `holds` (derived containment, 64-hex drawer ids) | **0 of 8** |
| other object-position drawer-id pointers | **2** |
| `kg_triples.source_drawer_id` (provenance) | **16** |

Eighteen pointers into drawers that no longer exist, and nothing reports any of them.

**And it is the argument for ending rather than deleting, made by a real incident.** Under this
decision that drawer would have been ENDED: the row still resolves, the edge still leads somewhere,
and the traversal finds *"this was retracted on D, because R"* instead of finding nothing. A deleted
target loses the decision AND the fact that a decision was taken.

**The same defect exists one table over, and it is reproduced.** Correcting a FACT has no atomic
verb: `kg.go:567`'s frozen no-auto-supersede rule says *"to replace a fact, invalidate it first"*, so
an agent hand-rolls invalidate + add. There is **no transaction anywhere in `kg.go`** — grepped, zero
`Transaction(` or `Begin()`. Upstream (`mempalace`, MIT) has `kg_supersede` and its protocol says why:
*"do NOT hand-roll invalidate + add, which leaves the old and new values overlapping at the
boundary."* agentsmemory has zero hits for any such verb.

Reproduced 2026-08-27 with the agent doing everything RIGHT — invalidating first, then adding, in the
order the frozen rule demands:

```
KGAdd("svc","deploys to","old-host", validFrom:"2026-01-01")
KGInvalidate("svc","deploys to","old-host", ended:"2026-08-24")
KGAdd("svc","deploys to","new-host", validFrom:"2026-08-24")
KGQuery(entity:"svc", as_of:"2026-08-24")  →  [old-host new-host]
```

Two contradictory answers to *"where does svc deploy on the 24th"*, on every changeover day.
`temporalEndKey` (`kg.go:117`) stretches a **date-only** `valid_to` to `T23:59:59Z`, and `inEffectAt`
(`kg.go:962`) excludes only below `as_of` — so the ended fact stays in effect for the whole changeover
day while its replacement is already in effect. Filed as issue #74; **insourced here on M's call
2026-08-27, because supersession in the graph and supersession in the drawer table are the same
decision applied to two tables, and this record has already learned to audit the class rather than
the instance.**

**Three records have already deferred to the primitive this ADR adds.** This is the part that makes it a decision rather than a cleanup:

- **ADR-015** — *"Making `DrawerID` independent of the wing so a merge does not invalidate anything derived from the id"* (Out of Scope, deferred; receipted in `docs/adr/BACKLOG.md` under the heading *"From ADR-015 (a wing merge must correct the search index it invalidates)"* — cited by heading rather than by line, because every insertion above a line number invalidates it, and this citation had already drifted from `:665` to `:778` before anyone read it).
- **ADR-027** — *"Make `Update` re-chunk … it changes which ids exist, and the open question — what happens to a reference pointing at a **non-parent** chunk — is unanswered."*
- **ADR-010** — rejects event sourcing partly because *"the store already has a working row model with vectors, chunking and anchors hanging off drawer identity."*
- **ADR-019** — rejected smaller chunks because *"it changes ids, invalidating every anchor, tunnel and knowledge-graph pointer."*

Four records, one cause. None of them can move while the id that references a row is also the id that describes its bytes.

## Existing Primitives Audit

| Primitive | Where | Disposition |
|---|---|---|
| `DrawerID` | `chunk.go:148` | **Reshape.** Its recipe is kept verbatim and becomes the **content key**. It stops being the primary key's definition. |
| `diaryEntryID` | `chunk.go:164` | **Reuse unchanged.** It is already an opaque mint; this ADR names that role rather than inventing it. Diary rows carry **no** content key — a journal must not dedupe. |
| `purgeSource` | `service.go:679` | **Reuse unchanged.** Named-source wholesale replacement is orthogonal and correct. |
| `OnConflict{UpdateAll: true}` | `repo.go:85` | **Reshape.** The conflict target moves from the primary key to the content key. |
| `RelabelDrawerWing*` | `admin.go:295,313` | **Extend.** Must recompute the content key in the same statement that moves the wing. |
| `pointID` (UUID5 of drawer id) | `store/qdrant/vector.go:29` | **Untouched.** No drawer id changes, so no vector is re-keyed. This is the reason for the shape chosen below. |
| `randomID` | `recallstats.go:179` | **Reuse.** Already the house opaque-id mint, used for `search_events`. |
| **`Service.CheckDuplicate` / `am_check_duplicate`** | `service.go:1854` | **Not reusable, and named here because it is the thing a reader will point at.** It is a SEMANTIC duplicate check — it embeds the content, runs a vector search for the single nearest hit and compares cosine similarity to a threshold. It is approximate, costs a model call, needs a live embedder, and answers "is something like this already filed" for a HUMAN to judge. A dedup key must be exact, free, and correct while the embedder is down (`SaveUnembedded` exists for exactly that state). Different question, different guarantees; it stays as it is. |

Inherited from ADR-010, for the lineage half:

| Primitive | Where | Disposition |
|---|---|---|
| KG validity windows (`valid_from`/`valid_to`, `KGInvalidate`) | `db/migrations/00010_kg.sql`, `kg.go` | **Reuse the semantics verbatim** rather than invent a second vocabulary: a record is current while `valid_to` is empty, and ending never deletes. |
| `Drawer.ParentID` | `palace.go` | **Reuse, do not overload.** It expresses "these rows are one memory". A supersession chain is a different axis and gets `superseded_by`; conflating them makes chunking and versioning two competing notions of identity. |
| `content_date` | `drawers` | **Reuse, keep distinct.** It is the date a memory is ABOUT. Conflating "when this was true" with "when we believed it" is how temporal stores become unreadable. |
| `DeleteWing` / `MergeWing` | `admin.go` | **Reuse as precedent.** Already operator-facing — the model for where erasure belongs. |

**The class audit, because fixing an instance is not fixing a shape.** Four other identifiers in
`internal/palace` are minted by hashing their own fields, and each was checked for the same defect —
a path that mutates a hashed field in place while keeping the id:

| Identifier | Hashes | Exposed? |
|---|---|---|
| `canonicalTunnelID` (`tunnel.go:26`, hashing at `:33`) | its two endpoints | **No** — a tunnel is created or deleted, never re-pointed. |
| `hallwayID` (`hallway.go:34`) | wing + the two entities | **No** — derived state, rebuilt wholesale by `RecomputeGraph`. |
| `closetID` (`closet.go:174`) | team, wing, room, source, ordinal — **no content** | **No** — location-only, so a closet's document can change without moving its id. |
| `anchorID` (`anchors.go:82`) | team, drawer id, path, normalized snippet | **No** — `ReplaceAnchors` swaps the set rather than patching a row. The in-place `Update`s on that table touch `last_checked`-style fields only. |

`drawers` is the only member of the class with an in-place mutation of a hashed field, which is why
this ADR is about one table and not five.

## Decision

**A drawer gets an identity that does not move and a history that does not vanish.** Two halves, one
record, because the index in the first is wrong without the second.

**1. A drawer gains a validity window (from ADR-010).** `valid_to`, `superseded_by`, `ended_reason`,
`ended_at`. A drawer is CURRENT while `valid_to` is empty, exactly as a knowledge-graph fact already
is. Additive: every existing row reads as current with no backfill. **This lands first**, so the
index in point 2 is created with its correct predicate rather than created narrow and widened a task
later.

**2. `drawers.id` becomes opaque by contract, and `drawers.content_key` carries the hash that dedup
matches on** — with a unique index on `(team_id, content_key)` **whose predicate is
`WHERE content_key != '' AND valid_to = ''`**.

   **That predicate is the load-bearing clause of this record, and it is the interaction that made
   the two ADRs one.** Two failures live in it, and only one was visible before the merge:

   - Without `content_key != ''`, every keyless row shares one index entry, and once point 4 points
     the upsert at that index, filing any keyless drawer would **overwrite an unrelated memory**. The
     only failure here that destroys rather than duplicates, and the only silent one.
   - Without `valid_to = ''`, a superseded row keeps competing for uniqueness on content it no longer
     asserts — so **text that was once superseded could never be filed again**. Neither half of this
     decision can see that alone: the identity half does not know what "current" means, and the
     lineage half does not know there is an index.

   It gets its own test, and the mutant is deleting each conjunct in turn.

**3. Every mint path (`Add`, `AbsorbDrawers`, `Mine`, `CopyWing`) writes the content key beside the
id; every in-place mutation path (`Update`, `MergeWing`) recomputes it** in the same statement that
changes a hashed field. Diary rows get an EMPTY key and stay outside the index — a journal must never
dedupe.

**4. Dedup and idempotency move to the content key.** `Add` and the import path upsert on
`(team_id, content_key)` and mint a fresh opaque id when there is no match. Import's contract at
`import.go:21` — *"the only field recomputed is the id … so re-running an import upserts rather than
duplicates"* — is preserved, now by the key rather than by the id.

**5. `id` is never recomputed, never compared to a hash, and never used to infer anything about a
row's content.** A source check (T6) fails when `DrawerID` is called anywhere other than a
content-key computation.

**6. Re-filing a named source becomes a set difference on the content key — and rows that left the
source are ENDED, not deleted.**

   `purgeSource` currently deletes every row under a `(wing, room, source_file)` triple, with its
   vectors, derived edges and anchors, and `Add` then re-inserts. Under this decision it upserts the
   new set by content key and **ends** the rows under that triple whose key is not in it.

   **Without the set difference this record is a regression.** Ids are deterministic today, so a
   re-file of unchanged content re-inserts the same ids and every reference survives. Mint an opaque
   id and that stops being true: the purge deletes the row, the upsert finds no key to match, and a
   fresh id is minted — so **every re-file of a named source would re-key every drawer under it**,
   breaking exactly the resolvability this decision exists to protect. The set difference removes the
   regression and repairs the pre-existing anchor loss in the same change, because a row that is
   never deleted never loses its anchors.

   **And ending rather than deleting is what point 1 buys.** Under ADR-010's rule an agent-initiated
   removal is a retraction, not an erasure. A chunk that a re-file dropped is a memory the team stopped
   asserting — it keeps its text, leaves recall, and is recoverable.

**7. Retraction is agent-facing and carries a reason; erasure is operator-facing (from ADR-010).**

   - `am_invalidate_drawer(id, reason)` — this memory no longer applies, and here is why. Nothing
     replaces it.
   - `am_update_drawer`'s content edit **supersedes**: it writes a NEW record, ends the old one, and
     returns the new id while naming the one it replaced.
   - `reason` is **required** on both, and on `am_kg_invalidate`, which today records a date and no
     why. A required free-text field is a weak guarantee — an agent can write "obsolete" — but it is
     the difference between a field nobody fills and a field somebody can be asked about, and it
     costs one argument.
   - `am_delete_drawer`, `am_delete_tunnel` and `am_delete_hallway` leave the agent surface. Genuine
     removal stays possible for an operator, because a store that cannot forget a leaked secret is
     not deployable; it stops being something a confused agent reaches for while trying to help.

   **Three questions the tasks had punted, decided 2026-08-27 rather than left to execution:**

   - **A correction CARRIES its anchors to the successor, with `status` reset to `unchecked`.**
     Not cleared, and not trusted. Verification is client-side (`list_anchors` hands them out, the
     client checks its working tree, `mark_anchors` takes verdicts back — the server cannot read a
     repo), so "re-verify now" is not available at supersede time. `drawer_anchors.status` already
     has `unchecked` meaning *"never verified"*, and `anchorID` already folds in the drawer id, so a
     carried anchor mints a new id on the successor for free. The next client-side sweep re-verifies
     it. Anchors are scarce — 41 of 2,024 drawers carry one, measured 2026-08-27 — so clearing them
     on every correction would spend a resource the palace barely has. **Named cost:** `Stale()` is
     true only for `drifted`/`missing`, so a carried anchor does not cry wolf before anyone looks at
     it, and there is a window where it reads as fine and has not been checked. That is the right
     trade — a false stale marker is worse than an unchecked one — but it is a window, not nothing.
   - **A knowledge-graph fact KEEPS its `source_drawer_id` when that drawer is ended.** Provenance is
     historical: the fact *was* extracted from that text. Re-pointing it at the successor would
     assert that the corrected text still supports the fact, which a correction may have removed —
     the store would be fabricating provenance. `am_kg_query` already returns `source_drawer_id`
     (ADR-026 T6), so a reader can see the pointer resolves to an ended record, and T6's
     `doctor --corpus` reports the ratio.
   - **The three destructive tools are REMOVED from the agent surface, with no deprecation window.**
     An agent doing a retraction currently gets an erasure; leaving the verb live for one more
     release leaves the defect live for one more release. The refusal text names the operator path.

   **Point 7 is what makes an opaque id necessary rather than merely tidy.** A supersede mints a
   second row for the same memory. Under a content-addressed id that is an identity problem — two ids
   derived from two contents, and nothing says which is the memory. Under an opaque id it is an
   ordinary edge: the new row has its own name and `superseded_by` carries the lineage.

**8. Recall returns current records, and the reason rides along.** Superseded TEXT never competes
with its correction. But the CURRENT record names what it replaced and why, truncated to 200
characters in a recall response, with the full text behind an explicit history flag.

   ADR-010 corrected its own first draft here and the correction is kept: hiding history behind a
   flag AND expecting retractions to prevent re-litigation cannot both be true, because *a session
   about to redo a rejected thing does not know to ask for history* — not knowing is the whole
   problem. So the reason reaches the default path while the stale text does not.

**9. Correcting a FACT gets the same treatment as correcting a memory: `am_kg_supersede(subject,
predicate, old, new, reason)`, atomic.** One call, one transaction, ending the old and adding the new
so neither a gap nor an overlap can be observed between them, and carrying the same required `reason`
point 7 puts on every other retraction.

   **It writes an instant, never a date, which collapses the overlap from a day to the boundary
   instant — and does NOT remove it.** `temporalEndKey` stretches a date-only `valid_to` to
   `T23:59:59Z`; a supersede stamping both `old.valid_to` and `new.valid_from` with the same RFC3339
   **datetime** is never date-only, so no stretch happens. **86,400 seconds becomes 1.** The 15
   already-ended facts keep the meaning they were written with, and nothing is migrated.

   ⚠ **The boundary instant itself still holds both values, and an earlier draft of this record
   claimed otherwise — twice.** Found by review 2026-08-27 and reproduced against the real function:

   ```
   as_of = 2026-08-24T10:00:00Z   old in effect = true    new in effect = true
   as_of = 2026-08-24T10:00:01Z   old in effect = false   new in effect = true
   ```

   `inEffectAt` is inclusive on BOTH ends — it excludes only on `>` and `<`, never `>=`/`<=` — so with
   a shared endpoint neither comparison fires. **This is a different axis from the date-only stretch:
   the interval is CLOSED `[valid_from, valid_to]` where a validity window wants half-open
   `[valid_from, valid_to)`.** Removing the stretch narrows a closed-interval overlap; it cannot
   remove one, because the shared endpoint IS the mechanism. Reachable: an agent that supersedes and
   then queries `as_of` the timestamp the supersede response just handed it lands exactly on it.

   The one-character fix (`<` → `<=`) *is* the half-open semantics, and it re-reads every ended fact
   by one boundary unit including those 15. That is the same interval-semantics question issue #74
   already defers from the other direction — #74 asks what a date-only `valid_to` MEANS, this asks
   whether `valid_to` is inclusive at all — so both go to #74 and one record answers them together.

   **What this does NOT decide:** whether a date-only `valid_to` should mean *through* that day
   (today's inclusive reading) or *as of* it. Both are defensible, `status:"current"` and `as_of`
   disagree for exactly one day, nothing documents that they differ, and no test pins either. The
   atomic verb sidesteps the question rather than answering it — deliberately, because answering it
   silently would be worse than the bug.

**Existing ids do not change.** No row is re-keyed, so no `code_anchor`, tunnel,
`kg_triples.source_drawer_id`, `parent_id`, `search_events` row or Qdrant point is re-pointed, and
nothing needs a transaction spanning SQLite and Qdrant. Both migrations are additive and the rollback
is dropped columns. That is the whole reason for this shape over minting new opaque ids, and it is
what makes the decision cheap enough to be reversible.

### Why keeping everything is affordable, which is the load-bearing claim (from ADR-010)

Keeping everything costs disk, and the cost that matters is **retrieval**: a larger, more
heterogeneous corpus retrieves worse, because unrelated records do not remove the answer — they add
competitors ahead of it. That is why deletion exists at all.

**The evidence, named, because a load-bearing claim should not rest on a pointer to itself.** Two
results are on record and they are not equally strong. MRR 0.83 on a focused corpus against 0.34–0.39
on a large mixed one is **confounded** — different corpora AND different question sets — and should
not be quoted alone. The one that carries the claim is **the closet prior: one source lifting fifty
unrelated drawers cost 0.10 MRR.** Same corpus, controlled intervention, measuring the mechanism
directly.

One boundary, from ADR-010's own 2026-08-21 survey: the IR literature does *not* establish that
widening retrieval scope degrades RANKING in general — selective search shows a well-chosen subset
matches exhaustive search at far lower cost. Degradation IS established at the ANSWER level. So the
claim holds for this system on this system's measurement and must not be restated as a general
property of vector search.

Give a record a validity window and the trade disappears: an ended record leaves the default recall
path without leaving the store. **Deletion is not the price of good retrieval once ending is
possible** — and `am_delete_drawer` is revealed as a workaround for the absence of a validity window.

**Storage and payload are separate budgets, and this record only spends the first.** A default recall
returns `DefaultSearchLimit` = 5 hits at `DefaultSnippetChars` = 400 characters — roughly 2,000
characters, on the order of 500 tokens, whether the palace holds 200 drawers or 200,000. Ending
records changes the candidate POOL, and the pool was already bounded before it reached anybody's
context. The one payload cost this adds is the reason on the live record, which is why it is capped
at 200 characters: five hits × 120 characters is a 30% increase on a 2,000-character page, and that
is real. A retraction whose reasoning needs more than 200 characters is a memory in its own right.

### Pre-registered falsification (inherited from ADR-010, unchanged)

> **Accumulation must not degrade recall of current records.** Measured on a corpus where superseded
> records outnumber current ones by at least 2:1, MRR over current-only cases must be within noise of
> the same case set measured before the ended records existed.

Noise here is this repo's measured floor: two arms with provably identical configuration scored 0.709
against 0.700 MRR on 2026-08-26, so a difference under ~0.01 MRR is not a result.

**The instrument for this already exists, and this record is what feeds it.** ADR-004 is Accepted with
all five tasks done, and `internal/palace/evalstats.go` carries `StaleAboveRate` — it counts, per arm,
how often a superseded memory outranked the current one (`StaleAbove`), how often it merely reached
the page (`StaleInPage`), and how often the superseded version never entered the pool at all
(`Vacuous`). That is exactly the falsification above, already built and already wired.

**It is starved, not missing.** `supersessionMinCases = 30` (`evalstats.go:417`) is a floor on
verified, non-vacuous pairs, and the gate refuses to answer below it rather than answering on noise.
Measured 2026-08-27 against the live palace: **5 `supersedes` triples workspace-wide, and 0
`retracts` and 0 `qualifies`.** Six times short of its own floor, with supersession expressible today
only as a hand-authored knowledge-graph edge.

T4 is what changes that. Once correcting a memory writes `superseded_by` on the drawer, **every
correction produces a pair**, and ADR-004's instrument gets its input from ordinary use instead of
from somebody remembering to file a triple. Naming it here because this record must not build a
second measurement beside a working one — and because "the corpus where superseded records outnumber
current ones 2:1" is not a hypothetical corpus to construct, it is what this palace becomes after T4
has been live for a while.

If it fails, the exclusion is not working and ended records are competing — a defect in the
implementation, not a reason to start deleting. The remedy is the filter; the second remedy, only if
the first is impossible, is a separate index for ended records.

**What ADR-010 struck, and why it stays struck:** its first draft proposed retracting the history
chain if `include_history` were rarely called. Read frequency is a bad proxy for the value of an
archive — a decision record's payoff is rare and large, and measuring it by call count is cancelling
insurance because no claim was filed. Likewise the `reason` field's quality is measured (median
length, a human reading a sample) to improve the **prompting**, never to retract the field.

**What would make the identity half FAIL, and does data that could produce it exist?** The backfill's
unique index is the falsifiable part: two CURRENT rows sharing a
`(team_id, wing, room, source_file, chunk_index, content)` tuple would collide and the migration
would abort. Measured 2026-08-27 on the live corpus: **1,705 non-diary rows produce 1,705 distinct
keys, 0 collisions.** Valid for this corpus at this date; the migration must fail loudly rather than
skip a colliding row, so a corpus that does collide is a stop condition and not a silent partial
backfill.

**Re-chunking on update is NOT part of this decision.** This record removes the blocker that four
records named; it does not spend it. ADR-027's remaining question — what happens to a reference
pointing at a non-parent chunk that a re-chunk deletes — is still open and is re-pointed, not
absorbed.

## Alternatives Considered

- **Mint new opaque ids and re-point every reference.** The clean version. Rejected on cost and risk: `pointID` is UUID5 of the drawer id, so every vector in Qdrant would be re-upserted and the old points deleted, with no transaction spanning SQLite and Qdrant — a half-done migration would leave rows whose vectors are unreachable, which is precisely the invisible state ADR-015 was written to end. The additive column buys the same property for a dropped column's worth of rollback.
- **Keep one id and make `Update` re-chunk (issue #39 part 2, ADR-027's rejected alternative).** Rejected again, and for the reason ADR-027 gave: re-chunking changes which ids exist, and those ids are what anchors, tunnels and KG facts point at. This is the option M's argument reaches for; it trades away the property that is still alive to compensate for one that is already gone.
- **Drop content-addressing entirely — random ids, no dedup.** Rejected: it is load-bearing in two places, not one. `Add` uses it for source-less idempotency, and `import.go:21` states the migration path's re-run safety rests on it. Removing it makes a re-run of an import duplicate a palace.
- **Do nothing; accept that the id no longer describes the row.** Rejected because the drift is unstatable today: with no column holding what the id used to promise, there is nothing a gate can compare, and the 27 rows were found by an ad-hoc script rather than by anything in the tree. A property nothing can check is not a property.
- **Store a `content_sha256` for reporting only, without moving dedup onto it.** Rejected: it would record the drift and fix neither failure mode. The silent revert survives, and the column becomes a number nobody acts on.

Inherited from ADR-010, for the lineage half:

- **Leave it; agents can file a new drawer and delete the old.** Rejected: that is today's behaviour, and it destroys the rejected alternative — the one thing irrecoverable at any price, because a rejected alternative leaves no trace in the artifact.
- **Soft-delete with a `deleted_at` tombstone.** Rejected as insufficient rather than wrong: it records THAT a record died and not what replaced it. *"Kafka until March, then NATS, because rebalancing"* needs the link, and a tombstone has nowhere to put it.
- **Full event sourcing — an append-only log as the source of truth, state as a projection.** Rejected, and ADR-010's stated reason was *"the store already has a working row model with vectors, chunking and anchors hanging off drawer identity"*. **That objection is partly dissolved by this record's own first half** — an opaque id is exactly what unhooks identity from content — so the honest restatement is narrower: a validity window IS the append-only property for the one thing that needs it, and a projection rewrite is not justified while there is one consumer of the history. Revisit when there is a second (deferred: `docs/adr/BACKLOG.md`).
- **Only supersede; no standalone invalidate verb.** Rejected: plenty of retractions replace nothing. *"We are not doing this after all"* has no successor record, and forcing one would make an agent invent a placeholder memory to express an absence.
- **Version everything, keep every revision.** Rejected: a typo fix would create a revision and the history that matters — a decision changing — would be buried in noise. Supersession is a deliberate act; a spelling correction is not.
- **Keep the two halves as two records (ADR-010 and ADR-038 separately).** Rejected on 2026-08-27, and the reason is the merge's whole justification: the unique index's `valid_to = ''` conjunct is invisible from either record alone. Shipped separately, they produce a store where text that was once superseded can never be filed again, and no gate in either record would have caught it.

## Component / Boundary Impact

`internal/palace` keeps ownership of drawer identity and gains two things: an explicit second key, and a validity window. No component moves. `internal/store` is untouched by design — the vector namespace still keys on the same drawer ids it keys on today, and no vector is re-upserted by either migration. `internal/mcpserver` changes on the lineage half only: one tool is added (`am_invalidate_drawer`), three leave the agent surface for the operator one, and `am_update_drawer` / `am_kg_invalidate` change signature. The index gains a rule — ended records leave the default search — which is a change to what `Search` retrieves, not to how it ranks.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `drawers.content_key` (schema) | new `TEXT` column + unique index `(team_id, content_key)` | `db/migrations/*_drawers_content_key.sql` | `Add`, `AbsorbDrawers`, `Mine`, `CopyWing`, `Update`, `MergeWing` |
| `Repo.Save` conflict target | `id` → `(team_id, content_key)`, **with the partial index's predicate repeated as `TargetWhere`** | `internal/palace/repo.go` | `Service.Add` (embedded path only) |
| `Repo.SaveUnembedded` conflict target | `(team_id, id)` → `(team_id, content_key)`, same `TargetWhere` | `internal/palace/repo.go` | `Service.AbsorbDrawers` (**every** import — `import.go:99` never calls `Save`), and `Service.Add` whenever the embedder is down |
| `DrawerID` role | primary-key recipe → content-key recipe (function body unchanged) | `internal/palace/chunk.go` | every mint path |
| Drawer id minting | content hash → `randomID`-style opaque mint for NEW rows | `internal/palace/chunk.go` | every mint path |
| `doctor --corpus` (new CLI flag + `--help` text) | new integrity check beside `--index`, `--schema`, `--roles`; exits non-zero on a finding | `cmd/server/doctor.go` | operators |
| `drawers.valid_to`, `superseded_by`, `ended_reason`, `ended_at` | add (migration) | `db/migrations/*_drawers_validity_window.sql` | `internal/palace`, and the content-key index predicate |
| `am_update_drawer` content edit | change — supersedes instead of overwriting; returns the NEW id and names the ended one | `internal/mcpserver/drawers.go` | every agent that corrects a memory |
| `am_invalidate_drawer(id, reason)` | add | `internal/mcpserver/drawers.go` | any agent retracting a memory |
| `am_kg_invalidate` `reason` | add — required, mirroring the drawer verb | `internal/mcpserver/kg.go` | anyone reading why a fact ended |
| `am_kg_supersede(subject, predicate, old, new, reason)` | **add** — one atomic call replacing hand-rolled invalidate+add; stamps a datetime boundary, collapsing the overlap from a day to the boundary instant (see Decision 9 — the boundary instant itself is #74's) | `internal/mcpserver/kg.go`, `internal/palace/kg.go` | every agent correcting a fact |
| `am_delete_wing` agent registration | change — removed from the agent surface (`admin.go:198` gates it on `local` today, which is not a boundary) | `internal/mcpserver/admin.go` | agents (removed), operators (retained via CLI) |
| recall response: `supersedes` + reason, truncated to 200 chars | add | `internal/mcpserver/drawers.go` | every recall — bounded so accumulation never grows the payload |
| `am_search` / `am_list_drawers` | change — current records only, with an explicit history flag | `internal/mcpserver` | every recall |
| `am_delete_drawer`, `am_delete_tunnel`, `am_delete_hallway` | change — leave the agent surface for the operator one | `internal/mcpserver` | agents (removed), operators (retained) |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `drawers.valid_to`, `superseded_by`, `ended_reason`, `ended_at` (T1) | T1 | T2, T3, T4, T5, T6 | No — additive; empty `valid_to` is every existing row, which is correct |
| `drawers.content_key` + `Drawer.ContentKey` + the two-conjunct unique index (T2) | T2 | T3, T6 | No — additive column, empty for diary rows |
| `Repo.Save` upserting on `(team_id, content_key)`, opaque mint, set-difference `purgeSource` (T3) | T3 | T4, T6 | Yes — a re-file no longer deletes, and a new row's id is no longer derivable |
| supersede semantics on `am_update_drawer`, `am_invalidate_drawer(id, reason)`, `am_kg_supersede(...)` (T4) | T4 | T5, T6 | **Yes** — `am_update_drawer` returns a different id than the one it was given, and three tools leave the agent surface |
| current-only recall + history flag + the carried reason (T5) | T5 | T6 | Yes — search stops returning ended records |

## Implementation

See `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite/tasks/README.md`. **Six tasks, and
the DAG is a straight chain** — the wave table is six single-task waves, which is what a topological
leveling of this dependency graph honestly is. No parallelism has been manufactured to make the table
look like a table.

The ordering is forced, and one step of it is the merge's own doing: **the validity window (T1) comes
before the content key (T2)** so the unique index is created with its `valid_to = ''` conjunct from
the start, rather than created narrow and widened one task later. That window — where the schema is
briefly wrong — is the kind of state this repo keeps finding, and it is removable by ordering alone.

## Consequences

- **Positive — identity:** re-filing a named source stops destroying the anchors of chunks it did not change (39 of the palace's 41 anchored drawers are exposed to that today), and both `am_add_drawer` failure modes stop being possible. The drift becomes checkable: 27 rows were found by an ad-hoc script, and after T6 `doctor --corpus` finds them.
- **Positive — lineage:** the record that explains why a decision changed survives the change. *"We used X until March, then Y, because Z"* becomes expressible, and it is the shape a reader actually needs. Every loss this record measured stops being permanent — a chunk dropped by a re-file, a memory corrected, a retracted claim: all end rather than vanish.
- **Positive — the corpus:** four deferred records (ADR-015, ADR-019, ADR-027, and ADR-010 itself, now absorbed) lose the blocker they each named.
- **Negative — the store grows monotonically**, and a correction costs a row rather than reusing one. Ended rows keep their vectors, so the index grows too. Accepted as the intended trade rather than tolerated as a cost: the growth is what is being bought, and it is affordable only because ended records leave the default recall path — which the pre-registered falsification above is what checks.
- **Negative — T3 is larger than it looks:** it cannot ship the opaque mint without also converting `purgeSource`, because the two together are what keep a re-file from re-keying its source. Splitting them across commits leaves the tree in the regressed state.
- **Negative — two keys where there was one**, and every mint path must write both. That is the classic shape of a field forgotten on the fifth path; T6's gate derives its universe from the source for exactly that reason.
- **Neutral:** `am_update_drawer` returns a NEW id after T4. Any caller assuming the id is stable across a content edit must be updated — inside this repository the only such caller is the tool itself.
- **Neutral:** new rows get opaque ids while existing rows keep hash-shaped ones. Heterogeneous on purpose: an id indistinguishable from a hash invites the next reader to re-derive it.

## Out of Scope

- Re-chunking on update (deferred: `docs/adr/BACKLOG.md`)
- Full event sourcing of the whole store (deferred: `docs/adr/BACKLOG.md`)
- Retention or automatic pruning of ended records (permanent: accumulation is the value this record is built on, and a pruner would spend engineering effort undoing it. Erasure stays available to an operator for a leaked credential or a deletion request — a legal and safety path, not housekeeping)
- Structured reasons — a taxonomy of why something ended (deferred: `docs/adr/BACKLOG.md`)
- Giving TUNNELS a validity window — **blocked on this record's own primitive, not merely out of scope.** `tunnels`' primary key is `(team_id, id)` (`00009_graph.sql:28`) where `id = canonicalTunnelID(endpoints)` (`tunnel.go:80`), and `UpsertExplicitTunnel` (`graph.go:181`) conflicts on exactly that key with `DoUpdates: {label, updated_at}`. End a tunnel, then let anyone re-create the same A↔B link: the identical id is minted, the upsert matches the ENDED row, and it updates the corpse's label instead of inserting a live tunnel — the link becomes permanently un-recreatable. That is the `valid_to = ''` interaction this record found when composing ADR-010, one table over and with no content key to fix it, so a tunnel cannot take a validity window until it takes an opaque id first. The class audit called `canonicalTunnelID` "not exposed" *because a tunnel is created or deleted, never re-pointed* — giving it a window is precisely what would expose it. 18 rows exist (deferred: `docs/adr/BACKLOG.md`)
- Deciding whether a date-only `valid_to` means *through* that day or *as of* it, and reconciling `status:"current"` with `as_of` (deferred: `docs/adr/BACKLOG.md` — issue #74. The atomic verb in point 9 sidesteps it by writing instants; answering it changes what 15 already-ended facts mean and is its own decision)
- Applying the validity window to diary entries (deferred: `docs/adr/BACKLOG.md`)
- Removing `delete_wing` from the OPERATOR surface (permanent: it is the erasure path this record requires to exist. **T4 removes it from the AGENT registration**, where it is reachable today whenever the server runs self-hosted — `registerDeleteWing` is gated on `local`, and "the operator is running it locally" is not a boundary, it is the case where agent and operator share a process)
- Taking `merge_wing` off the agent surface (deferred: `docs/adr/BACKLOG.md` — **it is not erasure**, it is a move, and ADR-015 governs what a move invalidates. `registerMergeWing` is unconditional today, so an agent reaches it everywhere; that is a real hazard and it is a different decision from this one. Found by review 2026-08-27, and the parenthetical it corrects previously claimed a property no task delivered)
- Changing `ChunkSize`, `ChunkOverlap` or `MaxEmbedRunes` (permanent: this record changes what an id means and how long a memory is current, never how text is split)
- Re-keying existing drawers to opaque ids (permanent: rejected in Alternatives — the Qdrant re-upsert has no cross-store transaction, and the additive column delivers the same property)
- Giving diary entries a content key (permanent: a journal must not dedupe — `chunk.go:157` already states why, and this record names it rather than changes it)
- Versioning wing/room MOVES (permanent: a move is not a claim about the world, so ending and re-filing would record noise as history. Re-checked on absorption because point 6 makes a re-FILE produce endings — a re-file changes what a source asserts, a move does not, and the distinction survives)
- Repairing the 27 drifted rows (deferred: `docs/adr/BACKLOG.md`)
- Whether re-filing a named source should discard an in-place edit to it at all (deferred: `docs/adr/BACKLOG.md`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Backfill hits a `(team_id, content_key)` collision and aborts mid-migration | Low | High | Measured 0 collisions across 1,705 non-diary rows on 2026-08-27. T2's migration aborts loudly rather than skipping — a silent partial backfill is the failure this repo keeps catching. Rollback is the down migration. |
| A future mint path writes an id and forgets the content key | Med | High | T6's gate derives its universe from the mint sites rather than a hand-kept list, so a path added tomorrow joins the check on the same commit. |
| Someone re-derives an id for a lookup after this lands, reintroducing the coupling | Med | Med | T6's source check fails when `DrawerID` is called outside a content-key computation. Prove it by adding such a call and watching it go red. |
| **`MergeWing` starts FAILING where it silently succeeded** | Low | Med | Today a merge relabels the wing and keeps the id, so an identical memory in source and target survives as two rows. Recomputing the key makes the second one collide and the unique index rejects the UPDATE — and ADR-015 already fails the whole merge on a failure. That converts a silent duplicate into a loud refusal, which is the better direction, but it IS a new failure mode on a shipped operation. Measured 2026-08-27: **0 tuples of `(team, room, source_file, chunk_index, content)` appear in more than one wing**, so no merge on today's corpus would hit it. T2 must give it a named error saying which drawer collided, not a bare constraint violation. |
| A required `reason` gets filled with "obsolete" and buys nothing | High | Med | Inherited from ADR-010 and accepted rather than designed around: T4 records reason length and the falsification reads it. A taxonomy imposed now would be a guess about reasons nobody has written yet. |
| Ended records leak back into default recall | Med | High | T5's falsification: an ended record must be unreachable by every default route — search, list and get — checked end to end rather than by unit test, since this exact failure shipped once already as a live chunk 1 with its own embedding. |
| Agents cannot erase a wrongly-filed secret and file it anyway | Med | High | The operator erasure path lands in the same task as the removal, and the refusal text names it. |
| The store grows without bound | High | Low | Accepted as the point, not tolerated as a cost. The falsification measures whether growth harms recall of CURRENT records; if it does, the filter is broken and the remedy is the filter. |
| **Six tasks is a long chain, and the middle of it is the risky part** | Med | Med | T3 and T4 both change what a write means, back to back, with no parallel path to fall back on. Nothing mitigates this except doing them in order and stopping at each — which is what the wave table encodes. Named rather than hidden, because a six-wave sequential ADR is a bigger commitment than a three-task one and the record should say so. |
| **This ships and reads as "memory loss is fixed"** | Med | Med | **Downgraded from High on 2026-08-27, when ADR-010 was absorbed** — with the validity window inside this record it now removes causes AND makes loss recoverable, so the claim is closer to true. It is still not the whole truth: nothing here recovers the losses ALREADY taken, and `doctor --corpus` will keep reporting them. Mitigation stays prose, because there is no exit code for how a document is read. |
| **Every number in this record is from the local palace; the hosted corpus is unmeasured** | Med | Med | The 0-collision backfill premise, the 2,013 rows and the 65 anchors are one deployment. The same migration runs on the SaaS, where row counts and collision odds are unknown. Take the same three measurements against hosted BEFORE merging the migration — the queries are in T2 and they are read-only. |
| **The backfill is an O(n) SHA-256 pass at startup, in Go rather than SQL** | Med | Low | SQLite cannot compute SHA-256, so every row must be read, hashed and written back on the boot that applies the migration. On this corpus that is 2,013 rows and unnoticeable; on a large hosted database it is a full-table read plus write while the server is coming up. Measure the hosted row count first, and if it is large, run the backfill as a bounded background repair rather than inline at boot. |
| The migration number is renumbered at merge and re-runs on a database that applied it | Low | High | Allocate the number at merge, never at authoring — the crash loop and its repair are already documented in `README.md` (Development). |
| Diary rows are accidentally pulled into the unique index by a later change | Low | Med | T2's test asserts two diary entries with identical text, agent and topic coexist. |
| **An opaque mint ships before `purgeSource` becomes a set difference** | Med | **High** | Every re-file of a named source would re-key every drawer under it, breaking every anchor, tunnel and KG pointer to them — the exact property this ADR protects, broken by this ADR. They are one task and one commit for that reason, and T3's first test is the one that fails if they are separated. |
| **The unique index ships without one of its two predicate conjuncts** | Low | **Data loss / permanent refusal** | Drop `content_key != ''` and every keyless row shares one index entry, so an upsert overwrites an unrelated memory — the only silent destroying failure here. Drop `valid_to = ''` and text that was once superseded can never be filed again — the interaction only visible once ADR-010 was absorbed. T2 tests the predicate directly, and the mutant is deleting each conjunct in turn. |
| `SaveUnembedded` keeps its own `(team_id, id)` conflict target (`repo.go:110`) after `Save` moves | Med | Med | The deferred-embedding path would keep id-based dedup, so the silent-revert mechanism survives on the one path taken when the embedder is down. Named in T3's Tests table for that reason. |
| Backfill aborts partway, leaving rows with an empty key | Med | Low | Fails toward DUPLICATES, not loss: a keyless row sits outside the partial index and never matches, so a re-file inserts beside it rather than over it. Detected by the query in Rollback. |

## Rollback

**Persistent state on both halves, so this is required, and it is deliberately cheap on both.**

Both migrations are **additive**. `goose down` on the content-key migration drops its index and
column; `goose down` on the validity-window migration drops four columns whose empty value already
means "current", so every existing row reads correctly with or without them. Revert the code commits.

Nothing else is touched: **no drawer id changes**, so every `code_anchor`, tunnel,
`kg_triples.source_drawer_id`, `parent_id` and `search_events` row still resolves, and every Qdrant
point keeps the UUID5 it already has. There is no cross-store half-state to detect, because the
vector store is never written by either migration.

**One asymmetry to state plainly: rolling back after T4 has run is lossy in one direction.** Records
superseded while the feature was live remain readable as ordinary drawers after a revert — they
differ only by columns nothing reads — but they come BACK into default recall, because the filter
that hid them is gone. That is recoverable (the rows are all there) and it is visible (recall starts
returning corrected-away text), which is the right side of the trade; it is not silent. Nothing is
destroyed by rolling back — only by rolling forward and then erasing.

The one state to detect is a **partially backfilled** content-key column: rows with an empty
`content_key` in a non-diary room. T2 ships that as a query, and the migration aborts rather than
continuing past a failure, so a partial backfill is a failed migration rather than a silent one.

## Follow-ups

- [ ] **Received from ADR-026:** drawer validity windows and recall returning only current drawers. ADR-026 shipped the GRAPH half (`am_kg_query` returns open-ended facts by default) and deferred the drawer half — first to ADR-010, re-pointed here on 2026-08-27 when ADR-010 was superseded. T1 and T5 are that work, and ADR-026's condition holds: the two halves must share the `valid_to == ''` vocabulary rather than invent a second one.
- [ ] **Received from ADR-004:** ranking a supersession chain when history IS requested. ADR-004 owns how a history request is ranked; T5 creates `include_history` and defers the ordering to it, exactly as ADR-010 T3 did before this record absorbed it.
- [x] **Answered 2026-08-27, and it fell as ZERO.** `doctor --corpus` against the live 2,037-drawer palace reports **0 content keys disagreeing with their rows**, against 27 of 1,705 measured before this record. The follow-up asked for the number "whichever way it falls — including zero", and zero here does NOT mean the 27 were repaired by ordinary re-filing: it means the property changed. T2 backfilled a key onto every row and T3 put one on every mint, so a row's key now describes it by construction. **The 27 themselves were never repaired and did not need to be** — every one was correct as stored; what was missing was a record of which key described it. ⚠ This also makes `BACKLOG.md`'s "Repairing the drifted rows" entry describe a property that no longer exists: it is about ids that no longer derive from their fields, and after this record EVERY id is opaque, so that is the correct state of every row rather than a defect. Its trigger — a row whose CONTENT KEY also disagrees — survives and currently reads 0. Left for the owner to rewrite or close rather than edited here.
- [ ] Report the `reason` field's median length and a human read of a sample once T4 has been live for a month. Per ADR-010's own correction the measurement improves the PROMPTING and never retracts the field.
- [ ] Run the pre-registered falsification through ADR-004's existing `StaleAboveRate` once T4 has produced 30 verified non-vacuous pairs — the floor at `evalstats.go:417`, against 5 workspace-wide today. Record the MRR delta against the ~0.01 noise floor whichever way it falls, and report the pair count that made it answerable, since "the gate finally ran" is itself the result.
- [ ] **Retrieval ORDERING is not touched by this record and should not be read as touched.** Measured n=54 on 2026-08-26: in-pool 100%, top-1 46%, top-5 74% — the answer is always retrieved and ordering is what fails. That is ADR-001/002/003/030's territory, all still pending. This record only promises that accumulation does not make ordering WORSE.
- [ ] Nothing checks that an `ADR-NNN` cross-reference PATH still resolves — `adr-lint` reads README↔task consistency and not link targets, so this record's own rename would have left stale pointers passing every gate. Same class as ADR-037's T1. One gate, not built here.
- [ ] Report the first `doctor --corpus` run against the **hosted** deployment, whichever way it falls — including "clean", which would mean the drift is local to one palace rather than a property of the write paths. **Still open: the run recorded above is the SELF-HOSTED container, a different corpus.** It found 16 facts naming no row and 0 key drift; the hosted palace could differ on either.
- [ ] Decide whether ADR-027's remaining question — a reference pointing at a non-parent chunk that a re-chunk deletes — is answerable now that ids are opaque, or whether it needs its own record.
