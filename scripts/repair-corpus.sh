#!/usr/bin/env bash
# repair-corpus.sh — apply a SQL repair to the running palace's database, safely.
#
# This exists because `doctor --corpus` reports damage that nothing can repair.
# The checker is read-only by decision — TestTheReadOnlyPathMintsNothing exists so
# a checker never reports on a palace it has just changed — and no MCP tool clears
# or repoints a source_drawer_id. So the only route from "16 facts name no row" to
# a clean corpus is SQL against the container's database, and doing that by hand
# went wrong three separate ways in one afternoon on 2026-09-02:
#
#   1. WAL MODE. /data holds agentsmemory.db plus -wal and -shm. Copying the main
#      file alone omits the log — 4 MB of the most recent writes, that day — and
#      sqlite3 then refuses to open it read-only because it cannot replay a WAL
#      that is not there. A count taken from such a copy is silently short.
#   2. `docker exec` CANNOT RUN IN A STOPPED CONTAINER. A cleanup step written
#      that way fails, and a `|| docker run -v <guessed-volume>` fallback is worse:
#      Docker CREATES a volume that does not exist, mounts it empty, removes
#      nothing and exits 0. Both failures are silent, the stale -wal is replayed
#      over the changed main file, and the server crash-loops on "database disk
#      image is malformed". That is how this script's ancestor corrupted a palace.
#   3. `docker cp` WRITES AS THE HOST USER. The file lands owned by the host uid
#      while the server runs as another, and the next start fails with "attempt to
#      write a readonly database (8)" on a file that is perfectly intact.
#
# So: the volume is DISCOVERED rather than guessed, ownership is taken from the
# directory the container itself wrote, the sidecars are removed and the removal is
# VERIFIED, and nothing is copied back until the repaired copy passes its checks.
# The palace is restarted on every failure path — a stopped container is a worse
# outcome than an unrepaired one.
#
# Usage: scripts/repair-corpus.sh <repair.sql> [--apply|--dry-run]
#   Dry by DEFAULT: with no second argument it does everything except copy back,
#   and leaves the modified copy for inspection. Pass --apply to copy back. The
#   dry pass is the only way to see what the SQL does before the palace sees it.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.full.yml)
SVC=agentsmemory
CONTAINER=agentsmemory-agentsmemory-1
DB=/data/agentsmemory.db
DBNAME="$(basename "$DB")"

# ⚠ DRY BY DEFAULT, DESTRUCTIVE ON PURPOSE. The default used to be the copy-back,
# with safety opt-in via --dry-run — so the whole class of "somebody omitted a flag
# they meant to pass" ended in a mutated database. For a script whose ancestor
# corrupted a palace that is the wrong way round, and it made the tool's default
# disagree with its own instructions, which already say to run the dry pass first.
# Raised in review of this PR. The real run now costs one deliberate word.
SQL="${1:-}"
DRY=1
case "${2:-}" in
  --apply)   DRY=0 ;;
  --dry-run|"") DRY=1 ;;
  *) echo "unknown option: ${2}" >&2; echo "usage: $0 <repair.sql> [--apply|--dry-run]" >&2; exit 2 ;;
esac
case "$SQL" in --*) echo "usage: $0 <repair.sql> [--apply|--dry-run]" >&2; exit 2 ;; esac

say() { printf '\n=== %s\n' "$*"; }
die() { printf '\nFAILED: %s\n' "$*" >&2; exit 1; }

[ -n "$SQL" ] || die "usage: $0 <repair.sql> [--apply|--dry-run]"
[ -f "$SQL" ] || die "no such SQL file: $SQL"
command -v sqlite3 >/dev/null || die "sqlite3 is not on PATH"
docker inspect "$CONTAINER" >/dev/null 2>&1 || die "container $CONTAINER not found"

TS="$(date +%Y%m%d%H%M%S)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/repair-corpus-$TS.XXXXXX")"

# The palace must come back up whatever happens below — EXCEPT where starting it is
# the corruption.
#
# ⚠ TWO FLAGS, BECAUSE THEY ANSWER DIFFERENT QUESTIONS. STOPPED says "did I stop it";
# SAFE says "is starting it safe right now". An earlier version had only the first,
# so every `die` inside the copy-back printed "do NOT start the server" and then the
# trap started it anyway — on a repaired main file with the stale -wal still beside
# it, which is traps 1 and 2 from the header, i.e. exactly the corruption this script
# exists to prevent. Found in review of this PR and reproduced against a replica of
# this control flow before being fixed.
#
# ⚠ AND IT RETURNS 0 EXPLICITLY. `[ "$STOPPED" = 1 ] && { … }` as the last command
# makes the function return 1 whenever STOPPED is 0 — which is the SUCCESS path — and
# bash takes the EXIT trap's status as the script's. A completed repair exited 1,
# indistinguishable from a failure to any `&&` chain or CI step. The dry-run cannot
# catch it: --dry-run exits with STOPPED still 1, so the trap ends on the restart's
# `|| true` and the status is 0. The bug lived only on the path the dry-run avoids.
STOPPED=0
SAFE=1
restart_if_stopped() {
  if [ "$STOPPED" = 1 ]; then
    if [ "$SAFE" = 1 ]; then
      say "restarting the palace"
      "${COMPOSE[@]}" up -d "$SVC" || true
    else
      printf '\n⚠ NOT restarting: the volume is mid-repair and starting now would replay\n'
      printf '  a stale WAL over a changed database. Restore the backup first:\n'
      printf '    %s\n' "$WORK/db.before"
      printf '  then start with: %s up -d %s\n' "${COMPOSE[*]}" "$SVC"
    fi
  fi
  return 0
}
trap restart_if_stopped EXIT

# Discovered ONCE, above the first use, and guarded. It was resolved inline in step 2
# and again in step 6, and only the second was checked — so a failure in the first
# reached a `docker run -v :/data`, which Docker reads as an anonymous volume rather
# than an error. Raised in review of this PR.
VOL="$(docker inspect "$CONTAINER" --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}')"
[ -n "$VOL" ] || die "could not discover the /data volume of $CONTAINER — nothing was touched"

say "1/7  stopping the palace  (data volume: $VOL)"
"${COMPOSE[@]}" stop "$SVC" || die "could not stop $SVC"
STOPPED=1

# docker cp reads a stopped container's filesystem; docker exec does not run in one.
say "2/7  copying the database out, sidecars included"
docker cp "$CONTAINER:$DB" "$WORK/db" || die "docker cp out failed"

# ⚠ ASK WHICH SIDECARS EXIST, THEN REQUIRE THOSE COPIES TO SUCCEED. `docker cp … ||
# true` gives one outcome for two very different states: a sidecar that is genuinely
# absent (fine — a cleanly checkpointed database has none) and one that exists but
# failed to copy (trap 1). The second is silent and its consequences are not: the
# checkpoint below folds in a log that is not there, BEFORE is counted on short data,
# and the copy-back drops every write the WAL held. Raised in review of this PR.
#
# ⚠ `|| true` ON THE LISTING, WHICH IS NOT THE SAME `|| true` THIS PARAGRAPH REJECTS.
# `ls` exits non-zero when an operand is missing, and under `set -euo pipefail` that
# aborts the whole script from inside a command substitution. Measured against
# BusyBox `ls` in alpine, which is what actually runs here: NEITHER sidecar exits 1,
# ONE sidecar exits 1, both exit 0 — so a cleanly checkpointed database, which is
# precisely what a successful repair leaves behind, made the SECOND run abort at
# step 2. Letting the listing come back empty is safe because the guard that matters
# is the loop below: for each sidecar found, the copy must succeed.
PRESENT="$(docker run --rm -v "$VOL:/data" alpine \
  sh -c "ls /data/$DBNAME-wal /data/$DBNAME-shm 2>/dev/null || true" | sed "s|/data/$DBNAME||" | tr '\n' ' ')"
for ext in $PRESENT; do
  docker cp "$CONTAINER:$DB$ext" "$WORK/db$ext" \
    || die "$DBNAME$ext exists in the volume but could not be copied — a checkpoint without it would silently drop the writes it holds"
done
echo "     sidecars present: ${PRESENT:-none}"

say "3/7  backing up, then folding the log into the file"
cp "$WORK/db" "$WORK/db.before"
for ext in -wal -shm; do [ -f "$WORK/db$ext" ] && cp "$WORK/db$ext" "$WORK/db.before$ext"; done
sqlite3 "$WORK/db" "PRAGMA journal_mode=DELETE;" >/dev/null || die "could not checkpoint the WAL"
echo "     backup: $WORK/db.before"

orphans() { sqlite3 -readonly "$1" \
  "SELECT COUNT(*) FROM kg_triples t WHERE t.source_drawer_id!='' \
     AND NOT EXISTS(SELECT 1 FROM drawers d WHERE d.id=t.source_drawer_id);"; }
BEFORE="$(orphans "$WORK/db")"
echo "     dangling fact provenance before: $BEFORE"

say "4/7  applying $SQL"
sqlite3 "$WORK/db" < "$SQL" || die "the SQL did not apply — nothing was copied back"

say "5/7  verifying the modified copy"
AFTER="$(orphans "$WORK/db")"
echo "     dangling fact provenance after:  $AFTER"
sqlite3 -readonly "$WORK/db" "PRAGMA integrity_check;" | head -1 | grep -qx ok \
  || die "integrity_check did not return ok — refusing to copy back"
# A repair that leaves MORE damage than it found is a repair going backwards. Equal
# is allowed: a SQL file may be fixing something this counter does not measure.
[ "$AFTER" -le "$BEFORE" ] || die "dangling references rose from $BEFORE to $AFTER — refusing to copy back"

[ "$DRY" = 1 ] && { say "dry run (default) — NOT copying back. Pass --apply to commit it.\n     Modified copy: $WORK/db"; exit 0; }

say "6/7  copying back, clearing sidecars, restoring ownership"
# From here until the sidecars are gone and ownership is restored, the volume holds a
# NEW main file beside an OLD log. Starting the server in that window is the
# corruption, so the trap must not.
SAFE=0
docker cp "$WORK/db" "$CONTAINER:$DB" || die "docker cp back failed — backup is $WORK/db.before"
OWNER="$(docker run --rm -v "$VOL:/data" alpine sh -c 'stat -c "%u:%g" /data')"
[ -n "$OWNER" ] || die "could not read /data ownership — do NOT start the server"
docker run --rm -v "$VOL:/data" alpine sh -c \
  "rm -f /data/$DBNAME-wal /data/$DBNAME-shm && chown $OWNER /data/$DBNAME" \
  || die "could not clear sidecars / fix ownership — do NOT start; restore $WORK/db.before"
LEFT="$(docker run --rm -v "$VOL:/data" alpine sh -c "ls /data/$DBNAME-wal /data/$DBNAME-shm 2>/dev/null | wc -l" | tr -d ' ')"
[ "$LEFT" = "0" ] || die "sidecars survived removal — do NOT start; restore $WORK/db.before"
echo "     volume $VOL, owner $OWNER, sidecars cleared"
SAFE=1   # the window is closed: one complete database, no stale log, right owner

say "7/7  restarting and re-checking"
STOPPED=0
"${COMPOSE[@]}" up -d "$SVC" || die "could not restart — the repaired db IS in place; start it by hand"
for _ in $(seq 1 30); do docker exec "$CONTAINER" true >/dev/null 2>&1 && break; sleep 2; done

# ⚠ NO PIPE. Reading ${PIPESTATUS[0]} after a `|| true` reads the wrong pipeline —
# an earlier version printed "corpus exits 0" over a container that was crash-looping
# and had refused to run the command at all.
set +e
docker exec "$CONTAINER" agentsmemory doctor --corpus --db "$DB" > "$WORK/corpus.out" 2>&1
RC=$?
set -e
grep -vE '^[0-9]{4}/' "$WORK/corpus.out" || true

printf '\n'
[ "$RC" = 0 ] && echo "DONE — doctor --corpus exits 0." \
              || echo "DONE — repair applied; doctor --corpus still exits $RC. Read the report above."
echo "Pre-repair backup (main file + sidecars): $WORK/db.before*"
