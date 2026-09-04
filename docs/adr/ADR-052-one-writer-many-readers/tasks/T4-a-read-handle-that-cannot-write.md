# Task ADR-052-T4: A read handle the read path cannot write through

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `openReaderDB`
**Consumes:** `readerDBPragmas` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `query_only being refused at the driver rather than in Go`

## Goal

Build and wire a second handle that SQLite itself refuses writes on, and prove
the refusal is real rather than a naming convention.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/main.go` | edit | add `openReaderDB` using `readerDBPragmas` and `SetMaxOpenConns(max(4, runtime.NumCPU()))`; open it beside the writer in the serve path and hold both |
| `cmd/server/dbwiring_test.go` | add | the gate proving the read handle refuses a write, and that the serve path builds one |

## Ordered Steps

1. [S1] Write `TestTheReadHandleCannotWrite` first and watch it fail to compile, because `openReaderDB` does not exist (TDD red).
2. [S2] Add `openReaderDB(path, debug)` wrapping `openDBWithPragmas` with `readerDBPragmas`, setting `SetMaxOpenConns` to `max(4, runtime.NumCPU())`. Doc-comment it with why the pool is many where the writer's is one, and cite ADR-052.
3. [S3] Open the reader in the serve path beside the writer and close both on shutdown. Nothing consumes it yet — T5 does — so this step is wiring only, and the point is that the handle exists at the composition root where the choice belongs.
4. [S4] Assert in the test that a write through the reader returns an error mentioning a readonly database, and that a read through it succeeds. Assert both, because a handle that refuses everything would also pass a refusal-only check.
5. [S5] Run the whole `cmd/server` suite before anything is routed onto the reader, so a `query_only` incompatibility surfaces while the blast radius is one commit. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./cmd/server/ -run 'TestTheReadHandleCannotWrite$|TestTheServePathOpensBothHandles$' -count=1 2>&1 | tee /tmp/adr052-t4a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t4a.out \
  && go test ./cmd/server/ -count=1 2>&1 | tee /tmp/adr052-t4b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t4b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheReadHandleCannotWrite` | `cmd/server/dbwiring_test.go` | a read succeeds and a write is refused by the driver through a handle from `openReaderDB` | — | S1, S2, S4 |
| `TestTheServePathOpensBothHandles` | `cmd/server/dbwiring_test.go` | the serve path constructs a reader as well as a writer, and closes both | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestTheReadHandleCannotWrite` |
| 2 — something selects it | the serve path calls `openReaderDB`; `TestTheServePathOpensBothHandles` goes red when that call is deleted, which is the mutation to run |
| 3 — the caller can discover it | `openReaderDB`'s doc comment names ADR-052 and states the rule that read-only work belongs on this handle |
| 4 — it is used | nothing consumes it until T5; that is the honest answer and it is why T5 exists |

## Mutation Log

## Invariants

- `readerDBPragmas` carries `query_only(1)` and never `_txlock=immediate`.
- The reader and the writer point at the same database file; this is a connection split, not a replica.
- `doctor`'s `openInspectionDB` is untouched.

## Risks

- `query_only` may refuse something gorm does on a read that is not obviously a write — a temp table for a complex join, or `PRAGMA optimize` on close. S5 runs the whole package suite before anything depends on the handle, so this is found before it matters.
- `runtime.NumCPU()` on a large host opens more connections than the file needs. Each connection has its own page cache, so this is memory, not correctness; the follow-up asks whether it should be a flag.

## Stop Condition

Stop and ask if `query_only(1)` refuses an operation the read path legitimately
performs. The alternative is a reader without `query_only`, which is a weaker
decision than the one this ADR made, and swapping it is the owner's call.

## Out of Scope

- Routing any read onto the reader handle — that is T5
- Giving `internal/mcptest` a reader handle (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
