# Task ADR-044-T6: Guarantee a caller never joins chunks, and take the tag off

**Depends-on:** T5
**Covers:** F-4, UC1-S3
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** tag removal from `internal/mcpserver/readcost_spec_test.go`
**Consumes:** `withheld` page field (T5)
**Data dependency:** hermetic — driven by a memory constructed several times longer than `ChunkSize`.

## Goal

Guarantee that a recall matching text inside a later chunk returns one hit whose content is the memory's content, with no caller-side reassembly, and put all four `mcpserver` bindings into the default test lane.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit or confirm | ADR-013 collapses a page to one hit per memory and ADR-024 ranks memories rather than chunks, so this may already hold. The binding's kill-case names what would break it: *"rendering `h.Drawer.Content` in place of the memory's content, so a match in a later chunk returns only that chunk."* Confirm which is true before writing code — if the guarantee already holds, this task PINS it rather than building it, and says so |
| `internal/mcpserver/readcost_spec_test.go` | edit | Turn `TestF4ChunkingCreatesNoReassemblyObligation` green **and remove `//go:build readcostspec`**. T6 is the last task in this file, so F-1, F-2, F-4 and F-7 enter the default lane together |
| `internal/mcptest/regions_test.go` | read only | `:193-199` reads `hit["chunk_index"]` over the real MCP transport. F-4 retains chunk metadata as diagnostics, so this test must keep passing — it is the evidence that retention is real rather than asserted |

## Deviations recorded during execution

**1. STEP 2 RESOLVED: the guarantee ALREADY HOLDS, so this task PINS it and no behaviour changed.**
`registerSearch`'s render loop assigns `fullContent := h.MemoryContent` and falls back to
`h.Drawer.Content` only when the memory content is empty — so ADR-013's collapse and ADR-024's
memory-level ranking already deliver the memory rather than the matching chunk. The assertions went
GREEN the moment they were written. Step 2 named that outcome as acceptable in advance, and
inventing a change to justify the task would have been the wrong repair.

**Consequence for evidence: there is no red-then-green pair here, and there cannot be.** What proves
the binding is the mutant it names — render `h.Drawer.Content` in place of the memory's content —
which is in the Mutation Log, tool-written, killed. A test written against already-correct code is
exactly the shape that proves nothing without one.

**2. THE FIXTURE POSES THE QUESTION, verified rather than assumed.** Measured 2026-08-29: the memory
stores at 8,137 runes across **7 chunks**, the query matched `chunk_index=4` with
`chunks_matched=7`, and the hit returned all 8,137 runes. A one- or two-chunk fixture would make
"the chunk" and "the memory" the same string and every assertion would pass without ever asking
F-4's question, so the test fails fast below three chunks. The needle appears in the LAST chunk and
the opening marker in the first; asserting both is what makes the check two-directional, since
either alone passes on the chunk that matched.

**3. THE TAG-RESTORATION MUTANT IS NOW TOOL-WRITTEN, AND LANDS AS `inconclusive` — twice blocked,
for two different and both-defensible reasons.** Recorded here in full because the first half of this
paragraph went false within hours of being written, which is the class this record exists to remove.

**First block, since FIXED upstream.** `adr-verify --mutant` refused the mutation as *comment-only*:
`//go:build readcostspec` is lexically a comment and semantically a build constraint, so restoring it
removes four tests from the lane CI runs — a larger behavioural change than most real mutants. Hand-
verified at the time, both directions:

```
$ # tag restored
$ go test ./internal/mcpserver/ -run 'TestF4ChunkingCreatesNoReassemblyObligation' -count=1
ok  github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver  0.019s [no tests to run]
$ echo $?
0
```

Reported upstream and **shipped in quality-harness v2.36.0** as an explicit directive list
(`//go:build`, `// +build`, `//go:generate`, `//go:embed`, `//go:linkname`, `//nolint`,
`# type: ignore`, `# noqa`, `// eslint-disable`, `/* istanbul ignore */`, a shebang) rather than a
heuristic — because "a comment whose first token ends in a colon" would swallow ordinary prose, and a
guard that exists to REFUSE mutants must not get a loose exemption.

**Second block, live as of v2.36.0 and reported.** The mutation now RUNS and is recorded, but as
`mutant inconclusive` rather than `killed`, with the reason *"the fence failed but scored no tests"*.
Both rules are individually right and they collide precisely here: the scored-no-tests rule exists to
catch a fence whose filter matches nothing and passes vacuously, while THIS mutant's entire signal is
that no tests ran — the fence detects it correctly, by grepping for `no tests to run`, and fails.

**So the class is: any mutant whose effect is to REMOVE tests from a lane is reported inconclusive,
however correctly the fence catches it.** The distinguishing question the classifier does not ask is
whether the fence scored no tests and PASSED (vacuous — inconclusive is right) or scored no tests and
FAILED BECAUSE IT DETECTED THAT (a kill). The exit code and the fence's own grep already separate
them.

The entry is left in the Mutation Log as `inconclusive` rather than argued away: it is accurate about
what the tool currently concludes, and rung 2's claim rests on the hand-run above until the classifier
can tell the two cases apart.

**4. A REPO GATE CAUGHT THE FIXTURE'S WING NAME, correctly — TWICE.** The first draft named the
fixture wing after this task. `TestNoRealProjectNamesInWings` refused it: a wing name is a project
name, and an undeclared one in the tree is either somebody's real project or an example nobody
declared. Fixed by reusing `budgetWing`, the neutral example this package already carries, rather
than widening `exampleWings` — an allowlist should not grow to accommodate a fixture that had no
reason to be named after its task.

Then it fired a SECOND time, on this very paragraph: the gate reads `docs/` as well as `.go`, so
writing the rejected name here to explain the fix reintroduced it. That is the gate working — the
name is as much a leak in prose as in source — and it is recorded rather than worked around,
because the reflex it corrects (quote the bad value when documenting the fix) is one every
deviation note invites.

## Ordered Steps

1. Confirm `TestF4ChunkingCreatesNoReassemblyObligation` is red for the right reason. Verified 2026-08-29.
2. **Determine whether the guarantee already holds** under ADR-013 + ADR-024 + `am_get_drawer whole=true`. If it does, the honest outcome is a pinning test and a written statement that no behaviour changed — not a change invented to justify the task. If it does not, fix the render path.
3. Assert chunk metadata survives: `memory_id`, `chunks_matched`, `chunk_index`, `parent_id` remain on the wire as diagnostics (ADR-024 compatibility, spec Contracts Touched).
4. **Remove the build tag from `internal/mcpserver/readcost_spec_test.go`.** Run the package untagged and confirm all four bindings are green before claiming the fence.
5. Re-run the whole default lane, since this is the commit that exposes four previously-hidden tests to CI.

## Acceptance

```bash
set -o pipefail
go test ./internal/mcpserver/ -run 'TestF4ChunkingCreatesNoReassemblyObligation' -count=1 2>&1 | tee /tmp/adr044-t6.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t6.out && go test ./internal/mcpserver/ -run 'TestF1CoverageCountsEveryDisclosedRange|TestF2NoHitIsSilentlyPartial|TestF7APageReportsWhatItWithheld' -count=1 && go vet ./... && go test ./... -count=1
```

No `-tags readcostspec` anywhere: the fence is red until step 4 removes the tag, so it proves the
removal as well as the behaviour. The three sibling bindings run untagged in the second command,
which is what catches a removal that exposes a test CI cannot reproduce.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF4ChunkingCreatesNoReassemblyObligation` | `internal/mcpserver/readcost_spec_test.go` | A match in a later chunk returns the memory's content, not that chunk's | F-4, UC1-S3 |
| `TestF4ChunkingCreatesNoReassemblyObligation/chunk_metadata_survives_as_diagnostics` | same | `chunk_index` and `parent_id` are retained, as a subtest inside the fence | F-4 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF4ChunkingCreatesNoReassemblyObligation` |
| 2 — something selects it | The tag removal is the selection: until it happens, none of these four tests runs in the lane CI executes (`.github/workflows/build.yml:65`). Mutation: restore the tag and watch the fence go red |
| 3 — the caller can discover it | `chunk_index` is already described; no new key is added — `n/a: no new declared interface` |
| 4 — it is used | `internal/mcptest/regions_test.go:193-199` reads `chunk_index` over the real transport, which is the only observed consumer |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->
- 2026-08-29 · 089323d · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the binding's named kill-case: render the matching CHUNK's content in place of the memory's, so a match in a later chunk returns only that chunk and the caller must fetch and join its siblings to obtain the memory · acceptance-sha256:846eb0d04b9d69f3dd82c48954953bd4db3630c3059570f6c8ba07b31a48d9e6
- 2026-08-29 · 25cd90b · mutant inconclusive · exit 1 · `internal/mcpserver/readcost_spec_test.go` · the Reachability rung-2 mutation: restore the build tag. All four bindings vanish from the lane CI runs and go test reports [no tests to run] at exit 0 — a green run over a suite that executed nothing, which is why the fence greps for that string rather than trusting the exit code · acceptance-sha256:846eb0d04b9d69f3dd82c48954953bd4db3630c3059570f6c8ba07b31a48d9e6
  ```
  the fence failed but scored no tests
  ```

## Invariants

- Chunking may remain the embedding and matching unit. F-4 constrains what a caller RECEIVES, not how retrieval finds it.
- `memory_id`, `chunks_matched`, `chunk_index` and `parent_id` stay on the wire.
- After this task, no binding in `internal/mcpserver/readcost_spec_test.go` is behind a build tag.

## Risks

- The task discovers there is nothing to build and gets padded with invented work. Mitigated by step 2 making "it already holds, here is the pin" an explicitly acceptable outcome.
- Removing the tag exposes T3/T4/T5's bindings to CI for the first time. Mitigated by step 4 running them untagged before the claim.

## Stop Condition

Stop if removing the tag makes any of the four bindings fail in the default lane for an environmental reason — a fixture needing a live palace, or a timing assumption. A binding that is green only under a tag is not green; bring it to the owner rather than re-adding the tag quietly.

## Out of Scope

- Changing what matches, or how chunking works (permanent: the spec makes this a Non-Goal — chunking may remain the embedding and matching unit, and F-4 is about the caller's obligation only)
- The `palace` and `repohygiene` tags — T7 and T2 own those

## Verification Log

<!-- Tool-written by `adr-verify`. -->
- 2026-08-29 · 089323d* · exit 1 · `set -o pipefail …` · acceptance-sha256:846eb0d04b9d69f3dd82c48954953bd4db3630c3059570f6c8ba07b31a48d9e6
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.024s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	0.453s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.013s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.020s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.447s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.020s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.122s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.018s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.018s
  FAIL
  ```
- 2026-08-29 · 089323d* · exit 0 · `set -o pipefail …` · acceptance-sha256:846eb0d04b9d69f3dd82c48954953bd4db3630c3059570f6c8ba07b31a48d9e6
