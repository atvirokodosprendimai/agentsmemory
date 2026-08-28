# Task ADR-004-T5: Turn the measurement into a pre-registered verdict

**Depends-on:** T2, T3, T4
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `palace.SupersessionVerdict`, `agentsmemory eval --supersession-gate`
**Consumes:** `palace.SupersessionMetrics`, `verified-pair meta`, `palace.ArmRecency`, `EvalCaseResult.DistractorPoolRank`

## Goal

Make ADR-004's acceptance criterion executable: one command that reads a report and answers `justified`, `not justified` or `unresolved` against a bar, an arm and an interval rule all fixed before the number was known.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/evalstats.go` | edit | the verdict function, the Wilson interval it reads, and the pre-registered constants — bar, floor, gated arm, non-inferiority margin — whose doc comments cite ADR-004 so a later reader can see each was set in advance |
| `cmd/server/eval.go` | edit | `--supersession-gate`: refuse an unhardened, undersized or degraded run, otherwise print the verdict, the arm it came from, both rate treatments, the recency veto's inputs and the verdict's distance from flipping |
| `internal/palace/evalstats_test.go` | edit | pin the three outcomes against hand-built counts, and pin that a band which is not non-inferior on the non-temporal cases cannot veto |
| `cmd/server/eval_test.go` | edit | pin all three refusals, each naming its own cause, and that a page-scoped arm with the lowest rate in the table moves no verdict |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestSupersessionGateThreeOutcomes` and `TestSupersessionGateVetoNeedsNonInferiority` in `internal/palace/evalstats_test.go`; `TestSupersessionGateRefusesUnhardenedCases` and `TestSupersessionGateIgnoresPageScopedArms` in `cmd/server/eval_test.go`. Commit them red.
2. Add the pre-registered constants to `internal/palace/evalstats.go`, each with a doc comment stating what it costs to be wrong in either direction and that changing it is an ADR amendment rather than tuning: `supersessionBar = 0.20`; `supersessionMinCases = 30`; `supersessionGatedArm`, the pool-scoped reconstruction of production ranking (today `ArmReranked`), whose comment says it must change in the same commit that changes production ranking and must never be chosen by score; and `supersessionNonInferiority = 0.05` MRR, whose comment records that the margin is set by what n=40 can resolve rather than by the loss we would accept.
3. Add `SupersessionVerdict` computing the three outcomes from one arm's stale-above rate and its **Wilson** interval: lower bound above the bar is `justified`, upper bound below it is `not justified`, anything straddling is `unresolved`. It takes the interval rule as data, not as a hard-coded call, so `TestSupersessionGateThreeOutcomes` can drive all three from hand-built counts.
4. Compute the rate twice — once counting `CurrentUnreachable` cases as failures, once over the cases where the gated arm retrieved the correction at all — and return `unresolved` naming both when the two treatments disagree. A verdict that depends on which defensible treatment you pick is not a verdict.
5. Select the arm the gate judges by IDENTITY: `supersessionGatedArm`, found in the report by name. Refuse if it is absent — a degraded run drops the reranked arms, and substituting the nearest available arm is the selection this task exists to remove. Never scan the table for a minimum: the first draft gated the lowest stale-above rate anywhere in the report, which is the winner's curse `printEvalTable` already warns about in the MRR table.
6. Apply the recency veto, selection-aware and cost-conditional. A band of `recencySweep` may downgrade a `justified` to `not justified — a date preference already closes it` only when both hold: its Wilson interval, computed at α/k over the k pre-registered bands, has an upper bound below the bar; and `palace.PairedDelta(band, gatedArm)` over the NON-temporal cases has `.Lo > -supersessionNonInferiority`. Write the arguments in that order — `PairedDelta(a, b)` is MRR(a) − MRR(b), and an inverted pair would let an arm veto by being worse. A band that clears the rate but whose non-inferiority interval straddles the margin prints as *unresolved on cost* and does not veto.
7. Add `--supersession-gate` to the eval command: refuse below `supersessionMinCases` pairs that are BOTH from a hardened file and non-vacuous in this run — the floor is that intersection computed at read time, never the generation-time `verified_pairs` integer, which knows nothing about the pool this run used — naming the count and pointing at the corpus rather than the bar; refuse a case set where any file contributing temporal cases carries no verification record, naming what actually fixes it. **Corrected during execution:** the task said to name `--verify-pairs`, but T2 wired pair verification unconditionally into temporal generation, so no such flag exists — and naming a flag that is not there is precisely the defect this branch exists to close. The refusal names `--style temporal` regeneration instead; refuse a report without the gated arm, and name the right cause: reranked arms missing too means a degraded run (`--allow-degraded`), reranked arms present means the constant is stale — ADR-003 may retire the closet prior that `ArmReranked` is named for, and pointing an operator at a flag for that would be a misdiagnosis. Read only pool-scoped arms: `ArmProduction` and `ArmContextual` are printed for the reader and are never the gated arm and never a veto.
8. Print the verdict with the arm it came from, both rates and their intervals, the case count, the run's `--pool` (vacuity is defined against it, so the same case file yields a different floor at 20 and at 50), the vacuous and current-unreachable counts, the veto's inputs when it fired, and **how many case-flips the verdict sits from the nearest boundary** — at n=30 that is often one, and a one-label margin should read as one. One line per outcome says what it authorises, including that `not justified` authorises leaving `kg-extract` unrun. The headline rate is the **gated arm's**, named on the same line: any other arm's rate printed first would be quoted as the finding.
9. Run the acceptance command; the four tests green.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestSupersessionGate|TestGatedArm|TestServiceReportsItsOwnGatedArm" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSupersessionGateThreeOutcomes` | `internal/palace/evalstats_test.go` | the Wilson-versus-bar rule yields all three verdicts, a straddling interval never resolves, and treatments that disagree return `unresolved` | — |
| `TestSupersessionGateVetoNeedsNonInferiority` | `internal/palace/evalstats_test.go` | a recency band whose rate clears the bar but whose non-temporal MRR delta is inferior (or unresolved) does not veto | — |
| `TestSupersessionGateRefusesUnhardenedCases` | `cmd/server/eval_test.go` | unverified pairs, too few non-vacuous cases, and a missing gated arm are each refused naming their own cause | — |
| `TestSupersessionGateIgnoresPageScopedArms` | `cmd/server/eval_test.go` | a page-scoped arm with the lowest stale-above rate in the report changes no verdict | — |

## Invariants

- The gate prints and exits: it writes no configuration, no case file and no graph row.
- A `not justified` verdict prints with the same prominence as `justified` — the command has no preferred answer.
- Vacuous pairs never enter the rate or the case count the floor is checked against.
- The gated arm is resolved by name only. No code path in the gate ranks arms by their stale-above rate to choose one.
- Only pool-scoped arms enter any verdict, any veto or any refusal count.
- A pair the judge never answered on cannot count toward the floor, and neither can a vacuous one: the floor counts the intersection at read time — hardened in the file AND non-vacuous in this run — never `verified_pairs` alone.

## Risks

- A single-number bar invites arguing after the fact. Mitigation: the constant, its doc comment and this ADR all carry the same number, so moving it shows up as a diff in review rather than as a judgement call in a meeting.
- At the floor a verdict can flip on one label — 10/30 is `unresolved`, 11/30 is `justified`. Mitigation: the command prints that distance beside the verdict, so nobody reads a one-case margin as a settled answer; the fix is more verified pairs, never a different rule.
- The non-inferiority margin is set by the instrument, so a band that costs up to 0.05 MRR reads as free. Mitigation: the measured delta prints beside the verdict, and the ADR carries a follow-up to re-derive the margin when the case set can resolve less.

- ⚠ **THE FENCE SELECTED ONLY HALF THIS TASK'S MECHANISM UNTIL 2026-08-28.** It ran
  `TestSupersessionGate*`, which drives `SupersessionVerdict` and never reaches `Service.gatedArm` —
  so returning a named arm where none reconstructs the served ranking, the exact defect
  `SupersessionGatedArmFor`'s doc comment says "is how the gate judged a pipeline nobody runs",
  passed this task's gate. Verified: mutating `case closetOn: return ""` to `return ArmHybridCloset`
  SURVIVED the old fence and is killed by `TestGatedArmReconstructsTheServedRanking`, which already
  existed in `internal/palace/gatedarm_test.go` and was simply not selected. The fence now names it.

## Stop Condition

Stop if fewer than `supersessionMinCases` verified, non-vacuous pairs exist once T2 has run — per the ADR's Decision the response is more dated corrections in the corpus, and lowering the floor to reach a verdict needs an explicit human decision to overrule this ADR. Stop too if the gated arm is missing from the report — whether the run was degraded or the constant has gone stale behind an ADR-003 change — because picking a replacement arm at read time would reintroduce exactly the selection this task removes.

## Out of Scope

- Choosing the interval rule at runtime, or offering a flag for it — the rule is pre-registered like the bar.
- Populating the graph on a `justified` verdict (deferred: docs/adr/BACKLOG.md)
- Wiring the graph into `Service.Search` (deferred: docs/adr/BACKLOG.md)
- Re-running the gate automatically as the corpus grows (deferred: docs/adr/BACKLOG.md)

## Verification Log
- 2026-08-20 · c57bd43 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestSupersessionGate" -count=1'`
- 2026-08-20 · 8602da5 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestSupersessionGate" -count=1'`
- 2026-08-20 · 829d1d5 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestSupersessionGate" -count=1'`
- 2026-08-20 · 9b72924 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestSupersessionGate" -count=1'`
- 2026-08-28 · 023c208 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestSupersessionGate" -count=1'` · acceptance-sha256:1eb71064269a5192b2883e619203fcc25753f032f52eed87f9146683c046348b
- 2026-08-28 · 098580d · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestSupersessionGate|TestGatedArm|TestServiceReportsItsOwnGatedArm" -count=1'` · acceptance-sha256:22e0cfc1f6908bda8c6f02977a6243837959d60ab0122c9fa5959e53ee0edf85

## Mutation Log
- 2026-08-28 · f1b7b0e · mutant survived · exit 0 · `internal/palace/evalstats.go` · naming the nearest arm when none reconstructs the served ranking is exactly how the gate came to judge a pipeline nobody runs · acceptance-sha256:1eb71064269a5192b2883e619203fcc25753f032f52eed87f9146683c046348b
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · f1b7b0e* · mutant killed · exit 1 · `internal/palace/evalstats.go` · two defensible treatments of an unretrieved correction that DISAGREE must resolve to unresolved, not to whichever one was computed first — a gate that picks a side silently reports a verdict the evidence does not support · acceptance-sha256:1eb71064269a5192b2883e619203fcc25753f032f52eed87f9146683c046348b
