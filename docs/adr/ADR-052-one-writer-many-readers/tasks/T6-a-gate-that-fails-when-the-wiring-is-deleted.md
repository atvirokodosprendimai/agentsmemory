# Task ADR-052-T6: A gate that fails when the wiring is deleted

**Depends-on:** T5
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `TestEveryServingHandleDeclaresItsRole`
**Consumes:** `openReaderDB` (T4), `palace.NewRepo` (T5)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the AST read of the composition root rather than a list beside it`

## Goal

Make the split survive the next contributor: a check that fails when the writer
loses its pool cap, when a serving handle is opened by something other than the
three named openers, or when a write-side serialisation pragma is added to paper
over a second writer instead of removing it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/dbwiring_test.go` | edit | add the source-level gate beside T4's behavioural one |
| `AGENTS.md` | edit | §Reachability lists the gates this repo relies on; a new gate that is not named there is one nobody knows to keep |

## Ordered Steps

1. [S1] Write the gate first and watch it fail against a deliberately broken fixture (TDD red).
2. [S2] Parse `cmd/server` with `go/parser` and derive the universe from the source rather than from a list: find every call to `openDBWithPragmas` and require each to be reached through `openWriterDB`, `openReaderDB` or `openInspectionDB`. A fourth opener added tomorrow joins the check on the same commit, which is the property a hand-kept list does not have.
3. [S3] Assert that `openWriterDB`'s body contains a `SetMaxOpenConns` call with the literal `1`, and that no serving opener adds a write-side serialisation pragma. The cap is a one-line deletion that leaves every behavioural test green, which is exactly the failure `AGENTS.md` §Reachability records against this repository.
4. [S4] Put the falsifiability case INSIDE the acceptance command as a subtest, driving the same extractor over a fixture that hard-codes an unbounded pool — a sibling test would sit outside the one command that has to pass.
5. [S5] Add the two gate names to `AGENTS.md` §Reachability with one sentence each saying what they do not see: neither can tell whether a future read method routes itself onto the writer, which stays review's job. [proof: human: a reviewer reads the AGENTS.md paragraph against the test bodies and confirms the stated limit matches what the tests actually check]
6. [S6] Derive the read-first population rather than freezing it: walk every `Transaction(` closure in non-test packages with `go/parser` and report each whose first statement on `tx` is a read. The record's "6 of 16" is true at `732b727` and drifts the moment someone adds a seventeenth. ⚠**A read-first site is not itself a defect under this decision** — one writer connection cannot deadlock against itself. What the gate exists to catch is a read-first transaction running on a handle that is NOT the single capped writer, which is the shape that reintroduces the failure.

## Acceptance

```bash
set -o pipefail
go test ./cmd/server/ -run 'TestEveryServingHandleDeclaresItsRole$|TestTheReadHandleCannotWrite$' -count=1 -v 2>&1 | tee /tmp/adr052-t6.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr052-t6.out \
  && grep -q "TestEveryServingHandleDeclaresItsRole/catches_an_unbounded_pool" /tmp/adr052-t6.out
```

The final `grep` is the point: it fails unless the falsifiability subtest
actually ran, so a gate that silently stops exercising its own negative case
cannot report success.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryServingHandleDeclaresItsRole` | `cmd/server/dbwiring_test.go` | every `openDBWithPragmas` call site is one of the three named openers, and the writer caps its pool at 1 | — | S1, S2, S3 |
| `TestNoServingOpenerAddsAWriteSerialisationPragma` | `cmd/server/dbwiring_test.go` | no serving DSN carries `_txlock` or an equivalent — the writer is serialised by its connection count, and a flag appearing there means a second writer was papered over | — | S6 |
| `TestEveryServingHandleDeclaresItsRole/catches_an_unbounded_pool` | `cmd/server/dbwiring_test.go` | the same extractor reports a fixture that omits the pool cap | — | S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the gate and its subtest |
| 2 — something selects it | `go test ./cmd/server/` runs it, and the subtest name is asserted in the fence so a skipped negative case is caught |
| 3 — the caller can discover it | `AGENTS.md` §Reachability names both gates and states their blind spot |
| 4 — it is used | it runs on every CI build of this package |

## Mutation Log

## Invariants

- The gate derives its universe from the AST, never from a list of file names kept beside the source.
- The gate reports what it cannot see, in `AGENTS.md`, rather than reading as total coverage.
- `openInspectionDB` stays an accepted opener; `doctor` is a legitimate third role.

## Risks

- An AST check can be real, passing and unable to see the thing it names. Mitigated by S4's fixture, which is the cheap test `AGENTS.md` prescribes: break it on purpose and watch the gate go red.
- Naming gates in `AGENTS.md` makes that file's claim checkable by `TestAgentsMdNamesGatesThatExist`; a typo there fails the suite. That is the mechanism working.

## Stop Condition

Stop and ask if the AST check needs an exemption list to pass. A list kept
beside the truth goes stale, and needing one means the universe was drawn
wrong — reshape the seam rather than adding the list.

## Out of Scope

- Checking that a future read method does not route itself onto the writer handle; no AST check can decide that and the record says so
- Extending the gate to the nine packages T5 did not touch (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
