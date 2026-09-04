# ADR-051: The session that grounds itself

**Status:** Accepted
**Date:** 2026-09-03
**Owner:** M
**Spec:** None — no spec stage. ⚠ Stated rather than left blank: ADR-041, the record this one completes, HAS a spec, and its facts (F-1…F-17) are what make its measurements comparable across sessions. This record ships no new measurement of recall RATE — it opens channels and corrects a false table, and every task's claim is settled by an exit code rather than by a rate. The one task that would need a rate (T3) is deliberately scoped to observation only, and the spec that would govern a rate is named as the prerequisite for any future task that tries to move one.
**Cross-references:** `docs/adr/ADR-041-the-recall-that-does-not-depend-on-remembering.md` (the prior art this completes — its T3/T4/T5 are STOPPED and this record does not restart them), `docs/adr/ADR-017-a-subagent-is-a-session.md` (measured that added prose is the weakest intervention), `docs/adr/ADR-050-a-memory-has-an-address.md` (the capability T5 makes discoverable), `docs/adr/ADR-021-the-handshake-carries-the-protocol.md`, `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md` (anchors and opaque ids, which T2 reads)
**Enforced-by:** `clients/claude-code/hookchannelknown_test.go::TestTheInjectingSetIsTheDocumentedFour`, `clients/claude-code/plugin_test.go::TestClaudeCodeActuallyLoadsThePlugin`, `clients/claude-code/plugin_test.go::TestEveryRegisteredPluginHookIsExecutable`, `clients/claude-code/plugin_test.go::TestADeniedActionIsActuallyRefused`
**Governs:** `clients/claude-code/hookchannel.go`, `clients/claude-code/hooks/**`, `clients/claude-code/installer.go`, `clients/claude-code/assets.go`, `clients/claude-code/agentkit.go`, `internal/mcpserver/resources.go`, `internal/palace/anchors.go`

**Numbering:** ADR-051. Verified 2026-09-03: the tree holds up to ADR-050 (merged as `799769d`) and the repository has **zero open pull requests**, so no branch can be claiming 051. ⚠ Allocate at merge — a per-branch check is blind to cross-branch collisions, which is the rule this repo recorded after its own ADR-number collision.

**Served-path change:** the installed Claude Code kit gains a corrected hook-channel table, a `PreToolUse` anchor cue, a `PostToolUse` touched-path recorder, a `UserPromptExpansion` injector, a status line, a native skill and a plugin manifest; the MCP server gains a bounded `resources/list`.

## Context

⚠ **This record's own `Enforced-by` header was false for a day, in both clauses.**
It read `None — every task is pending and this record ships no code` and promised
that T1's commit would fill it in. All nine tasks landed, 2,713 lines shipped, and
the header still said the record enforced nothing — while the gate it named,
`TestTheInjectingSetIsTheDocumentedFour`, existed the whole time. Caught by review
on 2026-09-04, which is exactly the rot this header exists to prevent, happening to
the header itself. The four gates above are named because each one FAILS when a
different half of this record is deleted; that is the only property that makes the
line worth reading.


An audit of Claude Code's extension surface against what this project actually installs, taken 2026-09-03 against the documentation and against the running local stack, found that **we use six of thirty-four hook events and two of twelve settings keys**, and that one of the six is registered on a table that is wrong.

Three findings drive this record. Each was verified rather than inferred, and they are listed in the order that matters: a correction, a discovery, and a gap.

**1. Our own channel table is false, and it fails in the expensive direction.**
`clients/claude-code/hookchannel.go` names three events whose plain stdout reaches the model and files `UserPromptExpansion` under the debug log, carrying a ⚠ comment asserting the documentation *moved it there* on 2026-09-03. The hooks reference, read the same day, says the opposite in as many words:

> "For most events, Claude Code writes stdout to the debug log and doesn't show it in the transcript. The exceptions are `UserPromptSubmit`, `UserPromptExpansion`, `SessionStart`, and `PostModelSwitch`, where Claude Code adds plain-text stdout as context that Claude can see and act on."

That file was written *because* the list had already gone stale once, and it says so at length. It went stale again anyway, in the same week, in the same field. What is worth recording is not the staleness but its direction: `doctor` labels a working `UserPromptExpansion` hook `DISCARDED` and exits non-zero, and `TestEveryInjectingHookIsOnAnInjectingEvent` would reject an install plan that registered one. **The table does not merely fail to describe a capability — it forbids it.** That is §Reachability inverted: not a feature nothing selects, but a gate refusing a feature that works.

**2. A structured hook reaches the model on events whose stdout is discarded — measured, with a falsifier.**
The injecting/discarded split governs *plain stdout only*. The reference states `additionalContext` support for `PreToolUse` ("`permissionDecision`, `updatedInput` and `additionalContext` — add context Claude sees"), `PostToolUse` (`updatedResult` or `additionalContext`) and `PostToolUseFailure`. So the twenty-seven unused events are mostly **not closed doors**.

`SubagentStart` is undocumented on this point and we already ship a hook that depends on it, so it was measured rather than assumed. **The first attempt was not good enough and is recorded because its weakness is instructive:** a subagent was asked to confirm receipt and to quote the text verbatim. It reported receipt and never quoted. That leaves two readings — it saw the text, or it gave an agreeable answer to a pointed question — and a refusal to quote is ambiguous between them.

The probe that settles it makes the payload ANSWER A QUESTION, so there is no agreeable middle. The hook emits `The access code is <16 random hex>`, unguessable and unique per spawn, and appends what it actually emitted to a local file — the ground truth for that spawn rather than for what the author believes was sent. The subagent's entire task: *"Report the access code. If no access code is present in your context, reply with exactly: NONE."* A model that saw nothing has exactly one available answer.

| arm | hook | subagent answered |
|---|---|---|
| A | live | **the nonce** — a 16-character assistant block matching the hook's own emission log |
| B | severed to `exit 0` | **`NONE`**, and zero occurrences of the nonce anywhere in the transcript |

**Arm B is the half that makes arm A worth anything.** Without it, a probe cannot distinguish the channel under test from any other route the harness might use to deliver text, and this record would be asserting a mechanism on a measurement with no negative arm — the vacuous-assertion class this repository already gates against. The installed hook was restored from a pre-probe backup and verified byte-identical afterwards.

⚠ **What this proves and does not.** The payload reaches the model's context. It does not show the model would act on it unprompted, which is a different and harder claim, and is exactly the claim ADR-041's instrument exists to measure. This record does not make it.

**3. ADR-050 shipped an address the primary client cannot discover.**
The MCP page enumerates the discovery calls Claude Code makes — "`tools/list`, `prompts/list`, and `resources/list`" — and `resources/templates/list` is not among them. Resource templates are not mentioned anywhere in Claude Code's documentation. ADR-050 deliberately serves an empty `resources/list`, for a good reason that assumed a client which reads templates. Measured against the running server: capabilities advertise `resources`, `resources/list` returns `[]`, the template resolves, and Claude Code's own `ListMcpResourcesTool` answers **"No resources found."**

### What this record is NOT, and the prior art that decides it

ADR-041 built four mechanisms for making recall happen without the agent choosing to comply, ranked by compliance-dependence. **Three of them are STOPPED, and this record does not restart any of them.** T5 is the one that matters here, because a careless reading of finding 2 would re-propose it:

> "at PreToolUse time the only query available is a bare grep pattern, and 0 of 25 such patterns reached canary-grade relevance against the live palace"

| query kind | top-hit distance (2026-08-28, live palace) |
|---|---|
| a real question (canary) | 0.317 – 0.444 |
| the 25 bare identifiers a cue would fire on | 0.408 – 0.567, median **0.486** |
| canary-grade (< 0.42) | **0 of 25** |

⚠ **THAT TABLE IS STALE AND MUST NOT BE RE-CITED. Re-measured 2026-09-03 against the live
palace, and the threshold it rests on no longer describes a good query.**

| query kind | 2026-08-28 | 2026-09-03 (re-run, n=25 / n=5) |
|---|---|---|
| a real question (canary) | 0.317 – 0.444 | **0.392 – 0.478**, median 0.466 |
| the bare identifiers a cue would fire on | median 0.486 | median **0.475** |
| under 0.42 | 0 of 25 | 3 of 25 |

The identifiers barely moved. **The canary band moved, and got worse** — only one of five real
questions now clears 0.42, so a frozen threshold taken from the old band would today disqualify
the very queries it was drawn to represent. Canary median 0.466 against identifier median 0.475
is a gap of 0.009: **distance no longer separates a real question from a bare symbol at all**,
and any argument resting on that separation is resting on an instrument that has drifted. This
is the frozen-number defect §Reachability records against this corpus, reproduced in a decision
record rather than in prose.

**The conclusion survives, on a measure that still discriminates.** What the two populations do
not share is where they land:

| query kind | top hit in a curated room | top hit in a narrative or scratch room |
|---|---|---|
| a real question (canary) | **100%** (5 of 5) | 0% |
| a bare identifier | 44% | **56%** — 14 of 25 top out in `diary`, `sessions`, `stress2` or `inbox` |

**Method, because a measurement whose method is not stated is a number to be re-derived.** Taken
2026-09-03 against the running local stack — Docker 29.7.2, compose 5.5.0, container rebuilt and
restarted from `main` at `799769d` and confirmed by `scripts/redeploy.sh` to carry this commit's
strings in the *serving* binary. Queries went through the `am_search` MCP tool: n=5 canary
questions, n=25 bare identifiers taken from symbols an agent would actually grep for in this
tree. Distances are the `distance` field of the top hit; rooms are its `room`. Two probes were
re-run through the deferred MCP tool rather than the transport to check the path did not change
the answer — `"why is the drawer id opaque…"` returned 0.392/`gotchas` and `SanitizeName`
returned 0.571/`diary` by both routes, identical to three decimal places.

T5 died on **query quality**, and that conclusion stands on the re-run — a bare identifier retrieves a session's narrative more often than a team's decision, which is exactly the failure T5 named, now evidenced by room rather than by a distance threshold that has stopped working. T2 of this record is a different mechanism reaching the same event, and the distinction is the whole of its justification: **it issues no query.** A code anchor is an exact pin — `internal/palace/anchors.go` stores `Repo`, `Path`, `Snippet`, `Status` and the `DrawerID` it belongs to — so the lookup is a join on a path the tool call already names, not a semantic search for a subject nobody stated. There is no distance to fall short of, because nothing is ranked. An anchor either pins the file being opened or it does not.

Likewise ADR-041 rejected "a `PostToolUse` audit that flags the assertion after it is written… it reports the error after it has been published." That rejection stands and T3 does not touch it: T3 records *which paths this session edited* and asserts nothing about their content, so there is no verdict to deliver late.

## Existing Primitives Audit

Almost every piece exists and is not wired.

- `palace.ListAnchors` with `AnchorFilter{Wing, Repo, Status, Limit}` already answers "which memories pin code in this repo", and `doctor --corpus` already reports drift per anchor. **`AnchorFilter` has no `Path` field** — that is the one addition T2 needs, and it is a `WHERE` clause, not a mechanism.
- `hookchannel.go` already distinguishes three answers (`channelInjected`, `channelDebugLog`, `channelUnknown`) and already refuses to report an unknown as a "no". T1 changes two entries of a data table; the logic and its gates are correct as they stand.
- The `hook-output:` declaration, `TestEveryHookScriptDeclaresItsOutputChannel` and `TestANonInjectedChannelIsJustified` already govern every script in `hooks/`, so new hooks join the existing gates rather than needing new ones.
- `agentsmemory-subagent-start-hook.sh` is a working exemplar of a structured `additionalContext` envelope, including the `esc()` JSON-safety it took a bug to learn.
- The kit already writes `settings.json` and already owns `doctor`; `statusLine` (T7) is one more key in a file it edits today.
- `am_list_skills` / `am_load_skill` already serve centralised skills. T8 ships a native `SKILL.md` that *calls* them; it does not fork the catalogue.
- ADR-050's `drawerURI` and `parseDrawerURI` already render and parse addresses. T5 adds a bounded listing over rows that already exist.

## Decision

**Take every reachable surface, ordered by how little each depends on the agent choosing to comply — the ordering ADR-041 froze in F-13 — and correct the table that is currently forbidding one of them.**

Nine tasks. **Waves are dependency levels — nothing more** — because that is the only ordering a
tool can check; the thematic grouping below is a reading aid and is deliberately not the wave
table.

| Track | Tasks | What it is for |
|---|---|---|
| Correct | T1 | Nothing that registers a new hook may ship while the table validating registrations is false |
| Open the channels | T2, T3, T4 | The routes that need no agent compliance |
| Make it reachable | T5, T6, T7, T8 | Capabilities we already built that nothing can find |
| Close the loop | T9 | What runs alone, and what still gates |

1. **Correct before extending (T1).** `UserPromptExpansion` moves to the injecting set, and the
   ⚠ paragraph teaching a maintainer the opposite of the truth is replaced rather than deleted.

   ⚠ **Amended 2026-09-04 while executing T1: this record twice claimed `PreModelSwitch` was in
   NEITHER map and would therefore answer `channelUnknown`. It was already in `debugLogEvents`,
   and adding it produced a duplicate-key build failure.** The claim came from arithmetic —
   3 injecting + 30 debug-log read as 33 against a documented set believed to be 34 — and the
   missing event was inferred rather than looked up. The real membership, counted from source
   after T1: **4 injecting, 29 debug-log, 33 named, no overlap.** The correction is left visible
   because the mistake is the one this record is about: a number derived from a table, trusted
   over the table itself.

2. **Open the channels that need no compliance (T2, T3, T4).** A `PreToolUse` anchor cue keyed
   on the path (T2); a `PostToolUse` recorder of touched paths (T3); a `UserPromptExpansion`
   injector, legal only after T1 (T4).

3. **Make what we already built reachable (T5, T6, T7, T8).** A bounded `resources/list` so
   ADR-050's addresses are discoverable (T5); a plugin manifest so the kit installs as one unit
   (T6); a status line (T7); a native skill (T8).

4. **Close the loop (T9).** Permission rules, headless invocation, and the persist gate that
   makes a session nobody watches still leave the palace better than it found it.

**The governing principle, and the reason "0 human intervention" is coherent rather than reckless: where a session would stop to ask a human, it consults the palace instead.** That is the whole thesis of this project turned on its own operation. A question about why the code is shaped this way, what was tried, what a previous session got wrong, which wing to write to — every one of those has an answer already recorded, and stopping to ask a human for it is the failure mode, not the safeguard.

⚠ **The stated exception, flagged rather than designed away.** Consulting memory replaces asking about *recall*. It does not replace consent for actions that are irreversible or leave the machine — a force-push, a release, a destructive migration, anything published outward. Those still gate, because the palace records what was decided and cannot consent on a human's behalf to something nobody has decided yet. T9 draws that line explicitly in `permissions` rules rather than leaving it to an agent's judgement mid-run. If the owner wants that line moved, it moves in one file with one review, which is the point of writing it down.

## Alternatives Considered

- **MCP elicitation for the persist decision.** REJECTED for PERSISTENCE, and it is the alternative this record most obviously invites — the client supports it, it is documented, and a server-initiated dialog is the natural way to ask "shall I file this?". It is rejected on the goal: a session that must stop and ask before persisting loses the work whenever nobody is watching, which is the failure this record exists to end.

  ⚠ **Amended 2026-09-04, on the owner's correction: "human elicitation sometimes is needed, but not the most of the turns."** That is right and the first draft overstated the rejection. The rejection is of elicitation as the DEFAULT — as the thing standing between a session and its own memory, on every turn. For the minority of turns where a human genuinely must decide, elicitation is the better primitive than what T9 ships: the deny rules make those turns STOP, and a stop is a refusal with no way to answer it. Elicitation would let the server ASK. It is a real follow-up, not a rejected alternative, and the distinction is the frequency rather than the mechanism.
- **MCP sampling to let the server curate memories itself.** REJECTED for now: Claude Code's documentation does not mention sampling anywhere, so the client support is unknown, and a mechanism whose reachability cannot be established is the defect this repository keeps shipping. Named in Follow-ups with the measurement that would settle it.
- **Restart ADR-041 T5 now that `additionalContext` is understood.** REJECTED — T5's blocker was never the channel. 0 of 25 bare identifiers reached canary-grade relevance, and understanding the envelope does not improve the query. T2 reaches the same event by a route that issues no query at all.
- **Fix the channel table by fetching the documentation in a test.** REJECTED — a gate that makes a network call fails when the network does, and it turns an upstream edit into a red build on an unrelated branch. The table stays data with a recorded retrieval date; `doctor` is where an operator learns it is old.
- **Enumerate every drawer in `resources/list`.** REJECTED, again, for ADR-050's original reason: thousands of entries with no relevance order is a worse answer than the search that exists. T5 bounds the listing instead, which keeps that argument intact and still gives the capability a door.
- **Keep the bespoke installer and fix its bugs one at a time.** REJECTED — three of the repository's open issues (#197, #198, #199) are installer defects on the non-Claude-Code kits, and the installer exists to hand-write registrations that a plugin manifest declares. T6 deletes the category rather than the instances.
- **Ship all nine tasks together.** REJECTED on ADR-041's F-9 ground, restated: nine mechanisms at once produce one outcome and nine candidate explanations. The waves above are the smallest grouping that keeps each task's evidence attributable.

## Component / Boundary Impact

`clients/claude-code` gains hook scripts, a plugin manifest, a skill and a status-line command; it already owns hook installation and `settings.json`, so ownership is unchanged. `internal/palace` gains one filter field. `internal/mcpserver` changes only inside `resources.go`. **No module is added or moved, so the architecture map is unchanged** and `arch-lint` has nothing new to cover.

## Wiring & Contract Changes

`injectingEvents` and `debugLogEvents` change membership (T1) — read by `doctor` and by two gates. `AnchorFilter` gains `Path`, an additive field with a zero value that preserves every existing call site (T2). Three hook events are registered for the first time — `PreToolUse`, `PostToolUse`, `UserPromptExpansion` — each with a `hook-output:` declaration so the existing gates classify them (T2, T3, T4). `resources/list` starts returning rows where it returned `[]`, which is additive for any client and is what makes the capability discoverable (T5). `settings.json` gains `statusLine` (T7). The kit gains `.claude-plugin/plugin.json` and a marketplace entry (T6); the existing installer path stays until T6's own acceptance proves the plugin path installs the same set.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `a channel table that matches the documented four` | T1 | T4 | No — a data change. But T4 cannot pass the install gate until it lands, which is a hard edge rather than a preference |
| `path-keyed anchor lookup` | T2 | T3 | No — `AnchorFilter.Path` is additive and its zero value preserves every existing call site |
| `one installable unit` | T6 | T7, T8, T9 | No — the plugin path ships beside the installer path until its own acceptance proves the two register the same set |
| `a bounded resource listing` | T5 | — | No — `resources/list` returning rows where it returned `[]` is additive for any client |

## Implementation

Nine tasks, T1–T9. See `docs/adr/ADR-051-the-session-that-grounds-itself/tasks/`.

## Consequences

A session that opens a file learns what the team decided about that file without asking, and without the agent choosing to search. A session whose work is never seen by a human still records what it touched. The kit installs as one versioned unit instead of a script that edits a config file and a `doctor` command to check its own edit. And the table that decides which hooks may exist stops forbidding one that works.

What this does **not** do is move a measured recall rate. ADR-041's instrument exists (`clients/claude-code/recallrate.go`) and its baseline is recorded; this record opens channels and is honest that opening a channel is not evidence anyone walked through it. Any future task claiming a rate improvement inherits ADR-041's spec and its F-4 gate on the gate — a classifier that matches nothing reports a perfect rate.

## Out of Scope

- Restarting ADR-041's T3, T4 or T5. (permanent: boundary: re-measured 2026-09-03 over the MCP tool against the container built from main 799769d, a bare identifier tops out in a narrative or scratch room 56% of the time against 0% for a real question — so it remains a disqualifying query for a semantic cue, and the evidence is this record's own Context section under the heading "What this record is NOT, and the prior art that decides it". ⚠ Stated as a boundary with its reason rather than as a fact with a `file:line` receipt: the typed-receipt form wants a line number, and the only file worth pointing at is the one task file certain to gain a Proof map header and Verification Log entries above the paragraph in question. A citation that drifts is the defect `TestNoDocCitesItsOwnLineNumbers` exists to prevent, and satisfying a format by minting a pointer that will rot is worse than declining the format)
- A `PostToolUse` audit that judges what the agent wrote. (permanent: boundary: ADR-041 rejected it on the ground that it reports the error after it has been published, and this record does not reopen a settled rejection)
- MCP sampling. (deferred: `docs/adr/BACKLOG.md`)
- MCP elicitation for anything but irreversible actions. (deferred: Follow-ups below)
- The hosted OAuth path and the non-Claude-Code kits' own hook surfaces. (permanent: boundary: codex, pi and cursor have different extension models, and this record's findings are all measured against Claude Code)

## Risks

**A hook on `PreToolUse` runs on the hot path of every tool call.** T2's lookup must be local and bounded or it taxes every Read in the session. The anchor table is small and path-keyed; the task's Stop Condition is a measured per-call budget, and exceeding it stops the task rather than shipping a tax.

**A cue that fires constantly is noise, and noise is worse than silence** — it trains a reader to skip the channel. T2 fires only when an anchor for that exact path exists, which is a far narrower trigger than T5's subject-keyed cue that fired on 3.4% of turns.

**Three of nine tasks depend on undocumented client behaviour.** T5's premise is that Claude Code reads `resources/list` and not templates — measured, but from one client version. T2 and T3 depend on `additionalContext` on `PreToolUse`/`PostToolUse`, which is documented. Each task carries a Stop Condition naming the observation that would disqualify it.

**A plugin migration can leave two installs registered at once.** T6's acceptance requires `doctor` to report exactly one registration per event after migration, because a duplicated hook is silent and doubles every injection.

## Rollback

Per task; each is independently revertible. T1 is a data change with no wire effect. T2, T3, T4 and T7 are hook or settings registrations removed by uninstalling them. T5 reverts to an empty listing. T6 keeps the existing installer path until its own acceptance passes, so rollback is "do not enable the plugin". T8 and T9 add files nothing else reads.

## Follow-ups

- [ ] Measure whether `ReadMcpResourceTool` accepts a template-matching URI in a session that connected *after* the server advertised resources — the observation that decides whether T5 is a discoverability fix or a reachability fix. (deferred: T5's Stop Condition)
- [ ] Elicitation for irreversible actions only, once T9's permission rules have named which actions those are. (deferred: `docs/adr/BACKLOG.md`)
- [ ] Whether Claude Code supports MCP sampling at all — the answer decides whether server-side memory curation is reachable or is a capability we would advertise and never deliver. (deferred: `docs/adr/BACKLOG.md`)
- [x] **Answered 2026-09-03, positively and with a falsifier.** `SubagentStart` `additionalContext` reaches the subagent's context: a per-spawn unguessable nonce was reported back with the hook live, and `NONE` with the hook severed. The discriminator was designed by an independent session after this record flagged the first attempt as ungraded; the design's own condition was that a probe without a negative arm proves nothing, and that condition is what made the result usable. See Context finding 2.
- [ ] **Retrieval may have regressed, and this record found it by accident rather than by looking.** Canary questions measured 0.317–0.444 on 2026-08-28 and 0.392–0.478 on 2026-09-03 against the same kind of question. Whether that is corpus growth, the move to RRF fusion, the reranker, or a real regression is unknown and is not this record's business — but a canary band that drifts silently invalidates every frozen threshold in the corpus, including the `max_distance=0.42` floor the task-recall hook ships with. Needs its own record. (deferred: `docs/adr/BACKLOG.md`)
- [ ] `listChanged`: the client supports live capability updates and this server cannot send them, because the transport is mounted `WithStateLess` and keeps no session to push down. That is why a session which connected before a redeploy still believes the server has no resources. Whether it is worth a stateful transport is a decision, not a task. (deferred: `docs/adr/BACKLOG.md`)
