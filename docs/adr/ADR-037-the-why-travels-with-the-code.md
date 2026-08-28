# ADR-037: Carry the why with the code, and gate the citations

**Status:** Proposed
**Date:** 2026-08-26
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `AGENTS.md`, PR #64, `internal/doclint`, `internal/repohygiene`, `docs/adr/ADR-019-a-hit-shows-its-matching-regions-and-lets-the-agent-choose.md`
**Governs:** None — declared by its tasks
**Invalidates:** none — checked. Grepped every accepted ADR for doc-comment or `doclint` authority: seven mention doc comments incidentally (as evidence in their own narratives), none decides the convention. The house-style drawer this replaces (`cc92e756`, "long by most standards") already carries a `qualifies` correction filed by PR #64.
**Served-path change:** None — this ADR changes only conventions and a repository-shape gate; no runtime code path.

## Context

The palace does not travel with the file. An agent on a harness where the `am_*` tools are not
connected, a contributor who cloned the repo, a reviewer three months out — all read the code with
no memory attached. The doc comment is the only artifact shipped with the declaration, so it is the
only *why* that reaches them. The owner asked on 2026-08-26 for that position to be recorded and
made durable: a memory-less agent looking at this code must get the why from the code.

PR #64 measured the tree before proposing (2026-08-26, 560 exported functions and methods in
non-test, non-generated files): median doc comment **30 words**; 96 (17%) already at 70+; 21 (4%)
with none; **8 (1.4%) citing an ADR**. The house-style drawer described the best 17% as if it were
the norm, so the convention is a change of level, not a description of the tree.

Re-measured 2026-08-26 on this tree, all Go source including tests: **180 `ADR-NNN` citations
across 25 distinct records, every one resolving under `docs/adr/`** — the citation gate this ADR
adopts has zero day-one offenders.

`adr-debt` run 2026-08-26: no open deferral or follow-up in the active corpus concerns doc
comments; nothing to pull in.

## Existing Primitives Audit

- **`internal/doclint`** — already gates that a doc comment is ATTACHED to the declaration it names
  and opens with its name. Reused untouched; this ADR adds nothing it checks.
- **`internal/repohygiene`** — checks about the shape of the repository itself, its package comment
  naming exactly this class: "the kind of thing a reviewer notices once, mentions, and then stops
  noticing." The new gate extends this package; no new component.
- **`adr-lint`** — checks cross-references between ADR documents; it never reads Go source. The gap
  it leaves (a Go comment citing a record that does not exist) is the gap this ADR closes.
- **The review skill** — carries every level judgement this ADR deliberately leaves ungated.

## Decision

Three positions, recorded here so they outlive the PR that first argued them:

**1. The doc comment is the context channel for readers without the palace.** An exported
declaration's comment is the name-first one-liner Go tooling requires, a blank line, then the WHY —
about 70 words, longer where the reason is longer: the failure it prevents, the alternative that
was rejected, the incident that motivated it. Where a decision record governs the behaviour, the
comment cites it inline as `ADR-NNN`. What earns the words is reason; restating what the code does
earns nothing at any length. A bare citation is the same defect in the other direction — a comment
reading only "ADR-013, because the owner said so" carries authority without reason, and a reader
with no palace learns obedience from it, not judgement. The citation supplements the why; it never
replaces it. `internal/palace/regions.go` is the exemplar: `SnippetRegions` carries
its measurement, `Region` cites ADR-019 for why the text is verbatim.

**2. Exactly one mechanical gate: every `ADR-NNN` cited in Go source resolves under `docs/adr/`.**
A citation that stops resolving is worse than no citation, because it reads as provenance while
pointing at nothing. The gate can fail today's shape in two real ways — a record is renumbered or
retired without sweeping its citers, or a comment cites a number that lives only in an unmerged
PR — and the failing state is trivially constructible (cite `ADR-999`), so the criterion is
falsifiable on this tree as it stands. Valid for this repository's citation grammar
(`ADR-` + three digits) and its single active corpus directory; a future archive move re-scopes it.

**3. The level is carried by review, deliberately ungated.** A review of a change that adds or
edits an exported declaration reports, by name, every comment that states only what the code does,
and every citation that does not resolve — at finding altitude, not as a footnote. No retrofit:
existing comments are upgraded only when their declaration is already being edited.

## Alternatives Considered

- **A minimum word-count gate:** rejected — 464 of 560 declarations offend on day one (measured
  2026-08-26, PR #64), and a word count measures padding, not reason. A gate with that alarm rate
  is one people delete, and `internal/doclint`'s own comment records exactly that: "the rule is
  deliberately narrow, because a noisy gate gets deleted."
- **A comment-on-every-export gate:** rejected — 21 day-one offenders, same noise argument, and
  presence is the weakest possible proxy for the thing wanted (a reason).
- **Convention prose only, no gate at all** (PR #64's original shape): rejected by the owner's ask
  and by the repo's own rule — "anything that must stay true gets a command whose exit code says
  so." The resolve gate is the one candidate PR #64 measured at zero offenders and left on the
  table; it costs one test and catches only future rot.

## Component / Boundary Impact

None — internal to `internal/repohygiene` (one added test) and `AGENTS.md` (convention prose,
landing via PR #64).

## Wiring & Contract Changes

None — implementation-internal only.

## Inter-task Contracts

None.

## Implementation

See `tasks/README.md` — one task, T1: the resolving-citation gate.

## Consequences

- **Positive:** the why survives cloning; a stale `ADR-NNN` citation is caught by exit code, not by
  a reviewer's memory; the convention has a durable record instead of living only in a merged PR
  body.
- **Negative:** review carries the level with no mechanical backstop — the measured cost PR #64
  named, accepted here for the measured reason (any level gate has hundreds of day-one offenders).
- **Neutral:** renumbering or retiring an ADR now requires sweeping its Go citers in the same
  change; the gate turns that from etiquette into a failing test.

## Out of Scope

- Retrofitting the 21 undocumented and 464 under-70 comments (permanent: the surgical-changes rule — a comment is upgraded only when its declaration is already being edited)
- Word-count or comment-presence gates (permanent: measured day-one offender counts of 464 and 21 make either a gate people learn to skip; the review clause in the Decision carries the level)
- Citations in Markdown and docs (permanent: `adr-lint` owns record cross-references; this ADR gates Go source, the surface it never reads)
- The AGENTS.md convention text itself (deferred: PR #64 — it carries the section; T1 verifies it landed and folds it in only if that PR dies)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| PR #64 is not merged when T1 executes, leaving the convention unrecorded in-tree | Low | Med | T1's steps verify the AGENTS.md section exists and fold it in from the PR in the same commit if not |
| A comment cites a number that exists only in an open PR (e.g. ADR-034 in PR #61 today) | Med | Low | The gate is the mitigation: it goes red until the record merges, which is the correct reading — the citation IS unresolvable for a clone at that commit |
| The citation grammar drifts (an archive directory appears) | Low | Low | The gate's corpus glob is one named constant; the Decision names the re-scope; an archive move re-scopes it deliberately |

## Rollback

Delete the added test in `internal/repohygiene` and, if desired, revert the AGENTS.md section via
PR #64's revert. No persistent state, no contract, no migration.

## Follow-ups

