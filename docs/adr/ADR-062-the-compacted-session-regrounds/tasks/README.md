# ADR-062 tasks

Two tasks, no dependency between them: T1 changes what the two compaction hooks
write and print; T2 adds the skill T1's directive names and makes the install set
derived. T1 consumes nothing T2 produces at build time — the directive names
`/amm` as a string — but the pair only makes sense shipped together, which is why
they are one PR.

| Task | Status | Depends-on | What it does |
|---|---|---|---|
| [T1](T1-the-note-carries-the-task-and-the-start-pauses.md) | partial | none | the PreCompact note carries the task in flight; a compact start pauses and names `/amm <task>` |
| [T2](T2-the-amm-skill-and-a-derived-install-set.md) | partial | none | the `amm` skill; the installed skill set is derived from the embed glob |

Both are `partial` for the same reason ADR-059's T2 is: the mechanism is built
and its mutants are killed, and only a real compaction in this checkout proves
the harness sends the payload these scripts parse. The hand-run evidence is in
the PR; it is recorded through `adr-verify` at execution, not written by hand —
a Mutation Log a person types is the self-declared evidence this pipeline exists
to remove.
