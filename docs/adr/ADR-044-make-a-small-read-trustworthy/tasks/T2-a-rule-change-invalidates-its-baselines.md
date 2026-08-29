# Task ADR-044-T2: Make a rule change invalidate every baseline taken under it

**Depends-on:** T1
**Covers:** F-6, UC3-S2
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** tag removal from `internal/repohygiene/readrule_spec_test.go`
**Consumes:** the counting-rule artifact and its content identity (T1)
**Data dependency:** hermetic — the whole behaviour is driven by fixture rules and fixture baselines; no corpus is required.

## Goal

Make a baseline whose cited rule digest no longer matches the rule on disk fail the gate, naming the rule change rather than reporting a comparison.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/repohygiene/readrule.go` | edit | Add the invalidation verdict: a cited digest that does not match the current rule is INVALID, distinct from a baseline with no citation at all (T1's case). Two states, reported differently — collapsing them loses which failure happened |
| `internal/repohygiene/readrule_spec_test.go` | edit | Turn `TestF6ARuleChangeInvalidatesItsBaselines` green **and remove `//go:build readcostspec`** — T2 is the last task in this file, so both bindings enter the default lane together |
| `docs/measurement/read-counting-rule.md` | edit | Add the sentence that a change to this file invalidates every baseline citing the old digest, so the artifact carries its own consequence |

## Ordered Steps

1. Confirm `TestF6ARuleChangeInvalidatesItsBaselines` is red for the right reason under the tag. Verified red 2026-08-29: *"not built yet — F-6 (UC3-S2)"*.
2. Extend `readrule.go` with the three-way verdict: `resolves`, `no citation`, `cited a rule that is no longer current`. A gate that reports one where the other happened is the class of defect this repository keeps finding.
3. Drive the verdict through the real resolution function, not a copy. A falsifiability half that re-implements the check passes while the real one is severed — recorded in `AGENTS.md` as the failure `TestASpecBindingThatNamesNothingIsCaught` shipped with.
4. Turn F-6 green: alter one byte of a fixture rule while a fixture baseline still cites the old digest, and assert the rate is refused and the rule change is named.
5. **Remove the build tag from this file only.** Confirm `go test ./internal/repohygiene/ -count=1` — no tag — now runs both bindings green.

## Acceptance

```bash
set -o pipefail
go test ./internal/repohygiene/ -run 'TestF6ARuleChangeInvalidatesItsBaselines' -count=1 2>&1 | tee /tmp/adr044-t2.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t2.out && go test ./internal/repohygiene/ -count=1 && go vet ./... && go test ./... -count=1
```

Note the absence of `-tags readcostspec`: this fence is red today precisely because the binding is
still behind the tag, and it goes green only when step 5 removes it. That is the intended shape —
the fence proves the tag came off as well as that the behaviour works.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF6ARuleChangeInvalidatesItsBaselines` | `internal/repohygiene/readrule_spec_test.go` | A baseline citing a superseded rule digest is invalid; the gate names the rule change instead of reporting a rate | F-6, UC3-S2 |
| `TestF5ABaselineNamesItsCountingRule` | same | Still green once the tag is removed — T1's binding must survive entering the default lane | F-5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF6ARuleChangeInvalidatesItsBaselines` |
| 2 — something selects it | Removing the build tag is what makes both bindings reachable from `go test ./...`, which CI runs on every push (`.github/workflows/build.yml:65`). Deleting the tag removal is the mutation: the tests stop running and nothing else notices |
| 3 — the caller can discover it | The rule file states its own invalidation consequence — `n/a: no declared interface` beyond that |
| 4 — it is used | Every subsequent rate quote is checked against a live digest; CI is the observation |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->

## Invariants

- A baseline citing a stale digest is INVALID, never silently compared.
- "No citation" and "stale citation" stay distinguishable in the report.
- After this task, no binding in `internal/repohygiene/readrule_spec_test.go` is behind a build tag.

## Risks

- Removing the tag exposes T1's binding to CI. If T1 is green only under a fixture that CI cannot reproduce, CI goes red on unrelated pushes. Mitigated by step 5 running the untagged package before the fence is claimed.

## Stop Condition

Stop if the rule's digest cannot be computed stably — trailing whitespace, line endings or an editor rewriting the file would make every baseline spuriously invalid, which is a gate that fires constantly and therefore gets ignored. Normalize before digesting, or bring the normalization rule to the owner.

## Out of Scope

- Removing the tag from `internal/mcpserver/readcost_spec_test.go` or `internal/palace/readcost_spec_test.go` — T6 and T7 own those
- Deciding what the rule counts, which T1 fixed and F-6 exists to protect

## Verification Log

<!-- Tool-written by `adr-verify`. -->
