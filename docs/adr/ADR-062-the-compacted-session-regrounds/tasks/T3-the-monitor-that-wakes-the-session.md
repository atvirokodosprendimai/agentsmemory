# Task ADR-062-T3: The marker and the monitor that wake the session

**Status:** partial
**Depends-on:** T1
**Produces:** the `agentsmemory-reground/<session>` marker, `/am` Step 1d
**Consumes:** T1's `prompt=` note field and `source=compact` block
**Proof map:** v1
**Rests-on:** `the marker is written on a compaction`, `only on a compaction`, `the two halves resolve one directory`, `the wake names the task`, `a slash command is not the task`, `harness chrome is not the task`, `a marker already on disk is not an event`, `both protocol copies name the wake`
**Covers:** —

## Goal

The post-compaction re-ground stops depending on the model choosing to read a
line of text. The hook leaves a marker; a monitor the session armed turns its
appearance into a notification, and a notification makes the session take a turn.

## Context — the boundary this record asserted and never probed

ADR-062's Decision says a trigger is not buildable here: *"No hook can invoke a
skill — not on a timer, not at all… A design that needed a real trigger would not
be buildable here."* The first clause is true. The generalisation is false, and it
was written without a probe — the counter-example was running in the session that
wrote it. A persistent `Monitor` emits every stdout line as a notification;
measured 2026-09-05, one armed before a 15:21:19Z compaction delivered an event to
the same session after it, because a compaction replaces the CONTEXT and not the
SESSION.

The hook never had to invoke anything. It writes a file whose APPEARANCE is the
event. Those are two mechanisms and the record collapsed them into one.

## Affected Files

| File | Change |
|---|---|
| `clients/claude-code/hooks/agentsmemory-recall-hook.sh` | write `agentsmemory-reground/<session>` carrying the task, inside the existing `AGENTSMEMORY_REGROUND` block; retire the false comment in place |
| `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` | drop a last user turn that opens with `/`, and one that is wrapped harness chrome (`<command-…>`, local-command stdout, a task notification, a system reminder) — neither names the task |
| `clients/claude-code/commands/am.md` | Step 1d: `TaskList`, then arm a persistent `Monitor` over that directory if none is armed |
| `docs/adr/ADR-062-the-compacted-session-regrounds.md` | amend the "not buildable" paragraph beside itself; re-disposition the Out of Scope entry |
| `clients/claude-code/regroundwake_test.go` | `TestACompactionWakesTheSessionThroughTheMonitor`, `TestOnlyACompactionArmsTheWake`, `TestASlashCommandIsNotTheTaskInFlight` |

## Ordered Steps

1. [S1] Write `TestACompactionWakesTheSessionThroughTheMonitor` red: drive the real PreCompact and recall hooks, then extract the bash fence from the shipped `am.md` and run it; assert it emits a line naming `/amm <task>`.
2. [S2] Write `TestOnlyACompactionArmsTheWake` red: `startup`, `resume` and `clear` write no marker.
3. [S3] Write the marker in the recall hook's `source=compact` block; retire the false comment in place rather than deleting it.
4. [S4] Add Step 1d to `am.md`, addressing the directory exactly as the hook does.
5. [S5] Run the pair under BOTH addressing modes — explicit `AGENTSMEMORY_STATE_DIR` and the `${TMPDIR:-/tmp}` default — because a test that always sets the variable never executes the default branch. [proof: mutation]
6. [S6] Drop a leading-`/` turn in the PreCompact extraction, with `TestASlashCommandIsNotTheTaskInFlight`.
7. [S7] Mutants, one per Rests-on mechanism. [proof: mutation]
8. [S8] Record one real compaction in which the monitor fires and the session re-grounds. [proof: human: no test can observe a notification arriving in a live session; only a transcript shows the wake changed what the session did next]

   **Observed 2026-09-05, session `a59e1cad`, monitor `bz4bkmjl3`, armed at `136004f` before the compaction.** The compaction replaced the context; the monitor's line arrived as a notification and the session took a turn on it. What it changed is the part no test could have shown: the re-ground was the FIRST action out of the summary, ahead of the two PR threads that were also waiting, and it was taken because the notification arrived — not because the summary asked for it. The printed `PAUSE` was in that same context and had been for every prior compaction; the wake is what got acted on.

   ⚠ **AND THE WAKE'S OWN LABEL WAS DEFECTIVE, WHICH IS THE EVIDENCE ARRIVING TWICE.** It read ``/amm <command-message>am</command-message><command-name>/am</command-name><command-args>recall</command-args>`` — the `^/` guard S6 added catches the bare spelling only, and a slash command reaches the transcript wrapped in tags, so its content begins with `<`. Fixed in the same turn and the fixtures widened. The mechanism was debugged BY being used, which is the whole argument for a human step here: five fixtures agreed with each other and none of them had the shape the harness actually produces.

   ⚠ **AND RUNNING THE FIXED HOOK AGAINST THE REAL TRANSCRIPT FOUND A FOURTH FORM, WHICH COMPOUNDS.** The label became `This session is being continued from a previous conversation…` — the plain `type=user` turn the harness injects AFTER a compaction. It is prose, so no bracket rule reaches it, and it stays the last plain turn for as long as the resumed session works without the user typing: a session's SECOND compaction would label its wake with its FIRST one's preamble, degrading exactly as the session gets longer. Verified against the live transcript before and after — `prompt=This session is being continued…` → `prompt=recall`.
9. [S9] Pin `am.md` and `bootstrap.md` equal on the wake and on its Claude-only caveat, with `TestBothProtocolsNameTheRegroundWake`. Two copies of one protocol is this repository's recorded hazard, and `Monitor` is a Claude Code tool — codex and pi run the same bootstrap and can arm nothing.

## Acceptance

```bash
gofmt -l clients/claude-code | grep -q . && exit 1
go vet ./clients/... || exit 1
go test ./clients/claude-code/ -run '^(TestACompactionWakesTheSessionThroughTheMonitor|TestOnlyACompactionArmsTheWake|TestASlashCommandIsNotTheTaskInFlight|TestBothProtocolsNameTheRegroundWake)$' -count=1 -v 2>&1 | tee /tmp/a62t3.out
if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a62t3.out; then echo "vacuous or failing"; exit 1; fi
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|---|---|---|---|---|
| `TestACompactionWakesTheSessionThroughTheMonitor` | `clients/claude-code/regroundwake_test.go` | armed over a directory already holding a stale marker and a subdirectory, the shipped script emits the NEW task and never a replayed or empty one, under both addressing modes | — | S1, S3, S4, S5, S7 |
| `TestOnlyACompactionArmsTheWake` | `clients/claude-code/regroundwake_test.go` | startup/resume/clear write no marker | — | S2 |
| `TestASlashCommandIsNotTheTaskInFlight` | `clients/claude-code/regroundwake_test.go` | neither spelling of a slash command, nor local-command stdout, a task notification or a system reminder, is recorded as the task — the wrapped case came from the live wake in S8, not from a fixture | — | S6, S8 |
| `TestBothProtocolsNameTheRegroundWake` | `clients/claude-code/regroundwake_test.go` | `am.md` and `bootstrap.md` agree on the wake and on its Claude-only caveat | — | S9 |

## Invariants

- The printed `PAUSE` is the FLOOR and stays. A session that never ran `/am` armed no monitor, and must be no worse off than before this task.
- The marker write cannot fail the hook: an unwritable state dir loses the wake, never the branch and uncommitted count ADR-059 hands back.
- The marker is keyed by session, like the note beside it, so two concurrent sessions cannot read each other's task.
- `AGENTSMEMORY_REGROUND=off` drops the marker with the directive; one knob, both halves.

## Risks

- A monitor armed twice would emit twice. Mitigated by Step 1d checking `TaskList` first; the cost of a duplicate is a repeated notification, not a wrong one.
- The wake is only armed if `/am` ran this session. Accepted and stated in the command: this raises the ceiling and does not remove the floor.

## Out of Scope

- Arming the monitor without `/am` (deferred: docs/adr/BACKLOG.md)
- A hook invoking a skill directly (permanent: boundary: the harness offers no callback that runs a slash command from a hook)

## Stop Condition

Stop if a monitor armed before a compaction is found NOT to survive it: the wake
would then need arming after the event it is meant to detect, which is circular,
and the printed pause would remain the only mechanism.

## Mutation Log

- 2026-09-05 · 0b8d533* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the hook writes no marker, so the monitor has no event to see and the wake never fires — the pause still prints, which is exactly how this would ship unnoticed · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:the marker is written on a compaction
- 2026-09-05 · 0b8d533* · mutant killed · exit 1 · `clients/claude-code/commands/am.md` · the command watches a directory the hook never writes to: a monitor that is armed, running and structurally unable to fire · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:the two halves resolve one directory
- 2026-09-05 · 0b8d533* · mutant killed · exit 1 · `clients/claude-code/commands/am.md` · the wake fires but names no task, so the session re-grounds on nothing — the failure the live /compact compaction produced for real · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:the wake names the task
- 2026-09-05 · 0b8d533* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the compaction command becomes the task, so the wake says /amm /compact — measured on a real compaction before the fix · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:a slash command is not the task
- 2026-09-05 · 0b8d533* · mutant killed · exit 1 · `clients/claude-code/commands/am.md` · arming replays every marker already on disk, so each new session is told to re-ground on a previous session finished work; markers outlive sessions by design, so this fires after any past compaction · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:a marker already on disk is not an event
- 2026-09-05 · 0b8d533* · mutant killed · exit 1 · `clients/claude-code/bootstrap.md` · the bootstrap promises codex and pi a trigger they cannot arm, because Monitor is a Claude Code tool and they run this same protocol · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:both protocol copies name the wake
- 2026-09-05 · 0b8d533* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · every startup and resume writes a marker, so the wake fires on sessions that were never compacted and sends them re-grounding on stale work · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:only on a compaction
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 0b8d533* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · every startup and resume writes a marker, so the wake fires on sessions that were never compacted · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:only on a compaction
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 1fcc2da* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · every startup and resume writes a marker with a note present, so the wake fires on sessions that were never compacted; re-run after the test was fixed to seed the note, which is what makes the source the deciding condition · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:only on a compaction
- 2026-09-05 · 74d5858* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the task label keeps every wrapped harness turn, so the wake names a command wrapper instead of work — exactly what the first live compaction with the monitor armed emitted · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · covers:harness chrome is not the task

## Verification Log
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7339
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7157
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7146
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7170
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7283
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7233
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7136
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7308
- 2026-09-05 · 0b8d533* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7135
- 2026-09-05 · 1fcc2da* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7636
- 2026-09-05 · 74d5858* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:9369
- 2026-09-05 · human-observed · S8: session a59e1cad, monitor bz4bkmjl3 armed at 136004f before a 16:12Z compaction — the wake arrived as a notification and the session re-grounded on it as its first action out of the summary; the wake's own label was defective (a wrapped slash command), which is how the harness-chrome guard was found
- 2026-09-05 · ad023a5* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1 …` · acceptance-sha256:a7041b09bb5c6dda0c8df95d5a20437f8d9e710f3b533d108aeaf25d1d32395f · ms:7798
