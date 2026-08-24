# ADR-022: A memory carries its own scope

**Status:** Proposed
**Date:** 2026-08-24
**Owner:** unassigned
**Spec:** None — no spec stage
**Layer:** Storage protocol — what a memory *is* and where it can be seen from. The thinking protocol, how an agent queries, is ADR-023. The split is deliberate: this ADR is a migration and a schema decision, that one is a tool surface and an agent behaviour, and they should be acceptable and reversible independently.
**Cross-references:** ADR-003 (the closet-prior measurement this ADR's whole argument rests on), ADR-010 (validity as a property of the record rather than a parameter of the call — this is the same move applied to scope), ADR-016 (made `entities` reachable on the `Add` path, which is the precondition for entity-based adjacency below), ADR-023 (the resolution protocol that depends on this one)
**Invalidates:** none — checked (grepped ADR-001..021 for `SEARCH_SCOPE`, `default_wing`, `traverse`, `adjacen`, `hallway`: ADR-012, ADR-015, ADR-017 and ADR-021 all *mention* wing scoping, none of them decides how scope is carried; ADR-016 is the only accepted ADR that touches the derived graph, and it decides that entities must be populated, not how adjacency is computed).
**Served-path change:** A drawer declares its own reachability, so `am_search` stops depending on the caller passing the right `wing`, and `am_traverse` stops returning most of the palace at hop two.

## Context

Scope in this system lives in the **call**, not in the record. Four mechanisms carry it — the `wing` argument, `default_wing` on the registration, `SEARCH_SCOPE`, and the `wing:"*"` escape — and every one of them is something the *caller* has to get right.

The cost is visible in our own documentation. The always-on bootstrap protocol spends roughly eighty lines teaching an agent to resolve a wing through four ordered fallback rungs (the server registration, `$AGENTSMEMORY_WING`, the `.aiagentmemory` file, the git remote, the directory basename), plus a paragraph on what to do when the rungs disagree. That is a great deal of prose to answer a question the record itself could answer.

And it does not work. Two sessions read the inbox convention and filed handoffs into `wing_to-<project>` rather than `wing_<project>` — **six drawers of real findings into wings no session will ever resolve to, and the writes succeeded.** The server now refuses an inbox item filed into an empty wing, which guards one symptom of the general problem: nothing about a memory says where it belongs, so nothing can check whether it landed there.

The gap has a second face. The craft-versus-project decision — the one keeping `wing_craft` from filling with project facts, where "every wrong entry is wrong everywhere at once" — is enforced by a **sentence an agent is asked to apply to itself**: *would this still be true and useful in a repository that shares no code with this one?* That is a good test and it is unenforceable, because the property it tests is recorded nowhere.

**The frame that produced this ADR.** IPv6 makes scope part of the address: `fe80::/10` is link-local and never leaves the link, `fd00::/8` is routable internally and never advertised, `2000::/3` is global. A router does not consult policy to discover that a link-local address must not be forwarded — the address says so. The palace has the same three scopes (session scratch, project memory, shared craft) and encodes none of them in the thing being scoped.

**The topology has a second, separate defect, and it is quantified.** `Traverse` treats two rooms as adjacent when they **share a wing** (`internal/palace/graphquery.go`). Out of `am_status`, on the live corpus:

| room | wings it spans |
|---|---|
| **diary** | **11 of 11** |
| tooling | 7 |
| decisions | 7 |
| learnings | 6 |

`diary` is in every wing, so `diary` is adjacent to every room in every wing: **the whole palace in one hop.** Two hops saturates — which `wing_agentmemories/tooling` already recorded from the other direction, *"am_traverse walks ROOMS, not memories, and saturates to most of the palace in two hops"*, without naming the cause. An adjacency predicate that nearly everything satisfies carries no information. This is not a graph being walked; it is a flood with a hop cap, and `maxHops` is a TTL. A TTL is what you use when you have no path information at all.

**Why more reachability is the wrong goal here — stated because the network frame actively misleads on this point.** In routing, reach is the objective. In recall it is the disease, and that is our own controlled result: ADR-003 measures the closet prior costing **~0.10 MRR** on the mined-transcript corpus, with 10 of 40 golds displaced — the correct memory still retrieved, then pushed down the page by neighbours a boost lifted. Unrelated records do not remove the answer; they add competitors ahead of it. So what is worth importing from network design is the half operators actually spend their time on — import policy, export policy, communities, `NO_EXPORT` — and not the advertisement machinery.

## Existing Primitives Audit

- **`Dynamics{Strength, Stability, LastActivated, AccessCount}`** (`internal/palace/palace.go:60`) — already stamped on every hallway and every tunnel by `initDynamics`, and **nothing routes on it**. That is LOCAL_PREF, MED and flap history: modelled, populated, and never consulted by `Traverse` or by `rank`. Reused as the edge weight, not reinvented. It is also another instance of this repository's signature defect — a capability finished and unreachable — alongside the eval arm and the config field the protocol already records.
- **`computeHallwaysForWing` / the `entities` column** — within-wing entity co-occurrence, already derived, already rebuilt by `am_recompute_graph`. Reused as the adjacency substrate. **Usable only because ADR-016 shipped**: before it, `extractEntities` was called from `mine.go` alone and `Service.Add` wrote no entities, so hallways were structurally unreachable on the agent path.
- **Tunnels (explicit + entity-derived)** — the cross-wing edges. Reshaped: they stop being undifferentiated links and start carrying policy.
- **The `wing` column** — the AS number. Kept exactly as is. This ADR does not change what a wing is.
- **`wing_craft`** — already the default route by convention. Formalised, not replaced.
- **The KG's workspace-wide scope** — kept as the *counter*-example rather than the model. `am_kg_add`'s own description says facts are returned to every project in the workspace: an import policy that accepts every announcement from every peer. Named here, fixed in issue #23, not here.
- **`Repo.Update`'s entity refresh** — already keeps `entities` in step with `content` in one statement, with a comment explaining why a wrong graph is worse than an empty one. Scope must be maintained with the same discipline, in the same place.

## Decision

**1. Scope becomes a field on the record, three values mirroring the IPv6 scopes.**

| value | analogue | reachable from |
|---|---|---|
| `session` | `fe80::/10` link-local | the writing session only; never persisted past it |
| `project` | `fd00::/8` ULA | its own wing only. **The default.** |
| `shared` | `2000::/3` global unicast | every wing |

The four-rung resolution protocol stops being load-bearing for *recall*: a `project` memory is unreachable from another wing whatever the caller passes, and a `shared` memory is reachable whatever they pass. Resolution still decides where a *write* lands, which is a smaller and far more checkable job.

**2. Adjacency stops being "shares a wing".** Two rooms are adjacent when they share entities above a co-occurrence threshold, weighted by `Dynamics.Strength`. The threshold is the whole point: it is what turns a flood into a walk.

**3. Communities — an export tag set on the record — generalise the current three-state hack.** Today a memory is in a wing (invisible elsewhere), in `wing_craft` (visible everywhere), or in the KG (workspace-wide, unconditionally): three unrelated mechanisms for one question. A tag set makes it one mechanism and, critically, **reversible** — re-tagging is an edit, where today moving a memory between wings means a new id and a re-embed.

**4. Origin validation.** A `shared` record whose content names project-specific entities is this palace's prefix hijack: a specific announcement from an origin with no authority over it, propagating everywhere. Scope is now a field and entities are already extracted, so this is checkable — a report, not a block, in the first cut.

**Pre-registered falsification.** Two numbers, declared before the work and not adjusted after they are seen:

- **Adjacency.** Fan-out from `diary` at one hop, before and after. Today it reaches every room in the palace. If the entity-threshold version does not *reduce* the reachable set at hop one on the live corpus, item 2 is retracted rather than tuned — a traversal that still saturates is the current behaviour in a more expensive implementation.
- **Scope.** Count of `shared`-scope records whose entities are project-specific, before and after. If declaring scope explicitly does not reduce misfiling relative to the prose test, item 1 has bought nothing and should be withdrawn; the protocol paragraph is cheaper than a migration.

## Alternatives Considered

**Keep scope in the call and improve the protocol prose.** Rejected on evidence: the prose already exists, is loaded into every session automatically, and the misfiling happened anyway — twice, with the writes succeeding. A rule that lives only in a document is enforced by whoever remembers to read it, and this repository's standing position is that anything which must stay true gets a command whose exit code says so.

**Full BGP — AS_PATH, LOCAL_PREF, best-path selection.** Rejected as over-fitting the analogy. Best-path selection collapses to *one* route while recall is inherently multipath (five hits across possibly three wings). AS_PATH length has no relationship to semantic relevance, so ranking on it would actively degrade results. And the convergence machinery — route reflectors, full mesh, flap damping — exists because ASes are independently administered and disagree; this is one SQLite database with one writer, so importing any of it is pure cost. Communities survive into the Decision; the rest does not.

**Derive scope from content at read time** rather than storing it. Rejected: it makes every recall pay for a classification, and it cannot be checked at write time, which is where the misfiling happens.

## Component / Boundary Impact

- `db/migrations` — one migration: `drawers` gains `scope` and a community tag column.
- `internal/palace` — `Add`, `WriteDiary`, `Update`, `Mine` set scope; `searchFilter` consults it; `computeHallwaysForWing` and `Traverse` change adjacency.
- `internal/mcpserver` — `scope` becomes an optional argument on the write tools, defaulting to `project`.
- `internal/store` — one filter key, through the existing `Filter map[string]string` seam.

## Wiring & Contract Changes

**Additive:** `scope` on the write tools; a `scope` filter on recall.

**⚠The riskiest line in this ADR is not any of the above.** The point payload is built in **at least four places** — `service.go:564`, `service.go:707`, `mine.go:262`, `evalctx.go:146`. Miss one and those drawers carry no scope, the filter never matches them, and **they vanish from recall the day it ships, with no error and no failing test.** This is precisely the defect class this repository keeps shipping, and the same hazard the AOF work already priced. The conformance test must assert that every write path produces a scoped point, and it must be verified by deleting one call site and watching the test go red.

⚠`searchFilter` opens `if q.Wing == "" && q.Room == "" { return nil }`, and nil means *search everything*. Leave that early return and an unscoped query silently returns records the scope was supposed to hide.

Backfill is a **rebuild, not a patch**: bump `schemaVersion`, `New` discards the stale index, boot reconcile replays from SQLite without re-embedding. The mechanism ships already — chromemvec's own comment records this failure happening once, when a v1 directory met a v2 filter and silently returned an empty page.

## Implementation

Sequenced so each step is independently verifiable. Item 2 (adjacency) depends on none of the others, is the cheapest, and buys the measurement that justifies the rest — so it goes first.

## Consequences

Recall stops depending on the caller. `am_traverse` becomes a walk rather than a flood, which is the precondition for ADR-023: a referral that says "try `diary`" carries zero bits while `diary` spans every wing. Craft-wing hijacks become detectable rather than merely discouraged.

## Out of Scope

- **How an agent queries** — ADR-023.
- **KG import policy and the current/expired filter** — issue #23.
- **Drawer validity windows** — ADR-010.
- **AS_PATH / provenance chains** (deferred: BACKLOG).
- **A uniform verb set over all objects** — the "everything is a file" reading. The useful half is a single *namespace*, which addressing gives; a single *verb* set would collapse forty discoverable tools into three undiscoverable ones, and the tool list is an agent's `ls` (deferred: BACKLOG).

## Risks

**Entity noise propagates into topology.** Adjacency is only as good as `extractEntities`, and ADR-016's own T1 measurement is the honest number: of 163 ordinary words checked, **47 survived when shouted** and 46 survived in Title Case. This session's drawers demonstrate it — extracted entities include `BGP`, `TTL`, `Traverse` and `Dynamics`, which are real, alongside `EARLIER`, `ROW` and `DIAGNOSIS`, which are shouted emphasis. Agents' notes are full of shouted emphasis, because in a note capitalisation marks stress where in prose it marks a name. Mitigation is structural rather than hopeful: the co-occurrence **threshold** plus `Dynamics.Strength` means one noisy entity cannot create an edge — it takes repeated co-occurrence. But the threshold is now load-bearing and must be measured, not guessed.

**Three scopes may be two.** `session` has no consumer until something writes scratch memories, and shipping a value nothing produces is a knob that does nothing (ADR-006). It may be right to ship `project` and `shared` only, and add `session` when something needs it.

**A default that hides memories.** `project` as the default is right for precision and wrong for anyone expecting the old behaviour. This is a real read-path behaviour change, and the migration must decide what existing records get: the safe answer is `project` for everything except `wing_craft`, which becomes `shared`.

## Rollback

Items 2–4 are independently revertable. Item 1 leaves a populated column behind; reverting means ignoring it, which is safe, plus a `schemaVersion` bump to rebuild the index without the filter key.

## Follow-ups

Whether the KG should adopt the same scope field, given its facts are workspace-wide today with no import policy at all. Related to #23, not the same decision.
