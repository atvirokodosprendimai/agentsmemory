# agentsmemory-stats.sh — shared /stats fetch for Stop and SessionEnd.
#
# Sourced, not executed. One origin (AGENTSMEMORY_MCP_URL with /mcp stripped),
# one off-switch (AGENTSMEMORY_STATS), one query builder. The installer prefixes
# every hook command with AGENTSMEMORY_MCP_URL so a hosted install and a --local
# install cannot disagree about which palace this hits.
#
# Callers set INPUT to the hook event JSON, then:
#   agentsmemory_stats_query   # sets STATS_QUERY
#   agentsmemory_stats_fetch   # sets STATS (empty when off, unset URL, or no curl)

agentsmemory_stats_query() {
  STATS_QUERY="hours=${AGENTSMEMORY_STATS_HOURS:-2}"
  TRANSCRIPT="$(printf '%s' "$INPUT" | sed -n 's/.*"transcript_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  if [ -n "${TRANSCRIPT:-}" ] && [ -f "$TRANSCRIPT" ]; then
    # Birth time where the filesystem records it (GNU %W, BSD %B), modification
    # time everywhere else — either bounds the session closely enough, and a bad
    # value simply falls back to the fixed window below.
    #
    # GNU form FIRST in both probes, and that order is load-bearing rather than
    # stylistic. BSD stat has no -c at all, so it REJECTS the GNU probe (usage
    # error, rc=1, nothing on stdout) and macOS falls through cleanly. The
    # reverse order does not fail over, because GNU's -f is --file-system and not
    # a format flag: `stat -f %B FILE` reads "%B" as a second filename, prints
    # the multiline filesystem block for FILE anyway, AND exits non-zero — so the
    # `||` branch runs too and BORN captures both. The `-gt` below then dies with
    # "integer expression expected", and every Linux session silently fell back
    # to the fixed window. Put the implementation that REJECTS the flag second;
    # the one that reinterprets it must never go first.
    BORN="$(stat -c %W "$TRANSCRIPT" 2>/dev/null || stat -f %B "$TRANSCRIPT" 2>/dev/null || true)"
    case "${BORN:-0}" in ''|*[!0-9]*|0) BORN="$(stat -c %Y "$TRANSCRIPT" 2>/dev/null || stat -f %m "$TRANSCRIPT" 2>/dev/null || echo 0)" ;; esac
    NOW="$(date +%s)"
    if [ "${BORN:-0}" -gt 0 ] && [ "$NOW" -ge "$BORN" ]; then
      MINUTES=$(( (NOW - BORN) / 60 + 1 ))
      [ "$MINUTES" -gt 1440 ] && MINUTES=1440
      STATS_QUERY="minutes=${MINUTES}&label=this%20session"
    fi
  fi
}

agentsmemory_stats_fetch() {
  STATS=""
  [ "${AGENTSMEMORY_STATS:-on}" != "off" ] || return 0
  [ -n "${AGENTSMEMORY_MCP_URL:-}" ] || return 0
  command -v curl >/dev/null 2>&1 || return 0
  # MCP URL is the palace; /stats lives on the same origin.
  STATS_ORIGIN="${AGENTSMEMORY_MCP_URL%/mcp}"
  STATS_URL="${STATS_ORIGIN}/stats?${STATS_QUERY}"
  # No arrays: macOS ships bash 3.2, where expanding an EMPTY array under `set -u`
  # aborts the script. Two explicit calls cannot break the hook on the platform
  # most of these installs run on.
  if [ -n "${AGENTSMEMORY_LOCAL_TOKEN:-}" ]; then
    STATS="$(curl -fsS -m 3 -H "Authorization: Bearer ${AGENTSMEMORY_LOCAL_TOKEN}" "$STATS_URL" 2>/dev/null || true)"
  else
    STATS="$(curl -fsS -m 3 "$STATS_URL" 2>/dev/null || true)"
  fi
}

# agentsmemory_recall_observe — ADR-041 T1. Records how many no-change assertions
# this session made and how many followed a recall.
#
# Silent and non-fatal by construction: no binary, no transcript, or a failed run
# each exit 0 without a word. A hook that reports on bookkeeping at the cost of a
# session has its priorities backwards, and this one has nothing to say in the
# common case.
agentsmemory_recall_observe() {
  [ "${AGENTSMEMORY_RECALL_RATE:-on}" != "off" ] || return 0
  command -v aiagentmemory >/dev/null 2>&1 || return 0
  [ -n "${TRANSCRIPT:-}" ] && [ -f "$TRANSCRIPT" ] || return 0
  aiagentmemory recall-observe --transcript "$TRANSCRIPT" >/dev/null 2>&1 || true
}
