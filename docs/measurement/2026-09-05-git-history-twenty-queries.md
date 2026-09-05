# Would git history have held the answer? Twenty real recall queries, 2026-09-05

**Gate this answers:** the competitor-parity note (2026-09-05) set the precondition for any
git-history-minting ADR at "take twenty real recall queries from `am_recall_stats` and check by hand
whether git history would have held the answer; under a quarter means distraction".

**Sample:** the twenty newest distinct human-shaped queries in `search_events` on the local palace
since 2026-08-30, after removing hook-origin rows, `<task-notification>` payloads the
UserPromptSubmit hook recalled on, probes, and branch-plus-basename queries. Taken from a copy of
the database at 14:26 UTC. ⚠ Nineteen of the twenty are this session's own queries — one day of
one agent's work, on a repository whose commit bodies carry the why by rule. A repository with
terse subjects would score lower.

**Judgement rule:** YES only if a commit subject, commit body or PR body — not a file in the tree —
would answer the question as asked. Judged by the session that asked them.

| # | query (cut) | git history holds it? | why |
|---|---|---|---|
| 1 | redeploy the local stack after merging to main | no | the procedure is in AGENTS.md, a file; commits say "redeploy", not how |
| 2 | fix the flaky session end hook test | YES | the commit that fixed it carries the cause in its body |
| 3 | why does the recall hook ask room diary | YES | the room change is one commit whose body has the measurement |
| 4 | am_search ids only … progressive disclosure decision | no | new work; no history |
| 5 | competitor comparison mem0 zep … | no | |
| 6 | the mutant survived, what does that mean | no | craft; palace |
| 7 | add a PreCompact hook that writes a state note | YES | ADR-041 T4's commit "moved from PreCompact" is the warning that mattered |
| 8 | why does the recall hook ask room diary and not decisions | YES | as 3 |
| 9 | WHERE SHOULD WORK RESUME AFTER A CRASH | no | checkpoint; palace-only |
| 10 | … RESUME AFTER A CRASH task/compaction-hands-back-state | no | as 9 |
| 11 | compact-aware wake-up SessionStart matcher compact PreCompact | YES | the ADR-041 T4 commits say the hooks are matcher-less and why |
| 12 | symbol to wing mapping | no | decided in the palace, 2026-09-04 |
| 13 | mining git history into memories | no | |
| 14 | competitor parity research | no | |
| 15 | codebase-memory integration with the kit: doctor, installer | YES | PR #263's body and the ADR-057 commits |
| 16 | rerank timeout pool size decision local reranker slow fail open | YES | the commit that set RERANK_TIMEOUT and the compose pool carries the numbers |
| 17 | agentsmemory task/anchor-label-read-side-closed | no | a branch name |
| 18 | accepted, push forward, release a new version | no | not a question |
| 19 | Monitor command shape GitHub watch | no | tooling, palace |
| 20 | ADR-020 T4: a served window that does not begin at line one | no | the record itself, a file |

**Result: 7 of 20 (35%) — over the quarter.** Every YES is a "why did X change" or "what warned
about X" question, and every one is answered by a commit BODY or a PR body, never by a subject
alone and never by a diff. Every no is either new work, a checkpoint, a branch name, or a file.

**What follows.** The gate passes, narrowly and with the sample bias above, so a record is worth
writing — scoped to what scored: commit bodies and PR bodies as verbatim episodes in their own
room, origin-stamped so `am_recall_stats` never lists them as memories to write (ADR-054), never
`git log -p` or diffs, and measured on this repository before any default. Subjects alone would not
have answered one of the seven.

**Side finding, filed in BACKLOG.** The UserPromptSubmit recall hook runs on `<task-notification>`
payloads — harness-generated text, not a prompt — and recorded them as unscoped searches
(four in the 2026-09-04 sample alone). They cost a round-trip each and pollute the to-write list.
