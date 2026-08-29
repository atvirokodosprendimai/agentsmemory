# Task ADR-044-T4: Make every incomplete hit say so, with its full length and fetch id

**Depends-on:** T3
**Covers:** F-2, UC1-S2
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `partialWithFetchID` — the marking a hit carries when it is not whole
**Consumes:** `coveredRunes` (T3)
**Data dependency:** hermetic — driven by memories constructed larger than a fixture budget.

## Goal

Make a hit that does not carry its whole memory report that fact, its full rune length, and the id that fetches the rest — never a fragment a caller cannot tell is a fragment.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | Two paths already set `Truncated` + `FullLength` (`:926-928` for the over-budget fallback, `:937-940` for the head bound). Unify them into one marking that also carries the fetch id, and extend it to the case neither covers: a memory larger than the response budget, which is **always** partial rather than made whole by growing the budget for it |
| `internal/mcpserver/drawers.go` | edit | The `am_search` description at `:817` names `content_truncated` and `content_length` as fields that *"appear only when they apply"*. If the fetch id becomes a new `omitempty` key it must be described here, or `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` fails — which is the gate working |
| `internal/mcpserver/readcost_spec_test.go` | edit | Turn `TestF2NoHitIsSilentlyPartial` green. **Tag STAYS** — F-4 and F-7 are still red in this file |

## Ordered Steps

1. Confirm `TestF2NoHitIsSilentlyPartial` is red for the right reason. Verified 2026-08-29: the binding names its own kill-cases — *"restoring a silent fragment, or an off-by-one in the reported length"*.
2. Enumerate the shapes the render path can already produce before writing anything, because this task operates on an existing path: a hit whole and untrimmed; trimmed by the head bound; falling back to a window because the whole memory exceeded the remaining budget; a memory larger than the ENTIRE budget so no window choice helps; and `snippet_chars` supplied by the caller, which is unclamped and can itself be smaller than the memory.
3. Introduce the single marking. The fetch id is the memory's id — `am_get_drawer(id, whole: true)` is the completion path, and F-2's contract is that this is the ONLY one: `am_search` gains no cursor (spec Non-Goals, Grill Log 8).
4. Assert the reported length is the memory's full rune length, not the chunk's and not the rendered window's. The off-by-one is the binding's named kill-case.
5. Confirm the description names any new key.

## Step 2 — the shapes the render path actually produces, measured

Probed 2026-08-29 through the real transport (`internal/mcptest`), on a 60,238-rune memory stored as
47 chunks, against `responseBudget = 40_000`:

| Call | `content_truncated` | `content_length` | content returned |
|------|--------------------|------------------|------------------|
| `am_search`, `snippet_chars: 400` | `true` | 60237 | 401 runes |
| `am_search`, `snippet_chars: 0` | `true` | 60237 | 401 runes |
| `am_search`, `snippet_chars: 100000` | `true` | 60237 | 40000 runes |
| **`am_get_drawer(id)`, no `whole`** | **absent** | **absent** | **1600 runes — one chunk of 47** |

**The search path already marks every partial it produces, and that is the finding.** All three
snippet shapes come back marked with the memory's full length, including the caller-supplied
unclamped `snippet_chars` that was the suspected hole. So F-2's search half is largely already met,
and unifying the three marking sites is a tidy-up rather than a fix.

**The live silent fragment is `am_get_drawer` without `whole: true`.** It returns one chunk of a
47-chunk memory with `content_truncated` and `content_length` both ABSENT, `chunk_index: 0` and
`parent_id` absent — byte-for-byte indistinguishable, to a caller, from a complete 1,600-rune memory.
This is the exact defect the team's own operating protocol patches in PROSE: *"⚠ `am_get_drawer`
RETURNS ONE CHUNK, AND IT LOOKS COMPLETE. Nothing marks the fragment as partial."* A warning an agent
must have read is what F-2 exists to replace with a field.

**Why this is F-2 and not F-4, given T3 routed `am_bootstrap`'s identical-looking residual to T6.**
The distinction is whether the vocabulary exists. `am_get_drawer` renders through `toView`, which
already HAS `Truncated` and `FullLength` — they are simply never set on this path, so F-2's remedy is
"apply the existing marking to the case it does not cover". `am_bootstrap` renders through
`WireShape()` and has no per-record partial vocabulary at all, so marking it means deciding whether a
row is a memory, which is F-4's question. Stated here because two identical-looking residuals routed
to different tasks is exactly the kind of split that reads as inconsistency a year later.

**NOT a finding, checked and dismissed:** `content_length` read 60237 against a 60238-rune fixture.
That is `add_drawer` trimming the fixture's trailing space at write, not a reporting off-by-one —
verified by fetching the memory whole. The binding names an off-by-one as a kill-case, so it is worth
recording that the corpus was checked for one and does not have it.

## RESOLVED — the cost question, and why option 1 stopped being expensive

Marking `am_get_drawer`'s chunk needs the memory's full rune length, and **nothing on the row carries
it**. Reassembly removes chunk overlap, so it is not the sum of the stored chunk lengths; computing
it exactly means loading every chunk — which is precisely what `whole: true` already does. So the
honest options are:

1. Load the chunks to compute the length, making an unmarked cheap read into a read that costs what
   the expensive one costs. Defeats the record's own purpose.
2. Mark `content_truncated` and omit `content_length`, against `drawerView`'s standing comment:
   *"Both fields or neither: 'truncated' without the original length tells a caller something is
   missing and not how much, which is not enough to decide whether to fetch it."*
3. Add a cheap metadata query (chunk count, and a stored length if one can be maintained) — a
   `palace` change, outside this task's declared Affected Files.

**Resolved as option 1, and the objection to it was mispriced.** "Defeats the record's own purpose"
assumed the purpose is that `am_get_drawer` be CHEAP. It is not — the record retracted that framing
before it was written. The purpose is that a small read be TRUSTWORTHY, and the asymmetry it rests on
is that server-side bytes are cheap while an agent's context is not. Loading a memory server-side to
report one number, and returning one chunk, spends in the right direction.

The parity argument is what settles it rather than the principle. The SEARCH path already reassembles
every candidate memory on every recall (`internal/palace/memory_search.go`, `representative.MemoryContent`),
so this is the cost of a path already measured, not a new class of it. `palace.MemorySize` reuses
`GetMemory` and `reassembleMemory` rather than adding a second reassembly.

Option 2 is rejected for the reason it was flagged: `drawerView`'s comment forbidding a lone
`content_truncated` is honoured here, not repealed. Option 3's stored-length column is not needed and
would have been a migration — 00037 stays free, as the ADR's Rollback section reserves.

**Why the sum of the chunks is not the answer, measured rather than assumed:** a 60,237-rune memory
is stored as 47 chunks whose lengths sum to roughly 75,200. Chunk overlap is removed at reassembly,
so the sum is an upper bound. Reporting it would be a wrong number rather than a missing one, which
is the direction this record exists to avoid.

## Additions to Affected Files, recorded rather than made silently

| File | Change | Why it was not in the table at authoring |
|------|--------|------------------------------------------|
| `internal/palace/service.go` | add `MemorySize` | The table assumed F-2 was a transport-only change, because it assumed the defect was on the search path. The live defect is on `am_get_drawer`, whose marking needs the memory's length — a `palace` question, since `reassembleMemory` is `palace`'s and duplicating it in `mcpserver` would be the second implementation this codebase's own comment on `CorrectionsFor` warns about |
| `internal/mcptest/regions_test.go` | add `TestScenarioAFetchedChunkSaysItIsOne` | Same reachability constraint as T3: `mcptest` imports `mcpserver`, so the F-2 binding can drive the marking but never the handler that selects it. The ROOT-chunk mutant is killed there and NOT by the binding |

**Rung 3 has no mechanical backstop here, and that is a deliberate outcome of reusing `memory_id`.**
No new `omitempty` key was added — the fetch id is the id the caller already passed, and
`am_get_drawer(id, whole: true)` is the completion path — so
`TestEveryOmitemptyWireKeyInThisPackageIsDescribed` does not fire. The `am_get_drawer` description was
extended by hand to state that an unmarked response is complete and a marked one is not. Written down
because a requirement no gate can see is exactly how a task goes green with its work unmet — the same
shape as T3's step 5.

## Acceptance

```bash
set -o pipefail
go test -tags readcostspec ./internal/mcpserver/ -run 'TestF2NoHitIsSilentlyPartial' -count=1 2>&1 | tee /tmp/adr044-t4.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t4.out && go test -tags readcostspec ./internal/mcpserver/ -run 'TestF1CoverageCountsEveryDisclosedRange' -count=1 && go vet ./... && go test ./... -count=1
```

T3's binding is re-run separately rather than folded into one filter: a fence naming both could be
satisfied by the already-green one alone, which is the aggregate-gate hole the task template records.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF2NoHitIsSilentlyPartial` | `internal/mcpserver/readcost_spec_test.go` | A hit that is not whole is marked, reports full rune length, and carries the fetch id | F-2, UC1-S2 |
| `TestF2NoHitIsSilentlyPartial/a_memory_larger_than_the_whole_budget_is_still_marked` | same | The case no existing path covers, as a subtest inside the fence | F-2 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF2NoHitIsSilentlyPartial` |
| 2 — something selects it | The marking is applied in the render loop every `toView` call site passes through. Mutation: skip the marking on the over-budget branch and watch the test go red |
| 3 — the caller can discover it | The `am_search` tool description must name the fetch-id key; `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` fails otherwise. This is the rung that is normally missed |
| 4 — it is used | T1's counting rule counts reads acted on without a second call — a correctly marked partial is precisely a read that needs one |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->
- 2026-08-29 · de0c6a5* · mutant inconclusive · exit 1 · `internal/mcpserver/drawers.go` · the ROOT-chunk mutant: keying the marking on ParentID instead of the chunk count leaves chunk 0 of a 47-chunk memory unmarked — the exact case measured on 2026-08-29, and the one a reader would implement by reflex. F-2 kill-case: restoring a silent fragment · acceptance-sha256:1ac2b0e23e0901ac96a0fc051401bb605073cf9484260276d8d8c0328cd03bcb
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-08-29 · de0c6a5* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the off-by-one the binding names: reporting the FRAGMENTS length instead of the memorys, so a caller comparing what it holds against what exists concludes it holds all of it · acceptance-sha256:1ac2b0e23e0901ac96a0fc051401bb605073cf9484260276d8d8c0328cd03bcb
- 2026-08-29 · de0c6a5* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the ROOT-chunk mutant: also requiring a ParentID leaves chunk 0 of a 47-chunk memory unmarked — the exact case measured on 2026-08-29, and the one a reader would implement by reflex since a root chunk has no parent and chunk_index 0. F-2 kill-case: restoring a silent fragment · acceptance-sha256:1ac2b0e23e0901ac96a0fc051401bb605073cf9484260276d8d8c0328cd03bcb
- 2026-08-29 · de0c6a5* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · a silent fragment restored: the length is reported and the flag is not, so a caller scanning for content_truncated sees nothing and reads the response as complete. drawerViews own comment requires both fields or neither · acceptance-sha256:1ac2b0e23e0901ac96a0fc051401bb605073cf9484260276d8d8c0328cd03bcb

## Invariants

- A memory larger than the response budget is ALWAYS partial-with-fetch-id. The flag is never conditional on record size.
- The completion path is `am_get_drawer`. No cursor or offset is added to `am_search`.
- `content` itself is unchanged in meaning — ADR-019's decision stands.

## Risks

- Growing the number of marked hits makes pages look worse than before. That is the point: the pages were already this short and did not say so. F-7 (T5) supplies the page-level half.
- A new `omitempty` key is invisible to a caller who never hits the case. Mitigated by the description requirement, which an existing gate enforces on a word boundary.

## Stop Condition

Stop if the fetch id available at render time does not address the whole memory — if it names a chunk rather than the memory, `am_get_drawer(id, whole: true)` would return the wrong thing and F-2's contract would be a pointer to the wrong object. ADR-038 made the id opaque and minted once, so verify against `toView` before building on it.

## Out of Scope

- The page-level withheld count, which is T5
- Whether chunking is visible at all, which is T6
- Removing the build tag — T6 owns it

## Verification Log

<!-- Tool-written by `adr-verify`. -->
- 2026-08-29 · e17db6a · exit 1 · `set -o pipefail …` · acceptance-sha256:1ac2b0e23e0901ac96a0fc051401bb605073cf9484260276d8d8c0328cd03bcb
  ```
  --- FAIL: TestF2NoHitIsSilentlyPartial (0.00s)
      readcost_spec_test.go:135: not built yet — F-2 (UC1-S2): a hit that does not carry its whole memory must say so, report the full length, and carry the id that fetches the rest — never a fragment a caller cannot tell is a fragment. Note `am_search` has limit but no cursor (drawers.go:786-800), so 'fetch the rest' means am_get_drawer, not paging. Kill it by restoring a silent fragment, or by an off-by-one in the reported length
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.019s
  FAIL
  ```
- 2026-08-29 · de0c6a5* · exit 0 · `set -o pipefail …` · acceptance-sha256:1ac2b0e23e0901ac96a0fc051401bb605073cf9484260276d8d8c0328cd03bcb
