# Task ADR-034-T2: persist the reason and report it, so a fail-open can be counted

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — schema, domain, MCP surface)
**Owner:** unassigned
**Produces:** `search_events.rerank_skip_reason`; `WingRecall.RerankSkips`; `am_recall_stats` `rerank_skips`
**Consumes:** `applyRerankWith` returning a reason (T1)
**Data dependency:** hermetic

## Goal

The reason is written to `search_events` and reported by `am_recall_stats`, so "what fraction of
recalls served a degraded ranking" becomes a query.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00029_search_events_rerank_skip_reason.sql` | add | nullable TEXT column, with a `-- +goose Down` — additive, following migration `00028` |
| `internal/palace/recallstats.go` | edit | `searchEventRow.RerankSkipReason` field (the row type is UNEXPORTED — this task file called it `SearchEvent`, which does not exist; scouted 2026-08-26); `WingRecall.RerankSkips` map; the aggregate that groups by reason |
| `internal/palace/service.go` | edit | `recordSearch` writes the reason T1 returns — the line that SELECTS it |
| `internal/mcpserver/admin.go` | edit | `am_recall_stats` result carries `rerank_skips` — the line that makes it DISCOVERABLE |
| `internal/palace/recallstats_test.go` | edit | aggregate test |
| `internal/mcpserver/admin_test.go` | edit | the rung-3 test on the tool's actual output |

## Ordered Steps

1. Write the failing tests first: (a) an aggregate over rows with mixed reasons returns the right
   per-reason counts, (b) `am_recall_stats`'s rendered result contains `rerank_skips`. Both RED —
   the column does not exist.
2. Add migration `00029`.
3. Add `RerankSkipReason` to `searchEventRow` (gorm column `rerank_skip_reason`); write it in `recordSearch` from T1's return, at the literal in `Search` that already sets `Reranked: boolToInt(reranked)` (`service.go:1194`).
4. Add `RerankSkips` to `WingRecall` and the grouping aggregate, leaving ADR-031's
   `hits > 0 AND reranked = 1` aggregate untouched.
5. Render `rerank_skips` in `am_recall_stats`.
6. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestRecallStatsCountsWhyRerankingWasSkipped|TestADisabledRerankerAndATimingOutOneAreNotTheSameRow|TestARowFromBeforeThisColumnIsNotAFalseSkip|TestADR031CalibrationAggregateIsUnchanged' -count=1 2>&1 | tee /tmp/acc34c.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc34c.out && go test ./internal/mcpserver/ -run 'TestRecallStatsResultCarriesTheSkipBreakdown' -count=1 2>&1 | tee /tmp/acc34d.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc34d.out && go test ./... -count=1 2>&1 | tee /tmp/acc34e.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc34e.out
```

Each new test runs alone before the full suite, so neither the suite nor the sibling test can carry
the verdict.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRecallStatsCountsWhyRerankingWasSkipped` | `internal/palace/rerankskip_test.go` | rows with `timeout`, `no_reranker` and `""` aggregate into the right per-reason counts | — |
| `TestADisabledRerankerAndATimingOutOneAreNotTheSameRow` | `internal/palace/rerankskip_test.go` | the two cases that are indistinguishable today produce different output — this is the whole ADR | — |
| `TestRecallStatsResultCarriesTheSkipBreakdown` | `internal/mcpserver/recallskips_test.go` | the field is in the TOOL'S RENDERED RESULT, not merely on the struct | — |
| `TestADR031CalibrationAggregateIsUnchanged` | `internal/palace/rerankskip_test.go` | `AvgTopRerank` / `Reranked` are identical before and after rows carry reasons | — |
| `TestARowFromBeforeThisColumnIsNotAFalseSkip` | `internal/palace/rerankskip_test.go` | a NULL (pre-column) row lands in no bucket — "not recorded" is not "nothing skipped" | — |

Shapes the creation path can already produce, decided rather than assumed: rows written by the
PREVIOUS binary have NULL in this column and must aggregate as "unknown" rather than as a skip
(a NULL is "we were not recording yet", which is not the same as "nothing was skipped"); a row with
`hits = 0`; and a row where reranking ran, which must contribute to no skip bucket at all.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestRecallStatsCountsWhyRerankingWasSkipped` |
| 2 — something selects it | `recordSearch` writing the value; mutation: write `""` unconditionally and `TestADisabledRerankerAndATimingOutOneAreNotTheSameRow` goes red |
| 3 — the caller can discover it | `TestRecallStatsResultCarriesTheSkipBreakdown` asserts on the tool's OUTPUT. A field on `WingRecall` that `am_recall_stats` never renders is invisible to every agent, and no test of the struct can see that |
| 4 — it is used | the ADR's first Follow-up: report the first measured fail-open rate in `BACKLOG.md`, including zero |

## Verification Log



- 2026-08-26 · b979a8e* · exit 0 · `go test ./internal/palace/ -run 'TestRecallStatsCountsWhyRerankingWasSkipped|TestADisabledRerankerAndATimingOutOneAreNotTheSameRow|TestARowFromBeforeThisColumnIsNotAFalseSkip|TestADR031CalibrationAggregateIsUnchanged' -count=1 2>&1 | tee /tmp/acc34c.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc34c.out && go test ./internal/mcpserver/ -run 'TestRecallStatsResultCarriesTheSkipBreakdown' -count=1 2>&1 | tee /tmp/acc34d.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc34d.out && go test ./... -count=1 2>&1 | tee /tmp/acc34e.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc34e.out` · acceptance-sha256:e2267c94256cf5f3b1c2e6288735a36a7bf99121635d794a022d85afc2f3bcee

## Mutation Log



- 2026-08-26 · b979a8e* · mutant killed · exit 1 · `internal/palace/recallstats.go` · a pre-column NULL row counts as a skip — "not recorded" silently becomes "degraded" · acceptance-sha256:e2267c94256cf5f3b1c2e6288735a36a7bf99121635d794a022d85afc2f3bcee
- 2026-08-26 · b979a8e* · mutant killed · exit 1 · `internal/palace/recallstats.go` · a recall where reranking RAN counts as a skip, so the column stops measuring degradation · acceptance-sha256:e2267c94256cf5f3b1c2e6288735a36a7bf99121635d794a022d85afc2f3bcee
- 2026-08-26 · b979a8e* · mutant killed · exit 1 · `internal/mcpserver/admin.go` · the breakdown is computed and never rendered, so an agent reading the tool cannot see it · acceptance-sha256:e2267c94256cf5f3b1c2e6288735a36a7bf99121635d794a022d85afc2f3bcee

## Invariants

- ADR-031's calibration aggregate is byte-identical before and after — its own test above.
- The column is nullable and NULL-safe: an old row does not become a false skip.
- Empty string means reranking ran; NULL means the row predates this column. They are different.

## Risks

- Conflating NULL with "no skip" would silently report every historical row as healthy. The explicit
  test above is the guard.
- The migration number could collide with another open branch; renumbered `00027` -> `00029` at merge
  2026-08-26 under ADR-036's recorded allocate-at-merge rule, because #67 landed first and goose
  refuses a pending migration below the maximum applied version. Originally checked against
  this tree and the behind-index branch.

## Out of Scope

- Changing pool or timeout defaults (deferred: docs/adr/BACKLOG.md)
- A dashboard or alert over the new counts (deferred: docs/adr/BACKLOG.md)
- Backfilling historical rows (permanent: the reason was never recorded, so there is nothing to backfill from — NULL is the honest value and the tests pin it.)

## Stop Condition

Stop and ask if the aggregate cannot leave ADR-031's numbers unchanged — that would mean the two
columns are coupled in a way this ADR asserted they are not.
