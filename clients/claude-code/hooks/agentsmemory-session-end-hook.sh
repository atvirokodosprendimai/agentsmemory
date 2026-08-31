#!/usr/bin/env bash
# agentsmemory SessionEnd hook — the closing read on how memory did this session.
# hook-output: none — SessionEnd cannot reach the model on any channel; the session is
# already over. What this writes is for the operator and for /stats.
#
#
# The Stop hook fires when the AGENT finishes a turn, which is the right place for
# the persist checkpoint (it needs the agent to still be running) but the wrong
# place for a summary: at the first Stop the session has barely started. This
# fires when the session actually ends, so the numbers describe the whole of it.
#
# Modes (env AGENTSMEMORY_STATS):
#   on  (default) — print the recall report when the session ends.
#   off           — disabled. Same switch as the Stop hook's report, so turning
#                   stats off is one name, not two.
#
# The palace is AGENTSMEMORY_MCP_URL (installer-injected). /stats is that origin
# with /mcp stripped. Everything is optional: no URL, no server, no curl each
# exit quietly. Nothing here is worth interfering with a session shutting down.
set -uo pipefail

# ⚠ READ THE PAYLOAD, BUT NEVER WAIT ON IT. `INPUT="$(cat)"` blocks until stdin
# reaches EOF, so this hook's runtime was governed by when the harness closed its
# stdin rather than by its own work — one 10ms GET. SessionEnd is the only event
# that runs while the harness is tearing DOWN, so whether it closes stdin and
# grants a scheduling slice before exiting is a race, and losing it prints
# `SessionEnd hook … failed: Hook cancelled`. Measured 2026-08-31: 1112ms with
# stdin closed promptly, 9187ms with a writer holding it open for 8s — the hook
# waits as long as it is given.
#
# The payload is a PRECISION input, not a requirement: agentsmemory_stats_query
# sets a working fixed window BEFORE consulting INPUT, and transcript_path only
# narrows it to the real session length. So the fallback this needs already
# existed and the unbounded read was what made it unreachable.
#
# `read -t` and `read -d` are both bash 3.2, which the stats helper's own
# comments target deliberately. On timeout bash keeps whatever arrived, so a slow
# writer degrades to a partial payload rather than to nothing.
INPUT=""
IFS= read -r -d '' -t "${AGENTSMEMORY_STATS_STDIN_TIMEOUT:-1}" INPUT || true

[ "${AGENTSMEMORY_STATS:-on}" = "off" ] && exit 0

HOOK_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck disable=SC1091
. "$HOOK_DIR/agentsmemory-stats.sh"
agentsmemory_stats_query
agentsmemory_stats_fetch

# stdout, not stderr: SessionEnd cannot block anything, so there is no feedback
# channel to use — this is a plain closing note.
# $(...) strips trailing newlines; give the report its last one back.
[ -n "${STATS:-}" ] && printf '%s\n' "$STATS"
exit 0
