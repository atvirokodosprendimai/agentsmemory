# ADR-062 tasks

Three tasks. T1 changes what the two compaction hooks write and print; T2 adds
the skill T1's directive names and makes the install set derived; T3 turns T1's
printed instruction into a trigger, and was added after T1's live compaction
showed both that the mechanism works and that an instruction is worth exactly the
model's willingness to read it. T1 and T2 have no dependency between them. T1 consumes nothing T2 produces at build time — the directive names
`/amm` as a string — but the pair only makes sense shipped together, which is why
they are one PR.

| Task | Status | Depends-on | What it does |
|---|---|---|---|
| [T1](T1-the-note-carries-the-task-and-the-start-pauses.md) | done | none | the PreCompact note carries the task in flight; a compact start pauses and names `/amm <task>` |
| [T2](T2-the-amm-skill-and-a-derived-install-set.md) | done | none | the `amm` skill; the installed skill set is derived from the embed glob |
| [T3](T3-the-monitor-that-wakes-the-session.md) | partial | T1 | the hook leaves a marker and `/am` arms a monitor over it, so the pause becomes a wake rather than an instruction |

Both are `partial` for the same reason ADR-059's T2 is: the mechanism is built
and its mutants are killed, and only a real compaction in this checkout proves
the harness sends the payload these scripts parse. The hand-run evidence is in
the PR; it is recorded through `adr-verify` at execution, not written by hand —
a Mutation Log a person types is the self-declared evidence this pipeline exists
to remove.
