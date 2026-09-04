# agentsmemory — project protocol (Claude Code)

The protocol for this repo lives in `AGENTS.md` and is imported below. It is kept
there rather than here because codex and pi read `AGENTS.md` and have **no import
directive** — the same split the installer already ships (`clients/claude-code/memory.go`:
Claude gets a one-line import, other agents get the text inlined).

@AGENTS.md

---

## Claude Code specifics

Everything above applies. Three things are particular to this harness:

- **The `am_*` tools load deferred here.** The session-start reminder lists them
  by name — `mcp__agentsmemory__am_status`, `…am_search`, `…am_skillset` and the
  rest — but **without schemas**, so calling one directly fails with an
  `InputValidationError`. That error is *not* the gate failing. Run
  `ToolSearch "select:mcp__agentsmemory__am_skillset,mcp__agentsmemory__am_status,mcp__agentsmemory__am_search"`
  first, then call. Only conclude the tools are absent when no `am_*` name
  appears in the reminder at all, or the calls fail on transport/auth.
- **Skills come from two places.** Your local `Skill` list is one; the team's
  centralised catalogue (`am_list_skills` / `am_load_skill`) is the other. This
  repo's Go idioms (`effective-go`, `cqrs`) live in the **centralised** one — a
  `Skill(effective-go)` miss means "check the catalogue", not "no such skill".
- **Slash commands** — `/am` (grounding scoped to a task) and
  `/load-skill <name>` (pull one centralised skill). These run the same sequence
  as `AGENTS.md` scoped to a task; the file above is the always-on baseline that
  applies whether or not you type one. ⚠ `/M` was RETIRED: it carried a Go- and
  UI-specific variant of the same grounding sequence, which is a second copy of a
  protocol, and the installer now removes it from config dirs that still have it.

- **Auto mode edits through Bash and `mrw`, and the shipped PostToolUse "touched"
  hook records `Edit|Write|NotebookEdit|MultiEdit` only** — so the Stop nudge's
  file list is EMPTY for a session that worked the house way. Do not read an
  empty list as "nothing changed"; `git status --porcelain` is the truth. Filed
  as a gotcha in `wing_agentmemories` on 2026-09-04.
- **The watch is a `Monitor` tool call, persistent, armed first.** The command
  shape lives in `wing_agentmemories/tooling`; the rule that it exists is
  `AGENTS.md` §The working loop, item 2. A session that says "the monitor stays
  armed" must have actually called it this session — one that was armed last
  session died with it.
- **Project permissions are in `.claude/settings.json`, and it ships with the
  clone.** The `allow` list names the repository's own gates and the read-only
  or reversible half of `git` and `gh` — `gh pr view`, `git push origin`, never
  `gh:*` or `git:*`. The `deny` list is ADR-051 T9's, copied verbatim so that
  `gh pr merge`, a force-push, `gh release create` and a data-destroying compose
  or goose command still PROMPT whatever the allow list says — deny wins. ⚠ The
  first draft of this file allowed `gh:*`, `git:*`, `python3:*` and `curl:*` with
  no deny list, in the same PR that told a session never to stop for permission;
  review caught that the two together remove the prompt and the boundary at
  once. Add a command here when you approve it twice — narrowly, and check the
  deny list still covers what it should.
- **`scripts/redeploy.sh` is pre-approved, and that is a deploy, not a gate.** It
  is on the allow list because AGENTS.md §The working loop item 3 requires it
  after every merge that changes served code, and a redeploy that prompts is one
  that gets skipped. It restarts the local palace; it cannot reach hosted.

