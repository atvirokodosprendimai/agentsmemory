# Hook event map — agentsmemory / clients/claude-code

**Traced from source, not from protocol docs.** Read at `main @ 5615b4e3`, 2026-09-07.
Registration counts read from `~/.claude/settings.json` on the author's machine the same day.

This document has two jobs. The first is to say what actually fires, in what order, and who
depends on whom. The second is to be a **re-runnable protocol**: every table below names the
command that derives it, so the map can be regenerated instead of trusted, and the same method
can be pointed at another kit.

⚠ **A count in this file is a measurement with a date, not a property of the code.** The
registration counts in §5 are what one machine had; the manifest in §1 is what ships. When they
disagree, that disagreement is the finding.

---

## 0. Method — how to re-derive every table here

Run from the repository root. Each block regenerates one section.

```bash
# §1 the manifest — what the kit registers
cat clients/claude-code/hooks/hooks.json

# §1 channel each script declares for itself
grep -m1 'hook-output:' clients/claude-code/hooks/*.sh

# §1 size
wc -l clients/claude-code/hooks/*.sh

# §2 palace calls per script
for f in clients/claude-code/hooks/*.sh; do
  echo "== $f"; grep -oE 'am_[a-z_]+' "$f" | sort -u | tr '\n' ' '; echo
done

# §3 state files: every path under the state dir, per script
for f in clients/claude-code/hooks/*.sh; do
  echo "== $f"
  grep -oE 'agentsmemory-[a-z-]+(/\$\{?[A-Za-z_]*\}?)?' "$f" | sort -u
done

# §3 find a state file with no reader — the dead-end test
grep -rl "agentsmemory-reground" clients/claude-code/ docs/ --include='*.sh' --include='*.go' --include='*.md'

# §4 gating: what makes a hook speak or stay silent
grep -nE '^\s*(if|case|exit|\[)' clients/claude-code/hooks/<script>.sh

# §5 what THIS machine actually has registered (not what ships)
python3 -c "
import json
h=json.load(open('$HOME/.claude/settings.json')).get('hooks',{})
for ev,gs in h.items():
    cmds=[k.get('command','') for g in gs for k in g.get('hooks',[])]
    print(ev, len(cmds))
    for c in cmds: print('   ', c[-70:])
"

# §6 orphaned state — markers written and never consumed
ls -la "${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}"/agentsmemory-reground/
```

**The three questions this method asks, in order.** They generalise to any hook kit:

1. *What is registered?* — the manifest. Cheap, and it is the claim.
2. *What does each one write, and who reads it back?* — the state graph. A file with a writer and
   no reader is a dead end; a file with two writers is a race.
3. *What does the machine actually have?* — the registration on disk. Nothing in a repository can
   see this, and it is where duplication lives.

---

## 1. The manifest

Derived from `clients/claude-code/hooks/hooks.json`. **Ten events, eleven scripts, two of which
are registered on no event.** Every registration carries `timeout: 75`.

| Event | Script | Lines | Declared channel | Reaches the model? | Palace calls |
|---|---|---:|---|---|---|
| `SessionStart` | `agentsmemory-verify-hook.sh` | 108 | `stdout-injected` | yes | — (CLI `verify`) |
| `SessionStart` | `agentsmemory-recall-hook.sh` | 533 | `stdout-injected` | yes | `am_search`, `am_recall_stats` |
| `UserPromptSubmit` | `agentsmemory-task-recall-hook.sh` | 218 | `stdout-injected` | yes | `am_search`, `am_recall_stats` |
| `UserPromptExpansion` | `agentsmemory-task-recall-hook.sh` | (same) | `stdout-injected` | yes | `am_search` |
| `PreToolUse` | `agentsmemory-anchor-cue-hook.sh` | 120 | `structured` | yes, via `hookSpecificOutput.additionalContext` | `am_get_drawer` |
| `PostToolUse` | `agentsmemory-touched-hook.sh` | 65 | `none` | no | — |
| `SubagentStart` | `agentsmemory-subagent-start-hook.sh` | 115 | `structured` | yes | `am_status`, `am_search` |
| `Stop` | `agentsmemory-stop-hook.sh` | 297 | `blocking` | yes, via `exit 2` stderr | `am_diary_write`, `am_add_drawer`, `am_kg_add` |
| `SubagentStop` | `agentsmemory-stop-hook.sh` | (same) | `blocking` | yes, via `exit 2` stderr | `am_add_drawer`, `am_kg_add` |
| `PreCompact` | `agentsmemory-precompact-hook.sh` | 232 | `none` | **no** — stdout goes to the debug log | — |
| `SessionEnd` | `agentsmemory-session-end-hook.sh` | 77 | `none` | **no** — the session is over | — |
| *(no event)* | `agentsmemory-statusline.sh` | 77 | `not-a-hook` | n/a — a `statusLine` command | — |
| *(no event)* | `agentsmemory-stats.sh` | 73 | `not-a-hook` | n/a — sourced helper | — |

**Channel vocabulary** is each script's own `# hook-output:` line, not an inference. It exists so
`TestEveryInjectingHookIsOnAnInjectingEvent` can fail when a script that intends to speak is
registered on an event whose stdout is discarded. `TestANonInjectedChannelIsJustified` refuses a
quieter channel without a written reason.

---

## 2. Firing conditions — when each one is silent

A hook that fires is not a hook that speaks. This is the column most maps omit.

| Script | Fires on | Stays silent when |
|---|---|---|
| `verify-hook` | every `SessionStart` | nothing drifted — silence is the healthy case |
| `recall-hook` | every `SessionStart` | the palace returns nothing; `AGENTSMEMORY_RECALL=off` |
| `recall-hook` (note block) | `source=compact` **and** a note file exists | any other `source` |
| `recall-hook` (last-turn block) | `source` is `startup` or `resume` | `AGENTSMEMORY_LAST_TURN=off` |
| `task-recall-hook` | prompt ≥ 24 characters | shorter prompts; `AGENTSMEMORY_TASK_RECALL=off`; server returns nothing |
| `task-recall-hook` (Expansion) | the slash command carries arguments | no arguments — `exit 0` |
| `anchor-cue-hook` | every `PreToolUse` | the target path has no anchor — the ordinary case |
| `touched-hook` | `PostToolUse` for `Edit`, `Write`, `NotebookEdit`, `MultiEdit` | every other tool — **filtered in-script; the manifest carries no matcher** |
| `stop-hook` | first `Stop` of a turn | `stop_hook_active: true`; the once-per-session marker exists in `once` mode |
| `precompact-hook` | every `PreCompact` | always silent to the model, by declaration |

⚠ **The `touched-hook` filter is why the Stop nudge's file list is empty for a session that edited
through Bash or `mrw`.** `git status --porcelain` is the truth; that list is not.

---

## 3. State graph — writers and readers

All paths under `${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}`.

| File | Keyed by | Written by | Read by | Verdict |
|---|---|---|---|---|
| `agentsmemory-touched/$SESSION` | session id | `touched-hook` | `precompact-hook`, `stop-hook` | closed |
| `agentsmemory-precompact/$SESSION` | session id | `precompact-hook` | `recall-hook` (only `source=compact`) | closed |
| `agentsmemory-last-turn/<project>` | project basename + checksum | `stop-hook` | `recall-hook` (only `startup`/`resume`) | closed |
| `agentsmemory-status-<uid>.txt` | uid | `verify-hook` | `statusline.sh` | closed |
| `agentsmemory-reground/$SESSION` | session id | `recall-hook` (on `compact`) | **no hook** — a `Monitor` the session arms by hand | **OPEN END** |

### Sequence, one session

Ordered because each step's state is what a later step reads.

```
1. SessionStart  verify-hook      → writes status cache;  prints only on drift
2. SessionStart  recall-hook      → reads precompact note OR last-turn note; am_search
                                  → on source=compact ALSO writes the reground marker
3. UserPromptSubmit task-recall   → am_search against the prompt        (every turn)
4. PreToolUse    anchor-cue       → am_get_drawer for the target path   (every tool call)
5. PostToolUse   touched-hook     → appends path to touched/$SESSION    (4 edit tools only)
6. Stop          stop-hook        → reads touched/$SESSION; exit 2 nudge
                                  → writes last-turn/<project>          (consumed by step 2)
7. PreCompact    precompact-hook  → reads touched/$SESSION
                                  → writes precompact/$SESSION          (consumed by step 2)
8. SessionEnd    session-end      → operator stats only; no model channel
```

---

## 4. Findings

Severity is what it costs, not how hard it is to fix.

### F1 — Every hook in this kit is registered twice · **duplication, measured**

`~/.claude/settings.json` carries two complete copies of the agentsmemory registration, differing
in exactly one thing: how `AGENTSMEMORY_WING` is resolved. One derives it by running
`agentsmemory-wing.sh`; the other pins the literal `wing_agentmemories`.

Everything runs twice — two session-start recalls, two prompt recalls per turn, two anchor-cue
lookups before every tool call, two appends per edit, two stop nudges. Visible without
instrumentation: every `UserPromptSubmit hook success` line arrives in pairs.

```
SessionStart          6   (2 ours ×2, plus cbm-session-reminder + otty)
PreToolUse            4   (anchor-cue ×2)
PostToolUse           4   (touched ×2)
UserPromptSubmit      3   (task-recall ×2)
UserPromptExpansion   2   (task-recall ×2)
PreCompact            2   (precompact ×2)
Stop                  3   (stop-hook ×2)
SubagentStop          2   (stop-hook ×2)
SessionEnd            2   (session-end ×2)
```

**Verdict: moat.** The pinned copy is redundant with the derived one whenever `wing.sh` resolves
correctly — and when it does not, the two disagree *silently* rather than loudly, which is the
Step 0c wing conflict expressed as configuration. Nothing in the repository can see this file, so
no gate can catch it; `aiagentmemory doctor` is the only thing that reads the registration.

### F2 — The re-ground marker has no consumer inside the kit · **dead end**

`recall-hook` writes `agentsmemory-reground/$SESSION` on every compaction. **Nothing in the kit
reads it.** The only consumer is a persistent `Monitor` the session must arm itself, documented in
`clients/claude-code/commands/am.md` §1d.

The mechanism has two halves and only one ships. A session that never ran `/am` writes a marker at
every compaction and consumes none.

Measured 2026-09-07: three stale markers in that directory, the oldest four hours old, including
this session's own — written at 22:17, still unconsumed minutes later, because no monitor had been
armed. `pgrep` found two re-ground loops on the machine, both belonging to other projects'
sessions.

**Second edge:** arming the monitor begins with `rm -f "$M"`, so arming it *after* a compaction
destroys the marker you were arming for, silently.

**Verdict: real gap.** The printed `PAUSE` is the documented floor and is the entire mechanism on
codex and pi, which have no `Monitor`.

### F3 — Two recall paths ask the palace the same kind of question · **repetition**

`recall-hook` searches the diary at `SessionStart`; `task-recall-hook` searches against the prompt
at `UserPromptSubmit`. The first prompt of any session pays both — two `am_search` round trips
seconds apart against the same corpus.

Defensible: the queries genuinely differ ("where was I" vs "what is this about"). But nothing
notices that the second follows the first, and with F1 that is **four searches before the first
tool call**.

**Verdict: watch.** Cheap to justify, never measured as a pair. §4/F7 is the precedent for fixing
it if it is ever worth fixing.

### F4 — One script on two prompt events · **repetition, correct**

`task-recall-hook` is registered on both `UserPromptSubmit` and `UserPromptExpansion`. Deliberate:
`UserPromptExpansion` is the only event carrying a slash command's arguments, and the script
branches on `EVENT` to read them, exiting silently when there are none.

**Verdict: keep.** Distinct inputs, one implementation.

### F5 — The Stop hook re-triggers Stop · **loop, guarded**

`stop-hook` speaks by `exit 2`, which is what makes Claude Code show its stderr — and which
re-fires `Stop`. Two guards terminate it: Claude Code sets `stop_hook_active: true` on every Stop
after the first in a turn and the hook exits 0 on seeing it; and a per-session marker
(`agentsmemory-stop-<sid>.done`) makes the checkpoint nudge fire once per session in `once` mode.

**Verdict: sound.** The loop exists, is named in the source, and terminates.

### F6 — Three hooks can never reach the model · **dead end, accepted**

`precompact-hook` (stdout → debug log), `session-end-hook` (session over), `touched-hook` (writes a
file). All three declare `none` and all three are correct.

The consequence worth holding: **the pre-compaction note is a single point of failure with no
fallback.** If that write fails, the hook is silent by design and the next context loses the
branch, the head, the uncommitted count and the prompt list.

**Verdict: accepted**, and gated — `TestANonInjectedChannelIsJustified` refuses a quieter channel
without a written reason.

### F7 — The status line does not re-ask the palace · **precedent**

`verify-hook` and `statusline.sh` want the same answer. Rather than asking twice, the verify hook
caches to `agentsmemory-status-<uid>.txt` and the status line reads the file.

**Verdict: this is the shape F3 is missing.**

---

## 5. What no gate can see

Written down because it bounds everything above.

- **The registration on disk.** `TestEveryInjectingHookIsOnAnInjectingEvent` gates the installer's
  *plan*. The `settings.json` in front of an operator — hand-edited, copied with `--copy`, or
  written by an older install — is gated by nothing but `aiagentmemory doctor`. F1 lives exactly
  here.
- **Healthy silence.** Both injecting hooks are silent when healthy, so `doctor` cannot fail on
  muteness without failing every correct install (`TestDoctorDoesNotFailOnSilence`). Each hook
  writes what it asked and what came back to stderr instead, and `doctor` prints it for a human.
- **Whether a marker was consumed.** Nothing observes the reground directory. F2 was found by
  `ls`, not by a check.

---

## 6. Reusing this as a protocol

To map another hook kit, run §0's three questions in order and fill four tables: **manifest**,
**firing conditions**, **state graph**, **actual registration**. The findings fall out of the
mismatches:

| Symptom in the tables | Finding class |
|---|---|
| a state file with a writer and no reader | dead end |
| a state file with two writers | race |
| the same call in two scripts on adjacent events | repetition |
| a hook whose declared channel is discarded by its event | unreachable output |
| a registration count above the manifest's | duplication |
| a hook that re-triggers its own event | loop — check the guard exists and is named |

The one table that cannot be derived from a repository is the fourth. Read it from the machine, and
date it.
