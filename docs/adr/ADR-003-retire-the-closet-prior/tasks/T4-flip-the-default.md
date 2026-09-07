# Task ADR-003-T4: Flip the closet prior's default to off, end to end

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `palace.DefaultClosetBoost` = 0, `config.Default().ClosetBoost` = 0, `palace.Service.ClosetBoostScale()` as the composition root's read-back, and the composition root applying the configured scale unconditionally
**Consumes:** the four `cells.json` records (T3)

## Goal

Ship the default the measurement justifies: the closet prior contributes nothing to ranking unless an operator asks for it, and `CLOSET_BOOST=1` restores today's behaviour exactly.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | add exported `DefaultClosetBoost = 0` beside `DefaultRerankPool`/`DefaultRerankWeight`, start `NewService` from it (`service.go:202`), and add `ClosetBoostScale()` so the composition root's wiring can be read back without exporting the field |
| `internal/config/config.go` | edit | `Default().ClosetBoost` 1 → 0 (`config.go:289`), duplicated from `palace.DefaultClosetBoost` the way `RerankPool` already is; rewrite the zero-value NOTE (`config.go:189-194`), which inverts once 0 is the default |
| `cmd/server/main.go` | edit | apply `WithClosetBoost(cfg.ClosetBoost)` unconditionally instead of only when it differs from 1 (`main.go:812`), log only when the prior is ON, and rewrite the flag's usage text (`main.go:183`) to state the new default and what turns it back on |
| `internal/palace/rank_test.go` | edit | `TestSearchAppliesClosetBoost` must ask for the boost with `WithClosetBoost(1)` — under the new default it is opt-in, and a test that assumes it would be pinning the old default |
| `cmd/server/config_test.go` | edit | pin that the two defaults agree and that `--closet-boost 1` restores the prior, in the package that imports both |
| `cmd/server/closetwiring_test.go` | add | the composition-root test: `CLOSET_BOOST=1` in the environment reaches the palace service that `buildServices` returns |
| `cmd/server/evidence_test.go` | edit | add the gate that binds this task to T3's records: D1's interval lies entirely below zero |

## Ordered Steps

1. Write the failing tests first (TDD red) and commit them red: `TestClosetPriorDefaultsOff`, `TestClosetPriorRestoresWhenSet`, `TestClosetBoostReachesTheServiceFromTheEnvironment`, `TestClosetFlipIsBackedByEvidence`.
2. Add `DefaultClosetBoost = 0` to `internal/palace/service.go` with the reason in the comment (the measured regression on mined corpora, and the one command that reverses it), and start `NewService` from it.
3. Add `func (s *Service) ClosetBoostScale() float64`. It exists for one reason and the comment says so: `buildServices` is the only place that turns an operator's `CLOSET_BOOST` into a served scale, and without a read-back that wiring can only be checked by reading the source — the defect class `wiring_test.go` was written for.
4. Set `config.Default().ClosetBoost` to 0 with the `palace.DefaultClosetBoost; duplicated to keep config dependency-free` marker, and replace the zero-value NOTE with what is now true: 0 is the default and 1 is the opt-in.
5. In `cmd/server/main.go`, drop the `!= 1` guard around `WithClosetBoost` and log only when `cfg.ClosetBoost > 0`. Under the new default the old guard would print a scaled-to-0.00 line on every boot, which reads as a misconfiguration rather than as the default.
6. Update the `--closet-boost` flag usage: default off, `CLOSET_BOOST=1` restores the full curation prior, and it is worth turning on only when the eval's closet block wins on your palace.
7. Fix `TestSearchAppliesClosetBoost` to opt in, and run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'set -e; go vet ./...; go test ./cmd/server/ ./internal/config/ ./internal/palace/ -run "TestClosetPriorDefaultsOff|TestClosetPriorRestoresWhenSet|TestClosetBoostReachesTheServiceFromTheEnvironment|TestClosetFlipIsBackedByEvidence|TestSearchAppliesClosetBoost|TestRankHybridClosetBoostLifts" -count=1 2>&1 | tee /tmp/adr003-t4.out; if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr003-t4.out; then exit 1; fi'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestClosetPriorDefaultsOff` | `cmd/server/config_test.go` | the resolved config and `palace.DefaultClosetBoost` are both 0 — the two copies cannot drift apart | — |
| `TestClosetPriorRestoresWhenSet` | `cmd/server/config_test.go` | `--closet-boost 1` resolves to the full prior, so the rollback path is executable | — |
| `TestClosetBoostReachesTheServiceFromTheEnvironment` | `cmd/server/closetwiring_test.go` | with `CLOSET_BOOST=1` in the environment and a temp `--db`, the service `buildServices` returns reports scale 1; with the variable unset it reports 0 | — |
| `TestClosetFlipIsBackedByEvidence` | `cmd/server/evidence_test.go` | D1 — `mined-paraphrase.cells.json` carries a `single` cell with at least 40 admitted cases and an interval whose upper bound is below zero; D2 — `mined-real.cells.json`'s `real` cell, at or above its floor of 10, does not lie entirely above zero | — |
| `TestSearchAppliesClosetBoost` | `internal/palace/rank_test.go` | the mechanism still works when asked for — the second link of the chain, field → `Search` order | — |
| `TestRankHybridClosetBoostLifts` | `internal/palace/rank_test.go` | the ranking formula is untouched: it takes boosts as an argument and never reads the default | — |

## Invariants

- `CLOSET_BOOST=1` produces the same ranking as the previous release: same constants, same cap, same fade.
- The wiring is proved as a chain of two links, each with its own test: the environment variable reaches `palace.Service`'s scale (`TestClosetBoostReachesTheServiceFromTheEnvironment`), and that scale reaches the order `Search` returns (`TestSearchAppliesClosetBoost`, opted in). Neither test alone proves an operator can turn the prior back on.
- No persistent state changes. Closets are still mined and stored while the prior is off, so turning it on needs no re-mine.
- The two defaults are one number in two files, pinned by a test rather than by a comment.

## Risks

- `NewService` starting at 0 changes behaviour for anyone embedding the package without going through `cmd/server`. That is deliberate — the library default should be the measured one — but it is the part of this task the flag help does not reach, so it belongs in the changelog entry as well.
- The composition-root test builds a real service against a temp SQLite database. It must not need a network: `config.Default().VectorBackend` is `sqlite` and the embedder is constructed without connecting, so `buildServices` opens, migrates and wires offline. If that stops being true, the test fails loudly rather than being deleted.
- A test elsewhere may quietly depend on the boost being on. The acceptance command names the closet tests explicitly; a full `go test ./...` before committing is cheap insurance.

## Stop Condition

Stop if `TestClosetFlipIsBackedByEvidence` cannot pass — the records are missing, D1's interval is not entirely below zero, or D2 fired its veto. This task exists to ship a measured decision; without the measurement it is a preference.

## Out of Scope

- Documentation prose — T5 owns it, and it must land in the same release as this task.
- Removing or retuning the mechanism (permanent: only the default moves; the ADR's Out of Scope explains why the code stays.)

## Verification Log

## Mutation Log
