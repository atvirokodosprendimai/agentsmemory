# Task ADR-060-T1: `am_search` returns a thin, truthful page when `ids_only` is true

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `ids_only` argument on `am_search`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the thin view carries no content`, `the argument is what selects the thin view`, `the thin hit says it is partial`, `a thin page over the budget withholds nothing`

## Goal

`am_search` with `ids_only: true` returns per hit only the identity and the numbers a caller ranks and fetches by, says on every hit that it is partial, keeps facts and `search_id`, and is at most half of the full page's bytes on a real-shaped fixture (the production tenth needs 88k-character hits with regions, which a hermetic fixture cannot honestly reproduce).

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | `searchHitIDView` built from `newSearchHitView(h)`; `ids_only` read via `req.GetBool` — the line that SELECTS the branch; the boolean declared with a description naming what a hit then carries and `am_get_drawer` as the second call; `snippet_chars`'s description points at it |
| `internal/mcptest/idsonly_test.go` | add | `TestAnIdsOnlyPageCarriesNoContentAndSaysSo` (drives the real tool over the harness) |
| `README.md` | edit | the `am_search` row names the mode |

## Ordered Steps

1. [S1] Write `TestAnIdsOnlyPageCarriesNoContentAndSaysSo` red: file three drawers (one over 1,600 runes so it chunks), search with and without `ids_only`; assert the thin page has the same `count` and `search_id` shape, every thin hit lacks `content`, `regions` and `content_coverage`, carries `id`, `memory_id`, `wing`, `room`, `identity`, `blended_score`, `content_truncated: true` and a positive `content_length`, its keys are a subset of the full hit's keys plus nothing, and the thin page's JSON is at most half of the full page's bytes. And that `ids_only: false` (and omitted) still returns `content`.
2. [S2] Implement the view and the branch; describe the argument; README row. [proof: mutation]
3. [S3] Mutants, one per Rests-on: render content on the thin hit; ignore the argument (always full); report `content_truncated: false` on the thin hit; delete the `withheld = 0` reset. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/mcptest/ -run 'TestAnIdsOnlyPageCarriesNoContentAndSaysSo$|TestAThinPageOverTheBudgetWithholdsNothing$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./internal/mcpserver/ -run 'TestEveryArgumentAHandlerReadsIsDeclared$|TestEveryOmitemptyWireKeyInThisPackageIsDescribed$' -count=1 2>&1 | tee /tmp/acc2.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc2.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAnIdsOnlyPageCarriesNoContentAndSaysSo` | `internal/mcptest/idsonly_test.go` | the thin page's shape, truthfulness and size against the full page on one fixture | — | S1, S2 |
| `TestAThinPageOverTheBudgetWithholdsNothing` | `internal/mcptest/idsonly_test.go` | three 22k-rune hits at snippet_chars=20000 make the full page withhold one; the thin page of the same hits reports no withheld (review of #277: the reset was an equivalent mutant under the small fixture) | — | S2 |
| `TestEveryArgumentAHandlerReadsIsDeclared` | `internal/mcpserver/argreach_test.go` | the handler reads no argument the schema does not advertise (rung 3) | — | S2 |
| `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` | `internal/mcpserver/wirekeys_test.go` | any new omitempty key is named in a description | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the mcptest case drives the real tool |
| 2 — something selects it | `req.GetBool("ids_only", false)` in the handler; the "ignore the argument" mutant proves it |
| 3 — the caller can discover it | the argument's description; the two schema gates in the fence |
| 4 — it is used | nothing measures this yet; the record's Follow-up counts it in `search_events` after a week |

## Mutation Log

- 2026-09-05 · 967f20c* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the thin hit no longer says it is partial, so a caller reads an empty memory as whole · acceptance-sha256:6ed99ac7c7b93327a629978ed4359a5228a67daf8ef36b52b303311e75c5e4c3 · covers:the thin hit says it is partial
- 2026-09-05 · 967f20c* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the argument is read and ignored, so every page is the full page and the mode is unreachable · acceptance-sha256:6ed99ac7c7b93327a629978ed4359a5228a67daf8ef36b52b303311e75c5e4c3 · covers:the argument is what selects the thin view
- 2026-09-05 · 967f20c* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the thin view is built and the full views are sent anyway, so the page carries content it claims not to · acceptance-sha256:6ed99ac7c7b93327a629978ed4359a5228a67daf8ef36b52b303311e75c5e4c3 · covers:the thin view carries no content
- 2026-09-05 · ef531bd* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the thin hit no longer says it is partial (re-recorded after the fence widened) · acceptance-sha256:597b819bd61b6aaa592c035caaa00ae4e0e13e70e3624ab63f40c033b49d0017 · covers:the thin hit says it is partial
- 2026-09-05 · ef531bd* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the argument is read and ignored, so the mode is unreachable (re-recorded) · acceptance-sha256:597b819bd61b6aaa592c035caaa00ae4e0e13e70e3624ab63f40c033b49d0017 · covers:the argument is what selects the thin view
- 2026-09-05 · ef531bd* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the full views are sent under the thin flag (re-recorded) · acceptance-sha256:597b819bd61b6aaa592c035caaa00ae4e0e13e70e3624ab63f40c033b49d0017 · covers:the thin view carries no content
- 2026-09-05 · ef531bd* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · a thin page over the budget reports a withheld hit that is on the page · acceptance-sha256:597b819bd61b6aaa592c035caaa00ae4e0e13e70e3624ab63f40c033b49d0017 · covers:a thin page over the budget withholds nothing

## Invariants

- A page with `ids_only` omitted or false is byte-for-byte what it was before this task.
- The thin hit is built from the full view's value, never from the palace hit directly.
- `count`, `search_id` and the three fact keys are identical between the two modes for one query.

## Risks

- The test's size ratio depends on the fixture; it uses three drawers of which one is over the chunk threshold so the full page renders a window and regions per hit and half is a meaningful bound.

## Stop Condition

Stop if `TestEveryArgumentAHandlerReadsIsDeclared` does not exist under that name — the rung-3 half of the fence would then be a pointer to nothing.

## Out of Scope

- The hooks (they keep the full page and the digest).

## Verification Log
- 2026-09-05 · 967f20c* · exit 1 · `set -o pipefail …` · acceptance-sha256:6ed99ac7c7b93327a629978ed4359a5228a67daf8ef36b52b303311e75c5e4c3 · ms:2696
  ```
  --- last 10 line(s) of stdout (of 58 after folding 58 raw)
      idsonly_test.go:73: thin hit 1 reports content_length 0; a fetch would return more than that
      idsonly_test.go:61: thin hit 2 carries "content"; an ids-only hit must hold none of the memory
      idsonly_test.go:61: thin hit 2 carries "content_coverage"; an ids-only hit must hold none of the memory
      idsonly_test.go:66: thin hit 2 lacks "blended_score", which a caller ranks or fetches by
      idsonly_test.go:66: thin hit 2 lacks "content_length", which a caller ranks or fetches by
      idsonly_test.go:70: thin hit 2 does not say it is partial (content_truncated); a caller reading it as whole reads an empty memory
      idsonly_test.go:73: thin hit 2 reports content_length 0; a fetch would return more than that
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	0.527s
  FAIL
  ```
- 2026-09-05 · 967f20c* · exit 0 · `set -o pipefail …` · acceptance-sha256:6ed99ac7c7b93327a629978ed4359a5228a67daf8ef36b52b303311e75c5e4c3 · ms:2900
- 2026-09-05 · 967f20c* · exit 0 · `set -o pipefail …` · acceptance-sha256:6ed99ac7c7b93327a629978ed4359a5228a67daf8ef36b52b303311e75c5e4c3 · ms:2185
- 2026-09-05 · 967f20c* · exit 0 · `set -o pipefail …` · acceptance-sha256:6ed99ac7c7b93327a629978ed4359a5228a67daf8ef36b52b303311e75c5e4c3 · ms:1752
- 2026-09-05 · ef531bd* · exit 0 · `set -o pipefail …` · acceptance-sha256:597b819bd61b6aaa592c035caaa00ae4e0e13e70e3624ab63f40c033b49d0017 · ms:3533
- 2026-09-05 · ef531bd* · exit 0 · `set -o pipefail …` · acceptance-sha256:597b819bd61b6aaa592c035caaa00ae4e0e13e70e3624ab63f40c033b49d0017 · ms:2392
- 2026-09-05 · ef531bd* · exit 0 · `set -o pipefail …` · acceptance-sha256:597b819bd61b6aaa592c035caaa00ae4e0e13e70e3624ab63f40c033b49d0017 · ms:2017
- 2026-09-05 · ef531bd* · exit 0 · `set -o pipefail …` · acceptance-sha256:597b819bd61b6aaa592c035caaa00ae4e0e13e70e3624ab63f40c033b49d0017 · ms:2144
