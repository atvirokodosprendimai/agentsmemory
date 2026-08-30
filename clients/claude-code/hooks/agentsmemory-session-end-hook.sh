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

INPUT="$(cat || true)"

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
