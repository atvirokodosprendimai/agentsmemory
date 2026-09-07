# Task ADR-009-T3: `agentsmemory tune`, and a config the server actually reads

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the `tune` subcommand and the tuned-config file's precedence
**Consumes:** `TuneResult` (T2)
**Data dependency:** hermetic for the wiring tests; a real run needs a corpus

## Goal

An operator runs one command, sees what moved and what did not, and the server reads the result on next start — below any knob they set explicitly.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/tune.go` | add | the subcommand: run both modes, apply the rule, write the file, print the record |
| `cmd/server/main.go` | edit | **the selection**: load the tuned file during config resolution, below flags and env. A file nothing reads is the defect this repo is named for |
| `cmd/server/tune_test.go` | add | precedence, staleness, and that the file is read at all |
| `internal/mcpserver/server.go` | edit | `am_status` names the tuned profile, so an agent can tell which configuration answered it |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestExplicitSettingBeatsTunedFile`, `TestTunedFileBeatsDefault`, `TestTunedFileIsReadAtStartup`. Commit them red.
2. Resolution order: explicit flag, environment, tuned file, `config.Default()`. An operator who set a knob keeps it — a tuner that overrides a deliberate choice is a bug that looks like a feature.
3. Record in the file what it was measured against: corpus size, case-set ids, commit, date. A tuned value with no provenance is a magic number with extra steps.
4. Print staleness at startup when the corpus has grown materially since the tuning run, naming the command to re-run. Do not auto-retune.
5. Surface the profile in `am_status`, so a session can say which configuration produced its results — the same reason the workspace block exists.
6. Falsify: make the tuned file win over an explicit flag; drop the startup read; remove the provenance; silence the staleness line.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true;
  set -e
  git config --global --add safe.directory /src
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestExplicitSettingBeatsTunedFile|TestTunedFileBeatsDefault|TestTunedFileIsReadAtStartup" -count=1 -v 2>&1 | tee /tmp/t3.out
  grep -q -- "--- PASS: TestExplicitSettingBeatsTunedFile" /tmp/t3.out
  grep -q -- "--- PASS: TestTunedFileBeatsDefault" /tmp/t3.out
  grep -q -- "--- PASS: TestTunedFileIsReadAtStartup" /tmp/t3.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t3.out; then exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestExplicitSettingBeatsTunedFile` | `cmd/server/tune_test.go` | a knob an operator set is never overridden by tuning | — |
| `TestTunedFileBeatsDefault` | `cmd/server/tune_test.go` | a tuned value is used when nothing explicit contradicts it | — |
| `TestTunedFileIsReadAtStartup` | `cmd/server/tune_test.go` | the file reaches the running configuration, not just the disk | — |
| `TestTunedProfileAppearsInStatus` | `internal/mcpserver/status_test.go` | a session can tell which configuration answered it | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| tuned file overrides an explicit flag | yes | `TestExplicitSettingBeatsTunedFile` |
| the file is written and never read | yes | `TestTunedFileIsReadAtStartup` |
| the profile is computed and not marshalled into `am_status` | yes | `TestTunedProfileAppearsInStatus` |

## Out of Scope

- Re-tuning automatically on a schedule or at startup (permanent: a server that changes its ranking between restarts makes every bug report unreproducible, and the run costs real inference)
- A UI for the result (deferred: docs/adr/BACKLOG.md)

## Invariants

- An explicit flag or environment variable always wins.
- A tuned value always carries what it was measured against.

## Risks

- The tuned file is copied between machines with different corpora. Mitigated: the provenance block records the corpus it was taken against, and the staleness line fires when it does not match.

## Stop Condition

Stop and ask if `am_status` cannot carry the profile without breaking an existing consumer — naming the configuration matters, but not at the cost of the wake-up call.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>

## Mutation Log
