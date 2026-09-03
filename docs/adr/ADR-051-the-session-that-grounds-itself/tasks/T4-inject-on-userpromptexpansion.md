# Task ADR-051-T4: Inject on UserPromptExpansion, the channel T1 unblocks

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S (one hook script, one registration)
**Owner:** unassigned
**Produces:** none
**Consumes:** `a channel table that matches the documented four` (T1)
**Data dependency:** hermetic

## Goal

Recall at the moment a slash command expands into a task — the earliest point at which the work
has a stated subject, and one of only four events whose plain stdout reaches the model.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-expansion-recall-hook.sh` | add | `# hook-output: stdout-injected` |
| `clients/claude-code/installer.go` | edit | registers `UserPromptExpansion` |

## Ordered Steps

1. Write the failing tests first (TDD red), starting with
   `TestTheExpansionHookIsOnAnInjectingEvent`. ⚠ Against a tree without T1 this test is red for
   the WRONG reason — the channel table, not the hook — so confirm T1 has landed before reading
   the red as evidence. That is what the `Depends-on` edge means here, and it is a real
   dependency rather than bookkeeping.
2. Write the hook, reusing the task-recall hook's query extraction and its BSD-safe `sed`.
   ⚠ Use the same `max_distance` floor the task-recall hook uses, and read this record's
   Follow-up on canary drift before trusting that number.
3. Register the event, run the fence, run the mutants, run the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestTheExpansionHookIsOnAnInjectingEvent|TestTheExpansionHookIsSilentOnAShortSubject|TestTheUserPromptExpansionHookIsRegistered' \
  -count=1 2>&1 | tee /tmp/adr051-t4.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t4.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheExpansionHookIsOnAnInjectingEvent` | `clients/claude-code/hookchannel_test.go` | The existing gate now PASSES for this registration — the same gate that would have rejected it before T1 | — |
| `TestTheExpansionHookIsSilentOnAShortSubject` | `clients/claude-code/expansion_test.go` | No output for a subject too short to be a query, matching the task-recall hook's refusal | — |
| `TestTheUserPromptExpansionHookIsRegistered` | `clients/claude-code/installer_test.go` | The plan registers it | — |

## Reachability

This task exists to USE a channel, so its gate is the channel gate. Reverting T1 turns
`TestTheExpansionHookIsOnAnInjectingEvent` red, which is the coupling stated as a test rather
than as a comment.

## Mutation Log

Filled by `adr-verify --mutant`.

## Invariants

- Silence when there is nothing to say.
- Plain stdout, not a JSON envelope — this event injects plain text and an envelope would be
  printed literally.

## Risks

Command expansion is frequent; a recall on every expansion is a tax and a noise source.

## Stop Condition

Stop if the hook fires on expansions that carry no subject — a recall keyed on nothing is the
failure ADR-041 T5 recorded, and this task must not repeat it in a new event.

## Out of Scope

Rewriting the expanded prompt via `updatedPrompt`. (deferred: `docs/adr/BACKLOG.md`)

## Verification Log

Filled by `adr-verify`.
