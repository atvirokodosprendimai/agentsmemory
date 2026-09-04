# Task ADR-051-T1: Correct the hook channel table it currently forbids a working event

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (two map entries, one comment, one gate)
**Owner:** unassigned
**Produces:** `a channel table that matches the documented four`
**Consumes:** none
**Data dependency:** hermetic

## Goal

Make `injectingEvents` name the four events the documentation names, so `doctor` stops
reporting a working hook as dead and the install gate stops refusing a channel that exists.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hookchannel.go` | edit | `UserPromptExpansion` moves to `injectingEvents`; `PreModelSwitch` joins `debugLogEvents`; the false ⚠ paragraph is replaced |
| `clients/claude-code/hookchannelknown_test.go` | edit | the new gate, and the RETIREMENT of `TestUserPromptExpansionIsNotTreatedAsInjecting`, which pinned the false claim |

## Ordered Steps

1. Write the failing test first (TDD red): `TestTheInjectingSetIsTheDocumentedFour` asserts
   membership of all four names and asserts `UserPromptExpansion` is NOT in `debugLogEvents`.
   Run the fence and confirm RED.
2. Move `UserPromptExpansion` from `debugLogEvents` to `injectingEvents`.
3. ⚠ **Do NOT add `PreModelSwitch` — it is already in `debugLogEvents`.** This step originally
   said to add it, on the belief that it was in neither map. That was inferred from a count
   rather than looked up, and adding it fails the build on a duplicate map key. The step is kept
   with its correction because the error is an instance of exactly what T1 fixes: a figure
   derived from the table, trusted over the table.
4. Replace the ⚠ paragraph that claims the docs moved `UserPromptExpansion` to the debug-log
   side. It is false; leaving it corrected-in-place rather than deleted is the point, because
   a file that quietly loses the claim it got wrong teaches nothing.
5. Re-run the fence and confirm GREEN, then the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestTheInjectingSetIsTheDocumentedFour|TestPostModelSwitchInjects|TestTheTwoChannelSetsAreDisjointAndComplete|TestEveryInjectingHookIsOnAnInjectingEvent|TestEveryHookScriptDeclaresItsOutputChannel' \
  -count=1 2>&1 | tee /tmp/adr051-t1.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t1.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheInjectingSetIsTheDocumentedFour` | `clients/claude-code/hookchannelknown_test.go` | All four documented names are in `injectingEvents`, and `UserPromptExpansion` is absent from `debugLogEvents` — the exact pair of assertions that is false today | — |
| `TestTheTwoChannelSetsAreDisjointAndComplete` | `clients/claude-code/hookchannelknown_test.go` | The pre-existing disjointness gate still holds after the move — it is what would have caught the duplicate `PreModelSwitch` key had `go vet` not caught it first | — |
| `TestPostModelSwitchInjects` | `clients/claude-code/hookchannelknown_test.go` | Replaces `TestUserPromptExpansionIsNotTreatedAsInjecting`, keeping the half of it that was true | — |

## Reachability

⚠ **THE WRONG BELIEF WAS GATED, WHICH IS WHY IT SURVIVED.**
`TestUserPromptExpansionIsNotTreatedAsInjecting` asserted
`hookEventChannel("UserPromptExpansion") != channelInjected`, citing the reference. The table
said three, that test held the table to three, and the suite was green over a channel the kit
was refusing to use. A test does not merely record a claim, it DEFENDS it — and defending the
wrong one turns every future correction into a failing build that reads as a regression. It is
replaced by `TestPostModelSwitchInjects`, which keeps the half that was true and asserts nothing
about what `UserPromptExpansion` is not; that membership belongs to the set-level gate.

The table is data read by two callers — `doctor` and the install-plan gate — so a wrong entry
is not inert: it produces a confident verdict. The gate asserts membership rather than
behaviour because behaviour here belongs to Claude Code, not to us.

⚠ **No test fetches the documentation.** A gate that makes a network call fails when the
network does and turns an upstream edit into a red build on an unrelated branch. The retrieval
date in the comment is the honesty mechanism; `doctor` is where an operator learns it is old.

## Mutation Log

Filled by `adr-verify --mutant`.
- 2026-09-04 · 11bd58d* · mutant killed · exit 1 · `clients/claude-code/hookchannel.go` · UserPromptExpansion dropped from the injecting set again — the state that made doctor call a working hook DISCARDED and made the install gate refuse the channel · acceptance-sha256:cd4d1e1dbc2e6ade313ebfcf3cc4de087a5df152a5a7477a03455665675dc16b
- 2026-09-04 · 11bd58d* · mutant killed · exit 1 · `clients/claude-code/hookchannel.go` · PreModelSwitch unclassified: a documented non-injecting event answers channelUnknown instead of channelDebugLog · acceptance-sha256:cd4d1e1dbc2e6ade313ebfcf3cc4de087a5df152a5a7477a03455665675dc16b
- 2026-09-04 · 11bd58d* · mutant killed · exit 1 · `clients/claude-code/hookchannel.go` · UserPromptExpansion dropped from the injecting set again — the state that made doctor call a working hook DISCARDED and made the install gate refuse the channel · acceptance-sha256:df344a4a53d563dec16f432a5ee7d19b156d0e0faf5e5c2316a3471a69833cc4
- 2026-09-04 · 11bd58d* · mutant killed · exit 1 · `clients/claude-code/hookchannelknown_test.go` · the gate itself reverted to the three-event claim, so it would pass over the very table it exists to correct · acceptance-sha256:df344a4a53d563dec16f432a5ee7d19b156d0e0faf5e5c2316a3471a69833cc4
- 2026-09-04 · 11bd58d* · mutant killed · exit 1 · `clients/claude-code/hookchannel.go` · UserPromptExpansion dropped from the injecting set again — doctor then calls a working hook DISCARDED and the install gate refuses the channel · acceptance-sha256:84f6688679905c09009925582ec758f7a1f0308de88878196b88912675bccb19
- 2026-09-04 · 11bd58d* · mutant killed · exit 1 · `clients/claude-code/hookchannelknown_test.go` · the gate itself reverted to the three-event claim, so it would pass over the table it exists to correct · acceptance-sha256:84f6688679905c09009925582ec758f7a1f0308de88878196b88912675bccb19

## Invariants

- No event appears in both sets.
- An event in neither set answers `channelUnknown`, never a definite "does not inject".
- The comment records the retrieval date of the claim.

## Risks

The documentation moves again. That is the recurrence this file already records twice; the
mitigation is the dated comment and `doctor`, not a cleverer data structure.

## Stop Condition

Stop and raise it if the documented set turns out to be version-dependent — that would make a
single table wrong for some installs, and the fix would be a per-version table, which is a
decision rather than an edit.

## Out of Scope

Registering anything on `UserPromptExpansion` — that is T4.

## Verification Log

Filled by `adr-verify`.
- 2026-09-04 · 11bd58d* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:cd4d1e1dbc2e6ade313ebfcf3cc4de087a5df152a5a7477a03455665675dc16b · ms:47998
  ```
  --- last 10 line(s) of stdout (of 129 after folding 129 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	5.838s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	4.605s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	2.210s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	2.843s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	2.511s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	2.680s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	3.011s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	2.732s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	2.957s
  FAIL
  ```
- 2026-09-04 · 11bd58d* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:cd4d1e1dbc2e6ade313ebfcf3cc4de087a5df152a5a7477a03455665675dc16b · ms:40400
  ```
  --- last 10 line(s) of stdout (of 129 after folding 129 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.796s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.969s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	1.282s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.932s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.672s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.830s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	1.557s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	1.466s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	1.753s
  FAIL
  ```
- 2026-09-04 · 11bd58d* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:cd4d1e1dbc2e6ade313ebfcf3cc4de087a5df152a5a7477a03455665675dc16b · ms:39431
  ```
  --- last 10 line(s) of stdout (of 129 after folding 129 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.737s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.878s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.950s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.625s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	0.985s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	1.261s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.797s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.732s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.918s
  FAIL
  ```
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:cd4d1e1dbc2e6ade313ebfcf3cc4de087a5df152a5a7477a03455665675dc16b · ms:41482
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:cd4d1e1dbc2e6ade313ebfcf3cc4de087a5df152a5a7477a03455665675dc16b · ms:38560
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:cd4d1e1dbc2e6ade313ebfcf3cc4de087a5df152a5a7477a03455665675dc16b · ms:50901
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:df344a4a53d563dec16f432a5ee7d19b156d0e0faf5e5c2316a3471a69833cc4 · ms:47603
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:df344a4a53d563dec16f432a5ee7d19b156d0e0faf5e5c2316a3471a69833cc4 · ms:40637
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:df344a4a53d563dec16f432a5ee7d19b156d0e0faf5e5c2316a3471a69833cc4 · ms:40688
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:84f6688679905c09009925582ec758f7a1f0308de88878196b88912675bccb19 · ms:59124
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:84f6688679905c09009925582ec758f7a1f0308de88878196b88912675bccb19 · ms:49345
- 2026-09-04 · 11bd58d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:84f6688679905c09009925582ec758f7a1f0308de88878196b88912675bccb19 · ms:49508
