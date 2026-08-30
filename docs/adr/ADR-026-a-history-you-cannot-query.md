# ADR-026: Ask for current facts, and stop paying for the dead ones

**Status:** Accepted
**Date:** 2026-08-25
**Owner:** unassigned
**Spec:** None — no spec stage; grounded in probes against the live graph and `EXPLAIN QUERY PLAN` against the real table, recorded inline.
**Cross-references:** ADR-004 (supersession is the graph's acceptance criterion — this ADR **amends one clause of its Out of Scope**, approved by M 2026-08-25), ADR-010 (supersede, do not overwrite — owned the drawer half and the `reason` field; **superseded by ADR-038 on 2026-08-27**, which now owns both), ADR-006 (a knob that does nothing must say when), ADR-007 (no number without its population), ADR-014 (the shipped default is the measured one), ADR-024 (precedent for a default change reaching every caller), `internal/palace/kg.go:366` (raw temporal storage), `:418` (`KGQuery`), `:532` (`Current`), `:553` (`inEffectAt`), `db/migrations/00010_kg.sql`, issue #23
**Numbering:** next free after ADR-025. PR #24 still claims ADR-022 and ADR-023; ADR-024 and ADR-025 are taken on `main`.
**Invalidates:** none — checked. It **amends** one clause of ADR-004's Out of Scope: the bar, pre-registered arm, interval rule, case floor, verdict branches and the entire write path are untouched. It closes items 1 and part of 3 of issue #23; the rest is listed in Follow-ups with triggers.
**Served-path change:** **Yes — a default change.** `am_kg_query` returns open-ended facts by default instead of every fact ever recorded, and any response that filtered something reports what it withheld and the parameter that restores it. `am_kg_query` also gains a `predicate` filter and returns three fields it already stores. `am_search`, ranking, drawers and the write tools are untouched.

## Amendment to ADR-004 — decided

ADR-004 is **Accepted** and its Out of Scope list reads:

> - Any change to `kg_add` / `kg_invalidate` / `kg_query` behaviour (**permanent**: the write path is what is being judged, and changing it mid-measurement invalidates the measurement)

**Decided by M, 2026-08-25: amend it to name only `kg_add` / `kg_invalidate`.** M's words: *"i vote to modify kg_query"*. The rejected alternative was leaving the clause intact and closing issue #23 as blocked — recorded because a decision without its alternative reads as preference and gets reopened.

The reasoning put to M, and it is **an inference rather than a reconstruction of intent**: the clause's own rationale names only the write path, and ADR-004's founding premise is that *"`Service.Search` … never touches a triple"*. No eval arm reads the graph, so a read-side filter cannot move the number the gate reads in either direction. If the clause was meant to cover reads deliberately, only its author can say so.

**T0 carries the edit.** Nothing else in this ADR is authorised by anything else.

**Landed 2026-08-25.** ADR-004's Out of Scope clause now names only `kg_add` / `kg_invalidate`, and the amendment is recorded *in ADR-004* — beside the clause it changes, flagged as an inference, with the rejected alternative. Recording it only here would have left the amended document silent about its own amendment, which is the failure mode ADR-004 itself is about: a claim whose evidence lives somewhere the reader is not.

## Context

`am_kg_query(entity: "X")` — what an agent naturally writes, and what this repo's own `llm_init` bootstrap instructs every session to write — returns **every fact ever recorded about X**, dead ones included, tagged `current:false` and left for the reader to honour. The default applies no temporal filter at all (`kg.go:553`):

```go
func inEffectAt(row kgTripleRow, asOfKey string) bool {
	if asOfKey == "" {
		return true
	}
```

`current` appears in every response and in no request. In a store whose argument is that accumulation is affordable **because ended records stop competing**, the cost lands exactly where the argument says it will not: the agent's context window.

### How much this actually saves today, measured

**1,363 triples, 1,316 current, 47 expired — 3.4%** (`am_kg_stats`, 2026-08-25).

So the immediate token saving is small, and this ADR does not rest on it. Claiming a large win from 3.4% would be the precise failure ADR-004 exists to prevent: a plausible number carrying a decision it cannot support. The case is narrower and does not depend on the ratio:

1. **Correctness of the default.** An agent that reads a retracted fact and acts on it is wrong regardless of how many such facts there are. Today nothing in the tool stops that; `current:false` is a convention the reader must honour, not something the server enforces.
2. **The ratio only grows.** 3.4% is what a young graph looks like. Every retraction is permanent and every session adds more.
3. **The mean is not where the cost lands.** An entity corrected repeatedly is exactly the entity an agent queries when it is confused, and it carries a far worse ratio than the corpus mean. Nobody has measured that tail; it is named here rather than claimed.

### Filtering belongs on the server

The filter exists to keep dead facts out of the **agent's context**, so it must run before serialisation. A `current` flag the client filters on has already cost the tokens — the bytes crossed the wire and entered the window. That is the difference between this and the status quo, and it is the whole point.

## Existing Primitives Audit

- **`inEffectAt`** (`kg.go:553`) — the point-in-time predicate. Untouched. Its `asOfKey == "" → true` early return is correct for "no `as_of` was asked"; the defect is that nothing else filters when it abstains.
- **`KGCounts`** (`kg.go:276`) — already owns the SQL form of exactly this predicate: `Where("team_id = ? AND valid_to = ''", teamID)`. "Open-ended" is already expressed once in the repo, so this ADR reuses that vocabulary rather than inventing a second one. It is also an existing unindexed caller that T2's index speeds up for free.
- **`idx_kg_triples_team_predicate`** (`00010_kg.sql`) — **an index queried by nothing.** `KGQuery` fetches by subject and object only. The schema was built for predicate lookups and the query layer never arrived, so T5 costs no migration.
- **`kgFact` / `KGFact.Current`** (`kg.go:532`) — the output flag, computed as `row.ValidTo == ""`. Kept, with its doc corrected to say **open-ended** (see Risks). Not renamed: it is a live contract agents read.
- **`kgTripleRow.ExtractedAt`, `.SourceDrawerID`, `.SourceFile`** — written on every fact (`kg.go:367`), returned by nothing. `SourceCloset` sits beside them and *is* returned. T6.

## Decision

Three things, and an index. Nothing else.

### 1. `status` — endedness, filtered on the server

`status` = `current` | `ended` | `all`.

- `current` — `valid_to = ''`, open-ended records. **The new default.**
- `ended` — `valid_to <> ''`, closed records. The audit direction.
- `all` — today's behaviour, explicitly asked for.

A tri-state rather than a boolean because *"show me only the retracted ones"* is a real question, and a boolean whose absence means "both" is a tri-state wearing a boolean's clothes.

### 2. An index for the default path — `(team_id, valid_to)`

Measured with `EXPLAIN QUERY PLAN` on the real schema, 2026-08-25:

```
status=current   before   SEARCH … idx_kg_triples_team_predicate (team_id=?)          tenant scan
status=current   after    SEARCH … idx_kg_triples_team_valid_to  (team_id=? AND valid_to=?)   indexed
status=ended     after    SEARCH … idx_kg_triples_team_valid_to  (team_id=?)          scan — inequality
```

One additive `CREATE INDEX`, no data rewrite. `status=ended` stays a tenant scan because `<>` cannot use the index; that is the rare audit query and it is acceptable at this size.

**⚠ Corrected at implementation, 2026-08-25 — this section claimed the index "serves the **default** path, which is the one every agent takes", and that was wrong.** The three rows above measure the **status-only** shape: `team_id` plus endedness and nothing else. That is `KGCounts`, not the default query. The default query is an **entity lookup**, which already had a selective index — and measured against 300 facts with no `ANALYZE` (production's condition, because nothing in this repo runs it), adding `idx_kg_triples_team_valid_to` made the planner resolve the entity lookup *through it*:

```
team_id AND subject AND valid_to='',  index present, no ANALYZE
  SEARCH kg_triples USING INDEX idx_kg_triples_team_valid_to (team_id=? AND valid_to=?)
```

An empty `valid_to` matches ~96% of a tenant's rows; a subject matches a handful. **So the index added to make the default path cheaper made it read almost the whole tenant** — and it printed `SEARCH … USING INDEX`, on the column it was filtering, throughout. That is this ADR's own Risk row about `SCAN`-grepping gates, one level deeper: the plan named an index *and the right column*, and was still the wrong plan.

The fix is a unary `+` on `valid_to` in the entry-point queries, SQLite's documented "this term may not drive an index", which leaves the value untouched. Two spellings now encode which term is meant to find the rows: `kgStatusScope` writes `+valid_to` because it always *refines* a subject, object or predicate; `kgCurrentQuery` writes `valid_to` plainly because there endedness is the only selective term, and that is the one shape the new index exists for. `TestStatusFilterRefinesTheEntryPointRatherThanReplacingIt` is what keeps the distinction; removing the `+` turns it red on all three entry points.

With `ANALYZE` the planner chooses correctly on its own, so this is a no-stats artefact — but no-stats is what production is, and an index whose benefit depends on statistics nobody collects is a Follow-up, not an assumption.

**⚠ And the index is a trap for one query it also speeds up.** A date *range* on `valid_to` becomes `(team_id=? AND valid_to>? AND valid_to<?)` — fully indexed, fast, **and wrong**, for the reason in the next section. Speed is what would make it look right. The index ships together with the rule that range filters stay out of SQL.

### 2b. Why `status` is indexable when a date range is not

`KGAdd` stores `valid_from` and `valid_to` **exactly as supplied** (`kg.go:366`), normalising them only to reject an inverted interval. So the column mixes `2026-08-25` with `2026-08-25T09:00:00Z`. SQLite compares TEXT as bytes and a shorter prefix sorts first — measured:

```
window [2026-08-01T00:00:00Z .. 2026-08-07T23:59:59Z]
  2026-08-01T09:00:00Z   MATCHED
  2026-08-07             MATCHED
  2026-08-01             DROPPED   ← a date-only value ON the lower bound
```

A fact ending on the window's first day is silently excluded. Only for date-only values, only at the lower edge — invisible to any test written with datetime fixtures, and wrong on exactly the rows a human files by hand.

**`status` is unaffected, and that is why it can be indexed.** `valid_to = ''` is an exact byte comparison against the empty string. Format never enters it. A range comparison against a mixed-format column is what breaks, and this ADR does not ship one.

Today's `as_of` is nonetheless correct, and how it manages that is the constraint: `inEffectAt` normalises **both sides** at comparison time (`temporalStartKey(row.ValidFrom)` against a normalised argument, `kg.go:434`). The correctness lives in Go, per row. An index cannot do that — it compares stored bytes. Indexing any date column therefore requires normalising on write plus a backfill, which changes `kg_add` and is out of scope by the Amendment. Follow-ups records the order.

### 3. The default flips to `current` — and says so, every time

Hiding history by default collides with the reason the history exists, and ADR-010 already wrote the collision down:

> A session about to redo a rejected thing does not know to ask for history — that is precisely what it does not know.

So the withholding is never silent. Any response that filtered something carries what it removed:

```json
{ "entity": "…", "facts": [ … ], "count": 3,
  "status": "current", "withheld": { "ended": 7 },
  "hint": "7 ended fact(s) not shown — pass status:\"all\" or status:\"ended\" to see them" }
```

`withheld` appears only when something was removed, so the key's presence is itself information. This is ADR-007's rule applied to retrieval: **a filtered set reports what it filtered rather than presenting itself as the whole.**

### 4. `predicate` — free, and the graph's own vocabulary

`predicate` is a **required** argument on `kg_add` and `kg_invalidate` and appears on `kg_query` nowhere, so the one dimension nothing can select on is the vocabulary the graph is built from. *"Show me every `retracts` edge"* — how you audit what the team has changed its mind about — is a scan by eye today.

`idx_kg_triples_team_predicate` already indexes it as a two-column match, so `predicate` is an **entry point**: supplying it makes `entity` optional. Zero migration, zero new index, using a structure that has been sitting unused since `00010_kg.sql`.

### 5. Surface three columns that are already written

`recorded_at` (from `extracted_at`), `source_drawer_id` and `source_file` are stored on every fact and returned by nothing, while `source_closet` beside them is returned.

`extracted_at` is **transaction time**: the graph has been half-bitemporal since it was built and unable to say so. *"What was true on D"* is answerable via `as_of`; *"what did we **know** on D"* is not, from data already on disk. `source_drawer_id` is the other cost — every fact knows which memory asserted it and no agent can ask.

Returning them is additive and needs no migration. **Filtering** on them is not in this ADR (Follow-ups): they have no index, so a filter would be a per-tenant scan, and unlike `status` there is no measured demand yet.

## Alternatives Considered

- **A `current: true` boolean instead of tri-state `status`.** Cannot express the audit direction; rejected in §1.
- **Keeping the default at `all` (non-breaking).** This is the status quo, which is the thing being fixed. Recorded because if T4 is rejected in review, T1–T3 and T5–T6 still stand on their own.
- **Defaulting to `current` and staying quiet.** Rejected on ADR-010's argument, quoted in §3.
- **Client-side filtering on the existing `current` flag.** Rejected: the bytes have already crossed the wire and entered the context window, which is the entire cost being removed.
- **Shipping the date-window filters (`started_*` / `ended_*` / `recorded_*`) in this ADR.** Drafted and cut. They are Go-side and cheap, but the demand is one assertion on issue #23 while the default's cost is continuous and measured. Follow-ups, with a trigger.
- **Renaming the wire field `current` to `open_ended`.** More accurate, and it trades a live contract for a word. Documentation corrected instead.
- **A partial index `WHERE valid_to = ''`.** Smaller, and it serves only `status=current`; the plain two-column index serves the equality test and leaves `status=ended` no worse. Not worth the asymmetry at this size.

## Component / Boundary Impact

| Component | Change | Boundary |
|---|---|---|
| `db/migrations` | one additive `CREATE INDEX` | schema, no data rewrite |
| `internal/palace` (`KGQuery`) | takes a status + predicate filter; SQL predicate for `status=current` | Service API, internal |
| `internal/mcpserver` (`registerKGQuery`) | new optional params; response gains `status`, `withheld`, `hint`, and three already-stored fields | **agent-facing MCP contract** |
| `Service.Search`, ranking, drawers | **None** — the retrieval path never reads a triple and still does not | not crossed |
| `kg_add`, `kg_invalidate`, storage semantics | **None** — deliberately, so ADR-004's measurement is unaffected | not crossed |

## Wiring & Contract Changes

| Parameter | Read by | Default | When omitted |
|---|---|---|---|
| `status` | `KGQuery` endedness predicate | `all` at T1, **`current` at T4** | T1–T3: today's behaviour. T4: open-ended only, with `withheld` |
| `predicate` | exact match on the indexed column; makes `entity` optional | none | every predicate |
| `entity`, `direction` | unchanged | unchanged | unchanged |
| `as_of` | unchanged as a parameter, but it **composes** with `status`, and T4 moves what that composition returns | unchanged | T1–T3: facts in effect at that instant. T4: `current` ∩ `as_of` is *open-ended facts that were also in effect then* — a snapshot of a past date needs `as_of` **plus** `status:"all"` |

Response additions, all additive keys so a later field cannot break a caller: `status` (always, echoing what was applied), `withheld` and `hint` (only when something was removed), `recorded_at`, `source_drawer_id`, `source_file`.

**Not exposed, each on purpose:** `team_id` (tenancy comes from the session; a caller-supplied team is a hole, not a filter), `id` (fetch-by-triple-id is a different tool's shape), `confidence` (`KGAdd` hardcodes `1.0` at `kg.go:366` and nothing writes another value — a filter over a constant is a knob that does nothing, ADR-006; revisit when `kg-extract` varies it, which ADR-004 gates).

## Inter-task Contracts

- **T1 publishes the filter value** T4 and T5 extend — one struct carrying `Status` and `Predicate`, passed to `KGQuery`. Published as Go code before T4 starts, so the contract is checkable with `go doc` rather than agreed in prose.
- **T1 must return the count of what it dropped**, not only the surviving rows: `(facts, dropped, err)`. T3 has nothing to report otherwise, and re-filtering to recover the number is a second place to be wrong.
- **T4 changes only a default value.** If T4 needs to touch filter logic, T1 was wrong and the fix belongs there.
- **T2, T5 and T6 are independent** of each other and of T1's ordering.

## Implementation

**All seven landed 2026-08-25**, in the order T0 → T1 → T2 → T3 → T5 → T6 → T4, one commit each. T4 is last so the breaking default sits on top of the branch and reverts without disturbing anything under it. Every gate below was **mutation-proved** — wiring removed, test watched go red, wiring restored — because a gate nobody has seen fail is a gate nobody has tested. T2 grew a second gate that this ADR did not anticipate; see §2.

| # | Task | Gate |
|---|---|---|
| T0 | Amend ADR-004's Out of Scope clause to `kg_add` / `kg_invalidate`, recording the reasoning there | Approved by M; nothing else starts until it lands |
| T1 | `status` on `KGQuery` and `am_kg_query`, default `all` | `TestEndedFactIsAbsentFromCurrentQuery` — add a fact, invalidate it, assert absent under `current` and present under `all`; delete the wiring and watch it go red |
| T2 | `CREATE INDEX idx_kg_triples_team_valid_to ON kg_triples (team_id, valid_to)` | `TestStatusCurrentIsIndexed` — `EXPLAIN QUERY PLAN` must show **`valid_to` in the constraint list**, not merely the word `SEARCH`. Mutate by dropping the index and it must go red; a test grepping for `SCAN` stays green through that and is worthless |
| T3 | `withheld` + `hint` on every filtered response | `TestFilteredResponseReportsWhatItWithheld` — assert the withheld **number** equals what was removed |
| T4 | Flip the default to `current` | `TestDefaultQueryIsCurrentOnly`, plus a release note per ADR-014 |
| T5 | `predicate` as an entry point (`entity` optional when supplied) | `TestPredicateOnlyQueryIsIndexed` — same constraint-list assertion as T2, against the existing predicate index |
| T6 | Return `recorded_at`, `source_drawer_id`, `source_file` | `TestEveryStoredTripleColumnIsReturnedOrExcluded` — walk `kgTripleRow` by reflection; each field must be returned on `KGFact` or named in an exclusion map carrying a reason. Derived, not hand-listed, so a column added tomorrow enters the check in the commit that creates it. Prove it by adding a dummy column and watching the build go red |

T1 ships with the old default deliberately, so the filter is exercised in production before the default moves. T4 is a separate, revertible commit for the same reason.

## Consequences

- **Positive:** the default query stops returning retracted facts, and stops returning them *before* they cost context. The graph's own vocabulary becomes selectable. Three columns stop being written-and-invisible. `KGCounts` gets faster for free.
- **Negative:** T4 is a breaking change to the agent-facing contract. A caller relying on the default returning ended facts gets fewer, mitigated only by `withheld` and a release note. ADR-024's default change also owes a release note that has not been written — after T4 that debt is two, and they should ship together.
- **Neutral:** the write path, storage semantics, ranking and every stored fact are untouched; `as_of` keeps its own meaning, and T1–T3 and T5–T6 are additive.
- **Watch:** `as_of` is the one parameter T4 changes without touching. The two select on different questions — `status` on whether a fact was *ever* ended, `as_of` on whether it was in effect at an instant — and they **compose**, so under the new default `as_of` alone answers "open-ended facts that were also in effect on D", not "facts in effect on D". Asking the graph what it believed on a past date now needs `as_of` **plus** `status:"all"`. Nothing warns the caller, because from the server's side nothing is inconsistent: the filter did exactly what it says.
- **Honest:** the measured saving today is 3.4% of facts. The decision rests on the default being *correct*, not on the current ratio (Context).

## Out of Scope

- **Date-window filters** `started_from/to`, `ended_from/to`, `recorded_from/to` (deferred: drafted, cut for lack of demand — Follow-ups carries the trigger)
- **Filtering on the provenance columns** surfaced by T6 (deferred: no index, no measured demand; returning them is the half that is justified)
- **Drawer validity windows and recall returning only current drawers** (deferred: `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md` — this is the graph half only, and the two must share the `valid_to == ''` vocabulary. Re-pointed 2026-08-27: ADR-010 owned this and was superseded by ADR-038, which carries the drawer half as its T1/T5)
- **A `reason` on invalidation** (permanent: here — ADR-010's "third gap" owns it, and it changes `am_kg_invalidate`, which the Amendment leaves untouched — issue #23 item 4)
- **New columns** `reason`, `ended_by`, `superseded_by` (permanent: here — a nullable column added ahead of its writer is the unreachable-capability defect this repo is named after, and §Existing Primitives lists three live instances. The **contract** is designed once; the **schema** grows when something writes to it)
- **Semantic search over the graph** (deferred: `am_kg_query` is an exact entity lookup; a fact cannot be found without knowing its entity name. A missing capability, not a missing filter — its own issue)
- **Wiring a graph read into `Service.Search`** (permanent: ADR-004 owns it, reachable only through a `justified` verdict — issue #34)
- **Wing-scoping the graph** (permanent: facts are workspace-wide and `TestKnowledgeGraphIsWorkspaceWideNotWingScoped` pins it. A decision with a test behind it, raised as a defect often enough that its absence here should be visibly deliberate)
- **Entity quality** (deferred: issue #41 — the graph harvests Go identifiers, `Repo` and `Fatalf` topping the degree table. Stated honestly: a better filter over bad entities returns bad facts faster)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The T2 index makes an incorrect date range *fast*, inviting someone to push range filters into SQL | Med | **High** | §2b states it beside the index; the silent-drop measurement is in this document rather than in a commit message, and Follow-ups fixes the order |
| The default flip breaks a caller relying on ended facts | Med | Med | `withheld` names what was removed and the parameter that restores it; T4 is one revertible line, separate from T1 |
| `withheld` is computed and never printed | Med | Med | T3 asserts the number, not the field's presence. `printSupersessionGate`'s near-miss explanation was computed and discarded for weeks — 246 characters produced, 0 printed — and only a test reading the value caught it |
| A gate greps for `SCAN` and passes on an unindexed filter | **High** | Med | T2 and T5 assert the filtered column appears in the constraint list. Six measured query shapes all print `SEARCH … USING INDEX`; only two are indexed on the column they filter |
| The new index CAPTURES the entity lookup and makes the default path slower | **Materialised** | **High** | Found while implementing T2, not predicted here — see §2. `valid_to` is the least selective equality this table has (~96% of rows), so with no `ANALYZE` the planner preferred it over `subject`. Fixed with a unary `+` in `kgStatusScope`; `TestStatusFilterRefinesTheEntryPointRatherThanReplacingIt` pins all three entry points and goes red without it |
| `current` keeps meaning open-ended while reading as "true now" | Low | Med | Documented in §Existing Primitives and in the field's description. A future-dated `valid_to` is the case that exposes it — reachable via `KGAdd` and written by nothing today |
| 3.4% is quoted later as the benefit and the ADR reads as overselling | Med | Low | The ratio and its three caveats are in Context, and the decision is explicitly not resting on it |

## Rollback

- **T1, T3, T5, T6** — additive parameters and response keys over unchanged storage. Rollback is deleting them; nothing was written in a new shape.
- **T2** — `DROP INDEX`. No data change; queries return to a tenant scan.
- **T4** — the one that can hurt, and built to be revertible: a single default value in the tool registration. Reverting restores `all` and every caller sees today's behaviour on the next request. This is why it is separate from T1.
- **T0** — an ADR edit; reverting restores the clause verbatim and the tasks stop being authorised.

No migration to reverse, no backfill, no index rebuild beyond the one `DROP`.

## Follow-ups

- **Date-window filters**, cut from this ADR. Trigger: someone asks *"what expired this week"* against the real corpus and cannot. They are Go-side refinements over an entry-point-narrowed set, need no index, and break nothing — a small ADR or an issue, not this one.
- **Normalise temporal values on write, then index them.** The prerequisite §2b identifies, and its own ADR because it changes `kg_add` and needs a backfill. Strict order: canonicalise `valid_from`/`valid_to`/`extracted_at` to `YYYY-MM-DDTHH:MM:SSZ` at the write path → backfill → *only then* index and push date filters into SQL. Out of order ships §2b's silent boundary drop into production, on the rows a human hand-filed.
- **Measure the tail, not the mean.** Context claims an entity corrected repeatedly carries a far worse expired ratio than the corpus's 3.4%. Nobody has measured it. That number is what would justify or deflate this ADR's premise, and per ADR-009 it must come from this corpus.
- **The two deferred indexes** — `(team_id, source_drawer_id)` when drawer→facts becomes a standalone question, `(team_id, valid_from)` when the entity-free timeline's temp B-tree stops being negligible. Name a row count, not a feeling.
- **Paging on the entity-free timeline.** `kgTimelineLimit = 100` with no paging; a trail you can only see the first hundred rows of is a sample. Cut here for scope, unchanged in urgency.
- **Generalise the `EXPLAIN QUERY PLAN` gate.** T2 and T5 pin the KG's entry points; every other table has query shapes nobody has checked, and `idx_kg_triples_team_predicate` proves the reverse case exists too — an index nothing uses is as invisible as a filter nothing indexes.
- **Decide whether this database should have statistics at all.** Nothing runs `ANALYZE`, so every plan in this repo is chosen by SQLite's no-stats heuristics — which is how a new index silently captured the query it was meant to help (§2). The unary `+` fixes that one shape; it does not answer the general question, and every future index carries the same risk until someone does. Trigger: the next index added to a table that already has a selective one. Note that `ANALYZE` is not free to adopt blindly either — it makes plans depend on when it last ran, which is a different failure mode, not an absent one.
