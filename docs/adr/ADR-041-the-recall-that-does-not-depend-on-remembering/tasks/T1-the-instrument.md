# Task ADR-041-T1: Count recall-before-assertion from a session transcript

**Depends-on:** none
**Covers:** F-1, F-2, F-4, F-5, F-15, F-16, F-17, UC1-S1, UC1-S2, UC1-S3
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `recall observation record` — the append-only store's line format
**Consumes:** none
**Data dependency:** hermetic

## Goal

A session's transcript yields one observation: how many no-change assertions it contained, and how
many of those were preceded by a recall.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/recallrate.go` | add | The classifier and the observation writer |
| `clients/claude-code/recallrate_spec_test.go` | edit | The 16 red spec bindings; this task turns seven of them green |
| `clients/claude-code/testdata/transcripts/` | add | Fixture transcripts, including one subagent (`isSidechain`) transcript |
| `clients/claude-code/hooks/agentsmemory-stats.sh` | edit | **This is what SELECTS the instrument.** It already parses `transcript_path`; without a call here the classifier is a function nothing runs |
| `clients/claude-code/assets.go` | edit | The hook script is embedded; an edited script that is not re-embedded ships the old one |

What SELECTS the new code: the hook script's invocation. Deleting that one line leaves every unit
test green and the instrument dead, which is why the Tests table carries a check on the script.

## Ordered Steps

1. Confirm the failing tests for `Covers:` exist and are red — `TestF1…`, `TestF2…`, `TestF4…`,
   `TestF5…`, `TestF15…`, `TestF16…`, `TestF17…` are already committed red on this branch.
2. Write the fixture transcripts first, including one that contains a no-change assertion with **no**
   preceding recall, one with a recall before it, one unreadable, and one subagent transcript.
3. Implement the classifier over the fixtures. Do **not** reuse `mineclaude.go:84`'s `isSidechain`
   filter — inheriting it would measure only main sessions and report the rest as absent.
4. Implement the observation writer: append-only, counts and identifiers only, classifier version on
   every row, and a representable miss (`preceded: 0`).
5. Call it from `agentsmemory-stats.sh` and re-embed.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "TestF1|TestF2|TestF4|TestF5|TestF15|TestF16|TestF17|TestTheInstrumentIsCalledByTheHook" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
```

Red before the work: the seven named tests fail by design on this branch, and the eighth does not
exist.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF1RecallRateIsCountedFromTranscripts` | `clients/claude-code/recallrate_spec_test.go` | the rate comes from a transcript, not a self-report | F-1 |
| `TestF2TheCountableUnitIsANoChangeAssertion` | same | an unrecalled no-change assertion counts as one miss | F-2, UC1-S1 |
| `TestF4AClassifierThatMatchesNothingFailsLoudly` | same | a fixture corpus containing assertions must produce matches | F-4, UC1-S3 |
| `TestF5AnUnreadableTranscriptRecordsNothing` | same | no observation, and the session does not fail | F-5, UC1-S2 |
| `TestF15AnObservationCarriesCountsNotContent` | same | no transcript text in the store | F-15 |
| `TestF16AnObservationCarriesItsClassifierVersion` | same | every row is versioned | F-16 |
| `TestF17AMissIsRepresentable` | same | a session with zero recalls is a row, not an absence | F-17 |
| `TestTheInstrumentIsCalledByTheHook` | `clients/claude-code/recallrate_reach_test.go` | the embedded hook script invokes the instrument | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the seven fixture-driven unit tests |
| 2 — something selects it | the hook script's call; `TestTheInstrumentIsCalledByTheHook` reads the EMBEDDED script, and deleting the line turns it red |
| 3 — the caller can discover it | n/a: no declared interface — the hook is the only caller and it is installed, not chosen |
| 4 — it is used | the store's own rows are the usage record; until T2 runs, nothing has read them |

## Mutation Log

## Invariants

- Counts and identifiers only. No transcript text is written anywhere.\n- A session with no recall produces a ROW, never a silence.\n- Every row carries the classifier version.\n- The instrument never fails a session, whatever the transcript looks like.

## Risks

- The classifier is a regex over prose and will be imperfect. F-4 bounds it to KNOWN imperfect: a\n  corpus that contains assertions must produce matches. Mitigated, not solved.\n- Reusing mineclaude's `isSidechain` filter would silently exclude subagents; the fixture set\n  includes one so the omission cannot pass.

## Stop Condition

Stop if the classifier cannot distinguish a no-change assertion from an ordinary statement at a\nrate worth reporting on the fixture corpus. A rate produced by a classifier nobody trusts is worse\nthan no rate, because it will be quoted. ⚠ What would make F-4 impossible to fail? A fixture\ncorpus assembled from text the regex was written against. Draw the fixtures from real transcripts\nwritten before the regex existed.

## Out of Scope

- The baseline run (that is T2)\n- Any mechanism intended to move the rate (T3-T6)\n- Reading the store from `doctor` (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
