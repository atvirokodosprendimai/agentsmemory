# Task ADR-038-T5: Recall returns what is current — and carries the reason forward

> Re-authored 2026-08-27 from ADR-010's T3, which this record supersedes. ADR-010's own mid-flight
> amendment is kept verbatim in intent: hiding history behind a flag AND expecting retractions to
> stop re-litigation cannot both hold, because a session about to redo a rejected thing does not
> know to ask for history. So the CURRENT record carries what it superseded and why.

**Depends-on:** T4
**Covers:** none — no spec
**Estimated scope:** L (every default read route)
**Owner:** unassigned
**Produces:** current-only recall across every default route; the superseded reason carried on the live record; the explicit history flag
**Consumes:** supersede semantics (T4); `current()` (T1)
**Data dependency:** hermetic

## Goal

An ended record is unreachable by every default route and reachable by one explicit one, and the live
record names what it replaced and why.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/memory_search.go` | edit | the ended branch joins `survivorsFrom`, the ONE scope predicate — see the correction below |
| `internal/palace/currentonly.go` | add | `truncateReason`, `PredecessorsOf`, `attachSupersedes`, `GetAnyVersion`, `ListAnyVersion`, `GetMemoryAnyVersion` |
| `internal/palace/repo.go` | edit | `ListCurrent` — the predicate is pushed into SQL, because `limit`/`offset` are applied by the database and a Go-side filter returns short pages that skip records as the offset walks past ended rows |
| `internal/palace/service.go` | edit | `Get`, `GetMemory` and `List` return current records only and name `include_history` in the refusal; `SearchQuery.IncludeHistory`; `scopeDrops.Superseded` reported as `am.dropped_superseded`; the live row's `supersedes` + reason resolved onto the ranked page |
| `internal/mcpserver/drawers.go` | edit | `include_history` on `am_search`, `am_list_drawers`, `am_get_drawer`; `supersedes` + `superseded_reason` on the drawer view |
| `internal/palace/currentonly_test.go` | add | the end-to-end failing tests, and the route MATRIX below |
| `internal/palace/memory_search.go` | edit (2nd) | the ended-SIBLING filter in `collapseCandidatesToMemories` — see the third correction |
| `internal/palace/bootstrap.go` → `repo.DrawersByIDs` | edit | `am_bootstrap` inlined ended records: an entry edge outlives an ending, and this is the first thing a waking session reads |
| `internal/palace/tunnel.go` | edit | `am_follow_tunnels` previewed an ended endpoint's TEXT through `repo.Get` |
| `internal/palace/service.go` (`CheckDuplicate`) | edit | probed depth 1 and answered with a retracted record, masking the current match behind it |
| `internal/palace/repo.go` (`diaryScope`) | edit | `am_diary_read` and its total both omitted the predicate |
| `internal/palace/copywing.go`, `internal/wingbundle/wingbundle.go` | edit | neither format carries a validity window, so an exported ended row arrived in the destination as CURRENT |
| `db/migrations/00033_drawers_superseded_by_idx.sql` | add | T5 makes `superseded_by` a per-page key; 00030 added the column with no index |

> **Correction, 2026-08-27, found while executing** — on the `memory_search.go` row. It said "the vector and BM25 halves both,
> since a filter applied to one leaks through the other". **There are no two halves.** There is one
> retrieval — the vector index — and `bm25Scores` re-scores the survivors `survivorsFrom` already
> returned (`service.go` `rankRetrieved` → `collapseCandidatesToMemories` → `rank.go:252`). The
> finding is STRONGER than what the row claimed: with one pool there is nowhere for the filter to
> leak, and `survivorsFrom` is called by both the widening loop and the ranking pass, so composing
> the predicate there covers retrieval width, the final page and the eval arms at once.
> The rung-2 mutation is amended below for the same reason: "remove it from the BM25 half alone" is
> unperformable.

> **Decided at execution, because the record contradicted itself** — on the `mcpserver/drawers.go`
> row. It put the 200-character
> truncation in `mcpserver`, and the Tests table put `TestTheCarriedReasonIsTruncatedTo200Chars` in
> `internal/palace/currentonly_test.go`, where it could not observe it. **Truncation lives in the
> palace** (`truncateReason`, applied by `attachSupersedes`), so the cap has one spelling, every
> route that carries a reason gets it, and the test that names it can see it. The MCP layer renders
> what it is handed.
> The predecessor keeps its reason WHOLE — the cap is about recall payload, never about what the
> store keeps.

> **Third correction, 2026-08-27 — found by an independent review while `go test ./...` was green.**
> Composing the predicate into search, list and get was **not** "every default route". Six findings, all
> reproduced in source and each now pinned by a regression proven red-without-the-fix:
>
> 1. **The ended SIBLING.** `survivorsFrom` filters the retrieved chunk; `collapseCandidatesToMemories`
>    then re-reads every sibling under the root through `MemoryChunksByRoots`, which is
>    history-inclusive. The ended sibling was reassembled into `MemoryContent` — what BM25 and the
>    cross-encoder score, and what the tool returns. **The mixed state is routine, not exotic:**
>    `purgeSource` ends only the chunks whose key left the source, so any re-file that shortens a
>    document leaves a current root with ended children. A supersede ends a memory whole; a re-file
>    does not.
> 2. **Four more default routes** returned ended TEXT by a query the filter never saw: `am_bootstrap`'s
>    inline hydration, `am_follow_tunnels`' endpoint preview, `am_check_duplicate` (depth 1, so a
>    retracted match masked the current one behind it), and `am_diary_read` — page and total both.
> 3. **The transfers resurrected history.** `CopyWing` and the wing bundle read history-inclusively
>    while neither format carries `valid_to`, so retracted text arrived in the destination **asserted
>    as current with the reason gone.** Dropping history loses the account of why; copying it loses the
>    account and re-asserts the claim.
> 4. **The carried reason did not reach a child chunk.** `superseded_by` names the successor's ROOT,
>    while a search page's representative is whichever chunk matched — so `attachSupersedes`, keyed on
>    the row's own id, found nothing for exactly the multi-chunk memories a correction most often
>    replaces. Now keyed by `memoryOf`, attached on `GetMemory` too, and `SearchPage` **fails closed**
>    rather than logging a warning: the invariant is stated without qualification, and a page silently
>    missing the reason is that invariant quietly false.
> 5. **An unindexed per-page key.** 00033 adds `(team_id, superseded_by)`, partial on `!= ''`; the
>    lookup is batched through `chunkIDs`.
> 6. **The rung-3 gate had the hole it exists to close.** It walked only `register*` bodies, so moving
>    the read into a one-line helper put the literal in a function no walk visited and the gate went
>    green with a tool honouring an argument it never declared. It now walks every occurrence in the
>    package and REPORTS any it cannot attribute.
>
> The lesson is the one this repository keeps paying for: "three routes tested" is not "no default
> route", and a predicate composed into the routes you thought of says nothing about the fourth door.


## Ordered Steps

1. Write the failing tests first — RED, and the end-to-end one is the task's real gate:
   - **an ended record is returned by NO default route.** Drive it through `am_search`,
     `am_list_drawers` and `am_get_drawer` end to end, not by unit test — this exact failure shipped
     once already, as a live chunk 1 with its own embedding ranking above the correction that
     replaced it;
   - `include_history: true` returns it, with its ending reason;
   - the CURRENT record carries `supersedes` and the reason on the DEFAULT path;
   - a reason longer than 200 characters is truncated in the recall response and whole under the
     history flag.
2. Compose `current()` into every default read route.
3. Resolve and attach `supersedes` + the truncated reason.
4. Add `include_history`, and declare it in every tool schema that honours it — **a handler that
   honours an argument the schema never advertises is a capability nobody will ever send** (rung 3).
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -run 'TestAnEndedRecordIsReturnedByNoDefaultRoute|TestNoDefaultRouteReturnsEndedText|TestBootstrapAndTunnelPreviewsHideEndedText|TestTransferPathsCarryOnlyWhatIsStillAsserted|TestAChildChunkOfACorrectionCarriesTheReason|TestNoMemoryEndsHalfway|TestIncludeHistoryReturnsItWithItsReason|TestTheLiveRecordCarriesWhatItReplaced|TestTheCarriedReasonIsTruncatedTo200Chars|TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt' -count=1 2>&1 | tee /tmp/acc38t5a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t5a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t5b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t5b.out
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAnEndedRecordIsReturnedByNoDefaultRoute` | `internal/palace/currentonly_test.go` | the ADR's own falsification, end to end across all three routes | — |
| `TestIncludeHistoryReturnsItWithItsReason` | `internal/palace/currentonly_test.go` | history is reachable by exactly one explicit route | — |
| `TestTheLiveRecordCarriesWhatItReplaced` | `internal/palace/currentonly_test.go` | the reason reaches the DEFAULT path — the correction ADR-010 made to its own first draft | — |
| `TestTheCarriedReasonIsTruncatedTo200Chars` | `internal/palace/currentonly_test.go` | accumulation never grows the payload | — |
| `TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt` | `internal/mcpserver/catalog_test.go` | **rung 3** — a schema check; a behavioural test that sends the argument passes whether or not the schema advertises it | — |
| `TestAnEndedRecordIsReturnedByNoDefaultRoute` | `internal/mcptest/currentonly_test.go` | the same falsification driven through the TOOLS, since every route has its own handler | — |
| `TestNoDefaultRouteReturnsEndedText` | `internal/palace/currentonly_test.go` | the route MATRIX: the ended sibling of a re-filed document, `check_duplicate`, and `diary_read`'s page AND total | — |
| `TestBootstrapAndTunnelPreviewsHideEndedText` | `internal/palace/currentonly_test.go` | the two doors that return drawer text without going through search, list or get | — |
| `TestTransferPathsCarryOnlyWhatIsStillAsserted` | `internal/palace/currentonly_test.go` | `List` stays history-inclusive and `ListCurrent` does not — the pair a bundle and a copy depend on | — |
| `TestNoMemoryEndsHalfway` | `internal/palace/currentonly_test.go` | a supersede ends a memory whole (asserted, not assumed) | — |
| `TestAChildChunkOfACorrectionCarriesTheReason` | `internal/palace/currentonly_test.go` | the lineage reaches a CHILD chunk — added because the `memoryOf` mutant SURVIVED: every earlier lineage test used a single-chunk memory, where root and row id are the same value and neither keying can be distinguished | — |

**Shapes the creation path can already produce:** a multi-chunk memory where one chunk is ended and
others are not (should be impossible after T4 — assert it, do not assume it); a superseded record
whose successor is itself superseded (a chain — the live record names its immediate predecessor, and
the full chain is history-flag territory); an ended record that is the `source_drawer_id` of a
current KG fact — **decided 2026-08-27: the fact KEEPS the pointer.** Provenance is historical; the
fact was extracted from that text, and re-pointing it at the successor would assert that the
corrected text still supports it, which a correction may have removed. `am_kg_query` already returns
`source_drawer_id` (ADR-026 T6) so the reader can see it resolves to an ended record.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the unit tests |
| 2 — something selects it | the predicate composed into each read route; mutation: **delete the ended branch from `survivorsFrom`** and `TestAnEndedRecordIsReturnedByNoDefaultRoute` must go red. That is the amended form of this row (see the correction above): `survivorsFrom` is the single scope predicate both the widening loop and the ranking pass call, so one deletion proves all of retrieval width, the served page and the eval arms are covered by it |
| 3 — the caller can discover it | `include_history` declared in every tool schema that honours it, asserted by a schema check |
| 4 — it is used | the ratio of `include_history` recalls to plain ones. **Deliberately NOT a retraction trigger** — ADR-010 struck that and the strike is kept: an archive's payoff is rare and large, and retiring it on call count is cancelling insurance because no claim was filed. |

## Mutation Log

- 2026-08-27 · bfe0b65* · mutant killed · exit 1 · `internal/palace/memory_search.go` · the single scope predicate stops filtering ended records — covers the widening loop, the served page and the eval arms at once · acceptance-sha256:0067f3c50f830c7ce75e408288ef1f19f3bb594f80c843b52dd96562a1791ca3
- 2026-08-27 · bfe0b65* · mutant killed · exit 1 · `internal/palace/repo.go` · the listing and every transfer path silently go history-inclusive again · acceptance-sha256:0067f3c50f830c7ce75e408288ef1f19f3bb594f80c843b52dd96562a1791ca3
- 2026-08-27 · bfe0b65* · mutant survived · exit 0 · `internal/palace/currentonly.go` · lineage keyed by the row id again, so a child chunk of a corrected memory carries no reason · acceptance-sha256:0067f3c50f830c7ce75e408288ef1f19f3bb594f80c843b52dd96562a1791ca3
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-27 · bfe0b65* · mutant killed · exit 1 · `internal/palace/currentonly.go` · lineage keyed by the row id again, so a child chunk of a corrected memory carries no reason · acceptance-sha256:a0b47be5798644732c56ed7bd764e38c057254c4466844d1b1f45d7b91d8627c
- 2026-08-27 · bfe0b65* · mutant inconclusive · exit 1 · `internal/palace/currentonly.go` · the carried reason is no longer capped, so a page grows with the corpus · acceptance-sha256:a0b47be5798644732c56ed7bd764e38c057254c4466844d1b1f45d7b91d8627c
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-08-27 · bfe0b65* · mutant killed · exit 1 · `internal/palace/currentonly.go` · the cap stops binding, so the carried reason rides every hit at full length · acceptance-sha256:a0b47be5798644732c56ed7bd764e38c057254c4466844d1b1f45d7b91d8627c
- 2026-08-27 · bfe0b65* · mutant killed · exit 1 · `internal/palace/repo.go` · am_diary_read goes back to returning retracted entries in its page and its total · acceptance-sha256:a0b47be5798644732c56ed7bd764e38c057254c4466844d1b1f45d7b91d8627c

## Invariants

- Ended TEXT never competes with its correction on any default route.
- The ending REASON always reaches the default route, on the live record.
- A KG fact's `source_drawer_id` is never rewritten by an ending. Provenance records where a claim came from, not where it is still true.
- The recall payload does not grow with corpus size: 200-character cap, `limit × snippet_chars` unchanged.

## Risks

- A filter applied to the vector half and not the lexical one leaks ended records back. The mutation in rung 2 exists for that specific shape.
- **An ended drawer keeps its vector, so the pool can shrink silently — REPORT it, and the counter already exists.** The vector store returns N candidates keyed by drawer id; `current()` runs in SQL and drops the ended ones, so a page can come back shorter than `limit` with nothing saying why. Decided 2026-08-27: report rather than over-fetch, because over-fetching changes what the pool means and every measurement taken against it. **Add a FOURTH field to `scopeDrops` (`memory_search.go:35`)** — it already carries `Orphan`, `OutOfScope`, `OverDistance`, is surfaced at `service.go:1359–1361` as `am.dropped_*`, and inherits `Any()`, the telemetry shape and ADR-034's precedent for free. **Keep it separate, never folded into an existing field:** a page that came back short because records were superseded and one that came back short because of wing policy are different facts about the system, and a merged counter answers neither — the same argument this record makes for `doctor --corpus` reporting three states rather than two.
- Truncating at 200 characters mid-word produces an unreadable fragment. Truncate on a boundary and mark it; a reason nobody can read is a reason nobody will act on.

## Stop Condition

Stop and ask if composing `current()` into `searchCandidates` measurably changes ranking for
CURRENT-only corpora — it must not. The ADR's falsification allows a ~0.01 MRR noise floor; a shift
larger than that means the filter is doing more than filtering.

**What would make this criterion impossible to fail?** Measuring it on a corpus with no ended records
at all, where the filter is a no-op by construction. The falsification requires ended records to
outnumber current ones 2:1 for exactly that reason.

## Out of Scope

- The corpus-integrity gate — T6.
- Ranking ended records when history IS requested (deferred: `docs/adr/BACKLOG.md` — inherited from ADR-010, which received it from ADR-004)

## Verification Log
- 2026-08-27 · bfe0b65* · exit 1 · `go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -run 'TestAnEndedRecordIsReturnedByNoDefaultRoute|TestIncludeHistoryReturnsItWithItsReason|TestTheLiveRecordCarriesWhatItReplaced|TestTheCarriedReasonIsTruncatedTo200Chars|TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt' -count=1 2>&1 | tee /tmp/acc38t5a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t5a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t5b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t5b.out` · acceptance-sha256:0067f3c50f830c7ce75e408288ef1f19f3bb594f80c843b52dd96562a1791ca3
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.122s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.028s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	0.024s [no tests to run]
  ```
- 2026-08-27 · bfe0b65* · exit 0 · `go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -run 'TestAnEndedRecordIsReturnedByNoDefaultRoute|TestIncludeHistoryReturnsItWithItsReason|TestTheLiveRecordCarriesWhatItReplaced|TestTheCarriedReasonIsTruncatedTo200Chars|TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt' -count=1 2>&1 | tee /tmp/acc38t5a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t5a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t5b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t5b.out` · acceptance-sha256:0067f3c50f830c7ce75e408288ef1f19f3bb594f80c843b52dd96562a1791ca3
- 2026-08-27 · bfe0b65* · exit 0 · `go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -run 'TestAnEndedRecordIsReturnedByNoDefaultRoute|TestIncludeHistoryReturnsItWithItsReason|TestTheLiveRecordCarriesWhatItReplaced|TestTheCarriedReasonIsTruncatedTo200Chars|TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt' -count=1 2>&1 | tee /tmp/acc38t5a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t5a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t5b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t5b.out` · acceptance-sha256:0067f3c50f830c7ce75e408288ef1f19f3bb594f80c843b52dd96562a1791ca3
- 2026-08-27 · bfe0b65* · exit 0 · `go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -run 'TestAnEndedRecordIsReturnedByNoDefaultRoute|TestNoDefaultRouteReturnsEndedText|TestBootstrapAndTunnelPreviewsHideEndedText|TestTransferPathsCarryOnlyWhatIsStillAsserted|TestAChildChunkOfACorrectionCarriesTheReason|TestNoMemoryEndsHalfway|TestIncludeHistoryReturnsItWithItsReason|TestTheLiveRecordCarriesWhatItReplaced|TestTheCarriedReasonIsTruncatedTo200Chars|TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt' -count=1 2>&1 | tee /tmp/acc38t5a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t5a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t5b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t5b.out` · acceptance-sha256:a0b47be5798644732c56ed7bd764e38c057254c4466844d1b1f45d7b91d8627c
