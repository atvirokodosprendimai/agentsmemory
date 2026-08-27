# ADR-038 Tasks

Implementation tasks for ADR-038: Refer by the id, dedupe on the content, end instead of overwrite.
See the parent ADR for the decision. T1, T4 and T5 are re-authored from ADR-010, which this record
supersedes and closes.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

**The DAG is a straight chain, so the wave table is six single-task waves.** That is what a
topological leveling of this dependency graph honestly is; no parallelism has been manufactured to
make the table look like a table.

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |
| 4 | T4 | T3 |
| 5 | T5 | T4 |
| 6 | T6 | T5 |

Two links in the chain are not merely conventional and are worth stating:

- **T1 before T2** because the content-key unique index needs `valid_to` for its second conjunct.
  Created without it, the index is briefly wrong and would have to be dropped and rebuilt one task
  later. Ordering removes that window for free.
- **T4 before T5** because a record cannot be hidden from recall before anything can end one, and
  T4 deliberately lands in a state where ended records are still returned. Say so in T4's commit —
  a half-landed pair looks like a bug.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Give a drawer a validity window | done | — | `go test ./internal/palace/ -run 'TestAFreshDrawerIsCurrent\|...' -count=1 ...` |
| T2 | Store what the id used to promise, on every path that mints or moves a drawer | done | — | `go test ./internal/palace/ -run 'TestAddStampsTheContentKey\|...' -count=1 ...` |
| T3 | Dedupe on the content key, mint an opaque id, and end what a re-file dropped | done | — | `go test ./internal/palace/ -run 'TestRefilingAnUnchangedSourceKeepsItsIdsAndAnchors\|...' -count=1 ...` |
| T4 | Retraction carries a reason, and erasure leaves the agent surface | done | — | `go test ./internal/palace/ ./internal/mcpserver/ ./cmd/server/ -run 'TestCorrectingAMemorySupersedesIt\|...' -count=1 ...` |
| T5 | Recall returns what is current — and carries the reason forward | done | — | `go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -run 'TestAnEndedRecordIsReturnedByNoDefaultRoute\|...' -count=1 ...` |
| T6 | A gate that fails when an id is re-derived, or a mint path forgets its key | done | — | `go test ./internal/palace/ -run 'TestNoPathRederivesADrawerID\|...' -count=1 ...` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `drawers.valid_to`, `superseded_by`, `ended_reason`, `ended_at`; `EndDrawer(id, reason)` | T2, T3, T4, T5, T6 | T1 first — the index predicate, the set difference and the supersede verb all read it |
| T2 | `drawers.content_key` + the two-conjunct unique index | T3, T6 | T2 before T3 — nothing to dedupe on until the column exists and is written |
| T3 | `Repo.Save` on `(team_id, content_key)`; opaque mint; set-difference `purgeSource` | T4, T6 | T3 before T4 — a supersede mints a new row, and that is only safe once the mint is opaque |
| T4 | supersede semantics; `am_invalidate_drawer(id, reason)` | T5 | T4 before T5 — nothing to hide from recall until something can end |
| T5 | current-only recall; the carried reason; `include_history` | T6 | T5 before T6 — `doctor --corpus` must tell ENDED from LOST, which needs endings to exist |

## Notes

- **Allocate both migration numbers at merge, not at authoring.** A renumber at merge re-runs the
  migration on any database that already applied it under the old number; the crash loop and its
  repair are documented in `README.md` (Development). T1 and T2 name their files `000NN_` for this.
- T2 carries a **read-only pre-flight against the hosted deployment** — collision count, anchor
  exposure, cross-wing tuples — to be run and recorded before either migration merges. Every number
  in the parent ADR is from one local palace.
- T6's `doctor --corpus` is deliberately outside the acceptance fence when run against a real
  corpus; its hermetic unit tests, which build the drift they assert on, are in the fence.
- **If this ADR stops after T1**, revert T1's migration rather than leaving four columns nothing
  reads. A column added and never read is this repository's characteristic defect.
