# Task ADR-001-T5: Derive the confidence verdict inside Search

**Depends-on:** T4
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `palace.Confidence` type, `palace.Verdict` constants, `Search` returning a populated confidence
**Consumes:** `palace.Service.WithCalibration` and the confirmed canary (T4)

## Goal

Compare the top hit's cross-encoder score against the two calibrated boundaries and return a four-valued verdict, without changing which hits are returned or their order.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/palace.go` | edit | `Confidence` struct (`Verdict`, `TopScore`, `Scored`, `AnswerAt`, `RefuseBelow`, `CalibrationID`) and the four verdict constants |
| `internal/palace/service.go` | edit | derive it after `applyRerank`; `unknown` when no calibration is loaded, the canary is unconfirmed, or the top hit was not scored |
| `internal/palace/service_test.go` | edit | pin all four verdicts, the band boundaries, and that hits are untouched |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestConfidenceVerdicts` covering answered / no_answer / uncertain / unknown including both boundary equalities, `TestConfidenceZeroScoreIsNotUnset` asserting a top hit scored exactly 0.0 with `Reranked` true still produces a real verdict rather than `unknown`, and `TestConfidenceNeverFiltersHits` asserting the returned page is identical with and without a calibration, and `TestConfidenceUnknownOutsideCalibratedPool` asserting a request whose `limit × hybridCandidateMultiplier` exceeds the calibrated rerank pool gets `unknown` while its hits are unchanged. Commit red.
2. Add the `Confidence` type and the four constants to `internal/palace/palace.go`. Both boundaries are `*float64` on the calibration and their presence — not a comparison against 0 — decides whether a verdict can be derived at all; a sigmoid backend can and does return 0.0 as an ordinary score, which is the same reason `SearchHit.Reranked` exists.
3. In `Search`, after `applyRerank`, derive the verdict from the top hit: `answered` when its score ≥ `answer_at`, `no_answer` when its score < `refuse_below`, `uncertain` in between, and `unknown` when no calibration is loaded, the canary is unconfirmed, the page is empty, the top hit's `Reranked` is false, or the request's `limit × hybridCandidateMultiplier` exceeds the calibration's rerank pool — beyond that point fusion decides which candidates the cross-encoder ever sees, so the top hit is not the one the curve was measured on. When the two boundaries are equal there is no band and the verdict is two-valued — the calibration file records that, and nothing here invents a width.
4. Carry the score, the presence flag, both boundaries and the calibration id on the `Confidence` so a consumer that disagrees with our operating point can apply its own bar, and so T6 can record them.
5. Return it alongside the hits without altering their content, order or count.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'set -e; gofmt -l internal/palace | grep -q . && exit 1; go vet ./...; go test ./internal/palace/ -run "TestConfidence" -count=1 2>&1 | tee /tmp/adr001-t5.out; if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr001-t5.out; then exit 1; fi'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestConfidenceVerdicts` | `internal/palace/service_test.go` | each of the four verdicts is produced in its own condition, boundaries included | — |
| `TestConfidenceZeroScoreIsNotUnset` | `internal/palace/service_test.go` | presence decides, not a comparison against zero | — |
| `TestConfidenceNeverFiltersHits` | `internal/palace/service_test.go` | the verdict annotates, never suppresses | — |
| `TestConfidenceUnknownOutsideCalibratedPool` | `internal/palace/service_test.go` | a caller-set `limit` that changes which candidates are cross-encoded costs the verdict, not the page | — |

## Invariants

- The returned hits are byte-identical with and without a configured calibration.
- `unknown` is returned whenever the score's presence is false — never inferred from a zero score.
- The verdict is derived from the same statistic the curve was built on: the production top-1's cross-encoder score, over a candidate set the cross-encoder scored in full.

## Risks

- The gate judges the top hit, and on this corpus the gold is top-1 for only about 75% of answerable questions. A page whose answer sits at rank 2–5 behind a confident-looking wrong hit can therefore be scored either way — the verdict describes the top result, not the page, and the ADR's Risks table says so where an operator reads it.
- A page whose top hit fell outside the rerank pool has no score; `Reranked` already distinguishes this, and the verdict must be `unknown` rather than `no_answer`.

## Stop Condition

Stop if deriving the verdict requires a knob that is not in the calibration file — the band, both boundaries and their provenance are T2's output, and inventing a width here would reproduce the inherited-constant failure this ADR exists to correct.

## Out of Scope

- Surfacing it over MCP and recording it — that is T6's job.
- Scoring the whole page rather than the top hit (deferred: docs/adr/BACKLOG.md)

## Verification Log

## Mutation Log
