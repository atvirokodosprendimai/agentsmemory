# Task ADR-047-T5: Run the grid, apply the pre-registered rule, decide what the skills may say

**Depends-on:** T4
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** M
**Produces:** `docs/adr/ADR-047-measure-the-writing-rule/evidence/grid-<date>.md`, and either a skill amendment or a recorded refusal to make one
**Consumes:** `<out>.cells.json` (T4)
**Data dependency:** needs the real LongMemEval-S file, a palace with an embedder, and a generative endpoint. The sign-off must record the subset size, the model and the context budget the run was taken against.

## Goal

Take the run, apply the decision rule the parent ADR pre-registered, and record what the centralised
skills are now entitled to claim — including, if that is the answer, that they are entitled to
claim less than they do today.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/ADR-047-measure-the-writing-rule/evidence/grid-<date>.md` | add | the full table, both metrics, every interval, and the configuration it is valid for |
| `docs/adr/ADR-047-measure-the-writing-rule.md` | edit | the outcome, amended in place as ADR-032 was |
| `docs/adr/BACKLOG.md` | edit | whatever the run raises and this ADR does not own |
| the `start-here` centralised skill | edit, **only if the rule permits** | the one place a promoted instruction lands; it is a server-side skill, not a repo file, so the amendment is recorded here with its version bump |

## Ordered Steps

1. This task adds no code, so there is no failing test to establish first; its TDD-red equivalent
   is the state it starts in — T4's fence green, the grid unrun, and no skill amended. Confirm all
   three before step 2, because a skill already carrying the conclusion makes the run unfalsifiable.
2. Split the subset in half by the recorded seed. Take the argmax cell on the first half.
3. Confirm the margin on the second half with `PairedDelta`. A policy is promoted only if that
   interval excludes zero.
4. Hand-score a sample of the judge's verdicts and report the judge's own agreement rate, so the
   headline number carries the error rate of the instrument that produced it.
5. Write the evidence file: both metrics side by side, every cell, every interval, and the header
   from `.cells.json` reproduced in full.
6. Amend the ADR with the outcome — including, explicitly, any rule `start-here` states today that
   the run found neutral.
7. Only then, and only for policies that cleared the rule, amend the skill. Resend its
   `description` verbatim when updating, because an omitted description blanks it.

## Acceptance

Acceptance is human-observed: the run is a judgement about what a document may claim, and no exit
code can carry that. The fence below is the sign-off template to copy, with every placeholder
replaced by what the run actually produced.

```bash
adr-verify docs/adr/ADR-047-measure-the-writing-rule/tasks/T5-run-the-grid-and-decide.md \
  --human "<date> · grid run n=<N> subset seed=<S> · reader/judge=<model> · budget=<T> tokens · profile=<ranking string> · judge agreement=<A> on <k> hand-scored · promoted: <policies or none> · decision <ship|withdraw|blocked>"
```

The `Data dependency` header is not `hermetic`, so the sign-off must record what the run was taken
against — which is why the subset size, the seed, the model and the budget are in the template
rather than left to whoever signs it.

The sign-off must name a decision from `ship`, `withdraw`, `blocked` — the vocabulary
`TestAHumanObservedSignOffAgreesWithTheIndex` requires — and the README status must match it.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| — | — | this task adds no code; its evidence is the run and the sign-off | — |

No test table entry is possible here and that is the honest answer rather than a formality: the
task's output is a judgement about what a document may claim, and a test asserting that judgement
would be the instrument deciding the hypothesis space, which is the failure `docs/adr/BACKLOG.md`
§"Standing…" names.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the evidence file |
| 2 — something selects it | the ADR amendment cites it |
| 3 — the caller can discover it | the skill text, if any is promoted, is what every session reads |
| 4 — it is used | nothing measures whether a promoted rule is followed; that gap is worth naming and is not closed here |

## Mutation Log

## Invariants

- The argmax and the confirmation are taken on different halves.
- No skill is amended for a policy whose held-out interval spans zero.
- The evidence file states the configuration the numbers are valid for, in full.

## Risks

- The run comes back all-neutral and the temptation is to widen the subset until something clears.
  Mitigation: `--n` and the seed are recorded, and a second larger run is a new sign-off line, not
  a re-reading of this one.
- A promoted rule is right for LongMemEval conversations and wrong for a code repository's memories.
  Mitigation: say so in the skill text; the corpus is named in the amendment.

## Stop Condition

Stop and ask before amending any centralised skill, whatever the numbers say. A skill change reaches
every project in the workspace, and the decision to broadcast a rule is the operator's.

**What would make this criterion impossible to fail:** nothing, and that is checked rather than
assumed — a policy that does nothing produces an interval spanning zero on any subset, and the
verbatim baseline is registered so such a policy exists in every run by construction.

## Out of Scope

- Changing any ranking default from this table (permanent: ADR-002, ADR-003 and ADR-014 own those,
  and this corpus is not the population they were chosen on)
- Re-deriving ADR-003's cited LongMemEval ablation figures (deferred: `docs/adr/BACKLOG.md` §"From ADR-047")

## Verification Log
