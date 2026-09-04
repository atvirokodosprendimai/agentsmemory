# Task ADR-053-T2: Containment edges are a listing, not a fact

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S (single file plus its test)
**Owner:** unassigned
**Produces:** `isContainmentEdge`, `am_kg_query` `include_containment`
**Consumes:** `withheld` keyed by cause (T1)
**Data dependency:** hermetic for the unit; the wing-root assertion is written against the shipped mint (`attachWingRootEdge`) rather than against a corpus, so it holds on a fresh install
**Proof map:** v1
**Rests-on:** `the exit code`, `every wing root still resolving with containment hidden`, `the flag restoring the hidden edges exactly`

## Goal

Stop 580 room-listing edges crowding out the facts somebody wrote, without
emptying the address every session is told to walk first.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/kg.go` | edit | `isContainmentEdge` and the default filter, plus the `containment` key in the withheld map |
| `internal/mcpserver/kg.go` | edit | declare `include_containment`, and say in the description what is hidden by default and why — a default nobody can discover is a silent answer change |
| `internal/palace/kg_test.go` | edit | the gate, and the wing-root assertion that is the whole reason for the subject-shape rule |

## Ordered Steps

1. [S1] Write `TestEveryWingRootStillResolvesWithContainmentHidden` first: mint a wing root through the shipped path, hide containment, and assert the root still returns its edge. Watch it fail against a naive `derived`-column filter written to fail it (TDD red). ⚠This is the ordering that matters — write the test that catches the wrong rule BEFORE writing the right one, or the right one is a guess that happened to work.
2. [S2] Add `isContainmentEdge`, keyed on the SUBJECT matching the `room:` prefix. Doc-comment it with the measurement: 580 of 586 derived edges are `room:*` listings, the other 6 are the wing-root spine, and keying on `derived` empties 3 of 6 wing roots. Cite ADR-053.
3. [S3] Filter containment edges out of `KGQuery` by default and count them into the withheld map under `containment`, so the page says what it hid rather than presenting itself as the whole.
4. [S4] Add `include_containment` (default `false`) and thread it through. Its description must say what it restores AND name `am_list_drawers` as the tool that answers room membership properly — a caller who wants a room's contents should be sent to the bounded tool, not to a flag.
5. [S5] Assert the flag restores exactly what was hidden: the union of a default page and its hidden count equals the `include_containment: true` result. A flag that returns a different set is not a restoration. [proof: acceptance]
6. [S6] ⚠**The mutant is `isContainmentEdge` re-keyed onto the `derived` column** — the version a later reader will propose, and the one that looks more principled. `TestEveryWingRootStillResolvesWithContainmentHidden` must go red on it, naming the three roots that empty. [proof: mutation]

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ -run 'TestEveryWingRootStillResolvesWithContainmentHidden$|TestAnAbsentEntryPointStillResolvesUnknown$|TestContainmentIsHiddenAndCounted$' -count=1 2>&1 | tee /tmp/adr053-t2a.out \
  && ! grep -qE "no tests to run|^FAIL|^--- FAIL|\[build failed\]" /tmp/adr053-t2a.out \
  && go test ./... -count=1 2>&1 | tee /tmp/adr053-t2b.out \
  && ! grep -qE "^FAIL|^--- FAIL|\[build failed\]" /tmp/adr053-t2b.out
```

The regression half is the WHOLE tree rather than two packages: changing what a
graph query returns by default can break any test that walks the graph, and the
skills and hooks that walk it live outside `internal/palace`.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryWingRootStillResolvesWithContainmentHidden` | `internal/palace/kg_test.go` | a wing root minted by the shipped path still returns its edge when containment is hidden | — | S1, S2 |
| `TestAnAbsentEntryPointStillResolvesUnknown` | `internal/palace/kg_test.go` | a wing with no entry point still resolves `unknown_term` rather than `known_term_no_facts` — the two are identical from a count, and only one of them makes a session fall back | — | S2, S3 |
| `TestContainmentIsHiddenAndCounted` | `internal/palace/kg_test.go` | a `room:*`-subject edge is absent by default, counted under the `containment` key, and returned in full with `include_containment` | — | S3, S4, S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | `KGQuery` calls `isContainmentEdge` on every row; deleting the call turns `TestContainmentIsHiddenAndCounted` red |
| 3 — the caller can discover it | `include_containment`'s description names the default and points at `am_list_drawers`; the withheld `containment` key is what tells a caller the answer was filtered at all |
| 4 — it is used | every `am_kg_query` call filters through it, and 580 live edges are subject to it today |

## Mutation Log

- 2026-09-04 · 8683557* · mutant killed · exit 1 · `internal/palace/kg.go` · the carve-out deleted: the rule then hides listings from a caller who NAMED the room, which is what EntryPoint and Bootstrap do — the wake-up path reports its entry point present and empty rather than absent · acceptance-sha256:de79b55a8a8a271af87e0b684765b68a405741bc02617f3d2e010473e35e928b

## Invariants

- The rule keys on the subject shape. The `derived` column is not consulted, because the wing-root spine is derived too.
- Nothing is deleted or migrated: every containment edge stays queryable behind one flag.
- The hidden count is reported whenever anything was hidden, so a filtered page never presents itself as complete.

## Risks

- A consumer outside this repository depends on containment edges arriving by default. Mitigated by the flag and by the withheld count naming what changed; the description is the only route by which they learn, which is why S4 treats it as part of the change rather than as documentation.
- `room:` as a prefix is a naming convention rather than a typed field, so an authored entity that begins `room:` would be hidden. Accepted and named in the doc comment; the mint is the only writer of that prefix today.

⚠ **`EntryPoint` READS CONTAINMENT EDGES THROUGH `KGQuery`, AND T2 AS WRITTEN
WOULD EMPTY IT.** Found 2026-09-04, after T1 landed and before T2 was executed,
while confirming issue #218. `Service.EntryPoint` (`internal/palace/graphquery.go`)
queries `DerivedEdgeSubject(wing, EntryRoom)` — literally `room:<wing>/llm_init`,
a `room:*` subject — and `Service.Bootstrap` builds its whole answer from those
edges. So the default this task introduces would make `am_entry_point` and
`am_bootstrap` return nothing: the wake-up path every session is told to walk.

This is the SAME failure the subject-shape rule was chosen to avoid, arriving
through the other door. Keying on `derived` would have emptied three wing roots;
keying on subject shape empties the entry room instead, unless the exclusion is
applied where the caller did not ASK for a room. So: **the filter must not apply
when the queried entity is itself the `room:*` node** — asking a room what it
holds is the one question containment edges answer, and it is the question
`EntryPoint` asks. `TestEveryWingRootStillResolvesWithContainmentHidden` must
gain a sibling asserting `am_bootstrap` still resolves, or the gate proves the
narrower half only.

⚠ **AND IT LANDS ONE NOTCH WORSE THAN AN EMPTY ANSWER — the entry point would
report itself as PRESENT AND EMPTY.** Raised in review of #217 and confirmed in
source. `EntryPoint` branches on `KGResolutionUnknownTerm` to keep three states
apart, and its own comment says so: no entry point, an error, and an entry point
that is merely empty. `resolveKGTerms` decides `unknown_term` on whether the
ENTITY NAME exists (`KGEntityNames`), not on whether rows came back — and
`attachDerivedEdge` upserts a `kg_entities` row for the `room:*` subject, so the
node exists whatever is filtered off it. Under a naive hiding the resolution
would therefore be `known_term_no_facts`, `EntryPoint` would fall through to
`out.Node = node`, and a session would read *"this wing's entry point is empty"*.

That is the WORST of the three, not the neutral one. "No entry point" is
recoverable — a session reads it and walks the fallback chain. "Empty entry
point" reads as an answer, and a session acts on it. It is this team's own
`start-here` rule — **an empty-looking room is not evidence of an empty room** —
arriving in the mechanism rather than in a reader's inference.

So the gate asserts BOTH: that a wing WITH an entry point still returns its
edges, and that a wing WITHOUT one still resolves `unknown_term` rather than
`known_term_no_facts`. The two failures are identical from a count, which is
exactly why the second assertion cannot be left to the first.

## Stop Condition

Stop and ask if anything in the tree reads containment edges through
`am_kg_query` rather than through `am_list_drawers`. That would mean the edges
have a real reader and the default should be argued rather than assumed — the
record's premise is that `am_list_drawers` already answers this question with a
budget.

## Out of Scope

- Removing the containment edges or their migration (deferred: `docs/adr/BACKLOG.md`)
- Changing what `am_list_drawers` returns (permanent: boundary: it already answers room membership with a budget and paging, which is the reason this task can hide the edges rather than replace them)

## Verification Log
- 2026-09-04 · 8683557* · exit 0 · `set -o pipefail …` · acceptance-sha256:de79b55a8a8a271af87e0b684765b68a405741bc02617f3d2e010473e35e928b · ms:47043
