package main

import (
	"bytes"
	"context"
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
		v := judgeServerBin(kit, dir, "")
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
		v := judgeServerBin(kit, dir, "")
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
		v := judgeServerBin(kit, dir, "")
		if v == nil {
			t.Fatal("no verdict")
		}
		if v.recorded != bin {
			t.Errorf("the verdict does not name the binary it judged: %q", v.recorded)
		}
		// ⚠ THE EXACT VERDICT, NOT "not a failure". This assertion used to check
		// only that STALE-PATH did not set bad, which held when the implementation
		// returned MISSING or NOT-EXECUTABLE too — it passed in the broken state as
		// well as the correct one, and a review found it saying nothing. The healthy
		// case has exactly one right answer and this is it.
		if v.label != "ok" || v.bad {
			t.Errorf("a healthy registration is %q/bad=%v, want \"ok\"/false: %s", v.label, v.bad, v.detail)
		}
	})

	t.Run("a config with no entry of ours is a finding, not silence", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, kit.mcpConfigFile)
		if err := os.WriteFile(path, []byte(`{"mcpServers":{"theirs":{"command":"/usr/bin/theirs"}}}`), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		v := judgeServerBin(kit, dir, "")
		// ⚠ THIS RETURNED nil, explained as "the hook report covers the rest" — and
		// that sentence is false for precisely the kits this check serves, because
		// they ship no hooks and so have no other report. An uninstalled bridge
		// looked exactly like a healthy one.
		if v == nil || !v.bad || v.label != "NOT-REGISTERED" {
			t.Errorf("a config carrying no %s entry produced %+v; an operator with no MCP "+
				"registered must be told so", mcpName, v)
		}
	})

	t.Run("a missing config is a finding, not silence", func(t *testing.T) {
		v := judgeServerBin(kit, t.TempDir(), "")
		if v == nil || !v.bad || v.label != "NOT-REGISTERED" {
			t.Errorf("an absent MCP config produced %+v, want a NOT-REGISTERED finding", v)
		}
	})

	t.Run("a byte-identical binary elsewhere is not stale", func(t *testing.T) {
		// ⚠ THE FALSE POSITIVE THIS BRANCH WOULD HAVE SHIPPED. Since the installer
		// places the binary it registers, the recorded path ALWAYS differs from the
		// one on PATH on a correct install — so a path-string comparison reported
		// every healthy install as STALE-PATH. Content is what the question is
		// actually about.
		dir := t.TempDir()
		body := []byte("#!/bin/sh\necho same\n")
		a, b := filepath.Join(dir, "recorded"), filepath.Join(dir, "onpath")
		for _, p := range []string{a, b} {
			if err := os.WriteFile(p, body, 0o755); err != nil {
				t.Fatalf("write %s: %v", p, err)
			}
		}
		if label, _ := compareWithPath(a, b); label != "ok" {
			t.Errorf("two byte-identical binaries at different paths compared as %q; a check that "+
				"fires on every correct install is one an operator learns to ignore", label)
		}
		// And a genuinely different build still reports.
		if err := os.WriteFile(b, []byte("#!/bin/sh\necho newer\n"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
		if label, _ := compareWithPath(a, b); label != "STALE-PATH" {
			t.Errorf("a different build on PATH compared as %q, want STALE-PATH", label)
		}
	})

	t.Run("a kit that hands over a URL has no binary to judge", func(t *testing.T) {
		kits, err := resolveAgentKits(agentClaude)
		if err != nil || len(kits) != 1 {
			t.Fatalf("resolve the claude kit: %v", err)
		}
		if v := judgeServerBin(kits[0], t.TempDir(), ""); v != nil {
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

// TestDoctorActuallyPrintsTheBinaryVerdict covers the rung every other test in
// this file is blind to: whether the command an operator runs can reach the
// judgement at all.
//
// ⚠ IT SHIPPED UNREACHABLE, AND THE COMMIT MESSAGE SAID OTHERWISE. judgeServerBin
// applies exactly when kitNeedsServerBin is true, which today is claude-desktop
// alone — and claude-desktop ships no hooks file, so runHookDoctor's
// `kit.hooksFile == ""` guard returned before the call was reached. Every kit that
// DID reach it has its own CLI binary, so kitNeedsServerBin was false and the call
// returned nil. No invocation of `doctor`, for any agent, could print a binary
// verdict. Every test in this file passed, because every one called judgeServerBin
// directly — the mechanism was exercised, its SELECTION was not.
//
// That is this repository's signature defect, shipped inside the commit claiming
// to report drift. This test goes through rootCommand, so removing the
// reportServerBin dispatch — or restoring a guard in front of it — turns it red.
func TestDoctorActuallyPrintsTheBinaryVerdict(t *testing.T) {
	kit := desktopKit(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "server")
	// ⚠ IT MUST NAME A BUILD, and matching the package's stubbed server version is
	// what makes this fixture HEALTHY rather than merely present. `exit 0` was
	// enough while doctor judged the binary's existence alone; since #207 it also
	// asks the bridge which build it is, and a bridge that answers nothing is a
	// finding — a real one, since the bridge is this project's own binary and
	// always prints. The stub is the fixture's whole claim to being an install.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'agentsmemory version v0.0.0-test-stub'\n"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	writeMCPConfig(t, dir, kit.mcpConfigFile, bin)

	var buf bytes.Buffer
	root := rootCommand()
	root.Writer = &buf
	err := root.Run(context.Background(), []string{
		"aiagentmemory", "doctor", "--agent", agentClaudeDesktop,
		"--target-dir", dir, "--project-dir", t.TempDir(),
	})
	report := buf.String()

	if err != nil {
		t.Fatalf("doctor on a healthy desktop install failed: %v\n%s", err, report)
	}
	if !strings.Contains(report, "mcp bridge binary") {
		t.Errorf("`doctor --agent %s` printed no bridge-binary line, so the check cannot be "+
			"reached from the command an operator runs:\n%s", agentClaudeDesktop, report)
	}
	if !strings.Contains(report, bin) {
		t.Errorf("the report does not name the binary it judged (%s):\n%s", bin, report)
	}
}

// TestDoctorFailsADesktopInstallWhoseBinaryIsGone is the same route in the state
// that must exit non-zero, because a report an operator can ignore and an exit
// code they cannot are different promises and only one of them gates anything.
func TestDoctorFailsADesktopInstallWhoseBinaryIsGone(t *testing.T) {
	kit := desktopKit(t)
	dir := t.TempDir()
	writeMCPConfig(t, dir, kit.mcpConfigFile, filepath.Join(dir, "not-here"))

	var buf bytes.Buffer
	root := rootCommand()
	root.Writer = &buf
	err := root.Run(context.Background(), []string{
		"aiagentmemory", "doctor", "--agent", agentClaudeDesktop,
		"--target-dir", dir, "--project-dir", t.TempDir(),
	})
	if err == nil {
		t.Errorf("doctor exited 0 over a registration naming a binary that does not exist; the "+
			"MCP can never connect, so this must fail:\n%s", buf.String())
	}
}

// TestAFailedPlacementLeavesThePreviousBinaryIntact pins the property the atomic
// rename exists for.
//
// ⚠ THE OBVIOUS IMPLEMENTATION FAILS THIS. os.Remove followed by os.WriteFile
// leaves NO file at a path an agent config already points at, for as long as the
// write takes — and permanently if it fails. Review caught it before it shipped:
// the installer treats a registration failure as non-fatal, so the install would
// have reported success over a bridge that could no longer start. Staging into
// the same directory and renaming means a failure never touches the live file.
//
// It drives the real placeServerBin, forcing a failure by making the destination
// directory unwritable after the previous binary is in place.
//
// ⚠ IT DOES NOT DISTINGUISH THE TWO IMPLEMENTATIONS, AND SAYING SO IS THE POINT.
// Graded by mutation: replacing the stage-and-rename with os.Remove followed by
// os.WriteFile leaves this test GREEN, because an unwritable directory denies the
// unlink too, so the destructive version returns before destroying anything. The
// window it opens — dest removed, replacement not yet written — is only reachable
// by failing BETWEEN two syscalls, which no black-box test here can force. So the
// atomicity claim rests on os.Rename's semantics and on replaceBinary's precedent,
// not on this test. What this test does pin is the weaker, still-worth-having
// property in its name: a placement that fails does not leave the operator worse
// off than before they ran it.
func TestAFailedPlacementLeavesThePreviousBinaryIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not deny root, so the failure " +
			"this test forces cannot be forced")
	}
	target := t.TempDir()
	binDir := filepath.Join(target, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dest := filepath.Join(binDir, installedServerBinName)
	previous := []byte("#!/bin/sh\necho the binary that is already registered\n")
	if err := os.WriteFile(dest, previous, 0o755); err != nil {
		t.Fatalf("seed the previous binary: %v", err)
	}

	src := filepath.Join(t.TempDir(), "newly-built")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho new\n"), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// Deny writes to the directory, so staging cannot create its temp file.
	if err := os.Chmod(binDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(binDir, 0o755) })

	i := &Installer{targetDir: target, serverBin: src, out: &strings.Builder{}}
	if _, err := i.placeServerBin(); err == nil {
		t.Fatal("placing into an unwritable directory reported success")
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("the previously installed binary is gone after a FAILED placement, so the "+
			"registration now names nothing: %v", err)
	}
	if string(got) != string(previous) {
		t.Errorf("a failed placement changed the installed binary:\n want %q\n got  %q", previous, got)
	}
}
