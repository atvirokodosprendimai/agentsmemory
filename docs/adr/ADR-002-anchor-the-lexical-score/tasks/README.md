# ADR-002 Tasks

Implementation tasks for ADR-002: Anchor the lexical score so the fusion weight means what it says.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |
| 4 | T4 | T3 |

Strictly sequential, and not by accident: T1 ships the option, T2 makes it measurable, T3 produces
the evidence, and T4 is the only task that changes what the server does. Nothing before T4 touches
a default, so the chain can be stopped after any of the first three and the palace ranks exactly as
it does today.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Make the lexical normaliser a choice, and add the two anchored transforms | done | — | `go test ./internal/palace/ -run "TestLexNorm\|TestRankHybrid\|..."` |
| T2 | Cross the anchored normalisers with the existing weight sweep in the eval | done | — | `go test ./internal/palace/ -run "TestAnchoredArms\|TestEvalArm\|..."` |
| T3 | Re-run the four tables under both normalisers and commit the evidence | blocked | — | `go test ./internal/palace/ -run "TestAnchorEvidence"` |
| T4 | Retire or ratify the adaptive lexical weighting, from the evidence | pending | — | `go test ./internal/palace/ ./internal/config/ ./cmd/server/ -run "TestLexNorm\|..."` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

The Acceptance column is abbreviated for reading; each task file carries the full command including
the `gofmt` guard and the Docker invocation this repo builds under, and `adr-verify` runs that one.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `lexNorm` + `lexNormCeiling` / `lexNormSaturating` | T2 | T1 before T2 |
| T2 | `evalArms` registry, the anchored arm names, the `no-closet` family, `fusionRankerFor` | T3, T4 | T2 before T3 |
| T3 | `anchorEvidence` — committed results under `internal/palace/testdata/anchor/` | T4 | T3 before T4 |

## Notes

- The ADR pre-registers three outcomes and T4 executes whichever one the evidence selects. Its
  acceptance test recomputes the deterministic shipping rule and all four cross-corpus deletion
  intervals from the committed evidence instead of reading a written verdict, so the branch cannot
  be softened at execution time — which matters because one branch deletes `adaptiveBM25Weight`,
  `LexicalCoverage` and `LexicalCoverageIDF` along with four tests.
- The deletion trigger selects its comparators on one corpus and computes the interval on the
  other, in both boost regimes. The first version of this ADR picked both arms and computed the
  interval on the same cases, which is not a 95% interval for anything; T4 carries a test whose only
  job is to stop that comparison from creeping back once the deletion is the last work left.
- T3 is the task that can invalidate the ADR. If the anchored sweep collapses, or the intervals
  never separate, T4 deletes nothing and the committed evidence is the result.
- Reachability is pinned behaviourally, not syntactically. `armreach_test.go`'s existing checks are
  deliberately syntactic and cannot see an anchored arm falling through to the page-max dispatch;
  T2's `TestAnchoredArmsRankDifferentlyFromPageMax` and T4's `TestLexNormChangesWhatSearchReturns`
  follow `TestLexicalIDFChangesWhatSearchReturns` instead — different scores through the ranking
  path, on a fixture built so the two settings must disagree.
- No task changes retrieval. Every arm re-orders one pool nominated by vector distance, so a moved
  `PoolRanks` figure means something was wired that should not have been.
