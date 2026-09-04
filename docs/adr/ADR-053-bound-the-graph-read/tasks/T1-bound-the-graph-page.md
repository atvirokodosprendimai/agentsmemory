# Task ADR-053-T1: A graph answer that is bounded and says what it cut

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `boundGraphPage`, `am_kg_query` `limit`/`cursor`/`next_cursor`, `withheld` keyed by cause
**Consumes:** `responseBudget` (`internal/mcpserver/drawers.go:78`), `withheldByBudget` (`:84`)
**Data dependency:** hermetic for the unit; the falsifying figures in the ADR were taken against the live corpus and are reproduced in the test as a generated fan-out of the same size
**Proof map:** v1
**Rests-on:** `the exit code`, `the rendered response staying under responseBudget`, `the cursor returning the rest rather than a different page`

## Goal

Give `am_kg_query` the bound every other agent-facing read already has, plus the
continuation a fan-out needs, and make the page say which of three causes removed
what is missing.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/kg.go` | edit | `KGQueryInput` gains `Limit` and `Cursor`; `KGQueryResult.Withheld` becomes a map keyed by cause and gains `NextCursor` |
| `internal/mcpserver/kg.go` | edit | declare `limit` and `cursor`, render `next_cursor`, render the withheld map — and say all of it in the tool description, which is the only route by which a caller learns the shape |
| `internal/mcpserver/drawers.go` | edit | export the budget helper so the graph page spends the same 40,000 runes rather than a second number |
| `internal/mcpserver/kg_test.go` | edit | the gate, and the cursor round trip |

## Ordered Steps

1. [S1] Write `TestAGraphAnswerIsBoundedAndSaysWhatItCut` first and watch it fail: build a subject with more edges than the budget can carry, query it, and assert the rendered response is at or under `responseBudget`. It fails today because nothing bounds the page (TDD red).
2. [S2] Add `Limit` and `Cursor` to `KGQueryInput` and thread them into `KGTriplesBySubject`, `KGTriplesByObject` and `KGTriplesByPredicate` — all three, because a bound on one entry point is not a bound on the tool. Order deterministically (by `id`) so a cursor names a stable position rather than a row that may move between calls.
3. [S3] Reshape `KGQueryResult.Withheld` from a single count plus `WithheldStatus` into a map keyed by cause. Keep `status` as one key so the existing behaviour survives the reshape, and add `budget`. ⚠The reshape is the breaking half of this task: a caller reading the old field reads a map now, and the tool description must change in the same commit — a description that has gone false unships the capability it describes, which this repository already records as its own defect class.
4. [S4] Add `boundGraphPage` in `internal/mcpserver`, filling facts in order until the budget is spent, and returning what it could not fit. It must check the budget BEFORE appending each fact as well as after — a budget checked only before the loop is not a bound, which is the exact defect `headWithin` was written to fix on the drawer side.
5. [S5] Render `next_cursor` only when a page was actually cut, so its presence is the signal rather than an empty key every caller compares against. Add the hint naming it, in the shape the search page's hint already uses.
6. [S6] Assert the cursor round trip: paging with the returned cursor returns the REST of the fan-out, with no fact repeated and none skipped, and the union over pages equals the unpaged set. A cursor that returns a different page is worse than no cursor, because the caller believes they have seen everything. [proof: acceptance]
7. [S7] ⚠**The mutant is `boundGraphPage`'s call replaced by the unbounded slice** — the page renders every fact and the budget is computed and thrown away. That is the inert-mechanism shape this repository keeps shipping: the code exists, the tests exercise it, and nothing selects it. The test must go red. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/mcpserver/ -run 'TestAGraphAnswerIsBoundedAndSaysWhatItCut$|TestAGraphCursorReturnsTheRestExactlyOnce$' -count=1 2>&1 | tee /tmp/adr053-t1a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr053-t1a.out \
  && go test ./internal/palace/... ./internal/mcpserver/... -count=1 2>&1 | tee /tmp/adr053-t1b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr053-t1b.out
```

The units run alone first so neither can be carried by the regression half, then
both packages run because the `withheld` reshape touches a type every graph
caller in the tree reads.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAGraphAnswerIsBoundedAndSaysWhatItCut` | `internal/mcpserver/kg_test.go` | a fan-out larger than the budget renders at or under `responseBudget` and reports the `budget` key in `withheld` | — | S1, S4 |
| `TestAGraphCursorReturnsTheRestExactlyOnce` | `internal/mcpserver/kg_test.go` | paging with `next_cursor` returns the remaining facts, none repeated, none skipped, union equal to the unpaged set | — | S2, S5, S6 |
| `TestWithheldNamesEveryCauseThatRemovedSomething` | `internal/mcpserver/kg_test.go` | a page filtered by status AND cut by budget reports both keys rather than one overwriting the other | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the three tests above |
| 2 — something selects it | the `am_kg_query` handler calls `boundGraphPage`; S7's mutant deletes that call and the gate goes red |
| 3 — the caller can discover it | `limit`, `cursor` and `include_containment` are parameter descriptions, and `next_cursor` is named in the tool description — an `omitempty` field is invisible until the case that produces it, which this repository gates elsewhere |
| 4 — it is used | every `am_kg_query` call renders through it, and the corpus contains two entry points that exercise the cut today |

## Mutation Log

- 2026-09-04 · f3695ba* · mutant killed · exit 1 · `internal/mcpserver/kg.go` · the page renders every fact and the budget is computed and thrown away — the inert-mechanism shape this repository keeps shipping: the code exists, its unit tests exercise it, and nothing selects it · acceptance-sha256:7fe635c04d355bd6aaa11d06836da9aaf8a4c405e1842e32ec189b10c3aaf17e

## Invariants

- One budget. The graph page spends `responseBudget`, not a second constant — a second number is a second thing to keep in step with the client's real limit.
- The cursor is opaque and one-way. No offset arithmetic leaks into the response, so the ordering can change without breaking a caller mid-walk.
- `withheld` keys name causes, never counts of causes; a key is present only when it removed something.
- `KGTimeline`'s existing bound is untouched.

## Risks

- The `withheld` reshape breaks a reader of the old shape. Mitigated by changing the description in the same commit and by `TestWithheldNamesEveryCauseThatRemovedSomething` pinning the new shape.
- A deterministic order by `id` is not the order a caller finds most useful. Accepted: a stable order is what makes a cursor correct, and usefulness of ordering is not a question this task has evidence for.

## Stop Condition

Stop and ask if bounding the page requires changing what `am_search` returns.
The two read paths share a budget constant deliberately and share nothing else;
a change that reaches into the search page means the budget helper is being
reshaped rather than reused, which is a wider decision than this task owns.

## Out of Scope

- Hiding containment edges — T2 owns that, and this task must be provably a size bound on its own
- Bounding `Traverse`, `ListTunnels`, `ListHallways` or `FollowTunnels` (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
- 2026-09-04 · f3695ba* · exit 1 · `set -o pipefail …` · acceptance-sha256:7fe635c04d355bd6aaa11d06836da9aaf8a4c405e1842e32ec189b10c3aaf17e · ms:2532
  ```
  --- last 10 line(s) of stdout (of 42 after folding 42 raw)
  2026/09/04 12:54:28 OK   00034_billing_checkout_intents.sql (525.42µs)
  2026/09/04 12:54:28 OK   00035_billing_applied_orders.sql (351.67µs)
  2026/09/04 12:54:28 OK   00036_drawer_fetches.sql (440µs)
  2026/09/04 12:54:28 goose: successfully migrated database to version: 36
  --- FAIL: TestAGraphAnswerIsBoundedAndSaysWhatItCut (0.57s)
      kg_test.go:93: the graph answer is 106884 runes, past the 40000-rune budget every other read obeys — a response this size spills to a file the model never reads
      kg_test.go:100: withheld is not a map keyed by cause: <nil>
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.907s
  FAIL
  ```
- 2026-09-04 · f3695ba* · exit 0 · `set -o pipefail …` · acceptance-sha256:7fe635c04d355bd6aaa11d06836da9aaf8a4c405e1842e32ec189b10c3aaf17e · ms:24546
