# Task ADR-057-T2: the installer registers the peer under one name and removes duplicate hook entries

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `codebaseMemoryMCPName = "codebase-memory-mcp"`; `dedupeHookEntries` in settings.go
**Consumes:** the `codebase-memory` doctor row (T1), which is how the result is checked on a real machine
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the single registration name`, `the dedupe of exact duplicate entries`, `the retirement of the old name`

## Goal

`--recommended` registers codebase-memory as `codebase-memory-mcp` only when upstream did not, removes a `codebasememory` registration it finds, and every install removes exact-duplicate hook entries within an event before writing its own.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/main.go` | edit | `codebaseMemoryName` becomes `codebaseMemoryMCPName = "codebase-memory-mcp"`; `retiredCodebaseMemoryName = "codebasememory"` kept only so the retirement can name it |
| `clients/claude-code/installer.go` | edit | `installRecommended` removes the retired name, registers the new one only when `.claude.json` lacks it; the `--dry-run` line says which of the two it would do |
| `clients/claude-code/settings.go` | edit | `dedupeHookEntries(hooks map[string]any) int` called at the top of `ensureHooks` for every event, counted into `changed` |
| `clients/claude-code/README.md` | edit | the `codebasememory` line becomes `codebase-memory-mcp`, and says the kit does not register twice |
| `clients/claude-code/settings_test.go` | edit | `TestEveryInstallRemovesDuplicateHookEntries` |
| `clients/claude-code/installer_test.go` | edit | `TestRecommendedRegistersThePeerOnceUnderUpstreamsName` |

## Ordered Steps

1. [S1] Write both tests red: a settings file with four identical `cbm-session-reminder` entries and two identical entries of an unrelated command collapses to one each and reports a change, and a second run reports none; the recording runner sees `mcp remove … codebasememory` and, when `.claude.json` already names `codebase-memory-mcp`, NO `mcp add` for it.
2. [S2] Implement `dedupeHookEntries` and call it from `ensureHooks`. [proof: mutation]
3. [S3] Rename the constant, reshape `installRecommended`, update the dry-run line and README. [proof: mutation]
4. [S4] Mutants, one per Rests-on mechanism: register under the old name; skip the dedupe call; skip the `mcp remove` of the retired name. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestEveryInstallRemovesDuplicateHookEntries$|TestRecommendedRegistersThePeerOnceUnderUpstreamsName$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryInstallRemovesDuplicateHookEntries` | `clients/claude-code/settings_test.go` | exact duplicates collapse for any command, once, idempotently | — | S1, S2 |
| `TestRecommendedRegistersThePeerOnceUnderUpstreamsName` | `clients/claude-code/installer_test.go` | one name, no second registration when upstream already did, retired name removed | — | S1, S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests |
| 2 — something selects it | `ensureHooks` calls the dedupe on every install; `installRecommended` is the only caller of the name — the mutants delete each call |
| 3 — the caller can discover it | README line and the `--dry-run` preview say what will be registered |
| 4 — it is used | the owner's next install over the real settings file, checked with T1's row |

## Mutation Log

## Invariants

- Dedupe matches the exact `(type, command)` pair; a differing env prefix is a different command.
- A retirement never removes `codebase-memory-mcp` itself.
- `ensureHooks` still writes nothing when nothing changed.

## Risks

- `claude mcp remove` of a name that is absent prints an error; it is already run with `ignoreErr`, as `addStdioMCP` does today.

## Stop Condition

Stop if upstream's `install.sh` has changed the name it registers — check the script's current text before renaming, because a name the kit adopts from upstream is only right while upstream keeps it.

## Out of Scope

- Reporting the state — T1.
- Upstream's installer (external: DeusData/codebase-memory-mcp: https://github.com/DeusData/codebase-memory-mcp)

## Verification Log
