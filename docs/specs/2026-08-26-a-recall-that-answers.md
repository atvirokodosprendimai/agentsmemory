# Spec: Reach a fact by asking a question

> **Date:** 2026-08-26 · **Status:** Ready-for-ADR *(marked Ready-for-ADR by the owner, 2026-08-26 — the ADR carries the Accepted state)*
> **Owner:** Zy · **Becomes:** [ADR-036](../adr/ADR-036-a-recall-that-answers.md) *(Accepted by the owner, 2026-08-26, after two cold review laps returned REJECT and were worked)*
> **Gate:** Status may become Ready-for-ADR only after `spec-verify --spec docs/specs/2026-08-26-a-recall-that-answers.md` exits 0.
> **Cross-references:** `docs/adr/ADR-001-recall-answers-or-abstains.md` (abstention — Accepted, all six tasks pending), `docs/adr/ADR-016-a-memory-an-agent-files-must-be-navigable.md` (entity stamping — executed), `docs/adr/ADR-031-the-column-abstention-would-calibrate-on.md`

## Problem

The palace holds a temporal knowledge graph — 342 entities, 196 triples, 182 current facts, validity
windows, provenance — and a recall never opens it: `kg_triples` and `kg_entities` appear zero times in
`internal/palace/service.go`, `memory_search.go` and `rank.go`. Facts carry only B-tree indexes on
subject/object/predicate, so one is reachable only by already knowing its entity string. The team's
operating skill compensates in prose, and states the consequence plainly: *"If your record is not the
top hit, it is not filed — it is stored."*

## Goal

A question asked in natural language can reach a fact, and a recall says when the answer lives in a
wing it did not search. Measured: fact answerable-rate rises from a 0% baseline (search returns no
facts today, by construction).

## Actors

| Actor | Kind | Goal |
|-------|------|------|
| Agent session | system | Ask a question and receive the fact that answers it, not only prose that mentions it |
| Operator | human role | Tell whether recall missed an answer or the answer is in another wing |
| `am_search` | system | Return hits within one wing without disclosing another wing's content |

## Use Cases

### UC-1: Agent asks a question whose answer is a fact

- **Trigger:** `am_search` with a natural-language query · **Preconditions:** the wing holds ≥1 `kg_triple` whose subject or object matches the query semantically
- **Main flow:**
  1. The query is embedded and matched against entity labels.
  2. Matching entities expand to their current triples.
  3. Triples belonging to the searched wing are returned in a distinct block beside the drawer hits.
- **Failure paths:** a. at step 3, every matching fact belongs to another wing → return no fact content, and report which wings hold them. b. at step 2, a matching entity's only triples are ended (`valid_to <> ''`) → the fact is not presented as current.
- **Postconditions:** no fact from outside the searched wing has had its content disclosed.

### UC-2: Agent recalls in a wing where the answer lives elsewhere

- **Trigger:** a wing-scoped `am_search` whose matching facts all lie outside the searched wing — whether their own wing is derivable or not · **Preconditions:** the workspace holds matching facts the searched wing does not own
- **Main flow:**
  1. Matching facts are found workspace-wide.
  2. None belongs to the searched wing.
  3. Each match resolves to one of three states: LOCAL, FOREIGN (its wing is derivable) or UNLOCATABLE (it is not).
  4. The response names every FOREIGN wing and states they can be queried, and reports the UNLOCATABLE count.
- **Failure paths:** a. at step 4, the response is silent about either → indistinguishable from "nothing is filed", which is the failure this case exists to remove. Dropping the unlocatable count is the same failure wearing a narrower scope, and on this corpus it is the majority of matches.
- **Postconditions:** the agent can reach every LOCATABLE match in one further call; matches whose wing cannot be derived are reported as a count so the agent knows they exist; and no sibling wing's fact content was returned.

### UC-3: Agent recalls a record that has been corrected

- **Trigger:** any `am_search` returning a record that is the object of a `retracts`, `supersedes` or `qualifies` edge · **Preconditions:** such an edge exists
- **Main flow:**
  1. The record is returned in its normal rank position.
  2. It carries the correction edge and the id of the record that replaced it.
- **Failure paths:** a. at step 2, the record is returned unmarked → a session acts on a retracted memory believing it current.
- **Postconditions:** nothing is hidden; the correction travels with the record.

### UC-4: Agent reaches a wing's taxonomy without being told an id

- **Trigger:** an agent starts work in a wing it has not seen · **Preconditions:** the wing holds records
- **Main flow:**
  1. The agent asks the server for the wing's entry point.
  2. The server returns the drawer other records hang from and its outgoing taxonomy edges.
  3. The agent loads what those edges name.
- **Failure paths:** a. at step 2, the wing has no entry point → say so explicitly, so "not built yet" is distinguishable from "the call failed".
- **Postconditions:** no hardcoded drawer id was required.

### UC-5: A filed memory is reachable by traversal, not only by search

- **Trigger:** any `am_add_drawer` · **Preconditions:** none
- **Main flow:**
  1. The drawer is written.
  2. The server attaches an edge, marked as server-derived.
  3. The drawer is reachable from the wing's graph.
- **Failure paths:** a. at step 2, a derived edge would overwrite an edge the writer authored → the authored edge wins.
- **Postconditions:** the drawer is not an orphan, and whether its edge was derived or authored is visible.

### UC-6: A session bootstraps a wing in one call, carrying no protocol

- **Trigger:** a session starts in a wing · **Preconditions:** none — no hardcoded id, no memorised procedure
- **Main flow:**
  1. The session asks the server to bootstrap the wing.
  2. One response carries: the entry point, the eager tier's CONTENT, the on-demand tier as pointers, incoming corrections already swept, the resolved wing, and what was left out.
  3. The session begins work.
- **Failure paths:** a. at step 2, the response would exceed its budget → it truncates and SAYS what it dropped, never silently. b. at step 1, the wing has no entry point → say so, distinguishably from an error, and return what exists.
- **Postconditions:** no second call and no constant from a skill file were needed; the session knows what it was not given.

## Scenarios

### UC1-S1 [happy] A question reaches the fact that answers it [@spec] → `internal/palace/recallanswers_spec_test.go::TestAQuestionReachesTheFactThatAnswersIt`

```gherkin
Given a wing holds a current fact whose subject is semantically close to the question
When an agent searches that wing in natural language without naming the entity
Then the fact is returned in a distinct block beside the drawer hits
```

### UC1-S2 [failure] An ended fact is not presented as current [@spec] → `internal/palace/recallanswers_spec_test.go::TestAnEndedFactIsNeverPresentedAsCurrent`

```gherkin
Given the only matching fact has a non-empty valid_to
When an agent searches that wing
Then the fact is not returned in the current-fact block
```

### UC2-S1 [happy] A recall names the wings that hold the answer [@spec] → `internal/palace/recallanswers_spec_test.go::TestARecallNamesTheWingsThatHoldTheAnswer`

```gherkin
Given matching facts exist only in sibling wings of the searched wing
When an agent searches the wing that holds none
Then the response names those sibling wings and states they can be queried
```

### UC2-S2 [failure] Another wing's fact content never crosses the boundary [@spec] → `internal/palace/recallanswers_spec_test.go::TestAWingScopedRecallNeverReturnsAnotherWingsFact`

```gherkin
Given a sibling wing holds a matching fact
When an agent searches a different wing
Then no subject, predicate or object of that fact appears anywhere in the response
```

### UC3-S1 [happy] A corrected record arrives carrying its correction [@spec] → `internal/palace/recallanswers_spec_test.go::TestACorrectedRecordArrivesCarryingItsCorrection`

```gherkin
Given a returned record is the object of a supersedes edge
When an agent searches and that record ranks onto the page
Then it is returned with the correction edge and the id of its replacement
```

### UC3-S2 [failure] A superseded record is never returned unmarked [@spec] → `internal/palace/recallanswers_spec_test.go::TestACorrectedRecordArrivesCarryingItsCorrection`

```gherkin
Given a returned record is the object of a retracts edge
When an agent searches and that record ranks onto the page
Then the response does not present it without the correction
```

### UC4-S1 [happy] A wing names its own entry point [@spec] → `internal/palace/recallanswers_spec_test.go::TestAWingReportsItsOwnEntryPoint`

```gherkin
Given a wing holds an entry-point record
When an agent asks the server for that wing's entry point
Then the entry drawer and its outgoing taxonomy edges are returned
```

### UC4-S2 [failure] A wing with no entry point says so [@spec] → `internal/palace/recallanswers_spec_test.go::TestAWingReportsItsOwnEntryPoint`

```gherkin
Given a wing holds records but no entry point
When an agent asks the server for that wing's entry point
Then the response states none exists, distinguishably from an error
```

### UC5-S1 [happy] A newly filed drawer is reachable by traversal [@spec] → `internal/palace/recallanswers_spec_test.go::TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked`

```gherkin
Given a wing with an entry point
When an agent files a drawer naming no edge
Then the drawer carries a server-derived edge marked as derived
```

### UC5-S2 [failure] A derived edge never overwrites an authored one [@spec] → `internal/palace/recallanswers_spec_test.go::TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked`

```gherkin
Given an agent files a drawer and authors its edge
When the server would also derive an edge
Then the authored edge is the one that stands
```

### UC6-S1 [happy] One call is enough to start [@spec] → `internal/palace/recallanswers_spec_test.go::TestOneCallBootstrapsAWing`

```gherkin
Given a wing with an entry point and an eager tier
When a session bootstraps that wing
Then it receives the eager content, the on-demand pointers and the swept corrections in one response
```

### UC6-S2 [failure] A truncated bootstrap says what it dropped [@spec] → `internal/palace/recallanswers_spec_test.go::TestATruncatedBootstrapSaysWhatItDropped`

```gherkin
Given a wing whose eager tier exceeds the response budget
When a session bootstraps that wing
Then the response reports what was omitted and how to fetch it
```

### UC6-S3 [failure] A wing with no entry point still bootstraps [@spec] → `internal/palace/recallanswers_spec_test.go::TestOneCallBootstrapsAWing`

```gherkin
Given a wing that has never had an entry point written
When a session bootstraps that wing
Then the response says so explicitly and returns what the wing does hold
```

## Facts

| ID | Assertion (invariant / behavior) | Test (`path::name`) | Tag | Cmd (optional) |
|----|----------------------------------|---------------------|-----|----------------|
| F-1 | A wing-scoped recall never returns the content of a fact belonging to another wing | `internal/palace/recallanswers_spec_test.go::TestAWingScopedRecallNeverReturnsAnotherWingsFact` | @implemented | `go test ./internal/palace/ -run '^TestAWingScopedRecallNeverReturnsAnotherWingsFact$' -count=1` |
| F-2 | When matching facts exist in other wings AND their wing is derivable, the response names those wings and states they can be queried; omitting a wing that IS derivable is a failure. Facts whose wing cannot be derived are F-18's business, not silence | `internal/palace/recallanswers_spec_test.go::TestARecallNamesTheWingsThatHoldTheAnswer` | @implemented | `go test ./internal/palace/ -run '^TestARecallNamesTheWingsThatHoldTheAnswer$' -count=1` |
| F-3 | A returned record that is the object of a `retracts`, `supersedes` or `qualifies` edge is returned WITH that edge and the id of the record that replaced it — marked, never hidden | `internal/palace/recallanswers_spec_test.go::TestACorrectedRecordArrivesCarryingItsCorrection` | @implemented | `go test ./internal/palace/ -run '^TestACorrectedRecordArrivesCarryingItsCorrection$' -count=1` |
| F-4 | Fact lookup matches a query against both `kg_entities` and `drawers.entities`, read-only. The second vocabulary has material to work with: 945 of 1,985 drawers carry entities (47.6%), measured 2026-08-26 — ADR-016 made `Service.Add` stamp them, though the derived hallway graph is still empty at 0; a fact whose subject appears only in `drawers.entities` is still reachable by a question naming that term | `internal/palace/recallanswers_spec_test.go::TestFactLookupMatchesBothEntityVocabularies` | @implemented | `go test ./internal/palace/ -run '^TestFactLookupMatchesBothEntityVocabularies$' -count=1` |
| F-5 | A case set of questions whose gold answer is a `kg_triple` exists, and an eval arm reports the fraction where that triple reached the response. Baseline is 0% by construction — which is what exempts it from the MRR noise floor measured 2026-08-26, where two arms of provably identical configuration scored 0.709 against 0.700 | `internal/palace/recallanswers_spec_test.go::TestFactAnswerableRateIsMeasured` | @implemented | `go test ./internal/palace/ -run '^TestFactAnswerableRateIsMeasured$' -count=1` |
| F-6 | The fact-retrieval arm scores ordering by MRR on the same paired bootstrap as every other arm, so a fact ranked second is distinguishable from one ranked tenth. This is a property of the INSTRUMENT and is measurable before facts reach the page — the arm scores the ranked fact list it retrieves, whatever consumes it | `internal/palace/recallanswers_spec_test.go::TestFactsOnThePageAreScoredByMRR` | @implemented | `go test ./internal/palace/ -run '^TestFactsOnThePageAreScoredByMRR$' -count=1` |
| F-7 | A fact whose `valid_to` is non-empty is never presented as current. Precedent: `am_kg_query` already defaults to `status=current` (`internal/mcpserver/kg.go:kgQueryDefaultStatus`), so the 14 ended facts are already invisible there — this requirement extends an existing default to the new path rather than inventing one | `internal/palace/recallanswers_spec_test.go::TestAnEndedFactIsNeverPresentedAsCurrent` | @implemented | `go test ./internal/palace/ -run '^TestAnEndedFactIsNeverPresentedAsCurrent$' -count=1` |
| F-8 | Wing membership of a fact is derived from `kg_triples.source_drawer_id`, in exactly three states: provenance resolves to a drawer in the searched wing (LOCAL), resolves to a drawer in another wing (FOREIGN — that wing is named, per F-2), or does not resolve at all (UNLOCATABLE — counted, per F-18). A fact is never attributed to the searched wing on unresolvable provenance. Measured 2026-08-26: of 196 triples, 106 carry an id, 90 resolve, 16 dangle — so the unlocatable state is the majority case at 54%, not an edge case | `internal/palace/recallanswers_spec_test.go::TestAFactsWingComesFromItsProvenance` | @implemented | `go test ./internal/palace/ -run '^TestAFactsWingComesFromItsProvenance$' -count=1` |
| F-18 | A matching fact whose wing cannot be derived is reported as a COUNT and never attributed to any wing. A response that silently drops it is a failure: silence is indistinguishable from "nothing is filed", which is the failure this spec exists to remove | `internal/palace/recallanswers_spec_test.go::TestAnUnlocatableFactIsCountedNotDropped` | @implemented | `go test ./internal/palace/ -run '^TestAnUnlocatableFactIsCountedNotDropped$' -count=1` |
| F-19 | ONE wing-authorization rule governs every response path that this spec adds — the fact block, the sibling pointer, the entry point's edges and the bootstrap's inline content. A foreign wing's content does not cross on ANY of them, and the rule is applied in one place rather than re-implemented per path | `internal/palace/recallanswers_spec_test.go::TestOneWingRuleGovernsEveryNewResponsePath` | @implemented | `go test ./internal/palace/ -run '^TestOneWingRuleGovernsEveryNewResponsePath$' -count=1` |
| F-13 | ONE call bootstraps a wing, returning: the entry point, the EAGER tier's content inline, the ON-DEMAND tier as pointers, incoming corrections already swept, the resolved wing, and a truncation report. A session needs no second call and no id from a skill file to start work | `internal/palace/recallanswers_spec_test.go::TestOneCallBootstrapsAWing` | @implemented | `go test ./internal/palace/ -run '^TestOneCallBootstrapsAWing$' -count=1` |
| F-14 | The bootstrap is BOUNDED and reports what it omitted. Silent spill is the failure it exists to remove — the client protocol it replaces records a prescribed tier losing 74% of itself to an unreported ~40KB cap | `internal/palace/recallanswers_spec_test.go::TestATruncatedBootstrapSaysWhatItDropped` | @implemented | `go test ./internal/palace/ -run '^TestATruncatedBootstrapSaysWhatItDropped$' -count=1` |
| F-15 | Corrections are swept SERVER-side across all three predicates (`retracts`, `supersedes`, `qualifies`) and read INCOMING. Outgoing-only traversal cannot see a correction, which is why the client protocol needs three separate queries and why running only `retracts` once shipped a pointer to an ADR that was not on `main` | `internal/palace/recallanswers_spec_test.go::TestCorrectionsAreSweptServerSideAcrossAllThreePredicates` | @implemented | `go test ./internal/palace/ -run '^TestCorrectionsAreSweptServerSideAcrossAllThreePredicates$' -count=1` |
| F-16 | The bootstrap carries the SAME logical payload as the client-side traversal it replaces, and costs fewer output tokens doing so. Parity is asserted first and the token comparison only runs if it holds — otherwise the cheapest conformant bootstrap is one that returns nothing. Baseline: 13 calls for ~2.8k output tokens, measured 2026-08-26 against the redacted manifest in `internal/palace/testdata/`, under the tokenizer that manifest names | `internal/palace/recallanswers_spec_test.go::TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces` | @implemented | `go test ./internal/palace/ -run '^TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces$' -count=1` |
| F-17 | The bootstrap resolves the entry point's edges DIRECTLY, never by graph walk. `am_traverse`'s `max_hops` is provably inert: `via` is an intersection carried forward (`internal/palace/graphquery.go:intersectSorted`), so every hop-2 room shares a wing with the start set and hop 1 already reached it. Verified 2026-08-26 from a hub (25 nodes, all hop ≤1 — every room in the palace) and from a leaf (10 nodes, all hop 1). A bootstrap built on multi-hop traversal would silently return only hop 1 | `internal/palace/recallanswers_spec_test.go::TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk` | @implemented | `go test ./internal/palace/ -run '^TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk$' -count=1` |
| F-12 | A fact lookup distinguishes "no matching fact" from "the lookup did not resolve". Observed 2026-08-26: `am_kg_query` returned `count: 0` with no error for both a nonexistent entity and a nonexistent predicate — so an empty result is currently indistinguishable from a failed one, and F-2's sibling-wing pointer cannot be trusted while that holds | `internal/palace/recallanswers_spec_test.go::TestAFactLookupDistinguishesAbsenceFromFailure` | @implemented | `go test ./internal/palace/ -run '^TestAFactLookupDistinguishesAbsenceFromFailure$' -count=1` |
| F-10 | A wing reports its own entry point — the record others hang from and its outgoing taxonomy edges — so reaching a wing's taxonomy never requires an id the server did not supply. A wing with no entry point says so, distinguishably from an error | `internal/palace/recallanswers_spec_test.go::TestAWingReportsItsOwnEntryPoint` | @implemented | `go test ./internal/palace/ -run '^TestAWingReportsItsOwnEntryPoint$' -count=1` |
| F-11 | Every drawer receives an edge at write time; a server-derived edge is MARKED as derived and never overwrites one the writer authored | `internal/palace/recallanswers_spec_test.go::TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked` | @implemented | `go test ./internal/palace/ -run '^TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked$' -count=1` |
| F-9 | Returning facts does not change which drawers reach the page, nor their order | `internal/palace/recallanswers_spec_test.go::TestReturningFactsDoesNotChangeDrawerRanking` | @implemented | `go test ./internal/palace/ -run '^TestReturningFactsDoesNotChangeDrawerRanking$' -count=1` |

## Domain

A **fact** is a `kg_triple` (subject, predicate, object) with a validity window and optional
`source_drawer_id` provenance. A **drawer** is one verbatim chunk; a **memory** is the set of chunks
sharing a `parent_id`. A **wing** is a project namespace owning drawers; facts have no wing of their
own and borrow one through provenance. **Correction edges** (`retracts`, `supersedes`, `qualifies`)
relate one record to another.

## Contracts Touched

| Surface | Change | Consumers |
|---------|--------|-----------|
| `am_search` result | add: a fact block, a sibling-wing pointer, correction marks on hits | every agent session; the client kit |
| `palace.SearchResult` | add: fields carrying the above | `internal/mcpserver`, eval arms |
| `am_kg_query` result | change: distinguish an empty result from an unresolved lookup | agent sessions; F-2's pointer |
| wing bootstrap | add: one call returning entry point, eager content, on-demand pointers, swept corrections, resolved wing, truncation report | agent sessions; every client kit; replaces client-side protocol |
| wing entry point | add: a surface reporting a wing's entry record and its taxonomy edges | agent sessions; the client kit |
| `am_add_drawer` result | add: whether the drawer carries an edge, and whether that edge was derived | agent sessions |
| eval arm registry | add: a fact-retrieval arm and its case set | `agentsmemory eval` |
| vector namespace | add: entity-label vectors alongside drawer vectors | embedding worker, `internal/store` |

## Non-Goals

- **Abstention / a confidence verdict** (permanent for this spec: ADR-001 is Accepted and owns it; all six of its tasks are pending. Re-deciding it here would fork an accepted decision.)
- **Memory-level or late-chunking embeddings** (deferred: `docs/specs/2026-08-26-a-recall-that-answers.md` §Risks). The measured ceiling is in-pool 100%, top-1 46% — ordering fails, not recall, so a representation change aimed at recall is unjustified on this corpus. An experiment at most.
- **Unifying the two entity vocabularies at the write path** (deferred: this spec's F-4 takes the read-only join instead). No hallway derives today even within the one vocabulary that has extraction wired, so merging an unmeasured mechanism into a working one adds risk with no way to detect it.
- **Defining the tier VOCABULARY** (permanent: the server distinguishes an eager tier from an on-demand one, and does not bless particular names. A team's `must.*`/`ref.*` is one spelling of that distinction, not the product's.)
- **Changing the reranker, the fusion, or `RERANK_*` defaults** (permanent: out of this spec's subject; ADR-030 and ADR-034 own that area.)
- **Adding a `wing` column to `kg_triples`** (deferred: F-8 derives wing from provenance instead, which needs no migration. Revisit if provenance proves too sparse to be useful.)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Most existing facts carry no `source_drawer_id`, so F-8 makes them unreachable from any wing-scoped recall | High | Med | F-5 measures reachability directly; if the answerable-rate stays near zero the cause is provenance, not retrieval, and the spec's premise is falsified rather than quietly unmet |
| A sibling-wing pointer discloses project names across wings | Low | Low | Facts are workspace-wide and a workspace is one tenant, so the names disclosed are that tenant's own; verified against `am_kg_add`'s documented scope |
| Facts crowd drawers out of the page | Med | Med | F-8 pins that drawer selection and order are unchanged; the fact block is additive |
| The fact block is built, correct, and never read | Med | High | F-5 is the rung-4 measurement; an arm that scores zero retrieved facts fails rather than reporting a score |
| **F-8 caps fact reachability at 46%.** Measured 2026-08-26 against the live palace: 196 triples, 106 carry `source_drawer_id`, and only **90 resolve to an existing drawer**. The other 54% are UNLOCATABLE — F-18 requires them counted in the response, so they are not invisible, but they cannot be routed to | High | High | This is F-5's ceiling, stated up front so a 46% result reads as the instrument working rather than the feature failing. Raising it is a write-path question, not a retrieval one |
| **16 provenance pointers are dangling** — they name a drawer that does not exist (106 carry an id, 90 resolve) | Med | Med | F-8 puts an unresolvable pointer in the UNLOCATABLE state, never LOCAL, so a dangling id degrades reachability rather than leaking across wings; F-18 makes it countable rather than silent |
| **97.1% of drawers are orphans.** Measured 2026-08-26: 1,985 drawers, 57 with any edge. And **0 drawers are named as a triple OBJECT**, so the pointer pattern this spec's F-10 is modelled on has zero adoption in this workspace | High | High | F-11 addresses it at the write path in this same spec; F-10 shipped alone would index 2.9% of the palace |
| The graph's own walk is broken in a way that is invisible: `max_hops` is documented 1–10 and cannot change any result. Anyone designing routing on top of it will assume depth works | High | Med | F-17 pins the bootstrap to direct edge resolution. Fixing `am_traverse` itself is deliberately NOT in this spec — whether traversal should be transitive or confined is a product decision nobody has made, and they are different products |
| A full bootstrap encodes a WORKFLOW, not just data. If the tier split or the sweep is wrong, it is expensive to walk back because clients will have been written against it | Med | High | F-16 makes the win measurable rather than assumed, and F-14 forces the response to declare its own limits — a bootstrap that cannot state what it omitted is not ready to be depended on |
| The bootstrap returns so much that it costs more context than the protocol it replaces | Med | High | F-16 is the gate: it must beat 13 calls / ~2.8k output tokens, measured, or it is not shipped |
| Derived edges invent taxonomy the writer did not choose, and S-8 says the extraction side derives nothing measurable today — so derived edges could be noise that makes traversal worse | Med | High | F-11 requires derived edges to be MARKED, which is what makes the noise measurable and removable; the orphan rate and the derived/authored split are both reportable numbers |
| F-10's entry point indexes a palace where 97.1% of drawers are orphans (1,985 drawers, 57 with any edge, measured 2026-08-26) | High | Med | F-11 addresses the write path in the same spec, so the index is not shipped against a corpus it cannot cover |
| F-2's pointer is built on a lookup that fails open, so "no facts in this wing" may mean "the lookup did not resolve" | High | High | F-12 makes the two distinguishable and is a precondition of F-2 rather than an independent nicety |
| A fact that is already wrong reads as current, with no marking — observed 2026-08-26: `drawers.entities is_written_only_by am_mine (retired)` (2026-08-20) is contradicted by `Service.Add stamps_entities_per_chunk_since ADR-016 T2`, and BOTH report `current: true` | High | High | F-3 marks corrections and F-7 refuses to present an ended fact as current; this pair is the specimen both were written for |
| A correction edge is itself wrong, and marking it entrenches an error | Low | Med | F-3 marks rather than hides, so the superseded record and its correction are both visible |

## Open Questions

## Verify

```bash
spec-verify --spec docs/specs/2026-08-26-a-recall-that-answers.md
```

## Grill Log (appendix)

| # | Question | Fact | Decision |
|---|----------|------|----------|
| 1 | How is the wing/workspace scope boundary resolved when returning facts? | F-1 | Amended by the user: never return another wing's content, but tell the agent matches exist elsewhere and can be queried |
| 2 | Should the pointer name the sibling wings? | F-2 | Yes — silence is indistinguishable from "nothing is filed", the failure the pointer exists to remove |
| 3 | Should recall apply supersession itself? | F-3 | Mark, do not hide — a retraction can itself be wrong, and a ranking input is a signal, never a gate |
| 4 | Join the two entity vocabularies? | F-4 | Read-only join at query time; no schema or write-path change |
| 5 | What must the measurement instrument prove? | F-5 | Both — binary answerable-rate gates shipping, MRR gates later tuning |
| 6 | Where does a fact's wing come from, given `kg_triples` has no wing column? | F-8 | Derived from `source_drawer_id`; unresolvable provenance means "elsewhere", never "here" |
| 7 | Does returning facts alter drawer ranking? | F-9 | No — the fact block is additive, so the change cannot be confounded with a ranking change |
| 8 | Should the wing's entry point be discoverable from the server? | F-10 | Yes — a power user already built this by hand with a hardcoded id in a skill file; it may outrank semantic fact lookup |
| 9 | A drawer with no edge is drifting data — enforce, assist, or derive? | F-11 | Derive automatically; the concern that derived edges invent taxonomy is recorded as a Risk and mitigated by marking them |
| 14 | Can the bootstrap use the existing graph walk? | F-17 | No — `max_hops` is provably inert, so the bootstrap resolves edges directly. Fixing traverse is a separate product decision |
| 12 | How opinionated should the server be about the must.*/ref.* tier grammar? | F-13 | Full bootstrap — the protocol becomes an API. It is the only option that removes the ~25k tokens of client-side protocol rather than half of it |
| 13 | What stops the bootstrap becoming the problem it replaces? | F-16 | It must beat the measured client baseline of 13 calls / ~2.8k output tokens, or it is not shipped |
| 11 | Does a fact lookup distinguish absence from failure? | F-12 | No — observed live: count:0 with no error for a nonexistent entity AND a nonexistent predicate. Made a precondition of F-2 |
| 10 | Spec file naming and location | non-behavioral | `docs/specs/YYYY-MM-DD-<topic>.md`; first spec in this repo |
