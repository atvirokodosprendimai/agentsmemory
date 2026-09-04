# Task ADR-054-T3: The to-write list is built from the searches nobody's hook made

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `am_recall_stats` per-wing `hook_searches`; `unanswered` and `suggestions` over origin-less rows
**Consumes:** `search_events.origin` (T1); hooks declaring `hook:<name>` (T2)
**Data dependency:** hermetic for the fence; the sign-off's re-measurement needs the live local palace after a deploy carrying T1–T3, and a window that begins at that deploy
**Proof map:** v1
**Rests-on:** `the unanswered scan excludes hook rows`, `hook_searches counts them`

## Goal

`RecallStats` builds `unanswered` and `suggestions` over rows whose origin does not start with `hook:`, keeps every row in the per-wing counts, and reports `hook_searches` per wing.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/recallstats.go` | edit | the unanswered query gains `AND origin NOT LIKE 'hook:%'`; `WingRecall` gains `HookSearches`; the per-wing aggregate counts them |
| `internal/mcpserver/admin.go` | edit | `hook_searches` named in the `recall_stats` tool description on a word boundary, so `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` can see it and a caller can discover it |
| `internal/palace/recallstats_origin_test.go` | edit | the two failing tests |
| `docs/adr/ADR-054-a-search-records-who-asked.md` | edit | the Follow-ups entry records the re-measurement's date, window and top entries |

## Ordered Steps

1. [S1] Write `TestSuggestionsHoldNoHookRecalls` and `TestHookSearchesAreCountedPerWing` and run them red: seed three searches with origin `hook:recall`, two with `''`, all unanswered; the first asserts only the two reach `unanswered` and `suggestions`, the second asserts the wing reports `searches: 5, hook_searches: 3`.
2. [S2] Add the predicate and the count; run the fence green.
3. [S3] Name `hook_searches` in the tool description; run `go test ./internal/mcpserver/ -run TestEveryOmitemptyWireKeyInThisPackageIsDescribed`. `[proof: acceptance]`
4. [S4] After the deploy carrying T1–T3, call `am_recall_stats(hours: <hours since deploy>, wing: "*")` on the local palace and record in the ADR's Follow-ups: the window, `hook_searches` for `(unscoped)`, and whether any `suggestions` entry is machine-shaped. `[proof: human: a person reads the top ten and judges whether each is a question]`

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ -run 'TestSuggestionsHoldNoHookRecalls|TestHookSearchesAreCountedPerWing' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./internal/palace/ ./internal/mcpserver/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestSuggestionsHoldNoHookRecalls` | `internal/palace/recallstats_origin_test.go` | a `hook:` row never reaches `unanswered` or `suggestions`; an origin-less one does | — | S1, S2 |
| `TestHookSearchesAreCountedPerWing` | `internal/palace/recallstats_origin_test.go` | the per-wing `searches` keeps every row and `hook_searches` counts the `hook:` ones | — | S1, S2 |
| `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` | `internal/mcpserver/omitempty_test.go` | existing gate; `hook_searches` is named in the description | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two new tests |
| 2 — something selects it | the predicate in the unanswered query; the mutant is deleting `AND origin NOT LIKE 'hook:%'`, which turns `TestSuggestionsHoldNoHookRecalls` red |
| 3 — the caller can discover it | the tool description names `hook_searches`; the omitempty gate |
| 4 — it is used | S4's re-measurement on the live palace, recorded in the ADR |

## Mutation Log

## Invariants

- Per-wing `searches` and `answered` keep every row; only the two lists narrow.
- Rows with `origin = ''` from before T1 are treated as a person's; nothing rewrites them.

## Risks

- The re-measurement is taken over a window that still contains pre-T1 rows and reads as "still polluted" — the sign-off names a window that begins at the deploy.

## Stop Condition

Stop and put it to the owner if, after a full window past the deploy, `suggestions` still carries machine-shaped entries with `hook_searches` non-zero: that means a machine caller reaches the palace by a route T2 did not label, and the record needs it named before a shape rule is considered.

## Out of Scope

- A shape heuristic (deferred: docs/adr/BACKLOG.md — "A shape heuristic for machine recalls needs its false-negative rate first").

## Verification Log
