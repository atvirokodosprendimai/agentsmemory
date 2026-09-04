# Task ADR-051-T4: Inject on UserPromptExpansion, the channel T1 unblocks

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S (one hook script, one registration)
**Owner:** unassigned
**Produces:** none
**Consumes:** `a channel table that matches the documented four` (T1)
**Data dependency:** hermetic

## Goal

Recall at the moment a slash command expands into a task — the earliest point at which the work
has a stated subject, and one of only four events whose plain stdout reaches the model.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` | edit | branches on `hook_event_name`; the SAME script, because the two recalls differ in which text they ask with and not in machinery |
| `clients/claude-code/installer.go` | edit | registers `UserPromptExpansion` |

## Ordered Steps

1. Write the failing tests first (TDD red), starting with
   `TestTheExpansionHookIsOnAnInjectingEvent`. ⚠ Against a tree without T1 this test is red for
   the WRONG reason — the channel table, not the hook — so confirm T1 has landed before reading
   the red as evidence. That is what the `Depends-on` edge means here, and it is a real
   dependency rather than bookkeeping.
2. Write the hook, reusing the task-recall hook's query extraction and its BSD-safe `sed`.
   ⚠ Use the same `max_distance` floor the task-recall hook uses, and read this record's
   Follow-up on canary drift before trusting that number.
3. Register the event, run the fence, run the mutants, run the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestTheExpansionBranchRecallsWhereTheSubmitBranchRefuses|TestTheExpansionBranchStillRefusesAnUnexpandedCommand|TestTheUserPromptExpansionHookIsRegistered|TestEveryInjectingHookIsOnAnInjectingEvent' \
  -count=1 2>&1 | tee /tmp/adr051-t4.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t4.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheExpansionBranchRecallsWhereTheSubmitBranchRefuses` | `clients/claude-code/anchorcue_test.go` | The PAIR, which is what proves this is a gap and not a duplicate: the submit branch refuses `/am …` and the expansion branch recalls against the text it expanded into | — |
| `TestTheUserPromptExpansionHookIsRegistered` | `clients/claude-code/anchorcue_test.go` | The plan registers the task-recall script on the event, AND the event is classified as injecting — the T1 dependency asserted rather than commented | — |
| `TestTheExpansionBranchStillRefusesAnUnexpandedCommand` | `clients/claude-code/anchorcue_test.go` | ⚠ Written because a mutant SURVIVED: with no recognised expansion field the prompt is still the literal `/am`, and the refusal must still fire | — |
| `TestEveryInjectingHookIsOnAnInjectingEvent` | `clients/claude-code/hookchannel_test.go` | The pre-existing install gate now passes for this registration; before T1 it rejected it | — |

⚠ **The gap is precise and was verified before the task was written.** The
`UserPromptSubmit` hook refuses a slash command deliberately — "/am" is a command
NAME, and recalling against it retrieves whatever is nearest to one. So every
slash-command turn got no task recall at all, which is the class of turn most
likely to be substantive work.

## Reachability

This task exists to USE a channel, so its gate is the channel gate. Reverting T1 turns
`TestTheExpansionHookIsOnAnInjectingEvent` red, which is the coupling stated as a test rather
than as a comment.

## Mutation Log

⚠ **A SURVIVOR CHANGED THE CODE RATHER THAN THE TEST, WHICH IS THE POINT OF RUNNING THEM.**
The first version exempted the expansion branch from the slash-command refusal. A mutant
applying the refusal to both branches SURVIVED — and the reason is that the exemption was dead
code: on a successful expansion `PROMPT` has already been replaced by text that does not begin
with a slash, so the refusal never fires either way. On a FAILED expansion — the field name is
undocumented — `PROMPT` is still the literal `/am`, and the exemption disabled the one check
that stops a recall against a command name. The right answer was to delete the exemption, not to
write a test defending it. Filled by `adr-verify --mutant`.
- 2026-09-04 · 89fc448* · mutant killed · exit 1 · `clients/claude-code/installer.go` · the expansion recall registered on an event that reaches nothing: slash-command turns get no recall and the install gate cannot see it because Notification is classified · acceptance-sha256:ee54975426d4059ace610a349e2e2fc36d26782c7eee116be358faad2ea1092e
- 2026-09-04 · 89fc448* · mutant survived · exit 0 · `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` · the slash-command refusal applied to the expansion branch too, which refuses the very turns the branch was added to cover · acceptance-sha256:ee54975426d4059ace610a349e2e2fc36d26782c7eee116be358faad2ea1092e
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-04 · 89fc448* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-task-recall-hook.sh` · the slash-command refusal removed: an expansion whose field name is unrecognised then recalls against the command name itself · acceptance-sha256:c4c0e3932edbbacfea941c362e428b7943b671c046d1688dd4910c6d5d2d555a
- 2026-09-04 · 89fc448* · mutant killed · exit 1 · `clients/claude-code/installer.go` · the expansion recall registered on an event that reaches nothing: slash-command turns get no recall · acceptance-sha256:c4c0e3932edbbacfea941c362e428b7943b671c046d1688dd4910c6d5d2d555a

## Invariants

- Silence when there is nothing to say.
- Plain stdout, not a JSON envelope — this event injects plain text and an envelope would be
  printed literally.

## Risks

Command expansion is frequent; a recall on every expansion is a tax and a noise source.

## Stop Condition

Stop if the hook fires on expansions that carry no subject — a recall keyed on nothing is the
failure ADR-041 T5 recorded, and this task must not repeat it in a new event.

## Out of Scope

Rewriting the expanded prompt via `updatedPrompt`. (deferred: `docs/adr/BACKLOG.md`)

## Verification Log

Filled by `adr-verify`.
- 2026-09-04 · 89fc448* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:ee54975426d4059ace610a349e2e2fc36d26782c7eee116be358faad2ea1092e · ms:39491
  ```
  --- last 10 line(s) of stdout (of 129 after folding 129 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	3.848s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	3.027s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	3.208s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	3.295s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	3.396s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	2.699s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	2.098s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	2.269s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	2.602s
  FAIL
  ```
- 2026-09-04 · 89fc448* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:ee54975426d4059ace610a349e2e2fc36d26782c7eee116be358faad2ea1092e · ms:32459
- 2026-09-04 · 89fc448* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:ee54975426d4059ace610a349e2e2fc36d26782c7eee116be358faad2ea1092e · ms:32149
- 2026-09-04 · 89fc448* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:ee54975426d4059ace610a349e2e2fc36d26782c7eee116be358faad2ea1092e · ms:32254
- 2026-09-04 · 89fc448* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:c4c0e3932edbbacfea941c362e428b7943b671c046d1688dd4910c6d5d2d555a · ms:32890
- 2026-09-04 · 89fc448* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:c4c0e3932edbbacfea941c362e428b7943b671c046d1688dd4910c6d5d2d555a · ms:32977
- 2026-09-04 · 89fc448* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:c4c0e3932edbbacfea941c362e428b7943b671c046d1688dd4910c6d5d2d555a · ms:40530
