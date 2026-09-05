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
| `clients/claude-code/digest.go` | add | `renderDigest(page searchPage, budget int) string` and the page types it reads (identity, wing, room, content_date, stale, regions, facts) |
| `clients/claude-code/mcpcall.go` | edit | the `--digest` int flag on `mcp search`; when set, the response is decoded and `renderDigest` is printed instead of the JSON — this is the line that SELECTS the renderer, and the CLI test deletes it |
| `clients/claude-code/digest_test.go` | add | `TestTheDigestFitsItsBudget` (fixture: three 88k-char hits with regions and two facts; budget 1,600) and `TestTheDigestIsSelectedByTheFlag` (drives the real CLI against an httptest MCP server) |
| `clients/claude-code/README.md` | edit | the `mcp search` section names `--digest` and shows one rendered example |

## Ordered Steps

1. [S1] Write `TestTheDigestFitsItsBudget` red: a fixture page whose raw JSON is ~6k chars renders to ≤1,600 chars, every hit present is whole (identity line, wing/room line, region line), the trailing line names the withheld count and the query, and each fact is one `subject → predicate → object` line. One fixture hit opens with a `SESSION:…|PROJ:…` header whose `regions[0]` is contained in its identity and whose `regions[1]` is body text: the content line must be `regions[1]` (review of #268 measured this shape on half the sampled hits). And `TestTheDigestIsSelectedByTheFlag` red: the real CLI with `--digest 1600` against a fake MCP server prints text, not JSON.
2. [S2] Implement `renderDigest` and wire the flag. [proof: mutation]
3. [S3] README example. [proof: human: the reviewer reads the example beside the code that renders it]
4. [S4] Mutants, one per Rests-on mechanism: render a hit past the budget (cut mid-line); drop the trailing line; render facts as raw JSON; always render `regions[0]`. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestTheDigestFitsItsBudget$|TestTheDigestIsSelectedByTheFlag$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheDigestFitsItsBudget` | `clients/claude-code/digest_test.go` | budget honoured hit-by-hit, trailing count line, one line per fact | — | S1, S2 |
| `TestTheDigestIsSelectedByTheFlag` | `clients/claude-code/digest_test.go` | the flag reaches the renderer through the real CLI | — | S1, S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestTheDigestFitsItsBudget` |
| 2 — something selects it | the `--digest` branch in `runRemoteMCP`; `TestTheDigestIsSelectedByTheFlag` drives the real CLI and fails when the branch is deleted |
| 3 — the caller can discover it | `mcp search --help` lists `--digest`; README example |
| 4 — it is used | T2 makes both hooks callers; before T2, nothing measures this |

## Mutation Log

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
