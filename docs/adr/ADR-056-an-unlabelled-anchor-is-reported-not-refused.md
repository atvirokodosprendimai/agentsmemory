# ADR-056: An anchor filed without a repository label is reported, not refused

**Status:** Proposed
**Date:** 2026-09-05
**Owner:** unassigned
**Spec:** None — no spec stage. The requirement is one sentence; the measurement that motivates it is in Context.
**Cross-references:** `docs/adr/ADR-053-bound-the-graph-read.md` (T3 — the fan-out precedent: a write that outgrows its tier answers with `fan_out` and is never refused), `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md` (`doctor --corpus`, the three-state reference report this record extends), `docs/adr/ADR-051-the-session-that-grounds-itself.md` (the verify hook that reads anchors at session start), `docs/adr/BACKLOG.md` (the 2026-08-29 entry "An anchor with an EMPTY repo label is checked against whatever tree is open", whose read side closed on 2026-09-04 and whose write side this record decides), `internal/mcpserver/drawers.go`, `internal/palace/anchors.go`, `cmd/server/doctorcorpus.go`, `clients/claude-code/verify.go`
**Governs:** None — declared by its tasks
**Enforced-by:** None — no gate exists at authoring time. T1 produces `internal/mcptest/anchorlabel_test.go::TestAnUnlabelledAnchorIsReportedAtWrite`, which fails when either write tool stops reporting an anchor it accepted without a label; T2 produces `cmd/server/doctorcorpus_test.go::TestDoctorCorpusReportsUnlabelledAnchors`, which fails when the report stops naming them or starts failing on them.
**Invalidates:** none — checked. ADR-053 T3's rule (report, never refuse, for a write whose shape is wrong) is what this record extends to anchors; ADR-038's `doctor --corpus` gains a fourth population and keeps its three; ADR-051's verify hook reads the same `unattributable` bucket it reads today. No accepted record consumes a refusal at anchor write time.
**Served-path change:** `am_add_drawer` and `am_update_drawer` accept an anchor without `repo` exactly as today, and their responses gain `anchors_unlabelled` (how many were accepted without a label) and `anchors_advice` (the one call that labels them) when the count is non-zero; `doctor --corpus` prints how many anchors no tree can ever attribute, with a sample of ids, beside ADR-038's populations and WITHOUT moving the exit code.

## Context

An anchor pins a memory to a file and a snippet, and `repo` is the only field that says WHICH tree
the file is in. Anchors are workspace-wide and verification runs in one checkout, so an anchor
without a label can never be attributed: the read side (`clients/claude-code/verify.go`,
`verifyAnchors`, fixed 2026-09-04) sorts it into `unattributable` rather than checking it, because
the alternative — checking it against whatever tree is open — produced the destructive verdict
recorded in `BACKLOG.md` on 2026-08-29: the same file, two opposite verdicts, differing only by the
label, and a session-start hook then telling an unrelated session to re-file correct memories.

With the read side closed, the write side is what remains, and its failure mode is now silence:
an unlabelled anchor is filed, never checked, and reports nothing forever. Measured 2026-09-04
against the local palace: 189 anchors, 7 unlabelled, every one pinning this repository's own files,
their verdicts frozen from before the read-side fix — and one of them read `verified` against code
that had moved away entirely. Nothing in the write path said anything at the moment those seven
were filed, and nothing in the corpus would ever have surfaced them.

The server cannot supply the label. `internal/mcpserver/drawers.go` builds `palace.AnchorInput`
from the request (`parseAnchorList`), and nothing there knows the caller's git remote. The tool
descriptions already say to ALWAYS send `repo` and state the consequence of omitting it; the seven
were filed anyway. So the decision is what the write path does when the field is missing.

## Existing Primitives Audit

- **`fan_out` / `fan_out_advice` on `am_kg_add`** (ADR-053 T3, `internal/mcpserver/kg.go`): a
  write that is accepted and reported back with what is wrong about its shape and the one call that
  fixes it. **Reused as the shape:** the same two-field, absent-when-clean pattern on the two anchor
  writes.
- **`parseAnchorList`** (`internal/mcpserver/drawers.go`): already the one place both tools read
  anchors, and already knows `repo` per entry. **Reshaped:** it also reports how many entries
  carried no label.
- **`doctor --corpus`** (`cmd/server/doctorcorpus.go`, ADR-038): walks references and reports three
  states, exit non-zero on a finding, plus `EndedFactSources` — a population it counts and prints at
  every run, including zero, and deliberately keeps OUT of `clean()` because it is the system
  working. **Reshaped:** gains a population in that second shape — anchors with an empty `repo`,
  printed with a sample of ids and the remedy, never a verdict.
- **`Service.ListAnchors` with `AnchorFilter.Repo`** (`internal/palace/anchors.go`): filters by
  label but cannot select the EMPTY label (an empty filter means "any"). **Reused** by the corpus
  walk through a direct query on the anchors table, where `doctor --corpus` already reads rows.
- **Rejected as a primitive:** deriving the label from the registration's default wing. The wing
  naming rule (`wing_<repo>`) is a convention with known exceptions (`wing_craft` is
  the shipped example: a wing every project reads that is no repository), and a wrong label is a positive claim
  that routes verification to the wrong tree, where an empty one is an unknown that is reported.

## Decision

**An anchor without a repository label is accepted, stored unchecked, and reported back to the
caller in the same response, with the one call that labels it; and `doctor --corpus` reports how
many such anchors the palace holds, as a population beside ADR-038's and not as a verdict.** No
write is refused for a missing label, and no label is invented. The criterion for T1 is falsifiable
in a hermetic scenario today: file a memory with `code_anchors: [{path, snippet}]` and the response
carries nothing about it — that is the red state. T2's criterion is falsifiable the same way: seed
one unlabelled anchor and `doctor --corpus` says nothing about it today.

The argument that settles it is about correctness, not convenience. The one label the server could
supply is a guess, and a guessed label is a positive claim: the read side attributes the anchor to
that tree and checks it there, and when the guess is wrong the verdict is a false `verified` — the
kind that says nothing forever, which the 2026-08-29 entry and its 2026-09-04 sequel both record
as the destructive one. An empty label is at least reported as unknown. So the server does not
invent one, and what remains is refuse or report.

That choice turns on what an anchor is worth relative to the memory it pins. `parseAnchors`'s own
comment states the rule this record follows: *the memory itself is worth more than its anchor*, so
an unreadable entry is skipped rather than failing the write. A label is less than an entry.
Refusing would turn a working write into a failure over its least important part, for every client
and every prompt that has not been updated, and would reach none of the anchors already filed.
Reporting reaches the caller at the one moment it can act (the label is in its working tree, not
the server's), and the corpus report reaches everything filed before this record.

**The population does not own the exit code**, and the review of the first draft is why. That code
is shared with ADR-038's three lost-reference populations, and every state they name is one no
legal write produces. An unlabelled anchor is a legal write by this very record, and T1 keeps it
legal — so a `clean()` term for it would go red on a permitted condition and stay red until every
caller labels its anchors, which is exactly the shape this corpus already names as worse than no
check: a permanently-failing gate stops being read, and it takes the three real findings down with
it. `EndedFactSources` is the precedent in the same function: counted, printed at every run
including zero, and kept out of the verdict. What drains the population is labelling — the advice
T1 puts in the caller's response, and the kit-side labelling this record defers — so the
record's theory of change depends on that work, and says so here rather than in a threshold.
The decision is valid for the MCP write surface; the CLI has no anchor-writing verb.

## Alternatives Considered

- **Refuse the write when any anchor lacks `repo`.** Rejected: it punishes the memory for its
  anchor, breaks every caller that omits the field (the seven existing anchors show such callers
  exist), cannot reach anchors already filed, and reverses the precedent ADR-053 T3 set for the
  analogous case — a write whose shape is wrong is filed and told so.
- **Default `repo` from the registration's default wing (`wing_<repo>` minus the prefix).**
  Rejected: the mapping is a convention with named exceptions, and a defaulted label that is wrong
  becomes a positive claim the read side attributes and checks in the wrong tree — the exact
  failure the 2026-08-29 entry records. An empty label is at least honest about being unknown.
- **Refuse only when the registration has a default wing, report otherwise.** Rejected: two
  behaviours for one field, chosen by a fact the caller cannot see, is the kind of conditional
  contract this corpus keeps paying for.
- **Do nothing; the description already says to send `repo`.** Rejected: it said so when the seven
  were filed. A sentence in a description is read before the call; a field in the response is read
  after it, by the caller who can fix it.
- **Count unlabelled anchors in `doctor --corpus`'s `clean()`, so the exit code carries them** (the
  first draft of this record, `2344964`). Rejected on review: T1 keeps the inflow legal, so the
  command can sit red indefinitely on a permitted state, and the exit code it would own is ADR-038's
  too. A bounded variant — non-zero above a threshold, or only for anchors filed after this record —
  was considered and rejected as a number nobody chose standing in for a decision.

## Component / Boundary Impact

`internal/mcpserver` (response shape of two write tools), `internal/palace` (no behaviour change;
the count is taken where anchors are parsed), `cmd/server` (`doctor --corpus`). Ownership unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_add_drawer` response | gains `anchors_unlabelled` (int) and `anchors_advice` (string), both absent when every anchor carried `repo` | `internal/mcpserver` | agents; the tool description names both keys (`TestEveryOmitemptyWireKeyInThisPackageIsDescribed`) |
| `am_update_drawer` response | the same two keys, for a `code_anchors` replacement | `internal/mcpserver` | agents |
| `doctor --corpus` | a fourth population: anchors with an empty `repo`, printed at every run with a sample of ids and the remedy; the exit code is unchanged | `cmd/server` | operators reading the report (`scripts/redeploy.sh` does not run `--corpus` today, measured 2026-09-05) |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `unlabelledAnchors` count from `parseAnchorList` | T1 | — | No — additive |
| `corpusFindings.UnlabelledAnchors` | T2 | — | No — `clean()` is unchanged |

The tasks are independent; T2 does not consume T1.

## Implementation

See `tasks/README.md`. Two tasks: the write-side report, and the corpus check.

## Consequences

- **Positive:** a caller learns at write time that an anchor it just filed can never be verified,
  and gets the one call that fixes it; an operator learns from `doctor --corpus` how many such
  anchors the palace already holds. No write that works today stops working.
- **Negative:** two more omitempty keys on the busiest write tool; a caller that ignores the
  report is no better off than today, which is the cost of not refusing; and the corpus population
  moves no exit code, so an operator has to read the line, which is the cost of not going red on a
  legal state.
- **Neutral:** an unlabelled anchor is stored exactly as today (`status: unchecked`, `repo: ""`);
  the read side's `unattributable` bucket is unchanged.

## Out of Scope

- Refusing the write (permanent: boundary: this record chooses report over refuse, for the reasons
  in Alternatives; a later record may reverse it once every shipped client sends the label)
- Deriving the label server-side from the wing name (permanent: boundary: a convention with named
  exceptions is not a source of truth for a field the read side attributes on)
- Anchors carried forward onto a correction by `carryAnchors` (`internal/palace/supersede.go`):
  they inherit the old record's labels and add no new unlabelled anchor, and T2 finds them if the
  old ones were unlabelled (permanent: boundary: a correction preserves what it was given)
- Anchors written by the importer or by `am_mine` (permanent: fact: neither path builds an
  `AnchorInput` — the class was enumerated with `grep -rn 'AnchorInput{' --include='*.go' internal
  cmd clients | grep -v _test` on 2026-09-05 and returned the two sites the tasks name; citation:
  file `internal/mcpserver/drawers.go:395`)
- Having the Claude Code kit inject the session's repository label into the tool description or
  the call, so the caller never has to know it (deferred: `docs/adr/BACKLOG.md`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Callers ignore the report and unlabelled anchors keep accumulating | Med | Low | T2 makes the accumulation visible to an operator with an exit code; the description names the key so a reader of the schema meets it before the first call |
| The report fires on a `code_anchors: []` clear or an omitted field | Low | Med | the count is over entries SENT and accepted; T1's scenario asserts the keys are absent for an omitted field, for `[]`, and for fully labelled entries |
| The population is refilled by legal writes and drained only by labelling, so the count never reaches zero | Med | Low | it owns no exit code, so it cannot make the other three populations unread; the report prints the ids and the remedy, T1's advice is the drain at the source, and the deferred kit-side labelling is the drain this record depends on and names |

## Rollback

Revert T1 and the two keys disappear from responses (they are additive and omitempty, so no caller
breaks); revert T2 and `doctor --corpus` stops printing the population, with its exit code never
having depended on it. No schema change, no row touched.

## Follow-ups

- [ ] none at authoring
