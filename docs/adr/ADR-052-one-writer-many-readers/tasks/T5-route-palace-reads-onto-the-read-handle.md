# Task ADR-052-T5: Route internal/palace reads onto the read handle

**Depends-on:** T4
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `palace.NewRepo`
**Consumes:** `openReaderDB` (T4)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the read methods holding the query_only handle`

## Goal

Give `internal/palace` two handles instead of one and put its read methods on
the reader, so the read/write split is a property of the code rather than a
convention.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/repo.go` | edit | `Repo` gains a second field; `NewRepo` takes a reader and a writer; read methods use the reader, write methods and any `Transaction` use the writer |
| every other `internal/palace` file that reaches a `*gorm.DB` directly | edit | **fourteen** files do, not the five an earlier draft listed — derive the set with `grep -rln 'r\.db\|s\.repo\.db\|\.db\.WithContext' --include='*.go' internal/palace/ \| grep -v _test`, which today returns `admin.go`, `anchors.go`, `closet.go`, `contentkey.go`, `currentonly.go`, `fetchlog.go`, `graph.go`, `kg.go`, `kgextract.go`, `recallstats.go`, `repo.go`, `service.go`, `supersede.go`, `validity.go`. `anchors.go`, `closet.go`, `graph.go`, `kg.go` and `recallstats.go` carry pure-read surfaces the earlier list omitted |
| `cmd/server/main.go` | edit | pass both handles to `NewRepo` — the line that SELECTS the split |
| `internal/mcpserver/*_test.go`, `internal/mcptest`, `cmd/server/*_test.go` | edit | **thirteen** constructions of `palace.NewRepo`/`NewService` live in `internal/mcpserver` alone; the signature change does not compile without them |
| `internal/palace/*_test.go` | edit | every test constructing a `Repo` gets the new signature |

## Ordered Steps

1. [S1] Write `TestReadMethodsUseTheReadHandle` first: build a `Repo` whose reader is a `query_only` handle and whose writer is a normal one, then assert every read method succeeds. It fails to compile today because `NewRepo` takes one argument (TDD red).
2. [S2] Add the second field to `Repo` and change `NewRepo(reader, writer *gorm.DB)`. Doc-comment the struct with the rule — reads on the reader, anything inside a `Transaction` on the writer — and cite ADR-052.
3. [S3] Move each read method onto the reader field. Where a method both reads and writes, it belongs wholly on the writer: a read on the reader followed by a write on the writer is two connections and no longer one transaction, which is a correctness change, not a routing one.
4. [S4] Point every `Transaction(` in the package at the writer handle explicitly, including the ones currently reaching `s.repo.db`.
5. [S5] Wire both handles at `cmd/server/main.go` and update every test constructor. Where a test has only one handle, pass it as both — a test is allowed to be less strict than production, and saying so in one place beats changing every fixture.
6. [S6] Confirm `internal/palace/service.go:1444` — the ADR-045 relocation whose read hides inside `Repo.Update` — now runs entirely on the writer, since it is a read-then-write inside one transaction. [proof: human: the reviewer follows the call from service.go into Repo.Update and confirms both statements use the writer field]

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ -run 'TestReadMethodsUseTheReadHandle$|TestEveryTransactionUsesTheWriteHandle$' -count=1 2>&1 | tee /tmp/adr052-t5a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t5a.out \
  && go test ./internal/palace/... ./internal/mcpserver/... ./internal/mcptest/... ./cmd/server/... -count=1 2>&1 | tee /tmp/adr052-t5b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t5b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestReadMethodsUseTheReadHandle` | `internal/palace/handles_test.go` | every read method succeeds against a `query_only` reader, so none of them writes | — | S1, S2, S3 |
| `TestEveryTransactionUsesTheWriteHandle` | `internal/palace/handles_test.go` | a `Repo` whose reader is `query_only` completes every write transaction, so no transaction is running on the reader | — | S4, S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | `cmd/server/main.go` passes the reader from `openReaderDB`; passing the writer for both arguments makes `TestReadMethodsUseTheReadHandle` unable to detect a stray write, which is why T6 adds the source-level check rather than relying on this rung alone |
| 3 — the caller can discover it | `NewRepo`'s signature is the interface: a caller cannot construct a `Repo` without deciding which handle is which |
| 4 — it is used | every MCP read tool goes through these methods; nothing counts the split yet |

## Mutation Log

- 2026-09-04 · c344304* · mutant killed · exit 1 · `internal/palace/repo.go` · the constructor ignores the reader: every read still succeeds on the writer, and only the closed-reader check in TestReadMethodsUseTheReadHandle sees that Get keeps answering · acceptance-sha256:7fd908b2a4259192fd39d55d9fbbcd6f147f282edc011be125e96382a5dc8517 · covers:the read methods holding the query_only handle
- 2026-09-04 · c344304* · mutant killed · exit 1 · `internal/palace/service.go` · moveMemory opens its transaction on the reader: the relocation in TestEveryTransactionUsesTheWriteHandle fails with the readonly error, which is the split the invariant forbids made visible · acceptance-sha256:7fd908b2a4259192fd39d55d9fbbcd6f147f282edc011be125e96382a5dc8517 · covers:the exit code

## Invariants

- No method reads on the reader and writes on the writer within what used to be one transaction. That would silently drop atomicity, which is worse than the contention this ADR fixes.
- `Repo`'s writer field is the only handle any `Transaction` in the package uses.
- Behaviour is unchanged for every existing caller; this task moves connections, not semantics.

## Risks

- The signature change is wide and mechanical, and a mechanical change is where an accidental semantic one hides. S3 names the one shape to watch — a split read-then-write — and the invariant above states it.
- A test passing the same handle for both arguments cannot catch a read method that writes. Accepted deliberately in S5, and covered instead by `TestReadMethodsUseTheReadHandle` building a genuinely `query_only` reader.

## Stop Condition

Stop and ask if any read method turns out to write — an upsert on a cache
table, a lazily-created row. That is a read path with a side effect, and
whether it should keep it is a design question this task cannot settle.

Stop and rebase before starting: this task touches every `internal/palace` test
file, and open ADR PRs on that package will conflict.

## Out of Scope

- The nine other SQL-owning packages (deferred: `docs/adr/BACKLOG.md`)
- Changing any query, index or schema

## Verification Log
- 2026-09-04 · c344304* · exit 1 · `set -o pipefail …` · acceptance-sha256:7fd908b2a4259192fd39d55d9fbbcd6f147f282edc011be125e96382a5dc8517 · ms:475
  ```
  --- last 7 line(s) of stdout
  # github.com/atvirokodosprendimai/agentsmemory/internal/palace [github.com/atvirokodosprendimai/agentsmemory/internal/palace.test]
  internal/palace/handles_test.go:77:36: too many arguments in call to NewRepo
  	have (*gorm.DB, *gorm.DB)
  	want (*gorm.DB)
  internal/palace/handles_test.go:151:31: svc.repo.reader undefined (type *Repo has no field or method reader)
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace [build failed]
  FAIL
  ```
- 2026-09-04 · c344304* · exit 1 · `set -o pipefail …` · acceptance-sha256:7fd908b2a4259192fd39d55d9fbbcd6f147f282edc011be125e96382a5dc8517 · ms:52328
  ```
  --- last 10 line(s) of stdout (of 100 after folding 101 raw)
  2026/09/04 22:55:14 OK   00036_drawer_fetches.sql (639.08µs)
  2026/09/04 22:55:14 goose: successfully migrated database to version: 36
  --- FAIL: TestCandidateWideningDoesNotRefetchRows (0.22s)
      widening_test.go:130: no statement resolved the first chunk; the gate is not watching the right query
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	30.532s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	14.086s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	15.443s
  ok  	github.com/atvirokodosprendimai/agentsmemory/cmd/server	28.505s
  FAIL
  ```
- 2026-09-04 · c344304* · exit 0 · `set -o pipefail …` · acceptance-sha256:7fd908b2a4259192fd39d55d9fbbcd6f147f282edc011be125e96382a5dc8517 · ms:18096
- 2026-09-04 · c344304* · exit 0 · `set -o pipefail …` · acceptance-sha256:7fd908b2a4259192fd39d55d9fbbcd6f147f282edc011be125e96382a5dc8517 · ms:16433
- 2026-09-04 · c344304* · exit 0 · `set -o pipefail …` · acceptance-sha256:7fd908b2a4259192fd39d55d9fbbcd6f147f282edc011be125e96382a5dc8517 · ms:25592
