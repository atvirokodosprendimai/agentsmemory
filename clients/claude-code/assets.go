// Command aiagentmemory is the single-binary installer and Claude Code wrapper
// for agentsmemory. It replaces the old clients/claude-code/install.sh: it
// embeds the slash-command files and the Stop hook, installs them into a Claude
// config directory, registers the Stop hook and the agentsmemory MCP endpoint,
// and can optionally pull in the recommended companion extensions
// (codebase-memory MCP plus the eidos and codex plugins).
//
// It supports two installation modes:
//
//   - Global   — `aiagentmemory install` wires our MCP + commands + hook into
//     the global ~/.claude, wrapping your existing Claude client.
//   - Isolated — `aiagentmemory install --sandbox <name>` installs a
//     self-contained config under ~/.sandboxes/<name>. Launch Claude against it
//     with `aiagentmemory run <name>`, which pins CLAUDE_CONFIG_DIR to that
//     sandbox so its commands, settings, MCP servers, and token stay isolated.
package main

import "embed"

// assets holds the command markdown and the Stop-hook script compiled into the
// binary with go:embed. Shipping them inside the executable is the whole point
// of replacing install.sh with a single downloadable binary — the installer
// needs nothing on disk beside it.
//
// Note the deliberate omission of the legacy commands/agentsmemory.md: it was
// retired in favour of the thin /am command, so only M.md and am.md ship.
//
//go:embed commands/M.md commands/am.md hooks/agentsmemory-stop-hook.sh
var assets embed.FS
