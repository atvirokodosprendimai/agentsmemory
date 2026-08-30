# Task ADR-044-T5: Make a page report how many hits the budget made it withhold

**Depends-on:** T4
**Covers:** F-7, UC1-S4
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `withheld` page field
**Consumes:** `partialWithFetchID` (T4), `coveredRunes` (T3)
**Data dependency:** hermetic — driven by more fixture memories than a fixture budget can carry.

## Goal

Make a page cut short by the response budget say how many hits it dropped, so a short page is legible as short rather than read as an exhausted corpus.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | `overBudget` is already counted in the render loop at `:929` and discarded. The page-level number is its sibling: hits the budget prevented from appearing at all, as distinct from hits it trimmed. **The two must not be conflated** — a trimmed hit is on the page and marked (T4); a withheld hit is not on the page and is otherwise invisible |
| `internal/mcpserver/drawers.go` | edit | Add `withheld` to the `am_search` result and name it in the tool description at `:817`, without which the key is undiscoverable by construction and `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` fails |
| `internal/mcpserver/kg.go` | read only | `out["withheld"] = map[string]int64{res.WithheldStatus: res.Withheld}` at `:223` is the existing vocabulary for this idea. Reuse the shape rather than inventing a second one; if the shapes must differ, say why here |
| `internal/mcpserver/bootstrap.go` | edit or record | `am_bootstrap` already carries a truncation report of its own via `res.WireShape()`. Either extend it to the same vocabulary, or record why its report already satisfies F-7. The ADR names this as a required decision, not an optional one |
| `internal/mcpserver/readcost_spec_test.go` | edit | Turn `TestF7APageReportsWhatItWithheld` green. **Tag STAYS** — F-4 is still red in this file |

## Deviations recorded during execution

**1. "Withheld" had to be REDEFINED, and Affected Files above is wrong about this code.**
The row says *"a withheld hit is not on the page and is otherwise invisible."* It is not: the
render loop never DROPS a hit. Past the budget `headWithin` returns the empty string with
`cut=true`, so the hit arrives with its id, its metadata, its `content_truncated` marking — and
zero runes of the memory. **Withheld therefore means ON THE PAGE CARRYING NOTHING.** That is
already this repository's vocabulary rather than an invention: `am_list_drawers`' own description
says a bounded listing carries *"as much of their opening as the budget still allows — possibly
none"*. The definition is narrower than the record assumed and strictly checkable, which the
alternative — a hit that never appears — is not.

**2. The Stop Condition did NOT fire, and this is not a dodge of it — but the first draft of this
paragraph overclaimed and is corrected here.** It stops the task if *"the candidate set is truncated
before the budget is applied"*, because then the count is unknowable. `limit` DOES truncate the
candidate set before the budget, so the honest statement is narrower than "every candidate reaches
the loop": **`withheld` is exact over the hits `limit` admitted**, and hits `limit` excluded are not
withheld *by definition* (Invariant 1), not by an approximation. The count is therefore exact for the
thing it names rather than a lower bound on some larger quantity — and the larger quantity is one this
record explicitly declines to measure.

Measured 2026-08-29 on the six-memory fixture at `snippet_chars=100000`: `limit=4` → `count=4`,
per-hit runes `[12041 12041 12041 3877]`, **no `withheld` key**, while memories 5 and 6 exist and
would have been emptied at a higher limit. `limit=6` and `limit=10` → `count=6`, `withheld
{budget:2}`, per-hit `[12041 12041 12041 3877 0 0]`. That is the contract working: the `limit=4` page
is short because the caller asked for four, which is legible without a counter, and the `limit=6` page
is short because the budget ran out, which is not.

**3. Rung 3 is UNENFORCED, and the Reachability table above overstates it.** That row says the
description must name `withheld` *"or `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` fails"*.
It does not. Measured 2026-08-29 by deleting the word from the description: the gate stayed green
and the whole package passed. Its own doc comment predicts this — *"A THIRD population is invisible
to any struct-tag scan: conditional `map[string]any` keys, set inside `if` blocks… Out of scope
here"* — and `withheld` is exactly that, as is `kg.go`'s. The description names `withheld` anyway,
because the obligation is real; what is absent is a gate over it. Widening that universe is the
follow-up the gate already names, not this task.

**4. `am_bootstrap` — RECORDED, NOT EXTENDED (step 5).** `internal/mcpserver/bootstrap.go` emits
`res.WireShape()` verbatim and applies **no rune budget at all** — no `responseBudget`, no
`headWithin`. So the failure F-7 exists to remove cannot occur there: no bootstrap record is ever
rendered to zero. Its only loss mode is record-level, and `BootstrapTruncation` already reports it
*unconditionally* with `omitted`, `reason` and `how_to_fetch` — a stronger contract than
`withheld`, since it is present even at zero and `parityTruncation` fails the response when
`omitted > 0` carries no `how_to_fetch`. Adding a second name for the same fact on that surface
would make the vocabulary worse, not more consistent. **Decision: bootstrap's existing truncation
report satisfies F-7; no change.**

**5. Shape, against `kg.go:223`.** Kept: a count keyed by what withheld it,
`map[string]int{"budget": n}`. `kg_query` keys by status because status is its axis; there is one
withholder here, and the key names it so a second could join without changing a shape callers
parse. Differences from that precedent, both deliberate: `int` not `int64` (the count is bounded by
`MaxSearchLimit`), and the remedy is appended to the existing `note` rather than given a `hint` key
of its own, because a page can be trimmed AND cut and two competing keys would each tell half the
story. **The old note went false and was fixed in the same commit** — it said the tail was
*"windowed instead"*, which is untrue of a hit carrying nothing and would teach a caller the memory
is short.

**6. A mutant that fails to BUILD grades nothing.** The first draft of the conflation mutant deleted
the `if trimmedHere { overBudget-- }` block outright and Go rejected it as `declared and not used`,
which proves the symbol is referenced and says nothing about what the test observes. Re-run as
`overBudget -= 0`, which compiles and leaves the hit counted in both totals.

## Ordered Steps

1. Confirm `TestF7APageReportsWhatItWithheld` is red for the right reason. Verified 2026-08-29; the binding names the trap explicitly — *"counting hits dropped for relevance as withheld — the count is about the BUDGET, not about ranking, which this spec does not touch"*.
2. Separate the two counters in the render loop: trimmed (already `overBudget`) and withheld (new). Do not reuse one variable for both.
3. Emit `withheld` on the page, following `kg.go:223`'s shape.
4. Turn F-7 green, with the ranking confusion as an explicit negative case: hits that never entered the candidate pool, or were dropped by `limit`, are NOT withheld. Only the budget withholds.
5. Decide `am_bootstrap` — extend or record, in this file's prose.

## Acceptance

```bash
set -o pipefail
go test -tags readcostspec ./internal/mcpserver/ -run 'TestF7APageReportsWhatItWithheld' -count=1 2>&1 | tee /tmp/adr044-t5.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t5.out && go test -tags readcostspec ./internal/mcpserver/ -run 'TestF1CoverageCountsEveryDisclosedRange|TestF2NoHitIsSilentlyPartial' -count=1 && go vet ./... && go test ./... -count=1
```

⚠ **This fence cannot observe step 5.** As in T3: `TestF7…` passes whether or not `am_bootstrap` was
decided, so **the sign-off for this task must name the `am_bootstrap` decision** — extended to the
`withheld` vocabulary, or a written reason its own truncation report already satisfies F-7. The ADR
makes this a required decision rather than an option; without the sign-off requirement it is a
requirement no gate can see.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF7APageReportsWhatItWithheld` | `internal/mcpserver/readcost_spec_test.go` | A budget-shortened page reports its withheld count; an exhausted corpus reports zero | F-7, UC1-S4 |
| `TestF7APageReportsWhatItWithheld/hits_dropped_by_limit_are_not_withheld` | same | The ranking confusion the binding names, as a subtest inside the fence | F-7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF7APageReportsWhatItWithheld` |
| 2 — something selects it | The counter is emitted from `registerSearch`'s result assembly (`drawers.go:815`). Mutation: report a constant zero and watch the test go red |
| 3 — the caller can discover it | The `am_search` description must name `withheld`; `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` matches on a word boundary, so a mention inside another word does not count |
| 4 — it is used | With no cursor, the count is the ONLY evidence a withheld hit existed. Whether callers act on it is what T1's rule measures |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->
- 2026-08-29 · bf182e4* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the Reachability table rung-2 mutation: report a constant zero. A page that emptied two hits still claims it withheld none, which is exactly the state F-7 exists to make impossible · acceptance-sha256:5fe6e1cecd6abe6feeb64f750d59d22df30310df028f3d8b1b020c4ae0bbbfd7
- 2026-08-29 · bf182e4* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · sever the back-out so one hit is counted as trimmed AND withheld. The conflation the Risks section names: the whole-memory branch increments overBudget before headWithin can empty the hit, so both numbers go wrong while each looks plausible · acceptance-sha256:5fe6e1cecd6abe6feeb64f750d59d22df30310df028f3d8b1b020c4ae0bbbfd7
- 2026-08-29 · bf182e4* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · widen the classifier so every trimmed hit counts as withheld — the binding kill-case, counting hits the budget merely shortened as hits it withheld. Caught by the limit subtest on a page the budget never cut · acceptance-sha256:5fe6e1cecd6abe6feeb64f750d59d22df30310df028f3d8b1b020c4ae0bbbfd7

## Invariants

- `withheld` counts hits the BUDGET excluded. Never hits excluded by `limit`, by relevance, or by `survivorsFrom`'s history filter.
- Zero withheld on a page that was cut is a defect, not an empty value.
- No cursor or offset is added; the count is the whole contract.

## Risks

- Conflating trimmed and withheld would make the number meaningless while looking correct. Mitigated by keeping two counters and by the negative subtest.
- A second withheld vocabulary diverging from `kg.go`'s. Mitigated by reusing that shape or recording the difference.

## Stop Condition

Stop if the render loop cannot know how many hits it withheld — if the candidate set is truncated before the budget is applied, the count is unknowable and reporting a guess would be worse than reporting nothing. Then the loop's structure has to change, which is larger than this task.

**What would make this criterion impossible to fail:** a fixture corpus small enough that the budget is never reached produces zero withheld on every page and the assertion passes vacuously. The fixture must be large enough to force a cut.

## Out of Scope

- Adding a cursor so withheld hits are resumable (permanent: declined in the spec's Non-Goals at Grill Log 8 — a second resumption contract for the job `am_get_drawer` already does is the cost this record exists to avoid)
- Removing the build tag — T6 owns it

## Verification Log

<!-- Tool-written by `adr-verify`. -->
- 2026-08-29 · f5021a7 · exit 1 · `set -o pipefail …` · acceptance-sha256:5fe6e1cecd6abe6feeb64f750d59d22df30310df028f3d8b1b020c4ae0bbbfd7
  ```
  --- FAIL: TestF7APageReportsWhatItWithheld (0.00s)
      readcost_spec_test.go:187: not built yet — F-7 (UC1-S4): a page must report how many hits it withheld. `am_search` has limit but no offset or cursor (drawers.go:786-800, M-10) and the spec declines to add one (Non-Goals, Grill Log 8), so the count is the ONLY evidence a withheld hit existed — without it a page cut short by the response budget is indistinguishable from an exhausted corpus. This is a NEW obligation restored from old F-2, kept as its own fact so the scope increase is visible rather than folded into an existing binding. Kill it by reporting zero withheld on a page that was cut, or by counting hits dropped for relevance as withheld — the count is about the BUDGET, not about ranking, which this spec does not touch
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.018s
  FAIL
  ```
- 2026-08-29 · 519e640 · exit 1 · `set -o pipefail …` · acceptance-sha256:5fe6e1cecd6abe6feeb64f750d59d22df30310df028f3d8b1b020c4ae0bbbfd7
  ```
  2026/08/29 18:43:45 OK   00033_drawers_superseded_by_idx.sql (277.75µs)
  2026/08/29 18:43:45 OK   00034_billing_checkout_intents.sql (348.08µs)
  2026/08/29 18:43:45 OK   00035_billing_applied_orders.sql (242.46µs)
  2026/08/29 18:43:45 OK   00036_drawer_fetches.sql (316.96µs)
  2026/08/29 18:43:45 goose: successfully migrated database to version: 36
  --- FAIL: TestF7APageReportsWhatItWithheld (0.09s)
      readcost_spec_test.go:273: a page that delivered 2 hit(s) carrying nothing reported no withheld count — it is indistinguishable from an exhausted corpus, which is the whole of F-7
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.102s
  FAIL
  ```
- 2026-08-29 · bf182e4 · exit 0 · `set -o pipefail …` · acceptance-sha256:5fe6e1cecd6abe6feeb64f750d59d22df30310df028f3d8b1b020c4ae0bbbfd7
