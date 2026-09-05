# Task ADR-058-T2: both recall hooks use the digest, carry the installed wing, and say "could not look" on both channels

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `AGENTSMEMORY_WING` in the hook environment prefix
**Consumes:** `--digest` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the wing reaches the search when set`, `the digest is what the hook prints`, `the could-not-look line on additionalContext`

## Goal

Every UserPromptSubmit and SessionStart recall injects the digest, scoped to the installed wing when there is one, and names a failed recall to the model as well as the transcript.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` | edit | `--digest 1600` on the search; `-a wing="$AGENTSMEMORY_WING"` when set; the preamble's "different project" sentence only when unset; on failure, the one-line "could not look" through `hookSpecificOutput.additionalContext` beside stderr |
| `clients/claude-code/hooks/agentsmemory-recall-hook.sh` | edit | the same three changes for SessionStart |
| `clients/claude-code/installer.go` | edit | the Claude hook environment prefix carries `AGENTSMEMORY_WING='<wing>'` beside the URL when `--wing` was given — this is what SELECTS the wing for the hook, and the test deletes it |
| `clients/claude-code/doctor.go` | edit | the hook environment `doctor` prints and runs with includes the wing, so a reinstall that added it is visible |
| `clients/claude-code/hooks_test.go` | edit | `TestTheRecallHookCarriesTheInstalledWing` (fake MCP server records the request; with the env set the search carries the wing, without it none), `TestARecallThatCouldNotLookSaysSoOnBothChannels` (server down: stdout carries `additionalContext` with "could not look", stderr carries the same line) |
| `clients/claude-code/installer_test.go` | edit | `TestTheHookPrefixCarriesTheWing` |
| `clients/claude-code/README.md` | edit | the hooks paragraph: what is injected now, and what `AGENTSMEMORY_WING` does |

## Ordered Steps

1. [S1] Write the three tests red.
2. [S2] Hooks: digest, wing, preamble, both-channel failure line. [proof: mutation]
3. [S3] Installer prefix and doctor's echo of it. [proof: mutation]
4. [S4] README. [proof: human: the reviewer reads the paragraph against a real injection]
5. [S5] Mutants, one per Rests-on mechanism: drop the `-a wing=` argument; print `$HITS` raw instead of the digest; send the failure line to stderr only. [proof: mutation]
6. [S6] Re-measure the injection on the same prompt as the record's Context and write the figure into the ADR's Follow-ups. [proof: human: the reviewer compares the two figures]

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestTheRecallHookCarriesTheInstalledWing$|TestARecallThatCouldNotLookSaysSoOnBothChannels$|TestTheHookPrefixCarriesTheWing$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheRecallHookCarriesTheInstalledWing` | `clients/claude-code/hooks_test.go` | the search carries the wing iff the env is set | — | S1, S2 |
| `TestARecallThatCouldNotLookSaysSoOnBothChannels` | `clients/claude-code/hooks_test.go` | a dead server is named on additionalContext and stderr | — | S1, S2 |
| `TestTheHookPrefixCarriesTheWing` | `clients/claude-code/installer_test.go` | the installer writes the wing into the prefix | — | S1, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the three tests |
| 2 — something selects it | the installer's prefix line and the hook's `-a wing=` line; the mutants delete each |
| 3 — the caller can discover it | README; `doctor` prints the environment it runs the hook with |
| 4 — it is used | S6's re-measurement on this machine, recorded in the ADR |

## Mutation Log

## Invariants

- Without `AGENTSMEMORY_WING` the hooks behave exactly as before this record, preamble included.
- The stderr line stays; the structured line is added, never substituted.
- `hookSpecificOutput` keeps the shape ADR-051's gates check.

## Risks

- A hook environment prefix with a wing containing a quote breaks the shell line; wings are validated at install (`--wing` normalisation) and the prefix quotes with single quotes as the URL does.

## Stop Condition

Stop if `hookSpecificOutput.additionalContext` on UserPromptSubmit is not injected by the current Claude Code — check the hook docs the ADR cites before assuming; the stderr line alone is then the honest state and the record must say so.

## Out of Scope

- The renderer — T1.
- PreCompact / compact-matcher hooks (deferred: docs/adr/BACKLOG.md)

## Verification Log
