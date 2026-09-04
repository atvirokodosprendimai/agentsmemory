# Task ADR-053-T4: A fetch carries the facts about what it returns

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** S (single file plus its test)
**Owner:** unassigned
**Produces:** `am_get_drawer` `facts`
**Consumes:** `boundGraphPage` (T1), `withheld` keyed by cause (T1), `isContainmentEdge` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the exit code`, `the facts block obeying the same budget as the drawer content`

## Goal

Put the graph context on the call a caller makes AFTER deciding to read a memory,
where today it is only on the call they made before.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | render `facts` on the `am_get_drawer` response through the same budget helper, and describe the new block |
| `internal/mcpserver/getdrawerfacts_test.go` | add | the gate, including the incoming-correction case that is the block's highest-value content. ⚠It is a NEW file in package `mcpserver_test`, not an edit to `drawers_test.go`: that file is package `mcpserver`, and driving the tool surface needs `internal/mcptest`, which imports this package — an import cycle from inside it |

## Ordered Steps

1. [S1] Write `TestAFetchCarriesTheFactsAboutItsDrawer` first: file a drawer, file a fact naming it, fetch by id, and assert the fact arrives. It fails today because `out["facts"]` exists only on the search page (TDD red).
2. [S2] Render the block through T1's `boundGraphPage`, so the drawer's facts spend the same budget as everything else and report through the same `withheld` map. ⚠Spend the budget on the facts BEFORE trimming the drawer's own content: a fetch whose content was trimmed to make room for its facts has inverted the caller's request, which was for the memory.
3. [S3] Apply T2's containment default here too, so fetching a drawer does not drag in its room's listing. A fetch that returns 184 sibling edges is the same defect this record exists to remove, arriving through a different door.
4. [S4] Include the INCOMING edges, not only the outgoing ones. A correction attaches to the record it corrects as an incoming edge, so an outgoing-only block would omit exactly the fact `start-here` says every leaf fetch must check — and it would look complete while doing it.
5. [S5] Describe the block in the tool description, naming that it is bounded and that corrections arrive here. An `omitempty` field is absent by construction until the case that produces it, so a caller who has never fetched a corrected drawer has no way to learn the block exists. [proof: acceptance]
6. [S6] ⚠**The mutant is the incoming half dropped**, leaving outgoing edges only. `TestAFetchSurfacesAnIncomingCorrection` must go red — a facts block that silently omits corrections is worse than none, because the caller reads a retracted memory believing they checked. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/mcpserver/ -run 'TestAFetchCarriesTheFactsAboutItsDrawer$|TestAFetchSurfacesAnIncomingCorrection$' -count=1 2>&1 | tee /tmp/adr053-t4a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr053-t4a.out \
  && go test ./internal/mcpserver/... -count=1 2>&1 | tee /tmp/adr053-t4b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr053-t4b.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestAFetchCarriesTheFactsAboutItsDrawer` | `internal/mcpserver/getdrawerfacts_test.go` | `am_get_drawer` returns a bounded `facts` block for a drawer named by a fact | — | S1, S2, S5 |
| `TestAFetchSurfacesAnIncomingCorrection` | `internal/mcpserver/getdrawerfacts_test.go` | a `retracts`/`supersedes`/`qualifies` edge pointing AT the drawer arrives in the block, and containment edges do not | — | S3, S4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | the `am_get_drawer` handler renders the block; deleting the render turns `TestAFetchCarriesTheFactsAboutItsDrawer` red |
| 3 — the caller can discover it | the tool description names the block and says corrections arrive in it — the only route, since the field is absent until a fact exists |
| 4 — it is used | every by-id fetch renders it, and the corpus holds live `qualifies` and `supersedes` edges that exercise the correction case today |

## Mutation Log

- 2026-09-04 · ab56495* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the incoming half dropped: a correction attaches to the record it corrects as an INCOMING edge, so an outgoing-only block looks complete while omitting every retracts/supersedes/qualifies — the caller reads a superseded memory believing they checked · acceptance-sha256:7bae97de17a092e6c0c8c0a64868dc70f6e7cc3de07140ad74e87d3766d8536f

## Invariants

- The drawer's own content is never trimmed to make room for its facts.
- Incoming edges are included; an outgoing-only block would omit every correction.
- Containment edges are excluded here by the same rule as T2, so a fetch cannot become a room listing.

## Risks

- A fetch becomes measurably more expensive. Mitigated by the shared budget and by the block being absent when no fact names the drawer, which is the majority case in a corpus where 90 of 196 triples resolved to a drawer when ADR-036 measured it.
- A caller that parsed the fetch response strictly sees a new key. Named in the ADR's Wiring table; the block is `omitempty`, so a drawer with no facts is byte-identical to today.

## Stop Condition

Stop and ask if the facts block cannot be bounded without trimming the drawer's
own content on a realistic memory. That would mean the budget is too small to
carry both, which is a question about the budget rather than about this block,
and it belongs to whoever owns that constant.

## Out of Scope

- Returning facts from `am_list_drawers` (deferred: `docs/adr/BACKLOG.md`)
- Returning facts whose only tie to the drawer is `source_drawer_id` — provenance rather than reference (deferred: `docs/adr/BACKLOG.md`)
- Changing which facts `am_search` returns (permanent: boundary: ADR-036 decided that shape and this task adds a second reader of it, not a second definition)

## Verification Log
- 2026-09-04 · ab56495* · exit 0 · `set -o pipefail …` · acceptance-sha256:7bae97de17a092e6c0c8c0a64868dc70f6e7cc3de07140ad74e87d3766d8536f · ms:5209
