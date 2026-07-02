package views

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestKeyBlockRevealedShowsInstallCommand renders a revealed API-key block and
// checks it offers both connect affordances: the full-install one-paste (carrying
// the token via AGENTSMEMORY_TOKEN and --global) and the register-only `claude mcp
// add`. Rendering the fragment directly verifies the KeyBlock → InstallBlock →
// InstallCommand path without booting the server. Note the `"` around the token is
// HTML-escaped in the <code> text, so the env var and token are asserted
// separately rather than as one quoted string.
func TestKeyBlockRevealedShowsInstallCommand(t *testing.T) {
	var buf bytes.Buffer
	vm := KeyVM{
		TeamID:     "t1",
		Revealed:   true,
		Secret:     "SECRET123",
		ServerBase: "https://memory.example",
		ServerName: "acme",
	}
	if err := KeyBlock(vm).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		"Install the kit",     // the full-install block label
		"install.sh",          // the bootstrap URL
		"AGENTSMEMORY_TOKEN=",  // token passed via env
		"SECRET123",           // the revealed token itself
		"--global",            // non-interactive global mode
		"Add to Claude Code",  // the register-only MCP block still present
	} {
		if !strings.Contains(html, want) {
			t.Errorf("revealed key block missing %q\n---\n%s", want, html)
		}
	}
}

// TestInstallCommandShape locks the exact install one-paste: it must pipe install.sh
// into a bash that carries the token in AGENTSMEMORY_TOKEN and forwards --global, so
// the install is fully non-interactive.
func TestInstallCommandShape(t *testing.T) {
	got := InstallCommand("TOK")
	for _, want := range []string{
		"curl -fsSL ",
		installScriptURL,
		`AGENTSMEMORY_TOKEN="TOK"`,
		"bash -s -- --global",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("InstallCommand missing %q\n got: %s", want, got)
		}
	}
}
