# ADR-046: Serve the whole entry record, then stop refusing long ones

**Status:** Accepted
**Date:** 2026-09-01
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/ADR-045-move-a-memory-not-a-row.md`, `docs/adr/ADR-043-one-spelling-for-the-entry-room.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/palace/bootstrap.go`, `internal/palace/service.go`, `internal/mcpserver/drawers.go`
**Enforced-by:** `internal/palace/bootstrap_whole_test.go::TestBootstrapServesEveryChunkOfAnEntryRecord`
**Invalidates:** `ADR-045` — T3's Invariants say the am_add_drawer description "still names what IS refused: a chunking record in the entry room". This record removes that refusal, so that clause is amended in the same commit.
**Served-path change:** `am_bootstrap`'s eager tier returns each entry record whole instead of its first chunk, and `am_add_drawer` stops refusing an entry record that would chunk.

## Context

Measured 2026-09-01 against this tree, driving `Service.Bootstrap` over a 3,600-rune
entry record:

    eager[0]: 1600 runes of 3600 total; ends "ENTRY: load the craft tier first and the"
    truncation.omitted=0 reason=""

The eager tier carries chunk 0 and stops. The response's own loss accounting says
`omitted: 0` with an empty reason — so a session is told nothing was withheld while
2,000 runes were, cut mid-sentence. `prepareWrite` refuses such a record at CREATION
to prevent exactly this (`service.go:715`), and that refusal is the only thing
standing between the corpus and a silently truncated front door.

⚠ **The refusal is already circumventable, and ADR-045 is what made it so.** It lives
in `prepareWrite`; `moveMemory` calls `Repo.Update` per chunk and never routes through
it. The measurement above was taken by filing a 3-chunk memory into `decisions` and
MOVING it into `llm_init` — the guard never fired. So as of `3545a9b` the guard
protects the write path only, and the state it exists to prevent is reachable in two
calls.

That leaves two ways forward. Adding the same check to the move path restores the
guarantee and keeps a limit whose whole justification is a serving bug. Fixing the
serving bug removes the reason for the limit. The refusal's own error message names
the cause — *"am_bootstrap serves the eager tier one chunk at a time"* — so it has
always been a workaround wearing the shape of a rule.

`reassembleMemory` already exists and is already the search path's answer to the same
question (`memory_search.go:358`); `MemoryChunks` resolves a memory's rows with no
wing or room filter (`repo.go:858`), so reassembly needs no new query.

## Existing Primitives Audit

- **`reassembleMemory`** — joins chunks and removes the `ChunkOverlap` seam; used by the search path today. Reused verbatim, not reshaped.
- **`Repo.MemoryChunksByRoots`** — resolves many memories' chunks in one query, which is the shape the eager tier needs (up to `bootstrapEagerLimit` roots). Reused; the per-id `MemoryChunks` would be N queries for the same answer.
- **`BootstrapResult.Truncation`** — the existing loss report. Reused; this record makes it TRUE rather than adding a field beside it.
- **None replaced.** No new primitive.

## Decision

`Service.Bootstrap` hydrates each eager record from every chunk of its memory and
returns the reassembled content, so an entry record arrives whole whatever its length.
With the serving bug gone, the `room == EntryRoom && len(chunks) > 1` refusal in
`prepareWrite` is deleted, and the `am_add_drawer` and `room` descriptions stop
advertising it.

The criterion is falsifiable with data that exists: an entry record longer than
`ChunkSize` must come back byte-identical to what was filed. It FAILS if the eager
tier returns a prefix, or if reassembly drops or duplicates the overlap seam — both
constructible in a unit test today, and the second is why the assertion is byte
equality against the source text rather than a length check.

This does not widen what the eager tier may cost. `bootstrapEagerLimit` still bounds
how many records are inlined; this record changes how much of EACH one is true, not
how many there are. That trade is stated plainly in Consequences: a long entry record
now costs its full length at every wake-up, which is the cost of the front door being
correct, and the existing bound is what keeps it finite.

## Alternatives Considered

- **Add the entry-room check to the move path too.** The smaller diff, and it closes the hole ADR-045 opened. Rejected because it spends a second enforcement point defending a limit that exists only because of a serving bug, and leaves the front door still unable to serve a long record — the operator-visible problem is untouched.
- **Mark the truncation honestly and keep serving one chunk.** Setting `omitted` and a reason would at least stop the silent loss. Rejected because a front door that reports "I gave you 44% of the entry protocol" is not usable: the session still does not have the protocol, and now knows it.
- **Page the eager tier.** Deferred, not dismissed — `bootstrapEagerLimit` already bounds the tier and no entry record in this corpus approaches a size where paging beats reassembly.
- **Leave the refusal and document the bypass.** Rejected: a rule enforced on one path and reachable on another is worse than no rule, because it reads as a guarantee.

## Component / Boundary Impact

None — internal to `internal/palace` plus two description strings in
`internal/mcpserver`. No module is added, moved or re-owned.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_bootstrap` eager tier | Each record's `content` is the whole memory, not its root chunk | `internal/palace/bootstrap.go` | MCP callers (agents) |
| `am_add_drawer` | An entry record that would chunk is accepted | `internal/palace/service.go` | MCP callers (agents) |
| `am_add_drawer` / `room` descriptions | Stop advertising the entry-room refusal | `internal/mcpserver/drawers.go` | MCP callers (agents) |

No schema change.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `Bootstrap` serves whole memories | T1 | T2 | No — T2 removes a refusal that is only safe once T1 has landed |

## Implementation

See `tasks/README.md`. Two tasks, sequential.

## Consequences

- **Positive:** the front door stops lying. An entry record arrives whole, and `omitted: 0` becomes true rather than merely present.
- **Positive:** the bypass ADR-045 opened is closed by removing the thing being bypassed, not by adding a second guard.
- **Positive:** entry records can be written at the length their subject needs, which is what the `llm_init` room was always for.
- **Negative:** a long entry record now costs its full length in every wake-up, on the one call no session skips. `bootstrapEagerLimit` bounds the count and nothing bounds the size — the discipline that a spine points at detail rather than inlining it becomes advice, exactly as it did for relocation in ADR-045.
- **Neutral:** existing short entry records are unaffected; reassembly of a one-chunk memory returns its content unchanged.

## Out of Scope

- Paging or byte-bounding the eager tier (deferred: `docs/adr/BACKLOG.md` §"From ADR-046 (serve the whole entry record)")
- `am_get_drawer`'s `whole` flag, which already reassembles correctly and is what proves reassembly is the right answer here (permanent: this record changes the bootstrap path only)
- The `ChunkSize` threshold itself (permanent: this record removes a REFUSAL keyed on chunk count, not the chunking)
- Whether `am_bootstrap` should serve the on-demand tier differently (permanent: pointers are pointers by design; this is about the eager tier's content)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A very long entry record inflates every wake-up | Med | Med | `bootstrapEagerLimit` bounds the count; the size trade is stated in Consequences and the paging option is filed with a trigger |
| Reassembly drops or duplicates the overlap seam | Low | High | T1 asserts byte equality against the filed text, not a length; `reassembleMemory` is the search path's existing, exercised implementation |
| Removing the refusal lets a chunked record reach some OTHER path that assumes one chunk | Med | High | T2's class audit enumerates every reader of `EntryRoom` before the refusal is deleted, and records the command and its result |

## Rollback

Restore the `room == EntryRoom && len(chunks) > 1` guard and revert the Bootstrap
hydration. No migration: reassembly is a read-path change that stores nothing, and
entry records filed while this was live remain valid rows — they would simply serve
their first chunk again, which is the pre-change behaviour. Any such record should be
shortened or split before rolling back, and `am_list_drawers` on the entry room names
them.

## Follow-ups

- [ ] Re-check the palace's own `llm_init` records after this lands: any that were split to fit the refusal can be rejoined, and the ADR-045 follow-up already owes a sweep of records teaching the old limit.
