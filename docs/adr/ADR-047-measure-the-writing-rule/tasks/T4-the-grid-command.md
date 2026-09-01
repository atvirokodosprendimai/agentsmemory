# Task ADR-047-T4: `agentsmemory longmemeval` — the grid, the fixed budget, the results file

**Depends-on:** T2, T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** M
**Produces:** `longmemeval.RunGrid()`, `longmemeval.Cells`, `<out>.cells.json`, the `longmemeval` subcommand
**Consumes:** `longmemeval.Dataset` (T1), `WritePolicy` registry (T2), `QueryPolicy` registry + `Judge` + `gen.Client` (T3)
**Data dependency:** needs a palace database, an embedder and a generative endpoint for a real run. The Acceptance fence is hermetic: `RunGrid` is driven against an in-memory service and a stub judge, and the registration check reads source.

## Goal

Run every (write-policy × query-policy) cell over one subset under one fixed context budget, score
it with Wilson intervals and paired deltas against the verbatim/verbatim baseline, and write a
results file that names every configuration the numbers are valid for.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/longmemeval/grid.go` | add | `RunGrid`, the per-cell loop, the budget |
| `internal/longmemeval/cells.go` | add | the results file: cells, intervals, deltas, and the header that identifies the run |
| `internal/longmemeval/grid_test.go`, `internal/longmemeval/cells_test.go` | add | their tests |
| `cmd/server/longmemeval.go` | add | the composition root: flags, registry lookup, wiring, output |
| `cmd/server/main.go` | edit | **register the command** — the line without which everything above is unreachable |
| `cmd/server/longmemeval_test.go` | add | `TestLongmemevalIsRegistered`, which drives the real root rather than building its own |

`main.go` is in this table because a command's own tests build their own root and cannot see the
registration — the rung `TestDoctorIsRegistered` and `TestPlaybookIsRegistered` exist for, and the
one this repository has shipped broken before (`AGENTS.md` §Reachability).

## Ordered Steps

1. Write the failing tests first (TDD red), `TestLongmemevalIsRegistered` among them.
2. `RunGrid(ctx, svc, ds Subset, writes, queries []string, opts)` — for each cell, and within a
   cell for each question: create the scratch store, ingest through `Service.Add`, search under
   the query policy, assemble up to `opts.ContextRunes`, read, judge, tally.
3. **The isolation boundary is per (cell, question), not per cell**, and this is the step to get
   right. Each question in LongMemEval-S carries its own ~48-session haystack; upstream evaluates
   each instance against its own haystack alone. A wing created once per cell and written to for
   every question leaves question 2 searching question 1's history, and the contamination grows
   monotonically through the cell — so the last questions of a cell are measured under a
   different instrument than the first, and the cell mean is not a mean of anything. Use a fresh
   wing per `(cell, question)`, or a query scope that provably cannot see another question's
   records. **Refuse to run when the scope is not empty**, and refuse when `ContextRunes` is
   zero. Prove it with a two-question test whose haystacks carry conflicting answers, asserting
   the second question's results cannot retrieve the first's history. (Found in review of PR
   #148.)
4. Assemble the reader context by taking returned memories in rank order until the budget is
   spent, and record how much of the budget each cell actually used — a policy that cannot fill
   the window is a finding, not a footnote. The budget is counted in **runes**, per ADR-047's
   property 1; alongside it, record the reader endpoint's own reported prompt-token count for
   each cell wherever the endpoint supplies one, so the realised token spread across a row is
   measured rather than assumed.
5. Score with `palace.WilsonInterval` for the cell and `palace.PairedDelta` against the baseline
   cell on the same question ids. Compute the retrieval-only secondary column from
   `answer_session_ids`, mapping each returned drawer back through the `Record.SessionID` T2
   carries; it is nearly free and the ADR wants the disagreement. **Exclude `_abs` questions from
   that column** — they have no answer location, which is why the upstream retrieval evaluator
   excludes them too, and scoring them would put a fixed zero into every cell alike and damp
   every contrast. They stay in the judged column, where unanswerability is the thing being
   scored.
6. Write `<out>.cells.json` with a header carrying: dataset path and SHA-256, subset ids and seed,
   reader/judge model id and endpoint kind, context budget in runes and the realised token
   tolerance, ranking profile string, and the commit. `LoadCells` refuses to merge two files
   whose headers differ in any of those.
7. Add the subcommand with `--data`, `--wing`, `--write`, `--query`, `--n`, `--seed`,
   `--context-runes`, `--out`. Build `--write` / `--query` usage text from the registries so
   `--help` cannot list a policy that does not exist or omit one that does.
8. Register it in `main.go`.

## Acceptance

```bash
set -o pipefail
  if [ -n "$(gofmt -l internal/longmemeval cmd/server)" ]; then echo "gofmt"; exit 1; fi
  go vet ./... || exit 1
  go test ./internal/longmemeval/ ./cmd/server/ -run "TestRunGridHoldsTheContextBudgetAcrossCells|TestRunGridRefusesANonEmptyWing|TestRunGridRefusesAZeroBudget|TestRunGridIsolatesEveryQuestion|TestRetrievalColumnExcludesAbstentionQuestions|TestCellsRefuseToMergeAcrossDifferentHeaders|TestCellsCarryTheRankingProfileAndModel|TestCellsReportTheRetrievalOnlyColumnBeside|TestLongmemevalIsRegistered|TestLongmemevalHelpListsEveryRegisteredPolicy" -count=1 -v 2>&1 | tee /tmp/a47t4.out
  grep -q -- "--- PASS: TestRunGridHoldsTheContextBudgetAcrossCells" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestRunGridRefusesANonEmptyWing" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestRunGridRefusesAZeroBudget" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestCellsReportTheRetrievalOnlyColumnBeside" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestRunGridIsolatesEveryQuestion" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestRetrievalColumnExcludesAbstentionQuestions" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestCellsRefuseToMergeAcrossDifferentHeaders" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestCellsCarryTheRankingProfileAndModel" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestLongmemevalIsRegistered" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestLongmemevalHelpListsEveryRegisteredPolicy" /tmp/a47t4.out || exit 1
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a47t4.out; then echo "vacuous or failing"; exit 1; fi
go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRunGridHoldsTheContextBudgetAcrossCells` | `internal/longmemeval/grid_test.go` | every cell's assembled context is bounded by the same budget — the ADR's central invariant | — |
| `TestRunGridRefusesANonEmptyWing` | `internal/longmemeval/grid_test.go` | a run cannot measure pre-existing memories | — |
| `TestRunGridRefusesAZeroBudget` | `internal/longmemeval/grid_test.go` | an unbounded run is not silently allowed | — |
| `TestRunGridIsolatesEveryQuestion` | `internal/longmemeval/grid_test.go` | two questions with conflicting answers in one cell: the second cannot retrieve the first's history — without this the cell mean is taken over a contaminated store | — |
| `TestRetrievalColumnExcludesAbstentionQuestions` | `internal/longmemeval/cells_test.go` | `_abs` items are out of the retrieval column, as upstream excludes them; scoring them would damp every contrast equally | — |
| `TestCellsRefuseToMergeAcrossDifferentHeaders` | `internal/longmemeval/cells_test.go` | cells from different models/budgets are never pooled | — |
| `TestCellsCarryTheRankingProfileAndModel` | `internal/longmemeval/cells_test.go` | ADR-007: no number without its population | — |
| `TestCellsReportTheRetrievalOnlyColumnBeside` | `internal/longmemeval/cells_test.go` | the secondary metric is present so the two can disagree in public | — |
| `TestLongmemevalIsRegistered` | `cmd/server/longmemeval_test.go` | the command is reachable from the real root; deleting the `main.go` line turns this red | — |
| `TestLongmemevalHelpListsEveryRegisteredPolicy` | `cmd/server/longmemeval_test.go` | `--help` is documentation and it is derived, not typed | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the `RunGrid` and `Cells` tests |
| 2 — something selects it | `TestLongmemevalIsRegistered`, driving the real root; the mutation is deleting `longmemevalCommand(),` from `main.go` |
| 3 — the caller can discover it | `TestLongmemevalHelpListsEveryRegisteredPolicy`, plus `TestEveryFlagIsRead` picking up the new flags automatically |
| 4 — it is used | T5's run is the first and only use, and it is recorded there |

## Mutation Log

- 2026-09-01 · cb803fa* · mutant killed · exit 1 · `cmd/server/main.go` · the command is built, tested and registered by nothing — this repo signature defect, at the one line that makes the grid reachable · acceptance-sha256:c46e3afd1bc543f2d783e87467fadc831509848e044abb38ea9bc9b6cdbd1c6f
- 2026-09-01 · cb803fa* · mutant killed · exit 1 · `internal/longmemeval/grid.go` · the scratch scope goes back to per-cell, so question 2 searches question 1 history and contamination grows through the cell · acceptance-sha256:c46e3afd1bc543f2d783e87467fadc831509848e044abb38ea9bc9b6cdbd1c6f
- 2026-09-01 · cb803fa* · mutant killed · exit 1 · `internal/longmemeval/cells.go` · abstention questions re-enter the retrieval column, putting the same zero into every cell and damping every contrast · acceptance-sha256:c46e3afd1bc543f2d783e87467fadc831509848e044abb38ea9bc9b6cdbd1c6f
- 2026-09-01 · cb803fa* · mutant killed · exit 1 · `cmd/server/main.go` · the command is built, tested and registered by nothing — this repo signature defect, at the one line that makes the grid reachable · acceptance-sha256:55504589d30f885c303cfedbba0a06bd603eefd6bff82d37dbe5047e72f43ae4
- 2026-09-01 · cb803fa* · mutant killed · exit 1 · `internal/longmemeval/grid.go` · the scratch scope goes back to per-cell, so question 2 searches question 1 history and contamination grows through the cell · acceptance-sha256:55504589d30f885c303cfedbba0a06bd603eefd6bff82d37dbe5047e72f43ae4
- 2026-09-01 · cb803fa* · mutant killed · exit 1 · `internal/longmemeval/cells.go` · abstention questions re-enter the retrieval column, putting the same zero into every cell and damping every contrast · acceptance-sha256:55504589d30f885c303cfedbba0a06bd603eefd6bff82d37dbe5047e72f43ae4

## Invariants

- Every cell in one results file shares one context budget, one reader/judge model and one ranking
  profile. This is the invariant the whole ADR rests on; if it can be violated the table means
  nothing.
- The scratch wing is never an existing wing with memories in it.
- A judge error aborts the cell with a message; it never becomes an `incorrect`.

## Risks

- The run is long and may outrun an agent's tool timeout. Mitigation: it is an operator command,
  it prints per-cell progress, and the results file is written incrementally so a killed run leaves
  the cells it finished.
- Ingesting the same haystack once per cell is wasteful and could tempt an optimisation that shares
  a wing across cells — which would destroy the invariant above. Mitigation: named here so the
  optimisation is recognised as a correctness change when someone proposes it.

## Stop Condition

Stop if holding the context budget equal turns out to be impossible because a policy's records are
individually larger than the budget: that is a real finding about the 1600-rune rule and it changes
what T5 can conclude, so it needs a decision rather than a workaround.

The budget invariant is the criterion here and it is falsifiable by construction: a cell that
exceeds the budget fails the test.

## Out of Scope

- Crossing the grid with the ranking arms (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")
- Running all 500 questions (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")
- Promoting anything into a skill — that is T5, and only T5

## Verification Log
- 2026-09-01 · cb803fa* · exit 1 · `set -o pipefail …` · acceptance-sha256:c46e3afd1bc543f2d783e87467fadc831509848e044abb38ea9bc9b6cdbd1c6f
  ```
  --- last 6 line(s) of stderr
  # github.com/atvirokodosprendimai/agentsmemory/internal/longmemeval
  # [github.com/atvirokodosprendimai/agentsmemory/internal/longmemeval]
  vet: internal/longmemeval/grid_test.go:57:34: undefined: GridOptions
  # github.com/atvirokodosprendimai/agentsmemory/cmd/server
  # [github.com/atvirokodosprendimai/agentsmemory/cmd/server]
  vet: cmd/server/longmemeval_test.go:33:9: undefined: longmemevalCommand
  ```
- 2026-09-01 · cb803fa* · exit 1 · `set -o pipefail …` · acceptance-sha256:c46e3afd1bc543f2d783e87467fadc831509848e044abb38ea9bc9b6cdbd1c6f
  ```
  --- last 10 line(s) of stdout (of 953 after folding 955 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	0.986s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	1.126s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	1.513s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	2.232s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	1.652s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	1.756s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	1.458s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.825s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.528s
  FAIL
  ```
- 2026-09-01 · cb803fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:c46e3afd1bc543f2d783e87467fadc831509848e044abb38ea9bc9b6cdbd1c6f
- 2026-09-01 · cb803fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:c46e3afd1bc543f2d783e87467fadc831509848e044abb38ea9bc9b6cdbd1c6f
- 2026-09-01 · cb803fa* · exit 0 · `set -o pipefail …` · acceptance-sha256:55504589d30f885c303cfedbba0a06bd603eefd6bff82d37dbe5047e72f43ae4
