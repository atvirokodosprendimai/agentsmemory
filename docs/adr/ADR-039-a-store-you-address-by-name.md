# ADR-039: A store you address by name

**Status:** Proposed
**Date:** 2026-08-27
**Owner:** M
**Spec:** None — no spec stage. Grounded in the declarations at `internal/palace/chunk.go:56` (`MaxEmbedRunes`), `:140` (`DrawerID`), `internal/palace/contentkey.go:297` (`mintOrReuse`), `internal/palace/kg.go:35` (`MaxKGValueLen`), `internal/mcpserver/drawers.go:56` (`wholeMemoryBudget`), `db/migrations/00001_init.sql:61` (`skills`), `db/migrations/00010_kg.sql:22` (`kg_triples`), plus a serialisation measurement recorded inline. ⚠ Line numbers are as of `main` at `5760bca`, re-checked 2026-08-27 after review; this record was first written against `7e8870a`, and three of its citations had already moved.
**Cross-references:** ADR-027 (a maintained document is a set of records — **qualified here, see Invalidates**), ADR-036 (the bootstrap surface this complements), ADR-038 (opaque ids — **it landed under this record and rewrote its Context; see below**), ADR-040 (the schema carries the pairing — where per-tool guidance is being decided, PR #77), ADR-003 (retire the closet prior — the ranking cost this protects), ADR-010 (supersede, do not overwrite — **CLOSED, absorbed in full by ADR-038**), ADR-013 (a page of memories, not chunks), ADR-016 (a memory an agent files must be navigable), `internal/mcpserver/catalog_test.go:429` (`TestEveryCatalogToolIsNamedInTheReadme`)
**Numbering:** next free after ADR-038, which **merged 2026-08-27** (PR #72 at 08:09:36Z, execution PR #76 at 13:54:56Z) and is now head on `main`. Union-checked across every open PR head 2026-08-27: only ADR-039 (PR #75) and ADR-040 (PR #77) are claimed, no collision. ⚠ **Allocate at merge** — a per-branch check is blind to cross-branch collisions, the rule this repo recorded after its ADR-number collision. Same for the migration: **`00033` is head on `main`** (ADR-038 took `00030`–`00033`), so the next free is **`00034`**.
**Invalidates:** none outright. ⚠ **QUALIFIES ADR-027 (Accepted)** for one class of document — see below. Checked by grepping ADR-001..038 on `main`, plus ADR-039/040 on their open branches, for `key/value`, `kv_`, `author-chosen`, `addressed by name`: no accepted ADR governs a name-addressed store. ADR-027 governs the adjacent question and its rule 3 phrase ("the spine stores how to traverse, never what is there") is adopted here verbatim rather than displaced.
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
6. ⚠ **The first dotted segment is a namespace, and the server requires it.** Added 2026-08-27 in
   response to review; see the argument below.

**Why the namespace is enforced rather than conventional.** A team is a workspace and a workspace
holds many projects — this one holds 13 wings. Team scope with free-form keys therefore means two
projects both reaching for `root` collide silently, and `list` with prefix `must.` returns another
project's must tier as if it were yours. The first draft mitigated that with prose ("each project
writes its key conventions in its own `AGENTS.md`") plus "`list` exposes it". **That is the mitigation
shape this repo has already ruled against:** `AGENTS.md` states it plainly — *prose belongs where a
human reads it and nowhere else; anything that must stay true gets a command whose exit code says
so.* A convention nobody can fail is not a convention, it is a hope.

So: `write` and `list` **require** a namespace segment, and `list` is scoped to one by default. This
costs no wing param, no resolution ladder, and the flat-dotted grammar survives intact.

⚠ **This creates one tension with property 1, and it is named rather than hidden:** a namespace is
prior knowledge, which is what property 1 promises a reader will not need. The resolution keeps
property 2 doing the work — **an unqualified `read` is an error that names the namespaces holding
that key**:

```
read id:"root"  -> ERROR: "root" exists in 2 namespaces: agentsmemory, forumchat
```

That fails closed, costs one extra call only when the key is genuinely ambiguous, and **teaches the
caller instead of guessing for them** — which is the same principle as property 2, applied one level
up. A single-namespace team never sees it.

Bootstrap end state:

```
am_kv_store({command:"read", id:"agentsmemory.root"})   -> the blob, one literal of prior knowledge
am_kv_store({command:"read", id:"root"})                -> ERROR naming the namespaces (see above)
am_kv_store({command:"list", id:"agentsmemory.must."})  -> the must tier, this project's only
am_kg_query({predicate:"retracts"})                     -> corrections; only the graph answers this
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

- **A stable opaque id plus a name→id pointer.** ⚠ **This alternative did not exist when the record
  was first written — ADR-038 created it hours later, and it is the strongest one on this list.**
  Since a drawer's id is now minted once and never recomputed, a name could simply point at it:
  `am_kg_add(subject: "root", predicate: "points_at", object: <64-hex>)`. A 64-hex id is 64 runes,
  comfortably inside `MaxKGValueLen = 128`, so this needs **no new table and no new tool** — a real
  advantage this proposal does not have, and the reason it deserves arguing rather than listing.

  REJECTED, on three counts:
  1. **The resolve step inherits the exact failure this record exists to delete.** `am_kg_query`
     fails open: a mistyped or absent name returns `count: 0` with no error, indistinguishable from a
     graph that holds nothing. So `read("root")` becomes *resolve-then-fetch*, where the resolve can
     silently answer "nothing" and the caller cannot tell. Property 2 below — read fails **closed** —
     is not a nicety; it is the difference between an entry point and a trap, and a pointer cannot
     have it while the graph fails open.
  2. **It leaves the payload problem untouched.** The thing at the end of the pointer is still a
     drawer: chunked at `ChunkSize`, refused for in-place update above `MaxEmbedRunes = 4000`, and
     **embedded into the ranked pool**. So "DO NOT GROW THIS DRAWER" survives, and session-start
     content keeps competing with real memories for top-1 — the ADR-003 cost this record protects
     against. Naming the address does not un-rank the content.
  3. **It creates a second thing to keep true.** The pointer and the row are now two records of one
     address. Under ADR-038 a re-file **ends** the old row rather than overwriting it, so a pointer
     can come to name an ended row while still resolving — and that is measured, not hypothetical:
     ADR-038 shipped `doctor --corpus` because 16 facts already named a drawer that no longer
     existed, and it reports "points at an ENDED row" as a distinct third state for this reason.

  ★ **The honest summary: this alternative fixes the *address*, and this record is about the
  *address and the payload together*.** If only the address were broken, this would win on cost.
- **A positional `[cmd, payload, version]` dispatcher** (the original proposal). REJECTED. A tool
  description is the strongest guidance surface — present at the moment of the call, in every client
  — and an opaque dispatcher deletes it at the call every session makes first.
  `TestEveryCatalogToolIsNamedInTheReadme` (`catalog_test.go:429`) already fails the build over
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
this PR — but what it decides *is* one new table and one new MCP tool, so the surfaces an
implementation must touch are named here rather than discovered later.

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `kv_entries` (schema) | **add** — new table, PK `(team_id, id, version)` | `db/migrations/00034_kv_entries.sql` (allocate at merge) | `internal/palace` KV service + repo |
| `am_kv_store(command, id, payload, version, limit, offset)` | **add** — one tool, five commands | `internal/mcpserver` | every agent at session start |
| Tool registration | **add** — must go through `newTool` + `registrar.add`/`addWrite` | `internal/mcpserver/server.go` | ⚠ `mcp.NewTool`/`AddTool` compile fine and silently skip the `am_` prefix, the catalogue **and** the role gate — this repo's own recorded trap, and reachability is its signature defect |
| Write-gate role | **add** — `write`/`delete` are write-gated, `read`/`list`/`history` are not | `internal/mcpserver` | member keys must be refused on the three writes |
| README tool count + tool list | **change** — the new tool must be named | `README.md` | `TestEveryCatalogToolIsNamedInTheReadme` (`catalog_test.go:429`) and `TestCatalogSizeIsWhatTheReadmeClaims` both fail the build otherwise |
| Key namespace validation | **add** — first dotted segment required on `write` and `list` (Decision property 6) | `internal/palace` | every caller; enforced server-side, not by convention |
| `read` on a missing key | **add** — errors, and an *unqualified* key errors naming the namespaces that hold it | `internal/mcpserver` | the fail-closed property this record rests on |
| Over-cap `write` | **add** — refused at 36,000 runes at **write** time | `internal/palace` | never at read time, where the spill is silent and unfixable |
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
on a hot key at 36,000 runes is unbounded — ~500 edits of the root is ~12MB for one key. Pruning to
last-N can be added later without an API change, because `history` is paged from day one.

## Out of Scope

- **Search over KV** (permanent: it is the known-address tier, and giving it recall would put the
  ephemera back in the ranked pool this record exists to keep it out of).
- **Relations** (permanent: `id|id|how` is the graph's job and the graph does it well; a second
  edge store would be two answers to one question).
- **`kg_supersede`** (permanent: **shipped, and this entry is what it looked like before it did**).
  ⚠ The first draft called this "a real, separate gap … ADR-010 (Proposed) already covers the
  principle. Filed to `wing_agentmemories`/`inbox`." All three clauses have since gone false:
  `am_kg_supersede` **landed in ADR-038 T4** — `KGSupersede` at `internal/palace/supersede.go:205`,
  registered at `internal/mcpserver/kg.go:126`, in one transaction with `reason` required — and
  ADR-010 is **CLOSED**, absorbed by ADR-038. The inbox item is stale and its atomic-verb half is
  closed; only the boundary-overlap half survives, in issue #47.
- **Per-tool guidance for `am_kv_store`'s commands** (deferred: **ADR-040, PR #77** — "the schema
  carries the pairing"). Review proposed MCP's `instructions` field as the home for the key grammar.
  That question is already being decided one record over, and ADR-040 answers it in a way that
  constrains this one: **there is no per-tool `instructions` field** in mcp-go v0.55.1 (it lists and
  rejects exactly that), and lengthening the *server-level* one is rejected there at 1,143 bytes of a
  tested budget (`TestInstructionsStayShort`, `instructions_test.go:151` — ADR-040 cites `:153`;
  re-checked against `main` here), defended by ADR-017's measurement. ⚠ ADR-040 also flags the "clients do not truncate it" premise as unverified — its
  risk table requires any truncation figure to "name the client and the version it was measured on,
  or it is folklore wearing a number". **So this record should not adopt the suggestion
  independently; the two must be read together.**
- **ADR-027's remaining open question** (deferred: ADR-038's own recorded follow-up) — a reference
  pointing at a non-parent chunk that a re-chunk deletes. It is *upstream* of this record's ADR-027
  qualification above: both concern where ADR-027's authority ends now that ids are opaque, and they
  are worth resolving in one pass rather than two.
- **Deploying `am_bootstrap`/`am_entry_point`** (permanent: already built, merged and deployed —
  the hosted catalogue serves both, verified 2026-08-27).

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| **Content drifts in that should have been a memory** — 36,000 runes is a lot of room, and roots grow | High | High | ADR-027 rule 3, adopted verbatim: the spine stores how to traverse, never what is there. A gate is worth considering. This is the risk that would turn the tier into a second, unsearchable corpus. |
| **A payload near the cap spills to a file on read** and never enters context | Low | High | Cap is 36,000 runes against `wholeMemoryBudget = 40_000` (`drawers.go:56`). Measured JSON overhead: **4.2%** on prose markdown, **7.3%** on dense drawer text, so 36,000 serialises to ~37.5–38.6KB. **Refuse an over-cap write at `write` time**, never at read time where it is silent and unfixable. |
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

⚠ **The first draft deferred to F-16 as unchecked and said it "should be checked first, since it may
close most of the gap on its own". Both halves of that are now answered, and the answer runs the
other way.**

**F-16 has been measured (2026-08-27), and `am_bootstrap` does not subtract.** The call itself is
cheap — 1 call, 10.7KB — but a *bootstrap-led session* still came to **33 calls / 182,575 B** against
the hand-executed protocol's **33 calls / 180,500 B**: the same call count and about 2KB *more*. The
reason is structural rather than a defect in the tool: the bootstrap's inline tier is the entry
*room*, while the `must.*` tier hangs off the root *drawer* one hop further out, so the payload stays
scattered across records reachable only by traversal. **One addressed key carrying the tier as a
value is precisely the thing `am_bootstrap` could not be** — which makes F-16 an argument *for* this
record, not a reason to wait on it.

⚠ **And the stated blocker no longer holds.** Review reported that F-16 "cannot be checked on the
motivating wing" because `am_bootstrap` returns `resolution: "unknown_term"` on `wing_agentmemories`.
Called live on the hosted palace (workspace `atvirokodosprendimai-498ccd`) it returns
`resolution: "matched"`: the entry edges were hand-authored at 08:28–08:29Z on 2026-08-27, roughly
six hours before that review was submitted at 14:59Z. The backfill for *other* wings is still filed
in `BACKLOG.md` and still has not run — this wing was fixed by hand, and no other wing has one.
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
- [ ] ⚠ **Confirm Decision property 6 — the enforced namespace segment — is the owner's call.** It
      was added in response to review, not present in the original design, and it trades one literal
      of prior knowledge for a collision that would otherwise be silent in a 13-wing workspace. It is
      the one substantive design change this round; reversing it is a one-line edit to property 6.
- [ ] Reconcile the surviving half of the disputed measurement in Risks (`llm_open_threads`: 1 vs
      13/15), with the endpoint named on both sides.
- [ ] **Settle ADR-039 against ADR-038** — if the `must.*`/`ref.*` tier moves onto KV keys, ADR-038's
      supersede-on-update leaves that tier alone; if it does not, every correction re-points it. This
      is live rather than dormant now that ADR-038 has merged, and it should be decided before
      implementation starts.

**Resolved by review (2026-08-27):**

- [x] ~~Confirm the ADR-027 qualification is the reviewer's intent, or narrow this ADR instead.~~
      Confirmed. Review: *"Narrowing an accepted decision by showing that its falsifier does not
      reach the new case … is the right way to do it,"* and adopting rule 3 verbatim rather than
      displacing it "is the correct call too."
