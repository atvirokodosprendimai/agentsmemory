# Task ADR-052-T2: One DSN per role, and a writer that takes its lock at BEGIN

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `openWriterDB`, `openReaderDB`, `readerDBPragmas`
**Consumes:** `TestAReadThenWriteTransactionSurvivesConcurrentWriters` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the writer handle capping its pool at one connection`, `foreign_keys reading 1 on a serving connection`

## Goal

Give the writer its own opener capped at ONE connection, give the readers their own, turn foreign keys on for every serving connection, and turn T1's test green.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/main.go` | edit | `dbPragmas` gains `foreign_keys(1)`; add `readerDBPragmas`; add `openWriterDB` capping the pool at 1; point the serve path at it |
| `cmd/server/dbcontention_test.go` | edit | T1's test now asserts zero failures and passes; add the pragma-readback assertions |
| `cmd/server/sync.go` | edit | `sync` writes, so it opens the writer handle rather than the generic one — this is the line that SELECTS the new opener for the second write path |

## Ordered Steps

1. [S1] Confirm T1's test is red for the documented reason before changing any source (TDD red). [proof: human: the executor reads T1's Verification Log and confirms its first entry is a non-zero exit at this tree]
2. [S2] Split the constant: keep `dbPragmas` as the shared base (`journal_mode(WAL)`, `busy_timeout(5000)`) and add `foreign_keys(1)` to it. Add `readerDBPragmas = dbPragmas + "&_pragma=query_only(1)"`. ⚠**Add NO writer-side serialisation pragma.** The writer is serialised by having one connection; a flag in front of that is a wall in front of a single-file door, and the record's Alternatives records the measurement that rejected it.
3. [S3] Add `openWriterDB(path, debug)` wrapping `openDBWithPragmas(path, debug, dbPragmas)` and calling `SetMaxOpenConns(1)` on the returned handle. The doc comment carries the whole decision: this cap IS the write serialisation, deleting it reintroduces 280 failures in 320, and no lock or pragma is standing behind it.
4. [S4] Point the serve path's `opener` (`cmd/server/main.go:1112`) and `cmd/server/sync.go:58` at `openWriterDB`. Leave `openInspectionDB` alone — `doctor` deliberately omits `journal_mode`.
5. [S5] Add assertions to T1's test file that a writer handle reads back `foreign_keys=1`, `journal_mode=wal` and `busy_timeout=5000`, so a DSN that silently stops being honoured is caught rather than assumed. [proof: acceptance]
6. [S6] Run the whole `cmd/server` suite and triage any failure that `foreign_keys(1)` newly surfaces. A cascade that was never happening is a found defect: fix it here if it is one line, otherwise stop per the Stop Condition. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./cmd/server/ -run 'TestAReadThenWriteTransactionSurvivesConcurrentWriters$|TestServingConnectionsCarryTheirPragmas$|TestTheReaderPragmasAreTheWritersPlusQueryOnly$' -count=1 2>&1 | tee /tmp/adr052-t2a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t2a.out \
  && go test ./... -count=1 2>&1 | tee /tmp/adr052-t2b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t2b.out
```

The new units run alone first so neither can be carried by the regression half,
then the whole `cmd/server` tree runs because `foreign_keys(1)` is exactly the
kind of change whose damage shows up somewhere else. The regression half is the
WHOLE tree, not `./cmd/server/...`: a foreign key enforced for the first time can
break a multi-row write in any package, and an earlier draft scoped this to one
package while the record claimed it ran the full suite.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAReadThenWriteTransactionSurvivesConcurrentWriters` | `cmd/server/dbcontention_test.go` | zero `SQLITE_BUSY` failures across eight independent writer handles on one file | — | S2, S3, S4 |
| `TestServingConnectionsCarryTheirPragmas` | `cmd/server/dbcontention_test.go` | a writer handle reads back `foreign_keys=1`, `journal_mode=wal`, `busy_timeout=5000` | — | S5 |
| `TestTheReaderPragmasAreTheWritersPlusQueryOnly` | `cmd/server/dbcontention_test.go` | `readerDBPragmas` is exactly `dbPragmas` plus `query_only(1)`, and neither constant carries a `_txlock` — the relationship rather than the string, so a pragma added to the writer cannot go missing from the reader | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | `cmd/server/main.go:1112` and `sync.go:58` call `openWriterDB`; deleting either line leaves the serve or sync path on the old DSN and `TestAReadThenWriteTransactionSurvivesConcurrentWriters` goes red only if the test opens through the same seam — S1 of T6 is what closes this rung properly |
| 3 — the caller can discover it | the doc comments on `openWriterDB` and `readerDBPragmas` name ADR-052 and the measurement; `TestEveryCitedADRResolves` keeps the citation honest |
| 4 — it is used | every `serve` and every `sync` opens through it; nothing counts the opens yet |

## Mutation Log


- 2026-09-04 · 242863f* · mutant killed · exit 1 · `cmd/server/main.go` · raising the writer cap to unlimited is the whole decision in one line. T1 goes red again at about 280 of 320 "database is locked", so the fence binds to the writer COUNT and not to the DSN, the pragmas, or any wall in front of it. · acceptance-sha256:af4e78ee44b0c74de9c851c3e1cde87f1b43e2df13e71e848d56d3eb45564964

## Invariants

- `openInspectionDB` and `inspectionDBPragmas` keep their current behaviour; `doctor` must not acquire a write lock or convert a journal mode.
- The data-export archive connection (`internal/dataexport`) keeps opening with no pragmas.
- No serialisation pragma or lock is added on the writer side. If one is ever needed, the writer count stopped being one and THAT is the defect to fix.

## Risks

- `foreign_keys(1)` can fail a multi-row write whose insert order the constraints refuse. Mitigated by running the whole package suite in the fence rather than the new test alone.
- Capping the pool at one connection makes a slow write block the next one rather than running it concurrently. That is the intended semantics, not a regression: measured cheaper than contention on this workload (30ms against 61ms), and recall does not queue behind it because reads are on the other handle.

## Stop Condition

Stop and ask if enabling `foreign_keys(1)` produces more than a one-line fix —
a cascade that was silently not happening is a data-integrity question this
task has no authority to settle, and it may deserve its own record.

Stop and ask if capping the writer at one connection does not bring the failure count to zero on the executing machine. The measurement said it does; a disagreement means there is a second writer this record has not accounted for, and finding it matters more than the cap.

⚠ Stop and ask BEFORE enabling `foreign_keys(1)` against any deployment that
holds real data. Turning the pragma on does not validate or repair existing
rows — it changes subsequent writes and activates cascades — so the question is
what the corpus already contains. Run `PRAGMA foreign_key_check` against every
deployment corpus, not just a local one, and bring the result with a stated
response to any violation. A local check passing over a single-team corpus says
nothing about hosted data.

## Out of Scope

- Building or wiring the reader handle — T4 owns that; this task only defines its constant
- Changing any package's `*gorm.DB` signature

## Verification Log
- 2026-09-04 · 242863f* · exit 0 · `set -o pipefail …` · acceptance-sha256:af4e78ee44b0c74de9c851c3e1cde87f1b43e2df13e71e848d56d3eb45564964 · ms:37328
- 2026-09-04 · 242863f* · exit 0 · `set -o pipefail …` · acceptance-sha256:af4e78ee44b0c74de9c851c3e1cde87f1b43e2df13e71e848d56d3eb45564964 · ms:38462
