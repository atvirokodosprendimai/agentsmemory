# ADR-040: A memory filed is not a memory linked

**Status:** Proposed
**Date:** 2026-08-27
**Owner:** unassigned
**Spec:** None — no spec stage. Grounded in the declarations at `internal/palace/kg.go:1058` (`EdgeAttachment`), `:1071` (`attachDerivedEdge`), `internal/palace/chunk.go:140` (`DrawerID`), `internal/mcpserver/drawers.go:197` (`has_edge`/`edge_derived`), `internal/mcpserver/kg.go:224` (the conditional hint), `internal/mcpserver/status.go:47` (`statusHint`), plus a session measurement recorded inline. ⚠ Line numbers are as of `main` at this branch point.
**Cross-references:** ADR-016 (a memory an agent files must be navigable — this is its write-path sequel), ADR-017 (placement beats instruction), ADR-021 (the handshake carries the protocol — the server-level twin of this per-tool question), ADR-039 (the derived-address problem this is a symptom of, open, PR #75)
**Numbering:** next free after ADR-039 (open, PR #75); ADR-038 is head on `main`. ⚠ **Allocate at merge** — a per-branch check is blind to cross-branch collisions, the rule this repo recorded after its ADR-number collision.
**Invalidates:** none — checked by grepping ADR-001..039 for `hint`, `edge_derived`, `attachDerivedEdge`, `am_kg_add` and `tool description`: ADR-016 governs the derived graph and is **extended, not displaced**; ADR-021 governs the server-level `instructions` field and is untouched.
**Served-path change:** **Yes.** `am_add_drawer` gains one optional argument and one conditional result field. No existing signature, default, or ranking path changes; a caller that passes nothing behaves exactly as it does today.

## Context

**The measurement is already in the tree, in the doc comment of the function this ADR is about.**
`attachDerivedEdge` (`kg.go:1071`) records it: **57 of 1,985 drawers carried any edge (2.9%), and 0
were named as a triple OBJECT** — so the taxonomy pattern the team's own operating skill is built on
had zero adoption in the workspace that wrote it.

That measurement produced a write-path fix: every newly filed drawer now gets a derived
`room:<wing>/<room> holds <id>` edge. The fix works. But it changed what "has an edge" means without
changing what the response says about it.

**Measured 2026-08-27.** Three drawers filed through `am_add_drawer` in one session. All three
returned `"has_edge": true, "edge_derived": true`. Read as a result, that is two green fields. Read
accurately, `edge_derived: true` means **the writer authored nothing and the server bucketed the
drawer under its room**. It is reachable from the room node and connected to nothing that carries
meaning — not the decision it corrects, not the ADR it grounds, not the measurement it rests on.

**And the reason the writer authored nothing is structural, not lax.**
`DrawerID = sha256(teamID, wing, room, sourceFile, chunkIndex, content)` (`chunk.go:140`). The
address does not exist until the write happens, so an edge to a new drawer **cannot** be authored in
the same breath as the drawer — it needs the id the write returns. The team's own
`memory-orchestration` skill instructs *"the write is `am_add_drawer` PLUS `am_kg_add`, in the same
breath"*, and the API makes "in the same breath" impossible. Every agent that follows the
instruction does so as two calls with a 64-hex id copied between them, and
*"⚠ Drawer ids are FULL LENGTH — a prefix silently creates a NEW node"* is the trap that lives in
exactly that gap.

So this is ADR-039's finding arriving from the write side: **an address you cannot choose is an
address you cannot reference until after the fact.**

## Existing Primitives Audit

| Primitive | Shape | Verdict |
|---|---|---|
| **`EdgeAttachment`** (`kg.go:1058`) | three outcomes — `EdgeAuthored` / `EdgeAlreadyDerived` / `EdgeNewlyDerived` | **Reuse verbatim.** The signal already exists and is already computed on every write. It exists precisely because "no error" covered three outcomes and the caller reported all of them as a fresh derivation. |
| **`has_edge` / `edge_derived`** (`drawers.go:197`) | two booleans on the add result | **Reuse.** They are the right facts. They lack a sentence. |
| **the `hint` key in a tool result** | four tools already emit one: `am_status` (`status.go:47`), `am_skillset`, `am_recall_stats`, `am_kg_query` (`kg.go:224`) | **Reuse and formalise.** `am_kg_query`'s is the model — computed and *conditional*, present only when facts were actually withheld, with `kgquery_test.go:133/266/286` asserting both presence **and absence**. |
| **`handoffRefusal`** (`drawers.go:173`) | refuses an inbox item into an empty wing, at write time, while the filer can still fix it | The precedent for a hard "must". Considered and **not chosen** — see Alternatives. |
| **`Tool._meta`** (mcp-go v0.55.1) | per-tool, spec-sanctioned, opaque passthrough | **Rejected.** No client surfaces it to a model. A field nothing reads is this repo's signature defect with a new hat on. |
| **`Tool.description`** | ~1.3KB headroom per tool before the ~1,800-char truncation some clients apply (our longest is 521B) | **Insufficient alone.** Read once at catalogue time, far from the call, and only for tools a client actually calls — the grounds on which ADR-021 already rejected descriptions for protocol. It also cannot say what happened on *this* write. |

**There is no per-tool `instructions` field to fill.** mcp-go v0.55.1's `Tool` is
`name`/`title`/`description`/`inputSchema`/`outputSchema`/`annotations`/`_meta`/`icons`/`execution`,
and the newer spec-side additions are `title`/`outputSchema`/`_meta`/`icons`. The protocol offers no
slot for a per-tool obligation, which is why this ADR reaches for the response instead.

## Decision

**Authoring the link becomes part of the write, and the response says so when it was not.**

Two mechanisms, in that order of strength:

**1. `am_add_drawer` gains an optional `edge` argument.**

```jsonc
am_add_drawer({
  wing: "wing_x", room: "decisions", content: "…",
  edge: { subject: "ADR-016", predicate: "grounded_in" }   // object = the id the server just computed
})
```

The server fills the object with the id it computed during the write, so **the caller never handles a
64-hex string** and the prefix trap cannot fire on this path. An authored edge suppresses derivation,
exactly as `attachDerivedEdge` already does when a triple already names the drawer as its object: an
authored edge always wins, and a server guess must not sit beside a human decision as though the two
were equivalent.

**2. `am_add_drawer` returns a conditional `hint` when `edge_derived == true`** — and only then. The
sentence names `am_kg_add`, and carries the id already filled in, so the two-call route stays open
and cheap for a writer who does not yet know what to link to.

**Why 1 before 2.** ADR-017 measured that placement beats instruction. This goes one step further and
removes the step rather than relocating the reminder: a hint is a better reminder, an argument is not
a reminder at all.

**Why 2 at all.** An `edge` a caller may omit is still optional, and the honest case for omitting it
is real — a writer sometimes genuinely has nothing to link to yet. The hint is what turns that from
an oversight into a decision.

### The convention, so hints do not accrete into a second protocol

Four rules, binding on **every** future hint, not only these:

1. **A hint states what to do NEXT, and appears only when the response shows it is needed.** An
   unconditional hint is scenery within three sessions.
2. **It is computed from what happened, never from what was asked.** `EdgeAttachment` is the input
   here.
3. **Its test fails when the wiring is removed.** Not "hint is a string": file a drawer with no
   authored edge and assert the hint names `am_kg_add`; file one **with** an authored edge and assert
   the hint is **absent**. That is the shape `kgquery_test.go` already uses, and it is what this
   repo's reachability rule demands — a test that asserts a call still returns something passes
   happily while the feature does nothing.
4. **It is budgeted, asserted rather than intended** — a per-hint ceiling in `catalog_test.go`, the
   same defence `TestInstructionsStayShort` gives `serverInstructions`.

## Alternatives Considered

- **A per-tool `instructions` field in the MCP schema.** REJECTED — it does not exist; see the audit.
- **`Tool._meta`.** REJECTED — opaque to every client that would have to act on it.
- **Put the rule in the tool `description`.** REJECTED as the primary mechanism, kept as a one-line
  supplement. Read at catalogue time, far from the call; ADR-021 rejected descriptions for protocol
  on the same grounds; and it cannot say what happened on this particular write.
- **Refuse the write unless an edge is authored**, on `handoffRefusal`'s precedent. REJECTED for now,
  and not out of squeamishness: it breaks every existing caller, and it forces an edge in the one
  case where omitting one is correct. **Revisit if mechanism 2 measurably fails to move adoption** —
  the falsifier below is written so that failure is visible rather than arguable.
- **Backfill the orphans instead.** Not an alternative. It is a separate, already-deferred item with
  a receipt in `BACKLOG.md`, and it governs the existing corpus while this governs the write path.
  Doing only the backfill re-accumulates orphans from the next write onward.
- **Put the pairing in the `am_skillset` preamble.** REJECTED — **it is already there**, in
  `memory-orchestration`, and the 2.9% in `attachDerivedEdge`'s comment is what compliance with it
  looks like. This is ADR-017's finding restated: the instruction exists and does not reach the
  moment of the call.

## Component / Boundary Impact

`internal/mcpserver` gains one optional argument on one tool and one conditional result key.
`internal/palace` gains an optional authored-edge write inside `Service.Add`, reusing the
`EdgeAttachment` outcome it already computes — no new domain concept, no schema change, no migration.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_add_drawer` `edge` argument | add — optional | `internal/mcpserver/drawers.go` | every MCP client that files a memory |
| `am_add_drawer` result `hint` | add — present only when `edge_derived == true` | `internal/mcpserver/drawers.go` | the calling agent, at the moment it can still act |
| `palace.AddInput.Edge` | add — optional `{subject, predicate}` | `internal/palace/service.go` | `am_add_drawer`; the importer keeps today's behaviour |
| hint budget assertion | add | `internal/mcpserver/catalog_test.go` | CI |

## Consequences

- **Positive:** the pairing the team's own skill mandates becomes one call, so there is nothing to
  forget and no 64-hex id to mis-copy. The prefix trap cannot fire on the write path at all.
- **Positive:** `edge_derived: true` stops reading as success. The response distinguishes *stored*
  from *linked* in a sentence, not only in a boolean a reader has to interpret.
- **Positive:** the `hint` convention gets a written rule and a test shape, which four ad-hoc call
  sites currently do not have.
- **Negative:** one more argument on the tool every session calls most. Mitigated by it being
  optional and by the description gaining one line, not a paragraph.
- **Negative:** hints are context every caller pays for. Mitigated by rule 1 (conditional) and rule 4
  (budgeted, asserted).
- **Neutral:** the existing orphans are untouched. That is the backfill's job, and it is filed.

## Out of Scope

- **Backfilling the existing orphaned drawers** — deferred with a receipt in `BACKLOG.md`.
- **Hard refusal** (rung 3) — named in Alternatives, revisited only against the falsifier below.
- **Hints on tools other than `am_add_drawer`** — the convention is written to bind them, but the
  candidates (`chunks > 1` freezing a memory, an unscoped `am_search`, a short-hex `am_kg_add`
  object) are listed as follow-ups rather than smuggled in here.
- **Which MCP clients surface a result `hint` to their model at all** — the result payload is read by
  every client that calls the tool, unlike `instructions`; ADR-021's open question does not apply,
  but this has not been measured either.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| **The `edge` argument ships and nobody passes it** | Med | Med | Mechanism 2 catches exactly this case, and the falsifier measures it rather than assuming. If both fail, the answer is rung 3, stated in advance. |
| **Hints accrete until they are scenery** | High | Med | Rule 1 (conditional) and rule 4 (a ceiling asserted by a test, not intended by an author) — the defence ADR-017 T2 and ADR-021 both used. |
| **A test asserts the hint exists without asserting it can be absent** | Med | High | Rule 3 makes the absence case part of the required test, matching `kgquery_test.go`. This is the repo's signature defect and it must be closed by construction. |
| **`subject` is a free string, so a typo makes a new entity silently** | Med | Med | The existing KG normalisation applies unchanged. ⚠ Not solved here, and worth the reviewer's attention: it is the same fail-open shape `am_kg_query` has. |

## Rollback

Both halves are additive. Drop the `edge` argument and the `hint` key and every caller behaves
exactly as it does today; an authored edge already written stays valid, because it is an ordinary KG
triple with no new shape. Nothing is stored, migrated, or re-shaped. Revert order: result key, tool
argument, `AddInput` field.

## What would falsify this

**The share of new drawers whose edge is authored rather than derived must move.** Measure
`edge_derived == false` as a fraction of writes over a fixed window before the change and after it,
against the same palace. The baseline exists: 0 of 1,985 drawers named as a triple object
(`kg.go:1071`, 2026-08-26).

If the rate does not move, agents are not reading the argument either, and this ADR has relocated a
reminder rather than removed a step — in which case the honest response is to say so here and escalate
to rung 3 (refusal), not to keep the hint and call it done.

## Follow-ups / still undecided

- [ ] **Is `subject` free-form, or must it resolve to an existing entity?** Free-form matches
      `am_kg_add` today and fails open on a typo. Reviewer's call.
- [ ] **Should `edge` accept more than one triple?** One covers the motivating case; N is not harder,
      but it invites the drawer-plus-taxonomy dump the KG is not for.
- [ ] The other hint candidates: `chunks > 1` (a memory frozen from birth — hit twice in the session
      that produced this ADR), an unscoped `am_search` under an empty `default_wing`, and a
      short-hex `am_kg_add` object.
- [ ] Report the measured before/after from the falsifier, whichever way it falls.
