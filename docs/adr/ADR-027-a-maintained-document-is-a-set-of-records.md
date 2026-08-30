# ADR-027: A maintained document is a set of records, not a long one

**Status:** Accepted
**Date:** 2026-08-25
**Owner:** unassigned
**Spec:** None — no spec stage; grounded in a server-side measurement of the live palace and the declarations at `internal/palace/chunk.go:20,56` and `internal/palace/service.go:775`, recorded inline.
**Cross-references:** ADR-010 (supersede, do not overwrite — delete-and-refile is exactly the history loss this avoids; **superseded by ADR-038 on 2026-08-27**), ADR-013 (a page of memories, not chunks), ADR-016 (a memory an agent files must be navigable — the same reachability principle, one layer down), ADR-019 (the agent sees a quarter of the memory), issue #39 part 2 (re-chunking on update — the server-layer alternative this declines), `internal/palace/chunk.go:20` (`ChunkSize`), `:56` (`MaxEmbedRunes`), `internal/palace/service.go:775` (the refusal), `internal/mcpserver/drawers.go:601` (`content_length` = `len([]rune(...))`)
**Numbering:** next free after ADR-026.
**Invalidates:** none — checked (grepped ADR-001..026 for `spine`, `linked drawers`, `one record per question`, `MaxEmbedRunes`, `re-chunk`: no accepted ADR governs how a long maintained document is stored).
**Served-path change:** **None.** No code, no schema, no tool signature, no default. This is a convention binding on agents that write to the palace, plus the repair of one live drawer that the convention makes necessary.

## Context

**The measurement, taken 2026-08-25 from the server's own report, not by eye.**
`content_length` is computed as `len([]rune(fullContent))` (`drawers.go:601`), so it is
runes and directly comparable to the bound:

| drawer | room | runes | bound | state |
|---|---|---|---|---|
| `688f73e994504763800fa62c4f0a6830edd620e98852379139740cfca6d88fd5` | `llm_open_threads` | **6448** | `MaxEmbedRunes` = 4000 | **61% over — already un-updatable** |

This is not a drawer at risk. `Service.Update` refuses above the bound
(`service.go:775`), so the sixteenth refresh pass of the team's live open-threads
list fails, and the failure lands on whoever next tries to record an open thread.

**How it got there is the part worth recording, because it is not carelessness.**
Two limits bind at different times:

- `ChunkSize` = 1600 runes binds at **creation**. A memory created at or under it
  stays one row and stays editable in place — at any size.
- `MaxEmbedRunes` = 4000 binds on **every update**, and was introduced
  2026-08-25.

The open-threads drawer was created short *precisely to obey the first rule*, then
grown in place across fifteen passes over two days — correct behaviour under the
rule as it stood. A bound introduced later then invalidated a decision taken
earlier, at a moment nobody was looking, and nothing warned on the way past it.
**The drawer that best exemplified the rule is the first casualty of the rule
becoming bounded.**

**The palace already solved this one layer up, and measured it.** On 2026-08-24
`llm_index` was filed as a single ~2800-char memory, so the server stored it as two
chunks and `am_update_drawer` refused every in-place edit. Its own closing line read
*"add a line here when a room appears"* — a maintenance instruction the store could
not honour. The repair was to split it into two single-chunk drawers, each opening
with the question it answers:

| record | rank for its own question |
|---|---|
| `WHAT SHOULD I LOAD NEXT / WHERE DO I LOOK / WHAT EXISTS` | 1 (0.953) |
| `WHICH ENTITY NAME DOES am_kg_query ACTUALLY RESOLVE?` | 1 (0.953) |

The finding recorded at the time: *splitting cost nothing and bought editability,
because two questions were being answered by one record anyway.*

**And the general form is already running in production at the top level.** The
`llm_init` root is a spine that stores no list of its contents; the `must.*` KG
edges are the parts; the bootstrap reads the spine and traverses. That mechanism has
carried every session in this wing since 2026-08-24. This ADR does not invent a
pattern — it names the one already load-bearing and scales it down a level.

## Existing Primitives Audit

- **`am_kg_add` / `am_kg_query`** (`internal/palace/kg.go`) — already the traversal
  mechanism the bootstrap runs on, already carrying typed `must.*` / `ref.*`
  predicates between drawer ids. Reuse verbatim; nothing new is required of them.
- **The `llm_init` root drawer** — already a spine that states a traversal rule and
  deliberately holds no count and no list. Reuse the shape; it is the working
  precedent for rule 3 below.
- **`am_add_drawer`'s single-chunk behaviour** (`ChunkText`, `chunk.go:72`) — already
  returns one chunk at or under `ChunkSize`. Reuse: the parts are ordinary short
  drawers, with no special status.
- **`content_length` on search results** (`drawers.go:601`) — already reports the
  rune length of a whole memory. Reuse as the measuring instrument; this ADR needs
  no new tooling to detect the condition it repairs.

## Decision

**A document intended to be maintained is stored as a SET of single-chunk records
linked from a spine, never as one long record.** Three rules, and the third is not
optional.

1. **Split by the QUESTION, never by the byte count.** Each part opens with the
   question it answers and is created under `ChunkSize`. Size is the **alarm**, not
   the rule: a document that will not fit one chunk is evidence it is answering more
   than one question.
2. **The set is enumerable by KG edges from a spine**, using a namespaced,
   order-bearing predicate: `<document>.part.<NN>` (e.g. `openthreads.part.01`).
   The namespace is load-bearing — see Consequences.
3. **The spine stores HOW TO TRAVERSE, never WHAT IS THERE.** No count, no list of
   parts, no summary of their contents. A spine that lists its parts rots on every
   addition and re-acquires the ceiling this ADR exists to remove.

**What would make this fail, and the data exists to check it.** The claim is that
question-split parts retrieve individually *at least as well* as the combined record
did — that the split is better retrieval and not merely a size workaround. It is
falsifiable directly: after splitting, ask each part's question and record the rank.
**If a part does not return at rank 1 for its own question, the split was by size
wearing a question's clothes**, rule 1 was not actually applied, and the parts must
be re-cut. Valid for: `bge-m3` embeddings at this repo's 1600-rune chunk sizing;
a different embedder is a different measurement.

## Alternatives Considered

- **Make `Update` re-chunk (issue #39 part 2).** The server-layer fix, and the one
  that would remove the condition rather than route around it. **Rejected for now,
  and this is M's call, recorded here rather than left in a commit message:** it
  changes which ids exist, and the open question — what happens to a reference
  pointing at a **non-parent** chunk — is unanswered. `BACKLOG.md` already puts it
  behind its own ADR. **This decision does not close that door.** If #39 lands, the
  convention still holds, because splitting by question is an independent retrieval
  win and not a workaround that #39 would make redundant.
- **Split by size into fixed parts of `MaxEmbedRunes`.** The proposal as first
  stated. Rejected on retrieval: *"part 2 of 3"* has no identity, so no query matches
  it. It reimplements chunking by hand while giving up the one affordance real
  chunking has — `am_get_drawer(whole: true)` reassembles chunks and **will not**
  reassemble separate memories. Enumeration gained, search lost.
- **Raise `MaxEmbedRunes`.** Rejected: it moves the cliff without removing it, and
  the bound is deliberately conservative for a reason the comment states outright
  (`chunk.go:36-50`) — it is headroom sized so that **swapping the embedding model
  stays survivable**, not a measured ceiling for `bge-m3`. Raising it optimises
  against today's model alone.
- **Compress the document to fit.** Rejected explicitly, because for this document
  it means dropping threads nobody agreed to close. A maintained list shrinking to
  satisfy a storage bound is data loss with a tidy appearance.
- **Let `llm_open_threads` become append-only, with a short live index drawer.**
  Genuinely considered and it remains the fallback if the split proves noisy in
  practice. Recorded here so that adopting it later is a decision rather than a
  drift.

## Component / Boundary Impact

None in code. No package gains or loses a responsibility. The convention binds the
**agent** writing to the palace; `docs/adr/` records it and `model/draf1.md` teaches
it. `internal/palace` keeps ownership of what a drawer is, unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| the `<document>.part.<NN>` predicate namespace | add — **convention only**, no validation | any agent filing a maintained document | `am_kg_query` |
| `llm_open_threads`: one 6448-rune drawer | change — becomes a spine plus N question-records | the repairing session | every session that reads or records an open thread |

No tool signature, schema, migration or default changes. Nothing in the server can
tell whether this convention is being followed, which is stated plainly under Risks.

## Inter-task Contracts

None — this ADR has no implementation tasks in the code. The single repair it
mandates is a data change to the live palace, carried out by a session, and its
acceptance is the falsification measurement above.

## Consequences

- **Positive:** a maintained document stops having a ceiling. Growth becomes *adding
  a part*, which is an unbounded operation, instead of *growing a record*, which is
  bounded at 4000 runes.
- **Positive, and measured:** better retrieval, not merely more capacity. N vectors
  each matching one question sharply beats one vector averaging N topics — 0.953 and
  0.953 at rank 1 for the `llm_index` split.
- **Positive:** per-part edits stop rewriting the whole document, so two sessions
  touching different threads stop contending for one row.
- **Negative:** reading the whole document costs 1 + N calls instead of 1. Mitigated
  by the fact that you rarely want all of it — that is the point of traversal, and
  the `must.*` tier already pays this cost willingly once per session.
- **Negative:** 2N writes, none of them transactional. A half-woven document is
  **silently half-reachable** — the orphan failure this repo keeps hitting,
  multiplied by N.
- **⚠ Negative:** **KG facts are workspace-wide while drawers are wing-scoped.** A
  predicate-only `am_kg_query` is a documented entry point ("every fact of this
  relation"), so an unnamespaced `part.01` would return every project's parts across
  the whole workspace. The `<document>.` prefix is what keeps this survivable, and it
  is a convention with nothing enforcing it.
- **Neutral:** ordering is carried by the predicate string, not by the graph. Edges
  come back unordered, and `<NN>` is what makes them sortable.

## Out of Scope

- **Re-chunking on update** — issue #39 part 2. ADR-038 removes the blocker this bullet
  named (an id that is also a content hash); what remains is a reference-survival rule for
  non-parent chunks (deferred: docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md)
- **Every server-side improvement surfaced in the same session.** Folding any of them in would
  widen this decision silently, which is how a decision stops being one, so each is listed
  below and none is decided here (deferred: each needs its own issue)
  - orphan detection — a drawer with zero KG edges is mechanically detectable, and
    "filed but never linked" is this team's most repeated failure
  - a headroom signal on write, so the cliff is a gauge rather than a surprise, and
    a refusal that names the remedy instead of only the error
  - `am_kg_query`'s **fail-open** on an unknown entity, currently defended by prose
    in four separate documents rather than by an error
  - a gate that re-checks a tool's own description against the declaration it
    describes — the shape `TestDocumentedEnvVarsAreRead` and
    `TestCatalogSizeIsWhatTheReadmeClaims` already establish
- **Backfilling other multi-chunk documents.** Only `llm_open_threads` is repaired
  here; others are found and repaired as they are touched.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| "Split by question" degenerates into split by size with question-shaped titles | Med | **High — it silently forfeits the entire retrieval argument** | The falsification: every part must return at rank 1 for its own question, **recorded**, not assumed. A part that does not is re-cut |
| Parts filed, edges forgotten — the document is half-reachable and looks finished | **High — it is this team's most repeated failure, caught twice in one day** | High | The write is `am_add_drawer` **plus** `am_kg_add` in one breath; verify with `am_kg_query(spine, outgoing)` before declaring done. Nothing in the server will report it |
| The spine accumulates a list of its parts and re-acquires the ceiling | Med | Med | Rule 3. The `llm_init` root is the working precedent: a traversal rule, no count, no list |
| Predicate-only queries grow noisy as more documents adopt this | Med | Low | The `<document>.` namespace prefix — a convention, unenforced |
| The repair of `llm_open_threads` loses a thread | Low | **High** | Split only; **no rewriting, no summarising, no dropping**. Every thread in the 6448 runes lands in exactly one part, verified by reading the parts back against the original before the original is retired |

## Rollback

Nothing is stored differently and no code changes, so rollback is simply to stop
applying the convention. A document already split stays split and stays readable
without it — the parts are ordinary drawers and the edges ordinary KG facts, both
of which predate this ADR. The one irreversible step is retiring the original
`llm_open_threads` drawer, which is why the risk table requires reading the parts
back against it first.

## Follow-ups

- **Split `llm_open_threads` (6448 runes) and record the ranks.** This is the
  falsification, not a chore: each part is asked its own question and the rank is
  pasted here. Below rank 1, rule 1 was not applied and the parts are re-cut.
- **File the four Out-of-Scope server-side improvements as issues**, so they are
  tracked rather than remembered.
- **`model/draf1.md` (PR #48) needs a section on maintained documents.** It currently
  says "created under 1600 runes" and stops there — it does not say what to do when
  the content legitimately outgrows one record, which is the gap this ADR fills.
