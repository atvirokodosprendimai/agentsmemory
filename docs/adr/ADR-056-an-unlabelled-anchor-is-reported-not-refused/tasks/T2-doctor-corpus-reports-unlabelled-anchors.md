# Task ADR-056-T2: `doctor --corpus` reports unlabelled anchors as a population, not a verdict

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `corpusFindings.UnlabelledAnchors`
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the walk selects anchors with an empty repo`, `the report prints the population and the exit code ignores it`

## Goal

An operator running `doctor --corpus` learns how many anchors in the palace can never be attributed to a tree, with a sample of their ids and the call that labels them, and the exit code means exactly what it meant before.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/doctorcorpus.go` | edit | `corpusFindings` gains `UnlabelledAnchors []string`; `walkCorpus` selects anchors whose `repo` is empty; `reportCorpus` prints the population at every run, including zero, the way it prints `EndedFactSources` — one line saying why it matters (no tree can verify them) and the remedy (`am_update_drawer(code_anchors:)` with `repo`), then a `shortSample` of ids when there are any; `clean()` is NOT touched |
| `cmd/server/doctorcorpus_test.go` | edit | `TestDoctorCorpusReportsUnlabelledAnchors`: the walk half over a migrated SQLite palace seeded with one labelled and one unlabelled anchor, the way `TestDoctorCorpusFindsRealDriftInARealDatabase` already drives it, and the rendering half over a `corpusFindings` value |

The selecting line is the print in `reportCorpus`: a population the walk counts and the report never prints is invisible, and a population the report prints and `clean()` consults is a verdict on a legal state — the review of `2344964` is why the second is wrong here (ADR-056 §Decision, "the population does not own the exit code"). The mutant is on the walk's predicate: with `repo = ''` removed the walk selects every anchor or none, and the exact-id assertion goes red.

## Ordered Steps

1. [S1] Write `TestDoctorCorpusReportsUnlabelledAnchors` and run it red: seed a palace with one memory carrying two anchors (one with `repo`, one with `repo: ""`), run `walkCorpus`, assert `UnlabelledAnchors` holds exactly the unlabelled one's id and `clean()` is still true; then `reportCorpus` over the findings, assert the id, the word `repo` and `am_update_drawer` appear in the output and the error is nil; then a findings value with no unlabelled anchor still prints the population line with a zero count. Today the field does not exist, so this is red at compile time.
2. [S2] Add the field, the query and the report block; leave `clean()` as it is. `[proof: mutation]`
3. [S3] Run the fence green, including the existing corpus tests, so the three original populations and the exit code report as they did. `[proof: acceptance]`

## Acceptance

```bash
set -o pipefail
go test ./cmd/server/ -run 'TestDoctorCorpusReportsUnlabelledAnchors$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./cmd/server/ -run 'TestDoctorCorpus' -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestDoctorCorpusReportsUnlabelledAnchors` | `cmd/server/doctorcorpus_test.go` | the walk selects exactly the anchors with an empty `repo`, the report names them and the remedy at every run including zero, and the verdict is unchanged by them | — | S1, S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the field and the query |
| 2 — something selects it | `reportCorpus` prints it; the mutant is deleting the walk's `repo = ''` predicate, which turns the exact-id assertion red |
| 3 — the caller can discover it | `doctor --corpus` is already advertised in help (`TestDoctorCorpusIsAdvertisedInHelp`); the population line is printed even at zero, so an operator learns the check exists from a clean run |
| 4 — it is used | a run against the local palace after the change; the seven measured on 2026-09-04 were labelled by hand that day, so the expected reading is zero and a non-zero one is a new finding |

## Mutation Log

- 2026-09-05 · 1a2287d* · mutant killed · exit 1 · `cmd/server/doctorcorpus.go` · the population must be selected on repo='' and reported in its own field; routing it into LostAnchors turns a permitted state into a verdict and empties the report line · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171
- 2026-09-05 · 1a2287d* · mutant killed · exit 1 · `cmd/server/doctorcorpus.go` · the population is printed at every run and never decides the exit code; turning it into a verdict is the design ADR-056 refused · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171 · covers:the report prints the population and the exit code ignores it
- 2026-09-05 · 1a2287d* · mutant survived · exit 0 · `cmd/server/doctorcorpus.go` · the walk must select the empty repo, not the labelled ones; inverting the predicate reports the labelled anchor and misses the unlabelled one · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171 · covers:the walk selects anchors with an empty repo
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 07db758* · mutant killed · exit 1 · `cmd/server/doctorcorpus.go` · the walk must select the empty repo, not the labelled ones; inverting the predicate reports the labelled drawer and misses the unlabelled one · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171 · covers:the walk selects anchors with an empty repo

## Invariants

- `clean()` and the exit code are unchanged: an unlabelled anchor is a legal write and never a corpus failure.
- The three existing populations and `EndedFactSources` report exactly as before; the new block is additive.
- A read-only run: `doctor --corpus` repairs nothing (`TestTheReadOnlyPathMintsNothing` stands).

## Risks

- A palace with many unlabelled anchors floods the report — mitigated by reusing `shortSample`, which already bounds the other lists.
- An operator reads a zero exit as "nothing to do" and never sees the line — accepted by the ADR as the cost of not going red on a legal state; the line is printed at every run so it is at least always there.

## Stop Condition

Stop if the anchors table has no `repo` column on the walk's read model (it is `repo` on `drawer_anchors`, read through `anchorRow` in `internal/palace/anchors.go`, today); then the query needs a schema read before the task, not a guess.

## Out of Scope

- Labelling the anchors it finds — the remedy is named in the report and belongs to the session that owns the memory.
- Making the population fail the check, bounded or not — rejected in the ADR's Alternatives.
- The write-side report — T1's job.

## Verification Log
- 2026-09-05 · 1a2287d* · exit 1 · `set -o pipefail …` · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171 · ms:373
  ```
  --- last 5 line(s) of stdout
  # github.com/atvirokodosprendimai/agentsmemory/cmd/server [github.com/atvirokodosprendimai/agentsmemory/cmd/server.test]
  cmd/server/doctorcorpus_test.go:255:15: found.UnlabelledAnchors undefined (type corpusFindings has no field or method UnlabelledAnchors)
  cmd/server/doctorcorpus_test.go:256:100: found.UnlabelledAnchors undefined (type corpusFindings has no field or method UnlabelledAnchors)
  FAIL	github.com/atvirokodosprendimai/agentsmemory/cmd/server [build failed]
  FAIL
  ```
- 2026-09-05 · 1a2287d* · exit 0 · `set -o pipefail …` · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171 · ms:11917
- 2026-09-05 · 1a2287d* · exit 0 · `set -o pipefail …` · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171 · ms:5355
- 2026-09-05 · 1a2287d* · exit 0 · `set -o pipefail …` · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171 · ms:11587
- 2026-09-05 · 07db758* · exit 0 · `set -o pipefail …` · acceptance-sha256:2e39a2da41aba71b42d213b6de69fad95474ba780174bac7214121a055b6c171 · ms:5033
