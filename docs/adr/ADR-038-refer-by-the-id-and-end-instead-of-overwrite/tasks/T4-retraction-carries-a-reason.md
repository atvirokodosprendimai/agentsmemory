# Task ADR-038-T4: Retraction carries a reason, and erasure leaves the agent surface

> Re-authored 2026-08-27 from ADR-010's T2, which this record supersedes. The decision is unchanged.
> What changed is that a supersede now mints an OPAQUE id (T3) instead of a content-derived one, so
> "the new record's id" is a name rather than a hash of the new text — which is what makes a
> supersession an ordinary edge instead of an identity problem.

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — palace + mcpserver + tool surface)
**Owner:** unassigned
**Produces:** `am_invalidate_drawer(id, reason)`; supersede semantics on `am_update_drawer`; `am_kg_supersede(subject, predicate, old, new, reason)`; a required `reason` on `am_kg_invalidate`; erasure moved to the operator surface
**Consumes:** `EndDrawer(id, reason)` and the validity window (T1); the opaque mint (T3)
**Data dependency:** hermetic

## Goal

An agent correcting a memory writes a new record and ends the old one with a reason; an agent cannot
erase.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | `Update`'s content path becomes a supersede: mint a new row, `EndDrawer` the old with the reason, link `superseded_by`. The multi-chunk refusal at `:951` is re-scoped — a supersede replaces the whole memory, which is what that refusal said to do by hand |
| `internal/mcpserver/drawers.go` | edit | `am_invalidate_drawer` declared and registered; `am_update_drawer` gains a required `reason` and returns the NEW id naming the ended one; `am_delete_drawer`, `am_delete_tunnel`, `am_delete_hallway` **and `am_delete_wing`** removed from the agent registration (`registerDeleteWing` at `admin.go:198` is gated on `local`, which is not a boundary — it is the case where agent and operator share a process) — this is the line that SELECTS the boundary, and deleting it puts erasure back in an agent's hands |
| `internal/mcpserver/server.go` | edit | the registration list — a tool removed from the agent surface must be absent from the catalogue an agent reads, not merely refused at call time |
| `cmd/server/` | edit | the operator erasure path for a single drawer, beside `wing delete`, so removal stays possible for a leaked secret |
| `internal/palace/anchors.go` | edit | carry the old record's anchors onto the successor with `status = 'unchecked'`, `checked_at = ''`. `anchorID` folds in the drawer id, so the copies mint new ids for free — no dedupe problem |
| `internal/mcpserver/kg.go` | edit | `am_kg_invalidate` gains a required `reason`; **`am_kg_supersede` is declared and registered** — the line that makes the atomic verb reachable, without which `KGSupersede` is a function no agent can call |
| `internal/palace/kg.go` | edit | `KGSupersede` — end the old and add the new in ONE transaction (there is no `Transaction(` anywhere in this file today), stamping both `old.valid_to` and `new.valid_from` with the same RFC3339 **datetime** so `temporalEndKey`'s date-only stretch never applies |
| `db/migrations/00032_kg_ended_reason.sql` | add | the column the KG reason lands in |
| `internal/palace/supersede.go` | add | `Supersede` / `supersedeInto`, `InvalidateDrawer`, `carryAnchors`, `KGSupersede` |
| `internal/palace/repo.go` | edit | `DrawerPatch.Reason`, required whenever `Content` is set |
| `internal/palace/kg.go` | edit | `kgAddOn` split out of `KGAdd` so the transaction-bound path shares it rather than copying it; `KGInvalidate` gains `reason`; `KGFact.EndedReason` surfaces it |
| `cmd/server/drawer.go` | add | `agentsmemory drawer erase` — the operator path, gated on the database file exactly as `wing delete` is |
| `README.md` | edit | the tool table — `TestEveryCatalogToolIsNamedInTheReadme` requires a first-cell row per catalogue tool, so adding one tool and removing three is a README change in this commit |

## Ordered Steps

1. Write the failing tests first — RED against the tree as it stands:
   - correcting a memory returns a NEW id, the old row is ended with the given reason, and
     `superseded_by` links them;
   - the old row's TEXT is still readable by id — ending is not deleting;
   - `am_update_drawer` without a reason is refused;
   - `am_invalidate_drawer(id, reason)` ends a memory that nothing replaces;
   - `am_kg_invalidate` without a reason is refused;
   - **superseding a fact is atomic and leaves no observable boundary.** Add a fact, supersede it,
     and assert `as_of` ONE UNIT PAST the boundary returns exactly one value, and that the day-scale
     overlap is gone. ⚠ **This assertion passes under inclusive AND half-open semantics alike, so it
     cannot fail on the thing #74 will decide — say so at the assertion**, or the next reader sees
     boundary coverage where there is none and #74 lands against a test that already looks green.
     **Not the boundary instant itself** — `inEffectAt` is inclusive on both ends,
     so that one is #74's and asserting it here would make the test unsatisfiable;
   - **the four destructive tools are absent from the agent catalogue** — a source or registration
     check, because a behavioural test that never calls them passes either way (rung 3);
   - a corrected memory's anchors appear on the SUCCESSOR with status `unchecked`, and are gone from
     nothing — the old record keeps its own, because it keeps its text.
2. Implement the supersede path in `Service`, ending through T1's single `EndDrawer`.
3. Declare `am_invalidate_drawer`; add the required reasons; remove the three tools from the agent
   registration and add the operator single-drawer erasure path.
4. Update the README tool table.
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ ./internal/mcpserver/ ./cmd/server/ -run 'TestCorrectingAMemorySupersedesIt|TestTheEndedTextIsStillReadableById|TestUpdateWithoutAReasonIsRefused|TestUpdateReturnsTheNewIdNamingTheOneItEnded|TestUpdateAppliesAnchorsToTheCorrectingRecord|TestInvalidateDrawerEndsWithNoSuccessor|TestKgInvalidateRequiresAReason|TestKgSupersedeIsReachableFromTheToolSurface|TestDestructiveToolsAreAbsentFromTheAgentCatalogue|TestAdminOffersNoErasure|TestASupersedeCarriesAnchorsAsUnchecked|TestKgSupersedeLeavesNoBoundaryOverlap|TestKgSupersedeRecordsWhyTheOldFactEnded|TestKgSupersedeIsAtomic|TestDrawerEraseNeedsTheIdSpelledTwice|TestDrawerEraseRemovesEveryChunk' -count=1 2>&1 | tee /tmp/acc38t4a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t4a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t4b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t4b.out
```

The whole tree runs second because this task edits `README.md`, which `TestEveryCatalogToolIsNamedInTheReadme` reads from another package.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCorrectingAMemorySupersedesIt` | `internal/palace/supersede_test.go` | a new row, the old one ended, the link written | — |
| `TestTheEndedTextIsStillReadableById` | `internal/palace/supersede_test.go` | ending is not deleting — the whole point | — |
| `TestUpdateWithoutAReasonIsRefused` | `internal/mcpserver/supersede_test.go` | the reason is required where an agent supplies it. Also `internal/palace/supersede_test.go` at the service boundary | — |
| `TestInvalidateDrawerEndsWithNoSuccessor` | `internal/mcpserver/supersede_test.go` | a retraction that replaces nothing is expressible, and its text survives | — |
| `TestKgInvalidateRequiresAReason` | `internal/mcpserver/kg_test.go` | the half of the store that kept history stops keeping only *that* a fact ended | — |
| `TestKgSupersedeLeavesNoBoundaryOverlap` | `internal/palace/kgsupersede_test.go` | both windows carry the same INSTANT, so the day-scale overlap #74 reproduced is gone. ⚠ Deliberately not asserted AT the boundary instant — that is #74's half-open question and `inEffectAt` is inclusive on both ends, so the assertion here passes under either semantics and says so | — |
| `TestKgSupersedeIsAtomic` | `internal/palace/kgsupersede_test.go` | a failure injected between the end and the add (a gorm Create callback that errors on `kg_triples`) leaves the graph unchanged, not with zero or two current values | — |
| `TestASupersedeCarriesAnchorsAsUnchecked` | `internal/palace/supersede_test.go` | anchors reach the successor and are NOT marked verified — an unverified pin must never read as a checked one | — |
| `TestUpdateAppliesAnchorsToTheCorrectingRecord` | `internal/mcpserver/supersede_test.go` | `code_anchors` land on the id the update RETURNED, not the one it ended — otherwise the argument silently stops doing the only thing it was written for | — |
| `TestDrawerEraseRemovesEveryChunk` / `TestDrawerEraseNeedsTheIdSpelledTwice` | `cmd/server/drawer_test.go` | the operator erasure path exists, takes the id twice, and reaches every chunk | — |
| `TestDestructiveToolsAreAbsentFromTheAgentCatalogue` | `internal/mcpserver/catalog_test.go` | **rung 3** — a registration check, since a behavioural test that never calls a tool passes whether or not it is offered | — |

**Shapes the creation path can already produce, decided rather than assumed:** a multi-chunk memory
(a supersede replaces the WHOLE memory — every chunk ends, one new set is written); a drawer with
anchors — **decided 2026-08-27: they CARRY to the successor with `status` reset to `unchecked`**,
because verification is client-side and the server cannot re-check at supersede time, and because 41
of 2,024 drawers carry an anchor at all so clearing them spends a scarce resource; a drawer already
ended (refuse);
a source-less drawer (no `purgeSource`, so the supersede is the only path — assert it works).

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the supersede and invalidate unit tests |
| 2 — something selects it | `Service.Update`'s content branch; mutation: restore the in-place `Updates()` and `TestCorrectingAMemorySupersedesIt` goes red |
| 3 — the caller can discover it | `am_invalidate_drawer` in the tool schema and the README table; the three removed tools ABSENT from the agent catalogue, asserted by a registration check. **This is the rung that decides whether the erasure boundary is real** — a tool still advertised is a tool an agent will call |
| 4 — it is used | the ratio of supersedes to invalidates over a month of real writes, and the median `reason` length. Nothing measures it today; the ADR carries a Follow-up to report it. |

## Mutation Log

- 2026-08-27 · 3800814* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · anchors written to the ENDED record instead of the correction · acceptance-sha256:615bd85ff75149f9ea1b43bed7504b476922767aa87861b9114c286158995e13
- 2026-08-27 · 3800814* · mutant killed · exit 1 · `internal/palace/supersede.go` · the successor-insert error is swallowed, so the transaction commits with the old fact ended and nothing replacing it · acceptance-sha256:615bd85ff75149f9ea1b43bed7504b476922767aa87861b9114c286158995e13
- 2026-08-27 · 3800814* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the retraction verb is written but never registered — the classic finished-and-unreachable defect · acceptance-sha256:615bd85ff75149f9ea1b43bed7504b476922767aa87861b9114c286158995e13
- 2026-08-27 · 3800814* · mutant killed · exit 1 · `internal/palace/service.go` · the content path falls back to the in-place edit — the id survives and the old text is destroyed · acceptance-sha256:615bd85ff75149f9ea1b43bed7504b476922767aa87861b9114c286158995e13

## Invariants

- Ending goes through T1's single `EndDrawer`. This task adds callers, never a second ending path.
- The old text survives every correction, and so do its own anchors.
- Superseding a fact never leaves an instant with ZERO current values, and never leaves more than the boundary instant with two. The boundary instant itself is issue #74's — `inEffectAt` is inclusive on both ends, so a shared endpoint is in effect for both rows.
- `KGSupersede` writes datetimes, never date-only values, so it never depends on `temporalEndKey`'s stretch.
- A carried anchor is never `verified` until a client says so. The server never mints a verification verdict.
- No agent-reachable tool destroys a drawer, a tunnel or a hallway after this task.
- Erasure remains possible for an operator — a store that cannot forget a leaked secret is not deployable.

## Risks

- Removing three tools from the agent surface is a breaking change for any client that calls them. The refusal text must name the operator path, or an agent that cannot delete will file a duplicate instead and the palace grows a class of junk this ADR did not intend.
- A required `reason` gets "obsolete". Accepted and measured, never designed around — the Follow-up reads the field and improves the prompting.
- The multi-chunk refusal at `service.go:951` was correct under the old model and becomes wrong here. Re-scope it deliberately; leaving it would make correction impossible for exactly the long documents that most need it.

## Stop Condition

**Answered 2026-08-27 — remove them, no deprecation window.** An agent doing a retraction currently
gets an erasure, and keeping the verb live for one more release keeps the defect live for one more
release. The refusal text must name the operator path, or an agent that cannot delete will file a
duplicate instead.

Stop and ask if the anchor carry turns out to need a server-side tree read after all — it must not,
because the server has no repository, and if the design drifts that way the decision above is
unimplementable as written.

## Out of Scope

- Making recall hide ended records — T5. This task ends them; nothing filters yet, so an ended record is still returned until T5 lands. Say so in the commit, because a half-landed pair looks like a bug.
- Structured reasons — a taxonomy (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
- 2026-08-27 · 3800814* · exit 0 · `go test ./internal/palace/ ./internal/mcpserver/ ./cmd/server/ -run 'TestCorrectingAMemorySupersedesIt|TestTheEndedTextIsStillReadableById|TestUpdateWithoutAReasonIsRefused|TestUpdateReturnsTheNewIdNamingTheOneItEnded|TestUpdateAppliesAnchorsToTheCorrectingRecord|TestInvalidateDrawerEndsWithNoSuccessor|TestKgInvalidateRequiresAReason|TestKgSupersedeIsReachableFromTheToolSurface|TestDestructiveToolsAreAbsentFromTheAgentCatalogue|TestAdminOffersNoErasure|TestASupersedeCarriesAnchorsAsUnchecked|TestKgSupersedeLeavesNoBoundaryOverlap|TestKgSupersedeRecordsWhyTheOldFactEnded|TestKgSupersedeIsAtomic|TestDrawerEraseNeedsTheIdSpelledTwice|TestDrawerEraseRemovesEveryChunk' -count=1 2>&1 | tee /tmp/acc38t4a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t4a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t4b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t4b.out` · acceptance-sha256:615bd85ff75149f9ea1b43bed7504b476922767aa87861b9114c286158995e13
