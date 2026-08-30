# Task ADR-038-T2: Store what the id used to promise, on every path that mints or moves a drawer

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — schema + every write path)
**Owner:** unassigned
**Produces:** `drawers.content_key` column + unique index `(team_id, content_key)` scoped to CURRENT rows; `Drawer.ContentKey` field; `DrawerID` re-documented as the content-key recipe
**Consumes:** `drawers.valid_to` (T1) — the index predicate's second conjunct
**Data dependency:** hermetic — the migration and its tests run from an empty database. The backfill was SIZED against the live corpus (1,705 non-diary rows, 0 collisions, measured 2026-08-27), but nothing in this task requires that corpus to run.

## Goal

Every drawer row carries the hash of what it currently holds, in its own column, written by every
path that mints a drawer and recomputed by every path that changes a hashed field.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/000NN_drawers_content_key.sql` | add | the column and the unique index. **NN is allocated at merge, never at authoring** — a renumber at merge re-runs on a database that already applied it (`README.md`, Development) |
| `db/migrations/00006_drawers.sql` | edit | line 18's comment on `id` reads `deterministic hash(team,wing,room,source,chunk) — idempotency key`. It is **factually wrong** — `DrawerID` hashes **content** too — and the phrase `idempotency key` names the primary key by its dedup job, which is the conflation this ADR ends. Safe to edit despite being applied: goose keys on version so the statement never re-runs, and a **fresh install runs this file**, so leaving it stale ships a new database whose schema lies about itself |
| `internal/palace/palace.go` | edit | `Drawer` gains `ContentKey string` with its gorm tag — the field the column maps to |
| `internal/palace/chunk.go` | edit | `DrawerID`'s doc comment stops calling it the identity of a drawer and calls it the content key; body unchanged |
| `internal/palace/palace.go` | edit | `:19`, the `Drawer` struct's own ID field: *"a deterministic hash of (team, wing, room, source, chunkIndex)"*. The TYPE DEFINITION's comment, the most-read one, and **already wrong today** — it omits content, the same error as `00006:18`. Found by sweeping the class rather than waiting to be told |
| `internal/palace/service.go` | edit | `:677` — *"a standalone memory (deduped by its content-hash id)"*, the exact sentence describing the mechanism T3 replaces |
| `internal/palace/service.go` | edit | `Add` (`:660`) and `WriteDiary` (`:2054`) — the mint sites. Diary sets an EMPTY key; that is the line that SELECTS a journal out of dedup |
| `internal/palace/import.go` | edit | `AbsorbDrawers` (`:82`) mints the key |
| `internal/palace/mine.go` | edit | `Mine` (`:155`) mints the key |
| `internal/palace/copywing.go` | edit | `CopyWing` (`:130`) mints the key for the TARGET team, not the source |
| `internal/palace/repo.go` | edit | `Update` (`:380`) recomputes the key in the same `updates` map that changes content/wing/room, and turns a key collision into a NAMED error saying which drawer already holds that content — the same treatment `admin.go`'s row demands, because an in-place wing/room move stays in-place forever and is the one path T4's supersede never covers |
| `internal/palace/admin.go` | edit | `RelabelDrawerWingReturningIDs` (`:295`) and `RelabelDrawerWing` (`:313`,`:324`,`:342`) recompute the key in the same statement that moves the wing — this is the line whose absence would leave a merged drawer describing a wing it no longer sits in. It must also turn a key collision into a NAMED error saying which drawer in the target already holds that content, not a bare constraint violation |
| `internal/palace/contentkey_test.go` | add | the failing tests |

## Ordered Steps

1. Write the failing tests first. They must be RED because `Drawer` has no `ContentKey` field, so
   they do not compile — the strongest red available:
   - a drawer filed by `Add` carries `ContentKey == DrawerID(team, wing, room, source, idx, content)`;
   - after `Update` rewrites the content, the key equals the hash of the NEW content;
   - after `MergeWing` moves a drawer, the key equals the hash computed with the TARGET wing;
   - two diary entries with identical text, agent and topic both persist, and both carry an EMPTY key;
   - merging a wing into a target that already holds an identical drawer fails with an error naming
     the colliding drawer, and leaves both wings unchanged (ADR-015 already fails the whole merge on
     any failure). Measured 2026-08-27: 0 such tuples exist today, so this test constructs the case.
2. Add the migration: `ALTER TABLE drawers ADD COLUMN content_key TEXT NOT NULL DEFAULT ''`, then a
   backfill `UPDATE` computing nothing (SQLite cannot SHA-256) — so the backfill is a Go step, see 3.
   Then `CREATE UNIQUE INDEX ... ON drawers(team_id, content_key) WHERE content_key != '' AND valid_to = ''`.
   **Both conjuncts are load-bearing and each fails differently.** `content_key != ''` keeps diary
   rows and any un-backfilled row out of the index; without it they share one entry and an upsert
   overwrites an unrelated memory. `valid_to = ''` scopes uniqueness to CURRENT rows; without it a
   superseded row keeps competing for content it no longer asserts, and text that was once
   superseded could never be filed again. T1 must land first for the second conjunct to be
   writable at all — that ordering is the only reason this task is not first.
3. Add the backfill as a startup repair that **re-runs until it completes**, and aborts on the first
   collision rather than skipping the row. "Runs once" is the wrong contract and the record used to
   say it: goose records the migration version the first time the SQL runs, so the SQL never runs
   again — a backfill that aborted halfway would therefore never resume. Gate it on work remaining
   (rows in a non-diary room with an empty `content_key`), not on the goose version, so an aborted
   run is retried on the next boot and a completed one costs one cheap count. A silent partial
   backfill is the failure shape this repo keeps catching; a failed migration is recoverable, a
   half-done one is invisible.
4. Add `ContentKey` to `Drawer` and write it at all five mint sites and both mutation sites.
4b. Correct `00006_drawers.sql:18` and give the new `content_key` column a comment that names its
   job, so the two roles are legible in the schema rather than only in this record.
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestAddStampsTheContentKey|TestUpdateRecomputesTheContentKey|TestMergeWingRecomputesTheContentKey|TestTwoIdenticalDiaryEntriesBothPersistWithNoContentKey|TestTheContentKeyIndexIsPartialOnBothConjuncts|TestAnEndedRowDoesNotBlockRefilingItsOwnText|TestBackfillAbortsOnCollision' -count=1 2>&1 | tee /tmp/acc38a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38a.out && go test ./internal/palace/ ./internal/store/ ./cmd/server/ -count=1 2>&1 | tee /tmp/acc38b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38b.out
```

All seven new tests run ALONE first, so the already-green palace suite in the second command cannot
carry the verdict by itself. `no test files` is in the guard because a `-run` filter that matches
nothing and a package with no tests both exit 0.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAddStampsTheContentKey` | `internal/palace/contentkey_test.go` | the mint path writes the key | — |
| `TestUpdateRecomputesTheContentKey` | `internal/palace/contentkey_test.go` | an in-place content edit updates the key | — |
| `TestMergeWingRecomputesTheContentKey` | `internal/palace/contentkey_test.go` | a wing move updates the key — the path that is easiest to forget | — |
| `TestTwoIdenticalDiaryEntriesBothPersistWithNoContentKey` | `internal/palace/contentkey_test.go` | a journal is not deduped, and the partial index is what allows it | — |
| `TestBackfillAbortsOnCollision` | `internal/palace/contentkey_test.go` | a colliding corpus fails the migration rather than skipping a row | — |
| `TestTheContentKeyIndexIsPartialOnBothConjuncts` | `internal/palace/contentkey_test.go` | reads the real index definition via `pragma_index_list`/`sql` and fails when EITHER conjunct is absent. Two mutants, one per conjunct — **`content_key != ''` is the clause whose loss destroys data; `valid_to = ''` is the clause whose loss makes a re-file impossible forever** | — |
| `TestAnEndedRowDoesNotBlockRefilingItsOwnText` | `internal/palace/contentkey_test.go` | supersede a drawer, then file its original text again: it must succeed. Red without the `valid_to = ''` conjunct — the interaction that only became visible when ADR-010 was absorbed | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the four unit tests above |
| 2 — something selects it | every mint/mutation site writes the key; mutation: delete the write in `RelabelDrawerWing` and `TestMergeWingRecomputesTheContentKey` goes red |
| 3 — the caller can discover it | n/a: no declared interface — the column is internal, no tool argument or response field changes in this task |
| 4 — it is used | **T3** is the consumer. Until T3 lands the column is written and read by nothing, which is deliberate and is why T3 is not optional. |

## What execution found that the task did not predict

**1. `Save`'s `UpdateAll: true` would have resurrected ended memories.** Re-filing the exact text of
a drawer ended by T1 mints the same id, and `UpdateAll` writes every column — resetting `valid_to`,
`ended_at` and `ended_reason` to their zero values and silently undoing a retraction somebody
decided. The conflict clause is now an explicit column list; the validity columns are owned by
`EndDrawer` alone and no filing path writes them. This is a T1×T2 interaction neither task named.

**2. The diary exemption had two mechanisms and only one was live.** The mint hardcoded
`ContentKey: ""` *and* `contentKeyFor` special-cased `DiaryRoom`, so a mutant deleting the exemption
**survived** — measured, the fence passed with the mechanism broken. Collapsed to one: the mint calls
`contentKeyFor`. Recorded because "two ways to do it" reads as belt-and-braces and is actually a dead
branch nothing can test.

**3. `BackfillContentKeys` could loop forever.** Its exit condition is "no rows left to key", so a row
that can never be keyed is re-selected every pass. Surfaced by the diary mutant, which turned the
backfill from failing into **hanging** — and a hang and a pass are indistinguishable from a
timed-out gate. It now counts progress per batch and errors, naming the first stuck row, rather than
spinning.

**4. `TestAnEndedRowDoesNotBlockRefilingItsOwnText` is scoped to a SOURCE-LESS drawer.** With a named
source, `purgeSource` still hard-deletes the whole source before re-inserting, so the test would have
been measuring T3's job. Here the question is only whether the unique index blocks the re-file and
whether `Save` resurrects the ended row.

## Mutation Log

- 2026-08-27 · 223600a* · mutant killed · exit 1 · `db/migrations/00031_drawers_content_key.sql` · drop the content_key != '' conjunct — every keyless row shares one index entry and an upsert overwrites an unrelated memory · acceptance-sha256:bc0c2f2e8cc7e69166a4dc2536b794491a747368e1a138c19f894e4f398cf7e6
- 2026-08-27 · 223600a* · mutant killed · exit 1 · `db/migrations/00031_drawers_content_key.sql` · drop the valid_to = '' conjunct — a superseded row keeps competing, so text once superseded can never be filed again · acceptance-sha256:bc0c2f2e8cc7e69166a4dc2536b794491a747368e1a138c19f894e4f398cf7e6
- 2026-08-27 · 223600a* · mutant survived · exit 0 · `internal/palace/contentkey.go` · diary rows stop being exempt, so a journal starts deduping · acceptance-sha256:bc0c2f2e8cc7e69166a4dc2536b794491a747368e1a138c19f894e4f398cf7e6
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-27 · 223600a* · mutant killed · exit 1 · `internal/palace/repo.go` · Save writes the validity columns again, so re-filing an ended row resurrects it · acceptance-sha256:bc0c2f2e8cc7e69166a4dc2536b794491a747368e1a138c19f894e4f398cf7e6
- 2026-08-27 · 223600a* · mutant killed · exit 1 · `internal/palace/admin.go` · a wing move stops carrying the content key, leaving it describing a wing the row left · acceptance-sha256:bc0c2f2e8cc7e69166a4dc2536b794491a747368e1a138c19f894e4f398cf7e6

## Invariants

- No drawer id changes. Anything that would re-key a row belongs to a different decision.
- The vector store is not written during the migration — there is no cross-store transaction to get wrong.
- Diary rows never enter the unique index, and the partial predicate — not a convention — is what keeps them out.
- Uniqueness is a property of CURRENT rows only. An ended row is history, and history does not compete for a name.
- Every failure mode of this task ends in a duplicate row, never in an overwritten one. The partial predicate is the whole reason that is true.

## Risks

- A mint path added between authoring and execution silently misses the key. T6's derived gate is the answer; until it lands, the Affected Files table is the list, and it was taken from `grep -n "DrawerID(" --include="*.go"` on 2026-08-27.
- `NOT NULL DEFAULT ''` on a large table rewrites it on some SQLite versions. 2,013 rows on the live corpus; trivial, but confirm on the real database before merging rather than on a fixture.

## Pre-flight against the hosted deployment — read-only, run BEFORE merging the migration

Every number in the parent ADR is from one local palace. Take the same three against hosted and
record them in the sign-off; each is a single read-only query:

1. non-diary row count, and distinct content keys among them (the collision premise);
2. anchors, and how many sit under a named source (the exposure the purge change repairs);
3. tuples of `(team_id, room, source_file, chunk_index, content)` spanning more than one wing (the
   merge-collision premise).

If (1) shows a row count large enough that an O(n) SHA-256 pass at boot is not free, the backfill
becomes a bounded background repair instead of an inline migration step. That is a decision to take
with the number in hand, not a default to guess at.

## Stop Condition

Stop and ask if the backfill finds any collision on the real corpus. Measured 0 on 2026-08-27, so a
non-zero count means something changed and the decision's cheap-rollback premise needs re-checking.

**What would make this criterion impossible to fail?** A backfill that skips colliding rows instead
of aborting. That is why step 3 says abort — a skip makes the check unfalsifiable.

## Out of Scope

- Reading the key for dedup — that is **T3**'s job.
- Repairing the 27 drifted rows (deferred: `docs/adr/BACKLOG.md`)
- The validity window itself — that is T1, and this task only consumes its column.

## Verification Log
- 2026-08-27 · 223600a* · exit 0 · `go test ./internal/palace/ -run 'TestAddStampsTheContentKey|TestUpdateRecomputesTheContentKey|TestMergeWingRecomputesTheContentKey|TestTwoIdenticalDiaryEntriesBothPersistWithNoContentKey|TestTheContentKeyIndexIsPartialOnBothConjuncts|TestAnEndedRowDoesNotBlockRefilingItsOwnText|TestBackfillAbortsOnCollision' -count=1 2>&1 | tee /tmp/acc38a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38a.out && go test ./internal/palace/ ./internal/store/ ./cmd/server/ -count=1 2>&1 | tee /tmp/acc38b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38b.out` · acceptance-sha256:bc0c2f2e8cc7e69166a4dc2536b794491a747368e1a138c19f894e4f398cf7e6
