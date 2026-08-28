# ADR backlog

Work deliberately punted out of an accepted or proposed ADR, kept here so it resurfaces at the
next `/adr-write` instead of dying in a scope section. `adr-debt docs/adr` sweeps the `(deferred:)`
pointers that lead here.

An entry leaves this file in one of two ways: it becomes an ADR, or it is re-tagged
`(permanent: <why>)` in its originating ADR because we decided it should never happen.


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
requires every human sign-off to name its outcome from `ship` / `withdraw` / `blocked`, requires the
sibling README to carry the status that outcome maps to (`done` / `failed` / `blocked`), and requires
the task's own acceptance section to OFFER all three — because the defect was a template prescribing
two values, and a gate demanding three beside a template offering two reproduces the dead end for
the next operator. It derives its universe from the corpus. ADR-001 T3's row now reads `blocked` and
its hint reads `decision <ship|withdraw|blocked>`.

**And this is a class rather than a one-off, which answers "why gate for a single case".** ADR-004's
supersession gate reached the identical third state on 2026-08-24 — recorded in the palace as
*"REFUSED — NOT 'no' … the gate could not answer. Those are different facts"* — a run that completed,
produced a third outcome, and had two slots to record it in. Issue #34 has been open on that
ambiguity since, before this finding existed. Two ADRs, two routes, one missing value.

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
change to `is_done` plus a vocabulary. It shares the externality question with the entry *"The ADR
evidence chain depends on a tool outside the repository"*, which resolves in this file today. ⚠ The
`Depends-on` limitation is a third finding in the same tool, but it lands with PR #91 and is NOT in
this file yet — an earlier version of this sentence cited it by a heading that exists only in this
paragraph, which is the pointer-to-nothing failure the corpus keeps producing. Once #91 merges,
three findings in one external tool is itself an argument that the vendoring option deserves a
decision.

**Not taken here, because it is the owner's:** ADR-001 is `Accepted` and its own T3 said to stop the
ADR. Whether that means re-running T3 against a corpus that is not saturated, or withdrawing the
record, is a decision this entry files rather than makes.

## ADR-041 T2 — the recall-before-assertion baseline, measured 2026-08-28

**27.6%** — of 221 no-change assertions across 46 sessions, 61 were preceded by a recall.

| | |
|---|---|
| sessions | 46 |
| assertions | 221 |
| preceded by a recall | 61 |
| **rate** | **27.6%** |
| classifier | v2 |
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
| hallways | **0**, and structurally so | Not "nobody ran the build step" — `am_recompute_graph` was run across all 8 wings on 2026-08-20 and returned `hallways: 0, entity_tunnels: 0`. Hallways are entity co-occurrence, and 82 of 82 drawers have an empty `entities` column: `Service.Add` (`internal/palace/service.go:305`) builds its `Drawer` literal without one, and the only code that ever calls `extractEntities` is `internal/palace/mine.go`. Mining is retired, so nothing writes the input. |
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

2. **Decide the entity graph: feed it or retire it.** This is the repository's own named defect,
   and the largest instance of it yet. Hallways, derived entity tunnels and the entity half of
   `am_traverse` are written, tested and reachable by tool call — three MCP tools and a rebuild
   command — and all of them return nothing, because their single input is written by one retired
   code path. `am_mine` calls `extractEntities`; `Service.Add` does not, so every drawer filed by
   `am_add_drawer` or `am_diary_write` carries an empty `entities` column, 82 of 82 today. The tests
   pass because they exercise the component (given entities, compute hallways) rather than the
   selection (does anything ever produce entities), which is the same shape as the eval arm that
   won four tables while being unreachable from production.

   Two honest options, and the measurement should pick between them. **Feed it:** call the existing
   entity extractor on the normal write path, so hallways and derived tunnels describe the curated
   corpus rather than a mined one — cheap, since `closetEntities` already exists and runs on
   content we already hold. **Retire it:** delete the hallway/entity-tunnel derivation and the two
   tools that expose it, and keep explicit tunnels only. What is not an option is leaving three
   tools in a catalogue of 41 that answer every call with an empty list, because an agent reading
   the catalogue cannot tell that apart from a palace that simply has no links yet.

   Whichever way it goes needs a gate that fails when the input dries up again — a test asserting
   that a drawer written through the normal path carries entities, which fails today and is
   therefore the right red test to open the ADR with.

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

Removing all nineteen would invalidate every Verification Log entry taken under them (adr-lint
rejects a `done` whose logged command no longer matches), so it is a deliberate sweep rather than a
drive-by: strip the guards, re-run adr-verify on every completed task, commit between runs. Until
then, scope a multi-package acceptance to the package that holds the tests.

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
- **Does a date-only `valid_to` mean *through* that day, or *as of* it?** Issue #74. `temporalEndKey`
  (`kg.go:117`) stretches a date-only `valid_to` to `T23:59:59Z`; `inEffectAt` (`:962`) excludes only
  below `as_of`. So `status:"current"` drops an ended fact immediately while `as_of` keeps it for the
  rest of that day — two filters, two answers, one day, nothing documenting the difference and no test
  pinning either reading. ADR-038's `am_kg_supersede` sidesteps it by stamping instants rather than
  dates, deliberately: answering it changes what the 15 already-ended facts on this palace mean.
  **Trigger: before any second consumer of `as_of` ships, or the first time someone reports a fact
  that "did not go away today".**
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
- **The interval is CLOSED where a validity window wants half-open.** Extends issue #74 from the other
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
`am_search` and `am_get_drawer` (`recallrate.go:51`), and `AGENTS.md:370` mandates
`am_get_drawer(id, whole:true)` once per `must.*` edge AS PART OF the wake-up sequence — dozens of
edges, before the task search. So a protocol-following wake-up flips the latch almost immediately.
`am_skillset` and `am_status` cannot flip it — nor can `am_bootstrap` (`AGENTS.md:345`),
`am_list_drawers` (`:368`) or `am_kg_query` (`:369`), none of which are in `recallTools`; an
earlier version of this sentence said "only" of the first two and was over-precise.

⚠ **That premise has an expiry the entry should name.** The wake-up flips the latch *because*
`AGENTS.md:357-362` records `am_bootstrap` returning `unknown_term` for this wing, which is what
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
2. **The metric has a ceiling any protocol-following session hits trivially.** `AGENTS.md:370`
   mandates `am_get_drawer(id, whole:true)` once per `must.*` edge as part of the wake-up sequence —
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
- Reset the latch at a boundary (user message, or compaction) and re-take the baseline.
- Bind "preceded" to subject relatedness — the honest reading of the spec's intent, and much the
  hardest to implement.

**The baseline must be re-taken under whatever definition wins.** A rate is only comparable with
another measured the same way; T2's number cannot be carried over.

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

⚠ **My earlier discriminator was wrong.** I claimed one `am_entry_point` call against production
settles it. It does not: `unknown_term` cannot distinguish "no `llm_init` room" from "`llm_init`
drawers that predate derived containment edges". The right call is
**`am_list_drawers(wing:"wing_agentmemories", room:"llm_init")`**, which sees the room whether or not
its drawers were ever stamped. If it returns drawers, follow with
`am_kg_query(entity:"<root drawer id>", direction:"outgoing")` and check the subject/predicate/object
shapes — that is what separates the canonical root-ID/`must.*`/drawer-ID protocol from this corpus's
`must`/`must_load*`/label one.

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

