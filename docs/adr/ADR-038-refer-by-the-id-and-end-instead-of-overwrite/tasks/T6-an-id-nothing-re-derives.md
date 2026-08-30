# Task ADR-038-T6: A gate that fails when an id is re-derived, or a mint path forgets its key

**Depends-on:** T5
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `TestNoPathRederivesADrawerID`, `TestEveryDrawerMintWritesAContentKey`, and `doctor --corpus`
**Consumes:** `Repo.Save` upserting on `(team_id, content_key)` (T3); current-only recall (T5)
**Data dependency:** hermetic for both source gates. `doctor --corpus` needs a real database by definition; its unit tests are hermetic and build the drift they assert on.

## Goal

The two properties this ADR decides are checked by exit code rather than by prose: nothing re-derives
a drawer id, and no mint path writes a drawer without its content key. And the drift they prevent
becomes something an operator can ASK about, instead of a script somebody has to think to write.

**Why the third one is not optional.** Every finding behind this ADR — 27 drifted rows, 39 of 41
anchored drawers exposed to a re-file, 16 knowledge-graph facts pointing at a drawer that no longer
exists — was produced by a throwaway script on 2026-08-27 and by nothing in the tree. `doctor`
already exists and already carries three integrity checks that exit non-zero on a finding
(`--index`, `--schema`, `--roles`), and not one of them reads the corpus. A drift query recorded in
a sign-off line is the state that let those 27 rows sit unnoticed; it is rung 3 of this repo's own
ladder, where the capability exists and its intended caller cannot discover it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/identityrole_test.go` | add | both gates |
| `internal/palace/chunk.go` | edit | `DrawerID`'s doc comment names the one legal use, so the gate's rule is readable where the function is |
| `cmd/server/doctor.go` | edit | THREE edits, not one. (a) the `--corpus` flag; (b) its line in the Description's integrity block — **the line that makes the check discoverable**; (c) **the dispatch guard at `:58`**, which refuses unless one of `--index/--graph/--roles/--schema/--windows` is set, so `--corpus` alone exits with *"nothing to check"*. And `--index` `return`s at `:83` rather than falling through, so a `--corpus` block placed after it is unreachable when both flags are passed |
| `cmd/server/doctorcorpus.go` | add | the check: rows whose `content_key` disagrees with the hash of their own fields, and, since it is walking the corpus anyway, references that no longer resolve — `parent_id`, `drawer_anchors.drawer_id`, `kg_triples.source_drawer_id` (16 dangling, measured 2026-08-27) — and, now that endings exist, it must distinguish ENDED from LOST. An ended row is the system working; a dangling pointer is not; and a KG fact whose `source_drawer_id` points at an ENDED drawer is **also** the system working (T5's decision — provenance is historical), so the check reports three states, not two. Conflating any pair of them reports the feature as a fault. Ids and counts only, never memory text: a doctor report gets pasted (`doctor.go:92`) |
| `AGENTS.md` | edit | the Reachability section lists the gates in this tree and `TestAgentsMdNamesGatesThatExist` pins it — new gates mean new lines, in this commit |

## Ordered Steps

1. Write the failing test(s) first and confirm they are RED — both gates, before any implementation:
   - `TestNoPathRederivesADrawerID` parses `internal/palace/*.go` (go/ast, not grep — a comment
     mentioning the name must not satisfy or trip it) and fails when `DrawerID(...)` appears anywhere
     other than an assignment to a `ContentKey` field or to a variable passed as one. Confirm it is
     red by adding a `DrawerID` call used as a lookup and watching it fail.
   - `TestNoCommentClaimsADrawerIdIsDerivedFromItsContent`, in **two parts, because the first draft of
     this gate was 3-for-5 against the very instances that motivated it** (found by review
     2026-08-27, and verified by running its four phrases against the tree: `00006:18` reads
     `deterministic hash(team,…)` with no *"of"*, and `DrawerID`'s comment says *"the deterministic
     IDENTITY of a drawer"* — **zero of the four phrases match either**. Reverting either fix would
     have left the gate green: a test that cannot fail, aimed at the mechanism meant to end that
     shape. It was also over-inclusive the other way — the same phrases hit four CSS-cache-buster
     comments outside `internal/palace`, so it would have shipped four allowlist entries about
     stylesheet hashing to guard one claim about drawer ids, which is the noise that gets an
     allowlist rubber-stamped):

     **(a) Declaration-anchored, and exhaustive by construction.** There are exactly THREE
     declarations where the claim "a drawer id is derived from its content" can be made: the
     `Drawer.ID` field (`palace.go:19`), the `id` column in `00006_drawers.sql:18`, and `DrawerID`'s
     own doc comment. Assert each against its declaration — `internal/doclint` already resolves a
     comment to the declaration it sits on. Three known sites cannot be under-inclusive; a phrase
     list over a package is a guess about wording.

     **(b) A phrase sweep scoped to `internal/palace/` and `db/migrations/` only**, for the
     incidental mentions that sit on OTHER declarations — `service.go:679` and `repo.go:98` are
     comments about `purgeSource` and `SaveUnembedded` that happen to assert the id's nature, and
     part (a) cannot see them. Scoping kills all four false positives. Allowlist entries carry a
     written reason, as `notOperatorFacing` does.

     Run both against the tree BEFORE the fixes land: (a) must be red on all three, (b) red on the
     other two. A gate whose motivating instances it cannot see is worse than no gate.
   - `TestDoctorCorpusIsReachable` — run `doctor --corpus` ALONE and assert it does not exit with
     "nothing to check", then run it WITH `--index` and assert both reports appear. **`TestEveryFlagIsRead`
     passes either way**: `--corpus` is read, in a block nothing can reach. Only a dispatch test sees this.
   - `TestEveryDrawerMintWritesAContentKey` **derives its universe** from the source: every composite
     literal of type `palace.Drawer` that sets `ID` must also set `ContentKey`. Derived, not
     hand-listed, so a mint path added tomorrow joins the check on the same commit. The diary mint
     sets `ContentKey: ""` explicitly, which satisfies the gate and documents the exemption at the
     site rather than in a list beside it.
2. Make them pass.
3. Add `doctor --corpus`, exiting non-zero on a finding exactly as `--index` and `--schema` do, and
   add its line to the Description's integrity block so `--help` advertises it. Its unit tests build
   a drifted row and a dangling reference and assert each is reported — a check whose fixture cannot
   exhibit the defect is unfalsifiable however it is worded.
   Run it against the real corpus and record the numbers in the sign-off. On 2026-08-27 the ad-hoc
   equivalents found 27 drifted of 1,705 and 16 dangling `source_drawer_id` of 207 facts.
4. Add the two gate names to `AGENTS.md`'s list and confirm `TestAgentsMdNamesGatesThatExist` passes.
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestNoPathRederivesADrawerID|TestEveryDrawerMintWritesAContentKey|TestNoCommentClaimsADrawerIdIsDerivedFromItsContent' -count=1 2>&1 | tee /tmp/acc38e.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38e.out && go test ./cmd/server/ -run 'TestDoctorCorpus'  -count=1 2>&1 | tee /tmp/acc38e.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38e.out && go test ./... -count=1 2>&1 | tee /tmp/acc38f.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38f.out
```

The whole tree runs in the second command because this task edits `AGENTS.md`, which
`TestAgentsMdNamesGatesThatExist` reads from another package.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestNoPathRederivesADrawerID` | `internal/palace/identityrole_test.go` | the id is never recomputed for a lookup or a comparison | — |
| `TestEveryDrawerMintWritesAContentKey` | `internal/palace/identityrole_test.go` | a mint path that forgets the key fails the build's gate, derived from the source | — |
| `TestDoctorCorpusReportsDriftAndDanglingReferences` | `cmd/server/doctorcorpus_test.go` | a drifted row and each kind of dangling reference are reported and exit non-zero | — |
| `TestDoctorCorpusIsAdvertisedInHelp` | `cmd/server/doctorcorpus_test.go` | the flag appears in the Description's integrity block — rung 3, and the only rung a behavioural test cannot reach | — |
| `TestNoCommentClaimsADrawerIdIsDerivedFromItsContent` | `internal/palace/identityrole_test.go` | documentation stops going false one instance at a time — the allowlist entry is the review | — |
| `TestDoctorCorpusIsReachable` | `cmd/server/doctorcorpus_test.go` | `--corpus` alone is dispatched, and `--corpus` with `--index` yields both — **rung 2**, invisible to `TestEveryFlagIsRead`, which passes while the flag is read in unreachable code | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | both gates run and pass |
| 2 — something selects it | `go test ./...` runs them; mutation: add a `DrawerID` lookup and a `Drawer{ID: ...}` literal with no `ContentKey`, and each gate goes red for its own reason |
| 3 — the caller can discover it | `doctor --help`'s integrity block names `--corpus`, asserted by `TestDoctorCorpusIsAdvertisedInHelp`; `AGENTS.md`'s Reachability list names the source gates, pinned by `TestAgentsMdNamesGatesThatExist`. **This is the rung the ADR was failing** — a drift query living in a sign-off line is a capability no operator can find. |
| 4 — it is used | `doctor --corpus` run against the real corpus, numbers in the sign-off. Whether anyone runs it afterwards is not measured here, and ADR-015 already recorded that operators may not run `doctor` at all — worth saying rather than assuming. |

> **Sign-off, 2026-08-27 — run against the REAL corpus, in the running container.**
>
> An earlier version of this note said the number was not reachable from a local checkout. That was
> wrong and is corrected here rather than deleted: the local checkout's database is an empty demo
> workspace, but the palace this repository actually runs is in the container's volume, and the
> rebuilt binary reads it directly.
>
> ```
> $ agentsmemory doctor --db /data/agentsmemory.db --corpus --project local
> corpus "local": 2037 drawers, 214 facts
>   1 fact(s) cite a retracted or superseded drawer — expected: provenance is historical
>   16 facts name no row (provenance that resolves to nothing — NOT the same as citing an ended
>      drawer, above)
> exit 1
> ```
>
> Three things this establishes, none of which the hermetic tests could:
>
> 1. **It reproduces the ADR's own finding.** 16 dangling `source_drawer_id` — the same 16 the
>    throwaway script found on 2026-08-27, now produced by a command with an exit code instead of by
>    somebody remembering to write the query.
> 2. **The drift is GONE.** 0 content keys disagree with their rows, against 27 of 1,705 before this
>    ADR. T2's backfill and T3's key-on-every-mint are what closed it, and this is the first
>    independent measurement of that.
> 3. **The three-state rule earns itself on real data.** One fact cites a drawer that was superseded,
>    and it is reported as expected rather than as a defect. A two-state check would have called it
>    the 17th dangling reference — a phantom that would grow with every correction the team makes,
>    which is exactly the shape that teaches operators to ignore a report.
>
> Repairing the 16 is out of scope and already deferred (`docs/adr/BACKLOG.md`). What changed today is
> that they are now findable by anyone, rather than by whoever thinks to look.

## Mutation Log

- 2026-08-27 · 49e4f62* · mutant killed · exit 1 · `internal/palace/mine.go` · a mint path re-derives the id directly, skipping the diary exemption — the defect M reproduced on #76 · acceptance-sha256:2404b3c24f2e4e6d51f86ce3f94db7fa0ffa77277c3613efc83d061de009ca43
- 2026-08-27 · 49e4f62* · mutant killed · exit 1 · `cmd/server/doctor.go` · the corpus check becomes unreachable while the flag stays declared, documented and read · acceptance-sha256:2404b3c24f2e4e6d51f86ce3f94db7fa0ffa77277c3613efc83d061de009ca43
- 2026-08-27 · 49e4f62* · mutant killed · exit 1 · `cmd/server/doctorcorpus.go` · an ENDED source is reported as lost, so every palace that uses corrections grows a pile of phantom defects · acceptance-sha256:2404b3c24f2e4e6d51f86ce3f94db7fa0ffa77277c3613efc83d061de009ca43
- 2026-08-27 · 49e4f62* · mutant killed · exit 1 · `internal/palace/import.go` · a mint path forgets its content key, so it never matches the dedup conflict target and every re-file inserts beside it · acceptance-sha256:2404b3c24f2e4e6d51f86ce3f94db7fa0ffa77277c3613efc83d061de009ca43

## Invariants

- The gates derive their universe from the source, never from a hand-kept list.
- A comment naming `DrawerID` neither satisfies nor trips `TestNoPathRederivesADrawerID` — that is why it parses instead of grepping.
- `doctor --corpus` stays OUT of the acceptance fence against a REAL corpus — a gate whose verdict depends on data nobody controls is one people learn to skip. Its hermetic unit tests, which build the drift they assert on, are in the fence.
- The report prints ids and counts, never memory text (`doctor.go:92`).

## Risks

- An ast-based gate is easy to write so permissively that it cannot fail. Mitigated by step 1's explicit red run, and by the Mutation Log entry required before this task may be marked done.
- `TestEveryDrawerMintWritesAContentKey` sees composite literals only. A mint built field-by-field on a `var d Drawer` escapes it. Say so in the test's own doc comment rather than claiming coverage the shape does not have.

## Stop Condition

Stop and ask if either gate cannot be made to fail. A gate that passes against the state it exists
to reject is decoration, and shipping it is worse than shipping nothing, because it reads as coverage.

**What would make these criteria impossible to fail?** For the ast gate: matching on a pattern no
real call site uses. Step 1's red run against a deliberately added violation is the check, and its
mutant entry is the evidence.

## Out of Scope

- Repairing the drifted rows the query finds (deferred: `docs/adr/BACKLOG.md`)
- Re-chunking on update (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
- 2026-08-27 · 49e4f62* · exit 0 · `go test ./internal/palace/ -run 'TestNoPathRederivesADrawerID|TestEveryDrawerMintWritesAContentKey|TestNoCommentClaimsADrawerIdIsDerivedFromItsContent' -count=1 2>&1 | tee /tmp/acc38e.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38e.out && go test ./cmd/server/ -run 'TestDoctorCorpus'  -count=1 2>&1 | tee /tmp/acc38e.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38e.out && go test ./... -count=1 2>&1 | tee /tmp/acc38f.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38f.out` · acceptance-sha256:2404b3c24f2e4e6d51f86ce3f94db7fa0ffa77277c3613efc83d061de009ca43
