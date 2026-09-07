# Task ADR-008-T3: Create, read, update, delete — proven by reading back, per area

**Depends-on:** T2

> **Amended 2026-08-20 during execution.** Scenarios live in `internal/mcptest` (import cycle —
> same amendment as T2 and T4). The three regression scenarios are named as registry entries rather
> than `Scenario*` Go functions, so the acceptance fence greps the gate's own test names instead.
> One scenario also had to be rewritten against the real contract: the chunk-0 defect was fixed by
> REFUSING a multi-chunk content edit, not by rewriting every chunk, so the regression asserts the
> refusal and that nothing half-landed.
> **Amended 2026-09-07. Step 2 was taken for the first time and the adoption bar was missed AGAIN,
> one scenario over from where the note below already records it.** The supersede regression could
> not see its own defect: the mutant `c.ValidTo == "" && c.ID == id` in `supersedeInto` — end only
> the chunk the caller named — compiled and left all three gate tests GREEN. Two independent causes.
> (1) The sweep runs over SEARCH HITS matched on content, and `SUPERSEDED-MARKER` sat in chunk 0 with
> a filler tail, so the tail chunk was never returned and never inspected — the identical fixture
> error the delete scenario already hit and fixed, which is why the Mutants note below says "moving
> the marker into the LAST chunk fixed it". Two adjacent scenarios, one hazard, only one of them
> corrected. (2) It asserted only `valid_to`, and TWO mechanisms set that: the compare-and-swap, and
> `persistRows` re-filing under the predecessor's SOURCE. Both fixed — a marker at both ends, and
> `superseded_by` asserted per chunk — and the mutant now dies naming `chunk_index: 1`.
>
> ⚠ **THE FENCE CHANGED, SO THE 2026-08-20 DIGEST NO LONGER MATCHES.** It gained
> `git config --global --add safe.directory /src`: the container runs as root over a host-owned bind
> mount, so on Linux git refused with `detected dubious ownership` and the fence died with TEN
> `exit status 128` failures over a tree that was green on the host and in CI (the same defect PR
> #383 fixed in `scripts/redeploy.sh`; 31 other fences still carry it). The provably inert
> `! grep -qE …` guard was replaced by the un-negated form in the same edit, since the digest was
> being re-recorded anyway — `set -e` exempts a negated pipeline, so it never could have failed.
>
> ⚠ **T3 STAYS `partial`: IT CANNOT BE VERIFIED FROM A LINUX CHECKOUT.** With `safe.directory` in
> place the 128s are gone (0 of them), and the fence is now blocked by a single unrelated test —
> `TestADeadlineKillsTheChildAndItsChildren`, "the process group was not reaped". It is NOT a flake:
> it failed both recorded runs at ~3.31s, while passing 3/3 in the same container in isolation and
> passing in the full suite on the host. Two failed runs are in the Verification Log deliberately,
> because an attempt that got further than the last one is evidence and deleting it would hide that
> the first blocker is fixed.
>
> ⚠ **AND THE MUTATION LOG IS DELIBERATELY STILL EMPTY, WHICH `adr-lint` CORRECTLY FLAGS.** All three
> mutants were run and their results are recorded in the Mutants table's prose below and in the
> commit that fixed the supersede scenario — M1 kills only after that fix, M2 and M3 kill as they
> stand. What cannot be written yet is the TOOL-WRITTEN entry, because `adr-verify --mutant` records
> a kill from the acceptance fence's exit code, and this fence is currently red for a reason that has
> nothing to do with any mutant. Every mutant would be recorded `killed` on a fence that fails with
> the mechanism intact — a verdict bound to nothing, which is the exact defect this ADR exists to
> prevent. The log stays empty until the fence is green on the machine recording it.

**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** one-party scenarios for every observable tool, including three regression scenarios
**Consumes:** the scenario registry (T2)
**Data dependency:** hermetic

## Goal

Every mutable area round-trips through the tool surface, and a delete is proven gone by every route that could still reach it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcptest/registry_test.go` | edit | the scenarios themselves, per area: drawers, anchors, tunnels, skills, KG, wings, diary, closets |
| `internal/mcptest/registry_test.go` | edit | the three regression scenarios named after the defects they cover |

## Ordered Steps

1. Write the failing tests first (TDD red): the three regression scenarios, named for the defects they cover — `ScenarioUpdateRewritesEveryChunk`, `ScenarioDeleteLeavesNoChunkBehind`, `ScenarioMalformedAnchorsDoNotClear`. Commit them red against a tree with the fixes reverted, so they are proven to fail on the real defect and not merely to pass on the fixed code.
2. **This is the calibration set and the parent ADR's adoption bar.** Re-introduce each of the three defects in turn and confirm the matching scenario fails. If any does not, the gate is not adopted — report rather than weaken it.
3. Write the per-area round trips. Each: create, read back and compare, update, read back and confirm the change is complete, delete, then confirm gone by EVERY route — direct get, list, and search — because the delete defect was invisible to a get and visible only to search.
4. For every area, assert the negative too: reading something never written returns absence, not an empty success.
5. Falsify each scenario by reverting the mechanism it covers; record every mutation in the table below with whether it compiled.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  git config --global --add safe.directory /src
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcptest/ -run "TestEveryToolIsExercisedEndToEnd|TestScenarios" -count=1 -v 2>&1 | tee /tmp/e3.out
  grep -q -- "--- PASS: TestEveryToolIsExercisedEndToEnd" /tmp/e3.out
  grep -q -- "--- PASS: TestScenariosObserveAnEffect" /tmp/e3.out
  grep -q -- "--- PASS: TestScenariosOnlyClaimToolsTheyCall" /tmp/e3.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/e3.out; then exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| regression: no stale half after update | `internal/mcptest/registry_test.go` | a multi-chunk update leaves no stale chunk competing in search | — |
| regression: delete leaves no chunk | `internal/mcptest/registry_test.go` | delete removes every chunk, checked by get AND search | — |
| regression: malformed anchors do not clear | `internal/mcptest/registry_test.go` | an all-unreadable anchor list refuses instead of clearing | — |
| per-area round trips | `internal/mcptest/registry_test.go` | create/read/update/delete observable for each area | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| multi-chunk update guard disarmed (`len(chunks) > 1 && false`) | yes | regression: no stale half after update |
| delete truncated to the parent row (`ids = ids[:1]`) | yes | regression: delete leaves no chunk |
| all-unreadable anchor refusal disarmed | yes | regression: malformed anchors do not clear |

**The adoption bar was not met on the first attempt, and the check is why.** The delete mutation
SURVIVED: the scenario put its marker at the START of the content, so it landed in chunk 0 — which
the buggy delete removes anyway — while the orphaned child held only filler. The scenario could not
see the defect it existed for. Moving the marker into the LAST chunk fixed it, and the mutation now
dies. This is the "fixture too small to expose the defect" risk in this task's own Risks section,
and it would have shipped as coverage.

**A review also found the harness unfaithful in a way that mattered.** `Parties` built a SERVER PER
PARTY and shared only the database. Production runs one process serving everybody, so per-process
state that leaks between clients — a cached search key omitting the wing, a latched "current wing"
field — was invisible: each party latched its own correct wing and isolation looked perfect. It is
now one server, N clients.

## Out of Scope

- Multi-party visibility (permanent: T4 owns it, and mixing the two makes a failure ambiguous between a lifecycle bug and a scoping one)
- Tools on the unobservable list (permanent: T2 defines it and requires a reason)

## Invariants

- Every delete is confirmed by more than one read route.
- No scenario asserts only that a call returned without error.

## Risks

- A scenario passes because the fixture is too small to expose chunking. Mitigated: the multi-chunk scenarios seed content over the chunking threshold and assert the chunk count first.

## Stop Condition

Stop and report if any of the three regression scenarios cannot be made to fail on the reverted defect — that means the harness does not observe what it claims to, and the parent ADR says the gate is then not adopted.

## Verification Log

- 2026-08-20 · 62d7c38* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-09-07 · 9c02132* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:22312dd76b0fe871c4aa6cbe37a4ff4a778e4711395b9b007178d9082beb994c · ms:308963
  ```
  --- last 10 line(s) of stdout (of 2958 after folding 2958 raw)
  --- FAIL: TestADeadlineKillsTheChildAndItsChildren (3.31s)
      testexec_test.go:50: grandchild 1696 is still alive after the deadline killed its parent; the process group was not reaped
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/testexec	3.319s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.064s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.029s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.502s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.030s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.014s
  FAIL
  ```
- 2026-09-07 · 9c02132* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:22312dd76b0fe871c4aa6cbe37a4ff4a778e4711395b9b007178d9082beb994c · ms:326695
  ```
  --- last 10 line(s) of stdout (of 2958 after folding 2958 raw)
  --- FAIL: TestADeadlineKillsTheChildAndItsChildren (3.31s)
      testexec_test.go:50: grandchild 1816 is still alive after the deadline killed its parent; the process group was not reaped
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/testexec	3.321s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.069s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.031s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.585s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.039s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.021s
  FAIL
  ```

## Mutation Log
