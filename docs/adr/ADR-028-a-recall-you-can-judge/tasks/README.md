# ADR-028 Tasks

Implementation tasks for ADR-028: Return the identifier and the score a recall was decided by. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` headers. This README is a derived index — when it disagrees with a task file, the task file wins and the README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | T1 |
| 4 | T4 | T3 |
| T3 | Record the fetch against the recall | `drawer_fetches` rows; `RecordFetch`; `CountFetches`; `fetches` and `recalls_fetched` on `am_recall_stats` | `search_id` (T1) | done | `go test ./internal/mcptest/ -run 'TestAFetchNamingItsRecallIsRecordedAtTheToolSurface' -count=1 …` |
| T4 | Report the ratio, with the profile beside it | `profile_id` on `search_events`; a fetch ratio reported with its population | `drawer_fetches`, `CountFetches` (T3) | pending | `go test ./internal/palace/ -run 'TestTheFetchRatioNamesItsPopulation' -count=1 …` |

T1 and T2 are independent in content and touch the same file, so they are ordered rather than parallel to keep the diff reviewable. T1 goes first because it is the half the recording task builds on.

T3 and T4 were the deferred half of this record and are now task files rather than a BACKLOG pointer. T3's own trigger — "the first week `am_get_drawer` receives a non-empty `search_id` from a non-test client" — was MET on 2026-08-29 and left no durable trace, which is what a trigger conditioned on an unobservable event does. T4 stays pending because a ratio needs `profile_id` beside it to mean anything.

## Task Index

| Task | Goal | Produces | Consumes | Status | Acceptance |
|------|------|----------|----------|--------|------------|
| T1 | Return the recall's identifier, and accept it back | `search_id` on the `am_search` response; the optional `search_id` argument on `am_get_drawer` | none | done | `go test ./internal/mcptest/ -run "TestSearchResponseCarriesItsSearchID\|TestGetDrawerSchemaAdvertisesSearchID\|TestGetDrawerIgnoresAnUnknownSearchID"` |
| T2 | Expose the score the order was actually decided by | `blended_score` on each `am_search` hit | none | done | `go test ./internal/mcpserver/ -run "TestSearchToolDescriptionSaysBlendedIsPoolRelative\|TestRenderedHitCarriesTheBlendedValueNotTheRerankScore"` + `go test ./internal/palace/ -run TestHitCarriesTheScoreItWasOrderedBy` |

## Not a task here

The third half of this work — recording the fetch against the recall and reporting the ratio — is deliberately NOT a task file. It is specified in the parent ADR's Out of Scope with an explicit trigger: the first week `am_get_drawer` receives a non-empty `search_id` from a client that is not a test. Writing it as a pending task file would put a plan in the corpus for work whose precondition does not exist yet, and `adr-debt` sweeps the deferral so it resurfaces at the next `/quality-harness:adr-write` instead of being forgotten.
