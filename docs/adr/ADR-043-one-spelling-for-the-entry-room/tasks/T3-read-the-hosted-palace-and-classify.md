# Task ADR-043-T3: Read the hosted palace and classify what it holds

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the hosted classification — `canonical` | `label` | `nothing` — recorded in `BACKLOG.md`, which T4 consumes and which can stop this record
**Consumes:** none
**Data dependency:** needs a live hosted palace. Not reachable from a clean checkout, which is why Acceptance is human-observed and the sign-off records the workspace the read was taken against.

## Goal

Resolve ADR-036 T7's 25-node claim against the hosted palace, and record the answer, BEFORE any other task spends effort on a direction it could refute.

⚠ **This task exists as its own dependency-free task because an earlier draft made it step 1 of the seeding task, which ran third.** That ordering let T1 and T2 land before anybody learned whether the direction survives contact with the hosted deployment — and this task's whole purpose is to be able to stop the record. A stopping condition that runs after two thirds of the work is not one.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/BACKLOG.md` | edit | §"Four spellings of one entry point" records the classification whichever way it falls — including "nothing", which would mean ADR-036 T7's observation was a fixture |
| `docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T3-read-the-hosted-palace-and-classify.md` | edit | The sign-off line, written by `adr-verify --human` |
| `docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/README.md` | edit | The status cell the sign-off maps onto — `done` for ship, `failed` for withdraw, `blocked` for blocked |

No source file changes and no palace writes. This task only READS the hosted workspace and records
what it found; T4 is where anything is written, and T4 depends on this task's recorded answer.

## Ordered Steps

1. **Read the hosted palace before writing anything anywhere.** `am_list_drawers(wing: "wing_agentmemories", room: "llm_init")` against the hosted workspace, then — if it returns drawers — `am_kg_query(entity: "<root drawer id>", direction: "outgoing")` and record the subject/predicate/object shapes. This is the discriminator `BACKLOG.md` names, and it is used instead of `am_entry_point`, which cannot tell "no such room" from "drawers filed before derived containment edges shipped".
2. Classify the result into exactly one of three, and record it in `BACKLOG.md` with the date and the workspace it was taken against: **canonical shape** (root-id → `must.*` → drawer-id) → the decision is confirmed and this is the local palace's catch-up; **label shape** (the local corpus's `must` → `must_load` → label) → the deployments have diverged, STOP; **nothing** → ADR-036 T7's observation was a fixture, record that, and continue.
3. If the classification is **label shape**, STOP and hand the record to the owner: the deployments have diverged and migrating either one strands the other. If it is **canonical** or **nothing**, record that and release T4.

## Acceptance

Acceptance is human-observed: an operator runs the three steps above against the two named palaces and
signs off with `adr-verify <this file> --human "<one line>"`, using exactly one of the three decision
words. All three templates are given, because a template that offers only the happy word is how an
honest third outcome ends up in free text no tool reads — measured 2026-08-28 in this corpus, where
ADR-001 T3's hint offered `decision <ship|withdraw>`, the run reached a third state, and every routing
tool answered `done`.

The migration ran and the entry point answers:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T3-read-the-hosted-palace-and-classify.md --human "hosted read <date>, workspace <slug>: <canonical|nothing>; T4 released; decision ship"
```

The hosted palace holds the label shape, so the deployments have diverged and nothing was written:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T3-read-the-hosted-palace-and-classify.md --human "hosted read <date>: label shape, deployments diverged, nothing written; decision blocked"
```

The hosted read shows the record's direction was wrong rather than merely stalled:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T3-read-the-hosted-palace-and-classify.md --human "hosted read <date>: <what was found>; ADR-043's direction does not hold; decision withdraw"
```

`blocked` and `withdraw` are different outcomes and both are offered deliberately: one says the
migration cannot proceed, the other says the decision was wrong.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAHumanObservedSignOffAgreesWithTheIndex` | `internal/repohygiene/humansignoff_test.go` | The sign-off names one of the three decision words and the sibling README carries the status it maps to — existing gate, this task is a new member of its universe | — |
| `TestASignOffThatSaysStopIsCaught` | `internal/repohygiene/humansignoff_test.go` | The same comparison over fixtures that are wrong — existing gate, no change | — |

No new test is added. This task writes data, and a unit test asserting that data exists in a live
palace would be a test of the operator's run rather than of the code — which is exactly the shape
`Acceptance is human-observed` exists for.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The root drawer and its `must.*` facts, returned by `am_list_drawers` and `am_kg_query` in step 6 |
| 2 — something selects it | `Bootstrap`'s `must.*` walk from T2; step 6 calls `am_bootstrap` and reads the tier back |
| 3 — the caller can discover it | `AGENTS.md`'s traversal, unchanged and now executable; the corrected §4.3 from T1 |
| 4 — it is used | Nothing measures this yet. Whether sessions call `am_bootstrap` once it answers is unmeasured, and `am_search` ran 52 times in 8,256 tool calls in session `ee8f1fc1`, which is the number that would have to move |

## Mutation Log

## Invariants

- Nothing is written to any palace before step 1 has been read and classified.
- The two `llm_index` drawers keep their ids. They are re-filed as `must.*` targets, never re-created.
- Label-shaped facts are superseded, not deleted, so the old protocol stays readable as history.
- The hosted palace is READ in step 1 and never written by this task.

## Risks

- A derived-edge backfill is applied instead of seeding a real root, producing `matched` with only the root room's drawers. Forbidden by the Stop Condition, and T2's test is what makes the shortcut fail rather than pass.
- Steps 3-5 are hand-run MCP calls with no transaction across them, so a failure between 4 and 5 leaves both fact shapes current. Mitigated by step 5 using `am_kg_supersede`, which is one transaction, and by doing it after the new edges exist rather than before.
- The sign-off records a `ship` for a run that only touched the local palace. Mitigated by the template requiring the hosted read's date and result in the same line.

## Stop Condition

Stop and ask the owner if step 1 returns the LABEL shape: the two deployments have diverged and
migrating either one silently strands the other. Stop also if anyone proposes satisfying this task by
backfilling derived containment edges for the existing rooms — that produces false reachability, is
named as a rejected Alternative in the ADR, and would pass a check that only asked whether
`am_entry_point` resolves.

What would make this criterion impossible to fail on the data available: nothing, and that is the
point of ordering the hosted read first. If step 1 were skipped, every remaining step would succeed
against the local palace and the sign-off would read `ship` whatever the hosted palace holds.

## Out of Scope

- Backfilling corpora on deployments other than the two named (deferred: `docs/adr/BACKLOG.md` §"Four spellings of one entry point").
- Giving the entry point's data a producer in the product rather than a procedure an agent runs by hand (deferred: Follow-ups, ADR-043).

## Verification Log
