# Task ADR-031-T1: Keep the separating score, and report it with an honest denominator

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (one column, one aggregate, two response fields)
**Owner:** unassigned
**Produces:** `search_events.top_rerank_score`; `WingRecall.AvgTopRerank` and `.Reranked`; `avg_top_rerank_score` and `reranked` on `am_recall_stats`
**Consumes:** none
**Data dependency:** hermetic
**Rests-on:** `the rerank mean and its denominator count only searches a cross-encoder ordered`, `the fused average stays a separate number`

## Goal

The cross-encoder score for the best hit reaches the durable row and the operator's report, averaged over the searches a cross-encoder actually ordered.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00026_search_events_top_rerank_score.sql` | add | additive column; the reasoning lives in the migration because that is where a reader meets the schema |
| `internal/palace/recallstats.go` | edit | the row field, the aggregate, and the two `WingRecall` fields |
| `internal/palace/service.go` | edit | the record stage writes `results[0].RerankScore` beside the existing fused score |
| `internal/mcpserver/admin.go` | edit | the rung-3 line: a column no tool reports is a column nobody can act on |
| `internal/palace/recallstats_test.go` | add | the value AND the denominator |

The reachability line is the `am_recall_stats` response, not the column. A stored number nothing surfaces is this repository's signature defect, and writing the column without the report would reproduce it in the very change that documents it.

## Ordered Steps

1. **TDD red.** `TestRerankSignalIsReportedAndNotDilutedByUnrerankedRows` files two reranked answered searches (logits 4.0 and 2.0) and one answered search no cross-encoder touched, then asserts `Reranked == 2` and `AvgTopRerank == 3.0`. Red before the column exists.
2. Add the migration. Additive, `NOT NULL DEFAULT 0`, with the reasoning in the file.
3. Add `TopRerankScore` to `searchEventRow` and write it at the record stage.
4. Aggregate it over `hits > 0 AND reranked = 1` only, and carry the matching count.
5. Report both on `am_recall_stats`.
6. Run the fence.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/palace/ ./internal/mcpserver/
  go test ./internal/palace/ -run "^(TestRerankSignalIsReportedAndNotDilutedByUnrerankedRows)$" -count=1 -v 2>&1 | tee /tmp/t1.out
  grep -qE "^--- PASS: TestRerankSignalIsReportedAndNotDilutedByUnrerankedRows \(" /tmp/t1.out
  if grep -qE "no tests to run|^FAIL" /tmp/t1.out; then exit 1; fi
  go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1
'
```

Both the selector and the PASS grep are anchored. An unanchored pair is satisfied by any test whose name merely starts with the required one — demonstrated with a decoy on 2026-08-25, in fences written by the author who had just asked a reviewer to check for exactly that.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRerankSignalIsReportedAndNotDilutedByUnrerankedRows` | `internal/palace/recallstats_test.go` | the mean is over reranked answered searches only, the count is reported, and the fused average is the near-constant this change works around | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the column is written and read back through `RecallStats` |
| 2 — something selects it | the aggregate's `reranked = 1` filter — mutation: widen it and the fence goes red |
| 3 — the caller can discover it | `am_recall_stats` returns `avg_top_rerank_score` and `reranked` per wing |
| 4 — it is used | the `reranked` count makes adoption measurable: a fleet with no cross-encoder reports 0 and the metric is honestly empty rather than misleadingly zero |

## Mutation Log

_(recorded by `adr-verify --mutant`)_
- 2026-09-07 · 6c55aff2* · mutant killed · exit 1 · `internal/palace/recallstats.go` · the denominator widens to every answered search, so a row no cross-encoder touched contributes a top_rerank_score of 0 — and these are LOGITS, where 0 is mid-range rather than "no match". A healthy wing is dragged toward zero and the number reads as evidence of a broken recall. The same shape as the write-to-read ratio this project already retracted. · acceptance-sha256:4e1b58432c0cd3aab1baca3c2ca47ae1bb5bb917777296e532f45b497d0348e0 · covers:the rerank mean and its denominator count only searches a cross-encoder ordered
- 2026-09-07 · 6c55aff2* · mutant killed · exit 1 · `internal/palace/recallstats.go` · the fused average starts reporting the cross-encoder sum, collapsing the two numbers into one. The whole point of the column is that avg_top_score is an average of an RRF near-constant and cannot separate a working recall from a broken one; if the two fields can silently become the same number, the operator has one signal wearing two names. · acceptance-sha256:4e1b58432c0cd3aab1baca3c2ca47ae1bb5bb917777296e532f45b497d0348e0 · covers:the fused average stays a separate number

## Invariants

- No ranking changes. Every survivor, score and order is identical before and after.
- `top_score` and `avg_top_score` are untouched; their meaning does not move.
- Rows written before the migration are EXCLUDED from the new average, never counted as zeros.
- No backfill. The cross-encoder score was never computed for historical searches, and inventing one would be fabrication.

## Risks

- A logit of 0 is mid-range, not "no match", so diluting the denominator would drag a healthy wing downward while looking like evidence. That is the mutant this task kills.
- A mean of logits over a short window is easy to over-read; the `reranked` count is reported beside it for that reason.

## Stop Condition

Stop and ask if `results[0].RerankScore` turns out not to be populated on some served path — that would mean the value on the wire and the value in the row can disagree, which is a different defect and needs its own record.

## Out of Scope

- An abstention threshold (deferred: `docs/adr/BACKLOG.md` §"From ADR-031")
- Changing `FUSION` away from `rrf` (deferred: `docs/adr/BACKLOG.md` §"From ADR-031")
- Removing `avg_top_score` (deferred: `docs/adr/BACKLOG.md` §"From ADR-031")

## Verification Log
- 2026-09-07 · 6c55aff2* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:4e1b58432c0cd3aab1baca3c2ca47ae1bb5bb917777296e532f45b497d0348e0 · ms:40248
- 2026-09-07 · 6c55aff2* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:4e1b58432c0cd3aab1baca3c2ca47ae1bb5bb917777296e532f45b497d0348e0 · ms:42500
- 2026-09-07 · 6c55aff2* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:4e1b58432c0cd3aab1baca3c2ca47ae1bb5bb917777296e532f45b497d0348e0 · ms:42788
