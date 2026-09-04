#!/usr/bin/env bash
# agentsmemory PostToolUse hook — record WHICH FILES this session edited.
# hook-output: none — it writes a local file and says nothing to the model. The
# Stop hook reads that file; this one is the write half and has no channel to be
# discarded.
#
# ⚠ THIS IS NOT THE PostToolUse AUDIT ADR-041 REJECTED, and that rejection stands:
# "it reports the error after it has been published, which is the position this
# repository was already in." This delivers no verdict. It appends a PATH to a
# list. There is nothing judged, so there is nothing to deliver late.
#
# WHAT IT IS FOR. At end of turn the Stop hook asks the agent to persist. It could
# only ever ask in the abstract, because nothing knew what the session had touched
# — so the nudge said "persist something" and the agent decided what that meant
# after the fact. With this list the nudge can name the files, which is the
# difference between a reminder and a question with an answer in it.
set -uo pipefail

INPUT="$(cat || true)"
: "${INPUT:=}"

trace() { printf 'agentsmemory-touched: %s\n' "$1" >&2; }

[ "${AGENTSMEMORY_TOUCHED_HOOK:-on}" = "off" ] && exit 0

# WRITE TOOLS ONLY. A record of every Read is a record of nothing: a session reads
# tens of files it never changes, and a list that long names nothing in particular.
TOOL="$(printf '%s' "$INPUT" | tr '\n' ' ' | sed -n 's/.*"tool_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
case "$TOOL" in
  Edit|Write|NotebookEdit|MultiEdit) : ;;
  *) exit 0 ;;
esac

FILE="$(printf '%s' "$INPUT" | tr '\n' ' ' | sed -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
[ -z "$FILE" ] && exit 0

SESSION="$(printf '%s' "$INPUT" | tr '\n' ' ' | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
# A record with no session id would be shared by every concurrent session, and one
# session would then report another's work at its own end of turn.
[ -z "$SESSION" ] && { trace "no session_id; refusing to write an unscoped record"; exit 0; }
# ⚠ IT BECOMES A PATH COMPONENT, so it is constrained to what a session id can be.
# The extraction is a greedy match over a flattened payload, so the LAST match wins
# and nested tool_input/tool_response fields appear after the real one. Not
# exploitable today — JSON escaping defeats the pattern — but it is one unescaped
# producer away from an arbitrary-append primitive. Reported by review.
case "$SESSION" in
  *[!A-Za-z0-9_-]*) trace "session_id is not a safe path component; refusing"; exit 0 ;;
esac

ROOT="${CLAUDE_PROJECT_DIR:-$PWD}"
REL="${FILE#"$ROOT"/}"
[ "$REL" = "$FILE" ] && REL="${FILE#./}"

DIR="${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}/agentsmemory-touched"
mkdir -p "$DIR" 2>/dev/null || exit 0
LIST="$DIR/$SESSION"

# DEDUPLICATED. A file edited fifteen times is one file. A list that grows with
# every keystroke is a list nobody reads, and the nudge that quotes it becomes a
# wall of repeats.
if [ -f "$LIST" ] && grep -qxF "$REL" "$LIST" 2>/dev/null; then
  exit 0
fi
printf '%s\n' "$REL" >> "$LIST"
exit 0
