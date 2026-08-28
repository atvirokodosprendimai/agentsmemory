# Task ADR-003-T3: Take the four runs the truth table is read from

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** four `cells.json` run records under `evidence/`, plus the evidence note that reads the ADR's truth table against them
**Consumes:** `palace.ClosetDelta`, the `<stem>.cells.json` run record and the closet-aware `CandidateUnion` (T2)

## Goal

Run the eval four times, at case counts and on categories fixed before the run, and leave behind machine-readable records that decide the ADR without anybody's judgement.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/ADR-003-retire-the-closet-prior/evidence/mined-paraphrase.cells.json` | add | cell D1 (and M1) — the run that decides the ADR |
| `docs/adr/ADR-003-retire-the-closet-prior/evidence/mined-real.cells.json` | add | cell D2 — the veto |
| `docs/adr/ADR-003-retire-the-closet-prior/evidence/curated-paraphrase.cells.json` | add | cell R1 — recorded, sets what T5 documents |
| `docs/adr/ADR-003-retire-the-closet-prior/evidence/curated-real.cells.json` | add | cell R2 — recorded, sets what T5 documents |
| `docs/adr/ADR-003-retire-the-closet-prior/evidence/README.md` | add | the run commands verbatim, the four records read against the ADR's Table 2, and which outcome row fired |
| `cmd/server/evidence_test.go` | add | the completeness gate: four records, one commit, no dirty tree, the deciding cells present and above their floors |

## Ordered Steps

1. Re-run T1's and T2's acceptance commands and confirm both are green. A table taken through an instrument that has not passed its own tests is not evidence.
2. Build the eval binary once, from a clean tree: `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go build -o /src/bin/agentsmemory ./cmd/server'`. All four runs use that one binary, so `vcs.revision` is the same in all four records. `go run` carries no VCS stamp and the gate below rejects a record without one.
3. Name the mined wing once — the wing `mine-claude` writes into on this palace — and export it as `MINED_WING`. Every mined run uses that value; a run whose `wing` field disagrees with its sibling is not the same corpus.
4. Run the four evals. `--n` is fixed by the ADR before the run and is not adjusted afterwards:
   ```bash
   E=docs/adr/ADR-003-retire-the-closet-prior/evidence
   ./bin/agentsmemory eval --wing "$MINED_WING"      --style paraphrase --n 80 --cases "$E/mined-paraphrase.jsonl"
   ./bin/agentsmemory eval --wing "$MINED_WING"      --style real       --n 40 --cases "$E/mined-real.jsonl"
   ./bin/agentsmemory eval --wing wing_agentmemories --style paraphrase --n 40 --cases "$E/curated-paraphrase.jsonl"
   ./bin/agentsmemory eval --wing wing_agentmemories --style real       --n 40 --cases "$E/curated-real.jsonl"
   ```
   Each `--cases` path is unique, so each run keeps its own questions, its own `.results.json` and its own `.cells.json` (`resultsPath` derives both from the stem). Re-running the same path replays the same questions instead of sampling new ones.
5. Copy only the four `.cells.json` files into git. The `.jsonl` case files and `.results.json` carry queries and drawer ids from a private palace and stay untracked — the evidence README records their paths and their sha256 so a claim about them can be checked on the machine that holds them.
6. Write `evidence/README.md`: the four commands as run, the cells each record carries, and one line per row of the ADR's Table 2 saying which state fired. No number is retyped — the README quotes the cells file.
7. Write `cmd/server/evidence_test.go` and run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./cmd/server/ -run "TestClosetEvidenceIsComplete" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestClosetEvidenceIsComplete` | `cmd/server/evidence_test.go` | four cells files exist; every one carries a known commit and `dirty: false`; all four agree on the commit; D1 carries a `single` cell with at least 40 admitted cases and D2/R1/R2 carry their categories; `moved > 0` in at least one record | — |

## Invariants

- The test checks completeness and provenance, never direction. A run that says the prior helps must pass this task exactly as one that says it hurts — a gate that only accepts the answer the author wanted is not a gate.
- The four runs share one binary and one commit. A record from a different sha is a different measurement and fails the test.
- Nothing here changes code that runs in production.

## Risks

- The curated wing is small (~103 drawers), so its cells may fall below their floors. That is recorded as `n/a` by Table 2's rules and changes only what T5 documents; it must not be written up as support for the prior.
- The `real` category depends on recorded traffic and has been as small as n=4. Its floor (10 admitted cases) is in the ADR, so a small `real` run is a recorded `n/a`, not an argument.
- A palace changes between runs — memories get filed, sources re-mined. Run the four back to back, and record the drawer count each `cells.json` reports so a corpus that moved mid-measurement is visible.
- ⚠ **THIS TASK CANNOT NAME ITS OWN CORPUS ON A REAL PALACE, and two repo rules collide over it.**
  Step 3 says to name the mined wing and use it in all four runs, and `writeCells`
  (`cmd/server/eval.go`) puts `"wing": meta.Wing` into the `.cells.json` — the file step 5 commits.
  But `mine-claude` derives a wing from each session's working directory, so on a real palace the
  mined wing is named after somebody's project, and `TestNoRealProjectNamesInWings`
  (`internal/repohygiene/hygiene_test.go:297`) fails on any `wing_*` in any tracked textual file that
  is not a declared example. Verified 2026-08-28 by planting
  `{"wing":"wing_<a real project>"}` in this evidence directory: the gate went red naming the file.
  So the run completes and its evidence cannot be committed.

  This is the gate working, not a bug in it. Resolve before running, not after: either point the
  mined runs at a neutrally-named wing, or decide what the record should carry instead of the raw
  wing — noting that step 3 leans on the `wing` field to prove two mined runs share a corpus, so
  dropping it removes a real check. That is an ADR-level call and it is filed in `BACKLOG.md`.

## Stop Condition

Stop and take it back to the ADR when D1's cell fires the tie or the `lo > 0` row of Table 2, or when D2 fires its veto row. Those outcomes end the ADR with the table attached; T4 is not started.

## Out of Scope

- Changing any default — T4 does that, and only when D1 fires the `hi < 0` row.
- Growing either corpus, or building a genuinely curated palace to measure against (deferred: docs/adr/BACKLOG.md)
- Re-running until a cell reads differently (permanent: the case counts and the categories are fixed before the run; re-rolling a measurement until it agrees is how a table stops being evidence.)

## Verification Log

## Mutation Log
