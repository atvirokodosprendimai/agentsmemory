#!/usr/bin/env bash
# agentsmemory recall hook — ADR-041 T4. Perform the recall, inject the result.
#
# hook-output: stdout-injected
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
# ⚠ IT RUNS ON SessionStart, NOT PreCompact, AND THAT IS THE WHOLE POINT OF THE
# EVENT CHOICE. Claude Code injects a hook's stdout into the model's context for
# exactly three events — SessionStart, UserPromptSubmit and UserPromptExpansion.
# For every other event, stdout goes to the debug log and the model never sees a
# character of it. This hook shipped first on PreCompact: it performed the recall,
# printed it, and threw it away, and every test passed because they all asserted
# what the SCRIPT wrote rather than whether anything could read it. Two mutants
# were killed against a mechanism that could not work.
#
# SessionStart also fires on the correct SIDE of a compaction. Output injected
# BEFORE compaction is part of the context being compacted — the recall would be
# summarised away in the same pass that discarded the palace. The fresh context is
# where it is needed, and SessionStart's `compact` matcher is where the fresh
# context begins.
#
# It is registered WITHOUT a matcher, so it fires on `startup`, `resume`, `clear`
# and `compact` alike. That is deliberate and it is broader than the named failure:
# all four begin a context that holds no palace, and `compact` is merely the most
# frequent in a long-lived session. A session that runs for days compacts many
# times and starts once.
#
# ⚠ IT PRINTS NOTHING WHEN IT HAS NOTHING (F-6). A hook that speaks at every
# session start is one people turn off, and its output is spent context — the same
# reasoning the SessionStart verify hook states for itself. Silence is the common
# case by design, not a failure path.
#
# Off-switch: AGENTSMEMORY_RECALL=off.
set -uo pipefail

INPUT="$(cat || true)"

[ "${AGENTSMEMORY_RECALL:-on}" = "off" ] && exit 0
command -v aiagentmemory >/dev/null 2>&1 || exit 0

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$PWD}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"

# ⚠ THE FILE LIST IS THE BRANCH'S WORK, NOT THE UNCOMMITTED DIFF, and the
# difference decides whether this hook can ever speak. The first version asked
# `git diff --name-only HEAD`, which is uncommitted changes only — empty on the
# clean tree that a session usually sits on after a commit. The query collapsed to
# the bare branch name, and measured 2026-08-28 against this palace, bare branch
# names land at 0.450-0.509 while the floor is 0.42: silent on every one of three
# real branches. The same three branches, queried with the merge-base file list,
# return hits at 0.391-0.414 and each returns DIFFERENT drawers — the composite is
# discriminating rather than ranking whatever is most popular.
DEFAULT="$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null || true)"
DEFAULT="${DEFAULT#origin/}"
if [ -z "$DEFAULT" ]; then
  for candidate in main master; do
    if git rev-parse --verify --quiet "$candidate" >/dev/null 2>&1; then DEFAULT="$candidate"; break; fi
  done
fi
FILES=""
if [ -n "$DEFAULT" ]; then
  BASE="$(git merge-base HEAD "$DEFAULT" 2>/dev/null || true)"
  if [ -n "$BASE" ]; then
    FILES="$(git diff --name-only "$BASE"..HEAD 2>/dev/null | head -8 | xargs -n1 basename 2>/dev/null | tr '\n' ' ' || true)"
  fi
fi
# On the default branch itself the merge-base is HEAD and that diff is empty, so
# fall back to what is uncommitted — the only work in progress there is.
[ -n "$FILES" ] || FILES="$(git diff --name-only HEAD 2>/dev/null | head -8 | xargs -n1 basename 2>/dev/null | tr '\n' ' ' || true)"

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
ERRFILE="$(mktemp 2>/dev/null || echo /tmp/agentsmemory-recall.err)"
#
# ⚠ room=decisions AND max_distance ARE BOTH LOAD-BEARING, and both were added after
# measuring what this hook actually injected. Unscoped, the top three hits for a real
# mid-work query were THIS SESSION'S OWN TRANSCRIPT CHUNKS at distance 0.46-0.52 —
# the hook would re-inject into the fresh context the very text compaction had just
# removed. The floor then decides relevance: measured 2026-08-28, real questions land
# at 0.32-0.44, bare identifiers at 0.41-0.57, and branch+file queries at 0.39-0.41.
# The classes overlap around 0.41-0.44, so 0.42 is a trade rather than a boundary.
# ⚠ PASS --token ONLY WHEN THE ENVIRONMENT SUPPLIES ONE. The first version always
# passed one, defaulting to the placeholder `local`, which looks harmless and is
# not: --token OVERRIDES the CLI's own resolution, so an install whose token lives
# in agentsmemory.env authenticated as "local" and was refused. Omitting the flag
# lets the CLI resolve the credential the way `verify` already does.
set -- mcp search "$QUERY" -a limit=3 -a snippet_chars=300 -a room=decisions -a max_distance=0.42
TOKEN="${AGENTSMEMORY_LOCAL_TOKEN:-${AGENTSMEMORY_TOKEN:-}}"
[ -n "$TOKEN" ] && set -- "$@" --token "$TOKEN"
HITS="$(aiagentmemory "$@" 2>"$ERRFILE")"
RC=$?
if [ "$RC" -ne 0 ]; then
  ERR="$(head -n1 "$ERRFILE" 2>/dev/null)"
  rm -f "$ERRFILE"
  # ⚠ NO CREDENTIAL CONFIGURED IS A STATE, NOT A FAULT — and it is the state a
  # Claude HOSTED install is in today. That install puts the token in the MCP
  # registration's Authorization header, which the CLI does not read, and writes
  # no agentsmemory.env (only the codex path does, because `codex mcp add` has no
  # static-header flag). So the hook cannot ask, and saying so at every session
  # start would be a line the operator cannot act on, four times a day — exactly
  # the noise F-6 exists to prevent.
  #
  # This is a CHECKED BRANCH, not a swallowed error: every other failure — a
  # wrong token, an unreachable server, a renamed flag — still speaks below. The
  # gap itself is recorded in BACKLOG.md rather than hidden by this line.
  case "$ERR" in
    *"no workspace token found"*) exit 0 ;;
  esac
  # This is not "reporting all good" — it is reporting a fault, which is the one
  # thing F-6 asks a hook to speak about.
  printf 'agentsmemory: the recall could not run, so this session starts without one: %s\n' "$ERR"
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
printf 'Memory recalled for this branch (agentsmemory, query: %s):\n\n%s\n' \
  "$QUERY" "$HITS"
