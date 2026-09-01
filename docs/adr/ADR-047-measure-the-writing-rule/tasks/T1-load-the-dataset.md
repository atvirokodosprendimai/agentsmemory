# Task ADR-047-T1: Load LongMemEval-S into typed records, with the subset written into the run

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** kme
**Produces:** `longmemeval.Dataset`, `longmemeval.Question`, `longmemeval.Session`, `longmemeval.Turn`, `longmemeval.Subset`
**Consumes:** none
**Data dependency:** needs the LongMemEval-S JSON file for a real run; every Ordered Step and the whole Acceptance fence are hermetic and run against committed fixtures.

## Goal

Parse a LongMemEval-S file into typed Go records, and make the question subset a run selects an
explicit, recorded, reproducible property rather than whatever order the file happened to be in.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/longmemeval/dataset.go` | add | the schema and the loader |
| `internal/longmemeval/subset.go` | add | deterministic, stratified, recorded question selection |
| `internal/longmemeval/dataset_test.go` | add | the loader's tests, including the shapes a real file has and a fixture cannot be trusted to have by accident |
| `internal/longmemeval/subset_test.go` | add | the selection's tests |
| `internal/longmemeval/testdata/longmemeval_s_sample.json` | add | a small hand-written fixture carrying every one of the six question types and at least one multi-session gold |
| `internal/longmemeval/doc.go` | add | package comment: what this package measures and why it is not `internal/palace/eval.go` |

Nothing selects this task's output yet — `Dataset` is consumed by T2 and T4, and that is the
Reachability rung 2 those tasks own. This task deliberately adds no flag, because a loader that
ships with a flag before there is anything to run is a knob that does nothing (ADR-006).

## Ordered Steps

1. Write the failing tests first (TDD red): `TestDatasetLoadsEverySixQuestionTypes`,
   `TestDatasetRejectsAQuestionWhoseGoldSessionIsNotInItsHaystack`,
   `TestSubsetIsDeterministicForASeed`, `TestSubsetStratifiesByQuestionType`. Commit them red.
2. Add the fixture. Hand-write it rather than trimming a downloaded file, so the licence question
   does not arrive with it and so every field the loader reads is present on purpose.
3. Define `Turn{Role, Content, HasAnswer}`, `Session{ID, Date, Turns}`,
   `Question{ID, Type, Question, Answer, Date, Haystack []Session, GoldSessionIDs []string}`,
   `Dataset{Questions []Question, Path, SHA256}`.
4. Implement `Load(path string) (Dataset, error)`. It zips `haystack_session_ids`,
   `haystack_dates` and `haystack_sessions` positionally — the format guarantees the order — and
   **fails loudly when the three lengths disagree**, because a silent zip produces sessions dated
   from their neighbours and every downstream temporal question is then wrong in a way no test
   would show.
5. Validate on load that every `answer_session_ids` entry names a session present in that
   question's haystack. A gold nothing can retrieve scores zero for every policy equally, which
   reads as "no policy helps" rather than as a broken row.
6. Record the file's SHA-256 on the `Dataset`, so the results file names the exact corpus.
7. Implement `Subset(d Dataset, n int, seed int64) Subset`: stratified by `question_type` so a
   small `n` cannot silently become five multi-session questions, deterministic for a seed, and
   carrying the chosen ids so a later run can reproduce it exactly.

## Acceptance

```bash
set -o pipefail
  if [ -n "$(gofmt -l internal/longmemeval)" ]; then echo "gofmt"; exit 1; fi
  go vet ./... || exit 1
  go test ./internal/longmemeval/ -run "TestDatasetLoadsEverySixQuestionTypes|TestDatasetPairsEverySessionWithItsOwnDate|TestDatasetRejectsAQuestionWhoseGoldSessionIsNotInItsHaystack|TestDatasetRejectsMisalignedHaystackArrays|TestDatasetRecordsItsFileDigest|TestSubsetIsDeterministicForASeed|TestSubsetStratifiesByQuestionType|TestSubsetOfMoreThanTheCorpusIsTheCorpus" -count=1 -v 2>&1 | tee /tmp/a47t1.out
  grep -q -- "--- PASS: TestDatasetLoadsEverySixQuestionTypes" /tmp/a47t1.out || exit 1
  grep -q -- "--- PASS: TestDatasetPairsEverySessionWithItsOwnDate" /tmp/a47t1.out || exit 1
  grep -q -- "--- PASS: TestDatasetRecordsItsFileDigest" /tmp/a47t1.out || exit 1
  grep -q -- "--- PASS: TestSubsetOfMoreThanTheCorpusIsTheCorpus" /tmp/a47t1.out || exit 1
  grep -q -- "--- PASS: TestDatasetRejectsAQuestionWhoseGoldSessionIsNotInItsHaystack" /tmp/a47t1.out || exit 1
  grep -q -- "--- PASS: TestDatasetRejectsMisalignedHaystackArrays" /tmp/a47t1.out || exit 1
  grep -q -- "--- PASS: TestSubsetIsDeterministicForASeed" /tmp/a47t1.out || exit 1
  grep -q -- "--- PASS: TestSubsetStratifiesByQuestionType" /tmp/a47t1.out || exit 1
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a47t1.out; then echo "vacuous or failing"; exit 1; fi
go test ./... -count=1
```

The named tests run first and each `--- PASS` is asserted individually, so the fence cannot be
satisfied by the repo-wide run that follows it. The `no tests to run` grep is what stops a renamed
test from exiting 0 on an empty filter.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestDatasetLoadsEverySixQuestionTypes` | `internal/longmemeval/dataset_test.go` | all six `question_type` values parse, and turn-level `has_answer` survives | — |
| `TestDatasetPairsEverySessionWithItsOwnDate` | `internal/longmemeval/dataset_test.go` | the positive half of the zip: a session keeps its own id and date, not its neighbour's | — |
| `TestDatasetRejectsAQuestionWhoseGoldSessionIsNotInItsHaystack` | `internal/longmemeval/dataset_test.go` | an unretrievable gold is an error, not a zero | — |
| `TestDatasetRejectsMisalignedHaystackArrays` | `internal/longmemeval/dataset_test.go` | ids/dates/sessions of unequal length fail loudly rather than zipping short | — |
| `TestDatasetRecordsItsFileDigest` | `internal/longmemeval/dataset_test.go` | the corpus identity travels with the data | — |
| `TestSubsetIsDeterministicForASeed` | `internal/longmemeval/subset_test.go` | two calls with one seed choose the same ids | — |
| `TestSubsetStratifiesByQuestionType` | `internal/longmemeval/subset_test.go` | a small `n` still spans the types present | — |
| `TestSubsetOfMoreThanTheCorpusIsTheCorpus` | `internal/longmemeval/subset_test.go` | asking for more questions than exist returns them all rather than looping | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the six tests above |
| 2 — something selects it | nothing yet, by design: `Load` and `Subset` are selected by T4's command, and T4's `TestLongmemevalIsRegistered` is the mutation that proves it |
| 3 — the caller can discover it | n/a: no declared interface — this package is internal and has no wire or flag surface until T4 |
| 4 — it is used | nothing measures this yet |

## Mutation Log

- 2026-09-01 · e4917c5* · mutant killed · exit 1 · `internal/longmemeval/dataset.go` · severs the haystack alignment check, the one Load failure that would otherwise date a session from its neighbour and make every temporal question wrong in silence · acceptance-sha256:78ed467169052a6ecc3073493786802ed65a2677eda6d53bfd77bf4941b12999
- 2026-09-01 · e4917c5* · mutant killed · exit 1 · `internal/longmemeval/subset.go` · severs the seeded visit-order shuffle, so a subset smaller than the number of types silently admits the same alphabetically-first types at every seed · acceptance-sha256:78ed467169052a6ecc3073493786802ed65a2677eda6d53bfd77bf4941b12999

## Invariants

- `Load` never silently repairs a malformed file. Every disagreement between the three haystack
  arrays, and every gold naming a session that is not there, is an error with the question id in it.
- `Subset` is a pure function of `(dataset, n, seed)`.
- No network access and no palace access anywhere in this package's T1 surface.

## Risks

- The published field list may not match a real file byte-for-byte, and the fixture is
  hand-written from the documented schema rather than from the artefact. Mitigation: `Load`'s
  errors name the field, so the first real run reports precisely what disagreed instead of
  panicking; T4's first real invocation is where a schema surprise surfaces.

## Stop Condition

Stop and ask if the real LongMemEval-S file turns out to carry a materially different shape from
the documented one — in particular if `has_answer` is absent, since the retrieval-only secondary
column in the ADR depends on it.

Nothing in this task turns on a measurement, so there is no criterion here that could be
impossible to fail.

## Out of Scope

- Downloading or vendoring the dataset (permanent boundary of this task: the ADR's Out of Scope
  records it, and the command takes a `--data` path)
- LongMemEval-M and the V2 trajectory format — that's the deferral the parent ADR files
- Anything that writes to a palace — that's T2's job

## Verification Log
- 2026-09-01 · 0437d97* · exit 125 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:de0e1f6e6cacd9dc4ab3d3d5e37afdcee5ad5c8a4930539bf6dc1180d72a88e4
  ```
  --- last 6 line(s) of stderr
  docker: Cannot connect to the Docker daemon at unix://~/.docker/run/docker.sock. Is the docker daemon running?

  Run 'docker run --help' for more information

  [adr-verify] ENVIRONMENT: the Docker daemon was unreachable. Start Docker Desktop, or the engine, and re-run.
               This is a machine problem, not a verdict about the code. The run still counts as failed.
  ```
- 2026-09-01 · 0437d97* · exit 125 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:de0e1f6e6cacd9dc4ab3d3d5e37afdcee5ad5c8a4930539bf6dc1180d72a88e4
  ```
  --- last 6 line(s) of stderr
  docker: Cannot connect to the Docker daemon at unix://~/.docker/run/docker.sock. Is the docker daemon running?

  Run 'docker run --help' for more information

  [adr-verify] ENVIRONMENT: the Docker daemon was unreachable. Start Docker Desktop, or the engine, and re-run.
               This is a machine problem, not a verdict about the code. The run still counts as failed.
  ```
- 2026-09-01 · e4917c5* · exit 1 · `set -o pipefail …` · acceptance-sha256:78ed467169052a6ecc3073493786802ed65a2677eda6d53bfd77bf4941b12999
  ```
  --- last 10 line(s) of stdout (of 73 after folding 73 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	2.139s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	2.197s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.519s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.563s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.927s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	1.697s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	1.962s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	2.218s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	2.233s
  FAIL
  ```
- 2026-09-01 · e4917c5* · exit 1 · `set -o pipefail …` · acceptance-sha256:78ed467169052a6ecc3073493786802ed65a2677eda6d53bfd77bf4941b12999
  ```
  --- last 10 line(s) of stdout (of 68 after folding 68 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.878s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	1.096s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.551s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.013s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.971s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	1.141s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	1.232s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.952s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	1.053s
  FAIL
  ```
- 2026-09-01 · e4917c5* · exit 0 · `set -o pipefail …` · acceptance-sha256:78ed467169052a6ecc3073493786802ed65a2677eda6d53bfd77bf4941b12999
