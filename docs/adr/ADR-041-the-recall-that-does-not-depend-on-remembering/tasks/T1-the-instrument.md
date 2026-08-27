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
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'
```

Red before the work: the seven named tests fail by design on this branch, and the eighth does not
exist.

⚠ FULL TEST NAMES, ANCHORED — and it took two wrong fences to get here, both recorded because
each is a named failure in this repository's own lessons.

Draft 1 used bare prefixes (`TestF1|TestF2|…`). `-run` takes a REGEX, so `TestF1` also matched
`TestF10`, `TestF12` and `TestF13` — three bindings owned by T2-T6 that are red BY DESIGN. The
fence failed T1 for work T1 was never asked to do.

Draft 2 anchored the prefixes (`^(TestF1|…)$`) and made it far worse in the opposite direction:
no test is NAMED `TestF1`, so the pattern selected **one test out of eight** and exited 0. That
is the filter-that-matches-nothing, and it is worse than the first because it PASSES. Measured:
the classifier was replaced with `return false` under that fence and the mutant SURVIVED —
a gate that could not fail, guarding the instrument built to detect gates that cannot fail.

The rule both drafts violate: a fence must name exactly the subjects that carry the verdict, and
you check that by counting what it selects. Eight names, eight `=== RUN` lines.

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

## Held-out evaluation, 2026-08-27

Run against 46 local transcripts the classifier was NOT tuned on. Numbers, and what they do and
do not support:

| | v1 | v2 |
|---|---|---|
| matches | 240 | 220 |
| precision (hand-judged sample) | 12/25 = **48%** | — |
| sentences v2 removed | — | 20, **all 20 noise, 0 genuine** |

⚠ **THE FIRST HELD-OUT SET WAS INADEQUATE AND SAID SO.** The four other transcripts in this repo
returned **zero** assertions — and they are 40-83 lines each, ~3,000 characters of assistant prose
in total. That is a true zero from an empty corpus, not a clean rate, and reporting it as one would
be the exact false all-clear F-4 exists to catch. The corpus was widened rather than the number
quoted.

⚠ **NO MEASURABLE PRECISION IMPROVEMENT, AND THE APPARENT ONE WAS SAMPLING NOISE.** A second
25-sentence sample under v2 judged 15/20 = 75%, which reads like a large gain and is not one: it is
a different random draw. v2 removes 20 of 240 matches, which bounds any real improvement to roughly
48% → 52%. The rejections were therefore judged COMPLETELY rather than sampled — all 20, every one
noise — which is what justifies the change. The precision number does not.

**What remains, and it is not fixable with another rule.** The surviving noise is assertions about
OTHER systems (a third-party API, a model name, a shell semantic) and observations of live state
(a service is not listening). Both are shape-correct and out of class, and telling them apart needs
a lexicon or a different unit, not a regex. Recorded rather than papered over.

**Consequence for T2, which changes that task:** a bare rate is not reportable. At ~50% precision
half the denominator is not the class, so the baseline must carry its precision and sample size or
it will be quoted as if it meant one thing when it means another.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the seven fixture-driven unit tests |
| 2 — something selects it | the hook script's call; `TestTheInstrumentIsCalledByTheHook` reads the EMBEDDED script, and deleting the line turns it red |
| 3 — the caller can discover it | n/a: no declared interface — the hook is the only caller and it is installed, not chosen |
| 4 — it is used | the store's own rows are the usage record; until T2 runs, nothing has read them |

## Mutation Log

- 2026-08-27 · 296d537* · mutant survived · exit 0 · `clients/claude-code/recallrate.go` · the classifier itself: with it gone every transcript reports zero assertions and a perfect rate · acceptance-sha256:d473f551c8108bb776d106367899568f2a04991dd910057d63e57a4559710fda
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-27 · 296d537* · mutant killed · exit 1 · `clients/claude-code/recallrate.go` · the classifier itself: with it gone every transcript reports zero assertions and a perfect rate · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
- 2026-08-27 · 296d537* · mutant killed · exit 1 · `clients/claude-code/recallrate.go` · the subject half: shape alone matched 154 sentences against 57, and the noise fixture proves it · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
- 2026-08-27 · 296d537* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-stop-hook.sh` · rung 2: the instrument is a function nothing runs if the Stop hook does not call it · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
- 2026-08-27 · f369f4e* · mutant survived · exit 0 · `clients/claude-code/recallrate.go` · v2 table-row rejection: 8 of the 20 real noise matches were markdown table cells · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-27 · f369f4e* · mutant killed · exit 1 · `clients/claude-code/recallrate.go` · v2 quoted-span rejection: quoting an error string is not asserting it · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
- 2026-08-27 · f369f4e* · mutant survived · exit 0 · `clients/claude-code/recallrate.go` · v2 table-row rejection: 8 of the 20 real noise matches were markdown table cells · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-27 · f369f4e* · mutant killed · exit 1 · `clients/claude-code/recallrate.go` · v2 table-row rejection: 8 of the 20 real noise matches were markdown table cells · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a

## Invariants

- Counts and identifiers only. No transcript text is written anywhere.\n- A session with no recall produces a ROW, never a silence.\n- Every row carries the classifier version.\n- The instrument never fails a session, whatever the transcript looks like.

## Risks

- The classifier is a regex over prose and will be imperfect. F-4 bounds it to KNOWN imperfect: a\n  corpus that contains assertions must produce matches. Mitigated, not solved.\n- Reusing mineclaude's `isSidechain` filter would silently exclude subagents; the fixture set\n  includes one so the omission cannot pass.

## Stop Condition

Stop if the classifier cannot distinguish a no-change assertion from an ordinary statement at a\nrate worth reporting on the fixture corpus. A rate produced by a classifier nobody trusts is worse\nthan no rate, because it will be quoted. ⚠ What would make F-4 impossible to fail? A fixture\ncorpus assembled from text the regex was written against. Draw the fixtures from real transcripts\nwritten before the regex existed.

## Out of Scope

- The baseline run (that is T2)\n- Any mechanism intended to move the rate (T3-T6)\n- Reading the store from `doctor` (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
- 2026-08-27 · 296d537* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:6b6f21ebea2f799fde85a997a4ec08a0adfed8e1a12aabeb4646a116b7ea3d45
  ```
  --- FAIL: TestF15AnObservationCarriesCountsNotContent (0.00s)
  === RUN   TestF16AnObservationCarriesItsClassifierVersion
      recallrate_spec_test.go:89: not built yet — F-16: rates from different classifier versions are never compared. Without this, tightening the regex reads as a behaviour change
  --- FAIL: TestF16AnObservationCarriesItsClassifierVersion (0.00s)
  === RUN   TestF17AMissIsRepresentable
      recallrate_spec_test.go:94: not built yet — F-17: the store records sessions where NO recall preceded an assertion. This is why search_events cannot hold it — its rows are searches, and the absence of one is the whole measurement
  --- FAIL: TestF17AMissIsRepresentable (0.00s)
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	0.012s
  FAIL
  ```
- 2026-08-27 · 296d537* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:6b6f21ebea2f799fde85a997a4ec08a0adfed8e1a12aabeb4646a116b7ea3d45
  ```
  --- FAIL: TestF10EveryResultIsRecordedEitherWay (0.00s)
  === RUN   TestF12EachMechanismNamesTheFailureItAddresses
      recallrate_spec_test.go:206: not built yet — F-12 (T3-T5): a mechanism that cannot name the distinct failure it addresses is not a candidate
  --- FAIL: TestF12EachMechanismNamesTheFailureItAddresses (0.00s)
  === RUN   TestF13MechanismsAreOrderedByComplianceDependence
      recallrate_spec_test.go:211: not built yet — F-13 (T3): the ordering is recorded BEFORE any of them ships
  --- FAIL: TestF13MechanismsAreOrderedByComplianceDependence (0.00s)
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	0.015s
  FAIL
  ```
- 2026-08-27 · 296d537* · exit 2 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:7059cf29be4717644d65938d5d22559ac390875f660028095d275d4a306cf323
  ```
  bash: -c: line 0: syntax error near unexpected token `('
  bash: -c: line 0: `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out''
  ```
- 2026-08-27 · 296d537* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:d473f551c8108bb776d106367899568f2a04991dd910057d63e57a4559710fda
- 2026-08-27 · 296d537* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
- 2026-08-27 · f369f4e* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
- 2026-08-27 · f369f4e* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./clients/claude-code/ && go test ./clients/claude-code/ -run "^(TestF1RecallRateIsCountedFromTranscripts|TestF2TheCountableUnitIsANoChangeAssertion|TestF4AClassifierThatMatchesNothingFailsLoudly|TestF5AnUnreadableTranscriptRecordsNothing|TestF15AnObservationCarriesCountsNotContent|TestF16AnObservationCarriesItsClassifierVersion|TestF17AMissIsRepresentable|TestTheInstrumentIsCalledByTheHook)$" -count=1 -v 2>&1 | tee /tmp/acc.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/acc.out'` · acceptance-sha256:40e8032187db8d66ff35a18ea02e928d8ddb30c37e5a9d7e84404bc98cc04c7a
