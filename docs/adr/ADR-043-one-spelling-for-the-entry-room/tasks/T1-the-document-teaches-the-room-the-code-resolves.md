# Task ADR-043-T1: The served onboarding document teaches the room the code resolves

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `entryRoomDisagreements()` — the detection function the gate and its falsifiability subtest both drive
**Consumes:** none
**Data dependency:** hermetic

## Goal

A new agent reading `/bootstrap-memory` is taught the entry room `palace.EntryRoom` actually resolves, and a gate fails when the two disagree again.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/repohygiene/entryroom_test.go` | add | The gate. Its universe is two real artifacts — the constant parsed out of `internal/palace/graphquery.go`, and the room names read out of the served document — so a rename joins the check on the same commit |
| `internal/web/bootstrap-memory.md` | edit | §4.3 seeds an `llm_init` root drawer plus `must.*` edges from that root's own drawer id to the drawer ids of the mandatory tier; `llm_index` becomes one of those targets rather than the entry point |
| `README.md` | edit | Line 167 explains `am_bootstrap`'s `unknown_term` as un-backfilled derived edges. Measured 2026-08-28 against the local palace there are no `llm_init` drawers to backfill, so the documented cause cannot be this palace's cause |
| `internal/repohygiene/hygiene_test.go` | none — read only | Confirms the sibling gates' shape before adding a fourth; no edit expected |

The gate is what SELECTS this decision: nothing else in the tree compares the served document against
the constant, and deleting the gate is the only way to make the two disagree silently again. `go test
./internal/repohygiene/` already runs every file in the package, so registration is the file's
existence — there is no list to add it to, which is why the mutation below targets the constant
rather than a registration line.

## Ordered Steps

1. Write `internal/repohygiene/entryroom_test.go` with `TestTheServedDocumentTeachesTheRoomTheCodeResolves` and its `theGateReportsADocumentThatTeachesAnotherRoom` subtest, and run it — it must go RED against the tree as it stands, because `internal/web/bootstrap-memory.md` says `llm_index` 15 times and `llm_init` zero (measured 2026-08-28). A green first run means the gate is reading something other than the two artifacts.
2. Parse `EntryRoom`'s value from `internal/palace/graphquery.go` with `go/parser` rather than importing `palace` — the same reason `TestEverySpecBindingNamesATestThatExists` parses instead of running: the gate must read the artifact, not a value the test binary happens to hold.
3. Route the verdict through a substitutable `testing.TB` so the falsifiability subtest can drive `entryRoomDisagreements()` over fixtures that ARE broken. A corpus with zero disagreements cannot exercise the branch that reports one, and a subtest that reimplements the loop pins nothing — AGENTS.md records two gates in this tree that shipped with exactly that hole.
4. Correct `internal/web/bootstrap-memory.md` §4.3: seed the root drawer whose content opens `WHAT MUST I LOAD AT THE START OF A SESSION?` into `llm_init`, then the `must.*` facts from its drawer id to each mandatory drawer's id. Keep `llm_index` and its key-list sibling; re-file them as `must.*` targets under the root.
5. Correct `README.md:167` to name the real cause — a wing with no root drawer in the entry room — and keep the un-backfilled-edges case as the second cause it also is, so an operator on an older corpus is not misdirected the other way.
6. Re-run the acceptance fence; it must now be green, and step 1's red run stays in the Verification Log.

## Acceptance

```bash
go test ./internal/repohygiene/ -run 'TestTheServedDocumentTeachesTheRoomTheCodeResolves' -count=1 2>&1 | tee /tmp/adr043-t1.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr043-t1.out && go test ./internal/repohygiene/ ./internal/web/ -count=1
```

The new test runs ALONE first so the already-passing `repohygiene` suite cannot carry the verdict by
itself; the regression run is chained after it with `&&`. The `no tests to run` guard is load-bearing
because `go test -run` on a name that does not exist prints `ok … [no tests to run]` and exits 0,
which is this fence's state at the moment it is written.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheServedDocumentTeachesTheRoomTheCodeResolves` | `internal/repohygiene/entryroom_test.go` | The served onboarding document names the room `palace.EntryRoom` declares, and does not teach a different one as the entry point | — |
| `TestTheServedDocumentTeachesTheRoomTheCodeResolves/theGateReportsADocumentThatTeachesAnotherRoom` | `internal/repohygiene/entryroom_test.go` | `entryRoomDisagreements()` reports a disagreement over fixtures that have one — driven through a substitutable `testing.TB`, so severing the CALL to it goes red rather than printing "all agree" | — |

Shapes the document can already produce, enumerated before writing the assertions: the room named in
prose but not in a code fence; named in a fence that is an EXAMPLE of what not to do; named inside a
sentence about another wing's room; and the word appearing as part of a longer identifier
(`llm_index_keys` contains `llm_index`). The last is the `stale`/"staleness" rule AGENTS.md already
records — match on a word boundary, and let `llm_index_keys` be its own token.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestTheServedDocumentTeachesTheRoomTheCodeResolves` |
| 2 — something selects it | The package test run; the mutation below changes `EntryRoom`'s value and the gate must follow the constant rather than stay green |
| 3 — the caller can discover it | The corrected §4.3 in `internal/web/bootstrap-memory.md` is the served artifact an agent reads; `AGENTS.md` already names the room, unchanged |
| 4 — it is used | `am_entry_point` / `am_bootstrap` on `wing_agentmemories` return `unknown_term` today; T3 is where that becomes observable |

## Mutation Log

## Invariants

- `palace.EntryRoom` keeps the value `"llm_init"`. This task changes documents, never the constant — if the constant has to move, the decision was the other one.
- The two existing `llm_index` drawers keep their content and their ids; §4.3 describes re-filing them under the root, not rewriting them.
- No ADR, `CHANGELOG.md` or `BACKLOG.md` line is edited to agree with this decision.

## Risks

- The gate is written against the document's current wording rather than against the constant, so correcting the document turns it green for the wrong reason. Mitigated by step 2 (parse the constant) and by the mutation, which changes the constant and requires the gate to follow.
- §4.3's rewrite teaches a procedure nobody runs, because the entry point's data still has no producer in the product. Named in the ADR's Follow-ups rather than solved here.

## Stop Condition

Stop if parsing `EntryRoom` out of `internal/palace/graphquery.go` requires importing `palace` into
`repohygiene` — that import direction would let a refactor of the palace package change what the gate
reads without touching the gate, which is the drift the gate exists to catch. Stop and ask before
adding it.

## Out of Scope

- Making `Bootstrap` follow the `must.*` tier — that is T2's job.
- Seeding this repository's own corpus — that is T3's job.
- Reconciling `model/draf1.md`'s mixed vocabulary (permanent: after this record the two words name two different things, and that document describes both).

## Verification Log
