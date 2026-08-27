# ADR-015: A wing merge must correct the search index it invalidates

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-013 (a page of memories, not chunks — the same `Search` path), ADR-005 (deliverable handoffs — the misfiled `wing_to-*` inboxes this palace merged are two of the four drifted sources)
**Invalidates:** none — checked (grepped ADR-001..014 for `MergeWing`, `searchFilter`, `payload`: no accepted ADR consumes the merge path or the index payload)
**Served-path change:** A recall scoped to a wing returns memories that were merged into it. Today `Service.Search` passes the wing to the vector index as a filter, the index filters on a payload the merge never updated, and 13 of this palace's 359 memories are unreachable by a scoped search of the wing they are actually filed in.

## Context

Measured 2026-08-21 against the live self-hosted palace (359 drawers, 359 Qdrant points, 116 recorded recalls). Wing names below are neutral stand-ins for the real ones — a wing name is a project name, and this file is public — but the SHAPES are exact and are what the argument rests on: two of the four sources are `wing_to-<project>` inboxes, the misdelivery ADR-005 describes, which somebody then merged into the project's real wing.

- **13 points carry a wing their drawer no longer has.** Four sources: `wing_acme-legacy` (4), `wing_to-beta` (2), `wing_acme-old` (1), `wing_to-x` (1) and the rest of the same families. Every one is a wing that was merged into another. The drawer row was relabelled; the index payload was not.
- **The memories are invisible to the recall that matters.** Probed three of them through the live `/mcp` endpoint with a phrase from their own text: scoped to the wing they are filed in, none came back; scoped to `*`, all three did. Scoped recall is the DEFAULT (`SEARCH_SCOPE=wing`), so these are memories an agent cannot reach by asking about its own project.
- **The stale name still costs a pool slot.** `search_events` records a recall scoped to `wing_acme-legacy` — a wing holding nothing — that retrieved 4 candidates and returned 0 hits. The index matched the stale payload, the redundant drawer-row check dropped all four, and the caller paid 4 of a 10-candidate pool for nothing.

The defect is stated as a fact in the code that causes it. `MergeWing`'s doc comment says:

> Vectors are not re-written: their payload wing is advisory (search filters on the drawer row's wing), so a merge needs no re-embedding.

`Service.Search` calls `s.vectors.Search(ctx, teamID, vec, candidateK, searchFilter(q))`, and `searchFilter` puts the wing in the index filter. The payload is not advisory: it is the PRIMARY filter, and the drawer-row comparison that follows is a second pass that can only remove candidates, never add one back. This repository already holds a test (`TestDocumentedEnvVarsAreRead`) built on the premise that documentation is load-bearing; this is the same failure in a doc comment rather than a compose file.

`Service.Update` — the other path that changes a drawer's wing — re-embeds and re-upserts, so it does not drift. Only the bulk relabel does.

**Repaired 2026-08-21, before this ADR was executed.** The 13 points were corrected by hand in both
stores — a payload patch on the Qdrant collection and an `UPDATE vectors SET payload` on the SQLite
source of truth — and both now report zero disagreement with the drawer rows. Twelve of the thirteen
were then probed through the live `/mcp` endpoint and found by a search of their own wing; the
thirteenth is chunk 2 of a four-chunk memory whose other three chunks all return, so the memory is
reachable and the miss is the probe's ranking rather than drift.

That repair is the evidence for the mechanism this ADR ships: it is exactly a payload write and it
needed no embedding call.

**Correction, found while executing T2.** The first draft of this ADR said the repair "could not be
done with any command the product offers today". That is wrong: `agentsmemory sync --repair-payload`
rebuilds payloads from the drawer rows and would have fixed the INDEX. What is true is worse and
more specific — it writes only the index, so the source of truth keeps the stale label, and a plain
`agentsmemory sync` replays that stale payload straight back over the repair. The product shipped
two repair paths that undo each other, and the one an operator reaches for first is the one that
loses. Fixed here by routing the repair through the `Hybrid`, which writes both.

## Existing Primitives Audit

- **`store.VectorStore`** (`internal/store/store.go`) — has `Upsert`, `Search`, `Delete`, `EnsureNamespace`. It cannot CHANGE a point's payload through the SEAM without supplying the vector again. Reshape: `SetPayload` promoted onto the interface and implemented by all four backends.

- **`SourceOfTruth.PointsByIDs`** (`internal/store/store.go`) — reads points by id, payloads included, and already exists: it is the read half of copying memory between tenants without re-embedding. Reuse by PROMOTION to `VectorStore`. The first draft of this audit missed it and declared a second method that did the same thing; the gap is not that no by-id read exists but that only the DURABLE store had one, so a check could read the source of truth and never the index — and the index is the copy a scoped search actually filters on. `SourceOfTruth` keeps the stronger promise that the vector comes back too.
- **`store.SourceOfTruth.AllPoints`** (`internal/store/store.go`) — already enumerates stored points with vectors, for replaying SQLite into a search index. Reuse: it is what makes a repair of the existing drift possible without re-embedding anything.
- **`qdrant.Client.SetPayload`** (`internal/store/qdrant/qdrant.go`) — already patches a Qdrant payload without touching vectors, and its own comment says it exists "so a palace can be repaired for the cost of a few HTTP calls rather than a full re-embedding". Reuse by PROMOTION, widened from `map[string]any` to the seam's `map[string]string`. The second existing primitive this audit missed on the first pass; the lesson is to grep for the method NAME, not only for the concept.
- **`agentsmemory sync --repair-payload`** (`cmd/server/sync.go`) — already rebuilds payloads from the drawer rows, which is exactly this repair. It writes ONLY the index, so the source of truth keeps the stale label and a plain `sync` — which replays the source of truth into the index — puts it back. Two repair paths that undo each other, and the one that runs by default is the one that loses. Reshape: it now writes through the `Hybrid`, so both stores move together.
- **`Service.Update`** (`internal/palace/service.go`) — already does the right thing for a single drawer. Reuse as the reference behaviour, not as the mechanism: re-embedding 13 drawers to fix a label is the cost this ADR exists to avoid.

## Decision

**A relabel of a drawer's wing is not complete until every index that filters on wing agrees.** `MergeWing` corrects the payload of the affected points in the same operation, and the doc comment that asserts the opposite is deleted rather than softened.

The mechanism is a payload write, not a re-embed: `VectorStore` gains `SetPayload(ctx, namespace string, ids []string, patch map[string]string) error`, implemented by the qdrant, sqlite and chromem backends. A merge relabels the rows, then patches `wing` on exactly those ids.

**What would make this fail, and whether such data exists.** The claim is falsifiable in one command and the data to falsify it exists today: for every point in the index, compare its payload wing against its drawer's wing. The count must be zero after a merge. It is 13 right now, so the check has a known red state to be verified against before it is trusted. If a backend cannot patch a payload without the vector, the fallback is `Upsert` with the vector read from the SQLite source of truth — still no embedding call — and the decision stands; only the mechanism changes.

Valid for: any deployment whose search index filters on a payload copy of the wing, which is all three backends as shipped.

## Alternatives Considered

- **Drop the wing from the index filter and rely on the drawer-row check.** Rejected on the numbers: the candidate pool is 10–50 points, so an unfiltered fetch spends it on the whole workspace and a small wing's own memories never enter the pool. That converts a bug affecting 13 memories into one affecting every scoped recall.
- **Make the merge re-embed the relabelled drawers.** Rejected: it is an embedding call per drawer for a label change, unbounded in the size of the merged wing, and the vector is already correct — the text did not change.
- **Leave it and tell operators to run `sync` after a merge.** Rejected on evidence from this very palace: the merge tool's description ALREADY says "Run am_recompute_graph afterwards", that instruction was followed or not without anything checking, and the drift is here anyway. A repair an operator must remember is a repair that does not happen.
- **Make the drawer id independent of the wing so a merge is a no-op.** Rejected as out of proportion: `DrawerID` hashes the wing, so this would rewrite every id in the palace and invalidate every anchor, tunnel and KG source pointer. Recorded as a deferral, not a rejection of the idea.

## Component / Boundary Impact

`internal/store` owns how points are stored and filtered; it gains one method on an interface it already owns. `internal/palace` owns what a merge means and calls the new method. No boundary moves, and no new dependency appears in either direction.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `store.VectorStore.PointsByIDs` | move — promoted from `SourceOfTruth`, so the INDEX can be read too | `internal/store/{qdrant,sqlitevec,chromemvec}`, `store.Hybrid` | `internal/palace/indexdrift.go` |
| `store.VectorStore.SetPayload` | move — promoted from `qdrant.Client`, widened to `map[string]string` | `internal/store/{qdrant,sqlitevec,chromemvec}`, `store.Hybrid` | `internal/palace/admin.go`, `cmd/server/sync.go` |
| `sync --repair-payload` | change — writes through the `Hybrid` so the source of truth moves too, instead of leaving a repair a plain `sync` undoes | `cmd/server/sync.go` | operators |
| `store.Hybrid.Halves` | add — a checker must compare the two copies, not use one | `internal/store/hybrid.go` | `internal/palace/indexdrift.go` |
| `cmd/server.rootCommand` | add — the CLI's command list was built inside `main`, where nothing could assert what is registered | `cmd/server/main.go` | `cmd/server/doctor_test.go` |
| `MergeWing` doc comment asserting the payload is advisory | remove — it is false and it is why the bug exists | `internal/palace/admin.go` | every reader |
| `agentsmemory doctor --index` (a read-only drift report, exit 1 when the index disagrees with the rows) | add | `cmd/server` | operators, and this ADR's own acceptance |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `VectorStore.PointsByIDs` | T1 | T1 | Yes — a third-party VectorStore implementation would no longer satisfy the interface. All four implementations are in this repository. |
| the index-drift report (`doctor --index`) | T1 | T3 | No — additive and read-only, and it is T3's acceptance command |
| `VectorStore.SetPayload` | T2 | T3 | Yes — same interface, same reason. Separate from `PointsByIDs` so the reader lands without the writer, and T3 cannot be verified by a method it also calls to make the change. |

## Implementation

Three tasks: `tasks/README.md`.

## Consequences

- **Positive:** a memory merged into a wing is recallable from that wing. The 13 already adrift are repaired, and the check that finds them runs on demand instead of requiring somebody to think of it.
- **Negative:** `VectorStore` grows one method and inherits another from `SourceOfTruth`, so every implementation must provide them — including the in-memory fakes in tests.
- **Neutral:** a merge does one extra write per affected point. It is a payload patch, not an embedding, so it is bounded by the merge's own size and costs no model call.

## Out of Scope

- Making `DrawerID` independent of the wing so a merge does not invalidate anything derived from the id (deferred: docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md)
- The derived graph a merge also invalidates — hallways and entity tunnels (deferred: docs/adr/ADR-016-a-memory-an-agent-files-must-be-navigable.md — that ADR shows hallways cannot exist at all on an agent-populated palace, which has to be true before a merge can be said to invalidate them)
- Wing rename as a first-class operation distinct from merge (permanent: a rename is a merge into a wing that holds nothing, and a second spelling of one operation is a second thing to keep correct)
- Repairing indexes on deployments nobody runs `doctor` against (permanent: this ADR gives the operator a check and a fix; running it is theirs)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A backend's payload patch silently no-ops, and the merge reports success over an index it did not correct | Med | High | T1's drift report is T3's acceptance command, so the fix is verified by READING the index rather than by the write returning nil |
| The SQLite source-of-truth payload is stale too, so a later `sync` reintroduces the drift | Med | High | T1 reads BOTH stores, and T3 patches both; a repair that fixed only the index would show green in one and red in the other |
| A partial merge leaves rows relabelled and payloads not | Low | Med | The drift report makes the state visible and is idempotent to re-run; the repair is a payload write, so re-running it costs nothing |

## Rollback

The interface method and the merge's extra write can be reverted together; a payload already corrected stays corrected, which is the desired state either way. No schema change, no re-embedding, nothing to migrate back. The `doctor` subcommand is read-only and can be dropped independently.

## Follow-ups
