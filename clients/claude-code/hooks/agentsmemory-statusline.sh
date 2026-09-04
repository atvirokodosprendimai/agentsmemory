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

# Per-user, matching the writer: one shared file across accounts is a
# predictable-path problem wearing a different hat.
CACHE="${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}/agentsmemory-status-$(id -u 2>/dev/null || echo 0).txt"

# No cache is the ordinary state before the first session-start hook has run. Say
# NOTHING: an error string in a status line is permanent noise, and a status line
# is the one surface a user cannot dismiss.
[ -s "$CACHE" ] || exit 0

# ⚠ PARSED AS DATA, NEVER SOURCED. `. "$CACHE"` executed the file, and the wing in
# it comes from a repository's own .aiagentmemory — so any checkout could put
# command substitution in a wing name and have it run on EVERY status-line render,
# in the user's shell, forever. Demonstrated 2026-09-04 with a wing of
# `$(touch /tmp/PWNED && echo owned)`: the file appeared. Reported by review.
#
# Fixed keys, read one at a time, and every value constrained to what a wing or a
# count can legitimately be. A status line is the last place to run untrusted
# input: it renders constantly, unattended, and nobody is reading its source.
read_key() {
  sed -n "s/^$1=\(.*\)$/\1/p" "$CACHE" 2>/dev/null | head -n1
}
AM_WING="$(read_key AM_WING)"
AM_DRIFTED="$(read_key AM_DRIFTED)"
AM_INBOX="$(read_key AM_INBOX)"

# A wing is a safe name (palace.SanitizeName's alphabet). Anything else is not a
# wing, and rendering it would be rendering whatever wrote the cache.
case "$AM_WING" in
  ''|*[!A-Za-z0-9_-]*) AM_WING="" ;;
esac
case "$AM_DRIFTED" in ''|*[!0-9]*) AM_DRIFTED=0 ;; esac
case "$AM_INBOX" in ''|*[!0-9]*) AM_INBOX=0 ;; esac

OUT="🧠 ${AM_WING:-no wing}"
[ "$AM_DRIFTED" -gt 0 ] 2>/dev/null && OUT="$OUT · ⚠ ${AM_DRIFTED} drifted"
[ "$AM_INBOX" -gt 0 ] 2>/dev/null && OUT="$OUT · 📥 ${AM_INBOX}"

# Age, in whole minutes, from the cache's own mtime. Nothing is stored about when
# it was written: a stored timestamp is one more thing that can disagree with the
# file it describes.
#
# ⚠ `-c %Y` FIRST, AND THE RESULT IS CHECKED FOR BEING A NUMBER. This is a real
# portability bug that macOS testing cannot see, found by the Linux suite:
# GNU/busybox `stat -c %Y` gives the mtime, while `-f` means FILESYSTEM there and
# prints a block containing "File:". Trying `-f %m` first therefore SUCCEEDED on
# Alpine with prose, which then reached $(( )) — and arithmetic expansion resolves
# a bare word as a variable name, so under `set -u` the status line died with
# "File: unbound variable" and exited 1 on every render. BSD/macOS is the opposite
# spelling, hence both, hence the numeric guard: a status line must never fail,
# and "it printed something non-numeric" is not a state worth reasoning about.
NOW=$(date +%s 2>/dev/null || echo 0)
MTIME=$(stat -c %Y "$CACHE" 2>/dev/null || stat -f %m "$CACHE" 2>/dev/null || echo "")
case "$MTIME" in
  ''|*[!0-9]*) MTIME="$NOW" ;;
esac
case "$NOW" in
  ''|*[!0-9]*) NOW=0; MTIME=0 ;;
esac
AGE=$(( (NOW - MTIME) / 60 ))
[ "$AGE" -gt 0 ] 2>/dev/null && OUT="$OUT · ${AGE}m ago"

printf '%s\n' "$OUT"
exit 0
