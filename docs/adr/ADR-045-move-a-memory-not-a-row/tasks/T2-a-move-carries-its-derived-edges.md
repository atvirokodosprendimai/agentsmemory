# Task ADR-045-T2: Make a relocation carry its derived edges

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** none
**Consumes:** `Service.moveMemory()` (T1)
**Data dependency:** hermetic

## Goal

A relocation ends the derived edges pointing at the memory from the room it left and
attaches them at the room it arrived in, for single- and multi-chunk memories alike.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | `moveMemory` ends derived edges inside its transaction; `Service.Update` re-attaches after the commit |
| `internal/palace/move_test.go` | edit | The new test |

The edge primitives already exist and are already reached from three other write
paths, so nothing new needs registering: `endDerivedEdgesFor` is called by
`supersede.go:211` and `validity.go:115`, and `attachDerivedEdgeTo` by
`service.go:655`, `supersede.go:251` and `import.go:125`. This task adds the fourth
and last call site of an existing pair.

## Ordered Steps

1. Write the failing test first (TDD red): `TestAMoveEndsTheOldRoomsEdgeAndAttachesTheNew` in `internal/palace/move_test.go`, exercised with a SINGLE-chunk memory so it fails against today's code as well as T1's. Run the Acceptance fence and confirm RED.
2. In `moveMemory`'s transaction, call `endDerivedEdgesFor(tx, teamID, ids, endedAt, reason)` over every chunk id, with a reason naming the relocation, so the ending commits with the relabel or not at all.
3. After the commit, call `s.attachDerivedEdgeTo(ctx, teamID, moved)` with the post-move rows, as `supersedeInto` does at `supersede.go:251`.
4. Confirm an authored edge is untouched — `endDerivedEdgesFor` ends DERIVED edges only, and a hand-woven `am_kg_add` pointer at a moved drawer must survive, since the id did not change.
5. Run the full package suite.

## Acceptance

```bash
set -o pipefail
gofmt -l internal/palace | grep -q . && exit 1
go vet ./... && go test ./internal/palace/ -run "TestAMoveEndsTheOldRoomsEdgeAndAttachesTheNew" -count=1 -v 2>&1 | tee /tmp/adr045-t2-new.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr045-t2-new.out
go test ./internal/palace/ -count=1 2>&1 | tee /tmp/adr045-t2-reg.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/adr045-t2-reg.out
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAMoveEndsTheOldRoomsEdgeAndAttachesTheNew` | `internal/palace/move_test.go` | After relocating a memory, the old room's derived `holds` edge is ended and the new room's is current; an AUTHORED edge naming the same drawer id is still current. Runs as two subtests, single-chunk and multi-chunk, because the single-chunk case is the pre-existing defect and would otherwise never be exercised | — |

The single-chunk subtest is the falsifiability half and sits INSIDE the acceptance
fence rather than beside it: today's code fails it, which is what proves the test can
go red without waiting for a mutation campaign.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestAMoveEndsTheOldRoomsEdgeAndAttachesTheNew` |
| 2 — something selects it | The `endDerivedEdgesFor` call inside `moveMemory` and the `attachDerivedEdgeTo` after it; deleting either makes a subtest red |
| 3 — the caller can discover it | n/a: no declared interface — derived edges are server-minted and appear through `am_kg_query`, which already documents them |
| 4 — it is used | Nothing measures edge churn on relocation yet |

## Mutation Log

## Invariants

- Only DERIVED edges are ended. An authored edge keeps pointing at the drawer, which is correct because a move does not change the id.
- The ending commits inside the same transaction as the relabel: a room's `holds` edge never describes a room the drawer has left.
- Re-attachment happens after the commit and fails open, so a failure leaves a recoverable missing edge rather than a false one.

## Risks

- Re-attachment after the commit can fail, leaving the memory with no derived edge at either address — worse for traversal than a stale edge, though better than a wrong one. Mitigation: warn loudly; `am_recompute_graph` rebuilds derived edges from current drawers.

## Stop Condition

Stop and ask if `attachDerivedEdgeTo` turns out to be conditional on something the
move does not satisfy — it is documented as edging a freshly WRITTEN set of chunks,
and a relocated set is not freshly written. If it silently no-ops on rows it did not
create, this task is a different and larger change than described.

## Out of Scope

- Hallways and entity tunnels, which `am_recompute_graph` derives separately and which a relabel does not invalidate.
- Repointing AUTHORED edges, which need no repointing here because a move preserves the id.

## Verification Log
