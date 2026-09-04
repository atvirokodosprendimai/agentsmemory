# ADR-053: Bound the graph read, and stop the containment edges crowding it out

**Status:** Proposed
**Date:** 2026-09-04
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/ADR-036-a-recall-that-answers.md`, `docs/adr/ADR-044-make-a-small-read-trustworthy.md`, `docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md`, `docs/adr/ADR-013-a-page-of-memories-not-chunks.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/palace/kg.go`, `internal/mcpserver/kg.go`, `internal/mcpserver/drawers.go`
**Enforced-by:** `internal/mcpserver/kg_test.go::TestAGraphAnswerIsBoundedAndSaysWhatItCut`
**Invalidates:** none — checked. ADR-036 put the graph on the read path and its migration minted the containment edges this record hides by default, but nothing in its Decision requires `am_kg_query` to return them; see Context.
**Served-path change:** `am_kg_query` stops returning an answer too large to reach the model — today a walk of `room:wing_craft/gotchas` or of the bare predicate `holds` is spilled to a file the agent never reads — and `am_get_drawer` starts carrying the facts about the memory it returns.

## Context

**One read path in this palace has no bound at all.** `Service.KGQuery`
(`internal/palace/kg.go:831`) is 132 lines and contains zero `Limit(` calls. It
reaches the database through `KGTriplesBySubject`, `KGTriplesByObject` and
`KGTriplesByPredicate`, and none of those limit either. Its `withheld` field
looks like a bound and is not: it counts facts removed by the `status` filter —
current against history — and says nothing about size.

Meanwhile `responseBudget = 40_000` runes (`internal/mcpserver/drawers.go:78`)
bounds `am_search` and `am_list_drawers`, and marks every record it cut. The
graph is the one agent-facing read that escaped it.

**Measured 2026-09-04 against the running local palace** — 3,687 drawers, 1,234
triples, 1,708 entities. Byte figures are the raw sum of `subject`, `predicate`,
`object` and `source_file`; the JSON envelope multiplies them several times over:

| Entry point | Edges returned | Raw bytes |
|-------------|----------------|-----------|
| entity `room:wing_craft/gotchas`, outgoing | **184** | ~16,928 |
| bare predicate `holds` | **587** | ~57,934 |
| the whole graph, for scale | 1,234 | 171,457 |

Both exceed the 40,000-rune budget every other read obeys, and both are already
recorded as reproducing a spill: the `start-here` skill carries `62,952 bytes`
for the traversal and `64,771 bytes` for the predicate sweep, each reproduced
independently three times on 2026-08-29. A spilled tool result does not arrive
smaller — it does not arrive at all, and an empty-looking answer reads as "the
graph holds nothing about this".

⚠**The cause is not agents filing too much.** Splitting the same corpus by
`derived`:

| | edges | max fan-out from one subject |
|---|---|---|
| authored (`derived IS NULL`) | 648 | **10** (`wing_craft.root.ref`) |
| derived (`derived = 1`) | 586 | **184** (`room:wing_craft/gotchas`) |

Every oversized fan-out is derived. The two-tier `must`/`ref` discipline the
skills teach is holding on the authored side — no authored node is anywhere near
the ~35-leaf guidance — while the containment edges ADR-036's migration mints
(`room:<wing>/<room> —holds→ <drawer id>`, one per drawer) grow with the corpus
and answer a question `am_list_drawers` already answers, with a budget.

⚠**And the naive fix breaks the front door.** 580 of the 586 derived edges have a
`room:*` subject; the other **6** are the wing-root spine,
`wing_<name>.root —holds→ room:<name>/llm_init`, minted by `attachWingRootEdge`.
Excluding edges by their `derived` flag would leave **3 of the 6 wing roots
answering empty**: three wings whose ONLY edge is that derived one, because
nobody has authored a tier for them yet — which is the normal state of a young
wing rather than a defect in it. And that address is the
one `start-here` tells every session to walk first. The exclusion therefore keys
on what the edge MEANS (a containment listing, subject `room:*`) rather than on
how it was made.

**The class this record governs, and the members it leaves alone.** A graph read
an agent can call and that returns rows. Enumerated with:

```
grep -rn "Limit(" internal/palace/kg.go internal/palace/graphquery.go \
     internal/palace/graph.go internal/palace/tunnel.go internal/palace/anchors.go
```

Two are bounded: `KGTimeline` at `kgTimelineLimit = 100`
(`internal/palace/kg.go:399`) and `ListAnchors` by a caller-supplied limit
(`internal/palace/anchors.go:226`). Five are not: `KGQuery`, `Traverse`,
`ListTunnels`, `ListHallways` and `FollowTunnels`. **This record fixes `KGQuery`
and names the other four**; they are deferred rather than silently omitted,
because a record that fixes one member of a class and does not say the others
exist reads as though there were none. `Traverse` is the one with a known
reproduction against it — the 62,952-byte walk — and it is deferred only because
its fan-out is a consequence of the same containment edges T2 hides, so its size
should be re-measured after T2 rather than guessed at now.

**`am_get_drawer` returns no facts.** `am_search` renders a `facts` block
(`internal/mcpserver/drawers.go:1140`); the by-id fetch does not, and that is the
wrong way round — the fetch is the call a caller makes after committing to read a
memory. It is also where `start-here`'s incoming-correction check is supposed to
happen, and today that costs a second call the caller has to know to make.

## Existing Primitives Audit

- **`responseBudget` and `headWithin` (`internal/mcpserver/drawers.go:78`, `:87`)** — the rune budget and the head-trimmer that already bound `am_search` and `am_list_drawers`, including the second bound that was once missing (a budget checked only before a record is added is not a bound). **Reuse unchanged.** This record adds callers, not a second budget; a graph read that invented its own number would be a second thing to keep in step.
- **`withheldByBudget` (`internal/mcpserver/drawers.go:84`)** — the key by which a search page names what withheld its hits, written so a second cause could join it. **Reuse**: the graph page's withheld map gains the same key, which is what that constant was left open for.
- **`KGQueryResult.Withheld` / `WithheldStatus` (`internal/palace/kg.go`)** — the existing "this page is filtered and says so" shape, today carrying only the status filter. **Reshape**: it becomes a map keyed by cause, so `status` and `budget` and `containment` are reported side by side rather than one overwriting the other.
- **`kgTimelineLimit = 100` (`internal/palace/kg.go:36`)** — the only bound the graph has today. **Reuse as precedent, not as the value**: a timeline is one entity's history and a query is a fan-out, so they do not share a number, but they should share the shape of naming it as a constant with the reason beside it.
- **`am_list_drawers`** — already answers "what is in this room", with a budget, paging and a wing scope. **Reuse**: it is why the containment edges have no reader worth keeping in `am_kg_query`, and T2's exclusion points there rather than removing the ability to ask.

## Decision

**A graph answer is bounded the way every other read in this palace is bounded,
and the containment edges stop competing with the facts somebody wrote.**

Four changes, in the order they can be proved:

**`am_kg_query` takes `limit` and `cursor`, and the rune budget backstops both.**
A caller may ask for a page and continue it; a page that would exceed
`responseBudget` is cut regardless of the limit asked for, and says so. The
budget is a backstop rather than the primary mechanism because a truncated
fan-out with no continuation is a permanently half-answered question — the caller
cannot tell which 30 of 184 edges they were handed. Both bounds report through
one `withheld` map keyed by cause, so "I filtered history", "I hid containment"
and "I ran out of room" are three different sentences rather than one number.

**Containment edges are excluded by default and asked for by name.** An edge
whose subject matches `room:*` is a listing, not a fact somebody filed;
`am_kg_query` omits it unless `include_containment: true`, and reports how many
it hid. ⚠**The exclusion keys on the subject shape, not on the `derived`
column** — the wing-root spine edges are also derived, and keying on `derived`
would empty three of six wing roots, which is the address every session is told
to walk first. That is the failure mode this clause exists to avoid, and the
measurement that shows it is in Context.

**`am_kg_add` warns when a node passes the fan-out limit; it does not refuse.**
The response carries the node's new edge count and the advice to split by topic.
A refusal was considered and rejected: the write is the moment an agent has the
knowledge in hand, and a refusal there loses the fact to save a shape.

**`am_get_drawer` returns the facts about the memory it returns**, bounded by the
same budget and reporting through the same `withheld` map.

**What would make this fail, and the data exists today.** The criterion is that
no `am_kg_query` response exceeds `responseBudget`, over every entity and every
predicate in the live corpus. Today two entry points fail it — `room:wing_craft/gotchas`
at ~184 edges and the bare `holds` at 587 — so the falsifying data is the corpus
itself and T1's test is written against it rather than against a fixture. The
threshold is valid for the 40,000-rune budget this server ships and for a corpus
of this shape; a client with a different limit is not what it is calibrated to,
which is the same bound ADR-044 already states about the budget.

## Alternatives Considered

- **Budget only, no cursor (the shape `am_search` uses):** cut the page at 40,000 runes, report a withheld count, offer no continuation. Rejected because a fan-out is not a ranked list. A search page is the best N by construction, so the tail is the part you asked to lose; the edges of one entity have no ranking, so an arbitrary 30 of 184 is a silent, unrepeatable subset — and the caller cannot even tell which question they got half an answer to.
- **A hard refusal above N edges, naming the node:** turn the ~35-leaf convention into a served error telling the caller to descend. Rejected as the primary mechanism because it makes the corpus's existing shape unreadable — three rooms exceed it today, and a reader cannot split a node they cannot read. Kept in weakened form as T3's warning on the write side, where the author still has the context to split.
- **Exclude by the `derived` column:** the obvious reading of "hide what the server minted". Rejected on measurement: 3 of 6 wing roots would answer empty, including the address `start-here` prescribes. Recorded because it is the version a later reader will propose.
- **Stop minting per-drawer containment edges at all:** remove the cause rather than hide it. Rejected for now, not on merit — it is a migration over 580 live rows and it forecloses a use nothing has measured. T2's flag keeps the edges reachable, which makes the removal decidable later on evidence rather than now on taste.
- **Move containment to a separate tool:** `am_kg_query` for authored facts, room membership somewhere else. Rejected as a bigger contract change than the problem needs; `am_list_drawers` already IS that tool, so the flag is the smaller step to the same place.
- **Raise `responseBudget` for graph answers:** let the graph return more because its rows are small. Rejected because the budget is about what reaches the model, not about what the server can serialise, and a second number is a second thing to keep in step with the client's real limit.

## Component / Boundary Impact

None — internal to `internal/palace` and `internal/mcpserver`. No component gains
or loses ownership: the palace still owns what a fact is, the MCP layer still owns
what a response may cost. The module map in `README.md` is unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_kg_query` `limit` / `cursor` params | new; `limit` defaults to a named constant, `cursor` opaque and one-way | `internal/mcpserver/kg.go` | every agent walking the graph |
| `am_kg_query` `next_cursor` | new; present only when a page was cut | `internal/mcpserver/kg.go` | the same |
| `am_kg_query` `withheld` | reshaped from `{status: n}` to a map keyed by cause (`status`, `containment`, `budget`) | `internal/palace/kg.go`, `internal/mcpserver/kg.go` | any caller reading `withheld` today |
| `am_kg_query` `include_containment` | new; `false` by default, so a `room:*`-subject edge is hidden unless asked for | `internal/mcpserver/kg.go` | `start-here`, the wing-root walk |
| `am_kg_add` response | gains a fan-out warning field when the node passes the limit | `internal/mcpserver/kg.go` | every writer |
| `am_get_drawer` `facts` | new block, bounded and reporting through the same `withheld` map | `internal/mcpserver/drawers.go` | every by-id fetch |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `boundGraphPage` (T1) | T1 | T2, T4 | No — additive; T2 and T4 both report through it |
| `withheld` keyed by cause (T1) | T1 | T2, T4 | Yes — the field changes shape from `{status: n}`, and a caller reading the old shape sees a different key |
| `isContainmentEdge` (T2) | T2 | T4 | No — T4 applies the same default so a drawer's facts do not carry its room's listing |

## Implementation

See `docs/adr/ADR-053-bound-the-graph-read/tasks/README.md`. Four tasks in two
waves.

## Consequences

- **Positive:** the walk `start-here` prescribes stops spilling. Both reproductions in that skill — 62,952 and 64,771 bytes — are answers the model never received, and a session that walked into one learned nothing while believing it had asked.
- **Positive:** `withheld` keyed by cause means a filtered graph page says which filter, where today three different reasons would arrive as one number or overwrite each other.
- **Positive:** an authored fact stops competing with 184 containment edges for the same budget, which is the ranking-free equivalent of what ADR-036 already refused to do to search — merging facts into the hits.
- **Negative:** `withheld` changes shape, so a caller reading `withheld.status` today reads a map instead. It is a young field and this corpus is its only consumer, but it is a break and it is named rather than smoothed over.
- **Negative:** a cursor is a new concept in this server's read surface — `am_search` deliberately has none. Two paging idioms is a cost, paid because a fan-out and a ranked page are different objects.
- **Neutral:** containment edges stay in the graph and stay queryable with one flag. Nothing is deleted, so the decision to stop minting them can be taken later on evidence.

## Out of Scope

- Bounding `Traverse`, `ListTunnels`, `ListHallways` and `FollowTunnels`, the four other unbounded graph reads (deferred: `docs/adr/BACKLOG.md`)
- Removing the per-drawer containment edges or their migration (deferred: `docs/adr/BACKLOG.md`)
- Any change to how `am_search` pages or to its lack of a cursor (permanent: boundary: a ranked page's tail is the part the caller asked to lose, so a cursor there answers a question nobody has; this record's cursor exists because a fan-out has no ranking)
- Enforcing the fan-out limit as a refusal on `am_kg_add` (permanent: boundary: the write is when the agent holds the knowledge, and a refusal loses the fact to save a shape — the owner chose the warning on 2026-09-04)
- A query-length or request-body limit on `/mcp` (deferred: `docs/adr/BACKLOG.md`)
- Symbol-anchored drawers, automatic recall on context, a token/step benchmark, and derived memory from commits and transcripts (deferred: `docs/adr/BACKLOG.md`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Hiding containment edges breaks a caller that depends on them | Med | High | The exclusion keys on subject shape, and T2's test asserts every wing root still resolves; the flag restores the old answer in one parameter |
| The `withheld` reshape silently changes behaviour for a reader of the old field | Med | Med | T1 changes the field and its description in the same commit; the description is the only route by which a caller learns the shape, and this repository already records what a false description costs |
| A cursor invites callers to page a 587-edge fan-out rather than descend | Med | Low | The default limit is small enough that paging is visibly the expensive path, and the hint names the cheaper one |
| The fan-out warning is a field nobody reads | High | Low | Accepted deliberately: the owner chose warn over refuse, and a warning nobody reads is the known cost of that choice rather than a defect in it |
| `am_get_drawer` gaining facts makes a cheap call expensive | Low | Med | Same budget, and the facts block is subject to it before the drawer's own content is trimmed |

## Rollback

No persistent state changes: no migration runs, no row is written or rewritten,
and the containment edges stay exactly as they are. Undo is per-task and each
step is independently safe — drop the `facts` block from `am_get_drawer` (T4),
drop the fan-out warning (T3), default `include_containment` back to true (T2),
and remove `limit`/`cursor` while restoring `withheld` to a bare count (T1). The
only asymmetry is the `withheld` shape: a caller written against the map form
sees a bare integer again after a rollback, which is why T1 changes the tool
description in the same commit rather than leaving the shape to be discovered.

## Follow-ups

- [ ] Re-measure `Traverse`'s fan-out after T2 lands, since its 62,952-byte reproduction is dominated by the same containment edges, and decide from the new number whether it needs its own bound
- [ ] Decide whether per-drawer containment edges should still be minted at all, once the flag has shown whether anything asks for them
- [ ] Give the four other unbounded graph reads a bound or a written reason they do not need one
