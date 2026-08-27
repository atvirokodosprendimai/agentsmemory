# Task ADR-037-T1: Every ADR cited in Go source resolves

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `TestEveryCitedADRResolves` (the citation gate)
**Consumes:** none
**Data dependency:** hermetic

## Goal

A `ADR-NNN` string in any Go file under this module resolves to a record `docs/adr/ADR-NNN-*.md`,
enforced by a test that fails when one does not.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/repohygiene/citation_test.go` | add | The gate. Extends the package whose comment already names this class of check; reuses its `repoRoot`/walk helpers so the scan reads the same tree the other hygiene checks read |
| `AGENTS.md` | verify, edit only if needed | The convention text lands via PR #64; if that PR has not merged when this executes, fold its §doc-comments section in here in the same commit — the gate must not ship pointing at a convention the tree does not state |

What SELECTS the new test: `go test ./...` — the repository's own named check and CI both run the
package unconditionally, so deleting the test file is the only way to unselect it, and the Tests
table's mutation proof covers the check inside it.

## Ordered Steps

1. TDD red: the Acceptance fence greps for the test's `=== RUN` line, so it exits 1 today (no such
   test exists). Record that red run with `adr-verify`.
2. Write `TestEveryCitedADRResolves` in `internal/repohygiene/citation_test.go`: walk the tree with
   the package's existing gitignore-aware walker, collect every `ADR-[0-9]{3}` match in `.go` files
   (tests included — a citation in a test is provenance too), and fail naming file, line and number
   for any with no `docs/adr/ADR-NNN-*.md`. Baseline measured 2026-08-26: 180 citations, 25
   distinct records, 0 unresolved — the gate must pass this tree unchanged.
3. Prove the gate can fail: `adr-verify --mutant` breaking the resolve check (e.g. invert the
   match condition, or point the corpus glob at a directory that does not exist so every citation
   is "unresolved" — the test must go red either way). Only `killed` counts.
4. Verify the AGENTS.md §doc-comments section exists (PR #64); fold it in if not.
5. Run the Acceptance fence; record with `adr-verify`.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./internal/repohygiene/ && go test ./internal/repohygiene/ -run "TestEveryCitedADRResolves" -count=1 -v 2>&1 | tee /tmp/acc.out; grep -q "=== RUN   TestEveryCitedADRResolves" /tmp/acc.out && ! grep -qE "^(--- )?FAIL" /tmp/acc.out'
```

Red before the work: the `grep -q "=== RUN"` clause fails while the test does not exist, so the
fence cannot pass on an empty match the way a bare `-run` filter would.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestEveryCitedADRResolves` | `internal/repohygiene/citation_test.go` | every `ADR-NNN` in Go source names an existing record under `docs/adr/` | — |

## Invariants

- The scan reads the working tree through the package's gitignore-aware walker — the same view the
  other hygiene checks read — never a hand-kept list of files or numbers.
- The failure message names file, line and the unresolved number; a bare count is not actionable.
- The gate never judges comment LENGTH or PRESENCE — those were measured and rejected in the ADR's
  Alternatives; this test's only subject is citation resolution.

## Risks

| Risk | Mitigation |
|------|------------|
| A future archive directory moves records out of `docs/adr/` and the gate goes red corpus-wide | The corpus glob is one named constant; the ADR's Rollback section records that an archive move re-scopes it deliberately |
| The regex over-matches (e.g. `ADR-9999` truncating to `ADR-999`) | Bound the match: three digits not followed by another digit |

## Out of Scope

- Markdown/docs citations (permanent: `adr-lint` owns record-to-record cross-references)
- Any check on comment quality or length (permanent: decided against in the ADR with measured offender counts)

## Stop Condition

Stop and return to the ADR if the baseline scan finds an unresolved citation on the unchanged
tree — that contradicts the ADR's zero-offender measurement, and the record must be corrected
before a gate ships red on day one.

## Mutation Log

- 2026-08-27 · 475aad0* · mutant killed · exit 1 · `internal/repohygiene/citation_test.go` · the resolve check itself: with it gone an unresolved citation is reported by nothing · acceptance-sha256:1a4b36c8fa76aec3f5162585f204ee13596f142d1b00c0eb8bdd534d076309c6

## Verification Log
- 2026-08-27 · 475aad0 · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./internal/repohygiene/ && go test ./internal/repohygiene/ -run "TestEveryCitedADRResolves" -count=1 -v 2>&1 | tee /tmp/acc.out; grep -q "=== RUN   TestEveryCitedADRResolves" /tmp/acc.out && ! grep -qE "^(--- )?FAIL" /tmp/acc.out'` · acceptance-sha256:1a4b36c8fa76aec3f5162585f204ee13596f142d1b00c0eb8bdd534d076309c6
  ```
  testing: warning: no tests to run
  PASS
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/repohygiene	0.002s [no tests to run]
  ```
- 2026-08-27 · 475aad0* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./internal/repohygiene/ && go test ./internal/repohygiene/ -run "TestEveryCitedADRResolves" -count=1 -v 2>&1 | tee /tmp/acc.out; grep -q "=== RUN   TestEveryCitedADRResolves" /tmp/acc.out && ! grep -qE "^(--- )?FAIL" /tmp/acc.out'` · acceptance-sha256:1a4b36c8fa76aec3f5162585f204ee13596f142d1b00c0eb8bdd534d076309c6
