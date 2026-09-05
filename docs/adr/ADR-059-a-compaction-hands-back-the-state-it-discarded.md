# ADR-059: A compaction hands back the state it discarded

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** Zydrunas
**Spec:** None — no spec stage
**Cross-references:** ADR-041 (recall that does not depend on remembering), ADR-051 (the session that grounds itself), ADR-058 (the recall injection is a digest with a budget), clients/claude-code/hooks/agentsmemory-recall-hook.sh, clients/claude-code/hooks/agentsmemory-touched-hook.sh, docs/adr/BACKLOG.md
**Governs:** clients/claude-code/hooks/agentsmemory-precompact-hook.sh, clients/claude-code/hooks/agentsmemory-recall-hook.sh, clients/claude-code/hooks/hooks.json, clients/claude-code/installer.go, clients/claude-code/README.md
**Enforced-by:** `clients/claude-code/precompact_test.go::TestACompactStartHandsBackTheStateNote`
**Invalidates:** none — checked. ADR-041 chose SessionStart over PreCompact for the RECALL because PreCompact's stdout is discarded; this record puts a hook on PreCompact that speaks to nobody and writes a file, and the SessionStart hook ADR-041 already registers matcher-less is what reads it. ADR-058's budgets are unchanged on `startup`, `resume` and `clear`; on `compact` the 400-character craft slot is re-purposed, which is a change inside ADR-058's total, not to it.
**Served-path change:** after a compaction, the SessionStart recall hook's injection opens with a short "before compaction" note — branch, HEAD, uncommitted count, the files this session edited — written by a new PreCompact hook, and the second recall call asks the wing's `llm_open_threads` room for the session's own checkpoint instead of `wing_craft`.

## Context

A compaction replaces the context with a model-written summary. What survives is what the summary
kept; what is lost first is the ground truth the session was standing on — which branch, which
commit, what is uncommitted, which files it had edited — because none of that is prose the summary
is optimised to keep. The session that motivated ADR-041 resumed exactly there and published a
wrong belief. This session compacted once on 2026-09-05 mid-release and resumed with a summary that
named the branch correctly and the working-tree state not at all.

Two facts decide the shape, both measured 2026-09-05 in this checkout:

- **The SessionStart hooks already fire on compaction.** They are registered matcher-less (the
  installer's comment on the recall plan says so, and ADR-041 T4 chose that deliberately), and
  this session's own post-compaction context carries `SessionStart:compact hook success` for both
  the verify hook (54 anchors checked, 1 drifted) and the recall hook (three diary hits and one
  craft fact). So the first half of the BACKLOG entry deferred from ADR-058 — "SessionStart with
  matcher `compact`, re-emitting the wake-up" — is already true of the recall, and adding a matcher
  would register a second copy of a hook that runs.
- **Nothing records the state before it is discarded.** PreCompact runs a hook whose stdout goes
  to the debug log (`debugLogEvents` in `hookchannel.go`, and the ADR-041 defect that put it
  there), so a PreCompact hook can only be useful by WRITING somewhere the post-compaction start
  reads. The touched-path recorder (ADR-051 T3) is the precedent: a PostToolUse hook that declares
  `hook-output: none`, writes a per-session file under `AGENTSMEMORY_STATE_DIR`, and is read by
  the Stop hook. The session id is the key, and Claude Code keeps it across a compaction.

What a post-compaction recall asks today is the branch-work query (branch name plus changed
basenames, room `diary`) — the same question a fresh start asks. It is the right question for a
fresh start and a weak one after a compaction, where the most useful record in the palace is the
one this session was told to write before its first edit: the crash-resume checkpoint in
`llm_open_threads` (AGENTS.md §The working loop, item 4). This session filed three of them today
and read none of them back after compacting, because nothing asked.

## Existing Primitives Audit

- **`agentsmemory-recall-hook.sh`** (ADR-041 T4, ADR-058): reused, extended. It already reads the
  event JSON from stdin and already runs on `compact`; it does not yet branch on the `source`
  field. The `recall()` helper takes a wing and a budget and can take a room.
- **`agentsmemory-touched-hook.sh`** (ADR-051 T3): the write-a-file-on-a-debug-log-event shape
  and the `AGENTSMEMORY_STATE_DIR` convention. Reused as the pattern and as a data source: the
  PreCompact note copies the session's touched list rather than recomputing it.
- **`hookPlansOn` / `hooks.json` / `TestThePluginDeclaresEveryHookTheInstallerRegisters`**: the
  registration, its plugin twin, and the gate that keeps them equal. Reused; one plan and one
  manifest entry added.
- **`hookEventChannel`**: PreCompact is already classified as a debug-log event, so
  `TestEveryPlannedEventIsClassified` passes without an edit and `TestANonInjectedChannelIsJustified`
  requires the new script's declaration line to carry a reason.
- **`am_search` with `room=llm_open_threads`**: the read side of the checkpoint the protocol
  already tells sessions to write. No new tool.

## Decision

Two halves, one contract between them.

**The write half (T1).** A new `agentsmemory-precompact-hook.sh`, registered on `PreCompact` by
the installer and the plugin manifest, declaring `hook-output: none` with its reason on the line.
It reads the event JSON, refuses a `session_id` that is not a safe path component (the same guard
as the touched hook), and writes `${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}/agentsmemory-precompact/<session_id>`
— overwritten on every compaction, so the file is always the most recent note — with one
`key=value` per line: `at` (UTC timestamp), `trigger` (`manual` or `auto`), `branch`, `head`
(short sha), `dirty` (count of `git status --porcelain` lines), `touched` (count of lines in the
session's touched list) and up to eight `file=` lines copied from that list. It prints nothing on
stdout and traces on stderr like every other hook here.

**The read half (T2).** The recall hook reads `source` from the SessionStart event. When it is
`compact` and a note exists for this `session_id`, the injection opens with a block of at most
ten lines rendered from the note — `Before compaction (<at>, <trigger>): branch <branch> at <head>,
<dirty> uncommitted file(s); edited this session: <files> (+N more)` — before the recalled
memories. And the second recall call, which on every other `source` asks `wing_craft` with the
400-character slot, on `compact` asks the installed wing's `llm_open_threads` room with the query
`WHERE SHOULD WORK RESUME AFTER A CRASH` followed by the branch NAME, `limit=1`, and NO
distance floor, under the same 400 characters, rendered under a `checkpoint:` line. Measured
2026-09-05 on this palace during T2's hand-run: under the hook's 0.42 floor the fixed sentence
alone returned zero hits (the three checkpoints sat at 0.428–0.463, every record in that room
opens with the same words); the sentence plus the branch name with the floor off put the
session's own checkpoint first (blended 0.735 against 0.65 for the next); and the sentence plus
the full branch-work query — branch plus eight changed basenames — ranked a day-old checkpoint
about a local reinstall first, because file names pull toward whichever record mentions those
files. The room is the scope — it
holds nothing but checkpoints — so the floor guarded against nothing there.
Only with `AGENTSMEMORY_WING` set: an unscoped checkpoint query returns some
other project's open thread, which is worse than silence. On `startup`, `resume` and `clear` the
hook behaves exactly as ADR-058 left it — the note is not read, because a note from a session that
was resumed a day later describes a tree that has moved.

**What would make this wrong.** If Claude Code assigned a new `session_id` after a compaction,
the note would never be found and the read half would be silent on every compaction. T2's
sign-off records one live compaction in this checkout with the note handed back; a silent one is
the stop condition, not a pass.

## Alternatives Considered

- **A SessionStart hook with matcher `compact`, as the BACKLOG entry proposed:** rejected because
  the hook already fires on `compact`; a matched registration beside the matcher-less one runs the
  recall twice, which is the DUPLICATED verdict doctor exists to report.
- **Print the state note from PreCompact itself:** rejected; PreCompact stdout goes to the debug
  log (ADR-041's shipped defect), and `hookSpecificOutput.additionalContext` on PreCompact lands
  in the context that is about to be summarised away.
- **Recompute the touched list at SessionStart from `git status`:** rejected; `git status` is
  what is uncommitted NOW, which is a subset of what the session edited (committed edits vanish
  from it) and a superset (a teammate's stash, generated files). The touched list is the
  session's own record, and it already exists.
- **Recall the checkpoint on every start, not only `compact`:** rejected for `startup` and
  `clear`, where the open thread belongs to a previous session and the wing's newest checkpoint
  may be days old — and the craft slot earns its place on a cold start. `resume` is the arguable
  case and is deferred rather than decided here.
- **Delete the note after handing it back:** rejected; a second compaction in the same session
  overwrites it, and a note left behind costs a few hundred bytes in a temp dir. A one-shot file
  would also make the read half untestable by re-running the hook.

## Component / Boundary Impact

`clients/claude-code` only: one new embedded hook, one new plan in `hookPlansOn`, one manifest
entry, one branch in the recall hook. The server and the MCP surface do not change. The state
directory is the same one the touched hook, the verify hook and the status line already share.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `PreCompact` hook registration (settings.json plan and `hooks/hooks.json`) | add, timeout 75 like every other entry | `hookPlansOn`, the manifest | Claude Code |
| `${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}/agentsmemory-precompact/<session_id>` | new file, `key=value` lines | precompact hook (T1) | recall hook (T2) |
| SessionStart injection on `source=compact` | opens with the note block; second call is `checkpoint:` from `llm_open_threads`, not `craft:` | recall hook | the model |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the note file path and its `key=value` line format | T1 | T2 | No — new file |

## Implementation

See `tasks/README.md`. Two tasks: T1 the write half, T2 the read half.

## Consequences

- **Positive:** a session that compacts resumes knowing its branch, commit, uncommitted count and
  the files it touched, from a record taken before the summary rather than from the summary.
- **Positive:** the crash-resume checkpoint the protocol asks every session to file is read by the
  mechanism that most needs it, so writing it stops being a favour to a hypothetical crash.
- **Negative:** one more hook process per compaction (PreCompact) — bounded by the same 75 s
  timeout, and doing three `git` calls and one file copy.
- **Negative:** on `compact`, `wing_craft` is not recalled; the craft fact a cold start injects
  is in the summary or is not, and this record chooses the checkpoint over repeating it.
- **Neutral:** the note is a temp file per session id, overwritten per compaction, never cleaned;
  the touched list has the same lifetime and nobody has minded.

## Out of Scope

- Reading the note on `resume`, where a note from the same session id may be hours old and the
  tree may have moved under it. (deferred: `docs/adr/BACKLOG.md`, under this record's name)
- A `PostCompact` hook. It is a debug-log event like PreCompact and this record has nothing it
  would need to write there. (permanent: boundary: the read half is SessionStart because that is
  the event whose stdout is injected, and a third event adds a registration for no channel)
- Codex, pi and Claude Desktop. Only the Claude kit ships companion hooks (`shipsCompanionHooks`),
  and no other client has documented a compaction event. (permanent: boundary: the same
  boundary ADR-051 draws for every companion hook)
- Whether `session_id` survives a compaction. The design assumes it does, as the touched-list
  read in the Stop hook already assumes, and T2's live sign-off is where the assumption is checked.
  (permanent: fact: Claude Code's hooks reference documents `session_id` on every event and `source`
  values `startup`, `resume`, `clear`, `compact` on SessionStart; citation: url https://code.claude.com/docs/en/hooks)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The note's git calls run in a directory that is not a repository (a session started outside one) | Med | Low | every `git` call is `2>/dev/null \|\| true`; the note is written with empty fields rather than not written |
| A second recall call on `compact` costs a second server round-trip at the moment the model is waiting | Low | Low | it replaces the craft call rather than adding to it; the budget total is unchanged |
| `session_id` changes across compaction and the note is never found | Low | High for this record | T2's Stop Condition: a live compaction that hands nothing back blocks `done` |
| The `source` field is absent on an older Claude Code | Low | Low | absent reads as not-`compact`, so the hook falls back to ADR-058 behaviour exactly |

## Rollback

Remove the PreCompact plan from `hookPlansOn` and the manifest entry; the recall hook's `compact`
branch is inert without a note. Notes already on disk are temp files and can be left.

## Follow-ups

- [ ] After one week of compactions in this checkout, count how often the checkpoint call returned a hit against how often the wing held a checkpoint newer than the session start — a checkpoint nobody filed is the silent case this record cannot fix.
