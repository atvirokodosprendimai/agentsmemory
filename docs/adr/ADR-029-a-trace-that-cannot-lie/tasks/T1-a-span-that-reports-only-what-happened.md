# Task ADR-029-T1: A span that reports success only for work that succeeded

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (six sites, three packages)
**Owner:** unassigned
**Produces:** `telemetry.ReasonTimeout`; `Repo.recordSearch` returning `error`; honest outcomes on `am.search.record`, `am.search.evidence`, `am.search.rerank` and the `am.tool` span over `am_search`
**Consumes:** none
**Data dependency:** hermetic

## Goal

Six spans stop asserting success over work that failed, was cut off by the operator's own budget, or did nothing. No ranking changes and no response shape changes; only what the instrument reports about itself.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/telemetry/telemetry.go` | edit | `ReasonTimeout` — the vocabulary cannot express a budget expiry today, so a timeout is forced onto `ReasonError` alongside a dead dependency |
| `internal/palace/recallstats.go` | edit | `recordSearch` must return its error so the caller has a value to branch on. It keeps swallowing it for control flow — that invariant is correct and is not what is being changed |
| `internal/palace/service.go` | edit | the record, rerank and evidence stage outcomes; `errors.Is(err, context.DeadlineExceeded)` at the rerank error branch |
| `internal/palace/evidence_test.go` | edit | **found by review**: `semanticRerankDocuments` already has a two-value caller here, so widening its return without touching this file does not compile |
| `internal/palace/evidence.go` | edit | `semanticRerankDocuments` returns how many documents it actually re-evidenced, so the caller can tell a full pass from a no-op |
| `internal/mcpserver/drawers.go` | edit | the anchor lookup's discarded error reaches the tool span instead of vanishing under `if err == nil` |
| `internal/mcpserver/emptywing.go` | edit | separate "the lookup failed" from "the wing has content"; today both return `"", nil` |
| `internal/palace/spantruth_test.go` | add | the four palace-side outcomes, each driven through a fixture that forces the failure |
| `internal/mcpserver/spantruth_test.go` | add | the two boundary-side annotations |

The reachability line here is the OUTCOME ARGUMENT, not the span. Every one of these spans already exists and already ends; deleting the fix means passing `telemetry.Ran` again, which is a one-token change — so each mutant is unusually direct and unusually convincing.

## Ordered Steps

1. **TDD red.** Write the failing tests first. `TestRecordStageReportsAWriteThatFailed` drives a `Search` whose `search_events` INSERT fails and asserts `am.search.record` ends `failed_open` with `reason=error`. `TestRerankTimeoutIsNotReportedAsAnOutage` expires a real rerank budget and asserts `reason=timeout`, not `error`. `TestEvidenceReportsHowManyDocumentsItActuallySelected` asserts `am.evidenced` is present and is 0 when the semantic selector produced no window. `TestRerankSaysWhetherItReorderedAnything` feeds an all-equal score vector and asserts `am.reordered=false`. Confirm all four red.
2. Add `telemetry.ReasonTimeout = "timeout"` with a doc comment naming the distinction it carries (operator budget vs dependency failure).
3. Change `Repo.recordSearch` to return `error`. Leave its documented swallow-for-control-flow behaviour intact and say so in the comment — the caller still refuses to act on it, and only the span reads it.
4. Branch the record stage on that error: `failed_open` + `ReasonError` on failure, `Ran` on success. A failed statistics write must not fail the recall.
5. Classify the rerank error: `errors.Is(err, context.DeadlineExceeded)` selects `ReasonTimeout`, everything else keeps `ReasonError`. The fallback is today's behaviour, so a misclassification degrades rather than lies in a new way.
6. Have `semanticRerankDocuments` return the count of documents it actually re-evidenced. When that count is zero, end `am.search.evidence` as `Bypassed` with `ReasonEmpty` rather than `Ran` — the early return at `evidence.go:83` hands back the lexical documents verbatim, which is a bypass wearing a success outcome. Carry the count as `am.evidenced` on the ran path.
7. Record `am.reordered` on the rerank span by comparing the head order before and after `BlendRerank`. `normalizeScores` maps an all-equal input to 1.0 at every position, so an identical-score response reproduces the fused order exactly while reporting `ran` — the outcome is right and the conclusion it invites is wrong. One boolean settles it.
8. Stop discarding the anchor error in `drawers.go`: annotate the tool span (`am.anchors_failed`) so a page that silently lost every `stale` flag is visible in the trace.
9. Separate the two causes in `emptyWingNote` and annotate the failure, so a zero-hit page that lost its explanation is distinguishable from a wing that genuinely holds memories.
10. Run the acceptance fence and confirm it is green only after steps 2–9.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/telemetry/ ./internal/palace/ ./internal/mcpserver/
  go test ./internal/palace/ -run "^(TestRecordStageReportsAWriteThatFailed|TestRerankTimeoutIsNotReportedAsAnOutage|TestEvidenceReportsHowManyDocumentsItActuallySelected|TestRerankSaysWhetherItReorderedAnything)$" -count=1 -v 2>&1 | tee /tmp/t1.out
  go test ./internal/mcpserver/ -run "^(TestAnchorFailureReachesTheToolSpan|TestEmptyWingLookupFailureIsNotSilence)$" -count=1 -v 2>&1 | tee -a /tmp/t1.out
  grep -qE "^--- PASS: TestRecordStageReportsAWriteThatFailed \(" /tmp/t1.out
  grep -qE "^--- PASS: TestRerankTimeoutIsNotReportedAsAnOutage \(" /tmp/t1.out
  grep -qE "^--- PASS: TestEvidenceReportsHowManyDocumentsItActuallySelected \(" /tmp/t1.out
  grep -qE "^--- PASS: TestRerankSaysWhetherItReorderedAnything \(" /tmp/t1.out
  grep -qE "^--- PASS: TestAnchorFailureReachesTheToolSpan \(" /tmp/t1.out
  grep -qE "^--- PASS: TestEmptyWingLookupFailureIsNotSilence \(" /tmp/t1.out
  ! grep -qE "no tests to run|^FAIL" /tmp/t1.out
  go test ./internal/telemetry/ ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1
'
```

The six `grep -q -- "--- PASS:"` lines are what make this fence red before the work: a `-run` filter matching nothing exits 0 with a cheerful summary, and the greps then fail on the absent PASS lines. The new units run alone first and the package suites run second under `set -e`, so the existing green suites cannot carry the verdict on their own.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRecordStageReportsAWriteThatFailed` | `internal/palace/spantruth_test.go` | a failed `search_events` INSERT ends `am.search.record` as `failed_open`, and the recall still succeeds | — |
| `TestRerankTimeoutIsNotReportedAsAnOutage` | `internal/palace/spantruth_test.go` | an expired rerank budget yields `reason=timeout`, a dead reranker still yields `reason=error` | — |
| `TestEvidenceReportsHowManyDocumentsItActuallySelected` | `internal/palace/spantruth_test.go` | `am.evidenced` exists and a zero-window shortlist ends `bypassed`, not `ran` with `am.pool=N` | — |
| `TestRerankSaysWhetherItReorderedAnything` | `internal/palace/spantruth_test.go` | an all-equal score vector records `am.reordered=false` while a spread vector records `true` | — |
| `TestAnchorFailureReachesTheToolSpan` | `internal/mcpserver/spantruth_test.go` | a failing anchor lookup is visible on `am.tool` instead of ending `ran` with a silently unflagged page | — |
| `TestEmptyWingLookupFailureIsNotSilence` | `internal/mcpserver/spantruth_test.go` | a failed `WingIsEmpty` is distinguishable from a wing with content | — |

Each test pairs the failure case with its control. `TestRerankTimeoutIsNotReportedAsAnOutage` asserting only `reason=timeout` would pass an implementation that reports every rerank error as a timeout, which is the same defect pointing the other way.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | each new attribute and reason is read back off a recorded span |
| 2 — something selects it | the outcome argument at each `End` call — mutation: pass `telemetry.Ran` again and the fence goes red |
| 3 — the caller can discover it | the new reason and attributes appear in a dumped tree, which is where a trace reader looks |
| 4 — it is used | confirmed against the deployed container: a live `am.search` tree carrying the new attributes, taken after `scripts/redeploy.sh` |

## Mutation Log

_(populated by `adr-verify --mutant` during execution)_

## Invariants

- No ranking changes. Every survivor, score and order is byte-identical before and after this task.
- No response shape changes. `recordSearch` still swallows its error for control flow; a failed statistics write still cannot fail a recall.
- No new configuration surface: no flags, no environment variables, no tool schema changes.
- ADR-025's privacy rule holds: every attribute added here is a count, a boolean or a bounded enum. No query text, memory content or wing name reaches a span.

## Risks

- `errors.Is(err, context.DeadlineExceeded)` can also match a caller-cancelled context. The fallback stays `ReasonError`, so a misclassification degrades to today's behaviour rather than to a new wrong answer, and the paired control test pins both directions.
- Changing `recordSearch`'s signature touches a function whose comment explicitly says the caller ignores the return. The comment is amended in the same edit to say what changed and what did not, because a file whose code and comment disagree reads as current in both halves.

## Stop Condition

Stop and ask if fixing the anchor lookup cannot be done without changing the `am_search` response shape. Surfacing the failure to the CALLER — rather than to the trace — is a contract change and belongs in its own record.

## Out of Scope

- `am.has_wing`'s three meanings. The capture has to happen at the MCP boundary and is T2's job.
- Telling the CALLER that anchors failed. This task makes it visible in the trace only; the response-shape question is receipted in `BACKLOG.md` §"From ADR-029".
- Backend identity on the span (deferred: docs/adr/BACKLOG.md §"From ADR-029")

## Verification Log
