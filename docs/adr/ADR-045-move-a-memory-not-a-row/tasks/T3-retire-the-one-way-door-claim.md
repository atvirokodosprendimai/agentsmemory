# Task ADR-045-T3: Retire the one-way-door claim, and gate it

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** none
**Consumes:** `Service.moveMemory()` (T1)
**Data dependency:** hermetic

## Goal

The agent surface stops telling every session that a memory over the chunk threshold
can never be moved, and a test fails if that claim comes back.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | The `am_add_drawer` description at the "never MOVED … can be relocated for life" clause is the sentence agents act on; it becomes false the moment T1 lands |
| `internal/mcpserver/description_test.go` | add | The gate |
| `AGENTS.md` | edit | §Reachability's list is pinned by `TestAgentsMdNamesGatesThatExist`, so a new gate must be named there |

`am_update_drawer`'s own description already says only that a correction supersedes
and does not claim a move is refused — confirm that by reading it, and leave it alone
if so.

## Ordered Steps

1. Write the failing test first (TDD red): `TestNoToolDescriptionClaimsALongMemoryCannotBeMoved` in `internal/mcpserver/description_test.go`. It parses this package's source for the tool description strings and fails on a description asserting that a chunked memory cannot be relocated. Run the fence and confirm RED against today's `drawers.go`.
2. Rewrite the `am_add_drawer` clause: keep the chunking fact and the recall reason for staying under the threshold as ADVICE, and delete the claim that the memory can never be moved. Say what IS still refused — relocating an ended record — since that is the remaining one-way door and it is the one nothing else states.
3. Confirm the gate now passes, then re-break the sentence by hand and confirm it goes red again before moving on.
4. Add the gate to `AGENTS.md` §Reachability with the one-line reason it exists, so `TestAgentsMdNamesGatesThatExist` keeps passing and the list does not rot.
5. Run the full suite.

## Acceptance

```bash
set -o pipefail
gofmt -l internal/mcpserver | grep -q . && exit 1
go vet ./... && go test ./internal/mcpserver/ -run "TestNoToolDescriptionClaimsALongMemoryCannotBeMoved" -count=1 -v 2>&1 | tee /tmp/adr045-t3-new.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr045-t3-new.out
go test ./... -count=1 2>&1 | tee /tmp/adr045-t3-reg.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/adr045-t3-reg.out
```

The regression command is `./...` rather than one package, because `AGENTS.md` is
pinned from a different package than the description being changed. This fence runs
the local toolchain for the reason T1's does — see the note there.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestNoToolDescriptionClaimsALongMemoryCannotBeMoved` | `internal/mcpserver/description_test.go` | No tool description in this package asserts that a multi-chunk memory cannot be relocated. Carries a subtest driving the same predicate over a FIXTURE string that does make the claim, because a corpus with zero offenders cannot exercise the branch that reports one | — |

The falsifiability subtest routes its verdict through a substitutable `testing.TB`,
matching `TestEveryCitedADRResolves`'s shape — `AGENTS.md` records that a gate which
cannot pin its own reporting stayed green while announcing a clean sweep over a tree
carrying a real offender.

Scope is named in the test's own declaration: the universe is `internal/mcpserver`
descriptions, not every string in the repo, for the reason
`TestEveryOmitemptyWireKeyInThisPackageIsDescribed` says out loud — a gate whose name
claims more than it covers is worse than a narrower one.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestNoToolDescriptionClaimsALongMemoryCannotBeMoved` |
| 2 — something selects it | It is a `go test` in a package the suite runs; step 3 re-breaks the sentence and watches it fail |
| 3 — the caller can discover it | The description IS rung 3 for T1 — this task is what makes T1's behaviour discoverable, and `AGENTS.md` §Reachability names the gate for a human reader |
| 4 — it is used | Nothing measures whether agents stop trimming; the honest answer is that the next session's behaviour is the only signal and nothing records it |

## Mutation Log

- 2026-09-01 · 84b09c8* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · puts the retired relocation refusal back into the shipped am_add_drawer description · acceptance-sha256:440e1396b0fbed522965e6c2d759f916e497f80e6ec83b7edf276e8d8f487b44

## Invariants

- The description still states the chunking fact and the one-vector-per-drawer recall reason. This task removes an enforcement claim, not the advice.
- The description still names what IS refused: relocating an ended record, and the `llm_init` entry-room refusal, which are unaffected by ADR-045.
- `AGENTS.md` §Reachability names every gate that exists and no gate that does not.

## Risks

- A gate matching on prose is a gate that can cry wolf, which `AGENTS.md` records happening to `TestNoDocCitesItsOwnLineNumbers`. Mitigation: match the specific claim about relocation, on word boundaries, over description strings only — and if an honest sentence cannot be written past the matcher, loosen the matcher rather than contorting the sentence.
- Rewriting the description changes what every session reads at wake-up. Mitigation: the replacement is shorter and states the same chunking fact; nothing that was true stops being said.

## Stop Condition

Stop and ask if the gate cannot be written so that the CURRENT sentence fails it —
that would mean the claim is phrased too loosely to match, and a gate that cannot see
today's offender will not see tomorrow's either.

## Out of Scope

- The palace-side records that teach the same one-way door (`start-here` §Size, the `sizes` L0 leaf, `wing_agentmemories/learnings` `0b771576…`). They live in the memory server, not this repository, so no test here can reach them — carried as a Follow-up on the parent ADR.
- Every other tool description in the package (permanent: this gate is about one retired claim, and widening it to "descriptions must be true" is a gate nothing could pass).

## Verification Log
- 2026-09-01 · 84b09c8* · exit 1 · `set -o pipefail …` · acceptance-sha256:440e1396b0fbed522965e6c2d759f916e497f80e6ec83b7edf276e8d8f487b44
  ```
  --- last 10 line(s) of stdout (of 780 after folding 784 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.917s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	1.121s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.595s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.176s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	1.059s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	1.109s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.921s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	1.057s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	1.145s
  FAIL
  ```
- 2026-09-01 · 84b09c8* · exit 0 · `set -o pipefail …` · acceptance-sha256:440e1396b0fbed522965e6c2d759f916e497f80e6ec83b7edf276e8d8f487b44
