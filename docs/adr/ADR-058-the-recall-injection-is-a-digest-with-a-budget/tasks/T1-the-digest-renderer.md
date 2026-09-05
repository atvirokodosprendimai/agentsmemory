# Task ADR-058-T1: `mcp search --digest <chars>` renders a bounded plain-text digest of the page

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `--digest` flag on `mcp search` and `renderDigest(page, budget)`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `whole hits within the budget`, `the withheld count line`, `in-wing facts as one line each`, `the content line is not the identity line`

## Goal

`aiagentmemory mcp search --digest 1600` prints, in the server's order, three lines per hit, one line per fact, and a trailing "N more" line when the budget withheld hits — never a hit cut mid-line.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpcli/digest.go` | add | `RenderDigest(page []byte, query string, budget int) string` and the page types it reads (identity, wing, room, content_date, stale, regions, facts) — in the package that owns the print path, so `PrintCallResult` and the digest are siblings |
| `clients/claude-code/mcpcall.go` | edit | the `--digest` int flag on `mcp`, passed as `Invocation.Digest` |
| `internal/mcpcli/mcpcli.go` | edit | `Invocation.Digest`; when set, `Run` prints `RenderDigest` instead of `PrintCallResult` — this is the line that SELECTS the renderer, and the CLI test deletes it |
| `internal/mcpcli/digest_test.go` | add | `TestTheDigestFitsItsBudget` (fixture: three 88k-char hits with regions and two facts; budget 1,600) |
| `clients/claude-code/digest_test.go` | add | `TestTheDigestIsSelectedByTheFlag` (drives the real CLI against an httptest MCP server) |
| `clients/claude-code/README.md` | edit | the `mcp search` section names `--digest` and shows one rendered example |

## Ordered Steps

1. [S1] Write `TestTheDigestFitsItsBudget` red: a fixture page whose raw JSON is ~6k chars renders to ≤1,600 chars, every hit present is whole (identity line, wing/room line, region line), the trailing line names the withheld count and the query, and each fact is one `subject → predicate → object` line. One fixture hit opens with a `SESSION:…|PROJ:…` header whose `regions[0]` is contained in its identity and whose `regions[1]` is body text: the content line must be `regions[1]` (review of #268 measured this shape on half the sampled hits). And `TestTheDigestIsSelectedByTheFlag` red: the real CLI with `--digest 1600` against a fake MCP server prints text, not JSON.
2. [S2] Implement `renderDigest` and wire the flag. [proof: mutation]
3. [S3] README example. [proof: human: the reviewer reads the example beside the code that renders it]
4. [S4] Mutants, one per Rests-on mechanism: render a hit past the budget (cut mid-line); drop the trailing line; render facts as raw JSON; always render `regions[0]`. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/mcpcli/ -run 'TestTheDigestFitsItsBudget$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ -run 'TestTheDigestIsSelectedByTheFlag$' -count=1 2>&1 | tee /tmp/acc2.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc2.out && \
go test ./internal/mcpcli/ ./clients/claude-code/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheDigestFitsItsBudget` | `internal/mcpcli/digest_test.go` | budget honoured hit-by-hit, trailing count line, one line per fact | — | S1, S2 |
| `TestTheDigestIsSelectedByTheFlag` | `clients/claude-code/digest_test.go` | the flag reaches the renderer through the real CLI | — | S1, S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestTheDigestFitsItsBudget` |
| 2 — something selects it | the `--digest` branch in `runRemoteMCP`; `TestTheDigestIsSelectedByTheFlag` drives the real CLI and fails when the branch is deleted |
| 3 — the caller can discover it | `mcp search --help` lists `--digest`; README example |
| 4 — it is used | T2 makes both hooks callers; before T2, nothing measures this |

## Mutation Log

- 2026-09-05 · 9f8b7ad* · mutant killed · exit 1 · `internal/mcpcli/digest.go` · a hit must be admitted only when it fits WHOLE; admitting it when only the reserve fits pushes the digest past its budget · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · covers:whole hits within the budget
- 2026-09-05 · 9f8b7ad* · mutant killed · exit 1 · `internal/mcpcli/digest.go` · a digest that withholds hits must say so, or a short page and a cut page read the same · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · covers:the withheld count line
- 2026-09-05 · 9f8b7ad* · mutant killed · exit 1 · `internal/mcpcli/digest.go` · facts are one readable line each, not JSON objects · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · covers:in-wing facts as one line each
- 2026-09-05 · 9f8b7ad* · mutant inconclusive · exit 1 · `internal/mcpcli/digest.go` · the content line must skip a region the identity already shows; regions[0] on a header-line memory reprints the identity · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · covers:the content line is not the identity line
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-05 · 9f8b7ad* · mutant killed · exit 1 · `internal/mcpcli/digest.go` · the content line must skip a region the identity already shows; regions[0] on a header-line memory reprints the identity · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · covers:the content line is not the identity line

## Invariants

- The server's hit order is kept; withholding drops from the END.
- A hit is whole or absent; no line is truncated.
- The content line is the first region not contained in the identity line; only when every region is contained does it fall back to the first.
- Without `--digest` the JSON page is byte-identical to today.

## Risks

- A hit with no regions renders its identity and wing lines only — acceptable, and the fixture carries one such hit.

## Stop Condition

Stop if the search page's JSON shape lacks a field the digest needs (identity, regions); the fix is then a server change and a different record.

## Out of Scope

- Calling it from the hooks — T2.

## Verification Log
- 2026-09-05 · 9f8b7ad* · exit 1 · `set -o pipefail …` · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · ms:332
  ```
  --- last 5 line(s) of stdout
  # github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli [github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli.test]
  internal/mcpcli/digest_test.go:30:9: undefined: RenderDigest
  internal/mcpcli/digest_test.go:66:11: undefined: RenderDigest
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli [build failed]
  FAIL
  ```
- 2026-09-05 · 9f8b7ad* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · ms:17466
- 2026-09-05 · 9f8b7ad* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · ms:15055
- 2026-09-05 · 9f8b7ad* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · ms:14751
- 2026-09-05 · 9f8b7ad* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · ms:15357
- 2026-09-05 · 9f8b7ad* · exit 0 · `set -o pipefail …` · acceptance-sha256:9d7c5908650d832101d86bfc7a5d5e01a5d9bb75f4011562ebad96568b257b68 · ms:16964
