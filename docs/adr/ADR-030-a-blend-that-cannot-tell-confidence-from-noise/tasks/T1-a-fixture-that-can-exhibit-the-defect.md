# Task ADR-030-T1: A fixture that can exhibit the defect, and arms that can tell the candidates apart

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (eval fixture + three arms)
**Owner:** unassigned
**Produces:** small-pool (2/3/4) and low-spread eval cases; three registered arms — sigmoid, rank-fusion, and the served blend at a non-0.5 weight
**Consumes:** none
**Data dependency:** hermetic — the fixture is authored, not sampled
**Rests-on:** `each normalisation is registered as a selectable arm`, `the fixture still exhibits the tie it was authored to contain`

## Goal

The eval can produce the failure. Today it cannot: the corpus was swept at pools of 128 and 10, where neither the extreme-swap tie nor a thousandfold noise amplification occurs, so the arms have nothing to disagree about.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | the arm registry and the case list; `TestEveryDeclaredArmIsRegistered` already fails for an arm nothing registers |
| `internal/palace/service.go` | edit | the alternative normalisations, added as functions the arms select — the served path keeps min-max in this task |
| `internal/palace/blenddegeneracy_test.go` | add | the fixture guards itself: it fails if the small-pool cases do not actually tie under the served blend |

The reachability line is the ARM REGISTRATION, not the normalisation function. A normalisation that exists and no arm selects produces no table row and is this repository's characteristic defect — an eval arm declared and never registered has shipped here before.

## Ordered Steps

1. **TDD red.** `TestServedBlendTiesOnATwoCandidatePool` asserts that under the CURRENT served configuration a two-candidate pool with maximally disagreeing rerank scores returns equal `Blended` values and keeps the fused order. It must PASS immediately — it pins today's behaviour, and it is the fixture's own guard. Then `TestSmallPoolArmsDisagree` asserts the three arms produce different orderings on that fixture; confirm it is red.
2. Add the low-spread case: a pool where the logits differ by ~0.001. Assert min-max stretches it to the full range, so the fixture demonstrably contains the second finding and not only the tie.
3. Implement the three normalisations as selectable functions. Do not change the served path.
4. Register the three arms. Confirm `TestEveryDeclaredArmIsRegistered` passes and that each arm appears in the eval table with a distinct row.
5. Run the eval over the existing corpus AND the new cases. Record both, because an arm that wins on small pools and loses everywhere else must be visible as such.
6. Run the acceptance fence.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/palace/
  go test ./internal/palace/ -run "^(TestServedBlendTiesOnATwoCandidatePool|TestSmallPoolArmsDisagree|TestLowSpreadIsAmplifiedByMinMax|TestEveryDeclaredArmIsRegistered)$" -count=1 -v 2>&1 | tee /tmp/t1.out
  grep -qE "^--- PASS: TestServedBlendTiesOnATwoCandidatePool \("  /tmp/t1.out
  grep -qE "^--- PASS: TestSmallPoolArmsDisagree \("  /tmp/t1.out
  grep -qE "^--- PASS: TestLowSpreadIsAmplifiedByMinMax \("  /tmp/t1.out
  grep -qE "^--- PASS: TestEveryDeclaredArmIsRegistered \("  /tmp/t1.out
  if grep -qE "no tests to run|^FAIL" /tmp/t1.out; then exit 1; fi
  go test ./internal/palace/ -count=1
'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestServedBlendTiesOnATwoCandidatePool` | `internal/palace/blenddegeneracy_test.go` | today's behaviour, pinned: maximal disagreement on a 2-pool ties at weight 0.5 | — |
| `TestLowSpreadIsAmplifiedByMinMax` | `internal/palace/blenddegeneracy_test.go` | a 0.001 logit spread normalises to the full [0,1] range | — |
| `TestSmallPoolArmsDisagree` | `internal/palace/blenddegeneracy_test.go` | the three arms order the fixture differently — a fixture they all agree on measures nothing | — |
| `TestEveryDeclaredArmIsRegistered` | existing | each new arm is selectable, not merely declared | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the normalisation functions, called directly |
| 2 — something selects it | the arm registration — mutation: drop one registration and `TestEveryDeclaredArmIsRegistered` goes red |
| 3 — the caller can discover it | the arms appear as named rows in the eval table, which is where a tuning decision is read |
| 4 — it is used | T2 consumes the measurement; if no arm wins, that result is the deliverable |

## Mutation Log

_(populated by `adr-verify --mutant` during execution)_
- 2026-09-07 · 7afaf85d* · mutant killed · exit 1 · `internal/palace/eval.go` · the sigmoid arm stays DECLARED and stops being registered, so it produces no table row and the normalisation it names becomes unselectable. That is this repository characteristic defect — a capability finished and unreachable — and an eval arm declared but never registered has shipped here before. · acceptance-sha256:7721e6c9df0be5196c5224712afff41e6855a721b962fea781700e9ae9d6aa26 · covers:each normalisation is registered as a selectable arm
- 2026-09-07 · 7afaf85d* · mutant killed · exit 1 · `internal/palace/service.go` · the served weight moves off 0.5 and the two-candidate pool stops tying, so the fixture no longer contains the defect the whole record exists to measure. This is the fixture guarding ITSELF: an authored fixture that quietly stops exhibiting its failure leaves three arms comparing orderings on a case none of them can disagree about, and the table still prints. · acceptance-sha256:7721e6c9df0be5196c5224712afff41e6855a721b962fea781700e9ae9d6aa26 · covers:the fixture still exhibits the tie it was authored to contain

## Invariants

- **The served path does not change in this task.** Every recall served during T1 is byte-identical to today.
- The fixture guards itself: if the small-pool cases stop tying under the served blend, the fixture no longer contains the defect and the arms are measuring nothing.
- Both corpora are reported. An arm is not adopted on the new cases alone.

## Risks

- An authored fixture can be tuned until a preferred arm wins. Mitigated by pinning today's behaviour first (step 1) and by reporting the existing corpus alongside.

## Stop Condition

Stop and ask if the three arms cannot be made to disagree on any small-pool fixture — that would mean the normalisation choice does not matter, which contradicts the measured probe and needs explaining before more work.

## Out of Scope

- Changing the served normalisation or the default weight (that is T2, and only after this reports).
- Persisting `blended_score` (deferred: docs/adr/BACKLOG.md §"From ADR-030")

## Verification Log
- 2026-09-07 · 7afaf85d · exit 2 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:ad43d0ef8b0f820b41b1d601f5d41409fd1fe8cec000debdbae725a92a247488 · ms:54
  ```
  --- last 2 line(s) of stderr
  bash: -c: line 5: syntax error near unexpected token `('
  bash: -c: line 5: `  go test ./internal/palace/ -run '^(TestServedBlendTiesOnATwoCandidatePool|TestSmallPoolArmsDisagree|TestLowSpreadIsAmplifiedByMinMax|TestEveryDeclaredArmIsRegistered)$' -count=1 -v 2>&1 | tee /tmp/t1.out'
  ```
- 2026-09-07 · 7afaf85d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:7721e6c9df0be5196c5224712afff41e6855a721b962fea781700e9ae9d6aa26 · ms:35064
- 2026-09-07 · 7afaf85d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:7721e6c9df0be5196c5224712afff41e6855a721b962fea781700e9ae9d6aa26 · ms:29837
- 2026-09-07 · 7afaf85d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:7721e6c9df0be5196c5224712afff41e6855a721b962fea781700e9ae9d6aa26 · ms:28469
