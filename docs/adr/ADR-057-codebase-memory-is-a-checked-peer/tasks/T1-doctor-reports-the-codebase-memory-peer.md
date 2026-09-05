# Task ADR-057-T1: `doctor` reports the codebase-memory peer: ok, absent, DUPLICATE or BROKEN

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the `codebase-memory` doctor row and its verdict vocabulary
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the hook count per script per event`, `the MCP entry count across both names`, `the exit code on DUPLICATE and BROKEN`, `the registry file a global install reads`

## Goal

`aiagentmemory doctor` prints one row for codebase-memory judged from `settings.json` and `.claude.json`, and exits non-zero when the peer is registered more than once or its binary cannot run.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/doctor.go` | edit | `judgeCodebaseMemory(configDir) peerVerdict` reading hook counts through `registeredHookEvents`' parse and MCP entries through the `.claude.json` `mcpServers` map; one `fmt.Fprintf` row in the same column format as `mcp server`; the verdict's `bad` joins the exit-code fold — this is the line that SELECTS the rung, and the test deletes it |
| `clients/claude-code/doctorpeer.go` | add | the rung, its two file readers, and `claudeMCPRegistry(kit, dir)` — a GLOBAL install's registry is `~/.claude.json`, a pinned dir's is `<dir>/.claude.json`, the rule `pinConfigDir` already encodes; the first draft read `<dir>/.claude.json` unconditionally and reported a ghost `~/.claude/.claude.json` on the owner's machine |
| `clients/claude-code/main.go` | edit | `codebaseMemoryHookScripts` — the `cbm-*` basenames the rung counts — beside the existing `codebaseMemory*` constants, so the universe is declared once |
| `clients/claude-code/doctorpeer_test.go` | add | `TestDoctorReportsTheCodebaseMemoryPeer` with four subtests (ok, absent, DUPLICATE hook, DUPLICATE mcp/BROKEN binary) driving the real CLI over a pinned config dir |
| `clients/claude-code/README.md` | edit | the doctor section names the new row and its four labels |

## Ordered Steps

1. [S1] Write `TestDoctorReportsTheCodebaseMemoryPeer`: a temp config dir with a `settings.json` carrying `cbm-session-reminder` four times on `SessionStart`, a `.claude.json` with `codebase-memory-mcp` pointing at a temp executable; assert the row reads `DUPLICATE` and doctor exits non-zero; subtests for `ok` (one each, exit 0), `absent` (no entries, exit 0, row still printed), and `BROKEN` (entry whose command is not executable, exit non-zero). Run it red.
2. [S2] Implement `judgeCodebaseMemory` and print the row; fold `bad` into the exit code. [proof: mutation]
3. [S3] README: the row and its labels, in the doctor section. [proof: human: the reviewer reads the paragraph beside the code that prints the row]
4. [S4] Mutants, one per Rests-on mechanism: count every script as 1 (hook count); read only one name (mcp count); drop `bad` from the fold (exit code). [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestDoctorReportsTheCodebaseMemoryPeer$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ -run 'TestDoctor' -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestDoctorReportsTheCodebaseMemoryPeer` | `clients/claude-code/doctorpeer_test.go` | the four verdicts from real files through the real CLI, and the exit code on the two bad ones | — | S1, S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestDoctorReportsTheCodebaseMemoryPeer` |
| 2 — something selects it | the `Fprintf` row and the `bad` fold in `runDoctor`; the test drives the real CLI, so deleting the call leaves no row and the DUPLICATE subtest fails |
| 3 — the caller can discover it | the README doctor section names the row; `doctor --help` is unchanged because the row needs no flag |
| 4 — it is used | the owner's own `doctor` run after this lands, recorded in the sign-off comment; nothing measures it beyond that |

## Mutation Log

- 2026-09-05 · c4d91d6* · mutant killed · exit 1 · `clients/claude-code/doctorpeer.go` · a script registered four times must count four; counting presence collapses the duplicate into ok · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · covers:the hook count per script per event
- 2026-09-05 · c4d91d6* · mutant killed · exit 1 · `clients/claude-code/doctorpeer.go` · the same binary under two names must be read as two; reading upstream's name alone reports ok · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · covers:the MCP entry count across both names
- 2026-09-05 · c4d91d6* · mutant killed · exit 1 · `clients/claude-code/doctor.go` · DUPLICATE and BROKEN must reach the exit code; a row that prints and changes nothing is a report nobody scripts against · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · covers:the exit code on DUPLICATE and BROKEN
- 2026-09-05 · c4d91d6* · mutant killed · exit 1 · `clients/claude-code/doctorpeer.go` · a global install must be judged from ~/.claude.json, the file Claude reads; reading <dir>/.claude.json reports the ghost registry the agent never spawns · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · covers:the registry file a global install reads

## Invariants

- `absent` never sets the exit code: the peer is optional (ADR-020's kits cannot host it).
- The rung reads files only; it never spawns the peer.
- Codex and pi print `n/a` in the row rather than a verdict.

## Risks

- `.claude.json` is large on a real machine (4,000+ lines here) and holds other servers' secrets; the rung reads only `mcpServers[*].command` and prints only the two peer names' commands.

## Stop Condition

Stop if `registeredHookEvents` cannot be reused without changing its verdicts for the kit's own hooks — the rung must add a count, not alter the existing report.

## Out of Scope

- Dialling the peer (deferred: docs/adr/BACKLOG.md)
- Removing the duplicates — that is T2's job; this task reports them.

## Verification Log
- 2026-09-05 · c4d91d6* · exit 1 · `set -o pipefail …` · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · ms:13206
  ```
  --- last 10 line(s) of stdout (of 59 after folding 59 raw)
              
                agentsmemory-recall-hook.sh            SessionStart   speaks       5 bytes
                agentsmemory-recall-hook.sh            —              STALE        differs from this binary's embedded copy — `aiagentmemory install` rewrites it
                mcp server                             handshake      ok           v0.0.0-test-stub @ https://aiagentmemory.dev/mcp
              
                all 1 injecting hook(s) are registered on an injecting event and ran
                the server at https://aiagentmemory.dev/mcp reports v0.0.0-test-stub (ok)
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	0.753s
  FAIL
  ```
- 2026-09-05 · c4d91d6* · exit 0 · `set -o pipefail …` · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · ms:3726
- 2026-09-05 · c4d91d6* · exit 0 · `set -o pipefail …` · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · ms:2176
- 2026-09-05 · c4d91d6* · exit 0 · `set -o pipefail …` · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · ms:2292
- 2026-09-05 · c4d91d6* · exit 0 · `set -o pipefail …` · acceptance-sha256:682e6f9b6cbbf0e92d5008a4088af7f3ff374c0a5af72c8944da314148468186 · ms:3793
