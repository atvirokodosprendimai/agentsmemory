# Task ADR-046-T2: Delete the entry-room chunk refusal

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** none
**Consumes:** `Bootstrap serves whole memories` (T1)
**Data dependency:** hermetic

## Goal

An entry record that would chunk is accepted, and no description claims otherwise.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | Delete the `room == EntryRoom && len(chunks) > 1` refusal in `prepareWrite` |
| `internal/mcpserver/drawers.go` | edit | The `am_add_drawer` description and the `room` parameter description both advertise the refusal; both go false when it is deleted |
| `internal/palace/entryroom_test.go` | add | The new tests |
| `internal/palace/wingroot_test.go` | edit | `TestAnEntryRecordThatChunksIsRefused` pins the refusal this task removes |
| `docs/adr/ADR-045-move-a-memory-not-a-row/tasks/T3-retire-the-one-way-door-claim.md` | edit | Its Invariants say the description "still names what IS refused: a chunking record in the entry room" — amended, not deleted, since the ENDED-record half stays true |

## Ordered Steps

1. Write the failing test first (TDD red): `TestALongEntryRecordIsAcceptedAndServedWhole` in `internal/palace/entryroom_test.go` — file a >`ChunkSize` record DIRECTLY into `llm_init` and assert both that the write succeeds and that `Bootstrap` returns it byte-identical. It is one test on purpose: accepting the write without serving it whole is the state this ADR exists to prevent, so the two assertions belong in one fixture. Confirm RED.
2. Audit the class BEFORE deleting anything: enumerate every reader of `EntryRoom` with `grep -rn EntryRoom --include=*.go . | grep -v _test` and decide, for each, whether it assumes one chunk. Record the command and its result in this file's Class Audit section. `attachDerivedEdgeTo`'s entry-room branch and `attachWingRootEdge` are the two to look at hardest — a wing root minted from a chunked record is the failure mode nobody would see.
3. Delete the refusal in `prepareWrite`.
4. Replace the two description strings: drop the entry-room refusal, keep the ENDED-record refusal, and keep the advice that a spine points at detail rather than inlining it — now as advice about wake-up cost, since it is no longer enforced.
5. Update `TestAnEntryRecordThatChunksIsRefused` to assert what is now true, or delete it and say in the commit which test replaced it.
6. Amend ADR-045 T3's Invariants line so it no longer claims the entry-room refusal is named in the description.
7. Run `go test ./...` — `TestNoToolDescriptionClaimsALongMemoryCannotBeMoved` and the repohygiene gates both read these files.

## Acceptance

```bash
gofmt -l internal/palace internal/mcpserver | grep -q . && exit 1
go vet ./... && go test ./internal/palace/ -run "TestALongEntryRecordIsAcceptedAndServedWhole" -count=1 -v 2>&1 | tee /tmp/adr046-t2-new.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr046-t2-new.out
go test ./... -count=1 2>&1 | tee /tmp/adr046-t2-reg.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/adr046-t2-reg.out
```

The regression command is `./...` rather than one package, because the descriptions
are gated from `internal/mcpserver` and `AGENTS.md` from `internal/repohygiene`.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestALongEntryRecordIsAcceptedAndServedWhole` | `internal/palace/entryroom_test.go` | A >`ChunkSize` record files directly into `llm_init` AND comes back whole from `Bootstrap` — the write and the serving asserted together, because either alone is the state this ADR prevents | — |
| `TestAWingRootIsMintedFromAChunkedEntryRecord` | `internal/palace/entryroom_test.go` | Filing a chunked record into the entry room still mints the wing's by-name root exactly once, since `attachDerivedEdgeTo` edges the ROOT chunk only | — |

The second test exists because step 2's audit names it as the failure nobody would
see: the root is minted in a branch keyed on `d.Room == EntryRoom`, and a chunked
record is the shape that branch has never been given.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestALongEntryRecordIsAcceptedAndServedWhole` |
| 2 — something selects it | Deleting a refusal removes a call site rather than adding one; the mutation is to REINSTATE the guard and confirm the new test goes red |
| 3 — the caller can discover it | The `am_add_drawer` and `room` descriptions, which step 4 rewrites — they are the only place a caller learns the entry room's rules |
| 4 — it is used | Nothing measures entry-record length; the ADR-046 follow-up owes a sweep of records that were split to fit the refusal |

## Mutation Log

## Invariants

- The ENDED-record relocation refusal is untouched.
- Filing into the entry room still mints the wing's by-name root, exactly once per wing per batch.
- No description claims a refusal that no longer exists — checked by `TestNoToolDescriptionClaimsALongMemoryCannotBeMoved`'s sibling gates and by review.
- ADR-045 T3's record is amended rather than rewritten: its ENDED-record clause stays.

## Risks

- Some path other than `Bootstrap` may assume an entry record is one chunk. Mitigation: step 2's class audit runs before the deletion and records its result; `TestAWingRootIsMintedFromAChunkedEntryRecord` covers the one the audit flags.
- Entry records grow, inflating every wake-up. Mitigation: accepted and stated in the ADR's Consequences; the paging option is filed with a trigger.

## Stop Condition

Stop and ask if step 2's audit finds a reader of `EntryRoom` that assumes one chunk
and is NOT fixable in this task — deleting the refusal would then move the silent
truncation somewhere else rather than removing it, which is worse than leaving the
refusal in place and closing the ADR-045 move bypass instead.

## Out of Scope

- Rejoining the palace's own entry records that were split to fit the refusal (deferred: `docs/adr/ADR-046-serve-the-whole-entry-record.md` — Follow-ups)
- The `ChunkSize` threshold (permanent: this task removes a refusal keyed on chunk count, not the chunking)

## Verification Log
