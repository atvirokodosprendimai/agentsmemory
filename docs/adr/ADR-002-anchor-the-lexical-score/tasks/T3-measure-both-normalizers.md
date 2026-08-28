# Task ADR-002-T3: Re-run the four tables under both normalisers and commit the evidence

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `anchorEvidence` — the committed eval results under `internal/palace/testdata/anchor/` plus their loader
**Consumes:** `evalArms` registry, anchored arm names, the `no-closet` anchored family (T2)


> **Amended 2026-08-20.** This task was written for a world where every fusion arm carried the
> closet prior and an unboosted `no-closet` family was added beside it as a control. ADR-003 T1
> made the prior opt-in by arm name and put closet variants of the sweep and adaptive arms
> permanently out of scope, so the ten anchored arms are one unboosted family and there is no
> `no-closet` counterpart to any of them — the confound the control existed for is gone rather
> than being controlled for. Read every `no-closet` reference below as "the anchored arms",
> and every count of four intervals or two regimes as two intervals over one regime. The bar per
> interval is unchanged. See the amendment note in the parent ADR's Decision.

## Goal

Produce the measurement the ADR's shipping rule and deletion trigger are read from: the four tables that produced the IDF-coverage result, re-run with fixed lexical weights 0.20/0.40/0.60 plus the adaptive and IDF arms, under page-maximum and both anchored normalisers, with every anchored arm additionally run without the closet boost, on both corpora — committed so the verdict is replayable offline.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/testdata/anchor/*.results.json` | add | one `writeResults` payload per re-run table; it already carries per-arm `ranks` and per-case `details[].category`, so per-category MRR and every paired delta are reconstructible without re-running |
| `internal/palace/anchor_evidence_test.go` | add | loader plus the completeness gate that fails while the evidence is missing, partial, unpairable, or degraded |

## Ordered Steps

1. Write the failing test first (TDD red): `TestAnchorEvidenceIsComplete` in `internal/palace/anchor_evidence_test.go`, globbing `testdata/anchor/*.results.json` and asserting at least four files; at least two distinct `wing` values (both corpora); within each file, an arm for each of `bm25=0.20/0.40/0.60`, `auto`, `auto-idf` under `page-max`, `ceiling` and `saturating`, **and** the `no-closet` counterpart of every anchored one. The no-closet regime is required for the **anchored** arms only: the deletion trigger is its only consumer and it compares anchored-fixed against anchored-adaptive, while the shipping rule reads boosted arms exclusively. Page-max needs no no-closet family beyond the `ArmHybrid` row that already exists at `w=0.4`. `details[]` must be non-empty and carry `category`. Commit it red — the evidence does not exist yet.
2. Add to the same test the pairing guard: every arm in a file must have `len(ranks)` equal to every other arm's. `PairedDelta` returns a zero `Interval{}` when `n != len(b)` (`evalstats.go:79`), so without this check the gate can pass while every interval it feeds is silently empty.
3. Add `TestAnchorEvidenceSweepDidNotCollapse`: within each file the three anchored fixed-weight arms must not return identical per-case `ranks`, in either boost regime. The reason is the corrected algebra, not a hunch — the `ceiling` arm at nominal weight `w` orders like page-max at `w' = w·a/(1 − w + w·a)` with `a = maxBM25/C < 1`, and `C` sums the IDF of every query term whether any candidate matched it or not, so on prose `a` can be small enough that `w' ` is near zero for all three of 0.20, 0.40 and 0.60. "The best anchored fixed-weight arm" would then be picked from three copies of one arm, and neither the shipping rule's argmax nor the deletion trigger could be read from it.
4. Add `TestAnchorEvidenceHasNoWarnings`: a file whose `EvalReport.Warnings` is non-empty must not be committed as evidence. A degraded reranker changes the reranked arms, and a fusion comparison read off a run that announced its own degradation is a comparison nobody can defend later.
5. Assert the identifier-query table is present and separable — a headline gain that hides a regression on the queries where lexical fusion is the whole win (1.000 v 0.847) must not pass the gate.
6. Run the eval per table with `--cases` pointing at the saved case file for that table, so the questions are the same ones the original result used, and copy each `.results.json` into `internal/palace/testdata/anchor/`.
7. Record, in the test file's package comment, which committed file corresponds to which of the four original tables and to which corpus — the mapping is the one thing the JSON does not carry, and T4's cross-corpus trigger cannot be computed without it.
8. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ -run "TestAnchorEvidence" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAnchorEvidenceIsComplete` | `internal/palace/anchor_evidence_test.go` | four tables, both corpora, every arm × normaliser present plus the `no-closet` counterpart of every anchored arm, per-category detail present | — |
| `TestAnchorEvidenceSweepDidNotCollapse` | `internal/palace/anchor_evidence_test.go` | the three anchored fixed-weight arms are not rank-identical in either regime, so the sweep still spans a range the rule and the trigger can be read against | — |
| `TestAnchorEvidenceIsPairable` | `internal/palace/anchor_evidence_test.go` | every arm in a file has the same number of per-case ranks, so `PairedDelta` cannot return an empty interval unnoticed | — |
| `TestAnchorEvidenceHasNoWarnings` | `internal/palace/anchor_evidence_test.go` | no committed file carries a degradation warning | — |
| `TestAnchorEvidenceNamesItsCorpus` | `internal/palace/anchor_evidence_test.go` | every file maps to a named corpus, so T4's cross-corpus selection has two disjoint sides to work with | — |

## Invariants

- The committed evidence is exactly what `writeResults` wrote — no hand-editing, no rounding, no dropped arms.
- Each table re-runs the ORIGINAL questions for that table via its saved `--cases` file; a regenerated question set would compare two different experiments.
- No arm is dropped from a run to make a file smaller.
- The two corpora stay separable in the committed files. T4 selects arms on one and computes intervals on the other; a merged file collapses that into the single-corpus comparison this ADR rejected.

## Risks



- A reranker outage mid-run degrades the reranked arms and `EvalReport.Warnings` records it; step 4 turns that from a thing to remember into a thing the gate refuses. Re-run instead.
- The four original tables may not all be reproducible from saved case files. If one is not, say so in the test's package comment and treat the evidence as three tables plus a gap, rather than quietly substituting a fresh corpus.
- The retrieval ceiling caps what any of this can show: on our mined corpus 98% of golds reach the pool, 75% are already at rank 1. An arm difference smaller than a couple of cases is inside the noise these n permit, and the intervals will say so.

## Stop Condition

Stop and report if the three anchored fixed-weight arms come back rank-identical: the sweep has collapsed, the shipping rule has nothing to take an argmax over, and the fix is a different weight range or a different `κ` — not a run of the remaining tables. Stop too if the anchored arms move the headline MRR by more than the paired CI width on the *first* corpus measured — a very large effect on one corpus and none on the other is more likely a wiring bug in T1 or T2 than a finding, and it should be diagnosed before three more runs are spent on it.

## Out of Scope

- Acting on what the numbers say — that is T4's job.
- Growing either corpus beyond the cases the original tables used (deferred: docs/adr/BACKLOG.md)

## Verification Log

## Mutation Log
