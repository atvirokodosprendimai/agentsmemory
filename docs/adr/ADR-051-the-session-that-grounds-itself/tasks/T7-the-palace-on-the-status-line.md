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
  -run 'TestTheStatusLineMakesNoNetworkCall|TestTheStatusLineIsSilentWithoutACache|TestTheStatusLineShowsWhatTheCacheHolds|TestTheStatusLineDoesNotReplaceOneTheUserSet|TestTheStatusLineIsRegistered|TestOneInstallLeavesAtMostOneBackup' \
  -count=1 2>&1 | tee /tmp/adr051-t7.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t7.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheStatusLineMakesNoNetworkCall` | `clients/claude-code/anchorcue_test.go` | The script contains no curl/binary invocation — a status line that waits on a server freezes the prompt | — |
| `TestTheStatusLineIsSilentWithoutACache` | `clients/claude-code/anchorcue_test.go` | No cache, no output, exit 0. An error string in the status line is permanent noise | — |
| `TestTheStatusLineShowsWhatTheCacheHolds` | `clients/claude-code/anchorcue_test.go` | Wing and drift render, and a ZERO drift count does not — a line that always says "0 drifted" spends attention on the absence of a problem, every second, forever | — |
| `TestTheStatusLineDoesNotReplaceOneTheUserSet` | `clients/claude-code/anchorcue_test.go` | An existing `statusLine` is left alone; only an absent key is filled | — |
| `TestTheStatusLineIsRegistered` | `clients/claude-code/anchorcue_test.go` | The installer passes the command to `ensureHooks` — a status line written to disk and registered by nothing renders never | — |
| `TestOneInstallLeavesAtMostOneBackup` | `clients/claude-code/installer_test.go` | ⚠ Pre-existing gate that CAUGHT the first version: writing `statusLine` in its own read-modify-write produced a second backup of `settings.json` in one install, and a user then cannot tell which backup is the state they had before | — |

## Reachability

A script that renders perfectly and is registered in no settings key is invisible. The
registration gate is the same rung `TestDoctorIsRegistered` covers.

## Mutation Log

Filled by `adr-verify --mutant`.
- 2026-09-04 · cb33ab7* · mutant killed · exit 1 · `clients/claude-code/installer.go` · the status line ships and is registered by nothing: it renders never, which is this repository characteristic defect in its cheapest form · acceptance-sha256:3eb0dfa817868b548eaa039d8c6facc324b846604a96d0da9d5d3f28e461dcaa
- 2026-09-04 · cb33ab7* · mutant killed · exit 1 · `clients/claude-code/settings.go` · the refusal removed: the installer overwrites a status line the user chose, which is the most visible and least invited thing it could do · acceptance-sha256:3eb0dfa817868b548eaa039d8c6facc324b846604a96d0da9d5d3f28e461dcaa

## Invariants

- One install, one read, one backup, one write of `settings.json`.
- A `statusLine` the user set is never replaced.
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
- 2026-09-04 · cb33ab7* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:3eb0dfa817868b548eaa039d8c6facc324b846604a96d0da9d5d3f28e461dcaa · ms:45614
- 2026-09-04 · cb33ab7* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:3eb0dfa817868b548eaa039d8c6facc324b846604a96d0da9d5d3f28e461dcaa · ms:33793
- 2026-09-04 · cb33ab7* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:3eb0dfa817868b548eaa039d8c6facc324b846604a96d0da9d5d3f28e461dcaa · ms:34506
- 2026-09-04 · 12de7bf* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:3eb0dfa817868b548eaa039d8c6facc324b846604a96d0da9d5d3f28e461dcaa · ms:36333
