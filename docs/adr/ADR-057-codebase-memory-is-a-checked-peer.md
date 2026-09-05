# ADR-057: codebase-memory is a checked peer of the kit, not an unwatched one

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** Zydrunas
**Spec:** None — no spec stage
**Cross-references:** ADR-041 (recall that does not depend on remembering), ADR-051 (the session that grounds itself), clients/claude-code/README.md, clients/claude-code/commands/am.md
**Governs:** clients/claude-code/doctor.go, clients/claude-code/installer.go, clients/claude-code/settings.go, clients/claude-code/main.go, clients/claude-code/README.md, clients/claude-code/commands/am.md
**Enforced-by:** `clients/claude-code/doctorpeer_test.go::TestDoctorReportsTheCodebaseMemoryPeer`
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
  ⚠ The first draft of this record said the owner's machine carried only `codebase-memory-mcp`.
  Review of #265 found both, in two config dirs: `~/.claude.json` has upstream's name and
  `~/.sandboxes/<name>/.claude.json` has the kit's, with the reviewing session running under the
  kit's and its tools exposed as `mcp__codebasememory__*`. Those are two INSTALLS, each with one
  registration — not one install registered twice — and the reviewer's own run of T1's binary
  against each (#266) reports each correctly on its own. That measurement is why the Decision reads
  one registry per install rather than a union across config dirs.
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

`aiagentmemory doctor` gains one row, `codebase-memory`, judged from files the kit already reads.
The MCP half reads the ONE registry the install under judgement actually uses — `~/.claude.json`
for a global install, `<config-dir>/.claude.json` for a pinned one (a sandbox, `--config-dir`),
the rule `pinConfigDir` already encodes — and collects each entry under either name. Doctor is
per-install (`--agent`, `--target-dir`), so a sandbox is judged by `doctor --target-dir
~/.sandboxes/<name>`, not folded into the global verdict: a union across config dirs was tried in
review and reported DUPLICATE over two legitimate installs that each carry one registration (see
Alternatives). The hook half counts how many times each `cbm-*` script is registered per event in
that install's `settings.json`. The binary named by each entry is checked for the execute bit. Verdicts: `ok` (one registration, one of each hook; when that one registration is under the
retired `codebasememory`, the row says so, because `install --recommended` renames it and until
then the session's tool prefix is one no document names), `absent` (nothing registered — the peer
is optional, exit stays 0), `DUPLICATE` (the peer under both names in this install's registry, or
a hook script registered more than once for one event — exit non-zero, because every session pays
for each copy and nothing else will ever say so),
`BROKEN` (registered but a named binary is missing or not executable, or hooks with no entry — exit
non-zero). It does NOT dial the server: a stdio MCP is spawned per session by
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
- **Read the union of every discoverable registry (global, `$CLAUDE_CONFIG_DIR`, the target dir,
  every sandbox):** raised as a blocker on #265 and built during T1. Rejected by measurement on
  #266: the two names on the reviewer's machine are two installs with one registration each, and
  the union reported DUPLICATE over both while `doctor --target-dir <each>` reported each right.
  A diagnostic that reads a file the agent under judgement does not is a diagnostic of nothing;
  the blocker was withdrawn by its author.
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
- Giving the peer's hook entries a `timeout` when the dedupe rewrites them (permanent: boundary: they are upstream's hooks, not the kit's to police — #263's gate derives its universe from `hookPlans()` and stops at the kit's own; the 1.5 s × 4 cost is the duplication, which T2 removes, not the absence of a bound)
- Codex and pi kits: codex registers MCPs through `codex mcp add` in `config.toml`, which the kit does not parse, and pi hosts no stdio MCP (permanent: boundary: the doctor rung is Claude-only, and the row says `n/a` for those kits rather than guessing)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The dedupe pass removes a duplicate an operator wanted (two identical hook entries are never wanted, but a near-duplicate with different env is) | Low | Med | dedupe on the exact `(type, command)` pair only; a differing prefix is a different command and stays |
| A ghost `<config-dir>/.claude.json` under the GLOBAL dir (left by an old pinned install) is never read by a plain `claude` and never by the rung — a stale registration nobody sees | Low | Low | correct by construction: the rung reads what the agent reads; the owner's machine carries such a ghost today and it is inert |
| Retiring `codebasememory` strands a machine whose harness tools were addressed under that prefix | Low | Low | none of this corpus's docs or hooks name that prefix (grep in Context); the README line is updated in T2 |

## Rollback

Revert T2's commit to restore the old name and drop the dedupe; revert T1's to drop the row.
Neither touches persistent state: a machine that already had duplicates removed keeps the cleaner
file, which is the state every version of the kit wanted.

## Follow-ups

- [ ] File the append-on-every-run defect upstream with the four-copy measurement, and link the issue here.
- [ ] Decide whether `doctor` should read `index_status` over stdio once codebase-memory's stale-IPC start is bounded (the Alternatives entry above).
