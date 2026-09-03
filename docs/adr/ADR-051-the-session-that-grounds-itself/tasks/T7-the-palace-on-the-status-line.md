# Task ADR-051-T7: Put the palace on the status line

**Depends-on:** T6
**Covers:** none — no spec
**Estimated scope:** S (one script, one settings key)
**Owner:** unassigned
**Produces:** none
**Consumes:** `one installable unit` (T6)
**Data dependency:** hermetic

## Goal

Show the wing, the drift count and the waiting inbox where a human already looks, so an
unattended run is glanceable without reading a transcript.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-statusline.sh` | add | `# hook-output: not-a-hook — a statusLine command, registered on no event.` |
| `clients/claude-code/installer.go` | edit | writes the `statusLine` key |

## Ordered Steps

1. Write the failing tests first (TDD red).
2. Write the script: wing, drifted-anchor count, waiting inbox count. **Read from a cached
   file, never the network** — the status line runs often and must never block the UI.
3. Have the SessionStart verify hook refresh that cache, since it already asks the questions.
4. Register the key. Run the fence, the mutants, the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestTheStatusLineMakesNoNetworkCall|TestTheStatusLineIsSilentWithoutACache|TestTheStatusLineKeyIsWritten' \
  -count=1 2>&1 | tee /tmp/adr051-t7.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t7.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheStatusLineMakesNoNetworkCall` | `clients/claude-code/statusline_test.go` | The script contains no curl/binary invocation — a status line that waits on a server freezes the prompt | — |
| `TestTheStatusLineIsSilentWithoutACache` | `clients/claude-code/statusline_test.go` | No cache, no output, exit 0. An error string in the status line is permanent noise | — |
| `TestTheStatusLineKeyIsWritten` | `clients/claude-code/installer_test.go` | The installer's plan sets `statusLine` | — |

## Reachability

A script that renders perfectly and is registered in no settings key is invisible. The
registration gate is the same rung `TestDoctorIsRegistered` covers.

## Mutation Log

Filled by `adr-verify --mutant`.

## Invariants

- No network call, ever.
- Silence rather than an error string.
- Never exits non-zero.

## Risks

A stale cache shows a drift count that was true an hour ago. Show the age, or show nothing.

## Stop Condition

Stop if the counts cannot be produced without a network call on the render path.

## Out of Scope

Interactivity. (permanent: boundary: a status line renders, it does not accept input)

## Verification Log

Filled by `adr-verify`.
