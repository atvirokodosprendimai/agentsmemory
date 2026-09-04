#!/usr/bin/env bash
# agentsmemory SessionStart hook — check that this project's memories still match
# hook-output: stdout-injected
#
# its code, before the session acts on any of them.
#
# The memories worth keeping are the ones explaining WHY code is the way it is,
# and they are the only kind that can go quietly wrong: the code gets fixed, the
# sentence does not, and the next session recalls it with full confidence. Anchors
# make that detectable; running the check at session start is what makes it
# detected in time to matter rather than after the wrong decision.
#
# It prints ONLY when something drifted. A hook that reports "all good" at every
# session start is a hook people stop reading, and its output is spent context.
#
# Modes (env AGENTSMEMORY_VERIFY_HOOK):
#   on  (default) — verify at session start, print only drift.
#   off           — disabled.
#
# Every dependency is optional by design: no binary, no server, no anchors, no
# wing — each exits quietly. A hook that fails a session start to report on
# bookkeeping has its priorities backwards.
set -uo pipefail

# Consume the SessionStart event JSON so the hook is a clean filter.
INPUT="$(cat || true)"

[ "${AGENTSMEMORY_VERIFY_HOOK:-on}" = "off" ] && exit 0
command -v aiagentmemory >/dev/null 2>&1 || exit 0

# Claude Code sets CLAUDE_PROJECT_DIR; fall back to the working directory so the
# hook also works when driven by hand.
DIR="${CLAUDE_PROJECT_DIR:-$PWD}"
cd "$DIR" 2>/dev/null || exit 0

# The installer prefixes this command with AGENTSMEMORY_MCP_URL. An unset
# value means the hook was not installed (or was run by hand without the env);
# guessing localhost would poke the wrong palace when MCP is hosted.
MCP_URL="${AGENTSMEMORY_MCP_URL:-}"
[ -n "$MCP_URL" ] || exit 0

# Fail fast if no server is listening: without this the CLI would sit through its
# own connect timeout on every session start of every project, which is exactly
# the kind of delay that gets a hook uninstalled.
if command -v curl >/dev/null 2>&1; then
  HEALTH="${MCP_URL%/mcp}/healthz"
  curl -fsS -m 1 -o /dev/null "$HEALTH" 2>/dev/null || exit 0
fi

OUT="$(aiagentmemory verify --mcp-url "$MCP_URL" 2>/dev/null || true)"

# Print only when a memory has gone stale. The exact strings come from
# `verify`'s report (clients/claude-code/verify.go).
case "$OUT" in
  *DRIFTED*|*MISSING*)
    printf 'agentsmemory: some memories for this project no longer match the code.\n\n%s\n' "$OUT"
    printf '\nTreat those memories as suspect: re-read the code before acting on them, and re-file whichever are now wrong.\n'
    ;;
esac

# WRITE THE STATUS CACHE (ADR-051 T7). This hook has already asked the palace what
# it knows; the status line is a second READER of that answer rather than a second
# asker. Putting the query here is what lets the status line make no network call
# at all, which is the only way it can render constantly without freezing a prompt.
#
# Counts come from `verify`'s own report line, so the number on screen and the
# number in the report are one figure rather than two that can disagree.
STATUS_DIR="${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}"
DRIFTED="$(printf '%s' "$OUT" | sed -n 's/.*, \([0-9][0-9]*\) drifted.*/\1/p' | head -n1)"
: "${DRIFTED:=0}"
WING="${AGENTSMEMORY_WING:-}"
if [ -z "$WING" ]; then
  d="${CLAUDE_PROJECT_DIR:-$PWD}"
  while [ -n "$d" ] && [ "$d" != "/" ]; do
    for f in "$d/.aiagentmemory.local" "$d/.aiagentmemory"; do
      [ -z "$WING" ] && [ -f "$f" ] && WING="$(sed -n 's/^[[:space:]]*wing[[:space:]]*=[[:space:]]*//p' "$f" 2>/dev/null | head -1)"
    done
    [ -n "$WING" ] && break
    d="$(dirname "$d")"
  done
fi
{
  printf 'AM_WING=%s\n' "${WING:-}"
  printf 'AM_DRIFTED=%s\n' "$DRIFTED"
  printf 'AM_INBOX=%s\n' "0"
} > "$STATUS_DIR/agentsmemory-status.txt" 2>/dev/null || true

# Always succeed. A SessionStart hook that exits non-zero blocks the session, and
# nothing here is worth that.
exit 0
