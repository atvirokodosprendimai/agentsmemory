# Task ADR-051-T8: A native skill that reaches the centralised catalogue

**Depends-on:** T6
**Covers:** none — no spec
**Estimated scope:** S (one SKILL.md)
**Owner:** unassigned
**Produces:** none
**Consumes:** `one installable unit` (T6)
**Data dependency:** hermetic

## Goal

Make the team's centralised skills discoverable by Claude Code's own skill mechanism, so an
agent finds them without being told the catalogue exists.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/skills/recall/SKILL.md` | add | the native entry point |
| `clients/claude-code/assets.go` | edit | embed it |

## Ordered Steps

1. Write the failing tests first (TDD red).
2. Write `SKILL.md` with a `description` that says WHEN to use it, and `allowed-tools` limited
   to the `am_*` read tools.
3. **The body calls `am_list_skills` / `am_load_skill`. It does not restate their contents.**
   A second copy of a protocol is a second thing to get wrong, and the copy nobody maintains
   is the one that stays wrong — this repository's own AGENTS.md records that against itself.
4. Run the fence, the mutants, the full suite.

## Acceptance

```bash
gofmt -l clients internal | (! grep -q .) && go vet ./... && \
go test ./clients/claude-code/ \
  -run 'TestTheSkillIsInstalled|TestTheSkillDoesNotRestateTheCatalogue|TestTheSkillFrontmatterIsValid' \
  -count=1 2>&1 | tee /tmp/adr051-t8.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t8.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheSkillIsInstalled` | `clients/claude-code/skill_test.go` | The install plan places `SKILL.md` where Claude Code discovers it | — |
| `TestTheSkillDoesNotRestateTheCatalogue` | `clients/claude-code/skill_test.go` | The body names `am_list_skills` and does NOT inline a skill's content — the drift gate, asserted rather than trusted | — |
| `TestTheSkillFrontmatterIsValid` | `clients/claude-code/skill_test.go` | `description` present and non-empty; `allowed-tools` names only read tools | — |

## Reachability

A `SKILL.md` in the wrong directory is invisible and errors nowhere. The install-plan test is
the only thing that can see the placement.

## Mutation Log

Filled by `adr-verify --mutant`.

## Invariants

- The skill points; it does not copy.
- Read tools only.

## Risks

A skill whose description overlaps `/am` gives the model two doors to one room. Say plainly in
the description which is which.

## Stop Condition

Stop if the skill cannot be scoped to read tools — a skill that can write is a second write
path with none of the protocol's gates.

## Out of Scope

Replacing `/am`. (deferred: `docs/adr/BACKLOG.md`)

## Verification Log

Filled by `adr-verify`.
