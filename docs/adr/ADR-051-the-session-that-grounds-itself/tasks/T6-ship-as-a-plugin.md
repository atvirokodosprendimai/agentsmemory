# Task ADR-051-T6: Ship the kit as one plugin instead of a script that edits settings

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (a manifest, a marketplace entry, a migration path)
**Owner:** unassigned
**Produces:** `one installable unit`
**Consumes:** none
**Data dependency:** hermetic

## Goal

Declare commands, agents, skills, hooks and the MCP server in one versioned manifest, so the
installer stops hand-writing registrations that a plugin format states.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/.claude-plugin/plugin.json` | add | the manifest |
| `clients/claude-code/hooks/hooks.json` | add | hook registrations as data rather than as installer code |
| `clients/claude-code/assets.go` | edit | embed the manifest |
| `clients/claude-code/installer.go` | edit | plugin path beside the existing path, not instead of it |
| `clients/claude-code/doctor.go` | edit | report duplicate registrations |

## Ordered Steps

1. Write the failing tests first (TDD red).
2. Write `plugin.json` and `hooks.json` declaring exactly what the installer registers today.
   The set must match, and the test that says so is the point of the task.
3. ⚠ **The plugin path is DECLARED, not yet wired as an install route.** The manifest ships and
   the equality gate holds it to the installer's plan, so the two cannot diverge. Actually
   installing *through* `/plugin install` is a distribution change that needs a marketplace
   entry and a release, and is deliberately left to the follow-up rather than half-done here.
   The existing installer path is untouched.
4. Teach `doctor` to fail on a duplicated registration — a hook registered by both paths is
   silent and doubles every injection.
5. Run the fence, the mutants, the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestThePluginDeclaresEveryHookTheInstallerRegisters|TestThePluginManifestIsValid|TestThePluginManifestHardcodesNoHomePath|TestDoctorFailsOnADuplicateRegistration' \
  -count=1 2>&1 | tee /tmp/adr051-t6.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t6.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestThePluginDeclaresEveryHookTheInstallerRegisters` | `clients/claude-code/plugin_test.go` | The manifest's event set equals the installer's plan, derived from both sources rather than from a hand-kept list — so a hook added tomorrow joins the check on the same commit | — |
| `TestThePluginManifestIsValid` | `clients/claude-code/plugin_test.go` | The manifest parses and carries name, version and description | — |
| `TestThePluginManifestHardcodesNoHomePath` | `clients/claude-code/plugin_test.go` | Every command resolves through `${CLAUDE_PLUGIN_ROOT}`. A path a plugin hardcodes is a path it depends on, and an absolute home path in a shipped file is a personal path published to whoever installs it | — |
| `TestDoctorFailsOnADuplicateRegistration` | `clients/claude-code/plugin_test.go` | Two registrations of one script on one event is a finding, not a shrug — and it is the check no test of the install PLAN can make, because the plan has one writer and a duplicate needs two | — |

## Reachability

The manifest can be perfect and embedded and installed by nothing. The equality test derives
BOTH sides from source, which is the shape this repo requires of a list kept beside a truth —
a hand-maintained copy goes stale and this one cannot.

## Mutation Log

Filled by `adr-verify --mutant`. At minimum: one event dropped from the manifest.
- 2026-09-04 · 87aff31* · mutant killed · exit 1 · `clients/claude-code/.claude-plugin/hooks.json` · one event dropped from the manifest: a user installing the plugin silently loses that hook while the installer path keeps it · acceptance-sha256:ee58aac14c210f2f6222dbcd7e112901b5adfaae1a8b33484dc7bbc7cf9abed1
- 2026-09-04 · 87aff31* · mutant killed · exit 1 · `clients/claude-code/doctor.go` · doctor stops recording duplicates: a half-finished migration leaves a hook injecting twice and the command reports it as healthy · acceptance-sha256:ee58aac14c210f2f6222dbcd7e112901b5adfaae1a8b33484dc7bbc7cf9abed1

## Invariants

- The manifest and the installer register the same set, or the build fails.
- The existing path stays until the plugin path is proven.
- No duplicate registration survives `doctor`.

## Risks

A migration that leaves both installs registered doubles every hook. That is what step 4 exists
for, and it is why `doctor` changes in this task rather than a later one.

## Stop Condition

Stop if the plugin format cannot express something the installer does today — a partial
migration that silently drops a registration is worse than no migration.

## Out of Scope

- The non-Claude-Code kits. (permanent: boundary: codex, pi and cursor have different extension models and no plugin format)
- Publishing to a public marketplace. (deferred: `docs/adr/BACKLOG.md`)

## ⚠ Status: PARTIAL — the manifest is not loadable (review, 2026-09-04)

Claude Code loads plugin hooks from `hooks/hooks.json`; `.claude-plugin/` holds
`plugin.json` and nothing else. This task wrote `.claude-plugin/hooks.json`, added
no `.mcp.json`, and embedded neither — so a `/plugin install` of this directory
would register no hooks and no MCP server.

What stands: `doctor`'s `DUPLICATED` verdict, and the equality gate deriving both
sides from source. What does not: the claim that the kit is installable as a
plugin. Moving the manifest and adding the MCP declaration is the remaining work,
and it needs a live `claude plugin` check rather than another JSON-reading test —
the reviewer ran `claude plugin validate` and it did not look at the misplaced
files at all, which is why nothing here caught it.

## Verification Log

Filled by `adr-verify`.
- 2026-09-04 · 87aff31* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:ee58aac14c210f2f6222dbcd7e112901b5adfaae1a8b33484dc7bbc7cf9abed1 · ms:34914
- 2026-09-04 · 87aff31* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:ee58aac14c210f2f6222dbcd7e112901b5adfaae1a8b33484dc7bbc7cf9abed1 · ms:34470
- 2026-09-04 · 87aff31* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:ee58aac14c210f2f6222dbcd7e112901b5adfaae1a8b33484dc7bbc7cf9abed1 · ms:33755
