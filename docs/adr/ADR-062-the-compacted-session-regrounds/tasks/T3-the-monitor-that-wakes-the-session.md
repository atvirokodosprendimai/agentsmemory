# Task ADR-062-T3: The marker and the monitor that wake the session

**Status:** partial
**Depends-on:** T1
**Produces:** the `agentsmemory-reground/<session>` marker, `/am` Step 1d
**Consumes:** T1's `prompt=` note field and `source=compact` block
**Proof map:** v1
**Rests-on:** `the marker is written on a compaction`, `only on a compaction`, `the two halves resolve one directory`, `the wake names the task`, `a slash command is not the task`
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
| `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` | drop a last user turn that opens with `/` — the compaction command is not the task |
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

## Acceptance

```bash
gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestACompactionWakesTheSessionThroughTheMonitor|TestOnlyACompactionArmsTheWake|TestASlashCommandIsNotTheTaskInFlight' -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|---|---|---|---|---|
| `TestACompactionWakesTheSessionThroughTheMonitor` | `clients/claude-code/regroundwake_test.go` | the shipped monitor script emits a line naming the task for the marker the real hook wrote, under both addressing modes | — | S1, S3, S4, S5 |
| `TestOnlyACompactionArmsTheWake` | `clients/claude-code/regroundwake_test.go` | startup/resume/clear write no marker | — | S2 |
| `TestASlashCommandIsNotTheTaskInFlight` | `clients/claude-code/regroundwake_test.go` | the compaction command is not recorded as the task | — | S6 |

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

## Verification Log
