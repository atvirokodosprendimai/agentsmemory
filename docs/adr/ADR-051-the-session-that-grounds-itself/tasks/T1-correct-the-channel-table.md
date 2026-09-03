# Task ADR-051-T1: Correct the hook channel table it currently forbids a working event

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (two map entries, one comment, one gate)
**Owner:** unassigned
**Produces:** `a channel table that matches the documented four`
**Consumes:** none
**Data dependency:** hermetic

## Goal

Make `injectingEvents` name the four events the documentation names, so `doctor` stops
reporting a working hook as dead and the install gate stops refusing a channel that exists.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hookchannel.go` | edit | `UserPromptExpansion` moves to `injectingEvents`; `PreModelSwitch` joins `debugLogEvents`; the false ⚠ paragraph is replaced |
| `clients/claude-code/hookchannelknown_test.go` | edit | the new gate, and the disjointness check it already makes |

## Ordered Steps

1. Write the failing test first (TDD red): `TestTheInjectingSetIsTheDocumentedFour` asserts
   membership of all four names and asserts `UserPromptExpansion` is NOT in `debugLogEvents`.
   Run the fence and confirm RED.
2. Move `UserPromptExpansion` from `debugLogEvents` to `injectingEvents`.
3. Add `PreModelSwitch` to `debugLogEvents` — it is documented (it appears in the exit-code-2
   blocking table) and it is in neither map today, so `hookEventChannel` answers
   `channelUnknown` for a known event.
4. Replace the ⚠ paragraph that claims the docs moved `UserPromptExpansion` to the debug-log
   side. It is false; leaving it corrected-in-place rather than deleted is the point, because
   a file that quietly loses the claim it got wrong teaches nothing.
5. Re-run the fence and confirm GREEN, then the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestTheInjectingSetIsTheDocumentedFour|TestNoEventIsInBothChannelSets|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel' \
  -count=1 2>&1 | tee /tmp/adr051-t1.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t1.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheInjectingSetIsTheDocumentedFour` | `clients/claude-code/hookchannelknown_test.go` | All four documented names are in `injectingEvents`, and `UserPromptExpansion` is absent from `debugLogEvents` — the exact pair of assertions that is false today | — |
| `TestNoEventIsInBothChannelSets` | `clients/claude-code/hookchannelknown_test.go` | Existing disjointness gate still holds after the move | — |

## Reachability

The table is data read by two callers — `doctor` and the install-plan gate — so a wrong entry
is not inert: it produces a confident verdict. The gate asserts membership rather than
behaviour because behaviour here belongs to Claude Code, not to us.

⚠ **No test fetches the documentation.** A gate that makes a network call fails when the
network does and turns an upstream edit into a red build on an unrelated branch. The retrieval
date in the comment is the honesty mechanism; `doctor` is where an operator learns it is old.

## Mutation Log

Filled by `adr-verify --mutant`.

## Invariants

- No event appears in both sets.
- An event in neither set answers `channelUnknown`, never a definite "does not inject".
- The comment records the retrieval date of the claim.

## Risks

The documentation moves again. That is the recurrence this file already records twice; the
mitigation is the dated comment and `doctor`, not a cleverer data structure.

## Stop Condition

Stop and raise it if the documented set turns out to be version-dependent — that would make a
single table wrong for some installs, and the fix would be a per-version table, which is a
decision rather than an edit.

## Out of Scope

Registering anything on `UserPromptExpansion` — that is T4.

## Verification Log

Filled by `adr-verify`.
