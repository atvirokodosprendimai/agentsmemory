# Task ADR-028-T4: Report the ratio, with the profile beside it

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `profile_id` on the durable `search_events` row; a fetch ratio reported with its population
**Consumes:** `drawer_fetches` rows and `CountFetches` (T3)
**Data dependency:** needs logged recalls and at least one recorded fetch. Hermetic for the migration and the arithmetic; the REPORTED number needs a real window, and the sign-off records what it was taken against.

## Goal

Turn two raw counts into a rate nobody can quote past its population.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/000NN_search_events_profile_id.sql` | add | Primitive #1's other half. `profile_id` is on the span today and absent from the durable row, which is what makes a ratio uninterpretable — "38% of recalls were followed by a fetch" means nothing without knowing which ranking profile produced them. Allocate the number at authoring time; 00036 was taken by T3 |
| `internal/palace/fetchlog.go` | edit | The ratio, computed over recalls THAT WERE LOGGED and grouped by profile |
| `internal/mcpserver/admin.go` | edit | Publishing it, with the population named in the response rather than in a comment |

## Ordered Steps

1. Write the failing test first: a ratio computed over two profiles must not collapse into one number, and a window with zero logged recalls must report no rate rather than a division by zero.
2. Add the migration and populate `profile_id` on write.
3. Compute the ratio per profile, over logged recalls only.
4. Publish it with its denominator and its profile beside it — ADR-007's rule, and this ADR's own deferral says the number is meaningless without them.
5. ⚠ **Run the canary before trusting any zero.** Confirm the instrument can report a POSITIVE — one logged recall, one fetch naming it, a non-zero rate — before reporting any figure at all. This corpus has a rule earned seven times in one evening, and the entry this task descends from was itself written without checking that its instrument existed.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestTheFetchRatioNamesItsPopulation' -count=1 2>&1 | tee /tmp/adr028-t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t4.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheFetchRatioNamesItsPopulation` | `internal/palace/fetchlog_test.go` | The rate is computed over logged recalls only, is reported per profile, and is withheld rather than zero when the window holds no logged recall | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestTheFetchRatioNamesItsPopulation` |
| 2 — something selects it | The `am_recall_stats` handler; a mutation removing the ratio from the response must go red |
| 3 — the caller can discover it | The tool description names the rate, its denominator and its profile |
| 4 — it is used | Nothing measures this yet. The first quoted rate is the point at which it becomes usable, and it must not be quoted before step 5's canary |

## Mutation Log

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
