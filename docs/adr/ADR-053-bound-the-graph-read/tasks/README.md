# ADR-053 Tasks

Implementation tasks for ADR-053: Bound the graph read, and stop the containment
edges crowding it out. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` /
`Covers` headers. This README is a derived index — when it disagrees with a task
file, the task file wins and the README must be regenerated.

## Execution Order

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1, T3 | none |
| 2 | T2 | T1 |
| 3 | T4 | T1, T2 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | A graph answer that is bounded and says what it cut | pending | — | `go test ./internal/palace/... ./internal/mcpserver/... -count=1` |
| T2 | Containment edges are a listing, not a fact | pending | — | `go test ./... -count=1` |
| T3 | Tell the writer when a node has outgrown its tier | pending | — | `go test ./internal/palace/... ./internal/mcpserver/... -count=1` |
| T4 | A fetch carries the facts about what it returns | pending | — | `go test ./internal/mcpserver/... -count=1` |

Status: `pending` | `partial` | `blocked` | `done`.

- `pending` — not started, or started and carrying no evidence yet.
- `partial` — genuinely part-done, with every landed claim checked as hard as a `done` one.
- `blocked` — waiting on something outside this repository, named in `**Blocked-on:**`.
- `done` — finished, with tool-written acceptance and mutation evidence to match.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `boundGraphPage` | T4 | T1 before T4 — T4 renders its block through it rather than inventing a second budget |
| T1 | `withheld` keyed by cause | T2, T4 | T1 before both — each adds a key to a map T1 reshapes, and the reshape is the breaking change |
| T2 | `isContainmentEdge` | T4 | T2 before T4 — a fetch must not become a room listing, and it uses T2's rule rather than a copy of it |

## Notes

- **T4 is wave 3, not wave 2.** It consumes T2's `isContainmentEdge` as well as T1's budget helper, so it cannot share a wave with T2 — a fetch that dragged in its room's listing would reintroduce the defect through a different door.
- **T3 is independent and can go first.** It touches the write path only and shares no symbol with the other three, so it is in wave 1 purely because nothing blocks it.
- **T2's regression half is the WHOLE tree**, not two packages. Changing what a graph query returns by default can break anything that walks the graph, and the hooks and skills that walk it live outside `internal/palace`.
- **The figures every task rests on were measured 2026-09-04** against the running local palace — 3,687 drawers, 1,234 triples. The two entry points that exceed the budget today (`room:wing_craft/gotchas` at 184 edges, the bare predicate `holds` at 587) are what make T1's gate falsifiable against the corpus rather than only against a fixture.
- **The wrong rule is the interesting one.** Keying the containment exclusion on the `derived` column reads as more principled and empties 3 of 6 wing roots, including the address `start-here` tells every session to walk first. T2's first step is the test that catches it, written before the rule it protects.
