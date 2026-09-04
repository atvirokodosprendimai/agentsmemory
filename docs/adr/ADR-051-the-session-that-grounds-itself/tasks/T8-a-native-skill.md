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
  -run 'TestTheSkillIsInstalled|TestTheSkillPointsAtTheCatalogueRatherThanCopyingIt' \
  -count=1 2>&1 | tee /tmp/adr051-t8.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t8.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheSkillIsInstalled` | `clients/claude-code/anchorcue_test.go` | The install plan places `SKILL.md` where Claude Code discovers it | — |
| `TestTheSkillPointsAtTheCatalogueRatherThanCopyingIt` | `clients/claude-code/anchorcue_test.go` | The body names `am_list_skills`/`am_load_skill`/`am_search` and inlines no convention; front matter declares a description and READ tools only — a recall skill that can write is a second write path with none of the protocol's gates | — |

## Reachability

A `SKILL.md` in the wrong directory is invisible and errors nowhere. The install-plan test is
the only thing that can see the placement.

## Mutation Log

Filled by `adr-verify --mutant`.
- 2026-09-04 · 76b274d* · mutant killed · exit 1 · `clients/claude-code/installer.go` · the skill is embedded and never written: Claude Code discovers nothing, and no test that merely READS the file would notice · acceptance-sha256:b3e0112b63a1ea499d064dd9f4e0ee30b6f44b97a797cf8024f2ad565ee813d9
- 2026-09-04 · 76b274d* · mutant killed · exit 1 · `clients/claude-code/skills/recall/SKILL.md` · the recall skill granted a WRITE tool and stripped of the catalogue tools: a second write path with none of the protocols gates · acceptance-sha256:b3e0112b63a1ea499d064dd9f4e0ee30b6f44b97a797cf8024f2ad565ee813d9

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
- 2026-09-04 · 76b274d* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:b3e0112b63a1ea499d064dd9f4e0ee30b6f44b97a797cf8024f2ad565ee813d9 · ms:34129
  ```
  --- last 10 line(s) of stdout (of 51 after folding 51 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.319s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	1.320s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	1.232s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.652s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	1.464s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.237s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	1.077s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.496s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.691s
  FAIL
  ```
- 2026-09-04 · 76b274d* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:b3e0112b63a1ea499d064dd9f4e0ee30b6f44b97a797cf8024f2ad565ee813d9 · ms:37831
  ```
  --- last 10 line(s) of stdout (of 51 after folding 51 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.563s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.896s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	1.087s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.670s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	1.409s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.968s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	1.329s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.929s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.747s
  FAIL
  ```
- 2026-09-04 · 76b274d* · exit 1 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:b3e0112b63a1ea499d064dd9f4e0ee30b6f44b97a797cf8024f2ad565ee813d9 · ms:54882
  ```
  --- last 10 line(s) of stdout (of 51 after folding 51 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	6.145s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	2.761s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	2.656s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	3.609s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	3.780s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	4.143s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	2.161s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.956s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	1.357s
  FAIL
  ```
- 2026-09-04 · 76b274d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:b3e0112b63a1ea499d064dd9f4e0ee30b6f44b97a797cf8024f2ad565ee813d9 · ms:45652
- 2026-09-04 · 76b274d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:b3e0112b63a1ea499d064dd9f4e0ee30b6f44b97a797cf8024f2ad565ee813d9 · ms:42935
- 2026-09-04 · 76b274d* · exit 0 · `gofmt -l clients internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:b3e0112b63a1ea499d064dd9f4e0ee30b6f44b97a797cf8024f2ad565ee813d9 · ms:38161
