# ADR-045: Move a memory, not a row

**Status:** Accepted
**Date:** 2026-09-01
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md`, `docs/adr/ADR-013-a-page-of-memories-not-chunks.md`, `docs/adr/ADR-024-rank-memories-not-chunks.md`, `docs/adr/ADR-027-a-maintained-document-is-a-set-of-records.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/palace/service.go`, `internal/palace/repo.go`, `internal/mcpserver/drawers.go`
**Enforced-by:** `internal/palace/move_test.go::TestAMoveRelocatesEveryChunkOfAMemory`
**Invalidates:** none — checked
**Served-path change:** `am_update_drawer` with `wing`/`room` relocates a memory of any length instead of refusing above ~1600 runes, and a relocation of any size stops leaving the old room's derived `holds` edge pointing at a drawer that left.

## Context

A memory over `ChunkSize` is several rows sharing a parent. Four `Service` methods
mutate an existing memory and all four resolve `MemoryChunks` first — but three of
them do it to ACT on every chunk and one does it to REFUSE:

    grep -rn '^func (s \*Service) \(Update\|Delete\|Supersede\|InvalidateDrawer\|EndDrawer\)' internal/palace/*.go

Run 2026-09-01: five methods. `Delete`, `Supersede` and `InvalidateDrawer` operate on
every chunk. `EndDrawer` is the per-chunk primitive `InvalidateDrawer` loops over, so
it is row-scoped by construction. `Update`'s move is the only one that resolves the
memory and then declines, at `internal/palace/service.go:1305`. Its own message says
what it is: *"Moving a whole multi-chunk memory is not expressible yet."*

That refusal is load-bearing on the agent surface. `internal/mcpserver/drawers.go:191`
tells every session a memory born long is *"never MOVED"* and one born short *"can be
relocated for life"*, so agents trim to stay under the threshold. In this
conversation, on 2026-09-01, one record was written at 1922 runes, split, measured at
1633, trimmed, measured at 1605, trimmed again, and filed at 1596 — four measure/trim
rounds whose only purchase was keeping the move available.

`docs/adr/BACKLOG.md` §"A memory is several rows and most operations treat it as one"
recorded both halves of this in 2026-08-20, and §"From ADR-038" set the trigger:
*"whenever `Service.Update`'s multi-chunk refusal blocks real work again — it already
blocks one live drawer measured at 6,448 runes."* The trigger has fired.

The backlog also names the blocker that kept it ADR-sized: re-chunking changes which
ids exist, and ADR-027's open question is what happens to a reference pointing at a
non-parent chunk that a re-chunk deletes. **This record does not answer that question
and does not need to.** A move does not change content, so chunk boundaries and
`chunk_index` are unchanged, no row is created or destroyed, and no reference is
invalidated. ADR-038 already made ids opaque and minted-once, which is why the rows
can be relabelled in place at all. Re-chunking on update stays deferred.

A second defect surfaced while reading the path: the move is the only write path in
the package that touches no derived edges. `Add` attaches (`service.go:655`),
`Supersede` ends and re-attaches (`supersede.go:211,251`), `Delete` drops
(`service.go:1390`), `InvalidateDrawer` ends via `validity.go:115` — the move does
none, so a **single-chunk** relocation today leaves the old room's derived `holds`
edge current. The multi-chunk refusal is the visible half of an unfinished path.

## Existing Primitives Audit

- **`Repo.MemoryChunks`** — resolves every row of a memory from any chunk's id. Reused as-is; it is what `Delete`, `Supersede` and `InvalidateDrawer` already call.
- **`Repo.Update`** — already recomputes `content_key` from post-patch state and already names a collision instead of leaking the driver error (`repo.go:483-516`). Reused per row inside the new transaction; not reshaped.
- **`endDerivedEdgesFor` / `Service.attachDerivedEdgeTo`** — already the end-then-attach pair `Supersede` uses. Reused; no new edge primitive.
- **`store.Vectors.Upsert`** — already accepts a point whose payload carries `wing`/`room`. Reused for the N moved ids.
- **None replaced.** The only new symbol is the transaction that drives them over N rows instead of 1.

## Decision

Replace `Service.Update`'s row-scoped move with a memory-scoped one and delete the
`len(chunks) > 1` refusal.

A move resolves `MemoryChunks` and, in ONE transaction, patches `wing`, `room` and a
recomputed `content_key` on every chunk, then ends the derived edges pointing at those
rows. After the commit it upserts each chunk's vector payload and re-attaches derived
edges at the new address. **Nothing is re-chunked, re-embedded or re-minted:** the
content is byte-identical, so the existing vectors are correct and the ids are stable,
which is what keeps every knowledge-graph fact, code anchor and pinned tunnel intact.

The criterion this turns on is atomicity under collision, and it is falsifiable with
data that exists today. `content_key` hashes wing and room, so a move recomputes N
keys against a partial unique index and any one of them can collide with a memory
already in the target room. **The decision fails if a collision on chunk k leaves
chunks 0..k-1 relocated** — that is the split-scope state the old refusal existed to
prevent, reintroduced by a non-atomic fix. A collision is constructible in a unit
test by filing identical text at the destination first, so the criterion can be
exercised rather than asserted. This is valid for the SQLite-backed store the tests
and the shipped server both use; it says nothing about a backend without transactions.

Staying under `ChunkSize` remains good advice for recall — one drawer is one vector,
and a long record averages its topics. It stops being enforced by a refusal.

## Alternatives Considered

- **Delete the chunks and re-file them at the new address.** What the caller reaches for first, and what the old refusal's message recommended. Rejected because `DrawerID` hashes wing and room (`chunk.go:159`), so re-filing mints N NEW ids and silently orphans every KG fact, anchor and tunnel pointing at the old ones — and it spans SQLite and the vector store non-atomically, so a crash between them loses the memory. It also destroys the ended rows in a store that is bitemporal by design (ADR-038).
- **Keep the refusal and raise `ChunkSize`.** Rejected because it moves the cliff instead of removing it: the same trimming rounds happen at the new number, and the split-scope hazard is unchanged for anything above it.
- **Re-chunk and re-embed on every update, unifying the move and correction paths.** Rejected for THIS record because it reopens ADR-027's unanswered question — what becomes of a reference pointing at a non-parent chunk a re-chunk deletes — which the move does not raise. Deferred, not dismissed.
- **Refuse the move but let the caller correct-and-relocate in one call.** Already possible: `am_update_drawer` accepts `content` with `wing`/`room` and `supersedeInto` honours both. Rejected as the answer because it costs the caller the entire content in output tokens to change a label, which is the cost this record exists to remove.

## Component / Boundary Impact

None — internal to `internal/palace`, plus one description string in
`internal/mcpserver`. No module is added, moved or re-owned; `palace` already owns
memory identity and the vector store handle, and `mcpserver` already owns the tool
descriptions. The repo has no `docs/architecture.md`, so no Module Map inherits.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_update_drawer` behaviour | A `wing`/`room` patch on a memory of any chunk count relocates the whole memory instead of returning `ErrInvalidInput` | `internal/palace` `Service.Update` | MCP callers (agents) |
| `am_add_drawer` tool description | Drops the claim that a memory over the threshold is "never MOVED"; keeps the recall rationale as advice | `internal/mcpserver/drawers.go` | MCP callers (agents) |
| Derived `holds` edges | A relocation now ends the old room's edge and attaches one at the new room, for single- and multi-chunk memories alike | `internal/palace` | `am_kg_query`, `am_entry_point`, `am_bootstrap` |

No schema change: `wing`, `room` and `content_key` are existing columns, and the
vector payload already carries `wing`/`room`.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `Service.moveMemory()` | T1 | T2, T3 | No — new unexported helper; `Service.Update`'s signature is unchanged |

## Implementation

See `tasks/README.md`. Three tasks, sequential.

## Consequences

- **Positive:** the trimming tax disappears. A record is written at the length its subject needs, and the four measure/trim rounds this conversation spent buy nothing because nothing is lost by exceeding the threshold.
- **Positive:** a single-chunk relocation stops orphaning its old room's derived edge — a pre-existing defect nothing reported, fixed by the same transaction.
- **Positive:** every operation on a memory becomes memory-scoped, so `MemoryChunks` means one thing across the write surface instead of two.
- **Negative:** a move now writes N rows and N vector payloads instead of 1, and can fail partway on a collision. The transaction is what makes that safe, and it is the only thing that does — a future backend without one reintroduces the hazard silently.
- **Negative:** the `1600` figure loses the enforcement that made agents respect it, so a legitimate recall argument now travels only as advice. Sessions that ignore it get mushier vectors and nothing tells them.
- **Neutral:** ids, anchors, tunnels and KG facts are untouched by a move, so nothing downstream needs migrating and no backfill is required.

## Out of Scope

- Re-chunking and re-embedding on a content update, and ADR-027's question about a reference pointing at a non-parent chunk a re-chunk deletes (deferred: `docs/adr/BACKLOG.md` §"From ADR-038 (dedupe on the content, refer by the id)")
- Deleting or filtering the vectors a superseded memory leaves in the index, and any durable job queue for that repair (deferred: `docs/adr/BACKLOG.md` §"From ADR-045 (move a memory, not a row)")
- Raising or removing `ChunkSize` itself (permanent: this record removes the CONSEQUENCE of the threshold, not the threshold; the one-vector-per-drawer recall argument for a small chunk is unaffected either way)
- Relocating an ENDED record (permanent: `service.go:1244` refuses it deliberately — the first ending is the one that is true, and relocating history rewrites where a decision was taken)
- `am_merge_wing`, which relabels wings wholesale by a different path (permanent: this record is about one memory moved by its author, not a bulk administrative relabel)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A `content_key` collision on chunk k>0 leaves chunks 0..k-1 moved | Med | High | One transaction over all N rows; T1's second test constructs the collision and asserts every chunk stayed put |
| The post-commit vector payload upsert fails, leaving points labelled with the old wing | Med | Med | Fails open with a warning, as `carryAnchors` does; the row is authoritative and `doctor --index` already reports payload/row disagreement as `Mislabelled` |
| Removing the refusal invites long records, degrading recall through averaged vectors | Med | Low | The recall rationale stays in the `am_add_drawer` description as advice; T3's gate keeps the description honest about what is enforced and what is advised |
| The tool description and this decision drift apart later | Med | Med | T3 adds a source-level gate rather than trusting prose, per §Reachability in `AGENTS.md` |

## Rollback

Restore the `len(chunks) > 1` guard in `Service.Update` and revert the description
string. No data migration is needed in either direction: a move writes only `wing`,
`room` and `content_key` on existing rows, mints nothing and deletes nothing, so
memories relocated while this was live remain valid and readable after a revert —
they are simply at their new address, exactly as a single-chunk move would have left
them. Derived edges re-attach on the next write to the room and are rebuildable with
`am_recompute_graph`.

## Follow-ups

- [ ] Correct the palace records that teach the one-way door: `wing_craft` L0 leaf `sizes` (`3c5645ce…`), the `start-here` skill §Size, and `wing_agentmemories/learnings` `0b771576…`. These live in the memory server, not this repository, so no task can gate them.
