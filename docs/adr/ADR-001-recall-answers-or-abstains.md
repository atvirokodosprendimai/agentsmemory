# ADR-001: Recall answers or abstains

**Status:** Withdrawn
**Date:** 2026-08-19
**Owner:** Zy (with Mindaugas as upstream maintainer)
**Spec:** None — no spec stage; the decision rests on measurements recorded in the eval harness rather than on elicited requirements.
**Cross-references:** `internal/palace/eval.go` (harness), `cmd/server/eval.go` (`verifyAbsent`, the absent generator), `internal/rerank/tei/tei.go` (the two-dialect rerank client), `db/migrations/00021_search_events.sql` (telemetry), `docs/adr/ADR-002-anchor-the-lexical-score.md` and `docs/adr/ADR-003-retire-the-closet-prior.md` (both change which document reaches production top-1, which is the document this ADR judges), `docs/adr/BACKLOG.md`, PR #20 comment recording the separation measurement
> **Withdrawn 2026-09-05, on its own pre-registered criterion.** T3 ran the gate twice. On 2026-08-22 the corpus (449 memories) put every answer in the pool, so the preflight disqualified the run and the verdict was BLOCKED (`evidence/abstain-gate-2026-08.md`). On 2026-09-04/05 the corpus (~3,800 drawers, vector ceiling 76–90% in-pool) was fit to decide, and the gate failed: at the threshold holding answer recall ≥ 0.95 the production arm correctly refuses **0 of 18** verified-absent questions, 90% Wilson lower bound **0.000** against the declared 0.30; no threshold on the curve clears both bars, the calibration band is empty, and the best separating signal the eval can read (the cross-encoder's top-1 score) reaches AUC 0.63 — chance, in effect. The easy-negative comparison fails the same way (0 of 19, AUC 0.60). `eval --calibrate --gate` exit 1, read unpiped. Full run, both curves and the signal sweep: `evidence/abstain-gate-2026-09.md`. T4, T5 and T6 are not started; nothing ships. The README's rule applies as written: a failing gate withdraws the ADR rather than lowering the target. What is NOT concluded: that abstention is impossible — only that none of the five scores this eval reads carries it on this corpus. A different score, corpus or target is a new proposal, not a retune of this one.

**Served-path change:** `Service.Search` returns a confidence verdict and ABSTAINS rather than answering when calibration says it cannot — `am_search` carries it. Not yet on the served path: 0 of 6 tasks done, and T4-T6, the three that touch it, are gated on T3's go/no-go.

## Amendment 2026-08-25 — the task table understates what exists

A source audit found `internal/palace/calibration.go` already exports
`RecommendThresholds`, `RiskCoverageCurve`, `ViablePoint`, `RefusalGate`,
`LoadCalibration`, `ScoreCanary` and `ScoresLookBounded`, and `cmd/server/eval.go`
already declares `--calibrate`, `--calibration-out`, `--gate` and `--rerank-model`.
That is T1's and T2's mechanism, built.

The README lists all six tasks as `pending`, which reads as "none of this exists".
The accurate statement is narrower: the mechanisms exist and **the verification
evidence does not** — no `adr-verify` run has recorded an acceptance digest for
them, which is a different debt from unwritten code and is repaid differently.

T4, T5 and T6 remain genuinely unbuilt, and this was checked rather than assumed:
nothing under `cmd/server` calls `LoadCalibration`, there is no `Confidence` type
in `internal/palace`, and a live search trace shows no abstention stage between
`am.search.rerank` and `am.search.record`. T3's go/no-go — the human decision that
governs whether T4 to T6 are built at all — has not been taken.
## Context

Recall currently returns a ranked page and says nothing about whether the page contains an answer. The agent consuming it cannot tell "here is the memory you asked for" from "here are the five least-unrelated things in the palace", so a confident-looking page of irrelevant memories is indistinguishable from a good one — and an agent that cannot tell will act on both.

`max_distance` looks like the knob for this and is not. Measured on 61 cases — 40 answerable, and 21 labelled absent by the generator's own check (4 of 25 candidates were rejected during generation because another memory answered them):

| signal | answerable | unanswerable |
|---|---|---|
| top-1 cosine distance | median 0.401 (0.251–0.496) | median 0.423 (0.364–0.519) |
| top-1 cross-encoder score | median 0.891 (−6.569–5.572) | median −3.832 (−6.327–1.500) |

The distance distributions overlap almost completely and their medians are 0.022 apart: **no threshold on cosine distance separates them at any value.** The cross-encoder's medians are ~4.7 apart, which is real signal, though the ranges still overlap at the tails.

**A claim this ADR made earlier was wrong.** The paragraph above used to say those 21 cases had their absence "verified against the whole corpus rather than against the note they were generated from". The code does not do that. `verifyAbsent` (`cmd/server/eval.go:389`) searches with `Limit: 3` and asks the checker about three hits, so what was actually established is that the top three results do not answer the question — a memory answering it at rank 4 leaves the case labelled absent. And when the checker itself errors, the caller prints `kept UNVERIFIED` and **keeps the case anyway** (`cmd/server/eval.go:538`), which is a negative label backed by nothing. So "21 verified-absent cases" is not a fact about this corpus; "21 cases labelled absent under a top-3 check, an unknown number of them unchecked" is. Every count in this ADR is written that way from here on, and the real number is whatever survives T1's repair: a stricter check applied to the same candidates can only keep fewer of them, and T3 reports what it kept.

That correction changes what the separation table can support, not whether the experiment was worth running. Two defects push in the same direction, and both inflate it:

- **The negatives are too easy.** `evalPromptAbsent` instructs the generator "do not reuse the note's distinctive identifiers", so our unanswerable queries are systematically stripped of the lexical overlap that makes a real negative hard. A threshold fitted to these over-answers on the negatives production actually sees — a near-miss from a neighbouring wing that shares identifiers.
- **The negative labels are partly unchecked**, as above. Cases where the palace *can* answer at rank 4–20, and cases where the checker simply failed, are both sitting in the unanswerable column.

So the ~4.7 gap is an upper bound on an upper bound. Repairing both is a precondition of calibration (T1), not an improvement to it, and the repair is expected to shrink the measured separation.

Verification cannot be corpus-wide either, and T1 does not claim it is. The eval prints a retrieval ceiling for this corpus: 98% of answerable golds are inside the pool, 75% at top-1, 92% in the top 5, 98% in the top 20, and 1 of 40 never retrieved at all. Checking the top 20 therefore covers everything this palace can retrieve; a memory the dense channel never surfaces is one recall could not have returned either, so "absent" is defined operationally as "not reachable", and the constant says so at its declaration.

The deeper reason this is worth building rather than left to the caller: a ranked page is a **human** interface. It exists because search engines return links for people to skim and discard. Our consumer is a model with a context budget and no ability to glance — handing it five results and no judgement makes it spend tokens re-deriving, per query, something the palace already computed and threw away.

Three properties of the cross-encoder score constrain any design.

**It is only present when a reranker scored the hit** — which is why `SearchHit.Reranked` exists, since zero is an ordinary logit and cannot serve as a sentinel. The same reasoning applies to every threshold this ADR introduces: absence is carried by presence, never by a comparison against 0.

**Its scale is backend-dependent.** `internal/rerank/tei/tei.go` sets `raw_scores: false` so TEI returns sigmoid-squashed values in (0,1), while llama.cpp's server returns bare logits — which is why the measured absent median above is negative. A single hardcoded threshold would be wrong on one of the two backends we ship.

**And the configuration cannot tell you which backend you have.** An earlier draft of this ADR guarded the threshold with an `ABSTAIN_BACKEND` key, checked against "the dialect `RERANK_URL` implies". That guard cannot work, and the client explains why: `rerankBatch` sends *both* dialects' field names on every request and `decodeResults` accepts whichever comes back, branching on the first non-space byte — so one URL legitimately serves either server. The URL names a route, not a model: the package comment states outright that "the model is fixed by the container's `--model-id`, which is why nothing here names a model". And the same endpoint can be restarted against different weights with nothing in our configuration changing. A backend name would have been one operator-typed label checked against another operator-typed label, which is not a check.

## Existing Primitives Audit

- **`palace.Service.Search`** — already computes and returns the cross-encoder score per hit (`SearchHit.RerankScore`) and, since the presence fix, `SearchHit.Reranked`. Reused as-is; the verdict is derived here, not recomputed elsewhere.
- **`ArmProduction` in `internal/palace/eval.go`** — already captures the production page's top-1 rerank score and its presence flag, with a comment saying that the abstention gate will run on this path so its calibration data has to come from here. Reused; T1 only attaches the population label and carries the pair onto `EvalCaseResult` so the curve gets labelled rows instead of the two flat `GoldRerank` / `AbsentRerank` arrays.
- **The eval's reranker preflight** — `EvaluateWith` already probes the reranker once, with a pool-sized batch, before scoring hundreds of cases, because a dead reranker degrades every reranked arm silently. Reshaped into the startup canary: same idea, different question — not "does it answer" but "does it answer the way it did when the threshold was measured".
- **`caseFileMeta`** — already the provenance line at the head of every case file, written because two runs of "the same" eval disagreed for reasons nothing recorded. Extended per case with the absence check's depth, model and time, so a merged file can be trusted row by row.
- **`config.Config` + `cmd/server/main.go` flag wiring** — the established pattern for an operator-set, env-overridable knob (`BM25_WEIGHT`, `CLOSET_BOOST`, `FUSION`). Reused; no new configuration mechanism.
- **`search_events` table + `recordSearch`** — already records one row per recall. Extended, not reused as-is: its `top_score` is the **fused** score and its `reranked` flag is **page-level**, and neither is the number a verdict is derived from.
- **`am_search` MCP tool** — the surface an agent already calls. Extended with one field; no new tool.

## Decision

`Search` gains a **confidence verdict** derived from the top hit's cross-encoder score compared against calibrated boundaries, and `am_search` returns it alongside the hits.

The verdict has four values and the fourth is the point of the design: `answered`, `no_answer`, `uncertain`, and **`unknown` — returned whenever no calibration is loaded, the loaded calibration cannot be confirmed against the running reranker, or no reranker scored the top hit.** There is no default threshold. A number that is right for `bge-reranker-v2-m3` on TEI is wrong for the same model on llama.cpp, and inventing one would reproduce exactly the `max_distance` mistake this ADR exists to correct: a plausible constant, inherited, never measured, quietly deciding what the palace admits to knowing.

**Three verdicts need two boundaries.** `answered` is score ≥ `answer_at`; `no_answer` is score < `refuse_below`; `uncertain` is the band between them. One threshold cannot produce three answers, and an "uncertain band around it" is a second number whether or not it is written down — so both boundaries come out of the same curve, under two declared rules. `answer_at` is the highest threshold holding answer-recall ≥ 0.95 on the reachable-answerable population. `refuse_below` is the highest threshold at which at most one reachable-answerable case scores below it (`--refuse-allowance`, default 1 case) — a count rather than a second recall target, because a rate cannot express it at this sample size: with ~39 reachable-answerable cases the achievable recall grid is 1.0, 0.974, 0.949, so a 0.975 target and a 0.95 target land on the same threshold and the band collapses to nothing. For the same reason the recall a chosen threshold actually achieves is printed beside the target it was asked for. Both boundaries are optional in the calibration file and their **presence** decides whether a verdict can be derived; 0.0 is an ordinary sigmoid score, not an unset marker.

The boundaries are produced by a new `eval --calibrate` mode rather than chosen. Given a merged answerable-plus-verified-absent case file it emits the risk–coverage curve — for each candidate threshold, the share of answerable queries still answered against the share of unanswerable ones correctly refused — and writes a calibration file the operator points the server at. It refuses to emit anything from a case file whose absent cases lack T1's verification provenance, or that was generated by the easy-negative prompt. (Earlier this ADR had the command *print* a threshold for the operator to paste into configuration. A pasted float travels without its provenance, which is the failure the fingerprint below exists to prevent, so the number now travels inside the file and the operator wires up the file.)

**The falsification runs first, and its criterion is a number declared here, before the run.** T1 makes the negatives honest; T2 builds the curve and the gate; T3 runs it. The gate: at `answer_at` — the threshold holding answer-recall ≥ 0.95 on the *reachable*-answerable population — the correct-refusal rate over verified-absent cases must have a 90% Wilson lower bound of at least **0.30**. On twenty verified-absent cases that means 10 refusals (bound 0.327); 9 gives 0.284 and fails. The bound rather than the point estimate, because at n≈20 the estimate carries roughly ±0.11 at one standard error and a gate that ignored that would pass on noise. The bar is a declared judgement, not a derived one: below it, the gate costs up to 5% of answerable queries a downgraded verdict while catching under a third of the unanswerable ones, and a knob, a wire field and a column are not worth that trade. `eval --calibrate --gate` exits non-zero when it is not met, and a non-zero exit withdraws this ADR on this corpus. T4, T5 and T6 do not start until T3's log records a `ship`. The earlier plan had calibration last, which meant four tasks could land before the ADR learned its premise had failed.

Two things about that bar are worth saying before the run rather than after. A 0.95 recall target sets `answer_at` at about the answerable distribution's 5th percentile — on 40 cases, roughly its second-lowest score — and that distribution reaches −6.569. If the second-lowest gold score sits below the absent **median** of −3.832, more than half the absent cases score above the boundary, are called `answered`, and the gate fails. The summary statistics recorded here cannot settle where that score sits, which is a large part of why T3 exists rather than a paragraph of reasoning standing in for it. And T1's repair pushes in both directions at once: identifier-preserving negatives score higher, which is worse for the gate, while dropping cases the palace can actually answer at rank 4–20 removes high-scoring impostors from the absent column, which is better for it. Which effect dominates is exactly what T3 measures, and the criterion stays where it is either way — moving a target after seeing the curve is how a plausible constant gets born.

Calibration scores **three** populations, not two. A case whose gold never entered the retrieved pool (`PoolRanks == 0`) is answerable-but-unreachable: abstaining on it is correct behaviour for a retrieval failure the gate cannot see, and counting it as a false abstention would tune the threshold toward over-answering. So the headline metric is precision at a declared recall on the *reachable*-answerable class, with the unreachable count reported beside it as the ceiling. Note what the recall term is computed on: the **production top-1's** score, the same statistic the served gate compares — not "the page contains the answer". The gold is top-1 for about 75% of answerable questions here, so a page whose answer sits at rank 2–5 behind a confident-looking wrong hit is judged on the wrong hit. The verdict describes the top result; it does not claim to describe the page.

**What protects the threshold is a fingerprint, not a backend name.** `eval --calibrate` records, beside the two boundaries: the ranking profile in force (fusion mode, BM25 weight and auto flag, closet scale, rerank pool, rerank weight); whether the observed scores were bounded in (0,1) or unbounded, inferred from the scores themselves because `Reranker.Rerank` returns floats and never reports which dialect decoded them; an operator-declared model label, which is documentation rather than a check; and a **canary** — a fixed set of (query, document) pairs scored five times through the configured reranker, storing each pair's mean and, as the comparison tolerance, the largest deviation observed across those repeats. The tolerance is measured from the instrument, never typed; deterministic inference yields a zero spread and therefore an exact-match check.

At startup the server checks both halves, and they fail differently on purpose. A **ranking-profile mismatch refuses startup**: the operator set the calibration and the knobs, they contradict each other, and a threshold measured under another profile is judging a document that is no longer the one recall returns. A **canary mismatch, or a reranker that does not answer, does not**: the palace still serves recall, verdicts drop to `unknown`, and the divergence is logged with both score sets. A memory server that refuses to boot because a cross-encoder moved is a worse failure than one that stops claiming confidence. The canary re-probes at most once more, on the first search with a reranked top hit, so a reranker still loading at boot resolves itself without a restart.

One field deliberately does **not** live in the startup check: the requested `limit`. It arrives per call — `am_search` lets the caller set it, defaulting to 5 — and it changes what the cross-encoder ever sees, because the candidate pool is `max(limit × hybridCandidateMultiplier, rerankPool)` = `max(limit × 3, 50)`. While `limit × 3 ≤ 50` the same 50 candidates are all cross-encoded and the top hit is the one calibration was measured on; above that, fusion picks which 50 of the candidates get scored and evicts the rest — "two different architectures", as the production arm's own comment puts it. So the verdict is derived only when the whole candidate set reached the cross-encoder, and is `unknown` otherwise. That is a per-request comparison in T5, not a startup check, because no boot-time validation can see a parameter that arrives with the query.

A canary detects **change, not identity** — it cannot tell you which model you have, only that it is not the one you calibrated against. That is exactly the guarantee the threshold needs, and it is more than a model name could give: it also catches a re-quantised checkpoint, a changed `raw_scores` setting, or a proxy that started answering in the other dialect.

The two live ranking ADRs matter here and are called out for the operator, not just for us. **ADR-002 renormalises the lexical half of the fused score and ADR-003 removes the closet prior from the default ranking; both change which document reaches production top-1.** The cross-encoder's score for a given pair does not move — but the *pair* does, because the document being scored is the one fusion put on top. A calibration taken before either lands is invalid after it, which is precisely why the profile fields are in the fingerprint and why the mismatch is a startup error rather than a warning.

Because the distributions overlap at the tails and the calibration set is small — around twenty verified-absent cases means a 10% rate is really 10% give or take about the same again — the output is an operating point with a stated error rate and an explicit sample count. No distribution-free guarantee is claimed or implied; the numbers are empirical quantiles over a set we generated, and the ADR says so where an operator will read it.

The table at the top of this ADR does settle one question about the `no_answer` band, and the answer is uncomfortable. Answerable scores run −6.569 to 5.572, absent ones −6.327 to 1.500: the **lowest answerable score sits below the lowest absent one**. An allowance of zero would therefore put `refuse_below` at or under −6.569, with no absent case beneath it — a fourth verdict that provably never fires on the measured set. That is why the allowance defaults to one rather than zero: refusing a single answerable case is what buys a boundary above the answerable low tail, and without it there is no `no_answer` at all. How many absent cases that catches cannot be read off these summary statistics, so the calibrate report prints the count and T3 records it. If it is still zero after T1's repair, this ADR ships three verdicts and says so at the knob rather than shipping a band nothing falls into.

Two floats and a fingerprint are deliberately the whole mechanism. Anything richer — a learned classifier, a multi-feature score — has to beat this baseline explicitly on the same curve before it earns the complexity, and nobody has measured the baseline yet.

## Alternatives Considered

- **Threshold on cosine distance (`max_distance`):** the knob that already exists. Rejected on measurement: the two distributions overlap with medians 0.022 apart, so every threshold either refuses answerable queries or admits unanswerable ones at a rate no operating point makes acceptable.
- **A hardcoded cross-encoder threshold with a sensible default:** simplest to ship. Rejected because the score scale differs by backend (sigmoid on TEI, logits on llama.cpp) and by model version, so a default is wrong for at least one shipped configuration while looking authoritative — the `max_distance` failure mode repeated.
- **One threshold instead of two, with the band "around" it:** fewer numbers. Rejected because the band's width is the second boundary under another name, and leaving it implicit means it gets invented in the serving code instead of measured on the curve.
- **An operator-declared backend name checked against the reranker URL (`ABSTAIN_BACKEND`):** this ADR's own earlier proposal. Rejected on reading the client: it sends both dialects' fields and decodes either reply, the URL names a route rather than a model, and the endpoint can change models under a running server. It would have checked one typed label against another and reported success.
- **Probing the reranker for its model id at startup:** a real identity check where an info endpoint exists. Rejected as unavailable through our abstraction — `Reranker.Rerank` returns `[]float64`, and widening it to carry server metadata would change the interface for every implementation to serve one caller. The canary gets the property we need from the call we already make.
- **Return the raw score and let the agent decide:** minimal, honest, and already effectively the case (`RerankScore` is in the response). Rejected because it pushes calibration onto every consumer, none of which has the absent-case data needed to do it; each agent would invent its own constant, and the palace would still not know whether it is being believed.
- **A learned multi-feature abstention classifier** (score, margin, distance, coverage): plausibly better than two thresholds. Rejected for now as unmeasurable — around twenty verified-absent cases, far too few to fit and hold out. Recorded as deferred, not dismissed.
- **Semantic-entropy / hidden-state probes on the consuming model:** the strongest published signal for "the model does not know". Rejected as inapplicable: we do not own the consuming model's forward pass; agents call us over MCP and receive text.

## Component / Boundary Impact

| Component | Change | One reason to change? |
|---|---|---|
| `internal/palace` | derives the verdict inside `Search`; owns the calibration type, the risk–coverage curve and the threshold comparison | yes — it already owns ranking, the score and the eval |
| `internal/mcpserver` | serialises one new field on `am_search` | yes — it owns the wire surface |
| `internal/config` + `cmd/server` | one new key, the profile check and the canary probe | yes — the composition root |
| `cmd/server/eval.go` | honest negative generation, absence verification, the `--calibrate` and `--gate` modes | yes — it owns measurement |
| `db/migrations` | four nullable columns on `search_events` | yes — it owns persistence |

No new component. No module moves. This repo has no `docs/architecture.md`; one should be written (`/arch-write`) before an ADR that *moves* boundaries — this one does not.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `palace.Confidence` on the search result | add verdict enum (`answered`/`uncertain`/`no_answer`/`unknown`) with the score, its presence flag, both boundaries and the calibration id | `palace.Service.Search` | `mcpserver`, eval |
| `palace.Calibration` (file format) | new: two optional boundaries + fingerprint (ranking profile, canary pairs/scores/tolerance, score transform, model label) + content id | `eval --calibrate` | `cmd/server`, `palace.Service` |
| `am_search` MCP result | add `confidence` object | `mcpserver` | every agent |
| `ABSTAIN_CALIBRATION` (env / `--abstain-calibration`) | new config key naming the calibration file; unset = verdict `unknown` | operator | `cmd/server`, `palace.Service` |
| startup validation | profile mismatch refuses startup; canary mismatch or unreachable reranker leaves verdicts `unknown` | `cmd/server` | operator |
| `search_events.verdict` / `.rerank_score` / `.rerank_scored` / `.calibration_id` | four new nullable columns | `recordSearch` | recall stats, future production calibration |
| `agentsmemory eval --calibrate` / `--gate` | new CLI mode and its exit code | operator | operator |
| `EvalCase.AbsentVerification`, `EvalCaseResult.Population` / `TopRerank` / `RerankScored` | new fields carrying label and provenance | eval generation | `--calibrate` |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| population labels, per-case top-1 score/presence, absence-verification provenance | T1 | T2 | No — new fields |
| `eval --calibrate --gate` and its exit code | T2 | T3 | No — new mode |
| `palace.Calibration` (thresholds + fingerprint + id) | T2 | T4, T6 | No — new type |
| the `ship` / `withdraw` decision | T3 | T4 | No — a gate, not a symbol |
| `config.AbstainCalibration` / `WithCalibration` + confirmed canary | T4 | T5 | No — new key, unset is valid |
| `Search` returning a populated `Confidence` | T5 | T6 | No — additive field |

## Implementation

Six tasks — see [`ADR-001-recall-answers-or-abstains/tasks/README.md`](ADR-001-recall-answers-or-abstains/tasks/README.md).

**Precondition — the corpus this runs on is not ours.** Accepted 2026-08-20 on the understanding
that execution waits for a corpus that can falsify it. The palace these tasks would run against was
reset on 2026-08-19 and holds 80 drawers across 8 wings; a smoke run on 2026-08-20 put the retrieval
ceiling at 100% in-pool, top-5 100%, with seven arms tied at MRR 1.000. On a corpus where every
answer is already in the pool, T3's recall constraint (answer-recall ≥ 0.95) is met at a trivially
low threshold, so `--gate` returns exit 0 without testing the premise it exists to test. That is not
a weak measurement, it is a gate that cannot fail — the defect class this repository names in
`AGENTS.md` and hunts with `TestEveryDeclaredArmIsRegistered` and friends, aimed at our own ADR.

Because T3's `ship` sign-off is what authorises T4–T6, running it here would authorise four shipping
tasks on a verdict that means nothing. **T1–T3 run on the upstream maintainer's corpus** (~5,020
drawers, the one every measurement in the Context section was taken on), or on ours once it is large
enough that the ceiling is no longer saturated. Whoever executes T3 must paste the corpus size and
the retrieval ceiling into the Verification Log beside the exit code; a `ship` recorded without them
is not a sign-off.

T1, T2 and T3 are the falsification half and they run first: honest negatives, then the curve and the gate, then the gate run. Nothing that ships starts until T3 records a `ship` sign-off. If identifier-preserving negatives and properly verified labels collapse the measured separation, this gate does not ship on this corpus and three tasks were spent finding that out instead of six.

## Consequences

- **Positive:** an agent can distinguish "the palace holds this" from "the palace holds nothing like this", which is the difference between recall being usable unsupervised and needing a human to sanity-check it. The `unknown` verdict makes the absence — or the staleness — of calibration visible instead of implying confidence nobody measured.
- **Positive:** the calibration procedure is executable and repeatable, so a model, backend or ranking change is re-calibrated rather than silently invalidated — unlike `max_distance`, which nobody could re-derive.
- **Positive:** the absence check T1 repairs is used by every future negative-case run, not only by this ADR. The eval's "unanswerable" column means something stricter afterwards, and past numbers taken with it are not comparable with future ones — which is worth saying out loud rather than discovering in a table.
- **Negative:** operators who configure a reranker but never calibrate get `unknown` forever and see no benefit until they run one command. This is deliberate — the alternative is a guessed constant — but it is real friction and must be documented at the knob.
- **Negative:** a ranking change (including ADR-002's and ADR-003's) now invalidates a calibration and refuses startup until the operator recalibrates or unsets the key. That is the intended cost of a threshold that means something, and it is a boot-time failure an operator meets at the worst moment unless the release notes say so.
- **Negative:** four more nullable columns on the hottest write path in the telemetry table, and one rerank call at boot when a calibration is configured.
- **Neutral:** ranking is untouched. Every arm in the eval scores exactly as before; this ADR adds a judgement about the top result, not a change to which result is on top.

## Out of Scope

- Contradiction reporting — "this changed on <date>, it was X and is now Y" (deferred: docs/adr/BACKLOG.md — blocked on a populated temporal knowledge graph; ~65 triples against ~5,020 drawers)
- The write-time findability gate — testing at file time whether a new memory can be retrieved by the question it answers (deferred: docs/adr/BACKLOG.md — drafted after this ships, since it reuses the same calibration)
- Continuous evaluation with automatic promotion of the winning retrieval configuration (deferred: docs/adr/BACKLOG.md — depends on real-query telemetry volume, currently ~10 rows)
- A profile identity stamped on every production search event, rather than only on the calibration file (deferred: docs/adr/BACKLOG.md — the same telemetry work as continuous evaluation; this ADR records only the calibration id)
- A learned multi-feature abstention classifier (deferred: docs/adr/BACKLOG.md — revisit above ~200 verified-absent cases; twenty-odd cannot fit and hold out)
- Scoring the whole returned page rather than its top hit (deferred: docs/adr/BACKLOG.md — a second curve on the same rows; it is a real question raised by the 75% top-1 ceiling, and adding it here would widen the gate before the gate has run once)
- Changing any ranking arm, fusion weight or the reranker blend (permanent: this ADR judges the top result; what reaches the top is decided elsewhere and measured by the existing arms.)
- Abstention for the consuming model's own generation, e.g. semantic entropy (permanent: we do not own that forward pass; agents receive text over MCP.)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The published separation is inflated: negatives are identifier-stripped and their labels were only checked against the top three hits, some not at all | Certain | High | T1 makes identifier-preserving negatives the default, verifies at retrieval depth 20, and drops rather than keeps a case whose check failed; the ADR states the old claim was wrong rather than quietly restating the number |
| Repaired negatives collapse the separation and no usable operating point exists | High | High | Already visible in the pre-repair tails: at a 0.95 recall target `answer_at` lands below the absent median, so the gate may well fail. T3 runs it before any serving code is written; a non-zero exit withdraws the ADR at three tasks instead of six |
| Thresholds calibrated on ~20 verified-absent cases do not generalise | High | Med | The gate compares a 90% Wilson lower bound, not a point estimate; counts and interval ship inside the calibration file and the report; re-calibration is one command as the corpus grows |
| Unreachable-answerable cases scored as false abstentions, tuning toward over-answering | Med | Med | Three-population scoring (T1); the unreachable count is reported separately as the retrieval ceiling |
| Operator serves a calibration measured on a different model, dialect or quantisation | Med | High | The canary re-scores fixed pairs at startup within a tolerance measured from repeats; on mismatch verdicts are `unknown`, never wrong |
| A ranking change (ADR-002, ADR-003, or a knob turn) moves production top-1 and silently invalidates the thresholds | High | High | The ranking profile is part of the fingerprint and a mismatch refuses startup; the Follow-up requires re-calibration after either ADR lands |
| The `refuse_below` band is vacuous — no verified-absent case scores below it — so `no_answer` never fires | High | Med | Provable at allowance 0 on the pre-repair set (min answerable −6.569 < min absent −6.327), which is why the allowance defaults to 1; the calibrate report prints the absent count below `refuse_below`, and a zero there is reported as "three verdicts on this corpus" rather than shipped as four |
| `no_answer` on a page that does hold the answer at rank 2–5, since the gold is top-1 only ~75% of the time | Med | Med | The verdict never filters hits and is documented as a judgement about the top result; the raw score and both boundaries are returned so a consumer can apply its own bar |
| A caller passes a `limit` above the calibrated regime, so fusion evicts candidates the cross-encoder never scores | Med | Med | The verdict is `unknown` unless `limit × 3 ≤` the calibrated rerank pool, checked per request in T5; the page itself is unaffected |
| Agents ignore the verdict and read hits regardless | High | Low | Additive field; no behaviour is removed, so ignoring it leaves today's behaviour intact |
| The gate is run on a corpus whose retrieval ceiling is saturated, so it passes vacuously and authorises T4–T6 on a meaningless sign-off | Certain on our post-reset corpus | High | The Implementation section makes the corpus a precondition, not a detail; T3's Verification Log must carry the corpus size and the measured ceiling beside the exit code |
| Overlapping tails make any operating point wrong for some queries | Certain | Med | Stated explicitly in the response (the score and both boundaries are returned) and in the calibration file's counts |

## Rollback

Unset `ABSTAIN_CALIBRATION` — every verdict becomes `unknown`, no canary probe runs, and the response is behaviourally identical to today with one extra ignorable field. This is also the escape hatch from a profile-mismatch startup failure: unset the key and the server boots. The four `search_events` columns are nullable and additive; the down migration in T6 drops them, and no read path requires them. No data is rewritten at any point, so rollback loses only the verdicts recorded while it was on.

## Follow-ups


- [ ] Re-calibrate and re-run the gate after ADR-002 or ADR-003 lands, since both change which document reaches production top-1, and record whether the boundaries moved
- [ ] Re-calibrate once the verified-absent corpus exceeds 200 cases and report whether the recommended boundaries moved
- [ ] Received from ADR-009 T1: running the crosslingual, temporal and absent eval styles, which have never been run. ADR-009 tunes against whatever the corpus contains; the styles that would populate those modes are this ADR's calibration instrument, so it owns whether they are worth running.
