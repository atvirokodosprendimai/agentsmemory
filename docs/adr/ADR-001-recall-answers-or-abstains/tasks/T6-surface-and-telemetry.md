# Task ADR-001-T6: Return the verdict over MCP and record what it was derived from

**Depends-on:** T5
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `am_search` `confidence` field, `search_events` verdict / rerank-score / scored / calibration columns
**Consumes:** `Search` returning a populated `palace.Confidence` (T5), the `palace.Calibration` id it carries (T2)

## Goal

Give the agent the verdict it can act on, and record enough beside it that a recorded verdict can be re-scored later at a different threshold instead of merely counted.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | serialise `confidence` on the `am_search` result and describe it in the tool description |
| `db/migrations/00023_search_event_confidence.sql` | add | four nullable columns, with a down migration that drops them |
| `internal/palace/recallstats.go` | edit | carry the verdict, the raw rerank score, its presence and the calibration id into `recordSearch` |
| `internal/mcpserver/search_test.go` | add | pin the field's presence and shape |
| `internal/palace/recallstats_test.go` | edit | pin that all four columns round-trip, including the unset case |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestSearchResultCarriesConfidence` asserting `am_search` returns the field with its four documented values and the numbers behind them, and `TestSearchEventRecordsVerdict` asserting all four columns round-trip including when nothing was scored. Commit red.
2. Add migration `00023_search_event_confidence.sql`: `verdict` TEXT, `rerank_score` REAL, `rerank_scored` INTEGER, `calibration_id` TEXT — all nullable, up and down. The comment must say why these are not the existing columns: `top_score` is the **fused** score of the best hit, not the cross-encoder's, and `reranked` says a cross-encoder ordered the **page**, not that it scored the **top hit**. Calibrating against either would calibrate against the wrong number, which is the mistake this ADR is about.
3. Thread all four values into `searchEventRow` and `recordSearch`, keeping it best-effort: a telemetry failure may never fail a search.
4. Serialise `confidence` in the `am_search` handler as an object carrying the verdict, the top score, whether it was scored, both boundaries and the calibration id, so a consumer that disagrees with our operating point can apply its own.
5. Extend the tool description to say what `unknown` means — an uncalibrated, stale or unscored palace, not a low-confidence answer — and that the verdict describes the top result rather than the whole page.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'set -e; gofmt -l internal/palace internal/mcpserver | grep -q . && exit 1; go vet ./...; go test ./internal/mcpserver/ ./internal/palace/ -run "TestSearchResultCarriesConfidence|TestSearchEventRecordsVerdict|TestRecallStats" -count=1 2>&1 | tee /tmp/adr001-t6.out; if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr001-t6.out; then exit 1; fi'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSearchResultCarriesConfidence` | `internal/mcpserver/search_test.go` | the wire surface exposes the verdict and every input it was derived from | — |
| `TestSearchEventRecordsVerdict` | `internal/palace/recallstats_test.go` | all four columns round-trip, including unset | — |

## Invariants

- Every column is nullable and no read path requires one, so the down migration is safe at any time.
- Existing `am_search` consumers that ignore the new field see no change.
- A recorded row identifies the calibration it was judged under; rows from before and after a re-calibration are never pooled as one population.

## Risks

- Four more values on the hottest write path in the telemetry table. Mitigation: same row, one short string, one float, one int, one short id; `recordSearch` is already best-effort and must stay so.
- The calibration id makes the operating point visible in a table an operator can read, which invites reading verdict counts as a quality metric. They are not one until the recorded scores are re-scored against labels — the ADR says so, and nothing here computes a rate.

## Stop Condition

Stop if adding the columns requires rewriting existing rows; they must be nullable and additive.

## Out of Scope

- Reading the recorded scores back to re-calibrate from production traffic (deferred: docs/adr/BACKLOG.md)

## Verification Log

## Mutation Log
