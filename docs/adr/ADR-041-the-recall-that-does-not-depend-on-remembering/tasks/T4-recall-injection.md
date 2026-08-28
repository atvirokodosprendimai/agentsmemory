# Task ADR-041-T4: Perform the recall for a fresh context and inject the result

**Depends-on:** T3
**Covers:** F-6
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** none
**Consumes:** `recall baseline` (T2), `compliance-dependence order` (T3)
**Data dependency:** needs REAL SESSIONS for the after-measurement — the fence below proves the
mechanism works, never that it moved the rate. The delta is recorded in the sign-off.

## Goal

When a context starts fresh — most often just after a compaction — a recall is performed and its result enters that context without the agent asking.

**The distinct failure this addresses (F-12):** A fresh context inherits a task queue and no palace. The motivating session began exactly there — mid-flight from a compaction, with momentum and no recall. This is the fallback ADR-017 named: a session cannot skip a recall that already happened.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-recall-hook.sh` | add | The hook |
| `clients/claude-code/installer.go` | edit | Registers the hook on `SessionStart` — without this line the script is dead, and on a non-injecting event it is dead while looking alive |
| `clients/claude-code/assets.go` | edit | Embed the script |
| `clients/claude-code/recallrate_spec_test.go` | edit | `TestF6…` turns green |
| `clients/claude-code/hookchannel_test.go` | add | The gate: an injecting hook on a non-injecting event |

## Ordered Steps

1. Confirm the failing test(s) for `Covers:` exist and are red.
2. Write the hook: derive a query from the branch and working diff, perform the recall, emit the result.
3. Emit NOTHING when the recall returns nothing (F-6). A hook that speaks every compaction is one people stop reading, and its output is spent context.
4. Register the hook on `SessionStart` in the installer and embed the script.
5. Gate the event choice: a script that prints for the model must be registered on an event whose stdout Claude Code injects.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified|TestTheQueryCarriesTheBranchWorkOnACleanTree|TestNoCredentialIsSilentButABadOneSpeaks|TestEveryHookScriptIsEmbedded)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
```

⚠ THE FENCE INSTALLS bash AND git, and that is not incidental. The acceptance image is
golang:1.26-alpine, which has neither. The test drives the real hook SCRIPT, so without bash it
called t.Skip — and a skipped test is a test that cannot fail: two mutants came back `survived`
while the same edits made the test go red locally. The mutation pass is what exposed it; a green
acceptance run never would have.

⚠ The fence proves the mechanism exists and is selected. The measured delta is a sign-off line:
`adr-verify --human "delta <before> -> <after>, N=<count> over <window>"`, recorded whichever way it
falls (F-10).

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF6AHookIsSilentInTheCommonCase` | `clients/claude-code/recall_test.go` | no output when there is nothing to say | F-6 |
| `TestRecallHookIsRegistered` | `clients/claude-code/recall_test.go` | the installer registers it, on an event that injects | — |
| `TestEveryInjectingHookIsOnAnInjectingEvent` | `clients/claude-code/hookchannel_test.go` | no hook prints for the model on an event that discards stdout | — |
| `TestEveryHookScriptDeclaresItsOutputChannel` | `clients/claude-code/hookchannel_test.go` | the gate's universe is the hooks directory, so a new script cannot be invisible | — |
| `TestANonInjectedChannelIsJustified` | `clients/claude-code/hookchannel_test.go` | a quieter channel carries a written reason | — |
| `TestTheQueryCarriesTheBranchWorkOnACleanTree` | `clients/claude-code/recall_test.go` | the query names committed branch work, not just the branch | — |
| `TestNoCredentialIsSilentButABadOneSpeaks` | `clients/claude-code/recall_test.go` | an unconfigured credential is silent; every other failure speaks | F-6 |
| `TestEveryHookScriptIsEmbedded` | `clients/claude-code/hookchannel_test.go` | a script in the tree but not in the //go:embed list ships as nothing | — |

## The mechanism was registered on an event that discards its output — 2026-08-28

**This task was recorded `done` while it could not work.** The hook was registered on
`PreCompact`. Claude Code adds a hook's plain stdout to the model's context for exactly three
events — `SessionStart`, `UserPromptSubmit`, `UserPromptExpansion` — and writes every other
event's stdout to the debug log. So the hook performed the recall at every compaction, printed
it, and threw it away.

**Every test passed, and two mutants were killed against it.** They drove the SCRIPT and asserted
what it wrote. None asked whether anything could read it. A killed mutant proves a test notices a
change to the thing it drives; it says nothing about whether that thing is reachable. This is the
repository's characteristic defect — a capability finished, tested and unselected — in the one
shape no unit test can see, because the selection is a string in a settings file.

**The query could not work either, and for an independent reason.** It was built from
`git diff --name-only HEAD` — uncommitted changes only, empty on the clean tree a session sits on
after a commit. The query collapsed to the bare branch name. Measured 2026-08-28 against the live
palace, three real branches: bare branch names return nearest hits at 0.450, 0.507 and 0.509
against a 0.42 floor — silent on all three. The same branches with a merge-base file list return
2-3 hits each at 0.391-0.414, and each returns DIFFERENT drawers, so the composite query
discriminates rather than ranking whatever is most popular.

Both were found by asking where the output goes, not by running the tests again.

The query defect SURVIVED its first mutant: reverting to the uncommitted-only diff broke nothing,
because every assertion read the hook's stdout and the stub returns a hit whatever it is asked. The
query is an argument, so a test has to record it. `TestTheQueryCarriesTheBranchWorkOnACleanTree`
builds a temp repository, commits work on a branch so the tree is CLEAN, and asserts the recorded
query names that work — with the mutant applied it reports the shipped query verbatim,
`mcp search fix/a-distinctive-branch`, with no file in it.

**The fix is `SessionStart`, and the timing argument is independent of the channel.** Output
injected *before* a compaction is part of the context being compacted — it would be summarised
away in the same pass that discarded the palace. The fresh context is where the recall is needed,
and `SessionStart`'s `compact` matcher is where the fresh context begins. It is registered without
a matcher, so it also fires on `startup`, `resume` and `clear`: broader than the named failure,
and deliberately so, because all four begin a context holding no palace, and a session that runs
for days compacts many times and starts once.

**Structured output does not rescue `PreCompact`.** `agentsmemory-subagent-start-hook.sh` reaches
a subagent through `hookSpecificOutput.additionalContext` on `SubagentStart`, which is not an
stdout-injecting event — so a second route exists. It was not taken here: the docs do not state
which events honour `additionalContext`, and the timing argument rules `PreCompact` out regardless.

**F-9 is not violated.** One mechanism per measurement window: T4-as-built never ran as a
mechanism, because nothing it produced could reach a model. No window is contaminated, and this is
the first window in which T4 exists at all.

**A THIRD reason it could not work, and the one that still stands.** The hook passed
`--token "${AGENTSMEMORY_LOCAL_TOKEN:-${AGENTSMEMORY_TOKEN:-local}}"` — always a token, defaulting
to the placeholder `local`. `--token` OVERRIDES the CLI's own resolution, so an install whose token
lives in `agentsmemory.env` authenticated as "local" and was refused. Fixed: the flag is passed only
when the environment supplies one.

That fix does not make the mechanism reachable everywhere, and the record must not imply it does.

**⚠ CORRECTION, same day.** This section first said the hook was inert on a Claude HOSTED install
because "the token lives in the MCP registration's `Authorization` header, which the CLI does not
read". **That is false.** `tokenFromClaudeJSON` reads exactly that header; it is wired at
`clients/claude-code/mcpcall.go:222` and its doc comment says so. I asserted that something DOES
NOT happen without checking — the precise failure shape this ADR exists to measure — and shipped it
into the record, the backlog, PR #84 and the palace before catching it.

**The real gap is narrower, and it is the `--local` install.** `aiagentmemory mcp` resolves a token
from `--token`, `$AGENTSMEMORY_TOKEN`, an `agentsmemory.env` file, or the `.claude.json`
registration header. `--local` populates none of them — `--help` says "no token is prompted for",
and `registerClaudeMCP` adds the header only when a token is non-empty — so the CLI refuses with
"no workspace token found" against a server that accepts no credentials at all. A client-side gate
with nothing behind it. Filed in `BACKLOG.md` with three options.

**Verified 2026-08-28 end to end**, from the INSTALLED path rather than the tree: with
`AGENTSMEMORY_TOKEN` present in `agentsmemory.env`, the hook speaks on a working branch
(`query: <branch> graph.go`, one hit from room `decisions`); on the default branch with a clean tree
it is correctly silent, because the query is the bare branch name and falls under the 8-character
floor; with no credential it is silent rather than noisy.

**A SEPARATE DEFECT FOUND THE SAME WAY.** `install --agent claude` without `--mcp-url` or `--local`
silently REPOINTED all five existing hooks from `http://localhost:8080/mcp` to the hosted default,
because the default wins over what is already configured. Every hook on that machine went mute, and
nothing said so. An install that rewrites a working configuration to a default is destructive, and
it is not this task's to fix — recorded here because it is how the confusion above started.

**So T4 does not read `done` on the strength of a green fence.** The sign-off must carry the
measured delta (F-10) *and* name the population the mechanism cannot reach — a gate that passes
while the hook is mute for most installs is the same defect one layer out, which this task has now
produced three times.

**The gate.** `TestEveryInjectingHookIsOnAnInjectingEvent` derives its universe from the hooks
directory and fails when a script declaring `# hook-output: stdout-injected` is registered on an
event that discards stdout. Flipping the registration back to `PreCompact` turns it red, which is
the whole point of it. `TestEveryHookScriptDeclaresItsOutputChannel` keeps a new script from being
invisible to it; `TestANonInjectedChannelIsJustified` refuses a quieter declaration without a
written reason, so the declaration cannot become the dodge.

## A shipped defect, found by accident — 2026-08-28

The first version of this hook wrote `HITS="$(aiagentmemory mcp search ... 2>/dev/null || true)"`
and then `[ -n "$HITS" ] || exit 0`. That makes **every failure identical to a clean empty recall**.

`aiagentmemory mcp` demands a workspace token, and a `--local` install has none — it is the
configuration this repository develops on. So on that install the hook could **never** have spoken,
and it would have looked exactly like the deliberate silence F-6 asks for. Every gate here passed:
the acceptance fence, three killed mutants, the registration check. None of them could see it,
because they all asked whether the hook was quiet and it was quiet.

**It was found by coincidence.** T5's fire-rate measurement used the same call to ask the palace
about 25 symbols and reported a tidy `0 of 25`. Checking that zero against a canary — a query whose
answer is certainly present — returned the same error 25 swallowed. The measurement was invalid and
the hook was broken, one cause.

**Fixed:** stderr and the exit code are captured and branched on, never emptiness. A recall that
could not RUN prints one line saying so. That is not "reporting all good" — it is reporting a fault,
which is the one thing a deliberately-quiet hook should still speak about. A token is passed from
`AGENTSMEMORY_LOCAL_TOKEN`/`AGENTSMEMORY_TOKEN` with a placeholder fallback: a `--local` server
ignores it, a hosted one rejects it loudly, and both outcomes are correct.

**Left for the owner:** `aiagentmemory mcp` refusing to run without a token against a `--local`
server is a client-side gate with nothing behind it — the server accepts any value. Fixing it there
would remove the placeholder. Out of scope here; recorded rather than worked around silently.

## A second shipped defect: it was injecting this session's own transcript — 2026-08-28

Found while measuring T5, by asking the question T5 forced: not "does the recall return something"
but "is what it returns relevant". Applied to T4's own query it fails the same way.

T4 builds its query from the branch name and changed filenames. Measured against the live palace:

| | top-hit distance | what came back |
|---|---|---|
| T4's real mid-work query, unscoped | 0.460 – 0.516 | **this session's own transcript chunks** |
| the same query, `room=decisions` | 0.501 – 0.560 | real decisions, all in the weak band |
| the same query, floor at 0.42 | — | **nothing; the hook stays silent** |

Injecting the session's own recent transcript into a freshly compacted context is worse than
useless: it restores exactly what compaction removed. The hook was shipped, marked done, and had
three killed mutants — none of which could see this, because they all asked whether it *spoke*, not
whether what it said was worth reading.

**Fixed:** `room=decisions` and `max_distance=0.42`. The floor is
a trade, not a boundary — the two classes overlap around 0.41–0.44 — set to exclude all 25 measured
bare-identifier queries while keeping question-grade hits. On a weak query the hook now returns
nothing and stays quiet, which is F-6 working.

> ⚠ **Corrected 2026-08-28, same day, PR #95 — `room=decisions` was NOT measured, and this record
> said it was.** Only the FLOOR was measured against the 25 bare-identifier queries; the room was
> chosen by argument (scope away the transcript chunks) and never compared against the alternatives.
> The row above recording `room=decisions` at floor 0.42 returning *"nothing; the hook stays silent"*
> is labelled **F-6 working** here and it is not: it is the hook being mute, and it stayed mute on
> every branch whose work was filed anywhere but `decisions`. The shipped room is now `diary`, on the
> argument that a branch+files query asks "what was done here and why" and `diary` is where the
> persist step writes that. See the hook's own comment for the evidence and for why the hit-COUNT
> version of that argument does not survive a growing corpus.

**The mutant needed the stub to assert the invocation.** These flags are arguments, not branches, so
no assertion on the hook's output can see them: a stub that ignores its arguments returns a hit
either way and the mutant survives. The stub now refuses to answer unless both are passed.

**Consequence to expect, and it is honest rather than disappointing:** T4 will now be silent far more
often. Its measured delta may be near zero. That is the outcome F-10 exists to record, and it is
better than a mechanism that appears to work by injecting noise.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the tests over the hook |
| 2 — something selects it | `TestRecallHookIsRegistered`; deleting the installer line turns it red |
| 2b — what selects it can RECEIVE it | `TestEveryInjectingHookIsOnAnInjectingEvent`; registering it on `PreCompact` turns it red. This rung is the one the first version passed rung 2 without: the line existed, and the event it named threw the output away |
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
- 2026-08-28 · 502f172 · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · F-6: with no query the hook must be silent, not guess a query · acceptance-sha256:e71b964ed8015a11f7cb06e10da893d578f41c8de99b25f319da6fe5a036ec4e
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 502f172* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the off-switch must short-circuit before the search · acceptance-sha256:e71b964ed8015a11f7cb06e10da893d578f41c8de99b25f319da6fe5a036ec4e
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 5a91bcc* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · F-6: with no query the hook must be silent, not guess a query · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · 5a91bcc* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the off-switch must short-circuit before the search · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 0a7005d · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the off-switch must short-circuit before the search · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · 36b67e4* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the failure diagnostic: without it a broken recall is indistinguishable from an empty one, which is how this defect hid · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · 019c963* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the relevance floor: without it the hook injects the nearest neighbour, which measured as this session own transcript · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · 0159db0 · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-precompact-hook.sh` · the relevance floor: without it the hook injects the nearest neighbour, measured as this session own transcript · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · f4c9324 · mutant killed · exit 1 · `clients/claude-code/installer.go` · the event IS the mechanism: on PreCompact the recall runs and its output goes to the debug log · acceptance-sha256:ecb7966c9934d73b7f50a2e67b482e85f83b531ae12612d376f8d492c557f00e
- 2026-08-28 · 4be3ccf · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the query on a clean tree: an uncommitted-only diff is empty, so it collapses to the bare branch name and falls below the relevance floor · acceptance-sha256:748b84cd7a42e0bd7ed80331a32d5d7b6ce2ece9902d0dafea534b07dec1baa2
- 2026-08-28 · 7683966 · mutant killed · exit 1 · `clients/claude-code/installer.go` · the event IS the mechanism: on PreCompact the recall runs and its output goes to the debug log · acceptance-sha256:c69c7aef536d0e00a5b2ccae16eb244e95c32bbc42e13d2633cfa8d6392b07c7
- 2026-08-28 · 53cac4c · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the token override: always passing one defeats the CLI resolution an install actually uses · acceptance-sha256:c69c7aef536d0e00a5b2ccae16eb244e95c32bbc42e13d2633cfa8d6392b07c7
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · cf5bfb5 · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-recall-hook.sh` · the token override: always passing one defeats the CLI resolution an install actually uses · acceptance-sha256:c69c7aef536d0e00a5b2ccae16eb244e95c32bbc42e13d2633cfa8d6392b07c7
- 2026-08-28 · 4f5ee39 · mutant killed · exit 1 · `clients/claude-code/installer.go` · the event IS the mechanism: on PreCompact the recall runs and its output goes to the debug log · acceptance-sha256:b6a56654d75cb5b4ee59cbacb279403b50345116acdbd168eedb832fcb0a09bb

## Invariants

- Silence in the common case (F-6).
- The injected content is a recall RESULT, not an instruction to recall.
- Failure of the recall never blocks the compaction.

## Risks

- Spends context on every compaction to fix a minority of them; the baseline from T2 prices that.
- A query derived from the diff may be a poor query. Report the scores the recall returned, so a useless injection is visible rather than assumed helpful.

## Stop Condition

Stop if `SessionStart` does not fire on the `compact` matcher, or fires without injecting its stdout, on the installed harness. Verify against a real compaction before building on it.

## Out of Scope

- T5 and T6
- Acting on the injected content automatically

## Verification Log
- 2026-08-28 · 5a91bcc · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · 5a91bcc* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · 0a7005d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · human-observed · mechanism shipped; delta not yet measurable — needs a measurement window of real compactions against the 27.6% baseline (F-10)
- 2026-08-28 · 36b67e4 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · 019c963 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · 0159db0* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:1ca9f7ca6761a677b0e7390dc80ac05c8471a18eeab4ce7fbf66f584663846fc
- 2026-08-28 · 741cfe5 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:ecb7966c9934d73b7f50a2e67b482e85f83b531ae12612d376f8d492c557f00e
- 2026-08-28 · 23c67f6 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified|TestTheQueryCarriesTheBranchWorkOnACleanTree)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:748b84cd7a42e0bd7ed80331a32d5d7b6ce2ece9902d0dafea534b07dec1baa2
- 2026-08-28 · 5090fe7 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified|TestTheQueryCarriesTheBranchWorkOnACleanTree)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:748b84cd7a42e0bd7ed80331a32d5d7b6ce2ece9902d0dafea534b07dec1baa2
- 2026-08-28 · bf92800 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified|TestTheQueryCarriesTheBranchWorkOnACleanTree|TestNoCredentialIsSilentButABadOneSpeaks)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:c69c7aef536d0e00a5b2ccae16eb244e95c32bbc42e13d2633cfa8d6392b07c7
- 2026-08-28 · 09977e9 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase|TestRecallHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel|TestANonInjectedChannelIsJustified|TestTheQueryCarriesTheBranchWorkOnACleanTree|TestNoCredentialIsSilentButABadOneSpeaks|TestEveryHookScriptIsEmbedded)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:b6a56654d75cb5b4ee59cbacb279403b50345116acdbd168eedb832fcb0a09bb
