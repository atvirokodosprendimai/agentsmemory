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
> #383 fixed in `scripts/redeploy.sh`; ⚠ **35** other fences still carry it — this said **31**, then
> **36**, until the population was measured from the FENCE, see below). The provably inert
> `! grep -qE …` guard was replaced by the un-negated form in the same edit, since the digest was
> being re-recorded anyway — `set -e` exempts a negated pipeline, so it never could have failed.
>
> ⚠ **THE COUNT WAS WRONG THREE TIMES — 31, 32, 37 — AND THE METHOD IS THE PART WORTH KEEPING.**
> Every wrong version grepped the WHOLE TASK FILE for `go test ./...`. What makes a fence affected is
> WHAT THE ACCEPTANCE FENCE EXECUTES, and the two differ often enough to matter: ADR-003 T4 was
> counted three times because its *Risks* prose says "a full `go test ./...` before committing is
> cheap insurance", while its fence runs `go vet ./...` plus three named packages and reaches no
> git-shelling test at all. In the other direction `go test ./clients/...` occurs nine times and
> COVERS `clients/claude-code`, which a pattern naming the package misses.
>
> Only three packages shell out to git — `clients/claude-code`, `internal/contractaxis`,
> `internal/repohygiene`, re-derived including non-test files. That derivation independently
> corroborates the "twelve failures across three packages" measured from the fence, which is what
> makes it trustworthy rather than merely recomputed.
>
> Measured from the `## Acceptance` section alone, calibrated first against one case that MUST hit
> and one that MUST miss: **36 of the 75 docker-fenced task files** are affected (75, not 76 — one
> file's `docker run` was also in prose). **27 hold a recorded exit-0 digest**; **9 do not**. The
> 31/32/37 figures were published in this file and in PR #390 before the fence-scoped measure, and
> are corrected rather than quietly replaced: the arithmetic was never the error, the definition was.
>
> ⚠ **KNOWN LIMIT OF THE 36**, written down so it is not rediscovered as a defect: it still matches
> on the `go test` invocation TEXT. A fence reaching a git-shelling test by a path naming neither the
> package nor a covering wildcard would still be missed. No such case was found; none is proved absent.
>
> ⚠ **THE FENCE NEEDED A SECOND FIX, AND THIS PARAGRAPH SAID SO WHILE STILL CALLING IT UNVERIFIABLE.**
> Retired in place rather than deleted, because the intermediate state is the finding: with
> `safe.directory` alone the ten 128s were gone and ONE unrelated test remained —
> `TestADeadlineKillsTheChildAndItsChildren`, "the process group was not reaped". It was NOT a flake.
> It failed both recorded runs at ~3.31s while passing 3/3 in the same container in isolation and
> passing in the full suite on the host, which is exactly the signature of a container with no init:
> the orphaned `sleep` is reparented to PID 1, `sh` never reaps it, and it lingers as a ZOMBIE — so
> `syscall.Kill(pid, 0)` keeps succeeding and never returns the `ESRCH` the test requires. The test's
> own comment anticipates a brief zombie; what it assumes is a reaper, and a container has none.
>
> `--init` supplies one. Measured as a control and a treatment, full suite, same image, same tree:
> **without `--init` 2 of 2 runs FAILED on that test; with `--init` 2 of 2 PASSED with zero
> failures.** The fence now carries both fixes and the acceptance is exit 0.
>
> ⚠ **THE MUTATION LOG WAS EMPTY ON PURPOSE UNTIL THE FENCE WENT GREEN, AND THAT REASONING IS WHY IT
> IS TRUSTWORTHY NOW.** `adr-verify --mutant` infers a kill from the fence's exit code. While the
> fence was red for a reason no mutant caused, every mutant would have recorded `killed` against a
> fence that fails with the mechanism intact — a verdict bound to nothing, which is the exact defect
> this ADR exists to prevent. The three entries below were recorded only after the acceptance reached
> exit 0, so each `mutant killed` means the fence went red BECAUSE of that mutant and not around it.
>
> ⚠ **THE THREE EARLIER VERIFICATION-LOG ENTRIES ARE KEPT ON PURPOSE** — one exit-1 from before
> `safe.directory`, two from between the two fixes. An attempt that got further than the last one is
> evidence, and deleting them would hide that the first blocker was fixed before the second was found.

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
docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
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
| ⚠ **2026-09-07, step 2 taken for the first time — the three below are what the Mutation Log actually records.** Same three mechanisms, re-spelled at the line each one is really decided on, because the rows above were written from the DIFF and §6 says to choose from the assertions. | | |
| `supersedeInto`: `c.ValidTo == ""` → `c.ValidTo == "" && c.ID == id` (end only the named chunk) | yes | regression: no stale half after update |
| `InvalidateDrawer`: `c.ValidTo != ""` → `c.ValidTo != "" \|\| c.ID != id` (retract only the named chunk) | yes | regression: delete leaves no chunk |
| `anchorReplacement`: `sent > 0 && len(anchors) == 0` → `false && …` (stop refusing an all-unreadable list) | yes | regression: malformed anchors do not clear |

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
- 2026-09-07 · ed9b01e* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:71af492465f463b3d0b5b9cb2a41047ab1d9901515b47f65d97d15266c3c0d1d · ms:283785
- 2026-09-07 · ed9b01e* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:71af492465f463b3d0b5b9cb2a41047ab1d9901515b47f65d97d15266c3c0d1d · ms:262112
- 2026-09-07 · ed9b01e* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:71af492465f463b3d0b5b9cb2a41047ab1d9901515b47f65d97d15266c3c0d1d · ms:254305
- 2026-09-07 · ed9b01e* · exit 0 · `docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:71af492465f463b3d0b5b9cb2a41047ab1d9901515b47f65d97d15266c3c0d1d · ms:278105

## Mutation Log
- 2026-09-07 · ed9b01e* · mutant killed · exit 1 · `internal/palace/supersede.go` · supersedeInto ends only the chunk the caller named; the tail is still ended by the persistRows re-file, but with no superseded_by — the route from a withdrawn claim to its replacement is lost · acceptance-sha256:71af492465f463b3d0b5b9cb2a41047ab1d9901515b47f65d97d15266c3c0d1d
- 2026-09-07 · ed9b01e* · mutant killed · exit 1 · `internal/palace/supersede.go` · InvalidateDrawer ends only the chunk the caller named, so a retracted multi-chunk memory keeps answering from its orphaned children — invisible to a get, visible only to search · acceptance-sha256:71af492465f463b3d0b5b9cb2a41047ab1d9901515b47f65d97d15266c3c0d1d
- 2026-09-07 · ed9b01e* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · anchorReplacement stops refusing an all-unreadable anchor list, so a typo in the only anchor sent CLEARS the anchors the memory already had instead of being rejected · acceptance-sha256:71af492465f463b3d0b5b9cb2a41047ab1d9901515b47f65d97d15266c3c0d1d
