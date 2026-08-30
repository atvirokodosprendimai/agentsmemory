# ADR-034: Record WHY the cross-encoder did not order a page

**Status:** Accepted
**Date:** 2026-08-26
**Owner:** Zy
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/ADR-029-a-trace-that-cannot-lie.md`, `docs/adr/ADR-030-a-blend-that-cannot-tell-confidence-from-noise.md`, `docs/adr/ADR-031-the-column-abstention-would-calibrate-on.md`
**Invalidates:** none — checked. ADR-031 aggregates on `hits > 0 AND reranked = 1` (`internal/palace/recallstats.go:212`), so `reranked` keeps its meaning and its calibration is untouched; this ADR adds a column beside it rather than repurposing it.
**Served-path change:** `am_recall_stats` gains a breakdown of why reranking did not happen, so an agent or operator reading it can tell a disabled cross-encoder from one timing out. Ranking itself is unchanged — no recall returns different memories in a different order because of this ADR.

## Context

A rerank that runs out of budget fails OPEN, by design, and serves the fused order instead of the
cross-encoder's. That design is right: ADR-030's rule is that a ranking input is a signal, never a
gate, and a reranker that is slow must degrade recall rather than break it.

What is wrong is that the degradation cannot be counted afterwards.

Measured 2026-08-26 against the live palace on a CPU cross-encoder, via traced `agentsmemory eval`
runs over the 54-case real corpus: 60 rerank calls at `RERANK_POOL=20` took a mean of 11.4s
(min 7.3s, max 18.2s); a larger frozen window from the same run gives n=132, mean 11.6s, 88 of 132
past 10s. A second figure of roughly 19s was taken while TWO evals shared one CPU cross-encoder and
is therefore a contention measurement, not a pool-20 one — it is excluded from the claim rather than
averaged into it. `docker-compose.full.yml` ships `RERANK_TIMEOUT: "10s"`, so a majority of
those calls would have overrun their budget. Pool 20 is a single batch — `maxBatch` is 32 in
`internal/rerank/tei/tei.go` — so this is the cost of scoring 20 documents, not batching overhead.
The shipped default is pool 10, and **no run has measured pool 10 on this hardware**, so none of
this is a verdict on the default.

The live trace can already see an overrun: `am.rerank_timeout_ms` sits beside the rerank stage's
duration and the stage ends `failed_open reason=timeout`, both shipped 2026-08-26 on this branch.
Traces are sampled and ephemeral. The durable record is what answers "what fraction of recalls
served a degraded ranking last week", and it cannot:
`search_events.reranked` is `boolToInt(reranked)` (`internal/palace/service.go:1194`), where that
bool is false alike for no reranker configured, an empty pool, `weight <= 0`, a wrong score count,
and a timeout. `am_recall_stats` therefore reports `reranked: 0` for a deliberately disabled
reranker and for one timing out on every query.

There is a related invariant that cannot be mechanised the usual way. `docker-compose.full.yml`
carries "keep RERANK_TIMEOUT above pool x per-doc cost, or every search degrades" as a comment with
nothing enforcing it. It cannot become a static gate, because per-doc cost is hardware-dependent —
a GPU and a CPU cross-encoder differ by roughly an order of magnitude, and the correct pool for one
is the broken pool for the other. An invariant that no exit code can check is one that has to be
MEASURED instead, which is what this ADR is for.

## Existing Primitives Audit

- **`search_events` + `recordSearch`** — one row per recall, already carrying `reranked` and (from
  ADR-031) `top_rerank_score`. **Extended, not reshaped:** a new nullable column. `reranked` keeps
  its exact current meaning because ADR-031's calibration aggregate depends on it.
- **`telemetry.Reason*` constants** (`internal/telemetry/telemetry.go`) — the vocabulary already
  exists and is already emitted on the rerank span: `no_reranker`, `empty`, `weight_zero`,
  `timeout`, `error`, `score_count`. **Reused verbatim** so the durable column and the span cannot
  drift into two vocabularies for one fact.
- **`applyRerankWith`** (`internal/palace/service.go`) — already computes the reason to put on the
  span. **Reshaped:** it returns the reason instead of discarding it after `sp.End`.
- **`WingRecall` / `am_recall_stats`** (`internal/palace/recallstats.go`, `internal/mcpserver/admin.go`)
  — the read surface. **Extended** with a breakdown field.
- **Migration 00026** — the additive-column precedent from ADR-031, followed exactly.

## Decision

`applyRerankWith` returns the reason the cross-encoder did not order the page, `recordSearch`
persists it in a new nullable `search_events.rerank_skip_reason` column, and `am_recall_stats`
reports the breakdown. The reason vocabulary is `telemetry`'s existing constants, so the span and
the row always say the same word about the same recall. When reranking runs, the column is empty.

**What would make this fail, and whether such data exists.** This ADR delivers a measurement, not a
threshold, so the honest failure mode is that the column is always empty — reranking never fails
open in practice and the work bought nothing. That outcome is data, not a null: today nobody can
state it, and "0 fail-opens in a week of real recalls" would be worth recording and would retire
the concern. Data that could produce it exists now: the live palace records `search_events` rows
continuously. The measurement is valid for the deployment that produces those rows and no other —
a CPU cross-encoder at a given pool — because a budget overrun is a fact about hardware and pool
together, never about the code in the abstract.

## Alternatives Considered

- **Leave it at the trace.** `reason=timeout` on the rerank span already distinguishes the cases.
  Rejected because traces are sampled and short-lived: the question is "what fraction, over a
  period", and a span answers "what happened, on this request". ADR-029 made the trace honest; it
  did not make it a time series.
- **Repurpose `reranked` into an enum.** Fewer columns. Rejected because ADR-031's calibration
  aggregates on `reranked = 1` and a silent semantic change there is precisely the shape this
  branch has already had to retract once — the eval's normaliser inheriting a served value and
  turning a control into a duplicate arm.
- **Warn at runtime when a call approaches its budget.** Cheap, and a leading indicator rather than
  a lagging one. Rejected as the FIRST step, not on merit: a warning is another thing to notice,
  and until the fail-open rate is known nobody can say what threshold deserves a warning. It stays
  on the table once the column has numbers in it.
- **Adaptive pool, or boot-time self-calibration** — shrink the pool when calls near the budget, or
  measure per-doc cost at startup and refuse a bad pair. Rejected as speculative: both are designed
  against a failure rate nobody has measured, and self-calibration adds a boot-time dependency on
  the reranker being warm, which is exactly when it is slowest.

## Component / Boundary Impact

None — internal to `internal/palace`, its `search_events` schema, and the `am_recall_stats` read
surface in `internal/mcpserver`. No component changes owner and no boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `search_events.rerank_skip_reason` (schema) | new nullable TEXT column, migration `00029` | `recordSearch` | `RecallStats`, `doctor` |
| `palace.searchEventRow.RerankSkipReason` (struct field, unexported) | added | `Service.Search` | `recordSearch` |
| `applyRerankWith` return signature | returns `(ranked, ok, reason)` | `internal/palace/service.go` | `Search`, `RerankScoresFor` |
| `palace.WingRecall.RerankSkips` (map reason→count) | added | `RecallStats` | `am_recall_stats` |
| `am_recall_stats` tool result | new `rerank_skips` object | `internal/mcpserver/admin.go` | agents, operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `applyRerankWith` returning a reason (T1) | T1 | T2 | No — internal signature, both call sites updated in T1 |
| `search_events.rerank_skip_reason` (T2) | T2 | T2 | No — additive nullable column |

## Implementation

See `tasks/README.md`. Two tasks.

## Consequences

- **Positive:** a degraded ranking becomes countable. "What fraction of recalls last week served the
  fused order because the cross-encoder ran out of budget" becomes a query rather than a guess, and
  the compose file's unenforceable prose invariant gains a way to be checked against reality.
- **Positive:** the span and the durable row share one vocabulary, so a trace and a stats call
  cannot disagree about why a page was not reranked.
- **Negative:** one more column and one more field on a read surface agents already parse. The
  column is empty on the healthy path, which is a cost paid on every row for a signal that should
  be rare.
- **Neutral:** ranking does not change. No recall returns different memories, or the same memories
  in a different order, because of this ADR.

## Out of Scope

- Changing `RERANK_POOL` or `RERANK_TIMEOUT` defaults — every measurement to date is at pool 20, the shipped default is 10 and has never been measured on this hardware, and a default is not moved on a corpus that never ran it (deferred: docs/adr/BACKLOG.md)
- A runtime warning when a rerank call approaches its budget — it needs a threshold, and the threshold needs the numbers this ADR produces (deferred: docs/adr/BACKLOG.md)
- Adaptive pool sizing and boot-time self-calibration (permanent: both are designed against a failure rate nobody has measured, and this ADR exists to produce that rate. If the numbers later justify either, it is a new decision with evidence rather than this one extended.)
- Changing the meaning of `reranked` (permanent: ADR-031's calibration aggregate reads it at `internal/palace/recallstats.go:212`; this ADR adds beside it, deliberately.)
- Making the compose invariant a static gate (permanent: per-doc cost is hardware-dependent — measured 2026-08-26, a CPU cross-encoder takes 11-20s for the pool a GPU would take under a second for — so no exit code computed at build or boot can know the right pair.)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The reason column is written but nothing ever reads it — a capability that ships unreachable | Med | Med | T2's Reachability rung 3 is the `am_recall_stats` result schema, and its test fails if the field is absent from the tool's output rather than merely absent from the struct |
| The span's reason and the column's reason drift into two vocabularies | Low | Med | Both take `telemetry.Reason*` constants; T1's test asserts the value the span carries equals the value returned for the row |
| The migration collides with another open branch | Low | High | Renumbered `00027` -> `00029` at merge 2026-08-26: ADR-036 (#67) landed first with `00028`, and goose refuses a pending migration below the maximum applied version, so the record that merges second reallocates. This is ADR-036's own recorded rule, applied |
| The column is always empty and the work bought nothing | Med | Low | That is a recordable result, and the ADR says so in the Decision rather than treating emptiness as failure |

## Rollback

Persistent state, so rollback is real: `db/migrations/00029_search_events_rerank_skip_reason.sql`
carries a `-- +goose Down` dropping the column, and the field is nullable, so a server running the
previous binary against the migrated schema writes NULL and reads nothing. Revert order is binary
then migration; the reverse also works because nothing reads the column at write time.

## Follow-ups

- [ ] Report the first measured fail-open rate in `docs/adr/BACKLOG.md` whichever way it falls, including "zero in a week of real recalls" — the outcome that would retire this concern rather than extend it.
- [ ] Measure pool 10, the shipped default, on the same hardware, so the compose file's invariant can be stated as a number instead of a warning.
