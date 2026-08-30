# ADR-036: Put the knowledge graph on the read path

**Status:** Accepted
**Date:** 2026-08-26
**Owner:** Zy
**Spec:** `docs/specs/2026-08-26-a-recall-that-answers.md`
**Cross-references:** `docs/adr/ADR-001-recall-answers-or-abstains.md`, `docs/adr/ADR-004-supersession-not-recall.md`, `docs/adr/ADR-016-a-memory-an-agent-files-must-be-navigable.md`, `docs/adr/ADR-031-the-column-abstention-would-calibrate-on.md`, `docs/architecture.md`
**Invalidates:** **ADR-004's categorical wiring bar, narrowed by owner sign-off 2026-08-26 — see
§"The ADR-004 condition" below.** This ADR crosses a condition ADR-004 set; the conflict was surfaced
in review, and M resolved it (*"P1 - i agree with change"*): ADR-004 is amended to its narrower
reading in the same commit, and its measurement (#34) stands on its own merits. The claim previously
made here ("none — checked") was wrong and is corrected rather than quietly amended, because the whole
point of the header is that somebody read it.
Otherwise:  ADR-001 (abstention) is Accepted with all six tasks pending and is a NON-GOAL here, deliberately not re-decided. ADR-016 is Accepted and executed; F-4 depends on it rather than changing it. ADR-031's calibration aggregate reads `reranked`, which this ADR does not touch. ADR-034 (open PR #61) adds `rerank_skip_reason`; this ADR takes migration `00028` to avoid its `00027`.
**Served-path change:** `am_search` gains a fact block, a sibling-wing pointer naming wings it did not search, and correction marks on hits; a new one-call bootstrap surface appears. An agent's recall visibly changes.

## Context

The palace holds a temporal knowledge graph — measured 2026-08-26 on the live palace: 342 entities,
196 triples, 182 current, 14 ended, validity windows, provenance — and a recall never opens it.
`kg_triples` and `kg_entities` appear **zero times** in `internal/palace/service.go`,
`memory_search.go` and `rank.go`. The only indexes are B-trees on subject/object/predicate, so a
fact is reachable only by already knowing its entity string.

**This ADR delivers a deferral that was recorded and then lost.** `ADR-004` T5 (Accepted, `done`)
carries `- Wiring the graph into Service.Search (deferred: docs/adr/BACKLOG.md)`, and `BACKLOG.md`
has no entry for it under any wording. `adr-debt` reports 0 unreceipted because the pointer resolves
to a real file — the exact failure the deferral rule exists to catch, surviving inside the sweep
built to catch it. Delivering it is also what crossed a condition ADR-004 set — see §"The ADR-004
condition" for how that conflict was surfaced and then resolved, rather than reading this paragraph
as the whole story.

**A related backlog item's premise is now false, and its expectation was falsified.** BACKLOG item 2
("Decide the entity graph: feed it or retire it") argues from *"`Service.Add` does not [call
extractEntities], 82 of 82 today"*. ADR-016 shipped since: `Service.Add` stamps entities on every
chunk, and **945 of 1,985 drawers (47.6%) carry them, measured 2026-08-26**. So the item's own
recommended option — *feed it* — was taken. Hallways are **still 0**. Feeding the extractor was
necessary and not sufficient, and nobody has separated the two remaining causes (recompute not run
since, or no entity pair clearing the co-occurrence threshold). That is why this ADR routes around
the derived graph rather than through it.

Two measurements bound what is achievable and are stated up front so a modest result reads as the
instrument working rather than the feature failing:

- **Fact reachability caps at 46%.** 196 triples, 106 carry `source_drawer_id`, **90 resolve** to an
  existing drawer. 16 pointers dangle.
- **97.1% of drawers are orphans** (57 of 1,985 carry any edge), and **0 drawers are named as a
  triple object** — so the pointer pattern an entry point indexes has no adoption in this workspace
  at all.

## Existing Primitives Audit

- **`kg_triples` / `kg_entities` + `am_kg_query`** — the store and its exact-match reader. **Reused,
  not reshaped.** The read path gains a semantic entry; the write path and schema of triples are
  untouched except for the additive column in T6.
- **`vectors` + the embedding worker** — already indexes drawer chunks per team. **Reused** by
  embedding entity labels into the same store under a distinct namespace; no new backend.
- **`extractEntities` / `drawers.entities`** (ADR-016, executed) — **reused read-only** by F-4. Not
  merged with `kg_entities`, deliberately: merging an unmeasured mechanism into a working one adds
  risk with no way to detect it.
- **`Service.Search` / `rankRetrieved` / `collapseCandidatesToMemories`** — **extended additively.**
  F-9 pins that drawer selection and order are unchanged, so the fact block cannot be confounded
  with a ranking change.
- **`am_traverse`** — **NOT reused.** Its `max_hops` is provably inert (F-17): `via` is an
  intersection carried forward, so hop ≥2 can never add a node. The bootstrap resolves edges
  directly.
- **`eval` arm registry + case sets** — **reused** for the instrument in T1, following the arm
  pattern ADR-003 established.

## Decision

The knowledge graph joins the read path, in four movements, each measurable before the next claims
anything.

**Facts become reachable by a question.** Entity labels are embedded into the existing vector store;
a recall matches the query against them, expands to current triples, and returns them in a block
BESIDE the drawer hits — never merged into them, so ranking is unaffected.

**The wing boundary is resolved by a pointer, not a crossing.** `kg_triples` has no wing column and
the graph is workspace-wide while search is wing-scoped. A wing-scoped recall therefore never
returns another wing's fact CONTENT. For every match it does not own it reports one of two things,
and never silence: the WING, when provenance makes it derivable and the agent can go and query it;
or a COUNT, when it does not. The second is the majority case — 90 of 196 triples resolve to a
drawer, measured 2026-08-26 — so a design that only handled the first would be silent about most
of what it found, which is the failure this decision exists to remove.

**Corrections apply at read time, marking rather than hiding.** A returned record that is the object
of `retracts`, `supersedes` or `qualifies` carries that edge and its replacement's id. Hiding is
refused because a retraction can itself be wrong.

**The protocol becomes an API.** One bootstrap call returns a wing's entry point, its eager tier's
content, its on-demand tier as pointers, corrections already swept, the resolved wing, and what it
omitted — replacing a client-side protocol measured at ~99KB (~25k tokens) plus a hardcoded root id
plus 13 calls.

**What would make this fail, and the data to produce that failure exists today.** T1 builds the
instrument first and its baseline is **0% by construction** — search returns no facts at all now.
Any non-zero answerable-rate is therefore real, which is what exempts it from the MRR noise floor
(two arms of provably identical configuration scored 0.709 against 0.700 on 2026-08-26). The
ceiling is 46% by F-8, and if the measured rate sits far below that with provenance resolving, the
retrieval premise is falsified rather than quietly unmet. F-16 is the bootstrap's own falsifier: it
must beat 13 calls / ~2.8k output tokens, measured, or it has reproduced the problem inside one
call. Every threshold here is valid for THIS corpus and this embedder, never in the abstract.

## The ADR-004 condition — named here rather than decided by implication; resolved 2026-08-26

ADR-004 (Accepted) says, categorically:

> Nothing about the graph is populated, wired or changed until that measurement exists and has spoken.

The measurement is its pre-registered supersession gate — `eval --supersession-gate`, one named arm,
a Wilson interval against a fixed bar of 0.20, a floor of 30 verified non-vacuous pairs. It has never
run. Issue #34 tracks it, is OPEN, and its 2026-08-24 rewrite is explicit about what may not be built
in the meantime:

> The first version of this issue proposed wiring supersession edges into `am_search`. That is
> precisely what ADR-004 (Accepted) forbids until a pre-registered measurement has run. The ask is now
> the measurement, not the feature. The original proposal is preserved at the bottom as the thing that
> must NOT be built yet.

**This ADR does both of the things that sentence covers.** T3 wires a graph read into the hot search
path — `factsFor` on every `SearchPage`, which T4 extends with both entity vocabularies — and T5 puts
`retracts`/`supersedes`/`qualifies` marks onto `am_search` hits. ADR-004's own cost justification names the first one in as many words: *"Feeding the
graph means running `kg-extract` across ~5,020 drawers, wiring a graph read into the hot recall path,
and keeping the graph fresh."*

There is an argument that ADR-004's gate is narrower than its sentence — its metric is
stale-above-current, a RANKING measure, and its verdict prices whether the graph earns a place in
ranking. This ADR pins ranking untouched (F-9), runs no `kg-extract`, and brings its own instrument
with a 0% baseline. On that reading ADR-036 is outside the gate's scope.

**That argument is not made here, because it is not this ADR's to make.** ADR-004 is Accepted, its
sentence is categorical, and its cost model names the hot-path read. Narrowing an accepted decision is
a decision, and this repository's protocol is explicit that a conflict between recorded decisions gets
surfaced rather than resolved by implication — which is exactly what "Invalidates: none — checked"
did, silently, in the header of this document.

**Resolution was required before merge, one of:**

1. **Run the gate** (#34's actual ask). A `justified` verdict authorises populating and wiring the
   graph *"as its own ADR"*, which this already is — the conflict then dissolves into a satisfied
   precondition. `not justified` or `unresolved` sends this to option 2 or 3. The two blockers #34
   recorded (#36, #35) are both fixed, so it is runnable; it needs ≥30 judge-verified non-vacuous
   temporal pairs generated against a live palace first.
2. **Split this ADR** — land what sits outside the condition and hold T3's hot-path read and T5's
   marks until the gate speaks.
3. **Amend ADR-004** to the narrower reading above, with the owner's sign-off recorded in both
   documents and #34 updated so it does not rot into contradiction.

**Resolved 2026-08-26 — option 3, by owner sign-off.** M, with this section's three paths in front of
him: *"P1 - i agree with change"*. The change proceeds without waiting for the gate; ADR-004 carries
the matching amendment (its Decision sentence and its Out-of-Scope wiring bullet), recorded in the
same commit as this paragraph, and #34 carries a status update so the measurement's ask survives on
its own merits — the gate still decides `kg-extract` population and any RANKING use of the graph; it
no longer bars this ADR's annotations. One honesty note: the owner's words did not name a path
number; the mapping onto option 3 is the recording agent's, on the grounds that options 1 and 2 both
hold the change back, so agreement with the change is agreement that ADR-004's condition yields.
Recorded on M's instruction ("adr and adapt to the current situation").

No gate catches this class: `adr-debt` reported 0 because the pointer resolved, and nothing reads
SEMANTIC conflict between two accepted records. Review is the only gate here, and review is what
caught it — and review is also where the resolution arrived.

## Alternatives Considered

- **Personalized PageRank over the graph** — REJECTED for now, not on merit: it presumes a connected
  graph, and ours derives **zero hallways** against 945 entity-carrying drawers, so seeding a walk
  over an edgeless graph returns the seeds. (The HippoRAG shape, arXiv 2405.14831.) Revisit once T6
  has produced edges and T1 can measure the difference.
- **Unify the two entity vocabularies at the write path** — REJECTED as the first move: the
  extraction-side vocabulary is itself unmeasured, so merging it into the authored one would spend a
  schema change on an unknown. F-4 takes the read-only join instead, which needs neither a schema nor
  a write-path change. (The HippoRAG 2 shape, arXiv 2502.14802, reports +7% on associative memory
  from putting phrase and passage nodes in one graph — worth revisiting once F-4 has measured whether
  the second vocabulary helps at all.)
- **Add a `wing` column to `kg_triples` and backfill.** Rejected in favour of deriving wing from
  provenance (F-8), which needs no migration on live data. The cost is a 46% ceiling, recorded in
  the spec's Risks. Revisit if provenance proves too sparse to be useful.
- **Fix `am_traverse` and build routing on it.** Rejected: whether traversal should be transitive or
  confined is an unmade product decision, and they are different products. F-17 resolves edges
  directly instead.

## Component / Boundary Impact

Inherited from `docs/architecture.md` §Module Map; delta: `internal/palace` gains a read-path
dependency on its own KG tables, which it did not have. No module moves and no ownership changes.
The MCP layer gains one surface. `internal/store` is unchanged — entity vectors use the existing
`VectorStore` under a separate namespace rather than a new backend.

## Wiring & Contract Changes

Inherited from `docs/specs/2026-08-26-a-recall-that-answers.md` §Contracts Touched; delta:

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `db/migrations/00028_kg_triples_derived.sql` | new nullable column marking a server-derived edge | T6 | T7, T8 |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the fact-retrieval arm and its case set (T1) | T1 | T3, T4, T8 | No — additive eval surface |
| `palace.ValidSearchID`-style resolution of absence vs failure (T2) | T2 | T3 | No — internal |
| `Service.factsFor` returning wing-resolved facts (T3) | T3 | T4, T5, T8 | No — new internal method |
| `searchEventRow`-style derived-edge marking (T6) | T6 | T7, T8 | No — additive nullable column |
| `Service.EntryPoint` (T7) | T7 | T8 | No — new internal method |
| `kg.CorrectionsFor` (T5) | T5 | T8 | No — one incoming sweep, consumed rather than reimplemented |
| `kg.Resolution` (T2) | T2 | T3, T7 | No — T7 reuses the absence vocabulary rather than inventing a second |
| `palace.WingPolicy` (T3) | T3 | T5, T7, T8 | No — the single authorization point F-19 requires; four filters that agree today diverge on the path nobody tested |

## Implementation

See `tasks/README.md`. Eight tasks in four waves.

A cold read-only review (different lineage, no team memory) returned REJECT against the first
draft with 14 findings. Four were spot-checked against source before any edit and all four held, so
the rest were worked rather than argued. What changed, and why it is recorded here rather than
quietly fixed:

- **UC1-S1 — the happy path of this entire decision — was bound to a NEGATIVE assertion.** "A
  question reaches the fact that answers it" was bound to a test asserting no FOREIGN wing's fact
  appears, which returning nothing satisfies completely. Now bound to a positive stub.
- **Six of the 26 stubs moved to `internal/mcpserver`.** Five tasks claimed "delete the render line
  and the test goes red" while naming only `package palace` tests, which call the service directly —
  precisely what an unwired caller also does. That claim was false as written.
- **F-2 and F-8 contradicted each other for 54% of facts.** F-8 called unresolvable provenance
  "elsewhere"; F-2 demanded the response NAME the wings holding elsewhere-matches. For the 106
  triples with no resolvable wing, no name exists. This was not an authoring slip but an undecided
  requirement, so it went back to the user rather than being answered here. Resolved as three
  states, and F-18 is the new fact that carries the third.
- **F-19 is new**: one wing-authorization rule governs the fact block, the sibling pointer, the entry
  point's edges and the bootstrap's inline content. A correction target's id and an outgoing
  taxonomy edge are both routes a foreign wing can leak that a subject/predicate/object check
  cannot see.
- **T5 now PRODUCES the correction sweep and T8 consumes it.** Both had independently implemented
  the same incoming three-predicate walk, which is two rules that diverge on the path nobody tested.
- **T6 defines the derived edge's subject, predicate, object and attachment root**, and its test
  proves TRAVERSAL rather than existence — a marked self-loop satisfied the original wording while
  making nothing reachable.
- **T1 and T8 commit FROZEN, dated corpora.** Both had a real-data requirement and a hermetic gate,
  a combination the gate cannot see: the fence goes green while the actual requirement is unmet.
- **F-16 asserts semantic parity before it compares tokens.** Without that, the bootstrap wins its
  own cost gate by returning less.

## Consequences

- **Positive:** a question can reach a fact, and a recall stops being silent about answers it did
  not search. The client-side protocol shrinks by roughly the half that is traversal instructions.
- **Positive:** every FACT-RETRIEVAL claim here is measurable before it is believed — T1 exists so
  nothing after it can report such an improvement without an instrument. The scope is deliberate:
  T2's four-state lookup and T6's write-path edges are correctness work proven by a test rather than
  a score, so they do not wait on a measurement that would say nothing about them.
- **Negative:** the bootstrap encodes a WORKFLOW, not just data. A wrong tier split or sweep is
  expensive to walk back once clients depend on it. F-14 and F-16 are what make that observable
  early.
- **Negative:** F-8's 46% ceiling means over half of today's facts cannot be placed in any wing.
  They are not hidden — F-18 requires them counted — but they cannot be routed to, and that is a
  write-path problem this ADR does not solve.
- **Negative:** T6 fixes the write path only. The 1,928 existing orphans stay orphaned, so the live
  corpus is still ~97% unreachable by traversal when T7 ships. T6 precedes T7 so the edge CONTRACT
  is settled before an entry point indexes against it — not because T6 delivers coverage. The
  backfill is deferred with a receipt.
- **Neutral:** ranking is untouched. F-9 pins it, so this cannot be confused with a retrieval change.

`adr-judge` reports C3 ("Consequences state no cost") against this section. Checked 2026-08-26 and
it stands as written: the three **Negative** bullets above name a workflow that is expensive to
reverse, a 46% ceiling on what can be located at all, and a corpus that stays ~97% unreachable after
T6. The finding is a parser artifact, not a missing trade-off, and it is recorded here rather than
answered by adding the word the heuristic looks for — writing prose to satisfy a checker is how a
section stops meaning anything.

## Out of Scope

Inherited from `docs/specs/2026-08-26-a-recall-that-answers.md` §Non-Goals; delta:

- Fixing `am_traverse`'s inert `max_hops` (deferred: docs/adr/BACKLOG.md §"From ADR-036")
- Separating why hallways derive nothing — recompute never run, or the co-occurrence threshold never met (deferred: docs/adr/BACKLOG.md §"From ADR-036")
- Repairing the 16 dangling `source_drawer_id` pointers (deferred: docs/adr/BACKLOG.md §"From ADR-036")
- Personalized PageRank over the graph — it presumes a connected graph, and ours derives zero hallways against 945 entity-carrying drawers, measured 2026-08-26; revisit once T6 has produced edges and T1 can judge it (deferred: docs/adr/BACKLOG.md)

## Risks

Inherited from `docs/specs/2026-08-26-a-recall-that-answers.md` §Risks; delta:

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Migration `00028` collides with another open branch | Low | High | Checked 2026-08-26 across every remote branch: `00027` is the highest anywhere, held by ADR-034 on PR #61 |
| ADR number 036 collides | Low | High | Checked across every remote branch: 033 (#58), 034 (#61), 035 (#60) are claimed; 036 is free. `TestADRNumbersAreUnique` guards it thereafter |

**Migration `00028` leaves `00027` unallocated, and that gap is only safe under a condition.**
`00027` belongs to ADR-034 on PR #61, which has not merged. Verified against the dependency
(`goose v3.27.1`, `up.go:82`): plain `goose.Up` — which `cmd/server/main.go:1382` calls, with no
`WithAllowMissing` — returns `found N missing migrations before current version M` when a pending
migration sits BELOW the database's maximum applied version, and this repository propagates that
error up through the CLI, so the server exits.

So merging this first, running any server against a database, and then merging #61 would leave that
database refusing to start. **The condition: whichever of the two merges SECOND renumbers at merge.**
If #67 lands first, #61 takes `00029`. That is the allocation rule this team already recorded after
the ADR-number collision — allocate at merge, never at authoring — and it applies to migrations for
the same reason: a per-branch uniqueness check is blind to cross-branch collisions by construction.

## Rollback

Persistent state and a public contract, so rollback is real and ordered. `00028` carries a
`-- +goose Down` dropping its column; the column is nullable, so a previous binary against the
migrated schema writes NULL and reads nothing. The `am_search` additions are additive fields — a
client ignoring them sees today's response. The bootstrap surface is a new tool: removing it breaks
only callers that adopted it, which is why F-16 gates adoption on a measured win rather than a
promise. Revert order: binary, then tool registration, then migration.

## Follow-ups

- [ ] Report the first measured fact answerable-rate in `BACKLOG.md` whichever way it falls, including "0% — provenance too sparse", which would falsify F-8's derivation rather than extend it.
- [ ] Report whether the bootstrap actually beat 13 calls / ~2.8k output tokens, with the measurement, before any client is told to depend on it.
