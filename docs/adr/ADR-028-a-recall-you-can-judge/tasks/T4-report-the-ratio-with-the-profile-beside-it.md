# Task ADR-028-T4: Report the ratio, with the profile beside it

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `profile_id` on the durable `search_events` row; a fetch ratio reported with its population
**Consumes:** `drawer_fetches` rows and `CountFetches` (T3)
**Data dependency:** needs logged recalls and at least one recorded fetch. Hermetic for the migration and the arithmetic; the REPORTED number needs a real window, and the sign-off records what it was taken against.
**Rests-on:** `the profile stamped on every logged recall`, `the NULL profile exclusion`, `one join row per fetched recall`, `the rate reaching the served response`

## Goal

Turn two raw counts into a rate nobody can quote past its population.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00038_search_events_profile_id.sql` | add | Primitive #1's other half. `profile_id` is on the span today and absent from the durable row, which is what makes a ratio uninterpretable — "38% of recalls were followed by a fetch" means nothing without knowing which ranking profile produced them. ⚠ Numbered at IMPLEMENTATION time, not here: this cell reserved `000NN` and noted "00036 was taken by T3", and by the time T4 ran, 00037 (search_events origin, ADR-054) had landed too. A migration number reserved at authoring time is stale the moment another task merges |
| `internal/palace/fetchlog.go` | edit | The ratio, computed over recalls THAT WERE LOGGED and grouped by profile |
| `internal/mcpserver/admin.go` | edit | Publishing it, with the population named in the response rather than in a comment |
| `internal/palace/fetchlog_test.go` | edit | `TestTheFetchRatioNamesItsPopulation` — the population assertions, which need two profiles in one window and so cannot use the live write path |
| `internal/mcptest/fetchratio_reach_test.go` | add | Rung 2. Not in the original plan and it should have been: a palace test cannot see its own call site, so nothing else can kill a mutant that drops `fetch_rates` from the response |

## Ordered Steps

1. Write the failing test first: a ratio computed over two profiles must not collapse into one number, and a window with zero logged recalls must report no rate rather than a division by zero.
2. Add the migration and populate `profile_id` on write.
3. Compute the ratio per profile, over logged recalls only.
4. Publish it with its denominator and its profile beside it — ADR-007's rule, and this ADR's own deferral says the number is meaningless without them.
5. ⚠ **Run the canary before trusting any zero.** Confirm the instrument can report a POSITIVE — one logged recall, one fetch naming it, a non-zero rate — before reporting any figure at all. This corpus has a rule earned seven times in one evening, and the entry this task descends from was itself written without checking that its instrument existed.

## Acceptance

```bash
go test ./internal/palace/ ./internal/mcptest/ -run 'TestTheFetchRatioNamesItsPopulation|TestTheFetchRateReachesTheToolSurfaceWithItsPopulation' -count=1 2>&1 | tee /tmp/adr028-t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t4.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheFetchRatioNamesItsPopulation` | `internal/palace/fetchlog_test.go` | The rate is computed over logged recalls only, is reported per profile, and is withheld rather than zero when the window holds no logged recall | — |
| `TestTheFetchRateReachesTheToolSurfaceWithItsPopulation` | `internal/mcptest/fetchratio_reach_test.go` | The rate reaches `am_recall_stats` carrying its profile and its denominator, names the same profile `am_status` publishes, and MOVES when an unfetched recall enters the denominator | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestTheFetchRatioNamesItsPopulation` |
| 2 — something selects it | The `am_recall_stats` handler; a mutation removing the ratio from the response must go red |
| 3 — the caller can discover it | The tool description names the rate, its denominator and its profile |
| 4 — it is used | Nothing measures this yet. The first quoted rate is the point at which it becomes usable, and it must not be quoted before step 5's canary |

## Mutation Log

- 2026-09-07 · 20db1fb* · mutant killed · exit 1 · `internal/palace/fetchlog.go` · Admits NULL-profile rows to the group-by. Every recall written before migration 00038 then forms its own group under the empty profile, publishing a rate for a configuration that never existed — and it arrives as a plausible extra row rather than as an error, which is exactly how this population defect ships. · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · covers:the NULL profile exclusion
- 2026-09-07 · 20db1fb* · mutant killed · exit 1 · `internal/palace/fetchlog.go` · Removes DISTINCT from the join subquery, so a recall a caller read twice joins twice. It inflates BOTH sides — the numerator counts fetches instead of fetched recalls, letting the rate exceed 1 on the heaviest readers, and COUNT(*) over the joined rows inflates the denominator too. Two independent assertions fail, which is why the defect is visible at all; a single-fetch fixture would never have shown it. · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · covers:one join row per fetched recall
- 2026-09-07 · 20db1fb* · mutant killed · exit 1 · `internal/mcpserver/admin.go` · Drops the rate from the served response while leaving every line that computes it. THIS IS THE REACHABILITY MUTANT: the palace test passes under it (measured, exit 0) because FetchRatesByProfile still returns the right answer to a direct caller — the component exercised instead of the selection, which AGENTS.md names as this repository characteristic defect. Only the mcptest reach test kills it, and without that test the whole feature could be unshipped with a green suite. · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · covers:the rate reaching the served response
- 2026-09-07 · 20db1fb* · mutant killed · exit 1 · `internal/palace/service.go` · Stamps an empty profile on every logged recall instead of the ranking that produced it. The column is still assigned and still non-NULL, so the aggregate happily groups and reports a rate — for a profile named by the empty string, which no operator can look up in am_status. It is the quietest of the four: nothing errors, a row exists, a number is published, and it describes nothing. · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · covers:the profile stamped on every logged recall

## Invariants

- No rate is reported for a window with no logged recalls.
- The denominator is recalls THAT WERE LOGGED, never recalls — `SkipTelemetry` means some recalls write no row at all, and the eval depends on that.
- A rate is never published without its profile and its denominator in the same response.

## Risks

- The rate gets quoted without its population by someone reading only the number. Mitigated by publishing the denominator alongside rather than in prose, which is the only mitigation this corpus has found to work.
- `profile_id` on a hot table is a migration on `search_events`. Additive and nullable; rows written before it carry NULL, which the aggregate must exclude rather than count as a profile.

## Stop Condition

Stop if the window holds no fetches at all when this is attempted: the honest outcome is to report that no client ever named a recall, which ADR-028's own deferral says is worth as much as the report. Do not manufacture a fixture rate and publish it as an observation.

## Out of Scope

- A relevance metric derived from the signal (deferred: `docs/adr/BACKLOG.md` §"From ADR-028").
- Retention for either table (deferred: `docs/adr/BACKLOG.md` §"From ADR-028").

## Verification Log
- 2026-09-07 · 20db1fb* · exit 1 · `go test ./internal/palace/ ./internal/mcptest/ -run 'TestTheFetchRatioNamesItsPopulation|TestTheFetchRateReachesTheToolSurfaceWithItsPopulation' -count=1 2>&1 | tee /tmp/adr028-t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t4.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1` · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · ms:18571
  ```
  --- last 10 line(s) of stdout (of 1572 after folding 1572 raw)
  2026/09/07 08:43:47 OK   00035_billing_applied_orders.sql (296.21µs)
  2026/09/07 08:43:47 OK   00036_drawer_fetches.sql (624.85µs)
  2026/09/07 08:43:47 OK   00037_search_events_origin.sql (857.31µs)
  2026/09/07 08:43:47 OK   00038_search_events_profile_id.sql (837.09µs)
  2026/09/07 08:43:47 goose: successfully migrated database to version: 38
  2026/09/07 08:43:47 WARN memory size lookup failed; drawer returned without its partial marking error="drawer not found: drawer 44994722d9e4 was ended on 2026-09-07T08:43:47Z (\"the plan was dropped\"). Pass include_history to read it" drawer=44994722d9e402213026b6a1402a4b1cf64aa7a471d62b34000623cf19531920
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	5.649s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	7.740s
  FAIL
  ```
- 2026-09-07 · 20db1fb* · exit 0 · `go test ./internal/palace/ ./internal/mcptest/ -run 'TestTheFetchRatioNamesItsPopulation|TestTheFetchRateReachesTheToolSurfaceWithItsPopulation' -count=1 2>&1 | tee /tmp/adr028-t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t4.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1` · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · ms:18315
- 2026-09-07 · 20db1fb* · exit 0 · `go test ./internal/palace/ ./internal/mcptest/ -run 'TestTheFetchRatioNamesItsPopulation|TestTheFetchRateReachesTheToolSurfaceWithItsPopulation' -count=1 2>&1 | tee /tmp/adr028-t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t4.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1` · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · ms:18173
- 2026-09-07 · 20db1fb* · exit 0 · `go test ./internal/palace/ ./internal/mcptest/ -run 'TestTheFetchRatioNamesItsPopulation|TestTheFetchRateReachesTheToolSurfaceWithItsPopulation' -count=1 2>&1 | tee /tmp/adr028-t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t4.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1` · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · ms:18732
- 2026-09-07 · 20db1fb* · exit 0 · `go test ./internal/palace/ ./internal/mcptest/ -run 'TestTheFetchRatioNamesItsPopulation|TestTheFetchRateReachesTheToolSurfaceWithItsPopulation' -count=1 2>&1 | tee /tmp/adr028-t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t4.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1` · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · ms:18357
- 2026-09-07 · 20db1fb* · exit 0 · `go test ./internal/palace/ ./internal/mcptest/ -run 'TestTheFetchRatioNamesItsPopulation|TestTheFetchRateReachesTheToolSurfaceWithItsPopulation' -count=1 2>&1 | tee /tmp/adr028-t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t4.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1` · acceptance-sha256:d878d3958d2bf6cc437d57151a12047ba71976559c1574807d05e4a237454c82 · ms:18552
