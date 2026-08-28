# Task ADR-041-T6: Replace the imperative in the handshake with the cue

**Depends-on:** T5
**Covers:** F-7, F-8, F-11, UC2-S1, UC2-S2
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** none
**Consumes:** `recall baseline` (T2)

⚠ **The `compliance-dependence order` edge to T3 was removed on 2026-08-28, when F-14 was
withdrawn.** The ordering itself is delivered and unchanged — T3's step 2 recorded it in
`tasks/README.md` BEFORE any code, which is exactly what F-13 requires and what its test reads. What
went with F-14 is T3's mechanism, and a task that can never complete cannot be a dependency: every
mechanism after it would wait forever on a decision already made. The ordering is an ADR-level
artifact in the README, not a thing this task waits for.
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

## DEFERRED by F-9, not blocked and not shipped — 2026-08-28

T6 is ready to write and must not be written yet. The reason is a rule the record fixed before any
of this was known, which is the only kind worth obeying when the conclusion is inconvenient.

**F-9: exactly one mechanism ships per measurement window.** T4 shipped; its window has not run —
its own sign-off says so: *"mechanism shipped; delta not yet measurable"*. Shipping T6 now puts two
mechanisms in flight against one baseline and neither becomes attributable. Four windows was always
going to be slow; the alternative is not faster, it is unmeasurable.

**"Ship it as tidying instead" does not escape this.** T6 edits the text every session reads at
handshake, so it moves the same number T4 is being judged on. There is no version of this change
that is neutral to T4's window.

**T6's own Stop Condition cannot be evaluated.** It says to stop *"if the three prior windows show
the rate already at ceiling"*. There are no three prior windows: T3 and T5 are blocked and produced
none, and T4's is pending. The premise is unmet, so the condition neither fires nor clears.

⚠ **T6 IS NOT VINDICATED BY BEING THE LAST ONE STANDING.** Two of the three mechanisms that asked
nothing of the agent have failed — T3 because deferral is not the server's to control, T5 because a
grep pattern is not a question. That elimination says nothing about whether prose works, and F-8
still holds: added protocol prose is not a mechanism. T6 replaces rather than adds, which is a
material difference in context cost and NOT an argument for effectiveness. When it does ship it
ships as the mechanism ranked LAST by compliance-dependence, and it is judged by the same number as
the others.

**What unblocks it:** a measured delta for T4 against the 27.6% baseline, recorded whichever way it
falls (F-10) — including "no effect", which is the outcome that would retire T4 rather than extend
it. That needs elapsed sessions with real compactions, not code.

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
