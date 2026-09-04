# Task ADR-052-T3: The test harness opens the database we ship

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `db.WriterPragmas` — the one writer DSN string, in a package both `cmd/server` and `internal/mcptest` already import. (Amended at execution: the plan said `mcptest.DBPragmas`, but exporting a second name for the same string is the second copy this task's Invariants forbid, and nothing consumes it.)
**Consumes:** `openWriterDB` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the harness DSN being the shipped one rather than a copy`

## Goal

Make `internal/mcptest` open with the pragmas the server ships, so a test
written there measures the database we run.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcptest/harness.go` | edit | line 416 opens with no pragmas; it must open with the writer configuration |
| `cmd/server/main.go` | edit | export the pragma string, or move it somewhere both can import, so the harness names one source rather than a second copy |
| `internal/mcptest/harness_internal_test.go` | add | the check that the harness DSN carries the shipped pragmas. An in-package file rather than `harness_test.go` (package `mcptest_test`), because the harness exposes no database handle and an accessor added for one test would be API surface the deferred reader-handle work has not decided on |
| `internal/mcptest/harness.go` | edit | S4 finding, recorded at execution: with `foreign_keys(1)` enforced, 30 scenarios failed at `usage metering failed` because `newServer` admits `TeamID`/`OtherTeamID` from a header with no `teams` row behind them, and `usage.team_id REFERENCES teams(id)`. One mechanism, not thirty tests depending on the old locking — the fixture lacked the parent row production's admission guarantees. `openDB` now seeds both teams; the pragma stays on |

## Ordered Steps

1. [S1] Write the failing test first: a harness-opened database reads back `journal_mode=wal`, `busy_timeout=5000` and `foreign_keys=1`. It is red today, where the harness reads `delete`, `0` and `0` (TDD red).
2. [S2] Decide where the constant lives so there is exactly one. `cmd/server` cannot be imported by `internal/mcptest` without an import cycle if the harness is used from `cmd/server` tests — check that first, and if it cycles, move the pragma constants into a small `internal/dbcfg` package that both import. Say in the commit which it was and why.
3. [S3] Point `internal/mcptest/harness.go:416` at the shared constant.
4. [S4] Run the full suite: the harness now has WAL and a five-second busy timeout where it had neither, so any test that depended on the old locking behaviour changes. Triage each failure as a real finding rather than adjusting the harness back. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/mcptest/... -run 'TestTheHarnessOpensTheShippedDatabase$|TestTheHarnessNamesOneDSNSource$' -count=1 2>&1 | tee /tmp/adr052-t3a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t3a.out \
  && go test ./internal/mcptest/... ./cmd/server/ -count=1 2>&1 | tee /tmp/adr052-t3b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t3b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheHarnessOpensTheShippedDatabase` | `internal/mcptest/harness_internal_test.go` | a harness database reads back the shipped `journal_mode`, `busy_timeout` and `foreign_keys` | — | S1, S3 |
| `TestTheHarnessNamesOneDSNSource` | `internal/mcptest/harness_internal_test.go` | the harness's `sqlite.Open` argument references `db.WriterPragmas` and carries no pragma literal, and no other non-test Go file spells `journal_mode(` — read from the AST, because two equal constants pin nothing | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | `harness.go:416` is the only place the harness opens a database; deleting the constant reference reverts to a literal and `TestTheHarnessNamesOneDSNSource` goes red |
| 3 — the caller can discover it | the exported constant's doc comment names ADR-052 and says the harness must not diverge from it |
| 4 — it is used | every `internal/mcptest` scenario opens through it |

## Mutation Log

- 2026-09-04 · a9b1ff6* · mutant killed · exit 1 · `internal/mcptest/harness.go` · the harness opens with no pragmas again: TestTheHarnessOpensTheShippedDatabase reads delete/0 back and TestTheHarnessNamesOneDSNSource finds no db.WriterPragmas in the open call · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · covers:the harness DSN being the shipped one rather than a copy
- 2026-09-04 · a9b1ff6* · mutant killed · exit 1 · `internal/mcptest/harness.go` · a byte-identical literal copy: the pragmas test stays green, so only the AST half of TestTheHarnessNamesOneDSNSource (no pragma literal in the open call; one journal_mode( literal in the module) can turn the exit code · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · covers:the exit code
- 2026-09-04 · a9b1ff6* · mutant killed · exit 1 · `internal/mcptest/harness.go` · the harness opens with no pragmas again: TestTheHarnessOpensTheShippedDatabase reads delete/0 back and TestTheHarnessNamesOneDSNSource finds no db.WriterPragmas in the open call · acceptance-sha256:4539686b485fc3df9f8d4b1154d4996dc3452bde3aa28924fecbb76cd33bb3c8 · covers:the harness DSN being the shipped one rather than a copy
- 2026-09-04 · a9b1ff6* · mutant killed · exit 1 · `internal/mcptest/harness.go` · a byte-identical literal copy: the pragmas test stays green, so only the AST half of TestTheHarnessNamesOneDSNSource (no pragma literal in the open call; one journal_mode( literal in the module) can turn the exit code · acceptance-sha256:4539686b485fc3df9f8d4b1154d4996dc3452bde3aa28924fecbb76cd33bb3c8 · covers:the exit code

## Invariants

- There is exactly one DSN string for the writer role in the tree after this task. A second literal is the defect this task closes.
- The harness keeps using a temp file per scenario; nothing here makes scenarios share a database.

## Risks

- Turning WAL on in the harness changes timing and may expose an order dependency between scenarios that has been passing by luck. That is a finding; record it rather than reverting the pragma.
- Moving the constants to a new package touches imports widely. Mitigated by doing it only if the cycle check in S2 says it is necessary.

## Stop Condition

Stop and ask if more than two existing scenarios fail once the harness matches
production. That would mean the suite has been depending on the divergence, and
which of those tests is right is not this task's call.

## Out of Scope

- Writing the concurrent-mutation scenarios the backlog defers; this task only makes the harness capable of measuring them honestly (deferred: `docs/adr/BACKLOG.md`)
- Giving the harness a reader handle

## Verification Log
- 2026-09-04 · a9b1ff6* · exit 1 · `set -o pipefail …` · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · ms:7225
  ```
  --- last 10 line(s) of stdout (of 42 after folding 42 raw)
  2026/09/04 22:20:54 OK   00034_billing_checkout_intents.sql (6.67ms)
  2026/09/04 22:20:54 OK   00035_billing_applied_orders.sql (646µs)
  2026/09/04 22:20:54 OK   00036_drawer_fetches.sql (5.31ms)
  2026/09/04 22:20:54 goose: successfully migrated database to version: 36
  --- FAIL: TestTheHarnessOpensTheShippedDatabase (0.13s)
      harness_internal_test.go:42: harness database reads journal_mode="delete"; the server ships "wal", so a scenario here measures a different database than the one we run
      harness_internal_test.go:42: harness database reads foreign_keys="0"; the server ships "1", so a scenario here measures a different database than the one we run
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	0.675s
  FAIL
  ```
- 2026-09-04 · a9b1ff6* · exit 1 · `set -o pipefail …` · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · ms:22731
  ```
  --- last 10 line(s) of stdout (of 1620 after folding 1620 raw)
  2026/09/04 22:22:55 OK   00032_kg_ended_reason.sql (1.24ms)
  2026/09/04 22:22:55 OK   00033_drawers_superseded_by_idx.sql (184.58µs)
  2026/09/04 22:22:55 OK   00034_billing_checkout_intents.sql (528.13µs)
  2026/09/04 22:22:55 OK   00035_billing_applied_orders.sql (185.54µs)
  2026/09/04 22:22:55 OK   00036_drawer_fetches.sql (327.58µs)
  2026/09/04 22:22:55 goose: successfully migrated database to version: 36
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	3.290s
  ok  	github.com/atvirokodosprendimai/agentsmemory/cmd/server	15.738s
  FAIL
  ```
- 2026-09-04 · a9b1ff6* · exit 1 · `set -o pipefail …` · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · ms:20395
  ```
  --- last 10 line(s) of stdout (of 1620 after folding 1620 raw)
  2026/09/04 22:23:19 OK   00032_kg_ended_reason.sql (558.58µs)
  2026/09/04 22:23:19 OK   00033_drawers_superseded_by_idx.sql (194.33µs)
  2026/09/04 22:23:19 OK   00034_billing_checkout_intents.sql (290.42µs)
  2026/09/04 22:23:19 OK   00035_billing_applied_orders.sql (158.96µs)
  2026/09/04 22:23:19 OK   00036_drawer_fetches.sql (352.46µs)
  2026/09/04 22:23:19 goose: successfully migrated database to version: 36
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	2.936s
  ok  	github.com/atvirokodosprendimai/agentsmemory/cmd/server	13.991s
  FAIL
  ```
- 2026-09-04 · a9b1ff6* · exit 0 · `set -o pipefail …` · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · ms:45825
- 2026-09-04 · a9b1ff6* · exit 0 · `set -o pipefail …` · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · ms:26575
- 2026-09-04 · a9b1ff6* · exit 0 · `set -o pipefail …` · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · ms:18087
- 2026-09-04 · a9b1ff6* · exit 0 · `set -o pipefail …` · acceptance-sha256:cee3c00e944acdb940b32aad2cbf0d7ec77802dfe42b188b7b32db901882e0eb · ms:16975
- 2026-09-04 · a9b1ff6* · exit 0 · `set -o pipefail …` · acceptance-sha256:4539686b485fc3df9f8d4b1154d4996dc3452bde3aa28924fecbb76cd33bb3c8 · ms:17112
- 2026-09-04 · a9b1ff6* · exit 0 · `set -o pipefail …` · acceptance-sha256:4539686b485fc3df9f8d4b1154d4996dc3452bde3aa28924fecbb76cd33bb3c8 · ms:15759
