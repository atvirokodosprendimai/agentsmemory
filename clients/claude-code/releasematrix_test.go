package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// releaseMatrixPattern reads one `- { goos: X, goarch: Y }` row of a workflow
// matrix. Regexp rather than a YAML parser because this repo depends on none,
// and the rows are written on one line by the workflows themselves.
var releaseMatrixPattern = regexp.MustCompile(`-\s*\{\s*goos:\s*(\w+),\s*goarch:\s*(\w+)\s*\}`)

// TestReleaseMatrixCoversEveryPlatformUpdateAccepts pins the two halves of a
// published binary against each other.
//
// ⚠ ONE HALF PROMISED WHAT THE OTHER NEVER BUILT. `update` computed a download URL
// for any of {darwin,linux}×{amd64,arm64} while release.yml published four
// specific rows, and nothing compared them — so a platform in one and not the
// other is a 404 partway through an upgrade, or an asset nothing ever requests.
// Windows was the live case: build.yml already cross-compiles windows/amd64 for
// ./cmd/server on the same runner, release.yml simply never carried the row, and
// a Windows operator could not install at all. Reported 2026-08-31 from a first
// Windows install.
//
// Deriving BOTH sides is what makes it a gate rather than a second list to
// maintain: a platform added to either joins the check on the same commit.
func TestReleaseMatrixCoversEveryPlatformUpdateAccepts(t *testing.T) {
	published := releaseMatrixPlatforms(t)
	accepted := make(map[string]bool, len(publishedPlatforms))
	for p := range publishedPlatforms {
		accepted[p] = true
	}

	for p := range published {
		if !accepted[p] {
			t.Errorf("release.yml publishes an aiagentmemory binary for %s that `update` will "+
				"never request — assetName rejects it, so the asset is built and downloaded by "+
				"nobody", p)
		}
	}
	for p := range accepted {
		if !published[p] {
			t.Errorf("assetName accepts %s but release.yml publishes no such asset — `update` "+
				"builds a download URL that 404s partway through an upgrade, which is the "+
				"failure its own comment says it exists to prevent", p)
		}
	}
	if len(published) == 0 {
		t.Fatal("no matrix rows parsed out of release.yml, so this test would pass whatever " +
			"either side said — the pattern no longer matches the workflow's shape")
	}
	t.Logf("release.yml and assetName agree on %d platform(s): %s",
		len(published), strings.Join(sortedKeys(published), " "))
}

// TestTheWindowsAssetCarriesItsSuffixEverywhere covers the third copy of the same
// fact, which is where this class of defect actually lives.
//
// The platform set can agree while the NAMES disagree: Windows assets carry .exe,
// so a release that publishes `aiagentmemory-windows-amd64.exe` and an installer
// that downloads `aiagentmemory-windows-amd64` are as broken as a missing row.
// install.sh is the third writer of that name and cannot import anything.
func TestTheWindowsAssetCarriesItsSuffixEverywhere(t *testing.T) {
	name, err := assetName("windows", "amd64")
	if err != nil {
		t.Fatalf("assetName rejects windows/amd64: %v", err)
	}
	if !strings.HasSuffix(name, ".exe") {
		t.Errorf("assetName returned %q for windows — the published asset carries .exe, so this "+
			"URL 404s", name)
	}

	root := filepath.Clean("../..")
	for _, f := range []string{
		filepath.Join(root, ".github", "workflows", "release.yml"),
		filepath.Join(root, "clients", "claude-code", "install.sh"),
	} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !strings.Contains(string(raw), ".exe") {
			t.Errorf("%s never mentions .exe, so it cannot be naming the Windows asset the "+
				"other two halves agree on", filepath.Base(f))
		}
	}
}

// releaseMatrixPlatforms returns the goos/goarch pairs release.yml builds
// aiagentmemory binaries for.
//
// ⚠ IT READS ONLY THE `binaries` JOB. release.yml carries other matrices, and a
// version that swept the whole file would silently widen its universe — passing
// because some other job happened to name the platform, which is the shape of a
// gate that has stopped checking anything.
func releaseMatrixPlatforms(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(filepath.Clean("../.."), ".github", "workflows", "release.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	const job = "\n  binaries:"
	start := strings.Index(body, job)
	if start < 0 {
		t.Fatalf("release.yml has no `binaries:` job — this gate reads a workflow shape that " +
			"no longer exists, so it would report agreement over nothing")
	}
	rest := body[start+len(job):]
	// The job ends at the next top-level key (two-space indent under `jobs:`).
	if end := regexp.MustCompile(`\n  [a-z][\w-]*:`).FindStringIndex(rest); end != nil {
		rest = rest[:end[0]]
	}
	out := map[string]bool{}
	for _, m := range releaseMatrixPattern.FindAllStringSubmatch(rest, -1) {
		out[fmt.Sprintf("%s/%s", m[1], m[2])] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
