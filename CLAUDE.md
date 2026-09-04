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
  or reversible half of `git` and `gh` — `gh pr view`, `git commit`, never
  `gh:*` or `git:*`. ⚠ `git push` is NOT on it, deliberately: a push is an
  outward action, and an enumerated deny list cannot cover its spellings —
  `git push origin --force main` puts the flag AFTER the remote and slips past
  both `git push --force:*` and `git push -f:*`, as `--force-with-lease` and a
  `+refspec` do. Letting every push prompt costs one keypress and removes the
  whole class; review found this on the first narrowing, in the artifact that
  had just been narrowed. ⚠ THE `deny` LIST NO LONGER HOLDS WHAT THIS FILE USED
  TO SAY IT DID. On 2026-09-04, at the owner's instruction, NINE entries moved
  from `deny` to `allow` in one change: `gh pr merge`, `gh release create`,
  `gh release delete`, `docker compose down -v`, `goose down`, `goose reset`,
  `am_merge_wing`, `am_invalidate_drawer` and `am_kg_invalidate`. What is still
  denied is `git push --force:*`, `git push -f:*` and `rm -rf /*`, and that is
  the whole list — **of this file, which governs interactive sessions in this
  checkout and nothing else.** ⚠ THERE IS A SECOND PERMISSIONS FILE AND IT DID
  NOT MOVE: `clients/claude-code/unattended-settings.json`, the asset the plugin
  ships, still carries all twelve original deny entries — the nine above plus
  the three that stayed. That is deliberate, not drift: the widening was
  approved for a checkout with a human in front of it, and an UNATTENDED run has
  nobody to be the decision point the prompt was. `plugin_test.go`'s
  `unattendedRules` gates that list, so it cannot quietly empty. Reconcile the
  two by reading which one you are under, never by assuming one is stale.
  ⚠ The first draft of this file allowed `gh:*`, `git:*`,
  `python3:*` and `curl:*` with no deny list, in the same PR that told a session
  never to stop for permission; review caught that the two together remove the
  prompt and the boundary at once. This allow list is the deliberate version of
  that — approved by the owner rather than slipped past a review — and the
  instruction is the whole difference. Add a command when you approve it twice.
- **What the widening costs, written down once so no session rediscovers it.**
  Each of those entries was denied because the prompt WAS the owner's decision
  point; a session that runs one now has nothing between it and the effect.
  `gh pr merge:*` also covers `--squash` and `--rebase`, which would break this
  repository's merge-commit convention. `gh release create` publishes to the
  world. `docker compose down -v` destroys the palace volume and `goose reset`
  its schema. `am_invalidate_drawer` and `am_kg_invalidate` end records that
  ADR-038 says are ended rather than overwritten — and an ended record cannot be
  relocated, so that one is a one-way door. Narrow any of them the day an
  unattended session fires one nobody asked for. Note what still stands where:
  `main`'s branch protection gates a merge, and NOTHING gates a release or a
  destructive local command except the session's own judgement.
- **⚠ THIS REPOSITORY DOES HAVE BRANCH PROTECTION ON `main`, and a palace record
  said otherwise until 2026-09-04.** Read from the API that day rather than
  inferred from a refusal message: required status checks `check`, `test` and
  `race` with `strict: true` (the branch must be up to date), `enforce_admins:
  true`, `allow_force_pushes: false`, `allow_deletions: false`,
  `required_conversation_resolution: true`, and
  `required_approving_review_count: 0` — so no approval is demanded, which is
  what makes the whole thing read like an absence until something is refused.
  Two consequences worth having in advance: `--admin` does NOT get past it,
  because admin enforcement is on; and a PR whose own checks are green is still
  refused `3 of 3 required status checks are expected` with
  `mergeStateStatus: BEHIND` the moment the base moves under it, which
  `gh pr update-branch` clears. Measured on #228 and #229 immediately after #226
  landed. So the gates before `main` are the review, the three contexts against
  the CURRENT base, and resolved conversations; the keypress is what went.
- **`scripts/redeploy.sh` is pre-approved, and that is a deploy, not a gate.** It
  is on the allow list because AGENTS.md §The working loop item 3 requires it
  after every merge that changes served code, and a redeploy that prompts is one
  that gets skipped. It restarts the local palace; it cannot reach hosted.

