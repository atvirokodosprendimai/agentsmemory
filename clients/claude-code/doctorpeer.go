package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// codebaseMemoryHookScripts are the hook scripts upstream's codebase-memory
// installer registers, by basename. Declared once here so the doctor rung and
// the installer's dedupe count the same universe; a script upstream adds
// tomorrow is invisible to both until it is added here, which is the honest
// state — the kit does not own that installer.
var codebaseMemoryHookScripts = []string{
	"cbm-session-reminder",
	"cbm-subagent-reminder",
	"cbm-code-discovery-gate",
}

// codebaseMemoryMCPNames are the registration names the peer has been known
// under: upstream's own, and the one this kit registered until ADR-057.
// The rung reads both because a machine that took the kit's --recommended path
// after upstream's installer carries the same binary under both.
var codebaseMemoryMCPNames = []string{codebaseMemoryMCPName, retiredCodebaseMemoryName}

// codebaseMemoryMCPName is upstream's registration name — the one the protocol
// text and the harness's tool prefix use. retiredCodebaseMemoryName is what
// this kit registered until ADR-057; T2 stops writing it and removes it.
const (
	codebaseMemoryMCPName     = "codebase-memory-mcp"
	retiredCodebaseMemoryName = "codebasememory"
)

// peerVerdict is what doctor concluded about the codebase-memory peer.
//
// ADR-057: the protocol tells every session to call this peer first, the kit
// installs it, and until this rung nothing in the kit ever looked at it again.
// Measured 2026-09-05: cbm-session-reminder registered four times on
// SessionStart, four injections and four processes at every session start,
// and doctor printed a clean report. The vocabulary is deliberately small —
// `absent` is legal because ADR-020's kits cannot host a stdio MCP at all;
// DUPLICATE and BROKEN set the exit code because every session pays for each
// copy and nothing else will ever say so.
type peerVerdict struct {
	label  string // ok | absent | DUPLICATE | BROKEN | n/a
	detail string
	bad    bool
}

// judgeCodebaseMemory reads the peer's state from the two files the kit
// already reads — settings.json for hooks, .claude.json for MCP entries — and
// never spawns it. "Can the harness spawn it" is answered by the executable
// bit; "is the index fresh" is the peer's own index_status and not the kit's
// to judge (ADR-057 Alternatives).
func judgeCodebaseMemory(kit agentKit, dir string) peerVerdict {
	if kit.name != agentClaude {
		return peerVerdict{label: "n/a", detail: "the rung reads Claude's registration files; " + kit.name + " keeps its MCPs elsewhere"}
	}
	commands, err := codebaseMemoryMCPCommands(claudeMCPRegistry(kit, dir))
	if err != nil {
		return peerVerdict{label: "BROKEN", detail: err.Error(), bad: true}
	}
	counts, err := codebaseMemoryHookCounts(filepath.Join(dir, kit.hooksFile))
	if err != nil {
		return peerVerdict{label: "BROKEN", detail: err.Error(), bad: true}
	}
	if len(commands) == 0 && len(counts) == 0 {
		return peerVerdict{label: "absent", detail: "no MCP registration and no cbm-* hook — optional; `aiagentmemory install --recommended` adds it"}
	}

	var findings []string
	if len(commands) > 1 {
		names := make([]string, 0, len(commands))
		for name := range commands {
			names = append(names, name)
		}
		sort.Strings(names)
		findings = append(findings, "the same server is registered as "+strings.Join(names, " and ")+" — two daemons, and the protocol names only codebase-memory-mcp")
	}
	var dupes []string
	for key, n := range counts {
		if n > 1 {
			dupes = append(dupes, fmt.Sprintf("%s ×%d", key, n))
		}
	}
	sort.Strings(dupes)
	if len(dupes) > 0 {
		findings = append(findings, "registered more than once: "+strings.Join(dupes, ", ")+" — each copy runs and injects every time")
	}
	if len(findings) > 0 {
		return peerVerdict{label: "DUPLICATE", detail: strings.Join(findings, "; "), bad: true}
	}

	for name, cmd := range commands {
		if err := executable(cmd); err != nil {
			return peerVerdict{label: "BROKEN", detail: name + " → " + cmd + ": " + err.Error(), bad: true}
		}
	}
	if len(commands) == 0 {
		return peerVerdict{label: "BROKEN", detail: "cbm-* hooks are registered but no MCP entry names the server — the hooks will run and the agent has no tools to call", bad: true}
	}
	for name, cmd := range commands {
		detail := name + " → " + cmd + fmt.Sprintf(", %d hook registration(s)", len(counts))
		if name == retiredCodebaseMemoryName {
			// One registration, so not a duplicate — but under a name no document
			// tells the agent to call. Said in the row, not the exit code: the
			// install works, and `install --recommended` renames it (T2).
			detail += " — under the RETIRED name; the protocol's tool prefix is " + codebaseMemoryMCPName + ", and `install --recommended` renames it"
		}
		return peerVerdict{label: "ok", detail: detail}
	}
	return peerVerdict{label: "ok"}
}

// claudeMCPRegistry is the file Claude reads its MCP registrations from for
// this config dir — the same rule pinConfigDir encodes for the installer: a
// GLOBAL install leaves CLAUDE_CONFIG_DIR unset, so Claude reads ~/.claude.json;
// a pinned dir (sandbox, --config-dir) moves the registry to <dir>/.claude.json.
//
// Measured 2026-09-05 on the owner's machine before this existed: the rung read
// <config-dir>/.claude.json unconditionally and reported `codebasememory → …`
// from a ghost file (~/.claude/.claude.json, left by an old pinned install)
// while `claude mcp list` knew only codebase-memory-mcp from ~/.claude.json. A
// diagnostic that reads a file the agent does not is a diagnostic of nothing.
//
// Resolved, NOT a union across config dirs — decided against the alternative
// on the evidence: review of #265/#266 found the peer under upstream's name in
// ~/.claude.json and under the kit's in ~/.sandboxes/aks/.claude.json, and a
// union read reports DUPLICATE over two legitimate installs that each carry
// exactly one registration. Doctor is per-install (--agent, --target-dir);
// `doctor --target-dir ~/.sandboxes/aks` judges that install on its own.
func claudeMCPRegistry(kit agentKit, dir string) string {
	if dir == kit.globalConfigDir(homeDir()) {
		return filepath.Join(homeDir(), ".claude.json")
	}
	return filepath.Join(dir, ".claude.json")
}

// codebaseMemoryMCPCommands returns name → command for every mcpServers entry
// under a name the peer has been known by. A missing file is an empty map:
// "nothing registered" is a state, not an error.
func codebaseMemoryMCPCommands(claudeJSON string) (map[string]string, error) {
	raw, err := os.ReadFile(claudeJSON)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", claudeJSON, err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", claudeJSON, err)
	}
	out := map[string]string{}
	for _, name := range codebaseMemoryMCPNames {
		if s, ok := cfg.MCPServers[name]; ok {
			out[name] = s.Command
		}
	}
	return out, nil
}

// codebaseMemoryHookCounts counts, per "event/script", how many times each
// cbm-* script is registered. The kit's own registeredHookEvents filters to the
// kit's scripts through installerHookPath, so the peer's need their own walk
// over the same document shape.
func codebaseMemoryHookCounts(settingsPath string) (map[string]int, error) {
	raw, err := os.ReadFile(settingsPath)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", settingsPath, err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", settingsPath, err)
	}
	out := map[string]int{}
	for event, matchers := range doc.Hooks {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				base := filepath.Base(strings.Trim(strings.TrimSpace(h.Command), `"'`))
				for _, script := range codebaseMemoryHookScripts {
					if base == script {
						out[event+"/"+script]++
					}
				}
			}
		}
	}
	return out, nil
}

// executable reports why a command cannot be spawned: absent, or present
// without the execute bit — the two states upstream's installer leaves behind
// when the download fails or lands with the wrong mode.
func executable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return errors.New("not executable")
	}
	return nil
}

// reportCodebaseMemory prints the row in the same columns the hook and server
// rows use, and returns whether it counts against the exit code.
func reportCodebaseMemory(out io.Writer, kit agentKit, dir string) bool {
	v := judgeCodebaseMemory(kit, dir)
	fmt.Fprintf(out, "  %-38s %-14s %-12s %s\n", "codebase-memory", "peer", v.label, v.detail)
	return v.bad
}
