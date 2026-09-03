# Task ADR-050-T1: Serve one memory by URI, advertise the template, and hand the address out with every hit

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (one new file, one capability option, one struct field)
**Owner:** M
**Produces:** `A memory is addressable at agentsmemory://wing/{wing}/room/{room}/drawer/{id}`
**Consumes:** none
**Data dependency:** hermetic

⚠ **No `Proof map:` header, for the reason ADR-049-T1 records rather than as a silent
pass.** `adr-lint` advises one on a newly authored task. The corpus still holds no example
to copy, so the format would be guessed, and a guessed structure that lints clean while
meaning nothing is worse than an admitted absence. The Tests table names the file and the
property for every gate, and the Mutation Log carries four tool-written receipts.

## Goal

Give a memory an address: serve `resources/read` for a drawer URI with the WHOLE memory,
advertise the shape through `resources/templates/list`, and put the `uri` on every drawer
view so a caller never has to compose one.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/resources.go` | add | the URI scheme, the template, the read handler, and the parser |
| `internal/mcpserver/server.go` | edit | declare the `resources` capability and register the template |
| `internal/mcpserver/drawers.go` | edit | `drawerView.URI`, populated in `toView` |
| `internal/mcpserver/resources_test.go` | add | reachability, wholeness, address integrity, and the round trip |

## Ordered Steps

1. Write the failing tests first (TDD red): the four names in the Tests table, in
   `internal/mcpserver/resources_test.go`. Run the Acceptance fence and confirm RED.
2. Write `resources.go`: `drawerURITemplate`, `registerResources`, `readDrawerResource`,
   `drawerURI`, `parseDrawerURI`.
3. Declare `server.WithResourceCapabilities(false, false)` in `New` and call
   `registerResources(srv, deps.Drawers, deps.Usage)` beside `registerPrompts`.
4. Add `URI string` under the `uri` JSON key to `drawerView` — deliberately NOT
   `omitempty` — and populate it in `toView`.
5. Run the Acceptance fence and confirm GREEN.
6. Run the four mutants in the Mutation Log with `adr-verify --mutant`; confirm each is
   killed by the gate meant for it.
7. Probe a RUNNING server over HTTP: `initialize` advertises `resources`,
   `resources/templates/list` returns the template, `am_add_drawer` hands back a `uri`,
   `resources/read` on it returns the content, and the same URI with a different wing is
   refused.

## Acceptance

```bash
gofmt -l cmd internal | (! grep -q .) && go vet ./... && \
go test ./internal/mcpserver/ \
  -run 'TestTheResourceTemplateIsAdvertisedAndReadable|TestAResourceReturnsTheWholeMemory|TestAnAddressThatNoLongerDescribesItsTargetIsRefused|TestEveryDrawerViewCarriesItsAddress' \
  -count=1 2>&1 | tee /tmp/adr050-t1.out; \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr050-t1.out && go test ./... -count=1
```

The `no tests to run` guard is not decoration: all four test names are new in this commit,
and a typo in one would otherwise let the fence pass over a test that never ran.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheResourceTemplateIsAdvertisedAndReadable` | `internal/mcpserver/resources_test.go` | Drives a real in-process client: `initialize` advertises the `resources` capability, `resources/templates/list` returns a described template carrying `{id}`, and a URI built by `drawerURI` reads back the memory's own text | — |
| `TestAResourceReturnsTheWholeMemory` | `internal/mcpserver/resources_test.go` | A URI naming a NON-FIRST chunk of a multi-chunk memory returns more than that chunk — the property that separates a resource from `am_get_drawer`'s default | — |
| `TestAnAddressThatNoLongerDescribesItsTargetIsRefused` | `internal/mcpserver/resources_test.go` | Five addresses that must not resolve: wrong wing, wrong room, a foreign scheme, a well-formed URI naming no drawer, and a truncated path | — |
| `TestEveryDrawerViewCarriesItsAddress` | `internal/mcpserver/resources_test.go` | `toView` populates `uri`, and the address the server renders parses back to the same three parts — including a room and an id containing the path separator | — |

The capability check is inside the reachability test rather than beside it because the two
halves fail together in the shape that matters: a registered handler behind an undeclared
capability is invisible to a conforming client, which never asks.

## Reachability

`registerResources` can be written, tested and correct while nothing calls it — the defect
AGENTS.md §Reachability records repeatedly. So the gate does not call `registerResources`;
it drives a client through `New`, which is the only path production takes. Deleting the
registration line leaves the handler compiled, the package green everywhere else, and this
test red at the template listing.

The `uri` field has the mirror-image failure: emitted but never populated. That is why
`TestEveryDrawerViewCarriesItsAddress` asserts on `toView`'s output rather than on the
struct tag, and why the field is not `omitempty` — an absent-by-construction field cannot
be discovered, which is the reason `TestEveryOmitemptyWireKeyInThisPackageIsDescribed`
exists.

## Mutation Log

One mutant per property, each severing a different half against a green tree. All four are
tool-written below by `adr-verify --mutant` against this task's own Acceptance digest — a
hand-filled table of the same four was written first and rejected by `adr-lint`, which is
exactly the fabrication hole that flag exists to close.

Worth reading twice: the registration mutant is the one a behaviour test cannot see. With
`registerResources` never called the handler still compiles, `readDrawerResource` is still
correct, and every other test in the package still passes — the failure is that no client
can ever ask.

⚠ **The address-integrity mutant is recorded twice, and the first attempt is left in the
log rather than tidied away.** Severing the check as `if false {` left `wing` and `room`
unused, so the fence died on a BUILD error and `adr-verify` correctly scored it
`inconclusive` rather than `killed` — a compiler refusing to build a mutant proves nothing
about the test, and the same severance run by hand earlier in the session had been read as
a kill. The mutant that counts is the one that compiles: `if _, _ = wing, room; false {`.
That is the whole reason the receipts are tool-written.
- 2026-09-03 · ad6ef3a* · mutant killed · exit 1 · `internal/mcpserver/server.go` · registerResources is never called, so the handler is compiled in and no conforming client can reach it — the reachability defect this repo keeps shipping · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499
- 2026-09-03 · ad6ef3a* · mutant killed · exit 1 · `internal/mcpserver/resources.go` · the handler resolves the single chunk the id names instead of the memory, reintroducing ADR-044 fragment-as-whole one protocol layer up · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499
- 2026-09-03 · ad6ef3a* · mutant inconclusive · exit 1 · `internal/mcpserver/resources.go` · the wing and room in the address are parsed and then ignored, so a stale URI returns a memory while displaying somebody elses provenance · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499
  ```
  the fence failed on a build/parse error, not an assertion
  ```
- 2026-09-03 · ad6ef3a* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · toView renders an empty uri, so the template is advertised and no response ever hands out an address that fits it · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499
- 2026-09-03 · ad6ef3a* · mutant killed · exit 1 · `internal/mcpserver/resources.go` · the wing and room in the address are parsed and then ignored, so a stale or hand-edited URI returns a memory while displaying somebody elses provenance · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499

## Invariants

- `resources/read` returns the whole memory. There is no `whole` parameter, because a URI
  names a thing and half of it is not that thing.
- The wing and room in an address are CHECKED against the record, never trusted.
- No `resources/list`. Enumerating thousands of drawers with no relevance order would make
  the capability's most obvious call its least useful one.
- The read is metered through the same `admit` the tools use; an uncounted route into the
  same data is a hole in the meter, not a feature.
- `uri` is not `omitempty`.

## Risks

A stored URI outlives what it names: a retracted memory keeps its id, and the handler
answers not-found rather than serving history. That is the intended behaviour and the
template's description says so, but a caller that cached an address will meet it.

## Stop Condition

Stop and raise it if serving the whole memory turns out to cost materially more than
`am_get_drawer` on a large record — the address would then need a bounded form, and that
is a decision, not an implementation detail.

## Out of Scope

`ResourceLink` blocks from `am_search`, and `resources/read` accepting a chunk id. Both are
Follow-ups on the parent record; the first is the one that would actually spend less, and
it is deferred for want of a measurement rather than dismissed.

## Verification Log

⚠ **The order actually taken was implementation first, then tests, then mutation** — not
the TDD-red step 1 prescribes. Step 1 is written as the order to re-run this in, and the
Mutation Log is what stands in for the red evidence TDD would have produced: each half was
severed against a green tree and the gate meant for it went red. For a REACHABILITY gate
that is the stronger evidence of the two, because a test written before the wiring exists
is red for the trivial reason that nothing compiles.

Filled by `adr-verify`.
- 2026-09-03 · ad6ef3a* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499 · ms:46409
- 2026-09-03 · ad6ef3a* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499 · ms:35343
- 2026-09-03 · ad6ef3a* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499 · ms:34156
- 2026-09-03 · ad6ef3a* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499 · ms:32393
- 2026-09-03 · ad6ef3a* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499 · ms:34779
- 2026-09-03 · ad6ef3a* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:69cceffff08511bd63cfe018ad5d81dca5a273b3e8d1d2301d4d13ea6d6c0499 · ms:36746
