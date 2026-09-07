# Task ADR-010-T2: Retraction carries a reason, and erasure leaves the agent surface

> **Amended 2026-08-20 before execution**, on the objection that plenty of retractions replace
> nothing: this task now adds an explicit `am_invalidate_drawer(id, reason)` verb beside the
> supersede path, and `reason` is required on both. It also removes THREE agent-facing destructive
> tools rather than one — `delete_drawer`, `delete_tunnel`, `delete_hallway` — and adds the same
> required `reason` to `am_kg_invalidate`, which today records a date and no why.

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `am_invalidate_drawer(id, reason)`; supersede semantics on `am_update_drawer`; a required `reason` on `am_kg_invalidate`; erasure moved to the operator surface
**Consumes:** `valid_to` / `superseded_by` (T1)
**Data dependency:** hermetic

## Goal

An agent correcting a memory writes a new record and ends the old one; an agent cannot erase.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | `Supersede`; `Update`'s content path routes through it |
| `internal/mcpserver/drawers.go` | edit | `am_invalidate_drawer` added; `am_update_drawer` returns the new id and names the ended one; `am_delete_drawer`, `am_delete_tunnel`, `am_delete_hallway` deregistered from the agent catalogue |
| `internal/mcpserver/kg.go` | edit | `am_kg_invalidate` gains a required `reason` — it records a date and no why today, which is the same defect one level down |
| `cmd/server/mcp.go` | edit | erasure available to the operator, where `delete_wing` already lives |
| `internal/mcptest/registry_test.go` | edit | **the selection**: a scenario proving a correction leaves the old text unreachable by default and reachable as history |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCorrectionSupersedesRatherThanOverwrites`, `TestAgentSurfaceHasNoErasure`. Commit them red.
2. `Supersede` writes the new record, links `superseded_by`, sets the old record's `valid_to`. One transaction: a half-applied supersession leaves two current records claiming different things, which is worse than either outcome.
3. The multi-chunk refusal added on 2026-08-20 is REPLACED, not kept. It exists because rewriting chunk 0 left chunk 1 live with the old text; superseding the whole memory removes the reason for the refusal, and leaving it would block the operation this ADR exists to enable.
4. Deregister the three destructive drawer/graph tools. `TestCatalogSizeIsWhatTheReadmeClaims` will fail — update the README count in the same commit, since that gate exists to make exactly this visible. Removing a tool also drops it from the ADR-008 coverage ratchet: lower the ceiling in the same commit rather than letting the headroom drift.
5. The refusal an agent now gets from the absent tool must name what to do instead: correct the memory, or ask an operator to erase.
6. Falsify: overwrite in place instead of superseding; leave both records current; keep erasure on the agent surface.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm --init -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true;
  set -e
  git config --global --add safe.directory /src
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcptest/ ./internal/mcpserver/ -run "TestCorrectionSupersedes|TestAgentSurfaceHasNoErasure|TestEveryToolIsExercised" -count=1 -v 2>&1 | tee /tmp/v2.out
  grep -q -- "--- PASS: TestCorrectionSupersedesRatherThanOverwrites" /tmp/v2.out
  grep -q -- "--- PASS: TestAgentSurfaceHasNoErasure" /tmp/v2.out
  if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/v2.out; then exit 1; fi
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCorrectionSupersedesRatherThanOverwrites` | `internal/mcptest/registry_test.go` | the old text survives, the new one is current, and they are linked | — |
| `TestAgentSurfaceHasNoErasure` | `internal/mcpserver/catalog_test.go` | no agent-facing tool hard-deletes a memory, a tunnel or a hallway | — |
| `TestRetractionRequiresAReason` | `internal/mcptest/registry_test.go` | invalidate and supersede both refuse without one, and `am_kg_invalidate` does too | — |
| `TestSupersedeIsAtomic` | `internal/palace/service_test.go` | a failure mid-supersession leaves exactly one current record | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| overwrite content in place | yes | `TestCorrectionSupersedesRatherThanOverwrites` |
| write the new record without ending the old | yes | `TestCorrectionSupersedesRatherThanOverwrites` |
| re-register `am_delete_drawer` on the agent surface | yes | `TestAgentSurfaceHasNoErasure` |
| make `reason` optional on invalidate | yes | `TestRetractionRequiresAReason` |
| accept an empty-string `reason` | yes | `TestRetractionRequiresAReason` |
| drop the transaction around supersede | yes | `TestSupersedeIsAtomic` |

## Out of Scope

- Retention policy for superseded records (deferred: docs/adr/ADR-010-supersede-do-not-overwrite.md)
- Superseding across a wing move (permanent: a move is not a claim about the world)

## Invariants

- Exactly one record in a supersession chain is current.
- No agent-facing tool removes a row or a vector.

## Risks

- An operator needs to erase and cannot find the path. Mitigated: the same task adds it, and the agent-facing refusal names it.

## Stop Condition

Stop and ask if superseding cannot be made atomic with the current store — two current records claiming different things is the failure this ADR exists to prevent, and shipping it partially would create it.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>

## Mutation Log
