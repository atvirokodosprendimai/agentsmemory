# Task ADR-056-T2: `doctor --corpus` counts an unlabelled anchor as a finding

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `corpusFindings.UnlabelledAnchors`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the walk selects anchors with an empty repo`, `clean() includes the new population`

## Goal

An operator running `doctor --corpus` learns how many anchors in the palace can never be attributed to a tree, with a sample of their ids, and the exit code says so.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/doctorcorpus.go` | edit | `corpusFindings` gains `UnlabelledAnchors []string`; `walkCorpus` selects anchors whose `repo` is empty; `clean()` gains the term; `reportCorpus` prints the population with the same sample rendering as the three lost-reference classes, with one line saying why it is a finding (no tree can verify it) and the remedy (`am_update_drawer(code_anchors:)` with `repo`) |
| `cmd/server/doctorcorpus_test.go` | edit | `TestDoctorCorpusReportsUnlabelledAnchors`: the rendering half over a `corpusFindings` with one unlabelled anchor, and the walk half over a migrated SQLite palace seeded with one labelled and one unlabelled anchor, the way `TestDoctorCorpusFindsRealDriftInARealDatabase` already drives it |

`clean()` is the selecting line: a population counted by the walk and left out of `clean()` is printed and never changes the exit code, which is the finding this repository's §Reachability section keeps recording. The mutant is removing the term from `clean()`.

## Ordered Steps

1. [S1] Write `TestDoctorCorpusReportsUnlabelledAnchors` and run it red: seed a palace with one memory carrying two anchors (one with `repo`, one with `repo: ""`), run `walkCorpus`, assert `UnlabelledAnchors` holds exactly the unlabelled one's id and `clean()` is false; then `reportCorpus` over the findings, assert the id and the word `repo` appear in the output and the error is non-nil; then a findings value with every anchor labelled reports clean. Today the field does not exist, so this is red at compile time.
2. [S2] Add the field, the query, the `clean()` term and the report block. `[proof: mutation]`
3. [S3] Run the fence green, including the existing corpus tests, so the three original populations still report as they did. `[proof: acceptance]`

## Acceptance

```bash
set -o pipefail
go test ./cmd/server/ -run 'TestDoctorCorpusReportsUnlabelledAnchors$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./cmd/server/ -run 'TestDoctorCorpus' -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestDoctorCorpusReportsUnlabelledAnchors` | `cmd/server/doctorcorpus_test.go` | the walk selects exactly the anchors with an empty `repo`, the report names them and the remedy, and the verdict is non-zero for one and zero for none | — | S1, S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the field and the query |
| 2 — something selects it | `clean()` reads it; the mutant is deleting that term, which leaves the report printing the id while `reportCorpus` returns nil — the test's error assertion goes red |
| 3 — the caller can discover it | `doctor --corpus` is already advertised in help (`TestDoctorCorpusIsAdvertisedInHelp`); the report's own line says what the population is and how to clear it |
| 4 — it is used | a run against the local palace after the change; the seven measured on 2026-09-04 were labelled by hand that day, so the expected reading is zero and a non-zero one is a new finding |

## Mutation Log

## Invariants

- The three existing populations and `EndedFactSources` report exactly as before; the new block is additive.
- A read-only run: `doctor --corpus` repairs nothing (`TestTheReadOnlyPathMintsNothing` stands).

## Risks

- A palace with many unlabelled anchors floods the report — mitigated by reusing `shortSample`, which already bounds the other three lists.

## Stop Condition

Stop if the anchors table has no `repo` column on the walk's read model (it is `repo` on `drawer_anchors`, read through `anchorRow` in `internal/palace/anchors.go`, today); then the query needs a schema read before the task, not a guess.

## Out of Scope

- Labelling the anchors it finds — the remedy is named in the report and belongs to the session that owns the memory.
- The write-side report — T1's job.

## Verification Log
