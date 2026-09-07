# Task ADR-010-T3: Recall returns what is current — and carries the reason forward

> **Amended 2026-08-20 before execution.** The first version hid history behind a flag AND expected
> retractions to stop a future session re-litigating a settled decision. Those cannot both hold: a
> session about to redo a rejected thing does not know to ask for history — not knowing is the whole
> problem. So the CURRENT record carries what it superseded and why, and the reason reaches the
> default recall path while the stale text does not.

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** current-only recall across every default route; the superseded reason carried on the live record; the explicit history flag
**Consumes:** supersede semantics (T2)
**Data dependency:** hermetic

## Goal

A superseded record is unreachable by every default route and reachable by one explicit one.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | `Search` and `List` filter to current unless history is requested |
| `internal/mcpserver/drawers.go` | edit | an `include_history` argument, declared as well as read |
| `internal/mcptest/scoping_audit_test.go` | edit | **the falsification**: every default route asked the same question, as the wing audit does |

## Ordered Steps

1. Write the failing test first (TDD red): `TestSupersededRecordIsUnreachableByEveryDefaultRoute` — search, list and get, each asked for the retracted text. Commit it red.
2. Filter through T1's single `current()` predicate rather than adding a condition per query.
3. Add `include_history`, declared on the tool as well as read by the handler — an argument the handler honours and the schema hides is a capability nobody can discover, which this repository shipped once already this week.
4. Prove accumulation is affordable, which is the claim the whole ADR rests on. Seed a wing until superseded records outnumber current ones 2:1, then run the same queries against it and against the same corpus before the endings existed. Current-record ordering must be identical. This is the ADR's pre-registered falsification: if it fails, ended records are competing after all and the filter is broken — the remedy is the filter, never deletion.
5. Audit the CLASS, not the instance. The wing-scoping leak was fixed in one tool and found in three more only because every route was asked; supersession has the same shape and the same number of routes.
6. Falsify: filter search but not list; filter list but not get; make `include_history` default true.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true;
  set -e
  git config --global --add safe.directory /src
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcptest/ -run "TestSupersededRecordIsUnreachable|TestHistoryIsReachableWhenAsked|TestAccumulationDoesNotDegradeCurrentRecall" -count=1 -v 2>&1 | tee /tmp/v3.out
  grep -q -- "--- PASS: TestSupersededRecordIsUnreachableByEveryDefaultRoute" /tmp/v3.out
  grep -q -- "--- PASS: TestHistoryIsReachableWhenAsked" /tmp/v3.out
  grep -q -- "--- PASS: TestAccumulationDoesNotDegradeCurrentRecall" /tmp/v3.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/v3.out; then exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSupersededRecordIsUnreachableByEveryDefaultRoute` | `internal/mcptest/scoping_audit_test.go` | search, list and get all exclude retracted text | — |
| `TestHistoryIsReachableWhenAsked` | `internal/mcptest/scoping_audit_test.go` | `include_history` returns the chain, newest first | — |
| `TestTheReasonReachesDefaultRecall` | `internal/mcptest/scoping_audit_test.go` | a default search for the CURRENT record surfaces why its predecessor was ended — without the stale text | — |
| `TestAccumulationDoesNotDegradeCurrentRecall` | `internal/mcptest/scoping_audit_test.go` | with superseded records outnumbering current ones 2:1, the same queries return the same current records in the same order | — |
| `TestRecallPayloadIsBoundedRegardlessOfHistory` | `internal/mcptest/scoping_audit_test.go` | a default recall over a corpus with a long supersession chain returns the same number of bytes as one with none, within the 200-char reason allowance | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| filter search but not list | yes | `TestSupersededRecordIsUnreachableByEveryDefaultRoute` |
| filter list but not get | yes | `TestSupersededRecordIsUnreachableByEveryDefaultRoute` |
| `include_history` defaults true | yes | `TestSupersededRecordIsUnreachableByEveryDefaultRoute` |
| the live record drops the ended reason | yes | `TestTheReasonReachesDefaultRecall` |
| ended records rejoin the candidate pool before ranking | yes | `TestAccumulationDoesNotDegradeCurrentRecall` |
| the carried reason is emitted untruncated | yes | `TestRecallPayloadIsBoundedRegardlessOfHistory` |
| `include_history` read but not declared | yes | `TestEveryArgumentAHandlerReadsIsDeclared` |

## Out of Scope

- Ranking superseded records when history IS requested (deferred: docs/adr/ADR-004-supersession-not-recall.md — it owns ordering)
- Showing the chain in `am_get_drawer` by default (permanent: the default is the current record; a caller asking for one memory should not get three)

## Invariants

- A superseded record's TEXT is invisible to every default route, not merely to search.
- Its REASON is visible on the live record without asking for history.
- `include_history` is declared on every tool that reads it.

## Risks

- A route is added later without the filter. Mitigated: the class audit in step 4 is written to ask every route, so a new one fails until it is answered.

## Stop Condition

Stop and report if accumulation DOES degrade current recall and the filter cannot be fixed — that is the one finding that would put the "keep everything" position and good retrieval genuinely in tension, and it deserves a decision rather than a workaround.

Stop and report if the falsification cannot be satisfied — a superseded record reachable by any default route reproduces the live-chunk-1 defect this ADR exists to make impossible, and shipping it would be worse than not shipping.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>

## Mutation Log
