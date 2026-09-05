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

## Invariants

- The two copies say the same thing; a drift between them is what this repository records as a second copy of a protocol.

## Risks

- none

## Stop Condition

Stop if `bootstrap.md` and `am.md` no longer share a Step 1c — the sentence would then have one home, not two.

## Out of Scope

- The AGENTS.md of this repository (it points at the protocol rather than restating it).

## Verification Log
