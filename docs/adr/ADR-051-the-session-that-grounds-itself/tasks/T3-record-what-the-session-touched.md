# Task ADR-051-T3: Record what the session touched, at PostToolUse

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (one hook script, one registration)
**Owner:** unassigned
**Produces:** `touched path record`
**Consumes:** `path-keyed anchor lookup` (T2)
**Data dependency:** hermetic

⚠ **THIS IS NOT THE `PostToolUse` AUDIT ADR-041 REJECTED.** That rejection reads: "it reports
the error after it has been published, which is the position this repository was already in."
It stands. This task delivers no verdict about what the agent wrote — it appends the path to a
local list. There is nothing to report late because nothing is judged.

## Goal

Keep a per-session record of which files were edited, so the persist step at end of turn knows
what to anchor without asking the agent to remember.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-touched-hook.sh` | add | appends `tool_input.file_path` to a session-scoped file; `# hook-output: none` |
| `clients/claude-code/installer.go` | edit | registers `PostToolUse`, matcher `Edit|Write` |
| `clients/claude-code/hooks/agentsmemory-stop-hook.sh` | edit | names the touched paths in the persist nudge |

## Ordered Steps

1. Write the failing tests first (TDD red). Run the fence and confirm RED.
2. Write the hook: append the path, deduplicated, to a file keyed by `session_id` under the
   kit's own state directory. No network, no binary dependency — the same optional-by-design
   rule every hook here follows.
3. Register `PostToolUse` scoped to write tools only. A record of every Read is a record of
   nothing.
4. Have the Stop hook read the list and name the paths it holds.
5. Run the fence, then the mutants, then the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestTouchedPathsAreRecordedOncePerPath|TestTouchedRecordIsScopedToTheSession|TestThePostToolUseHookIsRegistered|TestTheStopHookNamesTouchedPaths' \
  -count=1 2>&1 | tee /tmp/adr051-t3.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t3.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTouchedPathsAreRecordedOncePerPath` | `clients/claude-code/touched_test.go` | A path edited five times appears once — a list that grows with every keystroke is a list nobody reads | — |
| `TestTouchedRecordIsScopedToTheSession` | `clients/claude-code/touched_test.go` | Two session ids keep two lists; one session cannot report another's work | — |
| `TestThePostToolUseHookIsRegistered` | `clients/claude-code/installer_test.go` | The plan registers it, scoped to write tools | — |
| `TestTheStopHookNamesTouchedPaths` | `clients/claude-code/touched_test.go` | The persist nudge names the recorded paths, so the record is consumed rather than merely written | — |

## Reachability

A recorder nothing reads is a file that grows. `TestTheStopHookNamesTouchedPaths` is the gate
that makes the write reachable — it fails if the record is produced and never consumed.

## Mutation Log

Filled by `adr-verify --mutant`. At minimum: the dedup severed, and the Stop-hook read severed.

## Invariants

- Write tools only.
- No content is recorded, only paths — the file is a list, not a transcript.
- The hook never blocks and never fails a tool call.

## Risks

A long session edits many files and the nudge becomes a wall of paths. Bound the list and say
it is bounded.

## Stop Condition

Stop if the record cannot be scoped to a session — an unscoped file shared across concurrent
sessions reports one session's work to another, which is worse than no record.

## Out of Scope

- Judging what was written. (permanent: boundary: ADR-041 rejected a late verdict and that rejection stands)
- Filing memories automatically. (deferred: T9)

## Verification Log

Filled by `adr-verify`.
