# Task ADR-047-T4: `agentsmemory longmemeval` — the grid, the fixed budget, the results file

**Depends-on:** T2, T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
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
2. `RunGrid(ctx, svc, ds Subset, writes, queries []string, opts)` — for each cell: create the
   scratch wing, ingest through `Service.Add`, search under the query policy, assemble up to
   `opts.ContextTokens`, read, judge, tally.
3. **Refuse to run when the wing is not empty**, and refuse when `ContextTokens` is zero. A run
   into a populated wing measures somebody else's memories.
4. Assemble the reader context by taking returned memories in rank order until the budget is
   spent, and record how much of the budget each cell actually used — a policy that cannot fill
   the window is a finding, not a footnote.
5. Score with `palace.WilsonInterval` for the cell and `palace.PairedDelta` against the baseline
   cell on the same question ids. Compute the retrieval-only secondary column from
   `answer_session_ids` at the same time; it is nearly free and the ADR wants the disagreement.
6. Write `<out>.cells.json` with a header carrying: dataset path and SHA-256, subset ids and seed,
   reader/judge model id and endpoint kind, context budget, ranking profile string, and the commit.
   `LoadCells` refuses to merge two files whose headers differ in any of those.
7. Add the subcommand with `--data`, `--wing`, `--write`, `--query`, `--n`, `--seed`,
   `--context-tokens`, `--out`. Build `--write` / `--query` usage text from the registries so
   `--help` cannot list a policy that does not exist or omit one that does.
8. Register it in `main.go`.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null
  if [ -n "$(gofmt -l internal/longmemeval cmd/server)" ]; then echo "gofmt"; exit 1; fi
  go vet ./... || exit 1
  go test ./internal/longmemeval/ ./cmd/server/ -run "TestRunGridHoldsTheContextBudgetAcrossCells|TestRunGridRefusesANonEmptyWing|TestCellsRefuseToMergeAcrossDifferentHeaders|TestCellsCarryTheRankingProfileAndModel|TestLongmemevalIsRegistered|TestLongmemevalHelpListsEveryRegisteredPolicy" -count=1 -v 2>&1 | tee /tmp/a47t4.out
  grep -q -- "--- PASS: TestRunGridHoldsTheContextBudgetAcrossCells" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestRunGridRefusesANonEmptyWing" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestCellsRefuseToMergeAcrossDifferentHeaders" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestCellsCarryTheRankingProfileAndModel" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestLongmemevalIsRegistered" /tmp/a47t4.out || exit 1
  grep -q -- "--- PASS: TestLongmemevalHelpListsEveryRegisteredPolicy" /tmp/a47t4.out || exit 1
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a47t4.out; then echo "vacuous or failing"; exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRunGridHoldsTheContextBudgetAcrossCells` | `internal/longmemeval/grid_test.go` | every cell's assembled context is bounded by the same budget — the ADR's central invariant | — |
| `TestRunGridRefusesANonEmptyWing` | `internal/longmemeval/grid_test.go` | a run cannot measure pre-existing memories | — |
| `TestRunGridRefusesAZeroBudget` | `internal/longmemeval/grid_test.go` | an unbounded run is not silently allowed | — |
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
