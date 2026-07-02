---
description: Load a centralised, team-shared skill from agentsmemory by name and use it directly in this session (the client-side nicety over the am_load_skill MCP tool — no file written, always the live version)
argument-hint: [skill name]
---

Pull a **team-shared skill** from your agentsmemory workspace and use it **right
now, in this session**. This is the thin client wrapper over the `am_load_skill`
MCP tool, whose whole point is that the returned body is meant to be *used
directly*: one shared, versioned source of truth, fetched on demand instead of
copy-pasted between machines — and instead of frozen into a stale local file.

**Do not write the skill to disk.** Loading it means adopting its content as
active guidance for this conversation, exactly like invoking a built-in skill —
not installing a `SKILL.md` the harness would only notice after a restart. A file
write would (a) pick the wrong location under this sandboxed config, (b) do
nothing until a reload, and (c) freeze a stale copy that defeats the single
source of truth. Grab the output; use it.

## Skill to load

$ARGUMENTS

## What to do

### No skill name given
If `$ARGUMENTS` is empty, don't guess a name. Call **`am_list_skills`** and show
the available skills as a short table — name, version, description — then ask
which one to load. Stop there; don't load anything.

### A skill name is given
1. **Fetch it.** Call **`am_load_skill`** with `name` set to the argument
   (trimmed). It returns `{ id, name, version, description, content, updated_by,
   updated_at }`.
   - If the tool reports the skill does not exist, say so plainly and offer to
     run `am_list_skills` to show what *is* available. Never invent content.
2. **Adopt it as an active skill for this session.** Treat the returned
   `content` as if it were a skill you just loaded: read it, follow its
   instructions, and apply its conventions for the remainder of this
   conversation. If the body carries its own `---` frontmatter, the instructions
   are everything after it — the frontmatter is just metadata. Nothing is written
   to disk; the skill is live in context and is the freshest version every time.
3. **Confirm.** Report what loaded: skill name, version, and who last touched it
   (`updated_by` / `updated_at`), plus a one-line summary of what it now has you
   doing. No restart or reload is needed — it is already in effect.

Keep it surgical: fetch, adopt, confirm. Don't write files, and don't touch
unrelated skills.
