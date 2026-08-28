# ADR-041: Make recall happen without depending on the agent remembering

**Status:** Accepted
**Date:** 2026-08-27
**Owner:** Zy
**Spec:** docs/specs/2026-08-27-recall-before-asserting.md
**Cross-references:** ADR-017 (a subagent is a session — the prior art this record completes rather than reinvents), ADR-021 (the handshake carries the protocol — owns the surface T6 edits), ADR-036 (the recall that answers — the memory the motivating session failed to ask for), ADR-020, ADR-005, ADR-038
**Governs:** `clients/claude-code/hooks/**`, `clients/claude-code/installer.go`, `clients/claude-code/settings.go`, `internal/mcpserver/server.go`

<!-- Governs enumerated by: node scripts/adr-context.mjs internal/mcpserver/server.go clients/claude-code/installer.go clients/claude-code/hooks/agentsmemory-stats.sh (2026-08-27) — which returned six governing records, listed above. The class: every surface that reaches an agent WITHOUT the agent asking. Members deliberately excluded: the MCP tool handlers themselves, because a handler only runs once the agent has already decided to call it, which is the decision this record is about. -->

**Numbering:** ADR-041. Verified 2026-08-27: the tree holds up to ADR-038; open PRs claim 039 (#75) and 040 (#77). ⚠ **Allocate at merge** — a per-branch check is blind to cross-branch collisions, which is the rule this repo recorded after its own ADR-number collision.
**Invalidates:** none — checked. **ADR-017 is COMPLETED, not qualified:** it ordered its own mechanisms "by how little they depend on compliance… then — only if measurement supports it — an injection, whose named fallback is to have the hook PERFORM the recall and inject the results". T1 builds the measurement that condition waits on; T4 is that injection. **ADR-021 is NARROWED in one respect:** it decided the handshake carries the protocol, and T6 changes *what* it carries, not *whether*. Its `TestInstructionsStayShort` ceiling is inherited unchanged and binds T6 (F-7).
**Served-path change:** **Yes.** Every MCP client sees tool schemas at connect without a second round-trip (T3), and every installed agent's session receives content it did not ask for at two new moments (T4, T5). T1–T2 are measurement only.

## Context

See the spec's Problem and Goal. Only the decision-relevant additions:

The motivating failure was measured on an agent working **in this repository**, 2026-08-27, with the memory protocol delivered three times over — `clients/claude-code/bootstrap.md`, `AGENTS.md`, and `serverInstructions`. It published two false claims that one `am_search` would have prevented, one of them into `bootstrap.md` itself, which `go:embed`s into every install. Searched afterwards, the palace returned the correct answer as **hit 1 of 4**.

ADR-017 reached the same conclusion independently in 2026-08 from a different direction — a probe subagent — and named it precisely: *"a COMPLIANCE failure, not a delivery failure… injecting one more paragraph into a context that already holds the whole gate is the least promising thing to try."* Two independent observations of the same class, eleven months apart in corpus terms, is why this is a record rather than a postmortem.

**Nothing measures it.** `search_events` records searches; no artifact correlates a search with the assertion it should have preceded. Both observations above are anecdotes, and this record's first task exists to replace them with a count.

## Existing Primitives Audit

| Primitive | Shape | Disposition |
|---|---|---|
| The four shipped hooks (`clients/claude-code/hooks/`) and the five registered events (SessionStart, Stop, SubagentStart, SubagentStop, SessionEnd) | Bash filters over event JSON, wired by the installer into `settings.json` | **Reuse.** The delivery mechanism exists; this record adds what one *does*, and two events the installer does not yet register. |
| `agentsmemory-stats.sh:14` — `transcript_path` parsing | Already extracts the path from Stop-event JSON | **Reuse verbatim.** T1's instrument needs exactly this and nothing more. |
| `search_events` | One row per search | **Rejected as the store.** Its rows are searches, and the unit here is an assertion that may have had none — you cannot record a miss in a table whose rows are hits (spec F-17). |
| The mineclaude transcript path | Reads session transcripts to mine memories | **Reuse the reading, not the pipeline.** Note `mineclaude.go:84` drops `isSidechain` traffic by design, so a subagent's transcript is invisible to it; T1 must not inherit that filter or it measures only main sessions. |
| `serverInstructions` + `TestInstructionsStayShort` | 1200-char ceiling, justified by ADR-017's 0-recalls-in-5 measurement | **Reuse the ceiling, replace the text (T6).** Any cue replaces rather than adds. |

## Decision

Build the instrument first, then ship four mechanisms one at a time, ordered by how little each depends on the agent choosing to comply.

The instrument is a **local, append-only observation store** written by a hook at session end. One row per session: assertions seen, assertions preceded by a recall, and the classifier version. Counts and identifiers only — no transcript text leaves the machine that produced it. **No migration and no server-side schema change:** the store is a file, and the rejection of `search_events` above is on a structural ground rather than a preference.

**What would make this FAIL, and whether the data exists to produce that failure.** The criterion is the measured rate, and it can fail in two directions that must be distinguished. A mechanism that does not move the rate is recorded as *not shown to work* (F-10) — data for that exists the moment T2 runs. But the criterion is void if the classifier is wrong, and that failure is silent: a regex matching nothing reports a perfect rate. **F-4 is therefore the gate on the gate** — a fixture corpus that *contains* no-change assertions must produce matches, or the instrument fails rather than reporting 100%. The rate is valid only for the corpus and classifier version it was taken under (F-16), never in the abstract.

## Alternatives Considered

- **Write a better instruction and stop there.** REJECTED — this is the intervention ADR-017 measured as least promising, and the motivating session had three copies of the instruction in context. It survives only as T6, ranked last, and the spec's F-8 forbids counting it as a mechanism at all until the others have been measured.
- **Store the rate in `search_events`.** REJECTED on the structural ground above: a miss is not representable in a table of hits.
- **Store it in a drawer.** REJECTED — measuring recall would then pollute recall, and the observation would surface in the searches it is measuring.
- **Ask agents whether they would have recalled.** REJECTED, and named as a non-goal: ADR-017's method note records that the probe answered "likely yes", which is what the question selects for.
- **Ship all four mechanisms together.** REJECTED — the spec's F-9. Four together produce one number and four candidate explanations.
- **A `PostToolUse` audit that flags the assertion after it is written.** REJECTED — it reports the error after it has been published, which is the position this repository was already in.

## Component / Boundary Impact

`clients/claude-code` gains the observation store and two hook events; it already owns hook installation, so ownership is unchanged. `internal/mcpserver` changes only in tool registration (T3) and the instructions constant (T6) — both at existing chokepoints, no new component. No module is added or moved, so the architecture map is unchanged.

## Wiring & Contract Changes

Inherited from `docs/specs/2026-08-27-recall-before-asserting.md` §Contracts Touched; delta:

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `SessionStart` hook registration (second entry) | add | installer (T4) | every installed agent |
| `PreToolUse` hook registration | add | installer (T5) | every installed agent |
| Observation store file (path + line format) | add | T1 | T2, and any later reader of the rate |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| Observation store format (`recall observation record`) | T1 | T2, T3, T4, T5, T6 | No — additive; every mechanism reads the same rows |
| Baseline rate (`recall baseline`) | T2 | T3, T4, T5, T6 | Yes — F-3 forbids any mechanism shipping before it exists |
| Mechanism ordering (`compliance-dependence order`) | T3 | T4, T5, T6 | No — recorded once, read thereafter |

## Implementation

See `tasks/README.md`. Six tasks, sequential: the instrument, the baseline, then four mechanisms one per measurement window.

## Consequences

- **Positive:** the rate stops being an anecdote. Every mechanism after T2 carries a number, including the ones that do not work — which is the outcome that retires an idea instead of extending it.
- **Positive:** T3 removes a cost rather than adding a rule, and is the only change here that asks nothing of the agent.
- **Negative:** T4 and T5 spend context on every session to fix a minority of them. That trade is what T2's baseline exists to price, and the spec makes context cost part of the measurement rather than a footnote.
- **Negative:** the classifier is a regex over prose and will be imperfect. F-4 bounds the damage to *known* imperfect rather than *silently* perfect.
- **Neutral:** four measurement windows is slow by construction. Shipping faster means shipping unattributably.

## Out of Scope

Inherited from `docs/specs/2026-08-27-recall-before-asserting.md` §Non-Goals; delta:

- Applying the instrument to any harness other than the one whose transcripts the hooks receive (permanent: a hook can only read what its own harness hands it; other clients would need their own producer)
- Acting automatically on a low rate — refusing a turn, forcing a call (permanent: this record measures and cues; coercion is a different decision with its own failure modes)
- Backfilling the rate from historical transcripts (deferred: `docs/adr/BACKLOG.md`)

## Risks

Inherited from `docs/specs/2026-08-27-recall-before-asserting.md` §Risks; delta:

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| T3 and T6 both edit `internal/mcpserver/server.go`, whose body is pinned by `classifyToolMutationPatch` — a stored git patch in the contract-axis harness | **High** | Med | Both tasks re-cut the patch and run `go test -tags contractaxis ./internal/mcptest` on a clean tree. This exact failure shipped a red CI on 2026-08-27 from a green `go test ./...` |
| The instrument inherits `mineclaude.go:84`'s `isSidechain` filter and silently measures only main sessions | Med | High | T1's tests include a subagent transcript; the filter is not reused |

## Rollback

Additive throughout. T1's store is a file — delete it. T3 reverts to deferred registration in one constant. T4/T5 revert by removing two `settings.json` entries, which the installer already rewrites idempotently. T6 restores the previous `serverInstructions` text, whose ceiling test is unchanged. No migration, so no down-migration. Revert order: hook registrations, binary, instructions constant.

## Follow-ups

- [x] **The baseline is 7.6%** — 26/341 over 24 sessions, classifier **v3**, in `docs/adr/BACKLOG.md`.
      RE-TAKEN 2026-08-28 when the owner defined "preceded" as a recall since the last USER TURN;
      the v2 figure below is kept because it is what the earlier evidence proved, and the two are
      not comparable across counting rules (F-16). The v2 baseline was **27.6%** — 61/221 over 46
      sessions, classifier v2, precision 48%, window 2026-08-01..28. It carries its precision because at 48% roughly half
      the denominator is not the class. The classifier did NOT match nothing; the first held-out set
      that suggested it had was 3,000 characters of prose across four abandoned sessions.
- [ ] ⚠ **F-9 IS VIOLATED IN FACT AND THE RECORD SAID OTHERWISE.** This obligation read "T4 is
      shipped and needs a measurement window before T6 may ship", and T6 shipped 2026-08-28 anyway.
      The defence would be that T4 reads `blocked` — but `blocked` describes the RECORD, not the
      deployment. `installer.go` registers the recall hook on `SessionStart` unconditionally, and on
      a hosted install it fires every session; T4 reads `blocked` only because it is mute on a
      `--local` install. So T4 went live 2026-08-28 and T6 went live the same evening, both after
      the 7.6% baseline was taken, and **no delta from this window is attributable to either.**
      Raised in review, confirmed against source rather than argued.
      **Consequence, recorded rather than repaired:** this window is spent. The next clean one needs
      a fresh joint baseline taken with BOTH live — which `observed_at` now makes computable — and
      then exactly one further mechanism. Nothing is un-shipped to manufacture a window that has
      already been contaminated; F-10 records what happened, and this is what happened.
- [ ] ⚠ **AND THE GATE FOR F-9 READS THE RECORD, NOT THE WORLD.**
      `TestF9OneMechanismPerMeasurementWindow` counts README rows whose status string is `done`, so
      a mechanism that is live in the installer while recorded `blocked` is invisible to it. Its
      killed mutant — "recording two mechanisms `done` turns it red" — proves it reads the record
      and says nothing about whether the record is true. That is this repository's signature defect
      inside its own gate, and it is why the violation above was not caught by anything. Closing it
      means deriving liveness from the artifact each mechanism ships.
- [x] **F-14 was WITHDRAWN by the owner, 2026-08-28.** T3 measured deferral as a harness-wide policy
      over MCP tools as a class — a two-tool server is deferred — so the fact asserted an outcome
      this system cannot produce. Re-scoping was considered and rejected: the only non-deferred
      surface is the handshake, which is already F-11's. The fact row is gone, its binding removed,
      and the reasoning is in the spec's `## Withdrawn facts`.
- [ ] **F-12's binding stays red with T5.** A bare grep pattern is not a question: 0 of 25 subjects
      reached canary-grade distance. Reopening needs a different trigger, not a tighter bound.
- [ ] **F-2's narrowing is decided and unimplemented**, with two measured dead ends recorded. The
      next attempt starts from "prose does not token-match declarations", not from a better index.
- [ ] Decide whether the store should be readable by `doctor`, once there is a rate worth reading.
