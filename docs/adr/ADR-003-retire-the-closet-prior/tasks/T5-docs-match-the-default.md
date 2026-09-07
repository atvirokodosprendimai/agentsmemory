# Task ADR-003-T5: Make the documentation describe the ranking that ships

**Depends-on:** T4
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** a doc gate pinning that the operator-facing prose matches the shipped closet default
**Consumes:** `config.Default().ClosetBoost` = 0 (T4)

## Goal

Every place that tells an operator what recall does stops listing the closet boost as part of it, and the README documents the knob that turns it on.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `README.md` | edit | four claims that default recall is "vector + BM25 + closet boost" (`README.md:19`, `:41`, `:155`, `:945`) and no `--closet-boost` row in the flag table at all — the knob this ADR turns on is currently undocumented |
| `internal/web/ai/landing.md` | edit | three more: the capabilities bullet (`:71`), the one-line pitch (`:97`) and the FAQ answer (`:177`) all describe the fused ranking as including a closet boost |
| `internal/web/views/models.go` | edit | the glossary defines a closet as "a topic and quote pointer index that boosts ranking" (`:662`), and two more entries repeat the three-way claim (`:613`, `:682`) — false by default after T4 |
| `cmd/server/docs_test.go` | add | the gate: prose cannot be trusted to track a default, so assert it, the way `internal/mcpserver/catalog_test.go`'s `TestCatalogSizeIsWhatTheReadmeClaims` already asserts the tool count |
| `internal/web/views/landing_test.go` | edit | pin the Closet concept text in the package that owns it |
| `CHANGELOG.md` | edit | an upgrading operator should meet the default change before their ranking moves |

## Ordered Steps

1. Write the failing tests first (TDD red) and commit them red: `TestClosetDocsMatchTheShippedDefault` in a new `cmd/server/docs_test.go`, asserting `README.md` and `internal/web/ai/landing.md` describe default recall without the closet boost and that `README.md` documents `CLOSET_BOOST` with its default; and `TestLandingConceptsDescribeClosetsHonestly` in `internal/web/views/landing_test.go`, asserting the Closet concept no longer claims it boosts ranking.
2. Edit the README claims to say what ships — vector similarity fused with BM25 — and add the `--closet-boost` / `CLOSET_BOOST` row to the flag table: default 0 (off), 1 restores the full curation prior, with the one-line reason and the eval block that would justify it.
3. Edit `internal/web/ai/landing.md`'s capabilities bullet, pitch line and FAQ answer the same way.
4. Reword the glossary entry: a closet is a per-source summary index, mined and browsable, and an optional ranking prior.
5. Write what T3's curated-wing records say into the README's closet paragraph, following the row of the ADR's Table 2 that fired for R1 and R2 — the prior measured better on a curated palace, worse, inconclusive, or the wing was below the floor. This sentence is not a judgement: the cells file states which.
6. Note the default change in `CHANGELOG.md`, including that `NewService` now starts at 0 for anyone embedding the package.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'set -e; go vet ./...; go test ./cmd/server/ ./internal/web/views/ -run "TestClosetDocsMatchTheShippedDefault|TestLandingConceptsDescribeClosetsHonestly" -count=1 2>&1 | tee /tmp/adr003-t5.out; if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr003-t5.out; then exit 1; fi'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestClosetDocsMatchTheShippedDefault` | `cmd/server/docs_test.go` | README and the AI landing doc describe default recall without the closet boost, and README documents `CLOSET_BOOST` | — |
| `TestLandingConceptsDescribeClosetsHonestly` | `internal/web/views/landing_test.go` | the glossary does not claim a closet boosts ranking by default | — |

## Invariants

- The expected strings are spelled out in the test, not read out of the file being checked — a test that takes its expectation from its subject passes against anything, which is why `TestCatalogSizeIsWhatTheReadmeClaims` spells its numbers out.
- Nothing in this task changes behaviour. If a Go test outside the two named ones changes result, something was edited that should not have been.

## Risks

- A prose gate that matches exact phrases breaks on an innocent rewording, which trains people to weaken the gate. Match on the claim (a closet boost named as part of default recall), keep the assertion narrow, and say in the failure message what the correct sentence looks like.

## Stop Condition

Stop if the README's recall section cannot be corrected without also re-describing the reranker or the fusion mode — that would mean this ADR's prose change has grown into a documentation rewrite that needs its own review.

## Out of Scope

- Rewriting the recall documentation beyond the closet claims (permanent: this task corrects a default's description, not the chapter around it.)
- A general doc-vs-code gate for every configurable default (deferred: docs/adr/BACKLOG.md)

## Verification Log

## Mutation Log
