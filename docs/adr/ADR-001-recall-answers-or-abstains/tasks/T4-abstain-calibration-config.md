# Task ADR-001-T4: Load the calibration file and validate its fingerprint before any verdict is served

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `config.AbstainCalibration`, `palace.Service.WithCalibration`, the startup profile check and the canary probe
**Consumes:** `palace.Calibration` (T2), the `ship` decision (T3)

## Goal

Let an operator point the server at a calibration file, and make the server prove — before it emits a single verdict — that the palace it is serving is the one the thresholds were measured on.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/config/config.go` | edit | one new key, `AbstainCalibration` (a path); unset is valid and means "no verdict" |
| `cmd/server/main.go` | edit | flag + env (`ABSTAIN_CALIBRATION`), load the file, compare the ranking profile against the running configuration, run the canary probe, wire `WithCalibration` |
| `internal/palace/service.go` | edit | store the calibration; hold whether the canary is confirmed; re-probe at most once more on the first search that produces a reranked top hit |
| `cmd/server/abstain_test.go` | add | pin that a profile mismatch refuses startup, a canary mismatch does not, and that unset is accepted |
| `.env.example`, `.env.docker.example` | edit | document the key, that there is deliberately no default, and that a stale calibration yields `unknown` rather than a guess |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestAbstainProfileMismatchRefusesStartup` asserting a calibration whose recorded ranking profile disagrees with the running configuration is rejected with an error naming the field, both values and the fix; `TestAbstainCanaryMismatchYieldsUnknown` asserting a canary that scores differently — or a reranker that does not answer — leaves the server running with verdicts `unknown` and a warning naming both score sets; `TestAbstainUnsetIsValid` asserting no calibration configures cleanly. Commit red.
2. Add `AbstainCalibration string` to `config.Config`, empty in `Default()` — there is no defensible default file, as there is no defensible default threshold.
3. In `cmd/server/main.go`, load the file when the key is set and compare its recorded profile — fusion mode, BM25 weight and auto flag, closet scale, rerank pool, rerank weight — field by field against the running configuration. A mismatch is a **startup error**: the operator set both sides, they contradict each other, and a threshold measured under another ranking profile judges a document that is no longer the one recall returns.
4. Run the canary probe once at startup: re-score the fingerprint's pairs and compare each against its recorded mean, using the tolerance T2 measured from the repeats. A mismatch, or a reranker that does not answer, is **not** a startup error — the palace still serves recall, and a memory server that refuses to boot because a cross-encoder moved is a worse failure than one that stops claiming confidence. Log which pair diverged, by how much, and against what tolerance; leave the calibration unconfirmed so every verdict is `unknown`.
5. Re-probe at most once more, on the first search that produces a reranked top hit, so a reranker that finishes loading after the server does resolves itself without a restart. After that second attempt the process's answer is fixed — no background loop, no per-query probe.
6. Add `WithCalibration` beside `WithRerankWeight`, following the same post-construction-setter contract (call before the service is shared across goroutines); put the doc comment on its own declaration, which the doclint gate enforces.
7. Document the key in both env examples: what it points at, that `eval --calibrate` writes it, and that an uncalibrated or mismatched palace answers `unknown` rather than guessing.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'set -e; gofmt -l internal/palace internal/config cmd/server | grep -q . && exit 1; go vet ./...; go test ./cmd/server/ ./internal/palace/ ./internal/config/ -run "TestAbstain" -count=1 2>&1 | tee /tmp/adr001-t4.out; if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr001-t4.out; then exit 1; fi'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAbstainProfileMismatchRefusesStartup` | `cmd/server/abstain_test.go` | a calibration measured under another ranking profile cannot be served silently | — |
| `TestAbstainCanaryMismatchYieldsUnknown` | `cmd/server/abstain_test.go` | a changed or absent reranker costs the verdict, never the palace | — |
| `TestAbstainUnsetIsValid` | `cmd/server/abstain_test.go` | absence of calibration is a supported state, not an error | — |

## Invariants

- An unset calibration changes nothing about today's behaviour.
- No threshold value exists anywhere in the tree; every number comes from the file.
- No verdict other than `unknown` is served while the canary is unconfirmed.

## Risks

- The profile comparison is only as good as the fields recorded. A knob added later that changes which document reaches top-1, and is not added to the fingerprint, reopens exactly the hole this task closes. Mitigation: the comparison is field-by-field over a struct the ranking knobs live in, so a new knob is a compile-time visible omission rather than a silent one, and T2's writer and this reader share that struct.
- The canary costs one rerank call at boot. Mitigation: it is the same shape of preflight the eval already runs before scoring hundreds of cases, and it is skipped entirely when no calibration is configured.

## Stop Condition

Stop if the running configuration cannot report the ranking profile without a network call or a database read — the comparison must be a local, deterministic check of the operator's own configuration, and anything else is a different design.

## Out of Scope

- Deriving the verdict, including the per-request `limit` check against the calibrated rerank pool — that is T5's job, because the parameter arrives with the query and no startup check can see it.
- Surfacing it or recording it — that is T6's job.
- A profile identity carried on every production search event, as opposed to on the calibration file (deferred: docs/adr/BACKLOG.md)

## Verification Log

## Mutation Log
