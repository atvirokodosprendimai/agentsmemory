# ADR-047: Measure the writing rule, not only the ranking knob

**Status:** Proposed
**Date:** 2026-09-01
**Owner:** kme
**Spec:** None — no spec stage. Grounded in `docs/adr/BACKLOG.md` §"Standing: the instrument is not allowed to decide the hypothesis space", which names the two metrics this ADR builds, and in the LongMemEval-S schema read 2026-09-01 from the benchmark's published field list.
**Cross-references:** `docs/adr/ADR-032-the-corpus-that-chose-our-defaults.md` (why an externally-authored corpus is the point), `docs/adr/ADR-014-the-shipped-default-is-the-measured-one.md` (the principle this extends to prose), `docs/adr/ADR-003-retire-the-closet-prior.md` (cites LongMemEval as corroboration it deliberately never re-derived), `docs/adr/ADR-004-supersession-not-recall.md` (the same citation, load-bearing in a rejection), `docs/adr/ADR-009-tune-against-your-own-corpus.md` (per-install tuning; this is per-*rule* measurement and does not compete), `internal/palace/eval.go`, `internal/palace/evalstats.go`, `cmd/server/eval.go`, `cmd/server/kgextract.go`, `docs/adr/BACKLOG.md`
**Numbering:** next free after ADR-046. Read live across all 388 heads and remotes on 2026-09-01: `docs/adr/` holds ADR-001–ADR-046 somewhere in the corpus; 047 is claimed by nothing.
**Governs:** `internal/longmemeval/**`, `cmd/server/longmemeval.go`

Enumerated with `git ls-files 'internal/longmemeval/*' 'cmd/server/longmemeval*'` — empty today, because this decision creates the class. The class is *every artifact that measures a memory-writing or prompting rule against an externally authored corpus*; `agentsmemory eval` is deliberately NOT a member (it measures ranking against a self-derived corpus) and is left ungoverned by this record.

**Enforced-by:** `internal/longmemeval/policy_test.go::TestEveryDeclaredPolicyIsSelectable`

The decision this enforces is the reachability half: a policy that exists and no flag selects is this repository's signature defect, and a table with a missing row reads as a policy that lost. It does NOT enforce the measurement discipline below — that is prose and no gate can see it, which is why the pre-registration is written into the record rather than into a test.

**Invalidates:** none — checked. ADR-003 §Out of Scope carries `Re-deriving the LongMemEval figures ourselves (permanent: …)`; this ADR does not re-derive them and does not disturb that boundary, but it does build the instrument that would make re-deriving them possible, so the boundary becomes *re-openable by a later record* rather than closed by capability. Said out loud because `permanent` is the one disposition `adr-debt` never sweeps.

**Served-path change:** None. A new CLI subcommand is added and a scratch wing is written; no recall, ranking, MCP tool or default changes. Every conclusion this instrument produces reaches a served surface only through a later, separate change to the centralised skills.

## Context

**Two of this project's own decisions rest on LongMemEval numbers nobody here has ever run.** `ADR-003-retire-the-closet-prior.md:38` quotes summary-as-key indexing costing 0.134 Recall@5, and `:119` quotes +9.4% recall for the concatenated variant; `ADR-004-supersession-not-recall.md:94` uses the same pair inside a rejection. Both are labelled as corroboration rather than evidence, which is honest — and it leaves the benchmark that shapes our reasoning as the one corpus we have never executed.

**Meanwhile the rules that tell agents how to WRITE a memory are asserted, not measured.** The centralised `start-here` skill (v14, read 2026-09-01) instructs every session to open a record with the question's own words, to give experience its own record, and to keep a record under 1600 runes because "one drawer is ONE vector". Those rules carry real measurements of *recall rank* — 0.833 and 0.991 against 0.384 for the same content written as narrative. What no run has ever shown is whether following them makes an agent **answer better**, which is the only thing they are for.

**The existing instrument structurally cannot answer that**, and `docs/adr/BACKLOG.md` already says why in §"Standing: the instrument is not allowed to decide the hypothesis space": *"raw text is a superset of any summary of it, so a superset cannot lose that metric."* Under MRR over a ranked list, the verbatim blob is unbeatable by construction — so every writing rule that compresses, splits or re-titles is scored by an instrument that has already decided against it. That section's prescription is not "measure harder", it is **"when a claim does not fit the instrument, extend the instrument"**, and it names the missing metric first in its list: *answer-support / tokens-to-answer — a metric a superset cannot automatically win, which is the precondition for evaluating any consolidation or compression idea honestly.* This ADR builds that metric.

**And the corpus has to be one that can disagree with us.** ADR-032 measured two question sets on the same palace, same commit, same configuration, and found vector-only was the best arm on one and the worst on the other — because the paraphrase questions were generated by a model *from* the drawers they had to find. Its ruling stands: a measurement inherits the validity of its corpus. Every case style `agentsmemory eval` ships (`paraphrase`, `literal`, `temporal`, `absent-easy`) is derived from our own drawers; `real` replays our own agents' searches. LongMemEval-S was written by people who have never seen this codebase, which is the property no self-derived corpus can acquire.

**What LongMemEval-S provides, read 2026-09-01.** 500 questions over six types (`single-session-user`, `single-session-assistant`, `single-session-preference`, `temporal-reasoning`, `knowledge-update`, `multi-session`), each carrying `question`, `answer`, `question_date`, `haystack_sessions` (~48 sessions of user/assistant turns), `haystack_dates`, `answer_session_ids` (the gold sessions) and per-turn `has_answer: true`. The haystack is *conversation*, not our drawers — which is exactly the input a write policy is a function of.

## Existing Primitives Audit

| Primitive | Where | Reused? |
|---|---|---|
| `questionGen` — Ollama `POST /api/generate` with an OpenAI-compatible `/v1` branch (`openAIShaped()`) | `cmd/server/eval.go:812-1033` | **Yes, and extracted.** It is the reader and the judge. It is already copy-pasted into `cmd/server/kgextract.go:215-267`; this ADR moves the one implementation into `internal/gen` rather than writing a third. |
| `genURL(c)` + `EVAL_GEN_MODEL` / `EVAL_GEN_URL` / `EVAL_GEN_API_KEY` | `cmd/server/eval.go:797-803`, `.env.example:148-156`, `.env.docker.example:63-70` | **Yes, unchanged and no new env.** Operators already have this configured; a second trio of variables for the same endpoint is a knob nobody would set. |
| `WilsonInterval`, `PairedDelta`, `BootstrapMRR`, `Interval` | `internal/palace/evalstats.go:54-291` | Yes. Judged accuracy is a proportion, so Wilson gives the per-cell interval and `PairedDelta` gives the cell-vs-baseline contrast, on the same seeded bootstrap (`bootstrapSeed = 42`) every other table here uses. |
| `Service.Add` / `Service.Search` | `internal/palace` | Yes, through the ordinary API. A write policy that bypassed `Add` would measure a store no agent can write to. |
| `agentsmemory wing delete --wing --confirm` | `cmd/server/wing.go:109-118` | Yes — the rollback path for a scratch wing already exists and needs nothing added. |
| `EvalCase` / `Evaluate` / the ~30 ranking arms | `internal/palace/eval.go:301,650` | **No, deliberately.** `EvalCase` is one query and one gold DRAWER ID; under a write policy the drawer ids are an OUTPUT of the arm, so a case file keyed on them cannot be shared across cells. The gold here is a session id and a judged answer. Reusing the struct would force every policy to produce identically-identified drawers, which is the one thing they must not do. |
| `am_mine` | `internal/mcpserver/mine.go` | No. Mining is itself one candidate write policy, and it enters the table as a policy rather than as the ingestion mechanism for all of them. |

## Decision

**Add `agentsmemory longmemeval`: a command that scores judged answer accuracy over a (write-policy × query-policy) grid on the LongMemEval-S corpus, with a fixed context-token budget shared by every cell.**

One run of one cell is: load a question's haystack; ingest every session through `Service.Add` under the named **write policy**; ask the question through `Service.Search` under the named **query policy**; assemble the returned memories into the reader prompt up to `--context-tokens`; have the reader answer; have the judge score that answer against the gold `answer`. The cell's score is the share judged correct, with a Wilson interval; the headline contrast is `PairedDelta` against the baseline cell, on the same questions.

Six properties make it an instrument rather than a way to confirm what the skills already say.

1. **The context budget is fixed across every cell, and it is the whole point.** A policy that writes more text must fit it into the same reader window as one that writes less. This is what makes the metric one a superset cannot automatically win, and it is the difference between this table and every MRR table in the tree. A run whose budget differs between cells is not comparable and the command refuses it.

   ⚠**The unit is RUNES, and the flag is `--context-runes`.** It is deliberately not called a token budget, because this repository cannot measure tokens: `go.mod` declares no tokenizer, and `internal/palace/chunk.go:52-54` already records the same constraint in the same words — *"Characters rather than tokens because the palace cannot ask the tokenizer, and the ratio is script-dependent"*. Naming the instrument's load-bearing invariant after a unit nothing here can compute would make the central property unauditable, which is worse than an approximation stated as one. The approximation is not free, and the way it could bite is specific: the policies under test rewrite and split text differently, so equal rune counts can carry unequal token counts and a policy could in principle buy context rather than earn it. Two things bound that rather than one. The corpus is a single language, so the ratio moves with what a policy does to the text and not with its script; and each cell records the reader endpoint's own reported prompt-token count wherever the endpoint supplies one, so the realised token spread across a row is a number in the results file instead of an assumption. A run whose realised spread exceeds the tolerance declared in its header is reported as not comparable rather than tabulated. Adding a real tokenizer tied to the reader model is a deferral, not a refusal — see §Out of Scope.
2. **The baseline is the verbatim session**, not the policy we hope wins. `write=verbatim` writes each haystack session as one drawer, unedited — the thing an agent does when it follows no rule at all. Every other policy is scored as a delta against that, so "our advice helps" is a claim that has to survive a paired interval rather than a table someone reads charitably.
3. **The reader and the judge are one model, held fixed for the whole grid**, resolved from `EVAL_GEN_*`. The cell delta is then the policy. The run records the model id, and cells taken under different models are never pooled — the same rule ADR-024 already applies to ranking profiles.
4. **The judge is blind to the policy, and it is not blind to the question type.** It receives the question, the question's `question_type`, whether the item is an abstention (`_abs`) question, the gold answer and the candidate answer — and nothing that names which cell produced it. Blindness to the cell is stated because the judge prompt is the one place a preferred outcome could be smuggled in for free. The type is passed because the benchmark's own evaluator branches on it: preference questions are scored against a rubric rather than for equality, temporal answers tolerate a stated off-by-one, knowledge-update answers accept the superseded value when the update is present too, and `_abs` items are scored for unanswerability rather than for content. One generic consistency prompt reproduces none of that, so a number taken from one is not LongMemEval answer accuracy however fixed the model is held. T3 pins the type-specific behaviour with tests against the upstream evaluator's rules. Where our judge deliberately departs from those rules, the results file names the metric as ours and says where it differs — a house metric argued openly is defensible; a house metric reported under the benchmark's name is not.
5. **The decision rule is pre-registered here, before any cell is run.** A write or query policy may be promoted into the centralised skills as an instruction only when its paired delta against the baseline **excludes zero on a held-out half** of the question set, the argmax having been taken on the other half. A policy whose interval spans zero is reported as measured-and-neutral and the skill text is left alone.
6. **The first run is a smoke test of the instrument and carries no promotion authority in either direction.** The pilot `--n` is small by design — it is why `Subset` stratifies at all — and each half of a small subset holds a handful of questions per type, so a paired interval on it spans zero for almost any real effect. A neutral pilot is therefore what this instrument says at that size whether the rules work or not, and quoting one as evidence that a writing rule does not matter would be reading the sample size as a finding. **No promotion decision, and no retirement of an existing skill rule, may be taken from a run whose held-out half is the pilot subset.** A run that wants promotion authority must state, in its sign-off and before it is taken, the effect size it is powered to detect and the `--n` that gives the held-out half that power. Widening the subset after a neutral result is then a new pre-registration and a new sign-off, never a re-reading of the old one — which is the hazard T5's Risks section already names from the other side.

**What would make that rule fail, and whether such data exists.** It fails whenever a policy's interval spans zero, which is the outcome for any policy that does nothing — and, at pilot sizes, for policies that do something too, which is exactly why property 6 denies the pilot any authority to conclude from that. Sized for power, the rule stays falsifiable on the data we have: LongMemEval-S is 500 questions across six types, and the most likely single outcome of a properly powered run is still that some rules `start-here` states with confidence turn out to be neutral. The rule is valid **for** this corpus, this reader/judge model, this context budget and this ranking profile; a threshold is always valid for a configuration, and the run writes all four into its results file.

## Alternatives Considered

- **Retrieval-only recall@k against `answer_session_ids` / `has_answer`.** Deterministic, free, no LLM, and it plugs straight into `EvalReport`. **Recommended by the author and rejected by the owner**, correctly: it is the metric `docs/adr/BACKLOG.md` §"Standing…" names as unable to express this question, because a superset cannot lose it. It would have scored the verbatim baseline as the winner by construction and retired every writing rule in `start-here` on an artefact of the instrument. Kept as a **secondary column** in the results file — it is nearly free once the harness exists, and the gap between it and judged accuracy is itself the evidence that the two metrics disagree.
- **Run LongMemEval-V2's own Python harness with the palace as a registered `Memory` backend** (`insert(trajectory)` / `query(q)`, `@register_memory`). Rejected: it introduces a conda environment and a Python evaluation path into a Go repository whose entire gate corpus reads Go source, and its reader is pinned to the benchmark's model — so a cell delta would partly measure their reader. Its numbers are also not what we want: comparability with a public leaderboard is a different goal from deciding what a skill should tell an agent. Deferred, not dismissed.
- **Generate an external-looking corpus with `EVAL_GEN_MODEL` instead of downloading one.** Rejected by ADR-032 in advance: a corpus generated from the drawers it must find cannot disagree with the system that produced them, and this ADR exists precisely to buy a corpus that can.
- **Add `--style longmemeval` to `agentsmemory eval`.** Rejected as the shape, adopted in part. `eval`'s unit is one query and one gold drawer id, and here the drawer ids are produced BY the arm under test — the two axes are orthogonal and forcing them into one case file would make every policy share an identity it must not have. The statistics (`evalstats.go`) are reused wholesale; the case generator is not.
- **Score every cell against the full ~48-session haystack from the start.** Rejected for T1–T4 and deferred: 500 questions × ~48 sessions × |policies| × |query policies| embeddings is a corpus-scale ingest before anything has been shown to work. The grid runs on a declared subset with the subset written into the results file, and the full run is a later task with its own cost estimate.

## Component / Boundary Impact

New package `internal/longmemeval` — owns the dataset schema, the policy registries, the grid runner and the results file. One reason to change: how a writing or prompting rule is measured.

New package `internal/gen` — owns the generative-model client extracted from `cmd/server/eval.go` and `cmd/server/kgextract.go`. One reason to change: how this repository talks to a generative endpoint. Both existing call sites move to it in T3; no third copy is created.

`cmd/server/longmemeval.go` is the composition root only: flags, registry lookup, wiring, output. No policy or scoring logic lives there.

`internal/palace` is unchanged and is called as a consumer would call it.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `agentsmemory longmemeval` CLI subcommand | add | `cmd/server/longmemeval.go` | operators; `main.go`'s command list |
| `--data`, `--wing`, `--write`, `--query`, `--n`, `--context-runes`, `--out` flags | add | `cmd/server/longmemeval.go` | the run; `TestEveryFlagIsRead` |
| `EVAL_GEN_MODEL` / `EVAL_GEN_URL` / `EVAL_GEN_API_KEY` | **no change** — read by a third caller | `.env.example`, `.env.docker.example` | `internal/gen`, and through it `eval`, `kgextract`, `longmemeval` |
| `gen.Client` (extracted from `questionGen`) | add | `internal/gen` | `cmd/server/eval.go`, `cmd/server/kgextract.go`, `internal/longmemeval` |
| `longmemeval.WritePolicy` / `QueryPolicy` registries | add | `internal/longmemeval` | `cmd/server/longmemeval.go`, `TestEveryDeclaredPolicyIsSelectable` |
| `<out>.cells.json` results file | add | `internal/longmemeval` | humans; any later promotion decision |
| Database schema | **None** — the run writes ordinary drawers into a scratch wing through `Service.Add` | — | — |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `longmemeval.Dataset` / `Question` / `Session` (T1) | T1 | T2, T4 | No — new types |
| `longmemeval.WritePolicy` registry (T2) | T2 | T4, T5 | No — new registry |
| `gen.Client` (T3) | T3 | T4 | **Yes** — `questionGen` is deleted from two files and its call sites move; both must build in the same commit |
| `longmemeval.QueryPolicy` registry + `Judge` (T3) | T3 | T4, T5 | No — new |
| `longmemeval.RunGrid()` + `<out>.cells.json` (T4) | T4 | T5 | No — new |

## Implementation

See `tasks/README.md`. Five tasks, strictly sequential: dataset, write policies, the shared generator plus query policies and the judge, the grid command, then the run that decides what — if anything — the skills are allowed to say.

T5 is a human-observed decision task and it is the only one that may change a centralised skill. Nothing about `start-here` is edited before T5's sign-off, for the reason ADR-001's plan puts its falsification half first: four tasks can otherwise land before the ADR learns its premise failed.

## Consequences

- **Positive:** the writing rules every session is made to follow become measurable, on a corpus that did not come from us, by a metric a verbatim blob cannot win by construction. The two LongMemEval figures ADR-003 and ADR-004 lean on stop being the only numbers here nobody can reproduce.
- **Positive:** one generative client instead of two-going-on-three, reachable from `internal/`, reusing the env trio operators already set.
- **Negative:** the run costs LLM inference for every cell — questions × policies × query policies × 2 calls — and the honest expectation is that several rules the skills state confidently come back neutral. Someone then has to weaken skill text that reads well.
- **Negative:** an LLM judge introduces variance the MRR tables do not have. Mitigated by holding one model across the grid and by paired contrasts on identical questions, not eliminated.
- **Neutral:** a scratch wing per run accumulates drawers in whatever palace the command points at. `wing delete` already exists; the command refuses a wing that is not empty.

## Out of Scope

- LongMemEval-V2, its web/enterprise domains, screenshots, and any leaderboard submission (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")
- Crossing these policies with the ~30 ranking arms in one table (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")
- Running the full ~48-session haystack for all 500 questions rather than a declared subset (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")
- Re-deriving ADR-003's summary-as-key and concatenation figures, which this instrument would make possible (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")
- A tokenizer tied to the reader model, so the shared budget can be counted in tokens rather than in runes (deferred: `docs/adr/BACKLOG.md` §"From ADR-047"; the pilot enforces a rune budget and records the endpoint's reported prompt-token count beside it, which is what makes the approximation auditable rather than assumed)
- Changing any ranking default from this table (permanent: ranking defaults belong to ADR-002, ADR-003 and ADR-014; a writing-policy corpus is not the population those were chosen on, and letting it move them would repeat exactly the corpus-substitution error ADR-032 recorded)
- Shipping the dataset itself into the repository (permanent: it is third-party data with its own licence; the command takes a `--data` path and fetching is the operator's, checked at `cmd/server/wing.go`-style flag level)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Judge agrees with fluency rather than correctness, flattering verbose policies | Med | High | Judge sees the gold answer and is asked for a binary consistency verdict; T3 pins its prompt and T5 reports the judge's own agreement against a hand-scored sample of the run |
| The instrument confirms `start-here` because its author wrote both | Med | High | Baseline is verbatim, decision rule is pre-registered above, argmax and confirmation are on different halves |
| A policy exists and no flag selects it, so its row is silently absent | Med | Med | `TestEveryDeclaredPolicyIsSelectable` derives its universe from the registry; the repo's signature defect (§Reachability in `AGENTS.md`) |
| Extracting `questionGen` breaks `eval` or `kgextract` | Low | High | T3 moves both call sites in one commit; the existing `cmd/server` suite is inside T3's fence |
| Cells pooled across different reader models or context budgets | Low | High | Both written into `.cells.json`; the runner refuses to merge files whose header differs |
| The subset is small enough that everything is neutral | High | Med | This is a legitimate result and is reported as one; property 6 denies the pilot any promotion or retirement authority, so a neutral pilot cannot be quoted as evidence a rule does not matter; `--n`, the seed and the realised interval widths are in the results file so a later, powered run is comparable |
| A rune budget is not a token budget, and policies that rewrite text differently spend it differently | Med | Med | Property 1: the unit is named honestly, the corpus is one language, and each cell records the reader endpoint's reported prompt-token count where it supplies one, so the realised spread is measured rather than assumed; a run exceeding its declared tolerance is reported as not comparable |

## Rollback

The command writes ordinary drawers into a scratch wing through the public API and changes no schema, no default and no served path. Undo is `agentsmemory wing delete --wing <scratch> --confirm <scratch>` (`cmd/server/wing.go:109`) for the data, and reverting the branch for the code. The one irreversible-looking step is T3's extraction of `questionGen` into `internal/gen`; it is a pure move with both call sites in the same commit, so reverting that commit restores both files together.

## Follow-ups

- [ ] Report the judge's agreement with a hand-scored sample, so the judge itself has a measured error rate rather than an assumed one (T5)
- [ ] Decide whether the retrieval-only column and the judged column disagree in the direction §"Standing…" predicts; if they do not, that section's premise needs amending
