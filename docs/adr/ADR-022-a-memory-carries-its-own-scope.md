# ADR-022: A memory carries its own scope

**Status:** Proposed
**Date:** 2026-08-24
**Owner:** unassigned
**Spec:** None — no spec stage
**Layer:** Storage protocol — what a memory *is*, what it is called, and where it can be seen from. The thinking protocol, how an agent queries, is ADR-023. The split is deliberate: this ADR is a migration and a schema decision, that one is a tool surface and an agent behaviour, and they should be acceptable and reversible independently.
**Cross-references:** ADR-003 (the closet-prior measurement this ADR's whole argument rests on), ADR-010 (validity as a property of the record rather than a parameter of the call — this is the same move applied to scope), ADR-016 (made `entities` reachable on the `Add` path, the precondition for entity-based adjacency below), ADR-006 (a knob that does nothing — why `session` scope may not ship), ADR-023 (the resolution protocol that depends on this one)
**Invalidates:** none — checked (grepped ADR-001..021 for `SEARCH_SCOPE`, `default_wing`, `traverse`, `adjacen`, `hallway`, `taxonomy`: ADR-012, ADR-015, ADR-017 and ADR-021 all *mention* wing scoping, none decides how scope is carried; ADR-016 is the only accepted ADR touching the derived graph, and it decides that entities must be populated, not how adjacency is computed).
**Served-path change:** A drawer declares its own reachability in its own name, so `am_search` stops depending on the caller passing the right `wing`, and `am_traverse` stops returning most of the palace at hop two.

## Context

Scope in this system lives in the **call**, not in the record. Four mechanisms carry it — the `wing` argument, `default_wing` on the registration, `SEARCH_SCOPE`, and the `wing:"*"` escape — and every one is something the *caller* has to get right.

The cost is visible in our own documentation. The always-on bootstrap protocol spends roughly eighty lines teaching an agent to resolve a wing through four ordered fallback rungs (the server registration, `$AGENTSMEMORY_WING`, the `.aiagentmemory` file, the git remote, the directory basename), plus a paragraph on what to do when the rungs disagree. That is a great deal of prose to answer a question the record itself could answer.

And it does not work. Two sessions read the inbox convention and filed handoffs into `wing_to-<project>` rather than `wing_<project>` — **six drawers of real findings into wings no session will ever resolve to, and the writes succeeded.** The server now refuses an inbox item filed into an empty wing, which guards one symptom of the general problem: nothing about a memory says where it belongs, so nothing can check whether it landed there.

**A second face of the same gap: nothing says what a room *means*.** `wing_agentmemories` holds `architecture` (43 drawers), `technical` (27) and `decisions` (432). The boundaries between those three are not written down anywhere, and `Taxonomy` (`internal/palace/service.go:1189`) returns wings, rooms and **counts** — structure with no semantics. Grepping `Description` across `palace.go` and `repo.go` returns nothing. So filing is a guess, every time, by every agent. The session that produced this ADR chose between those three rooms by feel on each of six writes; that is the argument, not a hypothetical.

**A third face: the craft-versus-project decision is a sentence.** The rule keeping `wing_craft` from filling with project facts — where "every wrong entry is wrong everywhere at once" — is enforced by a test an agent is asked to apply to itself: *would this still be true and useful in a repository that shares no code with this one?* Good test, unenforceable, because the property it tests is recorded nowhere.

**The frames that produced this ADR.** IPv6 makes scope part of the address: `fe80::/10` is link-local and never leaves the link, `fd00::/8` is routable internally and never advertised, `2000::/3` is global. A router does not consult policy to discover that a link-local address must not be forwarded — the address says so.

NATS carries the same idea into naming. A subject is dot-separated tokens ordered general-to-specific, `*` matches one token, `>` matches the tail, and — the part that matters — **publishes are always concrete while subscriptions are patterned.** That asymmetry is exactly the write/read split this system needs: a memory is filed at one fully-specified address; a recall asks for a pattern.

DNS supplies the third piece, and it is about governance rather than syntax: **the root is governed and the leaves are free.** IANA decides what `.edu` means; nobody decides what your subdomains mean. That is what keeps a global namespace coherent without a central bottleneck, and it is the answer to the question any vocabulary proposal otherwise begs — who may add a top-level name.

**The topology has a separate defect, and it is quantified.** `Traverse` treats two rooms as adjacent when they **share a wing** (`internal/palace/graphquery.go`). Out of `am_status`, on the live corpus:

| room | wings it spans |
|---|---|
| **diary** | **11 of 11** |
| tooling | 7 |
| decisions | 7 |
| learnings | 6 |

`diary` is in every wing, so `diary` is adjacent to every room in every wing: **the whole palace in one hop.** Two hops saturates — which `wing_agentmemories/tooling` already recorded from the other direction, *"am_traverse walks ROOMS, not memories, and saturates to most of the palace in two hops"*, without naming the cause. An adjacency predicate nearly everything satisfies carries no information. This is not a graph being walked; it is a flood with a hop cap, and `maxHops` is a TTL — what you use when you have no path information at all.

**Why more reachability is the wrong goal here — stated because the network frame actively misleads on this point.** In routing, reach is the objective. In recall it is the disease, and that is our own controlled result: ADR-003 measures the closet prior costing **~0.10 MRR** on the mined-transcript corpus, with 10 of 40 golds displaced — the correct memory still retrieved, then pushed down the page by neighbours a boost lifted. Unrelated records do not remove the answer; they add competitors ahead of it. So what is worth importing from network design is the half operators actually spend their time on — naming, import policy, export policy — and not the advertisement machinery.

## Existing Primitives Audit

- **`wing` and `room` columns** — already a two-level hierarchy, stored as two flat strings with no relationship expressed between them. Reshaped into the first tokens of one address rather than replaced; a wing remains what it is today.
- **`store.Filter map[string]string`** (`internal/store/store.go:44`) — "narrows a search to points whose payload matches every entry, compared as strings". Equality-only, all three backends. **Reused unchanged**, because of the decomposition in Wiring below: this ADR adds no store primitive.
- **`Taxonomy` / `TaxonomyWing`** (`service.go:1189`) — already assembles the wing→room tree with counts. Reshaped: it gains the description field it never had and becomes the registry. The tool that surfaces it does not change shape.
- **`am_list_skills`** — already returns name, description and version for a team-shared object. **The registry precedent is in the tree**; rooms and wings simply never got the field. Reused as the model rather than inventing a second registry idiom.
- **`Dynamics{Strength, Stability, LastActivated, AccessCount}`** (`palace.go:60`) — already stamped on every hallway and tunnel by `initDynamics`, and **nothing routes on it**: LOCAL_PREF, MED and flap history, modelled, populated, never consulted by `Traverse` or `rank`. Reused as the edge weight. Another instance of this repository's signature defect — finished and unreachable.
- **`computeHallwaysForWing` / the `entities` column** — within-wing entity co-occurrence, already derived and rebuilt by `am_recompute_graph`. Reused as the adjacency substrate, and usable **only because ADR-016 shipped**: before it, `Service.Add` wrote no entities, so hallways were structurally unreachable on the agent path.
- **`wing_craft`** — already the default route by convention. Becomes a scope token, not a special-cased wing name.
- **The KG's workspace-wide scope** — kept as the *counter*-example. `am_kg_add`'s own description says facts are returned to every project: an import policy accepting every announcement from every peer. Named here, fixed in issue #23.
- **`MaxKGValueLen = 128`** — the existing attempt to bound a naming field. Reused as evidence rather than as a model; see Risks.

## Decision

### 1. A memory's address is a subject, and the subject replaces four mechanisms

```
<scope>.<wing>.<room>[.<free>...]

project.agentmemories.architecture.adr
shared.craft.verification.gh
project.forumchat.migrations
```

- **Tokens are ordered general to specific.** `*` matches exactly one token; `>` matches the remaining tail.
- **Writes are always fully specified. Only reads may use wildcards.** This is NATS's asymmetry and it is load-bearing: a wildcard write has no meaning, and forbidding it is what keeps the namespace enumerable.

| scope token | analogue | reachable from |
|---|---|---|
| `session` | `fe80::/10` link-local | the writing session only; never persisted past it |
| `project` | `fd00::/8` ULA | its own wing only. **The default.** |
| `shared` | `2000::/3` global unicast | every wing |

Recall becomes a subject pattern: `project.agentmemories.>` is this project, `shared.>` is all craft, `*.*.decisions` is decisions across every wing and scope.

**⚠This retracts item 3 of this ADR's first draft, and says so rather than quietly dropping it.** That draft proposed a separate community tag set as export policy. Subjects make it unnecessary — a tag set and a wildcard grammar are two vocabularies for one question, which is exactly the mistake ADR-010's primitives audit warns against: *reuse the semantics verbatim rather than inventing a second vocabulary.* The first draft also listed `wing`, `room`, `scope` and tags as four separate mechanisms; they are one name. **This revision makes the ADR smaller, and that is the strongest argument for it.**

### 2. The shape of the name is constrained by depth and vocabulary, not by length

- **≤ 5 tokens**, **≤ 32 characters per token.**
- **Tokens 1–2 (`scope`, `wing`) are closed** — a fixed set, changed by a human decision.
- **Token 3 (`room`) is reviewed** — additions allowed, but they are registry entries and someone signs them off.
- **Tokens 4+ are open** — agents coin them freely.

That is DNS's governance model, and the reason for it is measured rather than aesthetic. A total-length cap does not prevent sprawl: `MaxKGValueLen = 128` bounds a predicate's length today and the graph still carries **~800–900 distinct predicates for ~1021 facts** — nearly one per fact, a routing table where every entry is a host route. Length was never the binding constraint. **Depth and a governed vocabulary are.**

### 3. The registry says what each governed token means

`Taxonomy` gains a description per wing and per room: one line, the kind `am_list_skills` already returns for skills. `decisions` and `architecture` and `technical` stop being three words an agent chooses between by feel.

**A gloss is worth only what enforces it, so the enforcement is named here.** A description sitting in a taxonomy nobody reads at write time changes nothing. What makes it bite is the **write tool showing the vocabulary**: `am_add_drawer`'s `room` parameter enumerating the known rooms with their glosses, so the agent sees the choice while making it rather than after. Prose in a document is not load-bearing in this repository; a parameter description generated from the registry is.

### 4. Adjacency stops being "shares a wing"

Two rooms are adjacent when they share entities above a co-occurrence threshold, weighted by `Dynamics.Strength`. The threshold is the whole point: it is what turns a flood into a walk.

### 5. Origin validation

A `shared.*` record whose entities are project-specific is this palace's prefix hijack: a specific announcement from an origin with no authority over it, propagating everywhere. Scope is now the first token of a name and entities are already extracted, so this is a query. A report, not a block, in the first cut.

### Pre-registered falsification

Declared before the work, not adjusted after they are seen:

- **Adjacency.** Fan-out from `diary` at one hop, before and after. Today it reaches every room in the palace. If the entity-threshold version does not *reduce* the reachable set at hop one on the live corpus, item 4 is retracted rather than tuned — a traversal that still saturates is the current behaviour in a more expensive implementation.
- **Scope.** Count of `shared.*` records whose entities are project-specific, before and after. If declaring scope in the name does not reduce misfiling relative to the prose test, item 1's scope half has bought nothing.
- **Registry.** Distinct token count at level 3, before and after, plus the share of writes using a registered token. If agents keep coining new rooms at the same rate once the glosses are shown to them, item 3 is decoration and comes out.

## Alternatives Considered

**Keep scope in the call and improve the protocol prose.** Rejected on evidence: the prose already exists, is loaded into every session automatically, and the misfiling happened anyway — twice, with the writes succeeding. A rule living only in a document is enforced by whoever remembers to read it, and this repository's standing position is that anything which must stay true gets a command whose exit code says so.

**A path (`/`) rather than a subject (`.`).** Rejected as a coin-flip with one asymmetry: dots carry no filesystem connotation, and a path invites the "everything is a file" reading this ADR explicitly declines (see Out of Scope). Nothing else separates them.

**A length cap alone — the first form of this proposal, at 1024 characters.** Rejected on our own evidence, above: the KG bounds length already and sprawled anyway.

**Free-form subjects with no governed vocabulary.** Rejected: that is the KG's predicate list with dots in it. Every entry individually reasonable, the set collectively unqueryable.

**Full BGP — AS_PATH, LOCAL_PREF, best-path selection.** Rejected as over-fitting. Best-path collapses to *one* route while recall is inherently multipath. AS_PATH length has no relationship to semantic relevance, so ranking on it would degrade results. And the convergence machinery exists because ASes are independently administered and disagree; this is one SQLite database with one writer.

**Derive scope from content at read time.** Rejected: it makes every recall pay for a classification, and it cannot be checked at write time, which is where the misfiling happens.

## Component / Boundary Impact

- `db/migrations` — one migration: `drawers` gains the subject and its decomposed prefix keys; `wings`/`rooms` gain descriptions.
- `internal/palace` — `Add`, `WriteDiary`, `Update`, `Mine` compose and validate the subject; `searchFilter` matches on a prefix key; `Taxonomy` carries descriptions; `computeHallwaysForWing` and `Traverse` change adjacency.
- `internal/mcpserver` — write tools take subject tokens and surface the registry in their parameter descriptions.
- `internal/store` — **none.** See below.

## Wiring & Contract Changes

**⚠The implementation unlock, and it is what makes this affordable.** `store.Filter` is equality-only, which reads like a wall against prefix matching. It is not one. **Do not store the path — store every prefix of it, each as its own key:**

```
subject: project.agentmemories.architecture.adr
  subj1 = "project"
  subj2 = "project.agentmemories"
  subj3 = "project.agentmemories.architecture"
  subj4 = "project.agentmemories.architecture.adr"
```

`project.agentmemories.>` is then **one equality match on `subj2`**. `project.*.architecture` is an equality match on `subj1` plus one on `subj3`. A prefix query is a set-membership test, and membership is equality once the set is materialised at write time. Cost is N short string keys at depth N, which is precisely what chromem's flattening indexes well. **No store seam widens, no backend changes, no conformance case for a new predicate.**

**⚠The riskiest line in this ADR is not the addressing.** The point payload is built in **at least four places** — `service.go:564`, `service.go:707`, `mine.go:262`, `evalctx.go:146`. Miss one and those drawers carry no subject keys, the filter never matches them, and **they vanish from recall the day it ships, with no error and no failing test.** This is exactly the defect class this repository keeps shipping. The conformance test must assert every write path produces decomposed keys, and must be verified by deleting one call site and watching it go red.

**⚠`searchFilter` opens `if q.Wing == "" && q.Room == "" { return nil }`, and nil means *search everything*.** Leave that early return and an unscoped query silently returns records the scope was supposed to hide.

Backfill is a **rebuild, not a patch**: bump `schemaVersion`, `New` discards the stale index, boot reconcile replays from SQLite without re-embedding. The mechanism ships already — chromemvec's own comment records this failure happening once, when a v1 directory met a v2 filter and silently returned an empty page.

## Implementation

Item 4 (adjacency) depends on none of the others, is the cheapest, and buys the measurement that justifies the rest — so it goes first. Item 3 (the registry) is independently useful even if items 1–2 are rejected, because the glosses fix a filing problem that exists today under the current flat rooms.

## Consequences

Recall stops depending on the caller. Filing stops being a guess. `am_traverse` becomes a walk rather than a flood, which is the precondition for ADR-023: a referral saying "try `diary`" carries zero bits while `diary` spans every wing.

**⚠Promotion changes identity.** With scope as the first token, moving a memory from `project` to `shared` changes its address and therefore its id. That is consistent with ADR-010's supersede-do-not-overwrite — the promotion is a new record naming the one it replaced — but it must be stated rather than discovered by whoever tries to promote a lesson to craft.

## Out of Scope

- **How an agent queries** — ADR-023.
- **Temporal range queries.** Prefix decomposition works for hierarchical and set-membership predicates and cannot enumerate an open numeric domain, so `valid_from`/`valid_to` comparison still needs a real predicate at the seam. **Subjects address the namespace dimension, not time**, and nobody should later read this ADR as having solved both.
- **KG import policy and the current/expired filter** — issue #23.
- **Drawer validity windows** — ADR-010.
- **AS_PATH / provenance chains** (deferred: BACKLOG).
- **A uniform verb set over all objects** — the strong "everything is a file" reading. This ADR takes the useful half, one namespace, and declines the other: a model discovers capability by reading the tool list, so the tool list is an agent's `ls`, and three generic verbs would hide what forty named tools advertise (deferred: BACKLOG, re-tagged permanent).

## Risks

**A governed vocabulary needs a governor.** Items 2 and 3 move a cost rather than removing it: someone must review room additions. If nobody does, additions either stall or get waved through, and a rubber-stamped registry is sprawl with documentation attached — the KG's predicate list is exactly that failure with no registry at all. The falsification measures this directly: if level-3 token count keeps climbing at the old rate, the governance is not happening.

**Entity noise propagates into topology.** Adjacency is only as good as `extractEntities`, and ADR-016's own T1 gives the honest number: of 163 ordinary words checked, **47 survived when shouted** and 46 survived in Title Case. This session's drawers demonstrate it — extracted entities include `BGP`, `TTL`, `Traverse` and `Dynamics`, which are real, alongside `EARLIER`, `ROW` and `DIAGNOSIS`, which are shouted emphasis. Agents' notes are full of shouted emphasis, because in a note capitalisation marks stress where in prose it marks a name. Mitigation is structural: the co-occurrence **threshold** plus `Dynamics.Strength` means one noisy entity cannot create an edge. But the threshold is load-bearing and must be measured, not guessed.

**Three scopes may be two.** `session` has no consumer until something writes scratch memories, and shipping a value nothing produces is a knob that does nothing (ADR-006). It may be right to ship `project` and `shared` only.

**A default that hides memories.** `project` as the default is right for precision and wrong for anyone expecting the old behaviour. The migration must decide what existing records get: the safe answer is `project` for everything except `wing_craft`, which becomes `shared`.

**Depth is a place to misfile.** Every additional token is one more decision an agent must get right at write time, and this ADR's own Context is a list of filing decisions agents got wrong. Five tokens is a ceiling, not a target; three is the common case.

## Rollback

Items 2–5 are independently revertable. Item 1 leaves populated columns behind; reverting means ignoring them, which is safe, plus a `schemaVersion` bump to rebuild the index without the prefix keys.

## Follow-ups

Whether the KG should adopt subjects too — its facts are workspace-wide with no import policy, and a predicate list of ~800–900 for ~1021 facts is the same naming failure this ADR governs for drawers. Related to #23, not the same decision.
