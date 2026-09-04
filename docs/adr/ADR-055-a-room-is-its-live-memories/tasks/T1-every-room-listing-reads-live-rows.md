# Task ADR-055-T1: Every surface that lists or counts rooms reads live rows, and one test asks them all

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `GraphStats` room counts over live rows; `am_list_rooms` description stating what a room is
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the room aggregate filters valid_to`, `the agreement test asks every member`

## Goal

A room whose memories are all ended is absent from every surface that lists or counts rooms, and one test proves the surfaces agree.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/graph.go` | edit | `Repo.GraphRoomWings` — the query `GraphStats` builds `TotalRooms` and `RoomsPerWing` from — adds `valid_to = ''`, the rule `Repo.Rooms` and `Repo.Wings` already follow; `graphquery.go` is unchanged |
| `internal/palace/roomlife_test.go` | add | the enumeration's command in a comment, and `TestEveryRoomListingAgreesOnARetractedRoom` |
| `internal/mcpserver/drawers.go` | edit | `list_rooms` description: a room exists while it holds a live memory; retract or relocate its last memory to remove a mistyped one |
| `TROUBLESHOOTING.md` | edit | a short "I created a room by mistake" entry naming the same two verbs |

Enumerate the class before editing, and record the command and count in the test file's header
comment. ⚠ The first draft's pattern (`DISTINCT room|GROUP BY.*room|Group("wing, room")`) missed
the member that matters: `GraphRoomWings` selects `room` with no GROUP BY and filters on `room != ''`.
The pattern that finds every query touching the room column of the drawers table is:
`grep -rn 'room != \x27\x27\|DISTINCT room\|Group("wing, room")\|COUNT(DISTINCT room)\|Select("room' --include='*.go' internal cmd | grep -v _test`.
Measured 2026-09-04 at 8c8945f: `Repo.Wings` and `Repo.Rooms` (`internal/palace/repo.go`, both filtered on `valid_to = ''`) and `Repo.GraphRoomWings` (`internal/palace/graph.go`, unfiltered — the source of `GraphStats`' count). A member the command finds and this table does not name is a finding to add to both; a reviewer found this one by reading the call path rather than by the grep, which is why the pattern is recorded beside its miss.

## Ordered Steps

1. [S1] Write `TestEveryRoomListingAgreesOnARetractedRoom` and run it red: one wing, one live memory in `decisions`, one memory filed into a fresh room and then ended with `InvalidateDrawer`; assert `Rooms`, `Wings` (its `rooms` count) and `GraphStats` (`TotalRooms`, `RoomsPerWing[wing]`) all report one room. The measurement of 2026-09-04 says `GraphStats` reports two, so this is red before the edit.
2. [S2] Add the filter to the `GraphStats` aggregate; run the fence green. `[proof: acceptance]`
3. [S3] Update the `list_rooms` description and TROUBLESHOOTING.md; `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` is untouched because no key is added — the description change is prose, checked by reading. `[proof: human: a reader confirms the description names both verbs]`

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ -run 'TestEveryRoomListingAgreesOnARetractedRoom' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./internal/palace/ ./internal/mcpserver/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryRoomListingAgreesOnARetractedRoom` | `internal/palace/roomlife_test.go` | after ending a room's only memory, `Rooms`, `Wings` and `GraphStats` agree on one room | — | S1, S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the test |
| 2 — something selects it | the `valid_to = ''` clause in `GraphStats`; the mutant is deleting it, which turns the test red on the `GraphStats` assertion |
| 3 — the caller can discover it | the `list_rooms` description names the two verbs that empty a room |
| 4 — it is used | nothing measures this yet; a later `am_graph_stats` reading on the local palace after a retraction would |

## Mutation Log

## Invariants

- No row is deleted or rewritten; ended rows stay readable by id.
- `Repo.Rooms` and `Repo.Wings` are unchanged.

## Risks

- A listing surface the enumeration did not find keeps counting ended rooms — the command is recorded so the next reader re-runs it rather than trusting the table.

## Stop Condition

Stop if `GraphStats`' room count is consumed by something that expects the historical figure (a dashboard, a test fixture with the old number); the record then needs a sentence about that consumer.

## Out of Scope

- Closets, hallways and tunnels that name a room as a label (the ADR's Out of Scope).

## Verification Log
