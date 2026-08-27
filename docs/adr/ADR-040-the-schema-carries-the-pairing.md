# ADR-040: The schema carries the pairing

**Status:** Proposed
**Date:** 2026-08-27
**Owner:** unassigned
**Spec:** None — no spec stage. Grounded in the declarations at `internal/mcpserver/server.go:37` (`newTool`), `:52` (`CatalogEntry`), `:98` (`registrar.addWrite`), `:111` (`classifyTool`), `internal/mcpserver/catalog_test.go:38` (`liveSurface`), `:292` (`TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt`), `internal/palace/kg.go:1058` (`EdgeAttachment`), `internal/palace/chunk.go:140` (`DrawerID`). ⚠ Line numbers are as of `main` at this branch point.
**Cross-references:** ADR-021 (the handshake carries the protocol — this is its per-tool twin, and the division of labour between them is the decision here), ADR-016 (a memory an agent files must be navigable — the obligation this ADR's first pairing expresses), ADR-017 (placement beats instruction)
**Numbering:** next free after ADR-039 (open, PR #75); ADR-038 is head on `main`. ⚠ **Allocate at merge** — a per-branch check is blind to cross-branch collisions, the rule this repo recorded after its ADR-number collision.
**Invalidates:** none — checked by grepping ADR-001..039 for `description`, `tools/list`, `annotations`, `catalog`: ADR-021 governs the server-level `instructions` field and is **narrowed by agreement here, not displaced**.
**Served-path change:** **Yes.** Some tool descriptions on `tools/list` grow by one clause. No tool signature, argument, default, result shape, or ranking path changes.

## Context

**The question that opened this, asked 2026-08-27:** can we put an `instructions` field on our MCP
endpoints, so an agent picks up an obligation — *"`am_add_drawer` goes with `am_kg_add`, and that is
not optional"* — automatically, from the schema, without having loaded a protocol file?

**There is no per-tool `instructions` field to put it in.** In mcp-go v0.55.1, `Tool` is
`name` / `title` / `description` / `inputSchema` / `outputSchema` / `annotations` / `_meta` /
`icons` / `execution`, and the newer spec-side additions are `title` / `outputSchema` / `_meta` /
`icons`. `_meta` exists and is opaque — no client surfaces it to a model — so filling it would be a
capability that is finished and unreachable, this repository's signature defect.

**So the channel is `description`, and it has room.** Our longest tool description is 521 bytes;
some agent clients truncate at ~1,800. Every tool has roughly 1.3KB of unused, already-delivered,
already-model-visible space.

**And a per-tool obligation is what a description is FOR.** ADR-021 rejected putting protocol in
descriptions, and that rejection stands for what it actually judged: the *wing rule*, a cross-cutting
rule belonging to no single tool, which a client reading only the tools it calls would miss. **A
pairing is the opposite case.** It belongs to exactly one tool, it is read by exactly the agent about
to call that tool, and "a client only reads the tools it calls" stops being an objection and becomes
the delivery mechanism. Narrowing ADR-021 to the class it measured is part of this decision rather
than a side effect of it.

**The motivating pairing, and why exhortation has not worked.** `attachDerivedEdge`'s own doc comment
records the measurement: **57 of 1,985 drawers carried any edge (2.9%), and 0 were named as a triple
OBJECT.** The team's `memory-orchestration` skill already says *"the write is `am_add_drawer` PLUS
`am_kg_add`, in the same breath"*. The instruction exists, in a document sessions load, and the
adoption is zero — ADR-017's finding restated: an instruction that does not reach the moment of the
call does not reach the call.

## Existing Primitives Audit

Everything this needs is already built. That is the argument for doing it this way.

| Primitive | Shape | Verdict |
|---|---|---|
| **`classifyTool`** (`server.go:111`) | already **mutates the tool at registration** to stamp `ReadOnlyHint`/`DestructiveHint`, at the same chokepoint that enforces the policy | **Reuse verbatim.** Its own comment states the principle this ADR extends: make the policy visible on the wire at the chokepoint that enforces it, so clients need no second list that drifts. |
| **`registrar.add` / `addWrite`** (`server.go:80`, `:98`) | the two registration chokepoints; `addWrite` already knows a tool is a write | **Reuse.** The write/read split is already the axis a pairing rule needs. |
| **`CatalogEntry`** (`server.go:52`) | `{Name, Description, Write}`, accumulated live | **Reuse.** The place a declared pairing is carried. |
| **`liveSurface`** (`catalog_test.go:38`) | returns the registrar catalogue **and** the tools a real client receives from `tools/list` | **Reuse verbatim.** Its comment says why it exists: to stop catalogue-only tests blessing metadata never published on the wire. That is exactly the failure a pairing gate must not have. |
| **`TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt`** (`catalog_test.go:292`) | asserts every tool honouring a behaviour **declares it in its schema**, deriving its universe from the code | **The precedent, and the template.** A pairing gate is the same test with a different predicate. |
| **`EdgeAttachment`** (`palace/kg.go:1058`) | `EdgeAuthored` / `EdgeAlreadyDerived` / `EdgeNewlyDerived`, computed on every write | Not used by this ADR. Named because it is what a *runtime* mechanism would use — see Alternatives. |
| **`Tool._meta`** | per-tool, spec-sanctioned, opaque | **Rejected.** No client surfaces it to a model. |

## Decision

**A rule that belongs to one tool lives in that tool's description. A rule that belongs to no tool
lives in the server `instructions`. Neither carries the other's, and both are gated.**

Three parts:

**1. A pairing is declared as data at registration, and composed into the description there.**
`classifyTool` already rewrites the tool at the chokepoint; the pairing clause is appended in the
same place, so the obligation and the description cannot drift apart and no tool can carry one
without the other.

```
am_add_drawer  →  … existing description …
                  PAIRS WITH am_kg_add — a drawer with no authored edge is reachable only by its
                  room bucket, not by traversal from what it is about.
```

**2. Every WRITE tool declares a pairing, or is exempt with a written reason.** The exemption is the
review: this repo already refuses an unjustified escape hatch elsewhere (`notOperatorFacing` /
`TestNotOperatorFacingIsJustified`), and a bare list of exempt tools would be the same defect this
ADR is trying to close. Reads are out of the requirement's scope; a read that has a genuine pairing
may still declare one.

**3. Three gates, all reading the live wire.**

- **Declared** — every write tool has a pairing or a reasoned exemption. Derived from the live
  catalogue, so a tool added tomorrow joins the check on the same commit.
- **Published** — the clause appears in the description a real `tools/list` returns, via
  `liveSurface`. Catalogue metadata that never reached the wire must not pass.
- **Budgeted** — no description exceeds a ceiling below the ~1,800 truncation some clients apply.
  Asserted rather than intended, the defence `TestInstructionsStayShort` already gives
  `serverInstructions`.

⚠ **The gate must fail when the pairing is removed.** Not "the description is non-empty": delete a
declaration and the build goes red. That is this repository's stated rule, and the four capabilities
it has shipped finished-and-unreachable all had tests that exercised the component instead of the
selection.

**4. The skill ROUTES to the schema; it does not restate it.** One line in the team's centralised
wake-up guidance — *"how to use a tool is in that tool's own description; read it before you call a
write tool, and look for a PAIRS WITH clause"* — and nothing else about individual tools.

This is what makes part 1 work rather than merely true. The honest weakness of the description
channel is that it is read once, at catalogue time, and skimmed; a routing line turns it into a step
an agent takes deliberately at the moment it reaches for a tool. It is also the only part of this
ADR that costs no code.

⚠ **And it degrades gracefully, which is why the routing goes in the skill and the RULES do not.** A
skill body is a mutable row — measured 2026-08-27, `skillset.DefaultPlaybook` does not name the
`start-here` skill while the live edited preamble does, so a rule living only in a skill is pinned by
no test and restored by no seed. If the routing line is lost, the descriptions still carry the
obligation and the gates still enforce it: what is lost is the priming, not the rule. The inverse —
rules in the skill, routing in code — would lose the rule.

⚠ **A routing line must land in every document that teaches the route.** This repository has already
paid for the other outcome: the entry-point fix landed in `start-here` alone while
`memory-orchestration`, `human-decisions` and `AGENTS.md` kept teaching a route that terminated at
call 4, and the test that measured the fix scored full marks because it happened to load the one
patched document.

### The division of labour, stated so it can be checked later

| Rule | Home | Why |
|---|---|---|
| "pass an explicit wing; `"*"` is not a safe default" | server `instructions` | belongs to no one tool; a client reading only the tools it calls would miss it (ADR-021) |
| "`am_add_drawer` pairs with `am_kg_add`" | that tool's `description` | belongs to exactly one tool; reaches exactly the agent about to call it |
| "ids are FULL LENGTH — a prefix creates a new node" | `am_kg_add`'s argument description | belongs to one **argument**, and the argument description is already the closest text to the mistake |

## Alternatives Considered

- **A per-tool `instructions` field.** REJECTED — it does not exist in the protocol.
- **`Tool._meta`.** REJECTED — opaque to every client that would have to act on it.
- **An optional `edge` argument on `am_add_drawer`**, so the pairing becomes one call. REJECTED as
  out of scope: this ADR was asked to use the schema channel, and an argument is an API change with
  its own migration and review surface. It remains the strongest form of "not optional" — it removes
  the step rather than describing it — and is the obvious follow-up if the gate below fails. Recorded
  here rather than dropped, because it is what a reviewer will otherwise propose.
- **A conditional `hint` in the `am_add_drawer` result**, computed from `EdgeAttachment`. REJECTED as
  out of scope for the same reason, and noted as the runtime complement: the schema says what the
  pairing *is*, a result hint would say that *this* write is missing it. Four tools already emit a
  `hint` key, so the mechanism exists if it is ever wanted.
- **Put the pairing in the `am_skillset` preamble.** REJECTED — it is already there, in
  `memory-orchestration`, and 2.9% is what compliance with it looks like.
- **Lengthen the server `instructions` instead.** REJECTED — it is at 1,143 bytes of a tested
  1,200-byte ceiling, and ADR-017 measured that length is not what works. It is also the wrong home:
  see the division of labour above.

## Component / Boundary Impact

`internal/mcpserver` only: one declaration site per paired tool, one composition step in
`classifyTool`, and three assertions in `catalog_test.go`. No other package, no schema, no migration.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_add_drawer` description on `tools/list` | add — one pairing clause | `internal/mcpserver` | every MCP client, on every `tools/list` |
| pairing declaration | add — data beside registration | `internal/mcpserver` | `classifyTool`, the gates |
| declared / published / budgeted gates | add | `internal/mcpserver/catalog_test.go` | CI |
| one routing line in the team's wake-up guidance | add — points at the schema, restates nothing | the centralised skills + `AGENTS.md` | every session, at wake-up |

## Consequences

- **Positive:** the obligation reaches an agent that has loaded no protocol file at all, through a
  field every client already delivers to its model, at the moment it is choosing that tool.
- **Positive:** ADR-021's boundary becomes explicit and testable rather than a judgement re-made per
  rule. The division-of-labour table is the artifact a future reviewer checks against.
- **Positive:** the budget gate closes the ~1,800 truncation as a silent failure mode before it bites.
- **Positive:** the skill gets shorter, not longer. Routing to the schema means per-tool guidance has
  exactly one home, so a rule cannot be fixed in the description and left stale in a skill body.
- **Negative:** descriptions are read at catalogue time, not at call time. Part 4 mitigates this by
  making the read a deliberate step, but it does not eliminate it — the channel's nature is what it
  is. **This is the honest weakness**, and the falsifier below is written to expose it rather than
  argue around it.
- **Negative:** every description grows. Bounded by the budget gate; the headroom is real (521B used
  of ~1,800).
- **Neutral:** existing orphaned drawers are untouched — a separate deferred backfill with a receipt
  in `BACKLOG.md`.

## Out of Scope

- The `edge` argument and the result `hint` — both named in Alternatives, both deliberately not here.
- Backfilling existing orphans — deferred, receipt in `BACKLOG.md`.
- Pairings for tools other than `am_add_drawer` — the mechanism is general and the gate will demand
  them for writes; the wording of each is not decided in this ADR.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| **The clause ships and adoption does not move** — a description is read once, far from the call | **Med-High. This is the real risk and ADR-017 measured its shape** | Med | Part 4's routing line is the first mitigation: it makes reading the description a step rather than an accident. The falsifier below then measures it against a known baseline (0 of 1,985 authored). Below a moved rate, escalate to the `edge` argument, which is recorded in Alternatives so escalation is a decision already half-made. |
| **The routing line lands in one skill and not the others**, so sessions keep teaching the old route | **Med — this repository has already done exactly this** | Med | Part 4 requires every document that teaches tool usage to carry it; the follow-up names the grep. A measurement that loads only the patched document proves nothing. |
| **Pairings accrete into a protocol nobody reads** | High | Med | The budget gate, plus the requirement that a pairing name **one** other tool and say what breaks without it. |
| **The exemption list becomes where tools go to avoid the rule** | Med | Med | An exemption needs a written reason; the reason is the review. Same shape as `TestNotOperatorFacingIsJustified`. |
| **A gate passes on catalogue metadata never published on the wire** | Med | High | `liveSurface` exists precisely to prevent this and is reused rather than re-implemented. |

## Rollback

Additive and text-only. Drop the declarations and the composition step and `tools/list` returns
today's descriptions; drop the gates and CI returns to today's checks. Nothing is stored, migrated,
or re-shaped, and no client that ignored the clause behaves differently.

## What would falsify this

**The share of new drawers whose edge is AUTHORED rather than derived must move.** The baseline is
recorded in the code: 0 of 1,985 drawers named as a triple object (`palace/kg.go:1058`, 2026-08-26).
Measure `edge_derived == false` as a fraction of writes over a fixed window after the clause ships,
against the same palace.

If it does not move, **the schema channel does not carry obligations**, and this ADR has put a true
sentence somewhere nobody acts on it. The honest response is to say so here and escalate to the
`edge` argument — not to keep the clause and call the question answered.

## Follow-ups / still undecided

- [ ] **Is the ADR-021 narrowing the reviewer's intent**, or should that ADR be amended directly?
- [ ] **Do reads need pairings too?** The gate requires them of writes only; `am_search` → `am_status`
      (establish your scope) is a real candidate that would widen the rule.
- [ ] **Where does an ARGUMENT-level rule get gated?** The division-of-labour table puts the
      full-length-id trap in `am_kg_add`'s argument description; no gate covers argument text today.
- [ ] **Which documents carry part 4's routing line?** At minimum grep `start-here`,
      `memory-orchestration`, `human-decisions`, the `am_skillset` preamble and `AGENTS.md` before
      calling it landed — and note that the preamble is an unpinned row, so a line added there alone
      is restored by no seed.
- [ ] Report the measured before/after from the falsifier, whichever way it falls.
