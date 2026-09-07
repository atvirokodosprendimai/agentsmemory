# Task ADR-030-T2: Ship the measured winner, and pin the property rather than the number

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S (one function, possibly one default)
**Owner:** unassigned
**Produces:** the served rerank-axis normalisation; a property test a maximally-disagreeing cross-encoder must satisfy
**Consumes:** T1's measurement and fixture
**Data dependency:** requires T1's eval run — **this task has no defensible content before it exists**

## Goal

The arm that measured best becomes the served behaviour, and the degenerate case cannot come back silently.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | the rerank axis's normalisation — one call site inside `BlendRerank` |
| `internal/config/config.go` | edit (conditional) | only if the winning arm is the changed weight |
| `internal/palace/blenddegeneracy_test.go` | edit | the pinned test flips from "today ties" to "the cross-encoder decides" |

## Precondition

**Read T1's table before starting.** If no arm beat the served blend on both corpora, the correct content of this task is to record that and change nothing — see Stop Condition. That is a real outcome, not a failure, and it is checked before step 1 rather than as a step because it decides whether the task runs at all.

## Ordered Steps

1. **TDD red.** Rewrite `TestServedBlendTiesOnATwoCandidatePool` into `TestCrossEncoderDecidesATwoCandidatePool`: maximal disagreement on a 2-pool must CHANGE the order, and `TestLowSpreadDoesNotBecomeSignal` asserts a 0.001 logit spread does not outvote a decisive fused score. Both are red today by construction — that is the whole point of having pinned the opposite in T1. Confirm red before touching production code.
2. Swap the rerank axis's normalisation to the winner. The fused axis is already a bounded `[0,1]` RRF score and is not rescaled.
3. If the winner is the weight change, update `config.Default().RerankWeight` and its doc comment to say the value is measured against small pools, not only the sweep.
4. Re-run the full eval and confirm the served arm now matches the winner's numbers.
5. **Confirm on the deployed artifact.** Redeploy and run a recall that returns a small page; assert its `blended_score` values are not tied where the cross-encoder disagreed. `blended_score` reaches the wire because of ADR-028 T2, which is what makes this checkable from outside the process.
6. Run the acceptance fence.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/palace/ ./internal/config/
  go test ./internal/palace/ -run "^(TestCrossEncoderDecidesATwoCandidatePool|TestLowSpreadDoesNotBecomeSignal|TestSmallPoolArmsDisagree)$" -count=1 -v 2>&1 | tee /tmp/t2.out
  grep -qE "^--- PASS: TestCrossEncoderDecidesATwoCandidatePool \("  /tmp/t2.out
  grep -qE "^--- PASS: TestLowSpreadDoesNotBecomeSignal \("  /tmp/t2.out
  grep -qE "^--- PASS: TestSmallPoolArmsDisagree \("  /tmp/t2.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t2.out; then exit 1; fi
  go test ./internal/palace/ ./internal/config/ ./internal/mcptest/ -count=1
'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCrossEncoderDecidesATwoCandidatePool` | `internal/palace/blenddegeneracy_test.go` | maximal disagreement on a 2-pool changes the order — the exact case that fails today | — |
| `TestLowSpreadDoesNotBecomeSignal` | `internal/palace/blenddegeneracy_test.go` | a 0.001 logit spread does not reorder the page against a decisive fused score | — |
| `TestSmallPoolArmsDisagree` | existing from T1 | the fixture still discriminates after the swap | — |

The property is pinned, not the constant. A test asserting `blended == 0.6` would go red on any future retune and green on a normalisation that reintroduces the defect at a different weight.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the normalisation function |
| 2 — something selects it | the call inside `BlendRerank` — mutation: restore min-max and `TestCrossEncoderDecidesATwoCandidatePool` goes red |
| 3 — the caller can discover it | `am_status`'s `ranking` line and the `blended_score` on every hit |
| 4 — it is used | a served page from the deployed container whose small-pool ordering is decided by the cross-encoder |

## Mutation Log

_(populated by `adr-verify --mutant` during execution)_
- 2026-09-08 · 15e8be02* · mutant killed · exit 1 · `internal/palace/service.go` · the default normaliser reverts to min-max, which ADR-030 measured and rejected: on a two-candidate pool min-max maps the two cross-encoder logits to 0 and 1 whatever their true spread, so a 0.001 logit difference becomes a full-scale signal and reorders a page the fused score had already decided. The eval arm can tell that apart from a real preference; the shipped default is the half a user actually gets, and nothing else pins it. · acceptance-sha256:506ca0226f37922a6b326a29cf69eb07202925e1620e346e2bca9ac0bc9ad2f6

## Invariants

- The fused axis and RRF are untouched. ADR-024 owns fusion.
- `RerankWeight` keeps its meaning and its range; 0 still disables, 1 still hands over the order.
- No response shape, tool schema, or operator surface changes.

## Risks

- A normalisation that fixes small pools and costs recall on large ones is a net loss. Step 4 re-runs the full eval, and the ADR requires both corpora to be reported.
- A sigmoid imports a model-specific scale. If it wins, the constant is recorded as tied to the configured cross-encoder, not as universal.

## Stop Condition

**Stop and report if T1's table shows no arm beating the served blend.** Do not ship a change because the arithmetic is elegant — that is precisely how weight 0.5 arrived, and this ADR exists because of it.

## Out of Scope

- `max_distance` as a pool shrinker (deferred: docs/adr/BACKLOG.md §"From ADR-030")
- Persisting `blended_score` (deferred: docs/adr/BACKLOG.md §"From ADR-030")

## Verification Log
- 2026-09-08 · 15e8be02* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:506ca0226f37922a6b326a29cf69eb07202925e1620e346e2bca9ac0bc9ad2f6 · ms:33860
- 2026-09-08 · 15e8be02* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:506ca0226f37922a6b326a29cf69eb07202925e1620e346e2bca9ac0bc9ad2f6 · ms:35104
