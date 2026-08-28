# ADR-041 Tasks

Implementation tasks for ADR-041: Make recall happen without depending on the agent remembering.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

⚠ **Every wave holds exactly one task, and that is the decision rather than a failure to parallelise.**
F-3 forbids any mechanism shipping before a baseline exists, and F-9 allows exactly one mechanism per
measurement window. Two mechanisms in one wave would produce one number and two candidate
explanations for it. The chain is slow by construction; shipping faster means shipping
unattributably.

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |
| 4 | T4 | T3 |
| 5 | T5 | T4 |
| 6 | T6 | T5 |

## The mechanism ordering, recorded before any of them ships (F-13)

Ordered by how little each depends on the agent choosing to comply — the ordering principle ADR-017
established. Recorded here so it cannot be rearranged afterwards to fit whichever one happened to
work.

| Order | Task | Mechanism | Depends on compliance |
|-------|------|-----------|-----------------------|
| 1 | T3 | Eager tool registration | **None** — removes a cost, asks nothing |
| 2 | T4 | `SessionStart` recall injection | **None** — the recall already happened |
| 3 | T5 | `PreToolUse` cue on a source search | Low — a prompt at the point of action |
| 4 | T6 | Cue-shaped MCP instructions | **Highest** — prose, and F-8 applies |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Count recall-before-assertion from a session transcript | done | F-1, F-2, F-4, F-5, F-15, F-16, F-17, UC1-S1, UC1-S2, UC1-S3 | `go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts\|TestF2TheCountableUnitIsANoChangeAssertion\|TestF4AClassifierThatMatchesNothingFailsLoudly\|TestF5AnUnreadableTranscriptRecordsNothing\|TestF15AnObservationCarriesCountsNotContent\|TestF16AnObservationCarriesItsClassifierVersion\|TestF17AMissIsRepresentable\|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v` |
| T2 | Record the baseline rate before anything tries to move it | done | F-3 | `go test ./clients/claude-code/ -run "^(TestF3NoMechanismShipsBeforeABaseline\|TestTheBaselineRefusesAnUndersizedSample)$" -count=1 -v` |
| T3 | Register the tools so the first call needs no lookup | blocked | F-9, F-10, F-12, F-13, F-14, UC3-S1, UC3-S2 | `go test ./internal/mcpserver/ -run "TestF14" -count=1 -v && go test -tags contractaxis ./internal/mcptest` |
| T4 | Perform the recall for a fresh context and inject the result | blocked | F-6 | `apk add --no-cache bash git >/dev/null && go test ./clients/claude-code/ -run "^(TestF6AHookIsSilentInTheCommonCase\|TestRecallHookIsRegistered\|TestEveryInjectingHookIsOnAnInjectingEvent\|TestEveryHookScriptDeclaresItsOutputChannel\|TestANonInjectedChannelIsJustified\|TestTheQueryCarriesTheBranchWorkOnACleanTree\|TestNoCredentialIsSilentButABadOneSpeaks)$" -count=1 -v` |
| T5 | Cue at the moment a source search would form the belief | blocked | F-12 | `go test ./clients/claude-code/ -run "TestPreToolUseCueFiresOncePerSubsystem\|TestPreToolUseHookIsRegistered" -count=1 -v` |
| T6 | Replace the imperative in the handshake with the cue | blocked | F-7, F-8, F-11, UC2-S1, UC2-S2 | `go test ./internal/mcpserver/ -run "TestF11\|TestInstructionsStayShort" -count=1 -v && go test -tags contractaxis ./internal/mcptest` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `recall observation record` | T2, T3, T4, T5, T6 | T1 before every other task — nothing can be measured until a row exists |
| T2 | `recall baseline` | T3, T4, T5, T6 | T2 before every mechanism; F-3 makes this a rule, not a preference |
| T3 | `compliance-dependence order` | T4, T5, T6 | Recorded in this README before T3 ships (F-13) |

## Notes

- **The branch is deliberately red.** Sixteen spec-bound tests fail by design; execution turns them
  green. Do not delete them to make a suite pass.
- **`go test ./...` is not the whole gate.** `go test -tags contractaxis ./internal/mcptest` exists,
  refuses a dirty tree, and its stored mutants are git patches that pin surrounding context. T3 and
  T6 both edit `internal/mcpserver/server.go` and must re-cut `classifyToolMutationPatch`.
- **T2 and every mechanism task need real sessions.** Their fences are hermetic and cannot see that
  requirement; the sign-off line is where the sample size and window are recorded. A green fence
  with no sign-off means the code works and nothing has been measured.
- **No migration.** The observation store is a local file. `search_events` was rejected because its
  rows are searches, and a miss is not representable in a table of hits (F-17).
