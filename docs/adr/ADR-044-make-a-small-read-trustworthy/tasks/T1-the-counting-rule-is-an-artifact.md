# Task ADR-044-T1: Commit the counting rule as an artifact and make a baseline name it by content

**Depends-on:** none
**Covers:** F-5, UC3-S1
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the counting-rule artifact and its content identity (a digest), plus the recorded baseline that cites it
**Consumes:** `drawer_fetches` rows and `CountFetches` — landed as migration 00036, produced by a task in ADR-028 rather than by a sibling here
**Data dependency:** needs a populated corpus with logged recalls and at least one recorded fetch. The rule file and the digest check are hermetic; the BASELINE is not, and its sign-off must record the window and the sample size it was taken over.

## Goal

Make the rule under which a read rate is measured a committed file with an identity, and make a recorded baseline cite that identity rather than describe the rule in prose.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/measurement/read-counting-rule.md` | add | The artifact itself. States the quantity — **reads acted on without a second call** — the window it is attributed to, and what does not count. F-5 requires an artifact, not a description |
| `internal/repohygiene/readrule.go` | add | Reads the rule file, computes its content digest, and resolves a recorded baseline's citation against it. This is what SELECTS the artifact: without it the file is documentation nothing consumes |
| `internal/repohygiene/readrule_spec_test.go` | edit | Turn `TestF5ABaselineNamesItsCountingRule` green. The `//go:build readcostspec` tag STAYS — T2 is the last task in this file and removes it |
| `docs/measurement/baselines/2026-NN-NN-read-rate.md` | add | The first baseline, citing the rule by digest. Its sign-off records the window and n |

## Ordered Steps

1. Confirm `TestF5ABaselineNamesItsCountingRule` is red for the right reason: `go test -tags readcostspec ./internal/repohygiene/ -run TestF5ABaselineNamesItsCountingRule -count=1` fails with *"not built yet — F-5 (UC3-S1)"*. Verified red 2026-08-29.
2. Write `docs/measurement/read-counting-rule.md`. The quantity is **reads acted on without a second call**: a recall with no subsequent `am_get_drawer` naming its `search_id`. Say explicitly that read FREQUENCY is NOT the quantity, and why (ADR-041 already counts frequency and a mechanism making every hit trustworthy could leave it unmoved).
3. Write `readrule.go`: load the rule, digest its normalized content, parse a baseline's `rule-sha256:` citation, and report whether it resolves. Derive the universe of baselines from the directory rather than a maintained list, so a baseline added tomorrow joins the check without anyone editing it.
4. Turn the F-5 binding green against a fixture rule + fixture baseline. Include the falsifiability case as a SUBTEST inside the same test — a baseline citing no rule, and one citing a digest that resolves to nothing — because a sibling test sits outside the acceptance fence and `adr-verify --mutant` would then grade a mutant the fence never ran.
5. Take the real baseline over `drawer_fetches`. **If ADR-028 T4's reported ratio has landed, consume it**; if it has not, compute the complement from raw rows and record in the baseline file that T4 owns the reporting half. Record window and n in the file and in the sign-off.

## Acceptance

```bash
set -o pipefail
go test -tags readcostspec ./internal/repohygiene/ -run 'TestF5ABaselineNamesItsCountingRule' -count=1 2>&1 | tee /tmp/adr044-t1.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr044-t1.out && go vet ./... && go test ./... -count=1
```

The new unit runs alone first, so no already-passing suite can carry the verdict; the full run
follows as regression, chained with `&&` so both must pass. `set -o pipefail` is required — without
it the pipeline's status is `tee`'s and only the grep is tested.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestF5ABaselineNamesItsCountingRule` | `internal/repohygiene/readrule_spec_test.go` | A baseline cites the rule by content digest; a baseline citing nothing, or citing a digest that resolves to nothing, is reported | F-5, UC3-S1 |
| `TestF5ABaselineNamesItsCountingRule/a_baseline_with_no_citation_is_caught` | same | The falsifiability half, as a subtest so it is inside the fence | F-5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestF5ABaselineNamesItsCountingRule` |
| 2 — something selects it | `readrule.go` reads the artifact; the test drives `readrule.go` rather than re-implementing the resolution, so severing the real call site goes red |
| 3 — the caller can discover it | The rule file is named in the baseline's own citation line, and `docs/measurement/` is linked from the ADR. No wire interface — `n/a: no declared interface` |
| 4 — it is used | Every later rate quote must cite a digest; nothing measures compliance yet, and T2 is what makes a stale citation fail |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->
- 2026-08-29 · ea4680f* · mutant killed · exit 1 · `internal/repohygiene/readrule.go` · a baseline naming no rule must not read as one that resolves — F-5 kill-case: recording a baseline with no rule reference · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
- 2026-08-29 · 28c5fc0* · mutant killed · exit 1 · `internal/repohygiene/readrule.go` · the shipped defect, restored: a file that DECLARES ITSELF DEGENERATE counts as a baseline, so F-5 reads as satisfied by the existence of a document that disclaims it. This is the assert-X-is-available-passes-when-X-is-absent shape, reached by the gate that polices it · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
- 2026-08-29 · 28c5fc0* · mutant survived · exit 0 · `internal/repohygiene/readrule.go` · the SHIPPED defect, restored: a file that declares itself degenerate counts as a baseline, so F-5 reads as satisfied by the existence of a document that disclaims it. Found in review 2026-08-29 — the assert-X-is-available-passes-when-X-is-absent shape, reached by the gate that polices it · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-29 · 28c5fc0* · mutant killed · exit 1 · `internal/repohygiene/readrule.go` · the sibling-only glob restored: a baseline filed one directory down is invisible and the gate stays GREEN, against the functions own promise that a baseline added tomorrow joins the check · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
- 2026-08-29 · 28c5fc0* · mutant killed · exit 1 · `internal/repohygiene/readrule.go` · the SHIPPED defect restored one value over: a file declaring itself degenerate counts as a baseline, so F-5 reads as satisfied by a document that disclaims it. This mutant SURVIVED the first version of the falsifiability subtest, which built a Baseline by hand and never drove Usable() · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
- 2026-08-29 · 28c5fc0* · mutant survived · exit 0 · `internal/repohygiene/readrule_spec_test.go` · sever the CALL: the corpus has no usable baseline and no override is required, so F-5 passes over a corpus that does not satisfy it. A falsifiability half that only drives the helper leaves this green, which is why the call site is mutated rather than the helper · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
  ```
  the fence passed with the mechanism broken
  ```

## Invariants

- The rule's quantity is **reads acted on without a second call**. Changing it is not an edit, it is a rule change, and T2 makes that invalidate every baseline taken under the old one.
- The baseline never quotes a rate without its population.

## Risks

- ADR-028 T4 is pending and owns the reporting half. Mitigated by the step-5 fallback: compute from raw `drawer_fetches` rows and name T4 as the reporting half in the baseline file.
- A baseline over one session read as a corpus rate. Mitigated by requiring window and n in the sign-off; the one figure available today (6 searches against 18 writes, 2026-08-28) is explicitly n=1.

## Stop Condition

Stop if `drawer_fetches` cannot distinguish a recall that needed a second call from one that did not — that is, if fetches cannot be joined to the recall that produced them. Then the rule is unmeasurable as written and the quantity must be re-decided with the owner before anything is collected, because F-6 voids every baseline taken under a rule that later changes.

**What would make this criterion impossible to fail:** if every recall in the corpus were followed by a fetch, or none were, the rate would be 0 or 1 and no mechanism could move it. Check the spread before recording the baseline; a degenerate distribution is a finding, not a baseline.

## Out of Scope

- Reporting the ratio with `profile_id` beside it — that is ADR-028 T4's job, not a sibling here
- Any mechanism change: T1 ships the instrument only, which is F-5's whole point

## Amended 2026-08-29 after review — the gate counted a baseline that says it is not one

@blinkinglight's review of PR #108 found this task's gate satisfied by the very file that disclaims
it: `Baseline` carried only `Path` and `CitedDigest`, so `TestF5ABaselineNamesItsCountingRule`
counted the degenerate baseline toward its `len(found) == 0` check — the check whose message says
*"F-5 forbids shipping a mechanism in that case"*. **A test asserting that X is available, passing
when X is absent, is the defect this repository names itself after, and it had reached the gate that
polices it.** Confirmed by reading the code, not the review.

Three repairs, all from that review:

- **`baseline:` verdict line, and `Usable()`.** The baseline now declares `usable` or `degenerate` in
  a form a tool can read; the prose verdict was already correct and nothing could act on it. A file
  saying NOTHING is not usable — an unmarked baseline is one nobody has judged.
- **Shipping anyway is written, not silent.** `shippedWithoutUsableBaseline` names the record that
  decided and why, refused by `TestF5AnOverrideNamesItsReason` when the reason is blank. Deleting the
  entry is how the constraint comes back, which is stated there.
- **`WalkDir`, not `Glob`, and skip `README.md`.** A baseline one directory down was invisible and
  the gate stayed green, against this function's own promise. A README in that directory reddened CI.
- **`QuoteRate` in the loop.** The old message appended *"compares against a rule that is not the one
  on disk"* to BOTH refusals, which is false for a baseline nobody bound — nothing was compared.

### Two mutants SURVIVED, and they are recorded rather than papered over

**1. `Usable() { return b.Verdict != "unusable" }` survived the first version of the falsifiability
subtest.** That subtest built a `Baseline` by hand and called `requireWrittenOverride` directly, so
the PREDICATE was never driven: the corpus's `degenerate` file counted as usable and the fence still
passed. It is killed now by a subtest that drives `Usable()` over its own spellings. **`adr-verify`
refusing to score a surviving mutant is the only reason this was noticed** — the fence was green and
the hole was one value over from the one review had just reported.

**2. Severing the CALL to `requireWrittenOverride` still passes, and no test here can catch it.**
With the call deleted the corpus has no usable baseline, no override is demanded, and F-5 reports
satisfied. This is the severed-call-site class `AGENTS.md` records twice. The substitutable
`testing.TB` shim used here catches a severed check INSIDE the helper; it cannot catch the deletion
of the line that calls it, because the test that would notice is the test being emptied. Recorded as
an honest limit rather than claimed as covered: **the defence against this one is review, not the
suite.** The earlier `Verdict != ""` entry in the log below was killed against a corpus whose baseline
carried no verdict line yet, so it is true of that tree and not of this one.

## Verification Log

<!-- Tool-written by `adr-verify`. -->
- 2026-08-29 · ea4680f* · exit 1 · `set -o pipefail …` · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
  ```
  --- FAIL: TestF5ABaselineNamesItsCountingRule (0.00s)
      readrule_spec_test.go:27: not built yet — F-5 (UC3-S1): a baseline names the counting rule it was measured under BY CONTENT, not by description. ADR-041's spec F-3 already forbids shipping a mechanism before a baseline — this is the additive half: the rule is a committed artifact with an identity. Derive the universe from task metadata rather than a maintained list, so a mechanism added tomorrow joins the check. Kill it by recording a baseline with no rule reference
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/repohygiene	0.006s
  FAIL
  ```
- 2026-08-29 · ea4680f* · exit 0 · `set -o pipefail …` · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
- 2026-08-29 · 28c5fc0* · exit 0 · `set -o pipefail …` · acceptance-sha256:6f7d0ae66e340ef0cc10ba114346ee4ea202fb6a31fb724a284c54af2994b6e5
