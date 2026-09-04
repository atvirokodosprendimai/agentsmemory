#!/usr/bin/env bash
# agentsmemory status line — the palace, where a human already looks.
# hook-output: not-a-hook — a statusLine command, registered on no event.
#
# WHY A CACHE AND NEVER A NETWORK CALL. The status line renders constantly, and a
# command that waits on a server freezes the prompt for as long as the server
# takes. Every number here is read from a file the SessionStart verify hook writes
# — it already asks those questions once per session, so this is a second reader
# of an answer that exists rather than a second asker.
#
# The age is shown for the same reason `content_truncated` exists: a number whose
# staleness is invisible is worse than no number, because it reads as current.
set -uo pipefail

CACHE="${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}/agentsmemory-status.txt"

# No cache is the ordinary state before the first session-start hook has run. Say
# NOTHING: an error string in a status line is permanent noise, and a status line
# is the one surface a user cannot dismiss.
[ -s "$CACHE" ] || exit 0

. "$CACHE" 2>/dev/null || exit 0

OUT="🧠 ${AM_WING:-no wing}"
[ -n "${AM_DRIFTED:-}" ] && [ "${AM_DRIFTED:-0}" -gt 0 ] 2>/dev/null && OUT="$OUT · ⚠ ${AM_DRIFTED} drifted"
[ -n "${AM_INBOX:-}" ] && [ "${AM_INBOX:-0}" -gt 0 ] 2>/dev/null && OUT="$OUT · 📥 ${AM_INBOX}"

# Age, in whole minutes, from the cache's own mtime. Nothing is stored about when
# it was written: a stored timestamp is one more thing that can disagree with the
# file it describes.
NOW=$(date +%s 2>/dev/null || echo 0)
MTIME=$(stat -f %m "$CACHE" 2>/dev/null || stat -c %Y "$CACHE" 2>/dev/null || echo "$NOW")
AGE=$(( (NOW - MTIME) / 60 ))
[ "$AGE" -gt 0 ] 2>/dev/null && OUT="$OUT · ${AGE}m ago"

printf '%s\n' "$OUT"
exit 0
