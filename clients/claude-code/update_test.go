package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	got, err := assetName("darwin", "arm64")
	if err != nil {
		t.Fatalf("assetName(darwin, arm64): %v", err)
	}
	if got != "aiagentmemory-darwin-arm64" {
		t.Errorf("assetName(darwin, arm64) = %q, want aiagentmemory-darwin-arm64", got)
	}

	// windows/amd64 became a published target on 2026-08-31, and it carries the
	// suffix the release asset carries — without it the URL 404s.
	if got, err := assetName("windows", "amd64"); err != nil {
		t.Errorf("assetName(windows, amd64): %v", err)
	} else if got != "aiagentmemory-windows-amd64.exe" {
		t.Errorf("assetName(windows, amd64) = %q, want aiagentmemory-windows-amd64.exe", got)
	}

	// Platforms the release workflow does not build must fail before any
	// download rather than 404 halfway through one.
	//
	// ⚠ windows/arm64 IS THE CASE THAT MATTERS. The platform set is a set of
	// PAIRS, not two independent switches, so adding windows must not promise
	// every arch it could be crossed with — the old cross-product version would
	// have.
	for _, tc := range [][2]string{{"windows", "arm64"}, {"linux", "386"}, {"plan9", "arm64"}} {
		if _, err := assetName(tc[0], tc[1]); err == nil {
			t.Errorf("assetName(%s, %s) = nil error, want unsupported", tc[0], tc[1])
		}
	}
}

func TestReleaseAssetURL(t *testing.T) {
	base := releaseBaseURL
	got := releaseAssetURL("v0.0.47", "aiagentmemory-darwin-arm64")
	want := base + "/releases/download/v0.0.47/aiagentmemory-darwin-arm64"
	if got != want {
		t.Errorf("releaseAssetURL = %q, want %q", got, want)
	}
}

func TestTagFromLocation(t *testing.T) {
	cases := []struct {
		loc     string
		want    string
		wantErr bool
	}{
		{"https://github.com/o/r/releases/tag/v0.0.47", "v0.0.47", false},
		{"/o/r/releases/tag/v1.2.3-rc1", "v1.2.3-rc1", false},
		{"https://github.com/o/r/releases", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := tagFromLocation(tc.loc)
		if tc.wantErr {
			if err == nil {
				t.Errorf("tagFromLocation(%q) = %q, want an error", tc.loc, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("tagFromLocation(%q): %v", tc.loc, err)
			continue
		}
		if got != tc.want {
			t.Errorf("tagFromLocation(%q) = %q, want %q", tc.loc, got, tc.want)
		}
	}
}

func TestDownloadBinary(t *testing.T) {
	const body = "#!/bin/sh\necho stub\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	tmp, err := downloadBinary(context.Background(), srv.URL+"/ok", dir)
	if err != nil {
		t.Fatalf("downloadBinary: %v", err)
	}
	// Staging must happen in the destination dir, otherwise the later rename is
	// a cross-filesystem move and no longer atomic.
	if filepath.Dir(tmp) != dir {
		t.Errorf("staged in %s, want it inside %s", filepath.Dir(tmp), dir)
	}
	got, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("downloaded %q, want %q", got, body)
	}

	// A missing asset is an error, not an empty file left on disk.
	if _, err := downloadBinary(context.Background(), srv.URL+"/missing", dir); err == nil {
		t.Error("downloadBinary(404) = nil error, want a failure")
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "aiagentmemory")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, ".staged")
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBinaryModeOnly(t, tmp); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(tmp, target); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("target content = %q, want new", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("target mode = %v, want 0755", info.Mode().Perm())
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("staged file still exists after the rename")
	}
}

// verifyBinaryModeOnly applies just the chmod half of verifyBinary, so
// TestReplaceBinary can use a plain file instead of a runnable program.
func verifyBinaryModeOnly(t *testing.T, p string) error {
	t.Helper()
	return os.Chmod(p, 0o755)
}

// TestSelfUpdateEndToEnd drives the whole command against a stub release server:
// resolve the latest tag from a redirect, download the asset, run it to prove it
// works, and swap it over the target — with no config directory involved.
func TestSelfUpdateEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the release build targets unix only")
	}
	asset, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("no release asset for this platform: %v", err)
	}

	const newTag = "v9.9.9"
	// The served "binary" is a shell script so verifyBinary's `--version` run
	// succeeds on any architecture.
	payload := "#!/bin/sh\necho " + newTag + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+newTag, http.StatusFound)
		case "/releases/download/" + newTag + "/" + asset:
			fmt.Fprint(w, payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prev := releaseBaseURL
	releaseBaseURL = srv.URL
	t.Cleanup(func() { releaseBaseURL = prev })

	dir := t.TempDir()
	target := filepath.Join(dir, "aiagentmemory")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := selfUpdate(context.Background(), &out, "", target, false); err != nil {
		t.Fatalf("selfUpdate: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Errorf("target was not replaced: %q", got)
	}
	if !strings.Contains(out.String(), newTag) {
		t.Errorf("output %q does not mention the new tag", out.String())
	}
	if !strings.Contains(out.String(), "untouched") {
		t.Errorf("output %q does not reassure that configs are untouched", out.String())
	}

	// --check reports without writing anything.
	if err := os.WriteFile(target, []byte("sentinel"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := selfUpdate(context.Background(), &out, "", target, true); err != nil {
		t.Fatalf("selfUpdate(--check): %v", err)
	}
	if !strings.Contains(out.String(), "installed:") || !strings.Contains(out.String(), newTag) {
		t.Errorf("--check output = %q, want installed/latest lines", out.String())
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "sentinel" {
		t.Error("--check modified the binary, want a read-only report")
	}

	// A pinned tag that has no asset fails without harming the installed binary.
	if err := selfUpdate(context.Background(), &out, "v0.0.0", target, false); err == nil {
		t.Error("selfUpdate with a missing release = nil error, want a failure")
	}
	after, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "sentinel" {
		t.Error("a failed update replaced the binary anyway")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("leftover staging files in %s: %v", dir, entries)
	}
}
