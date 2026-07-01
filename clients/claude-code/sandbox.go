package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// sandboxRoot is the parent directory that holds per-project sandbox configs.
// Each sandbox is a self-contained Claude config dir, so wrapping a session with
// it (via CLAUDE_CONFIG_DIR) isolates that project's commands, settings, MCP
// servers, and agentsmemory token from every other project and from the global
// ~/.claude.
func sandboxRoot() string { return filepath.Join(homeDir(), ".sandboxes") }

// sandboxDir returns the config directory for the named sandbox. Callers must
// validate name with validSandboxName first.
func sandboxDir(name string) string { return filepath.Join(sandboxRoot(), name) }

// validSandboxName rejects names that could escape ~/.sandboxes via path
// separators or traversal. A sandbox is addressed by a plain name, so anything
// else is a mistake (or an attempt to point CLAUDE_CONFIG_DIR somewhere
// unexpected) — reject it rather than resolve a surprising path.
func validSandboxName(name string) error {
	if name == "" {
		return errors.New("sandbox name is empty")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) || name != filepath.Base(name) {
		return fmt.Errorf("invalid sandbox name %q: use a plain name with no path separators", name)
	}
	return nil
}

// wrapClaude replaces the current process with the Claude CLI, optionally pinning
// CLAUDE_CONFIG_DIR to an isolated sandbox config dir. It exec-replaces (rather
// than spawning a child) so the terminal, signals, and exit code pass straight
// through — Claude is a TUI, and `aiagentmemory run foo` should behave exactly
// like running claude, only against foo's configuration.
//
// configDir == "" means global mode: leave CLAUDE_CONFIG_DIR untouched so Claude
// uses its own default (~/.claude).
func wrapClaude(configDir string, claudeArgs []string) error {
	bin, err := resolveClaudeBin("")
	if err != nil {
		return err
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("cannot find the Claude CLI %q on PATH: %w", bin, err)
	}

	env := os.Environ()
	if configDir != "" {
		if _, statErr := os.Stat(configDir); statErr != nil {
			return fmt.Errorf("sandbox config dir %s does not exist — run `aiagentmemory install --sandbox %s` first",
				configDir, filepath.Base(configDir))
		}
		// CLAUDE_CONFIG_DIR is how Claude Code relocates its entire config
		// (settings, commands, MCP servers); setting it is what makes a sandbox
		// an isolated Claude environment.
		env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	}

	// syscall.Exec never returns on success; on failure it returns the errno.
	argv := append([]string{bin}, claudeArgs...)
	return syscall.Exec(path, argv, env)
}

// resolveClaudeBin decides which Claude CLI to drive. Precedence: an explicit
// override (the --claude-bin flag or TEISORA_CLAUDE_BIN), then a teisora-claude
// on PATH (the user's branded build), then plain claude. It returns the command
// name (not the resolved path) so callers can exec.LookPath it themselves.
func resolveClaudeBin(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := os.Getenv("TEISORA_CLAUDE_BIN"); env != "" {
		return env, nil
	}
	for _, candidate := range []string{"teisora-claude", "claude"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("no Claude CLI found on PATH (looked for teisora-claude, claude); set --claude-bin or TEISORA_CLAUDE_BIN")
}

// homeDir returns the user's home directory, falling back to $HOME. It does not
// fail hard here: callers that use the result build paths under it and will
// surface a clear filesystem error if the home dir is unusable.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

// expandTilde rewrites a leading ~ to the user's home directory. It is used for
// the codebase-memory binary path handed to the Claude CLI, so the registered
// MCP command is an absolute path rather than a shell-relative ~ that a non-shell
// exec would not expand.
func expandTilde(p string) string {
	switch {
	case p == "~":
		return homeDir()
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(homeDir(), p[2:])
	default:
		return p
	}
}
