# ADR-062: A compacted session re-grounds before it continues

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** Zydrunas
**Spec:** None — no spec stage
**Cross-references:** ADR-041 (the recall that does not depend on remembering), ADR-051 (the session that grounds itself), ADR-059 (a compaction hands back the state it discarded), `docs/research/2026-09-05-competitor-parity.md`
**Governs:** clients/claude-code/hooks/agentsmemory-precompact-hook.sh, clients/claude-code/hooks/agentsmemory-recall-hook.sh, clients/claude-code/skills/amm/SKILL.md, clients/claude-code/assets.go, clients/claude-code/installer.go
**Enforced-by:** `clients/claude-code/reground_test.go::TestACompactStartTellsTheSessionToReGround`
**Invalidates:** none — checked. ADR-059 decided that a compaction hands back STATE and this record does not change one field of that note; it adds a line after it and a subject to the note it is read from. ADR-041's F-6 (the hook prints nothing when it has nothing) is untouched: the directive is inside the `source=compact` block, which only runs when a note exists.
**Served-path change:** a post-compaction `SessionStart` injection ends with a `PAUSE` line naming `/amm <task>`; the PreCompact note gains a `prompt=` field; the kit installs a second Agent Skill, `amm`.

## Context

ADR-059 closed the half of a compaction that is about the TREE: branch, HEAD,
uncommitted count, the files the session edited, and its own `llm_open_threads`
checkpoint. Measured on a real compaction, that works.

It closes nothing about the REASONING. A session resuming from a summary has its
todo list, its branch and its momentum, and its grounding is now a summary of
itself — which is the one artifact this project refuses to treat as a memory
anywhere else (ADR-038: a drawer is verbatim and never summarised). The hooks
that would re-ground it cannot help, and the reason is structural: **every recall
this kit performs is driven by a prompt** — `UserPromptSubmit`, or a
`SessionStart` whose query is built from the branch's file list — and a
compaction is not a prompt. The session does not ask a question; it simply
continues.

The failure that produces is not "the session knows less". It is **confident
continuation**: momentum without grounding, which re-derives settled questions,
contradicts a decision taken an hour earlier in its own transcript, and reports
the result with the confidence of something it never checked. ADR-041's opening
paragraph describes that exact session — "it read source, formed a belief,
published it, and was wrong" — and ADR-059 gave it back its branch, not its
reasons.

The competitor note of 2026-09-05 found the same shape from outside: **Letta's
sleep-time agents re-ground on compaction**, and this project had no equivalent.
The note also recorded the general form of the gap — "a session that ignores the
nudge files nothing" — which is the write side of the same asymmetry: the kit
makes recall automatic and leaves re-grounding to whether the agent thinks of it.

The class this record governs: **every path by which a session's context is
replaced while its work continues.** Enumerated 2026-09-05 with
`grep -ln 'source' clients/claude-code/hooks/*.sh` and the harness's own
vocabulary — `source` is `startup`, `resume`, `clear` or `compact`. Only
`compact` continues work that was already in flight; the other three begin one.
So `compact` is the whole class today, and the record says that rather than
implying the others were forgotten.

## Existing Primitives Audit

- The PreCompact hook already writes a per-session `key=value` note and the
  recall hook already parses it with a `case`. The task in flight is one more
  line in a format both sides already speak — no new file, no new key derivation,
  and the two stay keyed identically because they were already.
- The recall hook's `source=compact` block already exists and is already the
  place that speaks after a compaction. The directive is printed inside it.
- `skills/*/SKILL.md` is already the embed glob and `writeSkills` already the
  install loop; `amm` is a second skill, not a new mechanism.
- `/am`'s pipeline is not re-authored. The skill carries the same sequence
  scoped to one task, because a second copy of a protocol is a second thing to
  get wrong — the skill points at the steps and names what a compacted session
  must reconcile, which `/am` has no reason to say.

## Decision

**The PreCompact note carries the task in flight.** One `prompt=` line, read from
the transcript before the context is summarised — the last PLAIN user turn, with
sidechain turns and this kit's own recall injections excluded, because both are
`type=user` in a transcript and either would name the wrong work. One line, 200
characters, whitespace collapsed: it is a label for a skill invocation, not a
record of the prompt.

**A post-compaction start ends with a pause.** After ADR-059's state lines, the
injection prints `PAUSE — do not continue from the summary`, names `/amm <task>`
as the first action, and asks for the reconciliation to be stated out loud.

⚠ **It is an INSTRUCTION, not a trigger, and the record says so because the
distinction is not a detail.** No hook can invoke a skill — not on a timer, not
at all. A hook writes text into the model's context and the model chooses; the
harness offers no callback that runs a slash command, and a delay inside a hook
delays only the hook. So the mechanism is the wording and the placement: the
directive is printed last in its block, immediately before the recall, so it is
the instruction the model is still holding when it picks its first action. A
design that needed a real trigger would not be buildable here, and pretending
otherwise would ship a mechanism that cannot fire — which is the defect
§Reachability exists to catch.

⚠⚠ **THE LAST TWO SENTENCES ABOVE WERE WRONG, AND THEY ARE LEFT STANDING BECAUSE
THE WAY THEY WERE WRONG IS THE TRANSFERABLE PART.** Amended 2026-09-05, the same
day they were written, at the owner's third asking. Everything before them holds:
a hook still cannot invoke a skill. The error is the LEAP from that to *no
trigger is buildable here*, which was never checked — it generalised a fact about
HOOKS into a claim about the whole harness, and the counter-example was already
running in the session that wrote it. A persistent `Monitor` emits every stdout
line as a notification, and **a notification makes the session take a turn**;
measured that day, a monitor armed before a 15:21:19Z compaction delivered an
event to the same session after it, because a compaction replaces the CONTEXT and
not the SESSION. So the hook is not the trigger and never had to be: it writes a
marker whose APPEARANCE is the event, and a monitor the session armed converts
that into a turn. The two mechanisms were collapsed into one and the buildable one
was never tried. ADR-062-T3 ships it; this paragraph is what a reader needs in
order to distrust the confident sentence above it. **A boundary asserted without a
probe is a guess with a citation format**, and §Reachability's usual defect —
shipping something unreachable — has this mirror image: refusing to ship
something reachable because the record said it could not be done.

**`amm` is a skill, not a command.** `/am` is a command file the installer writes;
a skill is discovered by Claude Code and can be invoked by name from inside a
turn, which is what a post-compaction injection needs — the session is mid-turn,
not typing.

**The installer's skill list is derived from the embed.** Adding the second skill
made a latent divergence reachable: the embed is a glob, the install loop read a
hand-kept slice, and a skill that shipped without being listed would be installed
by nothing and reported by no check.

What would make this fail: a compaction whose injection carries the state lines
and no pause; a pause naming a sidechain's turn as the task; the directive
appearing on `startup`, `resume` or `clear`; a skill in the binary that no
install writes. Each has a mutant.

## Alternatives Considered

- **Put the re-grounding in `/am` and tell the session to run that:** rejected.
  `/am` is a command; a command is typed by a human. The injection is read by a
  model mid-turn, and a skill is the artifact that shape can invoke.
- **Have the PreCompact hook perform the recall and write it into the note:**
  rejected, and this is ADR-041's shipped defect approached from the other side.
  A recall performed before compaction answers the question the session had
  BEFORE its context was replaced, and it would be one more thing the summary
  pass could drop. The post-compaction side is where a recall is worth running.
- **Inject the full grounding sequence as text after every compaction:** rejected
  on ADR-058's measurement. The injection is budgeted at 1,600 characters for a
  reason; restating a pipeline there spends the budget on instructions the skill
  already holds and crowds out the state lines that cannot be looked up.
- **A timed trigger (`sleep`, then run the pipeline):** not available, as above.
  Recorded because it is the obvious first idea and the reason it fails is not
  obvious until you look for the callback that does not exist.

## Component / Boundary Impact

Internal to the Claude Code kit. Two hooks gain a field and a line; one skill is
added; one hand-kept list becomes derived. No server change, no migration, no new
component. Codex and pi are untouched — neither discovers skills, which is why
`writeSkills` is already Claude-only.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---|---|---|---|
| PreCompact note | new `prompt=` line, the task in flight | agentsmemory-precompact-hook.sh | agentsmemory-recall-hook.sh |
| `source=compact` injection | trailing `PAUSE … /amm <task>` line | agentsmemory-recall-hook.sh | the model |
| installed skills | `amm` beside `recall`; the set is derived from the embed | assets.go / installer.go | Claude Code's skill discovery |
| `AGENTSMEMORY_REGROUND` | new; `off` keeps the state note and drops the directive | the hook | operators |

## Implementation

See `tasks/README.md`. T1 the note's subject and the pause; T2 the skill and the
derived install set; T3 the marker and the monitor that turn the pause into a
wake, added after T1's live compaction showed the instruction is only as good as
the model's willingness to read it.

## Consequences

- **Positive:** a compacted session is told to stop and re-ground, by name, with
  the task it was on — the one moment it cannot ask for that itself.
- **Negative:** one more line in an injection ADR-058 worked to shrink. It is
  bounded (one line, a 200-character task label) and it is the line that decides
  what the session does next, which is the only claim on that budget worth making.
- **Neutral:** a session that ignores the directive is no worse off than before
  this record; the state note is unchanged and still printed first.

## Out of Scope

- A hook invoking a skill directly, on a timer or otherwise (permanent: boundary: the harness offers no callback that runs a slash command from a hook; T3 wakes the session with a monitor instead, and the model still chooses to run `/amm`)
- `startup`, `resume` and `clear` (deferred: docs/adr/BACKLOG.md — PR #278 is the open proposal for what a non-compaction start hands back, and two changes writing the same injection is how they drift)
- Re-grounding a subagent after ITS context is replaced (permanent: boundary: ADR-017 makes a subagent a session, and SubagentStart already recalls; a subagent that compacts is a case nobody has measured)
- Judging whether the session actually re-grounded (permanent: fact: nothing in a hook can read what the model did next; the only evidence is the transcript, and reading it back is `aiagentmemory mine`; citation: file `clients/claude-code/hooks/agentsmemory-recall-hook.sh:4`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| The pause is read as noise and skimmed | Med | Med | it names one action and one task, printed last in its block; a session that skims it is no worse off than today |
| A prompt containing a quote or backtick breaks the injected line | Low | Low | whitespace is collapsed and the field is one line; the reader is a model, not a shell, and the note is never sourced |
| The task label names the wrong work on a transcript shape not seen here | Med | Med | sidechain and injection turns are excluded and both exclusions have a mutant; the label is advisory — the skill re-grounds from the palace and the tree either way |
| Two changes write the same injection and drift | Med | High | PR #278 is named in Out of Scope as the owner of non-compaction starts; it is unmerged, so this record cites it as a PR rather than as a record that does not exist |

## Rollback

Revert T1's commit to drop the `prompt=` line and the pause; the note and the
state lines return to ADR-059's shape exactly. Revert T2's to drop the skill and
restore the listed install set. Neither touches persistent state: the note is
per-session and rewritten on every compaction.

## Follow-ups

- [ ] Record one real compaction in this checkout: the pause appears, names the task, and the session re-grounds. Until then T1 is `partial` for the same reason ADR-059's T2 is.
- [ ] Measure whether the directive changes behaviour — a session that re-grounds and one that skims are distinguishable in the transcript, and `aiagentmemory mine` is the instrument.
