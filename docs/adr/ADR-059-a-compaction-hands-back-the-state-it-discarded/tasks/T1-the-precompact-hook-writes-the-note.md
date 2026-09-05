# Task ADR-059-T1: A PreCompact hook writes the session's state note before the context is summarised

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the note file `${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}/agentsmemory-precompact/<session_id>` with `key=value` lines `at`, `trigger`, `branch`, `head`, `dirty`, `touched`, `file` (≤8)
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the note is written under the session id`, `the PreCompact registration`

## Goal

Every compaction leaves a note of the branch, HEAD, uncommitted count and the session's touched files where the post-compaction SessionStart can read it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` | add | the script; `# hook-output: none — <reason>` on line 3 so `TestANonInjectedChannelIsJustified` accepts it |
| `clients/claude-code/installer.go` | edit | `precompactHookAsset`, `precompactHookFile`, `precompactHookPath()`, the write in the companion-hook loop, and the `PreCompact` entry in `hookPlansOn` — the plan is the line that SELECTS the script |
| `clients/claude-code/hooks/hooks.json` | edit | the `PreCompact` entry with `"timeout": 75`; `TestThePluginDeclaresEveryHookTheInstallerRegisters` fails until it matches the plan |
| `clients/claude-code/precompact_test.go` | add | `TestThePreCompactHookWritesTheStateNote`, `TestThePreCompactHookIsRegistered` |
| `clients/claude-code/README.md` | edit | the hook list gains the PreCompact entry |

## Ordered Steps

1. [S1] Write `TestThePreCompactHookWritesTheStateNote` red: a temp git repo with one commit, one uncommitted edit and a touched list of ten paths for session `s1`; drive the script with `{"hook_event_name":"PreCompact","session_id":"s1","trigger":"auto"}`; assert the note holds `branch=`, `head=` (7+ hex), `dirty=1`, `touched=10`, exactly eight `file=` lines, and nothing on stdout. A second case with `session_id":"../x"` asserts no file is written. And `TestThePreCompactHookIsRegistered` red: `hookPlans()` carries a `PreCompact` plan whose command ends in the script's basename.
2. [S2] Write the script, the installer constants and plan, the manifest entry, the README line. [proof: mutation]
3. [S3] Mutants: write the note under a fixed name instead of the session id; delete the plan. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestThePreCompactHookWritesTheStateNote$|TestThePreCompactHookIsRegistered$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ -run 'TestThePluginDeclaresEveryHookTheInstallerRegisters$|TestEveryPlannedEventIsClassified$|TestANonInjectedChannelIsJustified$|TestEveryRegisteredHookIsAlsoWritten$|TestEveryHookRegistrationCarriesATimeout$' -count=1 2>&1 | tee /tmp/acc2.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc2.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestThePreCompactHookWritesTheStateNote` | `clients/claude-code/precompact_test.go` | the shipped script writes the note keyed by session id, bounded to eight files, refuses an unsafe id, prints nothing on stdout | — | S1, S2 |
| `TestThePreCompactHookIsRegistered` | `clients/claude-code/precompact_test.go` | the installer plans a PreCompact registration naming the script (rung 2) | — | S1, S2 |
| `TestThePluginDeclaresEveryHookTheInstallerRegisters` | `clients/claude-code/plugin_test.go` | the plugin manifest carries the same registration | — | S2 |
| `TestANonInjectedChannelIsJustified` | `clients/claude-code/hookchannel_test.go` | the script's `hook-output: none` line carries a reason | — | S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestThePreCompactHookWritesTheStateNote` drives the real script |
| 2 — something selects it | the `PreCompact` plan in `hookPlansOn`; `TestThePreCompactHookIsRegistered` fails when it is deleted, and the manifest gate fails when only one of the two paths carries it |
| 3 — the caller can discover it | the README hook list and the installer's `note:` line printed at install |
| 4 — it is used | T2 reads the note; `doctor` does not judge a `none` hook, and nothing counts compactions |

## Mutation Log

## Invariants

- The script never writes to stdout: PreCompact stdout goes to the debug log and a line there is a line somebody will one day expect the model to have read.
- The session id is a path component and is validated exactly as the touched hook validates it.
- Every `git` call tolerates a directory that is not a repository.

## Risks

- The touched list is absent in a session that edited nothing: `touched=0` and no `file=` lines, not a missing note.

## Stop Condition

Stop if `hookEventChannel("PreCompact")` is not a definite debug-log answer — the script's declared channel would then be a guess.

## Out of Scope

- Reading the note (T2).

## Verification Log
