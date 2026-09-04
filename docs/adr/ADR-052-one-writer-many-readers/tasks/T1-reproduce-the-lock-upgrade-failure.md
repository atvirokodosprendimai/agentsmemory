# Task ADR-052-T1: Reproduce the lock upgrade failure as a red test

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `TestAReadThenWriteTransactionSurvivesConcurrentWriters`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the "database is locked" symptom in the output`

## Goal

Put the measurement this ADR rests on into the tree as a test that fails today
for the documented reason, so the defect stops being a session's private
evidence.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/dbcontention_test.go` | add | the new test, in the package that owns `dbPragmas` so it can open exactly the shipped DSN |

## Ordered Steps

1. [S1] Write `TestAReadThenWriteTransactionSurvivesConcurrentWriters` in `cmd/server/dbcontention_test.go`: open a temp-directory database through the serving writer opener, `AutoMigrate` a two-column throwaway model, then run 8 goroutines × 40 `Transaction` closures that read before they write. Collect every error and fail naming the count and the first error string. Confirm it is RED (TDD red) — 280 of 320 at `732b727`, because the handle is uncapped there.
2. [S2] Record in the test's own comment that the failure is a deferred-transaction lock upgrade, that `busy_timeout` does not cover it, and that the behaviour is pinned to `github.com/glebarez/sqlite@v1.11.0` — a version bump is a reason to re-read this test, not to delete it. [proof: human: a reviewer reads the comment and checks the version against `go.mod`]
3. [S3] Assert on the count being zero rather than on a threshold, so the test states the invariant the ADR decides rather than the number today's tree happens to produce.
4. [S4] Write the test's read-then-write closure in the shape of `internal/palace/service.go:1444` — a read reached through a HELPER rather than a visible `SELECT` before the `UPDATE` — because that is the one of the six sites a reviewer scanning the call site cannot see, and therefore the one a future change reintroduces. [proof: human: a reviewer compares the test closure against `Repo.Update`'s read at `repo.go:491` and confirms the shape matches]
5. [S5] ⚠**The mutant this test must die to is `SetMaxOpenConns(1)` deleted, not a DSN flag.** Measured 2026-09-04 through ONE handle: uncapped 280 of 320, capped 0 of 320 — and capped scores 0 with or without `_txlock=immediate`, which is why the record ships no such flag. Do not add one to make this test pass; if it passes uncapped, the measurement did not reproduce and the Stop Condition applies. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./cmd/server/ -run 'TestAReadThenWriteTransactionSurvivesConcurrentWriters$' -count=1 > /tmp/adr052-t1.out 2>&1
rc=$?
cat /tmp/adr052-t1.out
test "$rc" -ne 0 \
  && grep -q "database is locked" /tmp/adr052-t1.out \
  && ! grep -qE "no tests to run|\[build failed\]|^ok +.*no test files" /tmp/adr052-t1.out
```

This fence exits 0 exactly when the test exists, compiles, runs, and fails with
the documented symptom — which is this task's definition of done, because T1
delivers the red half of the TDD pair and T2 turns it green. Before the test is
written `go test -run` exits 0 with "no tests to run", so `rc -ne 0` fails and
the fence is red, which is the state it must be in at authoring time.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAReadThenWriteTransactionSurvivesConcurrentWriters` | `cmd/server/dbcontention_test.go` | a read-then-write transaction under 8 concurrent writers completes without `SQLITE_BUSY` | — | S1, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the test above |
| 2 — something selects it | `go test ./cmd/server/` runs it; the repository's CI job runs that package |
| 3 — the caller can discover it | n/a: no declared interface — this is a test, not a served capability |
| 4 — it is used | it is the acceptance fence of T2, so every T2 run exercises it |

## Mutation Log

- 2026-09-04 · 474de54* · mutant killed · exit 1 · `cmd/server/dbcontention_test.go` · deleting the READ turns the transaction into a plain UPDATE that never upgrades a lock: 0 of 320 fail, the test passes, and the fence goes red because it requires a non-zero exit AND the "database is locked" string. A fence that only asked for failure would have survived this, since a broken test fails too. · acceptance-sha256:1963a2503202d90d24a482bd2efce8ab2aba72eb4035692062f6880cbdde19fe

## Invariants

- The test opens through `openDB`, never by hand-assembling a DSN, so it keeps measuring whatever the shipped constant says rather than a copy of it that can drift.
- The test uses a temp directory and removes it, so it leaves nothing behind and can run in parallel with the rest of the package.
- The assertion is "zero failures", not "fewer than N".

## Risks

- The failure is a race and could in principle score 0 by luck on a very slow machine. Measured five runs at 273–281 of 320, so the margin is large; if it ever scores 0 at `732b727` that is itself a finding and the Stop Condition applies.
- 8 × 40 transactions is fast (about 20ms) but opens a real file; on a filesystem without working locking the test may fail for an unrelated reason. It names the symptom it greps for, so that case is distinguishable.

## Stop Condition

Stop and ask if the test passes at `732b727` before any fix is applied. That
would mean the measurement behind this ADR does not reproduce on the executing
machine, and the whole record's premise needs re-taking there before T2 changes
anything.

## Out of Scope

- Fixing the failure — that is T2's job
- Testing the same shape through the MCP surface end to end rather than at the database

## Verification Log
- 2026-09-04 · 474de54* · exit 0 · `set -o pipefail …` · acceptance-sha256:1963a2503202d90d24a482bd2efce8ab2aba72eb4035692062f6880cbdde19fe · ms:1575
- 2026-09-04 · 474de54* · exit 0 · `set -o pipefail …` · acceptance-sha256:1963a2503202d90d24a482bd2efce8ab2aba72eb4035692062f6880cbdde19fe · ms:2928
