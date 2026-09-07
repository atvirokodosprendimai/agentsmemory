# ADR-030 Tasks

Implementation tasks for ADR-030: A blend that cannot tell confidence from noise. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` headers. This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

**T2 genuinely depends on T1**, which is rare in this corpus and is the point of the split: T2 has no defensible content until the measurement exists. Shipping a normalisation because its arithmetic is persuasive is how weight 0.5 arrived.

## Task Index

| Task | Goal | Produces | Consumes | Status | Acceptance |
|------|------|----------|----------|--------|------------|
| T1 | A fixture that can exhibit the defect, and arms that can tell the candidates apart | small-pool and low-spread eval cases; three registered arms | none | done | `go test ./internal/palace/ -run "TestServedBlendTiesOnATwoCandidatePool\|TestSmallPoolArmsDisagree\|TestLowSpreadIsAmplifiedByMinMax\|TestEveryDeclaredArmIsRegistered"` |
| T2 | Ship the measured winner, and pin the property rather than the number | the served rerank-axis normalisation; a property test | T1's measurement and fixture | pending | `go test ./internal/palace/ -run "TestCrossEncoderDecidesATwoCandidatePool\|TestLowSpreadDoesNotBecomeSignal\|TestSmallPoolArmsDisagree"` |

## Not a task here

**Persisting `blended_score` to `search_events`.** It would let the tie rate be measured retrospectively, which is the only way to turn ADR-030's 17.6% exposure figure into an actual incidence. It is a migration, and T1's fixture answers the same question about the present without one. Receipted in `BACKLOG.md` §"From ADR-030".

**`max_distance` as a pool shrinker.** It makes small pools more likely — measured live, 10 candidates to 3 — and the corpus already holds a decision drawer reading "max_distance is DEAD as a confidence signal: on 61 cases the answerable/unanswerable top-1 cosine distributions overlap." Whether to floor it, default it differently, or remove it is its own decision with its own blast radius. Receipted.
