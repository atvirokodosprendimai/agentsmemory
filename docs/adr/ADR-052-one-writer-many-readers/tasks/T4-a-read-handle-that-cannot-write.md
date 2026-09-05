# Task ADR-052-T4: A read handle the read path cannot write through

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `openReaderDB`, `--db-reader-pool`
**Consumes:** `readerDBPragmas` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `query_only being refused at the driver rather than in Go`, `the pool size the serve path actually passes to SetMaxOpenConns`

## Goal

Build and wire a second handle that SQLite itself refuses writes on, and prove
the refusal is real rather than a naming convention.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/main.go` | edit | add `openReaderDB` using `readerDBPragmas` and `SetMaxOpenConns(cfg.DBReaderPool)`; declare `--db-reader-pool`; populate the config field; open the reader beside the writer in the serve path and hold both |
| `internal/config/config.go` | edit | `DBReaderPool int`, defaulted like `RerankPool` — a field an operator cannot set is a setting that does not exist, and `TestEveryConfigFieldIsPopulatedAndRead` fails on both halves |
| `README.md` | edit | the flags table row; `TestReadEnvVarsAreDocumented` fails on a variable the code reads and no operator doc mentions |
| `.env.example` | edit | `DB_READER_POOL`, for the same reason and in the direction `TestDocumentedEnvVarsAreRead` checks |
| `cmd/server/dbwiring_test.go` | add | the gate proving the read handle refuses a write, and that the serve path builds one |

## Ordered Steps

1. [S1] Write `TestTheReadHandleCannotWrite` first and watch it fail to compile, because `openReaderDB` does not exist (TDD red).
2. [S2] Add `openReaderDB(path, debug, pool)` wrapping `openDBWithPragmas` with `readerDBPragmas` and calling `SetMaxOpenConns(pool)`. Derive the pool from `max(4, runtime.NumCPU())` when the caller passes `0` or less, the way `RerankPool` already treats its zero. Doc-comment it with why the pool is many where the writer's is one, cite ADR-052, and say in the comment that no such knob exists for the writer because raising that one would delete the decision.
3. [S3] Declare `&cli.IntFlag{Name: "db-reader-pool", Sources: cli.EnvVars("DB_READER_POOL"), Value: def.DBReaderPool}` beside `rerank-pool` (`cmd/server/main.go:220`), assign it into `config.Config` where `RerankPool` is assigned (`main.go:157`), and add the field to `internal/config/config.go` with its default. Its `Usage` string must name what it does and that `0` derives from `NumCPU()`, because `--help` is the only place a caller learns the knob exists.
4. [S4] Document it in `README.md`'s flags table and in `.env.example`. Both directions are gated — a read variable no doc mentions fails `TestReadEnvVarsAreDocumented`, a documented one nothing reads fails `TestDocumentedEnvVarsAreRead` — so this step is not tidying.
5. [S5] Open the reader in the serve path beside the writer and close both on shutdown. Nothing consumes it yet — T5 does — so this step is wiring only, and the point is that the handle exists at the composition root where the choice belongs.
6. [S6] Assert in the test that a write through the reader returns an error mentioning a readonly database, and that a read through it succeeds. Assert both, because a handle that refuses everything would also pass a refusal-only check.
7. [S7] Run the whole `cmd/server` suite before anything is routed onto the reader, so a `query_only` incompatibility surfaces while the blast radius is one commit. [proof: acceptance]
8. [S8] Prove the flag is not decoration: `TestTheReaderPoolFlagReachesTheHandle` sets `--db-reader-pool` to a value the derivation cannot produce and asserts `db.Stats().MaxOpenConnections` is that value, then asserts the derived default with the flag unset. ⚠**The mutant is the `SetMaxOpenConns(pool)` argument replaced by the derived constant** — a knob that is read into a variable nothing passes on is exactly the inert setting ADR-006 rejects, and it passes every reachability check in the tree. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./cmd/server/ -run 'TestTheReadHandleCannotWrite$|TestTheServePathOpensBothHandles$|TestTheReaderPoolFlagReachesTheHandle$|TestReadEnvVarsAreDocumented$|TestDocumentedEnvVarsAreRead$' -count=1 2>&1 | tee /tmp/adr052-t4a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t4a.out \
  && go test ./cmd/server/ -count=1 2>&1 | tee /tmp/adr052-t4b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t4b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheReadHandleCannotWrite` | `cmd/server/dbwiring_test.go` | a read succeeds and a write is refused by the driver through a handle from `openReaderDB` | — | S1, S2, S6 |
| `TestTheServePathOpensBothHandles` | `cmd/server/dbwiring_test.go` | the serve path constructs a reader as well as a writer, and closes both | — | S5 |
| `TestTheReaderPoolFlagReachesTheHandle` | `cmd/server/dbwiring_test.go` | `--db-reader-pool` sets the reader handle's `MaxOpenConnections`, and an unset flag derives `max(4, NumCPU())` | — | S3, S8 |
| `TestReadEnvVarsAreDocumented` | `cmd/server/envreach_test.go` | `DB_READER_POOL` is named in operator documentation now that the code reads it — an existing gate, listed because it is what proves S4 rather than a reviewer's memory | — | S4 |
| `TestDocumentedEnvVarsAreRead` | `cmd/server/envreach_test.go` | the `DB_READER_POOL` added to `.env.example` is a variable the server actually reads, which is the arrow that catches a documented knob nothing consumes | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestTheReadHandleCannotWrite` |
| 2 — something selects it | the serve path calls `openReaderDB`; `TestTheServePathOpensBothHandles` goes red when that call is deleted, which is the mutation to run |
| 3 — the caller can discover it | `openReaderDB`'s doc comment names ADR-052 and states the rule that read-only work belongs on this handle; `--db-reader-pool` is in `--help`, in `README.md`'s flags table and in `.env.example`, which is the only route by which an operator learns the pool is theirs to set |
| 4 — it is used | the serve path passes `cfg.DBReaderPool` into the handle it builds, and `TestTheReaderPoolFlagReachesTheHandle` fails when that argument stops being the flag's value. Nothing yet READS through the handle — T5 does — and that is the honest answer to this rung |

## Mutation Log

- 2026-09-04 · 8648944* · mutant killed · exit 1 · `cmd/server/main.go` · the knob is read into cfg and passed to openReaderDB but never reaches SetMaxOpenConns — the inert setting ADR-006 rejects; TestTheReaderPoolFlagReachesTheHandle sees --db-reader-pool=3 open a derived pool · acceptance-sha256:cf21cbdd4f51b7a9a6e1bbdeae2211acff21d07fa1db63117657db4f92958999 · covers:the pool size the serve path actually passes to SetMaxOpenConns
- 2026-09-04 · 8648944* · mutant killed · exit 1 · `cmd/server/main.go` · the serve path stops opening the reader: openReaderDB still exists and still passes its own test, but nothing selects it — TestTheServePathOpensBothHandles finds rdb nil · acceptance-sha256:cf21cbdd4f51b7a9a6e1bbdeae2211acff21d07fa1db63117657db4f92958999 · covers:the exit code
- 2026-09-04 · 8648944* · mutant killed · exit 1 · `cmd/server/main.go` · the reader opens on the writer pragmas: every read still succeeds and the handle is still named a reader, but SQLite no longer refuses a write through it — TestTheReadHandleCannotWrite sees the INSERT land · acceptance-sha256:cf21cbdd4f51b7a9a6e1bbdeae2211acff21d07fa1db63117657db4f92958999 · covers:query_only being refused at the driver rather than in Go
- 2026-09-04 · 8648944* · mutant killed · exit 1 · `cmd/server/main.go` · the knob is read into cfg and passed to openReaderDB but never reaches SetMaxOpenConns — the inert setting ADR-006 rejects; TestTheReaderPoolFlagReachesTheHandle sees --db-reader-pool=3 open a derived pool · acceptance-sha256:ea94b05790c278470f06bf24a5e0069e81b20e571244f27a9fa32a5ed9482ba2 · covers:the pool size the serve path actually passes to SetMaxOpenConns
- 2026-09-04 · 8648944* · mutant killed · exit 1 · `cmd/server/main.go` · the serve path stops opening the reader: openReaderDB still exists and still passes its own test, but nothing selects it — TestTheServePathOpensBothHandles finds rdb nil · acceptance-sha256:ea94b05790c278470f06bf24a5e0069e81b20e571244f27a9fa32a5ed9482ba2 · covers:the exit code
- 2026-09-04 · 8648944* · mutant killed · exit 1 · `cmd/server/main.go` · the reader opens on the writer pragmas: every read still succeeds and the handle is still named a reader, but SQLite no longer refuses a write through it — TestTheReadHandleCannotWrite sees the INSERT land · acceptance-sha256:ea94b05790c278470f06bf24a5e0069e81b20e571244f27a9fa32a5ed9482ba2 · covers:query_only being refused at the driver rather than in Go

## Invariants

- `readerDBPragmas` carries `query_only(1)` and never `_txlock=immediate`.
- The reader and the writer point at the same database file; this is a connection split, not a replica.
- `doctor`'s `openInspectionDB` is untouched.
- The writer gets no pool flag. `SetMaxOpenConns(1)` on the writer stays a literal, because a knob that can raise it is a knob that can silently undo ADR-052 — T6's gate reads that literal out of the AST for this reason.

## Risks

- `query_only` may refuse something gorm does on a read that is not obviously a write — a temp table for a complex join, or `PRAGMA optimize` on close. S7 runs the whole package suite before anything depends on the handle, so this is found before it matters.
- `runtime.NumCPU()` on a large host opens more connections than the file needs. Each connection has its own page cache, so this is memory, not correctness — and it is now the DEFAULT rather than the only option: `--db-reader-pool` is the lever. What the flag does not come with is a measurement justifying its default, which the record carries as an open follow-up rather than pretending otherwise.

## Stop Condition

Stop and ask if `query_only(1)` refuses an operation the read path legitimately
performs. The alternative is a reader without `query_only`, which is a weaker
decision than the one this ADR made, and swapping it is the owner's call.

Stop and ask if anything asks for the same flag on the writer handle. The answer
this record gives is no, and a request for one is a sign the writer count is
being treated as a tuning parameter again — which is the defect ADR-052 exists
to remove, not a configuration gap.

## Out of Scope

- Routing any read onto the reader handle — that is T5
- Giving `internal/mcptest` a reader handle (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
- 2026-09-04 · 8648944* · exit 1 · `set -o pipefail …` · acceptance-sha256:cf21cbdd4f51b7a9a6e1bbdeae2211acff21d07fa1db63117657db4f92958999 · ms:628
  ```
  --- last 10 line(s) of stdout (of 11 after folding 11 raw)
  cmd/server/dbwiring_test.go:39:12: undefined: openReaderDB
  cmd/server/dbwiring_test.go:81:27: svc.rdb undefined (type *services has no field or method rdb)
  cmd/server/dbwiring_test.go:83:91: svc.rdb undefined (type *services has no field or method rdb)
  cmd/server/dbwiring_test.go:85:12: svc.rdb undefined (type *services has no field or method rdb)
  cmd/server/dbwiring_test.go:89:16: svc.Close undefined (type *services has no field or method Close)
  cmd/server/dbwiring_test.go:92:78: svc.rdb undefined (type *services has no field or method rdb)
  cmd/server/dbwiring_test.go:126:30: svc.Close undefined (type *services has no field or method Close)
  cmd/server/dbwiring_test.go:131:21: svc.rdb undefined (type *services has no field or method rdb)
  FAIL	github.com/atvirokodosprendimai/agentsmemory/cmd/server [build failed]
  FAIL
  ```
- 2026-09-04 · 8648944* · exit 0 · `set -o pipefail …` · acceptance-sha256:cf21cbdd4f51b7a9a6e1bbdeae2211acff21d07fa1db63117657db4f92958999 · ms:14875
- 2026-09-04 · 8648944* · exit 0 · `set -o pipefail …` · acceptance-sha256:cf21cbdd4f51b7a9a6e1bbdeae2211acff21d07fa1db63117657db4f92958999 · ms:12658
- 2026-09-04 · 8648944* · exit 0 · `set -o pipefail …` · acceptance-sha256:cf21cbdd4f51b7a9a6e1bbdeae2211acff21d07fa1db63117657db4f92958999 · ms:11697
- 2026-09-04 · 8648944* · exit 0 · `set -o pipefail …` · acceptance-sha256:ea94b05790c278470f06bf24a5e0069e81b20e571244f27a9fa32a5ed9482ba2 · ms:13099
- 2026-09-04 · 8648944* · exit 0 · `set -o pipefail …` · acceptance-sha256:ea94b05790c278470f06bf24a5e0069e81b20e571244f27a9fa32a5ed9482ba2 · ms:12141
- 2026-09-04 · 8648944* · exit 0 · `set -o pipefail …` · acceptance-sha256:ea94b05790c278470f06bf24a5e0069e81b20e571244f27a9fa32a5ed9482ba2 · ms:11973
