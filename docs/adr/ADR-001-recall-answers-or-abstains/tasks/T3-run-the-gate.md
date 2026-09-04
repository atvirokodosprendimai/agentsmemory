# Task ADR-001-T3: Run the gate on the real corpus and decide whether the rest of the ADR is built

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** the go/no-go decision, the recorded risk–coverage evidence, and the calibration file the server will be pointed at
**Consumes:** `agentsmemory eval --calibrate` with `--gate` (T2)

## Goal

Find out, before any of the serving code exists, whether a usable operating point survives on identifier-preserving negatives — and stop the ADR here if it does not.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/ADR-001-recall-answers-or-abstains/evidence/abstain-gate-2026-08.md` | add | the curve, both boundaries, the counts, the Wilson bound and the gate's exit code, so the decision has a run behind it rather than a memory of one |

## Ordered Steps

**Preflight — before step 1, check that the corpus can falsify the gate at all.** Run the eval once and read the retrieval ceiling it prints. If the answer is already in the pool for essentially every case, the answer-recall ≥ 0.95 constraint is met at a trivially low threshold and `--gate` exits 0 without testing anything — a pass here is not evidence, it is the instrument agreeing with itself. This is not hypothetical: on 2026-08-20 our own palace (80 drawers, post-reset) measured 100% in-pool, top-5 100%, with seven arms tied at MRR 1.000. Run this task against the upstream maintainer's ~5,020-drawer corpus, or against ours once its ceiling is no longer saturated. The corpus size and the measured ceiling go into the sign-off line beside the exit code.

Then the steps proper, test-first as everywhere else:

1. Re-run T1's and T2's acceptance commands first and confirm both test sets are green. A curve taken while the population labels or the verification drop are still red is not evidence — it is the old easy-negative measurement wearing a new name.
2. Generate a fresh case set with the identifier-preserving generator and the depth-20 verification (`agentsmemory eval --style absent --n 25 --cases <file>`), keeping the file, and record how many candidates survived verification and how many were dropped for which reason.
3. Generate or reuse the answerable set at the same settings so both populations come from one corpus and one build, and run the eval so the production arm scores every case.
4. Run `agentsmemory eval --calibrate --gate --cases <files>` and capture its whole output — the curve, `answer_at`, `refuse_below`, the count of absent cases below `refuse_below`, the refusal rate with its Wilson bound, the sample sizes, and the exit code.
5. Run the same command against a case set from `--style absent-easy` and record both curves side by side. That run exits non-zero by construction and writes no calibration file — it exists only for the comparison. The gap between the two curves is the size of the error the old corpus was hiding, and it is the one number that tells a future reader why this task exists.
6. Write the evidence file, then sign off with `adr-verify --human` stating the exit code. On a non-zero exit, stop: T4, T5 and T6 are not started, and the ADR is marked Withdrawn with the table attached.

## Acceptance

Acceptance is human-observed: the gate needs a populated palace, a live reranker and a generator model, so no hermetic exit code can stand in for it. Sign-off step —

```text
~/.claude/bin/adr-verify docs/adr/ADR-001-recall-answers-or-abstains/tasks/T3-run-the-gate.md \
  --human "corpus <n> drawers, retrieval ceiling in-pool <c>%; gate run on hard negatives: <n> verified-absent (<n> dropped), <n> reachable-answerable; answer_at=<x> refuse_below=<y>; refusal <k>/<n> = <r>, 90% Wilson lower bound <b> against the declared 0.30; absent cases below refuse_below = <n>; eval --calibrate --gate exit <0|1>; recorded in evidence/abstain-gate-2026-08.md; decision <ship|withdraw|blocked>"
```

⚠ **`blocked` is the third value and it is not decoration.** A run can END WITHOUT DECIDING — this
task's preflight disqualifies a saturated corpus, and the 2026-08-22 run hit exactly that: the gate
ran, exited 1, and the honest verdict was neither ship nor withdraw. The first version of this hint
offered two values, so that outcome went into free text where no tool reads it, and every routing
tool went on reporting the task done. `TestAHumanObservedSignOffAgreesWithTheIndex` now requires the
sign-off to name one of the three and the sibling README to carry the status it maps to
(`ship`→`done`, `withdraw`→`failed`, `blocked`→`blocked`).

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| — (human-observed) | `docs/adr/ADR-001-recall-answers-or-abstains/evidence/abstain-gate-2026-08.md` | the recorded run is the evidence; T1's and T2's tests are what prove the instrument was honest when it was taken | — |

## Invariants

- Both regimes are measured with the same binary, the same corpus and the same generator settings; a hard-negative curve compared against an easy-negative curve from another build is not a comparison.
- The decision is the exit code of `--gate`, not a reading of the curve. A failing gate is recorded and acted on; it is never re-run with a lower target to obtain a pass.

## Risks

- The gate fails and the ADR is withdrawn. That is the outcome this task exists to make cheap: three files of measurement code instead of a config key, a wire field, a migration and a verdict nobody could trust.
- The gate passes on a sample this small and the threshold still generalises badly. Mitigation: the Wilson bound is the comparison, the counts ship in the calibration file, and the ADR's Follow-up requires a re-calibration as the corpus grows.

## Stop Condition

Stop the ADR — not just this task — if `--gate` exits non-zero: no threshold reaching the declared answer-recall refuses enough unanswerable queries to be worth a knob, a wire field and a column. Stop before running anything if the corpus's retrieval ceiling is saturated — a gate that cannot
fail authorises T4–T6 on a verdict that means nothing, and running it anyway is worse than not
running it, because the Verification Log would then hold a `ship` nobody can distinguish from a real
one. Stop and ask if the two regimes disagree in direction (the easy set passing while the hard set fails is expected and is a pass for the *method*; the reverse means something is wired wrong).

## Out of Scope

- Any serving code — T4, T5 and T6 own that, and none of them starts until this task's log holds a `ship` sign-off.
- Re-calibrating after ADR-002 or ADR-003 change which document reaches top-1 (deferred: docs/adr/ADR-001-recall-answers-or-abstains.md — Follow-ups)

## Verification Log
- 2026-08-22 · human-observed · corpus 449 drawers, retrieval ceiling in-pool 100% — SATURATED, which T3's own preflight disqualifies; gate run on hard negatives: 17 verified-absent (8 dropped as answerable at depth 20), 21 reachable-answerable; answer_at=-3.1407 refuse_below=-3.1407 (band EMPTY); refusal 3/17 = 0.176, 90% Wilson lower bound 0.073 against the declared 0.30; absent cases below refuse_below = 0; eval --calibrate --gate exit 1; no threshold on the curve clears both bars, best point 0.920 recall / 0.294 refusal misses by 0.03 and 0.006; recorded in evidence/abstain-gate-2026-08.md; decision BLOCKED — neither ship nor withdraw, because the preflight names this corpus unfit to decide on; T4/T5/T6 not started
- 2026-09-05 · human-observed · corpus ~3,800 drawers across all wings, retrieval ceiling in-pool 76% on the gated answerable set (90% on a 40-case set) — unsaturated, preflight passes; gate run on hard negatives: 18 verified-absent (7 dropped as answerable at depth 20), 19 reachable-answerable (6 unreachable excluded); answer_at=-7.2799 refuse_below=-7.2799 (band EMPTY); refusal 0/18 = 0.000, 90% Wilson lower bound 0.000 against the declared 0.30; absent cases below refuse_below = 0; eval --calibrate --gate exit 1 (read unpiped); no threshold on the curve clears both bars, best separating signal top_rerank AUC 0.63; easy-negative comparison fails the same way, 0/19, AUC 0.60; recorded in evidence/abstain-gate-2026-09.md; decision withdraw

## Mutation Log
