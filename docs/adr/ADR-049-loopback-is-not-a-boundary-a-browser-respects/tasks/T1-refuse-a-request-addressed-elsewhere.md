# Task ADR-049-T1: Refuse a request addressed elsewhere, wherever this machine is the boundary

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (one new file, one parameter, five call sites)
**Owner:** M
**Produces:** `The credential-free local endpoints refuse an off-machine Host or Origin`
**Consumes:** none
**Data dependency:** hermetic

## Goal

Close the DNS-rebinding hole the parent ADR measured: make the credential-free local
endpoints refuse a request whose `Host` or `Origin` names something other than this
machine, wherever the operator's boundary IS this machine, and gate the WIRING as well
as the behaviour.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/auth/origin.go` | add | the classifier: which header betrays a rebind, and what counts as this machine |
| `internal/auth/auth.go` | edit | `LocalTenant` takes the guard as a parameter and runs it before the token check |
| `cmd/server/main.go` | edit | `localBoundary` extracted from the exposure-warning switch and passed at the mount |
| `internal/mcptest/harness.go` | edit | call site; the harness binds an ephemeral loopback port and does not exercise the guard |
| `internal/auth/local_test.go` | edit | three call sites; `httptest.NewRequest` defaults `Host` to `example.com`, so these pass `false` |
| `README.md` | edit | the refusal text an operator will paste into a search |
| `internal/auth/origin_test.go` | add | behaviour, both directions |
| `cmd/server/originwiring_test.go` | add | the wiring gate, the guard/warning agreement, and the docs gate |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestOffMachineAddressingNamesTheHeaderThatBetraysARebind`, `TestLocalTenantRefusesARebindAndOnlyWhenBounded` and `TestARebindIsRefusedBeforeTheTokenIsRead` in `internal/auth/origin_test.go`. Run the Acceptance fence and confirm RED.
2. Write `OffMachineAddressing` and `addressesThisMachine` in `internal/auth/origin.go`.
3. Add the `machineBounded` parameter to `LocalTenant`, running the guard BEFORE the token check, and update all five call sites. `internal/mcptest/harness.go` and the three in `internal/auth/local_test.go` pass `false`, because `httptest.NewRequest` defaults `Host` to `example.com` and those tests are about the tenant and the token, not the addressing.
4. Extract `localBoundary` in `cmd/server/main.go` and pass it at the one mount.
5. Write `cmd/server/originwiring_test.go` — read the third argument out of `main.go`'s AST, pin the guard/warning truth table, and require the refusal text in `README.md`.
6. Add the README paragraph. Keep the refusal string on ONE line: the first version wrapped it across two and the docs gate correctly failed.
7. Run the three mutants in the Mutation Log and confirm each is killed by the gate meant for it.
8. Re-probe the RUNNING container after `scripts/redeploy.sh`, because a green suite says nothing about the artifact that is serving.

## Acceptance

```bash
gofmt -l cmd internal | (! grep -q .) && go vet ./... && \
go test ./internal/auth/ ./cmd/server/ \
  -run 'TestOffMachineAddressingNamesTheHeaderThatBetraysARebind|TestLocalTenantRefusesARebindAndOnlyWhenBounded|TestARebindIsRefusedBeforeTheTokenIsRead|TestTheLocalEndpointIsMountedBehindTheRebindGuard|TestTheGuardAgreesWithTheExposureWarning|TestTheRebindGuardIsNamedInOperatorDocs' \
  -count=1 2>&1 | tee /tmp/adr049-t1.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr049-t1.out && go test ./... -count=1
```

The `no tests to run` guard is not decoration: five of the six test names are new in this
commit, and a typo in one of them would otherwise let the fence pass over a test that was
never executed.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOffMachineAddressingNamesTheHeaderThatBetraysARebind` | `internal/auth/origin_test.go` | The classifier in both directions: every spelling of this machine passes — bare `localhost`, a bracketed IPv6 loopback, anything in `127/8`, an ephemeral port — while a rebound name, a foreign `Origin` on a local `Host`, the literal `null`, an unparseable `Origin` and a private LAN address are all refused | — |
| `TestLocalTenantRefusesARebindAndOnlyWhenBounded` | `internal/auth/origin_test.go` | The middleware answers 403 and does not reach the handler when bounded, and stays entirely out of the way when the operator has bound a routable address | — |
| `TestARebindIsRefusedBeforeTheTokenIsRead` | `internal/auth/origin_test.go` | With a token configured and none presented, the answer is 403 rather than 401 — the addressing is judged first, so the message names the real problem | — |
| `TestTheLocalEndpointIsMountedBehindTheRebindGuard` | `cmd/server/originwiring_test.go` | The third argument at the real mount is `localBoundary(cfg)` and not a constant, read out of `main.go`'s AST; its subtest drives the same extractor over a fixture that hard-codes `false` | — |
| `TestTheGuardAgreesWithTheExposureWarning` | `cmd/server/originwiring_test.go` | `localBoundary` is exactly the three conditions the boot warning treats as bounded, in both directions — a socket, a loopback bind and a loopback-published container are bounded; a bare routable bind, a LAN bind, and a routable bind carrying a token are not | — |
| `TestTheRebindGuardIsNamedInOperatorDocs` | `cmd/server/originwiring_test.go` | `README.md` carries the refusal text an operator meets in a 403 and will paste into a search | — |

The classifier is tested apart from the middleware because the middleware test can only
show that SOME refusal happened; it cannot show that a legitimate agent still gets through
on every spelling of this machine, and that population is wider than it looks.

## Reachability

The signature change makes the compiler demand an argument at every mount, and a caller
passing a literal `false` compiles, serves, and passes every behaviour test in
`internal/auth` — a green suite over a guard that does nothing, which is the defect
AGENTS.md §Reachability records four times.
`TestTheLocalEndpointIsMountedBehindTheRebindGuard` therefore reads the third argument
out of `main.go`'s AST rather than driving `serveLocal`, which needs a database, a vector
namespace and a listener before it reaches the line under test.

Its falsifiability case is a SUBTEST driving the SAME extractor over a fixture that hard-
codes `false`, because a tree with zero offenders cannot exercise the branch that reports
one. A copy of the loop would pin nothing: severing the real call site would leave the
subtest green.

## Mutation Log

One mutant per half, all three tool-written below by `adr-verify --mutant` against this
task's own Acceptance digest. ⚠ The first draft of this section was a hand-filled table of
the same three, which is the fabrication hole `adr-verify --mutant` exists to close — it
was replaced rather than kept beside the receipts, because two accounts of one measurement
is one account too many.

Worth reading twice: severing the guard fails `TestARebindIsRefusedBeforeTheTokenIsRead`
with `401`, not with a pass. A server missing the guard still refuses that one request —
for the wrong reason — which is exactly the shape a weaker assertion would have scored as
success.

- 2026-09-03 · fdc16e2* · mutant killed · exit 1 · `cmd/server/main.go` · the mount passes a constant, so the guard is compiled in and never selected — the reachability defect this repo keeps shipping · acceptance-sha256:260ea989c06cd7addfb6e387c5c7141e51b6e2464a09994c3abfe273ae74af20
- 2026-09-03 · fdc16e2* · mutant killed · exit 1 · `cmd/server/main.go` · localBoundary forgets the container case, so a loopback-published deployment silently stops being guarded while the boot warning still calls it bounded · acceptance-sha256:260ea989c06cd7addfb6e387c5c7141e51b6e2464a09994c3abfe273ae74af20
- 2026-09-03 · fdc16e2* · mutant killed · exit 1 · `internal/auth/auth.go` · the guard is present and inert — the shape a green suite cannot tell from a guard that works · acceptance-sha256:260ea989c06cd7addfb6e387c5c7141e51b6e2464a09994c3abfe273ae74af20

## Invariants

- The guard never runs when `machineBounded` is false. An operator who deliberately bound
  a routable address was warned at boot and must not be broken by a security fix.
- `localBoundary` and the exposure-warning switch stay one predicate. If a fourth bounded
  case is added to one, the agreement gate fails until it is added to the other.
- The refusal precedes the token check.
- The hosted OAuth path is untouched.

## Risks

Local mode reached by a name that is not `localhost` — behind a reverse proxy, or by the
machine's own hostname — now answers `403`. That configuration should be carrying
`--token`, which turns the guard's condition off; the message names the header so the
diagnosis is one line. Recorded as the known cost, not discovered later.

## Stop Condition

Stop and raise it if the guard refuses any request from a real registered client in the
live smoke — that would mean `addressesThisMachine` is too narrow, and the fix is the
classifier, not the wiring.

## Out of Scope

The hosted OAuth path. The idle `GET /mcp` stream. The four unimplemented MCP
capabilities. All three are Follow-ups on the parent record.

## Verification Log

⚠ **The order actually taken was implementation first, then tests, then mutation** — not
the TDD-red step 1 prescribes. Step 1 is written as the order to re-run this in, and the
Mutation Log is what stands in for the red evidence TDD would have produced: each of the
three halves was severed against a green tree and the gate meant for it went red. That is
the stronger evidence of the two for a WIRING gate, because a test written before the
wiring exists is red for the trivial reason that nothing compiles.

Filled by `adr-verify`.
- 2026-09-03 · fdc16e2* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:260ea989c06cd7addfb6e387c5c7141e51b6e2464a09994c3abfe273ae74af20 · ms:48964
- 2026-09-03 · fdc16e2* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:260ea989c06cd7addfb6e387c5c7141e51b6e2464a09994c3abfe273ae74af20 · ms:44001
- 2026-09-03 · fdc16e2* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:260ea989c06cd7addfb6e387c5c7141e51b6e2464a09994c3abfe273ae74af20 · ms:40746
- 2026-09-03 · fdc16e2* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:260ea989c06cd7addfb6e387c5c7141e51b6e2464a09994c3abfe273ae74af20 · ms:35851
