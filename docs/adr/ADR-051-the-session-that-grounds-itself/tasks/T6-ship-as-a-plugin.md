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
3. Add the plugin install path. **Keep the existing path** until the acceptance below proves
   the two produce the same set.
4. Teach `doctor` to fail on a duplicated registration — a hook registered by both paths is
   silent and doubles every injection.
5. Run the fence, the mutants, the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestThePluginDeclaresEveryHookTheInstallerRegisters|TestThePluginManifestIsValid|TestDoctorFailsOnADuplicateRegistration' \
  -count=1 2>&1 | tee /tmp/adr051-t6.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t6.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestThePluginDeclaresEveryHookTheInstallerRegisters` | `clients/claude-code/plugin_test.go` | The manifest's event set equals the installer's plan, derived from both sources rather than from a hand-kept list — so a hook added tomorrow joins the check on the same commit | — |
| `TestThePluginManifestIsValid` | `clients/claude-code/plugin_test.go` | The manifest parses and carries name, version and description | — |
| `TestDoctorFailsOnADuplicateRegistration` | `clients/claude-code/doctor_test.go` | Two registrations of one script on one event is a finding, not a shrug | — |

## Reachability

The manifest can be perfect and embedded and installed by nothing. The equality test derives
BOTH sides from source, which is the shape this repo requires of a list kept beside a truth —
a hand-maintained copy goes stale and this one cannot.

## Mutation Log

Filled by `adr-verify --mutant`. At minimum: one event dropped from the manifest.

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

## Verification Log

Filled by `adr-verify`.
