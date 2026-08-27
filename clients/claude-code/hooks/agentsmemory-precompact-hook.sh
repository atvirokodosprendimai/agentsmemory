#!/usr/bin/env bash
# agentsmemory PreCompact hook — ADR-041 T4. Perform the recall, inject the result.
#
# THE FAILURE IT ADDRESSES, and it is specific: a fresh context inherits a task
# queue and no palace. The session that motivated ADR-041 began exactly there —
# mid-flight from a compaction, with momentum, a list of things to do, and every
# instruction to recall first sitting in a context it had just replaced. It read
# source, formed a belief, published it, and was wrong.
#
# ADR-017 named this mechanism in 2026-08 and left it unbuilt pending measurement:
# "have the hook PERFORM the recall and inject the results, because a subagent
# cannot skip a recall that already happened." That is the whole design. It does
# not ask the agent to remember anything.
#
# ⚠ IT PRINTS NOTHING WHEN IT HAS NOTHING (F-6). A hook that speaks at every
# compaction is one people turn off, and its output is spent context — the same
# reasoning the SessionStart verify hook states for itself. Silence is the common
# case by design, not a failure path.
#
# Off-switch: AGENTSMEMORY_PRECOMPACT=off.
set -uo pipefail

INPUT="$(cat || true)"

[ "${AGENTSMEMORY_PRECOMPACT:-on}" = "off" ] && exit 0
command -v aiagentmemory >/dev/null 2>&1 || exit 0

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$PWD}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

# The query is derived from what the session is actually touching, because the
# agent cannot be asked what it will asserted about next. Branch name plus the
# basenames of changed files is a poor query on a quiet tree and a good one on a
# working branch — which is when a compaction happens.
BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
FILES="$(git diff --name-only HEAD 2>/dev/null | head -8 | xargs -n1 basename 2>/dev/null | tr '\n' ' ' || true)"
QUERY="$(printf '%s %s' "${BRANCH:-}" "${FILES:-}" | tr -s ' ' | sed 's/^ *//;s/ *$//')"

# Nothing to go on is not a reason to guess. A query built from an empty tree
# recalls whatever is most popular, which is worse than silence.
[ -n "$QUERY" ] || exit 0
[ "${#QUERY}" -ge 8 ] || exit 0

# ⚠ A HOOK THAT CANNOT ASK MUST NOT LOOK LIKE A HOOK WITH NOTHING TO SAY. The
# first version wrote `2>/dev/null || true`, which made every failure — a missing
# token, an unreachable server, a renamed flag — identical to a clean empty recall.
# It was found by accident: the same call, used to MEASURE something else, returned
# 25 clean zeroes that were 25 swallowed errors. On a --local install this hook
# could never have spoken, and would have looked like F-6 working the whole time.
#
# `mcp` demands a workspace token even against a --local server, which has none and
# accepts any value. So pass the operator's token when there is one, and a
# placeholder when there is not — the local server ignores it, and a hosted one
# rejects it loudly, which is the correct outcome in both cases.
ERRFILE="$(mktemp 2>/dev/null || echo /tmp/agentsmemory-precompact.err)"
#
# ⚠ room=decisions AND max_distance ARE BOTH LOAD-BEARING, and both were added after
# measuring what this hook actually injected. Unscoped, the top three hits for a real
# mid-work query were THIS SESSION'S OWN TRANSCRIPT CHUNKS at distance 0.46-0.52 —
# the hook would re-inject into the fresh context the very text compaction had just
# removed. Scoped to decisions it returned real decisions, but at 0.50-0.56, which is
# the weak band: measured 2026-08-28, real questions land at 0.32-0.44 and bare
# identifiers at 0.41-0.57. A branch name is not a question.
#
# So the floor decides. At 0.42 this query returns nothing and the hook stays quiet,
# which is the correct outcome for a query with no real match — F-6 working rather
# than failing. The two classes DO overlap around 0.41-0.44, so the threshold is a
# trade rather than a boundary; it is set to exclude all 25 measured bare-identifier
# queries while keeping question-grade hits.
HITS="$(aiagentmemory mcp search "$QUERY" -a limit=3 -a snippet_chars=300 \
  -a room=decisions -a max_distance=0.42 \
  --token "${AGENTSMEMORY_LOCAL_TOKEN:-${AGENTSMEMORY_TOKEN:-local}}" 2>"$ERRFILE")"
RC=$?
if [ "$RC" -ne 0 ]; then
  # This is not "reporting all good" — it is reporting a fault, which is the one
  # thing F-6 asks a hook to speak about.
  printf 'agentsmemory: the PreCompact recall could not run, so this session starts without one: %s\n' \
    "$(head -n1 "$ERRFILE" 2>/dev/null)"
  rm -f "$ERRFILE"
  exit 0
fi
rm -f "$ERRFILE"
[ -n "$HITS" ] || exit 0

# count is the server's own field; no hits means nothing worth a line.
COUNT="$(printf '%s' "$HITS" | sed -n 's/.*"count"[[:space:]]*:[[:space:]]*\([0-9]*\).*/\1/p' | head -n1)"
case "${COUNT:-0}" in ''|0) exit 0 ;; esac

# The payload is the recall RESULT, not an instruction to recall. An instruction
# is what three layers of protocol already deliver, and what ADR-017 measured as
# the least promising intervention.
printf 'Memory recalled for this branch before compaction (agentsmemory, query: %s):\n\n%s\n' \
  "$QUERY" "$HITS"
