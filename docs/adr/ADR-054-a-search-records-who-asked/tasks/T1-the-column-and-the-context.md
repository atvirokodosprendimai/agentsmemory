# Task ADR-054-T1: A search records the origin its request carried

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `search_events.origin` column, `searchEventRow.Origin`, `mcpprotocol.OriginHeader`, `mcpprotocol.OriginEnvVar`, `auth.WithOrigin` / `auth.OriginFrom(ctx)`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the bridge lifts the header`, `the row carries the context's origin`

## Goal

A search made on a connection that declared an origin writes that origin into its `search_events` row, and one made without an origin writes `''`.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00037_search_events_origin.sql` | add | `ALTER TABLE search_events ADD COLUMN origin TEXT NOT NULL DEFAULT ''` — additive, so rows from before it read as a person's |
| `internal/mcpprotocol/constants.go` | edit | `OriginHeader = "X-Agentsmemory-Origin"` and `OriginEnvVar = "AGENTSMEMORY_ORIGIN"` beside `WingHeader`, so client and server share one spelling |
| `internal/auth/auth.go` | edit | `Bridge` lifts the header into the context beside the wing (`requestOrigin`, `WithOrigin`, `OriginFrom`) — the one place per request where HTTP is still visible |
| `cmd/server/mcp.go` | edit | the CLI's in-process `mcp` path sets the same context value from `OriginEnvVar`, beside `auth.WithDefaultWing` — this is the line that SELECTS the value for every shipped hook, which goes through this path and never through HTTP |
| `internal/palace/recallstats.go` | edit | `searchEventRow.Origin` (`gorm:"column:origin"`) |
| `internal/palace/service.go` | edit | `SearchPage` sets `Origin: auth.OriginFrom(ctx)` where the row is built — ⚠ `internal/palace` must not import a surface; `internal/auth` is not one (architecture contract D2), confirm `TestModuleDependenciesObeyTheContract` stays green |
| `internal/palace/recallstats_origin_test.go` | add | the failing tests, first |
| `internal/auth/origin_test.go` | add | the bridge half |

## Ordered Steps

1. [S1] Write `TestASearchRecordsTheOriginItsContextCarries` and `TestTheBridgeLiftsTheOriginHeader` and run them red — the first fails because the row has no `Origin` field, the second because `auth.OriginFrom` does not exist.
2. [S2] Add the migration and the row field; run `agentsmemory doctor --schema` against a fresh database to confirm goose applies it. `[proof: acceptance]`
3. [S3] Add the constants, `WithOrigin` / `OriginFrom`, and the `Bridge` half; `requestOrigin` reads the header only — no query-parameter fallback, because the wing's query form exists for a registration channel (Cursor's URL) that carries no origin by construction.
4. [S4] Set the origin in `cmd/server/mcp.go` from `OriginEnvVar`, beside the wing — a test that drives `mcp search` with the variable set and reads the row back pins that the in-process path is wired, not only the HTTP one.
5. [S5] Write the row field in `SearchPage`; run the fence green; record the red run from S1 in the Verification Log.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ -run 'TestASearchRecordsTheOriginItsContextCarries' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./internal/auth/ -run 'TestTheBridgeLiftsTheOriginHeader' -count=1 2>&1 | tee /tmp/acc2.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc2.out && \
go test ./internal/palace/ ./internal/auth/ ./cmd/server/ ./internal/archguard/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestASearchRecordsTheOriginItsContextCarries` | `internal/palace/recallstats_origin_test.go` | a `SearchPage` under `auth.WithOrigin(ctx, "hook:x")` writes `origin = "hook:x"`; one without writes `''` | — | S1, S5 |
| `TestTheBridgeLiftsTheOriginHeader` | `internal/auth/origin_test.go` | `Bridge` on a request carrying `X-Agentsmemory-Origin` yields `OriginFrom(ctx) == value`; a request without it yields `''` | — | S1, S3 |
| `TestTheCLIPathSetsTheOriginFromTheEnvironment` | `cmd/server/mcp_origin_test.go` | `mcp search` with `AGENTSMEMORY_ORIGIN=hook:t` set records a row with that origin | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the three tests above |
| 2 — something selects it | `SearchPage`'s row literal and `cmd/server/mcp.go`'s context line; the mutant is deleting `Origin: auth.OriginFrom(ctx)` from the row literal, which turns `TestASearchRecordsTheOriginItsContextCarries` red |
| 3 — the caller can discover it | `mcpprotocol` exports both names; `OriginEnvVar` is documented by T2 and read by nothing until T2 — say so in the T1 sign-off rather than leaving a read-by-nothing variable to `TestDocumentedEnvVarsAreRead` |
| 4 — it is used | `hook_searches` in T3 is the usage signal; nothing measures it before T3 |

## Mutation Log

## Invariants

- Every existing `search_events` row keeps reading as a person's (`origin = ''`); nothing rewrites history.
- ADR-001's calibration reads every row regardless of origin.
- `internal/palace` imports no surface package.

## Risks

- The migration number collides with a parallel branch — allocate at merge, never at authoring (UPDATE.md §Schema migrations).

## Stop Condition

Stop if `internal/palace` cannot read the context's origin without importing a surface; the fallback is a palace-owned context key that `auth` sets, and that is a design change worth a sentence in the record.

## Out of Scope

- Sending the header (T2).
- Reading the column in `RecallStats` (T3).

## Verification Log
