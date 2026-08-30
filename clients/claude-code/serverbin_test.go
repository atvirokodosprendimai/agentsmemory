package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// desktopKit returns the kit that registers by spawning a binary, which is the
// only kind this check applies to.
func desktopKit(t *testing.T) agentKit {
	t.Helper()
	kits, err := resolveAgentKits(agentClaudeDesktop)
	if err != nil || len(kits) != 1 {
		t.Fatalf("resolve the desktop kit: %v", err)
	}
	return kits[0]
}

// writeMCPConfig writes a desktop-shaped registration naming cmd.
func writeMCPConfig(t *testing.T, dir, file, cmd string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			mcpName: map[string]any{"command": cmd, "args": []string{"mcp-stdio"}},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestDoctorJudgesTheBinaryTheBridgeSpawns pins the check that would have caught
// a five-day-old server.
//
// ⚠ THE REGISTRATION IS FROZEN AT INSTALL TIME AND NOTHING RE-RESOLVED IT. On
// 2026-08-30 Claude Desktop was spawning a binary from one directory while a
// current one sat on PATH in another; it connected fine and served old code, and
// it was found by listing processes rather than by any check. These are the three
// states an operator can act on.
func TestDoctorJudgesTheBinaryTheBridgeSpawns(t *testing.T) {
	kit := desktopKit(t)

	t.Run("a registration naming nothing is a hard failure", func(t *testing.T) {
		dir := t.TempDir()
		writeMCPConfig(t, dir, kit.mcpConfigFile, filepath.Join(dir, "not-here"))
		v := judgeServerBin(kit, dir)
		if v == nil {
			t.Fatal("no verdict for a kit that spawns a binary")
		}
		if !v.bad || v.label != "MISSING" {
			t.Errorf("a registration naming a nonexistent binary is %q/bad=%v; the MCP can never "+
				"connect, so it must fail", v.label, v.bad)
		}
	})

	t.Run("a registration naming a non-executable is a hard failure", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "server")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		writeMCPConfig(t, dir, kit.mcpConfigFile, bin)
		v := judgeServerBin(kit, dir)
		if v == nil || !v.bad || v.label != "NOT-EXECUTABLE" {
			t.Errorf("a non-executable registration was not failed: %+v", v)
		}
	})

	t.Run("an executable that exists is ok, and says which one", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "server")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
		writeMCPConfig(t, dir, kit.mcpConfigFile, bin)
		v := judgeServerBin(kit, dir)
		if v == nil {
			t.Fatal("no verdict")
		}
		if v.recorded != bin {
			t.Errorf("the verdict does not name the binary it judged: %q", v.recorded)
		}
		// ⚠ STALE-PATH IS REPORTED, NEVER FAILED. An operator may have pointed a
		// kit at a deliberate build; a check that fails on a legitimate choice is
		// one that gets switched off.
		if v.label == "STALE-PATH" && v.bad {
			t.Error("STALE-PATH counted as a failure — it is a report, because pointing a kit at " +
				"a deliberate build is legitimate")
		}
	})

	t.Run("a kit that hands over a URL has no binary to judge", func(t *testing.T) {
		kits, err := resolveAgentKits(agentClaude)
		if err != nil || len(kits) != 1 {
			t.Fatalf("resolve the claude kit: %v", err)
		}
		if v := judgeServerBin(kits[0], t.TempDir()); v != nil {
			t.Errorf("a URL-handover kit got a binary verdict: %+v", v)
		}
	})
}

// TestInstallPlacesTheBinaryItRegisters pins the other half: the path in the
// config is one the installer owns and refreshes, not wherever the binary
// happened to be on the day someone last ran it.
func TestInstallPlacesTheBinaryItRegisters(t *testing.T) {
	src := filepath.Join(t.TempDir(), "built-elsewhere")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho v2\n"), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	target := t.TempDir()
	i := &Installer{targetDir: target, serverBin: src, out: &strings.Builder{}}

	placed, err := i.placeServerBin()
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	want := filepath.Join(target, "bin", installedServerBinName)
	if placed != want {
		t.Errorf("placed at %q, want %q — the registration must name a path the installer owns", placed, want)
	}
	info, err := os.Stat(placed)
	if err != nil {
		t.Fatalf("stat placed: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("the placed binary is not executable, so the bridge cannot spawn it")
	}

	// ⚠ A SECOND INSTALL REFRESHES IT. That is the whole point: drift becomes
	// impossible rather than merely visible.
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho v3\n"), 0o755); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}
	if _, err := i.placeServerBin(); err != nil {
		t.Fatalf("re-place: %v", err)
	}
	got, err := os.ReadFile(placed)
	if err != nil {
		t.Fatalf("read placed: %v", err)
	}
	if !strings.Contains(string(got), "v3") {
		t.Errorf("a re-install left the OLD binary in place: %q", got)
	}
}

// TestTheRegistrationNamesThePlacedBinary is the WIRING rung, and it was missing.
//
// ⚠ A MUTANT FOUND THIS: reverting the registration to the PATH-resolved binary —
// undoing the entire fix — passed every test in this file, because they exercised
// placeServerBin in isolation and nothing read what the registration actually
// wrote. The component tested instead of the selection, which is the defect this
// repository is named for, in the commit that fixes a drift problem.
func TestTheRegistrationNamesThePlacedBinary(t *testing.T) {
	kit := desktopKit(t)
	src := filepath.Join(t.TempDir(), "built-elsewhere")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	target := t.TempDir()
	i := &Installer{
		targetDir: target, serverBin: src, kit: kit,
		mcpURL: "http://localhost:8080/mcp", out: &strings.Builder{},
	}

	if err := i.registerClaudeDesktopMCP(""); err != nil {
		t.Fatalf("register: %v", err)
	}

	recorded, err := recordedMCPCommand(filepath.Join(target, kit.mcpConfigFile))
	if err != nil {
		t.Fatalf("read back the registration: %v", err)
	}
	want := filepath.Join(target, "bin", installedServerBinName)
	if recorded != want {
		t.Errorf("the registration spawns %q, not the placed binary %q — the config is frozen at "+
			"wherever the binary happened to be, which is the drift this change exists to remove",
			recorded, want)
	}
	if recorded == src {
		t.Error("the registration names the SOURCE path directly; a later build elsewhere leaves " +
			"this config spawning an old binary with no signal")
	}
}

// fakeBuiltServerBin writes a stand-in for a freshly built server binary and
// returns its path.
//
// ⚠ IT MUST BE A REAL FILE. Tests used to hand the installer a synthetic
// "/opt/bin/aiagentmemory-server" string, which was harmless while registration
// only recorded the path. It stopped being harmless when the installer began
// COPYING that binary into the kit — a path nothing can read is now a failure,
// and correctly so, because in production it means registering a command that
// cannot be spawned.
func fakeBuiltServerBin(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "aiagentmemory-server")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write a stand-in server binary: %v", err)
	}
	return p
}
