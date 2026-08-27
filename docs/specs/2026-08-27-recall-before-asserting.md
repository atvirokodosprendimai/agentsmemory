# Spec: Make recall happen without depending on the agent remembering

> **Date:** 2026-08-27 · **Status:** Grilling
> **Owner:** Zy · **Becomes:** ADR-NNN (allocate at merge)
> **Gate:** Status may become Ready-for-ADR only after `spec-verify --spec docs/specs/2026-08-27-recall-before-asserting.md` exits 0.
> **Cross-references:** ADR-017 (a subagent is a session — its investigation is the prior art this spec must not reinvent), ADR-036 (the recall that answers — the memory this session failed to ask for), `internal/mcpserver/server.go:serverInstructions`, `clients/claude-code/hooks/`

## Problem

An agent working **in this repository**, with the memory protocol delivered three times over, published two false claims that one `am_search` would have prevented — one of them into `clients/claude-code/bootstrap.md`, which `go:embed`s into every install. The palace held the correct answer at **rank 1**. This is not a retrieval problem and not a delivery problem: ADR-017 measured the same shape in 2026-08 and named it, *"a COMPLIANCE failure, not a delivery failure."* Today extends that finding from subagents to the main loop.

## Goal

Move the measured rate at which a session recalls before asserting that nothing changed — from a baseline that does not exist yet to a number, then above it. **No mechanism ships before the instrument does.**

## Actors

| Actor | Kind | Goal |
|-------|------|------|
| Working agent | system | Answer correctly at least cost; will skip any step that feels unnecessary in the moment |
| Session transcript | system | The only honest record of what an agent actually did |
| Hook (SessionStart / Stop / SubagentStop) | scheduled job | Reach the agent without depending on it choosing to be reached |
| MCP surface | system | Carry a cue at the moment of the call |
| Maintainer | human role | Decide which mechanisms are worth their context cost |

## Use Cases

### UC-1: The instrument counts recall-before-assertion from transcripts

- **Trigger:** a session ends (`Stop` / `SessionEnd`, which already receive `transcript_path`) · **Preconditions:** a readable transcript
- **Main flow:**
  1. Scan the transcript for **no-change assertions** — the sentence class defined in F-2.
  2. For each, determine whether an `am_search` (or `am_get_drawer`) call preceded it in the same session.
  3. Record the pair count and the preceded count. Report nothing when the count is zero.
- **Failure paths:** a. transcript unreadable or absent → exit quietly, record nothing, never fail the session. b. the classifier matches zero assertions across a corpus of sessions known to contain them → the instrument is reporting a false all-clear and must say so rather than report 100%.
- **Postconditions:** a durable count exists that nobody self-reported.

### UC-2: An agent about to assert no-change meets a cue

- **Trigger:** the agent is about to state that something still behaves a certain way, or does not do something · **Preconditions:** the cue is reachable in that moment
- **Main flow:**
  1. The cue names the **sentence shape**, not the duty.
  2. It states what source cannot show: a fix looks identical to code that was always right.
  3. The agent searches, or does not — and the instrument counts which.
- **Failure paths:** a. the cue is delivered as one more paragraph of protocol → ADR-017 already measured this as the least promising intervention; it must not be counted as a mechanism. b. the cue fires on every turn → it becomes noise and the scarcity rule (F-6) is violated.
- **Postconditions:** the cue's presence is attributable in the transcript, so its effect is separable from the other mechanisms.

### UC-3: A mechanism is accepted only if the measured rate moves

- **Trigger:** a candidate mechanism is built · **Preconditions:** UC-1's baseline exists
- **Main flow:**
  1. Record the rate before.
  2. Ship exactly one mechanism.
  3. Record the rate after, over a comparable session count.
- **Failure paths:** a. two mechanisms ship together → neither is attributable and the measurement is spent. b. the delta is inside noise → the mechanism is reported as not-shown-to-work, not as "directionally right".
- **Postconditions:** every shipped mechanism has a number beside it, including the ones that did not work.

## Scenarios

### UC1-S1 [happy] A transcript containing an unrecalled no-change assertion is counted as one miss [@draft] → `— to bind`

```gherkin
Given a session transcript in which the agent wrote that a behaviour "still" works a certain way
And no am_search call appears earlier in that transcript
When the instrument scans it
Then it records one no-change assertion and zero preceded-by-recall
```

### UC1-S2 [failure] A transcript the instrument cannot read reports nothing rather than a clean rate [@draft] → `— to bind`

```gherkin
Given a session whose transcript path is missing or unreadable
When the instrument runs
Then it records no observation at all
And it does not report a rate, because an unread transcript is not a compliant session
```

### UC1-S3 [failure] A classifier that matches nothing is reported as broken, not as perfect [@draft] → `— to bind`

```gherkin
Given a fixture corpus of transcripts known to contain no-change assertions
When the classifier matches none of them
Then the instrument fails loudly
And it does not report a 100% recall rate
```

### UC2-S1 [happy] The cue names the sentence shape rather than the obligation [@draft] → `— to bind`

```gherkin
Given the MCP instructions field
When an agent reads it
Then it finds the class of claim that requires a recall
And it does not find a bare instruction to recall before acting
```

### UC2-S2 [failure] A cue delivered as additional protocol text is not counted as a mechanism [@draft] → `— to bind`

```gherkin
Given a proposed change that adds a paragraph to a document the agent already receives in full
When it is evaluated against this spec
Then it is rejected as a mechanism
And ADR-017's measurement is cited as the reason
```

### UC3-S1 [happy] A mechanism ships alone and carries its measured delta [@draft] → `— to bind`

```gherkin
Given a recorded baseline rate
When exactly one mechanism ships
Then the after-rate is recorded over a comparable session count
And the delta is reported whichever way it falls
```

### UC3-S2 [failure] A delta inside noise is reported as not shown to work [@draft] → `— to bind`

```gherkin
Given a measured delta smaller than the instrument's resolution
When the result is written up
Then the mechanism is recorded as not shown to work
And it is not described as directionally correct
```

## Facts

| ID | Assertion (invariant / behavior) | Test (`path::name`) | Tag | Cmd (optional) |
|----|----------------------------------|---------------------|-----|----------------|
| F-1 | The recall rate is counted from session transcripts, never from an agent's self-report. | `— to bind` | @draft | |
| F-2 | The countable unit is a **no-change assertion**: a claim that something still behaves a given way, or does not do something, or has not been decided. | `— to bind` | @draft | |
| F-3 | No mechanism intended to raise the rate ships before a baseline has been recorded. | `— to bind` | @draft | |
| F-4 | A classifier that matches zero assertions over a fixture corpus containing them fails, rather than reporting a perfect rate. | `— to bind` | @draft | |
| F-5 | An unreadable or absent transcript records no observation, and never fails the session. | `— to bind` | @draft | |
| F-6 | A hook adds no output in the common case; it speaks only when it has something the session would otherwise get wrong. | `— to bind` | @draft | |
| F-7 | `serverInstructions` stays within its measured ceiling; any cue replaces text rather than adding to it. | `internal/mcpserver/instructions_test.go::TestInstructionsStayShort` | @draft | |
| F-8 | Adding a paragraph to a document the agent already receives in full is not a mechanism under this spec. | `— to bind` | @draft | |
| F-9 | Exactly one mechanism ships per measurement window. | `— to bind` | @draft | |
| F-10 | Every mechanism's result is recorded whichever way it falls, including no effect. | `— to bind` | @draft | |

## Domain

**No-change assertion** — a claim that the current state equals the past state. **Recall** — an `am_search` or `am_get_drawer` call. **Pushed content** — palace content that enters context without the agent asking. **Pulled content** — content the agent must call for. **Mechanism** — a change intended to raise the rate, excluding additional protocol prose (F-8).

## Contracts Touched

| Surface | Change | Consumers |
|---------|--------|-----------|
| `serverInstructions` (MCP `instructions`) | modify | every MCP client, on handshake |
| Hook scripts + `settings.json` registration | add/modify | every installed agent |
| Tool descriptions (`am_search`) | modify | every agent, at the call |
| A durable store for the rate | add | the maintainer reading the result |

## Non-Goals

- **Retrieval quality and ranking** (permanent: the answer was at rank 1 — this is not ADR-001/002/003's territory).
- **The skillset durability hole** (deferred: M's finding, PR #79 owns it).
- **Making hooks louder in the common case** (permanent: F-6, and the verify hook's own reasoning).
- **Judging a mechanism by asking an agent whether it would have complied** (permanent: ADR-017's method note — the probe said "likely yes", which is what the question selects for).

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The classifier for a no-change assertion is unreliable, so the instrument measures its own regex | High | High | F-4: a fixture corpus that must produce matches; report classifier precision alongside the rate, never the rate alone |
| Pushed recall spends context on every session to fix a minority of them | Med | High | F-6; scope the push to a trigger, not to every start; measure context cost as well as rate |
| The observed failure is one session (this one), generalised into a rule | High | Med | ADR-017 measured the same class independently in 2026-08; the instrument exists to replace both anecdotes with a count |
| A mechanism raises the rate by making agents search uselessly | Med | Med | Count searches that preceded an assertion, not searches; a search on an unrelated subject is not a recall |
| Transcript scanning reads content that must not leave the machine | Low | High | The instrument runs locally in a hook and records counts, never transcript text |

## Open Questions

1. **Is the cue-shaped `serverInstructions` rewrite accepted as drafted?** · owner: Zy · blocks F-7. Proposed text: *"SOURCE SHOWS THE STATE; THIS PALACE SHOWS THE CHANGE. Before you write that something STILL works a certain way, or does NOT do something — you are asserting nothing changed, and code cannot show you that: a fix looks identical to code that was always right."*
2. **Which trigger does pushed recall use?** · owner: Zy · blocks F-6. Candidates: SessionStart with branch/diff context; `PreCompact` (a fresh context is where this session's failure began); `PreToolUse` on a source search. Only the first two are events the installer already registers.
3. **Is eager (non-deferred) MCP tool loading in scope?** · owner: Zy · blocks nothing yet. It removes an activation cost rather than adding a cue, and it is an install-side change, not a server one.
4. **Where does the rate live?** · owner: Zy · blocks F-1. A local file, a `search_events` column, or a drawer — each has a different privacy and durability profile.

## Verify

```bash
spec-verify --spec docs/specs/2026-08-27-recall-before-asserting.md
```

## Grill Log (appendix)

| # | Question | Fact | Decision |
|---|----------|------|----------|
| 1 | Is this a retrieval problem? | F-1 | No — scouted: the answer was rank 1 for a natural question. Specced as compliance, not ranking. |
| 2 | Has this been investigated before? | F-8 | Yes — ADR-017, 2026-08-21, independently. Its conclusion is adopted rather than re-derived. |
| 3 | Can the agent's own account be used as evidence? | F-1 | No — ADR-017's method note: asked whether it would have complied, the probe said "likely yes". |
| 4 | What is the countable unit? | F-2 | The no-change assertion — both of today's errors share that shape. |
| 5 | Does a mechanism ship before measurement? | F-3, F-9 | No, and one at a time, per the repo's acceptance principle. |
| 6 | Is more protocol text a candidate? | F-8 | No — measured as least promising by ADR-017. |
| 7 | Should hooks get louder? | F-6 | non-behavioral in effect; the verify hook's own comment settles it. |
