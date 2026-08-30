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
3. **Probe, don't assume.** Call **`am_status` first**, then `am_skillset`.
   ⚠ That order is deliberate and was measured. `am_status` is the call no session
   prunes, and it carries the `entry_protocol` block naming the one skill this team
   wants loaded before anything else — three sessions in three repositories read
   the skillset playbook's own pointer and none of them acted on it, because it was
   conditional on a catalogue call they skipped. This file used to say
   `am_skillset` then `am_status`, which put the reachable pointer second. A non-error
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

**AND A KNOB READ IN ONE ENTRY POINT IS INERT IN THE ELEVEN THAT ALSO OFFER IT.**
`--otel-endpoint` was declared in `dataFlags`, so it appeared in the help of twelve
commands, while `telemetry.Setup` ran in `run()` and `runEval()` only: everywhere
else the flag parsed, reached `cfg.OTELEndpoint`, and installed no provider, so
every span was a `nonRecordingSpan` and nothing was exported — silently, and with
`README.md` and `.env.example` both promising otherwise. Every existing gate passed,
because each asks whether the field is assigned and read SOMEWHERE, which is the
weaker question ADR-006 already rejected; measured, the whole feature could be cut
out of the SERVING path with a green suite. The remedy is a seam rather than a
detector: `withTelemetry` is the one call site,
`TestTelemetrySetupHasOneChokepoint` holds it there, and
`TestEveryActionInTheCommandTreeIsWrapped` walks the tree `rootCommand` actually
returns — so a subcommand added tomorrow is instrumented by existing code instead of
by a reviewer noticing. A per-command *detector* was rejected for the reason this
section keeps recording: `wing`, `share` and `kg-extract` do no instrumented work,
so it would need an exemption list, and a list kept beside the truth goes stale.
`TestASubcommandOfferingOtelEndpointInstallsAProvider` runs the real `mcp` command
and reads the notice, because no AST check can tell a seam that exists from a seam
that fires.

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

**A REGISTRATION IS THE OTHER HALF OF A HOOK, AND NO TEST CAN SEE THE ONE ON DISK.**
`TestEveryInjectingHookIsOnAnInjectingEvent` gates the installer's PLAN; the
`settings.json` in front of an operator — hand-edited, copied from another config
dir with `--copy`, or written by an older install — is gated by nothing.
`aiagentmemory doctor` reads that file, finds every registration naming a script
that declares `# hook-output: stdout-injected`, and exits non-zero on three states:
installed and registered by no event, registered on an event whose stdout goes to
the debug log, or unable to run. `TestDoctorIsRegistered` covers the rung the
command's own tests cannot: they build their own root, so all of them passed with
`doctorCommand(),` deleted from `main.go`. `TestPlaybookIsRegistered` is the
same gate for `agentsmemory playbook`, added the day that command was written
rather than after it shipped unreachable — the pattern generalises to every
operator command, because a command's own tests build their own root and cannot
see the registration.

⚠ **It does NOT fail on silence, and that limit is the finding.** Both shipped
injecting hooks are silent when healthy — the verify hook prints only on drift, the
recall hook only when the palace has something for the branch — so an earlier
version that called zero bytes MUTE failed on a correct install. One run cannot tell
healthy silence from muteness, and resolving that in an exit code is a guess wearing
a check. `TestDoctorDoesNotFailOnSilence` keeps it out. What closes the gap instead
costs nothing: each hook writes what it asked and what came back to stderr, which no
event injects, and `doctor` prints it verbatim so a human judges the silence.

**A corpus check an operator can run.** Every finding behind ADR-038 — 27 drifted
rows, 39 of 41 anchored drawers one re-file from losing their pin, 16 facts naming
a drawer that no longer existed — was produced by a throwaway script and by nothing
in the tree. `doctor --corpus` exits non-zero on a finding like `--index` and
`--schema` do, and reports THREE states rather than two: a reference to a current
row, a reference to an ENDED row (the system working — provenance is historical),
and a reference to nothing. `TestDoctorCorpusIsReachable` covers the rung
`TestEveryFlagIsRead` cannot see: a flag that is declared, documented and read
inside a block nothing can reach.

**A MINT THAT FIRES ON A WRITE CANNOT REACH WHAT WAS WRITTEN BEFORE IT, AND THE
ROWS THAT NEED IT MOST ARE THE ONES NOBODY WILL WRITE TO AGAIN.**
`attachWingRootEdge` mints a wing's by-name root when a drawer lands in the entry
room, and its own test proves that works. It says nothing about the wings already
sitting there: measured 2026-08-31 on this project's palace, forty minutes decided
it — `wing_agentmemories` filed its entry records at 09:34-09:46, the binary
carrying the mint arrived before `wing_craft` filed one at 10:27, and the wing this
protocol tells every session to start in answered `unknown_term` to the first call
it prescribes while three younger wings resolved. `BackfillWingRoots` runs on every
prepared boot for the reason `BackfillContentKeys` does — goose stamps a version
once, so a backfill expressed as a migration cannot resume after an abort.
`TestBackfillMintsARootForAnEntryRoomThatPredatesTheMint` covers the mint,
`TestBackfillLeavesAWingWithNoLiveEntryRecordNameless` covers the half that matters
more (a root over a room whose records are all retracted resolves `matched` with
nothing behind it, which reads as an answer), and `TestWingRootBackfillIsRegistered`
covers the rung the package's own tests cannot see — it drives `buildServices` and
fails when the call leaves the boot path. `TestTheReadOnlyPathMintsNothing` keeps it
on the writing side, because a checker that repairs the corpus reports on a palace
it has just changed.

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

**A POINTER IN PROSE IS WORTH WHAT THE THING IT NAMES IS WORTH, AND MOST OF THIS
CORPUS'S POINTERS ARE IN PROSE.** `TestEveryCitedADRResolves` reads `.go` and only
`.go`, while the large majority of this corpus's ADR citations sit in ADRs, task
files, the README and the backlog, where a renamed or withdrawn record leaves a
pointer to nothing that still reads as provenance. (No count is written here: two
frozen ones shipped in the first draft of this section and one was false at the
commit carrying it, which is the recurrence `citation_test.go` already records. Both
gates log their live figures on a `-v` run.) `TestEveryCitedADRResolvesInDocsToo` is a
sibling rather than a widening, because the Go gate's scope is what lets it need no
exemptions and docs need three: a Numbering line naming which numbers a PR claims,
and two records that must SHOW an unresolvable number to explain the gate itself. A
mention is not a pointer, and telling them apart in prose is the whole difficulty —
shipped without that list the gate would have been all false alarms on day one.
Exemptions are keyed by FILE AND NUMBER, because keying by file alone took 36
working pointers out of the gate to hide one word, and the number is stored without
its `ADR-` prefix so this list does not itself cite a record that does not exist.
`TestDocCitedADRExemptionsAreJustified` refuses an entry with no reason and one that
has stopped earning its place.

**AND A LINE NUMBER IN THE FILE DOING THE CITING CANNOT SURVIVE ITS OWN FILE.** One
backlog entry cited a sibling bullet and drifted `:690` → `:716` → `:744` → `:763`
across four review rounds — every correction wrong again by the next, because the
entry doing the citing kept inserting lines above its target.
`TestNoDocCitesItsOwnLineNumbers` bans the form outright: cite the heading or quote
the sentence, both of which survive an insert. The corpus holds zero today, which
makes it a gate against recurrence rather than a cleanup.

⚠ **It CAN cry wolf, and the first version did** — an earlier draft of this paragraph
claimed otherwise. It compared basenames, and this tree holds 31 `README.md` and 28
`CLAUDE.md`, so one README citing ANOTHER by line read as self-reference; review
reproduced it with a correct cross-file pointer. Self-reference is decided by PATH
now, and ambiguity is declined rather than reported: a bare `README.md:5` that 31
files could mean is left alone, which costs a real false negative and buys the
gate's credibility. Line citations into OTHER files stay legal, including into
pinned third-party source, which is what this corpus's apparently-dangling ones are.
Source `file:line` refs pointing past the end of a real file are recorded in
`BACKLOG.md` as a command rather than gated, because most point into refactored
files where the right line is unknowable.

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

**A DESCRIPTION IS THE ONLY ROUTE BY WHICH A CALLER LEARNS WHAT THE SERVER ACCEPTS, SO
A DESCRIPTION THAT GOES FALSE UNSHIPS A CAPABILITY.** Every gate above asks whether code
is reachable; none asks whether the sentence describing it is still true, and the two
fail differently. `am_add_drawer` told every session a memory over the chunk threshold
was "never MOVED" and a short one "can be relocated for life" — true when written, false
the moment ADR-045 made a memory of any chunk count relocatable. Nothing would have
caught it: the move works, so behaviour passes; the description is emitted, so
reachability passes. What it costs is measurable in the other direction — agents spent
turns trimming records to fit, four measure-and-trim rounds on a single record on
2026-09-01, buying a capability they already had.
`TestNoToolDescriptionClaimsALongMemoryCannotBeMoved` parses `internal/mcpserver`'s
description strings and fails on a claim that a chunked memory cannot be relocated. It
matches the retired CLAIM rather than the topic, because the chunking advice beside it —
one drawer is one vector — must keep being said; a gate that forbids the true sentence
along with the false one is a gate somebody deletes. Its falsifiability case is a
SUBTEST driving the same regexp over a fixture that IS an offender, inside the fence for
the reason `TestASpecBindingThatNamesNothingIsCaught` already records, and it asserts
BOTH directions: the matcher must catch the retired clause and must not catch the advice.

The same principle covers the gates already in the tree: `internal/doclint`
(a doc comment must document the declaration it sits on), `TestEveryDeclaredArmIsRegistered`
(an eval arm that no code path registers appears in no table, silently), and
`TestCatalogSizeIsWhatTheReadmeClaims` (the README's tool count must be the real one).

Prose belongs where a human reads it and nowhere else. Anything that must stay
true gets a command whose exit code says so — including this section, which
`TestAgentsMdNamesGatesThatExist` pins so the list cannot rot into a claim about
tests nobody kept.

---

## Doc comments — written for a reader who has no memory

The palace does not travel with the file. An agent on a harness where the `am_*`
tools are not connected, a contributor reading the code on GitHub, and a reviewer
three months from now all arrive with the same context: none. The doc comment is
the only thing shipped alongside the declaration, so it carries the WHY — not the
signature restated in English.

**The shape**, and `internal/palace/regions.go` is the exemplar to copy: a
name-first one-line summary, a blank line, then the reason.

```go
// SnippetRegions returns every part of a memory that matched, verbatim,
// position-ordered and non-overlapping, within maxChars total.
//
// It exists because the single-window chooser cannot do better. Its score
// SATURATES — a window ranks by how many distinct query terms fall inside, so
// once a window holds the terms every other window holding them ties — and ties
// resolve to the earliest position. Measured 2026-08-21 across nine real
// queries: …
```

**Aim for about 70 words of why, and run longer when the reason is longer.** A
comment that takes a paragraph because the decision behind it took a paragraph is
correct, not verbose. This is a change of level rather than a description of the
tree: re-measured 2026-08-30 over the 1,520 exported functions and methods in
non-test, non-generated files, the median doc comment is 32 words and 18% are
already at 70 or above.

**Name the decision record.** Where the code implements a position an ADR took,
cite it inline — `Region`'s comment says "ADR-019 refuses to put generated prose
on the read path" — and a reader who does not know the corpus exists still gets
from the function to the reasoning. Only 1.8% do this, re-measured 2026-08-30.

Every ADR id cited in Go source must resolve to a file in `docs/adr/`, and that is
not a convention to remember — `TestEveryCitedADRResolves` fails naming file, line
and number, and prints its live figures on a `-v` run. ⚠ No count is written here
on purpose: this sentence carried a frozen one that was already stale by the time
the change shipped, which is the drift §Reachability records against this corpus.
The standard is unchanged either way: a citation that does not resolve is worse
than no citation, because it reads as provenance.

**What earns the words**, in rough order: the failure the code prevents, with the
incident where there was one; the decision it implements and where that decision
is written down; the measurement behind a constant; what was tried and rejected.
What does not: the signature in prose, and padding to reach a number.

This binds code you write or touch. An existing comment is upgraded when you are
already editing that declaration — surgical changes still rule, and a
comments-only sweep of adjacent code is not this.

**REVIEW CHECKS THE HALF NOTHING ELSE CAN.** The clause splits, and only one side
is a reviewer's:

- **Unresolvable citations are GATED.** `TestEveryCitedADRResolves` reads `.go` and
  only `.go`, so doc comments are exactly its universe. It fails naming file, line
  and number. Do not spend a reviewer on it; the test is exhaustive and cannot be
  skipped on a busy day, and it logs its live figures on a `-v` run — no count is
  written here, for the reason §Reachability already records against this corpus's
  frozen ones. §Reachability describes the gate above.
- **A comment that states only what the code does is NOT gateable, and the
  measurement says why.** A word-count rule flags most of the corpus, so it would
  measure padding rather than reason — `internal/doclint`'s own comment records why
  a noisy gate does not survive. Review is the only mechanism there is.

So a review of a change that adds or edits an exported declaration reports, by
name, every one whose comment states only what the code does. Raise it at the
altitude of the other findings, not as a footnote: a feature merged with a one-line
comment is a feature whose reasoning exists only in the session that wrote it, and
that session is gone.

⚠ **THIS PARAGRAPH ONCE SAID "there is deliberately no gate", AND IT WENT FALSE
WHILE THE PR SAT OPEN.** `TestEveryCitedADRResolves` landed in the 271 commits
between writing and merge, so the text asserted the absence of a gate that the
section directly above it documents. A convention with no gate and no reviewer
instruction is a preference; a reviewer instruction for work a test already does
exhaustively is wasted attention. Both halves of that are why the clause is split
rather than dropped.

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

**How to recall and persist is NOT this file's business.** Call `am_skillset`, then
**load `start-here` FIRST if it exists.** That is the general entry point: it carries
the entry protocol, the correction checks and the write-back contract, and it is
maintained where the palace is maintained — so it is right when the palace changes,
and this file cannot be. Whatever a team needs beyond it follows from there, in that
team's own skills.

⚠ **CONDITIONAL, BECAUSE THE SKILL IS NOT GUARANTEED PRESENT.** Measured 2026-08-30
across the two palaces this project runs against: `am_load_skill("start-here")`
resolved at v13 on the hosted palace and returned `skill: not found` on the local
one. The rest of the catalogue had diverged too, in both directions — `laravel-7`
and `writing-memories` exist only on local, `memory-layers` only on hosted, and
`human-decisions` is v1 local against v11 hosted. So `if it exists` is a fact about
this project's deployments, not a hedge.

⚠ **AND DO NOT SUBSTITUTE "LOAD WHATEVER THE CATALOGUE HOLDS".** A catalogue is
per-project — it follows that team's stack and its work — so no document can name
its contents in advance, and enumerating is not a general instruction. The
*convention* `start-here` is the one thing that travels; the catalogue behind it is
nobody else's business. (This paragraph replaces an earlier "enumerate, do not name"
version of this section, which had it backwards.)

⚠ **This file used to restate that protocol, and it drifted.** On 2026-08-29 it was
still teaching a traversal that returns 62,952 bytes and spills to a file — three
independent reproductions that day — and still instructing sessions to load a skill
that had been merged away. **A second copy of a protocol is a second thing to get
wrong, and the copy nobody maintains is the one that stays wrong.**

**What is project scope, and stays in this file:**

- **This repo's wing is `wing_agentmemories`.** If `am_status` does not list it yet,
  this is the first session here and your first write creates it.
- **Craft goes to `wing_craft`, not here.** If a lesson would still be true in a
  repository that shares no code with this one, it is not this project's memory.
- **`docs/adr/`, specs, README and `BACKLOG.md` are authoritative and the palace does
  NOT index them.** Silence from `am_search` proves nothing about what was decided.
  Name the sources you searched; a list of one establishes nothing.
- The gate above, the reachability defect below, and the read-only review exception
  are project policy and belong here.

A verified change that isn't written back is memory lost. Skip only when the session
produced nothing worth recalling — and say so.
