package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Agent identifiers accepted by `install --agent` and `run --agent`.
const (
	agentClaude        = "claude"
	agentCodex         = "codex"
	agentPi            = "pi"
	agentCursor        = "cursor"
	agentClaudeDesktop = "claude-desktop"
	agentBoth          = "both"
	agentAll           = "all"
)

// agentKit describes the parts of an install that differ between the agent CLIs
// we support. Everything structural is the same on both — a config dir holding
// slash-command markdown, an agent memory file, a hook registration, and an
// MCP server list driven through the agent's own CLI — so the differences are
// plain data plus the two registration steps that genuinely diverge (see
// Installer.registerAgentsMemoryMCP and Installer.installRecommended).
//
// Values are verified against Claude Code, codex-cli 0.144.5 and pi 0.84.2: each
// relocates its whole config with one env var (CLAUDE_CONFIG_DIR / CODEX_HOME /
// PI_CODING_AGENT_DIR), each reads top-level markdown in a commands dir as slash
// commands with the same `description:`/`argument-hint:` front matter and
// `$ARGUMENTS` expansion, and each loads an agent memory file from that dir. Only
// Claude takes hooks from settings.json, Codex from config.toml, and pi retired
// hooks in favour of extensions, so its kit carries no hooksFile.
type agentKit struct {
	name        string // agent identifier: claude | codex | pi
	bin         string // default CLI binary name to drive
	configEnv   string // env var that relocates the agent's config dir
	globalDir   string // slash-separated global config dir, relative to $HOME
	commandsDir string // subdir under the config dir that holds slash commands
	memoryFile  string // agent memory file our managed block merges into

	// shipsCompanionHooks reports whether this kit receives the Claude-only
	// companion hooks, of which the injecting ones are the subject of `doctor`.
	// False for an agent that gets the Stop hook and nothing else.
	shipsCompanionHooks bool

	// hooksFile is the file holding the Stop-hook registration, empty for an
	// agent with no hook system. pi is that case: it renamed hooks/ to extensions,
	// so its end-of-turn nudge lives in the extension we install instead.
	hooksFile string

	// agentsDir is the subdir under the config dir that holds subagent
	// definitions, empty for an agent with no subagent system.
	//
	// agentAssetExt is the DIALECT that directory expects, and the two agents
	// that have one do not agree: Claude reads markdown with YAML front matter
	// and a `tools:` allowlist, codex reads TOML with `developer_instructions`
	// and an `enabled_tools` array under an `[mcp_servers.…]` table. The same
	// definition therefore ships twice, once per dialect, rather than once with a
	// converter — the two are different enough that a converter would be a second
	// thing to get wrong, and both are checked into the tree where a human reads
	// them. Verified against codex-cli 0.144.5, whose ~/.codex/agents holds .toml.
	agentsDir     string
	agentAssetExt string

	// rulesFile is where an agent that has no memory file takes its always-on
	// protocol from, relative to the config dir. Cursor is that case: it has no
	// CLAUDE.md/AGENTS.md equivalent and instead loads every rules/*.mdc marked
	// `alwaysApply: true`. Empty for the agents that merge a managed block into a
	// memory file instead — the two are alternatives, never both.
	rulesFile string

	// mcpConfigFile is the JSON file this kit registers its MCP server into when
	// the agent ships no CLI to do it, relative to the config dir. Empty for the
	// agents that have a `mcp add` command — which is most of them, and is why
	// this is a field rather than the default.
	mcpConfigFile string

	// supportsImport reports whether the memory file can pull in a sibling file
	// by reference. Claude Code resolves `@file.md` imports, so it gets a
	// one-line import of the protocol; codex has no import mechanism in
	// AGENTS.md, so there the protocol is inlined into the managed block.
	supportsImport bool

	// commandHint shows the user how the installed commands are invoked. Codex
	// namespaces prompt files under `/prompts:`, Claude does not.
	commandHint string

	// authFiles are the credential files this agent keeps inside its config dir,
	// which `--shared-auth` links back to the global config so one login serves
	// every sandbox. Empty means the agent stores credentials outside the config
	// dir entirely — Claude Code on macOS keeps them in the login Keychain, which
	// is already shared, so there is nothing to link.
	authFiles []string
}

// claudeKit is the Claude Code layout: ~/.claude, commands/, CLAUDE.md + @import,
// hooks registered in settings.json.
var claudeKit = agentKit{
	name:           agentClaude,
	bin:            "claude",
	configEnv:      "CLAUDE_CONFIG_DIR",
	globalDir:      ".claude",
	commandsDir:    "commands",
	memoryFile:     "CLAUDE.md",
	hooksFile:      "settings.json",
	agentsDir:      "agents",
	agentAssetExt:  ".md",
	supportsImport: true,
	commandHint:    "/M",
	// The companion hooks — verify, subagent, session-end and recall — ship to
	// Claude only, for the reason installHooks states: codex exposes the event
	// names but its execution contract was never captured. TWO things read this,
	// which is why it is a field rather than a name comparison: the installer
	// decides what to write, and `doctor` decides whether an empty hooks directory
	// is a finding or the designed state.
	shipsCompanionHooks: true,
	// Claude Code stores its OAuth credentials in the OS keychain on macOS and in
	// .credentials.json elsewhere. Linking the file is a no-op on macOS (it never
	// exists) and the right thing on Linux, so naming it costs nothing.
	authFiles: []string{".credentials.json"},
}

// codexKit is the codex-cli layout: ~/.codex, prompts/, AGENTS.md with the
// protocol inlined, hooks registered in config.toml.
var codexKit = agentKit{
	name:        agentCodex,
	bin:         "codex",
	configEnv:   "CODEX_HOME",
	globalDir:   ".codex",
	commandsDir: "prompts",
	memoryFile:  "AGENTS.md",
	hooksFile:   "config.toml",
	// codex reads ~/.codex/agents/*.toml — a different dialect, same directory
	// name. pi gets neither: it has no subagent system to define agents for.
	agentsDir:     "agents",
	agentAssetExt: ".toml",
	// AGENTS.md has no import directive — codex reads the file itself, so the
	// protocol has to live in the managed block rather than beside it.
	supportsImport: false,
	commandHint:    "/prompts:M",
	authFiles:      []string{"auth.json"},
}

// piKit is the pi-coding-agent layout: ~/.pi/agent, prompts/, AGENTS.md with the
// protocol inlined, and no hooks file at all.
//
// pi is the odd one out in two ways, both verified against pi 0.84.2:
//
//   - Its config dir is two levels deep (~/.pi/agent), so globalDir carries a
//     separator where the others carry a bare basename.
//   - It ships no MCP client — "intentionally does not include built-in MCP"
//     (docs/usage.md) — and no hook system. Both gaps are filled by the extension
//     Installer.registerPiMCP writes into <config dir>/extensions.
var piKit = agentKit{
	name:        agentPi,
	bin:         "pi",
	configEnv:   "PI_CODING_AGENT_DIR",
	globalDir:   ".pi/agent",
	commandsDir: "prompts",
	memoryFile:  "AGENTS.md",
	// pi loads AGENTS.md from its agent dir verbatim; like codex it has no import
	// directive, so the protocol is inlined into the managed block.
	supportsImport: false,
	// pi prompt templates are invoked by bare name — no `/prompts:` namespace.
	commandHint: "/M",
	// models-store.json rides along with auth.json: it holds which provider models
	// have been added, so sharing one without the other leaves a sandbox
	// authenticated for models it does not list.
	authFiles: []string{"auth.json", "models-store.json"},
}

// resolveAgentKits maps the --agent value to the kits to install. Multi-agent
// values return Claude first so a mixed install's output reads in the same order
// as the docs. An unknown name is an error rather than a silent fallback:
// installing into the wrong agent's config dir would be invisible until the user
// wondered why their commands never showed up.
//
// "both" keeps meaning Claude + codex, the pair it shipped with; pi joins under
// the new "all" instead, so an existing `--agent both` script does not silently
// start installing into a third agent.
func resolveAgentKits(name string) ([]agentKit, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", agentClaude:
		return []agentKit{claudeKit}, nil
	case agentCodex:
		return []agentKit{codexKit}, nil
	case agentPi:
		return []agentKit{piKit}, nil
	case agentCursor:
		return []agentKit{cursorKit}, nil
	case agentClaudeDesktop:
		return []agentKit{claudeDesktopKit}, nil
	case agentBoth:
		return []agentKit{claudeKit, codexKit}, nil
	case agentAll:
		return []agentKit{claudeKit, codexKit, piKit, cursorKit, claudeDesktopKit}, nil
	default:
		return nil, fmt.Errorf("unknown --agent %q: use claude, codex, pi, cursor, claude-desktop, both or all", name)
	}
}

// resolveAgentKit maps a single agent name to its kit, rejecting "both" — used by
// `run`, which launches exactly one agent.
func resolveAgentKit(name string) (agentKit, error) {
	kits, err := resolveAgentKits(name)
	if err != nil {
		return agentKit{}, err
	}
	if len(kits) != 1 {
		return agentKit{}, fmt.Errorf("--agent %q selects more than one agent; name just one", name)
	}
	return kits[0], nil
}

// globalConfigDir is the agent's default config dir under home. globalDir is
// written slash-separated so a nested default like pi's ".pi/agent" stays
// readable in the kit; FromSlash turns it into the host's separator.
func (k agentKit) globalConfigDir(home string) string {
	return filepath.Join(home, filepath.FromSlash(k.globalDir))
}

// cursorKit is the Cursor layout, and it is mostly a list of things Cursor does
// not have. Every empty field below is a MEASURED absence on a real install
// (2026-08-22, cursor-agent on the reference machine), not an omission:
//
//   - configEnv: the binary reads CURSOR_API_KEY and CURSOR_INVOKED_AS and no
//     config-dir variable, so ~/.cursor cannot be relocated and --sandbox is
//     refused rather than writing a kit nothing will open.
//   - commandsDir: there is no ~/.cursor/commands.
//   - memoryFile: there is no CLAUDE.md/AGENTS.md equivalent; the protocol goes
//     in rules/agentsmemory.mdc with `alwaysApply: true`.
//   - hooksFile: ~/.cursor/hooks exists and its events, payloads and registration
//     file were never established. Registering against unverified events would
//     ship a branch that may never fire and look complete doing it (ADR-017 T3).
//
// What it does have: agents/ in the SAME markdown dialect Claude reads, and no
// CLI that registers an MCP server — `cursor-agent mcp` offers login, list,
// list-tools, enable and disable, so the registration writes mcp.json directly.
var cursorKit = agentKit{
	name:          agentCursor,
	bin:           "cursor-agent",
	globalDir:     ".cursor",
	agentsDir:     "agents",
	agentAssetExt: ".md",
	rulesFile:     "rules/agentsmemory.mdc",
	mcpConfigFile: "mcp.json",
}

// claudeDesktopKit is the thinnest kit there is: an MCP registration and nothing
// else, because Claude Desktop has nowhere to put anything else.
//
// No commands directory, no memory file, no rules file, no hooks, no agents
// directory, and no variable that relocates its config — every one of those is a
// measured absence on macOS, 2026-08-22. What it has is
// claude_desktop_config.json with an "mcpServers" object, the same shape Cursor
// and Claude Code use.
//
// The entry spawns a LOCAL PROCESS: Desktop's config file speaks to local
// processes, and the product already ships the bridge for that
// (`mcp-stdio --url`), so the Node.js route the project's own windows-guide
// recommends is unnecessary for a self-hosted server.
//
// The consequence worth stating: Desktop receives the tools and no protocol at
// all. ADR-021 T1's handshake instructions exist because this kit cannot deliver
// one — the read half without the write half, one step further than Cursor.
var claudeDesktopKit = agentKit{
	name:          agentClaudeDesktop,
	bin:           "", // there is no CLI to drive
	globalDir:     "Library/Application Support/Claude",
	mcpConfigFile: "claude_desktop_config.json",
}
