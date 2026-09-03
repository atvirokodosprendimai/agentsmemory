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
