# Task ADR-046-T1: Serve every chunk of an eager bootstrap record

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `Bootstrap serves whole memories`
**Consumes:** none
**Data dependency:** hermetic

## Goal

`Service.Bootstrap` returns each eager record's whole memory, so an entry record
longer than `ChunkSize` arrives byte-identical to what was filed.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/bootstrap.go` | edit | The eager loop appends the row `DrawersByIDs` returned, which is the root chunk; it must reassemble from every chunk of that memory |
| `internal/palace/bootstrap_whole_test.go` | add | The new tests |

Nothing selects this but `Service.Bootstrap` itself, which `internal/mcpserver`
already calls for `am_bootstrap` — no registry or flag is involved. The mutation in
Ordered Steps is what proves the reassembly is on the served path rather than merely
present.

## Ordered Steps

1. Write the failing tests first (TDD red): `TestBootstrapServesEveryChunkOfAnEntryRecord` and `TestBootstrapLeavesAShortEntryRecordUnchanged` in `internal/palace/bootstrap_whole_test.go`. Run the Acceptance fence and confirm RED. The multi-chunk fixture must reach `llm_init` by MOVING it there, because `prepareWrite` still refuses the direct write until T2 — say so in the test, since a reader will otherwise ask why the fixture takes two calls.
2. In `Bootstrap`'s eager loop, resolve the inline ids' chunks with `Repo.MemoryChunksByRoots` (one query for up to `bootstrapEagerLimit` roots, not N) and set each returned drawer's `Content` to `reassembleMemory` of its chunks, ordered by `chunk_index`.
3. Leave the wing-policy check where it is: it is placed per drawer and must keep deciding on the ROOT's placement, not on a reassembled body.
4. Confirm a single-chunk record is returned unchanged — `reassembleMemory` returns `chunks[0].Content` for one chunk, so this is a regression guard rather than new behaviour.
5. Run the full package suite.

## Acceptance

```bash
gofmt -l internal/palace | grep -q . && exit 1
go vet ./... && go test ./internal/palace/ -run "TestBootstrapServesEveryChunkOfAnEntryRecord|TestBootstrapLeavesAShortEntryRecordUnchanged" -count=1 -v 2>&1 | tee /tmp/adr046-t1-new.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr046-t1-new.out
go test ./internal/palace/ -count=1 2>&1 | tee /tmp/adr046-t1-reg.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/adr046-t1-reg.out
```

The new tests run ALONE in the second command so the regression suite in the third
cannot carry the verdict. Runs the local toolchain rather than `golang:1.26-alpine`
under docker, for the reason ADR-045's tasks record: docker is unavailable on the
executing machine and a fence that cannot run is not a gate.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestBootstrapServesEveryChunkOfAnEntryRecord` | `internal/palace/bootstrap_whole_test.go` | An entry record over `ChunkSize` comes back from the eager tier BYTE-IDENTICAL to the filed text, and `Truncation.Omitted` stays 0 because nothing was in fact omitted | — |
| `TestBootstrapLeavesAShortEntryRecordUnchanged` | `internal/palace/bootstrap_whole_test.go` | A one-chunk record is returned exactly as before, so reassembly did not change the common case | — |

Byte equality, not a length check: the failure this is written against is the
`ChunkOverlap` seam, and a reassembly that duplicates or drops 320 runes can still
produce a plausible length. Shapes the creation path can produce and the decision for
each: a record with an ENDED chunk among current ones (out of scope — the eager tier
resolves ids the entry point named, and an ended record is not current); a record
whose root is in another wing (covered by the existing wing-policy check, which this
task must not move); a diary memory (cannot occur — the entry room is not the diary
room).

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestBootstrapServesEveryChunkOfAnEntryRecord` |
| 2 — something selects it | The reassembly call inside `Bootstrap`'s eager loop; the mutation that reverts it to the raw row makes the multi-chunk test red |
| 3 — the caller can discover it | n/a: no declared interface — the eager tier's shape is unchanged, only the completeness of its content |
| 4 — it is used | `am_bootstrap` is called at every wake-up; nothing measures entry-record length yet |

## Mutation Log

- 2026-09-01 · 1e973f7* · mutant killed · exit 1 · `internal/palace/bootstrap.go` · serves the root chunk again, restoring the silent mid-sentence cut with omitted:0 · acceptance-sha256:fc1a56ff5530db8f4c8fdaae5ba665df255ee3f4650307b211a014afc4a3d4d9

## Invariants

- A single-chunk record is returned exactly as it was before this change.
- The wing-policy check still decides on the ROOT drawer's placement, before any reassembly is returned.
- `Truncation.Omitted` continues to count what the entry node offered and this call did not return; reassembly must not be counted as an omission, nor hide one.
- The eager tier's COUNT bound (`bootstrapEagerLimit`) is unchanged.

## Risks

- Resolving chunks per id would be N queries on the one call every session makes. Mitigation: `MemoryChunksByRoots` takes all roots at once, which is why it is named in Affected Files.
- A reassembly bug is invisible to a length assertion. Mitigation: byte equality against the filed text.

## Stop Condition

Stop and ask if `reassembleMemory` turns out not to be reusable here — it is the
search path's function and this task assumes it is a pure join over ordered chunks.
If it depends on search-specific state, this task is a different and larger change
than described, and inventing a second reassembly is exactly the duplication that
makes two answers to one question.

## Out of Scope

- Removing the entry-room refusal — that is T2's job, and it is only safe once this has landed.
- Paging or byte-bounding the eager tier (deferred: `docs/adr/BACKLOG.md` §"From ADR-046 (serve the whole entry record)")

## Verification Log
- 2026-09-01 · 1e973f7* · exit 1 · `gofmt -l internal/palace | grep -q . && exit 1 …` · acceptance-sha256:fc1a56ff5530db8f4c8fdaae5ba665df255ee3f4650307b211a014afc4a3d4d9
  ```
  --- last 10 line(s) of stdout (of 142 after folding 143 raw)
  2026/09/01 10:06:56 OK   00031_drawers_content_key.sql (6.18ms)
  2026/09/01 10:06:56 OK   00032_kg_ended_reason.sql (974.88µs)
  2026/09/01 10:06:56 OK   00033_drawers_superseded_by_idx.sql (535.08µs)
  2026/09/01 10:06:56 OK   00034_billing_checkout_intents.sql (645.5µs)
  2026/09/01 10:06:56 OK   00035_billing_applied_orders.sql (585.63µs)
  2026/09/01 10:06:56 OK   00036_drawer_fetches.sql (791.33µs)
  2026/09/01 10:06:56 goose: successfully migrated database to version: 36
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	11.501s
  FAIL
  ```
- 2026-09-01 · 1e973f7* · exit 0 · `gofmt -l internal/palace | grep -q . && exit 1 …` · acceptance-sha256:fc1a56ff5530db8f4c8fdaae5ba665df255ee3f4650307b211a014afc4a3d4d9
- 2026-09-01 · 1e973f7* · exit 0 · `gofmt -l internal/palace | grep -q . && exit 1 …` · acceptance-sha256:fc1a56ff5530db8f4c8fdaae5ba665df255ee3f4650307b211a014afc4a3d4d9
