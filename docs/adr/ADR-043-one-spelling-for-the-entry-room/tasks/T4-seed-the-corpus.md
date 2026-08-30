# Task ADR-043-T4: Seed this repository's entry point

**Depends-on:** T1, T2, T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the `wing_agentmemories` `llm_init` root drawer and its `must.*` facts
**Consumes:** the hosted classification (T3); `Bootstrap` returning `must.*` targets outside the root room (T2); the corrected §4.3 (T1)
**Data dependency:** needs the local palace (`http://localhost:8080/mcp`) and T3's recorded classification. Not reachable from a clean checkout, which is why Acceptance is human-observed and the sign-off records the counts the run was taken against.

## Goal

Seed `wing_agentmemories` with the canonical root and its mandatory tier, so `AGENTS.md`'s documented traversal is executable.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/BACKLOG.md` | edit | Records the before/after counts and the ids actually minted |
| `docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T4-seed-the-corpus.md` | edit | The sign-off line, written by `adr-verify --human` |
| `docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/README.md` | edit | The status cell the sign-off maps onto — `done` for ship, `failed` for withdraw, `blocked` for blocked |

No source file changes. What SELECTS the seeded data is `Bootstrap`'s `must.*` walk from T2; without it this task writes drawers nothing reads, which is why `Depends-on` names it.

## Ordered Steps

1. Refuse to start unless T3 recorded `canonical` or `nothing`. On `label`, this task does not run.
2. **Discover every id in full, and never type one.** `am_list_drawers(wing: "wing_agentmemories", room: "llm_index")`, then the same for `llm_open_threads`, `llm_corrections` and `human-decisions`, and copy each `id` verbatim from the response. ⚠ An abbreviated id is not a short id — `am_kg_add` validates only provenance, so a truncated subject or object mints a NEW node silently and the tier points at nothing. An earlier draft of this task listed two ids elided to `0715011203df…` and `8814ff9f0f…`, which is exactly the shape that fails.
3. File the root drawer into `wing_agentmemories` room `llm_init`, content opening `WHAT MUST I LOAD AT THE START OF A SESSION?`, following the §4.3 T1 corrected. Copy the returned id verbatim.
4. `am_kg_add` one `must.*` fact per mandatory drawer, subject = the root drawer's full id, object = that drawer's full id.
5. Verify the new graph BEFORE retiring anything: `am_kg_query(entity: "<root id>", direction: "outgoing")` returns one fact per drawer filed in step 4, `resolution: "matched"`, and every object resolves through `am_get_drawer`.
6. Only then retire the label-shaped facts with `am_kg_invalidate`, one per fact, each with a reason naming this record. ⚠ **`am_kg_supersede` CANNOT be used here and an earlier draft said to use it.** It takes `subject` and `predicate` as required arguments and replaces the OBJECT only — its own description says *"The relationship. Unchanged by a supersede — this replaces the OBJECT"* (`internal/mcpserver/kg.go:129-130`). The old facts are `must` → `must_load` → *label* and the new ones are `<root id>` → `must.*` → *drawer id*: different subject and different predicate, so this is a retirement plus a new fact, not a superseded value. Invalidating does not erase — the label protocol stays readable as history, which is the point.
7. Verify by calling `am_bootstrap(wing: "wing_agentmemories")` and confirming it returns the mandatory tier — not `unknown_term`, and not the root room's own drawers alone. Then run `AGENTS.md`'s documented traversal by hand and confirm its first call now returns drawers, which it does not today.
8. Sign off with `adr-verify --human`, recording the counts before and after and the decision word.

## Acceptance

Acceptance is human-observed: an operator runs the steps above against the local palace and signs off with `adr-verify <this file> --human "<one line>"`, using exactly one of the three decision words. All three templates are given, because a template offering only the happy word is how an honest third outcome ends up in free text no tool reads.

The seeding ran and the entry point answers:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T4-seed-the-corpus.md --human "seeded <N> must.* facts from root <full id>; am_bootstrap returns <M> tier drawers; <K> label facts invalidated; decision ship"
```

Something in the seeding cannot be completed and the record is stalled rather than wrong:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T4-seed-the-corpus.md --human "seeding halted at step <N>: <what happened>; nothing left half-applied; decision blocked"
```

The run shows the record's direction was wrong rather than merely stalled:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T4-seed-the-corpus.md --human "<what was found>; ADR-043's direction does not hold; decision withdraw"
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAHumanObservedSignOffAgreesWithTheIndex` | `internal/repohygiene/humansignoff_test.go` | The sign-off names one of the three decision words and the sibling README carries the status it maps to — existing gate, this task is a new member of its universe | — |
| `TestASignOffThatSaysStopIsCaught` | `internal/repohygiene/humansignoff_test.go` | The same comparison over fixtures that are wrong — existing gate, no change | — |

No new test. This task writes data, and a unit test asserting that data exists in a live palace would test the operator's run rather than the code — which is what `Acceptance is human-observed` exists for.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The root drawer and its `must.*` facts, returned by `am_list_drawers` and `am_kg_query` in steps 5 and 7 |
| 2 — something selects it | `Bootstrap`'s `must.*` walk from T2; step 7 calls `am_bootstrap` and reads the tier back |
| 3 — the caller can discover it | `AGENTS.md`'s traversal, unchanged and now executable; the corrected §4.3 from T1 |
| 4 — it is used | Nothing measures this yet. Whether sessions call `am_bootstrap` once it answers is unmeasured, and `am_search` ran 52 times in 8,256 tool calls in session `ee8f1fc1`, which is the number that would have to move |

## Mutation Log

## Invariants

- Nothing is written until T3's classification is recorded and is not `label`.
- Every id is copied from a response, never typed or abbreviated.
- The new facts are verified to resolve BEFORE any old fact is retired, so a failure between the two leaves the old protocol intact rather than leaving the wing with neither.
- The two `llm_index` drawers keep their ids. They become `must.*` targets; they are not re-created.
- Label-shaped facts are invalidated with a reason, never deleted — the old protocol stays readable as history.

## Risks

- A derived-edge backfill is applied instead of seeding a real root, producing `matched` with only the root room's drawers. Forbidden by the Stop Condition, and T2's test is what makes the shortcut fail rather than pass.
- Steps 3-6 are hand-run MCP calls with no transaction across them. Mitigated by ordering: the new facts exist and are verified before any old one is retired, so the failure mode is two current protocols rather than none.
- An id is transcribed rather than copied, minting a node that resolves to nothing. Step 2 states it and step 5 catches it, because a mistyped object fails to resolve through `am_get_drawer`.

## Stop Condition

Stop if T3 recorded `label`. Stop also if anyone proposes satisfying this task by backfilling derived containment edges for the existing rooms — that produces false reachability, is a rejected Alternative in the ADR, and would pass a check that only asked whether `am_entry_point` resolves.

What would make this criterion impossible to fail on the data available: nothing, because step 5 verifies against the graph rather than against the operator's account of it, and step 7 reads the tier back through the served tool.

## Out of Scope

- Reading the hosted palace — T3.
- Backfilling corpora on deployments other than the two named (deferred: `docs/adr/BACKLOG.md` §"Four spellings of one entry point").
- Giving the entry point's data a producer in the product rather than a procedure run by hand (deferred: Follow-ups, ADR-043).

## Verification Log
