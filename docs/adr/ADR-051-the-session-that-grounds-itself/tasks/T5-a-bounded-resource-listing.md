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
