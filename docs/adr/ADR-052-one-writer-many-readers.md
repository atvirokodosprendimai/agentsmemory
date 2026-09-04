# ADR-052: One writer, many readers — make the writer count a decision

**Status:** Proposed
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md`, `docs/adr/ADR-045-move-a-memory-not-a-row.md`, `docs/adr/ADR-006-knobs-that-do-nothing.md`, `docs/adr/BACKLOG.md`
**Governs:** `cmd/server/main.go`, `internal/palace/repo.go`, `internal/palace/service.go`, `internal/palace/admin.go`, `internal/mcptest/harness.go`
**Enforced-by:** `cmd/server/dbwiring_test.go::TestTheReadHandleCannotWrite`
**Invalidates:** none — checked
**Served-path change:** a wing merge, a whole-memory relocation and a membership change stop failing with `database is locked` when two agents write at once, and the read path can no longer write through the handle it was given.

## Context

This project's SQLite configuration was decided once, for a different question.
`dbPragmas` (`cmd/server/main.go:1707`) was added on 2026-08-17 to let `inspect`
read a live server, and its own comment says so: *"This is a concurrency
*performance* setting and nothing more."* Nothing since has asked how many
writers the serving path actually has.

The answer, read from source 2026-09-04 at `732b727`, is that nobody chose.
`grep -rn SetMaxOpenConns --include='*.go'` returns four hits and every one is a
test — `internal/tenant/local_test.go:35`, `internal/web/billing_gate_test.go:41`,
`internal/web/wingimport_authz_test.go:39`, `internal/billing/service_test.go:45`.
No non-test path sets a pool limit at all, so serving runs on `database/sql`'s
default: unbounded open connections, every one of them a separate SQLite
connection that may write. There is no writer goroutine, no queue, no mutex on
the write path, and no retry on `SQLITE_BUSY` — the only mention of "database is
locked" in the tree is the comment at `cmd/server/main.go:1695` explaining why
`busy_timeout(5000)` was chosen, which is the passage this record invalidates.
One `*gorm.DB` built at `cmd/server/main.go:1116` is shared by every package
that owns SQL.

★**And what those four tests set is `SetMaxOpenConns(1)`** — one of the two
configurations the table below shows passing 320 of 320. **They cannot reproduce
this defect by construction**: they are pinned to the configuration that works
while serving runs the one that fails. That is a sharper instance of the
harness finding below than the DSN difference is, and it arrives with a citation
rather than an argument.

**That default is load-bearing, and it fails.** SQLite grants a deferred
transaction's write lock on its first write statement. A transaction that reads
first must *upgrade*, and on an upgrade conflict SQLite returns `SQLITE_BUSY`
immediately rather than invoking the busy handler. In WAL that covers **two**
distinct cases, and this record no longer treats them as one: the writer lock is
held by someone else, and the reader's snapshot is stale so no amount of waiting
could make it writable. Confirmed in the pinned engine at
`modernc.org/sqlite@v1.49.1/lib/sqlite_darwin_arm64.go:50553-50565`, with the WAL
BUSY/BUSY_SNAPSHOT paths at 45693-45731.
So `busy_timeout(5000)` does not cover the case, and 6 of the 16
non-test `Transaction(` sites read before they write:
non-test `Transaction(` sites read before they write:

| Site | What it does |
|------|--------------|
| `internal/palace/admin.go:327` | wing merge — `Pluck` the drawer ids, then `Update("wing")` |
| `internal/palace/admin.go:363` | the same for closets |
| `internal/palace/service.go:1444` | ADR-045 relocation — the read is inside `Repo.Update` (`repo.go:491`) and invisible at the call site |
| `internal/tenant/tenant.go:476` | add a member — in-transaction re-check, then `Create` |
| `internal/tenant/tenant.go:514` | change a role — `First`, `countAdmins`, then `Update` |
| `internal/tenant/tenant.go:547` | remove a member — read, then revoke keys |

Measured 2026-09-04 against `github.com/glebarez/sqlite@v1.11.0` on the pinned
module graph, 8 goroutines × 40 transactions = 320, read-then-write in one
transaction, temp database per run:

| Configuration | Failed |
|---------------|--------|
| shipped DSN, unbounded pool (today) | 273–281 of 320, five runs, all `database is locked` |
| shipped DSN + `_txlock=immediate` | 0 of 320 |
| shipped DSN, `MaxOpenConns=1` | 0 of 320 |
| shipped DSN, plain inserts, unbounded pool | 0 of 320, 61ms |
| shipped DSN, plain inserts, `MaxOpenConns=1` | 0 of 320, 30ms |

Two further facts fell out of the same run. **Foreign keys are off**: `PRAGMA
foreign_keys` returns `0` under the shipped DSN and `1` when
`_pragma=foreign_keys(1)` is added, so the five `ON DELETE CASCADE` clauses in
`00001_init.sql` are decoration. **A `query_only` handle really refuses a
write**: reads succeed, and a write returns `attempt to write a readonly
database (8)`.

**And the tests measure a different database than the one we ship.**
`internal/mcptest/harness.go:416` opens with no pragmas at all, so it runs in
`journal_mode=delete` where serving runs WAL. The same read-then-write shape
failed 79 of 320 there against 273-281 under the shipped DSN, so a concurrency
test written in the harness under-reports the defect by roughly a factor of
three.

⚠**It is the JOURNAL MODE that differs, not the busy timeout, and an earlier
draft of this record said otherwise.** `docs/adr/BACKLOG.md:1065` states the
harness runs with `busy_timeout` at 0; that is false and this record repeated it
before checking. The pinned driver issues `pragma BUSY_TIMEOUT(5000)`
unconditionally on every connection BEFORE it applies any `_pragma` from the DSN
(`glebarez/go-sqlite@v1.21.2/sqlite.go:879-880`), and a probe agrees: a
pragma-free handle reports `busy_timeout=5000 journal_mode=delete
foreign_keys=0`. The 79/320 measurement stands; the explanation for it changes,
and the backlog entry needs the same correction.

The one thing this ADR does NOT need to add is the race detector. `-race` runs
in CI already, as its own required job (`.github/workflows/build.yml:148`,
`go test -race -timeout=30m ./...`), added 2026-08-30 in `11c7176`. ADR-042's
follow-up asking for it is stale, and an earlier draft of this record trusted
that follow-up over the workflow and proposed adding what already exists.

## Existing Primitives Audit

- **`inspectionDBPragmas` (`cmd/server/main.go:1712`)** — already proves the technique this ADR generalises: `query_only(1)` enforces a no-write boundary at SQLite itself for `doctor`. **Reshape** — the constant stays for `doctor`; the read handle is a sibling built the same way, not a reuse of this one, because `doctor` also omits `journal_mode` deliberately.
- **`openDBWithPragmas` (`cmd/server/main.go:1675`)** — the single opener every path already funnels through. **Reuse** unchanged; this ADR adds callers and pool configuration around it, not a second opener.
- **`lockDB` (`cmd/server/lock.go:54`)** — the single-instance file lock. **Reuse, and explicitly not extended**: it guards the database against a second `serve` *process* and says nothing about goroutines inside one server. It is the reason this ADR is about in-process writers only.
- **`keyedMutex` (`internal/palace/mine.go:19`)** — the only single-writer-shaped construct in the tree, scoped to one mining source. **Reuse** as-is; it is not a general write lock and is not being promoted into one.
- **`clause.OnConflict` upserts and the guarded update at `internal/palace/supersede.go:225`** — the existing optimistic-concurrency answers. **Reuse**: they handle *logical* races between two corrections; this ADR handles the *physical* lock, and neither substitutes for the other.

## Decision

Split the one shared `*gorm.DB` into a **writer handle** and a **reader
handle**, and make the writer count explicit at both ends.

The writer handle opens with `_txlock=immediate` and `SetMaxOpenConns(1)`.

**The DSN knob is what fixes correctness** — measured 0 of 320 — by making every
transaction take its write lock at `BEGIN`, so there is no upgrade to deadlock on
and `busy_timeout(5000)` covers the wait that replaces it.

⚠**The pool cap is NOT what serialises writing, and an earlier draft implied it
was.** `SetMaxOpenConns` binds one `*sql.DB`, and `serve`, `mcp` and `sync` each
open their own writer-capable handle; only `serve` takes the file lock. Measured
2026-09-04, eight independent handles on one file each capped at 1: **284 of 320
failed without `_txlock`, 0 of 320 with it.** So the cap does not serialise
across handles — SQLite and `_txlock` do. What the cap buys is that the writer
count becomes a stated decision rather than an unbounded default, and it costs
nothing measurable (30ms against 61ms on plain inserts). "One writer per
aggregate" describes the intent, not what this one line enforces.

The reader handle opens with `query_only(1)` and a pool of
`max(4, runtime.NumCPU())`. `query_only` turns the read/write split into
something SQLite enforces rather than something prose asks for — the `cqrs`
skill's "a read model is only ever handed a read-only port", with the compiler's
job done by the driver. ⚠It does not "finally spend WAL": the existing unbounded
pool and the separate `inspect` and export connections already use WAL
concurrency (`cmd/server/main.go:1689`). What is new is the enforcement, not the
concurrency.

Both handles get `foreign_keys(1)`, which is orthogonal to contention (268 of
320 still failed with it alone) and closes a separate silent defect. It is the
finding to lead with in any summary of this record: it needs no concurrency to
bite, and it is silently already true of every row in every deployment.

⚠**T2 and T4/T5 are not two halves of one fix, and the record should not read as
though they were.** `_txlock=immediate` alone takes the failure from 273–281 to
0 of 320, so **T2 fixes the bug**. T4 and T5 buy something else: connection-level
enforcement that a read path *cannot* write, which makes a class of future defect
unrepresentable rather than fixing a present one. If the owner wants the smaller
record, T1–T3 stand alone and T4–T6 can be split out; they are sequenced together
here because the reader handle is what keeps `MaxOpenConns(1)` on the writer from
serialising recall, and that coupling is real.

**What would make this fail, and the data exists today.** The criterion is T1's
test: eight INDEPENDENT writer handles on one file, 40 read-then-write
transactions each, must reach 0 failures. ⚠**The handle count is the whole
design and an earlier draft got it wrong.** A single handle capped at 1 scores
0 of 320 *whether or not* the DSN carries `_txlock`, so a one-handle test cannot
tell the knob from its absence and would go green with the fix deleted. Measured
2026-09-04: one handle capped at 1 gives 0/320 both ways; eight independent
handles capped at 1 give 284/320 without `_txlock` and 0/320 with it. The
criterion is valid for `glebarez/sqlite@v1.11.0` on this module graph and for
SQLite's deferred-transaction semantics; it is not a claim about a different
driver, and T2 pins the driver version in the test's own comment for that reason.

Routing reads onto the reader handle is staged: `internal/palace` in T5, every
other SQL-owning package deferred with a receipt, because threading a second
handle through eleven packages in one change is a rewrite wearing a refactor's
clothes.

## Alternatives Considered

- **`_txlock=immediate` alone, with no pool cap and no handle split:** change one constant and stop. Rejected as the RECORD's scope rather than as a bad idea — it is the measured 0-of-320 fix and it is what T2 ships, so anyone wanting the minimal change should take T1–T3 and leave the rest. It is listed here because the earlier draft left it implicit, and an alternative that is actually the core of the chosen option belongs on the page where a reader can weigh it.
- **`SetMaxOpenConns(1)` on the single shared handle, and nothing else:** cap the existing pool at one connection. Rejected because it serialises *reads* as well as writes, which throws away the reader/writer concurrency WAL was turned on for in the first place — this palace's dominant workload is recall, and a single connection would queue every search behind every write.
- **A Go-side write mutex or an actor goroutine owning the database:** serialise writes in application code. Rejected because it is a second lock over a resource that already has one — SQLite's — and it cannot see the writes that arrive from `inspect`, `sync`, `mcp` or a second process, so it would enforce an invariant only for callers that happened to route through it.
- **Retry on `SQLITE_BUSY` with backoff:** wrap every write in a retry loop. Rejected because `internal/palace/contentkey.go:106` already records that this driver wraps errors so detection is string matching, and because a retry converts a deterministic lock-ordering fix into a probabilistic one — `_txlock=immediate` removes the failure rather than re-running it.
- **`BEGIN IMMEDIATE` per read-first call site:** fix the six sites individually. Rejected because it is a list kept beside the truth: the seventh site added tomorrow is not on it, and `internal/palace/service.go:1444` shows the read can be hidden inside a helper and invisible at the call site.
- **Leave it and document the contention:** treat `database is locked` as an operating note. Rejected because it is already documented — `main.go:1694` explains `busy_timeout` — and the documentation is what made the gap invisible, since it describes a mechanism that does not cover the failing case.

## Component / Boundary Impact

`cmd/server` gains ownership of *which* handle each component receives; that is
the composition root and it already builds the one handle. `internal/palace`
changes from taking a `*gorm.DB` to taking a reader and a writer, so its one
reason to change stays "how the palace stores memories" — the choice of handle
is made above it. `internal/mcptest` moves from opening its own database to
opening the shipped one. No package boundary moves and no module is added, so
the module map in `README.md` is unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `writerDBPragmas`, `readerDBPragmas` | new constants; `dbPragmas` becomes the shared base | `cmd/server/main.go` | `internal/mcptest/harness.go`, `cmd/server/sync.go` |
| `openWriterDB`, `openReaderDB` | new openers wrapping `openDBWithPragmas` with pool configuration | `cmd/server/main.go` | `cmd/server` serve path |
| `palace.NewRepo` | signature takes a reader and a writer handle rather than one `*gorm.DB` | `internal/palace/repo.go` | `cmd/server/main.go`, `internal/palace` tests |
| `PRAGMA foreign_keys` | `0` to `1` on every serving connection | `cmd/server/main.go` | every package with a declared FK |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `TestAReadThenWriteTransactionSurvivesConcurrentWriters` (T1) | T1 | T2 | No — T2 turns it green |
| `writerDBPragmas` (T2) | T2 | T3, T4 | Yes — `dbPragmas` stops being the only constant, and `harness.go` must adopt one |
| `openReaderDB` (T4) | T4 | T5, T6 | No — additive |
| `palace.NewRepo` (T5) | T5 | T6 | Yes — every caller and test constructing a `Repo` changes |

## Implementation

See `docs/adr/ADR-052-one-writer-many-readers/tasks/README.md`. Six tasks in
five waves.

## Consequences

- **Positive:** the six read-first transactions stop failing under concurrency, measured from 273–281 failures to 0. Declared foreign keys start being enforced. The read path cannot write through the handle it holds, and that is checked by the driver rather than by review. The test harness exercises the database we ship.
- **Positive:** the writer count becomes greppable. `SetMaxOpenConns(1)` with a comment naming this ADR answers "how many writers does this have" in one line, where today the answer is the absence of a call.
- **Negative:** `_txlock=immediate` makes every transaction on the writer handle take a write lock at `BEGIN`, including one that only reads. Read-only work must go to the reader handle to stay concurrent, which is a rule a future caller can get wrong — T6's gate covers the wiring, not every future call site.
- **Negative:** `palace.NewRepo`'s signature change touches every test that builds a `Repo`. That is a wide, mechanical diff, and it is the reason T5 is scoped to one package.
- **Neutral:** turning foreign keys on can surface a latent ordering bug in a multi-row write that has been passing because nothing enforced the constraint. T2's acceptance runs `go test ./...` for exactly this reason, and a failure there is a real defect this ADR found rather than one it caused. ⚠It does NOT validate rows that already exist — the pragma changes subsequent writes and activates cascades, so T2's Stop Condition requires a `PRAGMA foreign_key_check` against every deployment corpus before the change ships, with a stated response to any violation.

## Out of Scope

- Routing reads onto the reader handle in the nine remaining SQL-owning packages (deferred: `docs/adr/BACKLOG.md`)
- Retry or backoff on `SQLITE_BUSY` anywhere in the tree (permanent: boundary: `_txlock=immediate` removes the failing case rather than re-running it, and a retry would hide a lock-ordering defect behind a success)
- Converting any aggregate to event sourcing, or introducing a bus, NATS or SSE fan-out (permanent: boundary: this repository is not CQRS-shaped and a half-migration is worse than either whole, so only the single-writer invariant travels)
- Cross-process write coordination beyond the existing file lock (permanent: fact: `lockDB` already refuses a second `serve` on one database, incumbent-wins; citation: file `cmd/server/lock.go:54`)
- Changing the pragma-free archive connection in the data export (permanent: fact: WAL would leave committed rows in a `-wal` sidecar that does not travel with the downloaded file; citation: file `cmd/server/main.go:1699`)
- Enforcing foreign keys in the data-export archive, which explicitly turns them off (permanent: fact: the archive disables them to copy tables in an order the constraints would refuse; citation: file `internal/dataexport/dataexport.go:187`)
- A parity gate between the CLI `mcp` adapter and the HTTP one (deferred: `docs/adr/BACKLOG.md`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| `foreign_keys(1)` breaks a write that has been passing unenforced | Med | High | T2's acceptance is the whole suite, not the new test alone; a failure is triaged as a found defect and either fixed in T2 or the ADR stops at its Stop Condition |
| `_txlock=immediate` is honoured by this driver version and silently dropped by a future one | Low | High | T1's test fails on exactly that regression, and T2 pins the driver version in the test's comment so a version bump reads as relevant |
| `query_only(1)` refuses something the read path legitimately does — a temp table, a `PRAGMA optimize` | Med | Med | T4 wires the handle and runs the full suite before T5 routes anything onto it, so the blast radius is one commit and the rollback is one line |
| `SetMaxOpenConns(1)` on the writer serialises a slow write behind others under real load | Med | Med | Measured cheaper than contention on this workload (30ms against 61ms), and the reader handle keeps recall off the writer entirely; revisit with a served-path measurement rather than by raising the cap |
| The wide `NewRepo` signature change collides with in-flight ADR PRs touching `internal/palace` | High | Low | T5 is the last substantive wave, and the Stop Condition requires rebasing onto main before it runs |

## Rollback

Persistent state changes shape only in that `foreign_keys` starts being
enforced; no migration runs and no row is rewritten, so a revert needs no data
repair. Undo in three steps, each independently safe: revert `palace.NewRepo`
to a single handle (T5), drop the reader handle from the composition root (T4),
and restore `dbPragmas` as the only DSN constant (T2, T3). A database that has
been written by the new configuration is byte-compatible with the old one —
WAL was already on, and `foreign_keys` is a connection property that leaves no
trace in the file. The one asymmetry: rows written while foreign keys were
enforced satisfy constraints the old configuration did not check, which is a
superset, so rolling back cannot orphan anything that was not already orphaned.

## Follow-ups

- [ ] Route reads onto the reader handle in the nine remaining SQL-owning packages, tracked in `docs/adr/BACKLOG.md` under this record's name
- [ ] Decide whether the reader pool size should be a flag rather than `max(4, NumCPU())`, once a served-path measurement exists — ADR-006 says a knob must change something, and nothing measures read concurrency today
- [ ] Re-take the contention measurement against the hosted deployment rather than a temp file, since a network filesystem changes SQLite's locking behaviour
