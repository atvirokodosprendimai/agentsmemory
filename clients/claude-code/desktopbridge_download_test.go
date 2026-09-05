package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
	asset, err := serverArchiveName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	// The release package as goreleaser ships it: a tar.gz holding `agentsmemory`.
	pack := func(script string) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "agentsmemory", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(script))
		tw.Close()
		gz.Close()
		return buf.Bytes()
	}
	archive := pack("#!/bin/sh\necho agentsmemory version " + tag + "\n")
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  " + asset + "\n" + strings.Repeat("0", 64) + "  other-asset\n"
	served := 0
	serve := archive // swapped by the subtests that want a wrong package
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, srv0(r)+"/releases/tag/"+tag, http.StatusFound)
		case "/releases/download/" + tag + "/" + asset:
			served++
			w.Write(serve)
		case "/releases/download/" + tag + "/SHA256SUMS.txt":
			w.Write([]byte(sums))
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

	t.Run("a package whose server names another release is refused", func(t *testing.T) {
		// Bytes the release DID publish (the sum matches) but whose server reports a
		// different version: a rebuilt or unstamped asset under this tag. The sum
		// cannot catch it; the --version comparison does.
		wrong := pack("#!/bin/sh\necho agentsmemory version v9.9.9\n")
		w := sha256.Sum256(wrong)
		prevServe, prevSums := serve, sums
		serve, sums = wrong, hex.EncodeToString(w[:])+"  "+asset+"\n"
		t.Cleanup(func() { serve, sums = prevServe, prevSums })
		inst, _, _ := newTestInstallerFor(t, claudeDesktopKit, false)
		inst.serverBin = ""
		err := inst.run()
		if err == nil || !strings.Contains(err.Error(), "v9.9.9") || !strings.Contains(err.Error(), tag) {
			t.Fatalf("a package naming another release was accepted (err=%v)", err)
		}
	})

	t.Run("a download that does not match the release's sums is refused", func(t *testing.T) {
		serve = pack("#!/bin/sh\necho not the release\n")
		t.Cleanup(func() { serve = archive })
		inst, _, dir := newTestInstallerFor(t, claudeDesktopKit, false)
		inst.serverBin = ""
		err := inst.run()
		if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
			t.Fatalf("a tampered bridge was accepted (err=%v)", err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "bin", installedServerBinFile())); statErr == nil {
			t.Error("the tampered download was placed anyway")
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
