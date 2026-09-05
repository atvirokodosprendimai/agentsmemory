# Task ADR-036-T8: The protocol becomes an API, and proves it costs less for the same meaning

**Depends-on:** T7, T3, T5
**Covers:** F-13, F-14, F-15, F-16, F-17, F-19, UC6-S1, UC6-S2, UC6-S3
**Estimated scope:** L
**Owner:** unassigned
**Produces:** the bootstrap surface
**Consumes:** `Service.EntryPoint` (T7), `Service.factsFor` and `palace.WingPolicy` (T3), `kg.CorrectionsFor` (T5)
**Data dependency:** **Needs real data, and must not commit it.** F-16 compares against `testdata/bootstrap-baseline-manifest-2026-08-26.json` — a REDACTED manifest carrying the call count, byte and token totals, tokenizer name and model build, and no transcript content. The real transcript stays untracked, under the same ADR-003 T2 boundary as T1's corpus. The fence runs against the manifest, never a live client.

## Goal

One call replaces a client-side protocol measured at ~99KB and 13 calls, and proves it costs less WITHOUT returning less.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/bootstrap.go` | add | assemble entry point, eager content, on-demand pointers, swept corrections, resolved wing, truncation report |
| `internal/palace/testdata/bootstrap-baseline-manifest-2026-08-26.json` | add | the REDACTED baseline: counts, totals, tokenizer, date — no transcript content |
| `internal/mcpserver/bootstrap.go` | add | the tool, its name and its schema |
| `internal/mcpserver/server.go` | edit | register it — the line that SELECTS it |
| `internal/palace/service.go` | edit | route the fact block through `WingPolicy` — one of the four call sites F-19 counts |
| `internal/palace/memory_search.go` | edit | route the correction mark through `WingPolicy` — the second call site |
| `internal/palace/graphquery.go` | edit | route EntryPoint's edges through `WingPolicy` — the third call site |
| `internal/palace/recallanswers_spec_test.go` | edit | five red tests |
| `internal/mcpserver/recallanswers_reach_test.go` | edit | the catalogue proof |

## Ordered Steps

1. Confirm all six tests are RED.
2. Pin the response CONTRACT first — tool name, request and response schema, which fields are mandatory under truncation, and the truncation ORDER. Without it F-16 is winnable by returning less.
3. Assemble the response. Corrections come from T5's `CorrectionsFor` — do not write a second sweep.
4. Bound the response and REPORT what was omitted, INCLUDING how to fetch it. The protocol this replaces lost 74% of a prescribed tier to an unreported cap; a report that says "3 omitted" without saying how to get them repeats it in a politer form.
5. Apply F-19 STRUCTURALLY, not behaviourally. Four call sites must invoke `WingPolicy`: the fact block (`service.go`), the correction mark (`memory_search.go`), EntryPoint's edges (`graphquery.go`) and the bootstrap's inline content. Assert it with a spy or a static check that every path CALLS the policy — a behavioural test over fixtures is satisfied by four duplicated filters that happen to agree, which is the state F-19 exists to forbid.
6. Also assert F-17 here rather than in T7: the bootstrap must not substitute `am_traverse` for direct resolution. T7 can prove `EntryPoint` resolves directly and still leave T8 free to build the bootstrap on a walk whose `max_hops` is inert, with every T7 test green.
7. Measure F-16 against the redacted manifest: assert SEMANTIC PARITY first — the response carries the same logical payload the 13 calls did — and only then compare tokens under the tokenizer the manifest names. Without parity the cheapest conformant bootstrap is one that returns nothing.
8. Assert the no-entry-point wing still bootstraps (UC6-S3), with its own step and its own assertion.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestBootstrapToolIsRegisteredAndDiscoverable' -count=1 2>&1 | tee /tmp/acc36t8.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t8.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent' 2>&1 | tee /tmp/acc36t8b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T8 does not depend on: T8's own 7 and its
ancestors' 18 still run, so a regression in what T8 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOneCallBootstrapsAWing` | `internal/palace/recallanswers_spec_test.go` | one call returns all six parts; no second call, no hardcoded id; a wing with no entry point still bootstraps | F-13, UC6-S1, UC6-S3 |
| `TestATruncatedBootstrapSaysWhatItDropped` | `internal/palace/recallanswers_spec_test.go` | a bounded response reports its omissions AND how to fetch them | F-14, UC6-S2 |
| `TestCorrectionsAreSweptServerSideAcrossAllThreePredicates` | `internal/palace/recallanswers_spec_test.go` | table-driven over retracts, supersedes and qualifies, read incoming, via T5's single resolver | F-15 |
| `TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces` | `internal/palace/recallanswers_spec_test.go` | semantic parity with the redacted manifest first, then fewer tokens under the tokenizer it names | F-16 |
| `TestOneWingRuleGovernsEveryNewResponsePath` | `internal/palace/recallanswers_spec_test.go` | all four paths INVOKE `WingPolicy` — proven by spy or static check, not by agreeing outputs | F-19 |
| `TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk` | `internal/palace/recallanswers_spec_test.go` | the bootstrap does not substitute multi-hop traversal for direct resolution | F-17 |
| `TestBootstrapToolIsRegisteredAndDiscoverable` | `internal/mcpserver/recallanswers_reach_test.go` | the tool is in the catalogue with its arguments | F-13 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the five palace tests |
| 2 — something selects it | the registration in `server.go`; mutation: unregister and the catalogue test goes red while the palace tests stay green |
| 3 — the caller can discover it | the catalogue test — a bootstrap nobody can find is the protocol it replaced |
| 4 — it is used | whether any client kit drops its hardcoded root id and its traversal instructions |

## Verification Log

- 2026-08-26 · 762592a · exit 1 · `set -o pipefail …` · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
  ```
      recallanswers_spec_test.go:705: F-17 not implemented: am_traverse's max_hops is provably inert (via is an intersection carried forward, so hop>=2 adds nothing), so a bootstrap built on multi-hop traversal would silently return only hop 1
  --- FAIL: TestOneWingRuleGovernsEveryNewResponsePath (0.00s)
      recallanswers_spec_test.go:782: F-19 not implemented: one wing-authorization rule governs the fact block, the sibling pointer, EntryPoint's edges and the bootstrap's inline content
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.024s
  --- FAIL: TestBootstrapToolIsRegisteredAndDiscoverable (0.00s)
      recallanswers_reach_test.go:164: ADR-036 T8 not implemented: the bootstrap tool is registered and appears in the catalogue with its arguments
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.020s
  FAIL
  ```
- 2026-08-26 · 762592a* · exit 0 · `set -o pipefail …` · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d

## Mutation Log

- 2026-08-26 · 762592a* · mutant survived · exit 0 · `internal/palace/bootstrap.go` · inline every record the entry point names, including one in another wing; a subject/predicate/object check would never see it · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-26 · 762592a* · mutant killed · exit 1 · `internal/palace/bootstrap.go` · report a count and not the call that resolves it — the politer form of the unreported cap that lost 74% of a tier · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
- 2026-08-26 · 762592a* · mutant killed · exit 1 · `internal/palace/bootstrap.go` · compute the placement and throw the answer away — the file still calls the policy, so only a behavioural assertion can see the leak · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
- 2026-08-26 · 762592a* · mutant survived · exit 0 · `internal/palace/bootstrap.go` · hollow out the parity check so F-16 becomes a bare token comparison, which the cheapest response wins by returning nothing · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-26 · 762592a* · mutant killed · exit 1 · `internal/palace/bootstrap.go` · hollow out the parity check so F-16 becomes a bare token comparison, which the cheapest response wins by returning nothing · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
- 2026-08-26 · 94eac55* · mutant killed · exit 1 · `internal/palace/bootstrap.go` · delete eager assembly — the response inlines nothing · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
- 2026-08-26 · 94eac55* · mutant survived · exit 0 · `internal/palace/bootstrap.go` · never defer anything: records past the eager bound vanish, named by no pointer · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-26 · 94eac55* · mutant killed · exit 1 · `internal/palace/bootstrap.go` · drop the correction sweep: a perfect bootstrap serves a record already contradicted · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d
- 2026-08-26 · 94eac55* · mutant killed · exit 1 · `internal/palace/bootstrap.go` · truncate without deferring: records past the eager bound vanish, named by no pointer and counted in no omission · acceptance-sha256:588b170436cba7a6adc6adcbfe5111d10cd69f7344bd6181cd4e61384abdd92d

## Invariants

- The response is always bounded, always states its omissions, and always says how to fetch them.
- Eager content is inline; on-demand is a pointer. Inlining everything reproduces the problem this removes.
- ONE wing rule, one correction sweep. Both are consumed, not reimplemented — and F-19 proves the CALL, not the agreement.
- No transcript content is committed. The manifest is the auditable record; the transcript stays untracked.

## Risks

- A full bootstrap encodes a WORKFLOW. If the tier split or the sweep is wrong it is expensive to walk back once clients depend on it — F-16 and F-14 make that observable before adoption.
- F-16 is falsifiable by construction: parity first, then tokens. Without parity the gate rewards omission, which is the failure it exists to prevent.

## Out of Scope

- Defining `must.*`/`ref.*` as server vocabulary (permanent: boundary: for THIS record the server distinguishes eager from on-demand and the names are a team convention. Amended 2026-09-05: the owner decided to merge #227, which serves the must/ref tier authored on the wing root through the bootstrap. That is ADR-043's territory, not a reopening of this task; the exclusion still says what T8 itself does not define.)
- Updating the client kits to use it (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask if the bootstrap cannot beat the frozen baseline at parity after two attempts — that falsifies F-16, and the decision should be revisited rather than the gate loosened.
