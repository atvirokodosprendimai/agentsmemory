#!/usr/bin/env bash
# agentsmemory PreCompact hook — ADR-059. Write the session's state note BEFORE the context is summarised.
# hook-output: none — PreCompact stdout goes to the debug log (ADR-041's shipped
# defect), so this hook speaks to nobody; it writes a file the SessionStart recall
# hook reads on `source=compact`, which is the event whose stdout IS injected.
#
# WHAT IS LOST IN A COMPACTION, FIRST: not the prose — the summary keeps prose —
# but the ground truth the session was standing on. Which branch, which commit,
# what is uncommitted, which files it had edited. None of that is a sentence a
# summary is optimised to keep, and the session that motivated ADR-041 resumed
# without it and published a wrong belief. This hook records exactly that, in the
# last moment it is still true, and the post-compaction start hands it back.
#
# THE TOUCHED LIST IS COPIED, NOT RECOMPUTED. `git status` is what is uncommitted
# NOW: a subset of what the session edited (committed edits are gone from it)
# and a superset (a teammate's stash, generated files). The PostToolUse recorder
# (ADR-051 T3) keeps the session's own list, keyed by the same session id, and
# that is the record a resumed session wants.
#
# It is the same shape as that recorder: a per-session file under the state dir,
# written on a debug-log event, read by a hook on an event that can speak. The
# note is OVERWRITTEN on every compaction so it is always the most recent one;
# a session that compacts twice wants the second note, not both.
set -uo pipefail

trace() { printf 'agentsmemory-precompact: %s\n' "$*" >&2; }

INPUT="$(cat || true)"
: "${INPUT:=}"

FLAT="$(printf '%s' "$INPUT" | tr '\n' ' ')"
SESSION="$(printf '%s' "$FLAT" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
TRIGGER="$(printf '%s' "$FLAT" | sed -n 's/.*"trigger"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
# ADR-062: the transcript is where the task in flight is read from, below. It is
# extracted here beside the other payload fields, and defaults to empty so the
# guard on it holds under `set -u` when the harness sends no path.
TRANSCRIPT="$(printf '%s' "$FLAT" | sed -n 's/.*"transcript_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

# The session id is the key AND a path component, so it is constrained to what a
# session id can be — the touched hook's guard, verbatim, because the two files
# must be keyed identically for the copy below to find its source.
[ -z "$SESSION" ] && { trace "no session_id; refusing to write an unscoped note"; exit 0; }
case "$SESSION" in
  *[!A-Za-z0-9_-]*) trace "session_id is not a safe path component; refusing"; exit 0 ;;
esac

STATE="${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}"
DIR="$STATE/agentsmemory-precompact"
mkdir -p "$DIR" 2>/dev/null || { trace "cannot create $DIR"; exit 0; }
NOTE="$DIR/$SESSION"

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$PWD}"
cd "$PROJECT_DIR" 2>/dev/null || true

# Every git field tolerates a directory that is not a repository: a session
# started outside one still compacts, and a note with empty fields is worth more
# than no note, because the touched list below does not depend on git at all.
BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
HEAD_SHA="$(git rev-parse --short HEAD 2>/dev/null || true)"
DIRTY="$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ' || true)"
: "${DIRTY:=0}"

TOUCHED_LIST="$STATE/agentsmemory-touched/$SESSION"
TOUCHED=0
[ -s "$TOUCHED_LIST" ] && TOUCHED="$(wc -l < "$TOUCHED_LIST" | tr -d ' ')"

# key=value per line. Written to a temp file and moved, so a compaction that
# fires while the previous note is being read never hands back half a note.
TMP="$(mktemp "$DIR/.$SESSION.XXXXXX" 2>/dev/null)" || { trace "cannot write under $DIR"; exit 0; }
{
  printf 'at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'trigger=%s\n' "${TRIGGER:-unknown}"
  printf 'branch=%s\n' "$BRANCH"
  printf 'head=%s\n' "$HEAD_SHA"
  printf 'dirty=%s\n' "$DIRTY"
  printf 'touched=%s\n' "$TOUCHED"
  # Bounded to eight: the read side renders these on one line, and a wall of
  # paths after a compaction is skimmed rather than read. The count above says
  # how many were elided.
  [ "$TOUCHED" -gt 0 ] && head -n 8 "$TOUCHED_LIST" | sed 's/^/file=/'
  # ADR-062: the TASK IN FLIGHT, so the other side can name what it interrupted.
  #
  # ADR-059 hands back where the tree was; it cannot say what the session was
  # DOING, and "re-ground" is an instruction nobody can act on without a subject.
  # The last plain user message is that subject. Read HERE, before the context is
  # summarised, because the post-compaction side has only the summary — the one
  # thing this project refuses to treat as a source.
  #
  # Plain user turns only: a sidechain turn and this kit's own recall injection
  # are both `type=user` in a transcript, and either would name the wrong work.
  # One line, 200 characters, whitespace collapsed — a label for a skill
  # invocation, not a record of the prompt. AGENTSMEMORY_LAST_TURN=off skips it.
  if [ "${AGENTSMEMORY_LAST_TURN:-on}" != "off" ] && [ -n "$TRANSCRIPT" ] && [ -r "$TRANSCRIPT" ]; then
    PENDING="$(grep -v '"isSidechain":[[:space:]]*true' "$TRANSCRIPT" 2>/dev/null \
      | sed -n 's/.*"role"[[:space:]]*:[[:space:]]*"user"[[:space:]]*,[[:space:]]*"content"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | grep -v '^agentsmemory recalled' | tail -n 1 | tr '\n\r\t' '   ' | cut -c1-200 \
      | sed -e 's/[[:space:]][[:space:]]*/ /g' -e 's/^ //' -e 's/ $//')"
    [ -n "$PENDING" ] && printf 'prompt=%s\n' "$PENDING"
  fi
} > "$TMP" && mv -f "$TMP" "$NOTE" || { rm -f "$TMP"; trace "could not write the note"; exit 0; }

trace "note written: $NOTE (branch=$BRANCH head=$HEAD_SHA dirty=$DIRTY touched=$TOUCHED)"
exit 0
