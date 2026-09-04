# Task ADR-051-T5: A bounded resources/list so an address is discoverable

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (one handler, one registration)
**Owner:** unassigned
**Produces:** `a bounded resource listing`
**Consumes:** none
**Data dependency:** hermetic for the fence; the Stop Condition needs a live client

## Goal

Make ADR-050's addresses discoverable in the client that matters, without reintroducing the
enumeration ADR-050 rejected.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/resources.go` | edit | register concrete resources alongside the template |
| `internal/mcpserver/resources_test.go` | edit | the listing gate |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestTheResourceListingIsBounded` and
   `TestAListedResourceUriReadsBack` both fail against today's empty listing. Run the fence and
   confirm RED.
2. **Then measure, before choosing the shape.** In a session that connects AFTER the server
   advertises resources, call `ReadMcpResourceTool` with a template-matching URI and record the
   answer in the Verification Log whichever way it falls. It decides whether this task is a
   discoverability fix or a reachability fix, and it is cheap. It is step 2 rather than step 1
   because the red must come first even when an observation may change the target.
3. Serve a BOUNDED listing: the N most recently filed current memories in the caller's default
   wing, each as a concrete resource with its `agentsmemory://` URI, name and description.
   Not the palace. The bound is the whole point.
4. Keep the template registered. A client that reads templates keeps the general form; a client
   that reads only the list gets a door.
5. Run the fence, the mutants, the full suite, then re-probe a live client.

## Acceptance

```bash
gofmt -l cmd internal | (! grep -q .) && go vet ./... && \
go test ./internal/mcpserver/ \
  -run 'TestTheResourceListingIsBounded|TestTheListingAndTheTemplateBothResolve|TestAListedResourceUriReadsBack' \
  -count=1 2>&1 | tee /tmp/adr051-t5.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr051-t5.out && go test ./... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheResourceListingIsBounded` | `internal/mcpserver/resources_test.go` | With far more memories than the bound, the listing returns exactly the bound — the assertion that fails if someone later "helpfully" removes the limit | — |
| `TestTheListingAndTheTemplateBothResolve` | `internal/mcpserver/resources_test.go` | `resources/list` is non-empty AND `resources/templates/list` still carries the template; adding the first must not cost the second | — |
| `TestAListedResourceUriReadsBack` | `internal/mcpserver/resources_test.go` | Every URI the listing hands out resolves through `resources/read` — a listing whose entries do not read is worse than an empty one | — |

## Reachability

`TestAListedResourceUriReadsBack` is the gate that matters: a listing is a set of promises, and
an entry that does not resolve is a pointer to nothing, which this corpus already treats as
worse than no pointer.

## Mutation Log

Filled by `adr-verify --mutant`. At minimum: the bound removed, and the template deregistered.
- 2026-09-04 · 673d52d* · mutant killed · exit 1 · `internal/mcpserver/resources.go` · the bound is not passed to the query: the listing returns whatever the service default yields instead of the doorway, which is the enumeration ADR-050 rejected · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d
- 2026-09-04 · 673d52d* · mutant killed · exit 1 · `internal/mcpserver/server.go` · the listing hook never registered: resources/list answers [] again, so the addresses stay undiscoverable in the client whose documented discovery calls name it · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d
- 2026-09-04 · 673d52d* · mutant killed · exit 1 · `internal/mcpserver/resources.go` · the listing hands out an address that does not resolve — a pointer to nothing, discovered by a caller rather than by us · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d

## Invariants

- The listing is bounded and the bound is asserted.
- Ended memories are never listed — ADR-050's current-only rule holds on both routes.
- The template stays.

## Risks

A listing scoped to a default wing is wrong for a registration that names none. Serve nothing
rather than serving another project's memories.

## Stop Condition

Stop and reconsider the shape if step 1 shows `ReadMcpResourceTool` already resolves a
template URI — then the capability is reachable and only *discovery* was blind, which may be
better answered by documentation than by a listing.

## Out of Scope

- Enumerating every drawer. (permanent: fact: rejected in ADR-050 because thousands of entries with no relevance order is a worse answer than the search that exists; citation: `docs/adr/ADR-050-a-memory-has-an-address.md` §Alternatives Considered)
- `ResourceLink` blocks from `am_search`. (deferred: `docs/adr/ADR-050-a-memory-has-an-address.md` §Follow-ups)

## Verification Log

Filled by `adr-verify`.
- 2026-09-04 · 673d52d* · exit 1 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d · ms:46552
  ```
  --- last 10 line(s) of stdout (of 4239 after folding 4239 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	3.456s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	3.586s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	3.496s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	3.947s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	3.273s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	3.566s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	3.639s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	3.285s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	4.636s
  FAIL
  ```
- 2026-09-04 · 673d52d* · exit 1 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d · ms:34983
  ```
  --- last 10 line(s) of stdout (of 4239 after folding 4239 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.667s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	1.554s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	1.258s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.636s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	1.442s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	1.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	1.520s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	1.070s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.484s
  FAIL
  ```
- 2026-09-04 · 673d52d* · exit 1 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d · ms:36203
  ```
  --- last 10 line(s) of stdout (of 4239 after folding 4239 raw)
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.788s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	1.484s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	1.182s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	1.643s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/updatecheck	1.436s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.938s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	1.359s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	1.322s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	1.116s
  FAIL
  ```
- 2026-09-04 · 673d52d* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d · ms:35688
- 2026-09-04 · 673d52d* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d · ms:43866
- 2026-09-04 · 673d52d* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d · ms:33478
- 2026-09-04 · 673d52d* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:aca6fe0ea4085afb98ec51e202c4e3c324a4e6805cc6a7c2ca82606c1d45d64d · ms:32817
