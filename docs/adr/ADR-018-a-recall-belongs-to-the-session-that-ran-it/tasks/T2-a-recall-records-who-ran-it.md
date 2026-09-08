# Task ADR-018-T2: A recall records which session ran it — WITHDRAWN

**Status:** Withdrawn 2026-08-22. Not deferred, not blocked: this task will not be
built, and the ADR named this outcome as acceptable before T1 was run.

**Why.** T1 measured that the server mints no session identity — `cmd/server/main.go`
builds its transport with `server.WithStateLess(true)`, whose session manager
`Generate()`s the empty string and `Validate()`s nothing. The ADR left two ways
forward: (a) require every client to send its own `Mcp-Session-Id` and record
absence honestly, or (b) leave the transport stateless and withdraw this task.
**(b) was chosen** — the decision is that a stateless transport is worth more than
per-session attribution.

**What that costs, stated plainly.** The "memories to write" list stays suppressed
permanently, not until this task ships. It was the most useful thing the Stop hook
emitted, and it is gone for good on the current transport. The numbers in the
recall report stay palace-wide and stay labelled as such (T3, which shipped).

**What would reopen it.** A switch away from stateless mode, which is pinned by a
test rather than by this sentence: `TestProductionStillRunsStateless`
(`internal/mcpserver/session_test.go`) fails the moment `main.go` stops passing
`server.WithStateLess(true)`. That red test is the signal to reconsider this task;
nothing else is.

**Not chosen, and why (a) was rejected.** Requiring the client to send the header
makes attribution depend on every caller's cooperation and degrades SILENTLY when
one does not — every anonymous caller lands in one shared bucket that reads as a
single busy session. An identity that is the same for everybody is worse than
none, because a column fills and a report groups by it. Making that safe means
recording absence as absence on every path that records a search, which is most of
the task's cost for a feature that only works when clients volunteer.

---

*Everything below is the task as it was written before the decision, kept verbatim
so the withdrawal can be read against what was actually proposed. None of it is to
be executed. Its Acceptance command is not run and its Verification Log stays
empty — an empty log on a withdrawn task is correct, where on a completed one it
is the fabrication hole this pipeline exists to close.*


**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `search_events.session_id`, `SearchQuery.SessionID`, `/stats?session=`
**Consumes:** T1's finding — this task does not begin until an identity is known to be reachable
**Data dependency:** hermetic

## Goal

A recall event says which session ran it, and the report can ask for one session's.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00023_search_events_session.sql` | add | one nullable column; existing rows carry no session and must not be given one |
| `internal/palace/recallstats.go` | edit | `recordSearch` writes it; `RecallStats` filters on it |
| `internal/palace/service.go` | edit | `SearchQuery.SessionID`, travelling exactly as `SkipTelemetry` does |
| `internal/mcpserver/drawers.go` | edit | the handler supplies it from what T1 found |
| `cmd/server/main.go` | edit | `/stats` accepts `session=` |
| `internal/palace/recallstats_test.go` | edit | the column is written by a REAL search, and the filter excludes other sessions |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestARealSearchRecordsItsSession`, `TestStatsExcludeOtherSessions`, `TestStatsWithoutASessionReportTheWholeTeam`. Commit them red.
2. Add the migration. NULLABLE, and no backfill: a row written before this has no session, and inventing one is the fabrication this ADR exists to prevent.
3. Thread `SessionID` through `SearchQuery` and into `recordSearch`. Keep it best-effort exactly as the rest of the recording is — a statistics write must never fail the search it measures.
4. Filter in `RecallStats` when a session is named; report the whole team when none is.
5. Assert the write through the REAL search path. A test that calls `recordSearch` directly passes while nothing populates the column, which is this repository's signature defect.
6. Falsify: add the column and never write it; write it and never filter; filter and silently include NULLs.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestARealSearchRecordsItsSession|TestStatsExcludeOtherSessions|TestStatsWithoutASessionReportTheWholeTeam" -count=1 -v 2>&1 | tee /tmp/a18t2.out
  grep -q -- "--- PASS: TestARealSearchRecordsItsSession" /tmp/a18t2.out
  grep -q -- "--- PASS: TestStatsExcludeOtherSessions" /tmp/a18t2.out
  grep -q -- "--- PASS: TestStatsWithoutASessionReportTheWholeTeam" /tmp/a18t2.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a18t2.out; then exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestARealSearchRecordsItsSession` | `internal/palace/recallstats_test.go` | a search through `Service.Search` writes a non-empty session id — not a direct call to `recordSearch` | — |
| `TestStatsExcludeOtherSessions` | `internal/palace/recallstats_test.go` | two sessions' recalls do not appear in each other's report, which is the whole defect | — |
| `TestStatsWithoutASessionReportTheWholeTeam` | `internal/palace/recallstats_test.go` | naming no session still answers, because that is a different question and not an error | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the migration and the column |
| 2 — something selects it | `TestARealSearchRecordsItsSession` drives the production search path |
| 3 — the caller can discover it | `/stats?session=` is a documented parameter |
| 4 — it is used | T3 passes it |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| add the column and never write it | yes | `TestARealSearchRecordsItsSession` |
| write it and ignore it when filtering | yes | `TestStatsExcludeOtherSessions` |
| treat a missing session as "match everything" | yes | `TestStatsExcludeOtherSessions` |
| backfill existing rows with the current session | yes | `TestStatsExcludeOtherSessions` |

## Out of Scope

- Per-session WRITE statistics (deferred: docs/adr/BACKLOG.md)
- Retro-attributing rows already written (permanent: they carry no identity, and inventing one is this ADR's own defect wearing a migration's clothes)

## Invariants

- A row written before this migration keeps a NULL session and appears in no session-scoped report.
- Recording stays best-effort: it never fails the search it measures.

## Risks

- The column exists and nothing fills it, so every session-scoped report is empty and reads as "you ran no searches". Mitigated: the first test drives the real path, and the mutant for it is listed above.

## Stop Condition

Stop and ask if the identity T1 found is not available on every path that records a search — a partially-attributed table is worse than an unattributed one, because the gaps look like silence rather than like missing data.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>

## Mutation Log
