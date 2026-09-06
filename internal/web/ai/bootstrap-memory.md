---
type: Guide
title: Bootstrap memory — the memory model to set up in another team
description: A pointer to the agent-facing Markdown document that describes what a team builds inside an agentsmemory palace once the MCP is connected: the wings and rooms, the two auto-loaded skills, the knowledge-graph rules, how to recall, and how a session resumes unfinished work.
resource: {{BASE_URL}}/bootstrap-memory
tags: [memory-model, palace, wings, rooms, skills, knowledge-graph, recall, onboarding]
sources:
  - id: guide
    resource: {{BASE_URL}}/bootstrap-memory
    title: Bootstrap memory — the memory model to set up in another team (full document, Markdown)
generated:
  by: claude/opus-5
---

# Bootstrap memory

`/bootstrap-memory` is the handoff document for setting up the memory model in a team that
already has the agentsmemory MCP connected. It is **not** an install guide and covers no
installation: it starts from a palace that already answers and describes what the team must
build inside it.

Hand its URL to a coding agent and say *"implement memory from this"*. The agent works through
it with `am_*` MCP tool calls — there is no CLI to run and nothing to deploy.

What it carries, and what teams most often get wrong:

- **Wings and rooms** — how to derive the project wing, why a shared craft wing needs the
  opposite scoping, and why rooms can never be renamed or deleted.
- **The two auto-loaded skills** — what earns a place in a document every session reads, and
  the layering test for what belongs in a drawer instead.
- **The knowledge graph** — that facts are workspace-wide rather than wing-scoped, and that the
  entity string is a key rather than a label.
- **Recall** — scoping, which tool answers which question, recalling mid-session rather than
  only at wake-up, and verifying recall by spawning a subagent that has not read what you wrote.
- **Continuity** — how a session picks up work the previous one left unfinished, which is the
  part that makes memory feel like memory rather than an archive.
- **Writing rules** — titling a record with the question a reader will ask, why writing *about*
  a probe degrades that probe, and why a maintained document is one chunk for RECALL rather than
  because anything refuses a longer one.
- **An acceptance test** to run rather than assume, and an honest list of the model's limits.

Every rule in it was measured, usually after being got wrong first.
