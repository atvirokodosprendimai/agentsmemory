# ADR-043 Tasks

Implementation tasks for ADR-043: One spelling for the entry room, and a tier the entry point
actually reaches. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T3 | none |
| 2 | T1 | none |
| 3 | T2 | none |
| 4 | T4 | T1, T2, T3 |

⚠ **T3 is numbered third and runs FIRST, deliberately.** It is the read that can stop this record,
and an earlier draft had it as step 1 of the seeding task, which ran last — so T1 and T2 would have
landed before anyone learned whether the direction survives contact with the hosted deployment. A
stopping condition that fires after two thirds of the work is not one. The ids were not renumbered
to match the order, because a task id is how the corpus refers to a task and renumbering breaks
every reference to it; `Depends-on` is the source of truth and this table is derived from it.

T1 and T2 are independent of everything and of each other — one corrects the artifacts, the other
corrects the mechanism — and either may run at any point. T4 is last because it writes data that
only T2 can read back and only T3 can authorise.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The served onboarding document teaches the room the code resolves | pending | — | `go test ./internal/repohygiene/ -run 'TestTheServedDocumentTeachesTheRoomTheCodeResolves' -count=1 …` |
| T2 | An entry point that resolves reaches the mandatory tier | pending | — | `go test ./internal/palace/ -run 'TestBootstrapReachesTheMandatoryTier' -count=1 …` |
| T3 | Read the hosted palace and classify what it holds | pending | — | human-observed sign-off via `adr-verify --human` |
| T4 | Seed this repository's entry point | pending | — | human-observed sign-off via `adr-verify --human` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T3 | the hosted classification — `canonical` \| `label` \| `nothing` | T4 | T3 before T4 — and before T1/T2 in practice, because `label` stops the record |
| T2 | `Bootstrap` returning `must.*` targets outside the root room | T4 | T2 before T4 — without it T4 writes drawers nothing reads |
| T1 | the corrected §4.3 | T4 | T1 before T4 — T4 follows the procedure T1 writes |
| T1 | `entryRoomDisagreements()` | none | internal to T1; listed because the gate and its falsifiability subtest must drive the same function |

## Notes

- **T3 reads a live hosted palace and writes nothing anywhere.** Its classification can stop the
  record; run it before spending T1 or T2.
- **T4 writes to the local palace.** It verifies the new facts resolve before retiring any old one,
  so a failure between the two leaves the old protocol intact rather than leaving the wing with
  neither.
- T3's and T4's sign-offs must each name one of `ship`, `withdraw` or `blocked`, and this README's
  status cell for each must carry the status it maps to (`done` / `failed` / `blocked`) — checked by
  `TestAHumanObservedSignOffAgreesWithTheIndex`.
