# agentsmemory — project protocol (repo root)

This is the **agentsmemory** repository: the multi-tenant memory palace that AI
agents read from and write to over MCP. Working here without agent memory is
building the thing while refusing to use it.

This file is the source of truth for how agents work in this repo. Claude Code
reads it through the `@AGENTS.md` import in `CLAUDE.md`; codex, pi and anything
else that honours `AGENTS.md` read it directly. It sits **on top of** the global
`agentsmemory-bootstrap.md` protocol, and its one addition is the hard gate
below.

---

## Gate — verify the `am_*` tools before you do anything else

**First action of every session in this repo.** Before you read a file, plan, or
write a line of code, confirm the `am_*` MCP tools are actually reachable:

1. **Look for them in your tool list.** They are named `am_status`, `am_search`,
   `am_skillset`, `am_add_drawer`, `am_diary_write`, `am_list_skills`,
   `am_load_skill`, and ~30 more. On a harness that namespaces MCP tools they
   appear as `mcp__agentsmemory__am_*`.
2. **A name you cannot call yet is not an absent tool.** Some harnesses load MCP
   tools **deferred** — the name is listed but the schema is not, so a direct
   call fails with a validation error. Load the schema first
   (`ToolSearch "select:am_skillset,am_status,am_search"` on Claude Code), *then*
   call. Do not conclude the tools are missing because the first call errored on
   its arguments.
   Likewise, a server that answers is not the right server: a workspace token
   from another project still returns OK. Only step 4's workspace check proves
   you're home.
3. **Probe, don't assume.** Call `am_skillset` and then `am_status`. A non-error
   return from both means the tools are present and the workspace token is valid
   — for *some* workspace. That is not enough.
4. **Verify the workspace identity.** `am_status` names the workspace it is
   scoped to: `mode` (`local` for a self-hosted server, `hosted` for the SaaS)
   and a `workspace` block carrying its `slug` and `name`. **That** is what
   proves you are home — a global registration carrying another project's token
   answers every probe happily, and only the workspace it names tells you whose
   palace you just opened.

   ⚠ **`mode` tells you WHICH SERVER answered, not where you are sitting, and it
   is NOT the identity check.** A local checkout pointed at the hosted palace —
   this repo's ordinary development setup — returns `mode: "hosted"`, and that is
   correct rather than a warning. **The `workspace` slug is the identity check:**
   compare it against the one this team uses. `mode` only tells you whether that
   workspace lives on the SaaS or on a server you run.

   A workspace you do not recognise is worse than a connection error: you would
   recall another project's decisions as if they were this team's, and every
   write would land in the wrong palace. Stop, run the absent path, and write
   nothing (no diary, no KG, no drawers — that's poisoning another project).

   **A missing `wing_agentmemories` is NOT a wrong workspace.** A wing comes into
   existence when something is first written to it, so on a fresh install the
   wing this protocol tells you to create is necessarily absent — the very first
   session in any repo would otherwise trip a gate that can only be satisfied by
   violating it. Read an empty or missing wing as "first session here; my writes
   will create it", say so in one line, and get on with the work.
5. **Likewise for skills.** A skill missing from your harness's *local* list is
   usually **centralised**, not absent — `am_list_skills` is the catalogue,
   `am_load_skill(<name>)` fetches the body. Check it before you decide the team
   has no convention for your stack.

**Present and correctly scoped** → follow
*[When the tools are present](#when-the-tools-are-present)* below and get on
with the work.

**Absent or wrong workspace** — no `am_*` names in the tool list at all,
`am_skillset` / `am_status` fail with a transport, auth, or connection error,
or `am_status` names a workspace that is not yours — → stop and run
*[When the tools are absent](#when-the-tools-are-absent)*. Do not start the
task. (An unfamiliar *wing* is not this case; an unfamiliar *workspace* is.)

---

## When the tools are absent

### Step 1 — tell the user, before anything else

Say this in your own words, but say all of it:

> **The agentsmemory (`aiagentmemory`) tools are not connected in this session.**
>
> These tools now exist, and this project's protocol is built on them. Without
> them I cannot work on tasks here the way this repo expects, because I am
> missing:
>
> - **`am_search` — cross-session *why*.** Every past decision, tradeoff and
>   gotcha this team recorded is unreachable. I will re-derive things that were
>   already settled, and I may quietly contradict a choice you made last week.
> - **`am_list_skills` / `am_load_skill` — the team's centralised conventions.**
>   The house skills (`effective-go`, `cqrs`, and whatever else the catalogue
>   holds) are versioned server-side, not in this repo. Without them I fall back
>   to generic conventions and my output will drift from house style.
> - **`am_diary_write` / `am_kg_add` / `am_add_drawer` — the write side.**
>   Nothing I learn this session gets persisted. The next session starts exactly
>   as blind as this one, and this session's work is memory lost.
>
> **Fixing it takes about two minutes:**
>
> ```bash
> # install the kit (commands + protocol + Stop hook) and register the MCP
> curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
>
> # or, if the binary is already installed — pick your agent
> aiagentmemory install --agent claude    # claude | codex | pi | both | all
>
> # or register the MCP by hand against a running server
> claude mcp add --transport http agentsmemory http://localhost:8080/mcp
> ```
>
> Run one of those, restart the session, and I will have full recall.

Then ask whether they want to install, or continue without memory.

### Step 2 — if they want to continue anyway, ask six times

Working memory-blind in *this* repo is a real cost, not a formality, so the
opt-out is deliberately expensive. Ask **all six** questions below.

**Rules — these are what make the gate a gate:**

- **One question per turn.** Ask, stop, wait for the user's answer, then ask the
  next. Never batch two into one message and never present all six as a list.
- **Six distinct questions**, in order. Each names a different thing they are
  giving up. Do not paraphrase one question six times.
- **A blanket "yes to everything" up front does not satisfy the gate.** Thank
  them and ask question 1 anyway. The point of six asks is six moments to
  reconsider, and one pre-emptive yes is zero of them.
- **Anything that is not a clear affirmative ends it.** "Maybe", "I guess",
  "whatever", silence, a change of subject, or a question back — treat as *no*,
  stop asking, and go back to offering the install.
- **Any "no" or "stop" at any point ends it immediately.** Do not re-ask, do not
  argue, do not restart the count. Offer the install and wait.
- **Never work around the gate** — no proceeding "just to look at one file", no
  starting the task while you ask, no substituting your own notes or a scratch
  file for the palace.

The six questions:

1. **Understanding.** "The agentsmemory tools are not connected. I'd be working
   with no memory of anything this team has decided before. Do you understand
   that, and still want to continue?"
2. **Losing the *why*.** "Without `am_search` I cannot recall why this code is
   shaped the way it is — I will reconstruct it from the source and I may get the
   reasoning wrong or reopen a settled decision. Is that acceptable?"
3. **Losing the house conventions.** "The team's centralised skills won't load,
   so I'll be writing to generic conventions instead of this team's. The result
   may not match the style of the surrounding code. Continue?"
4. **Losing the write side.** "Nothing from this session will be persisted — no
   diary entry, no drawers, no knowledge-graph facts. Whatever we work out today
   is gone by the next session and someone will pay for it twice. Still go
   ahead?"
5. **The cheap alternative, one more time.** "Installing takes about two minutes
   and then I'd have full recall for this and every future session. Would you
   rather do that now than have me work blind?"
6. **Final confirmation.** "Last check: proceed with this task **without** agent
   memory tools and without the team's skills, accepting all of the above?"

### Step 3 — after six explicit yeses

Only then may you start the task, and only in a degraded mode you keep visible:

- **Open every subsequent response with one line** stating you are working
  without agent memory — e.g. `⚠ no agentsmemory — working from source only`.
  Not once at the start; every turn, so it never fades into the background.
- **Say what you re-derived.** When you work something out that the palace would
  have told you, flag it as reconstructed and therefore unverified against past
  decisions.
- **Keep a written handoff.** Since nothing can be persisted, end the session
  with a summary the user can paste into the palace later — the decisions, the
  gotchas, the open threads.
- **Re-probe on any new session.** The gate is per-session. Six yeses today do
  not carry into tomorrow.

---

## Reachability — the defect this repo keeps shipping

The characteristic failure here is not a bug. It is a capability that is
**finished and unreachable**: the code works, the tests pass, and the one line
that lets anything select it was never written. In one week this shipped four
times — an eval arm declared and never registered, an IDF coverage function with
no branch in `Search`, an embedding backend whose selector existed only in a
package comment, and a config field nothing consumed. Every one of them had
tests. Every test exercised the component rather than the selection.

Two rules follow, and both are enforced mechanically rather than trusted:

- **A test for "X is now available" must fail when X is removed.** Prove it:
  delete the wiring, watch the test go red, put it back. A test asserting that a
  call still returns something passes happily while the feature does nothing —
  that is exactly how the IDF arm survived four winning eval tables without ever
  being reachable from production.
- **Documentation is load-bearing.** A variable shown with a value in a comment
  or an env example is a promise; `TestDocumentedEnvVarsAreRead` fails when the
  program reads no such variable. On its first run it found a shipped compose
  file advertising a rerank pool of 20 that the server had never read.

- **A setting is wired only when both halves exist.** `TestEveryConfigFieldIsPopulatedAndRead`
  fails when a `config.Config` field is never assigned from the command line (a
  setting an operator cannot set) or never read by anything (a setting that
  changes nothing when they do). `TestEveryFlagIsRead` fails when a flag is
  declared and never consulted — `--help` is documentation like any other.

**A setting is read only if it is read in the MODE THAT IS RUNNING** (ADR-006,
Accepted). "Is it read anywhere" is the weaker question and it passes knobs that
are inert under the configuration an operator is actually using.
`TestEveryKnobIsSweptOrNamed` derives its universe from `configureRanking`'s own
body, so a field that joins the wiring joins the check on the same commit; a knob
it finds inert must be listed with a reason, never just listed.
`TestDiscoveredPairsAdmitTheirCondition` then requires the `--help` text to name
the gating knob **greppably** — an honest sentence phrased without that name fails.

**Documentation is load-bearing in BOTH directions, and the reverse arrow is the
one that bit.** `TestDocumentedEnvVarsAreRead` fails when something advertised in
`.env.example`, a `docker-compose*.yml` `environment:` block, or a Go comment
showing a value is read by nothing — it caught a shipped compose file promising a
rerank pool the server never read. `TestReadEnvVarsAreDocumented` runs the other
way, because a variable the code reads and no operator doc mentions is a knob only
its author knows exists. The escape hatch is `notOperatorFacing`, and
`TestNotOperatorFacingIsJustified` refuses an entry without a written reason — the
reason is the review.

**An IDENTITY has a role, and a gate keeps it.** ADR-038 made a drawer's id opaque —
minted once, never recomputed — and moved the derived hash to `content_key`.
`TestNoPathRederivesADrawerID` parses this package and fails when anything other
than `contentKeyOf` calls `DrawerID`, because `contentKeyOf` is where the diary
exemption lives: a key computed anywhere else enters the partial unique index and
dedupes two identical journal entries into one. That is not hypothetical — four of
the five mint paths did it, and an import of two identical reflections produced one
row and reported two. `TestEveryDrawerMintWritesAContentKey` derives its universe
from the source, so a mint path added tomorrow joins the check on the same commit.
`TestNoCommentClaimsADrawerIdIsDerivedFromItsContent` keeps the prose from going
false one instance at a time, and is in two parts because its first draft matched
ZERO of the instances that motivated it.

**A HOOK'S EVENT IS ITS WIRING, and the tests that drive the script cannot see it.**
ADR-041 T4 shipped a hook that performed a recall and printed it — registered on
`PreCompact`, whose stdout Claude Code writes to the debug log. Only `SessionStart`,
`UserPromptSubmit` and `UserPromptExpansion` put a hook's plain stdout into the
model's context. The recall ran every compaction and was thrown away, and every test
passed, because every test drove the SCRIPT and asserted what it wrote. Two mutants
were killed against a mechanism that could not work: a mutant proves a test notices a
change, never that the thing under test is reachable.
`TestEveryInjectingHookIsOnAnInjectingEvent` derives its universe from the hooks
directory and fails when a script declaring `# hook-output: stdout-injected` is
registered on an event that discards stdout; `TestEveryHookScriptDeclaresItsOutputChannel`
is what stops a new script from being invisible to it, and
`TestANonInjectedChannelIsJustified` refuses a quieter channel without a written
reason, so the declaration cannot become the dodge.

**A corpus check an operator can run.** Every finding behind ADR-038 — 27 drifted
rows, 39 of 41 anchored drawers one re-file from losing their pin, 16 facts naming
a drawer that no longer existed — was produced by a throwaway script and by nothing
in the tree. `doctor --corpus` exits non-zero on a finding like `--index` and
`--schema` do, and reports THREE states rather than two: a reference to a current
row, a reference to an ENDED row (the system working — provenance is historical),
and a reference to nothing. `TestDoctorCorpusIsReachable` covers the rung
`TestEveryFlagIsRead` cannot see: a flag that is declared, documented and read
inside a block nothing can reach.

**A CITATION IS A POINTER, AND A POINTER TO NOTHING READS AS PROVENANCE.** A doc
comment naming ADR-031 is the only route from that code to the reasoning behind it,
and it is worth exactly what the record it names is worth. Nothing checked them:
`adr-lint` reads record-to-record cross-references and never opens Go source, `go
vet` does not know what a record is, and a rename passes every test in the tree.
`TestEveryCitedADRResolves` walks the same view of the tree the other hygiene checks
read and fails naming file, line and number. It judges resolution and nothing else —
comment length and presence were measured and rejected in ADR-037's Alternatives.
Its falsifiability is a subtest rather than a sibling, because a corpus with zero
unresolved citations cannot exercise the branch that reports one, and the acceptance
fence runs one test name. That subtest drives the verdict through a substitutable
`testing.TB`: a test cannot pin its own reporting, and without the shim a disabled
gate stayed green and announced "all resolved" over a tree carrying a real offender.

**A FIELD A CALLER CANNOT DISCOVER IS UNREACHABLE EVEN WHEN IT IS EMITTED.** An
`omitempty` response field is absent by construction until the case that produces it,
so a caller who has never hit that case has no way to learn it exists — and every
gate here is blind to that: the field is emitted, so reachability passes; the value
is right, so behaviour passes. `TestEveryOmitemptyWireKeyInThisPackageIsDescribed`
requires each one to be named in a tool or parameter description, matched on a WORD
BOUNDARY because a substring check credited `stale` to the word "staleness" in an
unrelated sentence. Its name says "in this package" because the universe is
`internal/mcpserver` — 26 keys against 79 repo-wide — and a gate whose name claims
more than it covers is worse than a narrower one.
`TestUndescribedOnPurposeIsJustified` refuses an exemption with no written reason,
one naming a field nothing emits, and one that is no longer needed.

**A SPEC BINDING IS A POINTER TOO, AND THE SPEC'S OWN GATE NEVER FOLLOWS IT.** A Facts
row binds an assertion to a `<file>::<test function>` string, and that string is the
only route from a requirement to its proof. `spec-verify` parses the table and checks the binding is
PRESENT — it never opens the file. Renaming a bound test, or deleting the stub,
leaves `spec-verify --draft` at `[PASS]` while the document goes on claiming a fact
is proved by a test nothing runs. Demonstrated 2026-08-28: renaming one binding in
`docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` kept spec-verify green.
`TestEverySpecBindingNamesATestThatExists` walks `docs/specs/` and resolves every
binding with `go/parser` rather than by running anything, so a deliberately-red
binding parked behind a build tag is checked exactly like a green one — which is
the property that matters, because during `@spec` no bound test passes by
definition. `TestASpecBindingThatNamesNothingIsCaught` drives `unresolvedBindings` — the same
function, not a copy — over fixtures that ARE broken, since a corpus with zero
broken bindings cannot exercise the branch that reports one. Its first draft
reimplemented the loop, and severing the real resolution check then left it green
with the whole suite at exit 0: a falsifiability half that shares nothing with the
gate pins nothing. It resolves a subtest binding on its PARENT only, which the
declaration says out loud rather than leaving a reader to assume otherwise.

**AN ACCEPTANCE THAT REPORTS ITS VERDICT IN PROSE IS READ BY NOTHING.** Every acceptance
route here reports a verdict a tool can act on — an exit code plus a fence digest,
and a task is done only when both match. The human-observed route carries neither:
`adr-next` counts such an entry done on its GRAMMAR, date and marker and `.+`, so
any text after the marker reads as success. Measured 2026-08-28: ADR-001 T3 signed
off *"decision BLOCKED — neither ship nor withdraw … T4/T5/T6 not started"* and
every routing tool answered `done T3` / `READY T1`, with `adr-lint` passing over a
README that still said `pending`. T3's Stop Condition says *"Stop the ADR — not
just this task"*; the stop is stated in three sections and read by none of them.
The half that is ours was a missing vocabulary, not a lax regex — T3's acceptance
hint offered `decision <ship|withdraw>`, two values, and the run reached a third.
`TestAHumanObservedSignOffAgreesWithTheIndex` requires each human sign-off to name
`ship`, `withdraw` or `blocked` and requires the sibling README to carry the status
it maps to. `TestASignOffThatSaysStopIsCaught` drives the same function over
fixtures that are wrong, sharing the comparison rather than copying it — the first
draft reimplemented it, and severing the real check left the subtest green. The same shape
recurred one file over: `TestAHumanObservedSignOffAgreesWithTheIndex`'s first version pinned only its
comparison helper, so severing the CALL to it left the suite at exit 0 while the gate printed that
every sign-off agreed with its index — over a corpus where one did not. Both now route the verdict
through a `testing.TB` the falsifiability half substitutes, which is the only form that catches a
severed call site.

The same principle covers the gates already in the tree: `internal/doclint`
(a doc comment must document the declaration it sits on), `TestEveryDeclaredArmIsRegistered`
(an eval arm that no code path registers appears in no table, silently), and
`TestCatalogSizeIsWhatTheReadmeClaims` (the README's tool count must be the real one).

Prose belongs where a human reads it and nowhere else. Anything that must stay
true gets a command whose exit code says so — including this section, which
`TestAgentsMdNamesGatesThatExist` pins so the list cannot rot into a claim about
tests nobody kept.

## Exception — read-only review

The gate above exists to protect WORK: an agent changing this repo without
memory re-derives settled decisions, drifts from house style, and persists
nothing. A reviewer does none of those things — and an independent review is
sometimes valuable precisely BECAUSE the reviewer shares none of our context or
lineage. The gate must not structurally exclude every second opinion: it has
already forced one different-lineage reviewer to be run in a stripped worktree
just to get an unbiased read.

An agent dispatched **solely to review** — read the code, judge it, report —
may proceed without the `am_*` tools, without the six questions, under all of
these conditions:

- **Read-only, absolutely.** No edits, no commits, no files written into the
  repo, no writes to any palace. The moment the task becomes "now fix it", the
  exception ends and the gate above applies in full.
- **Say so in the report.** One line up front: the review was done without team
  memory, so it may contradict decisions the palace records; findings are
  judgements about the code as it stands, not about the history that shaped it.
- **The dispatcher owns reconciliation.** Whoever commissioned the review and
  HAS palace access checks the findings against recorded decisions before
  acting on them — a finding that "X is wrong" may be a decision the team
  already made with reasons the reviewer could not see.

This is an exception for reviewing, not a loophole for working: "I'll just
review it and also quickly fix what I find" is the gate's case, not this one.

---

## When the tools are present

Normal operation. Recall before you act, persist before you stop.

**Recall, in this order:**

1. `am_skillset` — the server's own wake-up playbook and live tool catalogue.
2. `am_status` — workspace identity (`mode` + `workspace`), palace shape, quota.
   This repo's wing is **`wing_agentmemories`**; if it is not in the list yet,
   this is the first session here and your first write creates it.
3. **Try `am_bootstrap` first — it does steps 3 and 4 in one call, server-side.**

   ```
   am_bootstrap(wing:"wing_agentmemories")
   ```

   It resolves the wing's entry node, inlines its first records, returns pointers
   to the rest, and sweeps the corrections attached to any of them — the whole
   hand-executed protocol below, without the hardcoded root id and without the
   three predicate queries.

   ⚠ **It can honestly return nothing, and you must read that correctly.**
   `resolution: "unknown_term"` means the wing has no entry point — not that the
   call failed and not that the wing is empty. The entry node is a derived edge
   written when a drawer is written, so a wing whose `llm_init` drawers predate
   that mechanism has none, and **`wing_agentmemories` is currently such a wing**
   (verified 2026-08-26 against the live server: `unknown_term`). Backfilling
   existing corpora is filed in `BACKLOG.md` and has not run.

   **So until that backfill runs, the traversal below is still how you get in.**
   Do not read the one-call path as permission to skip it.

   **The manual traversal** — the only address you have to know is the root,
   and everything else resolves from it **by traversal, not by search**:

   ```
   am_list_drawers(wing:"wing_agentmemories", room:"llm_init")  # several drawers; see below
   am_kg_query(entity:"<the root drawer's own id>", direction:"outgoing")
   am_get_drawer(id, whole:true)                                # once per must.* edge
   ```

   **Fetch EVERY `must.*` edge — all of them, not the ones that look relevant to
   your task.** That selection is made with exactly the knowledge the tier exists to
   supply, and skipping is silent: nothing reports the drawer you did not read.
   `am_get_drawer` is a by-id row read — no embedding, no search, no ranking — so
   the whole tier costs less than one confident wrong assumption. Measured
   2026-08-25: two sessions each found load-bearing material inside a `must.*`
   drawer whose label sounded unrelated to their task, and would have cherry-picked
   it away. `ref.*` edges are on demand.

   ⚠ **The room holds several drawers and the listing does not flag which is the
   root.** It is the one whose **content** opens `WHAT MUST I LOAD AT THE START OF A
   SESSION?`. Use the content line, not the filename: a sibling is called
   `INIT FLOOR PLAN — …`, so an `INIT` prefix match picks the wrong drawer and the
   siblings are themselves `must.*` targets. Traversing from one of them returns zero
   edges, which this file teaches you to read as a failed query — silent, at the step
   where silence is indistinguishable from breakage.

   ⚠ **Zero edges means the query failed open, not that nothing is filed** —
   `am_kg_query` returns `count: 0` with no error for an unrecognised entity.

   ⚠ **`llm_init` is the one room small enough to list.** `am_list_drawers` caps at
   ~22-25 chunks and silently spills the rest to a file that never enters your
   context, so it is never how you load anything else.

   **Then sweep the retractions — one call, and it is not optional:**

   ```
   am_kg_query(predicate: "retracts")     # then "supersedes", then "qualifies"
   ```

   ⚠ **A correction attaches to the drawer it corrects, as an INCOMING edge, and
   nothing above would ever show it to you.** `am_search` does not check
   supersession either. So a session that executes the traversal perfectly still
   reads whatever the tier got wrong and believes it. This is not hypothetical: on
   2026-08-25 a `must.*` drawer asserted that production served pre-memory-ranking
   code and that nothing had been tagged in a release — both retracted, both still
   read as current by anyone following the steps above alone.

   Predicate-without-entity is an entry point in its own right, so **one call
   returns every retraction in the workspace** — a few hundred bytes.

   ⚠ **Read every row's `source_file`; do NOT just match objects against the ids you
   fetched.** Matching is the obvious reading and it misses the corrections that
   matter most: on 2026-08-25 the only datastar correction in the palace hung off a
   `wing_craft/examples` drawer that is in no tier at all, so an id-match returned
   nothing while the row itself named the problem. Each row's `source_file` says what
   was corrected and how narrowly.

   ⚠ **Run all three predicates.** The one that mattered that day was a `qualifies`,
   and a session that ran only `retracts` shipped a pointer to an ADR that is not on
   `main`.

   Cross-check anything that disagrees against the artifact, never against whichever
   drawer you happen to like.
4. `am_search(<task>, wing: …)` — past decisions and rationale for the work in front
   of you. This is the *only* source of cross-session *why*; don't reconstruct from
   code what memory explains.

   ⚠ **Pass the wing explicitly.** `am_status` reports `default_wing: ""` for the
   registrations used here, and an unscoped recall then spans EVERY wing in the
   workspace — thousands of drawers from unrelated projects. They do not remove
   your answer; they add competitors ahead of it.

   ⚠ **But WHICH wing depends on the question, and getting this wrong looks like
   silence rather than an error:**
   - *"why is this code shaped this way", "what did we decide here", "what is
     open"* → `wing: "wing_agentmemories"`.
   - *"how do we write X"* — datastar, Go, git, testing, any craft or library
     question → **`wing: "wing_craft"`**. Measured 2026-08-25: **every** datastar
     drawer lives in `wing_craft`, so a session that scoped this step to the project
     wing searched 1,722 drawers holding none of the answer, got silence, and — per
     the failure this protocol keeps naming — would have written the idiom it
     already knew. Two searches are cheap; a wrong one is indistinguishable from
     "nothing is filed".

   ⚠ Search the **task**, never the **entry point**: the root is reached by the
   address in step 3, and a note that quotes a query outranks the thing it
   describes.

   ⚠ **Silence here proves nothing about `docs/adr/`.** ADRs, specs, README and
   BACKLOG are a separate, authoritative source this palace does not index. Before
   reporting anything as undecided, name the sources you searched — a list of one
   establishes nothing.
5. `am_list_skills` → `am_load_skill(<name>)` — the team's centralised
   conventions for the stack you're touching. This repo is Go, so `effective-go`
   at minimum; add `cqrs` when the work is live/realtime or fans out across
   subagents.

   ⚠ **Load the BODIES of `human-decisions` and `memory-orchestration` every
   session, both of them, whatever your task is.** A description is not a body.
   The descriptions here are unusually dense, which makes substituting them feel
   sufficient — a session on 2026-08-25 did exactly that, having just resisted the
   identical shortcut on the `must.*` tier, and only noticed afterwards. It is the
   same error one layer over: you are judging what you can skip using the knowledge
   you skipped.

**Recall mid-session too, not just at the start.** Before any broad grep over
unfamiliar code, `am_search` for the symbol or subsystem first and grep only the
gap. Same for tools: if your hand hesitates on a tool's parameters, that
hesitation is the cue to `am_search` for its usage before guessing.

**Persist before you stop:**

- `am_diary_write` — an AAAK session entry (what you built, decided, learned, and
  any open thread) under a stable `agent_name` so the journal threads.
- `am_kg_add` — durable facts as `subject → predicate → object`.
- `am_add_drawer` — notable decisions and code, verbatim, into the right
  wing/room.
- `am_create_tunnel` — when the work connects to another project; check
  `am_find_tunnels` / `am_follow_tunnels` first so you reinforce rather than
  duplicate.

A verified change that isn't written back is memory lost. Skip only when the
session produced nothing worth recalling — and say so.
