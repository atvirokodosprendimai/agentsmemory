# ADR-044 Tasks

Implementation tasks for ADR-044: Make a small read trustworthy enough to act on. See the parent ADR
for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers. This
README is a derived index — when it disagrees with a task file, the task file wins and the README
must be regenerated.

## Execution Order

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1 | none |
| 2 | T2, T3, T7 | T1 |
| 3 | T4 | T3 |
| 4 | T5 | T4 |
| 5 | T6 | T5 |

**Wave 1 is one task, and that is the decision rather than an accident.** F-5 says no mechanism ships
before a baseline is recorded, so every mechanism task depends on T1 — the DAG is a spine, not a fan.
Reordering to start with a disclosure fix would ship the thing ADR-041 already shipped once: a
mechanism against an instrument that cannot see it.

Within wave 2 the three tasks touch three different packages (`repohygiene`, `mcpserver`, `palace`)
and are genuinely parallel-safe. Waves 3–5 are sequential because T4, T5 and T6 edit the same file
and share one build tag; that is a file constraint, not a logical one, and it is recorded in the
parent ADR's Inter-task Contracts.

The externally observable slice lands as early as the spine allows: T3 changes what every recall
reports in wave 2, so a human can see the effect after two waves rather than at the end.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Commit the counting rule as an artifact and make a baseline name it by content | done | F-5, UC3-S1 | `go test -tags readcostspec ./internal/repohygiene/ -run 'TestF5ABaselineNamesItsCountingRule' …` |
| T2 | Make a rule change invalidate every baseline taken under it | done | F-6, UC3-S2 | `go test ./internal/repohygiene/ -run 'TestF6ARuleChangeInvalidatesItsBaselines' …` |
| T3 | Count every disclosed range in `content_coverage` | done | F-1, UC1-S1 | `go test -tags readcostspec ./internal/mcpserver/ -run 'TestF1CoverageCountsEveryDisclosedRange' …` |
| T4 | Make every incomplete hit say so, with its full length and fetch id | done | F-2, UC1-S2 | `go test -tags readcostspec ./internal/mcpserver/ -run 'TestF2NoHitIsSilentlyPartial' …` |
| T5 | Make a page report how many hits the budget made it withhold | done | F-7, UC1-S4 | `go test -tags readcostspec ./internal/mcpserver/ -run 'TestF7APageReportsWhatItWithheld' …` |
| T6 | Guarantee a caller never joins chunks, and take the tag off | done | F-4, UC1-S3 | `go test ./internal/mcpserver/ -run 'TestF4ChunkingCreatesNoReassemblyObligation' …` |
| T7 | Make a correction leave exactly one current successor, atomically | done | F-3, UC2-S1, UC2-S2 | `go test ./internal/palace/ -run 'TestF3ACorrectionLeavesOneCurrentSuccessor' …` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

The Acceptance column is abbreviated. The full fence — with `set -o pipefail`, the `no tests to run`
guard and the regression run — lives in each task file and is what `adr-verify` digests.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | the counting-rule artifact and its content identity | T2, T3, T7 | T1 before every other task — this is F-5. T4, T5 and T6 inherit it transitively through T3 rather than declaring the edge again |
| T3 | `coveredRunes` | T4, T5 | T3 before T4 |
| T4 | `partialWithFetchID` | T5 | T4 before T5 |
| T5 | `withheld` page field | T6 | T5 before T6 |
| T2 | tag removal from `internal/repohygiene/readrule_spec_test.go` | none | last task in that file |
| T6 | tag removal from `internal/mcpserver/readcost_spec_test.go` | none | last task in that file |
| T7 | tag removal from `internal/palace/readcost_spec_test.go` | none | only task in that file |

## Notes

- **Three build tags, three removal points.** `readcostspec` gates seven bindings across three files.
  A task that is not the last in its file runs its fence WITH the tag; the last one removes it and
  runs without. Removing a tag early would put still-red bindings into the lane CI runs on every push
  (`.github/workflows/build.yml:65`), which is the failure this repository has already paid for.
- **T1 is not hermetic and its sign-off must say what it was measured against.** The baseline needs a
  populated corpus with logged recalls and at least one recorded fetch. Window and sample size go in
  the sign-off line; the only figure available today is n=1 (6 searches against 18 writes, 2026-08-28).
- **T1 has a cross-ADR dependency this record does not own.** ADR-028 T4 produces the fetch ratio the
  counting rule is derived from and is still pending. T1's step 5 carries the fallback.
- **T7 may legitimately end with a `survived` mutant.** If the harness cannot interleave two
  corrections, the entry stays in the log with a written reason rather than being replaced by an
  assertion that cannot fail.
