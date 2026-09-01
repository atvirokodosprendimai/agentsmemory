# Task ADR-045-T1: Relocate every chunk of a memory in one transaction

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `Service.moveMemory()`
**Consumes:** none
**Data dependency:** hermetic

## Goal

`Service.Update` relocates a memory of any chunk count by patching every chunk in one
transaction, and the `len(chunks) > 1` refusal is deleted.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | Delete the refusal at the `len(chunks) > 1` branch; route the move through `moveMemory` over every chunk; drop the `EmbedOne` call, which re-embeds unchanged content |
| `internal/palace/repo.go` | edit | Correct the stale comment claiming `Service.Update` refuses a content change on a multi-chunk memory — false since the supersede branch moved ahead of the guard |
| `internal/palace/service_test.go` | edit | `TestUpdateRefusesToHalfRewriteAMultiChunkMemory` asserts the refusal this task removes; it becomes the relocation test's negative half or is replaced |
| `internal/palace/move_test.go` | add | The new tests |

Nothing selects a move except `Service.Update` itself, which `internal/mcpserver/drawers.go`
already calls for `am_update_drawer` — no registry, flag or composition root is
involved, so no new wiring line exists to delete. T3 covers the description that
advertises the behaviour.

## Ordered Steps

1. Write the failing tests first (TDD red): `TestAMoveRelocatesEveryChunkOfAMemory` and `TestAMoveThatCollidesOnAnyChunkRelocatesNone` in `internal/palace/move_test.go`. Run the Acceptance fence and confirm it is RED before writing any implementation.
2. Add `Service.moveMemory(ctx, teamID, chunks []Drawer, wing, room string)`: open one transaction, call `Repo.Update` per chunk with the wing/room patch so each row's `content_key` is recomputed from post-patch state, and return the named collision error unchanged so a collision rolls the whole transaction back.
3. In `Service.Update`, delete the `len(chunks) > 1` refusal and route the move branch through `moveMemory` with the already-resolved `chunks`.
4. Remove the `EmbedOne` call on the move path and upsert only the payload for every moved chunk id, after the commit. The content is byte-identical, so the stored vector is already correct; document that in place of the comment being deleted.
5. Correct the stale `repo.go` comment about `Service.Update` refusing multi-chunk content changes.
6. Update or replace `TestUpdateRefusesToHalfRewriteAMultiChunkMemory` so the suite no longer asserts the removed refusal.
7. Run the full package suite and confirm no regression.

## Acceptance

```bash
set -o pipefail
gofmt -l internal/palace | grep -q . && exit 1
go vet ./... && go test ./internal/palace/ -run "TestAMoveRelocatesEveryChunkOfAMemory|TestAMoveThatCollidesOnAnyChunkRelocatesNone" -count=1 -v 2>&1 | tee /tmp/adr045-t1-new.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr045-t1-new.out
go test ./internal/palace/ -count=1 2>&1 | tee /tmp/adr045-t1-reg.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/adr045-t1-reg.out
```

The new tests run ALONE in the second command, so the regression suite in the third
cannot carry the verdict. Before either test exists that command prints
`no tests to run` and the fence is red.

⚠ This fence runs the LOCAL toolchain, not `golang:1.26-alpine` under docker like the
rest of this corpus. Amended 2026-09-01 during execution: docker is unavailable on the
machine executing this ADR, and a fence that cannot run is not a gate. Local
`go1.26.6` against the image's 1.26 and `go.mod`'s `go 1.25.7`. No evidence was
invalidated, because none had been recorded yet — which is the cheapest moment for
this change and the reason it was made before T1's first verified run rather than
after.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAMoveRelocatesEveryChunkOfAMemory` | `internal/palace/move_test.go` | A wing/room patch addressed through ANY chunk's id moves all N rows; ids, `parent_id` and content are unchanged; every point's payload reports the new wing | — |
| `TestAMoveThatCollidesOnAnyChunkRelocatesNone` | `internal/palace/move_test.go` | With identical text already at the destination so chunk k collides on `content_key`, the call returns the named collision error and every chunk — including 0..k-1 — is still at the old address | — |

Shapes the creation path can already produce, and the decision for each: a **single-chunk**
memory (covered — the same code path with N=1, and T2 asserts its edges); a memory whose
chunk 0 is `parent_id`-less while children point at it (covered — `TestAMoveRelocatesEveryChunkOfAMemory`
asserts parentage survives); a memory with an **ended** chunk among current ones, which
`purgeSource` can leave behind (out of scope — `service.go:1244` already refuses to move
an ended record, and the move resolves `MemoryChunks` from the id it was given); a **diary**
memory, whose `content_key` is empty by `contentKeyOf` (out of scope — no collision is
possible without a key, and the move is otherwise identical).

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestAMoveRelocatesEveryChunkOfAMemory` |
| 2 — something selects it | `Service.Update`'s move branch calls `moveMemory`; the mutation deleting that call makes both new tests red, since no other path relocates |
| 3 — the caller can discover it | T3's gate over `internal/mcpserver/drawers.go` — the `am_add_drawer` description is the only place a caller learns whether a long memory can be moved |
| 4 — it is used | Nothing measures relocation volume yet |

## Mutation Log

- 2026-09-01 · 25f5b17* · mutant killed · exit 1 · `internal/palace/service.go` · strips the transaction from moveMemory so a collision on chunk 1 cannot roll chunk 0 back · acceptance-sha256:e9c72321933014785387f478ed0d7bd0baaf5b77503e6ec709a04c4cf04d642d
- 2026-09-01 · 25f5b17* · mutant killed · exit 1 · `internal/palace/service.go` · moves only the root chunk, which is the row-scoped behaviour ADR-045 removes · acceptance-sha256:e9c72321933014785387f478ed0d7bd0baaf5b77503e6ec709a04c4cf04d642d
- 2026-09-01 · 25f5b17* · mutant killed · exit 1 · `internal/palace/service.go` · relabels no points, so the rows move and the index keeps answering from the old wing · acceptance-sha256:e9c72321933014785387f478ed0d7bd0baaf5b77503e6ec709a04c4cf04d642d

## Invariants

- A move mints no id, deletes no row and creates none: `MemoryChunks` returns the same ids, in the same order, before and after.
- A move re-embeds nothing. The content is unchanged, so the stored vector stays correct and only the payload's `wing`/`room` change.
- Either every chunk moves or none does.
- `content_key` is recomputed from post-patch state for each row, so the partial unique index keeps meaning what ADR-038 gave it.
- A correction still supersedes: the content branch stays ahead of the move branch in `Service.Update`.

## Risks

- The collision error surfaces from `Repo.Update` mid-loop; if the transaction is not the one `Repo.Update` writes through, the rollback is a no-op and the split state ships. Mitigation: pass the transaction's `*gorm.DB` into `&Repo{db: tx}`, as `supersedeInto` already does at its `persistRows` call.
- Vector payload upsert after commit can fail, leaving points labelled with the old wing. Mitigation: fail open with a warning; the row is authoritative and `doctor --index` reports the disagreement as `Mislabelled`.

## Stop Condition

Stop and ask if the store in front of you does not give `Repo.Update` a transaction
that actually rolls back — the whole decision rests on atomicity, and without it the
correct action is to keep the refusal rather than ship a non-atomic move. Also stop if
`TestAMoveThatCollidesOnAnyChunkRelocatesNone` cannot be made to collide: a criterion
that cannot fail authorises everything after it, and the collision is the one thing
this task must prove it handles.

## Out of Scope

- Derived edges on the move path — that is T2's job.
- The `am_add_drawer` description and its gate — that is T3's job.
- Re-chunking or re-embedding on a content update (deferred: `docs/adr/BACKLOG.md` §"From ADR-038 (dedupe on the content, refer by the id)")

## Class Audit

The class: **a `Service` write path that resolves `MemoryChunks` and then acts on
one row instead of every chunk.** Enumerated 2026-09-01, after the fix, with

    grep -rn '^func (s \*Service) \(Update\|Delete\|Supersede\|InvalidateDrawer\|EndDrawer\)' internal/palace/*.go
    grep -rn 'len(chunks) > 1' --include=*.go . | grep -v _test

Five methods; `refusal=0` for all of them. `EndDrawer` holds no `MemoryChunks`
reference because it is the per-chunk primitive `InvalidateDrawer` loops over,
which is correct by construction.

The second sweep returns exactly ONE remaining chunk-count guard,
`internal/palace/service.go:715` — `room == EntryRoom && len(chunks) > 1`. That is a
DIFFERENT rule and is deliberately left standing: `am_bootstrap` serves the entry
room one chunk at a time, so a record that chunks there arrives cut with nothing
marking it partial. It is a refusal about a ROOM, not about whether a memory can be
addressed as a whole, so ADR-045 does not touch it.

A sibling found and excluded on purpose, rather than a sweep that found nothing.

## Verification Log
- 2026-09-01 · 25f5b17* · exit 1 · `set -o pipefail …` · acceptance-sha256:e9c72321933014785387f478ed0d7bd0baaf5b77503e6ec709a04c4cf04d642d
  ```
  --- last 10 line(s) of stdout (of 141 after folding 142 raw)
  2026/09/01 09:34:49 OK   00031_drawers_content_key.sql (606.08µs)
  2026/09/01 09:34:49 OK   00032_kg_ended_reason.sql (483.54µs)
  2026/09/01 09:34:49 OK   00033_drawers_superseded_by_idx.sql (383.25µs)
  2026/09/01 09:34:49 OK   00034_billing_checkout_intents.sql (506.29µs)
  2026/09/01 09:34:49 OK   00035_billing_applied_orders.sql (330.25µs)
  2026/09/01 09:34:49 OK   00036_drawer_fetches.sql (411.92µs)
  2026/09/01 09:34:49 goose: successfully migrated database to version: 36
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	12.088s
  FAIL
  ```
- 2026-09-01 · 25f5b17* · exit 0 · `set -o pipefail …` · acceptance-sha256:e9c72321933014785387f478ed0d7bd0baaf5b77503e6ec709a04c4cf04d642d
