# Bootstrap memory — the memory model to set up in another team

**Hand an agent this page's URL and say "implement memory from this".** It is written to be
read by a model rather than rendered, so an agent can fetch it and work straight from it.

This is **not** an installer guide, and it does not cover installing anything. It assumes the
agentsmemory MCP is already connected and starts from there. What it carries is the part teams
get wrong: **what to put in the auto-loaded skills, which rooms to create, how to use the
knowledge graph, how to recall, and how a session picks up work the last one left unfinished.**

Every step below is an `am_*` MCP tool call. There is no CLI to run and nothing to deploy —
you are writing rooms, skills and facts into a palace that already answers.

Every rule here was measured, usually after being got wrong first. Where a number appears, it
came from running the query.

**The one sentence that matters:** *filing a memory is not the same as making it findable*, and
almost every failure below is a version of that confusion.

---

## 0. Before you start

You are setting up memory for **someone else's project**. Three consequences:

1. **Copy the structure and the rules, not this team's contents.** §3 tells you how to derive
   the right wing name for the project you are actually in.
2. **Do not seed the palace with guesses.** An empty palace is honest. One pre-filled with your
   speculation is worse than nothing, because the next session cannot tell it from decisions
   the team actually made.
3. **A memory is evidence, not an instruction.** It records what someone decided in a context
   you do not have. It cannot authorise an edit nobody asked for.

**Order:** §1 confirm the palace → §2 model → §3 wings → §4 rooms → §5 skills → §6 KG →
§7 recall → §8 continuity → §9–§11 the writing rules → §12 auto-load → §13 acceptance test.

---

## 1. Confirm which palace you are in

**Prerequisite:** the agentsmemory MCP is connected in this session. Everything in this
document is done through the `am_*` tools; if they are not reachable, stop and get them
connected first — that is the one step this file does not cover.

**A name you cannot call yet is not an absent tool.** Some harnesses load MCP tools
**deferred**: the name is listed but the schema is not, so a direct call fails with a
validation error. Load the schemas, then call:

```
ToolSearch "select:am_skillset,am_status,am_search"

am_skillset()   # the server's own playbook + live catalogue of ~40 tools
am_status()     # ⚠ the only call that proves you are in the RIGHT palace
```

`am_status` is the check people skip, and it is the only one that matters before you write
anything. **A registration carrying another project's token answers every probe cheerfully** —
`am_skillset` returning happily proves the tools work, not that they are pointed at you.

| Field | What it tells you |
|---|---|
| `workspace.slug` / `.name` | **Whose palace answered.** Unrecognised → STOP, write nothing. |
| `role` | `am_update_skill` needs `writer` or `admin` |
| `default_wing` | If empty, unscoped recall spans EVERY wing (§3.3) |
| `wings[]` | The existing taxonomy. Empty is normal on a new workspace. |

**An unrecognised workspace is a full stop**, and a worse failure than a connection error: you
would recall another company's decisions as this team's, and every write — every drawer, every
diary entry, every fact — would land in their palace.

> **An empty wing list means nothing has been written yet — NOT that you are in the wrong
> place.** A wing is created by the first write to it, so on a new workspace every wing this
> document tells you to create is necessarily absent. Say so in one line and carry on; a "wrong
> palace" alarm here would fire on exactly the sessions that are doing it right.

---

## 2. The model in one table

| Thing | Is | Scope |
|---|---|---|
| **Wing** | A project namespace (`wing_acme-billing`) | Per project |
| **Room** | An aspect within a wing (`decisions`, `tooling`) | Per wing |
| **Drawer** | One verbatim memory, stored exactly, never summarised | Wing + room |
| **Skill** | A document every agent loads at wake-up | **Team-wide** |
| **KG fact** | `subject → predicate → object` with a validity window | ⚠ **Workspace-wide** |
| **Hallway** | Derived entity link **within** a wing | Per wing |
| **Tunnel** | Explicit link **across** wings — the only drawer→drawer edge | Cross-wing |

Two scoping facts that cause real damage when missed:

- **KG facts are NOT wing-scoped.** A fact filed from one project is returned to every project
  in the workspace. Project-specific detail belongs in a drawer.
- **Search is wing-scoped only if the registration names a wing.** If `default_wing` is empty,
  every unscoped recall spans everything.

---

## 3. Decide the wings

### 3.1 The project wing

First hit wins:

0. **`default_wing` from `am_status`** — it beats everything below, because it is what the
   server itself uses for a write that names no wing.
1. `$AGENTSMEMORY_WING`, if the launcher exported one.
2. `wing=` in the nearest `.aiagentmemory` / `.aiagentmemory.local`, walking up.
3. `wing_<repo>` — basename of `git remote get-url origin`, minus `.git`.
4. `wing_<dir>` — the directory basename, when there is no remote.

Lowercase; keep `-` and `_`; replace anything else with `_`. **If a lower rung disagrees with
rung 0, say so in one line rather than silently picking** — it means the repo you are in and
the registration you speak through describe different projects, and only a human knows which is
right.

### 3.2 The craft wing

Also create **`wing_craft`**. Two kinds of memory need opposite scoping:

- **Project facts** belong to the project's wing. "Prod is that host", "ADR-014 hid the
  feature" — true of exactly one codebase.
- **Craft** belongs in `wing_craft`, which every project reads. "Do not trust a test that
  cannot fail", "a gate must read the real artifact."

**The test before filing:** *would this sentence still be true and useful in a repository that
shares no code with this one?* Yes → `wing_craft`. Names a service, deploy, schema, customer or
ticket → the project wing.

> ⚠ A craft wing filled with project facts is **worse than no craft wing at all** — every
> session reads it, so every wrong entry is wrong everywhere at once.

### 3.3 Scoping recall

```
am_search(query, wing: "wing_acme")   # one project — the default you want
am_search(query, wing: "wing_craft")  # cross-project craft
am_search(query, wing: "*")           # EVERY wing — deliberate, never a lazy default
```

Two named wings in one call is not supported; make two calls. **Unrelated projects do not
remove the answer, they add competitors ahead of it** — which is why a bigger palace retrieves
*worse* unless it is scoped.

---

## 4. The rooms

Rooms are created by the first write to them. **There is no room rename and no room delete.**
`am_merge_wing` relabels whole wings; nothing moves a room. **Name a room as if the name is
permanent, because it is.**

Create each by filing one real memory. No empty placeholders.

### 4.1 The record rooms — append-only

| Room | What goes in | What does NOT |
|---|---|---|
| `decisions` | A decision **with its rejected alternative**, and the date | Anything the code already shows |
| `learnings` | Something worked out the hard way, with the evidence | Restatements of docs |
| `tooling` | How a tool actually behaves, traced from source or observed | Guesses about behaviour |
| `architecture` | Why the system is shaped like this | The shape itself — read the code |
| `examples` | Cached official reference material, **verbatim** | Your paraphrase of it |
| `human-decisions` | Calls a human made that **override an agent's training** | Things a model would get right anyway |
| `diary` | Narrative: what you decided, what failed, what you assumed | State the next session must *find* (§8) |
| `inbox` | Findings handed over from another project (§11) | Your own project's work |

**The filing test for all of them:** *could the next session recover this from the code?* If
yes, do not file it. Git history, file structure and existing docs are not memory's job.

### 4.2 The state rooms — agent-maintained, edited in place

These are what make a returning session productive instead of archaeological. The `llm_` prefix
records that an agent originated the convention.

| Room | Holds | ⚠ Rule |
|---|---|---|
| `llm_open_threads` | **ONE live list** of unfinished work | Edit in place. **Under ~800 chars.** |
| `llm_index` | Routing: which room answers which question | Edit in place. **Each drawer under ~800 chars.** |
| `llm_corrections` | Claims published and then retracted | **Append-only.** New record per batch. |

> ⚠ **Same prefix, opposite rules.** The first two are *state* and must stay editable. The
> third is *history* and must not be edited. Confusing them breaks the two that matter more.
> Why the size limit: §10.

### 4.3 Seed `llm_index` — two drawers

`llm_index` is a **pointer, not a copy** — the cheapest thing in the system and the thing that
turns "search blind" into "one hop to the right room". Route it **by question**, because a
record titled by its topic answers no question.

```
am_add_drawer(wing: "<project wing>", room: "llm_index", content: """
WHAT SHOULD I LOAD NEXT / WHERE DO I LOOK / WHAT EXISTS — the routing index, organised by
QUESTION. Under 800 chars so it stays ONE chunk and can be edited in place.

UNFINISHED, AWAITING A HUMAN → room `llm_open_threads`. Read first.
WHAT WE GOT WRONG → `llm_corrections`.
WHAT THE HUMAN DECIDED → room `human-decisions`: wing_craft = cross-project, here = this product.
HOW A LIBRARY REALLY WORKS → wing_craft room `examples`. Cached; do not re-fetch.
WHY THE CODE IS LIKE THIS → room `architecture` AND ⚠ the ADRs/specs IN THE REPO, which this
  palace does NOT index.
HOW TO DRIVE THE TOOLS → room `tooling`.
HOW TO WORK ANYWHERE → wing_craft rooms `gates`, `verification`.
WHICH KG ENTITY NAMES RESOLVE → sibling drawer in this room.

⚠ A pointer, not a copy. Add a line when a room appears.
""")
```

The second drawer solves a problem nothing else in the system solves — see §6.2:

```
am_add_drawer(wing: "<project wing>", room: "llm_index", content: """
WHICH ENTITY NAME DOES am_kg_query ACTUALLY RESOLVE? — the canonical key list.

⚠ THE ENTITY STRING IS A KEY, NOT A LABEL. Normalised only by lowercase and
spaces→underscores, with NO fuzzy match. A name you invent returns nothing and silently
creates a NEW node.

KEYS THAT RESOLVE:
  <add each key here as you mint it>

⚠ Add the key here when you mint one, or this list stops being the answer.
""")
```

---

## 5. The skills — what to actually write in them

Skills are **centralised**: authored once, versioned server-side, loaded by every agent at
wake-up. They are how a convention reaches an agent **before it knows it needed one**.

```
am_list_skills()                              # catalogue (name, description, version)
am_load_skill(name)                           # fetch a body
am_update_skill(name, content, description)   # upsert; bumps version. Needs writer/admin.
```

> ⚠ **A skill missing from your harness's local list is usually centralised, not absent.**
> Check the catalogue before deciding the team has no convention for your stack.

### 5.1 Exactly two, and they are a pair

- **`human-decisions`** — WHAT the team decided, on topics where their call overrides training.
- **`memory-orchestration`** — HOW to find it, when to write, how much to trust it.

Neither works alone: knowing a decision exists is useless if you cannot find it, and finding
memories is useless if you do not know which of them outranks your own prior.

Add stack skills (`effective-go`, `cqrs`, house style) as separate entries. **Do not fold them
into these two** — these two must stay short enough to be read every session.

### 5.2 The layering test — what earns a place in a skill

> **It goes in the SKILL only if not knowing it causes a *silent* failure** — a rule whose
> absence produces a confident wrong answer rather than an error.
>
> **Everything that fails LOUDLY goes in a DRAWER** — a validation error, a refusal, a visibly
> wrong number — because **the failure itself sends you looking.**

That asymmetry is the whole argument. A loud failure is its own retrieval trigger; a silent one
is not, and no amount of filing fixes it.

**Why a test and not a judgement call:** without one, every session that finds a true and useful
fact adds it, because there is no principled way to refuse. One team edited the same skill four
times in a day. A skill that collects every true fact stops being read — and then the
silent-failure rules go down with the rest.

> ⚠ **VERIFY THE DRAWER EXISTS BEFORE CUTTING THE SKILL.** Search for each fact you are about
> to remove and confirm it comes back at the top. **A pointer to something nobody wrote** is
> the most common way this goes wrong: the skill gets shorter, the knowledge is gone, and
> nothing errors.

### 5.3 `human-decisions` — paste-ready template

The **description** is what every agent sees in `am_list_skills` even if it never loads the
body, so the description must carry the silent-failure set on its own.

```markdown
# human-decisions — read this before you trust your own training

**Load with `memory-orchestration`.**

You are a model with priors. This team has made calls that CONTRADICT those priors, usually
because the thing is newer, rarer, or weirder than your training data. On those topics our
decision wins and your default is a bug.

## Why this skill exists (the failure it prevents)

Recall does not save you here. Semantic search keys on similarity to what you ASKED, so
**you cannot retrieve what you do not know to ask for**. An agent that has never heard of a
framework will not search for it — it will confidently write the idiom it does know, and
nothing will contradict it. That is why this file is loaded unconditionally instead of
retrieved.

That is also the test for what belongs here, and it is NARROW: a topic earns a place only
when a competent model would confidently do the WRONG thing. If the model would merely be
*unsure*, it will search, and a drawer is enough.

**Filing a memory in a room does NOT make it surface at the right moment.** What a room buys
is ENUMERABILITY — "show me every call we have made" — and that is what makes promotion into
a skill possible. Room = the record. Skill = the rule that graduated.

## ⚠ <TOPIC> — the flagship case, and you WILL get this wrong

<Replace this section entirely. The shape that works:
   - Decided by <who>, <date>, for <library/system> at <exact version>.
   - Say plainly WHY the model is confidently wrong, not merely ignorant.
   - Concrete ✗ / ✓ pairs. Nothing beats a wrong/right column.
   - WHY it silently does nothing rather than failing loudly.
   - The official source URL.

 Worked example of the shape, from the team that wrote this file:

     ✗ data-on-load          ✓ data-init
     ✗ data-on-click="..."   ✓ data-on:click="..."

   …because the library postdates most training corpora and looks superficially like an
   older one, so the model writes the older thing with no hesitation and it silently does
   nothing rather than failing loudly.>

## Topics where our call beats your prior

- <topic> — see above.
- Add topics as they are decided, under the narrow test above. This list stays short so it
  stays read.

## Where everything else lives

**One search: `am_search("what should I load next")`** returns the routing index (room
`llm_index`), which names the room for each question.

⚠ **Project intent is a SEPARATE source the palace does not index** — ADRs, specs, README,
BACKLOG. Before reporting anything as undecided, check there too.

## How to record a new human decision

    am_add_drawer(wing: <project|wing_craft>, room: "human-decisions", content: ...)

Say WHAT was decided, WHAT the alternative was, WHY, and the DATE. **The rejected alternative
matters as much as the choice**: without it a decision reads as arbitrary preference and the
next session reopens it.

**Mark what YOU added.** If you supply an inference, a gloss, or a causal link the human did
not state, say so inline — an unmarked addition inherits their authority and the record becomes
your reasoning wearing their name. Tell them too; they are the only one who can confirm it.

**⚠ TEACH THE HUMAN BACK.** If you find OFFICIAL documentation bearing on a recorded decision —
confirming, refining or contradicting it — do not just file it. Tell the human and **LEAD WITH
THE SOURCE URL** so they can read it themselves. Official sources only: the project's own docs,
repo, spec or changelog. Never a blog, never your own recollection. Requiring a verifiable URL
is what makes this teaching rather than overruling.

**Version-pinned decisions are perishable.** If a library moves, CORRECT the file and its
drawers rather than filing a second copy — two live copies disagreeing is worse than one stale
copy.

**Do not update this skill for every new fact.** Drawers absorb knowledge continuously; this
skill changes only when a NEW confident-error class appears, or a rule here becomes wrong.

## Honest limits

This is a CONVENTION, not a ranking change. Nothing in the server boosts these rooms; it works
only because agents call `am_list_skills` at wake-up and act on what they read. Do not mistake
having filed a decision for having made it reachable.
```

### 5.4 `memory-orchestration` — paste-ready template

```markdown
# memory-orchestration — how to find it, when to write it, and how much to trust it

Load with `human-decisions`. That one is WHAT the team decided; this is HOW to find it.

## What is deliberately NOT in this file

**Everything else is one search away.** `am_search("what should I load next")` returns the
routing index, which names the room for every question this file used to answer inline.

What stays here is only what causes **SILENT failure**: a rule whose absence produces a
confident wrong answer rather than an error. Anything that fails loudly enough to send you
looking was moved behind that hop ON PURPOSE. **Apply that test before adding anything here.**
A skill that collects every true fact stops being read.

## ★ Write each record as the ANSWER TO A QUESTION

Semantic recall matches your text against a QUESTION someone types. A record titled by its
subject is reachable by that subject **and by nothing else**.

- **Open with the question's words.** Not "Session notes" but "WHAT IS STILL OPEN".
- **Give experience its own record**, never a paragraph inside a topic record.
- **Then ASK THE QUESTION.** If your record is not the top hit, it is not filed — it is stored.

## ⚠ The server ranks, it never rejects

`am_search` returns **exactly `limit` hits** whether or not that many deserve to exist. There is
no "nothing matched well" signal.

    rerank_score:  0.248   0.098   0.088   0.0010   0.00025

The last two are noise by three orders of magnitude. **YOU are the relevance filter.**
`max_distance` defaults to 1.5 of a 2.0 maximum — barely a filter, and the only true reject
knob. `context` does NOT filter; it re-orders for the reranker only.

## ⚠ Recall MID-SESSION, not only at wake-up

The moment you are unsure of anything — a spelling, a flag, a convention, a tool's shape —
search THEN. Startup grounding structurally cannot cover it: **at startup you do not yet know
which of your instincts is about to fail.** Same before any broad grep over unfamiliar code:
ask memory first, grep only the gap.

## How to weigh what comes back

1. **`human-decisions` outranks everything** on a topic it covers. A centralised skill outranks
   a drawer for standing rules.
2. **⚠ TEAM POLICY IS WHAT ONLY THE PALACE HOLDS.** An agent reviewing code without memory
   catches every *universal fact* (derivable from the artifact) and misses every *team policy*.
   Weight policy highest — it is recoverable from nowhere else.
3. **⚠ THE PALACE IS NOT THE WHOLE RECORD.** ADRs, specs, README are a SEPARATE source this
   system does not index. Before reporting anything as undecided, name the sources you
   searched. A list of one establishes nothing.
4. **Recency breaks a tie** (`content_date`) — drawers have no supersession field. **Anchored
   or verbatim beats paraphrase.** **`diary` is narrative, not fact** — invaluable for "why did
   we", nearly worthless for "what is the rule".

## The silent traps

- **⚠ `am_get_drawer` needs `whole=true`.** Otherwise you read the ONE chunk you named. It now
  SAYS it is a fragment — `content_truncated` with `content_length`, the whole memory's rune count
  — but the flag only reports the partial read, it does not complete it, and `whole=true` is the
  only completion path. (Until 2026-08-29 nothing marked it at all, and this page said so.)
- **⚠ A KG entity string is a KEY**, normalised only by lowercase + spaces→underscores, no fuzzy
  match. An invented name silently creates a new node. Keys are listed in `llm_index`.
- **⚠ `am_kg_add` IS IDEMPOTENT**, so replacing a fact means `am_kg_invalidate` FIRST, then add.
  Invalidate means "STOPPED being true", not "was recorded wrong"; there is no update.
- **⚠ `am_mine` defaults to room `general`, which the graph tools EXCLUDE.** Pass a room.
- **⚠ You can never rename or delete a room.** Name one as if the name is permanent.
- **⚠ `default_wing` may be EMPTY** — then every unscoped recall spans EVERY wing.

## WHEN to write

**The test: could the next session recover this from the code?** If yes, do not file it.

- **`am_add_drawer`** — a decision WITH ITS REJECTED ALTERNATIVE, an incident, cached reference.
  Add `code_anchors` — a verbatim snippet, NEVER a line number, which rots silently.
- **`llm_open_threads`** — the LIVE list of unfinished work. ⚠ Keep under ~800 chars so it stays
  ONE CHUNK; `am_update_drawer` refuses in-place edits to anything multi-chunk. Adding an item
  means compressing another. Same rule governs `llm_index`.
- **`llm_corrections`** — APPEND-ONLY log of claims published and retracted. ⚠ Same prefix as
  above, opposite rule.
- **`am_diary_write`** — the narrative. But put anything a future session must FIND into a room
  above; a diary tail is unretrievable.
- **`am_kg_add`** — **MUST, not optional**: entity-level facts true of the WORKSPACE, filed in
  the same breath as the drawer they describe. A drawer with no edge is an orphan — reachable
  by search, invisible to traversal, and it still surfaces in your OWN search, which is why
  authors believe it is reachable. ⚠ Facts are NOT wing-scoped.
- **`am_update_skill`** — ONLY for a NEW confident-error class, or a rule here that became
  wrong. Test: is it already reachable through a pointer this skill carries?

**⚠ MARK WHAT YOU ADDED.** An inference or causal link the human did not state gets said inline
— an unmarked addition inherits their authority. Tell them too.

## Before you stop

Update `llm_open_threads`, write the diary entry, file what a future session must find. A
verified change that is not written back is memory lost.
```

---

## 6. The knowledge graph — how to actually use it

The KG holds **entity-level facts with time**: `subject → predicate → object`, each with a
validity window. It is the cheapest way to answer *one small question about one named thing*.
It is **not** the record.

### 6.1 The tools

| Call | Answers |
|---|---|
| `am_kg_add(subject, predicate, object, valid_from?, source_drawer_id?)` | File a fact |
| `am_kg_query(entity?, predicate?, as_of?, direction?, status?)` | Facts about this entity — or, with `predicate` alone, every fact of one relation |
| `am_kg_invalidate(...)` | This **stopped being true** on this date |
| `am_kg_timeline(entity?)` | What changed, in order |
| `am_kg_stats()` | Totals and predicates in use — ⚠ can dump ~1000 predicate names |

⚠ **`am_kg_query` returns only facts that are still true.** The default is `status:"current"`,
filtered server-side, so retracted facts never reach your context. Nothing is dropped silently:
a response that filtered something carries `withheld: {"ended": N}` and a hint naming the
parameter that brings it back. Pass `status:"all"` for the whole history and `status:"ended"` to
audit what the team has changed its mind about.

⚠ **`as_of` and `status` compose, so asking about the past takes both.** They select on
different questions — `status` on whether a fact was *ever* retracted, `as_of` on whether it was
in effect at an instant. Under the default, `as_of` alone therefore answers "open-ended facts
that were *also* in effect on D", not "facts in effect on D". For a real snapshot of a past
date, pass `as_of` **and** `status:"all"`. The call succeeds either way and the short answer
still looks like history, which is what makes this one worth knowing.

### 6.2 ⚠ The entity string is a KEY, not a label

Normalised only by lowercase and spaces→underscores. **There is no fuzzy match.** `datastar`
and `datastar v1.0.x` are different nodes. An agent that invents a spelling gets silence *and
silently creates a duplicate node* — nothing warns it.

One team scattered a dozen facts about the same library across five spellings in a single day,
then queried the obvious name and got two results back.

**The fix is the key list in `llm_index` (§4.3): decide the entity name once, write it down,
and add to it whenever you mint one.** A name list turns an unguessable key into a lookup.
Nothing else in the system solves this.

### 6.3 The rules that bite

- **⚠ Facts are WORKSPACE-wide, not wing-scoped.** A fact filed from one project is returned to
  every project in the workspace. File here only what is true of the workspace; anything
  project-specific goes in a drawer.
- **⚠ `am_kg_add` is idempotent.** Re-adding an identical current fact is a no-op. To *replace*
  a fact you must `am_kg_invalidate` the old one **first**, then add. There is no update.
- **Invalidate means "stopped being true", not "was recorded wrong."** A fact recorded in error
  is not history; delete-and-refile is a different operation from expiry.
- **Reuse an existing predicate** rather than minting a near-synonym. `uses` and `is_using`
  are two predicates and no query joins them.
- **Objects are SHORT LABELS (≤128 chars), not sentences.** Evidence, commit ids and repro
  steps belong in a drawer.
- **⚠ `source_drawer_id` is write-only in some versions** — the returned fact may not carry it,
  so a fact cannot always be traversed back to its drawer. Do not design a workflow that
  depends on it; keep the explanation in the drawer and the key in the index.

### 6.4 When to use the graph, and when not to

| Use the KG | Use a drawer |
|---|---|
| "What version are we on?" | "Why did we choose that version?" |
| "Who owns this service?" | "What went wrong last time we deployed it?" |
| Facts that change over time and need a date | Anything with reasoning, evidence or nuance |
| One-line lookups by a name you know | Anything you would need to *explain* |

**The honest default: prefer a drawer for anything that matters.** The graph is a cheap
single-detail check. Prose that needs context loses that context when squeezed into a triple.

---

## 7. How to recall

### 7.1 Waking up — the sequence

```
1. am_skillset()                     # the server's own playbook + tool catalogue
2. am_status()                       # workspace identity, default_wing, role, taxonomy
3. am_search("<the task>", wing)     # past decisions and rationale for THIS work
4. am_list_skills() → am_load_skill  # the team's centralised conventions
5. read llm_open_threads             # what the last session left (§8)
6. read the inbox                    # findings handed over from other projects (§11)
```

Steps 3–6 are the point. Steps 1–2 only prove you are talking to the right palace.

### 7.2 Which tool answers which question

- **`am_search`** — "what do we know about X?" Semantic + lexical, ranked.
- **`am_list_drawers(wing, room)`** — "show me **everything** in this room." **Search cannot do
  this**, and enumerability is the entire reason rooms exist. Use it for `human-decisions`,
  `llm_corrections`, `inbox`.
- **`am_get_drawer(id, whole=true)`** — read one memory in full. ⚠ **Always pass `whole=true`**
  or you get the single chunk you named, marked `content_truncated` with the memory's
  `content_length` — a flag you have to read, not a completed answer.
- **`am_kg_query(entity)`** — one small fact about one named thing (§6).
- **`am_diary_read(agent_name)`** — narrative history. ⚠ May return chunks rather than whole
  entries, unordered.
- **`am_recall_stats()`** — **the queries that came back with nothing.** These are the memories
  the team looked for and does not have. This is the only feedback loop the system has; read it
  monthly.

### 7.3 ⚠ Filter the results yourself

`am_search` returns **exactly `limit` hits, always** — there is no "nothing matched" signal. A
query about something the palace has never heard of still returns five confident-looking,
on-topic-seeming results.

**Read `rerank_score`.** In practice: `0.248  0.098  0.088  0.0010  0.00025` — the last two are
noise by three orders of magnitude. An agent that forgets this will act on noise with total
confidence, and nothing in the system will contradict it.

### 7.4 Recall MID-SESSION, not just at startup

This is the habit that separates a palace that pays off from one that does not.

**At startup you do not yet know which of your instincts is about to fail.** So the moment you
hesitate on anything — a flag, a spelling, a convention, a tool's parameters — search *then*.
Before any broad grep over unfamiliar code: ask memory first, grep only the gap, and write back
whatever you had to re-derive.

**Hesitation is not the only trigger, and it is not the dangerous one.** Search before anything
you are doing **for the first time in this repository**, and before anything **outward-facing or
hard to reverse** — a tag, a merge, a push to a shared branch, a migration, a published
artifact, a message to a person. Those are the moments you are least likely to hesitate,
because the convention is usually derivable from the artifacts and deriving it feels
sufficient.

It is not, and here is the difference:

> **Artifacts show you the FORM of a convention. Memory shows you its BLAST RADIUS.**

Measured case: an agent asked to cut a release derived the conventions correctly — `git log`
gave the merge style, `git tag -l` gave the annotated-tag format — tagged, pushed, and reported
that a release had been published. The palace held a record whose first line was *"'tag' MEANS
RELEASE **AND DEPLOY**. Read this before pushing a version tag."* The tag fired three workflows,
not the two it had accounted for, and the third rolls the production host. It skipped only
because a deploy secret happened to be unset. Nothing in `git tag -l` could ever have said that.

**The usable tell:** *if you are reading `git log` or a config file to work out "how is this
normally done here", the answer to "has someone written down how this is normally done here" is
very often yes.* Reconstructing a procedure from artifacts is itself the signal to search.

And note why the wake-up recall does not cover this: at startup you search for **the task**, and
the task changes. The session above searched for the task it was given — reviewing a change —
four exchanges before releasing became the job.

---

## 8. Tasks, unfinished work, and continuing

**This is the section that makes memory feel like memory.** Everything else records the past;
this is how a new session picks up where the last one stopped.

### 8.1 Why the diary is not enough

The intuitive place to record "what's still open" is the end of a session journal. **It does not
work, and the failure is measurable.** Long entries are stored as multiple chunks, and search
returns *the chunk that matched* — so a query about what is unfinished hits the entry's topic
words and never the tail where the open items live.

A team ran the two questions a returning session would obviously ask, against a corpus that
genuinely contained the answers:

| Query | Result |
|---|---|
| "what is still open or unresolved" | 0.384 — *another project's* threads |
| "what did we get wrong and correct" | 0.131 — an unrelated policy decision |

**The information existed. It was unreachable.** After moving it into dedicated records whose
first line carries the querier's own words: **0.833 and 0.991, both rank 1.**

There is a second trap, and it is worse because it is self-inflicted: a skill that says *"weigh
diary below decisions"* while the diary is where continuation state is kept. The rule and the
practice contradict each other and nobody notices until a cold session fails.

### 8.2 `llm_open_threads` — one live list

**One drawer. Always current. Always single-chunk.** Edited in place, never appended to.

```
WHAT IS STILL OPEN OR UNRESOLVED, WHAT IS WAITING ON A HUMAN — the live list,
<project>, <date>. Under 800 chars so it stays ONE chunk, editable in place.

1. <thing> — <why it is stuck / who it is waiting on>.
2. <thing> — <the next concrete action>.
3. ⚠ <conflict or collision that needs a human to break the tie>.
```

Rules that make it work:

- **The first line is the question**, not "Open threads". §9.1 explains why.
- **Under ~800 characters**, so it stays one chunk and `am_update_drawer` will edit it in place
  (§10). This is the *reason* it is terse.
- **Adding an item means compressing another.** That pressure is a feature: a list of forty
  items is not a list anyone reads.
- **One line per item, and each line says what it is *waiting on*** — a person, a decision, a
  build. "Refactor the parser" is not an open thread; "parser refactor blocked on whether we
  keep the v1 endpoint — M's call" is.
- **Update it, never file a second one.** Two live lists is the failure this room prevents.

### 8.3 Closing an item

- **Done?** Remove the line. The record of what happened goes in `decisions` or the diary; the
  live list holds only what is *live*.
- **No longer applies?** Say so plainly, once, then remove it. A stale item that nobody
  contradicts gets rediscovered every month.
- **Turned out to be wrong?** That is `llm_corrections` — see §9.3.
- **Belongs to another project?** §11.

### 8.4 Resuming — what a fresh session does

The canonical probe is *"where did we finish?"* and the sequence is:

```
am_status()                                        # right palace? which wing?
am_search("what should I load next", wing)         # → llm_index, the routing hop
am_list_drawers(wing, room: "llm_open_threads")    # the live list, enumerated not searched
am_search("<the actual task>", wing)               # the why behind the code you are about to touch
```

Use `am_list_drawers` rather than `am_search` for the list itself: a room with one drawer should
be **enumerated**, not ranked. That is what rooms are for.

Then **say what you found before you act on it** — "the last session left three things open,
two of them waiting on you" is a better opening than silently starting work.

### 8.5 Before you stop — persist

A verified change that is not written back is memory lost. Every session ends with:

1. **`llm_open_threads`** — update in place. New items in, finished items out.
2. **`am_diary_write`** — the narrative: what you decided and **why**, which assumption turned
   out wrong, which approach you tried that failed. A repository keeps only what worked, so
   dead ends are lost unless they are written here. When nothing went wrong, say so briefly
   rather than inventing a lesson. Use a **stable `agent_name`** so entries thread across
   sessions — check with `am_diary_read` before inventing one, or you fork the journal.
3. **`am_add_drawer`** — decisions with their rejected alternative, incidents, anything traced
   the hard way.
4. **`am_kg_add`** — **required, not optional**: durable entity facts (§6), and add any new key
   to the `llm_index` key list. The edge is what makes 3 findable by traversal at all.

Steps 2 and 4 are the gate. Skip a step only when the session produced nothing worth recalling
— **and say so** rather than skipping silently.

---

## 9. How to write a memory that can be found

### 9.1 Title it with the question, not the topic

Semantic recall matches your text against **a question someone types**. A record titled by its
subject is reachable by that subject and by nothing else.

- **Open with the question's words.** Not "Session notes 2026-08" but "WHAT IS STILL OPEN".
- **Give experience its own record**, never a paragraph buried inside a topic record.
- **Cover the vocabulary a reader would actually use.** A record titled *"WHAT BELONGS IN AN
  ALWAYS-LOADED SKILL"* failed to retrieve for *"should this go in a skill or a drawer"* —
  because **the word "drawer" never appeared in it.** Re-titled to carry both phrasings: rank 1
  at 0.992.

### 9.2 Then run the probe

**Ask the question a future reader would ask. If your record is not the top hit, it is not
filed — it is stored.** One call, and it is the only thing standing between a palace and a
write-only archive.

**But you are the worst person to run it.** You just wrote the record, so its wording is in
your head, and the probe you reach for is shaped by the text you are testing. You will phrase
the question using the words you happened to choose — which is the one phrasing guaranteed to
work, and the one a stranger will never type.

### 9.2b ★ Spawn a subagent to verify recall

**The reliable form of the probe is to have someone else run it — an agent that has not read
what you wrote.** If your harness can spawn a subagent, this costs one call and it is the only
version of this test that can actually fail.

Give it the *question*, never the answer, and never the drawer:

> You have the agentsmemory MCP tools. Using **only** `am_search` in wing `<wing>` — do not
> read files, do not use prior knowledge — answer: **"<the question a future session would
> ask>"**. Report the answer, the drawer that carried it, and its rank. If nothing usable
> came back, say so plainly.

Then judge three things, in this order:

1. **Did it find the record at all?** Rank matters less than presence. Absent means unfiled.
2. **Did it answer correctly from the drawer alone?** A record that ranks first and still
   leaves the reader guessing is a pointer, not an answer (§5.2).
3. **What did it search for?** This is the part you cannot get any other way. If its query
   differs from yours, *its* phrasing is the real one — a future session will phrase it that
   way too, and you now know the vocabulary your record is missing.

**Never tell it the answer, the drawer id, or your probe string.** A subagent handed the
expected result will confirm it; the whole value is that it is uncontaminated. For the same
reason, do not paste your record into the prompt — that reintroduces exactly the lexical
contamination §9.4 is about.

**Cheapest high-value use:** run it once against `llm_index` (*"what should I load next"*) and
once against `llm_open_threads` (*"what is still open"*), because those two are the hops
everything else depends on. If a fresh agent cannot find them, nothing downstream is reachable
either — and that failure is invisible to you, because you know where they are.

This is also the honest version of the §14 limit *"retrievable ≠ retrieved"*. A subagent
narrows the gap: it proves an agent that was never told where to look still got there.

### 9.3 `llm_corrections` — retracted claims

When you publish something and it turns out to be wrong, the retraction needs its own record,
findable by *"what did we get wrong"* — not a footnote inside the drawer about its subject,
where it is findable only by the subject.

Keep the room **narrow**: claims that were *published and then withdrawn*. A conversational
misreading is not a correction, and diluting the room makes it useless. Append-only; multi-chunk
is fine.

### 9.4 ⚠ Writing about a probe degrades the probe

The subtlest failure in this document, and it caught its discoverer **twice inside two minutes**.

1. A record is built to answer a probe. Re-measured: rank 1. Fix confirmed.
2. The finding is **written up** — a journal entry, a decision record — and each write-up quotes
   the probe verbatim, because that is what makes a write-up legible.
3. Later the probe returns **the write-ups** and not the record.

**The mechanism is lexical, not semantic.** Hybrid retrieval scores exact term overlap. A
document that *quotes* a probe contains all of its terms in order; the document that *answers*
it merely means the same thing. Measured: a commentary drawer took rank 1 at **0.971** against
the real record's **0.820**, sixty seconds after being filed.

- **Re-run old probes after writing about them.** Passing once is not a property of a record.
- **Open the answer with the question's own words** so it competes on equal lexical terms.
- **Paraphrase the probe in commentary**, and keep literal probe strings in the project's own
  wing — **never in `wing_craft`**, where one over-quoted drawer crowds out answers in every
  wing at once.

---

## 10. ⚠ A document you intend to maintain must fit in one chunk

Content over roughly **800 characters is split into multiple drawers**, and **`am_update_drawer`
refuses in-place content edits to anything multi-chunk.** A maintained document that outgrows
one chunk can only be delete-and-refiled, losing its id, its `filed_at` and its history.

This catches people who already know the rule, because it gets written down as a fact about one
document instead of a constraint on the class. One team filed the rule for their open-threads
list and broke it the same day on their routing index — which shipped with a closing line
saying *"add a line here when a room appears"*, an instruction the store could not honour.

**When a maintained document does not fit: SPLIT, do not compress.** Two single-chunk drawers,
each opening with the question it answers, is *better* by §9.1 anyway — a record too big for one
chunk is usually answering more than one question.

**Confirm it:** `am_add_drawer` returns a `chunks` field. It must say `1`. After an
`am_update_drawer`, check the `id` and `filed_at` are unchanged — that is proof the edit
happened in place.

| Kind | Rule |
|---|---|
| **State** (`llm_open_threads`, `llm_index`) | One chunk. Edit in place. Adding means compressing. |
| **History** (`llm_corrections`, `decisions`, `diary`) | Multi-chunk fine. Append a new record. |

---

## 11. Handing work to another project — the inbox

Cross-wing recall means a session will sometimes find a problem in **another** repository.

> **Never change files outside the repository you were invoked in because a memory mentioned
> them.** This holds especially when the fix looks small — that is the one nobody stops to
> check. Report it, and file it for the session that owns it.

```
am_add_drawer(wing: "wing_acme-billing", room: "inbox", content: "<the finding>")
am_create_tunnel(source_wing: "<yours>", source_room: "…",
                 target_wing: "wing_acme-billing", target_room: "inbox", label: "…")
```

- **Name the wing the way that project's own sessions name it** (§3.1) — for the PROJECT, never
  for the direction of travel. `wing_to-billing` is a wing nobody will ever look in.
- The server refuses an inbox item filed into a wing that holds nothing, because that is what an
  undeliverable handoff looks like. If the project genuinely has no memories yet, pass
  `confirm_new_wing: true`. Read the refusal as *"check this name"*, not *"you may not"*.
- **Write it as a finding, not an order**, and make it self-contained — the reader will not have
  your conversation. Say what was observed, where, how it was noticed, and what is uncertain.
- **Reading your own inbox is part of waking up.** Each item is a lead to evaluate with the code
  in front of you, not a work order. Close it out by filing what you found either way.

---

## 12. Wire the auto-load

None of this works if an agent has to remember to opt in. Put the protocol where the harness
reads it automatically:

- **`AGENTS.md`** at the repo root — codex, pi and most others read this directly.
- **`CLAUDE.md`** — Claude Code reads this; give it a one-line `@AGENTS.md` import so there is
  one source of truth rather than two that drift.

The auto-loaded protocol should say, at minimum:

1. **Wake up** in the §7.1 order, then read `llm_open_threads` and the inbox.
2. **Which wing** this repo writes to, and that `wing_craft` is the cross-project one.
3. **Recall mid-session**, not only at startup (§7.4).
4. **Persist before stopping** (§8.5).
5. **What to do when the tools are absent** — say so plainly and ask for the MCP to be
   connected, rather than working blind and silently. An agent that quietly proceeds without
   memory will re-derive settled decisions and contradict last week's call with total
   confidence, and nobody in the session will be able to tell.

---

## 13. Acceptance test — run it, do not assume

| # | Run | Expect | If it fails |
|---|---|---|---|
| 1 | `am_status()` | `workspace.slug` is **this team's** | Wrong registration. Stop; write nothing. |
| 2 | `am_status()` | note `default_wing`; empty → always pass `wing` | Unscoped recall spans every project |
| 3 | `am_list_skills()` | both skills present | §5 not done, or role lacks writer |
| 4 | `am_list_rooms(wing)` | the §4 rooms exist with non-zero counts | Rooms never written to |
| 5 | `am_search("what should I load next", wing)` | `llm_index` routing drawer **rank 1** | §4.3 missing or not question-titled |
| 6 | `am_search("what is still open or unresolved", wing)` | `llm_open_threads` **rank 1** | Not titled with the question (§9.1) |
| 7 | `am_add_drawer(...)` on a maintained doc | response says `chunks: 1` | Too long — split it (§10) |
| 8 | `am_update_drawer(id, content)` on it | same `id`, same `filed_at` | It is multi-chunk (§10) |
| 9 | Search one fact you moved skill→drawer | comes back at/near the top | **A pointer to nothing (§5.2).** Restore it. |
| 10 | `am_kg_add` then `am_kg_query(subject)` | returns for the **exact** key | Spelling differs; add it to §4.3 |
| 11 | `am_recall_stats()` | it answers | The stats table may be missing; check the schema |
| 12 | **Spawn a subagent** (§9.2b): *"using only `am_search` in `<wing>`, what should I load next?"* | it reaches `llm_index` **without being told where to look** | The index hop is invisible to anyone who did not build it |
| 13 | Same, for *"what is still open?"* | it reaches `llm_open_threads` | Rows 5–6 passed on **your** phrasing, not a stranger's |

Rows 12–13 are not duplicates of 5–6. Those two you ran yourself, knowing the wording you were
testing; these are the same questions asked by someone who has never seen the drawers. When the
two disagree, the subagent is right — a future session is a stranger, not you.

**Then the test that is not a tool call:** open a fresh session, ask *"where did we finish?"*,
and see whether it answers from the palace without being told where to look. That is the only
test of the whole thing, and it is worth repeating monthly.

---

## 14. Honest limits — tell the team these

- **This is a convention, not a mechanism.** Nothing in the server boosts these rooms or
  enforces the index hop. It works because agents load the skills at wake-up and act on them.
- **Retrievable ≠ retrieved.** You can prove a fact comes back at the top for a probe. You
  cannot prove a future agent will *think to search* for it. That gap is what the always-loaded
  skills exist to bridge, and it is why §5.2's test is narrow. §9.2b's subagent probe narrows it
  further — an agent that was never told where to look either got there or did not — but it
  still tests a question you chose to ask.
- **Recall degrades as the corpus grows unless it is scoped.** More memory is not automatically
  better memory.
- **`am_search` never says "I found nothing."** Whoever reads the results is the filter.
- **The palace does not index the repository.** ADRs, specs and READMEs are a separate source. A
  session that searched only the palace and reports something as undecided has established
  nothing.

---

## Appendix — the tools, by the question they answer

**Waking up** · `am_skillset` `am_status` `am_list_skills` `am_load_skill`

**Recall** · `am_search` · `am_get_drawer(id, whole=true)` · `am_list_drawers(wing, room)` to
ENUMERATE · `am_check_duplicate` before filing · `am_diary_read`

**Write** · `am_add_drawer` `am_update_drawer` `am_invalidate_drawer` `am_diary_write`
`am_update_skill` `am_mine`

**Taxonomy** · `am_list_wings` `am_list_rooms` `am_get_taxonomy` `am_memories_filed_away`
`am_graph_stats` `am_traverse` `am_merge_wing`

**Links** · `am_create_tunnel` `am_list_tunnels` `am_find_tunnels` `am_follow_tunnels`
`am_list_hallways` `am_recompute_graph`

**Knowledge graph** · `am_kg_add` `am_kg_query` `am_kg_invalidate` `am_kg_timeline`
`am_kg_stats`

**Operations** · `am_recall_stats` — the queries that found **nothing** are the memories the
team wanted and does not have. · `am_list_anchors` / `am_mark_anchors` — anchors get created
constantly and verified almost never; if the working tree is open, checking a few is nearly
free.
