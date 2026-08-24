# ADR-023: Resolve by referral, remember the misses

**Status:** Proposed
**Date:** 2026-08-24
**Owner:** unassigned
**Spec:** None — no spec stage
**Layer:** Thinking protocol — the control loop an agent runs against the palace, and its token budget. What a memory *is* and where it can be seen from is the storage protocol, ADR-022. Kept apart because this one is a tool surface and an agent behaviour where that one is a migration, and they should be acceptable and reversible independently.
**Cross-references:** ADR-022 (**hard dependency** — it supplies both the address a referral names and the gloss a referral carries, and referrals are worthless without its adjacency fix), ADR-001 (recall answers or abstains — the abstain decision this ADR needs and does not duplicate; note its T3 block, addressed below), ADR-019 (the agent sees a quarter of the memory — the payload budget this ADR spends differently), ADR-013 (a page of memories, not chunks)
**Invalidates:** none — checked (grepped ADR-001..022 for `snippet`, `limit`, `abstain`, `recall_stats`, `search_events`: ADR-019 and ADR-013 decide what a *page* contains and neither decides how many round trips produce one; ADR-001 decides whether to answer, not how to reach the candidates).
**Served-path change:** `am_search` gains a referral mode that returns tens of tokens of *where to look next* instead of ~500 tokens of content, and a query that found nothing is remembered with its reason instead of being re-run blind by the next session.

## Context

Recall today is a single shot. `DefaultSearchLimit` = 5 hits at `DefaultSnippetChars` = 400 characters each — roughly 2,000 characters, on the order of 500 tokens — returned in one call, with no way for the caller to steer between "here is the query" and "here are five memories". If the page is wrong, the only recourse is another blind query at the same price.

**DNS solves exactly this problem and solves it by not answering.** A resolver asking the root for `www.example.com` does not get an address; it gets a **referral** — *ask the `.com` servers* — and the referral is tiny. It descends: root, TLD, authoritative, each step cheap, each step narrowing, and only the last one carries the answer. The expensive payload is paid once, at the end, after the search space has already collapsed.

That is the shape this ADR adopts, and it is the natural complement to ADR-022. ADR-022 borrows from BGP: **who can reach what, under what policy.** But BGP routes to a *known destination*, and recall has none — the packet carries an address while a query carries meaning, and *which wing holds the answer* is the problem rather than the input. DNS is the missing half: the name-to-location resolution that produces the destination BGP then reaches. The two frames fit together because in the real stack they are two different layers doing two different jobs.

**With ADR-022's subject addressing the analogy stops being an analogy.** A DNS name and a NATS subject are both dot-separated hierarchical tokens, and in both a referral is *here is a prefix, ask closer to the leaf*. One difference, stated so nobody trips on it: DNS resolves **right to left** — the root is the rightmost, implicit dot — while a subject reads **left to right**. Opposite direction, identical mechanism. That correspondence is what lets this ADR specify a resolution protocol without specifying an address format: ADR-022 owns the names, this one owns the walk.

**The second thing DNS has that we lack is a first-class negative answer.** `NXDOMAIN` means *this name does not exist* — not "here are the five nearest names". And RFC 2308 makes it cacheable: a resolver remembers the absence, with a TTL, so the next query for a name nobody has does not re-walk the tree.

**We already collect that signal and we already throw it away.** `search_events` records every query and whether it was answered. `groupSuggestions` collapses the unanswered ones across paraphrasings into `MemorySuggestion`s — real work, already written, already correct. And the only consumer in the tree is the `am_recall_stats` **report**: a human-facing summary of "the memories the team looked for and does not have". Nothing feeds it back into anything. **The negative cache is being written and never read** — the same defect class as `Dynamics` in ADR-022 and the eval arm before it: finished, and unreachable from the path that would use it.

**And this is what "learn what not to do, and why" needs.** A palace that only records what worked is a repository: it keeps the merged branch and loses the four that were tried first. The diary already fights this by convention — entries in `wing_agentmemories/diary` carry `★` and `⚠` markers and sentences like *"I had to retract the backfill claim I made one turn earlier"* — but that is an author remembering to write it, retrievable only by someone who already suspects it exists.

## Existing Primitives Audit

- **`search_events` + `recordSearch`** (`internal/palace/recallstats.go`) — every query, and whether it was answered, already persisted. This *is* the negative cache. Reused wholesale; nothing new is stored.
- **`groupSuggestions` / `MemorySuggestion` / `sameAsk` / `significantTokens`** — already collapse unanswered queries across paraphrasings, with a considered rule for when two asks are the same ask. Reused as the negative cache's **lookup key**. Writing a second notion of "these two queries are the same question" when this one exists and has been thought about would be the second-vocabulary mistake ADR-010's audit warns against.
- **`am_recall_stats`** — stays exactly as it is. It is the report; this ADR adds a consumer of the same data, it does not change the reporting.
- **ADR-001's calibration and abstain verdict** — reused verbatim, not re-derived. See the block below.
- **`buildGraph`'s room→wings fold** — the referral substrate. Usable **only after ADR-022**: today it folds into an adjacency where `diary` spans 11 of 11 wings.
- **ADR-022's subject grammar and registry** — the referral's *address* and its *gloss*, both supplied whole. This ADR invents no naming scheme of its own: a referral is a subject prefix, descent is token concatenation, and the one-line description comes from the same `Taxonomy` the write tools read. Reusing it rather than defining a parallel referral identifier is the point — two vocabularies for one namespace is the mistake ADR-010's audit names.
- **`snippet_chars` / `limit`** — the existing payload dials. A referral mode is a new *shape* of response, not a new budget mechanism.
- **`Dynamics.AccessCount`** — how often an edge was used. Notably *not* whether using it helped; see Risks.

## Decision

**1. A resolve step returns referrals, not content.** A referral is **a subject prefix, its registry gloss, and a count** — tens of tokens:

```
project.forumchat.migrations.>   schema changes and their ordering   (118)
project.forumchat.web.>          the HTTP surface and its templates  (535)
```

Three referrals cost roughly 60–90 tokens against ~500 for a full page.

**The gloss is not decoration, and it is why ADR-022's registry matters here.** A bare prefix makes the agent guess what `migrations` holds; a glossed one makes the descent **decidable without a second round trip**. On an ADR judged by total tokens including turn overhead, one avoided round trip is worth more than every byte the gloss costs.

**2. The caller picks one to three and descends by appending a token.** Breadth three, **depth capped at three**. Descent is literally subject concatenation — `project.forumchat` + `.migrations` — so the referral protocol needs no addressing scheme of its own; ADR-022's is the whole mechanism. This is a beam the agent steers, not a recursion the server runs: the model chooses which referrals to follow, because the model is the only party that knows what it is actually looking for.

**3. The final descent returns drawers as today.** The expensive payload is paid once, after the space has collapsed. Nothing about the page format changes — ADR-013 and ADR-019 continue to decide what a page contains.

**4. A miss is recorded with its reason and a TTL.** When a query resolves to nothing above threshold, that is stored against the `groupSuggestions` key. A later query that collapses to the same ask gets the cached miss — *"looked for, not found, on this date, and here is what was checked"* — for the price of the cache entry rather than a full search.

**5. The TTL is invalidated by writes, not only by time.** DNS can rely on time alone because it does not observe the zone changing. We do: any write into a wing the cached miss covers clears it. A negative cache that outlives the memory that would have answered it is worse than no cache, because it converts "we do not have this" into "we will not look".

**6. Valence is authored, never inferred.** A memory may carry an explicit outcome — *this worked* / *this failed, because* — set by the agent that learned it. **It is not derived from access counts.** And the retrieval requirement is the subtle half: **a negative memory must surface for the query that would lead someone into the mistake, not for a query about the mistake.** "Do not use X for Y" has to come back for *"how do I do Y"*. A warning that is only findable once you already suspect it is a warning nobody reads.

**Pre-registered falsification.** One number, declared before the work: **total tokens to a correct answer**, iterative versus one-shot, over the existing eval corpus using `ArmProduction`. Total means *everything* — referral payloads, the final page, and the per-turn overhead of each extra round trip, because a round trip here is a model turn and not a microsecond. **If referral resolution does not beat one-shot search on tokens-to-correct-answer, this ADR is withdrawn rather than tuned.** It is a token-economy argument; if the tokens do not come out ahead, there is no argument left.

Second, smaller: the negative cache must show a **hit rate above zero** on replayed real query logs. `am_recall_stats` already surfaces repeated unanswered queries, so the data to check this exists before any code is written — and if repeats are rare, item 4 is solving a problem we do not have.

## The ADR-001 dependency, stated rather than assumed

`NXDOMAIN` sounds like ADR-001's abstain, and it partly is — but ADR-001 is **BLOCKED, not accepted**: its T3 gate ran on the real corpus and exited 1, no threshold cleared both bars, and T3's own preflight disqualified the corpus (a retrieval ceiling of 100% in-pool, which is arithmetic rather than retrieval). So a *calibrated* "we do not have this" does not exist today and this ADR must not pretend otherwise.

The separation that makes this shippable anyway:

- **Recording a miss needs no calibration.** "Nothing came back above threshold" is observable now, and `search_events` already observes it. Item 4 ships.
- **Asserting confidently that the palace does not hold something needs calibration.** That is ADR-001's job and it stays there. Until it unblocks, a cached miss is phrased as *what was looked for and not found*, which is a fact about a past search, not a claim about the corpus.

The distinction is not pedantic: one is a log, the other is an assertion, and only the second can be wrong in a way that costs someone an answer that was there all along.

## Alternatives Considered

**Just raise `limit` and `snippet_chars`.** Rejected: it spends more tokens to make the same unsteered guess, and ADR-019 already established the agent sees a fraction of each memory. More of a blind page is not fewer wrong pages.

**Server-side recursion — the server walks the referrals itself and returns the final page.** Rejected, and this is the one worth being explicit about, because it is the obvious implementation. The server does not know what the agent is looking for; the whole value of a referral is that the *model* chooses the branch using context the server cannot see. Server-side recursion is just today's one-shot search with extra hops, and it would cost more while steering no better.

**Full DNS — zones, delegation, authority records, SOA.** Rejected as over-fitting, the same judgement ADR-022 makes about full BGP. Delegation and authority solve *distributed administration*: many parties owning parts of one namespace and needing to agree. This is one database with one writer. Referral and negative caching are the two mechanisms that carry over; the rest is machinery for a problem we do not have.

**Infer valence from `Dynamics.AccessCount`.** Rejected. Frequently retrieved is not the same as useful, the two diverge hardest exactly where it matters — a memory retrieved constantly because it keeps *almost* answering — and outcome attribution is a credit-assignment problem where recommender systems reliably get gamed. Authored valence is cheaper and honest about who is making the claim.

## Component / Boundary Impact

- `internal/palace` — a referral projection over `buildGraph`; a negative-cache read on the `Search` path, keyed by `groupSuggestions`.
- `internal/mcpserver` — a referral mode on `am_search`; the cached-miss response shape.
- `internal/store` — none. The referral projection reads what the graph already folds.
- `db/migrations` — one, for the cache TTL and the authored valence field.

## Wiring & Contract Changes

**Additive throughout.** Referral mode is opt-in; default `am_search` behaviour is unchanged. Nothing here breaks an existing caller.

⚠**The reachability trap for this ADR specifically.** A test asserting that referral mode "returns referrals" passes happily while the referrals are useless. The test that earns its keep measures **fan-out reduction**: a referral step must shrink the candidate set. Verify it by pointing referral mode at the pre-ADR-022 adjacency and watching it fail, because against `diary`-spans-everything it *should* fail.

## Implementation

Item 4 (record misses) is independent of everything else and of ADR-022, and it reads data that already exists — so it ships first and cheapest. Items 1–3 are gated on ADR-022's adjacency fix. Item 6 is independent of both.

## Consequences

A steered descent costs fewer tokens than repeated blind pages, and the agent controls where it goes. Repeated fruitless searches stop being repeated. Failed approaches become retrievable by the query that would repeat them.

## Out of Scope

- **Subject addressing, scope, the token registry, adjacency** — all ADR-022. (Its first draft also proposed community tags; subjects retract them, so nothing here depends on that idea.)
- **The abstain calibration** — ADR-001.
- **KG resolution.** `am_kg_query` is an exact entity lookup and `am_search` has no KG access, so a fact cannot be found without already knowing its entity name. That is a real gap and a feature rather than a protocol change (deferred: BACKLOG).
- **Automatic outcome attribution** (deferred: BACKLOG) — see Alternatives.

## Risks

**Each hop is a model turn.** This is the risk that could sink the whole ADR, so it is named first. A DNS resolver descends in microseconds; an agent pays a full turn — latency, and the conversation context re-sent — per hop. Three cheap referral steps can easily cost more in total than one 500-token page. This is exactly why the falsification measures *total* tokens including turn overhead, and why depth is capped at three rather than left open.

**Bad referrals are worse than no referrals.** A referral that misdirects costs a full turn to discover. That is the entire dependency on ADR-022: with today's adjacency, a referral to `diary` narrows nothing and the descent is three turns of noise.

**A stale negative cache silently withholds an answer.** This is the failure that would do real damage — it converts "not found" into "not looked for". Item 5 (write-invalidation) is the mitigation and it is not optional; shipping 4 without 5 is a bug with a TTL on it.

**Authored valence is as good as its authors.** Agents are not reliable narrators of their own failures, and an agent that summarises rather than records will write "obsolete" where the reason mattered. The mitigation is the same one ADR-010 reaches for: a required free-text reason is a weak guarantee, and it is still the difference between a field nobody fills and a field somebody can be asked about.

## Rollback

Every item is independently revertable and none changes existing behaviour by default. The negative cache can be disabled by not reading it; the rows stay, and `am_recall_stats` keeps reporting them exactly as it does today.

## Follow-ups

Whether referral resolution should also cover the KG, which currently cannot be searched at all — only looked up by exact entity name.
