# Task ADR-001-T1: Generate hard negatives, verify absence at retrieval depth, and label the three calibration populations

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `evalPromptAbsent` (identifier-preserving), `absentVerifyDepth` verification that DROPS unverified cases, `palace.AbsentVerification` provenance on `EvalCase`, `EvalCaseResult.Population` (`reachable`/`unreachable`/`absent`) and `EvalCaseResult.TopRerank`/`RerankScored`
**Consumes:** none

## Goal

Make the calibration set honest: negatives that keep the identifiers a real near-miss carries, absence checked as deep as the palace can retrieve, no case kept whose absence was not positively confirmed, and every case labelled with the population and the score the curve will be built from.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/eval.go` | edit | `evalPromptAbsent` instructs "do not reuse the note's distinctive identifiers", which manufactures easy negatives; `verifyAbsent` (line ~389) searches with `Limit: 3` while the ADR claimed a corpus-wide check; and its caller (line ~538) prints `kept UNVERIFIED` and **keeps** the case when the verifier errors |
| `internal/palace/eval.go` | edit | add `Population`, `TopRerank` and `RerankScored` to `EvalCaseResult`, and `AbsentVerification` provenance to `EvalCase`; populate the population from the category and the existing `poolRank` |
| `internal/palace/eval_test.go` | edit | pin the three-way labelling, including that a gold outside the pool is `unreachable` and not counted as answerable |
| `cmd/server/eval_test.go` | add | pin that the absent prompt keeps identifiers, that a verifier error drops the case, and that the saved case file carries per-case verification provenance |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestPopulationLabelsSeparateUnreachable` in `internal/palace/eval_test.go` asserting a case whose gold is absent from the pool is labelled `unreachable` rather than `reachable`; `TestAbsentPromptKeepsIdentifiers` and `TestVerifyAbsentDropsOnVerifierError` in `cmd/server/eval_test.go` asserting the absent prompt does not instruct the generator to drop identifiers and that a verifier error removes the case instead of keeping it. Commit them red.
2. Add `Population string` to `EvalCaseResult` with constants `PopReachable`, `PopUnreachable`, `PopAbsent` in `internal/palace/eval.go`, populated from `cat` and the existing `poolRank` where `PoolRanks` is appended; carry the production arm's `prodRerank` / `prodScored` onto the same struct as `TopRerank` / `RerankScored`, so the curve consumes labelled `(score, population)` rows rather than the two flat `GoldRerank` / `AbsentRerank` arrays, which lose the label.
3. Replace `evalPromptAbsent` with a version that asks for a neighbouring-topic question which **keeps** the note's identifiers, file names and flags, and that would be plausible against a different project's notes (cross-wing near-miss). Keep the old prompt as `--style absent-easy` so existing case files stay reproducible and the two regimes can be compared.
4. Widen `verifyAbsent` from `Limit: 3` to `absentVerifyDepth = 20`, with the reason at the constant: the eval's retrieval ceiling measured 2026-08-18 on the then-current ~5,020-drawer palace put 98% of answerable golds inside the top 20 by vector distance (top-1 75%, top-5 92%, top-20 98%, 1 of 40 never retrieved), so depth 20 checked everything that palace could retrieve at all. **Re-measure the ceiling before writing the constant**: that corpus was reset on 2026-08-19 and the figures above describe a palace that no longer exists — the comment must cite the ceiling of the corpus the constant is actually chosen for, with its date. It is not a corpus-wide proof and the constant's comment must say so — a memory the dense channel never surfaces is one recall could not have returned either.
5. Make a verifier failure remove the case: the first failure aborts the run with the generator hint (matching the preflight doctrine already in this file — a checker that cannot score one note is misconfigured, not unlucky), and any later failure drops that single case with a counted, printed reason. No path may append a case labelled absent whose check did not return a positive "nothing answers this".
6. Persist per-case provenance: `EvalCase.AbsentVerification` carrying the checker model, the depth searched and the timestamp, written into the JSONL alongside the case, plus the same depth in `caseFileMeta`. A case file merged from several runs must let T2 tell a verified case from an unverified one row by row.
7. Run the acceptance command; all three new tests green and the existing eval tests unchanged.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null
  if [ -n "$(gofmt -l internal/palace cmd/server)" ]; then echo "gofmt"; exit 1; fi
  go vet ./... || exit 1
  go test ./internal/palace/ ./cmd/server/ -run "TestPopulationLabelsSeparateUnreachable|TestAbsentPromptKeepsIdentifiers|TestAbsentCaseOutcomeDropsOnVerifierError|TestAbstentionCalibrationComesFromTheDefaultPage" -count=1 -v 2>&1 | tee /tmp/a1t1.out
  grep -q -- "--- PASS: TestPopulationLabelsSeparateUnreachable" /tmp/a1t1.out || exit 1
  grep -q -- "--- PASS: TestAbsentPromptKeepsIdentifiers" /tmp/a1t1.out || exit 1
  grep -q -- "--- PASS: TestAbsentCaseOutcomeDropsOnVerifierError" /tmp/a1t1.out || exit 1
  grep -q -- "--- PASS: TestAbstentionCalibrationComesFromTheDefaultPage" /tmp/a1t1.out || exit 1
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a1t1.out; then echo "vacuous or failing"; exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestPopulationLabelsSeparateUnreachable` | `internal/palace/eval_test.go` | a gold outside the retrieved pool is `unreachable`, not `reachable` | — |
| `TestAbsentPromptKeepsIdentifiers` | `cmd/server/eval_test.go` | the absent prompt does not instruct identifier removal | — |
| `TestAbsentCaseOutcomeDropsOnVerifierError` | `cmd/server/eval_test.go` | a verifier error drops the case; nothing labelled absent survives unverified. Named for the extracted decision `absentCaseOutcome` rather than for `verifyAbsent`, because the bug was in the CALLER's handling of the error, not in the check — a test of `verifyAbsent` would have passed throughout | — |
| `TestAbstentionCalibrationComesFromTheDefaultPage` | `internal/palace/proddepth_test.go` | pre-existing gate, strengthened here: it read only the FIRST `TopRerank:` in the file and this task adds a second, so it was inspecting the new line and no longer watching the one it protects. Now checks every occurrence, and pins the origin separately so "forwarded" is not a hole | — |

## Invariants

- Existing case files stay replayable: `--style absent-easy` reproduces the previous generator exactly.
- Every `CatAbsent` case written to a file carries verification provenance; a case without it is a case T2 must refuse.
- No arm's MRR changes — this task adds labels and changes generation, never ranking.

## Risks

- Hard negatives may prove *too* hard, collapsing the measured separation to nothing. That is a finding, not a failure: it would mean the gate cannot ship, and it is far better learnt here than after four tasks of wiring. Mitigation: report both regimes side by side; T3 is where the finding is acted on.
- Depth 20 costs 20 checker calls per candidate instead of 3, and drop-on-failure discards cases that were previously kept, so the verified-absent count will fall below the 21 the old top-3 check produced. Both are the price of a label that means what it says; the new count is reported and is what T2's sample size is stated from.

## Stop Condition

Stop and ask if the identifier-preserving generator yields negatives that another memory actually answers at a rate above ~30% at depth 20 — that would mean the corpus cannot supply hard negatives and the calibration plan needs rethinking rather than patching. Stop too if fewer than 15 verified-absent cases survive a `--n 25` run: T2's interval is already the weakest part of this ADR, and a smaller sample makes the gate criterion unmeasurable rather than merely noisy.

## Out of Scope

- The calibration report, the gate criterion and the refusal of unverified cases — that is T2's job.
- Growing the absent corpus beyond what `--n` produces, and mining hard negatives from real queries instead of generating them (deferred: docs/adr/BACKLOG.md)

## Stop Condition — measured 2026-08-21, and the threshold does not discriminate

Run on the live 449-memory corpus, `--n 25`, depth 20, same checker model for both.

| generator | rejected (another memory answers it) | verified-absent cases |
|---|---|---|
| `--style absent` (identifiers KEPT) | **8 of 25 (32%)** | 17 |
| `--style absent-easy` (identifiers stripped) | 6 of 25 (24%) | 19 |

**Against the Stop Condition as written**: 32% is marginally above the "~30%"
line, and 17 survivors clears the "fewer than 15" floor. At n=25 the difference
between 8 rejections and 7 is one case, so the trip is inside the resolution of
the instrument.

**But the control is the finding, and it falsified the prediction that motivated
the change.** The expectation was that the easy generator — which strips the
note's identifiers — would be rejected far less often, because its questions share
no vocabulary with the corpus. It was rejected 6 times against 8. Two cases apart
at n=25 is noise. **The identifier-preserving prompt did not measurably change how
often another memory answers the question.**

**Why the threshold was the wrong instrument.** The rejection rate measures whether
the CORPUS happens to answer a question. It says nothing about whether the negative
is harder to SEPARATE from an answerable one, which is the only property the
calibration curve cares about. A question can keep every identifier, be genuinely
unanswered, and still be trivially separable — and this measurement could not tell.

**The separation measurement, taken against the same 25 answerable questions.**

| negatives | answerable median | unanswerable median | gap |
|---|---|---|---|
| `absent-easy` (identifiers stripped) | 0.364 | 0.427 | **0.063** |
| `absent` (identifiers KEPT) | 0.364 | 0.394 | **0.030** |

The hard negatives sit HALF as far from answerable questions. That is the property
the calibration curve depends on, and it is the one the rejection rate could not
see. **The change is justified; the instrument that seemed to reject it was the
wrong instrument.**

**And the same run corrected the claim that motivated a sibling change.** Scored
through the production reranker over 25 answerable and 17 unanswerable cases:

| signal | kind | AUC |
|---|---|---|
| **`top_rerank`** | **absolute** | **0.81** |
| `top_gap` | contrastive | 0.71 |
| `score_spread` | contrastive | 0.70 |
| `dist_gap` | contrastive | 0.69 |
| `dist_spread` | contrastive | 0.61 |

The ABSOLUTE cross-encoder score beats every contrastive shape. A separate
measurement on wrong-WING detection had found the opposite — a contrastive margin
at 0.985 against the reranker's 0.841 — and that was generalised here as "every
strong signal is a difference, every weak one is a position". **That
generalisation was wrong for this question, and T2's plan to calibrate on the
production arm's top-1 stands as written.**

The distinction is what the contrast is taken AGAINST. Across wings there is a
meaningful alternative — is something better in another scope — so the margin
dominates. Within a single page there is not: five uniformly wrong documents still
produce a gap, because one of them is slightly less wrong, and the shape says
nothing about whether any of them answer. The cross-encoder wins because it answers
the question directly, which `printRerankSeparation`'s own comment said before any
of this was measured: *"Cosine distance answers 'how similar', which is not the
question… A cross-encoder score answers 'does this document answer this query',
which IS the question."*

The contrastive statistics stay, and stay beside the absolute one rather than
replacing it — that was the point of adding them. What changed is which one the
evidence now points at.

**Not a reason to withdraw the change.** Keeping identifiers is right on the
published evidence regardless of this corpus's rejection rate: abstention accuracy
collapses from 98.0% to 1.1% when the irrelevant passage is merely on-topic, and
the distractors models handle worst are the semantically related ones. The
correction here is to the CLAIM, not to the code — and `--style absent-easy` exists
precisely so the two regimes stay comparable rather than one silently replacing the
other.

## Mutation Log

- 2026-08-22 · 1c9506a* · mutant killed · exit 1 · `internal/palace/eval.go` · a gold that never entered the pool would be labelled reachable, making a retrieval failure look like a ranking result the arms all got wrong
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `cmd/server/eval.go` · a verified-absent case is only honest if its absence was actually checked; treating a verifier ERROR as a keep writes a row indistinguishable from a verified one, and every abstention number downstream then treats an unchecked assumption as a measurement · acceptance-sha256:6b4eb28eade01392f00e81c2222cf8405f51ab2c5f0cf1f9e851a65fe81a24e1

## Verification Log

> **2026-09-06 — the four `exit 1` entries below are NOT this task's work failing.** T1's own
> four tests pass (`TestPopulationLabelsSeparateUnreachable`, `TestAbsentPromptKeepsIdentifiers`,
> `TestAbsentCaseOutcomeDropsOnVerifierError`, `TestAbstentionCalibrationComesFromTheDefaultPage`),
> and every identifier under **Produces** already exists in the tree — this task's implementation
> landed and only its evidence was missing. What fails is the acceptance's trailing
> `go test ./...`, on `TestADeadlineKillsTheChildAndItsChildren` in `internal/testexec`, which is
> unrelated to T1 and is filed as **issue #338**.
>
> It fails only under sustained load: 4/4 under `adr-verify` (which runs `gofmt`, `go vet ./...`
> and the `-run` subset in the same container first), and 0/13 across every hand-run
> configuration — the subset alone ×8, `go test ./...` in this tree and in a fresh clone, the
> acceptance's shape by hand, and on the host. #338 records why the obvious fix is not obviously
> right: the test's 300ms deadline may simply be load-fragile, or `reapGroup` may have a real
> `setpgid` race, and lengthening the deadline would hide the second. Adding instrumentation to
> tell them apart perturbed the timing and stopped the failure reproducing.
>
> **This task stays not-done until a clean run says otherwise.** The entries stand as the honest
> record of four attempts rather than being deleted.

- 2026-08-21 · 9a88b51* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/chromemvec	0.022s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.006s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.566s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.012s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.224s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.004s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.004s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.008s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.003s
  FAIL
  ```
- 2026-08-21 · 9a88b51* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-22 · 1c9506a* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-09-06 · 3b80dab · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:6b4eb28eade01392f00e81c2222cf8405f51ab2c5f0cf1f9e851a65fe81a24e1 · ms:52691
  ```
  --- last 10 line(s) of stdout (of 1577 after folding 1583 raw)
  --- FAIL: TestADeadlineKillsTheChildAndItsChildren (3.32s)
      testexec_test.go:50: grandchild 5055 is still alive after the deadline killed its parent; the process group was not reaped
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/testexec	3.319s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.061s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.008s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.482s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.013s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.006s
  FAIL
  ```
- 2026-09-06 · 62b5079* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:6b4eb28eade01392f00e81c2222cf8405f51ab2c5f0cf1f9e851a65fe81a24e1 · ms:44294
  ```
  --- last 10 line(s) of stdout (of 99 after folding 99 raw)
  --- FAIL: TestADeadlineKillsTheChildAndItsChildren (3.32s)
      testexec_test.go:50: grandchild 5422 is still alive after the deadline killed its parent; the process group was not reaped
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/testexec	3.317s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.056s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.005s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.315s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.008s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.007s
  FAIL
  ```
- 2026-09-06 · 62b5079* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:6b4eb28eade01392f00e81c2222cf8405f51ab2c5f0cf1f9e851a65fe81a24e1 · ms:29590
  ```
  --- last 10 line(s) of stdout (of 99 after folding 99 raw)
  --- FAIL: TestADeadlineKillsTheChildAndItsChildren (3.32s)
      testexec_test.go:50: grandchild 5345 is still alive after the deadline killed its parent; the process group was not reaped
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/testexec	3.317s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.056s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.005s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.194s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.008s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.005s
  FAIL
  ```
- 2026-09-06 · 62b5079* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:6b4eb28eade01392f00e81c2222cf8405f51ab2c5f0cf1f9e851a65fe81a24e1 · ms:32006
  ```
  --- last 10 line(s) of stdout (of 99 after folding 99 raw)
  --- FAIL: TestADeadlineKillsTheChildAndItsChildren (3.32s)
      testexec_test.go:50: grandchild 4146 is still alive after the deadline killed its parent; the process group was not reaped
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/testexec	3.320s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.058s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.007s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.352s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.004s
  FAIL
  ```
