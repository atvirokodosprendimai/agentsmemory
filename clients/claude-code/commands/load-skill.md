---
description: Load a centralised, team-shared skill from agentsmemory by name and install it as a local Claude skill (the client-side nicety over the am_load_skill MCP tool)
argument-hint: [skill name]
---

Pull a **team-shared skill** from your agentsmemory workspace and install it
locally so it is usable in this project. This is the thin client wrapper over the
`am_load_skill` MCP tool: one shared, versioned source of truth, fetched on
demand instead of copy-pasted between machines.

## Skill to load

$ARGUMENTS

## What to do

### No skill name given
If `$ARGUMENTS` is empty, don't guess a name. Call **`am_list_skills`** and show
the available skills as a short table — name, version, description — then ask
which one to load. Stop there; don't install anything.

### A skill name is given
1. **Fetch it.** Call **`am_load_skill`** with `name` set to the argument
   (trimmed). It returns `{ id, name, version, description, content, updated_by,
   updated_at }`.
   - If the tool reports the skill does not exist, say so plainly and offer to
     run `am_list_skills` to show what *is* available. Never invent content.
2. **Install it into a skill slot.** Write the skill to
   `.claude/skills/<name>/SKILL.md` in the current project (create the directory),
   so it lives with the repo and the whole team gets it on the next pull. Build
   the file body as:
   - If the returned `content` already begins with its own `---` frontmatter,
     write `content` **verbatim** — do not add a second frontmatter block.
   - Otherwise, prepend a frontmatter block whose `name:` and `description:` come
     from the tool result, then the returned `content` as the body.
3. **Confirm.** Report what landed: skill name, version, install path, and who
   last touched it (`updated_by` / `updated_at`). Remind the user to restart
   Claude Code (or `/reload`) so the new `/<name>` skill is picked up.

Install into the **user** scope (`~/.claude/skills/<name>/SKILL.md`) instead when
the user asks for the skill everywhere, not just this project.

Keep it surgical: fetch, write one file, confirm. Never edit unrelated skills.
