package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestARetiredCommandIsNeitherShippedNorLeftBehind pins both halves of retiring a
// slash command, because doing only the first is the failure this repository
// already recorded once.
//
// ADR-041's SessionEnd work found that an install which merely stops PLANNING an
// asset leaves every upgraded machine carrying it, while the installer's own
// output lists only what it wrote — so the operator is told the thing is gone and
// it is not. A slash command has exactly that shape: dropping M.md from
// commandAssets stops new installs getting it and leaves every existing
// ~/.claude/commands/M.md in place, still offering a second grounding sequence
// beside /am.
func TestARetiredCommandIsNeitherShippedNorLeftBehind(t *testing.T) {
	if len(retiredCommands) == 0 {
		t.Skip("nothing retired yet; this gate arms itself when something is")
	}

	t.Run("not shipped", func(t *testing.T) {
		for _, name := range retiredCommands {
			for _, shipped := range commandAssets {
				if name == shipped {
					t.Errorf("%s is listed as retired AND still shipped in commandAssets", name)
				}
			}
			// The embed is the other place it could survive: a name dropped from
			// commandAssets but left in //go:embed is dead weight that a future
			// edit can quietly re-list.
			if _, err := assets.ReadFile("commands/" + name); err == nil {
				t.Errorf("commands/%s is still embedded in the binary; remove it from the //go:embed line too", name)
			}
		}
	})

	// ⚠ THIS DRIVES THE REAL INSTALL, AND THE FIRST VERSION DID NOT. It called
	// inst.clearRetiredCommands() directly, so deleting the call from run() left
	// the gate green — the function worked and nothing invoked it, which is the
	// §Reachability defect this file's own comment is about. Mutation caught it:
	// removing the call from the install path survived.
	t.Run("a real install removes it", func(t *testing.T) {
		inst, _, dir := newTestInstaller(t, false)

		cmds := filepath.Join(dir, inst.kit.commandsDir)
		if err := os.MkdirAll(cmds, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		stale := filepath.Join(cmds, retiredCommands[0])
		if err := os.WriteFile(stale, []byte("# an older kit wrote this\n"), 0o644); err != nil {
			t.Fatalf("seed stale command: %v", err)
		}

		if err := inst.run(); err != nil {
			t.Fatalf("install: %v", err)
		}

		if _, err := os.Stat(stale); err == nil {
			t.Errorf("%s survived a real install; an upgraded machine keeps a command this kit no longer ships, while the installer's output lists only what it wrote", stale)
		}
	})

	// A dry run must report the removal and change nothing — the same contract
	// every other install step keeps.
	t.Run("a dry run reports without removing", func(t *testing.T) {
		dir := t.TempDir()
		cmds := filepath.Join(dir, claudeKit.commandsDir)
		if err := os.MkdirAll(cmds, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		stale := filepath.Join(cmds, retiredCommands[0])
		if err := os.WriteFile(stale, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}

		inst := &Installer{kit: claudeKit, targetDir: dir, out: os.Stderr, dryRun: true}
		inst.clearRetiredCommands()

		if _, err := os.Stat(stale); err != nil {
			t.Errorf("a dry run removed %s; it must only say what it would do", stale)
		}
	})
}
