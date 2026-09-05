# Competitor parity — what the field does on write, recall, staleness and provenance, measured against this project's north-star

**Date:** 2026-09-05
**Status:** research note, read-only. It decides nothing by itself; two decisions it informs are named at the end.
**Method and its limit:** each system's own documentation or README was read on the date above through a fetch-and-summarise tool, plus one third-party comparison for cross-checking. Nothing here was RUN. Where a document did not say something, this note says "not documented", never "does not exist" — the summariser reported gaps in several places and those gaps are carried here as gaps. Benchmark figures are quoted with their source and are contested in public (Mem0 and Zep dispute each other's LoCoMo runs), so they are context, not evidence.

## The north-star this is measured against

From the README's "Why it exists" and the accepted records: a **drawer is verbatim and never summarised** (ADR-038 made its id opaque and its content key derived); a memory is corrected by **supersession, never overwrite** — the old record is ended with a reason and linked to its replacement, and no agent-reachable tool destroys one; facts live in a **temporal knowledge graph with validity windows** (`am_kg_supersede` ends one window and opens the next in one transaction); a memory can be **pinned to code** (`code_anchors`) and search marks it STALE when that code moves (ADR-056 made unlabelled anchors visible); recall is **hybrid** (vector + BM25 + closet boost, cross-encoder reranked) and is scoped to a **wing per project** with a shared craft wing; **who asked is recorded** (ADR-054) so hook-driven searches never read as memories someone should write; and the Claude Code kit makes recall happen **without depending on the agent remembering** (ADR-041, ADR-051, ADR-058, ADR-059 — a hook on ten events; no script count is written here because this corpus refuses frozen counts).

The axes below are those commitments, asked of each system.

## The systems

| System | What it is, in one line | Source read |
|---|---|---|
| **Mem0** | Extract-and-store personal memory for assistants; hosted + OSS | docs.mem0.ai memory operations, graph memory, history API |
| **Zep / Graphiti** | Temporal knowledge graph built from episodes; hosted (Zep) + OSS (Graphiti) | help.getzep.com overview and namespacing; graphiti README |
| **Letta (MemGPT)** | Stateful agent runtime; memory blocks the agent edits in context | docs.letta.com memory, sleep-time |
| **Cognee** | Document → graph + vector "cognify" pipeline, v1.0 `remember/recall/improve/forget` | docs.cognee.ai core concepts |
| **claude-mem** | Claude Code plugin: hooks capture tool observations, compress, reinject | github.com/thedotmack/claude-mem |
| **Hermes / OpenClaude** | File-based agent memories (MEMORY.md snapshot + FTS5; write-ahead SESSION-STATE.md) | innobu comparison only — not first-hand |

## Axis 1 — the write: what becomes a memory, and who decides

| System | Unit stored | Who decides | Verbatim? |
|---|---|---|---|
| Mem0 | LLM-extracted "facts, decisions, preferences" from messages; `infer=False` stores the raw payload | the extractor (ADD/UPDATE/DELETE/NOOP per the third-party table; the ops page read here described add-only + `expiration_date`) | no by default |
| Zep/Graphiti | entities and relationship edges extracted from an **episode**; the episode itself is kept | the extractor; developer-typed entities via Pydantic | edges are derived; episodes are kept |
| Letta | **memory blocks** (strings pinned to the system prompt) the agent rewrites via tools; archival passages | the agent, in-context; sleep-time subagents rewrite blocks offline (step-based or on compaction) | blocks are prose the agent maintains — overwritten in place |
| Cognee | chunks + extracted entities/relations from documents | the pipeline | chunks yes; graph derived |
| claude-mem | **tool observations** captured by PostToolUse, compressed by a model into summaries | the hooks, then a compressor model | no — compressed |
| agentsmemory | a **drawer**: verbatim text the agent or a human chose to file; facts filed separately as triples | the agent, on a nudge (Stop hook names the touched files) or by protocol | **yes, always** |

**Where this project stands:** alone in refusing to summarise on write. Every other system either extracts (Mem0, Zep, Cognee) or compresses (claude-mem) or lets the agent overwrite prose (Letta). The cost is real and measured here already — ADR-058 found 88k-character transcript chunks as top hits — and the answer this project gave was a bounded DIGEST at read time, not a lossy write. That is a defensible position, not parity, and it is the one thing to keep.

**What they have that this project does not:** an automatic write path. claude-mem and Letta's sleep-time agents write WITHOUT the agent deciding to. Here the Stop hook nudges and names files, and `aiagentmemory mine` ingests transcripts after the fact — but a session that ignores the nudge files nothing. The owner noticed exactly this on 2026-09-04 ("a session that reads the palace all day and writes nothing until told").

## Axis 2 — recall: how a memory comes back

| System | Retrieval | Scope | Rerank / budget |
|---|---|---|---|
| Mem0 | vector + BM25 + entity-graph boost fused into one `score` | `user_id` / `agent_id` / `run_id` filters | not documented |
| Zep/Graphiti | vector + BM25 + graph traversal, "no LLM-in-the-loop reranking"; rerank by distance from a centre node | `group_id` namespaces; search takes user OR group, never both (palace note, 2026-08) | RRF/MMR/cross-encoder recipes per README (names not confirmed in the excerpt read) |
| Letta | blocks are always in context; archival is vector search on demand | per agent; shared blocks across agents | the block's size IS the budget |
| Cognee | graph-backed + vector, "session-aware" | datasets | not documented |
| claude-mem | full-text + Chroma vectors, **three-layer progressive disclosure**: `search` (~50–100 tokens, ids only) → `timeline` → `get_observations` (~500–1,000 tokens) | per project | explicit token budgeting per layer |
| agentsmemory | vector + BM25 + closet boost, cross-encoder rerank, memory-level (not chunk-level) ranking | wing per project + `wing_craft`; `"*"` deliberate | ADR-058 digest: 1,600 chars, whole hits or withheld, "N more" line |

**Where this project stands:** at parity on the retrieval stack (hybrid + rerank is now table stakes; Zep's "no LLM reranking" is a choice this project also made — a cross-encoder, not a generator). Ahead on scoping discipline: the wing/craft split with a measured reason (a bigger corpus adds competitors — "unrelated projects do not remove the answer, they add competitors ahead of it", INSTALL.md and the shipped bootstrap text; ADR-058 records the observation that motivated it) is the same conclusion Zep reached by refusing to merge group and user graphs.

**What they have that this project does not:** claude-mem's **id-first progressive disclosure** is a cleaner budget model than a fixed-size digest: the first call costs ~100 tokens and returns only ids, and the agent pays for full text only for what it filters in. This project's `am_search` returns content by default and the digest is the hook's answer; an agent calling the tool directly still pays for whole hits. Worth a look, and cheap: an `ids_only` mode on `am_search` would be a one-field change with the existing `am_get_drawer` as the second layer.

## Axis 3 — staleness: what happens when the world moves

| System | Mechanism |
|---|---|
| Mem0 | `expiration_date` hides a memory from search; UPDATE/DELETE events in a **history API** (`old_memory`, `new_memory`, `event`, `input`); contradiction handling not documented in the pages read |
| Zep/Graphiti | **bi-temporal edges**: event time and ingestion time; "automatic fact invalidation with temporal history preserved" when a new episode contradicts an edge |
| Letta | blocks are overwritten in place by the agent or the sleep-time subagent; no history documented |
| Cognee | `improve` enriches; `forget` removes; re-cognify not documented |
| claude-mem | not documented; timeline ordering is the implicit answer |
| agentsmemory | **supersession with a reason** (drawer and KG); **code anchors** marked STALE by search when the pinned snippet moves; `doctor --corpus` reports drift as a population |

**Where this project stands:** ahead on one axis nobody else has — **code-anchored staleness**. No system read here ties a memory to a snippet of source and checks it against the tree. Zep is ahead on the FACT side: its bi-temporal model is what `am_kg_supersede` approximates, and its invalidation is automatic on contradiction where this project's is a call the agent makes. Mem0's history API is the closest sibling to ADR-038's "ended, never overwritten" records, and it carries `input` — the conversation that caused the change — which this project records only as `source_file` prose.

## Axis 4 — provenance: where a memory came from

| System | Carried |
|---|---|
| Mem0 | scope ids, metadata, history `input`; "limited" audit per the third-party table |
| Zep/Graphiti | the **episode** every edge was extracted from is kept and referenced ("maintaining data provenance"); `source_description` on episodes |
| Letta | runtime traces; a block carries no history |
| Cognee | relational store tracks documents, chunks, lineage |
| claude-mem | session id, timestamps, file paths per observation |
| agentsmemory | `source_file`, `content_date`, `filed_at`, the origin of every SEARCH (ADR-054), `supersedes`/`superseded_by` links, `source_drawer_id` on facts (checked: must resolve) |

**Where this project stands:** at or above parity. Recording who ASKED (ADR-054) has no counterpart in the pages read. Zep's episode-per-edge is stronger than this project's `source_file` string for derived facts; here a fact's `source_drawer_id` is checked to resolve, which is the same guarantee for facts that came from a drawer, and none for facts an agent typed.

## Axis 5 — harness integration (the axis the north-star adds)

Only claude-mem and this project live inside the coding harness. claude-mem registers five hooks (SessionStart, UserPromptSubmit, PostToolUse, Stop, SessionEnd); this project registers ten, including PreToolUse anchor cues, SubagentStart/Stop, UserPromptExpansion and, since ADR-059, PreCompact. claude-mem captures on PostToolUse — every tool call becomes an observation — where this project's PostToolUse hook records only the touched paths and leaves the writing to the agent. That is the write-path gap from Axis 1 seen from the other side: claude-mem trades verbatim for automatic.

## What follows for the two open questions

**Git-history minting** (mine commit subjects, bodies and PR text into a `history` room): none of the systems read here does it, so it is not a parity item — it is a differentiator or a distraction. The evidence that decides it is local: ADR-054 measured that hook-recalled commit subjects pollute the to-write list unless origin is recorded (it now is), and ADR-058 measured transcript chunks as harmful top hits. A history room would need its own origin stamp and the same digest discipline. Zep's episode model is the closest precedent — an episode is kept and edges are derived from it — and suggests the shape: mint the commit as the episode (verbatim, its own room) and derive facts (`<file> → changed_in → <sha>`) rather than chunking `git log -p`. Worth an ADR only after one measurement: take twenty real recall queries from `am_recall_stats` and check by hand whether the answer would have been in git history. If fewer than a quarter would, the room is a distraction.

**Symbol → wing mapping:** no system read here maps code symbols to memory partitions. Mem0's entity nodes and Zep's typed entities are the nearest idea, and both derive entities from the TEXT, which `code_anchors` already does for this project by pinning the memory to the snippet. The 2026-09-04 decision to keep codebase-memory as a separate peer (ADR-057) stands; nothing in the field contradicts it.

## Three cheap things the field suggests, ranked

1. **`ids_only` on `am_search`** (claude-mem's first layer). A one-field change; the second layer already exists. Measure the per-call saving on the hooks' own recorded searches before and after.
2. **A fact's provenance for typed facts.** Zep keeps the episode; here a fact typed by an agent has `source_file` prose only. Requiring either `source_drawer_id` or a `source_file` that names a commit, PR or run — and reporting the population that has neither in `doctor --corpus` — costs one check and closes the gap to Zep on the audit axis.
3. **Automatic supersession on contradiction** (Zep's invalidation). Expensive and risky here: a generator deciding two verbatim drawers contradict is exactly the LLM-in-the-loop this project has refused on the read path (ADR-019 as cited in `regions.go`). Not recommended; named so nobody re-proposes it as new.

## Sources

- https://docs.mem0.ai/core-concepts/memory-operations · https://docs.mem0.ai/open-source/features/graph-memory · https://docs.mem0.ai/api-reference/memory/history-memory
- https://help.getzep.com/graphiti/getting-started/overview · https://help.getzep.com/graphiti/core-concepts/graph-namespacing · https://github.com/getzep/graphiti
- https://docs.letta.com/guides/agents/memory · https://docs.letta.com/guides/agents/architectures/sleeptime
- https://docs.cognee.ai/core-concepts
- https://github.com/thedotmack/claude-mem
- https://www.innobu.com/en/articles/agent-memory-2026-mem0-letta-zep-hermes-openclaude-comparison.html (third-party; the Hermes/OpenClaude rows rest on it alone)
- Benchmark context, contested: Zep 63.8% vs Mem0 49.0% on LongMemEval per arXiv 2501.13956; Mem0's May 2026 self-report of 92.5% LoCoMo / 94.4% LongMemEval — https://mem0.ai/blog/state-of-ai-agent-memory-2026
