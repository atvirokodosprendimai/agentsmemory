# Task ADR-004-T2: Harden and grow the temporal pairs

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `verified-pair meta` (`pair_candidates`, `verified_pairs`, `judge` in `caseFileMeta`, returned by `readCases`), `--verify-pairs`
**Consumes:** `EvalCase.Distractor`

## Goal

Make every temporal case a pair that could actually mislead: close enough in embedding space to be a real trap, judged to be the same fact at an earlier state, counted honestly when it is not, and carrying a verification record that survives being read back.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | `OlderNeighbor` gains a distance ceiling (a similarity floor, expressed the way the vector store measures) and returns the pair's distance, so the least-distant older drawer stops passing as a superseded version by default |
| `cmd/server/eval.go` | edit | pair verification and its prompt, `--verify-pairs`, sampling that continues until the case floor or the dated population is exhausted, the yield recorded in `caseFileMeta`, and a reader that returns every meta record instead of skipping the provenance line |
| `internal/palace/eval_test.go` | edit | pin the ceiling, and that the existing three filters still hold |
| `cmd/server/eval_test.go` | edit | pin that a pair the judge rejects never reaches the case file, and that the yield is recorded |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestOlderNeighborFloorRejectsDistantPair` in `internal/palace/eval_test.go`, asserting a strictly-older different-source neighbour beyond the ceiling is not returned; and `TestPairVerifiedRejectsDrift`, `TestPairVerifiedJudgeErrorDropsPair` and `TestPairVerifiedMetaSurvivesRead` in `cmd/server/eval_test.go`. Commit them red.
2. Add a `maxDistance float64` parameter to `Service.OlderNeighbor` — 0 keeps today's behaviour, so the existing `TestOlderNeighbor*` tests pass a permissive value and stay meaningful — and return the accepted pair's distance alongside the drawer.
3. Extend the doc comment: the three existing filters say what a pair must *not* be; the ceiling says what it must be, and without it "nearest older neighbour" is a claim about the corpus's sparsity rather than about supersession.
4. Add `evalPromptPairCheck` and `verifyPair` in `cmd/server/eval.go`, mirroring `verifyAbsent`'s shape: show the judge both texts, ask whether the older one records an earlier state of the same fact, drop the pair on anything but a clear yes. Diverge from it on ONE point and say so in the doc comment: `verifyAbsent` prints `kept UNVERIFIED` and keeps the case when the judge call itself errors (`cmd/server/eval.go:539`), which is tolerable for a label that only shifts a distribution. Here the verified count is what T5's floor is checked against, so a judge error drops the pair and increments the rejection count — a floor met by pairs nobody verified is not a floor.
5. Keep sampling dated drawers until `--n` accepted cases exist or the dated population is exhausted, and write `pair_candidates`, `verified_pairs` and `judge` into `caseFileMeta` so a later run can tell a hardened case file from an old one.
6. Make the record readable: `readCases` currently drops the provenance line by substring match and hands back cases only, so nothing downstream can see whether pairs were verified. Return every meta record alongside the cases — a merged set carries one per file, and the gate must refuse when ANY file contributing temporal cases lacks one — parse the line as JSON rather than by substring, and decode both meta and cases with `DisallowUnknownFields`. A field this binary does not understand is a case file it cannot score, and silently ignoring it is how an unhardened file passes as hardened.
7. Print the pair's distance on the existing progress line beside the two dates — how close the trap was is the one thing that line cannot currently show.
8. Run the acceptance command; the new tests green and the four existing `OlderNeighbor` tests still green.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestOlderNeighbor|TestPairVerified" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOlderNeighborFloorRejectsDistantPair` | `internal/palace/eval_test.go` | a neighbour beyond the distance ceiling is refused, and 0 means no ceiling | — |
| `TestPairVerifiedMetaSurvivesRead` | `cmd/server/eval_test.go` | the pair provenance — candidates considered, pairs the judge confirmed, which judge — survives a replay | — |
| `TestPairVerifiedRejectsDrift` | `cmd/server/eval_test.go` | a judge-rejected pair is dropped and counted in the meta, never filed as a case | — |
| `TestPairVerifiedJudgeErrorDropsPair` | `cmd/server/eval_test.go` | a judge that errors drops the pair and never inflates `verified_pairs`, unlike `verifyAbsent` | — |
| `TestPairVerifiedMetaSurvivesRead` | `cmd/server/eval_test.go` | every meta record of a merged case set is returned, and an unknown field is an error rather than a silent drop | — |

## Invariants

- With no ceiling and `--verify-pairs=false`, generation reproduces today's pairs exactly, so previously generated case files stay comparable.
- No case reaches the file with an unverified pair while `--verify-pairs` is on, which is the default for `--style temporal`.
- Verification never rewrites a question or a pair; it accepts or rejects, and a judge error is a rejection.
- `verified_pairs` counts only pairs a judge actually answered on: it is the number T5's floor is checked against, so it can never include one the judge failed to reach.
- Every meta record in a merged case set survives the read; none is dropped, and no unrecognised field is silently ignored.

## Risks

- Verification may reject most candidates and starve the set. That is a finding about the corpus, reported as yield — not a reason to loosen the judge.
- Strict decoding turns a newer case file into a hard error for an older binary. That is the intent: the alternative is scoring a file whose verification fields this binary cannot see, which is precisely the silent failure the gate is built to refuse.

## Stop Condition

Stop and ask if accepted pairs fall below roughly one in five candidates: at that rate the corpus rather than the rule is the problem, and the ADR's declared response (more dated corrections, not a looser gate) needs a human decision before more generation time is spent.

## Out of Scope

- The rates and the report — T1 computes, T3 prints.
- Deciding what counts as enough verified pairs — T5 owns the floor; this task only makes the count honest.
- Mining pairs from real queries instead of dated drawers (deferred: docs/adr/BACKLOG.md)
- Measuring the judge's own agreement with a human label (deferred: docs/adr/BACKLOG.md)

## Verification Log
- 2026-08-20 · d0b37a9 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestOlderNeighbor|TestPairVerified" -count=1'`
- 2026-08-20 · f273396 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestOlderNeighbor|TestPairVerified" -count=1'`
- 2026-08-20 · d06e9e6 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestOlderNeighbor|TestPairVerified" -count=1'`
- 2026-08-28 · d006e01 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestOlderNeighbor|TestPairVerified" -count=1'` · acceptance-sha256:2666e828c292acb5f06d075a500c46014ae14026bda71cae0c2d39d93fd7ceb0

## Mutation Log
- 2026-08-28 · ee1bfa6 · mutant killed · exit 1 · `internal/palace/eval.go` · an undated drawer has no older neighbour by construction: returning no-pair silently is indistinguishable from a genuine miss and hides that the caller sampled the wrong population · acceptance-sha256:2666e828c292acb5f06d075a500c46014ae14026bda71cae0c2d39d93fd7ceb0
