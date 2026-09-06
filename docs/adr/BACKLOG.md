# ADR backlog

Work deliberately punted out of an accepted or proposed ADR, kept here so it resurfaces at the
next `/adr-write` instead of dying in a scope section. `adr-debt docs/adr` sweeps the `(deferred:)`
pointers that lead here.

An entry leaves this file in one of two ways: it becomes an ADR, or it is re-tagged
`(permanent: <why>)` in its originating ADR because we decided it should never happen.


## RESOLVED 2026-09-06 — A needle that is an identifier proves nothing, and a piped exit code hides the refusal (filed 2026-09-03)

Both halves taken, as the entry asked: *"reject a needle that matches no string literal anywhere in
the SOURCE before deploying, so a bad needle is caught as a bad needle rather than reported as a bad
deploy."*

`scripts/redeploy.sh` now refuses a caller-supplied needle that appears in no Go string literal,
BEFORE the suite runs — an hour of build and test spent to be told the grep was wrong is an hour
spent on nothing. Only caller-supplied needles are checked; a drifted default should still deploy
and report MISSING, which is a finding about the tree rather than a typo. `REDEPLOY_SKIP_NEEDLE_CHECK`
covers a literal genuinely built by concatenation, and is named in the refusal. The usage header now
carries the pipe warning too.

⚠ **BOTH HALVES OF THE GUARD WERE GOT WRONG WHILE WRITING IT, each reproducing the defect it was
written for.**

1. The first draft grepped the `.go` SOURCE for the needle — which matches an IDENTIFIER, the one
   thing that cannot be in a compiled binary. It admitted `evalPromptAbsent` (a real identifier
   here, in no literal) and refused `SocketAuthority` only because that name is absent from this
   tree entirely: it passed its own test for the wrong reason. Quoted segments are extracted first
   now.
2. The second piped those literals into `grep -qF`. Under `set -o pipefail` the early exit gives
   the upstream grep SIGPIPE (141) and the pipeline reports failure — so `chunks_matched`, present
   in four literals, was refused. That is this entry's own second half, committed inside the fix for
   its first. The count goes into a variable instead.

Verified after each: `evalPromptAbsent` refused, `SocketAuthority` refused, `chunks_matched` passes
the guard and stops at the next check.

`checkNeedlePreflight` (`internal/repohygiene/redeployscript_test.go`) gates all three properties —
literal extraction, no `grep -q` in the pipeline, and the escape hatch — with four fixtures.

Original entry, kept because its reasoning is what the guard implements:

## A needle that is an identifier proves nothing, and a piped exit code hides the refusal — 2026-09-03

Two mistakes made against `scripts/redeploy.sh` in one run, both mine and neither a defect in
the script. Filed because the second is the failure this repository already warns about in a
different tool's instructions, and the first is a trap the script's own design invites.

**An identifier is not a needle.** The proof step greps the shipped binary for strings that a
change introduces, and I passed `SocketAuthority` — a Go CONSTANT NAME. Identifiers are not in
a compiled binary; only string literals are. `strings` on the container confirms it: one hit
for the literal `does not address this machine`, zero for `SocketAuthority`. The script
correctly reported `MISSING` and exited 1, over a binary that DID carry the change. The
script's own comment already says needles "only prove a change that introduces a STRING" —
the trap is that the caller picks the needle, and a plausible-looking identifier fails in the
direction that looks like a broken deploy.

**And I read its verdict through a pipe.** I ran `bash scripts/redeploy.sh … | tail -14`, so
the status I saw was `tail`'s zero, not the script's one. The refusal was printed and
discarded in the same breath. The `mrw` instructions in this repo's own tooling guidance state
exactly this rule — "never read an exit code through a pipe" — for exactly this reason, and it
is worth having recorded against `redeploy.sh` too, since that script's entire purpose is a
verdict.

Neither wants a code change. What would help, if anyone touches the script: reject a needle
that matches no string literal anywhere in the SOURCE before deploying, so a bad needle is
caught as a bad needle rather than reported as a bad deploy.

## RESOLVED 2026-09-06 — `redeploy.sh`'s kit check reads a different binary than its own remedy writes (filed 2026-09-03)

**Fixed in `f80e12c` (2026-09-04), one day after this entry was written, and nothing held the fix
in place until now.** The script resolves one symlink hop before comparing and warns when the
remedy directory is shadowed (`scripts/redeploy.sh`, the `remedy_dir` / `readlink` / `SHADOWED`
block) — the entry's own second option, which needed no install-layout decision. The hash-or-drop
options it lists were never the blocker for this half.

`checkShadowWarning` (`internal/repohygiene/redeployscript_test.go`) now gates both halves, because
an ungated fix to a §Reachability defect is the same defect one level up.

⚠ **Its first draft matched the bare words `SHADOWED` and `readlink`, and a mutant replacing the
readlink CALL left it green** — the word also appears in the comment three lines above the code. The
gate now strips `#` comment lines before matching and asserts the code forms
(`readlink "$bin_path"`, `which is SHADOWED.`). Both mutants — deleting the warning line, and
dropping the readlink so `real_path` is never resolved — turn it red; the file was restored
byte-identical after each.

Original entry, kept because the reasoning is what makes the gate legible:

## `redeploy.sh`'s kit check reads a different binary than its own remedy writes — 2026-09-03

Observed on the machine this project is developed on. Two `aiagentmemory` binaries exist:
`~/.claude/bin/aiagentmemory` reports the current revision, `~/.local/bin/aiagentmemory`
reports `dev` and is two days older. `~/.claude/bin` comes first on `PATH`, so the older
copy is shadowed and never runs.

That alone is an operator's untidy machine, not a project defect. What makes it one is which
of the two the script consults. `scripts/redeploy.sh` resolves the kit with
`command -v aiagentmemory` (its `if command -v` / `bin_path="$(command -v …)"` lines), which
is `PATH`-first — so it read the current copy and printed `binary <revision>` under
`==> the installed client kit, against this checkout`. Its own remedy, printed a few lines
further down, is `go build -o $HOME/.local/bin/aiagentmemory ./clients/claude-code`: the
directory holding the STALE copy, which `command -v` will not look at while the other exists.
So the check passes, the fix writes somewhere the check does not read, and an operator who
followed the advice would see no change in the verdict either way.

This is the defect §Reachability records, inside the script whose entire purpose is proof —
"a build's success is a claim about the build; the only evidence that a change is live is
reading the artifact that is serving", which the script says about the SERVER and does not
apply to the kit beside it.

Not fixed here because the answer is an install-layout decision, not a script edit: either
`~/.local/bin` is the one install dir and `~/.claude/bin` should not hold a second copy, or
the kit is installed per-agent and the check must name which one it resolved and warn when a
shadowed duplicate exists. `clients/claude-code/install.sh` defaults to `~/.local/bin`
(`AIAGENTMEMORY_BIN_DIR`), which is evidence for the first reading but not a decision.

## RESOLVED 2026-09-03 — `/mcp` is bounded, and the cap `am_search` never enforced is gone

Both halves are closed. The description no longer promises "max 250 chars" (that claim was
retired with a gate against its return), and `/mcp` now carries a body limit.

**The measurement this was waiting on, taken 2026-09-03 against this project's own palace:**
the largest memory ever filed is 114,636 bytes across 82 chunks — a mined Claude transcript
— with p90 at 4,007 bytes and p50 at 2,466 over 1,253 memories. The bound that actually
governs is `palace.MaxContentLength`, 100,000 RUNES, enforced by sanitize on every drawer
and diary write; the miner's own cap of 90,000 was "learned the honest way, by the server
rejecting a 120k part".

So the limit is DERIVED as `32 * palace.MaxContentLength` rather than picked, and the
property it is chosen for is that **the outer limit must never shadow the inner one**: a
body limit below the worst-case encoding of 100,000 runes would refuse a payload sanitize
would have accepted, and the caller would get a transport error instead of the sentence
explaining the real rule. Deriving it means raising `MaxContentLength` raises this on the
same commit.

Verified on a live server: the 9.5 MB query that motivated the entry went from **200 after
11.7 seconds to 400 in 2.6 milliseconds**, while a maximum-length 100,000-rune memory is
still accepted and answered as JSON-RPC. Three mutants killed, including both directions —
a limit that shadows the content bound, and a limit so large it is not a bound.

## What the MCP protocol offers that this server answers "not supported" to — 2026-09-03

Probed against the running container over the same `http://localhost:8080/mcp` this project's
agents are registered against. Four method families answer `-32601`: `resources/list`
("resources not supported"), `prompts/list`, `completion/complete`, and there is no
`outputSchema` on any tool. Tool ANNOTATIONS are already published (`server.go`), so this list
is what remains, not the whole surface.

Two of the absences are consequences of `WithStateLess(true)` rather than gaps to fill, and
saying so is the point — a gap that is a consequence of a transport choice is not an edge, and
proposing it wastes the next session's time. Server-initiated requests (sampling, elicitation)
and anything subscription-shaped (logging levels, `listChanged`) need a session to route back
through, and stateless mode keeps none. Ranked by the measured failure each would attack:

- **Resources, and `ResourceLink` in tool results.** The strongest candidate. A drawer is
  addressable content with a natural URI, and today the only route to a memory's text is a tool
  call that spends the whole thing in the response. This is the cost ADR-013, ADR-019, ADR-024
  and ADR-044 are all about, and which `content_truncated`, `withheld` and `snippet_chars` are
  all workarounds for — `am_search`'s own description admits "there is no cursor". A page of ten
  links costs almost nothing and lets the client fetch only what it needs.
- **Prompts.** `serverInstructions`' own doc comment names the client this is for: Claude Desktop
  takes no protocol file, got the whole tool catalogue with no guidance, and invented wrong
  scoping semantics from the schemas. The installer ships slash commands for the agents that DO
  take a file; prompts are the protocol-native channel to the ones that do not, and they cost one
  registration each.
- **`outputSchema` / `structuredContent`.** Every `am_*` tool returns JSON inside a text block, so
  a caller learns the shape by receiving one. That is the cause behind
  `TestEveryOmitemptyWireKeyInThisPackageIsDescribed`: a field absent by construction cannot be
  discovered. A declared schema names the field whether or not this call emitted it, which
  attacks the cause rather than the symptom.
- **Completions.** Argument autocomplete for `wing` and `room`. This corpus's own record of agent
  error is largely wing names that resolve to nothing — `wing_to-<project>` filed into wings no
  session will look in, `unknown_term` from a bare-name/prefix confusion. Completion fixes that
  where it happens, in the client, before the call.

## The idle `GET /mcp` stream is held open forever and can never carry anything — 2026-09-03

`GET /mcp` answers `200` and holds the connection. Measured: a single stream held 12s and
delivered zero bytes; 25 concurrent streams were held with the server still answering POSTs
normally. Under `WithStateLess(true)` there is no session, so nothing can ever be pushed down
one — the stream is dead by construction, not merely idle. The transport's own guidance for a
server that offers no stream is `405`, which also tells a client not to keep retrying.

Not filed as urgent: Go holds idle connections cheaply and the server stayed responsive
throughout, so this is slow resource accumulation rather than a denial of service. It is filed
because it was found beside ADR-049 and shares its cause — the endpoint accepting shapes of
request nobody meant it to serve.

## A pointer in prose is checked by nothing, and most of this corpus's pointers are prose — 2026-08-28

Surveyed after four review rounds in which a majority of findings were claims nothing in the tree
could contradict.

⚠ **NO FROZEN COUNTS LIVE HERE.** The first draft of this entry carried five, and one was false at
the commit carrying it — the entry's own prose added three ADR citations to the number it was
reporting. That is verbatim the recurrence `internal/repohygiene/citation_test.go` already records
about two shipped counts, with the remedy written beside it: *"a hand-maintained integrity number is
not a check, it is a second source of truth… the gate logs the live figure on every `-v` run; read
it there."* A second count differed from a reviewer's by 30% purely because we extracted it with
different regexes, so these numbers are METHOD-dependent as well as time-dependent. Both live
figures come from the gates:

```bash
go test ./internal/repohygiene -run 'TestEveryCitedADRResolvesInDocsToo|TestNoDocCitesItsOwnLineNumbers' -v
```

For the ungated rows, the method is the command rather than the answer:

⚠ **The first version of this command could not reproduce its own answer**, and that is worth
keeping visible because it inverted the argument it was published to make. `tracked` was a `set` and
the resolver took `c[0]` from it, so a bare basename with more than one candidate — a third of the
references in this corpus — resolved to whichever file the set happened to yield. Six verbatim runs
on one clean tree returned six different figures for the third number. A frozen count is at least
falsifiable; a method that returns a different answer each run is worse than the number it replaced.

It sorts now, and it no longer resolves an ambiguous basename at all — the same rule the Go gate
adopted: 31 `README.md` in this tree means a basename is not an identity, so an ambiguous reference
is reported as its own class rather than guessed into one of the other two.

```bash
# source file:line citations written in docs, split by whether the file resolves
python3 - <<'EOF'
import re,subprocess
tracked=sorted(subprocess.run(["git","ls-files"],capture_output=True,text=True).stdout.split())
pl=re.compile(r"([A-Za-z0-9_./-]+\.(?:go|sh|yml|yaml)):(\d+)")
tot=nofile=amb=oob=0
for d in (t for t in tracked if t.endswith(".md")):
    for m in pl.finditer(open(d,encoding="utf-8",errors="replace").read()):
        p,ln=m.group(1),int(m.group(2)); tot+=1
        c=[t for t in tracked if t==p or t.endswith("/"+p)]
        if not c: nofile+=1
        elif len(c)>1: amb+=1          # a basename several files can mean: never guessed
        elif ln>sum(1 for _ in open(c[0],encoding="utf-8",errors="replace")): oob+=1
print(tot,"total,",nofile,"naming no tracked file,",amb,"ambiguous,",oob,"out of bounds")
EOF
```

**Two pointer classes are now gated; two deliberately are not.**

**Gated — ADR citations in docs.** `TestEveryCitedADRResolvesInDocsToo`. The Go gate reads `.go`
only, so the large majority of this corpus's ADR citations — the ones in ADRs, task files, the README
and this file — were unchecked. A renamed or withdrawn record leaves a pointer to nothing that still
reads as provenance.

Every unresolved citation the survey found turned out to be a MENTION rather than a pointer: a
Numbering line saying which numbers an open PR still claims, and two records that must DISPLAY an
unresolvable number to explain the citation gate itself. Shipped without an exemption list this gate
would have been all false alarms on day one, which is how a gate gets switched off; this repo has
already had one such incident (issue #16, the AGENTS.md gate false-positiving on every fresh
install). Exemptions are keyed by **file and number** — keying by file alone took 36 working
pointers out of the gate to hide one word — and `TestDocCitedADRExemptionsAreJustified` refuses a
blank reason or one that no longer applies.

**Gated — a doc citing its own line numbers.** `TestNoDocCitesItsOwnLineNumbers`. Zero findings, and
that is the point: a gate against recurrence, not a cleanup. The form cannot survive its own file —
one entry's self-citation drifted `:690` to `:716` to `:744` to `:763` across four review rounds
because the entry doing the citing kept inserting lines above its own target, and a second sat in
ADR-038 pointing at a receipt that had moved.

⚠ **A basename is not an identity here.** The first version compared `filepath.Base`, and this tree
holds 31 files called `README.md` and 28 called `CLAUDE.md` — so one README citing ANOTHER by line
read as self-reference. Reproduced in review by appending a correct cross-file pointer to a nested
README and watching the gate go red, with an error telling the author to cite a heading instead.
Self-reference is now decided by PATH, and **ambiguity is not a finding**: a bare `README.md:5` that
31 files could mean is left alone. That costs a real false negative and buys the gate's credibility,
which is the right trade — a missed finding costs one drifted pointer; a false alarm costs the gate.

**NOT gated — unresolved repo-relative paths.** Most are legitimate FORWARD references: a task file
naming files it will create (`cmd/server/abstain_test.go` in ADR-001 T4,
`internal/palace/anchor_evidence_test.go` in ADR-002 T3, both unexecuted). Telling a planned artifact
from a stale one needs the task's status — more machinery than the finding is worth.

**NOT gated — `file:line` refs whose file does not resolve.** Suggested in review as the cheap
subclass where the forward-reference objection does not apply. It does not survive reading the four
instances: `server/session.go:301` and `server/server.go:581` are mcp-go's source, and `up.go:82` is
goose's — the citing sentence names `goose v3.27.1` beside it. They are deliberate citations into
pinned third-party source, and a gate over them would be four findings and four false alarms. The
same shape as the mentions above, one class over.

**NOT gated — `file:line` refs pointing past the end of a file that does exist.** Real, and the floor
of the true number, since a citation naming the wrong-but-existing line is undetectable. Left as a
command rather than a gate because most point into refactored files where the correct line is
unknowable, so "fix them" means guesses that drift again — the fix this corpus has already disproved
four times.

**Scope, stated honestly.** These two gates cover ADR citations and self-references. By the survey's
own commands that is well under half of the pointers in the corpus, and the largest ungated class —
source `file:line` — is the one the title is about. This retires two classes and measures the rest;
it does not retire the problem.

**What none of it catches, and it is the larger half.** The two sharpest findings of the last four
rounds were a sentence that CONCEDED the premise it was meant to reinforce, and a check whose scope
could not see the defect it was written to prevent. Both semantic; no linter finds either. The
mechanical gates exist so review attention goes where only a reader can judge.

## adr-lint cannot express a cross-record dependency — 2026-08-28

**The general finding stands; the instance I filed it with was refuted in review and is corrected
below. Both halves are kept, because the way the instance was wrong is the more useful lesson.**

**The limitation, verified 2026-08-28 against the quality-harness plugin cache on the authoring machine**, where `adr-lint` on `PATH` resolves to **2.23.0**, and identical in the 2.19.0 and 2.21.0 copies present there — same line numbers in all three. ⚠ A reviewer whose machine carries only 2.19.0 can confirm that copy and nothing else, so read the multi-version claim as "not a version artefact *here*" rather than as reproducible anywhere. The behaviour is what matters and it reproduces on the version everybody has. It is stronger than "the DAG cannot see
these edges" — the schema forbids writing one:

- `bin/adr-lint:272-276` validates every `Depends-on` entry against `all_stems`, the SIBLING task
  files of that ADR, and emits *"Depends-on 'X' matches no sibling task file"*. So a cross-record
  dependency is a hard lint error: the field designed to carry the constraint refuses it.
- `bin/adr-next:136-160` builds the same edge set filtered to `if d in infos`, this ADR's tasks
  only. A foreign T-id is discarded silently. Its docstring says this is deliberate — *"Same edge
  set as adr-lint's DAG, so readiness here cannot disagree"*.
- The failure direction is what matters: **an unseen edge reads as NO edge**, so `adr-next` prints
  `ready` rather than `unknown`.

In this corpus **41 of 94 task files (44%) reference a foreign ADR** across 44 distinct pairs. Not
all imply ordering, but none of them can be represented.

**⚠ THE INSTANCE I USED WAS WRONG, and it is worth reading before reusing this entry.** I claimed
ADR-002 T3 was gated on ADR-003 T3/T4, quoting ADR-003's Decision. That sentence sits inside a
paragraph opening *"an earlier draft of this ADR was wrong"* (`ADR-003:68`) — it is **subjunctive**,
describing a hazard that draft *would have* created and which the accepted design removed at source
in **T1**, which is `done`. Four things say so, all four pre-existing. The round-1 change
edited two files and two of the four cited things lived in them; this head edits only `BACKLOG.md`,
so none of them does now:

- `T3-measure-both-normalizers.md:11-18` — *"the confound the control existed for is gone rather
  than being controlled for"*. That is 55 lines above where the retracted paragraph was added,
  in the same file. (The paragraph is gone from this branch, so the file is now byte-identical to
  `main`; the citation is to what was already there.)
- `ADR-014:51-53` — T3 is *"a check on a shipped default rather than a gate before one"*.
- `BACKLOG.md`, the bullet *"ADR-003 T3's two-corpus measurement is now a check, not a gate"* —
  which reports ADR-014's finding in its own words rather than quoting it. The flip already
  happened: `internal/config/config.go:374` ships `ClosetBoost: 0`. (No line number on purpose;
  this entry inserts lines above that bullet, so any number written here is wrong in the tree the
  entry produces — which is exactly what happened in round 1.)
- `ADR-002:157` — record B **already carried its own constraint**, and carried it better: scoped to
  T4 alone and stated as a conditional, *"If T4 ships closet-ON after all"*. T4 shipped closet-OFF,
  so the condition never fired.

That last one cuts at the thesis I was arguing. I wrote that the constraint "exists only in ADR-003's
prose"; ADR-002 had it, correctly, all along.

**What survives, and it is not nothing.** Two rules, both earned here:

1. **A quotation carries its mood.** Lifting a sentence out of a subjunctive paragraph turns a hazard
   that was designed out into one that is live. Before citing a record's Decision, read the sentence
   that opens its paragraph.
2. **A record that states a cross-record constraint should state it as a CONDITION with its
   trigger**, the way `ADR-002:157` does — not as a standing prerequisite. A conditional expires
   visibly when its condition resolves; a prerequisite has to be remembered and retired by hand, and
   nobody does.

**Still open for the harness owner:** let `Depends-on` name a qualified foreign task, resolve it
against the corpus, and make `adr-next` report `blocked: cannot evaluate X` rather than `ready` for
an edge it could not evaluate. Cycle checking would then need to run over the union rather than per
record.

⚠ **"A different project, not ours to change" is NOT settled here, and this entry said it was.**
The section *"The ADR evidence chain depends on a tool outside the repository"* treats the same
externality as an open decision and names **vendoring the checker into the repo** as one of two
ways out. And this repo already binds Go tests to a harness artefact twice —
`internal/mcpserver/recallcue_spec_test.go` (`taskIndexRow` + `statusOfTask`) and
`clients/claude-code/recallrate_spec_test.go` (`indexRow` :325 + `taskStatus` :328). The gate is
`status[m.task] == "done"` at `:386`; `:401` reads the same map into `st` and gates on `""` /
`"pending"` — both are status gates, only `:386` is that expression. Both read an ADR task README's status column. So a
gate on this side of the boundary is not hypothetical; it is precedent. Whether to add a third is
a decision, not a foregone no.

*(Found by a reviewer who first "corrected" the count from two to one and then retracted the
correction: the second precedent implements the same pattern under different identifiers, so a grep
for the first one's names missed it. Ask which entries exist, not which files contain this string.)*

## A human sign-off that said STOP reads to every routing tool as PROCEED — 2026-08-28

Found by checking what ADR-001 T3 decided before executing anything downstream of it.

**The observation.** `docs/adr/ADR-001-recall-answers-or-abstains/tasks/T3-run-the-gate.md` holds
one human-observed sign-off ending *"eval --calibrate --gate exit 1; no threshold on the curve
clears both bars … decision BLOCKED — neither ship nor withdraw, because the preflight names this
corpus unfit to decide; T4/T5/T6 not started"*. Against that:

- `adr-next ADR-001 --all` prints **`done T3`** and **`READY T1`**.
- `tasks/README.md` said **`pending`** for the same task, so the index and the router disagreed and
  neither said `blocked`.
- `adr-lint ADR-001` **PASSES** over that divergence. Its README↔evidence check is one-directional:
  it rejects `done` without evidence, never `pending` with it.
- `work-next` named ADR-001's remaining tasks as the next work in the whole repository.

So every tool that routes work pointed an executor at T1 — the first step of the sequence T3 had
just forbidden. The record is not vague about this. T3's **Stop Condition** says *"Stop the ADR —
not just this task"* and *"a gate that cannot fail authorises T4–T6 on a verdict that means
nothing"*; its **Out of Scope** says T4/T5/T6 start only *"until this task's log holds a `ship`
sign-off"*. The stop is stated three times in three sections and read by nothing.

**The cause, verified in source** (`bin/adr-next:96-106`; read on the authoring machine's plugin
cache, where `adr-lint` on `PATH` resolves to 2.23.0 and the 2.19.0 and 2.21.0 copies present there
are byte-identical here — ⚠ a reviewer carrying only one of those can confirm that one, and 2.19.0
is the version everybody has):

```python
VLOG_HUMAN_RE = re.compile(r"^- \d{4}-\d{2}-\d{2} · human-observed · .+$")
...
if human and VLOG_HUMAN_RE.match(line):
    return True
```

A human sign-off is counted done by its **grammar**: date, marker, and `.+`. Every other acceptance
route reports a verdict the tooling reads — a tool-written entry carries an exit code and a fence
digest, and a task is done only when both match. The human route carries neither, so any text after
the marker reads as success, including text that says to stop. `adr-lint` skips the same path
explicitly (`evidenced_task_ids`: `if inf.get("human"): continue`).

**The half that is ours, and it is the more useful half.** The schema had no representation for
*"ran, and the answer is stop"*. T3's own acceptance hint prescribes `decision <ship|withdraw>` —
**two** values — and the run reached a third. The executor recorded it correctly and it landed in
free text because there was nowhere else for it to go.

`TestAHumanObservedSignOffAgreesWithTheIndex` (`internal/repohygiene/humansignoff_test.go`) now
requires every human sign-off to name EXACTLY ONE outcome from `ship` / `withdraw` / `blocked`,
requires the sibling README to carry the status that outcome maps to (`done` / `failed` / `blocked`),
and requires the FENCED TEMPLATE in the task's Acceptance section — the command an operator copies,
not the prose around it — to offer all three — because the defect was a template prescribing
two values, and a gate demanding three beside a template offering two reproduces the dead end for
the next operator. It derives its universe from the corpus. ADR-001 T3's row now reads `blocked` and
its hint reads `decision <ship|withdraw|blocked>`.

**And this is a class rather than a one-off, which answers "why gate for a single case".** ADR-004's
supersession gate reached the identical third state on 2026-08-24 — recorded in the palace as
*"REFUSED — NOT 'no' … the gate could not answer. Those are different facts"* — a run that completed,
produced a third outcome, and had two slots to record it in. Issue #34 has been open on that
ambiguity since, before this finding existed. Two ADRs, two routes, one missing value.

⚠ **Exactly one, because no position rule works.** Three were tried: first match rejected a valid
sign-off ("…the decision is recorded in evidence/x.md; decision ship" → "is"), last match rejected
its mirror, and last-in-vocabulary admitted a FALSE PASS on the very failure this entry is about — a
verdict of BLOCKED indexed `done` passed the gate because a later "do not record decision ship"
clause won. Position was standing in for grammar. Counting refuses to guess instead: two outcome
DIFFERENT outcome words is reported rather than resolved.

⚠ **That is a cost, not a claim that a reader cannot resolve it.** The earlier wording said an entry
a machine cannot resolve is one a reader cannot resolve either, and this gate's own fixture is the
counter-example: *"decision blocked — saturated; the decision withdraw option was considered and
rejected"*, indexed `blocked`, reads unambiguously to a person and is rejected here — because "was
considered and rejected" is exactly the clause a machine cannot read. It is a deliberate casualty.

⚠ **DISTINCT words, not occurrences.** Counting occurrences rejected one verdict stated twice —
*"decision ship; recorded in evidence/x.md; per the stop condition T4 starts only on a decision
ship"* — which is what an author writes when the entry names the index it just updated. A false
alarm on a correct sign-off is what killed both position rules, and it nearly arrived again inside
the fix for them.

⚠ **The floor: it reads only the `decision <word>` template form.** A verdict in prose beside one
template mention — *"the decision is blocked … do not record decision ship until the corpus grows"* —
resolves to `ship` and passes. The remedy is to state the verdict in template form, and the gate
cannot say so, because recognising that shape is the thing it cannot do.

⚠ **`blocked` now carries three meanings across three tools**, and `statusForDecision`'s doc comment
is where that is written down: `adr-next --all` prints it for a task whose DEPENDENCIES are unmet,
`adr-lint:636-646` treats it as externally blocked with a green fence, and this gate means the task
RAN and its verdict was stop. No task is in two of those states today, so nothing conflicts — but a
reader comparing tools should know the word is overloaded.

⚠ **What this does NOT fix, stated plainly: `adr-next` still prints `done T3` / `READY T1`.** The
gate makes the corpus self-consistent and makes a future divergence fail a command; it cannot change
what a tool in another tree computes from the task file. An executor who trusts `adr-next` over the
README is still routed into forbidden work — and `/adr-execute`'s own instructions tell them to,
because where the two disagree the task files are supposed to win.

**Still open for the harness owner:** count a human-observed entry as done only when it names a
success outcome, and report a recorded stop as `blocked` rather than `done`. That is a four-line
change to `is_done` plus a vocabulary. It shares the externality question with two entries that both
resolve in this file: *"The ADR evidence chain depends on a tool outside the repository"* and
*"adr-lint cannot express a cross-record dependency"*. Three findings in one external tool is itself
an argument that the vendoring option deserves a decision.

*(This sentence has now been wrong in both directions. It first cited the third entry by a heading
that existed only in its own paragraph — a pointer to nothing. The correction said the entry "lands
with PR #91 and is NOT in this file yet", which went false the moment #91 merged, nine lines above
the heading it claimed was absent. A cross-reference written in the future tense expires; one
written by quoted heading does not, which is the rule this file already carries.)*

**Not taken here, because it is the owner's:** ADR-001 is `Accepted` and its own T3 said to stop the
ADR. Whether that means re-running T3 against a corpus that is not saturated, or withdrawing the
record, is a decision this entry files rather than makes.

## ADR-041 T2 — the recall-before-assertion baseline, measured 2026-08-28

⚠ **RE-TAKEN 2026-08-28 UNDER v3.** "Preceded" now means A RECALL SINCE THE LAST USER TURN, decided
by Zy from the measured distribution of all three candidate readings. Under v2 it meant "this
session touched the palace at some earlier point" — a latch that flipped at the first recall and
never reset, which nobody chose; it is simply what the code computed. Rates under the two are NOT
comparable, which is what the version stamp is for, and the v2 figure is kept below rather than
deleted because it is what the earlier evidence proved.

**7.6%** — of 341 no-change assertions across 24 sessions, 26 had a recall since the user turn that
asked for the work.

| | |
|---|---|
| transcripts scanned | 48 |
| sessions with at least one assertion | 24 |
| assertions | 341 |
| preceded by a recall (since the last user turn) | 26 |
| **rate** | **7.6%** |
| classifier | v3 |

Cross-readings on the same corpus, so the choice stays auditable: v2's latched reading **52.8%**;
since the last compaction **43.4%**; within 100 tool calls 28.7%, within 50 17.6%, within 25 12.3%,
within 10 **7.3%**, within 5 5.3%, within 1 1.8%.

The user-turn reading lands within half a point of the within-10-calls window from a completely
different derivation, which is the only evidence any particular window is more than a number
someone picked. `TestTheRecordedBaselineNamesTheVersionTheCodeStamps` pins the `classifier` row
above to the constant the code stamps, so a future redefinition cannot ship without re-taking this.

### T6 shipped 2026-08-28, into a window it shares with T4

T6 shipped — `serverInstructions` names the class of claim and carries no imperative — and was
verified against the RUNNING server rather than the build log: the live handshake returns 1194 bytes
carrying `WHAT SOURCE CANNOT SETTLE` and not `RECALL BEFORE YOU ACT`.

**The before-state, re-taken with the shipped binary at the moment the window opened**, and it
reproduces the baseline exactly, as it must — every transcript on disk predates T6 by minutes:

| | |
|---|---|
| sessions with at least one assertion | 24 |
| assertions | 341 |
| preceded (since the last user turn) | 26 = **7.6%** |
| made before ANY recall | 161 = 47.2% |
| recall calls | 128 |
| classifier | v3 |

⚠ **AND IT IS NOT A CLEAN WINDOW — F-9 IS VIOLATED IN FACT.** Raised in review and confirmed against
source: T4's recall hook is registered on `SessionStart` UNCONDITIONALLY, so on a hosted install it
went live the same day, hours before T6. T4's record reads `blocked`, but that describes the record
rather than the deployment — it is `blocked` only because it is mute on a `--local` install. Two
mechanisms went live after the 7.6% baseline was taken, so **no delta from this window is
attributable to either of them.**

Nothing is un-shipped to manufacture a window that is already spent. The next clean one needs a
fresh JOINT baseline taken with both live — which `observed_at` now makes computable — and then
exactly one further mechanism. F-10 records what happened, and this is what happened.

**The after-measurement therefore cannot be a T6 delta.** What it can be is a joint after-state, and
it still needs real sessions with `minBaselineSessions = 20` as the floor.

⚠ **AND THE STORE COULD NOT HAVE SEPARATED THE TWO WINDOWS.** Asked at the moment the window opened
how the after-measurement would know which rows were after, the answer was: it would not. Every
observation was UNDATED, so a store holding both windows answers "the rate over everything ever
recorded" and nothing else — the delta F-10 requires is not computable from it. The whole
before/ship-one/after design rested on a field that did not exist.

`observed_at` (RFC3339 UTC) is on every observation now, pinned by
`TestAnObservationCanBePlacedInAMeasurementWindow` with two mutants: the clock read from the wrong
place, and a format nothing can parse. Additive, so `preceded_by_recall` and the v3 stamp are
untouched. Rows written before today carry no `observed_at`, which is the correct reading — they are
the pre-T6 window by construction.

### Superseded: the v2 baseline, 2026-08-28

**27.6%** — of 221 no-change assertions across 46 sessions, 61 were preceded by a recall.

| | |
|---|---|
| sessions | 46 |
| assertions | 221 |
| preceded by a recall | 61 |
| **rate** | **27.6%** |
| classifier-v2 | v2 |
| **precision** | **48%** (12/25 hand-judged, 2026-08-27) |
| window | 2026-08-01 .. 2026-08-28 |

⚠ **THE PRECISION IS NOT A FOOTNOTE.** At 48%, roughly 110 of those 221 sentences are not the class,
so the 27.6% is a blend of the real rate and whatever rate the noise class happens to sit at —
measured at ~15% for the noise that could be isolated. The true rate on genuine assertions is
plausibly nearer 40%. **Do not quote 27.6% without 48% beside it**, and do not compare it against
any rate taken under a different classifier version (F-16).

**What this number is for:** the mechanisms in T3-T6 ship one per measurement window and are judged
against it. A mechanism that does not move it is recorded as not shown to work (F-10), which is the
outcome that retires an idea rather than extending it. At 48% precision an effect is attenuated by
roughly half, so a real improvement will show smaller than it is — an argument for measuring more
sessions per window, not for adjusting the number afterwards.

**Two narrowings were built, measured and rejected** before settling here; both traded away most of
the true class for a better-looking precision figure. See ADR-041 T1's evaluation sections.


## From ADR-001 (recall answers or abstains)

- **Contradiction reporting** — recall says "this changed on `<date>`: it was X, it is now Y".
  Blocked on a populated temporal knowledge graph: measured 2026-08-18 on the pre-reset palace, ~65
  triples against ~5,020 drawers, so the mechanism existed and was unfed. Post-reset (2026-08-20)
  the ratio inverted — 41 triples against 80 drawers — so the blocker is now corpus size, not
  extraction coverage. Revisit once `kg-extract` has run at corpus scale.
- **Write-time findability gate** — when a memory is filed, generate the question it answers and
  try to retrieve it; report at write time when a memory is unfindable at birth. Reuses ADR-001's
  calibration, so it is drafted after ADR-001 ships rather than beside it.
- **Continuous evaluation with automatic promotion** — shadow-run competing retrieval
  configurations against real traffic and promote the winner when a paired test clears. Blocked on
  real-query telemetry volume: `search_events` held ~10 rows on the pre-reset palace, which is why
  the `--style real` eval arm produced n=4; it holds 25 as of 2026-08-20.
- **Learned multi-feature abstention** — a classifier over score, margin, distance and lexical
  coverage rather than one threshold. Blocked on labels: the 21 verified-absent cases the pre-reset corpus
  produced cannot fit and hold out. Revisit above ~200, and only if it beats the one-float-per-backend baseline on the same
  risk–coverage curve.
- **Growing the verified-absent corpus** beyond what a single `--n` run produces, including whether
  hard negatives can be mined from real queries instead of generated.
- **Reading recorded verdicts back for production calibration** — ADR-001 records the verdict in
  `search_events`; nothing consumes it yet. That consumption is the same loop as continuous
  evaluation above and should land with it.

## Standing: the instrument is not allowed to decide the hypothesis space

The eval scores ranked lists by MRR, which is IR's framing — retrieve documents, rank them, score
the rank. That framing has already acted as a filter on what we consider worth building: an idea
was counted DOWN in a design review for being "unmeasurable by an eval that scores ranked lists",
which is the instrument choosing the experiments rather than the other way round.

It is also why a published "raw chunked storage beats summarisation" result read as a verdict on
consolidation when it is a recall result — and raw text is a superset of any summary of it, so a
superset cannot lose that metric. We built our measuring stick from the same tradition whose limits
we are trying to get past.

The rule is therefore NOT "measure before you default" — that one earns its keep every week. It is:
**when a claim does not fit the instrument, extend the instrument.** Never read "we cannot measure
it" as "it is not worth building"; read it as a gap in the harness.

Metrics the harness still cannot express, each blocking a class of idea:

- **Answer-support / tokens-to-answer** — a metric a superset cannot automatically win, which is
  the precondition for evaluating any consolidation or compression idea honestly.
- **Findability-at-write** — whether a memory can be retrieved by the question it answers, measured
  when it is filed rather than in an eval weeks later.
- **Retrieval-conditioned value** — which memories actually get used, from `search_events`, so
  consolidation can be driven by what is ASKED FOR rather than by what was written. No published
  memory benchmark can express this: a benchmark runs once and has no usage history. We are a
  service and do.
- **Non-ranking outcomes generally** — abstention quality (in progress, ADR-001) and supersession
  correctness (in progress, ADR-004) are the first two; they should not be the last.

## Candidate pool should be a measured ceiling, not a constant

`DefaultRerankPool = 50`, `DefaultSearchLimit = 5`, `MaxSearchLimit = 100` and
`hybridCandidateMultiplier = 3` are the same numbers on a 5,000-drawer palace and on one
orders of magnitude larger. The retrieval reach they buy is not the same:

Measured 2026-08-18, before the reset:

- large corpus, `--pool 50`: 3 of 30 answers outside the pool (~10% unreachable)
- large corpus, `--pool 128`: 1 of 30 (~3%)
- our corpus then (45x smaller, ~5,020 drawers), `--pool 20`: 1 of 40 (~2.5%)

A small palace reaches ~97% of its answers with a pool of 20; the large one needs ~128 for the
same reach. One constant is wrong for one of them by roughly a factor of six.

Three quantities are currently conflated under one idea of a "limit", and they scale differently:

- **candidate pool** — bounds what is reachable at all; should scale with corpus.
- **rerank pool** — bounded by cross-encoder inference cost, which is linear in pool size, NOT by
  corpus. Scaling it with the corpus makes latency scale with the corpus, which is the thing a
  vector index exists to avoid.
- **page returned to the agent** — bounded by the consumer's context budget. Should NOT scale with
  corpus at all: more results from a bigger palace is more to be wrong about.

The proposal is deliberately not `pool = f(N)`, which would be a new inherited constant with an
exponent bolted on. It is a **target retrieval ceiling** — declare that some share of answers must
be in the pool, and let the pool be whatever achieves it on this corpus, measured by the retrieval
ceiling the eval now reports. Same cure as `max_distance`, the BM25 normaliser and `rerankWeight`:
replace a number somebody typed once with an operating point somebody measured.

Note the coupling before changing either: when the candidate pool exceeds the rerank pool, fusion
decides which candidates the cross-encoder ever sees. Growing one without the other silently hands
more of the decision to the weaker signal.

## The product is a runtime quality control plane, not an eval score

Forty generated cases are a release guardrail. They caught real wiring defects this week — a dead
eval arm, chunk-level gold, a production arm measuring a limit nobody uses — and they cannot
establish production quality, because the thing that degrades in production is not the ranking
function. It is everything around it as the index, the traffic, the tenants and the models change.

What `search_events` records today: wing, room, query, candidate count, hit count, top score,
whether a reranker was configured, and a timestamp. That answers almost none of the questions a
running memory service has to answer:

- is the index fresh and complete, and what fraction is pending embedding?
- is candidate recall degrading as the corpus grows? (measurable without labels — see below)
- which stages actually ran, which failed OPEN, which were bypassed?
- what are the embed / vector-search / rerank / total latencies, per stage?
- are the score, margin and no-answer distributions drifting?
- which tenant, backend, ranking profile, index size and model version produced this behaviour?

Three primitives unlock all of it, in dependency order.

**Status, 2026-08-25.** Two of the three landed with the OpenTelemetry work (#52, merged as
`26f6531`), and the third is now ADR-028. **#2 is delivered in full**: 25 semantic stages report
`ran | bypassed | failed_open | failed_closed` with 15 reasons, and `scripts/redeploy.sh` fails a
deploy whose smoke search leaves no span. **#1 is delivered on the SPAN** (`am.profile_id` in
`searchAttrs`) and not on the durable `search_events` row, which is a migration and is deferred
below. **#3 is ADR-028** — the paragraph below is the brief it was written from, kept because the
argument for why this signal is the one that scales is not restated in the ADR.

**1. Profile identity on every event.** A `profile_id` covering candidate-pool configuration,
fusion mode, lexical normaliser and weight, closet scale, rerank model/backend/blend, and index
version. Without it no drift signal is interpretable and no calibration can state what it is valid
for — an abstention threshold should say "valid for profile X", never "valid for TEI".

**2. Stage outcomes, so failing open is visible.** Every stage records ran / bypassed / failed-open
with its latency. Reranking currently falls back to the fused order on error and says so only in a
log line — the exact defect class that shipped an inert reranker in a release and printed a full
table of "reranked" numbers that were the hybrid order.

**3. Implicit relevance feedback — the one that scales.** Return a `search_id` with every recall
and accept it on `am_get_drawer`. Then an agent fetching a memory in full after a search is a
click; an immediate reformulation is a miss; abandonment is a miss. Web search has run on this
signal for twenty-five years. No agent-memory benchmark can produce it, because a benchmark has no
users — and it is the only source of relevance judgement that grows with usage instead of with our
labelling budget. It also measures the thing that actually matters: whether agents keep using
recall because it earns its place in their context.

Pool-recall degradation is measurable without labels too. If the cross-encoder frequently promotes
a candidate from deep in the fused order, the pool boundary is binding and should grow; if it never
promotes below rank ten, the pool is oversized and is being paid for in latency. That is a
self-tuning signal for the candidate pool, from production traffic, with no gold anywhere.

The loop the product actually needs is serve → observe → detect drift → shadow alternatives →
canary → promote or roll back. Offline eval sits inside that loop; it does not own it.

And "every capability exercised" should not mean equal traffic — `am_status` should outrank
`am_delete_wing` by orders of magnitude. It should mean every enabled component proves it ran,
exposes its cost and its effect, and can be turned off when it adds neither.

## Unused core capabilities — what the palace offers and nobody calls

Audited 2026-08-20 against a live palace of 80 drawers across 8 wings, one day after a full reset.
The drawer count moves by tens per day while sessions refile, so read it as a snapshot; the zeros
below were re-confirmed against the same palace at 80 drawers.
The server registers 41 tools; roughly eight are in regular use. What is built, working, and idle:

| capability | live count | why it is idle |
|---|---|---|
| closets | **0** | Built by `am_mine` only, and mining is retired for now — the prior it feeds measured harmful on mined corpora (~0.10 MRR) and `CLOSET_BOOST` defaults to 0. The summary index itself is untested against a curated corpus, which is a different question from the ranking prior and has never been asked. |
| hallways | **0** | ⚠ The 2026-08-20 reason — an empty `entities` column on every drawer — is NO LONGER TRUE and the correction is in item 2 below. `Service.Add` writes entities (ADR-016). Still 0, for a different reason: the extractor yields too few and too generic entities for any pair to co-occur in the two drawers `hallwayMinCount` requires. |
| tunnels | **0** | Explicit tunnels have never been created by a session, and derived ones cannot exist: `entityTunnelsForWing` (`internal/palace/tunnel.go:180`) takes hallways as its input, so it inherits the zero above. The craft/project wing split is exactly what explicit tunnels are for, and that half is available today. |
| skills (centralised) | 2 | Was **0** for the project's whole life: every session reported `am_list_skills` empty and fell back to generic conventions while the bootstrap called loading them a hard gate, so the gate passed vacuously. `memory-orchestration` and `writing-memories` were published 2026-08-20 and sessions began loading them the same hour. `effective-go` and `cqrs` — the two this repo's protocol names by name — were published the same day, so the catalogue holds 4 and the promise in `AGENTS.md` and `CLAUDE.md` is true for the first time. |
| anchors | 5 | Used, and the cross-repo verdict bug that deleted memories is fixed. Adoption is still incidental rather than routine. |
| knowledge graph | 41 triples | Genuinely in use by sessions since the reset, but its job is undecided — ADR-004 exists to make supersession its acceptance criterion rather than recall. |
| `am_merge_wing` | first use 2026-08-20 | Folded two derived wings into one after registrations corrected. Worked exactly as documented; simply nobody had needed it before. |

Three of these are worth acting on, in order:

1. **Make the catalogue reachable on a fresh install.** The four skills exist in *this* palace
   because they were pushed by hand. A fresh `aiagentmemory install` seeds no skills at all, so
   `AGENTS.md`'s claim that `effective-go` lives in the centralised catalogue is true here and false
   everywhere else — the reachability defect one level up: the capability is finished and nothing
   selects it for a new workspace. `update-skill` is not this; it refreshes local markdown. What is
   missing is a seed path (skill bodies in the repo tree, pushed at install) plus the gate that
   naturally follows: a test failing when the protocol names a skill the tree does not carry.

2. **Decide the entity graph: feed it or retire it.** Hallways, derived entity tunnels and the
   entity half of `am_traverse` are written, tested and reachable by tool call — three MCP tools and
   a rebuild command — and all of them return nothing.

   ⚠ **THE DIAGNOSIS BELOW THIS LINE WAS WRONG FOR EIGHT DAYS, AND IT NAMED A FIX THAT HAD ALREADY
   SHIPPED.** It said the input was dry: *"`am_mine` calls `extractEntities`; `Service.Add` does not,
   so every drawer filed by `am_add_drawer` or `am_diary_write` carries an empty `entities` column,
   82 of 82 today."* ADR-016 closed that — `internal/palace/service.go:693` reads
   `Entities: extractEntities(c.Content)` and its own comment says *"Entities is the field this path
   was missing (ADR-016)."* A session picking this item up would have gone to add a line that is
   there. That is the pointer-to-nothing class this repository gates for in code and in prose,
   applied to a DIAGNOSIS, where nothing checks it at all: an entry dated once and read as current
   forever.

   **What is actually true, measured 2026-08-28 over 20 consecutive drawers in one room of this
   repo's own wing.** Entities are written; the extractor's YIELD is what starves the derivation:

   | | count |
   |---|---|
   | drawers carrying at least one entity | 13 of 20 |
   | drawers carrying **two or more** — the minimum for a pair to exist at all | **4 of 20** |
   | distinct entity pairs those four produced | 12 |
   | pairs occurring in two different drawers, which `hallwayMinCount = 2` requires | **0** |

   The method, not the answer — these numbers are a snapshot of a corpus that grows every session,
   and the whole point of this correction is that a dated figure read as current is what went wrong:

   ```bash
   # entities per drawer, over one room of this repo's wing
   aiagentmemory mcp am_list_drawers -a wing=wing_agentmemories -a room=decisions -a limit=20 \
     | python3 -c 'import sys,json,itertools,collections
   d=json.load(sys.stdin)["drawers"]
   pairs=collections.Counter()
   for x in d:
       e=sorted(set(x.get("entities") or []))
       pairs.update(itertools.combinations(e,2))
   print(sum(1 for x in d if x.get("entities")),"of",len(d),"carry an entity")
   print(sum(1 for x in d if len(x.get("entities") or [])>1),"carry two or more")
   print(len(pairs),"distinct pairs;",sum(1 for c in pairs.values() if c>=2),"reach hallwayMinCount")'
   ```

   The single most common entity is `ADR`, alone in seven of the twenty — a token that contributes
   no pair whatsoever. The rest are `DISCARDED`, `FAULT`, `GREP`, `TOKEN`, `CLI`: shouted words and
   acronyms, not subjects. The cause is `entityMinFreq = 2` (`internal/palace/entity.go:35`), which
   requires a word to REPEAT INSIDE ONE DRAWER. Prose written once mentions most of its subjects
   once, so what survives the filter is whatever the author happened to say twice — which is
   uppercase emphasis and abbreviations, and is close to orthogonal to what the memory is about.

   So the choice is unchanged but the evidence is not: this is no longer "an unreachable component
   fed by a retired path", it is **a component whose input is produced and is the wrong shape**.

   **Feed it properly:** the extractor needs to select subjects rather than repeated tokens.
   Whatever replaces it, the acceptance question is not "does a drawer carry entities" — it does —
   but "do two drawers about the same thing share a pair", which is the property `hallwayMinCount`
   actually tests and which is 0 out of 12 today.

   **Retire it:** delete the hallway/entity-tunnel derivation and the tools that expose it, and keep
   explicit tunnels only. What is not an option is leaving tools in the catalogue that answer every
   call with an empty list, because an agent reading the catalogue cannot tell that apart from a
   palace that simply has no links yet.

   ⚠ **The obvious red test is the WRONG one, and that is the trap this correction exists to stop.**
   "A drawer written through the normal path carries entities" was the right test on 2026-08-20 and
   it PASSES today over a corpus that derives no hallways at all — a gate that would have gone green
   on the first commit and reported the feature working ever since. The test that fails today is
   about co-occurrence, not presence.

   Note also that hallways are not hypothetical here: `internal/palace/hallway.go:85` records a
   production incident across **1,338** of them, so the derivation has run at scale on a corpus whose
   entities came from mining. Retiring it is a decision about the curated write path, not about
   whether the mechanism ever worked.

3. **Use explicit tunnels for the craft/project split.** Independent of the entity graph above and
   available now: a craft lesson learned in a project incident should carry a tunnel back to the
   incident that taught it, so a rule that gets challenged can be traced to its evidence. The
   protocol tells agents tunnels exist and never says when to weave one, which is why the count is
   zero on the explicit side too.

## Verified defects in the portability paths (found 2026-08-20, not yet fixed)

Found while asking a plainer question — *where does the palace's content actually live, and could we
get it back?* Both were reproduced, not inferred.

**A wing bundle restored beside its original duplicates every diary entry.** `cmd/server/wing.go`
states the feature as "a bundle is contents, not a place, so the same file can be restored beside its
original". It cannot. A diary drawer's id comes from `diaryEntryID`, which mixes in a per-write seed;
export drops the id and import re-mints it with `DrawerID`, a different hash over different inputs,
so the restored row never matches the original. Reproduced on a scratch wing: one entry written
normally, exported, imported back into the same wing — two rows, distinct ids, one distinct content.
Against the live palace that is 52 diary drawers doubling. Re-importing the same bundle into a
*fresh* wing is idempotent, which is why this was never noticed.

A second edge sits behind the same seam: `DrawerID` drops agent and topic, so two diary entries with
byte-identical content in one wing collapse to a single row on import — the opposite failure, and it
silently violates the append-only journal guarantee `diaryEntryID`'s own doc comment states.

**On a self-hosted server, no export path reaches skills, the knowledge graph, anchors, or
cross-wing tunnels.** `wing export` structurally cannot carry them — they are not bundle record
kinds. The one path that does, the data-subject archive, is mounted only on the multi-tenant
dashboard route; `serveLocal` mounts `/mcp`, `/import`, `/stats` and `/healthz` and nothing else. So
the four centralised skills, which are user-authored and seeded by no repo file, are reachable by no
backup the operator can run. `~/.claude/bin/palace-backup` works around it by copying the database
directly, which is a workaround and not the fix.

Related, and the repo's own named defect: `internal/importer` already handles a `kg` record kind,
preserving the validity window — and `wingbundle` has no such kind and never emits one. Half of KG
portability is finished and unreachable.

## The per-task acceptance guard has a false-positive mode

The guard added to every task's Acceptance fence — `! grep -qE "no tests to run|^FAIL|^--- FAIL"` —
fires when ANY package in a multi-package run reports no matching tests, even though another package
ran the task's tests perfectly well. ADR-004 T3 hit it: `./internal/palace/` ran all four,
`./cmd/server/` had none matching, and the gate called the run a failure.

`adr-verify` implements the same rule correctly and centrally: it fails only when a "nothing ran"
signature appears AND no evidence of a real run appears anywhere in the output. The per-task guards
predate that and are now both redundant and stricter than the thing they duplicate.

Removing them all would invalidate every Verification Log entry taken under them (adr-lint
rejects a `done` whose logged command no longer matches), so it is a deliberate sweep rather than a
drive-by: strip the guards, re-run adr-verify on every completed task, commit between runs. Until
then, scope a multi-package acceptance to the package that holds the tests.

⚠ **RE-MEASURED 2026-09-06: THE SWEEP IS ~7× THE SIZE THIS ENTRY SAYS.** It said "all nineteen".
Today **128** task files carry the guard and **69 of those are marked `done`**, so the sweep would
invalidate sixty-nine Verification Log entries rather than a handful — each needing its own
`adr-verify` re-run and commit. The number is not restated as a new frozen figure for the reason
this corpus keeps recording; re-measure before planning:

```
grep -l 'no tests to run|\^FAIL' docs/adr/*/tasks/T*.md | wc -l
```

The defect and the workaround are unchanged and still correct. What changed is the cost, and it
changed silently: every task authored since this entry was filed copied the guard from the template,
so the sweep grows with every task rather than staying still. That is the argument for doing it
sooner or deciding not to do it at all — a backlog item whose cost rises on its own is not one that
can be left indefinitely without that being a decision.

## MOSTLY RESOLVED 2026-09-06 — the multi-chunk refusals are gone; re-chunking on a content update is what remains (filed 2026-08-20)

⚠ **THIS ENTRY'S CENTRAL CLAIM IS NO LONGER TRUE.** It says `Update` "now refuses when the drawer
belongs to a multi-chunk memory". It does not, and the code says so in its own comment
(`internal/palace/repo.go`): *"Service.Update used to REFUSE a multi-chunk content edit and this
comment said so; ADR-038 T4 moved corrections onto supersede and ADR-045 removed the refusal's
remaining half, so the guard named here no longer exists."*

So a correction of a memory of any chunk count is ACCEPTED, addressed through any chunk's id, and
files the replacement through the chunking path. Verified in this session by superseding a
two-chunk record twice.

**The MOVE half is resolved too**, and this entry lists it as still open. `Service.Update`
(`internal/palace/service.go`) now resolves `MemoryChunks` and moves every row in one transaction;
the comment there records why the refusal was never protecting an invariant — *"it was the honest
answer of a function doing 1/N of the job, and it was the last row-scoped write path in this
package"* — and why removing it was safe: a move changes no CONTENT, so chunk boundaries and
`chunk_index` are unchanged, no row is minted or destroyed, and every fact, anchor and pinned tunnel
keeps pointing at a live id.

**WHAT ACTUALLY REMAINS** is the first of the two items, unchanged and correctly stated:
re-chunking on a CONTENT update. The same comment ends *"Re-chunking on a CONTENT update remains
unsolved and remains in the backlog"*, and it is still an ADR rather than a bug fix, because it
changes how many rows exist and which ids they carry — ADR-027's open question about a reference
into a chunk a re-chunk would delete.

⚠ **AND A SHIPPED SKILL STILL TEACHES THE REMOVED REFUSAL.** The centralised `start-here` skill
says "WHAT IS STILL REFUSED IS THE MOVE — sending only wing/room for a multi-chunk memory is
rejected", and concludes "born long = never RELOCATABLE". The server's own live `am_add_drawer`
description already contradicts it: *"a memory of ANY length can be corrected … and relocated
(wing/room moves every chunk in one transaction), so length costs you no capability — do not spend
turns trimming to fit."* A session reading the skill will size records around a constraint that was
removed, which is the cost this corpus records against stale prose. Not edited here: a centralised
skill is shared by every project and its correction is the owner's call, not a drive-by from one
repository.

Original entry, kept because the incident is the record:

## A memory is several rows and most operations treat it as one

Found in production 2026-08-20 by a session correcting one of its own memories, and reproduced here
against the running server.

`am_update_drawer` rewrote chunk 0 of a three-chunk memory and reported success. Chunks 1 and 2
stayed live with the old text, individually embedded — and a search for the subject returned the
stale chunks ABOVE the correction, with nothing marking them retracted. A memory store whose
correction competes with the text it corrects on equal footing is worse than one that refuses the
edit, so `Update` now refuses when the drawer belongs to a multi-chunk memory and says what to do
instead.

Refusing is the safe half of the fix, not the whole one. Two things are still open:

- **Re-chunking on update.** The right behaviour is to replace the whole memory, but that changes
  how many rows exist and which ids they carry, which silently invalidates every anchor, tunnel and
  knowledge-graph fact pointing at the old ones. Doing it properly means deciding what happens to
  those references, which is an ADR rather than a bug fix.
- **A wing or room MOVE split the memory** instead of contradicting it — one chunk leaves and the
  rest stay. Fixed in the same place as the content case: every patchable field is one the chunks
  must agree on. Worth recording because this release sharpened the consequence — recall now
  defaults to the registration's wing, so after a split neither wing returns the whole memory and
  nothing marks what you get as a fragment.
- ~~**`Delete` has the same shape.**~~ **Fixed.** Reproduced — deleting the parent of a two-chunk
  memory left chunk 1 live, embedded, searchable and pointing at a parent that no longer existed —
  then fixed to remove the whole memory from either end, parent or child. A delete has no reference
  ambiguity to weigh, unlike an update: the caller is removing the memory, so removing all of it is
  what they asked for. The tool now reports how many chunks went.
- ~~**`am_update_drawer` cannot set `code_anchors`.**~~ **Fixed.** `ReplaceAnchors` swaps rather than
  appends, because the case it exists for is a memory being corrected: the old anchor pins the OLD
  text, so the staleness check meant to protect the memory is what marks the correction out of date.
  An empty array clears them, which is the honest option when a rewrite no longer points at any
  particular code.

Still open from this cluster: re-chunking on update (above), which stays an ADR rather than a bug
fix because it changes which ids exist. **ADR-038 (Proposed, 2026-08-27) removes the blocker** — it
splits the id that dedupes from the id that refers, so re-chunking no longer invalidates anything
pointing at a drawer. It does NOT do the re-chunking; the open question it leaves is what happens to
a reference pointing at a non-parent chunk that a re-chunk deletes. See `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md`.

## `adr-next` announces a task the corpus records as impossible — 2026-08-28

Scanned every ADR with a tasks directory, comparing what `adr-next --all` calls READY against the
status its own `tasks/README.md` carries. **One live disagreement:** ADR-041 T3 is `blocked` in the
README and READY to `adr-next`.

The cause is that `adr-next` models two states — done and not-done — and derives done from a
Verification Log entry whose `acceptance-sha256` matches the current fence. `blocked` is not a state
it can represent, so a task recorded as impossible, with the evidence for that sitting in its own
file, is announced as the next thing to do. T3's Stop Condition fired and was honoured; a session
following the banner would rebuild against it.

This is the same shape as the finding that 9 `done` tasks read as not-done for want of a digest, and
it has the same consequence: **the corpus tells sessions to do work it has already settled.**

`adr-next` is in quality-harness, not this repository, so this is filed under the entry below rather
than fixed here. What IS repo-side, and is done: T3's stop is now a tool-written Verification Log
entry rather than only prose, so the evidence chain carries it.

## The ADR evidence chain depends on a tool outside the repository

Raised by review, and worth stating plainly rather than leaving implicit.

`adr-verify` lives in a personal harness directory, not in this tree. It is what runs each task's
Acceptance fence, writes the Verification Log entry, and — since the per-task guards were removed —
it is the only thing that fails a run whose `-run` filter matched no tests. CI cannot run it, and a
reviewer checking out this PR cannot read it.

So the acceptance commands recorded in the task files are reproducible by anyone, but the RULE that
makes a passing one meaningful is not in the artifact it certifies. Two ways out, neither taken yet
because both are a decision rather than a fix: vendor the checker into the repo so CI and reviewers
share it, or put the nothing-ran assertion back into the fences in a form that does not misfire on
multi-package runs — `go test -v` plus a check that at least one `=== RUN` appeared would do it
without the exit-code trap the first version had.


## From ADR-006 T3 (a knob that does nothing must say when)

- **A conditional-documentation gate over the compose files and `.env.example`** — `--bm25-weight`
  now names `--fusion=rrf` in its Usage, and `TestDiscoveredPairsAdmitTheirCondition` holds that
  line for every pair the sweep discovers. The operator-facing files are not covered.
  `TestDocumentedEnvVarsAreRead` already runs the READ direction — a variable a compose file
  advertises must be read by the server, which on its first run found a shipped rerank pool of 20
  the server had never read. The conditional direction is the unwritten half: `BM25_WEIGHT` can sit
  in a compose file beside `FUSION=rrf` with nothing saying it is inert there, and every existing
  gate passes. Wider than T3 because it needs a parser for three file formats rather than one flag
  table.

  Filed 2026-08-20 because T3's Out of Scope pointed here and this file did not hold it — the
  pointer resolved to a real file and the item was in neither, which is a punt that reports fine
  forever. `adr-debt` follows the pointer; it does not check that the destination received anything.

## From ADR-002 (anchor the lexical score)

- **Growing the eval corpora past the cases the original tables used** — every ADR-002 table re-runs the
  questions its saved case file holds, which is what makes a re-run comparable and also what caps it:
  growing a corpus means asking questions those tables never asked, so it is a new experiment rather than
  a longer run, and nothing measured on the grown one is comparable to the committed evidence. Check first
  whether the corpus those tables ran against still exists to be grown — mining is retired and the palace
  has been reset since. More than one ADR punts this, so collect them before starting.
- **Corpus-wide term statistics, so the lexical score stops depending on who else was retrieved** —
  `bm25ScoresAndCeiling` derives N, document frequency, IDF and average document length from the candidate
  pool it is handed, so a candidate's raw BM25 and the anchored ceiling `C` both move when a sibling is
  added or dropped; ADR-002 buys independence from *which candidate won* and explicitly not this. Blocked
  on there being no term-statistics store, and on nobody having measured how far `raw` and `C` actually
  travel between pools, so the size of the defect is unknown. It is the store a lexical first stage needs too.
- **A lexical first stage, so BM25 can nominate candidates instead of only reordering them** — every arm
  re-orders one pool nominated by vector distance, so no lexical change can alter what is reachable, only
  what is on top. There is nothing to nominate from: BM25 is computed in memory over the pool's documents
  and nothing in the tree indexes terms. The measured headroom is small and stale — 1 gold of 40 never
  entered the pool — and it is the same retrieval-ceiling number the candidate-pool section turns on, so
  it is worth doing once that ceiling is re-measured and lexical-only misses are a named share of it.
- **Recalibrating the closet boost against the rescaled fused range** — `rankFused` adds the boost in
  absolute units on top of the fused score, so an anchored normaliser, which only shrinks the lexical term,
  inflates a fixed boost by exactly `1/s`, `s = 1 − w(1 − a)`; this palace has already lost recall@1 from
  92% to 17% to that class of scale mismatch. There is nothing to recalibrate from yet: after ADR-003 T1
  the arms that would measure it carry no prior, and the anchored tables have not been run. Note the
  ADR-014 subsequently shipped `ClosetBoost: 0`, so the default path is no longer boosted; this
  recalibration remains relevant only to operators who deliberately restore the prior.

## From ADR-003 (retire the closet prior)

- **A preselected contrast for any arm pair** — `ClosetDelta` is hard-wired to one comparison,
  `hybrid+closet` minus `hybrid` over one category; the arms table's `vs best` verdicts are still
  measured against a baseline chosen from the same table. Generalising it means carrying ADR-007's
  per-contrast reporting rules too — `not measured` on a vacuous contrast, no aggregate across arm
  scopes, a case-set id per run — or the framework prints the numbers ADR-007 forbids. The first
  real customer would be ADR-002's normaliser comparison, which has to be re-taken on post-T1 arms.
- **A corpus sized for the question, and a genuinely curated palace to measure against** — both
  corpora in every closet run are whatever happened to be filed, not a design. ADR-003's curated
  cells carry a floor of 10 admitted cases and a wing that may not clear it, and those floors were
  fixed against a pre-reset palace. Growing it by hand is labelling budget; growing it by mining
  produces the mined corpus, which is the side being contrasted against. Nothing here moves until a
  decision turns on the curated cell rather than on what the docs say about it.
- **A doc-vs-code gate for every configurable default's value** — the pattern exists for exactly one
  hand-picked number: `TestCatalogSizeIsWhatTheReadmeClaims` pins the README's tool count to the real
  catalogue, and ADR-003 T5 copies it for one knob. The gates nearby prove a different thing — that a
  setting is settable and read (`TestEveryConfigFieldIsPopulatedAndRead`, `TestEveryFlagIsRead`,
  `TestDocumentedEnvVarsAreRead`), never that the number printed beside it ships. Blocked on
  extraction: a default appears in README prose, a flag table, the landing doc and the web glossary.
- **Closet summary concatenated into the indexed text at mine time** — the published +9.4% recall
  variant ADR-003 cites as corroboration and deliberately never re-derives. It is a different
  mechanism at a different stage from the rank-time prior: it changes what gets embedded, so it
  costs a re-index of every mined source and cannot ride along with a default flip. Blocked on
  having closets at all — the count was 0 on the 2026-08-20 audit and `am_mine` is idle — and on
  whether a gain measured on someone else's indexing unit survives our chunking and our model.
- **Normalising the closet boost by source fan-out** — divide it by how many drawers share the mined
  source, so one closet hit cannot lift a fifty-part session at once. ADR-003 calls this the most
  direct answer to the amplification argument it rests on, and rejects it there only because it is a
  new ranking formula with no run behind it: it has to be measured against a settled default rather
  than folded into the flip. It cannot be measured at all while no closets are filed, and nothing
  says what the divisor should be — fan-out, its log, or a cap.
- **Choosing the closet prior automatically from corpus composition** — scale it by the share of
  curated versus mined drawers, or by closet coverage, instead of shipping one global default.
  ADR-003 rejects it as unmeasured and names the precedent: `BM25_WEIGHT=auto` was an adaptive rule
  invented without a table, and it measured worse than the fixed weight on paraphrase queries until
  IDF weighting was added. It needs both corpus types measured first, and nobody has defined
  composition — drawer provenance, closet coverage: neither is a quantity the server reports today.

## From ADR-005 (deliverable handoffs)

- **The new-wing refusal on `am_diary_write`** — `am_add_drawer` refuses a first write into an empty
  wing when the room is `inbox`; the diary path has no equivalent check. A diary entry goes to the
  session's OWN wing, so the mis-naming the refusal exists for has no route in: of the 217 drawers
  measured 2026-08-20, both malformed wings were first written through the inbox path, not the diary
  one. Unknown is whether a registration carrying the wrong default wing produces the same orphan by
  another route — one observed case is what would make this worth building.
- **Mid-session inbox delivery** — `am_status` names a waiting inbox at wake-up only, so an item
  filed while a session runs stays invisible to it. Tagged `permanent` on a false premise and
  corrected: the server serves streamable HTTP and the MCP library exposes
  `SendNotificationToClient`, which nothing in this tree calls, so the transport can carry a push.
  What is genuinely unknown is the client half — whether a given harness surfaces an unsolicited
  notification to the model mid-turn — and that is answered by testing a harness, not by reasoning.
- **An inbox count for wings other than the session's own** — the `am_status` `inbox` block counts
  only the registration's default wing, deliberately: every extra count dilutes the one that
  matters, and nobody has asked for the others. It is not a purely additive change if it is ever
  wanted — ADR-008 T4 falsifies its isolation check by making one party's inbox count include
  another's, so a cross-wing count would need that test restated as scoping rather than absence.
  Worth revisiting when a surface exists that has to watch several wings at once.
- **Marking an inbox item read or closed** — the convention closes an item out by filing what was
  found, so a handled lead and an untouched one look identical to the next session and stale items
  get rediscovered. It needs per-drawer state ADR-005 does not introduce. ADR-010 (Proposed) is the
  nearest mechanism — a validity window plus a required reason — but it names no inbox, and
  read-versus-open is a different axis from current-versus-ended: an item can be read, still true,
  and still waiting. Whether one mechanism serves both is undecided.
- **A repository gate over centralised skill text** — ADR-005 T3 put the handoff naming rule into
  the two centralised skills, and no exit code here can prove it is still there: skill bodies live
  in the palace, not the tree, so the edit was accepted on a human sign-off recording each skill's
  version before and after. Blocked on the seed path described under unused core capabilities
  above; once skill bodies ship in the tree, a gate can read them the way
  `TestProtocolTextTeachesTheInboxConvention` reads the shipped protocol files.

## From ADR-007 (no number without its population)

- **The `measured` / `no effect` / `not measured` status over every preselected contrast** — ADR-007
  T2 gives the closet row a status derived from whether the corpus holds any closets at all. Nothing
  generalises it yet, because the closet pair is the only preselected contrast the eval computes;
  every other verdict it prints is against the table's own best arm. Nor is the generalisation
  mechanical: the input check is mechanism-specific — one corpus count for the closet prior, and no
  other arm pair has an equivalent single question. Revisit when a second preselected pair exists.
- **Comparing two eval runs by case-set id** — once a run stamps a content-derived case-set id into
  its record, a command could place two of them against each other: same id, same questions;
  different ids, and it refuses. Most of the value is the refusal, which is why this is not urgent —
  the id in the table header already makes a mismatch visible to whoever reads it. What a cross-run
  comparison should then compute is undecided: the paired bootstrap pairs arms inside one run over
  shared cases, and two runs over one case set still differ by corpus, configuration and code.
- **The same rule over `am_recall_stats`** — ADR-007 governs what an eval table may claim; the
  production statistic makes the same kind of claim with no population attached. Its rows span
  configurations, corpus sizes and code versions — `SearchEvent.Reranked` changes meaning at ADR-006
  T4's fix, so a rate averaged over that cutover counts two different things. Blocked on events
  carrying an identity to partition on, the profile-identity primitive this file already names.
  Whether the honest form is partitioning, refusing, or reporting `not measured` per stage is open.
- **Populating closets, so the closet contrast has an input** — `closets` is empty on both palaces
  the 2026-08-20 eval tables were taken on, so `hybrid` and `hybrid+closet` are the same arm and
  ADR-003's truth table is read off a comparison that never ran. Closets are built by `am_mine`
  alone, mining is retired, and ADR-003 tags closet mining and curation permanent out of its own
  scope, so nothing owns getting one populated. Which corpus would count is the open part: the prior
  measured harmful on mined transcripts, so a curated palace is needed and none has been defined.

## From ADR-008 (exercise the palace end to end)

- **Cross-WORKSPACE isolation is one scenario, not a class gate** — `TestScenarioAnotherWorkspaceSeesNothing`
  stands two workspaces on one database and proves four read tools do not cross the tenancy boundary, and one
  further test does the same for `am_kg_query`. Five routes out of 41 registered tools: no mutation is asked,
  nor any by-id route where the caller already holds another workspace's drawer id, nor anything that makes a
  tool added tomorrow answer at all. The wing boundary now has the broader
  `TestEveryReadToolDeclaresItsWingScope` gate; the outer boundary, which is tenancy, still does not.
  Deferred out of ADR-008 T4 as deserving its own scenarios.
- **Concurrent mutation by two parties is untested, and the harness cannot honestly test it yet** — every
  multi-party scenario in `internal/mcptest` acts in sequence, so nothing covers two registrations updating or
  deleting one memory at once. Two things are missing and the second is the blocker: there is no statement of
  what a race should do — last write wins, refuse, or supersede, which is ADR-010's question — and
  `internal/mcptest/harness.go` opens its SQLite without the server's `dbPragmas`, so it runs with no WAL and
  `busy_timeout` at 0 where the server waits five seconds. A test written there measures a database we do not ship.
- **Real-time multi-agent collaboration — two sessions mutating concurrently and observing each other live**
  — deferred out of ADR-008, whose three parties act in sequence. The shipped protocol states today's behaviour
  plainly (`clients/claude-code/bootstrap.md`: an inbox count is taken at wake-up, and "an item filed while you
  are running will not appear, because nothing pushes it"), and ADR-005 punted mid-session inbox delivery for
  the same reason. ADR-008 calls this the continuity spec's subject; no such spec is in the tree, and `WAVE.md`
  puts it outside wave 2 as `/spec-write` work whose requirements are openly undecided.
- **The CLI `mcp` adapter has no parity gate against the HTTP one** — the single divergence found by hand is
  closed (`parseArgsWithWing`, `TestCLIWingDefaultsLikeARegistration`), by giving the operator a wing default
  rather than by reading `SEARCH_SCOPE` as planned. The class is untouched: `readOnlyTools` mirrors 23 of the 41
  registered tools and nothing checks that any of the 23 answers as its HTTP twin does. ADR-008 pointed the gate
  at ADR-006, whose T4 fixed the instance and forwarded the general case here — so it was pointed twice and
  filed nowhere. Cheap while `internal/mcptest` is fresh: drive its scenarios through the CLI dispatch as well.

## From ADR-009 (tune against your own corpus)

- **The crosslingual eval style has never been run, and no ADR owns it** — `--style crosslingual` is
  implemented (`cmd/server/eval.go`, `CatCrossLingual` in `internal/palace/eval.go`) and appears in
  no table. ADR-009 T1 punted it beside `temporal` and `absent`, but only those two have homes:
  ADR-004 runs `temporal`, ADR-001's tasks run `absent`. ADR-003 excluded crosslingual from its
  deltas as dominated by the lexical weight — an assumption about a mode nobody has measured. Worth
  one run on a corpus large enough for arms to separate, to see whether ADR-002's knobs cover it.
- **A surface for a tuning result, once there is more than one** — `agentsmemory tune` (ADR-009 T3)
  prints its record and writes a file; nothing displays it, and there is nowhere obvious to put it.
  The dashboard in `internal/web` belongs to the multi-tenant path, while a self-hosted server mounts
  only `/mcp`, `/import`, `/stats` and `/healthz` (`serveLocal`, `cmd/server/main.go`) — so the
  operator who runs `tune` is exactly the one with no web surface. Blocked on `tune` existing at all,
  and worth building only once an operator has several runs to compare.
- **Tuning per wing rather than per install** — ADR-009 tunes one configuration for a whole server,
  yet wings hold corpora that differ as much as two installs do: a craft wing of short lessons and a
  project wing of long incident notes are not the same retrieval problem. Whether the optimum
  actually differs by wing is unmeasured, and that measurement comes first — a per-wing knob that
  lands on the same values everywhere is cost with no return. It also needs an answer for wings too
  small to hold any cases out.

## From ADR-010 (supersede, do not overwrite)

**Owner changed 2026-08-27: ADR-010 was superseded by ADR-038 (`docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md`), which absorbed its decision
in full. Every item below is now ADR-038's, and its Out of Scope carries them. They are left under
this heading rather than moved, so that a search for ADR-010 still finds where its obligations went.**

- **Ordering a supersession chain when history is asked for** — now ADR-038 T5, formerly ADR-010 T3 returns the chain newest-first behind `include_history`, and stops there: nothing decides whether a history response should be RANKED by relevance, or by what, once a chain runs past a handful of records. Filed because T3's Out of Scope pointed at ADR-004 as "it owns ordering" and that ADR holds nothing of the kind — it is Accepted, it measures where a stale drawer lands in DEFAULT recall as the gate on populating the graph, states "No MCP surface change" and "production ranking unchanged", and never mentions history at all. `include_history` does not exist until T3 creates it, so no ADR owns this yet and the pointer resolved to a real file that could not have received it.
- **Full event sourcing of the whole store** — an append-only log as the source of truth with current state as a projection: the stronger form of the validity window ADR-010 chose instead. Rejected there on risk rather than on merit — drawer identity already carries vectors, chunking and anchors hanging off it, and rebuilding that as a projection is a rewrite the window's benefit does not pay for. The stated trigger is a SECOND consumer of history; today the only one is the explicit history flag on recall, and nobody has written down what else would read the log. Revisit when that second consumer exists, not on principle.
- **Validity windows on diary entries** — ADR-010 gives drawers `valid_to`, `superseded_by` and a required reason; diary entries get none of them, deferred on the ground that a diary is append-only by construction so nothing overwrites an entry. This file already records the counter-evidence: `DrawerID` drops agent and topic, so two byte-identical entries in one wing collapse to a single row on import, which the portability section above calls a silent violation of the append-only guarantee `diaryEntryID`'s own doc comment states. Append-only-by-construction is therefore the premise to check first, not the reason to skip the work. The retraction half is untouched either way — an entry whose decision later reversed stays current and competes with its correction, and since there is no way to mark one ended, no instance has ever been recorded.
- **Structured reasons — a taxonomy of why something ended** — ADR-010 makes `reason` required free text on every retraction and on `am_kg_invalidate`, deliberately uncategorised, because a taxonomy chosen before there are reasons to classify is a guess. What would settle it is the corpus that field produces — median reason length plus a human reading a sample, which ADR-010 measures and which does not exist yet. The risk it would address is recorded there already: a required field an agent fills with "obsolete" buys nothing. Better tool prompting is the first remedy; a closed set only if the writing stays uninformative once there is writing to read.

## From ADR-011 (anchor prompting — withdrawn)

- **Retroactive Class-A classification of existing memories** — 179 of 270 sampled drawers (66%) make a
  claim the repository could settle and 165 of those carry no anchor: the coverage gap ADR-011 measured
  and left open. The labelled sample is the training data and it exists. Blocked on having no consumer —
  nothing reads a classification today, and building the classifier first is the unreachable-capability
  defect in its usual shape. What the sample cannot say is how far labellers agree, since four of them
  took disjoint slices and no drawer was labelled twice, or whether the 66% holds outside this palace.
- **`verified` should mean less when only a declaration line matched** — the cheapest compliant snippet is
  the symbol's declaration, the line a behavioural change never touches, so it reports `verified` on every
  recall while the behaviour it pins moves underneath — worse than no anchor, because it destroys the
  reader's calibration rather than leaving it absent. ADR-011 found it, and it is the one carve-out from
  that ADR's permanent "no change to how anchors are checked or reported". Not known whether a declaration
  is cheaply distinguishable across languages, nor whether the fix is a weaker verdict or a fifth status.

## From ADR-006 review (findings filed rather than fixed, 2026-08-20)

- **The mode-scope sweep notices an empty pair set, not a short one** — `TestDiscoveredPairsAdmitTheirCondition`
  fails when the sweep discovers zero pairs, and `TestModeScopedKnobsAreDiscovered` pins the one known
  bm25/rrf pair. Any OTHER pair going missing is unnoticed. Four concrete ways it can shorten silently:
  the fixture's rerank factory returns nil so the three rerank knobs are inert in every cell and never
  produce a pair; `RerankTimeout` is not in `sweptKnobs` at all; `values[0]` is assumed to be the
  effective default and never checked against `config.Default()`; and only pairwise cells are run, so a
  three-way interaction cannot appear. Worth doing when a knob's inertness matters more than the two
  already found: give each knob an enabling baseline plus an observable fake, and assert the expected
  inventory rather than a non-zero count.
- **Nothing stops `unobservableKnobs` from excusing an observable knob** — the exemption list requires a
  non-empty reason, no simultaneous sweep entry, and no stale field name. It does not require the knob to
  actually be unobservable. Removing `ClosetBoost` from `sweptKnobs` and adding it to the exemption list
  with any sentence passes. Two of the three current entries are questionable on the same ground:
  `RerankURL` is observable through the injected factory (`configureranking_test.go` already observes it)
  and `RerankTimeout` reaches the factory as an argument, so neither needs a live backend. The fix that
  would hold is mechanical rather than editorial: reject an exemption when varying its field changes the
  returned lines, the factory calls, or the ordering — the same test `TestFlagAliasesAreNecessary` applies
  to the alias table, where an alias is admissible only where no mechanical counterpart exists.
- **`fieldsReadBy` sees direct selectors only** — `cfg.RerankPool` is found; `c := cfg; c.RerankPool` and
  a field read inside a helper the config is passed to are not, so the universe under-reports and the
  gate goes quiet for that field. Type-checking the receiver, or failing when the Config parameter
  escapes direct field access, would close it. Not urgent while `configureRanking` reads every field
  directly, and that is exactly the condition that will change without anyone noticing.

## From ADR-012 (the agent surface enforces the role it reports)

- **The read/write split is spelled in three places and nothing compares them** — `registrar.add` vs
  `addWrite` in `internal/mcpserver`, `readOnlyTools()` in `cmd/server/mcp.go`, and
  `readOnlyRemoteTools` in `clients/claude-code/mcpcall.go`. Each is a hand-kept mirror of the same
  classification, and a tool added to one is not added to the others. ADR-012 rejected deriving the
  server's guard from the CLI list because it points the dependency the wrong way; the honest fix is
  the reverse — export the classification from the catalogue (`CatalogEntry.Write` now carries it) and
  have both adapters read it instead of restating it. Cheap now that the field exists.
- **A third privilege level, finer than read/write** — `delete_wing` is already gated by deployment
  mode rather than by role, which is a proxy for "is this a shared workspace". A real admin-only tier
  would replace that proxy. Blocked on evidence: nobody knows how the three roles are actually used,
  and a tier designed against a guess is a tier that gets granted to everyone.
- **Writes and refusals are unlogged** — a refused write returns a message to the caller and leaves no
  record, so an operator cannot see that an agent has been failing its write-back for a week, and a
  successful write names no actor beyond the drawer's own row. The audit question is larger than
  authorization and should be taken as its own ADR, not bolted onto this one.

## From the ADR gate itself (2026-08-21)

- **The Verification Log matches only the FIRST LINE of an Acceptance fence** — `adr-verify` records
  `<first line> …` and `adr-lint` compares that, so on a multi-line fence (every fence in this repo,
  since they all start with a container invocation) the `-run` filter, the grep assertions and the
  suite run can ALL change and the recorded run still "matches the current Acceptance". Three fences
  were widened today and their existing log entries would have satisfied the check unchanged; they
  were re-run only because the change was made deliberately. The fix is a hash of the whole fence
  recorded in the log entry, which means an `adr-verify` grammar change and invalidating every
  existing entry — worth doing, not worth doing casually.
- **A test named in a Tests table is now required to exist, to contain a failure path, and to be
  selected by the Acceptance filter.** The remaining hole in that chain: nothing checks the test
  actually FAILS when its subject breaks. That is what the Mutants table is for, and the Mutants
  table is prose. Requiring each `done` task to name at least one mutation, with the test that went
  red, would bind it — the objection is that a mutation cannot be re-run by a gate, so the row would
  be a claim like any other. A stronger version worth thinking about: keep one mutant per task as a
  build-tagged patch the suite can apply and assert red.

## From docs/architecture.md (2026-08-21, first version)

- **`internal/palace` is one module with four reasons to change** — storage, ranking, evaluation and
  the graph (hallways, tunnels, knowledge graph) move independently across 16k lines and 26 files.
  The module map has one row where the code has four concerns, which is the definition of a split
  candidate. Not urgent: nothing is currently blocked on it, and a split done before the eval work
  lands would have to be redone when ranking moves. Revisit when the eval milestone closes.
- **Nothing checks that a consumer-side interface stays narrower than the type it stands for** — the
  house style is to declare an interface at the consumer with the one or two methods it needs, and
  33 of 36 follow it. An interface that grows to mirror a whole service still compiles and still
  reads as a seam while being none. A gate could compare each interface's method count against the
  concrete implementation's exported method set and flag convergence.
- **`mcptest.fakeEmbedder` has no parity contract** — it returns deterministic vectors of the right
  dimension, which is enough to exercise the plumbing and nothing like a real model's geometry.
  Every end-to-end scenario's retrieval assertions therefore hold against a distance function no
  real embedder would produce. This matters most for the ranking milestone: a ranking change
  measured only against the fake is measured against nothing.
- **Three of the five dependency rules are held by the Go compiler, not by `archguard`** — `cmd/server`
  and `clients/claude-code` are `package main` and cannot be imported; `internal/store` importing a
  backend is a cycle. The rules are kept as documentation of direction and marked `heldBy: byCompiler`
  so the test does not take credit. If a future refactor makes any of them importable, the rule
  silently becomes live and nobody will notice the promotion.

## From ADR-013 (a page of memories, not chunks)

- **`search_events.Hits` changes meaning on 2026-08-21** — before this date it counted CHUNKS
  returned; after it counts distinct MEMORIES. ADR-001 calibrates its abstention threshold from these
  rows, so a calibration fitted across the boundary is fitted on two different quantities. No
  calibration has ever been run (ADR-001 is at 0 of 6), so nothing recorded is invalidated — this
  entry exists so the next reader of `am_recall_stats` can tell the two populations apart.
- **Merging the matched chunks into one snippet** — a memory that matched in four places now returns
  the best chunk plus `ChunksMatched: 4`, which tells the caller there is more without paying for it.
  Merging would need the chunks joined in order and de-overlapped (chunks overlap by construction),
  and it costs context window on every recall. Worth revisiting if callers routinely follow up with
  `am_get_drawer whole:true` — that follow-up rate is the evidence, and nothing records it yet.
- **Routing the eval's other ten arms through `Service.Search`** — ADR-013 makes production return the
  unit the eval already scores, which removes the mismatch but not the duplication: nine arms still
  fetch from `s.vectors.Search` and rank with the eval's own copy of the pipeline. A consensus round
  is deciding the shape; whatever it lands on, the gate that matters is one that makes an arm unable
  to diverge from the served pipeline silently.

## From the dead-code sweep (2026-08-21)

- **Topic tunnels were designed and never built** — `TunnelTopic TunnelKind = "topic"` was declared
  with the comment "auto-generated when two wings share a topic label", and nothing ever produced
  one. The constant is removed rather than left as a promise; `graph.go:152` converts whatever string
  the database holds, so a future producer needs no constant to exist first. Recorded here so the
  intent is not lost with the declaration: entity tunnels exist, topic tunnels were the sibling idea.
- **A trustworthy dead-export sweep needs type information** — a name-based scan over `internal/`
  reported 66 exported functions with no caller, and spot-checking six showed most were false
  positives: repository methods called from the same file, and interface implementations invoked by
  dispatch (`WebAuthnName`, `GetByName`). The five real ones in this commit came from a careful
  per-component audit, not from the scan. A `go/types`-based version — resolve each identifier,
  count call sites, treat interface satisfaction as a use — would be worth having, and until it
  exists nobody should act on the crude number.
- **`Service.Clone` is production API with only test callers** — added for the mode-scope sweep
  because every `With*` setter mutates. Not dead, but it exists for the benefit of a test, which is
  the honest reading. Either the sweep constructs services another way, or `Clone` earns a
  production use.

## From ADR-014 (the shipped default is the measured one)

- **rrf WITHOUT a reranker has no table** — the evidence for rank fusion is `rrf+rerank` winning at
  n=100, and the shipped default has no reranker configured. The combination that now ships is the
  one nobody measured. Measuring rrf against linear on at least one corpus, reranker off, is the
  single most valuable eval run outstanding.
- **ADR-003 T3's two-corpus measurement is now a check, not a gate** — it was designed to run BEFORE
  the closet default flipped and the flip happened first. It is still worth running, and the report
  must include the case where the evidence does not support what shipped; a re-measurement that can
  only confirm is not a measurement.
- **The mode-scope sweep cannot tell code-inertness from fixture-inertness** — its predicate observes
  orderings on one corpus, so "K did not move the page while D was set" also happens when D merely
  shrinks K's effect below that corpus's resolution. It observed "--bm25-weight is inert when
  --lex-norm is set", which is false in code: `rankHybridWeightedNorm` takes both. Only `--fusion` is
  confirmable structurally today (rankRRF has no weight parameter), so only `--fusion` pairs are
  enforced. Confirming a pair by checking the selected code path drops the parameter would make the
  rest enforceable.


## From ADR-015 (a wing merge must correct the search index it invalidates)

- **`DrawerID` hashes the wing, so a merge invalidates every id-derived reference** — the id is
  content-and-location derived, `MergeWing` deliberately leaves ids unchanged, and the result is a
  palace where a drawer's id encodes a wing it no longer lives in. Making the id independent of the
  wing would remove the whole class of merge-drift, and it would also rewrite every id and
  invalidate every anchor, tunnel and knowledge-graph source pointer. Too large for ADR-015; worth
  deciding deliberately rather than inheriting.
  **Taken up by ADR-038 (Proposed, 2026-08-27)**, which answers the concern without the rewrite:
  `DrawerID` still hashes the wing, but nothing derives identity from it any more, so a merge
  invalidates nothing. Close this entry when ADR-038 is executed or withdrawn.
- **The drift check looks only at `wing`** — a point's payload also carries `room`, and nothing
  compares it. `room` has no relabel path today, which is why it is not urgent, and "no path today"
  is exactly the assumption that produced the wing drift.
- **Patching payloads in bulk by filter rather than by id** — `SetPayload` takes ids because that is
  what a merge has. A backend-side filter update would make a whole-wing correction one call
  instead of N.

## From ADR-016 (a memory an agent files must be navigable)

- **Backfilling `entities` for drawers filed before the write path stamps them** — a palace will
  otherwise have a derived graph over its recent memories and nothing over its older ones. The
  extraction is pure and cheap, so a backfill is a batch job over existing rows with no model call;
  what it needs is a decision about whether it runs automatically or on request.
- **`am_recompute_graph` reports success when it derives nothing** — measured 2026-08-21 on a palace
  where every recompute was necessarily a no-op, because no drawer carried an entity. ADR-016 T3
  puts a note on the three READ tools; the write tool still reports a count of zero as though zero
  were an answer.

## From ADR-017 (a subagent is a session)

- **Codex subagent hook execution contract; pi remains hookless.**
  **REASON AMENDED 2026-08-22.** Codex CLI 0.144.5 exposes
  `SubagentStart` and `SubagentStop` as native TOML tables in `config.toml`.
  Event availability and registration shape are therefore no longer valid
  reasons to defer ADR-017. This audit did not establish the other Claude
  lifecycle events and makes no parity claim about them.
  The installer now writes its proven `Stop` checkpoint into `config.toml` and
  removes its old `hooks.json` entry; if foreign JSON hooks remain it preserves
  them and reports that Codex may keep warning about two representations.

  What remains unmeasured is the execution contract ADR-017's scripts depend on.
  Before registering either subagent hook, capture a real Codex start and stop
  and prove:
  - the payload fields used by the branches (`hook_event_name`, `agent_id`, and
    `stop_hook_active`, or their measured equivalents);
  - that `SubagentStart` stdout is injected into the dispatched subagent rather
    than printed or discarded; and
  - that exit 2 from `SubagentStop` feeds the nudge back to that subagent and
    retries at most once.

  ADR-017 T3 already showed why this is a gate: a hook can be registered, fire,
  and remain inert when the harness does not consume its output. Pi is still a
  separate permanent absence on the measured version: it has no hook system.
- **Codex subagent definitions are TOML, not markdown** — shipped 2026-08-22 (`agents/*.toml`,
  `enabled_tools` with BARE tool names under `[mcp_servers.…]`, url substituted at install time).
  Recorded here because the same split will bite the next definition anyone adds: the two dialects
  share a directory NAME and agree on nothing inside it.
- **Run the recall IN the hook and inject the RESULTS, not the instruction** — the strongest version
  of ADR-017's idea, because it removes the compliance question entirely: a subagent cannot skip a
  recall that already happened. Deferred only because the hook does not know the task, so it would
  have to guess the query. If T1 measures poor compliance, this becomes the design rather than a
  refinement.
- **Mining drops sidechains, so past subagent work is unrecoverable** — `mineclaude.go:84` filters
  `isSidechain` by design, documented as "subagent traffic, not the user's conversation". Correct for
  "mine the user's conversation" and wrong for "recover what a subagent learned"; one flag serving
  two jobs. Separating them would make already-finished subagent work minable.
- **A subagent's writes cannot be attributed** — to it, or to its dispatcher. Needs a session
  identity the palace does not record; see the recall-stats defect below, which is the same missing
  column seen from the other end.

## Recall statistics are attributed to the wrong session

Found 2026-08-21 by a peer session on this machine, which was handed a "memories to write" task list
naming failed searches in two wings it had never touched — and correctly refused to file invented
drawers for them.

`search_events` (db/migrations/00021) carries `team_id`, `wing`, `room`, `query`, counts and
`created_at`. **There is no session column.** `/stats?hours=N` (`cmd/server/main.go:1091`) therefore
filters by TEAM and TIME only, and the Stop hook's report is every search the whole palace served in
the window — on a machine running several sessions against one local server, that is every other
session's traffic reported as yours.

The hook's own comment states the opposite: *"The window is THIS SESSION, measured from the
transcript file the event names, not a fixed number of hours."* The window is computed per session
and the DATA is not filtered per session, so narrowing the window cannot separate sessions that
overlap in time. Same shape as the merge doc comment fixed the same day: a false premise justifying
a step that was never taken.

Two consequences, and the second is the serious one:

- the recall percentages are wrong, which is ADR-007's rule broken again — a number that means
  something other than what it says;
- the "memories to write" list is not a statistic but a TASK LIST, and it hands each session another
  session's gaps to fill. An agent that complies files a memory about a question it never asked, into
  a wing it never opened, from no evidence. One agent caught it. The next will not.

The fix needs a session id on `search_events` and a `session=` filter on `/stats`, which is a schema
change plus a contract change plus a hook change — an ADR, not a patch. Until then the honest
mitigation is for the hook to stop presenting the list as this session's.

## From ADR-021 (the handshake carries the protocol)

- **Claude Desktop extensions (`~/Library/Application Support/Claude/Claude Extensions/`)** as a
  packaging route instead of a config-file entry. The directory exists on the reference machine with
  several installed; its format was never established, and ADR-017 T3's lesson is not to ship
  against a shape nobody captured.
- **Windows and Linux Claude Desktop config paths** — ADR-021 T2's kit is written against the macOS
  path that was measured (`~/Library/Application Support/Claude/claude_desktop_config.json`). The
  Windows path appears in `internal/web/windows-guide.md` and was never exercised by the installer.
- **Whether other MCP clients surface `instructions` to their model at all** — measured for Claude
  Desktop in ADR-021 T3 and assumed nowhere else. Cursor, codex and Claude Code all receive the
  field now; nothing establishes that any of them shows it to the model.

## From ADR-020 (a kit for an agent that drives no CLI)

- **Cursor hooks — the Stop checkpoint and ADR-017's subagent pair** — `~/.cursor/hooks/` exists on
  the reference machine and its events, payloads and registration file were NOT established.
  ADR-020 ships no hooks for Cursor rather than registering something plausible, so a Cursor user
  reads memory and is never prompted to write it — ADR-017's asymmetry, in a new place. Capture a
  real Cursor hook payload before branching on anything, per ADR-017 T3.
- **Cursor skills (`~/.cursor/skills`) as a delivery route for centralised team skills** — the
  directory exists beside `skills-cursor`; neither was examined. `am_load_skill` is the current
  route and needs no filesystem.
- **Project-scoped Cursor installs (`.cursor/rules`, `.cursor/mcp.json` inside a repo)** — ADR-020
  installs globally, matching what the other kits do. Cursor reads a per-repository `.cursor` too,
  which is the natural home for a `--wing`-scoped registration; the other kits express that through
  `--sandbox`, which Cursor cannot support because it exposes no config-dir variable.
- **stdio / `--socket` registration for Cursor** — ADR-020 T2 writes an HTTP entry only. Cursor's
  `mcp.json` takes `command`/`args` entries as well, so a socket bridge is expressible; nobody has
  needed it.
- **Measuring whether a Cursor session actually recalls** — ADR-017 T1 measured Claude subagents
  from `search_events` with a control arm. The same measurement for Cursor needs per-client
  attribution, and ADR-018 T2's withdrawal means the server records none. Blocked on the same
  premise: a red `TestProductionStillRunsStateless`.

## From ADR-018 (a recall belongs to the session that ran it)

- **Per-session WRITE statistics — drawers filed, facts added** — it is the other half of "is memory
  earning its place": a session that recalled twenty times and filed nothing is a different story
  from one that filed ten. **BLOCKED, and the blocker is now permanent rather than a sequencing
  question.** This entry used to say "the same `session_id` column that ADR-018 puts on
  `search_events` would serve it"; ADR-018 T2 was WITHDRAWN on 2026-08-22 in favour of keeping the
  transport stateless, so that column does not exist and is not coming. There is no per-session
  anything until `TestProductionStillRunsStateless` goes red.
- **The hosted multi-workspace deployment's session model** — ADR-018 was found and is valid on the
  self-hosted single-palace shape, where several sessions share one local server. A hosted workspace
  has the same missing column and a less acute symptom, because a token is closer to a session
  there. Nobody has checked how much closer, and "less acute" is not "absent".
  Still open after T2's withdrawal, and arguably more interesting because of it: the withdrawal was
  decided on the self-hosted shape, where the transport is stateless by configuration. Whether the
  hosted deployment runs the same way has not been checked.

## From ADR-016 T2's lexicon (found by review, 2026-08-21)

**The stoplist loses real names and acronyms, and the obvious fix makes it worse.**

Inflection reduction strips `Jobs→job`, `Wells→well`, `Fields→field`, `Waters→water`, `Teams→team`,
`Fastly→fast`, `Harding→hard`. The irregular-verb section additionally removes `Drew`, `Rose`, and —
as acronyms — `RAN`, `LED`, `FED`. Every one is a real thing somebody might file a memory about.

The obvious repair is to add them to `known_systems.json`, which bypasses the stoplist entirely
(`ordinary()` is applied only to single-word candidates, AFTER the known-systems prepass masks its
matches). **That would be worse.** The known-systems matcher is `(?i)\b…\b`, so adding `LED` makes
every "this led to" an entity.

What is actually needed is a split by word CLASS, applied at different case-sensitivities:

- **Function words** (`and`, `was`, `unless`) are never entities in any casing, including shouted.
  Case-insensitive is right for them.
- **Irregular verb forms and common nouns that collide with names** (`led`, `fed`, `ran`, `rose`,
  `drew`, `teams`) are ordinary in lower or Title case and plausibly an ACRONYM or a product in all
  caps. Stripping them case-insensitively is what loses `LED` and `FED`.

That is a real design decision rather than a patch, and it is deliberately not being taken now: the
derived graph is days old, nothing depends on it yet, and the current lexicon is a large improvement
on what it replaced (ordinary words surviving fell 47/163 to 2/163 with every acronym kept). The
cost is recorded so the next person does not rediscover it, and so nobody "fixes" it via
known_systems.

Two smaller ones from the same review:

- **`Service.Update` leaves entity metadata stale.** `Add`, `WriteDiary` and `Mine` all stamp
  entities; `Update` re-embeds the content and updates only content/wing/room (`repo.go:267`). So
  editing a memory leaves the graph deriving from names the text no longer contains, and never
  seeing names it gained. Narrow today because `am_update_drawer` is rare and search is unaffected —
  only the derived graph goes stale.
- **`doctor --index` reports legitimately pending closets as missing points.** `ClosetWings` returns
  every closet without checking `embedded_at`, while `closet.go:252` deliberately creates pending
  closets with no vector — and `Pending` counts drawers only. So a palace mid-mine reports index
  corruption that is a queue. A check with false alarms is one people learn to skip, which is the
  failure mode that matters here.

- **No seam to interleave a writer inside `MergeWing`'s transaction.**
  `TestMergeCollectsAndRelabelsInOneTransaction` asserts the invariant — nothing ends with its row
  in one wing and its payload in another — but files both drawers BEFORE the merge, so it would
  still pass with the transaction removed. The transaction is correct (a reviewer confirmed SQLite
  gives serializable writes on success; a concurrent writer aborts the merge with
  `SQLITE_BUSY_SNAPSHOT` rather than corrupting it), but nothing PROVES it from the test suite. A
  hook that lets a test commit between the SELECT and the UPDATE would; adding one to production
  code purely for a test is the trade to weigh.

## From ADR-019 (the agent sees a quarter of the memory)

- **Let a cross-encoder choose the snippet window.** A cross-encoder scores a query against a
  passage, which is exactly "which part of this memory answers the question" — the same model
  already reranking the page, asked a question it is better suited to than term counting. Deferred
  because it costs an inference per candidate window and the rerank pool is already the slowest step
  in a search, and because the cheap version (rank the windows by term match, show more than one)
  has not been measured yet. If ADR-019 T1 finds the term-matching chooser picks the wrong window
  often, this is the next thing to try rather than a refinement of it.
- **Acting on coverage inside the server — abstaining, or auto-fetching a low-coverage hit.** Once a
  page reports how much of each memory it is showing, the server could refuse to answer below a
  threshold or silently fetch more. Deferred deliberately: the agent has the question and the
  server has the corpus, and the page's job is to make the agent's decision possible rather than to
  take it. Worth revisiting only with evidence that agents do not act on the signal — which is the
  same compliance question ADR-017 is measuring.
- **Wing scoping is 5 of 32 and untouched by anything in ADR-019.** All four hard failures in the
  first measurement and five in the second were queries scoped to a wing that does not hold the
  answer. The empty-wing note (ADR-013) makes two of them actionable — it tells the agent the wing
  is empty and names a near neighbour — and it does not put the fact on the page. The open question
  is whether a scoped search that finds nothing should widen automatically, which is a product
  decision about whether scoping is a filter or a preference, and nobody has taken it.

## From ADR-025 (executable contract axes)

- **A live-dependency integration cohort** — Qdrant, TEI, OAuth and model quality cannot be
  treated as hermetic, so the contract axes exclude them. Binding them needs a separate cohort
  with typed dependencies, run against real services rather than substitutes, and it is a
  different instrument from the in-process axis runner: an axis proves a selection is reachable,
  where this would prove an external boundary still behaves. Deferred from ADR-025's Out of Scope
  on 2026-08-25, when the disposition was given a receipt it had been missing.

## From ADR-028 (return the identifier and the score a recall was decided by)

ADR-028 ships the two halves that cross the tool boundary — `search_id` returned by `am_search` and
accepted by `am_get_drawer`, and `blended_score` on every hit. These three are what it deliberately
did not ship, each with the reason it was held back rather than the intention to get to it.

- ✅ **DONE 2026-08-29 — record the fetch against the recall.** Shipped as ADR-028 T3: `drawer_fetches`, `RecordFetch`, and `fetches` / `recalls_fetched` published on `am_recall_stats` so the write is observable through a served tool. The RATIO is not shipped and is now ADR-028 T4, because its denominator is recalls THAT WERE LOGGED and it needs `profile_id` beside it. ⚠ **The trigger below fired and could not be observed** — a non-test client sent a `search_id` on 2026-08-29 and nothing recorded it, since the id reached only a sampled span, and no first-party client calls `am_get_drawer` at all. The original text is kept because the trigger's failure mode is worth more than the trigger was.
- **Record the fetch against the recall, and report the ratio.** The consuming half of primitive #3:
  a fetch that names a `search_id` is a relevance click, and the ratio of recalls followed by a fetch
  is the first usage signal this palace has ever had. Held back because the precondition does not
  exist yet — nothing sends an id until ADR-028 T1 ships and a client adopts it, and a report built
  first would be measuring an empty set. **Trigger: the first week `am_get_drawer` receives a
  non-empty `search_id` from a client that is not a test.** If a year passes and no id ever arrives,
  the honest outcome is to REMOVE the argument, and that result is worth as much as the report.

- **`profile_id` on the durable `search_events` row.** Primitive #1's other half. It is on the span
  today, which makes a sampled trace interpretable, and absent from the durable row, which makes a
  ratio uninterpretable — "38% of recalls were followed by a fetch" means nothing without knowing
  which ranking profile produced them. A column addition, so it is a migration and belongs with the
  recording task above rather than with ADR-028's surface changes.

- **A relevance metric derived from the fetch signal.** Deliberately last. The signal has to exist
  and be observed before anything is derived from it; deriving a metric from a signal nobody has
  seen is how the eval acquired arms that measured configurations nobody ran.

## From ADR-029 (a trace that cannot lie about what it did)

A five-lens sweep of the search path on 2026-08-25 against `dcc1389` returned thirty findings; the
adversarial pass **confirmed sixteen and refuted fourteen**, and five of ADR-029's original seven
"lies" were among the refuted (see that ADR's amendment). These are the CONFIRMED findings ADR-029
does not take — real, verified, and held back with the reason, not the intention. Corrected
2026-08-25: an earlier version of this section said "thirty findings, each adversarially verified",
which reads thirty as a finding count. It is not.

- **Backend identity on the span. — RECEIVED 2026-08-26, the `VECTOR_BACKEND` half is delivered.**
  `am.vector_backend` is on the search span as of `6631dc1`, via a `VectorDescriber` optional
  interface implemented by sqlitevec, qdrant, chromemvec and Hybrid — the last naming BOTH halves
  (`hybrid(sqlitevec->qdrant)`), because a hybrid's two stores can disagree and a string naming one
  of them reads identically either way. It is in the explicit knob list, so removing it fails
  `TestKnobsThatDecideThePageAreAllOnTheParentSpan`, and `var _ VectorDescriber` assertions fail by
  name if any production store stops describing itself.
  It did NOT need its own ADR in the end: it turned out to be one attribute and an optional
  interface already used twice on this branch, not the `cmd/server/main.go` wiring change this entry
  predicted. The trigger it named — "the next eval table anyone intends to compare across a config
  change" — is what fired.
  The `EMBED_BACKEND` half was refuted in ADR-029 rather than delivered here; the embed span
  separately gained backend, model and input window via `DescribeEmbedder`, which is more than this
  entry asked for and does not change that refutation.
  Original text follows.

- **Backend identity on the span.** `VECTOR_BACKEND` selects sqlite brute force, embedded chromem or
  Qdrant over HTTP, and no search span names the one that ran; the three are not equivalent, since
  chromem clamps `k` to the collection size. `EMBED_BACKEND` and the embedding model are worse:
  they decide what every distance in every trace and every eval table MEANS, and both default paths
  serve the same dimension count, so the one attribute the embed span carries (`am.dim`) cannot
  separate them. This is the highest-consequence item the sweep found. Held back from ADR-029 only
  because it is `cmd/server/main.go` wiring rather than the search path, so it earns its own record.
  **Trigger: the next ADR that touches the embed or retrieve wiring, or the next eval table anyone
  intends to compare across a config change.**

- **The adaptive BM25 weight's resolved value.** Under `FUSION=linear` with `BM25Weight=auto`,
  `adaptiveBM25Weight(query, docs, base) = base × LexicalCoverage(query, docs)` is recomputed per
  query, and the fusion span carries `am.bm25_auto`, `am.bm25_idf`, `am.lex_norm` and `am.bm25_base`
  — that auto is ON and what the base was, never what it resolved to for this query. Held back
  because it makes the trace incomplete, not wrong.

- **The whole-memory degradation that lives only in prose.** The search handler silently degrades
  whole-memory requests to a 400-rune window once a page exceeds `wholeMemoryBudget`, and the fact
  reaches the caller as a `note` string and reaches no span at all. Held back with the same
  reasoning, and noted here because a prose field is exactly the shape this repository has ruled
  is not load-bearing.

- **`SearchQuery.Context` presence on the rerank span.** The context is concatenated onto the query
  handed to the cross-encoder and changes the served order; `am_search` advertises that it "sharpens
  re-ranking when a reranker is configured; ignored otherwise", and neither branch of that promise
  is observable.

- **The coerced-to-zero cosine rejection.** In semantic evidence selection, `similarity, ok :=
  cosineSimilarity(...); if !ok { similarity = 0 }` emits nothing, so a degraded embedder's
  non-finite vectors and a deliberately blank window produce the same score. `Span.Event` exists for
  exactly this and is unused here.

- **`closetBoostsAt`'s three discard paths.** A purged row, a duplicate source, and a distance past
  `closetDistanceCap` all drop a retrieved closet, and the span ends `ran` carrying only
  `am.count=len(boosts)` — `len(hits)` is recorded nowhere. So `am.count=0` reads identically for
  "the team has never mined" and "five closets were retrieved and every one was thrown away".

- **The evidence stage's window counts.** `am.pool` counts DOCUMENTS; the unit that determines the
  stage's cost is the window, and the file's own comment notes a five-thousand-rune memory yields
  seventeen of them. How many were generated, embedded, or discarded past
  `maxMemoryEvidenceRegions` is recorded nowhere.

- **An anchor/staleness stage.** The anchor pass has no span at all, so `SearchStages()` can never
  catch its absence. ADR-029 T1 makes its FAILURE visible on the enclosing tool span; giving it a
  stage of its own is a new stage rather than a list repair. **Trigger: the next time a stale flag
  is wrong in production and nobody can tell from a trace whether the lookup ran.**

- **Telling the CALLER that anchors failed, or that a wing lookup failed.** ADR-029 T1 makes both
  visible in the trace only. Surfacing them in the `am_search` response is a contract change and
  needs its own record. **Trigger: the first support question that turns out to be a silently
  unflagged stale page.**

- **Acting on a non-zero out-of-scope drop count.** ADR-029 T2 makes it visible, and it is an alarm
  rather than a metric: a non-zero count means the vector index and the durable rows have diverged.
  What the server should DO about that — refuse, repair, warn — has a blast radius this ADR does not
  take on. **Trigger: the first non-zero count observed in the deployed container.**


## From ADR-030 (a blend that cannot tell confidence from noise)

- **Persist `blended_score` to `search_events`.** ADR-028 T2 put it on the wire; the durable row still
  records only `top_score` and `reranked`. Without it the tie rate cannot be measured retrospectively,
  so ADR-030's 17.6% is an EXPOSURE figure (pages small enough for the pool to be degenerate) and not
  an incidence. A migration, and ADR-030 T1's fixture answers the same question about the present
  without one. **Trigger: the first time someone wants to know how often the blend actually tied.**

- **`max_distance` as a pool shrinker.** Measured live on 2026-08-25: `max_distance=0.45` cut the
  candidate pool from 10 to 3, and a pool of 3 is where min-max normalisation is most degenerate. The
  corpus already holds a decision drawer reading "max_distance is DEAD as a confidence signal — on 61
  cases the answerable/unanswerable top-1 cosine distributions overlap", matching ADR-001's table
  (medians 0.401 vs 0.423). So the knob is both useless as a confidence signal AND actively harmful to
  the ranking that follows it. Whether to floor it, change its default, or remove it is its own
  decision. **Trigger: ADR-030 T1's measurement, which will show how much the small-pool case costs.**

- **Re-examine every default set by the eval's weight sweep against the pool-size distribution
  production actually serves.** `RerankWeight: 0.5` is annotated "chosen by the eval's weight sweep",
  and the sweep ran at pools of 128 and 10 while 17.6% of real reranked recalls run at four or fewer.
  The general question — for any normalisation or threshold here, does the tuning fixture span the
  range production serves? — was answered "no" once and has not been asked of the others.

## From ADR-031 (keep the one score that separates a recall that worked)

- **An abstention threshold, calibrated on `top_rerank_score`.** ADR-031 keeps the signal; spending
  it is ADR-001's T3, which stays BLOCKED on its own preflight — a corpus measuring 100% in-pool is
  saturated and the go/no-go cannot be taken there in either direction. **Trigger: a corpus with hard
  identifier-preserving negatives and a retrieval ceiling under saturation, plus enough reranked rows
  to plot the answered-versus-unanswered distribution against ADR-001's table.**

- **Changing `FUSION` away from `rrf` so the fused score carries magnitude again.** Reciprocal rank
  fusion discards magnitude at retrieval on both arms, which is why `top_score`'s top-1 range is only
  0.0275..0.0328. A linear fusion would keep it. This changes the SERVED ORDERING, and the eval of
  2026-08-25 cannot support a change of that size at n=30 — every arm's verdict was "inconclusive vs
  best (CI spans zero)". **Trigger: an eval corpus large enough for a paired comparison to resolve.**

- **Removing `avg_top_score` from `am_recall_stats`.** Under `rrf` it is an average of a
  near-constant, so it invites a conclusion it cannot support. It is NOT wrong for a `FUSION=linear`
  deployment, and it may be on somebody's dashboard. Its doc comment now states its own limitation.
  **Trigger: `FUSION=rrf` becoming the only supported fusion, or a confirmed report that nobody reads
  the field.**

- **The 2026-08-25 eval's uncomfortable headline, unresolved.** On that 30-case replay, plain
  `vector` scored MRR 0.644 and `production (Search)` scored 0.592 with 7 golds ranked below the page
  cut — the whole ranking stack underperformed doing nothing. Three reasons not to act on it: n=30,
  questions generated FROM the drawers (which flatters vector similarity by construction), and
  ADR-001's finding that this corpus is saturated. It is recorded because an unexplained result that
  nobody writes down gets rediscovered every quarter. **Trigger: the next eval on a corpus that is
  not generated from the memories it searches.**

## From ADR-032 (the corpus that chose our defaults could not disagree with them)

- **The 14-of-40 unanswered real queries.** The largest single number in the 2026-08-25 real run
  and the least interpretable: the judge sees only the RETRIEVED POOL, so "no relevant memory"
  conflates a memory that is not there with one the judge missed. Four of the fourteen were the same
  question re-asked (`mutatesOnlyTempPaths temp-write exemption`), which is the "questions the team
  should have written and did not" signal `search_events` was built for — but separating a write gap
  from a retrieval miss needs an instrument that does not exist. **Trigger: the next time somebody
  wants to quote a recall-failure rate.**

- **A stronger judge than `qwen2.5-coder:7b`.** It bounds every ABSOLUTE number in the real table
  ("85% recall@5" is judge-limited) though not the arm-vs-arm comparisons, since every arm faces the
  same gold. **Trigger: publishing an absolute recall figure, or a run whose verdict hinges on cases
  the judge scored inconsistently.**

- **The recalls that never happened.** An agent that does not know a framework exists never searches
  for it — "you cannot retrieve what you do not know to ask for" — so no corpus built from
  `search_events` can contain that case, and no eval can see it. It is the one failure mode on
  ADR-032's subject with NO METRIC AT ALL, and the reason the push channel (`llm_init`, protocol
  files, centralised skills) exists on convention rather than on measurement. Naming it is the most
  that can be done honestly today. **Trigger: any proposal to reduce what is loaded unconditionally,
  since that is the only lever whose cost this blind spot hides.**

- **Re-examine every default annotated as "measured".** Two are named in ADR-032 (`Fusion`,
  `RerankWeight`); a sweep of `config.Default()` for comments claiming a measurement would say
  whether there are more. ADR-032 T2's `TestShippedDefaultsCiteTheirCorpus` is the mechanical
  version of this question. **Trigger: T2 landing.**

- **Make `--style real` the corpus the eval documentation leads with.** `cmd/server/eval.go`'s
  Description still presents the generated styles first, which is how a fixture that cannot exhibit
  the defect became the one that picked two shipped defaults. **Trigger: ADR-032 T2 reporting,
  either way.**

## From ADR-032 T1 (the null result, 2026-08-26)

- **`TestShippedDefaultsCiteTheirCorpus`.** Planned for T2 and NOT written, because it belongs with a
  default change and there was none. Every `config.Default()` field whose comment claims it was
  measured should name the case-set id it was measured on — `Fusion` and `RerankWeight` say "chosen
  by the eval's weight sweep" and name no corpus, which is how a measurement outlived the corpus that
  produced it. Worth doing on its own. **Trigger: the next change to any default annotated "measured".**

- **The 3 answers no arm retrieved (n=54 run).** The first corpus of three that is not saturated —
  94% in-pool against 100% for both earlier runs — so for the first time there are genuine RETRIEVAL
  failures, distinct from ranking ones. No reranker can reach them. The run's own advice: raise
  `--pool` and re-run; if they come back the pool was too small, if they stay missing the embedding is
  not placing those memories near their question. **Trigger: any work on retrieval rather than ranking
  — this is the only measured evidence of which of the two is failing.**

- **The 5 golds `production` lost below its page cut.** Retrieved and ranked, then cut by the page
  size rather than by the pool. The knobs are the search limit and `RERANK_POOL`, not `--pool`. This
  is a different failure from the 3 above and the table separates them. **Trigger: a complaint that a
  recall "missed something obvious" — this is the shape that produces it.**

- **`rerank blend w=0.25` is the top arm in BOTH real runs** (0.761 at n=26, 0.694 at n=54) against a
  shipped 0.50, and remains unresolved: it is the arm each table selected, so the comparison flatters
  it, and `w=0.50` is inconclusive against it in both. It is the strongest surviving hint about a
  shipped default. **Trigger: a run designed to test it specifically, with the contrast preselected
  rather than read off the winner column.**

## From ADR-032 trial 2 (the pool-width test, 2026-08-26)

- **`Search`'s retrieve floor is too narrow, and it is the first measured, actionable recall finding
  this corpus produced.** A paired pool 30 → 100 re-run lifted every eval arm by ~0.05 MRR and left
  `production (Search)` at exactly 0.660 with 8 misses, because `candidateKFor(limit, …)` computes its
  own fetch width from `limit×3` (raised to `RERANK_POOL`) and cannot see `--pool`. Its misses are
  golds retrieved and ranked, then cut by the PAGE: `limit=10` removes three of the eight,
  `retrieve-k=50` two. **Trigger: this is the next change to make, and it wants its own ADR — the
  knobs are `DefaultSearchLimit` and `RetrieveK`, both served-path defaults.**

- **Re-measure everything previously measured at pool 30.** The closet prior's cost was −0.048,
  −0.039 and −0.027 across three runs and **−0.002 with Δrecall@1 +0.000** once the pool widened —
  so three agreeing runs were weaker evidence than they looked, because all three shared a pool
  width nobody was varying. Any other conclusion drawn at pool 30 inherits the same doubt.
  **Trigger: before citing any pre-2026-08-26 eval number as settled.**

- **The latency cost of a wider pool is unmeasured.** Trial 2 shows what pool 100 buys in QUALITY and
  says nothing about what it costs in hydration and rerank time. A recall that is better and twice as
  slow is a different trade, and the eval does not report it. **Trigger: any proposal to raise the
  served retrieve floor — which is the item above, so this blocks it.**

## From ADR-034

Deferred by `docs/adr/ADR-034-a-degraded-ranking-you-can-count.md`, written here in the same commit
as the deferral so the pointer has a receiving end.

- **The `RERANK_POOL` / `RERANK_TIMEOUT` defaults.** Measured 2026-08-26 on a CPU cross-encoder over
  the 54-case real corpus: 60 rerank calls at pool 20 took mean 11.4s (min 7.3s, max 18.2s), and a
  second run the same day averaged ~17s with calls to 19.7s, against a shipped `RERANK_TIMEOUT` of
  10s. Pool 20 is one batch (`maxBatch` 32), so that is the cost of scoring 20 documents, not
  batching overhead. **The shipped default is pool 10 and has never been measured on this hardware**,
  so none of the above is a verdict on it and the default is deliberately unchanged.
  **HALF RECEIVED 2026-08-26 — pool 10 is measured and the default is safe.** 12 real recalls
  through the live server on an idle CPU cross-encoder: mean 4.3s, min 3.3s, max 5.5s, none past
  the 10s budget — about 2.3x headroom. Scaling to pool 20 costs 2.7x the time for 2x the
  documents, so cost is superlinear in pool and a per-doc model understates the risk of raising
  it. Recorded in the `RERANK_POOL` comment in `docker-compose.full.yml`.

  Two figures stated earlier that day were wrong, both from n=1 and both flattering the default:
  a single 2721ms sample (the mean is 4332ms, so headroom is 2.3x not 3.7x) and an inferred 4.3x
  scaling (measured 2.7x).

  **Still open: the first non-zero `timeout` count from ADR-034's column** — the lagging
  indicator, which needs real traffic rather than a bench.

- **A runtime warning when a rerank call approaches its budget.** A leading indicator rather than a
  lagging one, and cheap. It needs a threshold, and nobody can name a defensible threshold until the
  fail-open rate is known. **Trigger: ADR-034's `rerank_skip_reason` column having a week of real
  data.**
## From ADR-035 (a dataset you can recall)

- **Row-level import for small reference sets, under a stated ceiling.** ADR-035 refuses rows on
  evidence — a larger, more heterogeneous corpus retrieves measurably worse, so filing tens of
  thousands of seed rows would degrade recall for every other memory in the wing to answer
  questions SQL already answers better. The exception worth building is the set where the row *is*
  the knowledge: currencies, status codes, a country list. Trigger: someone actually wants such a
  set recallable row by row. It needs a row-count ceiling enforced in code (the profiler has none
  today, and nothing in the shipped command pretends otherwise), or it quietly becomes the bulk
  path the ADR rejected.
- **Watching the JSONL and re-importing on change.** A scheduled or hook-driven re-import is a
  deployment concern rather than a format one, so it stays out of the producer. Safe to build now
  that an unchanged file re-imports as a no-op.
- **Replacing a dataset's profile when the data changes — the gap review found.** The producer's
  drawer id is deterministic, so an unchanged file upserts. A CHANGED file produces different text,
  a different id, and therefore a SECOND profile: yesterday's numbers stay recallable next to
  today's, and the stale one has to be deleted by hand. Closing it means a purge-by-source on the
  import path, which is exactly what the migration path must NOT do — `AbsorbDrawers` absorbs
  without purging because a batched migration would otherwise delete the earlier batches of the
  source it is still uploading. So it needs an opt-in the producer can ask for (a `replace_source`
  on the bundle or the endpoint) rather than a change to the shared absorb. Filed 2026-08-26 from
  the PR #60 review, where the ADR had claimed the stronger "idempotent by source" four times.
- **Nested structures below the first level.** The profiler reports a nested object's presence and
  type, never its interior. Deep schema inference is its own decision — and, since values below the
  first level would have to pass the same `show_values` allowlist, its own disclosure question.

## From ADR-036 (the knowledge graph on the read path, 2026-08-26)

- **ADR-004 T5's deferral is received here.** T5 (Accepted, `done`) carries `- Wiring the graph into
  Service.Search (deferred: docs/adr/BACKLOG.md)` and this file never received it, so `adr-debt`
  reported zero unreceipted — the pointer resolved to a real file that did not mention it. ADR-036 is
  that work. **Trigger: closed by ADR-036 reaching `done`; until then this line is the receipt.**

- **`kg_triples` has no `wing` column,** so the graph is workspace-wide while drawers, anchors and
  search are wing-scoped. ADR-036 works around it by deriving a fact's wing from `source_drawer_id`,
  which caps reachability at 46% (196 triples, 106 carry an id, 90 resolve — measured 2026-08-26).
  A column plus backfill would lift the cap. **Trigger: when T1's answerable-rate plateaus and the
  unresolvable 54% is the named reason.**

- **Repair the 16 dangling `source_drawer_id` pointers.** They name a drawer that is not there, so
  they are unresolvable rather than merely unlabelled, and they are part of the 46% ceiling above.
  **Trigger: same as the wing column — they are the cheapest slice of it.**

- **Backfill edges for the 1,928 existing orphan drawers.** ADR-036 T6 fixes the write path only, so
  every drawer filed before it stays unreachable by traversal (57 of 1,985 carry any edge — 2.9%,
  measured 2026-08-26). **Trigger: after T6 has run long enough to show the derived-edge marker does
  not degrade recall; backfilling first would bake in a bad derivation.**

- **Why the derived graph produces zero hallways is still unseparated.** 945 of 1,985 drawers carry
  entities (47.6%, measured 2026-08-26) and `am_graph_stats` reports no hallway at all. Two causes
  are indistinguishable from outside: `am_recompute_graph` was never run, or the co-occurrence
  threshold is never met. BACKLOG item 2 argued from *"`Service.Add` does not [extract entities], 82
  of 82 today"* — false since ADR-016 — so "feed it" was necessary and demonstrably not sufficient.
  **Trigger: before anyone proposes a graph-derived ranking signal; it would rest on an empty graph.**

- **Unify the two entity vocabularies at the write path.** `drawers.entities` (frequency-extracted,
  ADR-016) and `kg_entities` (authored via `am_kg_add`) share nothing but `source_drawer_id`.
  ADR-036 T4 joins them at READ time only, deliberately. **Trigger: if T1 shows the read-time join
  helps and its cost per query becomes the bottleneck.**

- **Validate entity spelling on write.** `am_kg_query` fails open on an unknown entity; ADR-036 T2
  makes that distinguishable at read time but nothing stops a misspelled entity being stored.
  **Trigger: the first time a fact is filed and cannot be found by the name its author expected.**

- **Fix `am_traverse`'s inert `max_hops`.** `via` is an intersection carried forward, so hop >=2 can
  never add a node — verified 2026-08-26 from a hub (25 nodes, all hop <=1) and a leaf (10 nodes, all
  hop 1). ADR-036 T7 resolves edges directly rather than depending on it. The fix is blocked on an
  unmade product decision: should traversal be transitive across wings, or confined to the wings the
  start node already belongs to? **Trigger: someone deciding that question — not before.**

- **Update the client kits to use the bootstrap.** ADR-036 T8 adds the surface; the kits still carry
  a hardcoded root id and a 13-call client-side protocol. A bootstrap nobody adopts is the rung-4
  failure this ADR exists to remove. **Trigger: once T8's F-16 measurement beats the client baseline
  — the number is what makes adoption arguable.**

- **Personalized PageRank over the graph (HippoRAG, arXiv 2405.14831).** Rejected for ADR-036, not
  forever: it presumes a connected graph, and ours derives zero hallways. **Trigger: once T6 has
  produced edges and T1 can score whether PPR beats the direct lookup.**

## From ADR-036 T4 (the second entity vocabulary, 2026-08-26)

- **Whether the extracted vocabulary HELPS is still unmeasured, and the code shipped anyway.**
  T4's tests prove a fact reachable only through `drawers.entities` now arrives, and a mutant that
  drops the join is killed. What they cannot prove is whether the join raises or lowers the
  answerable-rate: T1's arm scores against gold triple ids, and the committed fixture's ids are
  invented, so an on/off comparison is 0/8 either way. The real corpus is deliberately untracked
  (ADR-003 T2), and nobody has built one. So T4's own stop condition — "stop if the join measurably
  LOWERS the rate" — has no number behind it yet, and frequency-extracted terms are noisier than
  authored names by construction. **Trigger: the first time the real fact corpus is built; run
  ArmFactRetrieval with the second vocabulary on and off and record both rates WITH denominators
  before trusting either.**

## From ADR-036 review (2026-08-26)

- **A migration-number gap is a startup failure, and nothing checks for one.** ADR-036 takes `00028`
  and leaves `00027` for ADR-034 on PR #61. Verified against `goose v3.27.1` (`up.go:82`): plain
  `goose.Up`, which `cmd/server/main.go:1382` calls, refuses to run when a pending migration sits
  below the database's max applied version, and the server exits. The gap is safe only while
  whichever branch merges SECOND renumbers at merge. Nothing enforces that: `adr-lint` checks ADR
  numbers, and no gate reads migration numbering across branches. **Trigger: before the second of
  #67 and #61 merges — and a contiguity check over `db/migrations` on `main` would make it
  mechanical rather than remembered.**

- **`DropDerivedEdgesFor` leaves structural `kg_entities` rows behind.** Deleting a drawer removes
  its derived triples but not the room node or the drawer-id entity those triples referenced. Bounded
  now that the label index excludes structural entities, so nothing reads them — but the table
  accumulates dead ids. **Trigger: when `am_kg_stats`' entity count stops matching what anyone
  expects, or before any feature counts entities as a measure of anything.**

- **The centralised skills become stale consumers the moment ADR-036 merges.** The BACKLOG item on
  updating the client kits names the kits; `start-here` (v3) and `memory-orchestration` are the other
  two consumers. `start-here` instructs every session to run three predicate queries BY HAND — which
  is exactly what `kg.CorrectionsFor` now does server-side — and to reach the taxonomy by traversal,
  which `am_bootstrap` replaces. **Trigger: same as the client kits; the skills are versioned
  server-side, so they change without a repo commit and will otherwise teach the old protocol
  indefinitely.**

- **The fact corpus is still not loadable by the eval CLI.** `LoadFactCases` is called only by tests;
  `agentsmemory eval --cases` uses `readCasesWithMeta`, which neither skips the fixture's leading
  `//` lines nor understands its `question`/`expect_triple`/`synthetic` schema. So the committed
  corpus cannot select `ArmFactRetrieval` end to end, and `FactAnswerRateFrom` is never consumed by
  the production reporter — the table prints recall and MRR and not the answered/cases fraction the
  arm exists to produce. **Trigger: before the first answerable-rate is quoted anywhere; until then
  that number can only be produced by a test, which is not the instrument this ADR claimed.**

## From ADR-038 (dedupe on the content, refer by the id)

- **Re-chunking on update, now unblocked.** ADR-038 makes a drawer id opaque and moves dedup onto a
  content key, so changing which chunk rows exist no longer invalidates any anchor, tunnel or
  knowledge-graph pointer. What it does NOT answer is ADR-027's question: what happens to a
  reference pointing at a **non-parent** chunk that a re-chunk deletes. **Trigger: whenever
  `Service.Update`'s multi-chunk refusal blocks real work again — it already blocks one live drawer
  measured at 6,448 runes.** Owner: whoever takes ADR-027's remaining half.
- **Repairing the drifted rows.** Measured 2026-08-27: 27 of 1,705 non-diary drawers carry an id
  that no longer derives from their current fields — 5 explained by a wing move, 1 by a room move,
  21 unattributed (an upper bound on in-place content edits, since a merge from a wing that now
  holds no drawers is undetectable by wing substitution). ADR-038 makes the drift *checkable* and
  deliberately does not repair it: every one of those rows is correct as stored, and the only thing
  wrong is that nothing recorded which key described it. **Trigger: the first time T3's drift query
  reports a row whose content key ALSO disagrees, which would mean a write path is losing the key
  rather than history explaining it.** See `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md`.
- **Should re-filing a named source discard an in-place edit to it?** `purgeSource` deletes every
  drawer under a `(wing, room, source_file)` triple before inserting the new set, so an
  `am_update_drawer` edit is destroyed by the next `am_add_drawer` for that source. Measured
  2026-08-27: 27 drifted rows across 19 source triples are in that state; the RATE cannot be
  measured, because a re-file leaves no trace of its predecessor. ADR-038 deliberately preserves
  this behaviour and fixes only the collateral damage — chunks the re-file did not change keep their
  ids and their anchors. Two defensible answers: a re-file means "replace the source" and the edit
  should go, or an edit is a correction and re-filing stale text over it is loss. **Trigger: the
  first time someone reports losing an edit this way; until then it is a known trade, not a bug.**
- **Taking `merge_wing` off the agent surface.** ADR-038 T4 removes `delete_drawer`, `delete_tunnel`,
  `delete_hallway` and `delete_wing` from the agent registration. `merge_wing` stays, and the reason
  is that it is not erasure — it is a move, and ADR-015 governs what a move invalidates. But
  `registerMergeWing` (`admin.go:196`) is UNCONDITIONAL, so an agent reaches it everywhere, and
  ADR-015 exists because a merge can silently invalidate a search index. **Trigger: whenever an agent
  is found to have merged a wing nobody asked it to merge.** Found by review 2026-08-27, correcting an
  ADR-010 claim that both were "already outside the agent surface" — they were not.
- **Does a date-only `valid_to` mean *through* that day, or *as of* it?** Issue #47 — **not** #74,
  which is closed and was the hand-rolled-supersede overlap; that pointer was stale from the day #47
  was filed. `temporalEndKey` stretches a date-only `valid_to` to `T23:59:59Z` and `inEffectAt`
  excludes only below `as_of`, so `status:"current"` drops an ended fact immediately while `as_of`
  keeps it for the rest of that day. **The WRITE half shipped with #47:** `KGInvalidate` no longer
  defaults `ended` to a bare date, so no new row joins the class, and both the `as_of` and `ended`
  descriptions plus `bootstrap-memory.md` §6.1 now name the lag. **What is still open is the READ
  half** — what a date-only `valid_to` MEANS for the rows that already carry one, which is the only
  part that re-reads history and therefore the only part that needs a decision record.
  **Trigger: the first time someone needs a past-date snapshot to be exact, or a backfill of the
  existing date-only rows is proposed (ADR-026's write-path normalisation Follow-up is where that
  lives).**
- **A validity window for TUNNELS.** Found auditing ADR-038's own class 2026-08-27. `tunnels` has zero
  validity columns and `DeleteTunnel` destroys. ADR-038 T4 takes `delete_tunnel` off the AGENT surface
  but leaves the operator path destroying an **authored** artifact with no trace, while that record's
  whole argument is that authored things are ended rather than deleted. Closets, hallways and anchors
  are derived or re-derivable and are correctly delete-only; tunnels are the one authored non-drawer
  artifact left delete-only. 18 exist. **BLOCKED, not merely deferred:** a tunnel's PK is `canonicalTunnelID(endpoints)` and
  `UpsertExplicitTunnel` conflicts on it, so an ENDED tunnel would swallow every attempt to re-create
  the same link — the id is minted identically and the upsert updates the corpse. Tunnels need an
  opaque id before they can have a validity window. **Trigger: when someone takes on opaque ids for
  the graph tables, not before.**
- **The interval is CLOSED where a validity window wants half-open.** Extends issue #47 from the other
  direction, found by review 2026-08-27 and reproduced: `inEffectAt` (`kg.go:955`) excludes only on
  `>` and `<`, never `>=`/`<=`, so with `old.valid_to == new.valid_from == B` both rows are in effect
  at exactly `B`. ADR-038's `am_kg_supersede` collapses the overlap from 86,400 seconds to 1 by
  stamping instants instead of dates; it cannot remove the last one, because the shared endpoint IS
  the mechanism. The one-character fix (`<` → `<=`) is the half-open semantics and re-reads every
  ended fact by one boundary unit, including the 15 already ended. **Same decision as #74's — what a
  `valid_to` means — so one record answers both.**
- **The other half of the "a write reports success and changed nothing" sweep.** #73 fixed the shape
  where a count EXISTS and is discarded. The other shape is a write that returns no count at all, so
  the caller cannot check. **Find them with the predicate, not with a list** — a list is a snapshot
  and rots exactly as the doc-comment list did:

      grep -nE '^func \(r \*Repo\) (Save|Update|Delete|Invalidate|Relabel|Drop|Mark|Upsert|Add|Replace)[A-Za-z]*\(' internal/palace/*.go

  On 2026-08-27 that returned 14 of 29 package-wide returning a bare `error`. Not all need a count —
  an insert of a known set does not — but every predicate-scoped UPDATE or DELETE does. **Trigger:
  the next time a write is reported as having done something it did not.**
- **`merge_wing` on the agent surface.** ADR-038 leaves it, because it is a MOVE — `MergeWing`
  (`admin.go:47`) relabels via `RelabelDrawerWingReturningIDs` and `RelabelClosetWingReturningIDs`
  and deletes nothing. **The trigger is a condition, not a date:** ADR-038 T2 makes a merge into a
  target holding identical content REFUSE rather than silently duplicate. If that refusal is ever
  softened to "end the loser and keep going", `merge_wing` becomes an ending operation performed by
  an agent and the surface question reopens.

## ADR-041's instrument cannot measure what ADR-041 is trying to change — 2026-08-28

Found by running the instrument on the session that built it.

`Observe` (`clients/claude-code/recallrate.go:166`) sets `recalled := false` ONCE PER SESSION and
flips it true at the first recall tool call. It is never reset. So every assertion after that point
counts as "preceded by a recall", for the rest of the session, however far away the recall was and
whatever it was about.

**Measured on this session:** 109 assertions, 109 preceded — a perfect 100% against T2's 27.6%
baseline. The latching call was `am_search` at tool_use **#172 of 8,277**, and every assertion after
it inherited the flag. The number is an artifact, not an achievement.

*(Two corrections, and the second is the same error one layer further out. First: an earlier version
said "#3 of 8,256", which was the first PALACE call — `am_skillset` — not the first RECALL call.
Second: the correction then claimed the latch "cannot flip on a wake-up call". `recallTools` is
`am_search` and `am_get_drawer` (`recallrate.go:51`), and `AGENTS.md`'s manual traversal (under *"When the tools are present"*) mandates
`am_get_drawer(id, whole:true)` once per `must.*` edge AS PART OF the wake-up sequence — dozens of
edges, before the task search. So a protocol-following wake-up flips the latch almost immediately.
`am_skillset` and `am_status` cannot flip it — nor can `am_bootstrap`,
`am_list_drawers` or `am_kg_query` — all three named in that same traversal, none of them in
`recallTools`; an
earlier version of this sentence said "only" of the first two and was over-precise.

⚠ **That premise has an expiry the entry should name.** The wake-up flips the latch *because*
`AGENTS.md` records `am_bootstrap` returning `unknown_term` for this wing (*"It can honestly return
nothing, and you must read that correctly"*), which is what
makes the manual `am_get_drawer` traversal mandatory today. Once that backfill runs, a compliant
session may make no `am_get_drawer` call at wake-up and this consequence evaporates. The mis-measurement is the same class as the
defect being reported, now twice over.)*

**What the metric actually answers** is "had this session touched the palace at any earlier point",
not "was this claim grounded in a recall". Those are different questions, and the second is the one
ADR-041 exists to move.

**Three consequences:**

1. **T2's 27.6% baseline measures the weaker thing.** Across 46 sessions it is approximately the
   share of assertions made in sessions that had called a recall tool at all, weighted by how many
   assertions each session made — not a rate of grounded claims.
2. **The metric has a ceiling any protocol-following session hits trivially.** `AGENTS.md`'s manual
   traversal mandates `am_get_drawer(id, whole:true)` once per `must.*` edge as part of the wake-up sequence —
   dozens of edges, before the task search — and `am_get_drawer` IS a recall tool. So a compliant
   session flips the latch almost immediately and scores 100% before it has recalled anything
   relevant to what it then asserts. It is not vacuous: `am_skillset` and `am_status` cannot flip
   it, so a session that only woke up and never fetched would score zero.

   ⚠ **RETRACTED, and the truth is worse.** An earlier version of this bullet said subagent records
   share the parent's transcript, so a subagent's recall flips the parent's latch. That is false:
   subagent records live in SEPARATE FILES. The repo's own captured payload proves it —
   `clients/claude-code/hooks_test.go:274-280` is a real `SubagentStop` event carrying both
   `transcript_path` (the parent) and `agent_transcript_path`
   (`…/<session>/subagents/agent-<id>.jsonl`). Measured on this machine 2026-08-28: 48 top-level
   transcripts, **0** containing `"isSidechain":true`; 17 `subagents/` directories holding 1,844
   files that do. What was conflated is `session_id` sharing — real, and documented at
   `agentsmemory-stop-hook.sh:76-83` — with TRANSCRIPT sharing, which is not.

   **The real finding is this repo's own characteristic defect.** `Observe` deliberately does not
   filter `isSidechain` (`recallrate.go:153-157`), for a reason it argues well: excluding subagents
   would silently drop "the population most likely to skip recall" from the measurement of skipping
   recall. That decision is **inert in production**, for two independent reasons:

   - `agentsmemory-stop-hook.sh` takes the `SubagentStop` branch at `:59` and `exit 2`s at `:117` —
     **before** `agentsmemory_recall_observe` at `:155`.
   - `agentsmemory-stats.sh:16` parses `TRANSCRIPT` from `"transcript_path"` only, never
     `agent_transcript_path`, and `:72` is the sole caller of `recall-observe`.

   So every line the instrument is ever handed comes from a parent transcript, which contains no
   sidechain lines. The non-filtering is finished, argued for in a comment, tested against a
   hand-made fixture (`recallrate_spec_test.go:86-92`), and **unreachable** — a capability that
   works and that nothing can select. Found in review after the reviewer retracted the transcript
   claim above.
3. **Therefore it cannot detect the improvement the ADR is for.** A mechanism that makes recall
   *proximate and relevant* — which is what T4, T5 and T6 are all about — moves this number by zero.
   ADR-041 T1's whole purpose was to create the measurement before any requirement claiming an
   improvement; the measurement it created is insensitive to that improvement.

★ **AND THE FLAGSHIP MECHANISM IS INVISIBLE TO THE INSTRUMENT — a stronger version of this entry's
thesis than the latch, and checkable from the tree by anyone.** T4's hook does not encourage a
recall, it PERFORMS one, as a CLI subprocess:
`HITS="$(aiagentmemory "$@" …)"` (`clients/claude-code/hooks/agentsmemory-recall-hook.sh:118`). `Observe` counts only
`tool_use` blocks by name (`recallrate.go:177-182`), and a subprocess emits no `tool_use`. So a
hook-performed recall is **not counted at all**.

Two consequences, and the second is the one that matters:

- T4 cannot register as an improvement however well it works.
- **If the injected recall does its job — the agent already has the answer and therefore does NOT
  call `am_search` — T4 measures as a DECREASE.** An instrument that scores a working mechanism
  negatively is worse than one that is merely insensitive to it.

**The spec DECIDED one thing in its flow and MITIGATED THE OPPOSITE in its Risks, and nothing
reconciled them.** Main flow step 2 (`docs/specs/2026-08-27-recall-before-asserting.md:33`) says to
determine whether an `am_search` (or `am_get_drawer`) call "preceded it **in the same session**",
and `## Domain` (`:155-157`) fixes what a recall is. `Observe` implements that faithfully.

But the Risks table of the same spec (`:184`) records a mitigation the code never implemented:
*"Count searches that preceded an assertion, not searches; **a search on an unrelated subject is not
a recall**."* So neither "the spec forgot" nor "the spec decided cleanly" is accurate — it decided a
session-wide window in one section and promised subject-relatedness in another, and neither the ADR
nor the implementation noticed.

That changes the remedy and makes the claim harder to wave away. This is not an underspecification
an implementer may fill — it is **a specified decision whose consequence was not drawn out**, so
changing what "preceded" means is an AMENDMENT TO AN ACCEPTED RECORD and the owner's call. "The spec
chose a session-wide window and the choice is insensitive" is a stronger statement than "the spec
forgot".

F-4 guards one route to a vacuous perfect rate (a classifier that matches nothing) and does not
guard this one, which arrives from the opposite side: a numerator that counts everything after the
first ask.

**Not fixed here, because the fix is a spec decision.** What counts as "preceded" — within N tool
calls, since the last user message, since the last compaction, or a recall whose query is related to
the assertion's subject — changes what the number means. Options, cheapest first:

- Record more without deciding: also emit `recalls`, `assertions_before_first_recall`, and the
  distance in tool calls from each assertion back to the nearest preceding recall. Additive, it
  re-reads existing transcripts, and it lets the window be chosen from data rather than guessed.
  **DONE 2026-08-28 — see the distribution below.**
- Reset the latch at a boundary (user message, or compaction) and re-take the baseline.
- Bind "preceded" to subject relatedness — the honest reading of the spec's intent, and much the
  hardest to implement.

**The baseline must be re-taken under whatever definition wins.** A rate is only comparable with
another measured the same way; T2's number cannot be carried over.

### The additive option shipped, and the distribution is what the decision was waiting for

`Observation` now carries `recalls`, `assertions_before_first_recall`, and `preceded_within` —
cumulative counts of assertions whose NEAREST preceding recall was within 1, 5, 10, 25, 50 or 100
tool calls. `preceded_by_recall` is deliberately UNCHANGED, latch and all, so T2's rate stays
comparable under F-16: nothing here redefines the number, it measures what a redefinition would have
to choose between. `classifierVersion` is unchanged for the same reason — neither `assertionShape`
nor `assertionSubject` moved.

`TestPrecededCannotSeeProximityAndTheObservationCan` is the gate, and it is written as the pair of
sessions the old field cannot separate: one recall then a wall of claims, versus a recall before
each claim. **Both score 100% on `preceded`.** Four mutants die on it — the distance never updating,
every window credited unconditionally, `recalls` never counted, and `assertions_before_first_recall`
never counted.

Measured 2026-08-28 over 48 local session transcripts — 24 carrying at least one assertion, 341
assertions, classifier v2. Re-run it rather than trusting these numbers; the corpus grows daily:

All three candidate definitions are measured, not just the tool-call windows:

| reading of "preceded" | rate |
|---|---|
| the latched field — *"this session touched the palace at some earlier point"* | **52.8%** |
| a recall since the last COMPACTION | 43.4% |
| nearest recall within 100 tool calls | 28.7% |
| within 50 | 17.6% |
| within 25 | 12.3% |
| **a recall since the last USER TURN** | **7.6%** |
| within 10 | 7.3% |
| within 5 | 5.3% |
| within 1 — the claim made immediately after asking | 1.8% |

47.2% of assertions are made before ANY recall in the session.

⚠ **The user-turn reading first measured a clean 0.0%, and the zero was the instrument.**
Claude Code records every TOOL RESULT as a `"type": "user"` line — 11,055 of 11,704 in one real
transcript — so taking those for user turns reset the boundary after nearly every tool call and a
recall could almost never be after one. A rate of exactly zero over a corpus yielding 52.8% by
another reading is an instrument fault until proven otherwise, and it is a fixture now
(`tool-results-are-not-user-turns.jsonl`). The same investigation found that a line whose content is
a bare STRING — a plain user turn — failed to unmarshal and was silently dropped by the
malformed-line skip: 600 of them in that transcript.

**Comparability was checked rather than asserted.** Re-running the whole corpus before and after the
parsing fix leaves `assertions` at 341, `preceded_by_recall` at 180 and the session count at 24,
byte-identical — so the shipped field, and T2's baseline with it, is untouched under F-16.

Two things follow, and they are why this mattered rather than being tidy-up:

1. **The reported rate and the strictest honest reading differ by a factor of about 29.** Every
   window is a defensible definition of "preceded". The latched field is the one nobody chose — it
   is simply what a flag with no reset computes.
2. **There is headroom, which the old number denied.** The latch saturates on any protocol-following
   session, so T4, T5 and T6 could only ever measure as "no effect" — the instrument would have
   faithfully recorded four nulls under F-10. Against a 1.8-12% proximity rate they have somewhere
   to move.

**Still a decision, and deliberately left open here:** which reading becomes the definition.
Choosing one voids every rate taken under another. The table above is what that choice should be
made from; this entry does not make it.

What the table shows, said once so the next reader does not have to re-derive it: the three
boundary-free tool-call windows spread across an order of magnitude with no natural break, which is
what a proxy looks like. The two BOUNDARY readings do have meanings — "did the agent ask about the
work it was just given" (7.6%) and "did it ask after its context was replaced" (43.4%) — and the
first of those lands almost exactly on the within-10-calls window, from a completely different
derivation. That agreement is the only evidence here that any particular window is more than a
number someone picked.

## Two tests name a property their fixtures never drive — 2026-08-28

Found by mutation while re-recording the corpus; both mutants SURVIVED first and the survivals are
kept in the task files rather than replaced by the kills that followed.

**`TestClosetDeltaExcludesUnreachableAndAbsentCases`** (`internal/palace/evalstats_test.go`) asks
`ClosetDelta` for `CatSingle`. The loop's first check is `if d.Category != category { continue }`,
so the absent case is filtered out before the `if category == CatAbsent` guard can run. Deleting
that guard changes nothing the test can see. The exclusion in the test's NAME is undriven; a call of
`ClosetDelta(report, CatAbsent)` would exercise it.

**ADR-004 T5's fence** is `TestSupersessionGate*`, which drives `SupersessionVerdict`. It never
reaches `gatedArm`, so returning a named arm where none reconstructs the served ranking — the exact
defect `SupersessionGatedArmFor`'s doc comment says "is how the gate judged a pipeline nobody runs"
— goes unnoticed by the gate that task is verified against.

Neither is a bug in shipped behaviour. Both are gates weaker than their names, which is the
condition this repository's checks exist to remove.

## ADR-003 T3's two mined runs cannot commit their evidence under the derived wing — 2026-08-28

Found while starting T3, before any eval was run.

T3 step 4 runs four evals, and `$MINED_WING` appears in TWO of them (`T3:33-34`); the other two name
`wing_agentmemories`, itself a declared example, and commit as-is. `writeCells` (`cmd/server/eval.go`)
writes `"wing": meta.Wing` into the `.cells.json`, which step 5 commits. But `mine-claude` derives a
wing from each session's working directory (`clients/claude-code/mineclaude.go:318`), so on a real
palace the mined wing is named after somebody's project — and `TestNoRealProjectNamesInWings`
(`internal/repohygiene/hygiene_test.go:297`) fails on any `wing_*` in any textual file the walk reaches — the filesystem minus `.gitignore`
(`hygiene_test.go:303`), NOT `git ls-files` — unless the name is a declared example.

**Verified 2026-08-28**: planting `{"wing":"wing_<a real project>"}` in
`docs/adr/ADR-003-retire-the-closet-prior/evidence/` turned the gate red, naming the file. Removed
immediately; nothing was committed and no `.cells.json` is tracked today, so nothing has leaked.

**The gate is right.** The conflict is that T3 leans on the `wing` field to prove two mined runs share
one corpus, so dropping it removes a real check, while keeping it makes the evidence uncommittable.
`writeCells`'s own doc comment claimed the record "must carry nothing that came out of the palace" —
which the `wing` field contradicted; the comment now names the exception rather than overstating the
rule.

**Option 1 needs no decision and is now written into T3.** `mine-claude` takes an explicit `--wing`
that wins over the derived name (`clients/claude-code/mineclaude.go:318`), and `wing_acme` /
`wing_alpha` are declared examples (`internal/repohygiene/hygiene_test.go:258` and `:264`), so
evidence mined into either commits as-is. It also supplies the single mined corpus `--n 80` needs, because forcing one
`--wing` mines every session into one wing. ⚠ That mixing is deliberate: `mineclaude.go:435-437`
refuses `$AGENTSMEMORY_WING` for exactly this reason, so `--wing` opts into it — a judgement T3 now
makes rather than leaving to the executor.

**Options that DO need the owner:** replace the raw wing with a one-way hash, as `case_set_id`
already does for questions (discloses nothing); or drop the field and replace the check. Either
changes what a published record means.

⚠ **Option 1 weakens the argument for keeping the field at all.** With `MINED_WING` pinned to the
literal `wing_acme`, both mined records agree by construction, so "the `wing` field proves two runs
share a corpus" now catches a typo and nothing else. The case for the status quo is thinner than
this entry first stated it.

**T3 is NOT blocked on this any more** — that was the entry's own earlier reading, and Option 1
retires it. What survives is a precondition rather than a block.

⚠ **And the precondition is counted in SOURCE FILES, not drawers — an earlier version of this
paragraph said "≥80 drawers" and that is the wrong unit.** `ListRandom`
(`internal/palace/repo.go:797`) over-fetches `limit*5` rows and keeps at most one drawer per
`source_file`, on purpose: a mined session arrives as many chunk drawers sharing one source, and two
eval cases from one session are not independent observations. So a wing holding 100 drawers across
4 mined sessions yields **4** cases at `--n 80`, against D1's floor of 40 admitted cases
(`ADR-003:93`) — and an executor who checked "≥80 drawers" would discover it after building the
binary and running all four evals. `aiagentmemory mine-claude --wing wing_acme` has to have run over
roughly 80 distinct mined session-parts, densely enough that a random `limit*5` over-fetch reaches
80 of them.

That the unit was wrong twice is itself the finding: **`SampleDrawers`/`ListRandom` had no test
anywhere in the tree**, which is why two rounds of careful prose about `corpus_drawers` could both
be wrong with every gate green. `TestSampleDrawersCountsSourcesNotDrawers`
(`internal/palace/samplesize_test.go`) now pins it — mutant killed 2026-08-28 by disabling the
dedup, which turns 2 of its 4 subtests red.

Only the hash-or-drop options still need the ADR owner, and neither gates T3.

⚠ **UPDATE 2026-09-06 — T3 IS BLOCKED AFTER ALL, ON A SECOND PRECONDITION THIS ENTRY DID NOT HAVE.**
Option 1 was executed and it works: `mine-claude --wing wing_acme --limit 0` mined 146 sessions,
`wing_acme` reached 210 distinct sources, and D1 admitted **76** against its floor of 40. The
source-file arithmetic this entry got right twice is right a third time. But **D2 cannot be taken at
all**, and the reason is Option 1 itself.

`--style real` replays RECORDED search traffic. A wing mined for the first time has never been
searched, so `search_events` holds nothing for it and the eval refuses:

```
no recorded searches to replay in wing_acme of workspace "local" — real-query cases
need search telemetry; run some sessions against this palace first, or use a
generated --style
```

Measured: `wing_acme` 0 rows, `wing_agentmemories` 1,486. So forcing an explicit `--wing` to make
the evidence committable is exactly what makes D2 unobtainable — the declared wing is committable
BECAUSE nobody works in it, and it has no telemetry for the same reason. The two requirements are
in tension by construction, and no `--n`, `--pool` or re-run reaches around it.

**Both closes were refused.** Generating traffic against `wing_acme` manufactures the telemetry the
`real` category is defined as replaying, so the cell would measure the runner's own questions; and
the derived wing has the traffic but step 3 forbids committing its `cells.json`. D2 is Table 2's
VETO, so the three cells that WERE taken (D1 76/13, R1 38/0, R2 17/0, one binary at `fa93749`,
`dirty:false`) do not decide the ADR.

Filed as PR #342, a DRAFT carrying the three records and `TestClosetEvidenceIsComplete` red on the
missing fourth, so `main` stays green and the task stays not-done.

**What would close it:** real sessions searching `wing_acme` until `search_events` holds enough for
D2's floor of 10 admitted cases, then re-run D2 alone with the same binary. Nothing else in the
evidence set needs re-taking. ⚠ An owner decision is available and cheaper — declaring that D2 may
be taken on a wing that has organic traffic, with the `wing` field hashed as the hash-or-drop option
above already proposes — but that is the same decision this entry has been holding open since
2026-08-28, now with a second reason to take it.

⚠ **One more trap, found by hitting it:** T3 step 2 builds the shared binary in
`golang:1.26-alpine`. That is a LINUX artifact and all four runs exit 126 on macOS. A native build
carries the identical stamp (`vcs.revision`, `vcs.modified=false`), so the invariant that matters —
four records, one commit, clean tree — still holds. Step 2's wording assumes a Linux host.

## Four spellings of one entry point, and the served document teaches a fifth — 2026-08-28

**Fourth framing. The three before it each named a single CAUSE and each died to one more query;
this one names the LAYERS and leaves the choice open, because the choice is a product decision.**
Independent read by a different-lineage advisor found most of the evidence below.

**The layers, all present in this tree today:**

1. **The served onboarding document.** `internal/web/bootstrap-memory.md` is `go:embed`-ed and
   served at `/bootstrap-memory`. It says `llm_index` **15 times and `llm_init` zero times**, and
   its §4.3 seeds two `llm_index` drawers. It was `setup.md` until commit `bd611a3`. This is what a
   new agent reads, and it is what the local corpus was built from — those drawers cite
   `setup.md §4.3` and `§6` in their `source_file`.
2. **The canonical model.** `model/draf1.md:94`: *"Every project's root is room `llm_init` in that
   project's wing."* `:197` — *"P2 — Write the ROOT INDEX DRAWER into `llm_init`"*. The graph shape
   it prescribes is **root-drawer-ID → `must.*` → drawer-ID** (`:224`, `:323`). `AGENTS.md` and
   ADR-027 (`:41`, `:56`, `:62`, `:197`) agree; ADR-036 T7 records a live 25-node `llm_init` root.
3. **The Go API.** `EntryRoom = "llm_init"` (`graphquery.go:465`), and `EntryPoint` resolves
   **derived room containment** at `room:<wing>/llm_init` (`:509`). `Bootstrap` takes outgoing edges
   from that containment node (`bootstrap.go:95`) and **never examines `must.*` or `ref.*`** —
   ADR-036 T8 put that vocabulary explicitly out of scope.
4. **This local corpus.** `must` → `must_load` / `must_load_skill` → **labels** (`llm_index`,
   `effective-go`, …), 8 facts, `matched`. Canonical is root-drawer-ID → `must.*` → **drawer IDs**.
   Different subject, different predicate, different object type — a fourth spelling, not the KG
   half of layer 2.

**Consequences that follow from the layers, not from a guess:**

- `must.*` appears in **no Go source**. It is a human protocol, described in prose and maintained by
  hand. Nothing produces or consumes it.
- Nothing in the tree **creates** a drawer in `llm_init` outside tests — no seed, migration,
  installer or fixture. `model/draf1.md` P2 is a human procedure. So the entry point's data has no
  producer in the product.
- **A derived-edge backfill alone would produce FALSE reachability**: `am_entry_point` would go
  `matched` while returning only the root room's own drawers, never the mandatory tier the manual
  protocol traverses. That is this repository's characteristic defect, and it is the trap in the
  cheapest-looking fix.

**Verified locally:** `am_kg_query(entity:"room:wing_agentmemories/llm_init", status:"all")` returns
`unknown_term`, `unresolved: "entity"` — so this workspace never held that node, not even ended.

### Proposed 2026-08-28 by ADR-043 (NOT accepted) — take the `llm_init` layer; two obligations received here

`docs/adr/ADR-043-one-spelling-for-the-entry-room.md` (Proposed, unassigned, unaccepted) PROPOSES for this entry: `llm_init` is the
canonical entry room, the served onboarding document is the outlier and is corrected, and `llm_index`
keeps ADR-027's job as a routing drawer filed UNDER the root rather than instead of it.

**A second local read, taken 2026-08-28 with a different call than the one this entry retracts.**
`am_list_drawers(wing:"*", room:"llm_init", include_history:true)` → **0**. Not one drawer in any
wing, and not one ended drawer either — so the two derivations agree, and the room has never existed
in this palace rather than merely lost its derived edges. `am_list_drawers(wing_agentmemories,
room:"llm_index")` → 2 drawers citing `setup.md §4.3` and `§6`; `am_kg_query(entity:"must",
direction:"outgoing")` → 8 facts, `matched`, objects are LABELS.

**Two things this entry did not name, found the same day:**

- `AGENTS.md`'s documented traversal opens with `am_list_drawers(wing:"wing_agentmemories",
  room:"llm_init")` and the comment `# several drawers`. Against this palace it returns zero, so the
  repo's own protocol ends before the step that teaches you to distrust a zero. ADR-043 fixes this by
  populating the room rather than by editing `AGENTS.md`, which already names the right one.
- `README.md:167` explains `am_bootstrap`'s `unknown_term` as `llm_init` drawers filed before the
  derived room edges shipped. There are no such drawers here. The documented cause cannot be this
  palace's cause, and an operator reading it goes looking for a backfill that would not help.

**Received from ADR-043 (deferred, receipted here per the deferral rule):** backfilling corpora on
deployments other than the local and hosted ones named in ADR-043 T3 — a third-party palace built
from the old served document keeps the old shape, and this record does not reach it. Also received:
whether the entry point's data should have a producer in the product at all, rather than a procedure
the served document instructs an agent to run by hand; that is the deeper cause of all four spellings
and ADR-043 does not fix it.

⚠ **ADR-036 T7's 25-node claim is unresolved and ADR-043 T3's first step is to resolve it.**
`docs/adr/ADR-036-a-recall-that-answers/tasks/T7-a-wing-names-its-entry.md` records a live
`wing_agentmemories` `llm_init` root of 25 nodes verified 2026-08-26. That cannot be this palace. It
was the hosted deployment or a fixture, and which one decides whether adopting `llm_init` strands an
existing corpus or none at all.

⚠ **My earlier discriminator was wrong.** I claimed one `am_entry_point` call against production
settles it. It does not: `unknown_term` cannot distinguish "no `llm_init` room" from "`llm_init`
drawers that predate derived containment edges". The right call is
**`am_list_drawers(wing:"wing_agentmemories", room:"llm_init")`**, which sees the room whether or not
its drawers were ever stamped. If it returns drawers, follow with
`am_kg_query(entity:"<root drawer id>", direction:"outgoing")` and check the subject/predicate/object
shapes — that is what separates the canonical root-ID/`must.*`/drawer-ID protocol from this corpus's
`must`/`must_load*`/label one.

⚠ **SUPERSEDED 2026-08-29 BY THE SECTION BELOW, WHICH IS A PROPOSAL AND NOT YET AN ACCEPTANCE.** The
paragraph that follows was written while nothing had decided this, and it is kept because the options
it lays out are still the options. What has changed is that ADR-043 now PROPOSES one of them; the
record is `Proposed` with no owner assigned, and two competing records are open on PRs #75 and #79, so
the decision remains open until somebody signs it. Read the heading below as "proposed by ADR-043",
never as "decided".

**The product decision, unmade:** which layer is canonical. Adopting the served document contradicts
`EntryRoom`, `AGENTS.md`, `model/draf1.md` and ADR-027. Adopting the model leaves the served document
teaching the wrong room to every new agent. Adopting room containment as the server's bootstrap makes
the manual-parity claims false until revised. Any of these is defensible; taking one silently strands
whichever corpus followed another.

**A gate belongs here once the decision is made**, and its universe must come from two real
artifacts: the entry-room name from `palace.EntryRoom` checked against the served onboarding
document, and bootstrap parity derived from a root fixture's ACTUAL outgoing edges with `must.*`
targets in other rooms. The existing test seeds records directly into `EntryRoom`, so it cannot
expose the mismatch.

## A `--socket` install's hooks still speak HTTP — 2026-08-28

Found by an independent review of PR #85; verified from source and made VISIBLE 2026-08-28, not
fixed.

`--socket` registers the agent's MCP over the stdio bridge and does not change `i.mcpURL`.
`hookCommand` (`clients/claude-code/installer.go:1133`) exports that URL — and only that URL — into
every hook command; the socket is never written into one. `listenerFor` (`cmd/server/listen.go:33`)
binds EITHER the socket OR the TCP address, never both. So every hook a socket-only install writes
carries an endpoint nothing is listening on.

**PR #85 changed the symptom, not the cause**: before it those hooks failed on token resolution,
after it they fail on connection. Either way a documented install shape produces hooks that cannot
reach their palace — and because a hook's healthy state is silence, nothing reported it.

**Now it says so.** `warnSocketHooksCannotReachTheServer` warns during a `--socket` install, naming
the variable the hooks carry and why it cannot work, pinned by
`TestASocketInstallSaysItsHooksCannotReachTheServer` — including a subtest that drives
`registerStopHook` rather than the helper, so deleting the CALL SITE goes red. That subtest was
added after review: the first version tested the function directly, so removing the one line that
invokes it left the whole package green. The mechanism built to make a silent failure loud was
itself silent if severed — the same defect one level out. **Its sibling `warnIfRepointing`
(`installer.go:874`) had the identical hole and is now pinned the same way**, because the class is
"a warning whose only test calls it directly", not this one warning; verified 2026-08-28 by
severing that call site and watching the new subtest go red. That is the cheap half: the failure is
no longer silent.

**The real fix is new capability and a product decision, so it stays here.** The `socket` flag
belongs to `install`; the `mcp` subcommand has NO socket flag and only dials HTTP (`dialMCP`), while
verify and stats use `curl`. Making hooks work over a socket means either giving `mcp` a
unix-socket transport and exporting the socket into hook commands, or having a socket-served server
also bind a loopback port. Which one is right depends on whether hooks should follow the bridge or
the server should always be reachable over TCP — nobody has decided that.

A third option was listed here before the warning shipped and is dropped on purpose rather than
forgotten: **have `--socket` refuse to install hooks it knows cannot work.** The warning does the
same job without the cost — a refusal removes every hook from a socket install, so an operator
who wanted the MCP over a socket silently loses capabilities that have nothing to do with the
transport. On a Claude install that is **six registered events** (Stop, SessionStart×2,
SubagentStart, SubagentStop, SessionEnd, `installer.go:960-1005`) across **five** of the six scripts
in `hooks/`; the sixth, `agentsmemory-stats.sh`, is SOURCED by the session-end hook rather than
registered, so calling it a hook is loose. ⚠ Six is CLAUDE-ONLY: `hookPlans` returns after the Stop
plan for any other kit (`installer.go:963-965`), so a codex `--socket` install registers one event
and pi registers none.

Four of the five contact the server. `agentsmemory-subagent-start-hook.sh` deliberately does not —
it reads stdin, `$AGENTSMEMORY_WING` and `.aiagentmemory`, then prints a fixed JSON envelope, with
"no dependency on the binary, the server, or the network" and "deliberately NOT `am_status`: that is
a network call on the dispatch path" in its own comments (`:39`, `:52`). Verified: no `curl`, no
`http`, no invocation of the binary anywhere in it.

**That hook is the argument, not an exception to it.** An earlier version of this paragraph said
"all of them contact the server, so the argument is if anything understated" — which asserts every
hook IS transport-coupled and therefore CONCEDES the premise it was meant to reinforce. The cleanest
instance of a capability a `--socket` refusal would take away for no transport-related reason is the
one hook that needs no transport at all. (`agentsmemory-stop-hook.sh` is a partial second: its
primary job is the exit-2 persist nudge, which needs no server; the `/stats` call is an explicit
optional extra, `:137`.) Saying so and installing them is strictly more
recoverable than not installing them. Named here so the next reader does not re-propose it as new.

**Whatever is chosen, the check must drive a GENERATED hook against a socket-only server.** The
existing socket tests assert the registration, which is the half that already works; the new warning
test asserts the warning, which is not the same as asserting the hook connects.

## A `--local` install gives its hooks no credential — 2026-08-28

**CORRECTED the same day, and the correction is the point.** This entry first claimed the CLI does
not read the token from the Claude MCP registration's `Authorization` header, and that a HOSTED
install was therefore the broken case. That is false. `tokenFromClaudeJSON` reads exactly that
header, it is wired at `clients/claude-code/mcpcall.go:222`, and its doc comment says so. The claim
was an assertion that something DOES NOT happen, published without checking — the exact failure
shape ADR-041 exists to measure, committed while writing ADR-041.

**The real gap, verified 2026-08-28.** `aiagentmemory mcp` resolves a workspace token from
`--token`, `$AGENTSMEMORY_TOKEN`, an `agentsmemory.env` file, or the agent's `.claude.json`
registration header. A `--local` install populates NONE of them: `--help` says of `--local` that
"no token is prompted for", and `registerClaudeMCP` adds an `Authorization` header only when a token
is non-empty. So the CLI refuses with "no workspace token found" — against a local server that
accepts no credentials at all. Every hook that shells out to `mcp` is silent on a `--local` install,
including ADR-041 T4's recall hook.

It is a client-side gate with nothing behind it: the server does not want the token the CLI insists
on having.

**Options.** Have `--local` write `agentsmemory.env` with the token the server was started with (or
a placeholder when it was started with none); or let `mcp` skip the token requirement when the
endpoint is loopback; or have the hook pass a placeholder only for a loopback URL — note the hook
USED to pass `--token …:-local` unconditionally, which broke every install that resolves its
credential elsewhere, so any placeholder must be conditional on the URL.

**Workaround that works today:** write `AGENTSMEMORY_TOKEN=<token-or-any-string>` into
`agentsmemory.env` in the config dir (0600). Verified: the CLI then reports
`token from …/agentsmemory.env` and the recall hook speaks.


## The recall hook searches every project, and its header says "this branch" — 2026-08-28

`agentsmemory-recall-hook.sh` calls `mcp search` with no `wing`. These registrations report
`default_wing: ""`, so per `drawers.go` an omitted wing searches EVERY wing in the workspace rather
than the project the session is in. Observed, not theorised: on 2026-08-28 an independent run of the
hook's exact query shape on two open branches put a drawer from an unrelated codebase into one of
the three slots, both times.

The protocol this repository ships is explicit that another wing's memory is context and never an
instruction, and that unrelated projects "do not remove your answer, they add competitors ahead of
it". PR #95 made the printed header say so instead of asserting a provenance the query cannot
guarantee. That is a label, not a fix.

The fix is to scope the query, and the reason it is filed rather than done is that the hook cannot
resolve the wing the way the protocol says to: rung 0 is what the server's registration reports, and
the hook has no way to ask before it searches. The candidates are all worse in a specific way —
deriving `wing_<repo>` from the git remote is rung 3 and disagrees with rung 0 on at least one live
registration; passing the wing at install time freezes it into a script. Whichever wins is a
decision, not an implementation detail.

## A stale-flagged hit is injected as current — 2026-08-28

The server marks a drawer `stale: true` when its code anchors no longer match the tree, and returns
a warning telling the caller to re-read the code before acting. `agentsmemory-recall-hook.sh` filters
on nothing but the server's `count`, so a stale hit is injected into a fresh context with the warning
dropped. Observed 2026-08-28: a PR#25 drawer carrying `stale: true` passed the 0.42 floor on a real
branch query.

Not fixed in PR #95 because it changes what reaches the model and therefore needs its own
measurement: dropping stale hits shrinks an already-scarce payload, and a stale memory is not
worthless — it is evidence that something changed, which is occasionally the most useful thing in
the page. The choice is between dropping them and labelling them, and that is F-10's kind of
question.

## The palace enforces its most expensive action and leaves its cheapest optional — 2026-08-28

⚠ **ESTIMATED, NOT MEASURED, AND THE FIRST VERSION OF THIS LINE SAID "Measured".** Every figure in the
table below is BPE arithmetic done by the model that emits the tokens, and **that model cannot count
its own output as it generates it** — no instrument in this tree or in the harness reports a turn's
output-token count back to it. Call it ±20% and treat the ORDERING as the finding rather than any
single number: a read is one or two orders of magnitude below a write, and that gap survives an error
far larger than 20%. A ratio quoted from these as though it came from a counter is the defect this
file has retracted before.

The distinction the figures are about is real regardless: OUTPUT tokens are what an agent EMITS, as
distinct from context, which is what a result CONSUMES. The two were conflated in every earlier discussion here and the conclusions
invert when they are separated.

| what the model emits | output tokens |
|---|---|
| `am_skillset` / `am_status`, no arguments | ~15 |
| `am_search(query, wing)` | ~30 |
| `am_get_drawer(id, whole:true)` | ~45 |
| a content-bearing `am_add_drawer` (~1,500 runes) | ~400 |
| a diary entry | ~525 |
| deliberating which drawers to fetch | 500–1,500 |

**So the Stop hook's three mandatory content-bearing writes cost ~1,400–2,000 output tokens per
session, roughly 10× the entire read-side protocol, and every read is optional.** Nothing fails when
a session skips recall; a session that files nothing is reminded until it does.

⚠ **AND THE ONE INSTRUMENT THAT WOULD PRICE THAT MANDATE IS INERT HERE.** `recall-observe` (ADR-041
T1) has written `recall-observations.jsonl` for exactly ONE project on this machine and NOT for this
repository, despite six transcripts. The 7.6% baseline in this file was produced by a hand-run scan,
not by the mechanism built to produce it. `agentsmemory_recall_observe` is invoked only from
`clients/claude-code/hooks/agentsmemory-stats.sh`, which is SOURCED by the session-end hook rather
than registered, needs `aiagentmemory` on PATH and `$TRANSCRIPT` set, and exits 0 silently on every
failure path (deliberately — ADR-041 T1, spec F-5). One of those preconditions is not holding and
nothing reports which.

**The change this argues for is a predicate, not advice.** "Write less" cannot be enforced by asking.
The Stop hook already sees the session's tool history: a session that recalled nothing and decided
nothing has nothing worth filing, and a session that made a decision does. Same hook, conditional
instead of unconditional three.

**Not taken here, because it is a decision rather than a fix.** It changes what the corpus
accumulates, which is a product question, and it should not be made before the measurement below
says whether the accumulation is worth anything. Filed rather than done.

**One number that would make this urgent or moot:** see the next entry.

## PARTLY RESOLVED 2026-09-06 — the instrument shipped, fired, and is starved at 0.27% (filed 2026-08-28)

Both of this entry's blocking claims are now stale, and the finding that replaces them is worse than
either.

**"The measurement is unrunnable" — no longer true.** ADR-028 T3 shipped: `db/migrations/00036_drawer_fetches.sql`
records one row per drawer fetched while naming the recall that sent the caller there. The join this
entry says has no left side now has one.

**"Its trigger cannot fire, nothing passes `search_id`" — falsified.** Measured 2026-09-06 against
the live palace: **11 rows, every one carrying a non-empty `search_id`, and all 11 join a real
`search_events` row.** They span 2026-08-29 to 2026-09-06 and land across ten distinct wing/room
pairs in six different wings — no wing contributed more than three — so this is several sessions
occasionally doing it, not one session doing it once. (The wings are not named here:
`TestNoRealProjectNamesInWings` refuses a real project name in a committed file, and it caught this
paragraph's first draft doing exactly that. The counts are the finding; the names are not.)

⚠ **THE NEW FINDING IS THE RATIO, AND IT IS THE SAME DEFECT ONE LEVEL UP.** In the window those
fetches span, the palace served **4,016 searches**. Eleven of them were followed by a fetch that
named the recall: **0.27%**. The instrument is reachable, correct, and receives almost nothing.

So the original question — *what fraction of filed drawers has ever been returned to anyone* — is
still unanswerable, for a new reason. It is no longer that nothing records drawer identity; it is
that the signal depends on a client volunteering `search_id` and clients essentially do not. A
figure computed from 11 rows would measure which agents happened to read a tool description, and
report it as the corpus's value — which is precisely the error the 2026-08-29 correction on this
entry warns about, arriving from the other side.

⚠ **Do not read 11/12,554 drawers as a usage figure.** `drawer_fetches` began on 2026-08-29 and only
ever sees fetches that name a recall, so it bounds nothing about the other 12,543.

**What would change it:** something that passes `search_id` without an agent choosing to. The
sender is still missing — the tool schema asks for it, and asking is what produces 0.27%. That is
ADR-017's finding again, and it stays open here rather than being restated as a new record.

Original entry, kept because its reasoning and its self-correction are the record:

## Nothing measures whether a filed drawer is ever read — 2026-08-28

`am_recall_stats` reports searches, `answered_pct`, drawers held and the queries that found nothing.
It does not report, and nothing in the tree reports, **what fraction of filed drawers has ever been
returned by any search.**

At the write-to-read ratios this repository keeps measuring — 1.9:1 across one long session, and
**3.0:1** (6 searches against 18 writes) in the two-hour window on 2026-08-28 during which an agent
was explicitly instructed to recall more — the median drawer may never have been returned to anyone.
Nobody has checked, and this entry deliberately makes no claim about the answer.

**The measurement, stated precisely enough to be run:** join `search_events` (or whatever durably
records which memories a page returned) against the drawer table, over the whole corpus, and report
the fraction of drawers with at least one recall, split by wing and by room. `wing_agentmemories`
held 1,080 drawers on 2026-08-29, of which 719 are in `sessions` — bulk-mined transcripts — so the
split matters (the total was 1,077 twelve hours earlier and drifts with every write, which is why it
carries a date; the `sessions` figure has not moved because nothing has re-mined):
a low overall figure driven entirely by mined sessions means something different from a low figure in
`decisions`.

⚠ **BEFORE RUNNING IT, ESTABLISH THAT THE INSTRUMENT CAN SEE A POSITIVE.** This repository's rule,
earned seven times over on 2026-08-28: run the canary before trusting any zero. `search_events` is
written only by `Search`, so a drawer reached by `am_get_drawer`, by `am_bootstrap`, or by a
traversal is invisible to it and would score as never-read while being read constantly. A figure
taken without that check measures the telemetry's coverage and reports it as the corpus's value.

⚠ **CORRECTED 2026-08-29, THE MORNING AFTER, AND THE ENTRY ABOVE IS THE THING IT WARNS ABOUT.** The
join it prescribes has no left side. `search_events` (`db/migrations/00021_search_events.sql:16-26`,
plus `00023` `00026` `00029`) records `hits` as an INTEGER COUNT and carries **no drawer identity of
any kind** — no id, no key reaching the drawer table. ⚠ An earlier version of this correction said
"nine columns"; the base migration declares TEN and two later ones add more, so the count is dropped
rather than repaired — it was brittle, it was wrong, and it was load-bearing for nothing. The
conclusion it was offered in support of is unchanged and is what matters: the row records HOW MANY
hits a page returned and never WHICH. And
`drawers` (`db/migrations/00006_drawers.sql`) has no usage column at all; the two `last_used_at`
columns in this tree belong to API keys and WebAuthn credentials. So nothing anywhere records WHICH
memories a page returned, and the measurement is not merely un-run, it is unrunnable. The paragraph
directly above says to establish that the instrument can see a positive before trusting a zero. It
was written without checking that the instrument exists.

**IT IS NOT NEW WORK, WHICH IS THE USEFUL HALF.** `ADR-028` already owns this and already designed
it: its **T3 — record the fetch against the recall and report the ratio** — is exactly the durable
join, deferred with a written trigger rather than forgotten. `search_id` is minted by `Search`, is
the primary key of the `search_events` row, reaches the wire on every page, and is accepted by
`am_get_drawer`, whose own schema says it "is recorded on the request's trace span, not yet stored
durably". The instrument is one write short. So the right move is not a new table and not a new
record; it is ADR-028's own deferred task, and proposing either would have created the contested
state `adr-state` reports.

⚠ **BUT ITS TRIGGER CANNOT FIRE, AND THAT IS A SECOND FINDING.** T3 starts on *"the first week in
which `am_get_drawer` receives a non-empty `search_id` from any client other than a test."* Nothing
in this repository passes one: grepping `clients/`, `hooks/` and `internal/` for `search_id` finds
the server-side reader, the schema declaration and the response emitter — **no sender**. The only
clients are agents, and the only thing asking an agent to pass it is a tool description.

**Measured on the session that wrote this entry:** it called `am_get_drawer` twice, with that schema
loaded, and passed `search_id` neither time. That is ADR-017's finding arriving from the other side —
prose is the weakest lever — and it is this repository's defect class inverted: not a capability that
is finished and unreachable, but one that is **reachable and unused, gating work that waits on its
use**. A trigger conditioned on a behaviour nothing produces is a task that never starts, and nothing
reports that either.

**AND T3 ANSWERS THE NARROWER QUESTION.** Recording the fetch measures which drawers an agent went on
to READ — implicit relevance. This entry asks which were ever SURFACED, read or not, and a drawer
returned on a page and ignored still cost the corpus its retrieval. The entry above conflated the two.
The surfaced question needs a row per hit per search; the fetched question needs the join that is
already half-built. **They are different measurements and only the second is designed.** Which one
answers "is this corpus worth accumulating" is the surfaced one — so the cheap half-built path does
not close this entry, it narrows it.

⚠ **AND NO FIRST-PARTY CLIENT CAN SATISFY THAT TRIGGER, BECAUSE NONE CALLS `am_get_drawer` AT ALL.**
Checked 2026-08-29 across `clients/claude-code/hooks/` — six scripts, none fetches a drawer; the
recall hook searches and prints. So the trigger is not waiting on wiring that somebody forgot. It is
conditioned entirely on an AGENT choosing to pass an optional argument it read in a tool description,
which is the weakest lever this repository has measured (ADR-017: the full protocol produced 0 recalls
in 5 dispatches, one short paragraph produced 5).

**The trigger has now been met, and meeting it demonstrated the second half of the problem.** On
2026-08-29 this session issued `am_search`, took the returned `search_id` (`964069852bd5cae2572fa9a9`)
and passed it to `am_get_drawer` — a non-test client sending a non-empty `search_id`, which is exactly
what ADR-028 T3 waits for. **Nothing recorded that it happened.** `annotateSearchID`
(`internal/mcpserver/drawers.go:368-374`) puts it on a trace span and the span is sampled, so the
condition "the first week `am_get_drawer` receives a non-empty `search_id`" is now TRUE and
UNOBSERVABLE to anyone who goes looking. A trigger whose satisfaction leaves no durable trace cannot
start the task it gates, however many times it fires.

**What is actually open, in order.** (1) ADR-028 T3's owner decides whether to persist the join
directly rather than wait on a trigger that cannot be observed — the trigger was reasonable when
written and is not reachable as specified. (2) Separately, whether the SURFACED question — which
drawers a page returned, read or not — earns a row per hit per search; it is the one that answers "is
this corpus worth accumulating", and it is undesigned. Nothing here decides either, and this entry
should not be read as authorising a schema change.

**Why it reorders work rather than adding to it.** If the fraction is high, the corpus is earning its
keep and the read-side facts in `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` are the right
next thing. If it is low, the constraint is not retrieval quality at all — it is that we are writing
material nobody will read, and every read-side improvement optimises retrieval over a corpus that
should be smaller. That would promote the entry above and demote the spec, and it is the only
measurement on this page that can do that.

## From ADR-044 (make a small read trustworthy)

Filed 2026-08-29 with ADR-044, in the same commit as the deferrals that point here. Written at the
destination rather than pointed at, because a pointer to a real file that never received anything
passes every check there is.

- **ADR-021 T3's Claude Desktop measurement, still not taken.** ADR-044's read paths travel to every
  MCP client, and exactly one client is confirmed to surface `server.WithInstructions` — a Claude Code
  session, verified 2026-08-22 as an "MCP Server Instructions" block. Desktop is unmeasured, and the
  ADR-021 T3 live measurement has been listed as PENDING since that date. Related and separately
  recorded: `mcp-stdio` takes `--socket/--url/--token` and no `--wing`, so a Desktop registration
  cannot scope itself to a project and falls through to every wing. Deferred from ADR-044 §Out of
  Scope. The cheap experiment is one restart and one question.

- **Retention or pruning of ended records.** ADR-044 T7 makes a correction end its predecessor
  atomically, which means the corpus accumulates ended rows at whatever rate corrections are filed.
  Nothing prunes them and nothing decides whether they should be pruned — `include_history` already
  keeps them out of default reads, so this is a storage question rather than a correctness one.
  Deferred from ADR-044 T7 §Out of Scope. Note ADR-028 T3/T4 defer the same question for
  `drawer_fetches` and `search_events`; if any of the three is answered, answer all three together,
  because a retention story for one table and not its siblings is the shape that gets rediscovered.

## `CorrectionsFor` scans every correction edge on every recall — 2026-08-29

Found by Mindaugas by reading `internal/palace/kg.go`, and confirmed in the source the same day.
Filed here rather than fixed, and deliberately NOT appended to ADR-044's Follow-ups: it is a
performance defect on a path ADR-044 touches, not an obligation ADR-044 took on.

`CorrectionsFor` is the server-side sweep that replaced the three predicate queries this repo's own
protocol tells agents to run by hand. It is consumed by both the search path
(`internal/palace/memory_search.go`) and the bootstrap (`internal/palace/bootstrap.go`), so it runs on
**every `am_search` and every `am_bootstrap`**. Its body loops the three correction predicates and,
for each, calls `KGTriplesByPredicate(teamID, pred, KGStatusCurrent)` — which returns every current
row of that predicate for the team — then discards the ones it did not ask for with an in-Go
`if !want[row.Object] { continue }`.

So the cost is three full predicate scans per recall, independent of how many record ids the caller
actually cares about (a page's roots: on the order of ten). At today's corpus — roughly 150
correction edges — that is invisible, which is why nothing noticed. It grows with the number of
corrections ever filed, not with page size, and this repository's whole supersession story is an
instruction to file MORE of them: ADR-038 ends records instead of overwriting, ADR-044 T7 makes an
atomic correction end its predecessor. The table this scans is the one we are actively encouraging
to grow.

**The fix is reuse, not new code, and it is already in the same file.** `KGTriplesForEntities` sits
directly below `CorrectionsFor` and does exactly the needed thing — `WHERE team_id = ? AND (subject
IN ? OR object IN ?)`, one statement per direction, cost independent of entity count. Its doc comment
records that it was written for precisely this shape of defect one layer over (`factsFor` issuing a
full `KGQuery` per candidate entity). What is wanted is the same batching keyed on OBJECT and
filtered by predicate, so the `want` map becomes a SQL `IN` rather than a Go `continue`.

Two things to preserve when it is done, both of which the current shape gets right by accident:

- The direction. A correction attaches to the record it corrects as an INCOMING edge, so the
  filtered column is `object` and the id exposed as `ReplacementID` is `subject`. The doc comment
  says this is easy to get backwards; a rewrite is exactly when it would be.
- The authorization. `policy.Place` is called on `row.Subject`, never on `row.SourceDrawerID` —
  `targetauth_test.go` exists because checking provenance instead both disclosed foreign
  replacements and suppressed local ones. Any batched version must keep the check on the correcting
  record.

Not measured, and it should be before it is fixed: the claim above is read off the code, and the
sensible before-figure is the statement count and latency of `CorrectionsFor` at current corpus size
against a seeded one. `ADR-029`'s span vocabulary already covers the search path.

## RETRACTED, then narrowed: identical chunks across DISTINCT memories dedupe, and the loser cannot be read whole — 2026-08-29

**The entry that stood here was wrong and is retracted rather than deleted, because the way it was
wrong is the useful part.** It reported that `am_search` returns `content_coverage: 1.000` while
carrying one chunk of fourteen, and named `collapseCandidatesToMemories` as the suspect on the
strength of an anchor-drift coincidence. The symptom was real and reproducible. The cause was **the
fixture**, and the entry should not have been filed before that was excluded.

The fixture built 25 memories as `strings.Repeat("filler prose about other matters entirely. ", 400)`
with only a short unique prefix, so chunks 1..13 of every memory were BYTE-IDENTICAL. Re-run with
per-memory filler, all five memories reassemble correctly — 16 chunks, 19,641 runes each — and
`content_coverage` is right. **The search path's marking was never at fault**, and the correction
this produced in ADR-044 T4's task file has itself been corrected.

### What is real, and it is narrower

Identical chunk CONTENT across distinct memories collapses to one row, and the memory that loses the
row can no longer be read whole. Measured with the degenerate fixture:

    5 memories x 14 chunks       am_add_drawer reported 14 drawers EACH time
    drawer rows actually stored  18, not 70
    MemoryChunks(root of #0)     1 chunk, not 14
    MemoryChunksByRoots          1 chunk for four roots, 14 for the LAST one written

So the last writer keeps the shared chunks and the earlier memories are left holding their opening
chunk alone. `am_get_drawer(root, whole: true)` then returns 1 of 14 and reports no error, which is
the read path this repository added specifically so that a long memory COULD be read as written.

**Two things make it worth an entry even though the trigger is artificial.** `Add` returns
`chunks: 14` for a write that stored one new row, so the write path reports a success it did not
achieve. And the loss is invisible from either end — nothing errors, and the shortened memory reads
as a memory that was always short.

### What has NOT been established

Whether this is reachable on non-degenerate content. A 1,600-rune byte-identical chunk shared by two
different memories is what boilerplate, templates and pasted log dumps look like, but no such case has
been found in this corpus and none was looked for. **Do not price this from the fixture.** The next
step is a corpus query for drawers sharing a `content_key` across different parents — `doctor --corpus`
is the natural home, and it already reports reference classes rather than a single count.

Also not established: whether chunk-level dedupe across memories is INTENDED. ADR-038 owns
`content_key` and its partial unique index, and its diary exemption exists precisely because two
identical entries must stay two records. Whether two identical CHUNKS OF DIFFERENT MEMORIES are the
same case or the opposite one is a question for that record, not an obvious bug.

### The lesson that generalises, and the reason this entry keeps its history

A fixture built from `strings.Repeat` of one sentence is not a scaled-up version of real content — in
a content-addressed store it is a different regime, and it manufactured a defect that looked like a
serious product failure for the better part of an hour. The tell was there in the first measurement
and was read past: `chunks_matched: 1` on a 14-chunk memory says the retrieval saw ONE chunk, which a
correct corpus of 14 sibling chunks would not produce. Vary the fixture before naming a suspect.

## The `omitempty` description gate cannot see a conditional map key, and `withheld` is now one — 2026-08-29

`TestEveryOmitemptyWireKeyInThisPackageIsDescribed` (`internal/mcpserver/wirekeys_test.go`) enumerates
Go struct tags. It has never covered the third population its own doc comment names — *"conditional
`map[string]any` keys, set inside `if` blocks. `out["stale_hits"]`, `out["warning"]`,
`out["supersedes"]`, `out["reason"]`, `out["ended_at"]` and others are emitted where no tag exists to
find. Out of scope here and named so the next reader knows it."*

**ADR-044 T5 added `out["withheld"]` to that population.** Its task file asserted the gate would fail
if the description omitted the key; measured 2026-08-29 by deleting the word, the gate stayed green and
the package passed. Recorded as a deviation in
`docs/adr/ADR-044-make-a-small-read-trustworthy/tasks/T5-a-page-reports-what-it-withheld.md` rather than
by widening the gate, because widening it is its own change.

What the gate's comment already proposes: *"a reflect walk from the registered view types, following
embedding and field types"*. That would also close the two blind-spot fields it names in
`internal/palace` — `replacement_id` and `elsewhere_wing` on `Correction`, the first of which is the
field telling a reader the memory in front of them has been contradicted.

Not filed as an ADR follow-up: the obligation is the gate's, not ADR-044's, and padding a record's
Follow-ups with unrelated work is how an open count stops meaning anything.

⚠ **Until it runs, the `withheld` sentence in `am_search`'s description is held up by nothing.** The
next edit to that description can drop it silently, and every gate stays green — which is the defect
the gate exists to catch, one level up.

## ⚠ An anchor with an EMPTY repo label is checked against whatever tree is open, and the hook then tells that session to re-file good memories — 2026-08-29

**Confirmed in source, reported independently by four sessions in four different repositories on one
day.** This is the highest-severity item in this file: it does not merely fail to help, it recruits
an unrelated session into destroying correct memories.

**THE PROOF** (from the infrastructure session, same file, same working tree, same day):

```
status=missing   path=internal/palace/service.go   repo=""             (their Ansible tree)
status=verified  path=internal/palace/service.go   repo="agentsmemory" line 693
```

One file, two opposite verdicts, differing only by whether the anchor carries a repo label. Their
tree's remote is an Ansible repository with zero `.go` files.

**THE CAUSE**, read in `clients/claude-code/verify.go`: every guard that protects an unknown from
being reported as an absence is conditioned on `a.Repo != ""` — the elsewhere check, the
not-found branch, and the snippet-non-match branch. So an anchor with an EMPTY label in a KNOWN
tree passes all three and falls through to `statusMissing`.

The guards were written for an unknown TREE. The unknown ANCHOR is the case nobody had. The file's
own comment already states the principle it is violating: *"calling it MISSING is not a small
inaccuracy: the honest response to 'the file is gone' is to delete the memory, so a check that
cannot see a file destroys the memory pinned to it. A session did exactly that … Unknown is not
absent."* An unlabelled anchor is an unknown; the code treats it as checkable-here.

**WHY IT IS WORSE THAN A WRONG COUNT.** The verdicts are RECORDED, so the damage is durable, and
`am_search` then flags those memories STALE. The session-start hook prints *"Re-read the code and
re-file whichever are wrong."* A session that complies rewrites correct records — including a
2026-08-25 OTel wiring decision — on evidence from a repository that has never contained that code.

**BOTH HALVES NEED FIXING, and they are different bugs:**
- **Read side:** `missing` must require a POSITIVE repo-label match against the current tree. An
  unattributable anchor joins the "not checked from here" set. This is the same
  could-not-look versus is-gone distinction this corpus keeps re-deriving.
- **Write side, the root:** those anchors were filed WITHOUT a label. If `am_add_drawer`
  (`code_anchors:`) does not default `repo` from the writing session's git remote, every anchor
  filed by a session that omits the field becomes a future false positive somewhere else. Not yet
  checked which, if either, happens today.

Also unverified and worth one command: whether the recorded false verdicts should be swept and
reset, since `doctor --corpus` already reports reference states.

### READ SIDE CLOSED, corpus swept — 2026-09-04

**The read side is fixed and was already fixed when this entry was re-read.** `verifyAnchors` in
`clients/claude-code/verify.go` now derives `attributed` as a POSITIVE match and routes everything
else through `unchecked()`, which sorts an anchor into `unattributable` when it carries no label and
`elsewhere` when it carries someone else's. The two are separate buckets on purpose and the type's
own comment says why: they have different remedies, and folding them together hides the one a human
can act on. So an unlabelled anchor can no longer be recorded `missing`, and the destructive verdict
this entry was written about cannot be produced any more.

**The corpus was swept, and it was NOT the sweep this entry predicted.** Measured against the live
local palace: 189 anchors, **7 unlabelled**, every one of them pinning this repository's own files.
Their recorded verdicts were 5 `verified` and 2 `drifted`, all frozen from before the read-side fix,
because nothing re-checks an anchor it will not attribute.

⚠ **Checking them one by one is what makes this worth recording, because the assumption "unlabelled
⇒ the verdict is bogus" was wrong in both directions.** Two of the three repairs were real drift the
frozen verdict happened to state correctly, and one was the opposite — an anchor recorded
**`verified`** pinning `shutdown, err := telemetry.Setup(...)` in `cmd/server/eval.go`, where **that
snippet no longer appears at all**. The code did not vanish, it MOVED: `telemetry.Setup` is now
reached from a single package-level seam, `var telemetrySetup = telemetry.Setup` in
`cmd/server/telemetry.go`, which `withTelemetry` calls — the arrangement
`TestTelemetrySetupHasOneChokepoint` requires and counts as exactly one. Moving rather than
deleting is precisely why a stale pin could go on reading `verified`: the package still compiles,
the feature still works, and only the pinned lines are gone. That is
the "permanently silent" half of the defect, and it is the more dangerous one — a false `missing`
argues loudly for deleting a good memory, while a false `verified` quietly certifies a memory as
current against code that is gone. Nothing in the corpus would ever have surfaced it, because the
verdict was frozen by the same missing label.

The three affected records — the 2026-08-25 OTel wiring decision this entry names by name, the
eval-parenting incident, and the hosted-MCP-URL SSOT decision — were repaired in place with
`am_update_drawer(code_anchors:)`, which keeps the id and mints no correction. All three were
confirmed live first, since an ended record refuses the call. Two anchors were re-pointed to where
their code now lives (`internal/telemetry/telemetry.go`, `cmd/server/telemetry.go`), one snippet was
re-taken after `searchAttrs(...)` became `attrs...`, and every one now carries `repo:
"agentsmemory"`. All seven read `status: unchecked`, so the next sweep verifies them for real
instead of re-serving a frozen verdict.

**What is still open is the write side, and it is a DECISION rather than a task.** The server cannot
default the label: `internal/mcpserver/drawers.go` builds `palace.AnchorInput` from the request and
nothing there knows the caller's git remote. The tool description already says to always send `repo`
and states the consequence of omitting it. So the remaining choice is whether an anchor without a
label should be REFUSED at write time, or accepted and reported back the way a fan-out warning is —
the first turns working writes into failures for every client that has not been updated, the second
adds a response field, and neither is cleanup.

**Decided as a Proposed record on 2026-09-05:** `docs/adr/ADR-056-an-unlabelled-anchor-is-reported-not-refused.md`
chooses accept-and-report (the fan-out shape), plus a `doctor --corpus` population for the anchors
already filed. The record is not Accepted; the owner decides.

## The wake-up surface counts rows and calls them memories, and counts retracted ones — 2026-08-29

Reported first-hand by a depozitas session; **not yet reproduced here**, so the cause is unverified
and the measurements are theirs.

1. **`am_status` counts RETRACTED drawers; `am_list_drawers` does not.** Same room, same minute:
   `am_list_drawers(<a peer project's wing>, inbox)` → 6, `am_status().wings[…].rooms[inbox]` → 8.
   The difference was exactly the 2 chunks they had just invalidated. The protocol asks sessions to
   close out inbox items so a stale lead is not rediscovered monthly — but the count that greets the
   next session never falls, so closing appears to do nothing.
2. **The inbox count counts CHUNKS and the hint calls them "memories".** 6 rows, 3 memories, pairing
   cleanly by `parent_id`. It scales with how long the sender wrote rather than with how much is
   waiting. Compounds with (1): that room reported 8 for 2 live memories, 4x. The word "memories" is
   what makes it a defect rather than an implementation detail — `am_status`'s own ranking line says
   `unit=memory`, so the wake-up surface is the one place still speaking in rows.
3. **`am_recall_stats` suggestions are polluted by machine-generated recalls.** Four of five entries
   were a git branch name concatenated with changed filenames, and a run of commit subjects — a hook
   issuing recalls keyed on branch plus dirty files. They land in `(unscoped)`, drag that bucket to
   67% answered, and `suggestions` is documented as a to-write list, so following it means writing
   memories to satisfy filenames no human will search for. A to-write list that should be empty.

## `am_status`'s hint recommends an unbounded listing, and `am_list_drawers` has no projection — 2026-08-29

Reported by an infrastructure session, credited. `am_status` composes
*"30 memories waiting in <that session's wing>/inbox — read them first with am_list_drawers(...)"* with
a count and **no `limit`**. Following it verbatim returned **51.2 KB**, over that harness's
tool-result cap, so it spilled to a file and had to be recovered with `jq`.

The documented bound did not save them: past a client's cap the whole result leaves the context, and
an empty-looking room reads as "nothing is filed" — the confusion this palace exists to remove.

The root is the missing projection. For triage a caller wants `id, source_file, content_date,
first-line` and nothing else; today the only way is to fetch everything and discard most of it. With
a projection the hint is safe as written. A proportional `limit` is the weaker fix: it still leaves
a bounded page indistinguishable from an exhausted room, which is the same defect one size down.

⚠ `am_search(room:"inbox", snippet_chars:0)` is NOT the workaround — `snippet_chars:0` means WHOLE
memories, so it is strictly worse. Two sessions read that parameter as its own opposite today.

## Anchor verification is mostly unverifiable from any one checkout — 2026-08-29

One observation, offered as data rather than a rate, and the session that reported it named the
confound itself. An infrastructure session was shown **66 anchors: 0 verified, 0 drifted, 7 missing
(all false, see above), 59 elsewhere**. So ~89% of what it was shown could not be checked from where
it sat.

Anchors are workspace-wide while verification is necessarily per-checkout, so most sessions can only
ever verify a minority of what they are handed. Nobody has measured what fraction of anchors are
checkable by the session that reads them. **Confound, stated by the reporter:** one very large wing
(`wing_agentmemories`) that most sessions are never checked out against plausibly skews this toward
"elsewhere", so it is one observation with a known bias, not a rate. They offered to report the same
three numbers from subsequent sessions, which would turn it into one.

## `am_status`'s inbox hint answers for the REGISTRATION's wing, and its confident count hides the miss — 2026-08-29

Reported first-hand by a front-end session; **not reproduced here**, so the cause is unverified.

Their cwd's git remote basename was their own project; the registration's `default_wing` named a
DIFFERENT project. Step 0c already says rung 0 wins, and they correctly did not fight it. The damage
is downstream of the naming: `am_status`'s `hint` and `inbox` block both answered for the
registration's wing — a confident *"16 items waiting, read them first"* — while **their own
project's wing held 23 inbox drawers that `am_status` never mentioned**, including a same-day item
from another session asking that repo a direct, blocking question about its deploy pipeline. They
found it only by listing their wing by hand.

**The count is what makes this dangerous.** A silent zero invites a second look; a confident 16
does not. A session that trusts the wake-up hint reads another project's inbox and honestly reports
"nothing waiting" for its own — and the handoff convention this palace is built on quietly stops
delivering.

**Their proposed shape, and it is better than either wing winning:** when the registration's wing
and the checkout's resolved wing disagree, SAY SO — *"registration wing X; cwd resolves to Y;
Y/inbox holds N"* — rather than silently answering for X. That turns an invisible miss into a
decision a human can make, which is the same move `resolution` made for `am_kg_query`'s `count: 0`.

Related: the same session hit the unlabelled-anchor defect above, and guessed the anchor scoping
keys off the registration wing rather than the checkout. That guess is NOT confirmed — the
confirmed cause is the empty repo label — but if both surfaces resolve scope from the registration,
they may share a root worth fixing once.

# Cross-session probe, 2026-08-29 — findings from six sessions given adversarial axes

Six Claude sessions in six unrelated repositories were each asked to stress ONE axis of the palace
and report measurements rather than impressions, read-mostly, writing only into their own wings.
Every entry below carries the reporter's own control. **Not one of these was found by our test
suite**, and several are in tools our suite exercises heavily — the difference is that a probe
asks "can I make this lie", and a test asks "does this still do what it did".

## ⚠ FUSION: two distinct memories with no `source_file` become ONE, and nothing marks the seam

**The worst finding of the day, and it upgrades an entry already in this file.** Reported with a
non-degenerate fixture: a shared ~1750-rune preamble of genuinely varied prose (a shared standard
preamble is an ordinary way to write) plus a short unique tail.

- **With different `source_file`** — no collapse. Byte-identical chunk 0, different ids,
  `whole:true` returns each memory correctly. **So ids are not content hashes and cross-memory
  chunk dedupe does not happen on that path** — which RETRACTS the framing of the older entry above.
- **With the same `source_file`** — collapse, handled CORRECTLY: the loser's tail is ENDED with
  `ended_reason: "dropped from <source> on re-file"` and stays readable. That is identity-by-source
  working. (Though `am_add_drawer` returned `chunks: 2, ok: true` and said nothing about having
  superseded an existing memory.)
- **With NO `source_file` at all — FUSION.** `source_file` is optional and most callers omit it.
  The second write reused the first's chunk-0 id, but the first's tail was **not ended** — no
  `valid_to`, no `ended_reason` — it was orphaned. `am_get_drawer(root, whole:true)` then returns
  **3 chunks, two of them carrying `chunk_index: 1`**: one ending in the first memory's subject, one
  in the second's. Two memories about different things, written seconds apart by separate calls,
  returned as one memory whose body says two unrelated things in sequence.

Arithmetic: 2 memories x 2 chunks = 4 rows expected, 3 stored, both writes reported `ok: true`.

**Why it is worse than loss:** nothing is missing, so nothing prompts a search. A memory that reads
continuously and contains two claims prompts nothing at all, and a reader cannot tell the second
half came from a different call about a different subject. *A write that reports success while
inventing a memory nobody wrote.*

### RESOLVED 2026-08-29: it is WRITE TIME, the parentage is wrong ON DISK, and a reader-side fix
recovers nothing

Reproduced in-process with two sourceless memories sharing a preamble, reading the table directly
rather than through any read path:

```
E ids: [5ff5c2a5 4d6d85fc]
F ids: [5ff5c2a5 38f30303]        <- chunk 0 id REUSED at write

rows stored: 3   (4 expected for two 2-chunk memories)
  id=5ff5c2a5  chunk_index=0  parent=(none)    tail="…preamble…"
  id=38f30303  chunk_index=1  parent=5ff5c2a5  tail=" PART F about an index"
  id=4d6d85fc  chunk_index=1  parent=5ff5c2a5  tail="…PART E about a gate"
```

Two rows carrying `chunk_index: 1`, both parented to one root, in the STORED ROWS. So `whole:true`
and `am_search` are reporting the table faithfully — the reporter's arithmetic tell
(`content_length: 3170` against controls of 2401/2486/2273, and `chunks_matched: 3`) is measuring
real data rather than a lookup artefact. Searching for either memory's subject lands on the same
`memory_id`, and neither result says the body contains the other.

**THE CAUSE IS ONE EXPRESSION.** `Add` computes a per-chunk content key as
`contentKeyOf(team, wing, room, SOURCE_FILE, chunk_index, content)` and reuses the id of any CURRENT
row already holding that key — a deliberate feature, so re-filing unchanged text keeps every anchor
and provenance pointer pinned to it. **With `source_file` empty that key is identical across two
different memories whose chunk 0 is identical**, so the second write's chunk 0 resolves to the
first's row, and its chunk 1 is then parented to it because parentage is "the id of chunk 0 of this
write". `purgeSource` — which correctly ENDS the loser when a source IS named — is gated on a
non-empty source, so nothing is ended. All three reported cases fall out of that single expression.

**WHY A MIGRATION AND NOT A READER FIX.** The first memory no longer has a root of its own: its
identity was consumed, not shadowed. Nothing in the rows records that the orphaned chunk was ever
part of a different write, so a repair must infer the split from `chunk_index` collisions under one
parent — recoverable in this fixture because the tails differ, and not obviously recoverable in
general.

**THE REMEDY IS AN IDENTITY QUESTION AND ADR-038 OWNS IDENTITY** — whether a chunk id may be reused
across memories at all, or whether reuse must require the WHOLE memory to match rather than one
chunk. That is a record to write, not a patch to apply, and `doctor --corpus` is the natural place
for the detection half (a `chunk_index` collision under one parent is mechanically findable).

Still open: whether an explicit `source_file` is meant to be REQUIRED for a multi-chunk write.

Fixture drawers were left in place in the reporter's wing.

## `am_diary_read` returns CHUNKS as ENTRIES, and `total`/`showing` make it look complete

Confirmed in source: `repo.Diary` selects drawer ROWS, each becomes a `DiaryEntry`, `DiaryCount`
counts rows, and `last_n` limits rows. Every doc comment on the path says "entries".

Measured: one diary entry of ~4.4k characters stored as 3 chunks. `last_n: 1` returned
`entries: [1 object], total: 3, showing: 1` — content was chunk 0 only, **ending mid-word**, with no
`content_truncated`, no `content_length`, no `chunk_index`, no ellipsis. `last_n: 3` returned three
"entries" with the SAME timestamp: the three chunks of the one entry.

**The fields make it worse rather than better.** `total: 3, showing: 1` reads exactly like paging
over entries — which is what a reader who passed `last_n: 1` expects — so the number CONFIRMS they
got what they asked for. An unmarked truncation leaves you uncertain; this leaves you confidently
wrong. Composite failure: an agent with five genuine entries asks for `last_n: 3` and can receive
three chunks of the newest one, with entries two and three never appearing and nothing saying they
were displaced.

The contrast that makes it a defect rather than house style: `am_search`'s `snippet_chars` is
well-behaved at both edges — `snippet_chars: 1` gave `content_coverage: 0.00246` (honest for what
was returned) and `snippet_chars: <exact length>` gave the whole memory with `content_truncated`
correctly ABSENT. One read path marks partial answers with three fields and a match count; the other
marks nothing and mislabels its count.

## THE ABSENCE CLASS: four tools answer a clean zero where two siblings say "I could not look"

The system has the concept in TWO forms already, which is what makes this a leak rather than a
missing feature: `am_traverse` REFUSES (`room "x" not found`), and `am_search` emits an excellent
note — *"the wing X holds no memories, so this is not a miss: there is nothing there to match. Wings
that do hold memories: …"*, which even lets a typo self-correct. Second control, proving the note is
not always-on: `am_search` with `max_distance: 0.02` (everything filtered out) returns `count: 0` and
NO note. So the distinction is drawn correctly, in one tool out of five.

The four that do not:
- **`am_diary_read`** — a real agent queried against a wing it never wrote to is BYTE-IDENTICAL to
  an agent that never existed: `{"entries":[],"total":0,"showing":0}` both times. Worse, the response
  ECHOES THE AGENT NAME BACK, which reads as confirmation the agent resolved. The tool must know the
  difference — the unfiltered call finds the entries — but the wing filter discards them without
  recording that a filter ran.
- **`am_list_drawers`** on a nonexistent wing, and on a nonexistent room of a real wing: `count: 0`.
  This is the call our own inbox hint recommends, so a typo'd wing reads as an empty inbox.
- **`am_list_rooms`** on a nonexistent wing: `count: 0, rooms: null`.
- **`am_follow_tunnels`** on a wing and room that both do not exist: `{"connections":null,"count":0}`.

## `am_recall_stats` manufactures a to-write task from a typo

One search against a nonexistent wing came back minutes later as
`suggestions: [{Query: …, Wing: "a wing that does not exist"}]`, beside a hint reading *"each entry is
one memory this team looked for and does not have, with which wing to file it in."*

So a wing that does not exist is recommended as the destination for a memory somebody should write.
This is past the absence class: it is not a confident nothing, it is **a task manufactured from a
mistyped argument**, and the task is undoable.

The same response already holds the disproof: that wing's row carries `drawers: 0, writes: 0,
last_filed: ""`. A query against a wing with zero drawers and zero writes is a typo or a probe, not
an unmet need. `rerank_skips: {"empty": 1}` is in the same payload, so the empty-corpus case is
already detected one layer down.

## FIVE MORE COUNTS count rows and count the dead, and one new axis: rooms outlive their contents

Method: one memory (3 chunks) filed into a NEW room in a wing no other session writes, sampled
before, after filing, and after retracting. Correct behaviour is +1 then 0.

| tool / field | before | after file | after retract |
|---|---|---|---|
| `am_list_rooms` (that room) | absent | 3 | 3 |
| `am_list_wings` (that wing) | 82 | 85 | 85 |
| `am_recall_stats.drawers` | 82 | 85 | 85 |
| `am_memories_filed_away` | — | +3 | stays |

`am_list_wings` and `am_list_rooms` are fixed in the wake-up-counts PR. **`am_recall_stats.drawers`
and `am_memories_filed_away` are not** — and the latter is the worst, because the defect is in its
name and in the sentence it emits: *"N memories filed across 17 wings and 27 rooms"*. It is rows, and
it counts retracted ones, so a headline number is inflated on two independent multipliers. Its count
equals the sum of `am_list_wings` drawers, so they are one aggregation with two names.

The contrast, same room, same instant: `am_list_drawers` → `count: 0`; `am_list_rooms` → `3 drawers`.
The correct filter exists and the aggregates do not use it, which suggests one shared fix.

**THE NEW AXIS: an empty room is still advertised, and cannot be un-created.** After retraction the
room still appears in `am_list_rooms`, `am_list_wings`, `am_graph_stats` (`total_rooms` and
`rooms_per_wing`) and `am_memories_filed_away`. Rooms are created implicitly by first write and there
is no un-create — so a mistyped room name (`decisons`, a stray capital) is a permanent addition to a
palace's taxonomy that no agent can remove, only an operator with the database. The reporter created
exactly such a room writing the report and could not remove it.

Excluding rooms with no live memories from room counts closes both halves: the empty room stops being
advertised, and a typo self-heals the moment its contents are retracted.

**Deliberately clean, do not "fix" on the strength of the pattern:** `am_kg_stats` reports
`triples / current_facts / expired_facts` — the total is stated AND the split is stated, so a reader
cannot be misled. That is the shape the others should copy: not "hide the dead" but "say which number
you are giving". And `am_status.coverage.expected` is a ROW count correctly, because the search index
stores chunks and comparing memories to indexed chunks would compare unlike things.

## A drawer correction does not reach the FACTS derived from it

`am_kg_query` returns facts with `current: true` whose `source_drawer_id` names a drawer that has
since been superseded, with no marker that the provenance was corrected. `status: "all"` returns no
ended twin, so nothing is auto-invalidated.

The composition is what makes it sharp: a single `am_search` returned, in one payload, the stale
fact (`current: true`, citing the ended drawer) and the correcting drawer (carrying `supersedes` and
`superseded_reason`) — side by side, linked by nothing but an id the reader would have to notice
matches.

**The design is not obviously wrong** — our docs are explicit that a fact records what was believed
then, and a fact can outlive its source. What is wrong is that the correction primitive is
DRAWER-SHAPED and the graph is downstream of it, and nothing tells the person holding the context
that dependent facts exist. A cheap version: have `am_update_drawer` name, in its result, the facts
whose `source_drawer_id` it just ended — not auto-invalidating them, but making the sweep visible to
whoever can judge it. Reported by the session that had filed the facts, corrected the drawers, and
was never prompted to connect the two.

## `am_bootstrap` returns `corrections: null` for a wing that has five, indistinguishably from none

A wing with five corrections filed the same day returned `{"corrections": null, "eager": null,
"on_demand": null, "entry_point": {"resolution": "unknown_term"}}`. The sweep appears gated behind an
entry point that wing does not have, so it is inert — and `null` is what a wing with genuinely no
corrections would return. That is the absence class in the one call whose selling point is that it
sweeps corrections server-side. May share a root with the entry-point backfill already filed.

## The knowledge graph's entity axis is effectively write-only for natural keys

`am_kg_query(entity: "ADR-013")` → `unknown_term`. `entity: "<a project name>"` → `unknown_term`.
Yet facts about both exist, under stored keys that are long descriptive sentences — 704 entities are
sentences rather than names, findable only by predicate query. `resolution: unknown_term` is honest,
but honest `unknown_term` on every natural key makes the axis unusable. This plausibly explains why
the graph is the least-used half of the palace.

Related, same reporter:
- **Two paths disagree about one fact's subject.** A case-insensitive entity match echoes the
  CALLER's casing in the returned fact; the same fact via predicate query and `am_kg_timeline`
  carries the stored form. A diffing consumer sees two entities.
- **`am_entry_point` breaks its own distinguishability promise.** Identical
  `{edges: null, node: "", resolution: "unknown_term"}` for a wing that exists with no entry point,
  another that exists, and one that does not exist. Its doc says a wing with no entry point "says so,
  distinguishably from an error". Wants `no_entry_point` vs `unknown_wing`.
- **Tunnel activation is never recorded.** Following a tunnel returned its target, and an immediate
  re-list showed `access_count: 0` and `last_activated == created_at`, unchanged. All five tunnels
  touching that wing show `access_count: 0` since creation. Either the counter is dead or following
  is not activation; either way listing and usage disagree.
- **A read result recommends a mutation.** `am_graph_stats` and `am_traverse` embed a note ending
  "Run `am_recompute_graph`" — a workspace-wide write, suggested to every reader with no mention of
  the blast radius.
- **A documented example points at nothing.** `predicate: "retracts"` → `unknown_term`. The deployed
  relation is `retracted_because`. Our own protocol uses `retracts` as the canonical audit example,
  so the example and the data have drifted.

## Probe hygiene, recorded because it is now in the data

The absence-class probes left two rows in `am_recall_stats`: one search against a nonexistent wing,
which will appear in `unanswered` and `suggestions` for 24h and reads like a real project to anyone
scanning the list cold, and one deliberately over-filtered query that reads as unanswered. Both are
noise from this exercise. Discount them, or remove them if a route exists that is not destructive.

## The corpus holds ONE `mutant killed` whose exit code contradicts its own detection path — 2026-08-29

Swept after a quality-harness session found a FALSE KILL in its own tool: a fence pointed at
`nosuchrunner` exited 127, matched no build-broken pattern, and fell through to
`mutant killed · a test went red`. An absent runner recorded as evidence that a suite noticed a
broken mechanism — and it predated the report. Their fix routes it to `environment_failure`.

**The retroactive consequence is what made this worth sweeping: a `killed` row is worse than no row,
because the tool-written stamp is what makes it trusted.** Every mutation-log entry written on a
machine with a missing or misnamed runner is suspect.

**This corpus is clean of the 127 signature.** Measured 2026-08-29 on `main`:

```
grep -rho 'mutant [a-z]* · exit [0-9]*' docs/adr --include='*.md' | sort | uniq -c
  195  mutant killed · exit 1
   39  mutant survived · exit 0
    6  mutant inconclusive · exit 1
    1  mutant killed · exit 2
grep -rn 'exit 127' docs/adr --include='*.md'   →  no matches
```

**But the single exit-2 row does not survive reading, and exit 2 is the ambiguous code** — from a
gate it can mean "I refuse this" or "I could not run".

`ADR-021-.../tasks/T3-does-the-instruction-change-the-answer.md:98` —
`2026-08-25 · 8c3167d* · mutant killed · exit 2 · README.md`, for a typo mutation that should make
`TestReadmeNamesEveryInstallableAgent` fail.

Its fence is a `docker run … sh -c 'set -e; …'` whose detection works like this:

```
go test … -run "TestReadmeNamesEveryInstallableAgent" … | tee /tmp/a21t3.out
grep -q -- "--- PASS: TestReadmeNamesEveryInstallableAgent" /tmp/a21t3.out
```

The `go test` is PIPED, so its status is `tee`'s and `set -e` does not fire there. **The detection is
the `grep -q` finding no PASS line — and `grep` exits 1 on no-match.** So a genuine kill by this
fence exits **1**, which is what the other 195 rows show. `grep` exits **2** on a FILE ERROR. The one
row at exit 2 therefore carries an exit code inconsistent with the path it claims to have taken.

**NOT established: that it is a false kill.** The tree was dirty at the time (`8c3167d*`), the fence
also runs `apk add`, `go vet`, a second `grep` against `internal/web/windows-guide.md` and a full
`go test ./...`, and any of those could produce a 2 by a route not reconstructed here. What is
established is that the row deserves the second look the other 195 do not, and that nobody has given
it one.

**RUN, 2026-08-30 at `0ebdad2`: the kill path exits 1.** Applied the mutation this row describes —
typoed both `--agent claude-desktop` occurrences in `README.md` — and ran T3's Acceptance fence
verbatim, tree clean before and after:

```
FENCE_EXIT=1
installer_test.go:1880: README.md never shows `--agent claude-desktop`, so a reader cannot tell the kit installs for it
--- FAIL: TestReadmeNamesEveryInstallableAgent (0.00s)
```

It fails exactly where this entry predicted, at the `grep -q -- "--- PASS: …"` line. The recorded
`exit 2` is now measurably inconsistent with the path this fence takes when it kills. **Still not
established that the row is false** — the tree was dirty at `8c3167d*`, and the reproduction rules
out the detection path, not every other command in the fence. ADR-021 T3 is still pending on its
human-observed half, so the task is live rather than archived.

**The general rule, worth more than this row:** a fence's detection path has a KNOWN exit code, and
an entry whose code differs took a different path. `exit 127` is the signature to grep for first;
`exit 2` from a gate is the one that needs reading rather than grepping.

## `! grep …` cannot fail a `set -e` fence, and 50 of our vacuity guards are written that way — 2026-08-30

Found while reading the fence above. POSIX specifies that `set -e` is IGNORED for a command whose
exit status is inverted with `!`. So the guard every fence here writes to mean *"the output must not
contain a failure marker"* fires and the script carries straight on:

```
$ printf 'FAIL\n' > /tmp/x.out
$ sh   -c 'set -e; ! grep -q FAIL /tmp/x.out; echo REACHED; exit 7'   -> REACHED, rc=7
$ bash -c 'set -e; ! grep -q FAIL /tmp/x.out; echo REACHED; exit 7'   -> REACHED, rc=7
$ sh   -c 'set -e;   grep -q NOPE /tmp/x.out; echo REACHED; exit 7'   -> rc=1   (control)
```

Both shells. The `! grep … && next` form is inert for the same reason — the `&&` short-circuits and
the script continues.

**Two forms, and only one is broken.** Swept `docs/adr/**/*.md` by fence block:

| | count |
|---|---|
| guards inside an `&&` chain with no `set -e` (short-circuits — **works**) | 5 |
| guards inside a `set -e` script (**inert**) | 50 |
| inert **and** the only detector in that block | **0** |

⚠ **NOTHING IS BROKEN TODAY, and that is the finding's actual shape rather than a softening of it.**
Every one of the 50 sits beside a POSITIVE assertion — `grep -q -- "--- PASS: …"` or
`grep -q "^ok"` — and a lane that scored no tests prints neither, so the positive check already
catches the vacuous case the negated one was written for. The `! grep` line is redundant, not
load-bearing. That redundancy is precisely why its inertness never produced a failure anyone
investigated.

It is a **latent hazard, not a live defect**: it READS like the vacuity check, and the first fence
written or edited without the positive assertion beside it loses the vacuity check silently. The
un-negated form costs nothing:

```bash
if grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/out; then exit 1; fi
```

**Why this is the same finding as the classifier one, from the other side.** PR #117 records that
`adr-verify` cannot tell a fence that scored no tests and PASSED (vacuous — inconclusive is right)
from one that scored no tests and FAILED BECAUSE IT DETECTED THAT (a kill). This is the guard that
was supposed to produce the second case, unable to produce it at all inside `set -e`. Reported to
the quality-harness session 2026-08-30.

**Not gated.** A check would have to parse a fence block, tell a `set -e` script from an `&&` chain,
and decide whether a positive assertion covers the same lane — and with zero live offenders it would
be a gate against a hypothesis. Recorded here so the next fence author reaches for the `if` form; a
gate earns its place the day an offender exists.

**The general rule:** a guard that has never fired is not evidence that it works. Before trusting
one, make its condition true and watch the exit code.

## From ADR-045 (move a memory, not a row)

- **The vectors a superseded memory leaves behind, and whether a job queue should reap them.**
  `supersedeInto` ends the predecessor's rows in SQLite and upserts the successor's vectors beside
  them; it deletes no points. The validity filter runs POST-retrieval in Go
  (`internal/palace/memory_search.go:87`, counted as `drops.Superseded`), and the design note above
  it says the consequence out loud: an ended drawer keeps its vector, so the index still returns it
  and a page can come back shorter than `limit` with nothing saying why. Measured 2026-09-01 against
  the hosted palace: `am_status` reported `drawers.indexed` 8972 against `total_drawers` 8935. The
  widen loop (`k *= 2`, ceiling `8 × candidateK`, `memory_search.go:20`) refills most short pages;
  past that ceiling recall degrades with only an `am.dropped_superseded` trace attribute to show it.
  Three options, none free: **delete the points** — cheapest, but `include_history` searches the
  same index, so history becomes address-reachable and no longer searchable, and `IndexDrift` starts
  counting every ended row as `IndexMissing` until it learns that an ended row is expected to have
  no point; **stamp `valid_to` into the payload** and filter server-side, which keeps history
  searchable and costs one more payload key; **a separate history namespace**, which keeps both at
  the cost of a second index to maintain. Note for whoever takes it: `Hybrid.Delete`
  (`internal/store/hybrid.go:449`) deletes source-of-truth FIRST and index second on purpose,
  because `Rebuild` reconstructs the index from the source of truth — deleting the index first and
  crashing lets the next `Rebuild` resurrect the point. On the queue question specifically: the
  desired index state is derivable from SQLite (`valid_to`, and `content_key` if it is stamped into
  the payload), so a reconciler in the every-boot prepare slot beside `BackfillContentKeys` and
  `BackfillWingRoots` repairs drift from causes it never witnessed — including every correction that
  predates the mechanism — while a queue repairs only what it recorded. A queue is a latency
  optimisation ON a reconciler, never a replacement for one; it earns its place when the O(corpus)
  scan becomes the binding constraint, which at ~9k drawers it is not.
  **Trigger: the first time a recall is measurably short because superseded points crowded the
  prefix, or when the corpus is large enough that the drift scan itself costs.**
  Deferred from `docs/adr/ADR-045-move-a-memory-not-a-row.md` §Out of Scope.

## From ADR-046 (serve the whole entry record)

- **Paging or byte-bounding the eager bootstrap tier.** ADR-046 makes `am_bootstrap`
  serve each eager record WHOLE, which is what makes `truncation.omitted: 0` true
  instead of merely present. It leaves `bootstrapEagerLimit` bounding how MANY records
  are inlined and nothing bounding how LARGE each is, so a long entry record now costs
  its full length on the one call no session skips. That is the deliberate trade —
  a front door that is correct beats one that is cheap — and the discipline that a
  spine points at detail rather than inlining it becomes advice, exactly as
  relocatability did in ADR-045. **Trigger: the first time a wake-up is measurably
  expensive because of entry-record size, or the first entry record past a few
  thousand runes.** The shape to reach for is a byte budget with an honest
  `truncation` report, NOT a refusal at write time — the refusal is what ADR-046
  removed, and reinstating it under a different name would be the same workaround
  wearing a new shape. Deferred from
  `docs/adr/ADR-046-serve-the-whole-entry-record.md` §Out of Scope.

## From the review of PR #147 (two independent reviews, 2026-09-01)

- **`doctor --index` compares the payload's WING only, so a ROOM-only drift reports clean.**
  `internal/palace/indexdrift.go` reads `p.Payload["wing"]` and compares that alone. A move now
  writes both keys with one `SetPayload`, and when that call fails the operator is pointed at a
  check that can only see half of what went wrong — a memory whose stored room is stale answers
  no room-scoped search while `--index` says the index and the rows agree. The fix is small
  (compare both keys, report which one drifted) and the cost of not doing it is the shape this
  corpus keeps recording: a check whose green is narrower than the question the reader asked.
  **Trigger: the next time anything is added to a point's payload, or the first room-scoped
  recall that comes back empty against rows that look right.**

- **`sync --repair-payload` refuses every backend but Qdrant.** `cmd/server/sync.go` returns an
  error unless `VectorBackend == qdrant`, so the remedy named in `Service.Update`'s fail-open
  warning does not exist on the default sqlite backend or on chromem. It is narrower than it
  sounds — on sqlite the source of truth IS the index, and chromem refills from SQLite at boot —
  but "run this command" is advice an operator on those backends cannot take. Either teach the
  command to say what to do per backend, or have the warning name the backend-specific route.
  **Trigger: the first operator report of that warning on a non-Qdrant deployment.**

- **A pinned tunnel does not follow a move.** `tunnelRow` stores the source and target wing+room
  beside the drawer id, and `canonicalTunnelID` hashes wing+room; `moveMemory` updates neither.
  ADR-045's claim that "every pinned tunnel goes on naming a live row" is true of the ID and
  misleading about REACHABILITY: after a move, following the tunnel from the new location finds
  nothing, and following it from the old previews a drawer that has left. Note for whoever takes
  it: rewriting the endpoint changes `canonicalTunnelID`, so this is a re-key rather than a field
  update, and the same pointer-survival question ADR-045 answered for drawers has to be answered
  again for tunnels. **Trigger: the first move of a memory that has an explicit tunnel pinned to
  it — today rare, and it stops being rare as soon as relocation is advertised as free.**

- **`moveMemory` has no compare-and-set.** `MemoryChunks` and the ended-record check both happen
  BEFORE the transaction opens, so a concurrent supersede or move landing in between relabels rows
  the caller never read. `supersedeInto` already solves the same race properly, by counting open
  chunks inside the transaction and treating a short `RowsAffected` as `ErrConcurrentCorrection`.
  Source-traced, not reproduced; narrow while relocation is rare. **Trigger: adopt the supersede
  path's CAS the first time two writers are expected on one wing, or the first unexplained
  half-moved memory.**

- **`memoryChunkQuery` has no `valid_to` filter**, so reassembly reads ended siblings alongside
  live ones (`internal/palace/repo.go`). Pre-existing — the search path has always used this query
  — but ADR-046 put it on `am_bootstrap`, which is the one call no session skips, so the blast
  radius changed even though the code did not. Confirm first whether an ended chunk can share a
  live root at all: `supersedeInto` mints a new root for the successor, which may make this
  unreachable in practice and therefore a comment rather than a fix. **Trigger: before adding any
  second caller of the reassembly path, or the first entry record that reads with a duplicated
  passage.**

- **~~`mcptest.NewLocalWithWing` is dead, and the local-mode tenant edge it proved is now covered by
  nothing.~~ CLOSED 2026-09-06 (issue #161), by giving it back a scenario rather than deleting it.**
  ADR-038 removed the local-only tools (`am_delete_wing` and the three sibling deletes), which were
  this constructor's only consumers; the constructor survived them, and the `docs/architecture.md`
  Test Doubles row was the only thing left making it look reachable. Its comment promised it "proves
  the HTTP edge injects the fixed local administrator that makes their handlers reachable" and no
  test asked that any more — `internal/auth/local_test.go` covers `auth.LocalTenant` as a unit, so
  the middleware was never unproven; what was unproven was that the mounted local server WIRES it.
  `internal/mcptest/localedge_test.go` now drives the real server through that edge with no
  credential, asserts local mode, and writes and reads back through it, so a per-request identity or
  a read-only one fails it. Both scenarios go red when the middleware is unmounted from
  `newLocalServer`, which is the mutant that decided keeping it was worth a file. Deleting the
  constructor was the alternative and was rejected for one reason: the cost lands on whoever adds
  the next tool behind `Deps.Local`, at the moment they are least inclined to rebuild a harness.

## From ADR-047 (measure the writing rule, not only the ranking knob)

Filed with the ADR, in the same commit as the deferrals that point here — a pointer to a file that
never received anything passes every check there is.

- **LongMemEval-V2, its web/enterprise domains and any leaderboard submission.** V2's harness is
  Python: a backend registers with `@register_memory` and implements `insert(trajectory)` /
  `query(q)`, and a fixed reader model scores it. Comparability with a public leaderboard is a
  different goal from deciding what a skill should tell an agent, and it would put a conda
  environment into a Go repository whose entire gate corpus reads Go source. Revisit if anyone
  needs an externally comparable number rather than an internally actionable one.
- **Crossing the write/query policies with the ~30 ranking arms in one table.** The most
  informative shape and the one whose cells go thin fastest. Worth doing once the policy axis has
  produced a non-neutral result, so the cross is spent on a difference that exists.
- **Running the full ~48-session haystack for all 500 questions.** ADR-047 runs a declared subset.
  The full run is a corpus-scale ingest per cell and deserves its own cost estimate before anyone
  starts it; the subset ids and seed are in `.cells.json` so a larger run stays comparable.
- **A write policy that calls a model to summarise, and a judge with partial credit.** Both were
  kept out of ADR-047 deliberately: a generative policy makes its row partly a measurement of that
  model, and a rubric judge introduces a second thing to argue about before the first has produced
  a number.
- **Re-deriving ADR-003's cited LongMemEval figures** — summary-as-key costing 0.134 Recall@5, and
  +9.4% for the concatenated variant. ADR-003 §Out of Scope marks this `permanent`, on the reason
  that they are corroboration and the decision rests on our own runs. That reason is untouched;
  what changes is that ADR-047 builds the instrument which would make re-deriving them possible.
  Recorded here because `permanent` is the one disposition `adr-debt` never sweeps, so a boundary
  that becomes re-openable has to be written down somewhere that is read.
- **Nothing measures whether a promoted rule is actually followed.** ADR-047 T5 can put an
  instruction into a centralised skill and has no way to observe compliance. Related to
  §"The product is a runtime quality control plane, not an eval score" — the same gap, one level up.
- **A tokenizer tied to the reader model, so the shared context budget is counted in tokens.**
  ADR-047's central invariant is one budget every cell shares, and the pilot enforces it in
  **runes**, because `go.mod` declares no tokenizer and `internal/palace/chunk.go:52-54` already
  records that the palace cannot ask one. Raised in review of PR #148, and the finding is right
  that a rune bound is an approximation: policies that rewrite and split text differently can
  carry different token counts at the same rune count. What the pilot does instead is record the
  reader endpoint's own reported prompt-token count per cell, so the realised spread is measured
  rather than assumed. Revisit when a run's realised spread actually exceeds its declared
  tolerance — a tokenizer dependency bought before that would be bought on a hypothesis.
## From ADR-048 (retire the reinforcement fields nothing writes)

Receipts for that record's two deferrals, written with it rather than after it, so `adr-debt` finds
the destination knows about them.

- **The four dynamics columns on `hallways` and `tunnels` are still there.** ADR-048 removed
  `strength`, `stability`, `last_activated` and `access_count` from the wire only. The columns keep
  their `NOT NULL` defaults, and `palace.Dynamics` keeps its json tags, because
  `internal/palace/hallway.go` still reads `LastActivated` as a fallback input to `earliestStamp`
  when preserving a hallway's `created_at` across a rebuild — the #38 stamp repair. Dropping the
  columns needs a migration with no path back for the data, and buys nothing until something either
  revives a verified-access signal or confirms nothing will. **Decide it then, not before.** Note the
  asymmetry that makes this safe to leave: storage that nobody reads costs bytes, whereas the wire
  fields cost a reader a wrong belief, which is why only one half was urgent.

- **Entity extraction quality — issue #41's second half — is untouched and still needs a
  measurement.** `internal/palace/entity.go` harvests capitalised Go identifiers through a general
  proper-noun regex, which is why the wings holding pasted source produce hallways by the thousand
  and the prose wings produce none. The obvious narrowing is already known to be wrong: `GitHub`,
  `PostgreSQL`, `API` and `Handler` are the same lexical shape, and ADR-016's T1 measurement
  overturned an ALL-CAPS exclusion once already because it would have killed `HTTP`, `MCP`, `ADR`,
  `TEI` and `RRF`. So this wants a preregistered measurement over the real corpus before any
  heuristic ships — the same discipline ADR-014 applies to a ranking default.

## From the 2026-09-02 corpus repair (no ADR yet)

Filed after repairing 16 facts whose `source_drawer_id` named no row, on this project's own
palace. Both entries are receipts for work that was NOT done, written now because the evidence
was in front of me and will not be again.

- **`doctor --corpus` reports damage nothing can repair.** It is deliberately read-only —
  `TestTheReadOnlyPathMintsNothing` exists so a checker never reports on a palace it has just
  changed — and no `am_*` tool clears or repoints a `source_drawer_id`. So the only route from
  "16 facts name no row" to a clean corpus is raw SQL against the container's database, which is
  what was done here: stop the server, `docker cp` the file out, `UPDATE`, copy back. Three
  separate ways that goes wrong were found by doing it. The database is in **WAL mode**, so
  copying `agentsmemory.db` alone silently omits the sidecar — 4 MB of recent writes on the day
  measured. `docker exec` cannot run in a stopped container, so a removal step written that way
  fails silently. And `docker cp` writes as the host uid, so the next start fails with `attempt
  to write a readonly database (8)` on a file that is perfectly intact. The first two together
  replayed a stale WAL over a changed main file and produced `database disk image is malformed`
  with a crash-looping server. **Any repair path worth shipping has to own those three, which is
  the argument for it living in the tool rather than in each operator's shell history.**

- **Nothing notices when a fact goes FALSE, and `--corpus` is structurally blind to it.** The
  check asks whether a pointer RESOLVES, never whether the fact still agrees with the memory it
  points at. Measured the same day: a fact of the shape `<host> -[<permits-a-thing>]-> <value>`
  sat `current` and answerable for two weeks, while a drawer in the same wing recorded that the
  setting had been turned off estate-wide on a named date. Search returns both; nothing
  reconciles them. The dangling pointers were cosmetic beside this — a current fact asserting a
  weaker security posture than production actually has is a false belief an agent acts on, and
  it was found only by reading the drawer a repointing exercise happened to open. The specifics
  stay in the palace, which is private; this repository is public, and none of the argument
  depends on which host or which setting. **Whether this is detectable at all is the open question**: the
  cheap version (flag a fact whose source drawer was superseded after the fact's `valid_from`)
  is a heuristic that would have caught this one and will produce false positives, so it wants a
  measurement over the real corpus before it wants an implementation. This is ADR-shaped and has
  no record yet.

## From #167 (the ADR-047 review findings, closed 2026-09-03)

One of the four is a gate hole rather than a defect, and it is filed here rather
than widened under that issue because the existing exemption was measured and a
widening has to be measured the same way.

- **`TestDocCommentsMatchTheirDeclaration` cannot see a single-word lowercase
  unexported name.** `goldRank` carried `retrieved`'s doc comment — wrong name and
  wrong contract, `go doc` handing a reader "whether" for a function returning a
  rank — and the gate was green over it. It escapes through `looksLikeIdentifier`,
  which requires `capitals >= 2`: `retrieved` is an ordinary English word, the
  `declared[...]` lookup is false because the old name no longer exists in the
  file, and the check `continue`s as prose. **Every single-word lowercase
  unexported name in the tree is outside this gate.** That is the same class the
  gate's own comment says it was extended to cover in `6f17446f`, after the live
  defect it exists for landed on an unexported method — so the widening is
  plausible and the cost is not known. The existing exemption is justified by a
  measurement (4 sites in 1,141 documented unexported declarations); a widening
  needs its own count of how many ordinary prose openers it would newly flag,
  because a gate that fires on correct comments is the gate somebody deletes.
  **Revisit with that measurement, not before.** The comment itself is fixed.

## From ADR-052 (one writer, many readers)

- **Route reads onto the read handle in the nine SQL-owning packages ADR-052 T5 did not touch** —
  `internal/tenant`, `internal/billing`, `internal/share`, `internal/skill`, `internal/skillset`,
  `internal/usage`, `internal/passkey`, `internal/mergejob` and `internal/store/sqlitevec` all take
  the one `*gorm.DB` built at `cmd/server/main.go:1116` and read and write through it. ADR-052 scopes
  its refactor to `internal/palace` because threading a second handle through eleven packages in one
  change is a rewrite wearing a refactor's clothes, not because the other nine are correct. Three of
  the six read-first transactions the record measured are in `internal/tenant` (`tenant.go:476`,
  `:514`, `:547`), so this is the half of the class that keeps the defect. Deferred out of ADR-052
  §Out of Scope and T5.
- **Extend `TestEveryServingHandleDeclaresItsRole` past `cmd/server`** — ADR-052 T6's gate reads the
  composition root's AST and can say which openers exist; it cannot say whether a read method in some
  other package routes itself onto the writer handle. The record states that limit rather than hiding
  it, and closing it needs a different shape of check than an AST walk over one package. Deferred out
  of ADR-052 T6.
- **Write the concurrent-mutation scenarios `internal/mcptest` still cannot honestly measure** — the
  existing entry under ADR-008 names two blockers: no statement of what a write race should do, and a
  harness opening SQLite without the server's pragmas. ADR-052 T3 closes the second and measured the
  first assumption to be backwards — the pragma-less harness failed a read-then-write shape 79 times
  in 320 against 273–281 under the shipped DSN, so it under-reports rather than over-reports. The
  remaining blocker is the undecided semantics, which is ADR-010's question. Deferred out of ADR-052 T3.
- **Give `internal/mcptest` a read handle as well as a write handle** — ADR-052 T3 makes the harness
  open the shipped writer configuration, which is what makes its measurements honest. It does not give
  the harness the reader/writer split the server will have after T4 and T5, so a scenario cannot
  exercise the `query_only` boundary end to end. Deferred out of ADR-052 T4.
- **ADR-042's `-race` follow-up is STALE and should be closed where it is written** — it says "Run the
  test suite under `-race` in CI. Nothing in this repository does: every workflow…". That stopped being
  true on 2026-08-30, when `11c7176` added a dedicated `race` job running `go test -race -timeout=30m
  ./...` as a REQUIRED branch-protection context (`.github/workflows/build.yml:148`); `release.yml:59`
  runs it too. ADR-052's first draft read the follow-up, believed it, and proposed a seventh task to add
  what already existed — caught only because the `race` check reported green on ADR-052's own PR. Nothing
  swept it, because `adr-debt` reports an open follow-up faithfully and cannot know the work landed
  elsewhere. Close it in ADR-042, and treat this as the recurrence it is: a written obligation outlives
  the condition that created it. Found via ADR-052.
- **A parity gate between the CLI `mcp` adapter and the HTTP one** — restated here only because ADR-052
  §Out of Scope points at this file for it; the substantive entry is already recorded under ADR-008 and
  is not duplicated. ADR-052 adds nothing to it beyond noting that a reader/writer split is one more
  thing the two adapters could diverge on. Deferred out of ADR-052 §Out of Scope.
- **Bound the four other unbounded graph reads** — `Traverse`, `ListTunnels`, `ListHallways` and
  `FollowTunnels` return rows with no limit at any layer. Enumerated 2026-09-04 with `grep -rn
  "Limit(" internal/palace/kg.go internal/palace/graphquery.go internal/palace/graph.go
  internal/palace/tunnel.go internal/palace/anchors.go`: only `KGTimeline`
  (`kgTimelineLimit = 100`) and `ListAnchors` (a caller-supplied limit) are bounded. ADR-053 fixes
  `KGQuery` and names these rather than pretending the class has one member. `Traverse` is the one
  with a live reproduction against it — 62,952 bytes, spilled, three independent reproductions on
  2026-08-29 recorded in the `start-here` skill — and it is deferred rather than fixed because its
  fan-out is dominated by the same containment edges ADR-053 T2 hides, so the number to design
  against does not exist until T2 lands. Deferred out of ADR-053 §Out of Scope and T1.
- **Decide whether per-drawer containment edges should still be minted** — ADR-036's migration mints
  one `room:<wing>/<room> —holds→ <drawer id>` edge per drawer. Measured 2026-09-04 on the live
  palace: 580 of 586 derived edges are these listings, they are every oversized fan-out in the graph
  (`room:wing_craft/gotchas` alone is 184 edges, ~16.9KB of raw fields), and they answer a question
  `am_list_drawers` already answers with a budget and paging. ADR-053 T2 hides them behind
  `include_containment` rather than removing them, deliberately: hiding is reversible in one
  parameter and a migration over 580 live rows is not, and the flag is what will show whether
  anything ever asks for them. Revisit once it has. Deferred out of ADR-053 §Out of Scope.
- **A query-length or request-body limit on `/mcp`** — `am_search`'s `query` parameter promises "max
  250 chars" and nothing enforces it: measured 2026-09-03, a `tools/call` carrying a 9.5 MB query
  string was accepted and answered HTTP 200 after 11.7 seconds. ADR-053 bounds what a graph read
  RETURNS and says nothing about what a call may send, which is the other half of the same surface.
  The care needed is on the write side — a body limit below the largest legitimate `am_add_drawer`
  turns a working write into a refusal. Deferred out of ADR-053 §Out of Scope; the measurement is
  already filed as a gotcha in `wing_agentmemories`.
- **Return facts from `am_list_drawers`** — ADR-053 T4 gives `am_get_drawer` the facts block that
  `am_search` already has. A listing is the third reader of the same shape and is deliberately not
  in that record: a page of whole drawers is already the response most likely to spend its budget,
  so adding a facts block there needs a measurement rather than a symmetry argument. Deferred out of
  ADR-053 T4.
- **Insource the four things entire.io does better, each on its own evidence** — reviewed 2026-09-04
  against `https://entire.io/blog/introducing-agentic-search-for-code-and-context` and
  `https://docs.entire.io/llms.txt`. Their whole corpus is DERIVED — 30+ commands and no write path,
  no memory bank, no decision log — which is the half this palace deliberately does not do, and it
  costs them the class of thing source cannot settle. What it buys them is the half we are weak at,
  and the owner selected all four on 2026-09-04: (1) **symbol-anchored drawers**, anchoring a memory
  to file plus symbol rather than a verbatim snippet, so a rename does not drift a memory that is
  still true — 11 of 171 anchors were reported drifted on this repository on 2026-09-04; (2)
  **automatic recall on context**, so the agent never has to decide to remember, which their `/search`
  skill achieves and our hooks only half do; (3) **a measured token/step benchmark** — they publish
  70/90 → 81/90 at "less than half the tokens and half the steps", and this project has no
  equivalent number for recall-with-memory against recall-without; (4) **derived memory from commits
  and transcripts**, the largest of the four and their entire product rather than a feature. Each
  needs its own record: (1) is a schema and mint change, (2) is a hooks change, (3) is an eval, and
  (4) is a new ingestion path. Deferred out of ADR-053 §Out of Scope.
- **A fetch does not return facts EXTRACTED FROM the drawer, only facts that point AT it** — ADR-053
  T4 gives `am_get_drawer` a facts block built from `KGQuery(Entity: <drawer id>, Direction: "both")`,
  so it carries every edge where the drawer is an endpoint. That covers the case the task singles out
  as highest value — a correction attaches as an INCOMING edge, so this is where `retracts`,
  `supersedes` and `qualifies` surface. It does NOT cover a fact whose only tie to the drawer is
  `source_drawer_id`: provenance rather than reference. Discovered 2026-09-04 while writing T4's first
  test, which had asserted the provenance case; the test was corrected to the scoped one rather than
  the scope widened silently. Closing it needs a repo lookup that does not exist — `grep -rn
  "source_drawer_id = ?" internal/palace` returns nothing — and a decision about whether a memory's
  fetch should carry facts somebody derived from it, which is a different question from what the
  graph says about it. Deferred out of ADR-053 T4 §Out of Scope.

## A shape heuristic for machine recalls needs its false-negative rate first — 2026-09-04

Deferred from `docs/adr/ADR-054-a-search-records-who-asked.md` (Alternatives, Out of Scope). ADR-054
records the origin of a search so hook-driven recalls stay out of `am_recall_stats`'s to-write list.
The alternative — classifying a query as machine-shaped by its text (mostly filenames, mostly commit
subjects) — is the heuristic-that-eats-real-questions this project has overturned before, and today's
data shows both failure directions in one top-ten: *"inbox findings handed over from another
project"* is a real question a shape rule keeps, while a run of three commit subjects reads as prose.
Revisit only if, after two default windows past ADR-054's deploy, `suggestions` still carries
machine-shaped entries with `hook_searches` non-zero — and only with a measured false-negative rate
over the real corpus, not a rule that looks right.

## A recompute rebuilds the hallway graph from every row, including ended ones — 2026-09-04

Deferred from `docs/adr/ADR-055-a-room-is-its-live-memories.md` (Out of Scope). Found by the
enumeration pattern that record's T1 records, on its first run, by a reviewer.

`Repo.DrawersForHallways` (`internal/palace/graph.go`) loads `(room, entities)` filtered on
`team_id` and `wing` only, and `computeHallwaysForWing` counts every returned row. Measured
2026-09-04 in a hermetic test: two drawers in one wing share an entity pair, one is retracted with
`InvalidateDrawer`, and the recomputed hallway still reads `co_occurrence: 2` with the retracted
drawer's room in its `rooms` list. So a retracted memory keeps pushing a pair over the hallway
threshold, and a room with no live memory can keep its name on a live hallway — which `am_list_rooms`
says does not exist.

The question is not "is this a bug" but "what is a hallway ABOUT". If it is what a wing currently
holds, this is the same defect ADR-055 fixes one surface over and the query wants `valid_to = ''`.
If it is what a wing has ever talked about, history belongs in it and the rule should be written down
rather than left as an unfiltered query nobody chose. ADR-055 declined to settle it because it
neither lists nor counts rooms, which is that record's whole subject.

## The kit could label an anchor for the caller, and nobody has to remember `repo` — 2026-09-05

Deferred from `docs/adr/ADR-056-an-unlabelled-anchor-is-reported-not-refused.md` (Out of Scope).

ADR-056 reports an unlabelled anchor back to the caller because the server cannot know the
caller's repository. The Claude Code kit CAN: `aiagentmemory` runs in the checkout, reads the git
remote for the verify hook already, and could put the label in front of the model — in the tool
description it installs, in a `SessionStart` line, or by filling `repo` on the way through a proxy
it does not have today. Each of those is a different mechanism with a different failure mode (a
description is read once, a session line is read before the first write, a proxy is a component
that does not exist), and none removes the need for the server-side report, because Codex, pi and a
raw MCP client do not run the kit. Worth a record once T1 has shipped and the population `doctor
--corpus` reports is measured to be still growing.

## RESOLVED 2026-09-05 — tests spawn children with no deadline, and a killed runner leaves them to launchd

Standing rule from the owner, relayed by another session on 2026-09-05: every child a test, hook
or script spawns carries a timeout, and the runner reaps its children before it exits. The
incident behind it was in another project — two hook children from a mutation run hung in regex
backtracking after their test had already recorded FAILED, were reparented to launchd, and burned
most of a core each for fifteen hours; the owner found out from the fan noise.

This tree has the same shape. Counted 2026-09-05 with
`grep -rn --include='*_test.go' 'exec\.Command(' . | grep -v CommandContext`: 41 sites, most of
them `exec.Command("bash", hook)` in `clients/claude-code` (`recall_test.go`, `hooks_test.go`,
`anchorcue_test.go`, `plugin_test.go`, `verify_test.go`, `recall_mergesubjects_test.go`), plus
one each in `internal/repohygiene` (two files), `internal/importer` and `internal/contractaxis`.
The hook the tests run shells out to `aiagentmemory mcp search`, whose HTTP call carries a client
timeout (`clients/claude-code/mcpcall.go`), so a hang in the SERVER call is bounded — but the
`bash` child itself has no deadline, and `go test`'s package timeout kills the test binary without
its grandchildren, which is exactly the reparenting the rule exists to stop. Of the shipped hooks,
only the session-end and verify hooks wrap their work in `timeout`; the two recall hooks do not.

Not measured: whether any child has actually leaked here. `ps -Ao pid,ppid,%cpu,etime,comm -r`
on the development machine that day showed nothing reparented from a test. The fix is mechanical
(`exec.CommandContext` with a deadline at every site, `timeout` around the recall hooks' one
call) and belongs in its own PR, with a gate that greps the universe so a 42nd site joins the
check on the same commit.

**Resolved the same day.** `internal/testexec.Command(t, …)` is the one door: deadline from the
test's context, own process group, group killed on cancel. `TestEveryTestChildCarriesADeadline`
refuses a direct `exec.Command` in any test file; `TestADeadlineKillsTheChildAndItsChildren`
proves the grandchild dies too. The two recall hooks are NOT
done by this: their `aiagentmemory mcp search` call carries a client `--timeout` (60 s default), so
the HTTP request is bounded, but the hook PROCESS is not — it can hang before or after that call,
and neither wraps anything in `timeout` while session-end and verify do. **The hook half closed
the same day, one layer up:** every registration the kit writes now carries Claude Code's
`timeout` (75 s — strictly above the 60 s client `--timeout` the recall hooks inherit, so the inner
call gives up before the harness kills the hook), a registration an older kit wrote without one is
upgraded in place, and the plugin manifest `hooks/hooks.json` — the second registration path,
hand-kept — carries the same number; `TestEveryHookRegistrationCarriesATimeout` derives both from
source. Review of the first draft found the plugin path unbounded beside a title that said "every". The harness
kills the hook at that bound; whether it reaps the hook's grandchildren is the harness's business,
not something this tree can gate. NOT covered: the four
non-test `exec.Command` sites in `clients/claude-code` (`installer.go` running install scripts,
`mineclaude.go` and `verify.go` reading a git remote) — production code, a different change.

## `doctor` could ask codebase-memory for `index_status` over stdio — 2026-09-05

Deferred from `docs/adr/ADR-057-codebase-memory-is-a-checked-peer.md` (Out of Scope). The strongest
check of the peer is to spawn it and ask whether this repository's index is ready; T1 stops at the
files because a diagnostic that starts a daemon inherits that daemon's stale-IPC start (recorded
2026-08-31: a leftover socket makes start poll to a 30 s timeout). Worth doing once that start is
bounded upstream.

## A session that compacts loses its wake-up, and nothing hands it back — 2026-09-05

Deferred from `docs/adr/ADR-058-the-recall-injection-is-a-digest-with-a-budget.md` (Out of Scope),
from a quality-harness session's finding the same day. Two hook events worth wiring: SessionStart
with matcher `compact`, re-emitting the wake-up (am_status summary, wing, inbox count) after
compaction — which is exactly when a session starts acting on stale assumptions; and PreCompact,
appending a two-line state note (wing, the open thread the diary would record) for the post-compact
SessionStart to hand back. Not in ADR-058 because that record is about the SIZE and SCOPE of what
one recall injects, and these are new events with their own reachability questions (which stdout
each injects — ADR-051's four-event set is the gate to extend).

**RESOLVED by `docs/adr/ADR-059-a-compaction-hands-back-the-state-it-discarded.md`, 2026-09-05 —
and the first half was already true.** The SessionStart hooks are registered matcher-less, so
they fire on `compact` and the wake-up recall was re-emitted after every compaction all along; a
`compact` matcher would have registered a duplicate. What was missing was a note written BEFORE
the summary (a `PreCompact` hook, T1) and a post-compaction recall that asks for the session's own
`llm_open_threads` checkpoint instead of the cold-start question (T2). Still deferred under
ADR-059's name: reading the note on `resume`, where a note from the same session id may be hours
old and the tree may have moved under it. **That deferral is RESOLVED by
`docs/adr/ADR-061-the-wake-up-hands-back-the-last-turn.md`, 2026-09-05**, by a different note:
the Stop hook writes a per-project last-turn note at every turn end, and `startup` and `resume`
hand THAT back — with its date — rather than the compaction note.

## The `wing_craft` call sits just above the floor for most prompts — 2026-09-05

Found by review of #274 (ADR-059), from the same hand-run method that caught the inert checkpoint
recall, and measured the same day against the local palace with six real prompt shapes: five of
six returned ZERO `wing_craft` hits under the hooks' `max_distance=0.42`, with the nearest craft
memories at 0.439–0.454 — just above the floor — and only the craft-shaped question ("the mutant
survived, what does that mean for the test") cleared it, at 0.342–0.395. So the `craft:` block in
both recall hooks (`agentsmemory-recall-hook.sh`, `agentsmemory-task-recall-hook.sh`) is mostly
silent on task prompts, and ADR-058's own T2 sign-off recorded one such silence as an aside.
Facts still flow (the digest's fact lines carry no floor), which is why the block is not entirely
mute. Whether 0.42 is doing real work there — keeping weakly related craft out — or losing
near-misses at 0.439 cannot be decided from six prompts: 0.42 was calibrated for in-wing `diary`
recall (ADR-041 T4), and a different wing sits just above it more often than not. Needs a
measurement over a day of real prompts with hit QUALITY judged, not hit count, before the floor on
that one call moves; ADR-054's `am_recall_stats` is the instrument. Not changed in ADR-059 because
`wing_craft` is a whole wing of mixed material, unlike `llm_open_threads`, where the room is the
scope and the floor guarded against nothing.

## The task-recall hook recalls on `<task-notification>` payloads — 2026-09-05

Found while sampling `search_events` for the git-history measurement
(`docs/measurement/2026-09-05-git-history-twenty-queries.md`): rows whose query is a
`<task-notification>…` block, i.e. the UserPromptSubmit hook ran its recall on text the harness
generated when a background task finished, not on anything a human typed. Four such rows on
2026-09-04 in one sample; each is a search round-trip at the moment the model is waiting, and each
enters `am_recall_stats`' to-write list as an unanswered question nobody asked. The hook already
refuses a slash command by design (ADR-051 T4); a payload opening with `<task-notification>` or
`<system-reminder>` is the same class and should be refused the same way, with the refusal on
stderr. Small; its own change.

## Git-history minting passed its gate at 7 of 20 — 2026-09-05

The parity note's precondition (twenty real queries, would history hold the answer, under a quarter
means distraction) was measured on 2026-09-05: 7 of 20, all answered by commit or PR BODIES and
none by a subject or a diff (`docs/measurement/2026-09-05-git-history-twenty-queries.md`, with its
sample bias stated). A record is worth writing, scoped to bodies as verbatim episodes in their own
room, origin-stamped (ADR-054), measured on this repository first. Awaiting the owner's decision to
draft it.

## ADR-062: arming the re-ground monitor without `/am` — 2026-09-05

T3 makes a compacted session WOKEN rather than merely instructed: the recall hook leaves a marker
and a persistent monitor over that directory turns its appearance into a notification, which makes
the session take a turn. The monitor is armed by `/am` Step 1d, so a session that never ran `/am`
gets only ADR-062's printed `PAUSE`. That floor is deliberate and stays, but the ceiling is reachable
for more sessions than it currently is — a `SessionStart` that could arm the watch itself, or the
kit shipping the loop as a script the command only has to name, would both remove the dependency on
one command having been typed. Neither is drafted; the shape to avoid is a second copy of the loop,
since the whole point of extracting it from `am.md` in the test is that one directory is named once.

## ADR-062: `startup`, `resume` and `clear` after a context replacement — 2026-09-05

ADR-062's Out of Scope defers the non-compaction starts, naming PR #278 as the open proposal for
what such a start hands back, because two changes writing the same injection is how they drift. This
line is that deferral's receipt in the file it points at — `adr-debt` reported it UNRECEIPTED
(the destination existed and never named the source ADR), which defeats the sweep that justified
punting the question in the first place.

## `SessionStart` has a fifth source, `fork`, and the wake-up ignores it — 2026-09-05

The recall hook branches on `source`: `compact` reads the PreCompact note, `startup` and `resume` read
the last-turn note, and anything else falls through to the plain branch-work recall with `wing_craft`
second. Claude Code's hooks reference lists **five** SessionStart matcher values — `startup`,
`resume`, `clear`, `compact`, `fork` — and `fork` is not one the hook has ever handled. Measured
2026-09-05 with a real last-turn note on disk: `source=startup` opens with the `Last turn (…)` block,
`source=fork` does not, and falls straight to the recalled memories. A forked session is a context
that continues work someone else was doing, so it is arguably the case that most wants the note. Not
changed on discovery because the open question is INTENT, not mechanism, and the mechanism is nearly
free: the last-turn note's key is per PROJECT on both sides (`basename $CLAUDE_PROJECT_DIR` plus a
checksum of its path, `agentsmemory-stop-hook.sh` and `agentsmemory-recall-hook.sh`), so a fork in
the same directory already resolves the key its parent wrote — `lastturn_test.go` writes the note
under session `s1` and reads it back under `s2`, which is exactly that case, tested. The cost of
handling `fork` is one token at the `startup`/`resume` test. What is not free is deciding whether a
fork WANTS the parent's last turn, since a fork is often a deliberate divergence — the same argument
shape as `clear` below. The PreCompact note and the re-ground marker are per SESSION and would not
resolve for a fork; only the last-turn note would. Review of #293 caught the first version of this
entry giving a mechanical reason that does not hold, which made a one-token change look like design
work. `clear` is deliberately excluded and stays so — a cleared
context is a deliberate reset, and handing back what was cleared would defeat the user's own action.
Found while checking M's collected references (Reddit, claudikins, anthropics/claude-code#32407)
against what this kit already ships.
