# Task ADR-041-T5: Cue at the moment a source search would form the belief

**Depends-on:** T4
**Covers:** F-12
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** none
**Consumes:** `recall baseline` (T2), `compliance-dependence order` (T3)
**Data dependency:** needs REAL SESSIONS for the after-measurement — the fence below proves the
mechanism works, never that it moved the rate. The delta is recorded in the sign-off.

## Goal

A source search whose subject is behaviour meets a cue before the belief is written down.

**The distinct failure this addresses (F-12):** The moment the belief FORMS, which is neither session start nor compaction. The motivating session ran `grep` for a function, read its preamble, and concluded from it — a moment no session-scoped mechanism can reach.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-pretooluse-hook.sh` | add | The hook |
| `clients/claude-code/installer.go` | edit | Registers `PreToolUse` — not registered today |
| `clients/claude-code/assets.go` | edit | Embed the script |

## Ordered Steps

1. Confirm the failing test(s) for `Covers:` exist and are red.
2. Write the matcher: a source search whose pattern looks like a symbol or behaviour question.
3. Fire at most ONCE per subsystem per session. A cue on every grep is noise, and noise is how a mechanism gets disabled — the reasoning of F-6, applied to a louder event.
4. Register and embed.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./clients/claude-code/ -run "TestPreToolUseCueFiresOncePerSubsystem|TestPreToolUseHookIsRegistered" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
```

⚠ The fence proves the mechanism exists and is selected. The measured delta is a sign-off line:
`adr-verify --human "delta <before> -> <after>, N=<count> over <window>"`, recorded whichever way it
falls (F-10).

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestPreToolUseCueFiresOncePerSubsystem` | `clients/claude-code/pretooluse_test.go` | a second grep in the same subsystem is silent | — |
| `TestPreToolUseHookIsRegistered` | `clients/claude-code/installer_test.go` | the installer writes the event | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the unit tests over the matcher |
| 2 — something selects it | `TestPreToolUseHookIsRegistered` |
| 3 — the caller can discover it | n/a: no declared interface |
| 4 — it is used | the rate after this ships, against the window of T4 |

## Mutation Log

## Invariants

- At most one cue per subsystem per session.
- The cue never blocks the tool call.

## Risks

- The highest-frequency event in this record, and therefore the likeliest to be turned off. The once-per-subsystem bound is the whole mitigation and must be tested, not asserted.

## Stop Condition

Stop if `PreToolUse` fires often enough that the once-per-subsystem bound still produces a cue on most turns. Measure the fire rate on a real session BEFORE shipping it.

## Out of Scope

- T6
- Blocking or rewriting the tool call

## Verification Log
