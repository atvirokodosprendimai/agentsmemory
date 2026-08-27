# Task ADR-041-T6: Replace the imperative in the handshake with the cue

**Depends-on:** T5
**Covers:** F-7, F-8, F-11, UC2-S1, UC2-S2
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** none
**Consumes:** `recall baseline` (T2), `compliance-dependence order` (T3)
**Data dependency:** needs REAL SESSIONS for the after-measurement — the fence below proves the
mechanism works, never that it moved the rate. The delta is recorded in the sign-off.

## Goal

The MCP instructions name the CLASS OF CLAIM that requires a recall, and carry no bare imperative.

**The distinct failure this addresses (F-12):** The agent has no NAME for the class of claim that needs a recall. Ranked last deliberately: ADR-017 measured added protocol prose as the least promising intervention, and F-8 forbids counting prose as a mechanism. It ships only after three measured windows say whether the others worked.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/server.go` | edit | `serverInstructions`; pinned by `classifyToolMutationPatch` |
| `internal/mcptest/mcp_contract_axis_test.go` | edit | Re-cut the patch if this edit moves it |
| `internal/mcpserver/recallcue_spec_test.go` | edit | `TestF11…` turns green |

## Ordered Steps

1. Confirm the failing test(s) for `Covers:` exist and are red.
2. REPLACE, do not add. The ceiling is 1200 chars and short is measured-better (F-7).
3. The text names the sentence shape and what source cannot show; it does not say "recall first".
4. Re-run the contract axis on a clean tree.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./internal/mcpserver/ -run "TestF11|TestInstructionsStayShort" -count=1 -v && go test -tags contractaxis ./internal/mcptest 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
```

⚠ The fence proves the mechanism exists and is selected. The measured delta is a sign-off line:
`adr-verify --human "delta <before> -> <after>, N=<count> over <window>"`, recorded whichever way it
falls (F-10).

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF11InstructionsNameTheClassOfClaimNotTheDuty` | `internal/mcpserver/recallcue_spec_test.go` | the class is named; no bare imperative | F-11, UC2-S1 |
| `TestInstructionsStayShort` | `internal/mcpserver/instructions_test.go` | the ceiling holds (already passing) | F-7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF11…` |
| 2 — something selects it | `TestHandshakeCarriesInstructions` drives a real initialize |
| 3 — the caller can discover it | the handshake IS the discovery surface (ADR-021) |
| 4 — it is used | the rate after this ships, against the window of T5 |

## Mutation Log

## Invariants

- Replaces rather than adds; the ceiling holds (F-7).
- Names no wing: the field is construction-time and one process serves many workspaces.

## Risks

- This is the intervention most likely to be believed without evidence, because it reads well. F-8 exists to stop exactly that, and this task is ordered last for the same reason.

## Stop Condition

Stop if the three prior windows show the rate already at ceiling — then this ships as tidying, not as a mechanism, and says so.

## Out of Scope

- Any further protocol prose anywhere (permanent boundary of this record)

## Verification Log
