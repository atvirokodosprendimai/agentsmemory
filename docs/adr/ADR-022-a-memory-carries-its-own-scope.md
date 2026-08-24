# ADR-022: A memory carries its own scope

**Status:** Proposed
**Date:** 2026-08-24
**Owner:** unassigned
**Spec:** None — no spec stage
**Layer:** Storage protocol — what a memory *is*, what it is called, and where it can be seen from. The thinking protocol, how an agent queries, is ADR-023. The split is deliberate: this ADR is a schema and indexing decision, that one is a tool surface and an agent behaviour, and they should be acceptable and reversible independently.
**Breaking changes: none.** The subject is **derived** from fields that already exist, not a new thing anyone writes. Every current drawer gains an address at index time, no default changes, no recall behaviour changes, and every capability here is opt-in at the query. This is stated up front because an earlier draft of this ADR *was* breaking, and the difference is the whole point.
**Cross-references:** ADR-003 (the closet-prior measurement this ADR's argument rests on), ADR-010 (validity as a property of the record rather than a parameter of the call, and the "do not invent a second vocabulary" rule this revision obeys), ADR-016 (made `entities` reachable on the `Add` path — both the precondition for entity-based adjacency and the cautionary precedent for optional fields), ADR-006 (a knob that does nothing), ADR-014 (the shipped default is the measured one), ADR-023 (the resolution protocol built on this one)
**Invalidates:** none — checked (grepped ADR-001..021 for `SEARCH_SCOPE`, `default_wing`, `traverse`, `adjacen`, `hallway`, `taxonomy`: ADR-012, ADR-015, ADR-017 and ADR-021 all *mention* wing scoping, none decides how scope is carried; ADR-016 is the only accepted ADR touching the derived graph, and it decides that entities must be populated, not how adjacency is computed).
**Served-path change:** `am_search` gains subject-pattern filtering — `project.agentmemories.>`, `*.*.decisions` — over an address every existing drawer already has. Unfiltered recall behaves exactly as it does today.

## Context

Scope in this system lives in the **call**, not in the record. Four mechanisms carry it — the `wing` argument, `default_wing` on the registration, `SEARCH_SCOPE`, and the `wing:"*"` escape — and every one is something the *caller* has to get right.

The cost is visible in our own documentation. The always-on bootstrap protocol spends roughly eighty lines teaching an agent to resolve a wing through four ordered fallback rungs, plus a paragraph on what to do when the rungs disagree. That is a great deal of prose to answer a question the record itself could answer.

And it does not work. Two sessions read the inbox convention and filed handoffs into `wing_to-<project>` rather than `wing_<project>` — **six drawers of real findings into wings no session will ever resolve to, and the writes succeeded.**

**A second face of the same gap: nothing says what a room *means*.** `wing_agentmemories` holds `architecture` (43 drawers), `technical` (27) and `decisions` (432). The boundaries between those three are written down nowhere, and `Taxonomy` (`internal/palace/service.go:1189`) returns wings, rooms and **counts** — structure with no semantics. Grepping `Description` across `palace.go` and `repo.go` returns nothing. So filing is a guess, every time, by every agent. The session that produced this ADR chose between those three rooms by feel on each of six writes; that is the argument, not a hypothetical.

**A third face: the craft-versus-project decision is a sentence.** The rule keeping `wing_craft` from filling with project facts — where "every wrong entry is wrong everywhere at once" — is enforced by a test an agent applies to itself: *would this still be true and useful in a repository that shares no code with this one?* Good test, unenforceable, because the property it tests is recorded nowhere.

**The frames that produced this ADR.** IPv6 makes scope part of the address: a router does not consult policy to discover that `fe80::` must not be forwarded — the address says so. NATS carries the idea into naming: dot-separated tokens ordered general to specific, `*` for one token, `>` for the tail, and — the part that matters — **publishes are always concrete while subscriptions are patterned.** DNS supplies the governance piece: **the root is governed and the leaves are free.** IANA decides what `.edu` means; nobody decides what your subdomains mean.

**The key observation that makes all of this additive.** `wing` and `room` are *already* a two-level hierarchy. `wing_agentmemories` + `room=architecture` **is** `project.agentmemories.architecture` — the address exists today, spelled as two flat columns with no relationship expressed between them and therefore not matchable as a hierarchy. This ADR does not introduce an addressing scheme. It **writes down the one already in use** and makes it queryable.

**The topology has a separate defect, and it is quantified.** `Traverse` treats two rooms as adjacent when they **share a wing** (`internal/palace/graphquery.go`). Out of `am_status`, on the live corpus:

| room | wings it spans |
|---|---|
| **diary** | **11 of 11** |
| tooling | 7 |
| decisions | 7 |
| learnings | 6 |

`diary` is in every wing, so `diary` is adjacent to every room in every wing: **the whole palace in one hop.** Two hops saturates — which `wing_agentmemories/tooling` already recorded from the other direction, *"am_traverse walks ROOMS, not memories, and saturates to most of the palace in two hops"*, without naming the cause. An adjacency predicate nearly everything satisfies carries no information. This is a flood with a hop cap, and `maxHops` is a TTL — what you use when you have no path information at all.

**Why more reachability is the wrong goal here — stated because the network frame actively misleads on this point.** In routing, reach is the objective. In recall it is the disease: ADR-003 measures the closet prior costing **~0.10 MRR**, with 10 of 40 golds displaced — the correct memory still retrieved, then pushed down the page by neighbours a boost lifted. Unrelated records do not remove the answer; they add competitors ahead of it. So what is worth importing is the half operators actually spend their time on — naming and filtering — and not the advertisement machinery.

## Existing Primitives Audit

- **`wing` and `room` columns** — already a two-level hierarchy stored as two flat strings. **Reused as the address itself**, not replaced and not duplicated. A wing remains exactly what it is today.
- **`store.Filter map[string]string`** (`internal/store/store.go:44`) — "narrows a search to points whose payload matches every entry, compared as strings". Equality-only, all three backends. **Reused unchanged** — see the decomposition in Wiring. This ADR adds no store primitive.
- **`Taxonomy` / `TaxonomyWing`** (`service.go:1189`) — already assembles the wing→room tree with counts. Reshaped: it gains the description field it never had and becomes the registry. The tool's shape does not change.
- **`am_list_skills`** — already returns name, description and version for a team-shared object. **The registry precedent is in the tree**; rooms and wings simply never got the field.
- **`Dynamics{Strength, Stability, LastActivated, AccessCount}`** (`palace.go:60`) — stamped on every hallway and tunnel by `initDynamics`, and **nothing routes on it**. Reused as the edge weight. Another instance of this repository's signature defect: finished and unreachable.
- **`computeHallwaysForWing` / the `entities` column** — within-wing entity co-occurrence, already derived. Reused as the adjacency substrate, usable **only because ADR-016 shipped**.
- **ADR-016's finding about `entities` itself** — reused as a *cautionary* precedent rather than a mechanism. `entities` existed as a column and `Service.Add` never populated it, so the whole hallway subsystem was structurally unreachable on the path agents actually use. **That is what an optional field becomes**, and it is why the subject here is derived rather than offered.
- **`wing_craft`** — already the default route by convention. Becomes the rule that derives the `shared` scope token, not a special-cased name.
- **The KG's workspace-wide scope** — kept as the *counter*-example. Named here, fixed in issue #23.
- **`MaxKGValueLen = 128`** — the existing attempt to bound a naming field. Reused as evidence rather than as a model; see item 2.

## Decision

### 1. Every memory gets a derived subject. Nobody writes one.

```
<scope>.<wing>.<room>[.<free>...]

project.agentmemories.architecture
shared.craft.verification
project.forumchat.migrations
```

- **`scope` derives**: `shared` when the wing is the craft wing, `project` otherwise.
- **`wing` and `room` are the columns that already exist.**
- **Tokens 4+ are the only optional part** — an agent *may* add depth. That is new capability, not a second way to say something already said.

Reads gain patterns — `*` matches one token, `>` matches the tail. `project.agentmemories.>` is this project; `shared.>` is all craft; `*.*.decisions` is decisions across every wing and scope. **Writes never carry a pattern**, which is NATS's asymmetry and what keeps the namespace enumerable.

Because the subject is a projection of `wing` + `room`, **all 7,238 existing drawers acquire an address the moment the index is rebuilt**. There is no backfill of agent behaviour, no period where the corpus is half-addressed, and no default to flip.

**⚠This revises this ADR's first draft on two points, stated rather than quietly dropped.**

- That draft made the subject **replace** `wing`, `room`, a declared `scope` enum and a community tag set. Replacement meant a migration, a default that hid memories, and an identity change on promotion — real breaking changes for a capability that had not yet been measured. Deriving the subject buys the same query power at none of that cost.
- That draft's community tag set is **retracted**. A tag set plus a wildcard grammar is two vocabularies for one question, which is precisely what ADR-010's primitives audit warns against.

### 2. The shape of the name is constrained by depth and vocabulary, not by length

**≤ 5 tokens**, **≤ 32 characters per token.** Tokens 1–3 are already governed by what `wing` and `room` accept; tokens 4+ are free.

A total-length cap does not prevent sprawl, and that is measured rather than asserted: `MaxKGValueLen = 128` bounds a predicate's length today and the graph still carries **~800–900 distinct predicates for ~1021 facts** — nearly one per fact, a routing table where every entry is a host route. Length was never the binding constraint. **Depth and a governed vocabulary are.**

### 3. The registry says what each governed token means

`Taxonomy` gains a description per wing and per room — one line, the kind `am_list_skills` already returns. `decisions`, `architecture` and `technical` stop being three words an agent chooses between by feel.

**A gloss is worth only what enforces it.** A description nobody reads at write time changes nothing. What makes it bite is the **write tool showing the vocabulary**: `am_add_drawer`'s `room` parameter enumerating known rooms with their glosses, so the agent sees the choice while making it. Prose in a document is not load-bearing here; a parameter description generated from the registry is.

This item is **independently useful even if items 1 and 4 are rejected**, because it fixes a filing problem that exists today under the current flat rooms.

### 4. Adjacency stops being "shares a wing"

Two rooms are adjacent when they share entities above a co-occurrence threshold, weighted by `Dynamics.Strength`. The threshold is the whole point: it is what turns a flood into a walk. This is the one item that changes existing behaviour — `am_traverse` returns different results — and it changes behaviour that is currently useless.

### 5. Origin validation

A `shared.*` record whose entities are project-specific is this palace's prefix hijack: a specific announcement from an origin with no authority over it. Scope is now the first token of an address and entities are already extracted, so this is a query. A report, not a block.

### Pre-registered falsification

Declared before the work, not adjusted after they are seen:

- **Adjacency.** Fan-out from `diary` at one hop, before and after. If the entity-threshold version does not *reduce* the reachable set at hop one on the live corpus, item 4 is retracted rather than tuned.
- **Subject filtering.** MRR on the eval corpus with and without a subject filter applied to a scoped question set. This is ADR-003's mechanism run forwards — removing competitors before ranking — so if it does not move, the closet-prior finding does not generalise the way this ADR assumes and item 1 has bought only tidiness.
- **Registry.** Distinct token count at level 3, plus the share of writes using a registered token. If agents keep coining new rooms at the same rate once glosses are shown to them, item 3 is decoration and comes out.

## Alternatives Considered

**A parallel optional `subject` field that agents fill in alongside `wing`/`room`.** This was the proposal that prompted the revision, and it is rejected for two reasons that reinforce each other. First, **two addressing schemes is the second-vocabulary mistake** — an agent would have to decide which to use, giving two ways to misfile where today there is one, and this ADR's own Context is a list of filing decisions agents got wrong. Second, **optional fields do not get populated**, and we have the receipt: `entities` was a column `Service.Add` never wrote, which left the entire hallway subsystem unreachable on the agent path until ADR-016. A subject nothing populates is a knob that does nothing. Deriving it keeps the additive property the proposal wanted while avoiding both failures.

**Compressing subject tokens in the AAAK dialect.** Rejected. AAAK compresses *narrative* — three-letter entity codes, emotion markers, pipe-separated fields — and its codes are resolved by a reader who holds the context (`ALC` = Alice). **An identifier must resolve globally**, and three-letter codes over a growing namespace alias: `ARC` is architecture or archive, and nothing in the encoding says which. The saving is roughly fifteen tokens across three referrals against tokens already capped at 32 characters. What AAAK *does* offer — a shared dialect with an agreed encoding, which is why `am_get_aaak_spec` exists — is the same property item 3's registry provides, applied to governance rather than length. (A terser *gloss* is a different question and belongs to ADR-023.)

**Keep scope in the call and improve the protocol prose.** Rejected on evidence: the prose exists, is loaded into every session automatically, and the misfiling happened anyway — twice, with the writes succeeding.

**A declared `scope` field agents set explicitly.** Deferred rather than rejected — see Follow-ups. Deriving scope from the wing is right for every case in the corpus today, and a declared override is a strictly additive follow-up that can be measured once subjects are in use.

**A path (`/`) rather than a subject (`.`).** A coin-flip with one asymmetry: a path invites the "everything is a file" reading this ADR explicitly declines.

**Full BGP — AS_PATH, LOCAL_PREF, best-path selection.** Rejected as over-fitting. Best-path collapses to *one* route while recall is inherently multipath; AS_PATH length has no relationship to semantic relevance; and the convergence machinery exists because ASes are independently administered and disagree, where this is one database with one writer.

## Component / Boundary Impact

- `db/migrations` — one migration for the derived prefix keys and the wing/room descriptions. No column an agent writes.
- `internal/palace` — subject derivation in one place; `searchFilter` gains an optional prefix match; `Taxonomy` carries descriptions; `computeHallwaysForWing` and `Traverse` change adjacency.
- `internal/mcpserver` — an optional subject pattern on recall; the registry surfaced in write-tool parameter descriptions.
- `internal/store` — **none.**

## Wiring & Contract Changes

**⚠The implementation unlock.** `store.Filter` is equality-only, which reads like a wall against prefix matching. It is not one. **Do not store the path — store every prefix of it, each as its own key:**

```
subject: project.agentmemories.architecture
  subj1 = "project"
  subj2 = "project.agentmemories"
  subj3 = "project.agentmemories.architecture"
```

`project.agentmemories.>` is then **one equality match on `subj2`**; `project.*.architecture` is `subj1` plus `subj3`. A prefix query is a set-membership test, and membership is equality once the set is materialised at write time. Cost is N short string keys at depth N, which is what chromem's flattening indexes well. **No store seam widens, no backend changes, no conformance case for a new predicate.**

**⚠The payload is built in at least four places** — `service.go:564`, `service.go:707`, `mine.go:262`, `evalctx.go:146` — and a missed one produces points without prefix keys. Deriving the subject shrinks this risk without removing it: the derivation reads `wing` and `room`, which every one of those sites already sets, so it belongs in one shared helper they all call rather than four independent additions. The conformance test must assert every write path produces prefix keys, verified by deleting one call site and watching it go red. **And because filtering is opt-in, a missed site degrades to "this drawer is not found by a subject filter" rather than "this drawer vanishes from recall"** — which is the difference between a bug and an outage, and is a direct consequence of the additive framing.

Backfill is a **rebuild, not a patch**: bump `schemaVersion`, `New` discards the stale index, boot reconcile replays from SQLite without re-embedding. The mechanism ships already.

## Implementation

Item 3 (the registry) is independent of everything else and useful on its own, so it can go first or last. Item 4 (adjacency) depends on none of the others, is the cheapest, and buys the measurement that justifies the rest. Item 1 is inert until something queries it, which is what makes it safe to land early.

## Consequences

Recall gains a way to scope by hierarchy that it did not have. Filing stops being a guess. `am_traverse` becomes a walk rather than a flood, which is the precondition for ADR-023.

Nothing that works today stops working. An agent that never learns about subjects behaves exactly as it does now.

## Out of Scope

- **How an agent queries** — ADR-023.
- **Temporal range queries.** Prefix decomposition works for hierarchical and set-membership predicates and cannot enumerate an open numeric domain, so `valid_from`/`valid_to` comparison still needs a real predicate at the seam. **Subjects address the namespace dimension, not time.**
- **KG import policy and the current/expired filter** — issue #23.
- **Drawer validity windows** — ADR-010.
- **AS_PATH / provenance chains** (deferred: BACKLOG).
- **A uniform verb set over all objects** (deferred: BACKLOG, re-tagged permanent) — the tool list is an agent's `ls`.

## Risks

**A governed vocabulary needs a governor.** Item 3 moves a cost rather than removing it: someone must review room additions. A rubber-stamped registry is sprawl with documentation attached — the KG's predicate list is that failure with no registry at all. The falsification measures it: if level-3 token count keeps climbing at the old rate, the governance is not happening.

**Entity noise propagates into topology.** Adjacency is only as good as `extractEntities`, and ADR-016's T1 gives the honest number: of 163 ordinary words checked, **47 survived when shouted** and 46 in Title Case. This session's drawers demonstrate it — `BGP`, `TTL`, `Traverse` and `Dynamics` are real entities; `EARLIER`, `ROW` and `DIAGNOSIS` are shouted emphasis. Agents' notes are full of shouted emphasis, because in a note capitalisation marks stress where in prose it marks a name. Mitigation is structural: the co-occurrence **threshold** plus `Dynamics.Strength` means one noisy entity cannot create an edge. The threshold is load-bearing and must be measured, not guessed.

**Derived scope is right until it isn't.** Deriving `shared` from "the wing is craft" is correct for every record in the corpus today, and it will be wrong the first time someone wants a shared memory outside the craft wing. That is what the declared-scope follow-up is for, and until then the derivation is a rule with one exception waiting to happen rather than a general answer.

**Depth is a place to misfile.** Tokens 4+ are optional and free-form, which is where sprawl would enter if it enters. Five tokens is a ceiling, not a target; three is the common case and is derived rather than chosen.

**An inert feature is easy to leave inert.** Because item 1 changes nothing until something queries it, it can ship, be measured at zero, and sit there — the reachability failure this repository keeps shipping, arrived at by a new route. The falsification is the guard: subject filtering has to move MRR on a scoped question set, or item 1 is tidiness rather than capability.

## Rollback

Every item is independently revertable, and items 1–3 and 5 change no existing behaviour. Item 1 leaves populated keys behind; reverting means ignoring them plus a `schemaVersion` bump. Item 4 is the only one whose revert restores different results, and the results it restores are the saturating ones.

## Follow-ups

- **A declared `scope` that overrides the derivation**, once subjects are in use and the derivation's one exception has actually been hit. Strictly additive.
- **Whether the KG should adopt subjects** — its facts are workspace-wide with no import policy, and ~800–900 predicates for ~1021 facts is the same naming failure this ADR governs for drawers. Related to #23.
