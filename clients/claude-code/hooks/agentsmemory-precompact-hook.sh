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
  #
  # ⚠ A SLASH COMMAND IS NOT THE TASK, AND A LIVE COMPACTION IS WHAT PROVED IT.
  # Measured 2026-09-05 in this checkout, on the real compaction ADR-062's
  # follow-up asked for: a session compacted by `/compact` wrote `prompt=/compact`,
  # so the directive read "your first action is `/amm /compact`" — a label naming
  # no work, produced on the one occasion the session cannot recover the work any
  # other way. The turn that TRIGGERS a compaction is usually the command that
  # triggered it, so the last plain turn is the wrong one exactly when this fires.
  # Dropping a turn that opens with `/` falls back to the work actually in flight.
  #
  # ⚠ AND `^/` IS ONLY ONE OF THE SPELLINGS — THE LIVE WAKE PROVED IT.
  # Measured 2026-09-05, on the first compaction with the monitor armed: the
  # emitted line read "/amm <command-message>am</command-message>
  # <command-name>/am</command-name><command-args>recall</command-args>". A slash
  # command reaches the transcript WRAPPED in those tags, so its content does not
  # begin with `/` and the guard above passed it through — as it would for
  # `<local-command-stdout>`, a `<task-notification>` and a `<system-reminder>`,
  # all of which are harness chrome that names no work. The `^/` case is real and
  # stays; this is the same defect in the spelling the fixture did not have.
  # Named rather than `^<`, because a user turn may legitimately open with `<`.
  #
  # ⚠ AND THE CONTINUATION PREAMBLE IS THE FORM THAT COMPOUNDS. Found by running
  # this hook against the REAL transcript rather than a fixture: after any
  # compaction the harness injects a plain `type=user` turn opening "This session
  # is being continued from a previous conversation…". It is prose, so no
  # bracket rule reaches it — and it is the LAST plain turn for as long as the
  # resumed session works without the user typing, so the SECOND compaction of a
  # session would label its wake with the FIRST compaction's preamble. The label
  # would degrade exactly as the session got longer, which is when re-grounding
  # matters most.
  #
  # ⚠ AND A PEER SESSION'S MESSAGE IS CHROME BY THE SAME ARGUMENT. Reported on
  # PR #283 by a reviewer who ran this extraction against a DIFFERENT session's
  # real transcript — a second corpus, which is the only reason it was found:
  # a relayed peer message arrives as a plain `type=user` turn opening "Another
  # Claude session sent a message:". Prose again, so no bracket rule reaches it,
  # and on a session that talks to peers it can be the last plain turn for a long
  # stretch. Not reproducible in the session that fixed it, which has no peers —
  # accepted on the reviewer's artifact rather than on a fixture anybody typed.
  #
  # ⚠ SO THE FORMS ARE A DENY LIST NOW, AND EVERY ENTRY ON IT WAS OBSERVED
  # RATHER THAN IMAGINED. They were found one at a time, each invisible to the
  # fixtures that existed when the previous one was fixed, and three of them came
  # from running this code against a real transcript instead of a written one.
  # The list is kept in one place so a reader can see that, and each entry names
  # where it was seen; nothing goes on it by reasoning about what a harness might
  # emit. Matched by NAME rather than by shape (`^<` would take four of them in
  # one stroke) because a user turn may legitimately open with a bracket, and the
  # cost of that shortcut is silently dropping the work it was meant to name.
  if [ "${AGENTSMEMORY_LAST_TURN:-on}" != "off" ] && [ -n "$TRANSCRIPT" ] && [ -r "$TRANSCRIPT" ]; then
    CHROME='^agentsmemory recalled'                                        # this kit's own recall injection
    CHROME="$CHROME|^/"                                                    # bare slash command — the /compact that triggers the compaction
    CHROME="$CHROME|^<command-"                                            # wrapped slash command — the live wake's own defective label
    # ⚠ `^<local-command` STAYS BROAD. Narrowing it to `-stdout` while writing the
    # list above let `<local-command-caveat>` — the wrapper the harness puts round
    # a local command's own output — through on the very next real-transcript run.
    # The prefix covers a family whose members are not enumerable in advance.
    CHROME="$CHROME|^<local-command|^<task-notification|^<system-reminder"  # bracketed harness chrome
    CHROME="$CHROME|^This session is being continued from a previous conversation" # the post-compaction preamble
    CHROME="$CHROME|^Another Claude session sent a message"                # a peer session's relayed message
    # A hook's own output, handed back to the model as a plain user turn. Found
    # 2026-09-05 in THIS session's transcript and in two OTHER sessions' notes
    # on the same machine, all three reading
    #   prompt=Stop hook feedback:\n[... bash -- '.../agentsmemory-stop-hook.sh']
    # — the kit labelling the wake with its own command line, which names no
    # work at all. Prose, so no bracket rule reaches it.
    CHROME="$CHROME|^Stop hook feedback"                                   # this kit's own Stop hook, fed back as a user turn
    # ⚠ THE grep PREFILTER IS NOT A TIDY-UP; IT IS WHY THIS HOOK FINISHES.
    # The `sed` below opens with `.*`, and sed BACKTRACKS: it retries the match
    # from every position on the line. Measured 2026-09-05 against this
    # checkout's own transcript — 29MB, 16,406 lines, longest line 243,820
    # characters — that one stage took 47.25s of the hook's 49.19s total, while
    # READING the whole file takes 0.01s. So the cost is not I/O and not the
    # file's size; it is the length of the longest LINE, which a tool result
    # sets and nothing bounds.
    #
    # ⚠ AND THE FAILURE IS SILENT AND TOTAL, NOT SLOW. The hook is registered
    # with `timeout: 75`, so a session slightly longer than the one measured is
    # KILLED before the note is written — and with no note the recall hook's
    # `[ -s "$NOTE" ]` skips its whole block, so no re-ground marker is written
    # and the monitor armed by `/am` Step 1d waits for ever with nothing to see.
    # Reported by the owner as "/compact and nothing happens for 1 minute", on a
    # session started with `--autocompact 600000`, which is exactly the flag that
    # lets a transcript grow this far before compacting.
    #
    # `grep` runs a DFA and never backtracks, so restricting sed to the lines
    # that CAN match takes the same work from 47.25s to 0.10s — 271 lines in and
    # 271 lines out, byte-identical. The pattern is the sed's own middle, kept
    # beside it: if one is edited the other must be, and a prefilter narrower
    # than the extraction would drop turns silently.
    #
    # Every test over this pipeline drives a short fixture, which is why nothing
    # caught it — the §Reachability defect in its performance spelling.
    USERTURN='"role"[[:space:]]*:[[:space:]]*"user"[[:space:]]*,[[:space:]]*"content"[[:space:]]*:[[:space:]]*"'
    PENDING="$(grep -v '"isSidechain":[[:space:]]*true' "$TRANSCRIPT" 2>/dev/null \
      | grep -E "$USERTURN" \
      | sed -n 's/.*"role"[[:space:]]*:[[:space:]]*"user"[[:space:]]*,[[:space:]]*"content"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | grep -Ev "$CHROME" \
      | tail -n 1 | tr '\n\r\t' '   ' | cut -c1-200 \
      | sed -e 's/[[:space:]][[:space:]]*/ /g' -e 's/^ //' -e 's/ $//')"
    [ -n "$PENDING" ] && printf 'prompt=%s\n' "$PENDING"
  fi
} > "$TMP" && mv -f "$TMP" "$NOTE" || { rm -f "$TMP"; trace "could not write the note"; exit 0; }

trace "note written: $NOTE (branch=$BRANCH head=$HEAD_SHA dirty=$DIRTY touched=$TOUCHED)"
exit 0
