# ADR-050: A memory has an address

**Status:** Accepted
**Date:** 2026-09-03
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** `docs/adr/ADR-013-a-page-of-memories-not-chunks.md`, `docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md`, `docs/adr/ADR-044-make-a-small-read-trustworthy.md`, `docs/adr/BACKLOG.md`
**Governs:** `internal/mcpserver/resources.go`, `internal/mcpserver/server.go`
**Enforced-by:** `internal/mcpserver/resources_test.go::TestTheResourceTemplateIsAdvertisedAndReadable`
**Served-path change:** the server advertises the `resources` capability, serves a `agentsmemory://wing/{wing}/room/{room}/drawer/{id}` template, answers `resources/read` for that URI with the whole memory, and every `am_search` hit and `am_get_drawer` response carries a `uri` naming itself.

## Context

Every route into a memory today spends it. `am_search` returns a window of the
text; `am_get_drawer` returns a chunk, or the whole thing if you knew to ask.
Both put the content in a tool result, which is the model's context, and both
require a tool call composed from ids the caller has to have kept.

That cost is the subject of four accepted records. ADR-013 stopped paging chunks
and started paging memories. ADR-019 returns the regions that matched rather than
one window, because the single-window chooser saturates and ties resolve to the
earliest position. ADR-024 ranks memories rather than chunks. ADR-044 exists to
make a small read trustworthy — `content_truncated` and `content_length` — because
the alternative was a fragment indistinguishable from a short memory. Every one
of those is a mitigation for the same underlying fact: **a memory is only ever
delivered by being spent.**

`am_search`'s own description records where that ends: *"there is no cursor"*. A
page is bounded, a hit can be `withheld`, and the only completion path is another
tool call.

MCP has a first-class answer that this server does not implement: a **resource**
is content with a URI, fetched by that URI, and — through `resources/templates/list`
— discoverable without enumerating anything. A drawer already has everything a
resource needs. It has a stable, opaque id (ADR-038 minted it once and stopped
recomputing it), it has a wing and a room, and `am_get_drawer whole=true` already
reassembles it.

What is missing is the address.

## Existing Primitives Audit

`Service.GetDrawer` with `whole` already reassembles a memory from its chunks and
is already the answer to "give me this thing entire" (`drawers.go:484`).
`reassembleMemory` and `MemoryChunks` exist and are used by both the search path
and `Bootstrap`. `palace.SanitizeName` already validates a wing and a room. So the
read side needs no new query — it needs a URI parser and a handler that calls what
is there. mcp-go supports resource templates and `resources/read` directly.

## Decision

**Give a memory an address, and hand the address out beside the content rather
than instead of it.**

1. **A URI shape that is readable and complete**:
   `agentsmemory://wing/{wing}/room/{room}/drawer/{id}`. It carries the wing and
   room because those are what scope a memory and what a reader needs to judge
   provenance — a bare id would be an opaque token that says nothing.

2. **`resources/templates/list` advertises it.** Not `resources/list`: this palace
   holds 3,400 drawers and enumerating them would be a page nobody asked for, in a
   protocol whose listing has no relevance ordering. A template lets a client
   construct the URI for a memory it already has a reason to want.

3. **`resources/read` returns the WHOLE memory**, via the existing `whole=true`
   path. A resource that returned one chunk would reintroduce the exact defect
   ADR-044 was written against, one protocol layer over.

4. **Search hits and drawer fetches carry `uri`.** This is the half that makes the
   address reachable: a caller that has a hit can fetch the whole memory without
   composing a tool call or knowing the scheme.

## Alternatives Considered

- **Return `ResourceLink` content blocks from `am_search` instead of text.** Rejected for now, and this is the one worth revisiting. It is the shape that would actually reduce what a page costs — links instead of snippets — but it changes what every existing caller receives, and this project has no measurement of whether an agent handed links will fetch what it needs or simply act on less. The `uri` field is the additive 80%: the address is available, nothing is taken away, and the measurement can be taken against real traffic before anything is removed. Deferred, not dismissed.
- **`resources/list` enumerating every drawer.** Rejected: 3,400 entries with no relevance order is a worse answer than the search that exists, and it would make the capability's most obvious call its least useful one.
- **A bare `agentsmemory://{id}` URI.** Rejected because provenance is the thing a reader most needs and most often lacks — the protocol's own rule is that a memory is evidence from a context you do not have, and an address that hides the project it belongs to argues against that.
- **Serving a chunk per URI.** Rejected for the reason ADR-044 records: a fragment that reads as a whole is the defect, and a URI per chunk would mint 3,400 addresses that are individually misleading.

## Component / Boundary Impact

`internal/mcpserver` gains one file and one capability declaration. No storage
change, no schema change, no new query. `palace` is untouched.

## Wiring & Contract Changes

The `initialize` response gains `resources`. Two new methods answer where they
previously returned `-32601`. `am_search` hits and `am_get_drawer` responses gain
a `uri` field — additive, and `omitempty` is deliberately NOT used, because a
field that is absent by construction cannot be discovered (the reason
`TestEveryOmitemptyWireKeyInThisPackageIsDescribed` exists).

## Inter-task Contracts

Single task; no producer/consumer coupling.

## Implementation

One task, T1. See `docs/adr/ADR-050-a-memory-has-an-address/tasks/`.

## Consequences

A memory can be fetched by address, by a client that never called a tool. The
address is stable for the life of the drawer, because ADR-038 made the id opaque
and permanent. What this does NOT do is reduce what a search page costs today —
that is the deferred alternative above, and saying so is the point: this record
adds an address, it does not yet spend less.

## Risks

**The URI outlives what it names.** A retracted memory keeps its id, so a stored
URI can resolve to an ended record. `resources/read` follows the same rule the
tools do — current by default — and says so, rather than silently serving history.

**A wing or room in a URI is caller-supplied.** They are sanitized exactly as the
tools sanitize them; a URI naming a wing the caller cannot see resolves to
nothing rather than to somebody else's memory.

## Rollback

Remove the capability declaration and the two handlers; the `uri` field is
additive and can stay. Nothing else depends on it.

## Follow-ups

- [ ] Measure whether returning `ResourceLink` blocks from `am_search` reduces
      what a page costs without costing recall — the deferred alternative, and the
      one that would actually spend less. (deferred: `docs/adr/BACKLOG.md`)
- [ ] Whether `resources/read` should accept a chunk id and resolve it to its
      parent, rather than requiring the memory's own id. (deferred:
      `docs/adr/BACKLOG.md`)
