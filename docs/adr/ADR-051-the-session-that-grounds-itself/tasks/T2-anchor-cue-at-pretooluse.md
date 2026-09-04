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
  -run 'TestAnchorFilterSelectsByPath|TestTheAnchorCueIsSilentWithoutAnAnchor|TestTheAnchorCueIsSilentWithoutAFilePath|TestTheAnchorCueEmitsAParseableEnvelope|TestTheAnchorCueRefusesAnUnfilteredAnswer|TestThePreToolUseHookIsRegistered|TestNoHookPlanIsRegisteredTwice' \
  -count=1 2>&1 | tee /tmp/adr051-t2.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t2.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAnchorFilterSelectsByPath` | `internal/palace/anchors_test.go` | `Path` narrows to anchors on that exact path, and an empty `Path` returns what it returned before — the additive guarantee | — |
| `TestTheAnchorCueIsSilentWithoutAnAnchor` | `clients/claude-code/anchorcue_test.go` | Zero bytes on stdout for a path nothing pins. Noise on the hot path is worse than silence, because it trains a reader to skip the channel | — |
| `TestTheAnchorCueEmitsAParseableEnvelope` | `clients/claude-code/anchorcue_test.go` | With an anchor present, stdout parses as JSON and carries `hookSpecificOutput.additionalContext` — a snippet with a quote or newline in it must not break the envelope | — |
| `TestTheAnchorCueIsSilentWithoutAFilePath` | `clients/claude-code/anchorcue_test.go` | Every tool that names no file. `PreToolUse` fires for tools this kit has never heard of, which is why the script filters rather than the registration | — |
| `TestTheAnchorCueRefusesAnUnfilteredAnswer` | `clients/claude-code/anchorcue_test.go` | ⚠ Earned by a live run: an MCP server that does not recognise an argument IGNORES it, and a container one commit behind returned five anchors from three repositories for a file nothing pinned. The hook confirms the path it asked about is in the answer rather than trusting that filtering happened | — |
| `TestNoHookPlanIsRegisteredTwice` | `clients/claude-code/anchorcue_test.go` | ⚠ Earned by nearly shipping one: a truncated `head -3` made an insert read as a no-op and it was repeated. A duplicate registration is not a compile error and no single test observes it — the hook simply runs twice and injects twice | — |
| `TestThePreToolUseHookIsRegistered` | `clients/claude-code/anchorcue_test.go` | The installer's plan registers the script on `PreToolUse` — the rung a script's own tests cannot see | — |

## Reachability

A hook script can be perfect and registered on nothing. The registration gate reads the
installer's plan, which is the same rung `TestDoctorIsRegistered` covers for commands.

## Mutation Log

Filled by `adr-verify --mutant`. At minimum: the `Path` clause severed (the filter returns every
anchor, so the cue fires on unrelated files), and the registration removed.
- 2026-09-04 · 0486b50* · mutant killed · exit 1 · `internal/palace/anchors.go` · the Path clause severed: the filter returns every anchor in the workspace, so the cue fires on files nothing pins and attaches other projects memories · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372
- 2026-09-04 · 0486b50* · mutant killed · exit 1 · `clients/claude-code/installer.go` · the cue registered on the wrong event: the script ships, installs, and nothing selects it when a file is opened · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372
- 2026-09-04 · 0486b50* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-anchor-cue-hook.sh` · the unfiltered-answer guard removed: an older server that ignores path= leaks other repositories anchors into the cue, which is exactly what a live run produced · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372

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
- 2026-09-04 · 0486b50* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372 · ms:46934
- 2026-09-04 · 0486b50* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372 · ms:46021
- 2026-09-04 · 0486b50* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372 · ms:37065
  ```
  --- last 10 line(s) of stdout (of 119 after folding 119 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.887s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.937s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.940s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.863s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.373s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.285s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.790s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.706s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.909s
  FAIL
  ```
- 2026-09-04 · 0486b50* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372 · ms:49764
  ```
  --- last 10 line(s) of stdout (of 46 after folding 46 raw)
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec [build failed]
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	1.157s
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry [build failed]
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/tenant [build failed]
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	1.367s
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/usage [build failed]
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/web [build failed]
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	1.232s
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle [build failed]
  FAIL
  --- last 10 line(s) of stderr (of 50 after folding 50 raw)
  github.com/stripe/stripe-go/v82: /opt/homebrew/Cellar/go/1.27.0/libexec/pkg/tool/darwin_arm64/vet: chdir ~/go/pkg/mod/github.com/stripe/stripe-go/v82@v82.5.1: no such file or directory
  # github.com/stripe/stripe-go/v82/checkout/session
  open ../../go/pkg/mod/github.com/stripe/stripe-go/v82@v82.5.1/checkout/session/client.go: no such file or directory
  # github.com/stripe/stripe-go/v82/webhook
  open ../../go/pkg/mod/github.com/stripe/stripe-go/v82@v82.5.1/webhook/client.go: no such file or directory
  # github.com/stripe/stripe-go/v82/billingportal/session
  open ../../go/pkg/mod/github.com/stripe/stripe-go/v82@v82.5.1/billingportal/session/client.go: no such file or directory
  modernc.org/sqlite/lib: /opt/homebrew/Cellar/go/1.27.0/libexec/pkg/tool/darwin_arm64/vet: chdir ~/go/pkg/mod/modernc.org/sqlite@v1.49.1/lib: no such file or directory
  # github.com/glebarez/go-sqlite
  open ../../go/pkg/mod/github.com/glebarez/go-sqlite@v1.21.2/mutex.go: no such file or directory
  ```
- 2026-09-04 · 0486b50* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372 · ms:72476
- 2026-09-04 · 0486b50* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372 · ms:36075
- 2026-09-04 · 0486b50* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372 · ms:41836
- 2026-09-04 · 0486b50* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:1922505f753bd60cab5c775bfdfc19aeda63146c85961a543bdeeeacb185b372 · ms:34294
