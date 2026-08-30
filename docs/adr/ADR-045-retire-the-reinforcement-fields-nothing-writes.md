# ADR-045: Retire the reinforcement fields nothing writes

**Status:** Proposed
**Date:** 2026-08-30
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-016 (governs `internal/mcpserver/graph.go` and found the derived graph structurally empty), ADR-014 (a shipped default must be the measured one), ADR-010 (a memory ends because something ended it)
**Governs:** internal/mcpserver/graph.go
**Enforced-by:** `internal/mcpserver/dynamicswire_test.go::TestNoUnwrittenDynamicsFieldReachesTheWire`
**Invalidates:** none — checked
**Served-path change:** `am_create_tunnel`, `am_list_tunnels` and `am_list_hallways` stop returning `strength`, `stability`, `last_activated` and `access_count`, so an agent reading a result no longer sees four fields describing a reinforcement layer this server does not implement. Three tool responses change, and `am_find_tunnels` is **not** one of them: its handler returns raw rooms (`registerFindTunnels`, which answers `{"rooms": ...}` rather than a `tunnelView`) and it never carried these fields.

## Context

Four fields are published on every hallway and tunnel result and none of them has ever moved off the value it was stamped with at creation. `initDynamics` at `internal/palace/graph.go:27` is the only writer in the tree; `internal/palace/hallway.go:143` already records in a doc comment that nothing has ever written `LastActivated` after that stamp. Measured 2026-08-25 across the palace, `access_count > 0` held for 0 of 1,338 hallways, and re-listing a tunnel immediately after following it left `access_count: 0` and `last_activated == created_at` unchanged — recorded in `docs/adr/BACKLOG.md` under *"Tunnel activation is never recorded"*.

Issue #41 asked which of four candidate semantics an "activation" should have: a traverse step, a search returning both entities, a list call, or nothing — retire the fields. The owner answered on 2026-08-28 while reviewing upstream mempalace: strength as an accessed-or-verified signal is acceptable, strength as a decaying function of elapsed time is rejected, and the inert fields are not to be wired up. Two of the recorded arguments rule out the first three candidates as a class rather than one at a time. Reinforcement by access makes recall self-confirming — read, rank higher, read again — and the record with the fewest reads is very often the correction nobody has found yet, which is the one that most needs to outrank the drawer it corrects. And the constants that would drive potentiation come from human psychology rather than from any measurement on an agent corpus, so wiring them would ship an unmeasured default, which ADR-014 forbids.

So the decision this record carries has been taken. What is missing is the removal: the fields are still on the wire, still advertising a capability that does not exist, which is this repository's signature defect read in the other direction — not a capability that is finished and unreachable, but a wire surface that is reachable and backed by nothing.

## Existing Primitives Audit

- **`internal/mcpserver/wirekeys_test.go`** — already carries the judgement this record acts on. Its `undescribedOnPurpose` map excuses `last_activated` from needing a description, with a reason naming issue #38 and stating that describing it *"would promise reinforcement this store does not implement"*. Reused, not reshaped: removing the field makes that entry dead, and the sibling `TestUndescribedOnPurposeIsJustified` at `:183` refuses an entry excusing a field the package no longer emits. The existing gate therefore forces the exemption to be deleted in the same change.
- **The wire-view indirection** — `tunnelView` and `hallwayView` exist precisely so the JSON shape can differ from the domain type. Reused as-is: this record changes the views and leaves `palace.Dynamics` alone.
- **`internal/wingbundle`** — declares its own record types with their own tags and never carries these four fields, so the export format needs no change. Checked by reading `wingbundle.go`, not assumed.

## Decision

Remove `strength`, `stability`, `last_activated` and `access_count` from `tunnelView` and `hallwayView` in `internal/mcpserver/graph.go`, and delete the two assignments that populate them. Delete the now-dead `undescribedOnPurpose["last_activated"]` entry. Add a gate that derives its universe from `palace.Dynamics`'s own json tags and fails when any of them reappears **as a json tag declared in `internal/mcpserver`**, so a field added to the dynamics layer tomorrow joins the check on the same commit rather than needing anyone to remember this record.

That scope is the gate's real one and is stated narrowly on purpose. `packageSources` globs this package's own `*.go` and the matcher reads `json:"..."` tags, so three routes put a key back on the wire with the gate green: a handler returning `palace.Tunnel` directly, a view embedding it, and a hand-built `map[string]any{"strength": ...}` that has no tag at all. This is the same blind spot `wirekeys_test.go` documents against itself — its first name overclaimed for exactly this reason and was narrowed to "in this package" — and a gate whose name claims more than it covers is worse than a narrower one.

The database columns, `palace.Dynamics`, and `initDynamics` all stay. This is deliberate and is the narrow reading of the owner's ruling: the wire is what advertises the capability, and `LastActivated` still has one internal reader — `internal/palace/hallway.go:109` uses it as a fallback input to `earliestStamp` when preserving a hallway's `created_at` across a rebuild, which is the #38 stamp repair. Removing the field from the type would rework a repair that is working.

**What would make this decision wrong, stated so it is falsifiable:** a measurement on a real palace showing that a reinforcement signal improves retrieval of the *right* record. The owner's ruling says so explicitly and marks itself open to reversal on that evidence. No such measurement exists today and none is proposed here. The criterion is valid for this corpus and this ranking configuration, not in the abstract.

## Alternatives Considered

- **Wire the fields up — pick one of issue #41's three activation semantics.** Rejected because the owner ruled against it on 2026-08-28, and because reinforcement by access is self-confirming: the least-read record is disproportionately the correction nobody has found, and ranking it lower is the opposite of what the palace needs. ADR-014 independently rejects shipping the Ebbinghaus constants, which have never been measured on an agent corpus.
- **Leave the fields and document them as inert.** Rejected: a description saying "this is always 1.0" is a promise with nothing behind it, and `undescribedOnPurpose`'s existing entry already reached the opposite conclusion for `last_activated` — describing it *"would promise reinforcement this store does not implement"*. Documenting the other three would contradict a judgement already recorded in the tree.
- **Remove the domain type and the columns as well.** Rejected for this record, not forever: `LastActivated` has a live internal reader in the hallway stamp repair, and a SQLite column drop is destructive with no path back for data. Deferred rather than refused.
- **Keep `strength` because the ruling allows an accessed-or-verified signal.** Rejected: the ruling permits that signal, it does not implement it, and `strength` is inert at 1.0 today. Shipping it back when it means something is cheaper than leaving a field on the wire that reads as meaningful and is not.

## Component / Boundary Impact

None — internal to `internal/mcpserver`. Ownership is unchanged: the view structs already existed to decouple the wire shape from `palace.Hallway` and `palace.Tunnel`, and this record uses that seam rather than adding one.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_list_hallways` response objects | remove `strength`, `stability`, `last_activated`, `access_count` | `hallwayView` in `internal/mcpserver/graph.go` | any MCP client reading hallways |
| `am_create_tunnel` / `am_list_tunnels` response objects | remove `strength`, `stability`, `last_activated`, `access_count` | `tunnelView` in `internal/mcpserver/graph.go` | any MCP client reading tunnels |
| `undescribedOnPurpose` allowlist | delete the `last_activated` entry | `internal/mcpserver/wirekeys_test.go` | `TestUndescribedOnPurposeIsJustified` |

## Inter-task Contracts

None — single task.

## Implementation

One task; see `ADR-045-retire-the-reinforcement-fields-nothing-writes/tasks/README.md`.

## Consequences

- **Positive:** four fields that described a behaviour the server does not have stop being published, so an agent can no longer read a reinforcement signal into `access_count: 0`. The new gate makes the absence durable rather than a fact about today's tree.
- **Negative:** a breaking change to three published tool responses. A client parsing these keys into a non-pointer required field will fail to decode. No such client is known in this workspace, and the values it would lose are constants.
- **Neutral:** storage is untouched. The columns keep their `NOT NULL` defaults and a future record that revives a verified-access signal can use them without a migration.

## Out of Scope

- `palace.Dynamics` and its json tags, still read internally by the hallway stamp repair (permanent: this record retires a wire surface, not an internal type)
- The four columns on `hallways` and `tunnels`, which keep their NOT NULL defaults (deferred: docs/adr/BACKLOG.md)
- Entity extraction quality, the second half of the issue whose first half this closes (deferred: issue #41)
- Any reinforcement, potentiation or decay mechanism (permanent: the owner ruled on 2026-08-28 that time-based decay is rejected here and that the inert fields are not to be wired up)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| An unknown client parses these keys as required | Low | Med | The values are constants, so a client reading them learns nothing, and the Served-path change line above names the three responses that change. ⚠ A release note is **owed, not written**: `CHANGELOG.md` is authored at release time in its own `docs(changelog):` commit, never in the PR that does the work, so this record cannot ship one — the obligation is carried as a Follow-up rather than claimed here |
| The gate hardcodes the four names and rots when a fifth is added | Low | Med | It derives its universe from `palace.Dynamics`'s own json tags rather than a literal list, so a new dynamics field joins the check on the same commit |
| The gate passes vacuously because it read no sources | Low | High | It fails when the package glob returns nothing, the same guard `packageSources` already uses at `wirekeys_test.go:208` |
| Removing the field leaves a dead exemption nobody notices | Low | Low | It cannot: `TestUndescribedOnPurposeIsJustified` at `wirekeys_test.go:183` fails on an entry excusing a field the package no longer emits |

## Rollback

Revert the commit. No migration runs, no persistent state changes, and the columns and domain type are untouched, so a revert restores the previous wire shape exactly with no data step.

## Follow-ups

- [ ] Name the removal in the release note for whichever version carries it: three tool responses (`am_create_tunnel`, `am_list_tunnels`, `am_list_hallways`) lose four keys (`strength`, `stability`, `last_activated`, `access_count`). This is the Risks table's mitigation and the only part of it that does not exist yet — `CHANGELOG.md` is written at release time, so the obligation is here rather than in this PR's diff.
- [ ] Decide whether the four columns should be dropped, once something either revives a verified-access signal or confirms nothing will — recorded in `docs/adr/BACKLOG.md` under this record's name.
