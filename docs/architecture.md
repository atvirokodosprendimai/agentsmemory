# Architecture: agentsmemory

**Status:** Living — every ADR that changes structure updates this doc in the same commit.
**Repo:** `github.com/atvirokodosprendimai/agentsmemory`
**Tier:** service (single repo, one deployable binary plus an agent-side kit)
**Gate command:** `go test ./internal/archguard/ ./internal/mcpserver/ ./cmd/server/ ./internal/doclint/ ./internal/repohygiene/ -count=1`
**Last full audit:** 2026-08-21 — module map and edges derived from `go list -json ./...`, interfaces from an AST sweep, not from memory.

This doc is the current-state integral; ADRs are deltas against it. Every rule row binds to a check
that runs. A check that does not exist yet is tagged `(deferred: …)` and swept by `adr-debt`.

## What this system is

A multi-tenant memory palace that AI agents read from and write to over MCP. An agent wakes,
recalls what its team already decided, does the work, and writes back what it learned. The
retrieval half has to be good enough that recall beats re-deriving from source; the tenancy half
has to be strict enough that one team's decisions never surface in another's session.

## Retrieval unit

SQLite remains the durable source of truth and vectors remain indexed per chunk.
The served ranking unit is always the logical memory: vector retrieval widens
the ordered prefix to distinct memory roots, hydrates their chunks, and applies
BM25, closet boost and cross-encoding once per memory.

`am_search` carries memory-level identity, regions, coverage and anchor
staleness. The startup and `am_status` ranking profile ends in `unit=memory`;
this is the live-arm authority because process environment may override an
`.env` file. ADR-024 owns the ranking-unit decision; the 2026-08-25 retirement
note records why the chunk-ranked control was deleted rather than left as a flag.

`MEMORY_EVIDENCE_SELECTOR` / `--memory-evidence-selector` selects the bounded
cross-encoder document. `lexical` (default/control) chooses regions by literal
query-term coverage. `semantic` reuses the raw query vector, batch-embeds
overlapping windows from the reassembled long memory, then selects several
distant high-similarity passages in source order. Short memories bypass the extra
pass, and any invalid or failed passage batch falls back to lexical evidence for
the whole shortlist. Semantic batches are bounded at 128 windows;
the TEI adapter reads `max_client_batch_size` from `/info` — caching only a successful
answer, retried after a backoff otherwise, and never probed under the lock that
guards it — then splits to the
server's actual limit, falling back to TEI's standard 32 when discovery is not
available. The profile reports `evidence=lexical|semantic`.

## Module Map

One row per module, one reason to change per module. `In use` says what selects it in a running
server — the repository's characteristic defect is a component that is finished and reachable from
nothing, so this column is the one to distrust first.

| Module | Layer | One reason to change | In use |
|--------|-------|----------------------|--------|
| `cmd/server` | composition root | the wiring of adapters to the domain changes | the binary; `buildServices` + `configureRanking` |
| `internal/palace` | domain | how memory is stored, ranked or related changes | `mcpserver`, `web`, `cmd/server` |
| `internal/store` | domain port | the vector-store contract changes | `palace` via `VectorStore` / `SourceOfTruth` |
| `internal/store/sqlitevec` | adapter | SQLite vector storage changes | selected by `--vector-backend=sqlite` (default) |
| `internal/store/chromemvec` | adapter | in-process vector storage changes | selected by `--vector-backend=chromem` (local default) |
| `internal/store/qdrant` | adapter | the Qdrant protocol changes | selected by `--vector-backend=qdrant` |
| `internal/embed/ollama` | adapter | Ollama's embedding API changes | selected by `--embed-backend` |
| `internal/embed/teiembed` | adapter | TEI's embedding API changes | selected by `--embed-backend=tei` |
| `internal/rerank/tei` | adapter | the cross-encoder API changes | constructed when `--rerank-url` is set |
| `internal/embedworker` | infra | when pending embeddings are backfilled changes | background loop in `cmd/server` |
| `internal/mcpserver` | api | the MCP tool surface changes | `/mcp` transport |
| `internal/web` | ui | the dashboard changes | the HTTP router |
| `internal/web/views` | ui | rendered markup changes | `internal/web` |
| `internal/tenant` | domain | identity, membership or plans change | everything that resolves a caller (9 importers) |
| `internal/auth` | adapter | how a bearer token becomes a tenant changes | HTTP middleware + `auth.Bridge` |
| `internal/oauth` | api | the OAuth exchange changes | dashboard sign-in |
| `internal/passkey` | adapter | WebAuthn handling changes | dashboard sign-in |
| `internal/usage` | domain | metering or quota changes | `admit()` on every MCP call |
| `internal/billing` | adapter | the payment provider's contract changes | dashboard checkout + webhooks |
| `internal/skill` | domain | the centralised skill model changes | `am_list_skills` / `am_load_skill` / `am_update_skill` |
| `internal/skillset` | domain | the wake-up playbook changes | `am_skillset` |
| `internal/share` | domain | cross-workspace sharing changes | dashboard share flow |
| `internal/mergejob` | infra | how wing merges are queued changes | background claimer |
| `internal/importer` | adapter | the import format changes | `aiagentmemory` import path |
| `internal/dataexport` | adapter | the export manifest or redaction changes | dashboard export |
| `internal/wingbundle` | adapter | the wing bundle format changes | wing export/import |
| `internal/config` | infra | a setting is added or removed | `configFromCmd` |
| `internal/telemetry` | infra | how traces and feature counters are exported | `telemetry.Setup`, Search stages, stdout stage tree, MCP `traceTool`, outbound HTTP |
| `internal/mcptest` | test-support | the end-to-end harness changes | tests only (ADR-008) |
| `internal/archguard` | test-support | a dependency rule changes | tests only (this doc's gate) |
| `internal/doclint` | tooling | the doc-comment rule changes | tests only |
| `internal/repohygiene` | tooling | a repo-hygiene rule changes | tests only |
| `db` | infra | the schema changes | goose migrations at startup |
| `clients/claude-code` | ui | the agent-side kit changes | the `aiagentmemory install` CLI |

**Split candidates.** `internal/palace` is 16k lines and has more than one reason to change:
storage, ranking, evaluation, and the graph (hallways/tunnels/KG) move independently. It is listed
as one module because it ships as one package today; the honest reading is that the module map has
one row where the code has four concerns. `(deferred: docs/adr/BACKLOG.md)`

## How the modules connect

```mermaid
graph TD
  subgraph root[composition root]
    CMD[cmd/server]
  end
  subgraph surfaces[surfaces]
    MCP[internal/mcpserver]
    WEB[internal/web]
  end
  subgraph domain[domain]
    PAL[internal/palace]
    TEN[internal/tenant]
    USG[internal/usage]
    SKL[internal/skill]
    SKS[internal/skillset]
    STO[internal/store]
  end
  subgraph adapters[adapters]
    SQL[store/sqlitevec]
    CHR[store/chromemvec]
    QDR[store/qdrant]
    EMB[embed/ollama · embed/teiembed]
    RRK[rerank/tei]
  end
  AUTH[internal/auth]
  TEL[internal/telemetry]

  CMD --> MCP
  CMD --> WEB
  CMD --> PAL
  CMD --> STO
  CMD --> TEN
  CMD --> AUTH
  CMD --> SQL
  CMD --> CHR
  CMD --> QDR
  CMD --> EMB
  CMD --> RRK

  MCP --> PAL
  MCP --> TEN
  MCP --> USG
  MCP --> SKL
  MCP --> SKS
  MCP --> AUTH
  WEB --> PAL
  WEB --> TEN
  WEB --> USG
  WEB --> SKL
  WEB --> SKS
  AUTH --> TEN
  PAL --> STO
  PAL --> TEL
  MCP --> TEL
  CMD --> TEL
  SQL -.implements VectorStore.-> STO
  CHR -.implements VectorStore.-> STO
  QDR -.implements VectorStore.-> STO
  EMB -.implements palace.Embedder.-> PAL
  RRK -.implements palace.Reranker.-> PAL
```

The graph is **representative, not complete**: it shows 24 of the 70 first-party edges — the
composition root, the two surfaces, the domain and the adapters. The remaining 46 are between
modules this view does not name, and `internal/archguard` is what checks all of them.

Solid arrows are imports; dotted arrows are implementations, which point the other way — the
adapter depends on the port, never the reverse. `internal/palace` imports two first-party
packages (`internal/store` and `internal/telemetry`) and `internal/tenant` imports none, which is
what lets nine modules share identity without a cycle. Telemetry is not a D2 surface: palace
records semantic stages; it does not know which transport asked.

## Dependency Contracts

Measured off the real import graph on 2026-08-21; every rule was already true when written, so each
is a ratchet rather than a wish. **All of them are about PRODUCTION imports** — `archguard` excludes
`_test.go`, deliberately, because a test may import anything it needs to stand the subject up. So
"`internal/tenant` imports no other first-party package" is true of `tenant.go` and not of
`tenant_test.go`, which imports `db`. `Held by` records what actually prevents the violation — three of
the five are enforced by the Go toolchain, and the test must not be read as if it held them.

| # | Rule | Held by | Check |
|---|------|---------|-------|
| D1 | Nothing imports `cmd/server` — the composition root is constructed by nothing | the Go compiler (`package main`) | `internal/archguard/arch_test.go` `TestModuleDependenciesObeyTheContract` |
| D2 | `internal/palace` must not import a surface (mcpserver, web, oauth, billing, share, mergejob, importer, dataexport, wingbundle, embedworker, mcptest) | this test | `internal/archguard/arch_test.go` `TestModuleDependenciesObeyTheContract` |
| D3 | `internal/tenant` imports no other first-party package — identity is a leaf | this test | `internal/archguard/arch_test.go` `TestModuleDependenciesObeyTheContract` |
| D4 | `internal/store` must not import a backend — the dependency points inward | the Go compiler (import cycle) | `internal/archguard/arch_test.go` `TestModuleDependenciesObeyTheContract` |
| D5 | No `internal/*` package imports `clients/*` | the Go compiler (`package main`) | `internal/archguard/arch_test.go` `TestModuleDependenciesObeyTheContract` |
| D6 | Every rule's two halves must match something in the tree | this test | `internal/archguard/arch_test.go` `TestEveryRuleCanFail` |

## Interfaces (the seams)

Thirty-six interfaces. **20 are exported** — the ports an adapter is written against — and **16 are
unexported**, declared at the consumer so a module depends on the one method set it needs rather than
on a whole package. 28 of the 36 have at most two methods.

An earlier version of this paragraph said "almost all are declared at the consumer and unexported"
and the table below listed 30 of the 36. Both were wrong, and a different-lineage reviewer counted
them. The narrowness claim is the one that survives — most of these are two methods wide — and it is
the property worth keeping; the exported/unexported split is not evidence for it either way.

| Interface | Declared in | Method set | Connects |
|-----------|-------------|------------|----------|
| `store.VectorStore` | `internal/store` | EnsureNamespace, Upsert, Search, Delete | palace → sqlitevec / chromemvec / qdrant |
| `store.SourceOfTruth` | `internal/store` | AllPoints, Namespaces, PointsByIDs | reindex/sync → the store that owns the vectors |
| `palace.Embedder` | `internal/palace` | Embed, EmbedOne | palace → embed/ollama, embed/teiembed |
| `palace.Reranker` | `internal/palace` | Rerank | palace → rerank/tei |
| `palace.pointReader` | `internal/palace` | PointsByIDs | palace → the store, narrowed to one read |
| `auth.Resolver` | `internal/auth` | ResolveToken | the HTTP middleware → tenant |
| `oauth.RawResolver`, `oauth.ClientValidator` | `internal/oauth` | ResolveToken / ClientByKey, ValidateClient | oauth → tenant |
| `usage.CapLookup` | `internal/usage` | MonthlyCap | usage → billing plans |
| `mcpserver.WorkspaceLookup` | `internal/mcpserver` | TeamByID | the status tool → tenant, one method wide |
| `mcpserver.wingReader` | `internal/mcpserver` | WingIsEmpty, WingNames | the handoff refusal → palace |
| `skill.Store`, `skill.RoleHolder` | `internal/skill` | GetByName, Upsert, List / Team, User, CanWrite | skill → its repo, and → the caller's role |
| `skillset.Store`, `skillset.SuperHolder` | `internal/skillset` | Get, Set / User, IsSuperAdmin | skillset → its repo and the caller |
| `importer.Drawers`, `importer.Metering` | `internal/importer` | AbsorbDrawers, AbsorbClosets, KGAdd, CreateTunnel… / Allow | importer → palace and usage |
| `mergejob.Merger`, `claimer`, `jobStore`, `roleLookup`, `wingLister` | `internal/mergejob` | MergeWing, RecomputeGraph / ClaimNext… | the background job → palace and tenant |
| `share.requestStore`, `teamLookup`, `wingProvider` | `internal/share` | Create, Get… / TeamBySlug, MembershipRole / Wings, CopyWing | share → its repo, tenant and palace |
| `billing.PlanStore`, `checkoutAPI`, `portalAPI`, `webhookParser` | `internal/billing` | PlanByCode, SetTeamPlan / createCheckout… | billing → tenant and the payment provider |
| `embedworker.Service` | `internal/embedworker` | TeamsWithPending, EmbedPendingForTeam | the backfill loop → palace |
| `web.WingTransfer` | `internal/web` | (wing export/import) | the dashboard → wingbundle |
| `wingbundle.Source` | `internal/wingbundle` | (bundle contents) | the bundle writer → palace |
| `assetSource` | `clients/claude-code` | ReadFile | the installer → its embedded assets |
| `commandRunner` | `clients/claude-code` | run, runShell | the installer → the shell |
| `mcpCaller` | `clients/claude-code` | CallTool | verify → the MCP surface |
| `mineClient` | `clients/claude-code` | CallTool | mining → the MCP surface |

`(deferred: docs/adr/BACKLOG.md)` — nothing checks that a consumer-side interface stays narrower
than the concrete type it stands for, so an interface can grow to mirror a whole service and the
seam stops being a seam.

## Concept Ownership (DRY)

| Concept | Single source | Consumers | Divergence check |
|---------|---------------|-----------|------------------|
| The tool catalogue | `registrar.catalog`, built by registration | `tools/list`, `am_skillset`, README count | `internal/mcpserver/catalog_test.go` `TestCatalogSizeIsWhatTheReadmeClaims` |
| Which tools may write | `registrar.addWrite` (sets `CatalogEntry.Write`) | the write guard, the catalogue | `internal/mcpserver/writeauth_test.go` `TestEveryMutatingToolIsRegisteredAsAWrite` |
| "May change stored memory" | `mcpserver.canWrite` | the write guard, `skillCaller.CanWrite` | `internal/mcpserver/writeauth_test.go` `TestAReadOnlyRoleIsRefusedByEveryWriteTool` |
| A tool's declared arguments | the `mcp.With*` schema on the tool | the handler's reads | `internal/mcpserver/argreach_test.go` `TestEveryArgumentAHandlerReadsIsDeclared` |
| Operator settings | `config.Config` | flags, env vars, the wiring | `cmd/server/wiring_test.go` `TestEveryConfigFieldIsPopulatedAndRead` |
| Which flag fills which field | `configFromCmd` | every flag | `cmd/server/flagbinding_test.go` `TestEveryFlagFillsTheFieldItNamesRunsTheRealCLI` |
| Documented environment variables | the compose files and README | what the program reads | `cmd/server/envreach_test.go` `TestDocumentedEnvVarsAreRead`, `TestReadEnvVarsAreDocumented` |
| Eval arms | `EvalArm` constants | `evalArms()` registry | `internal/palace/armreach_test.go` `TestEveryDeclaredArmIsRegistered` |
| The read/write split | `registrar.add` / `addWrite`, published as `readOnlyHint` and `CatalogEntry.Write` | write guard, `tools/list`, `am_skillset`, both CLIs | `TestLiveToolMetadataMatchesRegistrationPolicy`, `TestDirectCLIReadSurfaceComesFromLiveAnnotations`, `TestIsReadOnlyTool` |

## Composition Root

| Root | Constructs | Check |
|------|------------|-------|
| `cmd/server/main.go` `buildServices` | the database, vector store, embedder, every domain service | `cmd/server/wiring_test.go` `TestEveryConfigFieldIsPopulatedAndRead` |
| `cmd/server/main.go` `productionMCPServer` | the one MCP handler graph used by HTTP and the direct CLI | `cmd/server/mcp_test.go` `TestProductionMCPConstructionHasOneChokepoint` |
| `cmd/server/main.go` `configureRanking` | the ranking configuration, retrieval unit and the reranker | `cmd/server/configureranking_test.go` `TestConfigureRankingReportsTheMemoryUnit`, `TestRerankSurvivesEveryFusionMode` |
| `internal/mcpserver` `registerAll` | every MCP tool, via `add` / `addWrite` | `internal/mcptest/exhaustive_test.go` `TestEveryToolIsExercisedEndToEnd` |

## Test Doubles

| Fake | Stands in for | Contract | Check |
|------|---------------|----------|-------|
| `internal/mcptest` harness | a running server | real HTTP transport, real MCP client, real registration; ordinary scenarios substitute hosted identity and `NewHosted` crosses the real OAuth gate. No scenario crosses the local-mode tenant edge any more — ADR-038 removed the local-only tools that were its only consumers `(deferred: docs/adr/BACKLOG.md)` | `internal/mcptest/harness_test.go` `TestHarnessObservesAWriteThroughARead`, `TestHarnessFailsOnAnEmptyCatalogue`; `internal/mcptest/hosted_auth_test.go` `TestHostedMCPAuthenticationAndIsolation` |
| `mcptest.fakeEmbedder` | a real embedder | deterministic vectors of the configured dimension | none — the fake's vectors are not asserted to behave like a real model's `(deferred: docs/adr/BACKLOG.md)` |
| `cmd/server` `noReranker` | a cross-encoder | returns nil, so the factory call is observable while nothing reranks | `cmd/server/configureranking_test.go` `TestConfigureRankingHonoursTheRerankURLGuard` |

## Trust & Data Boundaries

| Boundary | Crossing | Check |
|----------|----------|-------|
| Unauthenticated → tenant | `auth.Bridge` puts a resolved `tenant.Tenant` on the context; every tool calls `admit` | `internal/mcpserver/writeauth_test.go` `TestAnUnauthenticatedCallIsRefusedBeforeTheRoleCheck` |
| Read-only role → stored memory | `registrar.addWrite` refuses before the handler runs | `internal/mcptest/roles_test.go` `TestScenarioAMemberMayReadAndMayNotWrite` |
| One workspace → another | every query is team-scoped | `internal/mcptest` `TestScenarioAnotherWorkspaceSeesNothing` |
| One wing → another | every read declares its wing boundary; scoped reads widen only with `wing:"*"` | `internal/mcptest` `TestEveryReadToolDeclaresItsWingScope` |
| Stored credentials → an export | credential columns are redacted in the archive | `internal/dataexport/credentials_test.go` `TestExportedCredentialColumnsAreRedacted` |

## Superseded

Nothing yet — this is the first version.
