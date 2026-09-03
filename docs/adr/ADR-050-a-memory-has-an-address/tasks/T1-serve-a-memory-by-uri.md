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
  -run 'TestTheResourceTemplateIsAdvertisedAndReadable|TestAResourceReturnsTheWholeMemory|TestAnAddressThatNoLongerDescribesItsTargetIsRefused|TestACaseVariantAddressDoesNotResolve|TestAnAddressForAnEndedRecordIsRefused|TestEveryDrawerViewCarriesItsAddress' \
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
| `TestACaseVariantAddressDoesNotResolve` | `internal/mcpserver/resources_test.go` | Two wings differing only in case hold two different memories, and each one's own address still resolves while the crossed address is refused — the case the first implementation got wrong | — |
| `TestAnAddressForAnEndedRecordIsRefused` | `internal/mcpserver/resources_test.go` | The address resolves while the record is current and stops resolving once it is invalidated — the promise the template's own description makes | — |
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

⚠ **AN INDEPENDENT REVIEW FOUND A CORRECTNESS BUG THAT EVERY GATE HERE PASSED OVER, AND
THE FIXTURE WAS HALF THE REASON.** The handler joined the chunks with a newline.
`ChunkText` overlaps adjacent chunks by 320 runes for context continuity, so the response
repeated up to 320 characters at every seam and inserted separators the memory never had —
longer than any chunk, containing the memory's words, and not the memory. Nothing
downstream can detect that: duplicated prose reads as prose. `palace.ReassembleMemory`
exports the implementation the search path already used.

The wholeness assertion went through three versions before it could see any of this, and
each failure is worth keeping because each is a different way to write a test that cannot
fail. **v1** compared lengths — the head is 1600 runes and the addressed last chunk 929, so
a `chunks[:1]` handler passed. **v2** required the text to CONTAIN both ends — but the
fixture repeated ONE sentence, so the last chunk was a literal substring of the first and
`chunks[:1]` passed again. **v3** compares byte-for-byte against what was filed, over a
fixture whose every sentence is numbered. A fixture whose pieces are indistinguishable
cannot witness a claim about which piece came back.

⚠ **AND ONE REVIEW FINDING DID NOT HOLD, WHICH IS REPORTED RATHER THAN QUIETLY DROPPED.**
Review argued a URI naming an ENDED CHILD could resolve to its surviving siblings, since
`MemoryChunks` resolves any id to its root and `GetMemory` refuses only when every chunk
has ended. Probed both routes it named — invalidating one child of a five-chunk memory, and
shortening the memory through `Update`. Both end the whole family (5 ended, 0 current),
because a memory ends whole and `TestNoMemoryEndsHalfway` pins it. The mixed family is not
reachable through the public API, so the mutant severing that branch SURVIVES and is
recorded below as a survivor. The guard stays as defence against an invariant break; its
call site is selected by the wing/room check regardless.

⚠ **A FIFTH MUTANT WAS ADDED AFTER THE FIRST IMPLEMENTATION SHIPPED THE BUG IT CATCHES.**
The address check was first written with `strings.EqualFold`, which reads like defensive
leniency and is in fact a hole: `SanitizeName` preserves case, so `wing_acme` and
`wing_ACME` are two wings holding two different memories (probed directly — two `Add` calls
returned two distinct drawer ids). The folded comparison therefore resolved an address
naming one wing against a record living in the other, which is exactly the crossing the
check exists to refuse. Found by reading the normalisation the palace actually applies
rather than by any gate; the mutant restoring `EqualFold` is now killed.

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
- 2026-09-03 · 84fcae8* · mutant killed · exit 1 · `internal/mcpserver/resources.go` · the folded comparison restored: SanitizeName preserves case, so wing_acme and wing_ACME hold different memories and an address naming one resolves a drawer living in the other — the exact crossing the check exists to refuse · acceptance-sha256:975745c5e13a9f3bb94dafffd70b46bddbee5d4fc53ea858296a4617d7a5a413
- 2026-09-03 · 4a1e912* · mutant survived · exit 0 · `internal/mcpserver/resources.go` · the handler serves only the memorys FIRST chunk — the fragment bug a length comparison could not see, since the head is longer than the addressed last chunk · acceptance-sha256:975745c5e13a9f3bb94dafffd70b46bddbee5d4fc53ea858296a4617d7a5a413
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-03 · 4a1e912* · mutant killed · exit 1 · `internal/mcpserver/resources.go` · the handler serves only the memorys FIRST chunk — the fragment bug both a length comparison and a homogeneous fixture were blind to · acceptance-sha256:975745c5e13a9f3bb94dafffd70b46bddbee5d4fc53ea858296a4617d7a5a413
- 2026-09-03 · 4a1e912* · mutant killed · exit 1 · `internal/mcpserver/resources.go` · chunks joined raw instead of reassembled: adjacent chunks overlap by 320 runes, so every seam repeats text and the separator inserts bytes the memory never had — corruption that reads as prose · acceptance-sha256:bc3acad0ab30849d6164acf3ffb1381884095c03c0e85046f648ee00dcd7b273
- 2026-09-03 · 4a1e912* · mutant survived · exit 0 · `internal/mcpserver/resources.go` · the addressed row is fetched WITHOUT the current-only check, so a URI naming an ended record resolves to whatever of its family survived — the false promise the template description makes · acceptance-sha256:bc3acad0ab30849d6164acf3ffb1381884095c03c0e85046f648ee00dcd7b273
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-03 · 4a1e912* · mutant killed · exit 1 · `internal/mcpserver/resources.go` · the refusal names where the record really lives again, turning a wrong guess into a lookup for a caller holding only an id · acceptance-sha256:bc3acad0ab30849d6164acf3ffb1381884095c03c0e85046f648ee00dcd7b273

## Invariants

- `resources/read` returns the whole memory, REASSEMBLED rather than joined. There is no
  `whole` parameter, because a URI names a thing and half of it is not that thing — and
  adjacent chunks overlap by `ChunkOverlap` (320) runes, so concatenating them repeats
  text at every seam. `palace.ReassembleMemory` is the implementation the search path
  already uses.
- A refusal does not name where the record really lives. Saying so would turn a wrong
  guess into a lookup for a caller holding only an id.
- The wing and room in an address are CHECKED against the record, never trusted, and the
  comparison is EXACT. `SanitizeName` preserves case, so `wing_acme` and `wing_ACME` are
  two wings holding two different sets of memories — measured, not assumed. A folded
  comparison is one case-fold wider than the palace and lets an address naming one wing
  return a memory living in the other.
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
- 2026-09-03 · 84fcae8* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:975745c5e13a9f3bb94dafffd70b46bddbee5d4fc53ea858296a4617d7a5a413 · ms:45112
- 2026-09-03 · 84fcae8* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:975745c5e13a9f3bb94dafffd70b46bddbee5d4fc53ea858296a4617d7a5a413 · ms:33559
- 2026-09-03 · 4a1e912* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:975745c5e13a9f3bb94dafffd70b46bddbee5d4fc53ea858296a4617d7a5a413 · ms:47280
- 2026-09-03 · 4a1e912* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:975745c5e13a9f3bb94dafffd70b46bddbee5d4fc53ea858296a4617d7a5a413 · ms:34105
- 2026-09-03 · 4a1e912* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:bc3acad0ab30849d6164acf3ffb1381884095c03c0e85046f648ee00dcd7b273 · ms:45413
- 2026-09-03 · 4a1e912* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:bc3acad0ab30849d6164acf3ffb1381884095c03c0e85046f648ee00dcd7b273 · ms:34942
- 2026-09-03 · 4a1e912* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:bc3acad0ab30849d6164acf3ffb1381884095c03c0e85046f648ee00dcd7b273 · ms:35293
- 2026-09-03 · 4a1e912* · exit 0 · `gofmt -l cmd internal | (! grep -q .) && go vet ./... && \ …` · acceptance-sha256:bc3acad0ab30849d6164acf3ffb1381884095c03c0e85046f648ee00dcd7b273 · ms:33645
