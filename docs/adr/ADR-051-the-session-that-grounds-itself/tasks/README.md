# ADR-051 Tasks

Implementation tasks for ADR-051: The session that grounds itself. See the parent ADR for
the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers`
headers. This README is a derived index — when it disagrees with a task file, the task
file wins.

## Execution Order

**Waves are dependency levels, and only that.** A producer must land in a strictly earlier wave
than its consumers, which is what `adr-lint` checks; the thematic grouping in the parent record
is a reading aid and deliberately is not this table. ADR-041's F-9 rejected shipping four
mechanisms together because four at once produce one outcome and four candidate explanations —
nine would be worse, so within a wave these still land one at a time.

| Wave | Tasks | Why this wave |
|------|-------|---------------|
| 1 | T1, T2, T5, T6 | Depend on nothing. T1 and T6 are the two that unblock others |
| 2 | T3, T4, T7, T8, T9 | Each consumes a wave-1 contract |

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | T2 |
| 4 | T4 | T1 |
| 5 | T5 | none |
| 6 | T6 | none |
| 7 | T7 | T6 |
| 8 | T8 | T6 |
| 9 | T9 | T6 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Correct the hook channel table it currently forbids a working event | done | — | `go test ./clients/claude-code/ -run '…Channel…'` |
| T2 | Cue the memory that pins THIS file, by path, at PreToolUse | done | — | `go test ./clients/claude-code/ ./internal/palace/ -run '…Anchor…'` |
| T3 | Record what the session touched, at PostToolUse | done | — | `go test ./clients/claude-code/ -run '…Touched…'` |
| T4 | Inject on UserPromptExpansion, the channel T1 unblocks | done | — | `go test ./clients/claude-code/ -run '…Expansion…'` |
| T5 | A bounded resources/list so an address is discoverable | done | — | `go test ./internal/mcpserver/ -run '…Listing…'` |
| T6 | Ship the kit as one plugin instead of a script that edits settings | done | — | `go test ./clients/claude-code/ -run '…Plugin…'` |
| T7 | Put the palace on the status line | done | — | `go test ./clients/claude-code/ -run '…StatusLine…'` |
| T8 | A native skill that reaches the centralised catalogue | done | — | `go test ./clients/claude-code/ -run '…Skill…'` |
| T9 | The unattended loop: what runs alone, and what still gates | done | — | `go test ./clients/claude-code/ -run '…Unattended…'` |

Status: `pending` | `partial` | `blocked` | `done`.

⚠ **T6 and T9 were marked `done`, downgraded to `partial` by an independent
review on 2026-09-04, and are `done` again only after being made to work.** The
review was right: both shipped artifacts that NOTHING LOADED.

- **T6.** Plugin hooks must live at `hooks/hooks.json`; `.claude-plugin/` is for
  `plugin.json` alone. The manifest here is at the wrong path, carries no MCP
  declaration, and is not embedded — so `/plugin install` would load none of it.
  The equality gate holds the manifest to the installer's plan, which is real and
  worth keeping, but it validates a file Claude Code never reads.
- **T9.** `permissions` is not a key plugin `settings.json` supports, and nothing
  in this repository reads that file except its own tests. The rules are inert.

Neither was a lie about test results — every receipt was real — and that is the
uncomfortable part: **the fences passed because the tests read the same files the
code wrote, and never asked whether anything downstream consumes them.** That is
this corpus's §Reachability defect committed by the gates written to prevent it.

**What closed them was asking something other than the filesystem.**

- **T6.** The manifest moved to `hooks/hooks.json` and `.mcp.json` was added, and
  `claude --plugin-dir . plugin details agentsmemory` now reports **Hooks (9), MCP
  servers (1), Skills (4), Agents (1)**. Measured both ways first: with the
  manifest at the old path the same command reports **Hooks (0)**. ⚠ Note that
  `claude plugin validate` passed in BOTH states without mentioning hooks — the
  validator was never the check.
- **T9.** The rules moved to a `--settings` file, which is the route Claude Code
  loads. Proven with a two-arm probe over one harmless command: deny `echo` and
  the model answers `BLOCKED`; allow it and the command runs. ⚠ "The harness
  accepted the file" was rejected as evidence first — measured, `--settings` also
  accepts `{"permissions":{"deny":"not-an-array"}}` without complaint.

Acceptance commands are abbreviated here; each task file carries the full fence including
its `no tests to run` guard. The task file wins.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `a channel table that matches the documented four` | T4 | T4 cannot pass the install gate until T1 lands |
| T2 | `path-keyed anchor lookup` | T3 | T3 records the paths T2 reads |
| T6 | `one installable unit` | T7, T8, T9 | the status line, the skill and the permission rules ship inside the plugin |

## Notes

- Fences run the LOCAL Go toolchain, matching ADR-045 through ADR-050.
- Every task registering a hook must also carry a `# hook-output:` declaration, or
  `TestEveryHookScriptDeclaresItsOutputChannel` fails — that gate is already in the tree and
  new scripts join it rather than needing new coverage.
- ⚠ **T2 is not ADR-041's T5.** T5 is stopped on a measured finding about QUERY QUALITY;
  T2 issues no query. Any review of T2 should start by reading T5's stop note, because the
  two reach the same event and only the retrieval differs.
