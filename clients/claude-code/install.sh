#!/usr/bin/env bash
# Install the agentsmemory Claude Code kit:
#   1. the /agentsmemory startup command -> <claude>/commands/agentsmemory.md
#   2. the Stop persistence hook         -> <claude>/hooks/agentsmemory-stop-hook.sh
#   3. registers the Stop hook in        -> <claude>/settings.json (jq, with backup)
#
# Usage:   ./install.sh            # installs into ~/.claude
#          CLAUDE_DIR=/path ./install.sh
#
# Idempotent: re-running overwrites the command/hook files and never adds a
# duplicate Stop-hook entry. Configure the MCP connection separately (see README).
set -euo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude}"
CMD_DIR="$CLAUDE_DIR/commands"
HOOK_DIR="$CLAUDE_DIR/hooks"
SETTINGS="$CLAUDE_DIR/settings.json"
HOOK_CMD="bash $HOOK_DIR/agentsmemory-stop-hook.sh"

mkdir -p "$CMD_DIR" "$HOOK_DIR"

# 1 + 2: copy the command and the hook script.
cp "$SRC/commands/agentsmemory.md" "$CMD_DIR/agentsmemory.md"
cp "$SRC/hooks/agentsmemory-stop-hook.sh" "$HOOK_DIR/agentsmemory-stop-hook.sh"
chmod +x "$HOOK_DIR/agentsmemory-stop-hook.sh"
echo "installed: $CMD_DIR/agentsmemory.md"
echo "installed: $HOOK_DIR/agentsmemory-stop-hook.sh"

# 3: register the Stop hook in settings.json.
if ! command -v jq >/dev/null 2>&1; then
  cat <<EOF

jq not found — add the Stop hook to $SETTINGS manually:

  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "$HOOK_CMD" } ] }
    ]
  }
EOF
  exit 0
fi

[ -f "$SETTINGS" ] || echo '{}' >"$SETTINGS"
cp "$SETTINGS" "$SETTINGS.bak.$(date +%s 2>/dev/null || echo backup)" 2>/dev/null || true

tmp="$(mktemp)"
# Append our Stop hook only if an identical command is not already registered.
jq --arg cmd "$HOOK_CMD" '
  .hooks //= {} |
  .hooks.Stop //= [] |
  if any(.hooks.Stop[]?; (.hooks[]?.command) == $cmd)
  then .
  else .hooks.Stop += [ { "hooks": [ { "type": "command", "command": $cmd } ] } ]
  end
' "$SETTINGS" >"$tmp" && mv "$tmp" "$SETTINGS"

echo "registered Stop hook in: $SETTINGS"
echo
echo "Done. Restart Claude Code (or /reload), then run /agentsmemory in a project"
echo "where the agentsmemory MCP is connected (see README.md to configure it)."
