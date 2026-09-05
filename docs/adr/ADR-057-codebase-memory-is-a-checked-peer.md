# ADR-057: codebase-memory is a checked peer of the kit, not an unwatched one

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** Zydrunas
**Spec:** None — no spec stage
**Cross-references:** ADR-041 (recall that does not depend on remembering), ADR-051 (the session that grounds itself), clients/claude-code/README.md, clients/claude-code/commands/am.md
**Governs:** clients/claude-code/doctor.go, clients/claude-code/installer.go, clients/claude-code/settings.go, clients/claude-code/main.go, clients/claude-code/README.md, clients/claude-code/commands/am.md
**Enforced-by:** `clients/claude-code/doctor_test.go::TestDoctorReportsTheCodebaseMemoryPeer`
**Invalidates:** none — checked
**Served-path change:** `aiagentmemory doctor` prints one more row, `codebase-memory`, and exits non-zero when that peer's hooks or MCP entry are registered more than once; `aiagentmemory install --recommended` stops registering the peer under a second name and removes duplicate peer hook registrations it finds.

## Context

The protocol every session runs (`clients/claude-code/commands/am.md` Step 1b, and the same text in
`agentsmemory-bootstrap.md`) tells the agent to call codebase-memory FIRST, on every task. The kit
installs it under `--recommended`. Nothing in the kit ever looks at it again. Three things were
measured on the owner's machine on 2026-09-05, none of which the kit could report:

- `cbm-session-reminder` was registered FOUR times on `SessionStart` in `~/.claude/settings.json`
  (the upstream `install.sh` appends on every run and never dedupes). Four injections and four
  1.5 s processes at every session start, on top of the recall hook. `aiagentmemory doctor` printed
  a clean report the whole time.
- The kit's `--recommended` path runs upstream's `install.sh` — which registers the MCP as
  `codebase-memory-mcp` — and then registers the SAME binary again as `codebasememory`
  (`clients/claude-code/main.go`, `codebaseMemoryName`). A machine that took that path carries two
  stdio registrations of one server, two daemons, and two tool prefixes; the protocol text and the
  harness's own reminders name `codebase-memory-mcp`, so the kit's name is the one nothing reads.
  The owner's machine has only `codebase-memory-mcp`, because it was installed upstream-first.
- `claude mcp list` reported `codebase-memory-mcp ✘ CONNECTION_CLOSED` for days in August
  (recorded in `am.md` itself), and three sessions were read as "forgot Step 1b" while the server
  was down. The kit's doctor, the one place an operator looks, said nothing.

The class this record governs: **every place the kit names, installs, registers or is told to use
codebase-memory.** Enumerated 2026-09-05 with
`grep -rln 'codebaseMemory\|codebasememory\|codebase-memory' clients/claude-code/*.go clients/claude-code/README.md clients/claude-code/commands/am.md README.md`
— ten files, of which the non-test Go files are `assets.go`, `installer.go`, `main.go`,
`sandbox.go` (27 mentions). Left out on purpose: the server (`internal/`, `cmd/`) holds zero
mentions — the palace's 2026-09-04 fact "share no runtime integration" still holds, and this record
keeps it so.

## Existing Primitives Audit

- `doctor`'s hook rung (`registeredHookEvents`, `doctor.go`) already parses `settings.json`
  registrations per event; the peer rung REUSES that parse and adds a count per script.
- `doctor`'s bridge rung (`recordedMCPCommand`) already parses an `mcpServers` map; the peer rung
  reuses the shape against `.claude.json`, which `tokenFromConfigDir` (`mcpcall.go`) already
  resolves and reads — so this is not "starting to parse a format this project does not own", it is
  reading the file the kit already reads, for a second key.
- `ensureHooks` (`settings.go`) rewrites the kit's own registrations idempotently; the dedupe pass is
  a sibling that runs over EVERY event's entries and removes exact duplicates of any command, so the
  peer's four copies collapse the same way the kit's would.
- `installRecommended` and `addStdioMCP` (`installer.go`) are reshaped, not replaced.

## Decision

`aiagentmemory doctor` gains one row, `codebase-memory`, judged from files the kit already reads:
the MCP registration(s) naming the codebase-memory binary in `.claude.json` (either name), whether
that binary exists and is executable, and how many times each `cbm-*` script is registered per
event in `settings.json`. Verdicts: `ok` (one registration, one of each hook), `absent` (nothing
registered — the peer is optional, exit stays 0), `DUPLICATE` (the same MCP under two names, or a
hook script registered more than once for one event — exit non-zero, because every session pays
for each copy and nothing else will ever say so), `BROKEN` (registered but the binary is missing or
not executable — exit non-zero). It does NOT dial the server: a stdio MCP is spawned per session by
the harness, and "can I spawn it" is answered by the executable bit, while "is the index fresh" is
codebase-memory's own `index_status` and not the kit's to judge.

`aiagentmemory install --recommended` registers the peer under ONE name, `codebase-memory-mcp` —
the name upstream's installer uses, the protocol text names, and the harness exposes as the tool
prefix — and only when upstream's script did not already register it; the kit's `codebasememory`
name is retired, and an existing registration under it is removed on the next `--recommended`
install. Every install (not only `--recommended`) removes exact-duplicate hook entries within an
event before writing the kit's own, so an upstream installer that appends on every run is corrected
by the next kit install rather than accumulating.

What would make this fail: a doctor run over a settings file carrying four `cbm-session-reminder`
entries that prints `ok` or exits 0; an install over the same file that leaves more than one. Both
fixtures exist today — the owner's pre-cleanup `settings.json.bak-20260905-*` is the real one, and
the tests seed the same shape.

## Alternatives Considered

- **Fix upstream's `install.sh` instead:** it is the actual author of the duplicates. Rejected as
  the ONLY fix because the kit is where the operator looks and the kit runs that script under
  `--recommended`; an upstream fix is welcome and filed as a follow-up, but the kit must report the
  state it creates.
- **Doctor dials the peer over stdio and asks `index_status`:** the strongest check. Rejected here
  because it spawns a daemon from a diagnostic command, on a server whose stale-IPC failure mode
  (recorded 2026-08-31: a leftover socket makes start poll to a 30 s timeout) would turn `doctor`
  into the thing it diagnoses. Deferred, not refused.
- **Keep the kit's own MCP name `codebasememory`:** it is what the README promises today. Rejected
  because the protocol, upstream, and the harness's tool prefix all say `codebase-memory-mcp`; a
  second name is a second daemon and a tool set no document tells the agent to call.
- **Make codebase-memory a hard dependency the kit refuses to run without:** rejected; ADR-020's
  kits (Desktop, pi) cannot host a stdio MCP at all, and `am.md` already carries the
  "unreachable — say so and carry on" branch. `absent` is a legal verdict.

## Component / Boundary Impact

Internal to the Claude Code kit (`clients/claude-code`): `doctor.go` gains a rung, `installer.go`
changes one registration name and adds a dedupe pass in `settings.go`. The server is untouched. No
new component; the bounded context (the kit judges the operator's machine) is unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `aiagentmemory doctor` output | new row `codebase-memory` with labels ok / absent / DUPLICATE / BROKEN; DUPLICATE and BROKEN set a non-zero exit | doctor.go | operators, scripted health checks |
| Claude MCP registration name for the peer | `codebasememory` retired; `codebase-memory-mcp` is the one name the kit registers or accepts | installer.go / main.go | the harness tool prefix, `am.md`, README |
| `settings.json` hook entries | exact duplicates within an event are removed on every install | settings.go | Claude Code |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `codebaseMemoryMCPName = "codebase-memory-mcp"` and the retired `codebasememory` | T2 | T1 (doctor reads both names to report DUPLICATE) | No — T1 reads both; T2 writes one |

## Implementation

See `tasks/README.md`. T1 the doctor rung; T2 the installer's single name and the dedupe pass.
T1 first, because it is the externally observable half and the one that would have caught the
measured state; T2 consumes the name constant only to retire it.

## Consequences

- **Positive:** the one command an operator runs reports the peer the protocol depends on; four
  injections per session start become one on the next install; one daemon instead of two on a
  `--recommended` machine.
- **Negative:** `doctor` exits non-zero on a state that used to pass silently — a scripted health
  check that never saw DUPLICATE will see it once. That is the point.
- **Neutral:** the README's `codebasememory` promise changes; a machine already carrying both names
  loses the kit's on the next `--recommended` install and keeps upstream's.

## Out of Scope

- The server learning anything about codebase-memory (permanent: boundary: the palace's code anchors are AAM's own index of code reality; whether they duplicate the graph is a different decision, and "share no runtime integration" stays true)
- Dialling the peer from `doctor` to read `index_status` (deferred: docs/adr/BACKLOG.md)
- Fixing upstream's `install.sh` so it stops appending hook registrations (external: DeusData/codebase-memory-mcp: https://github.com/DeusData/codebase-memory-mcp)
- Codex and pi kits: codex registers MCPs through `codex mcp add` in `config.toml`, which the kit does not parse, and pi hosts no stdio MCP (permanent: boundary: the doctor rung is Claude-only, and the row says `n/a` for those kits rather than guessing)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The dedupe pass removes a duplicate an operator wanted (two identical hook entries are never wanted, but a near-duplicate with different env is) | Low | Med | dedupe on the exact `(type, command)` pair only; a differing prefix is a different command and stays |
| `.claude.json` moves with `CLAUDE_CONFIG_DIR` and the rung reads the wrong one | Med | Med | resolve through the same function `tokenFromConfigDir` uses, and test with a pinned config dir |
| Retiring `codebasememory` strands a machine whose harness tools were addressed under that prefix | Low | Low | none of this corpus's docs or hooks name that prefix (grep in Context); the README line is updated in T2 |

## Rollback

Revert T2's commit to restore the old name and drop the dedupe; revert T1's to drop the row.
Neither touches persistent state: a machine that already had duplicates removed keeps the cleaner
file, which is the state every version of the kit wanted.

## Follow-ups

- [ ] File the append-on-every-run defect upstream with the four-copy measurement, and link the issue here.
- [ ] Decide whether `doctor` should read `index_status` over stdio once codebase-memory's stale-IPC start is bounded (the Alternatives entry above).
