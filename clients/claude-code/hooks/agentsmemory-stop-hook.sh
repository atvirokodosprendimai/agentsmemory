#!/usr/bin/env bash
# agentsmemory Stop hook — nudge Claude to persist the session into agentsmemory
# memory (the team-shared MCP) before the turn ends: a diary entry, new
# knowledge-graph facts, and any notable decisions as drawers. Mirrors the
# mempalace stop-hook pattern.
#
# It reads the Stop event JSON on stdin, prints a checkpoint to stderr, and exits
# 2 so Claude Code surfaces it as blocking Stop feedback — the turn pauses until
# the session is persisted (or the reminder is acknowledged).
#
# Modes (env AGENTSMEMORY_STOP_HOOK):
#   on   (default) — remind on every Stop, like mempalace.
#   once           — remind only on the first Stop of a session, then stay quiet.
#   off            — disabled.
set -euo pipefail

# Consume stdin so the hook is a clean filter even when nothing reads it.
INPUT="$(cat || true)"

MODE="${AGENTSMEMORY_STOP_HOOK:-on}"
[ "$MODE" = "off" ] && exit 0

# In "once" mode, fire only the first time per harness session. The session id is
# parsed from the event JSON without requiring jq, so the hook has no runtime deps.
if [ "$MODE" = "once" ]; then
  SID="$(printf '%s' "$INPUT" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  MARKER="${TMPDIR:-/tmp}/agentsmemory-stop-${SID:-nosession}.done"
  if [ -n "${SID:-}" ] && [ -f "$MARKER" ]; then
    exit 0
  fi
  [ -n "${SID:-}" ] && : >"$MARKER" 2>/dev/null || true
fi

# The checkpoint goes to stderr; exit 2 makes Claude Code show it as Stop feedback.
cat >&2 <<'MSG'
agentsmemory checkpoint — persist this session into team memory before stopping:
  1. am_diary_write — an AAAK session summary (what changed, why, open threads).
  2. am_kg_add      — new durable facts as subject -> predicate -> object triples.
  3. am_add_drawer  — notable decisions / code, verbatim, into the right wing + room.
Use the agentsmemory MCP tools (am_ prefix). Skip only if nothing was worth
remembering — and say so. Disable this reminder with AGENTSMEMORY_STOP_HOOK=off (or =once).
MSG
exit 2
