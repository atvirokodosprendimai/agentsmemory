# Abstention gate — run of 2026-09-04/05

**Decision: withdraw.** The preflight passed this time — the corpus is no longer
saturated — so the gate could be taken, and it failed on the criterion the ADR
declared before the run: at the threshold holding answer recall ≥ 0.95, the
90% Wilson lower bound on correct refusal over verified-absent cases is 0.000
against the declared 0.30. No threshold on the curve clears both bars. T4, T5
and T6 are not started.

The 2026-08 run (`abstain-gate-2026-08.md`) could not decide because its corpus
put every answer in the pool; this run decides because its corpus does not. Both
files stay: the first is why the task has a preflight, the second is the answer.

## The preflight, which passes

T3 disqualifies a corpus whose retrieval ceiling is saturated. Measured first on
40 paraphrase cases over all wings (pool 50, vector distance alone):

```
retrieval ceiling — where the answer sits by VECTOR DISTANCE alone:
  in pool: 90%   top-1 42%   top-5 68%   top-10 75%   top-20 78%   top-50 90%
  4 of 40 answer(s) were never retrieved at all
```

and again on the 25-case answerable set the gate was scored over:

```
  in pool: 76%   top-1 48%   top-5 68%   top-10 76%   top-20 76%   top-50 76%
  6 of 25 answer(s) were never retrieved at all
```

Neither is the 100% in-pool state the preflight names. The corpus is ~3,800
drawers across nineteen wings (`am_status` on the day), against 449 when the
2026-08 run was disqualified; the eval's pool of 50 is now about 1.3% of it.

## What was measured

Both populations from one corpus and one build (`v0.0.114`, the served palace,
`docker exec` into `agentsmemory-agentsmemory-1`), the production arm scoring
every case, live cross-encoder (`--rerank-timeout 60s`, warmed first — the
default 10 s budget is exceeded by the reranker's cold start, and the eval then
DROPS every reranked arm rather than score them as hybrid).

| | |
|---|---|
| corpus | all wings, ~3,800 drawers |
| verified-absent | **18** of 25 generated (`--style absent`) — 7 rejected because another memory answered them at depth 20 |
| reachable-answerable | **19** of 25 generated — 6 unreachable (gold never entered the pool), excluded from calibration |
| `answer_at` | −7.2799 (target recall 0.95, achieved 1.000) |
| `refuse_below` | −7.2799 (allowance 1) — **the band is EMPTY**: both rules chose the same threshold, so this corpus supports three verdicts and not four |
| verified-absent below `refuse_below` | **0 of 18** — the "no answer" verdict can never fire on evidence like this sample |
| correct refusal at `answer_at` | **0/18 = 0.000**, 90% Wilson lower bound **0.000**, bar 0.30 — **FAIL** |
| `eval --calibrate --gate` | exit **1** — the replay for the exit code was run unpiped and reproduced the verdict exactly (0/18, lower bound 0.000; the best-signal AUC read 0.62 on the replay against 0.63 first time, the cross-encoder is not bit-deterministic) |

The risk–coverage curve, every threshold the calibrator considered:

```
threshold    answer recall    correct refusal
-7.3963      1.000 (19)      0.000 ( 0)
-5.2574      0.947 (18)      0.056 ( 1)
-2.2426      0.947 (18)      0.222 ( 4)
-1.6222      0.895 (17)      0.333 ( 6)
-1.4744      0.842 (16)      0.444 ( 8)
-0.6783      0.789 (15)      0.556 (10)
-0.5052      0.737 (14)      0.667 (12)
-0.2497      0.632 (12)      0.722 (13)
 0.3034      0.579 (11)      0.833 (15)
 0.5824      0.474 ( 9)      0.889 (16)
 1.8703      0.316 ( 6)      0.889 (16)
 2.6847      0.263 ( 5)      1.000 (18)
 3.8509      0.105 ( 2)      1.000 (18)
 4.4836      0.000 ( 0)      1.000 (18)
```

The closest point to both bars is −2.2426: recall 0.947 (one short of the 0.95
target) and refusal 0.222 (below 0.30, and its 90% lower bound lower still). The
first threshold that refuses 30% of absent cases, −1.6222, costs recall down to
0.895. Retuning cannot reach a point that does not exist on this curve.

## The signal, which is the finding

The eval measures how well each candidate score separates the two populations:

```
top-1 distance:     answerable 0.219–0.504 (median 0.397) | unanswerable 0.318–0.486 (median 0.411) — OVERLAP
top-1 rerank score: answerable -7.605–4.367 (median -0.291) | unanswerable -5.656–2.179 (median -1.463)

separation by signal (n=25 answerable, 18 unanswerable):
  dist_gap      AUC 0.48
  dist_spread   AUC 0.49
  top_rerank    AUC 0.63
  top_gap       AUC 0.52
  score_spread  AUC 0.54
  best separating signal: top_rerank (AUC 0.63) — NO signal here separates
```

The cross-encoder's top-1 score is the best of the five and it is barely above
chance. On identifier-preserving negatives — questions that share the vocabulary
of a real memory but whose answer is not in the palace — the thing the gate
would read cannot tell the two apart. That is the premise ADR-001 was falsifiable
on, and it is false on this corpus.

## The easy-negative comparison (T3 step 5)

`--style absent-easy`, 25 generated, **19** verified-absent at depth 20 (6
rejected), gated against the same 25-case answerable set on the same build:

```
top-1 rerank score: answerable -7.605–4.367 (median -0.291) | unanswerable -5.911–4.555 (median -1.230)
separation by signal (n=25 answerable, 19 unanswerable):
  dist_gap 0.53   dist_spread 0.58   top_rerank 0.60   top_gap 0.59   score_spread 0.51
  best separating signal: top_rerank (AUC 0.60) — NO signal here separates
calibration — 19 reachable-answerable, 19 verified-absent, 6 unreachable (excluded)
  answer_at -7.2799   refuse_below -7.2799   band EMPTY
  correct refusal at answer_at: 0/19 = 0.000, 90% lower bound 0.000, bar 0.30 — FAIL
threshold    answer recall    correct refusal
-7.3982      1.000 (19)      0.000 ( 0)
-5.5909      0.947 (18)      0.105 ( 2)
-2.2931      0.947 (18)      0.316 ( 6)
-1.5697      0.842 (16)      0.421 ( 8)
-0.6776      0.789 (15)      0.579 (11)
-0.2909      0.684 (13)      0.684 (13)
 0.5824      0.474 ( 9)      0.684 (13)
 0.9721      0.421 ( 8)      0.842 (16)
 2.8314      0.211 ( 4)      0.842 (16)
 3.7951      0.105 ( 2)      0.947 (18)
 4.6729      0.000 ( 0)      1.000 (19)
```

**The gap the step exists to measure is within noise on this corpus.** The easy
regime's best signal is AUC 0.60 against the hard regime's 0.63; its nearest
point to the bars is −2.2931 (recall 0.947, refusal 0.316) against the hard
set's −2.2426 (recall 0.947, refusal 0.222). Neither clears both, and one
answerable case short of the recall target is the whole difference. T3's Stop
Condition names the case where the regimes *disagree in direction* — easy
passing while hard fails is a pass for the method, the reverse means something
is wired wrong — and this is neither: both fail the same way. What it does say
is that in 2026-08 it was the corpus's saturation, not the negative style, that
hid the error, and that for this scorer on this corpus the identifier-preserving
generator does not produce measurably harder negatives than the easy one. That
narrows T1's contribution rather than undermining the verdict.

## What is NOT concluded

- **Not** that abstention is impossible — that the five scores this eval can
  read do not carry it on this corpus. The eval printed the three levers: change
  the score the gate reads, the corpus it is calibrated on, or the targets.
  Each of those is a new proposal, not a retune of this one.
- **Not** a claim about the upstream maintainer's ~5,020-drawer corpus, which
  the task also names; this run is the one corpus a session here can reach.
- The sample is small (18 absent, 19 answerable). The Wilson bound is the
  comparison for exactly that reason, and at 0/18 no widening of the sample
  rescues a 0.30 bar.

## How it was run

```
docker exec agentsmemory-agentsmemory-1 sh -c '
  agentsmemory eval --db /data/agentsmemory.db --style absent --n 25 --pool 50 --rerank-timeout 60s \
    --cases /data/adr001-absent-2026-09-04.jsonl --gen-url http://host.docker.internal:11434
  agentsmemory eval --db /data/agentsmemory.db --n 25 --pool 50 --rerank-timeout 60s \
    --cases /data/adr001-answerable-2026-09-04.jsonl --gen-url http://host.docker.internal:11434
  agentsmemory eval --db /data/agentsmemory.db --pool 50 --rerank-timeout 60s --arms production --calibrate --gate \
    --cases /data/adr001-absent-2026-09-04.jsonl,/data/adr001-answerable-2026-09-04.jsonl
'
```

Generator `qwen2.5-coder:7b` through Ollama on the host. `--arms production`
because a gate run needs the production arm only; without it the eval scores
every one of 27+ arms per case at ~10 s per reranked arm. Per-case output:
`/data/adr001-absent-2026-09-04.results.json` on the palace volume.
