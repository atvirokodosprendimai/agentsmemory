# ADR-028: Return the identifier and the score a recall was decided by

**Status:** Accepted
**Date:** 2026-08-25
**Owner:** Zy
**Spec:** None — no spec stage; grounded in a source audit and a live span tree taken 2026-08-25.
**Cross-references:** `internal/palace/service.go` (`Search`, `BlendRerank`), `internal/palace/recallstats.go` (`searchEventRow`), `internal/mcpserver/drawers.go` (the `am_search` hit shape and `am_get_drawer`), `docs/adr/ADR-024-rank-memories-not-chunks.md` (memory is the ranking unit), `docs/adr/ADR-018-a-recall-belongs-to-the-session-that-ran-it.md` (T2 withdrawn: the transport stays stateless), `docs/adr/BACKLOG.md` (§"The product is a runtime quality control plane", primitives 1–3)
**Invalidates:** none — checked. ADR-018 stays intact: this ADR identifies a RECALL, never a session, so the transport remains stateless and T2's withdrawal is untouched. ADR-024 is unaffected: the ranking unit does not change and no score is recomputed.
**Served-path change:** `am_search` returns a `search_id` for the page and a `blended_score` on every hit, and `am_get_drawer` accepts an optional `search_id` — so an agent can say which recall sent it to a memory, and can see the number that actually decided the order it was shown.

## Context

Two facts, both established by reading source on 2026-08-25 against `8c3167d`, both invisible from the tool surface.

**The identifier exists and does not leave the server.** `Search` mints `searchID := randomID()`, puts it on the context (`telemetry.WithSearchID`), and every stage span carries it — a live trace shows `search_id=4710aada0291c7d44f0777bd` on all nine children of one `am.search`. It is also the PRIMARY KEY of the durable row: `ev := searchEventRow{ID: searchID, …}` (`service.go`, record stage). So a returned id already joins a page to its `search_events` row with no migration at all. The string `search_id` appears nowhere in `internal/mcpserver`: never returned, never accepted.

**The score that decides the order is not the score that is shown.** `BlendRerank` (`service.go:1316`) computes `Blended = weight*rerankNorm + (1-weight)*fusedNorm` over pool-normalised inputs and sorts on it (`service.go:1340`). The response exposes `rerank_score` only (`drawers.go:472`). A page whose order is not monotonic in `rerank_score` is therefore CORRECT and unexplainable at the same time — reported as a suspected bug on 2026-08-25 from a live comparison, and diagnosed only by reading `BlendRerank`. Measured on that run: with query context the logits spread 0.480 → 0.178 and the two orders coincide; without it they sit within 0.07 of each other (0.273, 0.108, 0.041, 0.042, 0.064) and `fusedNorm` decides, which is the blend working as ADR-024 argues it should.

Both are rung-3 defects in this repository's own vocabulary: the capability exists, works, is covered by passing behavioural tests, and the caller cannot discover it. No behavioural test can see either, because a test that already knows the field passes.

**Debt this ADR consumes.** `BACKLOG.md` §"The product is a runtime quality control plane" names three primitives in dependency order. Since it was written, the OpenTelemetry work (#52, merged as `26f6531`) delivered **#2 stage outcomes** in full — 25 stages reporting `ran|bypassed|failed_open|failed_closed` with 15 reasons — and **#1 profile identity** on the span (`am.profile_id`) though not yet on the durable row. **#3 implicit relevance feedback** is this ADR, and it is the one BACKLOG argues scales: *"an agent fetching a memory in full after a search is a click… it is the only source of relevance judgement that grows with usage instead of with our labelling budget."* The backlog entry is rewritten in the same commit to point here rather than describing unstarted work.

## Existing Primitives Audit

| Primitive | State | This ADR |
|-----------|-------|----------|
| `palace.Service.Search` mints `searchID` | exists, used by spans and the durable row | reuse — no new identifier, no new lifetime |
| `searchEventRow.ID` | already IS the search id (primary key) | reuse — no migration for T1/T2 |
| `HybridScore.Blended` | computed and sorted on, never surfaced | reuse — expose the existing field |
| `drawers.go` hit shape | already carries `score`, `bm25_score`, `distance`, `rerank_score`, `content_coverage` | reshape — one field added beside them |
| `telemetry.SearchIDFrom` | reads the id off the context | reuse for T3 when it lands |

No new component. No new store.

## Decision

`am_search` returns the page's `search_id` and adds `blended_score` to every hit. `am_get_drawer` accepts an optional `search_id` argument, documented as "the recall that sent you here". Both tool schemas advertise the fields, because a schema the caller reads is the only thing that makes an argument reachable.

Nothing about ranking, storage or scoring changes. `blended_score` is the value already sorted on; `search_id` is the value already stored. This ADR moves two existing numbers across the tool boundary and adds one optional input.

**What this does NOT yet do, and the trigger that starts it.** Accepting `search_id` on `am_get_drawer` without recording the join produces a capability with no consumer — the same half-loop asymmetry this repository keeps shipping, and the reason T3 is written rather than assumed. T3 records the fetch against the recall and reports the ratio. Its trigger is explicit: **the first week in which `am_get_drawer` receives a non-empty `search_id` from any client other than a test.** Until an id actually arrives there is nothing to record and no ratio to report, and building the report first would be measuring an empty set.

**Falsifiability of the T3 criterion, stated now because it is a measurement.** If no client ever sends the id, T3 must not ship a report of zeros presented as a finding; it should report that the loop is unused, which is itself the answer. Data that could produce that failure exists trivially — it is the current state.

## Alternatives Considered

- **Return the whole `HybridScore` (fused, rerankNorm, fusedNorm, blended):** rejected. Four numbers where one decides the order; `rerankNorm`/`fusedNorm` are pool-relative and meaningless to a caller who cannot see the pool. `blended_score` plus the existing `rerank_score` is the smallest pair that explains an ordering.
- **Log the blend server-side and leave the response unchanged:** rejected. It is already logged — the span carries the stage and the weight. The party that cannot explain the order is the AGENT reading the page, and a server-side log does not reach it. This is what makes it a rung-3 defect rather than an observability gap.
- **Identify the SESSION rather than the recall:** rejected, and this one is settled rather than argued — ADR-018 T2 was withdrawn on 2026-08-22 by the project owner because the transport stays stateless, so the server mints no session identity. A recall id needs none: it identifies one request, which the server already does.
- **Ship T3 in the same ADR:** rejected for sequencing, not for value. T1/T2 are additive surface changes provable by a schema check; T3 changes what is persisted and needs a decision on whether the fetch updates the recall's row or gets its own. Deferring it keeps the first change small and reversible — but it is written as a named task with a trigger, because a silently omitted second half is how the write-side/read-side asymmetry got here.

## Component / Boundary Impact

Inherited from `docs/architecture.md` §Module Map; delta:

| Module | Change | One reason to change — still true? |
|--------|--------|-----------------------------------|
| `internal/mcpserver` | adds two response fields and one optional tool argument | yes — the agent-facing contract changed, which is this module's reason |
| `internal/palace` | none for T1/T2; `Blended` and `searchID` already exist | yes — untouched |

No module gains a second reason to change. No new bounded context: `search_id` names a recall inside the palace's own request, and crosses no ownership line.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_search` response | adds `search_id` (page-level) and `blended_score` (per hit) | `internal/mcpserver/drawers.go` | any MCP client; `adr-verify` schema check |
| `am_get_drawer` schema | adds optional `search_id` string argument | `internal/mcpserver/drawers.go` | any MCP client |
| `am_get_drawer` handler | reads the argument; T1 validates and ignores, T3 records it | `internal/mcpserver/drawers.go` | `search_events` (T3 only) |
| `search_events` | none in T1/T2 — `ID` already holds the search id | — | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `search_id` on the `am_search` response and `am_get_drawer` schema | T1 | none in this ADR — the deferred recording task consumes it, see Out of Scope | No — additive; absent fields were previously absent |
| `blended_score` on each hit | T2 | none | No — additive |

T1 and T2 touch the same file and are independent in content; T1 is ordered first because it is the half T3 depends on.

## Implementation

See `tasks/README.md`. Two executable tasks; T3 is specified in Out of Scope with its trigger.

## Consequences

- **Positive:** an agent can explain why a page was ordered as it was, without reading `BlendRerank`.
- **Positive:** a recall becomes referenceable, which is the precondition for measuring whether recall is USED — the rung-4 question this repository has never been able to answer.
- **Positive:** no migration, no ranking change, no new store. The two numbers already exist.
- **Negative:** two more fields in an already wide hit shape. Mitigated by rejecting the four-number alternative.
- **Negative:** until T3, `am_get_drawer` accepts an argument it does not act on. Deliberate and named, not silent — see the trigger.
- **Neutral:** `blended_score` is pool-relative, so it is comparable WITHIN a page and not across pages. The field description must say so, or it will be averaged by someone.

## Out of Scope

- ~~**T3 — record the fetch against the recall and report the ratio**~~ — **no longer deferred; split into T3 (done 2026-08-29) and T4 (pending) in this record's `tasks/`.** ⚠ Its trigger fired and could not be observed: a non-test client sent a `search_id` on 2026-08-29 and nothing recorded it, because the id reached only a sampled span. No first-party client calls `am_get_drawer` at all, so the trigger was never waiting on wiring — it was conditioned on an agent choosing to pass an optional argument, and its satisfaction left no durable trace. A trigger that cannot be observed cannot start the task it gates, which is the lesson rather than the deferral being wrong to write (permanent: superseded by the task files, kept visible because the trigger's failure mode is the finding)
- **`profile_id` on the durable `search_events` row** (deferred: ADR-028 T4, in this record's `tasks/` — BACKLOG primitive 1 is on the span already; putting it on the row is a migration, and it belongs with the RATIO rather than with the recording, since raw counts are interpretable without it and a rate is not)
- **Any change to ranking, scoring or the retrieval unit** (permanent: this ADR moves existing numbers across the tool boundary and changes no decision; ADR-024 owns the unit and a future ADR owns page diversity)
- **A relevance metric derived from the fetch signal** (deferred: `docs/adr/BACKLOG.md` §"From ADR-028" — the signal must exist and be observed before anything is derived from it)
- **Identifying the session or the agent** (permanent: ADR-018 T2 was withdrawn by the project owner on 2026-08-22 and the transport stays stateless; a recall id is per-request and needs no session)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| `blended_score` is read as cross-page comparable | Med | Med | the schema description states it is pool-relative; T2's test asserts the description says so |
| An agent sends a stale or fabricated `search_id` | Med | Low | T1 validates shape and ignores unknown ids; nothing is trusted from it until T3, which must join before recording |
| The added fields push an already large hit shape past a client's tolerance | Low | Low | two scalars; `snippet_chars` remains the dominant term in response size |
| T3 never triggers and the accepted argument stays inert | Med | Med | the trigger is written here and in BACKLOG; if a year passes with no id arriving, the honest outcome is to REMOVE the argument, and that is a legitimate result |

## Rollback

Remove the two response fields and the optional argument. No persistent state is written by T1/T2 and no schema migration is applied, so rollback is a code revert with no data step. Clients that had begun sending `search_id` degrade to sending an argument the server ignores, which is the pre-ADR behaviour.

## Follow-ups

- [ ] When T3 lands, report the first measured recall-followed-by-fetch ratio in `BACKLOG.md` whichever way it falls — including "no client ever sent one", which is the outcome that would retire the argument rather than extend it.
