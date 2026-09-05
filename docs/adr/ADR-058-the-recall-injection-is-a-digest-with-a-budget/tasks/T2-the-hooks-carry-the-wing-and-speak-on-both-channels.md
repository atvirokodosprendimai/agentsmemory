# Task ADR-058-T2: both recall hooks use the digest, carry the installed wing, and say "could not look" on both channels

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `AGENTSMEMORY_WING` in the hook environment prefix
**Consumes:** `--digest` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the wing reaches the search when set`, `the craft call beside the project call`, `the digest is what the hook prints`, `the could-not-look line on additionalContext`

## Goal

Every UserPromptSubmit and SessionStart recall injects the digest — the installed wing's page and then `wing_craft`'s under one budget when a wing is set — and names a failed recall to the model as well as the transcript.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` | edit | with the wing set: two searches, `-a wing="$AGENTSMEMORY_WING" --digest 1200` then `-a wing=wing_craft --digest 400` printed under a `craft:` line (review of #268: one call reads one wing, and craft must reach every project); without it: today's one unscoped call with `--digest 1600`; the preamble's "different project" sentence only when unset; on failure, the one-line "could not look" through `hookSpecificOutput.additionalContext` beside stderr |
| `clients/claude-code/hooks/agentsmemory-recall-hook.sh` | edit | the same three changes for SessionStart |
| `clients/claude-code/installer.go` | edit | the Claude hook environment prefix carries `AGENTSMEMORY_WING='<wing>'` beside the URL when `--wing` was given — this is what SELECTS the wing for the hook, and the test deletes it |
| `clients/claude-code/doctor.go` | edit | the hook environment `doctor` prints and runs with includes the wing, so a reinstall that added it is visible |
| `clients/claude-code/recallscope_test.go` | add | `TestTheRecallHookCarriesTheInstalledWing` (fake MCP server records the requests; with the env set there are TWO searches, the first carrying the wing and the second `wing_craft`, and the craft page's hits appear under `craft:`; without it exactly one search carrying no wing), `TestARecallThatCouldNotLookSaysSoOnBothChannels` (server down: stdout carries `additionalContext` with "could not look", stderr carries the same line) |
| `clients/claude-code/installer_test.go` | edit | `TestTheHookPrefixCarriesTheWing` |
| `clients/claude-code/README.md` | edit | the hooks paragraph: what is injected now, and what `AGENTSMEMORY_WING` does |

## Ordered Steps

1. [S1] Write the three tests red.
2. [S2] Hooks: digest, wing, preamble, both-channel failure line. [proof: mutation]
3. [S3] Installer prefix and doctor's echo of it. [proof: mutation]
4. [S4] README. [proof: human: the reviewer reads the paragraph against a real injection]
5. [S5] Mutants, one per Rests-on mechanism: drop the `-a wing=` argument; drop the craft call; print `$HITS` raw instead of the digest; send the failure line to stderr only. [proof: mutation]
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
| `TestTheRecallHookCarriesTheInstalledWing` | `clients/claude-code/recallscope_test.go` | the search carries the wing iff the env is set | — | S1, S2 |
| `TestARecallThatCouldNotLookSaysSoOnBothChannels` | `clients/claude-code/recallscope_test.go` | a dead server is named on additionalContext and stderr | — | S1, S2 |
| `TestTheHookPrefixCarriesTheWing` | `clients/claude-code/installer_test.go` | the installer writes the wing into the prefix | — | S1, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the three tests |
| 2 — something selects it | the installer's prefix line and the hook's `-a wing=` line; the mutants delete each |
| 3 — the caller can discover it | README; `doctor` prints the environment it runs the hook with |
| 4 — it is used | S6's re-measurement on this machine, recorded in the ADR |

## Mutation Log

- 2026-09-05 · 9a4ea94* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` · the installed wing must reach the search; dropping it restores the workspace-wide recall the record exists to end · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · covers:the wing reaches the search when set
- 2026-09-05 · 9a4ea94* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` · the hook must ask for the digest; without --digest it injects the JSON page again · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · covers:the digest is what the hook prints
- 2026-09-05 · 9a4ea94* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` · a failed recall must reach the model through additionalContext, not stderr alone · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · covers:the could-not-look line on additionalContext
- 2026-09-05 · 8d8c898* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` · a scoped recall must still read wing_craft; without the second call the craft wing goes silent and nothing reports it · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · covers:the craft call beside the project call

## Invariants

- Without `AGENTSMEMORY_WING` the hooks behave exactly as before this record, preamble included.
- With it, `wing_craft` is always the second call: a project-scoped recall never drops craft.
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
- 2026-09-05 · 9a4ea94* · exit 1 · `set -o pipefail …` · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · ms:452
  ```
  --- last 6 line(s) of stdout
  # github.com/atvirokodosprendimai/agentsmemory/clients/claude-code [github.com/atvirokodosprendimai/agentsmemory/clients/claude-code.test]
  clients/claude-code/installer_test.go:2091:9: undefined: hookCommandWithWing
  clients/claude-code/installer_test.go:2103:12: undefined: hookCommandWithWing
  clients/claude-code/recallwing_test.go:56:81: undefined: itoa
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code [build failed]
  FAIL
  ```
- 2026-09-05 · 9a4ea94* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · ms:18758
- 2026-09-05 · 9a4ea94* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · ms:17717
- 2026-09-05 · 9a4ea94* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · ms:17687
- 2026-09-05 · 8d8c898* · exit 0 · `set -o pipefail …` · acceptance-sha256:dc5bcf359c6795c0aab99f56e0143ade876012ee21813e85e97796120992ac0f · ms:19213
