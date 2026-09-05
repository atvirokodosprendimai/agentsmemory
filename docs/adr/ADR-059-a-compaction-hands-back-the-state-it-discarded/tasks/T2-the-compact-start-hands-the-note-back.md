# Task ADR-059-T2: The SessionStart recall on `source=compact` hands the note back and recalls the session's checkpoint

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** none
**Consumes:** the note file and its `key=value` format (T1)
**Data dependency:** hermetic for the fence; the sign-off records one live compaction in this checkout
**Proof map:** v1
**Rests-on:** `the note block`, `the checkpoint recall replaces craft on compact`, `the source gate`, `the checkpoint call has no distance floor`

## Goal

After a compaction the recall injection opens with the pre-compaction note and carries the wing's crash-resume checkpoint; on every other `source` the hook is unchanged.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-recall-hook.sh` | edit | read `source` and `session_id` from the event; on `compact`, render the note block and make the second `recall()` call `room=llm_open_threads` with the fixed query, under `checkpoint:` |
| `clients/claude-code/precompact_test.go` | edit | `TestACompactStartHandsBackTheStateNote`, `TestAColdStartDoesNotReadTheNote` |
| `clients/claude-code/README.md` | edit | the recall hook entry says what a `compact` start injects |
| `docs/adr/BACKLOG.md` | edit | the entry deferred from ADR-058 is marked resolved by this record, with the `resume` case deferred under this record's name |

## Ordered Steps

1. [S1] Write both tests red. `TestACompactStartHandsBackTheStateNote`: a stub `aiagentmemory` on PATH that records every argv line and answers one hit; a note for session `s1`; the event `{"hook_event_name":"SessionStart","session_id":"s1","source":"compact"}` with `AGENTSMEMORY_WING=wing_acme`; assert stdout opens with `Before compaction (` naming the branch, head and `3 uncommitted`, that the stub's second call carries `room=llm_open_threads`, `wing=wing_acme` and `limit=1`, that no call carries `wing=wing_craft`, and that the injection carries `checkpoint:`. `TestAColdStartDoesNotReadTheNote`: the same note, `source":"startup"`; assert no `Before compaction`, and that the second call carries `wing=wing_craft`.
2. [S2] Implement the branch in the hook; update README and BACKLOG. [proof: mutation]
3. [S3] Mutants: drop the note block; ask `wing_craft` on `compact`; ignore `source`. [proof: mutation]
4. [S4] Live: compact this session (or a scratch one) in this checkout after installing the kit, and record in the sign-off what the injection opened with. [proof: human: the operator reads the post-compaction context for the `Before compaction` line]

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestACompactStartHandsBackTheStateNote$|TestAColdStartDoesNotReadTheNote$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out && \
go test ./clients/claude-code/ -run 'TestF6AHookIsSilentInTheCommonCase$|TestTheQueryCarriesTheBranchWorkOnACleanTree$|TestNoCredentialIsSilentButABadOneSpeaks$|TestTheRecallHookAsksTheRoomItsRecordShips$|TestAThinQueryIsWidenedOnEveryBranch$|TestTheRecallHookCarriesTheInstalledWing$' -count=1 2>&1 | tee /tmp/acc2.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc2.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestACompactStartHandsBackTheStateNote` | `clients/claude-code/precompact_test.go` | on `compact` the injection opens with the note and the second call asks `llm_open_threads` in the installed wing, not `wing_craft` | — | S1, S2 |
| `TestAColdStartDoesNotReadTheNote` | `clients/claude-code/precompact_test.go` | on `startup` the note is ignored and craft is still asked | — | S1, S2 |
| `TestTheRecallHookCarriesTheInstalledWing` | `clients/claude-code/recallscope_test.go` | ADR-058's wing scoping survives the edit (regression) | — | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests drive the shipped script |
| 2 — something selects it | the hook is already registered matcher-less on SessionStart, so `compact` reaches it (`TestRecallHookIsRegistered`); the `source` gate is what selects the branch, and the "ignore `source`" mutant proves it |
| 3 — the caller can discover it | the README entry; the injection's own first line names what it is |
| 4 — it is used | S4's live compaction; nothing counts it afterwards (Follow-up in the record) |

## Mutation Log

- 2026-09-05 · 42137b9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the note block is never rendered, so a compaction hands nothing back · acceptance-sha256:c0f4ddf49ed44371576d94e63bb5c24dcb6ca606f6e951c0876353861d23d586 · covers:the note block
- 2026-09-05 · 42137b9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · a compact start asks wing_craft like a cold start, so the checkpoint is never recalled · acceptance-sha256:c0f4ddf49ed44371576d94e63bb5c24dcb6ca606f6e951c0876353861d23d586 · covers:the checkpoint recall replaces craft on compact
- 2026-09-05 · 42137b9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the source field is ignored and every start is treated as a compaction, so a cold start reads a stale note and skips craft · acceptance-sha256:c0f4ddf49ed44371576d94e63bb5c24dcb6ca606f6e951c0876353861d23d586 · covers:the source gate
- 2026-09-05 · 42137b9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the note block is never rendered, so a compaction hands nothing back (re-recorded after the fence named the real regression test) · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · covers:the note block
- 2026-09-05 · 42137b9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · a compact start asks wing_craft like a cold start, so the checkpoint is never recalled (re-recorded) · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · covers:the checkpoint recall replaces craft on compact
- 2026-09-05 · 42137b9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the source field is ignored and every start is treated as a compaction (re-recorded) · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · covers:the source gate
- 2026-09-05 · 6df040d* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the checkpoint call keeps the 0.42 floor, under which the fixed sentence returned zero checkpoints on 2026-09-05 · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · covers:the checkpoint call has no distance floor
- 2026-09-05 · 6df040d* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the checkpoint query carries the changed basenames, which ranked a day-old checkpoint first on 2026-09-05 · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · covers:the checkpoint recall replaces craft on compact

## Invariants

- On `startup`, `resume` and `clear` the hook's output and calls are byte-for-byte what ADR-058 T2 left.
- The checkpoint call is made only when `AGENTSMEMORY_WING` is set.
- The note block is bounded: one header line and at most eight file names with an `(+N more)` tail.

## Risks

- `session_id` differs across compaction: the note is not found and the block is silent. The live sign-off is where this shows.

## Stop Condition

Stop and reopen the record if S4's live compaction injects no `Before compaction` line while a note for the session exists on disk — that is the `session_id` premise failing, and no unit test can see it.

## Out of Scope

- Reading the note on `resume` (deferred: `docs/adr/BACKLOG.md`, under ADR-059's name).

## Verification Log
- 2026-09-05 · 42137b9* · exit 1 · `set -o pipefail …` · acceptance-sha256:c0f4ddf49ed44371576d94e63bb5c24dcb6ca606f6e951c0876353861d23d586 · ms:3757
  ```
  --- last 10 line(s) of stdout (of 51 after folding 51 raw)
          craft:
          a hit
      precompact_test.go:204: the checkpoint call lacks "room=llm_open_threads": mcp search task/note a.go -a limit=3 -a snippet_chars=300 -a room=diary -a max_distance=0.42 --digest 400 -a wing=wing_craft --token t
      precompact_test.go:204: the checkpoint call lacks "wing=wing_acme": mcp search task/note a.go -a limit=3 -a snippet_chars=300 -a room=diary -a max_distance=0.42 --digest 400 -a wing=wing_craft --token t
      precompact_test.go:204: the checkpoint call lacks "limit=1": mcp search task/note a.go -a limit=3 -a snippet_chars=300 -a room=diary -a max_distance=0.42 --digest 400 -a wing=wing_craft --token t
      precompact_test.go:204: the checkpoint call lacks "WHERE SHOULD WORK RESUME AFTER A CRASH": mcp search task/note a.go -a limit=3 -a snippet_chars=300 -a room=diary -a max_distance=0.42 --digest 400 -a wing=wing_craft --token t
      precompact_test.go:209: a compact start still asks wing_craft: mcp search task/note a.go -a limit=3 -a snippet_chars=300 -a room=diary -a max_distance=0.42 --digest 400 -a wing=wing_craft --token t
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	1.315s
  FAIL
  ```
- 2026-09-05 · 42137b9* · exit 0 · `set -o pipefail …` · acceptance-sha256:c0f4ddf49ed44371576d94e63bb5c24dcb6ca606f6e951c0876353861d23d586 · ms:8354
- 2026-09-05 · 42137b9* · exit 0 · `set -o pipefail …` · acceptance-sha256:c0f4ddf49ed44371576d94e63bb5c24dcb6ca606f6e951c0876353861d23d586 · ms:6481
- 2026-09-05 · 42137b9* · exit 0 · `set -o pipefail …` · acceptance-sha256:c0f4ddf49ed44371576d94e63bb5c24dcb6ca606f6e951c0876353861d23d586 · ms:6332
- 2026-09-05 · 42137b9* · exit 0 · `set -o pipefail …` · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · ms:8351
- 2026-09-05 · 42137b9* · exit 0 · `set -o pipefail …` · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · ms:7010
- 2026-09-05 · 42137b9* · exit 0 · `set -o pipefail …` · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · ms:7422
- 2026-09-05 · 6df040d* · exit 0 · `set -o pipefail …` · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · ms:7972
- 2026-09-05 · 6df040d* · exit 0 · `set -o pipefail …` · acceptance-sha256:7ed5f00de21c3b659caa2052903f3386d3669b1e299189896623bdcc54d53a18 · ms:8429
