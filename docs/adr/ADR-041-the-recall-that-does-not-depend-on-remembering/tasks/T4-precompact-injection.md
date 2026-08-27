# Task ADR-041-T4: Perform the recall at compaction and inject the result

**Depends-on:** T3
**Covers:** F-6
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** none
**Consumes:** `recall baseline` (T2), `compliance-dependence order` (T3)
**Data dependency:** needs REAL SESSIONS for the after-measurement — the fence below proves the
mechanism works, never that it moved the rate. The delta is recorded in the sign-off.

## Goal

At compaction, a recall is performed and its result enters the new context without the agent asking.

**The distinct failure this addresses (F-12):** A fresh context inherits a task queue and no palace. The motivating session began exactly there — mid-flight from a compaction, with momentum and no recall. This is the fallback ADR-017 named: a session cannot skip a recall that already happened.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` | add | The hook |
| `clients/claude-code/installer.go` | edit | Registers `PreCompact` — the event is not registered today; without this line the script is dead |
| `clients/claude-code/assets.go` | edit | Embed the script |
| `clients/claude-code/recallrate_spec_test.go` | edit | `TestF6…` turns green |

## Ordered Steps

1. Confirm the failing test(s) for `Covers:` exist and are red.
2. Write the hook: derive a query from the branch and working diff, perform the recall, emit the result.
3. Emit NOTHING when the recall returns nothing (F-6). A hook that speaks every compaction is one people stop reading, and its output is spent context.
4. Register `PreCompact` in the installer and embed the script.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestPreCompactHookIsRegistered)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
```

⚠ The fence proves the mechanism exists and is selected. The measured delta is a sign-off line:
`adr-verify --human "delta <before> -> <after>, N=<count> over <window>"`, recorded whichever way it
falls (F-10).

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF6AHookIsSilentInTheCommonCase` | `clients/claude-code/recallrate_spec_test.go` | no output when there is nothing to say | F-6 |
| `TestPreCompactHookIsRegistered` | `clients/claude-code/installer_test.go` | the installer writes the event into settings.json | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the tests over the hook |
| 2 — something selects it | `TestPreCompactHookIsRegistered`; deleting the installer line turns it red |
| 3 — the caller can discover it | n/a: no declared interface — a hook is installed, not chosen |
| 4 — it is used | the rate after this ships, against the window of T3 |

## Mutation Log

- 2026-08-28 · 5e30ae1* · mutant killed · exit 1 · `clients/claude-code/installer.go` · rung 2: the script is inert without the registration, and a hook nothing invokes is the characteristic defect · acceptance-sha256:7363ebbebae8ec6d3c369be8979e3a4bc1ed4d9167496c25d2034fa0a4f9f65f
- 2026-08-28 · 5e30ae1* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · F-6: with no query the hook must be silent, not guess — a hook that speaks every compaction gets turned off · acceptance-sha256:7363ebbebae8ec6d3c369be8979e3a4bc1ed4d9167496c25d2034fa0a4f9f65f
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 5e30ae1* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the off-switch: an operator who turns it off must get silence · acceptance-sha256:7363ebbebae8ec6d3c369be8979e3a4bc1ed4d9167496c25d2034fa0a4f9f65f
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 5e30ae1* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · F-6: with no query the hook must be silent, not guess · acceptance-sha256:e71b964ed8015a11f7cb06e10da893d578f41c8de99b25f319da6fe5a036ec4e
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 5e30ae1* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the off-switch must short-circuit before the search · acceptance-sha256:e71b964ed8015a11f7cb06e10da893d578f41c8de99b25f319da6fe5a036ec4e
  ```
  the fence passed with the mechanism broken
  ```

## Invariants

- Silence in the common case (F-6).
- The injected content is a recall RESULT, not an instruction to recall.
- Failure of the recall never blocks the compaction.

## Risks

- Spends context on every compaction to fix a minority of them; the baseline from T2 prices that.
- A query derived from the diff may be a poor query. Report the scores the recall returned, so a useless injection is visible rather than assumed helpful.

## Stop Condition

Stop if `PreCompact` does not fire, or fires without a usable payload, on the installed harness. Verify against a real compaction before building on it.

## Out of Scope

- T5 and T6
- Acting on the injected content automatically

## Verification Log
