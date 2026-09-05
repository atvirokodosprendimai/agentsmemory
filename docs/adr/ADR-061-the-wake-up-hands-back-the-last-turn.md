# ADR-061: The wake-up hands back the last turn

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** Zydrunas
**Spec:** None — no spec stage
**Cross-references:** ADR-059 (a compaction hands back the state it discarded), ADR-058 (the recall injection is a digest with a budget), ADR-051 (the session that grounds itself), ADR-041 (recall that does not depend on remembering), clients/claude-code/hooks/agentsmemory-stop-hook.sh, clients/claude-code/hooks/agentsmemory-recall-hook.sh
**Governs:** clients/claude-code/hooks/agentsmemory-stop-hook.sh, clients/claude-code/hooks/agentsmemory-recall-hook.sh, clients/claude-code/commands/am.md, clients/claude-code/bootstrap.md, clients/claude-code/README.md
**Enforced-by:** `clients/claude-code/lastturn_test.go::TestAColdStartOnTheSameBranchHandsBackTheLastTurn`
**Invalidates:** none — checked. ADR-059 reads its note on `compact` only and leaves `startup`/`resume` "exactly as ADR-058 left it" — this record changes those two sources, by a different note with a different key, and ADR-059's `compact` path is untouched. ADR-059 §Alternatives deferred the `resume` case to BACKLOG; this record takes it. ADR-058's budgets are unchanged; the second 400-character slot goes to the checkpoint when the note's branch is the current branch and to `wing_craft` otherwise.
**Served-path change:** every Stop writes a per-project fact note (branch, HEAD, dirty count, touched files, the last user prompts); a `startup` or `resume` on the same branch opens the recall injection with it and asks the wing's `llm_open_threads` checkpoint instead of `wing_craft`; two environment knobs turn the note off or size it.

## Context

Proposal relayed from Mindaugas, 2026-09-05: at wake-up hand back the last 100 lines of the
previous session, recall on them, check the inbox and the unfinished tasks; `/am` should check
unfinished work and what was done last. Three parts already exist: `am_status` reports the inbox
count at every wake-up; a hook prints `adr-next`'s READY tasks at session start; ADR-059 hands the
pre-compaction note and the crash-resume checkpoint back — on `compact` only. What is missing is
the cross-SESSION half: what the previous session did last, on a `startup` or `resume`.

The last 100 lines of a transcript are the wrong input. Measured in this checkout on 2026-09-05,
the tail of a session transcript is tool output and hook payloads; the user's own prompts are a
few lines in it, and ADR-058 measured transcript chunks as the most harmful top hits a recall can
return. The owner's rule the same day: what goes into memory is concise facts and important bits,
no prose. So the note is `key=value` facts, and the recall query is the last user prompts, not the
tail.

`SessionEnd` is the obvious writer and the wrong one: it is not registered on Windows (the teardown
race, #150), and a crashed session never fires it. `Stop` fires at every turn end on every platform
and already reads the touched list, so the note is always as fresh as the last completed turn and
survives a crash — the same reason ADR-059 chose PreCompact over the summary.

## Existing Primitives Audit

- **ADR-059's note format and reader** (`key=value` lines, the `case` reader in the recall hook, the
  `Before compaction (…)` renderer): reused with a second key (project, not session) and a second
  header. One renderer, two headers.
- **The Stop hook's session-id and touched-list handling**: reused; the note writer is a block in
  that script, not a new hook, so there is no new registration and no new manifest entry.
- **ADR-059's checkpoint call** (`llm_open_threads`, branch name, floor off): reused on
  `startup`/`resume` under a branch-equality gate.
- **`AGENTSMEMORY_RECALL=off`, `AGENTSMEMORY_STOP_HOOK`**: the knob convention; two knobs added the
  same way and documented where those are — by review, since `TestReadEnvVarsAreDocumented` reads Go source and not the shell hooks.

## Decision

**Write half (T1), in the Stop hook, every Stop and SubagentStop-excluded.** After the existing
touched-list read, write `${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}/agentsmemory-last-turn/<project-key>`
— `<project-key>` is the basename of `CLAUDE_PROJECT_DIR` plus a short checksum of its full path,
so two checkouts of one repository get two notes — with `at`, `session`, `branch`, `head`, `dirty`,
`touched`, up to eight `file=` lines, and up to `AGENTSMEMORY_LAST_TURN_PROMPTS` (default 3, max 10)
`prompt=` lines: the last user prompts read from `transcript_path`, plain-string user messages
only (tool results are arrays and are skipped), each cut to 200 characters, newlines flattened.
`AGENTSMEMORY_LAST_TURN=off` skips the write. Overwritten every turn; never cleaned.

**Read half (T2), in the recall hook, on `source` `startup` or `resume`.** When the note exists and
`AGENTSMEMORY_LAST_TURN` is not `off`, the injection opens with
`Last turn (<at>, session <first 8 of id>): branch <b> at <head>, <dirty> uncommitted file(s)`,
then `edited: …` and one `prompt: …` line per recorded prompt — facts, no sentence around them —
before the recalled memories. When the note's `branch` equals the current branch, the second
400-character call asks `llm_open_threads` as ADR-059 does on `compact` (same work continuing);
otherwise it asks `wing_craft` as before (a cold start on other work). `compact` is unchanged.
The branch-work query for the FIRST call is unchanged: the prompts are handed back, not searched —
a prompt is a question, and the palace's answer to it was already injected when it was asked.

**Protocol (T3).** `/am` Step 1c and the bootstrap protocol gain one sentence: read the wake-up's
`Last turn` and `checkpoint:` blocks before planning; when neither is present, ask
`llm_open_threads` in your wing yourself.

**Config.** Two knobs, in the hook environment like the others: `AGENTSMEMORY_LAST_TURN` (`on`
default, `off`), `AGENTSMEMORY_LAST_TURN_PROMPTS` (0–10, default 3; 0 records no prompts). Both
documented in the kit README's knob list.

**What would make this wrong:** if the `Last turn` block on a real `startup` in this checkout does
not name the branch and prompts of the previous session, or names another checkout's. T2's sign-off
records one real restart.

## Alternatives Considered

- **The last 100 transcript lines, as proposed:** rejected; measured as tool output, and ADR-058's
  transcript-chunk finding says recalling on it retrieves the wrong thing. The prompts are the part
  of those lines a human wrote.
- **Write the note from SessionEnd:** rejected; not registered on Windows, never fires on a crash.
- **Recall on the prompts (a second search per start):** rejected; each prompt's recall already ran
  when it was submitted, and a startup that repeats three searches pays three round-trips for
  answers that are in the summary or were not needed.
- **Hand the note back on `compact` too:** rejected; ADR-059's PreCompact note is fresher there and
  the two would say the same thing twice.
- **Key the note by session id:** rejected; a new session has a new id and could never find it. The
  project key is what "the previous session in this checkout" means.

## Component / Boundary Impact

`clients/claude-code` only: two existing scripts, two docs, no new registration.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `…/agentsmemory-last-turn/<project-key>` | new `key=value` file | Stop hook (T1) | recall hook (T2) |
| `AGENTSMEMORY_LAST_TURN`, `AGENTSMEMORY_LAST_TURN_PROMPTS` | new env knobs, documented | operator | both hooks |
| SessionStart injection on `startup`/`resume` | opens with `Last turn`; second call is `checkpoint:` when the branch matches | recall hook | the model |
| `/am` and the bootstrap protocol, Step 1c | one sentence | T3 | agents |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the note path, key and `key=value` format | T1 | T2 | No — new file |

## Implementation

See `tasks/README.md`. Three tasks: T1 write, T2 read, T3 protocol.

## Consequences

- **Positive:** a restarted session knows what the last one was doing on this branch, from facts recorded at its last completed turn, on every platform, crash or not.
- **Positive:** the crash-resume checkpoint is read on the start that most needs it — the same branch, continued.
- **Negative:** one file write and one transcript tail read per turn end; bounded by the 75 s hook timeout and measured in T1.
- **Neutral:** prompts are stored on local disk in a temp dir, as the touched list already is; nothing leaves the machine.

## Out of Scope

- Reading the last 100 transcript lines. (permanent: boundary: measured as tool output; the prompts are the human part)
- Recalling on the prompts. (permanent: boundary: each prompt's recall ran when it was submitted)
- A note on `compact`. (permanent: boundary: ADR-059's PreCompact note is fresher there)
- Codex and pi. (permanent: fact: only the Claude kit ships companion hooks; citation: file `clients/claude-code/installer.go:760`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A stale note from days ago is handed back as "last turn" | Med | Low | the header carries `at` and the session id; the branch gate decides the checkpoint call, the note itself is always shown with its date |
| Prompt extraction picks a tool result or a hook payload | Med | Low | plain-string `content` only, `type: user` only, 200-char cut; T1's fixture has both shapes |
| Two checkouts of one repo share a note | Low | Med | the key carries a checksum of the full path |

## Rollback

Remove the block from the Stop hook and the `startup`/`resume` branch from the recall hook; notes are temp files.

## Follow-ups

- [ ] After a week, count startups whose note branch equalled the current branch against those that did not — the ratio says whether the checkpoint call is reaching the case it was built for.
