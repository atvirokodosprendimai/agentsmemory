# ADR-049: Loopback is not a boundary a browser respects

**Status:** Accepted
**Date:** 2026-09-03
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/ADR-012-the-agent-surface-enforces-the-role-it-reports.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/auth/origin.go`, `internal/auth/auth.go`, `cmd/server/main.go`, `internal/mcpserver/server.go`
**Enforced-by:** `cmd/server/originwiring_test.go::TestTheLocalEndpointIsMountedBehindTheRebindGuard`
**Served-path change:** the credential-free local endpoints (`/mcp`, `/import`, `/stats`) answer `403` to a request whose `Host` or `Origin` names anything other than this machine, whenever the operator's boundary IS this machine — and the `initialize` handshake stops advertising `tools.listChanged`.

## Context

Measured 2026-09-03 against the shipped container at `fdc16e2`, over the same
`http://localhost:8080/mcp` this project's own agents are registered against:

    $ curl -X POST http://127.0.0.1:8080/mcp \
        -H 'Host: evil.example.com:8080' \
        -H 'Origin: http://evil.example.com:8080' \
        -d '{"jsonrpc":"2.0","id":8,"method":"tools/call",
             "params":{"name":"am_search","arguments":{"query":"…","wing":"*"}}}'
    HTTP 200
    {"jsonrpc":"2.0","id":8,"result":{"content":[{"type":"text","text":"{\"count\":1,…

That is a full cross-wing recall, returned to a request that says on its face it
was addressed somewhere else. Three further probes bound the finding: an
`initialize` with a hostile `Origin` succeeded (HTTP 200), a `tools/call`
carrying a deliberately invalid `Authorization: Bearer` succeeded, and a CORS
preflight returned `404` with no `Access-Control-Allow-*` header anywhere. The
tree contains no occurrence of the string `Origin` outside this change.

**The gap is not that local mode is unauthenticated — that is a decision, and it
is documented.** `LocalTenant`'s own doc comment states the threat model
exactly: *"With an empty token it authenticates nothing by design … That makes
the listen address the only thing standing between the caller and the whole
database, which is why local mode defaults to binding loopback."* The finding is
that the boundary named there does not hold against the attack the MCP
Streamable HTTP transport specifically calls out. An attacker page on
`evil.example` with a one-second DNS TTL re-resolves that name to `127.0.0.1`.
The browser then considers `http://evil.example:8080` **same-origin with the
page**, so it sends no preflight and CORS never runs — the 404 preflight above
protects nothing in that scenario. The packet arrives on the loopback interface
from a local process (the browser), which is precisely what "reachability is
authorization" was trusting.

What is reachable that way is not a subset: `am_search` accepts `wing: "*"`, so
one request reads every project in the palace, and the write tools are on the
same endpoint. This machine's palace holds 19 wings across unrelated clients.

The absent mitigation is the transport's own MUST: *servers MUST validate the
`Origin` header on all incoming connections to prevent DNS rebinding attacks*.
It has never been implemented on any path here.

## Existing Primitives Audit

`config.IsLoopback` already classifies a listen address (`config.go:57`) and
`publishedLoopback` already reads the compose file's assertion that a container's
port is published to the host loopback (`main.go:1525`). `serveLocal`'s exposure
warning already switches on exactly those two plus `SocketPath` to decide whether
to warn. So the predicate this decision needs exists three times in one switch
and had no name; it needed extracting, not inventing. `LocalTenant` is already
the single middleware wrapping all three credential-free endpoints, so there is
one seam to change rather than three.

## Decision

**Refuse a request that does not address this machine, wherever this machine is
the boundary.**

1. `auth.OffMachineAddressing(r)` returns the header that betrays a rebind, or
   `""`. `Origin` is judged when present — including the literal `null`, which a
   sandboxed iframe sends and which must not read as absent. `Host` is judged
   always, because the no-`Origin` variant of the attack (a form post, an `<img>`)
   carries no `Origin` at all and only `Host` still names where the request was
   addressed. "This machine" is `localhost` or any loopback literal, at **any**
   port.

2. The guard is a parameter of `LocalTenant`, not a second middleware. That is
   the argument that function's own comment already makes about the token: two
   middlewares let a caller mount the credential-free one by mistake. Here the
   stakes are the same — the guard exists to protect the assumption `LocalTenant`
   documents, so it must not be possible to mount `LocalTenant` without deciding
   about it.

3. It is ON when the operator's boundary is this machine — a loopback bind, a
   `--socket`, or a container publishing to the host loopback — and OFF when they
   have deliberately bound a routable address. `cmd/server.localBoundary` is that
   predicate, extracted so the guard and the boot warning cannot disagree.

4. The refusal is judged BEFORE the token. A browser on a rebound name holds no
   token either way, so both orders refuse; `401` would send an operator hunting
   for a credential problem that does not exist.

## Alternatives Considered

- **A new `--allowed-origin` flag as the primary mechanism.** Rejected because it adds a knob that is wrong-by-default — empty means either "allow all" or "allow none" and both are bad — and this repository's gates then require it to be populated, read, swept and documented. The deployment that genuinely needs a foreign `Host`, local mode behind a reverse proxy, has a better answer already shipped: `--token`, which an endpoint reachable by a name other than `localhost` needs regardless. Deferred, not dismissed — reconsider when a real operator hits it.
- **Validate `Origin` only, per the letter of the spec.** Rejected because it leaves the no-`Origin` variant open (a form post or an `<img>` to a rebound name sends none), and the check would then pass every non-browser client by doing nothing at all — a guard that is trivially satisfied is the shape of a test that asserts nothing.
- **Apply it unconditionally, including on routable binds.** Rejected as a silent breaking change delivered as a security fix. An operator running `--local` on a LAN address without a token was warned at boot and is reaching it by that address; turning that into a `403` is how a guard gets reverted wholesale instead of tuned.
- **Refuse at the mux instead of inside the middleware.** Rejected because the hosted path (`main.go:544`) sits behind the OAuth gate, where a rebound browser has no token and already fails. Widening the blast radius to a path that does not need it buys nothing and risks the deployment that has paying tenants.
- **A second middleware mounted beside `LocalTenant`.** Rejected for the reason `LocalTenant`'s own comment gives about folding the token check in: a separate middleware can be forgotten at a future mount, and the guard exists precisely to protect the assumption `LocalTenant` documents.

## Component / Boundary Impact

`internal/auth` gains one file and one parameter. `cmd/server` gains one
predicate. No storage, no schema, no tool description, no MCP wire change: a
conforming client is unaffected because a conforming client addresses the server
by the name it was registered under.

## Wiring & Contract Changes

`auth.LocalTenant` takes a third parameter. Every call site is updated in the
same commit (`cmd/server/main.go`, `internal/mcptest/harness.go`, and three in
`internal/auth/local_test.go`). The signature change is deliberate: it makes the
compiler force the question at every future mount.

## Inter-task Contracts

Single task; no producer/consumer coupling.

## Implementation

One task, T1. See `docs/adr/ADR-049-loopback-is-not-a-boundary-a-browser-respects/tasks/`.

## Consequences

A page in the operator's browser can no longer read or rewrite the palace by
renaming its own domain. The cost is a `403` for any deployment that reaches a
loopback-bounded server by a name that is not `localhost` — which is the
configuration this decision holds should be carrying a token anyway, and the
refusal names the header so the diagnosis is one line rather than a hunt.

The check runs per request and does two string comparisons; it is not on any
measured hot path and no benchmark was taken, which is stated rather than
implied.

**The handshake change rides with this record rather than under its own.** The same
probe run found `initialize` advertising `"tools":{"listChanged":true}` while nothing
in `internal/mcpserver` calls `SendNotificationToClient` and the transport is mounted
`WithStateLess`, so no code path could keep that promise. It is wire-visible, which is
why `Governs` and `Served-path change` name it rather than leaving it to ride silently:
a public contract changed, and a reader of this record has to be able to see that from
the header. It is not its own decision because there is nothing to decide — the
advertisement was false, and `TestNoCapabilityIsAdvertisedThatNothingCanDeliver` flips
it back the day something can deliver.

⚠ **THE SOCKET CASE WAS SHIPPED BROKEN AND CAUGHT BY REVIEW, NOT BY A TEST.** The first
version of `addressesThisMachine` accepted `localhost` and loopback literals only, while
`localBoundary` returns true for a `--socket` deployment — and the proxy dials
`http://unix/mcp` (`cmd/server/stdio.go`), so every socket client sends `Host: unix` and
would have been refused. Worse, that version's own comment cited `--socket` while
reasoning about the PORT, which is absent there; the header that mattered was the host.
Awareness of a case is not coverage of it, and T1's Stop Condition named this exact
failure without any test being able to reach it. `TestTheSocketAuthorityIsTheOneTheProxySends`
now pins the guard's exemption to the URL the proxy actually mints, across the package
boundary where the two literals live.

## Out of Scope

The hosted OAuth path. The idle `GET /mcp` stream that mcp-go holds open in
stateless mode, which the same probe run found and which is filed to
`docs/adr/BACKLOG.md`. The four MCP capabilities this server answers "not
supported" to (resources, prompts, completions, and structured tool output),
also filed there.

## Risks

**A legitimate deployment is refused.** Local mode behind a reverse proxy, or
reached by a machine hostname on the same box, now gets a `403`. Mitigated by
the message naming the header and by `--token` turning the guard's condition
off; recorded here as the known cost rather than discovered later.

**The guard drifts from the warning.** If someone adds a fourth bounded case to
the boot warning and not to `localBoundary`, the two disagree.
`TestTheGuardAgreesWithTheExposureWarning` pins the truth table.

**The wiring is removed while the tests stay green.** This is the failure mode
AGENTS.md §Reachability records four times, and it is live here because a caller
passing a literal `false` compiles and passes every behaviour test.
`TestTheLocalEndpointIsMountedBehindTheRebindGuard` reads the argument out of
`main.go`'s AST and fails on a constant. Demonstrated: three mutants, one per
half, each killed by the gate meant for it.

## Rollback

Pass `false` at the single call site in `main.go` and the guard is off, with the
behaviour tests still passing and the wiring gate failing loudly — which is the
correct signal, not an inconvenience. Revert the commit to remove it entirely.

## Follow-ups

- [ ] The idle `GET /mcp` stream: mcp-go answers `200` and holds the connection
      open in stateless mode, where nothing can ever be pushed to it. Measured
      2026-09-03: 25 concurrent GETs held, zero bytes delivered, server still
      responsive. `405` is what the transport suggests for a server that offers
      no stream. (deferred: `docs/adr/BACKLOG.md`)
- [ ] Whether the four unimplemented MCP capabilities are worth adding, resources
      first — a drawer is addressable content with a natural URI, and this corpus
      already has ADR-013, ADR-019 and ADR-044 about the cost of serving memory
      through tool results. (deferred: `docs/adr/BACKLOG.md`)
