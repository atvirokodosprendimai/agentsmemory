# Task ADR-061-T3: `/am` and the bootstrap protocol read the wake-up before planning

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** none
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `both protocol copies carry the sentence`

## Goal

Step 1c of `/am` and of the bootstrap protocol tells a session to read the `Last turn` and `checkpoint:` blocks before planning, and to ask `llm_open_threads` itself when neither is present.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/commands/am.md` | edit | one sentence in Step 1c |
| `clients/claude-code/bootstrap.md` | edit | the same sentence in Step 1c |
| `clients/claude-code/lastturn_test.go` | edit | `TestBothProtocolsReadTheWakeUp` |

## Ordered Steps

1. [S1] Write `TestBothProtocolsReadTheWakeUp` red: both embedded files contain the phrase `Last turn` within Step 1c and name `llm_open_threads`.
2. [S2] Add the sentence to both. [proof: mutation]
3. [S3] Mutant: delete it from one. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./clients/claude-code/ -run 'TestBothProtocolsReadTheWakeUp$' -count=1 2>&1 | tee /tmp/acc.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestBothProtocolsReadTheWakeUp` | `clients/claude-code/lastturn_test.go` | the sentence is in both copies, in Step 1c | — | S1, S2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the test reads the embedded assets |
| 2 — something selects it | both files are what the installer writes and the server serves at /bootstrap-memory; `assets_test.go` already gates that |
| 3 — the caller can discover it | it IS the caller-facing text |
| 4 — it is used | nothing measures this yet |

## Mutation Log

- 2026-09-05 · 7ed2c57* · mutant survived · exit 0 · `clients/claude-code/bootstrap.md` · the bootstrap copy loses the sentence while the /am copy keeps it: the two protocols drift · acceptance-sha256:f8b245d60a6b35af90567ea4ccb89f50b23b9beee7429a81ad661f61fff2af2b · covers:both protocol copies carry the sentence
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-05 · 7ed2c57* · mutant killed · exit 1 · `clients/claude-code/bootstrap.md` · the bootstrap copy stops naming the checkpoint block and the room while the /am copy keeps them: the two protocols drift · acceptance-sha256:f8b245d60a6b35af90567ea4ccb89f50b23b9beee7429a81ad661f61fff2af2b · covers:both protocol copies carry the sentence
- 2026-09-05 · 708fc34* · mutant killed · exit 1 · `clients/claude-code/bootstrap.md` · the instruction sentence is deleted from the bootstrap copy while its tokens remain in Step 1c — the mutant the token check let survive · acceptance-sha256:f8b245d60a6b35af90567ea4ccb89f50b23b9beee7429a81ad661f61fff2af2b · covers:both protocol copies carry the sentence

## Invariants

- The two copies say the same thing; a drift between them is what this repository records as a second copy of a protocol.

## Risks

- The first mutant recorded below SURVIVED and is left in. Review of #278 read it correctly: the test then checked three tokens anywhere in Step 1c, and other bullets there name the same rooms, so deleting the instruction sentence passed. The test now asserts the whitespace-normalised SENTENCE in both copies; the third mutant deletes that sentence from the bootstrap copy and is killed.

## Stop Condition

Stop if `bootstrap.md` and `am.md` no longer share a Step 1c — the sentence would then have one home, not two.

## Out of Scope

- The AGENTS.md of this repository (it points at the protocol rather than restating it).

## Verification Log
- 2026-09-05 · 7ed2c57* · exit 1 · `set -o pipefail …` · acceptance-sha256:f8b245d60a6b35af90567ea4ccb89f50b23b9beee7429a81ad661f61fff2af2b · ms:2127
  ```
  --- last 10 line(s) of stdout
  --- FAIL: TestBothProtocolsReadTheWakeUp (0.00s)
      lastturn_test.go:246: commands/am.md Step 1c does not mention "Last turn": a session is not told to read the wake-up before planning
      lastturn_test.go:246: commands/am.md Step 1c does not mention "checkpoint:": a session is not told to read the wake-up before planning
      lastturn_test.go:246: commands/am.md Step 1c does not mention "llm_open_threads": a session is not told to read the wake-up before planning
      lastturn_test.go:246: bootstrap.md Step 1c does not mention "Last turn": a session is not told to read the wake-up before planning
      lastturn_test.go:246: bootstrap.md Step 1c does not mention "checkpoint:": a session is not told to read the wake-up before planning
      lastturn_test.go:246: bootstrap.md Step 1c does not mention "llm_open_threads": a session is not told to read the wake-up before planning
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	0.325s
  FAIL
  ```
- 2026-09-05 · 7ed2c57* · exit 0 · `set -o pipefail …` · acceptance-sha256:f8b245d60a6b35af90567ea4ccb89f50b23b9beee7429a81ad661f61fff2af2b · ms:2236
- 2026-09-05 · 7ed2c57* · exit 0 · `set -o pipefail …` · acceptance-sha256:f8b245d60a6b35af90567ea4ccb89f50b23b9beee7429a81ad661f61fff2af2b · ms:2559
- 2026-09-05 · 708fc34* · exit 0 · `set -o pipefail …` · acceptance-sha256:f8b245d60a6b35af90567ea4ccb89f50b23b9beee7429a81ad661f61fff2af2b · ms:1055
