# Task ADR-062-T1: The note carries the task in flight, and a compact start pauses

**Status:** partial
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

## Verification Log
