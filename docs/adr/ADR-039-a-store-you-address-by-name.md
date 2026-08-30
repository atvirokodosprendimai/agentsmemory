# ADR-039: A store you address by name

**Status:** Proposed
**Date:** 2026-08-27
**Owner:** M
**Spec:** None — no spec stage. Grounded in the declarations at `internal/palace/chunk.go:56` (`MaxEmbedRunes`), `:140` (`DrawerID`), `internal/palace/contentkey.go:297` (`mintOrReuse`), `internal/palace/kg.go:35` (`MaxKGValueLen`), `internal/mcpserver/drawers.go:56` (`wholeMemoryBudget`), `db/migrations/00001_init.sql:61` (`skills`), `db/migrations/00010_kg.sql:22` (`kg_triples`), plus a serialisation measurement recorded inline. ⚠ Line numbers are as of `main` at `5760bca`, re-checked 2026-08-27 after review; this record was first written against `7e8870a`, and three of its citations had already moved.
**Cross-references:** ADR-027 (a maintained document is a set of records — **qualified here, see Invalidates**), ADR-036 (the bootstrap surface this complements), ADR-038 (opaque ids — **it landed under this record and rewrote its Context; see below**), ADR-040 (the schema carries the pairing — where per-tool guidance is being decided, PR #77), ADR-003 (retire the closet prior — the ranking cost this protects), ADR-010 (supersede, do not overwrite — **CLOSED, absorbed in full by ADR-038**), ADR-013 (a page of memories, not chunks), ADR-016 (a memory an agent files must be navigable), `internal/mcpserver/catalog_test.go:430` (`TestEveryCatalogToolIsNamedInTheReadme`)
**Numbering:** next free after ADR-038, whose status line reads **Accepted** and which **merged 2026-08-27** (PR #72 at 08:09:36Z, execution PR #76 at 13:54:56Z). This branch now carries `main`, so ADR-038 is present in-tree and its citations resolve from here. Union-checked across every open PR head 2026-08-27: only ADR-039 (PR #75) and ADR-040 (PR #77) are claimed, no collision. ⚠ **Allocate at merge** — a per-branch check is blind to cross-branch collisions, the rule this repo recorded after its ADR-number collision. Same for the migration: **`00033` is head on `main`** (ADR-038 took `00030`–`00033`), so the next free is **`00034`**.
**Invalidates:** none outright. ⚠ **QUALIFIES ADR-027 (Accepted)** for one class of document — see below. Checked by grepping ADR-001..038 on `main`, plus ADR-039/040 on their open branches, for `key/value`, `kv_`, `author-chosen`, `addressed by name`: no accepted ADR governs a name-addressed store. ADR-027 governs the adjacent question and its rule 3 phrase ("the spine stores how to traverse, never what is there") is adopted here verbatim rather than displaced.
**Served-path change:** **Yes.** One new table, one new MCP tool. No existing tool signature, default, or ranking path changes.

## ⚠ RESCOPED 2026-08-30 — read this before the Context below

**M's decision: this record is narrowed to two claims, and the title is no longer
what it is about.** The text below is kept as written, superseded rather than
excised, because this record's own convention is that a correction stays readable
beside what it corrects — see the two ⚠ paragraphs in Context that already do this.

**What this record now argues, in this order:**

1. **A WRITE BOUNDARY.** `am_kg_add`'s subject and object are never validated — the
   tool's own description says so: *"subject and object are entity labels in a
   schemaless graph and are never checked, so a mistyped one still mints a NEW node
   silently."* Any project in the workspace can therefore write into another's
   must-load tier, and a typo mints a dead node reporting `success: true`. Observed
   unprompted on 2026-08-30, on hosted, by a session that was not looking for it: a
   fact filed with an HTML-escaped subject (`adr-verify &lt;task file&gt;`) created a
   node nothing will ever resolve. That is this record's line 168 risk happening,
   and it is a **designed hazard nobody gated** rather than a bug. It leads because
   it is the sharper of the two: an unreviewed write path into the one tier every
   session loads without judgement.

2. **A PAYLOAD SLOT.** `MaxKGValueLen = 128` (`internal/palace/kg.go:35`) means the
   graph stores labels, not payloads. The tier works today as edges → drawers; a
   single addressed blob does not exist. Second because it is a capability gap
   rather than a live hazard.

**What has left this record:** the addressing argument its title names. See Out of
Scope — it is deferred with an accurate statement of what serves it today, not
declared solved.

## Context

Every session reaches its must-load tier through a chain: the `start-here` skill description names
room `llm_init`; a session lists that room; identifies the root **by reading its first line**; then
walks a 64-hex drawer id through the KG. ADR-036 measured the client-side form at **13 calls, ~99KB
(~25k tokens), plus a hardcoded root drawer id.**

Three hops exist only to compensate for a defect, and the root drawer carries prose to paper over
each: identify-by-content-prefix, because there is no address; three predicate sweeps, because
corrections are incoming edges; and `⚠IF STEP 1 RETURNS ZERO EDGES IT FAILED — STOP`.

⚠ **That third hop is a WORKAROUND FOR A DEFECT THAT IS ALREADY FIXED, and this record originally
said otherwise.** ADR-036 T2 gave the lookup `matched` / `known_term_no_facts` / `unknown_term`, so
the prose warning survives in the root drawer as folklore rather than as a live compensation. The
honest count is therefore **two** compensating hops, not three — and the palace itself was the source
of the error: its `must.craft.traps` record still asserted the old fail-open behaviour while the
`start-here` skill had the correction, and this record followed the wrong one.

**The root cause is that every address in this system is derived, not chosen.**

⚠ **This record originally argued that from instability, and that half is now false.** It was written
against `7e8870a`, hours before ADR-038 merged underneath it. The original diagnosis read: *content
is in the hash, so a re-add, a `merge_wing` relabel or a chunk-parent change moves the id.* Two of
those three are gone. `DrawerID` is now a **content key**, not an identity — its own doc on `main`
opens *"It is the dedup key, and it is not a name — a drawer's id is opaque, minted once and never
recomputed (ADR-038)"* (`chunk.go:140`). A re-add resolves to the existing row's id through
`mintOrReuse` (`contentkey.go:297`), and `merge_wing` recomputes the content key while leaving the id
untouched (`TestMergeWingRecomputesTheContentKey`, `contentkey_test.go:64`). The correction is kept
here rather than silently edited away, because the next reader will check it.

**What survives is the half this record actually rests on, and it is untouched by ADR-038: an opaque
id is still *derived*, not *chosen*.** ADR-038 made the address *stable*; it did not make it
*nameable*. A session still cannot say `read("root")` — it must be handed a 64-hex string by
something else, which is the chain this record exists to delete. Stability without nameability
removes the drift, not the dance: you still discover the id before you can use it, and every hop that
exists to perform that discovery still exists.

**You cannot build a zero-knowledge entry point on an address you must first be told.**

## Existing Primitives Audit

Four candidates. None serves, and the reasons differ:

| Primitive | Shape | Why it does not serve |
|---|---|---|
| **Skills** (`00001_init.sql:61`) | `name` unique per team, `content`, `version` bumped on update — **a versioned KV keyed by name** | The closest existing thing. But a skill is a wake-up convention every session loads; filling it with bootstrap payloads pollutes `am_list_skills`, the surface agents read to learn how to work here. |
| **KG** (`00010_kg.sql:22`) | `source_drawer_id` defaults `''`, so facts stand alone; `valid_from`/`valid_to` + `KGInvalidate` is real versioning | Serves the ADDRESSING half **already**: entity labels are author-chosen and never validated, so property 1 holds today — and a three-level prefix tree over them was measured working 2026-08-29 (17 nodes, every one inline, built with `am_kg_add`/`am_kg_invalidate` alone, no schema change, no migration). Fails on the other two axes: `MaxKGValueLen = 128` runes (`kg.go:35`) makes it a store for labels, not payloads; and because subject and object are **never checked**, any project in the workspace can write into another's tier undetected. |
| **Drawers** | verbatim, chunked, embedded, ranked | Content-hash id; `MaxEmbedRunes = 4000` refuses an update above it (`service.go:775`) — which is why the live root drawer must instruct "DO NOT GROW THIS DRAWER". |
| **Artifacts** (upstream mempalace, HEAD `4c1e6d0`) | exact content, no chunk, no embed — the right blob semantics | `put` returns a **server-assigned id**; `get` takes that id. The create→get-id→remember-the-id dance again. |

**Three near-misses across two codebases, all failing on the same axis: the address is assigned, not
chosen.** That is the gap.

## Decision

Add a team-scoped, versioned store addressed by an author-chosen name, as **five tools — one per
operation**, not one tool with a `command` argument.

```jsonc
// registrar.add — no role gate, available to every member
am_kv_read({    id: "agentsmemory.root", version?: 7 })      // omit version -> latest
am_kv_list({    id: "agentsmemory.must.", limit, offset })    // id is a PREFIX here
am_kv_history({ id: "agentsmemory.root",  limit, offset })    // metadata only, never payloads

// registrar.addWrite — role-gated, refused for a member key
am_kv_write({   id: "agentsmemory.root", payload: "…", if_version?: 7 })
am_kv_delete({  id: "agentsmemory.root" })
```

```sql
CREATE TABLE kv_entries (
    team_id    TEXT NOT NULL,
    id         TEXT NOT NULL,
    version    INTEGER NOT NULL,
    payload    TEXT NOT NULL,
    written_by TEXT NOT NULL,   -- which key wrote it; a version with no origin is unattributable
    written_at TEXT NOT NULL,
    PRIMARY KEY (team_id, id, version)
);
```

Eight properties, each load-bearing:

1. **The key is chosen, not derived.** `root` survives every edit. This is the whole point.
2. **`am_kv_read` on a missing key is an ERROR**, not an empty success — a bootstrap that cannot
   distinguish "absent" from "empty" builds a pointer to nowhere and reports success.
   ⚠ **An earlier draft justified this by contrast with `am_kg_query`'s "`count:0` and no error". That
   contrast is FALSE and is withdrawn:** ADR-036 T2 gave the lookup three resolutions — `matched`,
   `known_term_no_facts`, `unknown_term` — rendered at `internal/mcpserver/kg.go:196`, and
   `KGResolution`'s own doc (`internal/palace/kg.go:453-477`) records the old behaviour in the past
   tense. The graph already fails closed. This property is therefore **consistency with an existing
   guarantee, not a new one**, and the `⚠IF STEP 1 RETURNS ZERO EDGES` prose it claimed to retire was
   already obsolete when this record was written.
3. **No embedding, no chunking, no ranking.** `MaxEmbedRunes` does not apply, and this content never
   enters the ranked pool.
4. **Keep all versions, paged.** `am_kv_read` returns latest; `am_kv_history` returns **metadata
   only** (`version`, `written_at`, `written_by`, `bytes`, `first_line`) plus `total_versions`,
   newest first.
5. **Team-scoped; keys flat-dotted**, the same grammar as KG predicates, so one vocabulary. The tier
   lives in the key, so `am_kv_list` with prefix `must.` **is** the must tier — no KG hop, and
   nothing points at a key for tier reasons.
6. **The first dotted segment is a namespace, and the server requires it** on `write` and `list`.
7. ⚠ **The cap is 36,000 SERIALIZED BYTES, not runes**, refused at write time. Runes were the wrong
   unit: the risk is that a response exceeds `wholeMemoryBudget` and spills to a file the agent never
   sees, and that budget is in bytes. An escape-heavy or multibyte payload passes a rune cap and
   fails the byte one, which would create a key that can be written and **never read** — precisely
   the failure the cap exists to prevent. The 4.2%/7.3% overhead samples are English prose and are
   **not** a worst-case bound; measuring the bound is a task, not a claim.
8. ⚠ **`am_kv_write` takes an optional `if_version`** — write only if the current version is that
   one, else refuse with the current version. Without it, two sessions editing one key silently
   produce successive versions where the second overwrites the first's intent. See the ADR-027
   section: this is the concurrency guarantee ADR-027 gets from per-part records.

### ⚠ Why five tools rather than one with a `command` argument

**This is the change the second review forced, and it is this record finishing an argument it had
only half-made.** Alternatives below reject a positional dispatcher because *"a tool description is
the strongest guidance surface — present at the moment of the call, in every client."* One tool
carrying five behaviours is a milder version of the same defect: one description doing five jobs.

But the decisive reason is authorization, and it is structural rather than stylistic.
`internal/mcpserver/server.go` splits registration into `registrar.add` and `registrar.addWrite`, and
**the caller's role is enforced only inside `addWrite`** — its own comment says the split "is not
bookkeeping". A single mixed-mode tool cannot be registered correctly in that model:

- registered with `add`, its `write` and `delete` paths **bypass the role guard entirely**, and
  `TestEveryMutatingToolIsRegisteredAsAWrite` (`internal/mcpserver/writeauth_test.go:61`) fails the
  build — correctly;
- registered with `addWrite`, a read-only member loses `read`, `list` and `history`, which are the
  three calls a bootstrap is made of.

Static per-tool annotations do not help: the hint is per *tool*, and the mode here varies per
*command*. **One operation per tool is the only shape this repo's authorization model can express.**

★ **It also dissolves this record's dependency on ADR-040.** The earlier draft needed somewhere to
put guidance for five commands and looked to the MCP `instructions` channel. Five tools each carry
their own description at the moment of their own call, so the guidance has a home by construction.

### ⚠ Why the namespace is enforced rather than conventional

A team is a workspace and a workspace holds many projects. Team scope with free-form keys means two
projects both reaching for `root` interfere, and `am_kv_list` with prefix `must.` returns another
project's must tier as if it were yours. The first draft mitigated that with prose ("each project
writes its key conventions in its own `AGENTS.md`") plus "`list` exposes it". **That is the mitigation
shape this repo has already ruled against:** `AGENTS.md` states it plainly — *prose belongs where a
human reads it and nowhere else; anything that must stay true gets a command whose exit code says
so.* A convention nobody can fail is not a convention, it is a hope.

⚠ **And the second review showed the failure is worse than a collision, which is why the mitigation
had to be a gate.** With PK `(team_id, id, version)` and no writer column, a second project writing
`root` does not conflict — it appends **version 2 of the same logical key**, and the first project's
next read silently returns the other project's content. Nothing errors, and `history` as first
specified exposed only version/time/size, so the row could not even be attributed afterwards. That
is why property 4 now records `written_by`: a version whose origin is unrecoverable makes the
interference undiagnosable as well as undetected.

⚠ **And the graph makes the same failure worse, which is the sharpest available argument for enforcing this.** KG subject and object are entity labels in a schemaless graph and are **never checked** (`am_kg_add`'s own contract says so). So any project in the workspace can add an edge to another project's mandatory tier — no error, no review, no attribution — and every session traversing that tier then fetches it unconditionally. That is an unreviewed write path into the one thing every agent loads without judgement, and naming convention is the only thing standing in front of it.

So `am_kv_write` and `am_kv_list` **require** a namespace segment. This costs no wing param and no
resolution ladder, and the flat-dotted grammar survives intact.

⚠ **This creates one tension with property 1, named rather than hidden:** a namespace is prior
knowledge, which is what property 1 promises a reader will not need. Property 2 does the work — **an
unqualified read is an error that names the namespaces holding that key**:

```
am_kv_read({id:"root"})  ->  ERROR: "root" exists in 2 namespaces: acme, alpha
```

That fails closed, costs one extra call only when the key is genuinely ambiguous, and **teaches the
caller instead of guessing for them**. A single-namespace team never sees it.

Bootstrap end state:

```
am_kv_read({id:"acme.root"})     -> the blob, one literal of prior knowledge
am_kv_read({id:"root"})          -> ERROR naming the namespaces (see above)
am_kv_list({id:"acme.must."})    -> the must tier, this project's only
am_kg_query({predicate:"retracts"})  -> corrections; still the one thing only the graph answers
```

## ⚠ How this qualifies ADR-027, stated rather than assumed

ADR-027 (Accepted) decides: *"A document intended to be maintained is stored as a SET of
single-chunk records linked from a spine, never as one long record."* The entrypoint root **is** a
maintained document, and this ADR proposes storing it as one record of up to 36,000 bytes. That is
a direct interaction and it is the reviewer's call, not one to make by implication.

⚠ **The first draft argued this badly and the second review was right to reject the argument.** It
claimed *"ADR-027's rationale and its falsifier are both retrieval-based"*, which is **false**.
Retrieval is one of four consequences ADR-027 records (`ADR-027:153-168`); the others are unbounded
growth, per-part editing with concurrent writers, and failure isolation. An argument that answers one
of four and calls the record answered is not a qualification, it is an oversight — and this section
was the part two reviewers had praised, which is precisely why it needed the cold read.

**Argued against all four, and the answer differs for each:**

| ADR-027 consequence | Does it reach a name-addressed payload? |
|---|---|
| **Retrieval** — *"N vectors each matching one question sharply beats one vector averaging N topics"*, falsified by asking each part its own question and recording the rank | **No.** The test presupposes retrieval by question. A `kv_entries` payload is never embedded, ranked or searched, so neither the mechanism nor its falsifier reaches it. This is the one the first draft got right. |
| **Failure isolation** — *"2N writes, none of them transactional. A half-woven document is silently half-reachable"* | **No — and it runs the other way.** That negative is a cost ADR-027 accepts for splitting. One key is **one atomic write**, so the orphan-half-document failure cannot occur. The KV tier is *better* on this axis, and the first draft never noticed it had an argument here. |
| **Unbounded growth** — *"a maintained document stops having a ceiling"* | **Partly.** 36,000 bytes is a ceiling where ADR-027 offers none. Mitigated only by adopting rule 3 below: a spine that stores how to traverse never approaches the bound. ⚠ That is a discipline, not a mechanism, so it is listed as this record's High/High risk rather than claimed as solved. |
| **Concurrent writers** — *"per-part edits stop rewriting the whole document, so two sessions touching different threads stop contending for one row"* | **YES. This one lands, and it is the real hit.** One key is one row, so two sessions editing different parts of the root contend exactly as ADR-027 describes. **Answered by mechanism, not by prose:** property 8's `if_version` makes a contended write a *refusal naming the current version* instead of a silent overwrite. ADR-027 avoids contention by partitioning; this record detects it by compare-and-swap. Different guarantee, comparable safety, and it is stated so a reviewer can reject the substitution. |

**Proposed narrowing, unchanged in substance but now earned:** ADR-027 governs maintained documents
**in the searchable corpus**. Documents addressed only by name are out of its scope and governed
here — because retrieval does not reach them, failure isolation favours them, growth is bounded by
rule 3, and concurrency is answered by `if_version` rather than ignored.

**ADR-027 rule 3 is adopted verbatim, not displaced:** the spine stores how to traverse, never what
is there. It is the discipline that keeps this tier from becoming a second corpus — see Risks, where
it is the mitigation for the one axis above that is answered by discipline alone.

## Alternatives Considered

- **A stable opaque id plus a name→id pointer.** REJECTED on three counts, below. ⚠ **This
  alternative did not exist when the record was first written — ADR-038 created it hours later, and
  it is the strongest one on this list.** Since a drawer's id is now minted once and never
  recomputed, a name could simply point at it:
  `am_kg_add(subject: "root", predicate: "points_at", object: <64-hex>)`. A 64-hex id is 64 runes,
  comfortably inside `MaxKGValueLen = 128`, so this needs **no new table and no new tool** — a real
  advantage this proposal does not have, and the reason it deserves arguing rather than listing.

  **(1) WITHDRAWN — this argument was false and is kept visible rather than deleted.** It read: *"the
  resolve step inherits the exact failure this record exists to delete — `am_kg_query` fails open, so
  resolve-then-fetch can silently answer nothing."* The graph **does not fail open**: ADR-036 T2 gave
  it `matched` / `known_term_no_facts` / `unknown_term` (`internal/palace/kg.go:453-477`, rendered at
  `internal/mcpserver/kg.go:196`). A pointer resolve fails closed exactly as `am_kv_read` would. This
  was the load-bearing leg of the rejection, it came from a stale memory rather than from source, and
  **the alternative is stronger for its removal.**

  **(2) It leaves the payload problem untouched, and that is independent of addressing.** The thing at
  the end of the pointer is still a
  drawer: chunked at `ChunkSize`, refused for in-place update above `MaxEmbedRunes = 4000`, and
  **embedded into the ranked pool**. So "DO NOT GROW THIS DRAWER" survives, and session-start content
  keeps competing with real memories for top-1 — the ADR-003 cost this record protects against.
  Naming the address does not un-rank the content.

  **(3) It creates a second thing to keep true.** The pointer and the row are now two records of one
  address. Under ADR-038 a re-file **ends** the old row rather than overwriting it, so a pointer can
  come to name an ended row while still resolving — and that is measured, not hypothetical: ADR-038
  shipped `doctor --corpus` to find exactly this, and its first run reported **16 facts naming no
  row**. ⚠ Scoped as ADR-038 scopes it: that run was against the **self-hosted container, a different
  corpus**, and its own follow-up says the hosted palace could differ. The class is demonstrated; the
  count is not a property of this palace. `doctor --corpus` reports "points at an ENDED row" as a
  distinct third state for this reason (`AGENTS.md:245`).

  **(4) ⚠ NARROWED BY MEASUREMENT — this leg no longer carries the rejection.** It read that pointing
  at an ADR-027-compliant spine reintroduces `1 + N` calls, citing F-16, where a bootstrap-led session
  reached the *same* call count as the hand protocol. Measured 2026-08-29 against the hosted palace,
  one task run through both entry points: the flat production star took **23 calls and spilled
  twice**; a prefix-path tree over KG entities took **26 calls and spilled once**; both returned the
  correct answer. Call count barely moved. What moved was the spill — and a response over the budget
  does not return a smaller answer, it returns **nothing to the model**, recoverable only with a shell
  an MCP-only client does not have. So this leg measures a quantity that is close to free and misses
  the one that binds. It is kept visible rather than deleted, as (1) was. **(2) and (3) carry the
  rejection now.**

  **(5) ⚠ A THIRD SHAPE, ABSENT FROM THIS LIST WHEN IT WAS WRITTEN, AND BUILT SINCE.** Both forms
  above treat the named entity as *indirection* — a name pointing at one drawer. The variant never
  considered is the named entity **carrying the tier's edges itself**, with the name as the path and
  `child = parent + "." + segment`:
  `<base>.root --must--> <base>.root.must --craft--> <base>.root.must.craft --deletion--> <drawer>`.
  Built 2026-08-29 with `am_kg_add` and `am_kg_invalidate` only — no table, no tool, no migration —
  it produced 17 nodes, **every one returning inline**, entry node at 3 edges and largest at 37,
  against a flat star of 109 edges that spills at 63KB. Four read-only recall runs answered correctly
  through it. **This is the strongest form of the alternative, and it defeats the addressing and
  call-count arguments outright.** It leaves (2) and (3) untouched: the payload at the end is still a
  chunked, ranked drawer, and nothing in the graph enforces a namespace. Those two legs alone are what
  this record now rests on.

  ★ **The honest summary, and it is narrower than the first draft claimed: this alternative fixes the
  *address*, and this record is about the *address, the ranking and the call count together*.** It
  needs no new table and no new tool, and **if addressing were the only broken thing it would win.**
  ⚠ The review's challenge — that unembedded atomic blob semantics are asserted rather than
  established independently of addressing — is answered by (2) and (4), which are both about what
  sits at the end of the pointer and neither of which addressing changes. A reviewer who thinks the
  ranked-pool cost and the 1+N call count are acceptable should prefer the pointer, and that is a
  legitimate reading of the same evidence rather than a misreading.
- **A positional `[cmd, payload, version]` dispatcher** (the original proposal). REJECTED. A tool
  description is the strongest guidance surface — present at the moment of the call, in every client
  — and an opaque dispatcher deletes it at the call every session makes first.
  `TestEveryCatalogToolIsNamedInTheReadme` (`catalog_test.go:430`) already fails the build over
  undiscoverable capability. The flexibility is preserved without the cost: the server enforces only
  the namespace segment (Decision property 5); everything below it is the project's own vocabulary,
  written in its `AGENTS.md`.
- **Wing-scoped, with a `wing` column and param.** REJECTED. Reading the entrypoint would require
  resolving a wing first — a five-rung ladder, with `default_wing` empty on real registrations — but
  knowing how to work here is *what the entrypoint tells you*. Circular. The KG's team-wide scope
  hurts because facts surface **unbidden in recall**; a name-addressed store has no recall, so the
  only exposure is a collision between two authors choosing one key.
  ⚠ **The first draft stopped there and left that collision to convention. Review was right that it
  should not.** Decision property 6 now enforces a namespace segment instead — which is what a wing
  param was reaching for, obtained without the ladder: **one literal the project states, not a
  procedure the session must execute.** That distinction is the whole reason this stays rejected and
  the namespace does not.
- **Put payloads in the KG.** REJECTED at `MaxKGValueLen = 128`.
- **Put them in skills.** REJECTED — pollutes the wake-up surface.
- **Raise `MaxEmbedRunes`.** REJECTED — it would enlarge what gets embedded and ranked, the opposite
  of the goal.
- **A TTL / scratch tier.** REJECTED. Keys persist until `delete`.

## Wiring & Contract Changes

⚠ **`None` would be false for this record.** It is record-only — no code and no migration ship in
this PR — but what it decides *is* one new table and **five** new MCP tools, so the surfaces an
implementation must touch are named here rather than discovered later.

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `kv_entries` (schema) | **add** — new table, PK `(team_id, id, version)`, incl. `written_by` | `db/migrations/00034_kv_entries.sql` (allocate at merge) | `internal/palace` KV service + repo |
| `am_kv_read(id, version?)` | **add** via `registrar.add` — no role gate | `internal/mcpserver` | every agent at session start |
| `am_kv_list(id, limit, offset)` | **add** via `registrar.add` — `id` is a prefix | `internal/mcpserver` | tier enumeration |
| `am_kv_history(id, limit, offset)` | **add** via `registrar.add` — metadata only, never payloads | `internal/mcpserver` | audit of who rewrote a key |
| `am_kv_write(id, payload, if_version?)` | **add** via `registrar.addWrite` — role-gated | `internal/mcpserver` | refused for a member key |
| `am_kv_delete(id)` | **add** via `registrar.addWrite` — role-gated, removes every version | `internal/mcpserver` | ⚠ see Follow-ups: ADR-038 T4's precedent argues this belongs on the OPERATOR surface, not the agent one |
| Tool registration | **add** — all five must go through `newTool` + `registrar.add`/`addWrite` | `internal/mcpserver/server.go` | ⚠ `mcp.NewTool`/`AddTool` compile fine and silently skip the `am_` prefix, the catalogue **and** the role gate — this repo's own recorded trap, and reachability is its signature defect |
| Write-gate correctness | **add** — the read/write split is per TOOL, which is why there are five | `internal/mcpserver/server.go` | `TestEveryMutatingToolIsRegisteredAsAWrite` (`writeauth_test.go:61`) fails the build if a mutating command hides inside a read tool |
| README tool count + tool list | **change** — all five named; the count moves by five | `README.md` | `TestEveryCatalogToolIsNamedInTheReadme` (`catalog_test.go:430`) and `TestCatalogSizeIsWhatTheReadmeClaims` both fail the build otherwise |
| Key namespace validation | **add** — first dotted segment required on `am_kv_write` and `am_kv_list` (property 6) | `internal/palace` | every caller; enforced server-side, not by convention |
| `am_kv_read` on a missing key | **add** — errors, and an *unqualified* key errors naming the namespaces that hold it | `internal/mcpserver` | the fail-closed property this record rests on |
| Byte-cap enforcement | **add** — refuse a write whose SERIALIZED form exceeds the cap (property 7) | `internal/palace` | the cap is bytes because `wholeMemoryBudget` is bytes |
| `if_version` conflict result | **add** — refusal naming the current version, not a silent overwrite (property 8) | `internal/palace` | the ADR-027 concurrency answer |
| `doctor` | **add** — the store needs an integrity check beside `--index`, `--schema`, `--corpus` | `cmd/server/doctor.go` | operators. ⚠ Still open below: an unseen store is an unmaintained one |
| `doctor` | **add** — the store needs an integrity check beside `--index`, `--schema`, `--corpus` | `cmd/server/doctor.go` | operators. ⚠ Still open below: an unseen store is an unmaintained one |
| Quota (`admit`) | **undecided** — whether a KV read counts | `internal/mcpserver` | see Follow-ups; a per-bootstrap read is a real per-session cost |

## Consequences

**Retires:** the `start-here` → room → content-prefix → 64-hex chain; the "DO NOT GROW THIS DRAWER"
instruction; the `⚠IF STEP 1 RETURNS ZERO EDGES` trap; and handling 64-hex ids **on the bootstrap
path**.

⚠ **Narrowly, and this was overstated in the first draft:** `⚠IDS ARE FULL-LENGTH — a prefix
silently creates a NEW node` is a warning about `am_kg_add`, which this record does not touch. It
stops applying **to the bootstrap path only**, because that path stops handling ids at all. It
remains true everywhere else — every session that files a fact still has to copy a full id — and the
traps drawer must keep saying so.

**Protects retrieval quality.** Bootstrap and session-start content are unrelated documents
competing in the ranked pool. ADR-003 retired the closet prior for what it did to top-1; this keeps
a whole class of ephemera out of that pool by construction rather than by discipline.

**Costs:** one new table, one new tool, one more thing `doctor` should know about. Keep-all-versions
on a hot key at 36,000 bytes is unbounded — ~500 edits of the root is ~12MB for one key. Pruning to
last-N can be added later without an API change, because `history` is paged from day one.

## Out of Scope

- **Search over KV** — giving this tier recall would put the ephemera back in the ranked pool the record exists to keep out of it (permanent: it is the known-address tier by construction)
- **Relations** — `id|id|how` is the graph's job and the graph does it well; a second edge store would be two answers to one question (permanent: the graph owns relations)
- **`kg_supersede`** — ⚠ this entry is what the gap looked like before it was closed, and it is kept rather than deleted because the next reader will check it. The first draft called it "a real, separate gap … ADR-010 (Proposed) already covers the principle. Filed to `wing_agentmemories`/`inbox`." All three clauses have since gone false: `am_kg_supersede` landed in ADR-038 T4 — `KGSupersede` at `internal/palace/supersede.go:205`, registered at `internal/mcpserver/kg.go:126`, in one transaction with `reason` required — and ADR-010 is CLOSED, absorbed by ADR-038. The inbox item is stale and its atomic-verb half is closed; only the boundary-overlap half survives, in issue #47 (permanent: shipped in ADR-038 T4, and not this record's work)
- **A shared guidance home for the KV commands** — ⚠ this was a dependency and **is now dissolved**, which is the clearest downstream effect of splitting the tool. The first review proposed MCP's `instructions` field as somewhere to put guidance for five commands sharing one description. With five tools there is no shared description to compensate for: each carries its own at the moment of its own call, which is this record's stated reason for rejecting a dispatcher in the first place. The related finding stands and belongs to its own record: ADR-040 establishes there is **no per-tool `instructions` field** in mcp-go v0.55.1 and rejects lengthening the server-level one at 1,143 bytes of a tested budget — `TestInstructionsStayShort`, `instructions_test.go:151`, where ADR-040 itself cites `:153` (permanent: five tools need no shared guidance channel)
- **ADR-027's remaining open question** — a reference pointing at a non-parent chunk that a re-chunk deletes. It is *upstream* of this record's ADR-027 qualification above: both concern where ADR-027's authority ends now that ids are opaque, and the first review asked for them in one pass. ⚠ **Answered here only for the name-addressed half**: a `kv_entries` payload has no chunks, so re-chunking cannot strand a reference into one, and that half needs no new record. The drawer-side half — a KG fact naming a non-parent chunk id that a re-chunk removes — is untouched by this record and stays where ADR-038 filed it (deferred: `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md`)
- **THE ADDRESSING ARGUMENT THIS RECORD IS TITLED AFTER** — ⚠ removed from scope on 2026-08-30 by M's decision, and the reason has to be stated precisely because both easy summaries are false. It is **not solved by a shipped mechanism**: `am_entry_point` resolves exactly one label, `room:<wing>/llm_init` (`internal/palace/graphquery.go:471` and `:518`, via `DerivedEdgeSubject` at `internal/palace/kg.go:1076`), minted only by `attachDerivedEdge` when a drawer is filed into a room of that name. It is **also not unserved**: the hosted palace answers a chosen literal in three edges inline, through hand-authored facts in a `<wing>.root` convention that nothing in `main` mints or reads. That prototype demonstrates the shape and is not reproducible — the local palace returns `unknown_term` for both `am_entry_point` and the named node. ⚠ The hosted side is **not verifiable from this tree**: it was reported by a session working against that palace on 2026-08-30, and the live edge there states its own provenance as a hand-authored 2026-08-27 backfill. Deferred rather than permanent, because a working prototype outside the product is a reason to decide, not a reason to stop (deferred: `docs/adr/BACKLOG.md`)
- **Deploying `am_bootstrap`/`am_entry_point`** — the hosted catalogue serves both, verified 2026-08-27. ⚠ **SERVED IS NOT POPULATED**, measured 2026-08-30: `am_entry_point("wing_agentmemories")` returns `unknown_term` on the local palace, because no drawer has ever been filed into a room named `llm_init` and that room name is the entry node's only source. The tools are deployed; the data they resolve is not a deployment question (permanent: already built, merged and deployed — the empty result is the Out of Scope entry above, not this one)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| **Content drifts in that should have been a memory** — 36,000 bytes is a lot of room, and roots grow | High | High | ADR-027 rule 3, adopted verbatim: the spine stores how to traverse, never what is there. ⚠ This is the ONE ADR-027 consequence answered by discipline rather than by mechanism, and the second review named it as the ADR-027 objection restated. A gate is worth considering and is listed in Follow-ups. It is the risk that would turn the tier into a second, unsearchable corpus. |
| **A payload near the cap spills to a file on read** and never enters context | Low | High | ⚠ **The first draft capped RUNES, which does not bound this risk** — `wholeMemoryBudget = 40_000` (`drawers.go:56`) is in BYTES, so an escape-heavy or multibyte payload passes a rune cap and still spills, creating a key that can be written and never read. Property 7 therefore caps the **serialized byte length**, refused at write time, never at read time where the spill is silent and unfixable. ⚠ The 4.2% / 7.3% JSON-overhead samples behind the original 36,000 are ENGLISH PROSE and are **not** a worst-case bound; deriving the real bound is a Follow-up, not a claim made here. |
| ⚠ **The 40-45KB budget the cap rests on has no executable provenance** | Med | Med | Raised by the second review, and it is upstream of this record rather than caused by it: `wholeMemoryBudget` is a declared constant, and the figure justifying it is not reproducible from anything in the tree. This record inherits that weakness by depending on the constant. Not fixable here; named so the dependency is visible. |
| **The migration collides with an open branch** | Low | High | Allocate at merge. ⚠ The first draft named `00030`, which ADR-038 has since taken along with `00031`–`00033`; head on `main` is `00033` and the next free is `00034`. That the number rotted inside a row whose own subject is number collisions is the argument for allocating at merge, not at authoring. |
| ⚠ **Two motivating measurements were DISPUTED — one is now reconciled** | — | Medium | Originally: this session measured `llm_open_threads` at 13 drawers and `am_skillset` at 40 tools; a reviewer measured 1 drawer and both bootstrap tools live. **The tool count was a TIMELINE, not two instances** — the hosted catalogue held 40 before a redeploy on 2026-08-27 and 42 after, with both bootstrap tools answering, so both readings were correct hours apart and neither was stale in the sense claimed. Re-measured 2026-08-27 on hosted `atvirokodosprendimai-498ccd`: **42 tools**, and `llm_open_threads` holds **15** drawers — consistent with 13 plus two filed since, and not with 1. ⚠ The 1-versus-13 half stays **unreconciled**; it is the one that still needs an endpoint attached before it is quoted. The decision rests on neither; the urgency claim rests on the second. |

## Rollback

A new table and a new tool, both additive. The migration — `00034` at time of writing, allocated at
merge — carries a `-- +goose Down`. A client ignoring the
tool sees today's behaviour. Removing it breaks only callers that adopted it — which is why adoption
should follow the measurement below, not a promise. Revert order: binary, tool registration,
migration.

## What would falsify this

**The bootstrap read must measurably beat the chain it replaces**, on calls and on output tokens,
**against the same palace**. If it does not, this has reproduced the problem behind a nicer name.
This is the same falsifier ADR-036 set for itself in F-16.

⚠ **The first draft deferred to F-16 as "still unchecked". That was wrong, and so was the correction
that replaced it — F-16 is TWO things and both drafts conflated them.**

**F-16's code gate exists and passes.** `TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces`
(`internal/palace/recallanswers_spec_test.go:847`) checks semantic parity before tokens, and ADR-036's
task README records its **T8** as complete against F-16. So the falsifier has a committed, running
test — and note that this is ADR-036's task status, not a completion claim by this record, which owns
no tasks yet.
⚠ **But that gate does not validate this record**: it exercises `Service.Bootstrap` over fresh
fixture data with derived edges already present, and never calls the store proposed here. A green
F-16 is evidence about the bootstrap API, not about KV.

**What is open is the LIVE-CORPUS adoption measurement, and there `am_bootstrap` does not subtract.**
The call itself is
cheap — 1 call, 10.7KB — but a *bootstrap-led session* still came to **33 calls / 182,575 B** against
the hand-executed protocol's **33 calls / 180,500 B**: the same call count and about 2KB *more*. The
reason is structural rather than a defect in the tool: the bootstrap's inline tier is the entry
*room*, while the `must.*` tier hangs off the root *drawer* one hop further out, so the payload stays
scattered across records reachable only by traversal. **One addressed key carrying the tier as a
value is precisely the thing `am_bootstrap` could not be** — which makes F-16 an argument *for* this
record, not a reason to wait on it.

⚠ **And the stated blocker applies only to that live half.** The first review reported that F-16
"cannot be checked on the motivating wing" because `am_bootstrap` returns `resolution: "unknown_term"`
on this project's wing.
Called live on the hosted palace (workspace `atvirokodosprendimai-498ccd`) it returns
`resolution: "matched"`: the entry edges were hand-authored at 08:28–08:29Z on 2026-08-27, roughly
six hours before that review was submitted at 14:59Z. The automated backfill is still filed in
`BACKLOG.md` and still has not run — every entry point that exists was authored by hand, this wing's
and **four others**, each of which resolves `matched`, checked here rather than recalled. ⚠ That
correction is this record's own lesson landing on it: an earlier draft of this paragraph said "no
other wing has one", taken from a memory written before those other wings were authored.
⚠ **Instance named deliberately, not to settle it by assertion:** this record already carries one
unreconciled local-versus-hosted disagreement in Risks, so a second measurement gets its endpoint
attached rather than a verdict.

**What is still genuinely unmeasured** is the only thing that can falsify this record: the KV read
against the same palace, end to end. That number is owed before adoption, not before acceptance.

## Follow-ups / still undecided

- [ ] **Does `doctor` see the store?** An unseen store is an unmaintained one.
- [ ] **Does a read count against quota?** Every handler calls `admit(ctx, usageSvc)`, so a KV read
      per bootstrap is a real per-session cost — deliberate, or exempt?
- [ ] Report the measured before/after for the bootstrap path, whichever way it falls. ⚠ This is the
      **only** thing that can falsify this record, and it is still unmeasured — ADR-036's F-16 is now
      answered and does not substitute for it.
- [ ] ⚠ **Should `am_kv_delete` be on the AGENT surface at all?** It is write-gated, but it removes
      every version of a key — irreversible erasure reachable by an agent, three weeks after ADR-038
      T4 removed exactly that class from the agent catalogue and kept it for the operator CLI. The
      consistent answer is that `am_kv_delete` follows `delete_wing`: operator-only, with the agent
      surface getting nothing destructive. **Not decided here** — it narrows the tool set the owner
      just chose, so it is put back to them rather than assumed.
- [ ] ⚠ **Confirm Decision property 6 — the enforced namespace segment — is the owner's call.** It
      was added in response to review, not present in the original design, and it trades one literal
      of prior knowledge for interference that would otherwise be silent. Reversing it is a one-line
      edit to property 6.
- [ ] **Derive the real serialization bound for property 7.** The 4.2% / 7.3% overhead figures are
      English prose and are not a worst-case bound; escape-heavy and multibyte payloads are the cases
      that matter, and 36,000 is provisional until they are measured.
- [ ] **Is rule 3 gateable?** The one ADR-027 consequence answered by discipline rather than mechanism
      is unbounded growth. A check — payload size trend, or a spine that must stay under N — would
      convert this record's High/High risk into an exit code, which is what this repo asks of prose.
- [ ] Reconcile the surviving half of the disputed measurement in Risks, with the endpoint named on
      both sides.
- [ ] **Settle ADR-039 against ADR-038** — if the `must.*`/`ref.*` tier moves onto KV keys, ADR-038's
      supersede-on-update leaves that tier alone; if it does not, every correction re-points it. Live
      rather than dormant now that ADR-038 is Accepted and merged; decide before implementation.

**Closed in review (2026-08-27) — recorded as prose rather than as checked boxes, because a checked
box in a record with no `tasks/` directory is a done-claim `adr-verify` has nowhere to write evidence
for, and `adr-lint` says so:**

- *Confirm the ADR-027 qualification, or narrow this ADR instead.* **Re-opened by the second review
  and now answered properly.** The first review endorsed the qualification; the cold read showed its
  stated argument was wrong, because ADR-027's rationale is not only retrieval. The qualification
  survives, but on four arguments rather than one — see the table above. ⚠ The endorsement is what
  made this easy to leave alone, which is why it took a reviewer with no stake in the earlier round
  to catch it.
- *A guidance home for five commands sharing one tool description.* Dissolved by the split into five
  tools; see Out of Scope.
