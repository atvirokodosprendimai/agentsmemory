# ADR-060: A recall you can afford to page — `ids_only` on `am_search`

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** Zydrunas
**Spec:** None — no spec stage
**Cross-references:** ADR-019 (a hit shows its matching regions), ADR-028 (return the identifier and the score), ADR-044 (make a small read trustworthy), ADR-058 (the recall injection is a digest with a budget), docs/research/2026-09-05-competitor-parity.md
**Governs:** internal/mcpserver/drawers.go, internal/mcptest/idsonly_test.go, README.md
**Enforced-by:** `internal/mcptest/idsonly_test.go::TestAnIdsOnlyPageCarriesNoContentAndSaysSo`
**Invalidates:** none — checked. ADR-019 decided a hit CARRIES its regions and ADR-044 decided a hit tells the truth about how much of the memory it holds; both describe the default page, which this record does not touch. An `ids_only` hit holds none of the memory and says so with the fields ADR-044 already defined (`content_truncated`, `content_length`, the id that fetches the rest), so it is the degenerate case of ADR-044's disclosure rule, not an exception to it. ADR-028's `search_id` and `blended_score` are on every ids-only hit and page.
**Served-path change:** `am_search` accepts `ids_only: true` and then returns, per hit, the identity and the numbers a caller ranks and fetches by — id, memory id, wing, room, identity line, dates, blended score, distance, stale marker, content length — and no content, regions or coverage; facts are unchanged; the page never withholds a hit for budget because nothing on it is paid for by content.

## Context

An agent calling `am_search` directly pays for whole hits it may not want. Measured 2026-09-05 on
this project's local palace, wing-scoped, `limit=5`, default `snippet_chars`: three real prompts
returned pages of 12,713, 12,631 and 14,912 bytes. The same five hits rendered as id, wing, room,
identity (80 chars), date, blended score, stale marker and content length measured 1,451 and
1,555 bytes for two of them — roughly 360–390 tokens against 3,200–3,700, a 9–10x cut. Every hit
on those pages was an 88k-character session transcript trimmed to a window, which is the shape
ADR-058 measured as harmful in the hook injection; the hook's answer was a digest, and a direct
caller has no equivalent.

The competitor note (`docs/research/2026-09-05-competitor-parity.md`) read claude-mem's three-layer
disclosure — `search` returns ids in ~50–100 tokens, `get_observations` fetches only the filtered
few — and named it the cheapest borrow in the field, because this project's second layer already
exists: `am_get_drawer` takes an id and `whole: true`, and since ADR-028 it takes the `search_id`
that sent it. What is missing is a first layer thin enough to page.

`snippet_chars` cannot be it. Its floor is documented as `0` meaning WHOLE memories (a caller who
wants less text gets more), and a positive value still renders regions beside the window — ADR-019
chose that on purpose. A negative value would be a second meaning on one knob, which is how the
`max_distance=0` disables convention already costs a sentence of explanation every time it is read.

## Existing Primitives Audit

- **`searchHitView`** and `newSearchHitView` (`internal/mcpserver/drawers.go`): the full hit shape;
  reused as the source of every field the thin view keeps, so a rename there renames both.
- **ADR-044's disclosure fields** `content_truncated`, `content_length`, `content_coverage`: reused;
  an ids-only hit sets the first two and omits coverage, which is the truthful reading (nothing of
  the content was served).
- **`ResponseBudget` / `withheld`** (ADR-044 T-series): bypassed, not changed — the loop that spends
  the budget on content is skipped when there is no content to spend it on, and `withheld` is
  therefore always absent on an ids-only page. The description says so.
- **`am_get_drawer(id, whole, search_id)`**: the second layer, unchanged.
- **`TestEveryArgumentAHandlerReadsIsDeclared`** and `TestEveryOmitemptyWireKeyInThisPackageIsDescribed`:
  the rung-3 gates; the new argument and any new omitempty key must be described in the tool.

## Decision

`am_search` gains a boolean `ids_only` (default false). When true the handler builds the page from a
thin view — `id`, `memory_id`, `uri`, `wing`, `room`, `identity`, `filed_at`, `content_date`,
`source_file`, `blended_score`, `distance`, `stale`, `stale_index`, `content_truncated` (always true
when the memory has content), `content_length`, plus `valid_to`, `ended_reason`, `superseded_by` and
`supersedes` when `include_history` or supersession applies — and skips the content/regions/coverage
rendering and the budget loop entirely. `count`, `search_id`, `facts`, `elsewhere_wings` and
`unlocatable_facts` are exactly as on a full page. The tool description names the mode, says what a
hit then carries, and says that `am_get_drawer` is the second call.

**What would make this wrong:** if an ids-only page for the measured prompts came back above a third
of the full page, the mode would not be worth a second argument; the measurement above says it comes
back at about a tenth, and the acceptance test asserts the ratio on a fixture rather than trusting
the number.

## Alternatives Considered

- **`snippet_chars=-1` as the thin mode:** rejected; a second meaning on a knob whose zero already
  means "whole", and a negative length is a value a caller types by accident.
- **Make the thin page the default and `snippet_chars` opt-in:** rejected; every existing client and
  the two hooks read the full page, and ADR-019/ADR-044 chose that default with measurements.
- **Render the digest (ADR-058) on the server:** rejected; the digest is prose for a model's context
  and belongs to the client that knows its budget; an ids-only page is data a client pages.
- **Drop facts from the thin page:** rejected; facts are one line each and are the cheapest part of
  the page already.

## Component / Boundary Impact

`internal/mcpserver` only. No storage, ranking or palace change; `SearchPage` is called unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_search` argument `ids_only` (boolean, default false) | add, described | `internal/mcpserver/drawers.go` | any MCP client |
| `am_search` hit shape when `ids_only` | the thin view above; no `content`, `regions`, `content_coverage`; no page `withheld` | same | same |
| README `am_search` row | names the mode | README.md | readers |

## Inter-task Contracts

None — one task.

## Implementation

See `tasks/README.md`. One task.

## Consequences

- **Positive:** a direct caller can page a recall for a tenth of the tokens and fetch only what it filters in, with the ids and the `search_id` ADR-028 already joins on.
- **Negative:** one more argument on the tool with the longest description in the catalogue.
- **Neutral:** the hooks do not use it; their digest already reads the full page and renders three lines per hit.

## Out of Scope

- A server-side digest. (permanent: boundary: ADR-058 put the digest in the client that knows its budget)
- Changing what the two recall hooks request. (permanent: boundary: the hooks' page is measured and gated by ADR-058; a thinner page there would lose the region line the digest prints)
- Recording whether ids-only pages are followed by fetches. (deferred: ADR-028's T3 trigger — the first week `am_get_drawer` receives a `search_id` from a non-test client — already covers the join, and an ids-only page is a search like any other)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A client reads `content_truncated: true` on every hit as "the palace is degraded" | Low | Low | the description says an ids-only hit always reports partial, and `content_length` says how much a fetch would return |
| The thin view drifts from the full view when a field is added to one | Med | Low | the thin view is built FROM `newSearchHitView`'s value, never from the palace hit, so a field added to the full view is one line away from the thin one; the test checks the thin hit's keys are a subset of the full hit's |

## Rollback

Remove the argument and the branch; no stored state changes.

## Follow-ups

- [ ] After the mode ships, count ids-only searches in `search_events` (the origin header already distinguishes hooks from agents) for a week; zero is the answer ADR-028's T3 rule says to report as "unused", not as a finding.
