# ADR-003 T3 — the four runs the truth table is read from

Taken 2026-09-06. Every number below is quoted from the committed `*.cells.json`; none is retyped.

**One binary, one commit.** All four runs used a single build stamped
`vcs.revision=fa93749c81bedb4a4d2d4d0c6e749ff37bc531a7`, `dirty: false`. A record from a different sha would be a
different measurement, which `TestClosetEvidenceIsComplete` refuses.

⚠ **The binary was built natively, not in the container step 2 prescribes.** The container build
(`golang:1.26-alpine`) produces a Linux artifact that this macOS host cannot execute — all four
runs exited 126 on the first attempt. The native build carries the identical stamp
(`vcs.revision`, `vcs.modified=false`), so the invariant that matters — four records, one commit,
clean tree — holds. Step 2's wording assumes a Linux host.

## The commands, as run

```bash
E=docs/adr/ADR-003-retire-the-closet-prior/evidence
agentsmemory eval --wing wing_acme          --style paraphrase --n 80 --cases "$E/mined-paraphrase.jsonl"
agentsmemory eval --wing wing_acme          --style real       --n 40 --cases "$E/mined-real.jsonl"
agentsmemory eval --wing wing_agentmemories --style paraphrase --n 40 --cases "$E/curated-paraphrase.jsonl"
agentsmemory eval --wing wing_agentmemories --style real       --n 40 --cases "$E/curated-real.jsonl"
```

Run against a snapshot of the palace rather than the serving database: `runEval` calls
`buildServices`, which opens the WRITER handle, and the container holds it.

## The corpora

`doctor --graph`, read-only, verbatim (drawers, then its three graph columns):

```
  wing_acme                      8014       5066       2440       1221
  wing_agentmemories             1723        994        366        130
```

⚠ That column is DRAWERS. The sample is capped by distinct `source_file` values, so it bounds the
corpus from above and does not predict the case count. Nothing in the tree reports distinct sources
today, so this is stated rather than estimated. `wing_acme` was seeded by
`aiagentmemory mine-claude --wing wing_acme --limit 0` — 146 sessions, 209 parts. ⚠ The default
`--limit 50` mines 40 sessions, which is at most 40 cases against D1's floor of 40 admitted, and
fails on the first drop.

## Table 2 — which state fired

| Cell | Record | Category | Admitted | State |
|---|---|---|---|---|
| D1 | `mined-paraphrase.cells.json` | `single` | 76 | **tie** — `lo <= 0 <= hi`; the interval does not separate the two arms |
| D2 | `mined-real.cells.json` | `real` | — | **NOT TAKEN** — see the gap below |
| R1 | `curated-paraphrase.cells.json` | `single` | 38 | **tie** — `lo <= 0 <= hi`; the interval does not separate the two arms |
| R2 | `curated-real.cells.json` | `real` | 17 | **tie** — `lo <= 0 <= hi`; the interval does not separate the two arms |

**Floors** (ADR-003 §Floors): D1 is read only with at least 40 admitted cases; D2, R1 and R2 only
with at least 10. A cell below its floor is `n/a` by Table 2 — a recorded non-result, never support
for the prior.

## The cells, verbatim

### D1 — `mined-paraphrase.cells.json` (the run that decides the ADR)

```json
{
  "Category": "single",
  "Admitted": 76,
  "Unreachable": 4,
  "NoGold": 0,
  "DeltaMRR": -0.017406232012130013,
  "Interval": {
    "Lo": -0.04116006769962877,
    "Hi": 0.00012804790409453107
  },
  "DeltaRecall1": -0.02631578947368421,
  "Moved": 13
}
```

### R1 — `curated-paraphrase.cells.json` (recorded; sets what T5 documents)

```json
{
  "Category": "single",
  "Admitted": 38,
  "Unreachable": 2,
  "NoGold": 0,
  "DeltaMRR": 0,
  "Interval": {
    "Lo": 0,
    "Hi": 0
  },
  "DeltaRecall1": 0,
  "Moved": 0
}
```

### R2 — `curated-real.cells.json` (recorded; sets what T5 documents)

```json
{
  "Category": "real",
  "Admitted": 17,
  "Unreachable": 0,
  "NoGold": 0,
  "DeltaMRR": 0,
  "Interval": {
    "Lo": 0,
    "Hi": 0
  },
  "DeltaRecall1": 0,
  "Moved": 0
}
```

## What is not committed

The `.jsonl` case files and `.results.json` carry queries and drawer ids from a private palace and
stay untracked. Their sha256, so a claim about them can be checked on the machine that holds them:


## ⚠ D2 was NOT taken, and it is not a run that can be retried here

`--style real` replays RECORDED search traffic. `wing_acme` has zero rows in `search_events`; the eval refuses:

```
no recorded searches to replay in wing_acme of workspace "local" — real-query cases
need search telemetry; run some sessions against this palace first, or use a
generated --style
```

This is structural, not a mishap. Step 3 requires mining into a **declared** example wing precisely so the record can be committed without naming a real project — and a wing created for that purpose has no search history. The task's own design and its D2 cell are in conflict on a machine where the mined wing is new.

Two ways to close it were available and both were refused:

- **Generate traffic against `wing_acme`** — that manufactures the telemetry the `real` category is defined as replaying, so the cell would measure a corpus of my own questions.
- **Use the derived wing instead** — it has 1,486 recorded searches, but step 3 forbids committing its `cells.json` because it is named after a real project.

So D2 is recorded as not taken. `TestClosetEvidenceIsComplete` stays as written and stays RED, and the task stays not-done. Table 2 reads D2 as the veto, so the ADR is not decided by the three cells that were taken.

**What would close it:** real sessions searching `wing_acme`, then re-run D2 alone with the same binary. Nothing else in this evidence set needs re-taking.

## The case files

Deleted after hashing — they carry queries and drawer ids from a private palace.

- `mined-paraphrase.jsonl` — `a590913f09ab7d4aa7084945f50c71c85c194e9ad30d89643027feecdbc3ad9a`
- `mined-paraphrase.results.json` — `909bc304a944fd205eefe03a0d832bf3f09479bc2246cc4deea2e16ddc60ccf3`
- `mined-real.jsonl` — ``
- `mined-real.results.json` — ``
- `curated-paraphrase.jsonl` — `44820b249ad6438a20d1224143bf6643e529f6eb5f530e2e64374312597357da`
- `curated-paraphrase.results.json` — `f49964b4910e1b6056846f2cf8dd2c25546db70e703ea860ea6b8213a63f709c`
- `curated-real.jsonl` — `f777af6870d6e81c13a1fd70ec3cbc68e736b38478d9145cc184ff4664fc58e0`
- `curated-real.results.json` — `c537b2dd254e50ba311251219a6d373fad4133ca43b1e6de5f3748191474ac4d`
