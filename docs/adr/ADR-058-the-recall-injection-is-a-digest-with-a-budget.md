# ADR-058: The recall injection is a digest with a budget, not a dump

**Status:** Accepted
**Date:** 2026-09-05
**Owner:** Zydrunas
**Spec:** None — no spec stage
**Cross-references:** ADR-041 (recall that does not depend on remembering), ADR-051 (the session that grounds itself), ADR-054 (a search records who asked), ADR-057 (codebase-memory is a checked peer), clients/claude-code/hooks/agentsmemory-task-recall-hook.sh, clients/claude-code/hooks/agentsmemory-recall-hook.sh
**Governs:** clients/claude-code/hooks/agentsmemory-task-recall-hook.sh, clients/claude-code/hooks/agentsmemory-recall-hook.sh, clients/claude-code/mcpcall.go, clients/claude-code/installer.go, clients/claude-code/README.md
**Enforced-by:** `clients/claude-code/digest_test.go::TestTheDigestFitsItsBudget`
**Invalidates:** none — checked. ADR-041 decided that the hook PERFORMS the recall and injects the RESULT rather than an instruction; this record changes the shape and size of that result and the scope of its query, never whether it is injected. ADR-041's F-9 (drawer selection and order unchanged by the fact block) is untouched: the digest renders the page the server returned in the order it returned it.
**Served-path change:** every UserPromptSubmit and SessionStart recall injects a bounded plain-text digest (a few lines per hit, a trailing "N more" line, facts from the installed wing only) instead of the raw `am_search` JSON page, and says on both channels when it could not look.

## Context

A quality-harness session measured the kit's per-prompt recall on 2026-09-05 and relayed it to
this project's inbox: roughly 3–4k tokens of raw JSON injected on EVERY prompt — full hit objects
with regions, scores, uris and rerank fields — plus knowledge-graph facts from unrelated wings
(invoice-reminder facts from a billing project, in a session about a CI leak check). This session
reproduced it with the hook itself, 2026-09-05, prompt "fix the flaky session end hook test",
against the local palace: 5,877 bytes (~1,470 tokens) on plain stdout, two hits that were both
88k-character session transcripts carrying 24 JSON keys each, and two facts whose subjects were
`deploy-router` and `tool-multipathreadwrite` — neither this repository. Every one of this
session's own prompts carried the same four unrelated billing facts and one 88k-char transcript
chunk, so the figure holds across prompts, not just the probe. Review of #268 measured a
DIFFERENT session on the HOSTED palace, seven injections: 5,301–6,408 bytes, mean ~1,364 tokens —
and, counting wing names across them in a session working only on agentsmemory,
two unrelated projects' wings 34 and 34, `wing_agentmemories` 12 (the two are named in the
review, not here: `TestNoRealProjectNamesInWings` keeps other projects' wing names out of this
corpus). The repository's own wing was the LEAST represented, outnumbered about five to one by two
projects that share no code with it; one recurring hit was a service-worker JavaScript file about push-notification
badges, returned against prompts about ADR reviews. Out-of-wing content is the majority of every
injection, not a tail on a good answer — which is the argument for the wing half of this record.

Two mechanisms, both already written down in the code:

- **The render is the raw page.** `agentsmemory-task-recall-hook.sh` prints `HITS` verbatim after
  its preamble; nothing between the server's JSON and the model's context shortens it. The page
  is a tool response designed for an agent that will call `am_get_drawer` next, not a paragraph
  for a model mid-prompt. `snippet_chars=280` bounds the window, not the envelope around it.
- **The query passes no wing on purpose.** The hook's own comment says so: *"THE HEADER CLAIMS NO
  PROVENANCE THE QUERY CANNOT GUARANTEE. This passes no wing, and a registration reporting an
  empty default_wing searches every project in the workspace."* That was the right call when the
  hook could not know the wing. It can now: `aiagentmemory install --wing` records it, the
  installer already writes `wingEnvVar` into codex's hook environment, and `FactBlock.Facts` is
  in-wing by construction the moment a wing is given — the unrelated facts are the cost of asking
  without one.

A third, from the same peer: when the server is unreachable the hook says so on stderr only,
which the transcript shows and the model never sees; the `CONNECTION_CLOSED` class was diagnosed
for weeks as "the agent forgot" for exactly that reason.

The class this record governs: **every hook that injects a recall into the model's context**.
Enumerated 2026-09-05 with `grep -ln 'mcp search' clients/claude-code/hooks/*.sh` — two scripts,
`agentsmemory-task-recall-hook.sh` (UserPromptSubmit, UserPromptExpansion) and
`agentsmemory-recall-hook.sh` (SessionStart). The anchor-cue and verify hooks inject other
things and are not this class.

## Existing Primitives Audit

- `aiagentmemory mcp search` (`mcpcall.go`) is the one call both hooks make; the digest renderer
  lives there as a flag, so both hooks share one tested renderer rather than two `sed` pipelines.
- `SearchHit`'s `identity`, `wing`, `room`, `content_date`, `regions[0].text` and `stale` are
  already on every hit; the digest selects, it does not compute.
- `wingEnvVar` and the installer's hook-environment prefix (`AGENTSMEMORY_MCP_URL='…'`) are the
  existing channel for install-time facts to reach a hook; the wing joins the URL there.
- `hookSpecificOutput.additionalContext` is the structured channel ADR-051's gates already know
  (`TestTheInjectingSetIsTheDocumentedFour`); the "could not look" line uses it beside stderr.
- `--timeout` on `mcp search` (60 s default) and #263's registration `timeout` (75 s) already
  bound the wait; this record adds attribution, not another bound.

## Decision

`aiagentmemory mcp search` gains `--digest <chars>`: instead of the JSON page it prints, for each
hit in the server's order, three lines — `identity` (the memory's first line), `wing/room` with
`content_date` when present and `STALE` when the hit says so, and the first matched region whose
text is NOT already contained in the identity line (falling back to the first region when every
region is) — then
one line per in-wing fact as `subject → predicate → object`, and, when the page held more than
fit, a final line `N more hit(s) withheld by the budget; am_search "<query>" for the rest`. The
budget is total characters of the rendered text, applied hit by hit so a hit is either whole or
withheld, never cut mid-line. Default 1,600 characters (~400 tokens), which fits two hits with
their region lines and a handful of facts; a hook that wants more says so on the command line.

Both recall hooks call it with the digest, and both carry the installed wing: the installer
writes `AGENTSMEMORY_WING='<wing>'` into the hook's environment prefix beside the URL, and the
hook passes `-a wing="$AGENTSMEMORY_WING"` when the variable is set, which makes the fact block
in-wing and the hits project-scoped. When it is unset — an install without `--wing` — the hook
does what it does today, and the preamble keeps its "may be about a different project" sentence
for exactly that case and drops it otherwise.

When the recall cannot run — no token, server unreachable, the client's `--timeout` fired — the
hook prints ONE line to stderr as today AND the same line through
`hookSpecificOutput.additionalContext`: `agentsmemory could not look: <first line of the error>`.
A model that reads "could not look" does not read "nothing is filed"; a transcript that shows it
does not need a week of diagnosis.

What would make this fail: a rendered digest over a fixture page of three 88k-char hits that
exceeds its budget, or one that cuts a hit mid-line; a hook run with `AGENTSMEMORY_WING` set whose
search request carries no wing; an unreachable-server run whose stdout carries no
`additionalContext`. All three fixtures are hermetic and the first is the measured page shape.

## Alternatives Considered

- **Render the digest in the hook with `sed`/`jq`:** no Go change. Rejected because two scripts
  would carry two renderers, the budget arithmetic in shell is where the last three hook defects
  lived, and the kit's tests drive the CLI, not `jq`.
- **Lower `limit` to 1 and keep the JSON:** halves the bytes, keeps the shape. Rejected: one hit
  is still 24 keys and a 280-char window wrapped in ~2k of envelope, and the unrelated facts
  remain.
- **Filter facts server-side for a wing-less query by guessing the wing from the token:** the
  server cannot know which project a bare token is speaking for; ADR-054 chose to record origin
  rather than guess it, and this record makes the same choice — the kit knows the wing and says it.
- **Render `regions[0]` as the content line:** the obvious choice, and review of #268 measured it
  against this palace's diary memories, which open with a `SESSION:…|PROJ:…|TASK:…` header: in one
  of two sampled hits `regions[0]` was 100% contained in `identity`, so the digest's one content
  line would reprint the line above it — the single-window failure ADR-019's `SnippetRegions`
  exists to prevent, re-created one layer up. Rejected for the first region not contained in the
  identity.
- **Drop the fact block from the hook entirely:** simplest. Rejected because in-wing facts are the
  cheapest thing the palace returns (one line each) and the one thing a dormant wing can still
  answer (ADR-041's reasoning for the block).

## Component / Boundary Impact

Internal to the Claude Code kit: `mcpcall.go` (renderer and flag), the two recall hooks, the
installer's hook-environment prefix. The server is untouched; the JSON page is unchanged for
every other caller.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `aiagentmemory mcp search --digest <chars>` | new flag; plain-text digest instead of the JSON page | mcpcall.go | the two recall hooks, operators |
| hook environment prefix | `AGENTSMEMORY_WING='<wing>'` written beside `AGENTSMEMORY_MCP_URL` when the install has a wing | installer.go | the two recall hooks |
| recall injection | bounded digest with a trailing "N more" line; facts in-wing when a wing is set; "could not look" on `additionalContext` and stderr | the two hooks | the model, the transcript |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `--digest` flag and its output shape | T1 | T2 | No — T2 is the first caller |
| `AGENTSMEMORY_WING` in the hook prefix | T2 (installer half) | T2 (hook half) | No — same task |

## Implementation

See `tasks/README.md`. T1 the renderer in the CLI, hermetic; T2 the hooks, the wing in the
prefix, and the two-channel "could not look" line.

## Consequences

- **Positive:** each prompt costs ~400 tokens of recall instead of ~1,500–4,000; the facts a
  session sees are its own project's; a dead server is named to the model.
- **Negative:** a hit's scores and uri are no longer in the injection — an agent that wants them
  calls `am_search` itself, which the trailing line tells it to do. The JSON stays available to
  every other caller.
- **Neutral:** an install without `--wing` behaves as today, preamble and all.

## Out of Scope

- SessionStart with matcher `compact` re-emitting the wake-up, and a PreCompact state note (deferred: docs/adr/BACKLOG.md)
- A wall-clock budget across the hook's own subprocesses beyond the client `--timeout` and #263's registration bound (permanent: boundary: the recall hooks make one network call; the two bounds already cover it, and ADR-057 records the reasoning)
- Changing what the server returns for `am_search` (permanent: boundary: the page is an agent-facing tool response and every other caller keeps it; the digest is the kit's rendering of it)
- A token count rather than a character budget (permanent: fact: the kit has no tokenizer for the model it injects into, and a character budget is what the harness's own limits are stated in; citation: url https://code.claude.com/docs/en/hooks)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A hit's first region is not its most useful line and the digest reads worse than the window did | Med | Low | the region is the server's best match for the query (ADR-019's snippet regions); the identity line carries the memory's own first line beside it |
| An install upgraded without re-running `install` has no `AGENTSMEMORY_WING` and keeps workspace-wide facts | Med | Low | the preamble's "different project" sentence stays exactly when the wing is absent, so the state is visible; `doctor` prints the hook environment it runs |
| The budget withholds the one hit that mattered | Low | Med | the trailing line names the count and the query, and the withheld hits are the LOWER-ranked ones — the server's order is kept |

## Rollback

Revert T2 to put the JSON page back in the hooks; revert T1 to drop the flag. No persistent
state; a hook registration written with `AGENTSMEMORY_WING` in its prefix is harmless to an older
hook, which ignores the variable.

## Follow-ups

- [ ] Re-measure the per-prompt injection size after T2 lands, on the same prompt, and record it here beside the 5,877-byte figure.
