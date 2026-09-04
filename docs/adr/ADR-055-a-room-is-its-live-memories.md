# ADR-055: A room is the set of its live memories, and every surface that lists rooms says so

**Status:** Proposed
**Date:** 2026-09-04
**Owner:** unassigned
**Spec:** None — no spec stage. The requirement is one sentence; the measurement that motivates it is in Context.
**Cross-references:** `docs/adr/ADR-015-the-index-must-not-outlive-the-wing-it-indexed.md` (the one destructive verb, `am_merge_wing`, and why deletion left the agent surface), `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md` (ending, not erasing), `internal/palace/repo.go`, `internal/palace/graphquery.go`
**Governs:** None — declared by its tasks
**Enforced-by:** None — no gate exists at authoring time. T1 produces `internal/palace/roomlife_test.go::TestEveryRoomListingAgreesOnARetractedRoom`, which fails when any room-listing surface still counts a room whose memories are all ended.
**Invalidates:** none — checked. ADR-015 keeps `am_merge_wing` as the only relabelling verb and this record adds no verb. ADR-038's ending-not-erasing is what this record builds on.
**Served-path change:** `am_graph_stats` (and any other surface T1's enumeration finds) stops counting a room whose memories are all retracted, so `am_list_rooms`, `am_get_taxonomy`, `am_status` and `am_graph_stats` give one answer to "which rooms does this wing hold".

## Context

A room comes into existence on the first write that names it, and there is no verb that removes one.
A mistyped room name is therefore a permanent addition to the taxonomy — that was the axis two inbox
findings (2026-08-29, re-checked 2026-09-02 and 2026-09-03) left open, the last of them narrowing it:
`am_memories_filed_away` had stopped counting rooms whose memories are all retracted, and
`am_list_rooms` and `am_graph_stats` "were not touched and were not re-measured".

Measured 2026-09-04 against the service in a hermetic test (`newTestService`, one wing, one real
memory in `decisions`, one memory filed into `typo-room` and then retracted with
`Service.InvalidateDrawer`):

| surface | rooms reported for the wing |
|---|---|
| `Service.Rooms` (`am_list_rooms`) | 1 — `typo-room` is gone |
| `Service.GraphStats` (`am_graph_stats`) | 2 — `TotalRooms: 2`, `RoomsPerWing: {wing: 2}` |

So the un-create verb already exists and is the one ADR-038 gave every memory: end it, or relocate
it with `am_update_drawer`. `Repo.Rooms` and `Repo.Wings` read `valid_to = ''`
(`internal/palace/repo.go`) and honour it. `GraphStats` does not, and a session reading the two side
by side sees a room that one surface says exists and another says does not. The defect is not a
missing verb; it is a listing that reads rows the rest of the palace has agreed to treat as
history.

## Existing Primitives Audit

- **Ending a memory** (`Service.InvalidateDrawer`, ADR-038) and **relocating one**
  (`am_update_drawer` with `room`, ADR-045). **Reused as the un-create verb:** a room with no live
  memory is no room, and both already exist.
- **`Repo.Rooms` / `Repo.Wings`** (`internal/palace/repo.go`) already filter on `valid_to = ''` and
  are the shape to copy. **Reused.**
- **`Service.GraphStats`** (`internal/palace/graphquery.go`) builds its room count from
  `Repo.GraphRoomWings` (`internal/palace/graph.go`), whose query filters `wing != '' AND room != ''
  AND room != 'general'` and never `valid_to` — verified in source 2026-09-04. **Reshaped:** that
  query reads live rows; `GraphStats` itself is unchanged.
- **Rejected as a primitive:** a `rooms` table with explicit create/delete. It would be a second
  source of truth for a fact the drawers table already carries, and the one destructive verb this
  surface kept (ADR-015) was kept precisely because a delete that erases is what the agent surface
  gave up.

## Decision

**A room exists exactly while it holds a live memory, and every surface that lists or counts rooms
derives from live rows.** No room-delete verb is added; retracting or relocating a room's last
memory is how a mistyped room disappears, and the record says so where an operator will read it
(`am_list_rooms`'s description and TROUBLESHOOTING.md).

T1 enumerates the class — every code path that groups drawers by `room` for a listing or a count —
with a command rather than from memory, brings each member to the `valid_to = ''` rule, and pins the
agreement with one test that files a memory into a fresh room, ends it, and asks every member. The
criterion is falsifiable today: the hermetic measurement above already produces the disagreement.
The decision is valid for the drawers table as the source of truth for room membership; it says
nothing about closets, hallways or tunnels, which name rooms as labels and are rebuilt by
`am_recompute_graph` (Out of Scope).

## Alternatives Considered

- **Add `am_delete_room`.** Rejected: a delete that erases contradicts ADR-038 (end, do not
  overwrite) and reintroduces the class of verb ADR-015 removed from the agent surface; and it is
  unnecessary once every listing reads live rows, because the memories' own ending already empties
  the room.
- **Leave `graph_stats` as it is and document the difference.** Rejected: two surfaces answering
  one question differently is the defect, and a sentence beside one of them does not remove it.
- **Count a room while ANY row, ended or not, names it, everywhere.** Rejected: this is the state
  `am_memories_filed_away` was corrected out of on 2026-09-03 (PR #187) after it reported 3,460
  memories for 1,142; the corpus has already chosen live rows as the unit.

## Component / Boundary Impact

`internal/palace` only. Ownership unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_graph_stats` `total_rooms`, `rooms_per_wing` | now count live rows only | `internal/palace` `GraphStats` | agents reading stats; the Stop hook's stats line |
| `am_list_rooms` description | states that a room with no live memory is gone, and how to empty one | `internal/mcpserver` | agents |

## Inter-task Contracts

None — one task.

## Implementation

See `tasks/README.md`. One task: enumerate the class, bring it to the rule, pin the agreement.

## Consequences

- **Positive:** one answer to "which rooms", and an operator can undo a mistyped room with a verb
  that already exists.
- **Negative:** a wing's room count in `am_graph_stats` can drop after a retraction — which is the
  point, and a reader who tracked the old number sees a change.
- **Neutral:** rooms named only by closets, hallways or tunnels keep their labels until the graph is
  recomputed.

## Out of Scope

- A room-delete verb (permanent: boundary: ADR-038 ends and never erases, and ADR-015 kept one
  relabelling verb on purpose; this record adds none)
- Closets, hallways and tunnels that name a room as a label (permanent: boundary: they are derived
  and rebuilt by `am_recompute_graph`; membership is the drawers table's fact)
- Renaming a room in place (permanent: fact: a drawer's room is changed only by `am_update_drawer`, which relocates one memory at a time; citation: file `internal/palace/service.go:1`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The enumeration misses a listing surface and two answers persist | Med | Low | T1 enumerates with a command recorded in the task, and the agreement test asks every member it names; a member added later that reads all rows breaks the test only if added to it — the Stop hook's stats line and `am_status` are named explicitly |
| A reader treats a dropped room count as data loss | Low | Low | the `am_list_rooms` description says what a room is |

## Rollback

Revert T1's filter; no schema or contract changes. Rows are never touched.

## Follow-ups

- [ ] none at authoring
