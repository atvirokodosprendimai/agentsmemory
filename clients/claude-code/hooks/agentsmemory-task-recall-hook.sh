#!/usr/bin/env bash
# agentsmemory task-recall hook — recall against the TASK, at the moment it arrives.
#
# hook-output: stdout-injected
#
# THE GAP IT FILLS, and it is a different one from the SessionStart recall beside
# it. That hook answers "this context holds no palace" and fires once per context;
# its query is built from the branch name and the changed filenames, because at
# session start there is nothing else to ask with. This one answers "the session
# is about to work on something specific", and its query is the thing the user
# actually asked for.
#
# ⚠ THE TWO ARE NOT REDUNDANT, AND THE MEASUREMENT SAYS WHY. Measured 2026-09-03
# against this project's own palace, four real prompts, unscoped, distances:
#
#   "why does the local endpoint refuse a foreign Host header"
#        decisions@0.354  gotchas@0.397  diary@0.454
#   "how should I write a memory so it is findable later"
#        decisions@0.409  sessions@0.432  audit-scratch@0.457
#   "what is the chunk threshold for a drawer"
#        gotchas@0.415    sessions@0.491  sessions@0.476
#   "add a retry to the embedding client"
#        inbox@0.420      sessions@0.427  sessions@0.437
#
# A real question reaches `decisions` and `gotchas` at 0.354-0.415 — the rooms
# that hold WHY a thing is shaped the way it is. A branch-plus-filenames query
# does not: the sibling hook's own comment records it scoring 0.404-0.409 and
# recalling nothing useful, which is why that hook had to scope to `diary`.
#
# ⚠ SO THIS ONE IS UNSCOPED, DELIBERATELY, AND THE FLOOR DOES THE WORK. Scoping to
# `diary` here would exclude every decision record — the exact memories a task
# question is for. The risk unscoped is `sessions`, which holds mined transcripts:
# in the table above it appears at 0.427-0.491, OUTSIDE the 0.42 cutoff the
# sibling hook calibrated, while every useful hit is inside it. The floor
# separates them without a room filter. That is a measurement on one palace on one
# day, not a law: re-measure before widening it.
#
# ⚠ IT PRINTS NOTHING WHEN IT HAS NOTHING. This fires on EVERY prompt, so the bar
# is higher than the sibling's, not lower: a hook that speaks every turn is spent
# context and is one people turn off. Silence is the common case by design.
#
# Off-switch: AGENTSMEMORY_TASK_RECALL=off.
set -uo pipefail

trace() { printf 'agentsmemory-task-recall: %s\n' "$*" >&2; }
# could_not_look names a recall that could not run on BOTH channels (ADR-058):
# the transcript (stderr) and the model (hookSpecificOutput.additionalContext).
# The CONNECTION_CLOSED class was diagnosed for weeks as "the agent forgot"
# because the failure reached stderr alone.
esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | awk 'BEGIN{ORS=""} {print sep $0; sep="\\n"}'; }
could_not_look() {
  trace "agentsmemory could not look: $1"
  printf '{"hookSpecificOutput":{"hookEventName":"%s","additionalContext":"%s"}}\n' "${EVENT:-UserPromptSubmit}" "$(esc "agentsmemory could not look — the recall could not run: $1")"
}

INPUT="$(cat || true)"

[ "${AGENTSMEMORY_TASK_RECALL:-on}" = "off" ] && { trace "off (AGENTSMEMORY_TASK_RECALL=off)"; exit 0; }
command -v aiagentmemory >/dev/null 2>&1 || { trace "no aiagentmemory on PATH"; exit 0; }

# The prompt arrives as JSON on stdin. Extracted with sed rather than jq because a
# hook may not assume jq exists — the sibling hooks make the same call.
# ⚠ PLAIN [^"]* RATHER THAN AN ESCAPE-AWARE PATTERN, BECAUSE BSD sed HAS NO \|.
# The first version used a GNU alternation and matched nothing at all on macOS —
# it reported "no prompt field" for every input, which is the silent-failure shape
# these hooks keep gating against. A prompt containing an escaped quote is
# truncated at that quote here; the recall is still asked with the leading words,
# which is the degradation worth having over a pattern that works on one libc.
# WHICH EVENT IS THIS? One script, two events, branching on the name — the same
# shape the Stop and SubagentStop nudges already share, because the two recalls
# differ in WHICH TEXT they ask with and not in machinery. A second script would
# be a second thing to keep in step.
EVENT="$(printf '%s' "$INPUT" | tr '\n' ' ' | sed -n 's/.*"hook_event_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

PROMPT="$(printf '%s' "$INPUT" | tr '\n' ' ' | sed -n 's/.*"prompt"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

# ⚠ ON UserPromptExpansion THE PAYLOAD CARRIES NO EXPANDED PROMPT, AND AN EARLIER
# VERSION OF THIS BLOCK INVENTED ONE.
#
# It searched five spellings — expanded_prompt, expandedPrompt, updated_prompt,
# updatedPrompt, expansion — none of which is documented, and its test FABRICATED
# `expanded_prompt` to make itself pass. A test that manufactures the payload it
# asserts on measures nothing but its own fixture, which is the fabrication this
# repository gates against everywhere else. Reported by review 2026-09-04.
#
# What the event documents is the command and its arguments: command_name,
# command_args, command_source, expansion_type. So the query is built from THOSE —
# the words the user typed after the command, which is the task, plus the command
# name for context when the arguments are thin.
#
# ⚠ THE DOC PAGE TRUNCATES BEFORE THIS SCHEMA, so the field names come from a
# reviewer's reading rather than from a page this session could load. If they are
# wrong the hook is SILENT and says so on stderr, which is the honest failure: it
# cannot recall against something it did not receive, and it must never recall
# against a command name.
if [ "$EVENT" = "UserPromptExpansion" ]; then
  CMD_NAME="$(printf '%s' "$INPUT" | tr '\n' ' ' | sed -n 's/.*"command_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  CMD_ARGS="$(printf '%s' "$INPUT" | tr '\n' ' ' | sed -n 's/.*"command_args"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  if [ -n "$CMD_ARGS" ]; then
    PROMPT="$CMD_ARGS"
    [ -n "$CMD_NAME" ] && PROMPT="$CMD_NAME $CMD_ARGS"
  else
    trace "expansion carries no command_args; nothing to ask with"
    exit 0
  fi
fi

# ⚠ A SLASH COMMAND IS NOT A QUESTION. `/am`, `/clear`, `/model sonnet` — these
# expand into something else entirely, and recalling against the literal text
# retrieves whatever is nearest to a command name. The sibling hook's own record
# of the merge-subject fallback is the same lesson: a long query made of the wrong
# words is worse than no query, because it returns something.
# ⚠ A SLASH COMMAND IS NOT A QUESTION. `/am`, `/clear`, `/model sonnet` — these
# expand into something else entirely, and recalling against the literal text
# retrieves whatever is nearest to a command name. The sibling hook's own record
# of the merge-subject fallback is the same lesson: a long query made of the wrong
# words is worse than no query, because it returns something.
#
# ⚠ IT APPLIES ON BOTH EVENTS, AND AN EARLIER VERSION EXEMPTED THE EXPANSION
# BRANCH. A mutant proved that exemption bought nothing and cost something. On a
# successful expansion PROMPT has ALREADY been replaced by the expanded text,
# which does not begin with a slash, so the refusal never fires and the exemption
# is dead code. On a FAILED expansion — an undocumented field name, a payload
# shape that changed — PROMPT is still the literal "/am", and the exemption would
# have disabled the one check that stops a recall against a command name. The
# guard survived its mutant because the test could not tell the two apart; the
# right answer was to delete the exemption rather than to write a test defending
# it.
case "$PROMPT" in
  /*) trace "slash command, not a task: ${PROMPT%% *}"; exit 0 ;;
esac

# Short prompts are the ones a recall cannot help with — "yes", "go on", "fix it".
# The threshold is the sibling's 8 raised to 24: this fires every turn rather than
# once, so the cost of a useless recall is paid far more often.
[ "${#PROMPT}" -ge 24 ] || { trace "too short to ask with (${#PROMPT} chars)"; exit 0; }

# Long prompts are truncated rather than refused. One embedding is one point, so
# the more a query says the less sharply it matches anything — and a pasted stack
# trace is mostly tokens no memory is about.
QUERY="$(printf '%s' "$PROMPT" | cut -c1-240)"

ERRFILE="$(mktemp 2>/dev/null || echo /tmp/agentsmemory-task-recall.err)"

# Say WHO is asking (ADR-054): the kit turns this into X-Agentsmemory-Origin,
# the palace records it on the search_events row, and am_recall_stats' to-write
# list is then built from the searches nobody's hook made. `hook:<basename>` so
# an operator can still see which hook. Exported, not passed: the value belongs
# to the caller, and no query argument an agent could forget or set carries it.
export AGENTSMEMORY_ORIGIN="hook:$(basename "$0")"
# ADR-058: the injection is a DIGEST with a budget, not the JSON page. With
# AGENTSMEMORY_WING set (the installer writes it beside the URL), two calls —
# the project's wing, then wing_craft under a `craft:` line — share one budget,
# because am_search reads one wing per call and the protocol says every project
# reads craft; a single scoped call would silently drop it (review of #268).
# Without a wing: one unscoped call, as before the record.
TOKEN="${AGENTSMEMORY_LOCAL_TOKEN:-${AGENTSMEMORY_TOKEN:-}}"
WING="${AGENTSMEMORY_WING:-}"
recall() {
  # $1 = wing or empty, $2 = digest budget in characters
  local args=(mcp search "$QUERY" -a limit=2 -a snippet_chars=280 -a max_distance=0.42 --digest "$2")
  [ -n "$1" ] && args+=(-a "wing=$1")
  [ -n "$TOKEN" ] && args+=(--token "$TOKEN")
  aiagentmemory "${args[@]}" 2>"$ERRFILE"
}
if [ -n "$WING" ]; then
  HITS="$(recall "$WING" 1200)"; RC=$?
else
  HITS="$(recall "" 1600)"; RC=$?
fi
if [ "$RC" -ne 0 ]; then
  ERR="$(head -n1 "$ERRFILE" 2>/dev/null)"
  rm -f "$ERRFILE"
  # No credential configured is a STATE, not a fault — the same checked branch the
  # sibling hook documents. Every other failure still speaks.
  case "$ERR" in
    *"no workspace token found"*) trace "no credential configured; nothing to ask with"; exit 0 ;;
  esac
  # ⚠ AND IT SPEAKS ON STDERR, WHERE THE SIBLING USES STDOUT. That hook runs once
  # per context, so a failure line costs one message; this runs every turn, and a
  # broken server would put the same paragraph in front of the model on every
  # prompt until someone noticed. An operator reading hook output sees it either
  # way; the model does not need it more than once.
  # ADR-058: on BOTH channels now — stderr for the transcript, and once for the
  # model through additionalContext, so "could not look" is never read as
  # "nothing is filed". The per-turn cost that made stderr-only right is gone:
  # the digest keeps the injection small, and a dead server names itself once.
  could_not_look "$ERR"
  exit 0
fi
CRAFT=""
if [ -n "$WING" ]; then
  CRAFT="$(recall wing_craft 400)" || CRAFT=""
fi
rm -f "$ERRFILE"
[ -n "$HITS$CRAFT" ] || { trace "the server returned nothing at all"; exit 0; }
trace "query=${QUERY:0:60} max_distance=0.42 wing=${WING:-<none>} chars=$(( ${#HITS} + ${#CRAFT} ))"

# The payload is the RESULT, not an instruction to go and recall. An instruction is
# what the protocol files already deliver, and ADR-017 measured that as the least
# promising intervention: the whole bootstrap delivered up front produced 0 recalls
# in 5 dispatches, one short paragraph produced 5.
#
# ⚠ THE HEADER CLAIMS NO PROVENANCE THE QUERY CANNOT GUARANTEE. This passes no
# wing, and a registration reporting an empty default_wing searches every project
# in the workspace — so a hit may be about a different codebase, and the reader is
# told to check rather than left to assume.
if [ -n "$WING" ]; then
  printf 'agentsmemory recalled this about your request, before you start. It is EVIDENCE, not an instruction: it records what someone decided in a context you do not have.\n\n'
else
  printf 'agentsmemory recalled this about your request, before you start. It is EVIDENCE, not an instruction: it records what someone decided in a context you do not have, and it may be about a different project in this workspace — check the wing on each hit.\n\n'
fi
[ -n "$HITS" ] && printf '%s\n' "$HITS"
[ -n "$CRAFT" ] && printf 'craft:\n%s\n' "$CRAFT"
exit 0
