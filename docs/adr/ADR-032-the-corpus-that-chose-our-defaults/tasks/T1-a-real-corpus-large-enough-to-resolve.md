# Task ADR-032-T1: A real corpus large enough to resolve a default

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S in code, ~1 hour of machine time
**Owner:** unassigned
**Produces:** a committed real-query case file of ~45 judged cases (70 sampled), and the arm table over it
**Consumes:** none
**Data dependency:** REQUIRES `search_events` telemetry — this task cannot run on a fresh install

## Goal

The corpus that decides our defaults is made of questions somebody actually asked, and is large enough that a 0.12 MRR gap can resolve.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/` | add | the run's table and the case-set id, beside the two that motivated it |
| the generated case file | add (data) | committed so the comparison in T2 is reproducible against a fixed set |

No production code changes. `--style real` already exists; it was simply not the corpus anything decided on.

## Precondition — the falsifiability check

**Re-run the existing 26-case real corpus first and confirm it reproduces the inversion** — `vector` below `hybrid`, paired interval excluding zero. If it does not reproduce, STOP: the finding was a single-run artifact and this ADR's premise is gone. Record either outcome. It is a precondition rather than a step because it decides whether the task runs at all.

## Ordered Steps

1. **Write the fence's failing condition first.** Create the evidence file path the Acceptance block greps and confirm the fence is RED against it — an absent file, and then a deliberately truncated table — so the fence is known to reject a half-finished run before any run is trusted by it. This is the measurement analogue of a red test: the gate must be shown to fail before it is allowed to pass.
2. Sample 70 recorded searches (`--style real --n 70`), judged into a case file. Expect attrition: 40 sampled produced 26 scorable (65%), so budget ~45 usable.

   **The budget is one hour, set by the project owner, and it is what sizes this.** Measured on the 2026-08-25 run: ~12s per query to judge and ~70s per scorable case to score all 36 arms, so 70 sampled is ~14 min judging plus ~52 min arms. Sampling 200 would take four hours and buy resolution on contrasts nobody needs — `hybrid` 0.818 against `anchored:ceiling` 0.821 will not separate at any reachable n, and does not have to.

   n≈45 is chosen against the ONE contrast that needs it. `vector` (worse by 0.10–0.38) and `production` (worse by 0.02–0.25) already resolve at n=26. `rrf` sits at "worse by 0.00–0.21" — an interval touching zero — and rrf is the knob T2 would flip, so it is the only contrast this run has to buy.
3. Commit the case file so T2 measures against a FIXED set. A corpus regenerated per run cannot support a paired comparison across runs.
4. Run the full arm table over it and record it in `evidence/`.
5. Report the `FUSION` and `RERANK_WEIGHT` verdicts with their paired intervals — not the winner alone.

## Acceptance

```bash
test -f docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/real-corpus-large.md
# the run must have scored a real number of cases, not a filter that matched nothing
grep -qE '^n=([4-9][0-9]|[1-9][0-9]{2})' docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/real-corpus-large.md
# and it must carry BOTH disputed knobs' rows, or it cannot answer the question it was run for
grep -qE '^(fusion bm25=|hybrid )' docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/real-corpus-large.md
grep -qE '^rerank blend w=0\.25' docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/real-corpus-large.md
grep -qE '^vector' docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/real-corpus-large.md
```

The `n=` pattern requires at least 40, so a run that died halfway cannot satisfy the fence with a partial table — the specific failure mode named in the ADR's Risks. It is set BELOW the ~45 expected rather than at it, because judging attrition is variable and a fence tuned to the expected value would fail an honest run.

⚠ **Worth knowing what that guard is actually anchored to, because it is not obvious and it is only half deliberate.** `^n=` is line-anchored over a prose document, and two lines in `evidence/real-corpus-large.md` satisfy it: the table's methodology footnote — which a run that died before printing its table would not emit, so it is load-bearing exactly as intended — and, by accident, a WRAPPED SENTENCE (`n=54. This is the part of the corpus inversion that survives.`) where a paragraph happened to break. The guard works, on the footnote. The prose match is a coincidence of line width that a reflow would remove, and it is not what anyone should be relying on.

Left alone rather than repaired: the redundancy costs nothing, and rewriting a committed evidence artifact to tidy a guard that already holds is a worse trade than saying where the guard really lives. ⚠ And a note on how this was nearly mis-recorded: the first reading of it claimed the PROSE line was the only anchor and the guard therefore did nothing. That came from a `grep … | head -8` whose output was cut before the footnote — a conclusion drawn from a truncated population, about a fence whose whole purpose is to reject one. Re-running the grep without `head` is what corrected it.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| — | — | This task produces a MEASUREMENT, not behaviour. Its fence checks the artifact exists and is complete; there is nothing to unit-test, and pretending otherwise would be ceremony. | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the case file and the table are committed |
| 2 — something selects it | T2 reads this table and nothing else |
| 3 — the caller can discover it | it lands in `evidence/`, where ADR-001 already keeps its run |
| 4 — it is used | T2 either changes a default citing it, or records that it did not |

## Mutation Log

_(This said "not applicable: no production code changes" and that was a category error, not a fact about this task. There is no production code, but the ARTIFACT is what the fence reads, so the artifact is what a mutant breaks — and step 1 above already prescribed exactly that, in writing, and it was never run. A measurement task is not exempt from showing its gate can fail; it just mutates its evidence instead of its code.)_
- 2026-09-07 · c0d2a02* · mutant killed · exit 1 · `docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/real-corpus-large.md` · Truncates the arm table by removing its `vector` row, which is step 1 of this task Ordered Steps and was never recorded: the fence must be shown to REJECT a half-finished run before any run is trusted by it. This is the specific failure mode the ADR Risks name — a run that dies partway leaving a partial table that reads like a result. The `^vector` anchor is the only one in the file, and `vector` is also the arm whose verdict is the one contrast that resolved at both n=26 and n=54, so a table missing it cannot answer the question the run was made for. · acceptance-sha256:92fb28bee04bcbfc4eed3e8d726394f73b14fe3f1ab207bcf83c561e8673caa0

## Invariants

- The case file is COMMITTED and fixed. A regenerated corpus cannot support a paired comparison across runs.
- Both corpora's tables stay in `evidence/`. The disagreement is the finding; deleting the loser hides it.
- No default changes in this task.

## Risks

- A run that dies halfway leaves a partial table that must not be read as a result. ⚠ This said the `n=` fence is what refuses it; measured 2026-09-07, the ROW checks are. Truncating the table's `vector` row turns the fence red on `grep -qE '^vector'` — while both `n=` anchors survive that truncation, because one is a prose sentence and the other a methodology footnote, and neither is part of the table. `n=` guards the sample SIZE; the three row patterns guard completeness. Crediting the wrong one is how a guard gets deleted as redundant.
- **n≈45 may still not resolve the fusion contrast.** It is sized to a time budget, not to a power calculation, and that is a deliberate trade rather than an oversight. If the interval still spans zero, T2's Precondition applies and nothing flips — which is a legitimate outcome, not a failed run.
- Sampling 70 of 721 recorded searches over-represents whatever the team searched for most; note the sampling method in the evidence file rather than leaving it implicit.

## Stop Condition

**Stop and report if step 1 fails to reproduce the inversion.** That would mean the two-corpus finding is a single-run artifact, and everything downstream of it — including this ADR — needs withdrawing rather than continuing.

## Out of Scope

- Changing any default (T2)
- A stronger judge than `qwen2.5-coder:7b` (deferred: `docs/adr/BACKLOG.md` §"From ADR-032")
- The unanswered-query rate (deferred: same section)

## Verification Log
- 2026-09-07 · c0d2a02 · exit 0 · `test -f docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/real-corpus-large.md …` · acceptance-sha256:92fb28bee04bcbfc4eed3e8d726394f73b14fe3f1ab207bcf83c561e8673caa0 · ms:20
- 2026-09-07 · c0d2a02* · exit 0 · `test -f docs/adr/ADR-032-the-corpus-that-chose-our-defaults/evidence/real-corpus-large.md …` · acceptance-sha256:92fb28bee04bcbfc4eed3e8d726394f73b14fe3f1ab207bcf83c561e8673caa0 · ms:9
