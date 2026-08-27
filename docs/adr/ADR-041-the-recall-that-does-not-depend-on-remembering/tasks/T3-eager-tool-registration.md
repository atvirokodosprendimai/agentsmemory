# Task ADR-041-T3: Register the tools so the first call needs no lookup

**Depends-on:** T2
**Covers:** F-9, F-10, F-12, F-13, F-14, UC3-S1, UC3-S2
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `compliance-dependence order` — the shipping order, recorded before the first mechanism ships
**Consumes:** `recall baseline` (T2)
**Data dependency:** needs REAL SESSIONS for the after-measurement — the fence below proves the
mechanism works, never that it moved the rate. The delta is recorded in the sign-off.

## Goal

The `am_*` tools are reachable on the first call, with no schema round-trip in front of them.

**The distinct failure this addresses (F-12):** The tool is a two-step DECISION rather than a reflex. A decision gets made only when there is already a reason to make it — which is exactly when recall is least needed and most often skipped.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/server.go` | edit | Registration; its body is pinned by `classifyToolMutationPatch` |
| `internal/mcptest/mcp_contract_axis_test.go` | edit | Re-cut the stored mutant patch this edit invalidates |
| `internal/mcpserver/recallcue_spec_test.go` | edit | `TestF14…` turns green |
| `tasks/README.md` | edit | The ordering is recorded here BEFORE this ships (F-13) |

## Ordered Steps

1. Confirm the failing test(s) for `Covers:` exist and are red.
2. Record the four-mechanism ordering in the README, before changing any code (F-13).
3. Make registration eager.
4. Re-cut `classifyToolMutationPatch` — a stored mutant is a git patch pinning surrounding context, and this edit moves it. A green `go test ./...` shipped a red CI this way on 2026-08-27.
5. Run the contract axis on a CLEAN tree; it refuses a dirty one.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./internal/mcpserver/ -run "TestF14" -count=1 -v && go test -tags contractaxis ./internal/mcptest 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
```

⚠ The fence proves the mechanism exists and is selected. The measured delta is a sign-off line:
`adr-verify --human "delta <before> -> <after>, N=<count> over <window>"`, recorded whichever way it
falls (F-10).

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF14NoSchemaLookupBeforeTheFirstCall` | `internal/mcpserver/recallcue_spec_test.go` | no lookup precedes the first call | F-14 |
| `TestMCPContractAxis` | `internal/mcptest/mcp_contract_axis_test.go` | the re-cut mutant still applies and is killed | — |

## STOPPED — the mechanism is not the server's to perform, 2026-08-28

T3's Stop Condition anticipated this: *"Stop if the harness does not honour eager registration.
Deferring the tool list is the decision of the harness, and if it defers regardless, this mechanism
is unavailable and the ordering moves on to T4."* It fired.

**Evidence, observed in one live session rather than reasoned about.** Every MCP server present is
deferred, across every size:

| server | tools | deferred? |
|---|---|---|
| a plugin connector | **2** | yes |
| codebase-memory | ~15 | yes |
| a browser connector | ~25 | yes |
| agentsmemory | 41 | yes |
| a container connector | ~200 | yes |

**A two-tool server is deferred.** That settles it: deferral is not a function of tool count, schema
size, or anything else this server publishes — 41 tools and 37,568 bytes of schema are irrelevant to
a policy applied to MCP tools as a class. There is no field in the protocol to opt out of it, and
the Affected Files table's premise — that `internal/mcpserver/server.go` could change this — was
wrong when the task was written.

**F-14 is therefore not implementable as stated** and its binding stays red. That is a spec
correction, not a task failure: the fact asserts an outcome the system cannot produce, so it needs
re-scoping or withdrawing by the owner rather than an implementation.

**What the finding is worth, because it is not nothing.** The one surface that reaches an agent
WITHOUT a lookup is the `instructions` field on the handshake — it is not deferred and cannot be.
That is T6's surface, and this result raises its standing relative to its ranking: T6 was placed
last as the most compliance-dependent, and it is now also the only mechanism guaranteed to arrive.
T4 and T5 remain fully in our control because a hook runs regardless of tool loading.

**Ordering unchanged.** T3 is recorded blocked rather than reordered — F-13 fixes the ordering
before shipping precisely so a mechanism that fails cannot be quietly promoted past the others.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF14…` |
| 2 — something selects it | registration is the selection; the contract axis drives the live surface |
| 3 — the caller can discover it | the client sees schemas at connect — that IS the change |
| 4 — it is used | the rate after this ships, against T2 |

## Mutation Log

## Invariants

- The ordering is recorded before this ships, not rearranged afterwards to fit the result (F-13).
- Exactly one mechanism ships in this window (F-9).
- The contract axis passes on a clean tree.

## Risks

- Eager registration costs context at connect for every client, including those that never recall. That cost is part of the measurement, not a footnote.
- The stored mutant patch WILL break on this edit. It is a step rather than a hope.

## Stop Condition

Stop if the harness does not honour eager registration. Deferring the tool list is the decision of the harness, and if it defers regardless, this mechanism is unavailable and the ordering moves on to T4.

## Out of Scope

- The other three mechanisms (T4, T5, T6)
- Changing which tools exist

## Verification Log
