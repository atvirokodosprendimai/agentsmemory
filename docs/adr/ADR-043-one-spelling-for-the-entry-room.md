# ADR-043: One spelling for the entry room, and a tier the entry point actually reaches

**Status:** Proposed
**Date:** 2026-08-28
**Owner:** unassigned
**Spec:** None — no spec stage. `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` names this decision in its Non-Goals ("Deciding which entry-point layer is canonical … an ADR-level decision") and proceeds independently of it; this record is that decision, not an implementation of that spec.
**Cross-references:** `docs/adr/BACKLOG.md` (§"Four spellings of one entry point, and the served document teaches a fifth"), `docs/adr/ADR-027-a-maintained-document-is-a-set-of-records.md`, `docs/adr/ADR-036-a-recall-that-answers.md` (T7, T8), `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md`, `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md`, `internal/palace/graphquery.go`, `internal/palace/bootstrap.go`, `internal/web/bootstrap-memory.md`, `AGENTS.md`, `README.md`, `model/draf1.md`, and — in flight and unmerged, so named by PR rather than by number — PR #75 (a store you address by name), PR #77 (the schema carries the pairing), PR #79 (the seeded playbook routes to an entry protocol)
**Governs:**
- type: path
  pattern: "internal/web/bootstrap-memory.md"
- type: path
  pattern: "internal/repohygiene/entryroom_test.go"

<!-- Class: every artifact that TEACHES or RESOLVES the room a wing's entry point lives in — as
opposed to one that merely records what was once decided about it. Enumerated 2026-08-28 with
`grep -rln "llm_init\|llm_index\|EntryRoom" --include="*.go" --include="*.md" . | grep -v '^./.git' | grep -v _test.go`
→ **16 files at this record's own post-image**, and 11 before it — the four files this record ADDS
match their own enumeration, which a receipt taken against the pre-image silently misses. The
classification below is over the 11 pre-existing ones; ADR-043's own files are excluded as the
record doing the enumerating. Five teach or resolve: `AGENTS.md`, `README.md`, `internal/web/bootstrap-memory.md`,
`model/draf1.md`, `internal/palace/graphquery.go`. Six are historical records and are deliberately
NOT members — `CHANGELOG.md`, `docs/adr/BACKLOG.md`, and the four ADR files
(ADR-026, ADR-027, ADR-036/T7, ADR-038): rewriting a record to agree with a later decision is the
evidence-chain edit this corpus exists to prevent. Of the five members,
`internal/web/bootstrap-memory.md` and `README.md:167` are the two T1 WILL CHANGE — neither is in
this diff, and an earlier draft said "CHANGED here", which is a claim a reader checks against the
diff and finds false; `AGENTS.md`,
`model/draf1.md` and `graphquery.go` already say what this decision decides, which is the largest
single argument for the direction taken. Five test files also name the room
(`internal/mcpserver/kgquery_test.go`, `internal/mcptest/registry_test.go`,
`internal/palace/currentonly_test.go`, `internal/palace/recallanswers_spec_test.go`,
`internal/palace/targetauth_test.go`); they consume `palace.EntryRoom` or a fixture and need no edit
because the constant does not change. -->

**Enforced-by:** None — no gate exists at authoring time, and naming one that does not resolve is the rot this header exists to prevent. T1 produces `internal/repohygiene/entryroom_test.go::TestTheServedDocumentTeachesTheRoomTheCodeResolves`, whose universe is the two real artifacts (the constant parsed from `internal/palace/graphquery.go`, the room names read from the served document) rather than a list kept beside them; this header is updated to name it when T1 lands.
**Invalidates:** **ADR-036 T8's scoping, narrowly and deliberately.** T8 put the `must.*` / `ref.*` vocabulary explicitly out of scope for `Bootstrap`, which was correct for T8's goal (replace a 13-call client protocol with one call) and is not correct once the entry room is populated by backfill: a containment edge alone makes `am_entry_point` answer `matched` while returning only the root room's own drawers. ⚠ **AND "SCOPING" UNDERSTATES IT — T8 MARKED THAT BOUNDARY `permanent`, NOT `deferred`.** Verified at `docs/adr/ADR-036-a-recall-that-answers/tasks/T8-the-protocol-becomes-an-api.md:135`, whose Out of Scope reads: *"Defining `must.*`/`ref.*` as server vocabulary (permanent: the server distinguishes eager from on-demand; the names are a team convention.)"* A `permanent` disposition means the boundary dies there by design, so T2 is not a widening — it OVERTURNS one, and a record that calls that "amending a scope" is hiding the size of its own claim. **The reason the boundary no longer holds is that its premise did:** T8 could treat the names as a team convention because the server's own eager/on-demand split was doing the work. It is not — `am_bootstrap` returns `unknown_term` on every wing of this palace, so there is no split to rely on, and the only thing that partitions the tier is the `must.*` convention T8 declined to read. If T3's hosted read finds a working entry point elsewhere, that premise is restored and T2 should be reconsidered rather than shipped. T2 amends nothing else in ADR-036; ADR-036 remains authoritative over the entry-point API, and this record does not re-decide T7's derived-edge design. Otherwise: ADR-027 is Accepted and this record USES its model (an `llm_init` root spine, an `llm_index` routing drawer) rather than changing it. ADR-038 is untouched — no id is recomputed here. Checked by grepping every record in `docs/adr` for `llm_init`, `llm_index` and `EntryRoom` — 4 records, all listed above. ⚠ **THAT GREP IS INCOMPLETE BY CONSTRUCTION AND THE FIRST VERSION OF THIS HEADER DID NOT SAY SO.** Two proposed records exist only on unmerged branches and are invisible to any search of this tree: the record on PR #75 (*"a store you address by name"*, changes-requested) and the record on PR #77 (*"the schema carries the pairing"*, draft). The first decides the same question this one does and is now argued in Alternatives; the second is adjacent. Their numbers are deliberately not written here — they do not exist on `main`, and `TestEveryCitedADRResolvesInDocsToo` correctly refuses a citation to a record that cannot be opened. A reviewer checking this header should re-run the grep against open PRs, not only the tree.
**Served-path change:** An agent that calls `am_bootstrap` or `am_entry_point` on `wing_agentmemories` gets its mandatory tier instead of `unknown_term`, and a new agent reading `/bootstrap-memory` is taught the room the server actually resolves instead of one it does not.

## Context

`BACKLOG.md` §"Four spellings of one entry point" records four layers that all claim to be the
entry point and leaves the choice open, correctly, as a product decision. This record makes it.

Measured 2026-08-28 against the palace this repository's sessions actually use
(`http://localhost:8080/mcp`, `mode: local`, workspace slug `local`, 2,153 drawers):

- `am_list_drawers(wing: "*", room: "llm_init", include_history: true)` → **0**. Not one drawer in
  any wing, and not one ended drawer either: this palace has never held the room the code resolves.
  That is the second independent read of the same fact — `BACKLOG.md` records
  `am_kg_query(entity: "room:wing_agentmemories/llm_init", status: "all")` returning `unknown_term`
  on 2026-08-28, and the two derivations agree.
- `am_list_drawers(wing_agentmemories, room: "llm_index")` → **2 drawers**, whose `source_file`
  values cite `setup.md §4.3` and `setup.md §6` — the served onboarding document, which was
  `setup.md` until commit `bd611a3` and is now `internal/web/bootstrap-memory.md`.
- `am_kg_query(entity: "must", direction: "outgoing")` → **8 facts, `resolution: "matched"`**, whose
  objects are LABELS (`llm_index`, `llm_index_keys`, `llm_open_threads`, `llm_corrections`,
  `human_decisions`, `effective-go`, `memory-orchestration`, `human-decisions`) and not drawer ids.

Counts over the artifacts, same day: `internal/web/bootstrap-memory.md` says `llm_index` 15 times
and `llm_init` 0; `AGENTS.md` says `llm_init` 3 and `llm_index` 0; `model/draf1.md` says `llm_init`
8 and `llm_index` 5; `internal/palace/graphquery.go:471` declares `const EntryRoom = "llm_init"`.

**Two consequences follow that nothing currently reports.**

1. **`AGENTS.md`'s documented traversal is unexecutable against this palace today.** It instructs a
   session to run `am_list_drawers(wing:"wing_agentmemories", room:"llm_init")` with the comment
   `# several drawers; see below`, and then teaches that zero edges from the wrong drawer must be
   read as a failed query. Against this palace the first call returns zero drawers, so the protocol
   ends before the step that would tell you it had.
2. **`README.md:167` teaches a false diagnosis.** It explains `am_bootstrap`'s `unknown_term` as
   happening "on a wing whose `llm_init` drawers were filed before the derived room edges shipped".
   There are no such drawers here to be un-backfilled. The stated cause cannot be this palace's
   cause, so an operator who reads it goes looking for a backfill that would not help.

**One conflict is named rather than resolved here.**
`docs/adr/ADR-036-a-recall-that-answers/tasks/T7-a-wing-names-its-entry.md:27` records a
verification taken 2026-08-26 "from the `wing_agentmemories` `llm_init` root (25 nodes, all hop
<=1)". That cannot be this palace, which has never held the room. It was the hosted deployment or a
fixture. Which one decides whether adopting `llm_init` strands an existing corpus or none at all, so
T3 verifies it against the hosted palace **before** any other task spends effort, and records the answer
whichever way it falls. This record is written for the direction the evidence supports and names the
observation that could refute the cheap version of it.

## Existing Primitives Audit

- **`palace.EntryRoom` + `EntryPoint` (`internal/palace/graphquery.go:471`, `:509`)** — reuse
  unchanged. The constant already names the room this record makes canonical; nothing about it is
  wrong, which is why no code constant moves.
- **`Bootstrap` (`internal/palace/bootstrap.go`)** — reshape. It takes outgoing edges from the
  derived containment node and never examines `must.*` or `ref.*` (ADR-036 T8, deliberate). T2
  extends it to follow the mandatory tier, because a containment edge alone is the false-reachability
  trap.
- **The `must.*` protocol** — reshape, not replace. It exists in prose only: `must.*` appears in no
  Go source, and nothing in the tree produces or consumes it. T2 gives it a consumer; T4 gives this
  corpus a producer's output in the canonical shape (drawer ids, not labels).
- **`internal/repohygiene`** — reuse. This is where the tree's artifact-agreement gates already live
  (`TestEveryCitedADRResolves`, `TestAgentsMdNamesGatesThatExist`, `TestAHumanObservedSignOffAgreesWithTheIndex`),
  and T1's gate is the same shape: a universe derived from two real artifacts rather than a list.
- **`am_merge_wing` / `am_update_drawer` relocation** — reuse for T4. A memory created at or under
  1,600 runes stays one row and can be relocated for life; both `llm_index` drawers are under that
  ceiling, so no re-chunk is needed.

## Decision

**`llm_init` is the canonical entry room.** The served onboarding document
`internal/web/bootstrap-memory.md` is the outlier and is corrected: §4.3 seeds an `llm_init` root
drawer whose content opens `WHAT MUST I LOAD AT THE START OF A SESSION?`, plus `must.*` knowledge-graph
edges from that root's own drawer id to the drawer ids of the mandatory tier. `llm_index` keeps
exactly the job ADR-027 already gives it — a routing drawer, "which room answers which question" —
and is reached as one of the root's `must.*` targets rather than instead of the root.

**And a resolving entry point must reach the mandatory tier, not merely the root room.** A backfill
that writes derived containment edges alone makes `am_entry_point` answer `matched` while returning
only the root room's own drawers; that is a worse state than `unknown_term`, because the caller has
no way to tell a complete answer from a truncated one. T2 makes `Bootstrap` follow `must.*` targets
into other rooms, and T1's gate covers the artifact half.

**What would make this decision fail, and whether data that could produce that failure exists
today.** The direction rests on `llm_init` being empty everywhere, so that adopting it strands no
corpus. That is measured on the local palace (0 drawers, 0 ended, all wings, 2026-08-28) and is
**not** measured on the hosted one, where ADR-036 T7 recorded a 25-node root on 2026-08-26. T3's
first ordered step is to read the hosted palace. **If the hosted palace holds an `llm_init` corpus in
the canonical root-id → `must.*` → drawer-id shape, this decision is confirmed and T4's seeding is
only the local palace's catch-up. If it holds one in the LABEL shape this corpus uses, the two
deployments have diverged and T3 stops the record for the owner rather than releasing T4.** The criterion is
falsifiable because the data that would falsify it is one call away and has not been made; T3 is
`Data dependency: needs a live hosted palace` for exactly this reason.

This decision is valid for the two deployments named — the local self-hosted server and the hosted
SaaS workspace. It says nothing about a third-party palace built from an older served document; those
are covered by the served document's correction going forward, not retroactively.

## Alternatives Considered

- **Adopt `llm_index` as the entry room** (change `EntryRoom`, `AGENTS.md`, `model/draf1.md`, and
  ADR-027's model to match the served document and this corpus). Rejected because it is the larger
  edit for the smaller gain: it changes four artifacts to preserve two drawers, and it does not
  actually make the entry point resolve — `llm_index` has no root drawer and no `must.*` edges from a
  drawer id either, so it would need the same T2/T4 work under a different name. The two drawers it
  would preserve cost one relocation to move.
- **Backfill derived containment edges for the existing rooms and change nothing else.** Rejected
  explicitly, and named here so it is not re-proposed: it is the cheapest-looking fix and it produces
  FALSE reachability. `am_entry_point` would answer `matched` while returning only the root room's own
  drawers, never the mandatory tier the manual protocol traverses — this repository's characteristic
  defect, delivered by the fix for it.
- **Replace derived addressing entirely with a store you address by NAME** — the record proposed on
  PR #75 (unmerged, changes-requested 2026-08-27), which gives the palace flat-dotted chosen keys so
  `read("root")` and `read("must.*")` work without any of this. **Not rejected — SEQUENCED, and this
  is the alternative a reviewer will reach for first.** ⚠ **One of the three reasons first given here was
  FALSE and is withdrawn rather than repaired.** It claimed that record's falsifier is downstream of
  this one, because "F-16 cannot be checked while the entry point returns `unknown_term`". F-16 has a
  committed test that needs no live wing at all —
  `internal/palace/recallanswers_spec_test.go:847`, `TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces`,
  which builds its own fixture wing through `svc.Add`. The claim came from my own review of that PR
  and was wrong there too. **Withdrawing it weakens the sequencing case, which is why it is withdrawn
  in place rather than quietly replaced.** Two reasons survive, and they are about cost rather than
  order. (b) It needs a new MCP tool and a new table; this
  record needs no new surface at all and changes one served document plus two drawers. (c) They
  compose rather than collide — if name-addressing lands, the `must.*` edges T4 writes become one
  client of it rather than dead weight, because the tier is the same tier either way. **What would
  make this record the wrong one:** if name-addressing ships first, T1's document correction should
  teach `read("root")` instead of a seeded room, and T4's seeding is wasted work. That is a
  sequencing decision across two authors and it belongs to the owner, not to this record.
- **Route the seeded playbook to a `start-here` skill** — proposed on PR #79 (unmerged). Rejected as
  the canonical entry point on the measurement this repository already has: a skill is prose, ADR-017
  measured the full protocol producing **0 recalls in 5 dispatches** against **5** for one short
  paragraph, and ADR-041 F-8 rejects prose as a mechanism outright. It would also be a fifth spelling
  beside the four this record exists to collapse. ⚠ **But it fixes a real and DIFFERENT defect that
  this record does not touch**, and rejecting it as the entry point is not rejecting the PR: a fresh
  or restored database gets a seeded playbook that names no entry protocol at all, which is a silent
  failure on every self-hosted install. The two are compatible — that PR makes the playbook name a
  way in, this record makes the way in resolve.
- **Declare the served document canonical for onboarding and the code canonical for the API, and
  document the split.** Rejected because it is the current state, written down. A new agent would keep
  building corpora the server cannot resolve, and the split is invisible from either side.
- **Withdraw the entry-point surface entirely and rely on `am_search("what should I load next")`**,
  which is what the served document's §4.3 actually teaches (a search hop to a routing drawer at rank
  1). Rejected on measurement rather than taste: `am_search` ran 52 times in 8,256 tool calls in
  session `ee8f1fc1`, and a hop that depends on an agent thinking to ask a particular question is the
  failure mode `am_entry_point` exists to remove.

## Component / Boundary Impact

| Component | Ownership after change | One reason to change? |
|-----------|------------------------|-----------------------|
| `internal/web` (served onboarding document) | Unchanged — web owns the document; this record is authoritative over the room it teaches | Yes — it changes when the onboarding protocol changes |
| `internal/palace` (entry point + bootstrap) | Unchanged — ADR-036 remains authoritative over the API; T2 amends only its `must.*` scoping | Yes |
| `internal/repohygiene` (gates) | Gains one gate, same shape as its siblings | Yes |
| The palace corpus (data, not code) | Owned by the operator; T4 is a migration, not a code change | n/a — not a code component, which is why T3 and T4 are human-observed |

No module is added, moved or renamed, so `docs/architecture.md`'s Module Map is unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `internal/web/bootstrap-memory.md` §4.3 (the seeding procedure a new agent follows) | modify — seeds an `llm_init` root drawer and `must.*` edges to drawer ids; `llm_index` becomes one of its targets | T1 | every agent that reads `/bootstrap-memory`; every corpus built from it |
| `README.md:167` (`am_bootstrap`'s documented `unknown_term` cause) | modify — the cause is a wing with no root drawer, not un-backfilled edges | T1 | operators diagnosing a bootstrap that returns nothing |
| `Bootstrap`'s returned tier (`internal/palace/bootstrap.go`) | modify — follows `must.*` targets into other rooms; additive to the existing response shape | T2 | `am_bootstrap` callers; `AGENTS.md`'s one-call path |
| `palace.EntryRoom` | retain — unchanged at `"llm_init"`, and the gate in T1 reads it rather than restating it | none | T1's gate; five existing test files |
| The `wing_agentmemories` corpus (`llm_init` root drawer + `must.*` edges) | add | T4 | every session in this repository |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `entryRoomDisagreements()` — the detection function T1's gate and its falsifiability subtest both drive | T1 | none | No — internal to T1; it is listed because a falsifiability half that shares nothing with the gate pins nothing |
| `Bootstrap` returning `must.*` targets outside the root room | T2 | T4 | No — additive; a wing with no `must.*` edges is unchanged |
| The hosted classification — `canonical` \| `label` \| `nothing` | T3 | T4 | No — T3 writes nothing anywhere; it records what it read, and `label` stops the record |
| The seeded `llm_init` root drawer id and its `must.*` facts | T4 | none | No — T4 is last |

## Implementation

See `tasks/README.md`. Four tasks: T3 the hosted read that can stop the record and therefore runs FIRST, T1 the gate and
the document correction it makes green, T2 the mandatory-tier reachability, T4 the corpus seeding.
⚠ The ids are not in execution order and that is deliberate — see `tasks/README.md`; renumbering a
task breaks every reference to it, and `Depends-on` is the source of truth.

## Consequences

- **Positive:** `am_bootstrap` and `am_entry_point` answer on this repository's own wing, which is
  the surface ADR-036 T8 built and which has never once worked here. `AGENTS.md`'s traversal becomes
  executable without editing `AGENTS.md`. The four spellings become one, and the three artifacts that
  already agreed (`AGENTS.md`, `model/draf1.md`, `graphquery.go`) are not touched.
- **Positive, and ESTIMATED in the currency an agent actually spends** — BPE arithmetic at ±20%, not a counter reading, because nothing reports a turn's output-token count back to the model that emitted it; the ORDERING is the claim: the manual traversal makes a session EMIT drawer ids, and a 64-character hex id BPEs at roughly two characters per token — about 30 output tokens each, so a five-item `must.*` tier costs ~150 output tokens in ids alone before a single one is fetched. Worse, the traversal asks the session to CHOOSE which edges to follow, and deliberating that costs 500-1,500 output tokens against ~225 to fetch five outright. `Bootstrap` returning the tier inline removes both: no ids are emitted, and a decision becomes a lookup. Estimated 2026-08-28. This is a second and independent argument for the same code — the record was written on reachability alone, and the cost argument arrives at the same line. ⚠ Shortening the id is NOT the alternative: ADR-038 made it opaque deliberately and `TestNoPathRederivesADrawerID` guards that, because a content-derived key put two identical journal entries in one row and reported two.
- **Negative:** T2 amends a scoping ADR-036 T8 chose deliberately, which is real scope in the palace
  package rather than a documentation edit. And the decision is taken on a measurement of one
  deployment; T3 can stop it, and now runs before T1 and T2 rather than after them — an earlier draft had the hosted read as step 1 of the last task, so two thirds of the work would have landed before the stopping condition fired.
- **Neutral:** the two existing `llm_index` drawers keep their content and their ids — they are
  relocated under the root, not rewritten, so nothing in ADR-038's identity model is disturbed.
- **Neutral:** the label-shaped `must` facts in this corpus (`must → must_load → "llm_index"`) are
  superseded by drawer-id-shaped ones rather than deleted, so the label protocol stays readable as
  history.

## Out of Scope

- Reconciling `model/draf1.md`'s mixed usage — `llm_init` 8, `llm_index` 5 — into one vocabulary (permanent: after this record the two words name two different things, an entry room and a routing drawer, so a document that describes both correctly uses both)
- Rewriting `CHANGELOG.md`, `docs/adr/BACKLOG.md`, ADR-026, ADR-027, ADR-036/T7 or ADR-038 to agree with this decision (permanent: they are historical records, and editing one to match a later decision is the evidence-chain edit this corpus exists to prevent — BACKLOG.md gains a new dated entry instead)
- Backfilling derived containment edges for corpora on deployments other than the local and hosted ones T3 and T4 name (deferred: `docs/adr/BACKLOG.md` §"Four spellings of one entry point" — entry written in the same commit as this record, naming ADR-043)
- Any change to how `am_search` ranks the routing drawer, or to read cost generally (permanent: `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` owns read cost and names this decision in its own Non-Goals, so the two proceed independently by mutual agreement of both documents)
- Deciding whether `ref.*` edges join the tier `Bootstrap` follows (deferred: Follow-ups, ADR-043 — T2 covers `must.*` only, because `ref.*` is on-demand by the manual protocol's own design and making it eager would reintroduce the response-size problem ADR-036 T8 measured)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The hosted palace holds an `llm_init` corpus in the label shape, so the two deployments have diverged | Med | High | T3 reads it, writes nothing, and STOPS the record for the owner rather than releasing T4; the falsifier is its own dependency-free task rather than a step inside the last one |
| T2's change to `Bootstrap` inflates the response past the budget ADR-036 T8 measured | Med | Med | `must.*` only, `ref.*` stays on demand; T2's Acceptance asserts the truncation report is populated rather than the tier being silently cut |
| A backfill is applied instead of T4's seeding, producing false reachability | Med | High | Named as a rejected Alternative and forbidden in T4's Stop Condition; T2's test fails when the tier is unreachable, so the cheap fix cannot pass the gate |
| T1's gate is written to match the document rather than the constant, so correcting the document is not what turns it green | Med | Med | The gate's universe is `palace.EntryRoom` parsed from source; T1's mutation is changing the constant and watching the gate follow it |
| **Drawer-id `must.*` edges go stale on the next correction of any target, and nothing notices** | **High** | **Med** | ⚠ Observed 2026-08-29, not hypothetical. ADR-038 mints an id once and never recomputes it — but `am_update_drawer` with content does not EDIT a row, it writes a new one and ends the old, so every correction mints a new id and silently invalidates every pointer to the previous one. The `llm_index` key-list drawer carried a two-generations-stale pointer to `llm_open_threads` and nothing reported it, because a retired row reads exactly like a live one unless the caller passes `include_history` and checks `valid_to`. **This is the strongest surviving argument FOR the label scheme this record replaces:** `must` → `must_load` → *label* survives a correction because the label is resolved through a drawer, and the indirection absorbs the churn. T4 writes `<root id>` → `must.*` → `<drawer id>`, which does not. Not mitigated here, and named rather than argued away — the honest options are a gate that resolves every `must.*` object and fails on an ended one, or keeping the label indirection and giving it a resolver. Both are larger than this record, and PR #75's name-addressing is a third answer to the same problem, which strengthens its case rather than this one's |
| ADR-036 T7's 25-node claim is simply wrong, and there is no hosted corpus either | Low | Low | T3 records the answer whichever way it falls; a refutation makes the migration smaller, not larger |

## Rollback

Persistent state changes in T4 only; T3 writes nothing anywhere. T4 creates exactly two kinds of
row and the rollback is over those, not over an earlier draft's imagined relocation:

- the `llm_init` root drawer → `am_invalidate_drawer` with a reason. It is not erased; it stops being
  current and stays readable by its id.
- the `must.*` facts from that root → `am_kg_invalidate`, one per fact, each with a reason.

The label-shaped `must` → `must_load` facts T4 retired are restored by `am_kg_add`, deliberately as an
explicit re-add rather than an automatic reversal: they were invalidated with a stated reason, and a
rollback that silently resurrects them would discard that reason. ⚠ **An earlier version of this
section said the two `llm_index` drawers could be "moved back", which no task moves** — T4 makes them
`must.*` targets and does not relocate them, so there is nothing to undo there.

T1 and T2 are code and documentation, reverted by reverting their commits; nothing they change is
read by a seeding that has already run.

## Follow-ups

- [ ] Report ADR-036 T7's 25-node observation as CONFIRMED (hosted), REFUTED, or FIXTURE, in
      `BACKLOG.md`, whichever way it falls — including "fixture", which would mean this repository has
      never had a working entry point on any deployment and the four spellings were four descriptions
      of nothing.
- [ ] Decide whether `ref.*` joins the tier `Bootstrap` follows, once T2 has a measured response size
      for `must.*` alone.
- [ ] Decide whether the served document should be able to SEED a corpus mechanically rather than by
      instructing an agent to run `am_add_drawer` by hand — the entry point's data has no producer in
      the product, which is the deeper cause of all four spellings and is not fixed here.
