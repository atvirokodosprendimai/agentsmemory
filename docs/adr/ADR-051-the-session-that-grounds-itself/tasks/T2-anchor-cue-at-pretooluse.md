# Task ADR-051-T2: Cue the memory that pins THIS file, by path, at PreToolUse

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (one filter field, one hook script, one registration)
**Owner:** unassigned
**Produces:** `path-keyed anchor lookup`
**Consumes:** none
**Data dependency:** hermetic for the fence; the per-call budget in the Stop Condition needs a real palace

⚠ **READ ADR-041 T5's STOP NOTE BEFORE REVIEWING THIS TASK.** T5 reached the same event and is
stopped on a measured, disqualifying finding: at `PreToolUse` the only query available is a bare
grep pattern, and 0 of 25 such patterns reached canary-grade relevance (median distance 0.486
against a canary band of 0.317–0.444). **That finding stands and this task does not dispute it.**
This task is a different mechanism: it issues NO QUERY. A code anchor is an exact pin carrying
`Repo`, `Path`, `Snippet`, `Status` and its `DrawerID`, so the lookup is a join on the path the
tool call already names. There is no distance to fall short of, because nothing is ranked.

## Goal

When a tool is about to read or edit a file that a memory pins, put that memory in front of the
model — without the agent choosing to search, and without a query.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/anchors.go` | edit | `AnchorFilter` gains `Path`; additive, zero value preserves every call site |
| `clients/claude-code/hooks/agentsmemory-anchor-cue-hook.sh` | add | the cue; `# hook-output: structured` |
| `clients/claude-code/installer.go` | edit | registers `PreToolUse` |
| `clients/claude-code/assets.go` | edit | embeds the new script |

## Ordered Steps

1. Write the failing tests first (TDD red). Run the fence and confirm RED.
2. Add `Path` to `AnchorFilter` and its `WHERE` clause. Exact match on the stored path.
3. Write the hook: read the event JSON, extract `tool_input.file_path`, and emit a
   `hookSpecificOutput.additionalContext` envelope naming the pinned memory and its status.
   Copy `esc()` from the SubagentStart hook verbatim — hand-assembled JSON is how an envelope
   becomes unparseable and is then dropped in silence.
4. **Emit nothing when no anchor pins that path.** Silence is the common case and must cost
   nothing.
5. Register `PreToolUse` in the installer, matcher scoped to `Read|Edit|Write`.
6. Run the fence, then the mutants, then the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ ./internal/palace/ \
  -run 'TestAnchorFilterSelectsByPath|TestTheAnchorCueIsSilentWithoutAnAnchor|TestTheAnchorCueEmitsAParseableEnvelope|TestThePreToolUseHookIsRegistered' \
  -count=1 2>&1 | tee /tmp/adr051-t2.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t2.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAnchorFilterSelectsByPath` | `internal/palace/anchors_test.go` | `Path` narrows to anchors on that exact path, and an empty `Path` returns what it returned before — the additive guarantee | — |
| `TestTheAnchorCueIsSilentWithoutAnAnchor` | `clients/claude-code/anchorcue_test.go` | Zero bytes on stdout for a path nothing pins. Noise on the hot path is worse than silence, because it trains a reader to skip the channel | — |
| `TestTheAnchorCueEmitsAParseableEnvelope` | `clients/claude-code/anchorcue_test.go` | With an anchor present, stdout parses as JSON and carries `hookSpecificOutput.additionalContext` — a snippet with a quote or newline in it must not break the envelope | — |
| `TestThePreToolUseHookIsRegistered` | `clients/claude-code/installer_test.go` | The installer's plan registers the script on `PreToolUse` — the rung a script's own tests cannot see | — |

## Reachability

A hook script can be perfect and registered on nothing. The registration gate reads the
installer's plan, which is the same rung `TestDoctorIsRegistered` covers for commands.

## Mutation Log

Filled by `adr-verify --mutant`. At minimum: the `Path` clause severed (the filter returns every
anchor, so the cue fires on unrelated files), and the registration removed.

## Invariants

- No query is issued. If this task ever needs a search, it has become ADR-041 T5 and must stop.
- Silence when nothing is pinned.
- The hook never blocks: it exits 0 whatever happens, because a cue is not worth failing a tool call.

## Risks

`PreToolUse` runs on the hot path of every tool call. A slow lookup taxes every Read in the
session.

## Stop Condition

Stop if the lookup cannot stay under a measured per-call budget on a real palace, or if the cue
fires often enough to become noise. Measure the fire rate on a real session BEFORE shipping —
that is the discipline T5's stop note established, and it is the reason T5's frequency arm
passed while its relevance arm failed.

## Out of Scope

- Blocking or rewriting the tool call.
- Any semantic search at `PreToolUse`. (permanent: that is ADR-041 T5 and it is stopped)

## Verification Log

Filled by `adr-verify`.
