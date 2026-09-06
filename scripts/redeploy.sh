#!/usr/bin/env bash
# redeploy.sh — build, restart, and PROVE the running server carries the change.
#
# This exists because the server ran a 17-hour-old binary through an entire day of
# work, and nothing noticed. A build's success is a claim about the build; the only
# evidence that a change is live is reading the artifact that is serving.
#
# Usage: scripts/redeploy.sh [needle ...]
#   Each needle is a string the NEW binary must contain — one per change, so an
#   absent one names which change is missing. With no arguments it checks a
#   standing set plus a control.
set -euo pipefail
cd "$(dirname "$0")/.."

SVC=agentsmemory
# The DEFAULT project's container, used only to read the chain label before
# anything is deployed. After `up -d` it is re-resolved from the project the
# chain actually selected (see there) — a chain can name a differently named
# project, and a hardcoded container would then read one this run never touched.
CONTAINER=agentsmemory-agentsmemory-1
BIN=/usr/local/bin/agentsmemory

# The compose chain is READ FROM THE RUNNING PROJECT, not declared here. The
# repository ships six compose files and the documented local setup uses four;
# a hardcoded two-file chain recreated the service from the SHORTER one and
# silently reverted the overlay's decisions (RERANK_URL, issue #209), because
# `up -d` recreates whenever the resolved config hash changes. Precedence:
#   1. COMPOSE_FILE — Compose's own mechanism. Explicit -f flags override it,
#      which is why it could not steer the old script at all;
#   2. the config_files label on the running container, by BASENAME, so the
#      overlay SET follows the stack that is up while the files come from THIS
#      checkout — the label's absolute paths point at whatever directory the
#      stack was last brought up from, often a clone that no longer exists;
#   3. the two-file default, for a first deploy with nothing running.
# A basename the label names and this checkout lacks is a refusal, not a
# fallback: deploying a different stack silently is the defect being removed.
chain="${COMPOSE_FILE:-}"
chain_from="COMPOSE_FILE"
# The separator belongs to the SOURCE, and conflating them refused every Windows
# redeploy (issue #328). Compose's label joins with ',' and its COMPOSE_FILE uses
# the platform's path separator, so a single IFS=':,' split cut 'C:\...\
# docker-compose.yml' at the drive letter and the guard went looking for a file
# named 'C'. Splitting by source needs no platform detection and no special case
# for a path that happens to contain a colon.
chain_sep=':'
case "$chain" in *';'*) chain_sep=';' ;; esac
if [ -z "$chain" ]; then
  chain="$(docker inspect "$CONTAINER" --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' 2>/dev/null || true)"
  chain_from="the running $CONTAINER"
  chain_sep=','
fi
if [ -z "$chain" ]; then
  chain="docker-compose.yml:docker-compose.full.yml"
  chain_from="the default: no such container and COMPOSE_FILE is unset"
  chain_sep=':'
fi
COMPOSE=(docker compose)
chain_names=""
# Split on the separator this chain's SOURCE uses. The ${arr[@]+"${arr[@]}"}
# form is the bash 3.2 idiom for "expand an array that may be empty" — a bare
# "${arr[@]}" on an empty array is an unbound-variable death under set -u, the
# trap AUTH_HEADER below already records. Blank parts (adjacent separators,
# a trailing one) are skipped rather than resolved to a blank filename.
IFS="$chain_sep" read -r -a chain_parts <<< "$chain"
for f in ${chain_parts[@]+"${chain_parts[@]}"}; do
  [ -n "$f" ] || continue
  name="$(basename "$f")"
  [ -f "$name" ] || { echo "compose file $name (from $chain_from) is not in this checkout — refusing to deploy a different stack"; exit 1; }
  COMPOSE+=(-f "$name")
  chain_names="$chain_names $name"
done
echo "==> compose chain:$chain_names  (from $chain_from)"

# A needle that MUST be present whatever changed. Without it, "absent" cannot be
# told apart from "the grep is looking in the wrong place", which is how a wrong
# path once reported every change as missing.
CONTROL=am_search

needles=("$@")
if [ ${#needles[@]} -eq 0 ]; then
  needles=("ranking: " "chunks_matched" "reranked" "lex-norm" "BEST over " "case_set_id")
fi

# The version is compiled in, so a build without it is a DIFFERENT artifact that
# reports `dev` with every check below green — the container is healthy, the
# digest matches, the needles are present — and the one comparison that tells a
# stale server from a current one (checkout against am_status's version) is
# gone. Refused BEFORE the suite runs rather than detected after it: a dev
# server detected after `up -d` is a dev server already serving (issue #210).
case "${AGENTSMEMORY_VERSION:-}" in
  ""|dev|dev-*)
    echo "AGENTSMEMORY_VERSION is '${AGENTSMEMORY_VERSION:-}': the image would report version 'dev'."
    echo "    Stamp it:  AGENTSMEMORY_VERSION=\$(git describe --tags) scripts/redeploy.sh"
    exit 1 ;;
esac

echo "==> tests must pass before anything is built"
docker run --rm -v "$PWD":/src \
  -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod \
  -w /src golang:1.26-alpine sh -c '
    # The hook tests execute the SHIPPED shell hooks, whose shebang is bash. The
    # base image has only ash, and those tests FAIL LOUDLY without it rather than
    # skipping — which is why this line exists: the first deploy after they landed
    # was correctly refused, instead of shipping over a suite that had not run.
    # git joins it for the same reason: internal/contractaxis drives a real
    # repository (git init, commit, worktree) to prove a mutation actually
    # applied, and alpine ships no git — so those 15 tests failed on the
    # environment, not the code, and the gate refused every deploy for a day.
    apk add --no-cache bash git >/dev/null 2>&1 || true
    gofmt -l cmd internal | grep -q . && { echo "gofmt dirty"; exit 1; }
    go vet ./... || exit 1
    # The reason a red suite is red must reach the operator. This line used to
    # end in >/dev/null 2>&1, so a failing gate printed its banner and nothing
    # else: the script whose whole purpose is proof was hiding the proof.
    go test ./... -count=1 >/tmp/suite.log 2>&1 || {
      echo "--- suite RED ---"
      grep -E "^(--- FAIL|FAIL|panic:|.*\[build failed\])" /tmp/suite.log | head -40
      exit 1
    }
  '
echo "    suite green"

echo "==> build"
"${COMPOSE[@]}" build "$SVC" >/dev/null
echo "==> restart"
"${COMPOSE[@]}" up -d "$SVC" >/dev/null
# Re-resolve the container from the project the chain SELECTED. docker-compose.yml
# sets `name: agentsmemory` and docker-compose.prod.yml sets `name:
# agentsmemory-hosted`, and the last name: in a chain wins — so a chain that
# includes the hosted overlay brings up a different project, and every `docker
# exec` below on the hardcoded name would read a container this run never
# touched, consistently enough to print "deployed and verified" over it.
CONTAINER="$("${COMPOSE[@]}" ps -q "$SVC" 2>/dev/null | head -n1)"
[ -n "$CONTAINER" ] || { echo "    no container for service $SVC in the project the chain selected"; exit 1; }

# The host port and the local token are configurable, and this script probed
# 8080 with no Authorization header whatever they were set to: a server on
# another port looked dead, and a server with AGENTSMEMORY_LOCAL_TOKEN set
# answered 401 and was reported as "did not answer".
PORT="${AGENTSMEMORY_HOST_PORT:-8080}"
BASE="http://localhost:${PORT}"
# Written as a single string rather than an array: macOS ships bash 3.2, where
# "${ARR[@]}" on an EMPTY array is an unbound-variable error under `set -u`, and
# this script died at the smoke step the first time it ran with no token set.
AUTH_HEADER=""
if [ -n "${AGENTSMEMORY_LOCAL_TOKEN:-}" ]; then
  AUTH_HEADER="Authorization: Bearer ${AGENTSMEMORY_LOCAL_TOKEN}"
fi

echo "==> wait for health"
for _ in $(seq 1 60); do
  curl -fsS -m 2 "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS -m 5 "$BASE/healthz" >/dev/null || { echo "    server did not come back on $BASE"; exit 1; }

echo "==> version: the running server must name the stamp it was built with"
# Read from the ARTIFACT (`agentsmemory --version` inside the container), not
# from the build args this script was given. REDEPLOY_WANT_VERSION drives the
# comparison to fail without deploying a different build, for the reason
# REDEPLOY_IMAGE exists below: a gate nobody can make fail is not a gate.
want_ver="${REDEPLOY_WANT_VERSION:-$AGENTSMEMORY_VERSION}"
# `|| true`, as the digest read below has: under `set -e -o pipefail` a failing
# `docker exec` otherwise kills the script at this assignment and the
# <unreadable> diagnostic two lines down never prints.
served_ver="$(docker exec "$CONTAINER" "$BIN" --version 2>/dev/null | sed -n 's/^agentsmemory version //p' || true)"
# The dev arm comes FIRST: with the exact-match arm first, a stamp of literally
# `dev` matched itself and printed "served dev" as a pass.
case "$served_ver" in
  ""|dev|dev-*)
    echo "    served version is '${served_ver:-<unreadable>}': the stamp did not reach the binary"
    exit 1 ;;
  "$want_ver") printf "    served %s\n" "$served_ver" ;;
  *)
    printf "    MISMATCH  served=%s  stamped=%s\n" "$served_ver" "$want_ver"
    echo "    the container is not running the build this script stamped"
    exit 1 ;;
esac

echo "==> read the ARTIFACT that is serving, not the build log"
if ! docker exec "$CONTAINER" grep -ac -- "$CONTROL" "$BIN" >/dev/null 2>&1; then
  echo "    control needle '$CONTROL' not found — the grep is wrong, not the build. Refusing to report."
  exit 1
fi
missing=0
for n in "${needles[@]}"; do
  if docker exec "$CONTAINER" grep -ac -- "$n" "$BIN" >/dev/null 2>&1; then
    printf "    present  %s\n" "$n"
  else
    printf "    MISSING  %s\n" "$n"
    missing=1
  fi
done
[ "$missing" -eq 0 ] || { echo "    a change is not in the running binary"; exit 1; }

# Needles only prove a change that introduces a STRING. A pure-code change — a
# switch rewritten, a constant derived instead of declared — adds no literal, so
# no needle can distinguish it and "all present" would be a true statement about
# nothing. Comparing the running binary against a fresh build of the image is what
# covers those. It must be IMAGE-to-IMAGE: a host `go build` of the same source
# produces a different digest (the docker context excludes .git, and the two do
# not share a layer cache), so that comparison reports a false mismatch.
echo "==> digest: the running binary against the image just built"
# The image name comes from the CONTAINER, not from `compose config --images`,
# which lists every service's image and would depend on ordering.
# REDEPLOY_IMAGE is an override so this comparison can be DRIVEN — pointing it at
# a different image must make the check fail. A gate nobody can make fail is not a
# gate, and editing the script to test it tests the edit.
image=${REDEPLOY_IMAGE:-$(docker inspect "$CONTAINER" --format '{{.Config.Image}}' 2>/dev/null || true)}
fresh=""
live=$(docker exec "$CONTAINER" sha256sum "$BIN" 2>/dev/null | awk '{print $1}' || true)
if [ -n "$image" ]; then
  fresh=$(docker run --rm --entrypoint sha256sum "$image" "$BIN" 2>/dev/null | awk '{print $1}' || true)
fi
if [ -n "$fresh" ] && [ -n "$live" ]; then
  if [ "$fresh" = "$live" ]; then
    printf "    match %s\n" "$(printf %s "$live" | cut -c1-16)"
  else
    printf "    MISMATCH  image=%s  running=%s\n" \
      "$(printf %s "$fresh" | cut -c1-16)" "$(printf %s "$live" | cut -c1-16)"
    echo "    the container is not running what the image contains"
    exit 1
  fi
else
  # Failing to compare is not a pass. It means the check did not run, and a check
  # that did not run must not look like one that succeeded.
  echo "    could not compare digests: image=${image:-<unknown>} fresh=${fresh:-<unreadable>} running=${live:-<unreadable>}"
  echo "    the check did not run, so this deploy is unverified"
  exit 1
fi

echo "==> what the running server resolved"
# Informational, and NEVER fatal. Under `set -o pipefail` a grep that matches
# nothing returns 1 and kills the deploy — which is what happened the first time
# this ran against a container that had not just restarted, so there were no
start=$(date +%s)
# ⚠ TWO ANSWERS, NEVER CONCATENATED. This was `code=$(curl … || echo 000)`, which
# APPENDS on failure: curl printing a perfectly good 200 and then exiting 23
# (write error) produced `200000`, and the script failed a deploy that had passed
# every other check — measured on Windows, where mingw64 curl needs MSYS path
# conversion ON while the docker steps in this same script need it OFF (issue
# #329). The body is read below, so a write failure is a real finding; it is just
# a different finding from "the endpoint did not answer", and folding them made
# the true one unreportable.
smoke_rc=0
code=$(curl -s -o /tmp/redeploy-smoke.json -w '%{http_code}' -m 60 -X POST "$BASE/mcp" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  ${AUTH_HEADER:+-H "$AUTH_HEADER"} \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"am_search","arguments":{"query":"reachability","limit":3,"snippet_chars":1}}}') || smoke_rc=$?
elapsed=$(( $(date +%s) - start ))
printf "    HTTP %s in %ss\n" "${code:-none}" "$elapsed"
if [ "$smoke_rc" -ne 0 ]; then
  echo "    curl exited $smoke_rc after reporting HTTP ${code:-none}."
  echo "    23 is a WRITE error: the request was answered and the body did not reach"
  echo "    /tmp/redeploy-smoke.json, which the checks below read. Fix the path, not the server."
  exit 1
fi
[ "$code" = "200" ] || { echo "    the endpoint agents call did not answer"; exit 1; }
# HTTP 200 is not a successful search. MCP returns tool FAILURES inside a 200
# JSON-RPC envelope, so a dead embedder answers 200 with an error in the body and
# this script printed "deployed and verified" over it. The body was already
# being saved and never read.
if grep -q '"isError"[[:space:]]*:[[:space:]]*true' /tmp/redeploy-smoke.json ||
   grep -q '"error"[[:space:]]*:[[:space:]]*{' /tmp/redeploy-smoke.json; then
  echo "    the search returned HTTP 200 carrying an MCP error:"
  head -c 400 /tmp/redeploy-smoke.json | sed 's/^/      /'
  exit 1
fi
# The payload is a JSON string INSIDE the envelope, so its quotes arrive escaped.
if ! grep -qE '\\?"count\\?"[[:space:]]*:' /tmp/redeploy-smoke.json; then
  echo "    the search answered 200 with no result payload — this is not a working search:"
  # Bounded, and deliberately short: this body carries real memory text, and a
  # pasted deploy log is a public artifact.
  head -c 200 /tmp/redeploy-smoke.json | sed 's/^/      /'
  exit 1
fi
if [ "$elapsed" -gt 25 ]; then
  echo "    WARNING: ${elapsed}s is beyond what MCP clients have been observed to wait."
  echo "    A search that times out returns nothing, which is worse than a bad ranking."
  exit 1
fi

# The smoke search above just ran through every semantic stage. If it left no
# span, this deploy has no instrument — and an unmeasured deploy is one whose
# claims have to be believed. Asserted on the SEARCH THAT JUST RAN rather than on
# the startup banner, because the banner is only in the window when the container
# actually restarted, and an unchanged image does not restart it. Asserted on the
# trace rather than on the compose file for the same reason the needle check
# above reads the binary: a variable set in a file is a claim about the file.
echo "==> otel: the smoke search must have left a trace"
# --since "$start", NOT a duration: $start is the epoch second captured just
# before the smoke curl above. A window like `--since 120s` asks "did ANY search
# emit a span recently", which a span from a minute-old search satisfies while the
# tracer is dead — docker logs survive a restart, so the stale line is right there.
# That version passed its own mutant for a timing reason rather than a wiring one.
# Polled, not sampled once. The stdout path uses a SimpleSpanProcessor, so there
# is no batch delay — but the tree exporter prints a tree when its ROOT span ends,
# and that is the same instant the HTTP response is written. A single grep the
# millisecond curl returns lost that race and failed a deploy whose tracer was
# demonstrably on.
# 60 half-second attempts, not 20. The first search after a restart runs against a
# cold cross-encoder: measured 10.9s on 2026-08-25, against a 10s poll, and the
# gate failed a deploy whose tracing was demonstrably working. A gate that cries
# wolf on a cold start is one people learn to pass with the skip flag, so the
# bound must exceed the slowest legitimate smoke rather than the typical one.
traced=0
for _ in $(seq 1 60); do
  if docker logs --since "$start" "$CONTAINER" 2>&1 | grep -q "am\.search "; then traced=1; break; fi
  sleep 0.5
done
if [ "$traced" -eq 1 ]; then
  echo "    span tree emitted"
else
  echo "    the smoke search left NO am.search span."
  echo "    AGENTSMEMORY_OTEL_ENDPOINT is unset or the tracer never started, so this"
  echo "    stack cannot tell you which stages ran, which were bypassed, and why."
  exit 1
fi
# ---------------------------------------------------------------------------
# The CLIENT half. Everything above proves the SERVER carries the change; none of
# it says anything about the binary and kit installed on this machine.
#
# This exists for the same reason the rest of the script does. The server once ran
# a 17-hour-old binary through a whole day and nothing noticed; on 2026-08-22 the
# installed CLI was a day-old build, so the Stop hook embedded in it still printed
# a "memories to write" list that had been removed from the source, and `/M` still
# named `mempalace_*` tools that no longer exist. Both were discovered by the
# defect firing, not by a check.
#
# It FAILS rather than warns. A gate whose result is printed and not branched on
# is decoration, and this one has already been ignored once.
# judge_tree LABEL PATH prints one verdict line for a Go binary against this
# checkout and sets kit_stale when it is behind. It reads the ARTIFACT — the
# vcs.revision `go build` stamps inside a checkout — never the binary's
# self-report, and it compares TREES, not revisions: a merge commit gives the
# same tree a new sha, and revision equality reported every kit stale the moment
# a branch merged. One function for every binary the kit check judges, because
# the first version of the Desktop check below used a different oracle
# (doctor's "does the bridge match the PATH copy"), and since the installer
# COPIES the PATH binary into Desktop, agreeing was the normal outcome of the
# very incident it was written to catch. Review of f80e12c found it blind by
# construction.
judge_tree() {
  label="$1"; path="$2"
  # sed, not awk: an unescaped $NF is expanded by the SHELL under `set -u`
  # before awk sees it, and the gate then dies with "NF: unbound variable"
  # instead of reporting staleness — a check that fails for its own reasons.
  have_rev="$(go version -m "$path" 2>/dev/null | sed -n 's/.*vcs\.revision=//p' | head -n1)"
  have_dirty="$(go version -m "$path" 2>/dev/null | sed -n 's/.*vcs\.modified=//p' | head -n1)"
  # git rev-parse on an unknown object fails, and the empty result then falls
  # through to the revision comparison below.
  have_tree="$(git rev-parse "${have_rev}^{tree}" 2>/dev/null || echo "")"
  if [ -n "$have_rev" ] && [ -n "$have_tree" ] && [ "$have_tree" = "$want_tree" ] &&
     [ "$have_rev" != "$want_rev" ] && [ "$have_dirty" != "true" ]; then
    echo "    $label $(printf '%.7s' "$have_rev") (tree identical to $(printf '%.7s' "$want_rev"))  $path"
  elif [ -n "$have_rev" ]; then
    if [ "$have_rev" = "$want_rev" ] && [ "$have_dirty" != "true" ]; then
      echo "    $label $(printf '%.7s' "$have_rev")  $path"
    elif [ "$have_rev" = "$want_rev" ]; then
      echo "    $label STALE: $path built from $(printf '%.7s' "$have_rev") with uncommitted changes"; kit_stale=1
    else
      echo "    $label STALE: $path built from $(printf '%.7s' "$have_rev"), checkout is $(printf '%.7s' "$want_rev")"; kit_stale=1
    fi
  else
    # No Go toolchain here, or a binary carrying no VCS stamp: the artifact
    # cannot be read, so say that rather than guess. Not a failure — a gate that
    # blocks with no way to satisfy it is the bug this block once fixed — but it
    # must never look like a pass, because silence is not success.
    echo "    $label UNVERIFIED: no vcs stamp readable in $path (need go on PATH)"
  fi
}
echo "==> the installed client kit, against this checkout"
kit_stale=0
if command -v aiagentmemory >/dev/null 2>&1; then
  want_rev="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  bin_path="$(command -v aiagentmemory)"
  # Read the ARTIFACT, not its self-report — the same rule the needle check above
  # follows. The old check compared `--version` against the short SHA, so every
  # locally built kit reported STALE forever, including one built from this exact
  # commit by the very command the failure message prescribes. A gate whose own
  # remedy cannot satisfy it is a gate people learn to skip.
  want_tree="$(git rev-parse "${want_rev}^{tree}" 2>/dev/null || echo "")"
  judge_tree "binary " "$bin_path"

  # The path is printed above because the remedy below writes to ONE directory
  # and `command -v` reads whatever wins PATH: on 2026-09-04 ~/.claude/bin
  # preceded ~/.local/bin and held an older copy, so the prescribed `go build`
  # ran, the warning persisted, and the file it named was not the problem
  # (issue #204). A symlink into the remedy directory is the sanctioned shape
  # and is not a shadow, so one hop of readlink is resolved before comparing.
  # The same directory install.sh writes to, overridable the same way; a
  # hardcoded ~/.local/bin warned about a directory holding nothing on a host
  # that installed elsewhere.
  remedy_dir="${AIAGENTMEMORY_BIN_DIR:-$HOME/.local/bin}"
  real_path="$bin_path"
  if [ -L "$bin_path" ]; then
    real_path="$(readlink "$bin_path")"
    case "$real_path" in /*) ;; *) real_path="$(dirname "$bin_path")/$real_path" ;; esac
  fi
  if [ "$(cd "$(dirname "$real_path")" 2>/dev/null && pwd -P)" != "$(cd "$remedy_dir" 2>/dev/null && pwd -P)" ]; then
    echo "    ⚠ PATH resolves aiagentmemory to $bin_path; the Fix below writes to $remedy_dir, which is SHADOWED."
    echo "      Make the shadow a symlink to $remedy_dir/aiagentmemory, or the remedy cannot clear this."
  fi

  # Byte-compare what the installer would lay down against what is there. The
  # binary embeds these, so a stale binary shows up here too — but a kit that
  # was never re-installed after a fresh binary shows up ONLY here.
  # This list is hand-maintained and has already drifted once: the SubagentStart
  # hook shipped without being added, so the one artifact the kit had just gained
  # was the one artifact this gate could not see. TestRedeployKitCheckCoversEveryInstalledArtifact
  # now fails when a hook, command, or agent definition is added to the kit and
  # not to this list — a gate maintained by intention is the thing this whole
  # script exists to replace.
  cfg="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
  for pair in \
    "commands/M.md:clients/claude-code/commands/M.md" \
    "commands/am.md:clients/claude-code/commands/am.md" \
    "commands/load-skill.md:clients/claude-code/commands/load-skill.md" \
    "agentsmemory-bootstrap.md:clients/claude-code/bootstrap.md" \
    "agentsmemory-stop-hook.sh:clients/claude-code/hooks/agentsmemory-stop-hook.sh" \
    "agentsmemory-verify-hook.sh:clients/claude-code/hooks/agentsmemory-verify-hook.sh" \
    "agentsmemory-session-end-hook.sh:clients/claude-code/hooks/agentsmemory-session-end-hook.sh" \
    "agentsmemory-stats.sh:clients/claude-code/hooks/agentsmemory-stats.sh" \
    "agentsmemory-subagent-start-hook.sh:clients/claude-code/hooks/agentsmemory-subagent-start-hook.sh" \
    "agents/agentsmemory-researcher.md:clients/claude-code/agents/agentsmemory-researcher.md" \
    "agents/agentsmemory-researcher.toml:clients/claude-code/agents/agentsmemory-researcher.toml"; do
    inst="$cfg/${pair%%:*}"; src="${pair##*:}"
    [ -f "$inst" ] || continue           # not installed is not stale
    if ! diff -q "$inst" "$src" >/dev/null 2>&1; then
      echo "    kit     STALE: ${pair%%:*}"
      kit_stale=1
    fi
  done

  # The Claude Desktop bridge is a THIRD binary at a THIRD path — a copy the
  # installer places in Desktop's own config dir and registers as `mcp-stdio` —
  # and nothing above reads it. On 2026-09-04 the server was current at
  # v0.0.113 while Desktop spawned a build from before the release, because
  # this loop rebuilt two host binaries and never that one. Judged by the SAME
  # tree comparison as the CLI, against THIS checkout: `doctor --agent
  # claude-desktop` was tried first and compares the bridge with the PATH copy
  # it was installed from, which agrees exactly when both are stale together —
  # the documented order rebuilds the host binaries AFTER this script runs. Only
  # when a Desktop config registers a bridge; a machine without Desktop, or one
  # registered by URL, has nothing to judge.
  case "$(uname -s)" in
    Darwin) desktop_cfg="$HOME/Library/Application Support/Claude/claude_desktop_config.json" ;;
    MINGW*|MSYS*|CYGWIN*) desktop_cfg="${APPDATA:-}/Claude/claude_desktop_config.json" ;;
    *) desktop_cfg="${XDG_CONFIG_HOME:-$HOME/.config}/Claude/claude_desktop_config.json" ;;
  esac
  if [ -f "$desktop_cfg" ]; then
    # The registered command, JSON-unescaped: Windows paths arrive as C:\\Users.
    bridge="$(sed -n 's/.*"command": *"\([^"]*aiagentmemory-server[^"]*\)".*/\1/p' "$desktop_cfg" | head -n1 | sed 's/\\\\/\\/g')"
    if [ -n "$bridge" ]; then
      if [ -f "$bridge" ]; then
        judge_tree "desktop" "$bridge"
      else
        echo "    desktop MISSING: the registration names $bridge and nothing is there"; kit_stale=1
      fi
    fi
  fi
else
  echo "    (aiagentmemory not on PATH — nothing installed to check)"
fi
if [ "$kit_stale" -ne 0 ]; then
  echo
  echo "    The server is current and the client is not. That gap is invisible until"
  echo "    something embedded in the old kit misbehaves, which is how it was found."
  echo "    Fix:  go build -o $remedy_dir/aiagentmemory ./clients/claude-code"
  echo "          go build -o $remedy_dir/aiagentmemory-server ./cmd/server   # the Desktop bridge is copied from this"
  echo "          aiagentmemory install --agent claude --local --yes                 # with the same --wing/--scope as before"
  echo "          aiagentmemory install --agent claude-desktop --local --yes         # quit Claude Desktop first"
  echo "    Skip: REDEPLOY_SKIP_KIT_CHECK=1 scripts/redeploy.sh"
  [ "${REDEPLOY_SKIP_KIT_CHECK:-0}" = "1" ] || exit 1
fi

echo "==> deployed and verified"
