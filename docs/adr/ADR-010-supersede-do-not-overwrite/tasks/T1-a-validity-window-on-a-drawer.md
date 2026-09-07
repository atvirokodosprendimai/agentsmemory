# Task ADR-010-T1: Give a drawer a validity window

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `drawers.valid_to`, `superseded_by`, `ended_reason`, `ended_at`, and the repo predicates that read them
**Consumes:** none
**Data dependency:** hermetic for the tests; the migration is additionally checked against a copy of a real database

## Goal

A drawer can be current or ended, ending never deletes, and every existing row reads as current with no backfill.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00023_drawer_validity.sql` | add | `valid_to`, `superseded_by`, `ended_reason`, `ended_at` — all `TEXT NOT NULL DEFAULT ''` — and an index on (team_id, wing, valid_to) since every default read filters on it |
| `internal/palace/repo.go` | edit | a `current()` scope predicate; every read that should see live records only routes through it |
| `internal/palace/repo_test.go` | edit | the predicate, and that an ended row is still fetchable when asked for |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestExistingRowsReadAsCurrent`, `TestEndingARecordDoesNotDeleteIt`. Commit them red.
2. Write the migration. Empty `valid_to` means current — the same vocabulary migration 00010 already uses for a KG fact, deliberately, so the store has one notion of "ended" rather than two.
3. Add the `current()` scope. One predicate, one place: a filter copied into each query is the shape that produced four separate wing-scoping leaks in this repository this week.
4. Run the migration against a COPY of a real database and assert the row count is unchanged and every row reads as current. A migration tested only against an empty schema has not met the data it will actually run on.
5. Falsify: default `valid_to` to something non-empty; drop the index and confirm the plan degrades; make `current()` match ended rows.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true;
  set -e
  git config --global --add safe.directory /src
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestExistingRowsReadAsCurrent|TestEndingARecordDoesNotDeleteIt" -count=1 -v 2>&1 | tee /tmp/v1.out
  grep -q -- "--- PASS: TestExistingRowsReadAsCurrent" /tmp/v1.out
  grep -q -- "--- PASS: TestEndingARecordDoesNotDeleteIt" /tmp/v1.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/v1.out; then exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestExistingRowsReadAsCurrent` | `internal/palace/repo_test.go` | rows written before the migration are current, with no backfill | — |
| `TestEndingARecordDoesNotDeleteIt` | `internal/palace/repo_test.go` | an ended row is absent from current reads and present when history is asked for | — |
| `TestAnEndingAlwaysCarriesAReason` | `internal/palace/repo_test.go` | the store cannot represent an ending with no reason | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| `valid_to` defaults non-empty | yes | `TestExistingRowsReadAsCurrent` |
| `current()` matches ended rows too | yes | `TestEndingARecordDoesNotDeleteIt` |
| ending a record deletes the row | yes | `TestEndingARecordDoesNotDeleteIt` |
| a row can be ended with an empty `ended_reason` | yes | `TestAnEndingAlwaysCarriesAReason` |

## Out of Scope

- Changing any tool's behaviour (permanent: T2 and T3 own the surface; a migration that also changes semantics cannot be rolled back independently)
- Pruning ended records (deferred: docs/adr/ADR-010-supersede-do-not-overwrite.md)
## Invariants

- Empty `valid_to` means current, in drawers exactly as in KG facts.
- A row with a non-empty `valid_to` always has a non-empty `ended_reason`; an ending with no reason is the defect this ADR exists to remove, so the schema must not be able to express one.
- Ending a record never removes a row or a vector.

## Risks

- A read that should be current-only is missed and silently returns history. Mitigated by the single predicate in step 3, and by T3's end-to-end check across every default route.

## Stop Condition

Stop and ask if a real-database copy is unavailable — step 4 is the only step that meets the data this migration will run on, and skipping it is how a migration passes on an empty schema and fails in production.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>

## Mutation Log
