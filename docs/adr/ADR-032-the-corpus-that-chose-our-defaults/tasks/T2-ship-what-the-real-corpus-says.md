# Task ADR-032-T2: Ship what the real corpus says, or record that it said nothing

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S (at most two literals)
**Owner:** unassigned
**Produces:** the measured `Fusion` and `RerankWeight` defaults, or a recorded null result
**Consumes:** T1's case file and table
**Data dependency:** requires T1's committed corpus

## Precondition

**Read T1's table before starting.** If neither knob's contrast resolves — both paired intervals spanning zero at n≥150 — the correct content of this task is to record that and change nothing. A default that survived a corpus which could finally have rejected it is a better default than it was yesterday, even unchanged.

## Goal

`FUSION` and `RERANK_WEIGHT` are what the real corpus measured, or are explicitly recorded as unresolved at that sample size.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/config/config.go` | edit (conditional) | `Fusion` at :376 and `RerankWeight` at :352 — two literals, both already annotated as measured |
| `internal/palace/blenddegeneracy_test.go` | edit (conditional) | the shipped-weight property tests read `DefaultRerankWeight`; changing it must not silently weaken them |
| `docs/adr/ADR-032-.../evidence/` | edit | the verdict, with intervals |

## Ordered Steps

1. **TDD red.** Add `TestShippedDefaultsCiteTheirCorpus`: every `config.Default()` field whose comment claims it was measured must name the case-set id it was measured on. It is red today — `Fusion` and `RerankWeight` say "chosen by the eval's weight sweep" and name no corpus, which is exactly how a measurement outlived the corpus that produced it. Confirm red before touching defaults.
2. Change whichever defaults T1 resolved, and only those.
3. Update each changed field's comment to name the case-set id and the interval, so the next reader can tell what would have to change for it to be revisited.
4. Re-run ADR-030's property tests. `TestCrossEncoderDecidesATwoCandidatePool` reads `DefaultRerankWeight`; at some weights the two-candidate tie cannot occur at all, so a weight change could make it pass vacuously.
5. Run the acceptance fence.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/config/ ./internal/palace/ ./cmd/server/
  go test ./internal/repohygiene/ -run "^(TestShippedDefaultsCiteTheirCorpus)$" -count=1 -v 2>&1 | tee /tmp/t2.out
  grep -qE "^--- PASS: TestShippedDefaultsCiteTheirCorpus \(" /tmp/t2.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t2.out; then exit 1; fi
  go test ./internal/config/ ./internal/palace/ ./cmd/server/ ./internal/mcptest/ ./internal/repohygiene/ -count=1
'
```

Selector and PASS grep are anchored: an unanchored pair is satisfied by any test whose name merely begins with the required one.

⚠ **The package was WRONG until 2026-09-07, and the gate it names now lives somewhere else.** This fence read `go test ./cmd/server/`, because that is where step 1 planned to put the test. When `TestShippedDefaultsCiteTheirCorpus` was finally written it landed in `internal/repohygiene/shippeddefaults_test.go`, beside the other corpus-hygiene gates, and nothing updated the fence, the Tests table, or the sibling README. Measured before the fix: `go test ./cmd/server/ -run "^(TestShippedDefaultsCiteTheirCorpus)$"` exits **0** and prints `ok … [no tests to run]`.

★ **The fence caught it anyway, and that is worth recording rather than glossing.** The anchored `--- PASS:` grep on the next line is a POSITIVE assertion under `set -e`: no test ran, so no PASS line was printed, so the fence goes red. A fence that only checked the exit code would have reported success over zero tests. The `no tests to run` guard below it, by contrast, could NOT have caught it — `! grep` is exempt from `set -e`, so it was inert; it is written in the un-negated form now, which does fail.

⚠ So the ordering of the lesson is: the exit code proved nothing, the negated guard proved nothing, and the one line that worked was the positive assertion naming what it expected to see.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestShippedDefaultsCiteTheirCorpus` | `internal/repohygiene/shippeddefaults_test.go` | a default whose comment claims a measurement names the case set it was measured on | — |
| `TestCrossEncoderDecidesATwoCandidatePool` | existing | still fails for the right reason after any weight change, rather than passing vacuously | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the defaults are read by `configureRanking` |
| 2 — something selects it | `config.Default()` — mutation: revert a changed literal and the citation test goes red |
| 3 — the caller can discover it | `am_status` prints the ranking profile including fusion and weight |
| 4 — it is used | every deployment inherits it, which is why it waited for n≥150 |

## Mutation Log

_(populated by `adr-verify --mutant` during execution)_

## Invariants

- Only knobs T1 RESOLVED change. An inconclusive contrast leaves the default alone.
- Every changed default's comment names its corpus, so this cannot silently recur.
- ADR-030's degeneracy property survives any weight change, or the change is wrong.

## Risks

- A weight change can make the two-candidate tie unreachable, so ADR-030's test would pass without testing anything. Step 4 exists for that, and it is the failure mode this repository names most often.
- Flipping `FUSION` changes which knobs are live at all: `lex-norm` and `bm25-weight` are inert under `rrf` and become load-bearing under `linear`. That is a larger behavioural change than the diff suggests and must be said in the commit.

## Stop Condition

**Stop and record if T1's contrasts do not resolve.** Do not ship the point estimate because it points the way you expected — that is the error ADR-032 exists to document.

## Out of Scope

- The unanswered-query rate, a stronger judge, and the recalls that never happened (deferred: `docs/adr/BACKLOG.md` §"From ADR-032")
- Revisiting ADR-030's sigmoid default (permanent: reverting on an inconclusive result is the same error inverted)

## Verification Log
