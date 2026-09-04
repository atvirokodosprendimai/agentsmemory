# Task ADR-052-T7: Run the suite under -race in CI

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** none
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`

## Goal

Turn the race detector on in CI, before the refactor rather than after, so a
pre-existing race is reported as its own finding instead of as fallout from
this ADR.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `.github/workflows/build.yml` | edit | the Go test step gains `-race`; this is the line that SELECTS the detector, and without it the flag exists nowhere |
| `docs/adr/BACKLOG.md` | edit | close the ADR-042 follow-up that asked for this, naming ADR-052 as where it landed |

## Ordered Steps

1. [S1] Run `go test -race ./...` locally first and record what it reports at this tree. This is the TDD-red equivalent: the task's value is the finding, and a clean run is a legitimate and useful result to write down. [proof: acceptance]
2. [S2] Add `-race` to the test step in `.github/workflows/build.yml`. Check whether the workflow's runner has the time budget: `-race` costs roughly two to ten times the runtime, and a job that starts timing out is a failure mode that reads as a flaky suite. [proof: human: the executor compares the job's duration before and after on the first CI run and records both numbers here]
3. [S3] If S1 found a race, do not fix it in this task. File it, name it in this record's Follow-ups, and say whether it is in code this ADR touches — a race in `internal/palace` changes T5's risk profile and the owner needs to know before wave 4. [proof: human: the executor states in the sign-off whether any race was found and where]
4. [S4] Close the ADR-042 follow-up in `docs/adr/BACKLOG.md`, naming ADR-052 so the tie is greppable. [proof: human: `grep -n 'ADR-052' docs/adr/BACKLOG.md` shows the receipt beside the closed item]

## Acceptance

```bash
set -o pipefail
go test -race ./internal/palace/... -count=1 2>&1 | tee /tmp/adr052-t7.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]|WARNING: DATA RACE" /tmp/adr052-t7.out \
  && grep -qE "^ok" /tmp/adr052-t7.out
```

Scoped to `internal/palace` because that is the package this ADR refactors and
the one whose races would matter to T5; the workflow change runs the whole tree
in CI, which the fence deliberately does not duplicate on a developer's
machine. The trailing `grep -qE "^ok"` guards against a run that compiled
nothing and printed nothing.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| — | — | no new test; this task changes what the existing suite is run WITH, and the acceptance fence is the check | — | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `-race` in the workflow's test step |
| 2 — something selects it | CI runs that step on every push; deleting `-race` is visible in the workflow diff, and no test can see it because a workflow file is not compiled |
| 3 — the caller can discover it | the closed backlog item names ADR-052, so the next reader of that follow-up finds where it went |
| 4 — it is used | every CI run; whether it ever catches anything is what S1 and S3 record |

## Mutation Log

## Invariants

- `-race` is on the test step of the workflow that actually gates merges, not only on a manually triggered one.
- No race found by S1 is fixed inside this task.

## Risks

- `-race` can push the job past its time limit, which reads as a flaky suite rather than a configuration choice. S2 requires the before-and-after durations to be recorded rather than assumed.
- A race in a dependency rather than in this repository would fail CI with a finding nobody here can fix. If that happens it is a Stop Condition, not a reason to remove the flag.

## Stop Condition

Stop and ask if `-race` reports a data race in code this ADR is about to
refactor. Knowing that before wave 4 is the entire reason this task is in wave
1, and how to sequence the fix against T5 is the owner's call.

Stop and ask if the CI job exceeds its time limit with `-race` on. The answer
may be to shard the job, which is a change to how this project builds and not
something this task may decide alone.

## Out of Scope

- Fixing any race the detector finds (deferred: `docs/adr/BACKLOG.md`)
- Adding `-race` to any local developer workflow or pre-commit hook

## Verification Log
