# ADR-011: Do not prompt agents to anchor at write time

**Status:** Withdrawn
**Date:** 2026-08-20
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-008 (the end-to-end harness that made the measurement cheap), ADR-010 (supersession — the other half of "memory must be able to say it is no longer true"; **superseded by ADR-038 on 2026-08-27**)
**Invalidates:** none — checked (no accepted ADR consumes anchor coverage; ADR-010 references anchors only as an existing mechanism)

> **Withdrawn before implementation, on its own evidence.** It was proposed, designed three ways,
> judged, and then measured — and the measurement says the mechanism would do more harm than good.
> It is recorded rather than deleted because the finding is worth more than the feature would have
> been, and because a proposal this reasonable will be made again by someone who has not seen the
> numbers.

## Context

There is a rule this project is built on: *code is the state of record, memory is the rationale of record.* Memory must never be authoritative for a claim the repository can settle, because code changes and memories do not.

The palace already has the mechanism that enforces it. A memory can carry **code anchors** — verbatim source lines plus a path — and when an anchored snippet later disappears, recall marks the memory STALE and tells the reader to check the code first. What it does not have is any way to decide *which* memories need one. The protocol asks (`internal/mcpserver/drawers.go`, the `code_anchors` description: *"Anchor whenever a memory explains a specific piece of code"*), and asking is a convention.

**Measured, on 270 real drawers, four independent labellers over disjoint slices:**
**Served-path change:** None — withdrawn before implementation.

| | |
|---|---|
| makes a claim the repository could settle (Class A or mixed) | **179 (66%)** |
| of those, carrying no anchor | **165** |
| drawers with any anchor at all | **14 (5%)** |

So the standing instruction in the tool description produced **5% coverage of a 66% population**. That is the measured effectiveness of asking, and it is why the proposal was to move the ask into the *response* — where the agent reads it having just written, with the file still open.

## Existing Primitives Audit

- **`code_anchors` + `stale_hits`** — the enforcement mechanism, already built and already correct. Nothing here proposed changing it. Reuse.
- **`AnchorHint` (proposed)** — a scored token scan with suppressors, over submitted content. Not built; see Decision.
- **`clients/claude-code/verify.go`** — the anchor checker. Audited during this work and found sound; see Alternatives for the one claim against it that was refuted.
- **`internal/mcptest`** (ADR-008) — the harness that made a 270-drawer measurement a cheap thing to do rather than a project. Reused, and the reason this decision rests on numbers instead of taste.

## Decision

**Do not ship a write-time anchor prompt.** Three independent designs were produced — an improved lexical hint, a resolve-then-anchor path that proposes anchors from the working tree, and a read-time annotation — one was selected by a judge, and two adversarial passes then rejected it. The specific findings, each verified rather than argued:

**1. It would fire on 58% of writes and be wrong 61% of the time.** One reviewer implemented the selected design to spec and ran it over 135 real drawers: 76 hints on 130 eligible writes, of which **46 were false positives**. The stated death condition for the mechanism was "an unanswerable warning, three of them and the reader has learned to skip it." The largest single false-positive bucket — **27 of 76** — is exactly that.

**2. A large class of true Class-A claims cannot be anchored at all.** This is the finding worth keeping, and it was not visible before the corpus was read. An anchor proves that a line is *present*. It therefore cannot express: a claim of **absence** ("the named test is not in that file"); a **count** ("N rows across M files"); **branch or merge state**, including claims about a branch not checked out, which the checker reads from the working tree and can never confirm; a **live-system measurement** (which database a connection selected, a refused probe); or a claim about **another repository**. These decay exactly like anchorable claims and the mechanism has nothing to offer them. Prompting for an anchor on one is asking for something that does not exist.

**3. The cheapest compliant answer is the least useful one, structurally.** To satisfy a check that the snippet relates to the claim, the snippet must share a name with it. The line in a file most likely to contain a symbol's name is its **declaration** — the most stable line there is, and precisely the line a behavioural change never touches. So the path of least effort produces an anchor that will verify forever while the behaviour it supposedly pins moves underneath it. The attack path is the *compliant* path, not an adversarial one.

**4. And that failure is silent in the worst direction.** A **wrong-file** anchor is safe: the checker reports it missing, the memory goes STALE, and it self-corrects noisily. A **present-but-irrelevant** anchor reports `verified` on every recall — an affirmative "checked, and it holds" printed beside a claim nobody checked. More anchors of that kind is a corpus that is *worse* than one with none, because the reader's calibration is destroyed rather than merely absent.

Taken together: the mechanism would add noise to two writes in three, be unanswerable for a third of what it flags, and reward exactly the anchor that carries the least information — while making the resulting memory look verified. That is not a gate that needs tuning; it is a gate whose incentives point the wrong way.

**What is kept instead.** The 5%-of-66% coverage gap is real and remains open. This ADR closes one route to it and records why, so the next attempt starts from the numbers rather than from the intuition.

## Alternatives Considered

- **Resolve-then-anchor** — the write path proposes an anchor by resolving the mentioned path or symbol against the working tree, turning a guess into a fact. Rejected, and it was the most attractive of the three: its own author put the promotion step at ~0.6 precision, which is the "a fake anchor marks a memory verified" harm executed automatically and at scale. Its stated blast radius was also wrong in four checkable ways — anchors with no path are rejected by the store outright, the status vocabulary is a closed set of four constants, the checker sends no status filter so machine-proposed rows would receive real drift verdicts and manufacture STALE on live memories, and proposed anchors would be indistinguishable from hand-filed ones in search results. Each is fixable; the corrected design is a multi-file protocol change plus a client update whose central step still carries ~0.6.
- **Read-time annotation** — mark unanchored Class-A-shaped memories as unverifiable in the recall response instead of gating the write. Rejected as the base: it never captures an anchor, so the corpus only improves where it is read, and the only cheap way to clear a mark is to paste a stable-but-irrelevant snippet — the same harm arriving by a different route. One factual correction to it, verified: per-anchor `status` is already on the wire in search results, so `unchecked` is not indistinguishable from `verified`; only the summary boolean collapses.
- **Strengthen the tool description.** Rejected on the measurement that opens this ADR: the description already says it, and produced 5%.
- **The `repo` field is an unvalidated bypass** (raised as a blocking finding). **Refuted.** An anchor labelled for another repository is skipped deliberately, and the code records why: calling it MISSING would mark the memory stale, and the honest response to "the file is gone" is deletion — a session destroyed three chunks whose file lived in a sibling repository before that skip existed. A skipped anchor reports `elsewhere`, never `verified`, so it cannot falsely endorse. Unknown is not absent.

## Component / Boundary Impact

None — nothing was built. The audit touched `internal/palace/anchors.go`, `internal/mcpserver/drawers.go` and `clients/claude-code/verify.go` read-only and found each sound for its stated purpose.

## Wiring & Contract Changes

None — implementation-internal only, and no implementation.

## Inter-task Contracts

None.

## Implementation

None. Withdrawn before implementation.

## Consequences

- **Positive:** a plausible mechanism is not shipped, and the reason is recorded with numbers rather than as an opinion. The unanchorable-claim taxonomy is new knowledge that constrains any future attempt.
- **Negative:** the coverage gap stays open — 165 memories make repository-settleable claims with nothing checking them, and a reader has no signal distinguishing those from rationale.
- **Neutral:** the anchor mechanism is unchanged and remains correct for the memories that do use it.

## Out of Scope

- Any change to how anchors are checked or reported (permanent: audited during this work and found sound; the defect was in the proposed prompting, not in the mechanism)
- Classifying existing memories retroactively (deferred: docs/adr/BACKLOG.md — the labelled 270-drawer sample exists and would be the training data, but nothing consumes a classification yet, and building the classifier before its consumer is how the unreachable-capability defect starts)
- Making `verified` mean less when only a declaration line matched (deferred: docs/adr/BACKLOG.md — a real finding from this work, and the one piece worth building if the coverage gap is attacked again)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The coverage gap is read as acceptable because this ADR closed a route to it | Med | Med | Consequences states it plainly as still open, with the number |
| Someone re-proposes write-time prompting without seeing this | High | Low | Precisely why a withdrawn ADR is recorded rather than deleted; the backlog entry points here |
| The measurement is corpus-specific and a different palace would behave differently | Med | Med | Likely true and stated: 270 drawers, one project's memories, four labellers. The unanchorable-claim taxonomy is the part most likely to generalise, since it follows from what an anchor can express rather than from this corpus |

## Rollback

None required — nothing shipped.

## Follow-ups
