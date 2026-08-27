# ADR-039: A store you address by name

**Status:** Proposed
**Date:** 2026-08-27
**Owner:** M
**Spec:** None — no spec stage. Grounded in the declarations at `internal/palace/chunk.go:56` (`MaxEmbedRunes`), `:148` (`DrawerID`), `internal/palace/kg.go:35` (`MaxKGValueLen`), `internal/mcpserver/drawers.go:56` (`wholeMemoryBudget`), `db/migrations/00001_init.sql:61` (`skills`), `db/migrations/00010_kg.sql:22` (`kg_triples`), plus a serialisation measurement recorded inline.
**Cross-references:** ADR-027 (a maintained document is a set of records — **qualified here, see Invalidates**), ADR-036 (the bootstrap surface this complements), ADR-003 (retire the closet prior — the ranking cost this protects), ADR-010 (supersede, do not overwrite), ADR-013 (a page of memories, not chunks), ADR-016 (a memory an agent files must be navigable), `internal/mcpserver/catalog_test.go:212` (`TestEveryCatalogToolIsNamedInTheReadme`)
**Numbering:** next free after ADR-038 (open, PR #72). ⚠ **Allocate at merge** — a per-branch check is blind to cross-branch collisions, the rule this repo recorded after its ADR-number collision. Same for migration `00030`: `00029` is head on `main`.
**Invalidates:** none outright. ⚠ **QUALIFIES ADR-027 (Accepted)** for one class of document — see below. Checked by grepping ADR-001..038 for `key/value`, `kv_`, `author-chosen`, `addressed by name`: no accepted ADR governs a name-addressed store. ADR-027 governs the adjacent question and its rule 3 phrase ("the spine stores how to traverse, never what is there") is adopted here verbatim rather than displaced.
**Served-path change:** **Yes.** One new table, one new MCP tool. No existing tool signature, default, or ranking path changes.

## Context

Every session reaches its must-load tier through a chain: the `start-here` skill description names
room `llm_init`; a session lists that room; identifies the root **by reading its first line**; then
walks a 64-hex drawer id through the KG. ADR-036 measured the client-side form at **13 calls, ~99KB
(~25k tokens), plus a hardcoded root drawer id.**

Three hops exist only to compensate for a defect, and the root drawer carries prose to paper over
each: identify-by-content-prefix, because there is no address; three predicate sweeps, because
corrections are incoming edges; and `⚠IF STEP 1 RETURNS ZERO EDGES IT FAILED — STOP`, because
`am_kg_query` fails open.

**The root cause is that every address in this system is derived, not chosen.**
`DrawerID = sha256(teamID, wing, room, sourceFile, chunkIndex, content)` (`chunk.go:148`) — content
is in the hash, so a re-add, a `merge_wing` relabel or a chunk-parent change moves it. **You cannot
build a stable entry point on a content hash.**

## Existing Primitives Audit

Four candidates. None serves, and the reasons differ:

| Primitive | Shape | Why it does not serve |
|---|---|---|
| **Skills** (`00001_init.sql:61`) | `name` unique per team, `content`, `version` bumped on update — **a versioned KV keyed by name** | The closest existing thing. But a skill is a wake-up convention every session loads; filling it with bootstrap payloads pollutes `am_list_skills`, the surface agents read to learn how to work here. |
| **KG** (`00010_kg.sql:22`) | `source_drawer_id` defaults `''`, so facts stand alone; `valid_from`/`valid_to` + `KGInvalidate` is real versioning | `MaxKGValueLen = 128` runes (`kg.go:35`). A store for labels, not payloads. |
| **Drawers** | verbatim, chunked, embedded, ranked | Content-hash id; `MaxEmbedRunes = 4000` refuses an update above it (`service.go:775`) — which is why the live root drawer must instruct "DO NOT GROW THIS DRAWER". |
| **Artifacts** (upstream mempalace, HEAD `4c1e6d0`) | exact content, no chunk, no embed — the right blob semantics | `put` returns a **server-assigned id**; `get` takes that id. The create→get-id→remember-the-id dance again. |

**Three near-misses across two codebases, all failing on the same axis: the address is assigned, not
chosen.** That is the gap.

## Decision

Add **`am_kv_store`** — a team-scoped, versioned key/value store addressed by an author-chosen name.

```jsonc
am_kv_store({
  command: "read" | "write" | "delete" | "list" | "history",
  id:      "root",       // author-chosen key. NO wing param.
  payload: "…",          // write, <= 36,000 runes
  version:  3,           // read, optional -> latest
  limit: 50, offset: 0   // list | history
})
```

```sql
CREATE TABLE kv_entries (
    team_id    TEXT NOT NULL,
    id         TEXT NOT NULL,
    version    INTEGER NOT NULL,
    payload    TEXT NOT NULL,
    written_at TEXT NOT NULL,
    PRIMARY KEY (team_id, id, version)
);
```

Five properties, each load-bearing:

1. **The key is chosen, not derived.** `root` survives every edit. This is the whole point.
2. **`read` on a missing key is an ERROR.** Fails closed, unlike `am_kg_query`'s `count:0`-no-error.
   This retires `⚠IF STEP 1 RETURNS ZERO EDGES` from prose an agent must remember into the protocol.
3. **No embedding, no chunking, no ranking.** `MaxEmbedRunes` does not apply, and this content never
   enters the ranked pool.
4. **Keep all versions, paged.** `read` returns latest; `history` returns **metadata only**
   (`version`, `written_at`, `runes`, `first_line`) plus `total_versions`, newest first. `delete`
   takes every version of a key.
5. **Team-scoped; keys flat-dotted**, the same grammar as KG predicates, so one vocabulary. The tier
   lives in the key, so `list` with prefix `must.` **is** the must tier — no KG hop, and nothing
   points at a key for tier reasons.

Bootstrap end state:

```
am_kv_store({command:"read", id:"root"})    -> the blob, zero prior knowledge
am_kv_store({command:"list", id:"must."})   -> the must tier
am_kg_query({predicate:"retracts"})         -> corrections; the one thing only the graph can answer
```

## ⚠ How this qualifies ADR-027, stated rather than assumed

ADR-027 (Accepted) decides: *"A document intended to be maintained is stored as a SET of
single-chunk records linked from a spine, never as one long record."* The entrypoint root **is** a
maintained document, and this ADR proposes storing it as one record of up to 36,000 runes. That is
a direct interaction and it is the reviewer's call, not one to make by implication.

**The argument for qualifying it:** ADR-027's rationale and its falsifier are both *retrieval*-based
— rule 1 splits by question, and the stated falsifier is *"ask each part's question and record the
rank; if a part does not return at rank 1 for its own question, the split was by size wearing a
question's clothes."* That test presupposes the document is retrieved by question. A `kv_entries`
payload is **never retrieved** — no embedding, no ranking, no search path — so ADR-027's evidence
does not reach it, and its mechanism (single-chunk parts, because `ChunkSize`/`MaxEmbedRunes` bound a
drawer) solves a constraint that does not exist here.

**Proposed narrowing:** ADR-027 governs maintained documents **in the searchable corpus**. Documents
addressed only by name are out of its scope and governed here.

**ADR-027 rule 3 is adopted verbatim, not displaced:** the spine stores how to traverse, never what
is there. It is the discipline that keeps this tier from becoming a second corpus — see Risks.

## Alternatives Considered

- **A positional `[cmd, payload, version]` dispatcher** (the original proposal). REJECTED. A tool
  description is the strongest guidance surface — present at the moment of the call, in every client
  — and an opaque dispatcher deletes it at the call every session makes first.
  `TestEveryCatalogToolIsNamedInTheReadme` (`catalog_test.go:212`) already fails the build over
  undiscoverable capability. The flexibility is preserved without the cost: **the server defines no
  key semantics; each project writes its key conventions in its own `AGENTS.md`.**
- **Wing-scoped, with a `wing` column and param.** REJECTED. Reading the entrypoint would require
  resolving a wing first — a five-rung ladder, with `default_wing` empty on real registrations — but
  knowing how to work here is *what the entrypoint tells you*. Circular. The KG's team-wide scope
  hurts because facts surface **unbidden in recall**; a name-addressed store has no recall, so a
  collision needs two authors choosing one key and `list` exposes it.
- **Put payloads in the KG.** REJECTED at `MaxKGValueLen = 128`.
- **Put them in skills.** REJECTED — pollutes the wake-up surface.
- **Raise `MaxEmbedRunes`.** REJECTED — it would enlarge what gets embedded and ranked, the opposite
  of the goal.
- **A TTL / scratch tier.** REJECTED. Keys persist until `delete`.

## Consequences

**Retires:** the `start-here` → room → content-prefix → 64-hex chain; the "DO NOT GROW THIS DRAWER"
instruction; the `⚠IF STEP 1 RETURNS ZERO EDGES` trap; and handling 64-hex ids on the bootstrap path
at all, so `⚠IDS ARE FULL-LENGTH — a prefix silently creates a NEW node` stops applying there.

**Protects retrieval quality.** Bootstrap and session-start content are unrelated documents
competing in the ranked pool. ADR-003 retired the closet prior for what it did to top-1; this keeps
a whole class of ephemera out of that pool by construction rather than by discipline.

**Costs:** one new table, one new tool, one more thing `doctor` should know about. Keep-all-versions
on a hot key at 36,000 runes is unbounded — ~500 edits of the root is ~12MB for one key. Pruning to
last-N can be added later without an API change, because `history` is paged from day one.

## Out of Scope

- **Search over KV.** Deliberate — it is the known-address tier.
- **Relations.** `id|id|how` stays in the KG.
- **`kg_supersede`.** A real, separate gap: upstream mempalace ships it and its wake-up protocol
  warns against hand-rolling invalidate+add, and ADR-010 (Proposed) already covers the principle.
  Filed to `wing_agentmemories`/`inbox`; independent of this ADR.
- **Deploying `am_bootstrap`/`am_entry_point` and checking ADR-036's F-16 follow-up.** Independent
  and higher priority — they are already built and merged.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| **Content drifts in that should have been a memory** — 36,000 runes is a lot of room, and roots grow | High | High | ADR-027 rule 3, adopted verbatim: the spine stores how to traverse, never what is there. A gate is worth considering. This is the risk that would turn the tier into a second, unsearchable corpus. |
| **A payload near the cap spills to a file on read** and never enters context | Low | High | Cap is 36,000 runes against `wholeMemoryBudget = 40_000` (`drawers.go:56`). Measured JSON overhead: **4.2%** on prose markdown, **7.3%** on dense drawer text, so 36,000 serialises to ~37.5–38.6KB. **Refuse an over-cap write at `write` time**, never at read time where it is silent and unfixable. |
| **Migration `00030` collides with an open branch** | Low | High | Allocate at merge. |
| ⚠ **Two motivating measurements are DISPUTED** | — | Medium | This session measured `llm_open_threads` at 13 drawers and `am_skillset` at 40 tools against the hosted palace; a reviewer measured 1 drawer and both bootstrap tools live. Likely a `--local` vs hosted split. **Reconcile before either number is quoted in the accepted ADR.** The decision does not rest on them; the urgency claim does. |

## Rollback

A new table and a new tool, both additive. `00030` carries a `-- +goose Down`. A client ignoring the
tool sees today's behaviour. Removing it breaks only callers that adopted it — which is why adoption
should follow the measurement below, not a promise. Revert order: binary, tool registration,
migration.

## What would falsify this

**The bootstrap read must measurably beat the chain it replaces**, on calls and on output tokens,
**against the same palace**. If it does not, this has reproduced the problem behind a nicer name.
This is the same falsifier ADR-036 set for itself in F-16 — which is still unchecked, and which
should be checked first, since it may close most of the gap on its own.

## Follow-ups / still undecided

- [ ] **Does `doctor` see the store?** An unseen store is an unmaintained one.
- [ ] **Does a read count against quota?** Every handler calls `admit(ctx, usageSvc)`, so a KV read
      per bootstrap is a real per-session cost — deliberate, or exempt?
- [ ] Report the measured before/after for the bootstrap path, whichever way it falls.
- [ ] Reconcile the disputed palace measurements named in Risks.
- [ ] Confirm the ADR-027 qualification is the reviewer's intent, or narrow this ADR instead.
