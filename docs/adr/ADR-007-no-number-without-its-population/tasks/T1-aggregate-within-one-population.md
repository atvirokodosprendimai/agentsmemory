# Task ADR-007-T1: No statistic may combine arms measuring different populations

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** scope-partitioned aggregation, general rather than at one call site
**Consumes:** none
**Data dependency:** hermetic
**Rests-on:** `the baseline is per-population`, `the population comes from the classification, never a list of arm names`, `the ceiling's closing claim is conditional on the table`

## Goal

Every aggregate the eval prints over multiple arms includes only arms sharing an `ArmScope`, and a new aggregate cannot be written without choosing one.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/eval.go` | edit | the remaining aggregates get the same partition `printPoolDiagnosis` already has |
| `cmd/server/pooldiag_test.go` | edit | generalise from the one fixed call site to the rule |

## Ordered Steps

1. Write the failing test first (TDD red): `TestNoAggregateMixesScopes` — enumerate every place the eval reduces over `report.Arms` and assert each filters on `palace.ArmScope`. Commit it red if any does not.
2. Audit the reductions. `printPoolDiagnosis` is done; check the ceiling block, the category breakdown and the `vs best` baseline for the same mistake.
3. Where an aggregate legitimately spans scopes, it must say so in its own output rather than being exempted in a list — the exclusion-list-by-name is what let production inherit the contextual arm's bug.
4. Falsify: remove the partition from one aggregate; add a new arm with a non-pool scope and confirm it is excluded without anyone editing a list.
5. Run the acceptance command.

## Acceptance

```bash
docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true;
  set -e
  git config --global --add safe.directory /src
  gofmt -l cmd | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestNoAggregateMixesScopes|TestPoolDiagnosis" -count=1 -v 2>&1 | tee /tmp/a1.out
  grep -q -- "--- PASS: TestNoAggregateMixesScopes" /tmp/a1.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a1.out; then exit 1; fi
  go test ./cmd/server/ ./internal/palace/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestNoAggregateMixesScopes` | `cmd/server/pooldiag_test.go` | every multi-arm reduction filters on ArmScope | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestNoAggregateMixesScopes` drives the reduction directly |
| 2 — something selects it | the aggregate reads `palace.ArmScope`; removing the filter turns the test red |
| 3 — the caller can discover it | n/a: no declared interface — this is an internal reduction, not a caller-facing surface |
| 4 — it is used | the pool-diagnosis line is printed on every eval run, so a wrong partition is visible in the next table anybody reads |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| `bestByScope` returns one argmax over every arm | yes | `TestNoAggregateMixesScopes` |
| the partition becomes an exclusion list keyed by arm name | yes | `TestNoAggregateMixesScopes` |
| the ceiling block prints its closing sentence unconditionally | yes | `TestNoAggregateMixesScopes` |

## Out of Scope

- Changing what any aggregate measures (permanent: this task governs which arms feed it, not the statistic)
- The `vs best` baseline's selection bias (deferred: docs/adr/ADR-002-anchor-the-lexical-score.md — its Follow-ups already own the selection-aware bootstrap)

## Invariants

- A new arm with a new scope is excluded from pooled aggregates without anyone editing a list.

## Risks

- An aggregate that genuinely spans scopes gets wrongly partitioned. Mitigated: step 3 requires it to declare itself rather than be silently exempt.

## Stop Condition

Stop and ask if an aggregate cannot be assigned a single scope — that means the statistic itself is ambiguous and needs redefining, not filtering.

## Verification Log

- 2026-09-07 · d8147ca7 · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:8ed3417962ad8bf78b39f1eb7485bda2cc501cb1312aa02ffc3a4b0962c9b3c6 · ms:31391
- 2026-09-07 · d8147ca7* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:8ed3417962ad8bf78b39f1eb7485bda2cc501cb1312aa02ffc3a4b0962c9b3c6 · ms:28644
- 2026-09-07 · d8147ca7* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:8ed3417962ad8bf78b39f1eb7485bda2cc501cb1312aa02ffc3a4b0962c9b3c6 · ms:33590
- 2026-09-07 · d8147ca7* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:8ed3417962ad8bf78b39f1eb7485bda2cc501cb1312aa02ffc3a4b0962c9b3c6 · ms:28994
- 2026-09-07 · d8147ca7* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:2f124f69aabd989eff3208a2a59af56cee9b7a6ff767e260a5a32ad37b463ea9 · ms:29020
- 2026-09-07 · d8147ca7* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:2f124f69aabd989eff3208a2a59af56cee9b7a6ff767e260a5a32ad37b463ea9 · ms:27893
- 2026-09-07 · d8147ca7* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:2f124f69aabd989eff3208a2a59af56cee9b7a6ff767e260a5a32ad37b463ea9 · ms:28256
- 2026-09-07 · d8147ca7* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:2f124f69aabd989eff3208a2a59af56cee9b7a6ff767e260a5a32ad37b463ea9 · ms:35072

## Mutation Log
- 2026-09-07 · d8147ca7* · mutant killed · exit 1 · `cmd/server/eval.go` · bestByScope drops the classification and returns one argmax over every arm — what printEvalTable did before this task. Production and the contextual arm are then ranked against the pooled winner and reported "worse by", for a difference that is a change of question, not a ranking loss. · acceptance-sha256:8ed3417962ad8bf78b39f1eb7485bda2cc501cb1312aa02ffc3a4b0962c9b3c6 · covers:the baseline is per-population
- 2026-09-07 · d8147ca7* · mutant killed · exit 1 · `cmd/server/eval.go` · the partition goes back to an exclusion list keyed by arm name — correct for every arm that exists today, and it folds the NEXT one into the pool silently. That is the route by which production inherited the bug already fixed for the contextual arm; the fixture arm ArmScope does not classify is what catches it. · acceptance-sha256:8ed3417962ad8bf78b39f1eb7485bda2cc501cb1312aa02ffc3a4b0962c9b3c6 · covers:the population comes from the classification, never a list of arm names
- 2026-09-07 · d8147ca7* · mutant killed · exit 1 · `cmd/server/eval.go` · the ceiling block prints its closing sentence unconditionally again, telling the reader that every arm above re-orders the shared pool while production is scored over a page cut from it and the contextual arm never touched it. Same defect as the statistic, stated in prose, and no numeric assertion can see it. · acceptance-sha256:8ed3417962ad8bf78b39f1eb7485bda2cc501cb1312aa02ffc3a4b0962c9b3c6 · covers:the ceiling's closing claim is conditional on the table
- 2026-09-07 · d8147ca7* · mutant killed · exit 1 · `cmd/server/eval.go` · bestByScope drops the classification and returns one argmax over every arm — what printEvalTable did before this task. Production and the contextual arm are then ranked against the pooled winner and reported "worse by", for a difference that is a change of question, not a ranking loss. · acceptance-sha256:2f124f69aabd989eff3208a2a59af56cee9b7a6ff767e260a5a32ad37b463ea9 · covers:the baseline is per-population
- 2026-09-07 · d8147ca7* · mutant killed · exit 1 · `cmd/server/eval.go` · the partition goes back to an exclusion list keyed by arm name — correct for every arm that exists today, and it folds the NEXT one into the pool silently. That is the route by which production inherited the bug already fixed for the contextual arm; the fixture arm ArmScope does not classify is what catches it. · acceptance-sha256:2f124f69aabd989eff3208a2a59af56cee9b7a6ff767e260a5a32ad37b463ea9 · covers:the population comes from the classification, never a list of arm names
- 2026-09-07 · d8147ca7* · mutant killed · exit 1 · `cmd/server/eval.go` · the ceiling block prints its closing sentence unconditionally again, telling the reader that every arm above re-orders the shared pool while production is scored over a page cut from it and the contextual arm never touched it. Same defect as the statistic, stated in prose, and no numeric assertion can see it. · acceptance-sha256:2f124f69aabd989eff3208a2a59af56cee9b7a6ff767e260a5a32ad37b463ea9 · covers:the ceiling's closing claim is conditional on the table
