# Task ADR-048-T1: Remove the four inert dynamics keys from both wire views and gate their return

**Depends-on:** none
**Covers:** none — no spec stage
**Estimated scope:** S (one source file, one new test, one deleted allowlist entry)
**Owner:** unassigned
**Produces:** `TestNoDynamicsFieldIsDeclaredOnTheWireInThisPackage` and the reduced `tunnelView` / `hallwayView` shapes
**Consumes:** `palace.Dynamics`'s json tags, which are the gate's universe — an existing type, not produced by a sibling task
**Rests-on:** `a retired key returning to the wire is caught`, `the forbidden set is derived, never vacuous`, `the detector reports an offender`
**Data dependency:** none. Every check here is hermetic: the gate parses source text and the suite needs no corpus, no live server and no populated palace.

## Goal

Stop `am_create_tunnel`, `am_list_tunnels` and `am_list_hallways` publishing four fields that describe
a reinforcement layer this server does not implement, and make the absence durable by deriving the
forbidden key set from `palace.Dynamics` itself rather than from a literal list.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/dynamicswire_test.go` | add | The gate. Reads `palace.Dynamics`'s json tags and fails when any appears as a wire key in this package. This is the check `Enforced-by` names |
| `internal/mcpserver/graph.go` | edit | Remove the four fields from `tunnelView` and `hallwayView` and the two assignments in `toTunnelView` / `toHallwayView`. This is what SELECTS the shape an agent receives — the views are the only thing that puts these keys on the wire |
| `internal/mcpserver/wirekeys_test.go` | edit | Delete `undescribedOnPurpose["last_activated"]`. Once the field is gone the entry excuses nothing, and `TestUndescribedOnPurposeIsJustified` fails on exactly that |
| `docs/adr/BACKLOG.md` | edit | Write the receipt for this record's two deferrals, so `adr-debt` does not report them UNRECEIPTED |

## Ordered Steps

1. Write `dynamicswire_test.go` and confirm it is red for the right reason: it must report all eight
   keys — four on `tunnelView`, four on `hallwayView` — before any removal. A gate that is green at
   authoring is measuring nothing, which is the failure `adr-verify` records as a finding.
2. Delete the four fields from `tunnelView` and from `hallwayView`, and drop the two lines in
   `toTunnelView` and `toHallwayView` that assign them. Leave `palace.Dynamics`, `initDynamics` and
   the columns alone — `internal/palace/hallway.go:109` still reads `LastActivated` as a fallback
   input to `earliestStamp`, and breaking the #38 stamp repair is not in this record's scope.
3. Run the suite and watch `TestUndescribedOnPurposeIsJustified` go red on the now-dead
   `last_activated` entry. That failure is the existing gate working; delete the entry to close it.
   Do not delete it in step 2 — seeing it fire is what proves the coupling is real rather than
   assumed.
4. Record the two deferrals in `docs/adr/BACKLOG.md` naming ADR-048, in this same commit, so the
   pointers are honoured rather than merely resolvable.
5. Run the full suite as regression.

⚠ **THE WORK PREDATED THIS EVIDENCE, AND THE LOG WOULD OTHERWISE IMPLY A CYCLE THAT
DID NOT HAPPEN.** Steps 1-4 landed in `bb8d030`, under this record's ORIGINAL number:
it was authored as ADR-045, `main` took 045 while it sat open, and `f7c83d9`
renumbered it to 048. The renumber carried the files and lost the bookkeeping — the
tasks README stayed `pending` and the record stayed `Proposed` over a tree where the
fields were already gone, the gate already existed and the dead `undescribedOnPurpose`
entry was already deleted. So no entry below is a TDD red run, because there was no
red state left to observe by the time anyone ran the fence: step 1's "confirm it is
red for the right reason" was satisfied at authoring time in `bb8d030` and is not
re-creatable now. What carries that burden instead is the `a_returned_key_is_caught`
subtest and the three mutants, each of which puts the offender back and watches the
gate report it — a stronger claim than a red run, because it is repeatable.

## Acceptance

```bash
set -o pipefail
go test ./internal/mcpserver/ -run 'TestNoDynamicsFieldIsDeclaredOnTheWireInThisPackage|TestUndescribedOnPurposeIsJustified|TestEveryOmitemptyWireKeyInThisPackageIsDescribed' -count=1 2>&1 | tee /tmp/adr045-t1.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr045-t1.out && gofmt -l internal/mcpserver | (! grep -q .) && go vet ./... && go test ./... -count=1
```

The three named units run alone first, so no already-passing suite can carry the verdict, and the
`! grep -qE "no tests to run"` clause is what stops the fence exiting 0 before the new test exists —
a `-run` filter matching nothing is the vacuous-green shape this pipeline records as a finding.
`set -o pipefail` is required: without it the pipeline's status is `tee`'s, not the suite's. The full
run follows as regression, chained with `&&` so every stage must pass.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestNoDynamicsFieldIsDeclaredOnTheWireInThisPackage` | `internal/mcpserver/dynamicswire_test.go` | No json tag in this package's non-test sources names a field of `palace.Dynamics` | none — no spec stage |
| `TestNoDynamicsFieldIsDeclaredOnTheWireInThisPackage/a_returned_key_is_caught` | same | The falsifiability half: the detector reports a key when one IS present, driven over a fixture through the same function the gate uses | none — no spec stage |
| `TestUndescribedOnPurposeIsJustified` | `internal/mcpserver/wirekeys_test.go` | Existing gate; must stay green after the exemption is deleted | none — no spec stage |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestNoDynamicsFieldIsDeclaredOnTheWireInThisPackage` |
| 2 — something selects it | It is an ordinary test in the package, run by `go test ./...`; it needs no registration and no build tag, so nothing can leave it compiled-out the way `internal/mcptest`'s contract axis can be |
| 3 — the caller can discover it | `n/a: no declared interface` — this task REMOVES wire keys, so the discoverability question it raises is the inverse one, and `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` is in the fence to prove the removal leaves no undescribed survivor |
| 4 — it is used | The gate's universe is `palace.Dynamics`'s own tags, so it is exercised by every future field added to that struct, not only by today's four |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->
- 2026-09-05 · 276cba9* · mutant killed · exit 1 · `internal/mcpserver/graph.go` · Puts one retired dynamics key back on tunnelView, the regression this record exists to prevent: an agent reading a tunnel sees a strength it can never learn anything from, because initDynamics stamps it once and nothing writes it again. · acceptance-sha256:852859ee7b4643e593dd2089da2dec756723d2752e4fe17b61cd75fa821576f4 · covers:a retired key returning to the wire is caught
- 2026-09-05 · 276cba9* · mutant killed · exit 1 · `internal/mcpserver/dynamicswire_test.go` · The forbidden set is read from a struct that no longer matches, so dynamicsKeys returns nothing and the gate would sweep a package it never looked at — reporting a clean wire because it forgot what to look for. This is the vacuity a derived universe buys and the reason the len==0 Fatal exists; without that guard the gate is green forever after any rename of palace.Dynamics. · acceptance-sha256:852859ee7b4643e593dd2089da2dec756723d2752e4fe17b61cd75fa821576f4 · covers:the forbidden set is derived, never vacuous
- 2026-09-05 · 276cba9* · mutant killed · exit 1 · `internal/mcpserver/dynamicswire_test.go` · Severs the detector so it reports nothing whatever it is given. The main assertion still passes — a clean package and a blind detector are byte-identical from outside — and only the falsifiability subtest can tell them apart. This is why that half is a SUBTEST driving the same function rather than a sibling that reimplements it. · acceptance-sha256:852859ee7b4643e593dd2089da2dec756723d2752e4fe17b61cd75fa821576f4 · covers:the detector reports an offender

## Invariants

- `palace.Dynamics`, `initDynamics` and the four database columns are unchanged. This record retires a wire surface, not storage.
- `internal/palace/hallway.go:109` keeps reading `LastActivated` for the `created_at` repair — the #38 fix stays working.
- The gate's forbidden set is DERIVED from `palace.Dynamics`, never written out as a literal. A list kept beside the truth is the shape this repository rejects.

## Risks

- The gate could pass vacuously if its source glob returns nothing. Mitigated by failing on an empty read, the same guard `packageSources` uses in `wirekeys_test.go`.
- Parsing json tags by regex could miss a field spelled unusually. Mitigated by driving the falsifiability subtest through the same extraction function, so a detector that sees nothing fails the subtest rather than passing the gate.

## Stop Condition

Stop if `LastActivated` turns out to have a second internal reader beyond `earliestStamp` at
`internal/palace/hallway.go:109` — grep before step 2. That would mean the field is load-bearing
somewhere this record did not survey, and the wire-only scope would need re-deciding with the owner
rather than widened here.

**What would make this criterion impossible to fail:** nothing. The grep is over a tree that today
has exactly one such reader, so the check can genuinely come back either way.

## Out of Scope

- Dropping the four columns, which needs a migration and has no path back for data (deferred: docs/adr/BACKLOG.md)
- Removing `palace.Dynamics`, whose `LastActivated` still feeds the hallway stamp repair (permanent: this task retires a wire surface, not an internal type)

## Verification Log

<!-- Tool-written by `adr-verify`. -->
- 2026-09-05 · 276cba9 · exit 0 · `set -o pipefail …` · acceptance-sha256:852859ee7b4643e593dd2089da2dec756723d2752e4fe17b61cd75fa821576f4 · ms:48918
- 2026-09-05 · 276cba9* · exit 0 · `set -o pipefail …` · acceptance-sha256:852859ee7b4643e593dd2089da2dec756723d2752e4fe17b61cd75fa821576f4 · ms:53319
- 2026-09-05 · 276cba9* · exit 0 · `set -o pipefail …` · acceptance-sha256:852859ee7b4643e593dd2089da2dec756723d2752e4fe17b61cd75fa821576f4 · ms:43806
- 2026-09-05 · 276cba9* · exit 0 · `set -o pipefail …` · acceptance-sha256:852859ee7b4643e593dd2089da2dec756723d2752e4fe17b61cd75fa821576f4 · ms:42792
