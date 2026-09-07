# ADR-006: A setting an operator changes must change something, or say why not

**Status:** Accepted
**Date:** 2026-08-20
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** `cmd/server/wiring_test.go` (the existing reachability gates), `cmd/server/envreach_test.go`, ADR-003 (owns the `CLOSET_BOOST` default), ADR-002 (owns `BM25_WEIGHT`'s meaning)
**Invalidates:** none — checked (grepped ADR-001..005 for `BM25_WEIGHT`, `RERANK_`, `SEARCH_SCOPE`, `wiring_test`: ADR-002 and ADR-003 read these knobs' VALUES and neither depends on how reachability is checked)
**Served-path change:** `--http-timeout` becomes settable at all; `SearchEvent.Reranked` stops claiming reranking that did not happen; the CLI adapter scopes to its registration's wing; `--bm25-weight` states when it is inert and startup stops contradicting itself. All shipped.

## Context

This repository's stated characteristic defect is a capability that is finished and unreachable, and it ships gates against it: `TestEveryConfigFieldIsPopulatedAndRead`, `TestEveryFlagIsRead`, `TestDocumentedEnvVarsAreRead`, `TestReadEnvVarsAreDocumented`, `TestEveryDeclaredArmIsRegistered`. All of them ask *is this read anywhere*. An audit on 2026-08-20 found that the question that matters is *is it read in the mode that is running*, and that the gap has three distinct shapes plus two live defects.

**Mode-gated.** `BM25_WEIGHT` is parsed into `s.bm25Base` and, under `FUSION=rrf`, never read: `internal/palace/service.go:773` `case s.fusionRRF:` preempts all three reads, and `rankRRF` takes no weight parameter. The same shape covers `OLLAMA_URL` / `OLLAMA_EMBED_MODEL` under `EMBED_BACKEND=tei` (`cmd/server/main.go:894`), `EMBED_URL` unless tei, `QDRANT_URL` / `QDRANT_API_KEY` unless `VECTOR_BACKEND=qdrant` (`main.go:909-931`), and `--addr` when `--socket` is set (`listen.go:34`).

**Unsettable.** `HTTPTimeout` is assigned at `cmd/server/main.go:135` as `HTTPTimeout: def.HTTPTimeout` and from nowhere else — no flag, no env var. The existing gate passes it because its assignment check is `strings.Contains(text, "HTTPTimeout:")`, which a default assignment satisfies, while its failure message promises the stronger property *"an operator has no way to set it"*.

**Not carried across adapters.** `SEARCH_SCOPE` reaches the HTTP MCP path and not the CLI one. Reproduced on the running server, same query, same database, same default `SEARCH_SCOPE=wing`: `am_search` returned 8 hits, all from the registration's wing; `agentsmemory mcp search` returned 8 hits from two *other* projects' wings and none from the registration's. `cmd/server/mcp.go` builds `palace.SearchQuery` directly and never consults `cfg.SearchScope`, in a file whose header claims parity with the HTTP gate.

**Two live defects, not merely inert knobs.** With `RERANK_URL` set and `RERANK_WEIGHT=0`, `applyRerankWith` returns at `service.go:854` before scoring anything, yet `service.go:813` records `Reranked: boolToInt(s.rerank != nil)` = 1 on every search event — recall telemetry asserts reranking that never happened, and ADR-001's calibration reads those rows. In the same state `service.go:714` still widens `candidateK` to `s.rerankPool`, so the fetch and the `GetMany` join are paid for on every search and buy nothing. Separately, `BM25_WEIGHT=auto-idf` with `FUSION=rrf` prints two contradicting startup lines (`main.go:829` then `main.go:836`).

## Existing Primitives Audit

- **`cmd/server/wiring_test.go`** — `configFields`, `goFilesUnder`, `repoRoot`, `isFlagLiteral`. Reuse wholesale; the new check lives beside them in `package main` and shares the helpers.
- **`internal/palace/service_test.go:51` `newTestService`** — migrated SQLite plus a deterministic fake embedder, already used by ~20 tests. Reuse: it is what makes a behavioural sweep cheap enough to be a unit test.
- **`internal/palace/eval.go:1322` `withoutReranker`** — the shallow-copy-a-Service idiom. Reuse for per-cell configuration.
- **`palace.ArmScope`** — the precedent for the shape this ADR generalises: classify once, exhaustively, and have readers consult the classification instead of maintaining a list of exceptions. Reshape, do not reuse the code.

## Decision

A test discovers, by running the real ranking wiring, which knobs are inert under which modes, and requires each one it finds to admit its condition on the surface an operator reads. Nothing declares the inert set; it is computed every run, so it cannot drift from the code the way a table beside the truth does.

The predicate is deliberately two-part, and this is the whole correctness of the check:

1. **K is live at baseline** — varying K alone, from `config.Default()`, changes the output; and
2. **K is inert when D is set** — with D at a non-default value, varying K over the same range changes nothing.

Only both together mean "K is mode-scoped by D". **Pre-registered falsification, and it already fired.** The one-part version — cell varies D, K does not move the output, therefore D scopes K — was the design an independent judge selected. Two adversarial reviewers rejected it independently and for the same reason: `config.Default()` leaves `RerankURL` empty, so `--rerank-pool`, `--rerank-weight` and `--rerank-timeout` are inert in *every* cell, and the check charges that inertness to whichever knob the cell happened to vary. That is **13 misattributed cells**, and one of them is the shipped self-hosted stack (`.env.docker` sets `CLOSET_BOOST=0`, and only `docker-compose.full.yml` sets `RERANK_URL`). A gate that fires on the default stack is one people learn to skip, so the one-part version is withdrawn rather than shipped with a caveat. Knobs failing part 1 are reported as their own category — *inert at baseline* — which is a true statement about `RERANK_POOL` with no rerank URL and is already documented at `config.go:176`.

The check is valid for the ranking and retrieval knobs reachable from the composition root with a fake embedder and a SQLite store. It says nothing about knobs whose effect is only observable against a live Qdrant, TEI or OAuth issuer, and it must name those rather than pass them silently.

## Alternatives Considered

- **A declared `config.Scopes` table**, one row per field listing its inert modes, checked for completeness. Rejected: it is a list kept beside the truth, which this repo has already been bitten by — the eval's pool diagnosis excluded one arm *by name* and the next arm with the same property inherited the bug.
- **A runtime ledger**: wrap each knob so a read marks it, then report at startup which set knobs nothing consulted. Rejected as the primary mechanism because the extraction seam is the danger — `s.closetBoostScale` is read at `mine.go:300`, on a path needing a vector store, so a probe that does not execute it prints a false warning on a correctly-configured server. Its idea is grafted: the *authority* on what a mode reads is execution, not declaration.
- **Static taint analysis** from flag to `With*` call. Rejected: it dies at the first struct-field boundary, and the sharpest finding in the audit — `SEARCH_SCOPE` crossing into `mcpserver.Deps` — is exactly that shape, so the analysis would have missed the defect that motivated it.
- **Fix the four knobs and move on.** Rejected: five gates already exist for this defect family and all five walked past these instances. The gap is the question they ask, not the diligence of whoever last edited them.

## Component / Boundary Impact

`cmd/server` gains a ranking-wiring function extracted from `newServices` so the wiring is drivable without a server; ownership does not move. `internal/palace` is unchanged except for the two live defects. No new module.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `configureRanking(svc, cfg, newReranker) (*palace.Service, []string)` | add | `cmd/server/main.go` | `newServices`, the new gate |
| `SearchEvent.Reranked` | fix — records whether reranking HAPPENED, not whether a reranker exists | `internal/palace/service.go` | `am_recall_stats`, ADR-001 calibration |
| `--rerank-weight`, `--rerank-timeout` Usage strings | edit — state the `--rerank-url` dependency, as `--rerank-pool` already does | `cmd/server/main.go` | `--help`, `TestDocumentedEnvVarsAreRead` |
| `HTTPTimeout` | add `--http-timeout` / `HTTP_TIMEOUT` | `cmd/server/main.go` | operators |
| CLI `mcp search` | fix — honour `SEARCH_SCOPE` as the HTTP path does | `cmd/server/mcp.go` | operators, scripts |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `configureRanking` | T1 | T2 | No — extraction, same behaviour |
| the discovered inert set | T2 | T3 | No — a test artifact |

## Implementation

`tasks/README.md` — four tasks.

## Consequences

- **Positive:** an operator who sets a knob either changes behaviour or is told, at the surface they read, what would have to change first. The five existing gates keep their jobs; this adds the question none of them asks.
- **Negative:** a new class of test that runs the ranking pipeline over a small corpus. It is a unit test with a fake embedder, but it is slower than a grep, and a knob only observable against a live backend is out of its reach and must be named as such.
- **Neutral:** `--help` text grows a dependency clause on two flags.

## Out of Scope

- Knobs whose effect needs a live Qdrant, TEI or OAuth issuer (permanent: a unit gate cannot observe them, and pretending otherwise is how a gate starts lying; T3 names them instead)
- The value-range family — `CLOSET_BOOST=5` silently clamped to 1, `BM25_WEIGHT=2` dropping its magnitude while still flipping the auto/fixed regime (deferred: docs/adr/BACKLOG.md — same "operator intent diverges from behaviour" family, different mechanism: the value is altered rather than ignored, and it wants a validation pass at parse time)
- Making `CLOSET_BOOST` comparable between linear and rrf (permanent: `rank.go:264` scales it by `rrfK` deliberately and explains why; a difference in calibration between two fusion modes is a design choice, not a dead knob, and the gate must not fire on it)
- Auditing MCP tool arguments for the same defect (deferred: docs/adr/BACKLOG.md — this ADR is about operator configuration; a tool argument has a different reader and a different remedy)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The two-part predicate still misattributes in a case nobody thought of | Med | High | T3 runs it against the shipped compose stacks and `.env.example` and requires zero findings there; a finding on a shipped default fails the task |
| The sweep is slow enough that people stop running it | Med | Med | One seeded corpus, shallow-copied per cell; T2's acceptance records the wall-clock and fails above a stated budget |
| Extracting `configureRanking` changes startup behaviour or log text | Low | Med | T1 is a pure extraction; its acceptance diffs the emitted lines against the current ones |
| Fixing `Reranked` invalidates recall stats already collected | Low | Low | The old rows were wrong in one direction only (over-claiming); T4 says so in the migration note rather than rewriting history |

## Rollback

Every change is additive or a localised fix, and none touches persistent layout. Revert the commits: `configureRanking` folds back into `newServices`, the gate disappears, the two Usage strings lose a clause, `--http-timeout` becomes an unknown flag, and `Reranked` returns to over-reporting. The one row worth noting is `SearchEvent.Reranked` — rows written after T4 mean something different from rows written before it, and T4 records the cutover date in `docs/adr/BACKLOG.md` so a later reader of `am_recall_stats` can tell the two populations apart.

## Follow-ups

- [ ] Received from ADR-008: the CLI `mcp` adapter's parity with the HTTP one. ADR-008's harness exercises the HTTP transport; the CLI adapter builds its own config through `configFromCmd` and shares no assertion with it. This ADR owns the config-to-wiring path both entry points use, which is where a parity check belongs.
