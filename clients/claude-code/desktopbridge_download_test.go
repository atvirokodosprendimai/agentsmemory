package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestClaudeDesktopInstallDownloadsTheBridgeWhenNoHostBinaryExists is issue
// #199: a Compose-only install — the documented local server — leaves no host
// binary, and `--agent claude-desktop` needs one for its mcp-stdio bridge, so
// the documented happy path ended in a refusal telling the operator to install
// a Go toolchain. Every release already publishes aiagentmemory-server for each
// platform; the installer now fetches it when nothing is on PATH. The refusal
// stays for the case where the fetch fails too, because an entry naming a binary
// that is not there fails inside Claude Desktop and reads as our bug.
func TestClaudeDesktopInstallDownloadsTheBridgeWhenNoHostBinaryExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture bridge is a shell script; verifyBinary runs it")
	}
	const tag = "v0.0.0-test"
	asset, err := serverAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	served := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, srv0(r)+"/releases/tag/"+tag, http.StatusFound)
		case "/releases/download/" + tag + "/" + asset:
			served++
			w.Write([]byte("#!/bin/sh\necho agentsmemory version " + tag + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	prev := releaseBaseURL
	releaseBaseURL = srv.URL
	t.Cleanup(func() { releaseBaseURL = prev })

	t.Run("fetched from the release and registered", func(t *testing.T) {
		inst, _, dir := newTestInstallerFor(t, claudeDesktopKit, false)
		inst.serverBin = ""
		inst.mcpURL = "http://localhost:8080/mcp"
		if err := inst.run(); err != nil {
			t.Fatalf("install with no host binary failed: %v\n%s", err, inst.out.(interface{ String() string }).String())
		}
		if served == 0 {
			t.Fatal("the install succeeded without downloading the bridge — what did it register?")
		}
		body, err := os.ReadFile(filepath.Join(dir, claudeDesktopKit.mcpConfigFile))
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			MCPServers map[string]struct{ Command string } `json:"mcpServers"`
		}
		if err := json.Unmarshal(body, &cfg); err != nil {
			t.Fatal(err)
		}
		cmd := cfg.MCPServers[mcpName].Command
		if !strings.HasPrefix(cmd, filepath.Join(dir, "bin")) {
			t.Fatalf("registered command %q is not the placed binary under %s/bin", cmd, dir)
		}
		if st, err := os.Stat(cmd); err != nil || st.Mode()&0o111 == 0 {
			t.Fatalf("registered binary %q is missing or not executable (%v)", cmd, err)
		}
		out := inst.out.(interface{ String() string }).String()
		if !strings.Contains(out, tag) || !strings.Contains(out, asset) {
			t.Errorf("the report does not say which release and asset were fetched:\n%s", out)
		}
	})

	t.Run("a failed download still refuses with the build hint", func(t *testing.T) {
		releaseBaseURL = srv.URL + "/nowhere"
		t.Cleanup(func() { releaseBaseURL = srv.URL })
		inst, _, _ := newTestInstallerFor(t, claudeDesktopKit, false)
		inst.serverBin = ""
		err := inst.run()
		if err == nil {
			t.Fatal("an install whose bridge could neither be found nor fetched exited 0")
		}
		for _, want := range []string{"go build", "--server-bin", asset} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal lost %q — an operator needs both the cause and the hand remedy:\n%v", want, err)
			}
		}
	})

	t.Run("dry-run says it would download and touches nothing", func(t *testing.T) {
		before := served
		inst, _, dir := newTestInstallerFor(t, claudeDesktopKit, false)
		inst.serverBin = ""
		inst.dryRun = true
		if err := inst.run(); err != nil {
			t.Fatalf("dry-run: %v", err)
		}
		out := inst.out.(interface{ String() string }).String()
		if !strings.Contains(out, "would download") || !strings.Contains(out, asset) {
			t.Errorf("the dry-run does not say it would download the bridge:\n%s", out)
		}
		if served != before {
			t.Error("a dry-run downloaded the bridge")
		}
		if _, err := os.Stat(filepath.Join(dir, "bin")); err == nil {
			t.Error("a dry-run created the bin directory")
		}
	})
}

// No test in this package may reach GitHub. The Desktop bridge fallback dials
// releaseBaseURL whenever an install has no server binary, which is the state
// several existing refusal tests set up on purpose — and with the real default
// they downloaded the real bridge and passed for the wrong reason (measured
// 2026-09-05: a 4-second "refusal" test that registered a working bridge). Tests
// that want a release point releaseBaseURL at their own httptest server.
func init() {
	releaseBaseURL = "http://127.0.0.1:1"
}

// srv0 rebuilds the scheme+host of the request so the redirect stays on the
// fixture server whatever port it was given.
func srv0(r *http.Request) string { return "http://" + r.Host }
