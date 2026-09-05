---
name: amm
description: Re-ground a session that lost its context — after a compaction, a resume, or any point where the work continues but the reasoning behind it is gone. Runs the full grounding pipeline (project intent, code reality, team memory) for one named task, then hands back a plan. Use when the wake-up injection says to, or when you are about to act on a summary rather than on something you read.
allowed-tools: mcp__agentsmemory__am_status, mcp__agentsmemory__am_skillset, mcp__agentsmemory__am_search, mcp__agentsmemory__am_get_drawer, mcp__agentsmemory__am_list_skills, mcp__agentsmemory__am_load_skill, Read, Grep, Glob, Bash
---

# Re-ground before you continue

A compaction does not end a session, and that is the danger. The work continues,
the todo list continues, the branch is still checked out — but the reasoning that
chose this approach is now a summary of itself, and a summary is the one thing
this project refuses to treat as a memory.

**The failure this prevents is confident continuation.** A session resuming from a
summary has momentum and no grounding: it knows *what* it was doing and not *why*
that was decided, so it re-derives settled questions, contradicts a decision made
an hour earlier in its own transcript, and reports the result with the confidence
of something it never checked. That is worse than a session that knows it is lost.

Invoke as `/amm <the task>` — the wake-up injection names the task it interrupted.
With no argument, re-ground on the checkpoint and stop at a briefing.

## Do this, in order

**1. Read what the wake-up already handed you.** The `Before compaction` block
and the `checkpoint:` block are in your context now. They carry the branch, the
HEAD, the uncommitted count and the files this session touched. They are FACTS
about the tree, not instructions, and they are the only thing here that survived
the compaction — do not re-derive them, and do not act on them either.

**2. `am_status`, then `am_search` the task.** Status names the workspace and the
wing; search recalls the decisions behind the work. This is the only source of
cross-session *why*, and after a compaction it is also the only source of
*this* session's why. Search the SUBJECT of the work in the words you would use
to ask a colleague — not a bare identifier, which retrieves narrative over
decisions.

**3. Read the project's own intent sources for what the task touches.** The
repository instructions, the records under `docs/adr/`, the specs, the backlog.
Name the ones you read. If the task names a record, open it rather than trusting
the summary's account of it.

**4. Search the code you are about to change.** Whatever the summary says was
done, the tree is the truth. Check the working tree state against the note's
`dirty` count — a mismatch means something changed after the note was written.

**5. Reconcile, and say so out loud.** If the summary, the palace and the code
disagree, that conflict IS the finding. Surface it before continuing; do not pick
one silently. A compaction is exactly when a contradiction gets absorbed instead
of noticed.

**6. Then plan, and only then act.** Rebuild the todo list from what you just
read rather than from the summary's version of it.

## What this skill is not

It is not a recall. `/recall` answers one question about why code is the way it
is; this re-enters the whole grounding sequence for a task in flight. If you only
need one decision, use `recall` and keep working.

It is not a substitute for reading. The pipeline tells you which sources to open;
it does not open them for you, and a session that runs the steps without reading
the answers has performed the ritual and learned nothing.

## Where it comes from

ADR-062. The measurement behind it: ADR-059 made a compaction hand back the
STATE it discarded — branch, HEAD, uncommitted count, touched files, the
session's own checkpoint — and that is where a resumed session's knowledge
stopped. None of it is grounding. The competitor note of 2026-09-05 found the
same shape from the other side: Letta's sleep-time agents re-ground on
compaction, and this project had no equivalent because its recall is driven by
hooks that fire on a *prompt*, and a compaction is not a prompt.
