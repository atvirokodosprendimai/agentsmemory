# Task ADR-062-T1: The note carries the task in flight, and a compact start pauses

**Status:** done
**Depends-on:** none
**Produces:** `prompt=` in the PreCompact note (T2), the `PAUSE … /amm <task>` line
**Consumes:** ADR-059's note format and `source=compact` block
**Proof map:** v1
**Rests-on:** `the task in flight reaches the note`, `plain user turns only`, `the pause is printed on a compaction`, `only on a compaction`
**Covers:** —

## Goal

A session resuming from a compaction is told to stop and re-ground, and told what
to re-ground ON. ADR-059 gave it the tree; this gives it the task and the
instruction.

## Affected Files

| File | Change |
|---|---|
| `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` | extract `transcript_path`; write `prompt=` — last plain user turn, sidechains and this kit's own injections excluded, one line, 200 chars, whitespace collapsed |
| `clients/claude-code/hooks/agentsmemory-recall-hook.sh` | parse `prompt=`; print the `PAUSE … /amm <task>` line inside the existing `source=compact` block; `AGENTSMEMORY_REGROUND=off` drops it |
| `clients/claude-code/reground_test.go` | `TestACompactStartTellsTheSessionToReGround`, `TestAStartThatIsNotACompactIsUnchanged` |

## Ordered Steps

1. [S1] Write `TestACompactStartTellsTheSessionToReGround` red: drive the real PreCompact hook over a transcript holding a plain turn, a sidechain turn and a recall injection; assert the note names the plain turn and neither other; then drive the real recall hook on `source=compact` and assert the output carries `PAUSE` and `/amm <the task>`.
2. [S2] Write `TestAStartThatIsNotACompactIsUnchanged` red: `startup`, `resume` and `clear` carry neither `PAUSE` nor `/amm`.
3. [S3] Extract `transcript_path` beside the other payload fields, defaulting to empty so the guard holds under `set -u`.
4. [S4] Write the `prompt=` line, with the two exclusions and the bound.
5. [S5] Parse `prompt=` and print the directive inside the `source=compact` block, last, before the recall.
6. [S6] Mutants, one per Rests-on mechanism: `prompt=` never written; the sidechain/injection filter removed; the directive block disabled; the `source=compact` guard widened. [proof: mutation]
7. [S7] Record one real compaction in this checkout and sign off. [proof: human: only a live compaction proves the harness sends the payload these scripts parse; every test here supplies that payload itself]

## Acceptance

```bash
gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestACompactStartTellsTheSessionToReGround|TestAStartThatIsNotACompactIsUnchanged' -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|---|---|---|---|---|
| `TestACompactStartTellsTheSessionToReGround` | `clients/claude-code/reground_test.go` | the note names the plain turn; the compact start pauses and names the task | — | S1, S3, S4, S5 |
| `TestAStartThatIsNotACompactIsUnchanged` | `clients/claude-code/reground_test.go` | startup/resume/clear carry no directive | — | S2 |

## Invariants

- The note's other fields are byte-identical to ADR-059's; this task adds one line and changes none.
- The directive is inside the `source=compact` block, so ADR-041's F-6 (silence when there is nothing) is untouched.
- The task label is ADVISORY. The skill re-grounds from the palace and the tree; a wrong label costs a worse search, never a wrong source.

## Risks

- A transcript shape not seen here names the wrong turn. Mitigated by the two exclusions, each with a mutant, and by the label being advisory.

## Out of Scope

- Non-compaction starts (deferred: PR #278 owns them; unmerged at the time of writing)
- Making the skill run automatically (permanent: boundary: no hook can invoke a skill)

## Stop Condition

Stop if a real compaction shows the harness sends no `transcript_path` on
`PreCompact`: the task label would then be unavailable at the only moment it can
be read, and the pause would have to name the work some other way.

## Mutation Log

- 2026-09-05 · 276cba9 · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · The note is written without its prompt= line, so the wake has no subject: the compact start still prints a PAUSE and the session is told to re-ground on nothing. This ships unnoticed because every other field of ADR-059 note is intact and the block looks healthy. · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · covers:the task in flight reaches the note
- 2026-09-05 · 276cba9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · Drops the sidechain half of the plain-user-turn filter. A subagent turn is type=user in a transcript, so the last one becomes the label and the wake names a subagent errand instead of the work. Bound to its own assertion: the chrome deny list is untouched, so only the subagent case can go red. · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · covers:plain user turns only
- 2026-09-05 · 276cba9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · Drops the chrome deny list while leaving the sidechain filter intact, so the kit own recall injection becomes the last plain turn and labels the wake with the hook own output. Every entry on that list was observed on a real transcript rather than imagined; this binds the list as a whole to its own assertion. · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · covers:plain user turns only
- 2026-09-05 · 276cba9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · The directive block never runs, so a compacted session is handed ADR-059 state and no instruction to stop — it reads the summary, finds the branch and HEAD confirming, and carries on. Silent by construction: the note is present and correct, only the PAUSE is gone. · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · covers:the pause is printed on a compaction
- 2026-09-05 · 276cba9* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · Widens the source guard, so every startup and resume that finds a note prints the PAUSE and sends a session that never compacted re-grounding on stale work. The blast radius this task claims to hold. · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · covers:only on a compaction
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 276cba9* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · Widens the source guard so every startup and resume holding a note prints the PAUSE, sending a session that never compacted to re-ground on stale work. Re-run after the fixture was fixed to SEED the note, which is what makes the source the deciding condition; against the unseeded fixture this same mutant survived, because the empty-note test short-circuited before the guard was read. · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · covers:only on a compaction

## Verification Log
- 2026-09-05 · 276cba9 · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestACompactStartTellsTheSessionToReGround|TestAStartThatIsNotACompactIsUnchanged' -count=1` · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · ms:3049
- 2026-09-05 · 276cba9* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestACompactStartTellsTheSessionToReGround|TestAStartThatIsNotACompactIsUnchanged' -count=1` · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · ms:2274
- 2026-09-05 · 276cba9* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestACompactStartTellsTheSessionToReGround|TestAStartThatIsNotACompactIsUnchanged' -count=1` · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · ms:2260
- 2026-09-05 · 276cba9* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestACompactStartTellsTheSessionToReGround|TestAStartThatIsNotACompactIsUnchanged' -count=1` · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · ms:1483
- 2026-09-05 · 276cba9* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestACompactStartTellsTheSessionToReGround|TestAStartThatIsNotACompactIsUnchanged' -count=1` · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · ms:1586
- 2026-09-05 · 276cba9* · exit 0 · `gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestACompactStartTellsTheSessionToReGround|TestAStartThatIsNotACompactIsUnchanged' -count=1` · acceptance-sha256:77823a5f4b88ba99ab53f8f24fc0b50eec5e567c0d6ab120c0fe66a56762b6c9 · ms:3223
- 2026-09-05 · human-observed · S7: the Stop Condition is settled in the affirmative — the harness DOES send transcript_path on PreCompact. Observed on the live compaction of 2026-09-05 in this checkout (session a59e1cad, 16:12Z, monitor bz4bkmjl3 armed): the PreCompact hook read that transcript and wrote a prompt= line, and the compact start printed the PAUSE naming it. The label it produced was DEFECTIVE (a wrapped slash command), which is the whole reason the harness-chrome deny list exists — so this sign-off attests that the payload arrives and is parsed, not that the first label was right. Logged in full on T3, which was added after that compaction; recorded here because it is T1's scripts the event exercised and T1's Stop Condition it answers.
