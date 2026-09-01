# ADR-047 Tasks

Implementation tasks for ADR-047: Measure the writing rule, not only the ranking knob. See the
parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T1 |
| 4 | T4 | T2, T3 |
| 5 | T5 | T4 |

T2 and T3 are independent of each other and both depend only on T1, so they may run in either
order or together; T4 needs both. The chain after that is strict.

**Nothing edits a centralised skill before T5.** T5 is the task that decides whether this ADR's
premise survived, and it is human-observed for that reason: promoting a writing rule into
`start-here` is a claim about what every session in every project should do, and it is not a
claim a test can make. An earlier draft had T2 write its winning policy into the skill as it
went, which would have shipped four tasks' worth of advice before the ADR learned whether any of
it helps.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Load LongMemEval-S into typed records, with the subset written into the run | done | — | `go test ./internal/longmemeval/ -run "TestDataset\|TestSubset"` |
| T2 | The write-policy registry, and a flag that can select every member of it | done | — | `go test ./internal/longmemeval/ -run "TestWritePolicy\|TestEveryDeclaredPolicyIsSelectable"` |
| T3 | Extract one generative client, then the query policies and the blind judge | pending | — | `go test ./internal/gen/ ./internal/longmemeval/ ./cmd/server/ -run "TestGen\|TestQueryPolicy\|TestJudge"` |
| T4 | `agentsmemory longmemeval` — the grid, the fixed budget, the results file | pending | — | `go test ./internal/longmemeval/ ./cmd/server/ -run "TestRunGrid\|TestLongmemevalIsRegistered\|TestCells"` |
| T5 | Run the grid, apply the pre-registered rule, decide what the skills may say | blocked | — | human-observed: `adr-verify --human "…decision <ship\|withdraw\|blocked>…"` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

**T1 and T2 are `done`, and the record they sit under is `Accepted` as of 2026-09-01.** Until that
day T1 was built, verified and still `pending` on purpose: `adr-lint` refuses a `done` task under a
record nobody has accepted, because execution before acceptance is either a decision that was never
taken or a record that was never updated. Both statuses moved when the decision did, and not before.
Each carries an exit-0 run of its own fence and killed mutants, all tool-written by `adr-verify`.

⚠**T2 changed this plan while executing it, in two places, and both are recorded in the task file
rather than only here.** Its step 8 asserted that every policy "appears in the flag's usage string",
but the flag is built by T4 — so T2 now produces `WritePolicyUsage()` and gates that instead, and
T4's flag is required to call it. And its Reachability rung 2 claimed a deletion mutation that was
measured NOT to kill: severing a registration leaves a registry-derived gate green, because the
deleted policy leaves the gate's universe along with the wiring.

The Acceptance column is abbreviated for reading; the task file carries the full command including
its `gofmt`, `go vet` and per-test `--- PASS` assertions, and `adr-verify` runs that one.

The fences run `go` directly rather than inside the container other records here use — M's call,
2026-09-01. `set -o pipefail` is the first line of every one of them and is load-bearing: the test
run is piped into `tee`, and without it the pipeline reports `tee`'s status, so a suite that never
started would read as a pass.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `longmemeval.Dataset`, `Question`, `Session` | T2, T4 | T2 cannot write a policy over a haystack it cannot parse |
| T2 | `longmemeval.WritePolicy` registry | T4, T5 | the grid's rows |
| T3 | `gen.Client` | T4 | breaking move: `questionGen` leaves `eval.go` and `kgextract.go` in the same commit |
| T3 | `longmemeval.QueryPolicy` registry, `Judge` | T4, T5 | the grid's columns, and its scorer |
| T4 | `longmemeval.RunGrid()`, `<out>.cells.json` | T5 | T5 reads the file T4 writes; it does not re-derive the numbers |
