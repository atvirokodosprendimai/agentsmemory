# Task ADR-062-T2: The `amm` skill, and an install set derived from the embed

**Status:** partial
**Depends-on:** none
**Produces:** `skills/amm/SKILL.md`, `nativeSkillAssets()` derived from the embed
**Consumes:** the `PAUSE … /amm <task>` line (T1)
**Proof map:** v1
**Rests-on:** `every embedded skill is installed`
**Covers:** —

## Goal

`/amm` exists as a skill a session can invoke mid-turn, and no skill can ship
inside the binary while being installed by nothing.

## Affected Files

| File | Change |
|---|---|
| `clients/claude-code/skills/amm/SKILL.md` | new: the grounding pipeline scoped to one task, for a session whose context was replaced |
| `clients/claude-code/assets.go` | `nativeSkillAssets` becomes a function reading the embedded `skills/` directory |
| `clients/claude-code/installer.go` | call it |
| `clients/claude-code/anchorcue_test.go` | call it |
| `clients/claude-code/reground_test.go` | `TestEverySkillEmbeddedIsInstalled` |

## Ordered Steps

1. [S1] Write `TestEverySkillEmbeddedIsInstalled` red: every directory under the embedded `skills/` holding a `SKILL.md` appears in the install set; fail vacuously if fewer than two skills are embedded, since one cannot show a divergence.
2. [S2] Write `skills/amm/SKILL.md` — the pipeline scoped to a task, pointing at the steps rather than restating `/am`. [proof: human: a skill body is prose a model reads; no test can judge whether it grounds a session, and a word-count gate would measure padding — the same reason internal/doclint refuses one]
3. [S3] Derive `nativeSkillAssets()` from the embed; update both callers.
4. [S4] Mutant: the function returns the old hand-kept `{"recall"}`. [proof: mutation]

## Acceptance

```bash
gofmt -l clients/claude-code | grep -q . && exit 1; go vet ./clients/... && go test ./clients/claude-code/ -run 'TestEverySkillEmbeddedIsInstalled' -count=1
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|---|---|---|---|---|
| `TestEverySkillEmbeddedIsInstalled` | `clients/claude-code/reground_test.go` | no skill ships in the binary and reaches no config dir | — | S1, S3 |

## Invariants

- The embed glob is the one source of truth for which skills exist.
- A directory under `skills/` without a `SKILL.md` is not a skill and is skipped, so an incidental directory cannot break an install.
- The skill restates no protocol: it names the steps and what a compacted session must reconcile.

## Risks

- A future skill that should NOT be installed everywhere would need an exemption this design has no place for. Accepted: no such skill exists, and the shape to add then is a marker in the skill's own front matter, not a list beside the glob.

## Out of Scope

- Centralised skills (permanent: boundary: `am_list_skills` / `am_load_skill` serve those; this is the kit's own shipped set)

## Stop Condition

Stop if Claude Code stops discovering `skills/<name>/SKILL.md`: the skill would
be installed and unreachable, which is the defect this record's own audit names.

## Mutation Log

## Verification Log
