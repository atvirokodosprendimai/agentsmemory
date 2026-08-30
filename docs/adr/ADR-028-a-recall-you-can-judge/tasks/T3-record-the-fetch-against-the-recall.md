# Task ADR-028-T3: Record the fetch against the recall

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `drawer_fetches` rows; `Service.RecordFetch`; `Service.CountFetches`; `fetches` and `recalls_fetched` on `am_recall_stats`
**Consumes:** `search_id` returned by `am_search` and accepted by `am_get_drawer` (T1)
**Data dependency:** hermetic

## Goal

A fetch that names the recall which sent it there is recorded durably, and the count is readable through a served tool.

⚠ **Its trigger fired and could not be observed, which is why this ran now.** T1 deferred this task behind *"the first week `am_get_drawer` receives a non-empty `search_id` from a client that is not a test."* On 2026-08-29 a non-test client sent one — and nothing recorded it, because `annotateSearchID` puts the id on a sampled span and nowhere else. Checked the same day: no first-party client calls `am_get_drawer` at all (six scripts in `clients/claude-code/hooks/`, none fetches a drawer), so the trigger was never waiting on wiring somebody forgot. It was conditioned on an agent choosing to pass an optional argument it read in a tool description, and its satisfaction left no durable trace. A trigger that cannot be observed cannot start the task it gates.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00036_drawer_fetches.sql` | add | The table. `search_id` is deliberately NOT a foreign key: `SkipTelemetry` means some recalls write no `search_events` row, so a constraint would silently drop exactly the fetches that came from an unlogged recall |
| `internal/palace/fetchlog.go` | add | `RecordFetch` (best-effort, refuses what would pollute the join) and `CountFetches` (two raw counts, never a rate) |
| `internal/mcpserver/drawers.go` | edit | **The call sites — what SELECTS the recorder.** `recordFetchJoin` runs only where a fetch has already succeeded, in both the single and `whole` branches. Also corrects the `search_id` description, which said "not yet stored durably" |
| `internal/mcpserver/admin.go` | edit | **What makes the write observable.** `am_recall_stats` publishes `fetches` and `recalls_fetched`; a count nothing publishes is exactly the defect this task exists to close |
| `internal/mcpserver/mutationset_test.go` | edit | Classifies the recorder as an INCIDENTAL write. Without it `am_get_drawer` becomes a write tool and every read-only member loses the ability to fetch a drawer — the gate caught this, and it is the same judgement `SearchPage` already carries |

## Ordered Steps

1. Write `internal/mcptest/fetchjoin_reach_test.go` and run it red: it drives the real MCP transport and reads the count back through `am_recall_stats`, so it fails while nothing records or publishes anything.
2. Add the migration. Confirm `goose` applies it and the head moves to 36 — ⚠ the next free number is **00036**, not 00034: ADR-042's billing migrations took 00034 and 00035, and a stale note in a PR review said otherwise.
3. Add `internal/palace/fetchlog.go` with `RecordFetch` and `CountFetches`, plus `internal/palace/fetchlog_test.go` for the recorder's own behaviour.
4. Call the recorder from both success paths in `registerGetDrawer` — never from the error paths, because a request for an id that does not resolve is not a click and would put misses in the numerator of every ratio derived from this.
5. Publish `fetches` and `recalls_fetched` on `am_recall_stats`, as RAW COUNTS. Not a rate: the denominator is recalls THAT WERE LOGGED, and a ratio needs `profile_id` beside it to mean anything — both are T4.
6. Classify `RecordFetch` and `CountFetches` in `incidentalWrites` with written reasons.
7. Re-run the fence green, then MUTATE: sever each call site in turn and confirm the reach test goes red.

## Acceptance

```bash
go test ./internal/mcptest/ -run 'TestAFetchNamingItsRecallIsRecordedAtTheToolSurface' -count=1 2>&1 | tee /tmp/adr028-t3.out && go test ./internal/palace/ -run 'TestAFetchIsRecordedAgainstTheRecallThatSentIt' -count=1 2>&1 | tee -a /tmp/adr028-t3.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t3.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ ./internal/doclint/ ./internal/repohygiene/ -count=1
```

Both named tests are run under their own filters first, so neither the already-passing suites nor each other can carry the verdict — `adr-lint` rejected a first version whose filter selected only one of the two tests its own table promised. `doclint` and `repohygiene` are in the regression half deliberately — both went red on the first attempt at this task, one for a doc comment that did not open with its declaration and one for an undeclared example wing.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAFetchNamingItsRecallIsRecordedAtTheToolSurface` | `internal/mcptest/fetchjoin_reach_test.go` | Rung 2: the served `am_get_drawer` records the fetch and `am_recall_stats` publishes it; a fetch naming no recall, and one that resolved nothing, record nothing; `whole` on a chunked memory is ONE read | — |
| `TestAFetchIsRecordedAgainstTheRecallThatSentIt` | `internal/palace/fetchlog_test.go` | The recorder itself: two fetches from one recall count 2 fetches / 1 recall; malformed ids, missing drawer and missing tenant record nothing; the count is team-scoped and window-bounded | — |

Shapes enumerated before writing the assertions, because the creation path can already produce all of them: a fetch whose id resolves to nothing (not a click); a `whole` fetch of a chunked memory (one read, not one per chunk); two fetches from one page (the distinct-recall count must not move); a fetch from another tenant; and a `search_id` that is well-formed but names a recall this server never logged — accepted deliberately, since `SkipTelemetry` makes that a real and interesting state rather than an error.

⚠ **The window has ONE-SECOND resolution and the test says so.** `created_at` is RFC3339 seconds, the same format `search_events` uses, so no sub-second window can exclude a row written in the same second. A first draft asked for a one-nanosecond window and failed against correct code.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestAFetchIsRecordedAgainstTheRecallThatSentIt` |
| 2 — something selects it | `TestAFetchNamingItsRecallIsRecordedAtTheToolSurface` over the real transport. **Demonstrated:** severing the single-fetch call site turns it red while every palace unit test stays GREEN — the component-instead-of-the-selection defect, reproduced deliberately |
| 3 — the caller can discover it | `am_get_drawer`'s `search_id` description no longer says "not yet stored durably"; `am_recall_stats`'s description names both counts and says why they are not a rate |
| 4 — it is used | `fetches` and `recalls_fetched` on `am_recall_stats` are the measurement. Nothing has been read from them yet — the first non-zero count is the first evidence any agent has ever named the recall that sent it to a memory |

## Mutation Log

- 2026-08-29 · df4857d* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · Severs the single-fetch call site. Every palace unit test stays GREEN under this mutant — the component exercised instead of the selection — so only the mcptest reach test can kill it. · acceptance-sha256:2ebe8f2901ac3e4544d27a4dae347e763b2a521ce713dd07796828f85af6a7d1
- 2026-08-29 · df4857d* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · Severs the whole-fetch call site. Kills the assertion that a whole fetch of a chunked memory is ONE read; without it the count silently weights long notes by their chunk count. · acceptance-sha256:2ebe8f2901ac3e4544d27a4dae347e763b2a521ce713dd07796828f85af6a7d1
- 2026-08-29 · df4857d* · mutant killed · exit 1 · `internal/palace/fetchlog.go` · Removes every refusal in RecordFetch. A malformed search_id, a missing drawer and a missing tenant would all enter the join, which is what makes any ratio derived from it meaningless. · acceptance-sha256:2ebe8f2901ac3e4544d27a4dae347e763b2a521ce713dd07796828f85af6a7d1

## Invariants

- A statistics write can never fail a read: `recordFetch` is best-effort, exactly like `recordSearch`.
- Only a fetch that RETURNED something is recorded.
- A `whole` fetch is one row, not one per chunk.
- `am_get_drawer` stays a READ tool. The row is observability about a read and stores no memory anyone can recall, which is why it belongs in `incidentalWrites` beside `SearchPage`.
- No rate is published from this table until T4.

## Risks

- The table grows one row per fetch with no retention policy. `search_events` has the same property and the same absence; if one gets a retention story they both should. Not solved here.
- `search_id` is client-supplied. It is shape-checked (`palace.ValidSearchID`) before storage, and a well-formed id naming an unlogged recall is stored on purpose — see Tests.
- A future reader may divide `fetches` by `searches` and publish it. The tool description and the code comment both say why that is wrong; T4 is where it becomes right.

## Stop Condition

Stop if classifying the recorder as an incidental write turns out to require the writer role for `am_get_drawer` — that would mean read-only members can no longer fetch a drawer, which is a worse outcome than having no fetch signal at all, and the task should be withdrawn rather than shipped that way.

## Out of Scope

- The ratio and `profile_id` on the durable row (deferred: ADR-028 T4, in this directory).
- A relevance metric derived from the signal (deferred: `docs/adr/BACKLOG.md` §"From ADR-028" — the signal must be observed before anything is derived from it).
- Retention or pruning for `drawer_fetches` or `search_events` (deferred: `docs/adr/BACKLOG.md` §"From ADR-028").

## Verification Log
- 2026-08-29 · df4857d* · exit 0 · `go test ./internal/mcptest/ -run 'TestAFetchNamingItsRecallIsRecordedAtTheToolSurface' -count=1 2>&1 | tee /tmp/adr028-t3.out && go test ./internal/palace/ -run 'TestAFetchIsRecordedAgainstTheRecallThatSentIt' -count=1 2>&1 | tee -a /tmp/adr028-t3.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr028-t3.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ ./internal/doclint/ ./internal/repohygiene/ -count=1` · acceptance-sha256:2ebe8f2901ac3e4544d27a4dae347e763b2a521ce713dd07796828f85af6a7d1
