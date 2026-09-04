#!/usr/bin/env bash
# agentsmemory PreToolUse hook — surface the memory that pins THIS file.
# hook-output: structured — it writes hookSpecificOutput.additionalContext rather
# than bare stdout, which is how PreToolUse (not an stdout-injecting event) still
# reaches the model. Measured 2026-09-03 on SubagentStart with a nonce probe and a
# severed-hook arm; PreToolUse is documented for the same field.
#
# ⚠ THIS IS NOT ADR-041's T5, AND THE DIFFERENCE IS THE WHOLE JUSTIFICATION.
# T5 is a PreToolUse cue and it is STOPPED on a measured, disqualifying finding:
# the only query available at that moment is a bare grep pattern, and a bare
# identifier retrieves a session's narrative rather than a team's decision
# (re-measured 2026-09-03: 14 of 25 bare identifiers top out in diary, sessions,
# stress2 or inbox, against 0 of 5 real questions).
#
# This hook ISSUES NO QUERY. A code anchor is an exact pin — path, snippet, repo
# and the drawer it belongs to — so the lookup is a join on a path the tool call
# already names. Nothing is ranked, so there is no relevance to fall short of. An
# anchor either pins the file being opened or it does not.
#
# THE FIRING RATE IS THE OTHER HALF. T5's frequency arm passed (3.4% of turns) and
# its relevance arm failed. This one fires only when a memory pins that exact path,
# which is far narrower, and it prints NOTHING otherwise. Silence is the common
# case and it must cost nothing: a cue that fires when it has nothing to say is
# how a channel gets ignored.
set -uo pipefail

INPUT="$(cat || true)"
: "${INPUT:=}"

trace() { printf 'agentsmemory-anchor-cue: %s\n' "$1" >&2; }

[ "${AGENTSMEMORY_ANCHOR_CUE:-on}" = "off" ] && exit 0

# The path the tool is about to touch. BSD-safe sed (no \| alternation): the
# task-recall hook shipped a GNU-only pattern that matched nothing on macOS and
# reported "no prompt field" for every input.
FILE="$(printf '%s' "$INPUT" | sed -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
[ -z "$FILE" ] && { trace "no file_path in the hook input; nothing to pin against"; exit 0; }

# Anchors are stored repo-relative. A hook receives an absolute path, so strip the
# project root — an absolute path matches no stored anchor and the cue would be
# permanently silent for the wrong reason.
ROOT="${CLAUDE_PROJECT_DIR:-$PWD}"
REL="${FILE#"$ROOT"/}"
[ "$REL" = "$FILE" ] && REL="${FILE#./}"

ERRFILE="$(mktemp 2>/dev/null || echo /tmp/agentsmemory-anchor-cue.err)"
# repo as well as path: anchors are workspace-wide, so an unscoped listing returns
# other projects' pins. The repo label is what tells a verifier whether an anchor
# is even about the tree in front of it.
# ⚠ THE REMOTE, NOT THE DIRECTORY. An anchor's repo label is minted from the git
# remote basename, and a checkout is very often named something else — this tree
# lives in "agentsmemory-main" while every anchor in it is labelled
# "agentsmemory". Measured 2026-09-04: deriving the label from the working
# directory made the cue permanently silent on files that ARE pinned, and silence
# is indistinguishable from "nothing pins this" — the failure a live run catches
# and no unit test with a stubbed binary can.
# ⚠ NOT `basename -s`: it is not POSIX and busybox lacks it, so on Alpine REPO
# came back empty and fell through to the directory name — which this file's own
# comment says makes the cue permanently silent. A silent wrong answer on the one
# platform this kit otherwise took trouble to support. Reported by review.
REPO="$(basename "$(git -C "$ROOT" remote get-url origin 2>/dev/null)" 2>/dev/null)"
REPO="${REPO%.git}"
[ -z "$REPO" ] && REPO="$(basename "$(git -C "$ROOT" rev-parse --show-toplevel 2>/dev/null || echo "$ROOT")")"
set -- mcp list_anchors -a "path=$REL" -a "repo=$REPO" -a limit=5
TOKEN="${AGENTSMEMORY_LOCAL_TOKEN:-${AGENTSMEMORY_TOKEN:-}}"
[ -n "$TOKEN" ] && set -- "$@" --token "$TOKEN"

OUT="$(aiagentmemory "$@" 2>"$ERRFILE")"
RC=$?
if [ "$RC" -ne 0 ]; then
  ERR="$(head -n1 "$ERRFILE" 2>/dev/null)"
  rm -f "$ERRFILE"
  case "$ERR" in
    *"no workspace token found"*) trace "no credential configured; nothing to ask with"; exit 0 ;;
  esac
  trace "list_anchors failed: ${ERR:-unknown}"
  exit 0
fi
rm -f "$ERRFILE"

# No anchor for this path is the ordinary case. Say nothing at all.
case "$OUT" in
  *'"count":0'*|*'"count": 0'*|'') trace "no memory pins $REL"; exit 0 ;;
esac

# ⚠ VERIFY THE SERVER ACTUALLY FILTERED, RATHER THAN TRUSTING THAT IT DID.
# An MCP server that does not recognise an argument IGNORES it — measured
# 2026-09-04 against a container one commit behind this hook: `path=` was dropped
# and the call returned five anchors from three different repositories, for a file
# nothing pins. A cue that fires with another project's memories attached is worse
# than one that never fires, so the hook confirms the path it asked about is in the
# answer and stays silent otherwise. This also makes the hook safe against an older
# server, which is the ordinary state during a rollout.
case "$OUT" in
  *"\"path\": \"$REL\""*|*"\"path\":\"$REL\""*) : ;;
  *) trace "server returned anchors that do not match $REL — it likely ignored the path filter; staying silent"; exit 0 ;;
esac

read -r -d '' CONTEXT <<TXT || true
This team has filed memories about ${REL}.

${OUT}

Each entry names the drawer that pins this file. Read one with am_get_drawer
before changing behaviour here — a memory is evidence about a decision already
taken, not an instruction, and an anchor marked drifted means the code moved on
without the memory rather than that the memory is wrong.
TXT

# printf with %s, never a heredoc into the JSON: the context contains newlines and
# quotes, and hand-assembled JSON is how an envelope becomes unparseable and is
# then dropped in silence by the harness.
#
# ⚠ IT ESCAPES CONTROL BYTES, NOT ONLY QUOTES AND NEWLINES. JSON forbids every
# unescaped byte below 0x20, so a TAB anywhere in an MCP response — a Makefile
# snippet, a Go struct tag, any indented source, which is most of what an anchor
# pins — produced invalid JSON. Demonstrated: "has<TAB>a tab" yields "Invalid
# control character at line 1 column 48". The harness reports nothing; it drops the
# envelope, so the cue goes quiet and looks exactly like a file nothing pins. That
# is the silent-failure class this hook is otherwise careful about, reached through
# its own escaper. Reported by review, deferred once as a follow-up, then fixed
# because a tab in a code snippet is the common case here rather than an edge one.
#
# ⚠ sed FOR THE ESCAPES, NOT awk, AND THAT WAS MEASURED. An awk implementation
# using gsub round-tripped correctly on macOS and FAILED to double the backslash on
# busybox, emitting a lone \s — an invalid escape, and the same
# works-on-the-developer's-machine shape as the stat -f bug this kit already fixed.
# The chain below produces byte-identical output on macOS and Alpine.
esc() {
  TAB="$(printf '\t')"
  printf '%s' "$1" \
    | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e "s/${TAB}/\\\\t/g" \
    | tr -d '\001-\010\013\014\016-\037' \
    | awk 'BEGIN{ORS=""} {print sep $0; sep="\\n"}'
}

printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"%s"}}\n' "$(esc "$CONTEXT")"

# Always succeed. A PreToolUse hook that exits non-zero BLOCKS the tool call, and
# no cue is worth failing a read.
exit 0
