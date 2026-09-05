# Task ADR-061-T1: The Stop hook writes the project's last-turn note

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the note `${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}/agentsmemory-last-turn/<project-key>` with `at`, `session`, `branch`, `head`, `dirty`, `touched`, `file` (≤8), `prompt` (≤N)
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the note is keyed by project`, `prompts are the last plain user messages only`, `the off knob`, `the note is private to the user`

## Goal

Every Stop (not SubagentStop) leaves a `key=value` note of the tree and the last user prompts where the next session's start can read it, unless `AGENTSMEMORY_LAST_TURN=off`.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-stop-hook.sh` | edit | the writer block after the touched-list read; reads `transcript_path` from the event |
| `clients/claude-code/lastturn_test.go` | add | `TestTheStopHookWritesTheLastTurnNote`, `TestTheLastTurnNoteIsOffWhenAsked` |
| `clients/claude-code/README.md` | edit | the two knobs in the knob list (`TestReadEnvVarsAreDocumented` reads Go only, so this is review-enforced; the shell hooks' knobs live in the README by convention) |

## Ordered Steps

1. [S1] Write both tests red. `TestTheStopHookWritesTheLastTurnNote`: a temp git repo (one commit, one dirty file), a touched list of ten paths, a fake transcript with five `type: user` lines — three plain strings, one tool-result array, one over 200 chars — and a `hook_event_name: Stop` event naming it; assert the note under the project key holds `branch=`, `head=`, `dirty=1`, `touched=10`, eight `file=` lines, exactly three `prompt=` lines, the newest first, none containing `tool_use_id`, none over 200 chars; and that a `SubagentStop` event writes nothing. `TestTheLastTurnNoteIsOffWhenAsked`: `AGENTSMEMORY_LAST_TURN=off` writes nothing; `AGENTSMEMORY_LAST_TURN_PROMPTS=1` writes one.
2. [S2] Implement the block and the README knobs. [proof: mutation]
3. [S3] Mutants: key by session id; take every `type: user` line; ignore the off knob. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestTheStopHookWritesTheLastTurnNote$|TestTheLastTurnNoteIsOffWhenAsked$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ -run 'TestTheStopHookNamesTouchedPaths$|TestTheStopHookIsQuietWhenNothingWasTouched$' -count=1 2>&1 | tee /tmp/acc2.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc2.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestTheStopHookWritesTheLastTurnNote` | `clients/claude-code/lastturn_test.go` | note keyed by project, bounded files, last three plain prompts newest first, nothing on SubagentStop | — | S1, S2 |
| `TestTheLastTurnNoteIsOffWhenAsked` | `clients/claude-code/lastturn_test.go` | the two knobs | — | S1, S2 |
| `TestTheStopHookNamesTouchedPaths` | `clients/claude-code/anchorcue_test.go` | the nudge still names touched files (regression) | — | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the tests drive the shipped script |
| 2 — something selects it | the Stop hook is registered on Stop already (`TestNoHookPlanIsRegisteredTwice` and the plugin manifest gate keep it so); the block runs on every Stop, and the "ignore the off knob" mutant proves the knob is read |
| 3 — the caller can discover it | the README knob list; no gate reads a shell hook's environment, so review checks it |
| 4 — it is used | T2 reads it; the record's Follow-up counts branch matches |

## Mutation Log

- 2026-09-05 · f398864* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-stop-hook.sh` · the note is keyed by session id, which a new session can never find · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · covers:the note is keyed by project
- 2026-09-05 · f398864* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-stop-hook.sh` · every content string on a user line is taken, so a tool result becomes a prompt · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · covers:prompts are the last plain user messages only
- 2026-09-05 · f398864* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-stop-hook.sh` · the off knob is ignored · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · covers:the off knob
- 2026-09-05 · 708fc34* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-stop-hook.sh` · prompt text on disk becomes world-readable · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · covers:the note is private to the user

## Invariants

- The note is written AFTER the nudge is printed and never changes the hook's exit code or stderr text — the nudge's tests are in the fence.
- The session id and project path are validated before either becomes a path component.
- Prompt lines are single-line and at most 200 characters.

## Risks

- `transcript_path` absent (older harness): no `prompt=` lines, the rest of the note still written.

## Stop Condition

Stop if the Stop event's payload does not carry `transcript_path` in this checkout's Claude Code — the prompt half would then be built on nothing; measure with a real Stop first.

## Out of Scope

- Reading the note (T2).

## Verification Log
- 2026-09-05 · f398864* · exit 1 · `set -o pipefail …` · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · ms:3763
  ```
  --- last 7 line(s) of stdout
  --- FAIL: TestTheStopHookWritesTheLastTurnNote (0.14s)
      lastturn_test.go:76: expected one note, got []
  --- FAIL: TestTheLastTurnNoteIsOffWhenAsked (0.16s)
      lastturn_test.go:124: AGENTSMEMORY_LAST_TURN_PROMPTS=1 recorded 0 prompts:
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	0.821s
  FAIL
  ```
- 2026-09-05 · f398864* · exit 0 · `set -o pipefail …` · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · ms:4555
- 2026-09-05 · f398864* · exit 0 · `set -o pipefail …` · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · ms:2882
- 2026-09-05 · f398864* · exit 0 · `set -o pipefail …` · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · ms:2962
- 2026-09-05 · 708fc34* · exit 0 · `set -o pipefail …` · acceptance-sha256:ef4a16e8b5e5e8d6631fb8516fcd47a751da60afb449ff9c05bd5657b18a4566 · ms:2381
