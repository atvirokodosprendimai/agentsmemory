# Task ADR-029-T2: What was asked, what was searched, and what was dropped

**Depends-on:** none
**Estimated scope:** M (three sites, two packages)
**Covers:** none — no spec
**Owner:** unassigned
**Produces:** `am.limit_requested`, `am.query_runes`, `am.query_truncated`, `am.max_distance` on the parent span; `am.scope_source` at the MCP boundary; `scopeDrops` from `survivorsFrom` recorded at `rankRetrieved`
**Consumes:** none
**Data dependency:** hermetic

## Goal

A trace can answer three questions it cannot answer today: what the caller actually asked for, which scope was actually searched and who chose it, and how many candidates the filters removed before ranking.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/trace.go` | edit | `searchAttrs` is the single parent-knob site; the requested limit, query length and `max_distance` land here rather than at a second site |
| `internal/palace/service.go` | edit | pass the pre-clamp limit and pre-truncation rune count into `searchAttrs`; record the drop counts at `rankRetrieved`'s single call site |
| `internal/palace/memory_search.go` | edit | `survivorsFrom` returns its three drop counts instead of discarding them |
| `internal/mcpserver/server.go` | edit | `searchWingFor` reports HOW the wing was chosen — the only place that information still exists |
| `internal/mcpserver/drawers.go` | edit | annotate the tool span with the wing source; **three** call sites here (467, 626, 832), not one |
| `internal/mcpserver/admin.go` | edit | **found by review**: two more `searchWingFor` callers (40, 127) |
| `internal/mcpserver/graph.go` | edit | **found by review**: two more (178, 250) |
| the wing-resolution test file (`internal/mcpserver/`) | edit | **found by review**: four direct test callers — changing the signature without these does not compile |
| `internal/palace/scopedrops_test.go` | add | the drop counts, and the request-vs-served deltas |
| `internal/mcpserver/wingsource_test.go` | add | the four wing-source cases, each distinguishable from the others |

## Ordered Steps

1. **TDD red.** `TestRequestedLimitSurvivesTheClamp` asserts a search asking for 5000 records both `am.limit=100` and `am.limit_requested=5000`. `TestTruncatedQueryLeavesEvidence` asserts a 400-rune query records `am.query_runes=400` and `am.query_truncated=true`, and that a short query records `false`. `TestScopeDropsAreCounted` asserts the three counts and that a stale-index fixture produces a non-zero out-of-scope count. `TestWingSourceDistinguishesCallerFromServer` asserts four distinct values. Confirm all red.
2. Widen `survivorsFrom` to return its drop counts. Keep it a pure function taking no context — see the ADR's Alternatives for why instrumenting inside it produces wrong numbers.
3. Record the counts at `rankRetrieved` via `telemetry.Annotate`, at the single call site (`service.go:1089`) and nowhere else. The widening-loop call site at `memory_search.go:135` keeps discarding them; it runs once per round and its numbers would multiply.
4. Assert the eval attribution explicitly. `rankRetrieved` has four callers, and **only two of them own an arm span** — `evalCaseResult` (`eval.go:1175`) starts `StageEvalArm`, but `CandidateUnion` (`eval.go:1358`) starts none, so `Annotate` there paints whatever outer span happens to be current, or nothing. **Found by review; the ADR's original claim of three arm-span callers was wrong.** Return the counts from `rankRetrieved` and let each caller annotate the span it owns, rather than annotating from inside and the `am.search` parent for a served call. That is the correct per-caller attribution, and it is pinned by a test, because arm numbers reading as served-path numbers in a table nobody re-derives is exactly the failure this repository already retracted a statistic for.
5. Add `am.max_distance` to `searchAttrs`. It is the one retrieval boundary the knob set omits, and `retrieveStop` can already end the widening loop with `reason=max_distance` — so the trace names the stop and not the threshold.
6. Pass the pre-clamp limit and the pre-truncation rune count into `searchAttrs`, keeping `am.limit` as the served value. Both numbers, not a flag: `am.limit_requested` answers what was asked, `am.limit` answers what ran, and the delta is the finding.
7. Have `searchWingFor` report how it resolved the wing. **It has ELEVEN call sites** — seven production across three files and four in the wing-resolution test file — so prefer a second, additive resolver over widening the existing signature, or update every caller in the same commit — caller-supplied, server-default-substituted, explicit-star-widened, or workspace-scope-widened — and annotate the tool span with it. `searchAttrs` runs with `q.Wing` already resolved, so no attribute added inside `internal/palace` can recover this; the capture must happen at the boundary.
8. Run the acceptance fence and confirm it is green only after steps 2–7.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/palace/ ./internal/mcpserver/
  go test ./internal/palace/ -run "^(TestRequestedLimitSurvivesTheClamp|TestTruncatedQueryLeavesEvidence|TestScopeDropsAreCounted|TestScopeDropsLandOnTheArmSpanForEvalArms)$" -count=1 -v 2>&1 | tee /tmp/t2.out
  go test ./internal/mcpserver/ -run "^(TestWingSourceDistinguishesCallerFromServer)$" -count=1 -v 2>&1 | tee -a /tmp/t2.out
  grep -qE "^--- PASS: TestRequestedLimitSurvivesTheClamp \(" /tmp/t2.out
  grep -qE "^--- PASS: TestTruncatedQueryLeavesEvidence \(" /tmp/t2.out
  grep -qE "^--- PASS: TestScopeDropsAreCounted \(" /tmp/t2.out
  grep -qE "^--- PASS: TestScopeDropsLandOnTheArmSpanForEvalArms \(" /tmp/t2.out
  grep -qE "^--- PASS: TestWingSourceDistinguishesCallerFromServer \(" /tmp/t2.out
  if grep -qE "no tests to run|^FAIL" /tmp/t2.out; then exit 1; fi
  go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1
'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRequestedLimitSurvivesTheClamp` | `internal/palace/scopedrops_test.go` | asked-for and served limits are both on the span and differ when clamped | — |
| `TestTruncatedQueryLeavesEvidence` | `internal/palace/scopedrops_test.go` | a truncated query is distinguishable from a short one; both cases asserted | — |
| `TestScopeDropsAreCounted` | `internal/palace/scopedrops_test.go` | orphan, out-of-scope and over-distance counts reach the span, with a fixture that forces each to be non-zero | — |
| `TestScopeDropsLandOnTheArmSpanForEvalArms` | `internal/palace/scopedrops_test.go` | an eval arm's drop counts attach to its own arm span, never to the served parent | — |
| `TestWingSourceDistinguishesCallerFromServer` | `internal/mcpserver/wingsource_test.go` | four resolution paths produce four distinct values, so `has_wing=true` stops meaning two things | — |

`TestScopeDropsAreCounted` guards its own fixture: it fails if all three counts are zero, because a fixture where nothing is dropped cannot tell a correct implementation from one that always reports zero.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | each attribute read back off a recorded span |
| 2 — something selects it | `searchAttrs` for the knobs, the `Annotate` call for the drops, the boundary annotation for the wing source — mutation: delete each and the fence goes red |
| 3 — the caller can discover it | the attributes appear in a dumped tree, which is the trace reader's surface; no schema advertises them and none should |
| 4 — it is used | the wing-source attribute makes the ambiguity that made `am.has_wing` unusable measurable, so the recall statistics sliced on it can be re-derived and compared |

## Mutation Log

_(populated by `adr-verify --mutant` during execution)_

## Invariants

- No ranking changes. `survivorsFrom` returns the identical survivor slice and distinct count; only its extra return values are new.
- `survivorsFrom` stays pure and context-free. Instrumenting inside it multiplies the counts by the widening-round count.
- The widening call site keeps discarding the drops. One recording site, one set of numbers.
- ADR-025's privacy rule holds: `am.query_runes` is a length, never the text; `am.scope_source` is a bounded enum, never a wing name.

## Risks

- **A non-zero out-of-scope drop is an alarm, not a metric.** `service.go:1081-1083` documents the wing/room comparison as redundant when the index honoured the filter, kept solely so a stale index cannot surface another wing's memory. A non-zero count there means the vector index and the durable rows have diverged. This task only makes it visible; acting on it is out of scope and receipted.
- Widening an internal signature touches the eval path. Both call sites are internal and unexported, and step 4 pins the attribution rather than trusting it.

## Stop Condition

Stop and ask if the wing source cannot be captured without changing `searchWingFor`'s signature in a way that reaches a public surface. It is internal today; if it is not, that is a different task.

## Out of Scope

- **Alerting or failing on a non-zero out-of-scope drop.** Making the divergence visible is this task; deciding what the server does about it is a separate decision with its own blast radius.
- The six tail findings and backend identity (deferred: docs/adr/BACKLOG.md §"From ADR-029")

## Verification Log
