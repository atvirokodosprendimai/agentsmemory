// Command aiagentmemory is the single-binary installer and Claude Code wrapper
// for agentsmemory. It replaces the old clients/claude-code/install.sh: it
// embeds the slash-command files and the Stop hook, installs them into a Claude
// config directory, registers the Stop hook and the agentsmemory MCP endpoint,
// and can optionally pull in the recommended companion extensions
// (the codebase-memory MCP plus the codex review plugin).
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
// Note the deliberate omission of the legacy commands/agentsmemory.md and of
// commands/M.md: both were retired in favour of the thin /am command, so only
// am.md and load-skill.md ship. M.md carried a Go- and UI-specific variant of
// the same grounding sequence, which is a second copy of a protocol maintained
// in one place — and the copy nobody maintains is the one that goes false. load-skill.md is the /load-skill nicety over the
// am_load_skill MCP tool — it fetches a team-shared skill and installs it locally.
//
// The SessionStart hook (hooks/agentsmemory-verify-hook.sh) is the other half of
// code anchors: it checks, before a session acts on anything, that the memories
// about this project's code still match the code. Detection that arrives after
// the wrong decision is not detection.
//
// The SessionEnd hook closes the loop the other two open: Stop asks the agent to
// persist, SessionStart checks what is stored against the code, and this reports
// what recall actually did across the whole session — the only one of the three
// that can, because at Stop the session has barely begun.
//
// bootstrap.md is the always-on operating protocol the installer writes into the
// target config dir as agentsmemory-bootstrap.md and imports from CLAUDE.md, so
// the memory-first workflow applies every session without typing /am.
//
// extensions/agentsmemory.ts is the pi bridge: pi ships no MCP client and no hook
// system, so that one extension both re-registers the remote agentsmemory tools
// natively and fires the end-of-turn memory checkpoint.
//
//go:embed commands/am.md commands/load-skill.md hooks/agentsmemory-stop-hook.sh hooks/agentsmemory-verify-hook.sh hooks/agentsmemory-session-end-hook.sh hooks/agentsmemory-stats.sh hooks/agentsmemory-subagent-start-hook.sh hooks/agentsmemory-recall-hook.sh hooks/agentsmemory-task-recall-hook.sh hooks/agentsmemory-anchor-cue-hook.sh hooks/agentsmemory-touched-hook.sh hooks/agentsmemory-precompact-hook.sh hooks/agentsmemory-statusline.sh unattended-settings.json skills/*/SKILL.md agents/*.md agents/*.toml bootstrap.md extensions/agentsmemory.ts
var assets embed.FS

// commandAssets are the slash-command files the kit installs, in the order they
// are written and reported. Both the installer and `update-skill` iterate this
// one list so a command added here reaches every install path at once.
var commandAssets = []string{"am.md", "load-skill.md"}

// skillAssets are the native Agent Skills the kit installs (ADR-051 T8).
//
// Claude Code only: a SKILL.md is discovered by Claude Code's own skill mechanism,
// which codex and pi do not have. The skill POINTS at the centralised catalogue —
// am_list_skills, am_load_skill — and restates none of it, because a second copy
// of a convention is a second thing to get wrong and the copy nobody maintains is
// the one that stays wrong.
var nativeSkillAssets = []string{"recall"}

// retiredCommands are command files earlier versions shipped and this one does
// not. They are REMOVED from a config dir on install, not merely left unshipped.
//
// ⚠ UNSHIPPING IS NOT REMOVING, AND THIS REPOSITORY HAS THE RECEIPT. ADR-041's
// SessionEnd work found that an install which merely stops PLANNING an asset
// leaves every upgraded machine carrying it while the installer's own output
// says otherwise. A stale /M keeps offering a second, diverging grounding
// sequence next to /am — which is exactly the "second copy of a protocol" this
// project keeps recording as the thing that goes wrong.
var retiredCommands = []string{"M.md"}

// agentAssets are the subagent definitions the kit installs, as BASE NAMES: the
// extension comes from the kit, because Claude reads markdown with a `tools:`
// front-matter allowlist and codex reads TOML with `enabled_tools`. Every name
// here must exist in every dialect an installing kit asks for, which
// TestEveryShippedAgentDefinitionExistsInEveryDialect asserts.
//
// The list is explicit rather than a directory walk because assetSource is
// ReadFile-only: `update-skill` fetches the same names over HTTP, where there is
// nothing to walk. TestEveryShippedAgentDefinitionIsInstalled keeps it honest
// against the directory, because "added the file, forgot the list" is the exact
// shape that shipped agentsmemory-researcher.md embedded in the binary and
// written to no disk anywhere.
var agentAssets = []string{"agentsmemory-researcher"}

// assetSource supplies installable assets by their embed-relative name, e.g.
// "commands/M.md" or "bootstrap.md". embed.FS satisfies it already, so the
// embedded kit above is the default implementation.
//
// It exists so `update-skill` can hand the installer a source that fetches the
// same names from GitHub instead of reading them out of the binary. Both
// commands then share one write path — which matters because installing an
// asset is not a plain file copy: the memory protocol is imported on Claude and
// inlined on codex/pi (see Installer.registerMemoryBootstrap), and duplicating
// that split is exactly how the two commands would drift apart.
type assetSource interface {
	// ReadFile returns the contents of the named asset.
	ReadFile(name string) ([]byte, error)
}
