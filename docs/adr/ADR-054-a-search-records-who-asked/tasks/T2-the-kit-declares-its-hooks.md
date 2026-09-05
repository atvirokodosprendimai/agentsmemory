# Task ADR-054-T2: The kit sends the origin, and every hook declares what it is

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** every shipped hook's `mcp search` call carries `hook:<basename>`
**Consumes:** `mcpprotocol.OriginHeader`, `mcpprotocol.OriginEnvVar` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the CLI sends the header`, `every hook exports the variable`

## Goal

`aiagentmemory mcp` sends `X-Agentsmemory-Origin` when `AGENTSMEMORY_ORIGIN` is set, and each shipped hook exports `AGENTSMEMORY_ORIGIN=hook:<its basename>` before calling it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/mcpcall.go` | edit | add the header to the map handed to `transport.WithHTTPHeaders` when the variable is non-empty — the ONE client every shipped hook goes through |
| `clients/claude-code/hooks/agentsmemory-recall-hook.sh` | edit | `export AGENTSMEMORY_ORIGIN="hook:$(basename "$0")"` before `set -- mcp search …` |
| `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` | edit | same |
| `clients/claude-code/hooks/agentsmemory-anchor-cue-hook.sh` | edit | same, if it calls `mcp search` — the gate below decides, not this table |
| `clients/claude-code/hookorigin_test.go` | add | the gate over the hooks directory: any script that calls `mcp search` must export the variable — derived from the directory, so a hook added tomorrow is asked the same question |
| `clients/claude-code/mcpcall_origin_test.go` | add | the header half |
| `clients/claude-code/README.md` | edit | document `AGENTSMEMORY_ORIGIN` in the hooks section — a variable the code reads and no doc names is the failure `TestReadEnvVarsAreDocumented` exists for; confirm that gate's universe includes this package, and if it does not, say so in the sign-off |

## Ordered Steps

1. [S1] Write `TestEveryRecallHookDeclaresItsOrigin` and `TestMCPCallSendsTheOriginHeaderFromTheEnvironment` and run them red.
2. [S2] Add the header in `mcpcall.go`; the value is the variable verbatim, and an empty variable sends no header at all — a header with an empty value would be an origin of `''` claimed explicitly, which is indistinguishable from the absent case and adds a byte to every call.
3. [S3] Export the variable in every hook the gate names; keep the value `hook:<basename>` so `RecallStats` can filter on the `hook:` prefix and an operator can still see which hook.
4. [S4] Document the variable; run the fence green; record S1's red run. `[proof: acceptance]`

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestEveryRecallHookDeclaresItsOrigin|TestMCPCallSendsTheOriginHeaderFromTheEnvironment' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ ./cmd/server/ -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryRecallHookDeclaresItsOrigin` | `clients/claude-code/hookorigin_test.go` | every `hooks/*.sh` containing `mcp search` also contains `AGENTSMEMORY_ORIGIN=hook:`; fails naming the script | — | S1, S3 |
| `TestMCPCallSendsTheOriginHeaderFromTheEnvironment` | `clients/claude-code/mcpcall_origin_test.go` | with the variable set the request headers carry `X-Agentsmemory-Origin`; unset, they do not | — | S1, S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests |
| 2 — something selects it | the hooks' `export` lines; the mutant is removing one export, which turns the directory gate red naming that script |
| 3 — the caller can discover it | the kit README names the variable; `TestReadEnvVarsAreDocumented` if its universe reaches this package |
| 4 — it is used | T3's `hook_searches` per wing on the live palace |

## Mutation Log

- 2026-09-05 · 5c0fd56* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the recall hook stops declaring its origin, so its searches record as a person; TestEveryRecallHookDeclaresItsOrigin must name the script · acceptance-sha256:b11431129085315c1d3539b188731a89ee8eaf53f804cd38dd17765d416d086e

## Invariants

- A hook that cannot reach the palace still exits 0 and prints nothing — the variable changes what is recorded, never whether the hook speaks.
- No tool schema changes.

## Risks

- A hook on an older kit sends nothing and its recalls read as a person's until the kit is updated; `hook_searches: 0` beside a polluted list is the tell (Consequences).

## Stop Condition

Stop if a shipped hook reaches the palace by a route other than `aiagentmemory mcp` — the gate would then be asking the wrong question, and the record needs a second channel named.

## Out of Scope

- The report (T3).

## Verification Log
- 2026-09-05 · 5c0fd56* · exit 1 · `set -o pipefail …` · acceptance-sha256:b11431129085315c1d3539b188731a89ee8eaf53f804cd38dd17765d416d086e · ms:1282
  ```
  --- last 8 line(s) of stdout
  --- FAIL: TestEveryRecallHookDeclaresItsOrigin (0.00s)
      hookorigin_test.go:34: agentsmemory-recall-hook.sh performs a search and never exports AGENTSMEMORY_ORIGIN=hook:<name>; its recalls will be recorded as a person's and reach am_recall_stats' to-write list
      hookorigin_test.go:34: agentsmemory-task-recall-hook.sh performs a search and never exports AGENTSMEMORY_ORIGIN=hook:<name>; its recalls will be recorded as a person's and reach am_recall_stats' to-write list
  --- FAIL: TestMCPCallSendsTheOriginHeaderFromTheEnvironment (0.00s)
      mcpcall_origin_test.go:55: AGENTSMEMORY_ORIGIN did not reach the server verbatim: X-Agentsmemory-Origin=""
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	0.427s
  FAIL
  ```
- 2026-09-05 · 5c0fd56* · exit 0 · `set -o pipefail …` · acceptance-sha256:b11431129085315c1d3539b188731a89ee8eaf53f804cd38dd17765d416d086e · ms:21300
