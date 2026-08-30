package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newSocketInstaller builds an installer wired for the --local --socket path,
// so the registration can be asserted without touching a real agent CLI.
func newSocketInstaller(t *testing.T, kit agentKit, socket, serverBin string) (*Installer, *recordingRunner) {
	t.Helper()
	rr := &recordingRunner{}
	return &Installer{
		targetDir: t.TempDir(),
		kit:       kit,
		agentBin:  kit.bin,
		mcpURL:    localMCPURL,
		socket:    socket,
		serverBin: serverBin,
		scope:     "user",
		local:     true,
		out:       &bytes.Buffer{},
		in:        strings.NewReader(""),
		runner:    rr,
	}, rr
}

// TestRegisterSocketMCPClaude pins the exact command Claude is given. The server
// arguments must land after the `--` separator, or the agent CLI would try to
// parse --socket as one of its own flags.
//
// ⚠ THE BINARY IS THE PLACED COPY, not whatever --server-bin resolved to. Socket
// mode used to freeze a PATH lookup into the agent's config, so a rebuild
// elsewhere left the bridge spawning a stale server and nothing could say so.
func TestRegisterSocketMCPClaude(t *testing.T) {
	inst, rr := newSocketInstaller(t, claudeKit, "/tmp/am.sock", fakeBuiltServerBin(t))
	inst.wing = "wing_acme"
	placed := filepath.Join(inst.targetDir, "bin", installedServerBinName)

	if err := inst.registerSocketMCP(); err != nil {
		t.Fatalf("registerSocketMCP: %v", err)
	}

	want := []string{
		"mcp remove --scope user agentsmemory",
		"mcp add --transport stdio --scope user agentsmemory -- " + placed + " mcp-stdio --socket /tmp/am.sock --wing wing_acme",
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Errorf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestRegisterSocketMCPCodex covers the other spelling: codex infers stdio from a
// trailing command and takes no --scope.
func TestRegisterSocketMCPCodex(t *testing.T) {
	inst, rr := newSocketInstaller(t, codexKit, "/tmp/am.sock", fakeBuiltServerBin(t))
	inst.wing = "wing_acme"
	placed := filepath.Join(inst.targetDir, "bin", installedServerBinName)

	if err := inst.registerSocketMCP(); err != nil {
		t.Fatalf("registerSocketMCP: %v", err)
	}

	want := []string{
		"mcp remove agentsmemory",
		"mcp add agentsmemory -- " + placed + " mcp-stdio --socket /tmp/am.sock --wing wing_acme",
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Errorf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestRegisterSocketMCPNoToken is the security assertion: the registered command
// is stored in plain config and visible in `ps`, so a token must never appear in
// it.
func TestRegisterSocketMCPNoToken(t *testing.T) {
	inst, rr := newSocketInstaller(t, claudeKit, "/tmp/am.sock", fakeBuiltServerBin(t))
	inst.token = "SECRET-TOKEN"

	if err := inst.registerSocketMCP(); err != nil {
		t.Fatalf("registerSocketMCP: %v", err)
	}

	for _, line := range renderAll(rr.calls) {
		if strings.Contains(line, "SECRET-TOKEN") {
			t.Fatalf("token leaked onto the MCP command line: %q", line)
		}
	}
}

// TestResolveServerBinAbsolute checks the bridge command gets an absolute path.
// The agent spawns it from an arbitrary working directory with its own PATH, so a
// bare name that resolves at install time can fail at launch time.
func TestResolveServerBinAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bit handling differs on windows")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "agentsmemory")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed binary: %v", err)
	}

	// Pass it as a relative path from the directory holding it.
	t.Chdir(dir)
	got, err := resolveServerBin("./agentsmemory", false)
	if err != nil {
		t.Fatalf("resolveServerBin: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("resolveServerBin returned %q, want an absolute path", got)
	}
}

// TestResolveServerBinMissing must fail loudly rather than register a bridge
// pointing at a binary that does not exist — that would surface much later as an
// MCP server which silently never connects.
func TestResolveServerBinMissing(t *testing.T) {
	if _, err := resolveServerBin("definitely-not-a-real-binary-xyz", false); err == nil {
		t.Fatal("expected an error for a missing server binary")
	}
}

// TestResolveServerBinDryRun keeps `--dry-run` usable on a machine that has not
// installed the server yet: the plan should still print.
func TestResolveServerBinDryRun(t *testing.T) {
	got, err := resolveServerBin("definitely-not-a-real-binary-xyz", true)
	if err != nil {
		t.Fatalf("dry run should tolerate a missing binary: %v", err)
	}
	if got != "definitely-not-a-real-binary-xyz" {
		t.Fatalf("got %q, want the requested name echoed back", got)
	}
}

// TestRegisterSocketMCPRejectsPi documents the one agent this cannot serve: pi
// has no MCP client, and its bridge extension speaks HTTP to a URL.
func TestRegisterSocketMCPRejectsPi(t *testing.T) {
	inst, rr := newSocketInstaller(t, piKit, "/tmp/am.sock", "/opt/bin/agentsmemory")

	if err := inst.registerSocketMCP(); err == nil {
		t.Fatal("expected --socket to be rejected for pi")
	}
	if len(rr.calls) != 0 {
		t.Errorf("nothing should have been registered, got %v", renderAll(rr.calls))
	}
}
