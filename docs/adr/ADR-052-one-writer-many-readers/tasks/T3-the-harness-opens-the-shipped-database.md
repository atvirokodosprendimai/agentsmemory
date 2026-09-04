# Task ADR-052-T3: The test harness opens the database we ship

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `mcptest.DBPragmas`
**Consumes:** `writerDBPragmas` (T2)
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
| `internal/mcptest/harness_test.go` | add | the check that the harness DSN carries the shipped pragmas |

## Ordered Steps

1. [S1] Write the failing test first: a harness-opened database reads back `journal_mode=wal`, `busy_timeout=5000` and `foreign_keys=1`. It is red today, where the harness reads `delete`, `0` and `0` (TDD red).
2. [S2] Decide where the constant lives so there is exactly one. `cmd/server` cannot be imported by `internal/mcptest` without an import cycle if the harness is used from `cmd/server` tests — check that first, and if it cycles, move the pragma constants into a small `internal/dbcfg` package that both import. Say in the commit which it was and why.
3. [S3] Point `internal/mcptest/harness.go:416` at the shared constant.
4. [S4] Run the full suite: the harness now has WAL and a five-second busy timeout where it had neither, so any test that depended on the old locking behaviour changes. Triage each failure as a real finding rather than adjusting the harness back. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/mcptest/... -run 'TestTheHarnessOpensTheShippedDatabase$' -count=1 2>&1 | tee /tmp/adr052-t3a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t3a.out \
  && go test ./internal/mcptest/... ./cmd/server/ -count=1 2>&1 | tee /tmp/adr052-t3b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t3b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheHarnessOpensTheShippedDatabase` | `internal/mcptest/harness_test.go` | a harness database reads back the shipped `journal_mode`, `busy_timeout` and `foreign_keys` | — | S1, S3 |
| `TestTheHarnessNamesOneDSNSource` | `internal/mcptest/harness_test.go` | the harness's pragma string is the shared constant, not a string literal of its own | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | `harness.go:416` is the only place the harness opens a database; deleting the constant reference reverts to a literal and `TestTheHarnessNamesOneDSNSource` goes red |
| 3 — the caller can discover it | the exported constant's doc comment names ADR-052 and says the harness must not diverge from it |
| 4 — it is used | every `internal/mcptest` scenario opens through it |

## Mutation Log

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
