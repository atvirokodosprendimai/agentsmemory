# Task ADR-045-T1: Remove the four inert dynamics keys from both wire views and gate their return

**Depends-on:** none
**Covers:** none — no spec stage
**Estimated scope:** S (one source file, one new test, one deleted allowlist entry)
**Owner:** unassigned
**Produces:** `TestNoUnwrittenDynamicsFieldReachesTheWire` and the reduced `tunnelView` / `hallwayView` shapes
**Consumes:** `palace.Dynamics`'s json tags, which are the gate's universe — an existing type, not produced by a sibling task
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
4. Record the two deferrals in `docs/adr/BACKLOG.md` naming ADR-045, in this same commit, so the
   pointers are honoured rather than merely resolvable.
5. Run the full suite as regression.

## Acceptance

```bash
set -o pipefail
go test ./internal/mcpserver/ -run 'TestNoUnwrittenDynamicsFieldReachesTheWire|TestUndescribedOnPurposeIsJustified|TestEveryOmitemptyWireKeyInThisPackageIsDescribed' -count=1 2>&1 | tee /tmp/adr045-t1.out && ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr045-t1.out && gofmt -l internal/mcpserver | (! grep -q .) && go vet ./... && go test ./... -count=1
```

The three named units run alone first, so no already-passing suite can carry the verdict, and the
`! grep -qE "no tests to run"` clause is what stops the fence exiting 0 before the new test exists —
a `-run` filter matching nothing is the vacuous-green shape this pipeline records as a finding.
`set -o pipefail` is required: without it the pipeline's status is `tee`'s, not the suite's. The full
run follows as regression, chained with `&&` so every stage must pass.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestNoUnwrittenDynamicsFieldReachesTheWire` | `internal/mcpserver/dynamicswire_test.go` | No json tag in this package's non-test sources names a field of `palace.Dynamics` | none — no spec stage |
| `TestNoUnwrittenDynamicsFieldReachesTheWire/a_returned_key_is_caught` | same | The falsifiability half: the detector reports a key when one IS present, driven over a fixture through the same function the gate uses | none — no spec stage |
| `TestUndescribedOnPurposeIsJustified` | `internal/mcpserver/wirekeys_test.go` | Existing gate; must stay green after the exemption is deleted | none — no spec stage |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestNoUnwrittenDynamicsFieldReachesTheWire` |
| 2 — something selects it | It is an ordinary test in the package, run by `go test ./...`; it needs no registration and no build tag, so nothing can leave it compiled-out the way `internal/mcptest`'s contract axis can be |
| 3 — the caller can discover it | `n/a: no declared interface` — this task REMOVES wire keys, so the discoverability question it raises is the inverse one, and `TestEveryOmitemptyWireKeyInThisPackageIsDescribed` is in the fence to prove the removal leaves no undescribed survivor |
| 4 — it is used | The gate's universe is `palace.Dynamics`'s own tags, so it is exercised by every future field added to that struct, not only by today's four |

## Mutation Log

<!-- Tool-written by `adr-verify --mutant`. Empty at authoring. -->

## Invariants

- `palace.Dynamics`, `initDynamics` and the four database columns are unchanged. This record retires a wire surface, not storage.
- `internal/palace/hallway.go:109` keeps reading `LastActivated` for the `created_at` repair — the #38 fix stays working.
- The gate's forbidden set is DERIVED from `palace.Dynamics`, never written out as a literal. A list kept beside the truth is the shape this repository rejects.

## Risks

- The gate could pass vacuously if its source glob returns nothing. Mitigated by failing on an empty read, the same guard `packageSources` uses at `wirekeys_test.go:208`.
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
