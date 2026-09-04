# Task ADR-052-T2: One DSN per role, and a writer that takes its lock at BEGIN

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `writerDBPragmas`, `readerDBPragmas`, `openWriterDB`
**Consumes:** `TestAReadThenWriteTransactionSurvivesConcurrentWriters` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the writer DSN carrying _txlock=immediate`, `foreign_keys reading 1 on a serving connection`

## Goal

Give the writer its own DSN with `_txlock=immediate` and a pool of one, turn
foreign keys on for every serving connection, and turn T1's test green.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/main.go` | edit | `dbPragmas` becomes the shared base; add `writerDBPragmas` and `readerDBPragmas`; add `openWriterDB` setting `SetMaxOpenConns(1)`; point the serve path at it |
| `cmd/server/dbcontention_test.go` | edit | T1's test now asserts zero failures and passes; add the pragma-readback assertions |
| `cmd/server/sync.go` | edit | `sync` writes, so it opens the writer handle rather than the generic one — this is the line that SELECTS the new opener for the second write path |

## Ordered Steps

1. [S1] Confirm T1's test is red for the documented reason before changing any source (TDD red). [proof: human: the executor reads T1's Verification Log and confirms its first entry is a non-zero exit at this tree]
2. [S2] Split the constant: keep `dbPragmas` as the shared base (`journal_mode(WAL)`, `busy_timeout(5000)`) and add `foreign_keys(1)` to it. Add `writerDBPragmas = dbPragmas + "&_txlock=immediate"` and `readerDBPragmas = dbPragmas + "&_pragma=query_only(1)"`. Give each a doc comment naming ADR-052 and the measurement, per `AGENTS.md` §Doc comments.
3. [S3] Add `openWriterDB(path, debug)` wrapping `openDBWithPragmas(path, debug, writerDBPragmas)` and calling `SetMaxOpenConns(1)` on the returned handle, with the comment explaining that the cap is the writer count made explicit and the DSN knob is what fixes correctness.
4. [S4] Point the serve path's `opener` (`cmd/server/main.go:1112`) and `cmd/server/sync.go:58` at `openWriterDB`. Leave `openInspectionDB` alone — `doctor` deliberately omits `journal_mode`.
5. [S5] Add assertions to T1's test file that a writer handle reads back `foreign_keys=1`, `journal_mode=wal` and `busy_timeout=5000`, so a DSN that silently stops being honoured is caught rather than assumed. [proof: acceptance]
6. [S6] Run the whole `cmd/server` suite and triage any failure that `foreign_keys(1)` newly surfaces. A cascade that was never happening is a found defect: fix it here if it is one line, otherwise stop per the Stop Condition. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./cmd/server/ -run 'TestAReadThenWriteTransactionSurvivesConcurrentWriters$|TestServingConnectionsCarryTheirPragmas$' -count=1 2>&1 | tee /tmp/adr052-t2a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t2a.out \
  && go test ./cmd/server/... -count=1 2>&1 | tee /tmp/adr052-t2b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t2b.out
```

The new units run alone first so neither can be carried by the regression half,
then the whole `cmd/server` tree runs because `foreign_keys(1)` is exactly the
kind of change whose damage shows up somewhere else.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAReadThenWriteTransactionSurvivesConcurrentWriters` | `cmd/server/dbcontention_test.go` | zero `SQLITE_BUSY` failures under 8 concurrent read-then-write transactions | — | S2, S3, S4 |
| `TestServingConnectionsCarryTheirPragmas` | `cmd/server/dbcontention_test.go` | a writer handle reads back `foreign_keys=1`, `journal_mode=wal`, `busy_timeout=5000` | — | S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | `cmd/server/main.go:1112` and `sync.go:58` call `openWriterDB`; deleting either line leaves the serve or sync path on the old DSN and `TestAReadThenWriteTransactionSurvivesConcurrentWriters` goes red only if the test opens through the same seam — S1 of T6 is what closes this rung properly |
| 3 — the caller can discover it | the doc comments on `writerDBPragmas` and `openWriterDB` name ADR-052 and the measurement; `TestEveryCitedADRResolves` keeps the citation honest |
| 4 — it is used | every `serve` and every `sync` opens through it; nothing counts the opens yet |

## Mutation Log

## Invariants

- `openInspectionDB` and `inspectionDBPragmas` keep their current behaviour; `doctor` must not acquire a write lock or convert a journal mode.
- The data-export archive connection (`internal/dataexport`) keeps opening with no pragmas.
- `_txlock=immediate` appears on the writer DSN only. A reader that took a write lock at `BEGIN` would serialise recall, which is the thing this ADR exists to avoid.

## Risks

- `foreign_keys(1)` can fail a multi-row write whose insert order the constraints refuse. Mitigated by running the whole package suite in the fence rather than the new test alone.
- A future driver could accept `_txlock` and ignore it. `TestAReadThenWriteTransactionSurvivesConcurrentWriters` fails in that case, which is the point of keeping it after it goes green.

## Stop Condition

Stop and ask if enabling `foreign_keys(1)` produces more than a one-line fix —
a cascade that was silently not happening is a data-integrity question this
task has no authority to settle, and it may deserve its own record.

Stop and ask if `_txlock=immediate` does not bring the failure count to zero on
the executing machine. The measurement said it does; a disagreement means the
Decision rests on something that is not true here.

## Out of Scope

- Building or wiring the reader handle — T4 owns that; this task only defines its constant
- Changing any package's `*gorm.DB` signature

## Verification Log
