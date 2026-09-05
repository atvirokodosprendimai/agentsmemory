package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

// releaseBaseURL is where the CLI's release assets live. It is a var rather than
// a const purely so tests can point the updater at a local httptest server.
var releaseBaseURL = "https://github.com/atvirokodosprendimai/agentsmemory"

const (
	// latestTimeout bounds the "which tag is newest" redirect lookup — one
	// request, no body, so a short leash is enough to fail fast when offline.
	latestTimeout = 15 * time.Second

	// downloadTimeout bounds fetching the asset itself (a few MB), generous
	// enough for a slow link but not an indefinite hang.
	downloadTimeout = 5 * time.Minute
)

// updateCommand builds `update` — replace the aiagentmemory binary in place.
//
// It deliberately does only that: no config dir is written, no MCP server is
// re-registered, no token is prompted for. Re-running `install` would redo all
// of that, which is exactly what you do not want when the only stale thing is
// the binary itself.
func updateCommand() *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "update the aiagentmemory binary in place (configs, sandboxes and MCP registration are untouched)",
		Description: "Upgrade to the latest release:   aiagentmemory update\n" +
			"See what is available:           aiagentmemory update --check\n" +
			"Pin or roll back to a tag:       aiagentmemory update --version v0.0.46\n\n" +
			"Only the binary is replaced. Nothing under ~/.claude or ~/.sandboxes is\n" +
			"read or written, so your commands, settings, sandboxes, MCP registration\n" +
			"and workspace token survive the upgrade untouched.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "version",
				Sources: cli.EnvVars("AIAGENTMEMORY_VERSION"),
				Usage:   "release tag to install (default: the latest release) — also how you roll back",
			},
			&cli.BoolFlag{
				Name:  "check",
				Usage: "report the installed and latest versions without downloading anything",
			},
			&cli.StringFlag{
				Name:  "bin",
				Usage: "binary to replace (default: the running aiagentmemory)",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return selfUpdate(ctx, os.Stdout, c.String("version"), c.String("bin"), c.Bool("check"))
		},
	}
}

// selfUpdate downloads the requested release and swaps it over the running
// binary. The order matters: resolve the tag, download beside the target, prove
// the new file actually runs, and only then rename it into place — so a failed
// or truncated download leaves the working binary untouched.
func selfUpdate(ctx context.Context, out io.Writer, tag, binOverride string, checkOnly bool) error {
	asset, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	if checkOnly {
		latest, err := latestTag(ctx)
		if err != nil {
			return err
		}
		return reportVersions(out, version, latest)
	}

	if tag == "" || tag == "latest" {
		if tag, err = latestTag(ctx); err != nil {
			return err
		}
	}
	// version is stamped at build time; a dev build never matches a tag, so it
	// always updates. Re-installing the same tag is a no-op worth skipping.
	if tag == version {
		fmt.Fprintf(out, "aiagentmemory %s is already installed — nothing to do\n", version)
		return nil
	}

	target, err := selfPath(binOverride)
	if err != nil {
		return err
	}

	tmp, err := downloadBinary(ctx, releaseAssetURL(tag, asset), filepath.Dir(target))
	if err != nil {
		return err
	}
	// Cleans up the staged file on any failure below; a no-op once renamed.
	defer os.Remove(tmp)

	if err := verifyBinary(ctx, tmp); err != nil {
		return err
	}
	if err := replaceBinary(tmp, target); err != nil {
		return err
	}

	fmt.Fprintf(out, "updated %s: %s -> %s\n", target, version, tag)
	fmt.Fprintln(out, "your Claude config, sandboxes and MCP registration were left untouched")
	return nil
}

// reportVersions prints the --check summary: what is installed, what is
// published, and what to do about it.
func reportVersions(out io.Writer, installed, latest string) error {
	fmt.Fprintf(out, "installed: %s\nlatest:    %s\n", installed, latest)
	if installed == latest {
		fmt.Fprintln(out, "up to date")
		return nil
	}
	fmt.Fprintln(out, "run `aiagentmemory update` to upgrade")
	return nil
}

// assetName maps a Go platform to the release asset the build publishes (see
// .github/workflows/release.yml). Platforms without a published build fail here
// with a clear message instead of a 404 halfway through a download.
func assetName(goos, goarch string) (string, error) {
	if !publishedPlatforms[goos+"/"+goarch] {
		return "", fmt.Errorf("no published aiagentmemory build for %s/%s — build it from source with `go build ./clients/claude-code`", goos, goarch)
	}
	name := fmt.Sprintf("aiagentmemory-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

// serverAssetName is assetName for the server binary the same release publishes
// beside the client — the mcp-stdio bridge a Claude Desktop install spawns. It
// shares publishedPlatforms because release.yml builds both from one matrix.
func serverAssetName(goos, goarch string) (string, error) {
	if !publishedPlatforms[goos+"/"+goarch] {
		return "", fmt.Errorf("no published aiagentmemory-server build for %s/%s — build it from source with `go build ./cmd/server`", goos, goarch)
	}
	name := fmt.Sprintf("aiagentmemory-server-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

// serverArchiveName is the versioned package the release publishes for the
// server — `agentsmemory_<os>_<arch>.tar.gz` (`.zip` on Windows) — which is the
// form SHA256SUMS.txt lists. The bare aiagentmemory-server-<os>-<arch> asset is
// uploaded by a separate job with no sum beside it (measured on v0.0.115's
// SHA256SUMS.txt, five archive lines and nothing else), so a fetch that wants to
// verify what it runs takes the archive and extracts the binary.
func serverArchiveName(goos, goarch string) (string, error) {
	if !publishedPlatforms[goos+"/"+goarch] {
		return "", fmt.Errorf("no published agentsmemory package for %s/%s — build it from source with `go build ./cmd/server`", goos, goarch)
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("agentsmemory_%s_%s%s", goos, goarch, ext), nil
}

// publishedPlatforms is exactly the set release.yml's `binaries` matrix builds.
//
// ⚠ IT IS A SET OF PAIRS, NOT TWO INDEPENDENT SWITCHES, and that is the whole
// point. The previous version accepted any OS crossed with any arch, so adding
// windows would silently have promised a windows/arm64 asset the release does not
// publish — a 404 partway through an upgrade, which is the failure assetName's own
// comment says it exists to prevent. TestReleaseMatrixCoversEveryPlatformUpdateAccepts
// derives both sides and fails when they drift, so a platform added to either
// joins the check on the same commit.
var publishedPlatforms = map[string]bool{
	"linux/amd64":   true,
	"linux/arm64":   true,
	"darwin/amd64":  true,
	"darwin/arm64":  true,
	"windows/amd64": true,
}

// releaseAssetURL is the direct download URL for one asset of one release.
func releaseAssetURL(tag, asset string) string {
	return fmt.Sprintf("%s/releases/download/%s/%s", releaseBaseURL, tag, asset)
}

// latestTag asks GitHub which release is newest by following the
// /releases/latest redirect and reading the tag out of the Location header. The
// redirect is used rather than the REST API because it needs no token and is not
// subject to the 60 requests/hour anonymous API rate limit.
func latestTag(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, latestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseBaseURL+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		// Stop at the first redirect — its Location is the answer.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach GitHub Releases: %w", err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("cannot resolve the latest release: %s answered %s with no redirect", req.URL, resp.Status)
	}
	return tagFromLocation(loc)
}

// tagFromLocation pulls vX.Y.Z out of a .../releases/tag/vX.Y.Z redirect target.
func tagFromLocation(loc string) (string, error) {
	const marker = "/releases/tag/"
	u, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("cannot parse the latest-release redirect %q: %w", loc, err)
	}
	i := strings.Index(u.Path, marker)
	if i < 0 {
		return "", fmt.Errorf("unexpected latest-release redirect %q", loc)
	}
	tag := strings.Trim(u.Path[i+len(marker):], "/")
	if tag == "" {
		return "", fmt.Errorf("unexpected latest-release redirect %q", loc)
	}
	return tag, nil
}

// selfPath resolves which file to overwrite: the --bin override, else the
// running executable. Symlinks are resolved so that updating a binary reached
// through a symlink rewrites the real file instead of clobbering the link.
func selfPath(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot locate the running aiagentmemory binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s: %w", exe, err)
	}
	return resolved, nil
}

// downloadBinary fetches url into a temp file inside dir and returns its path.
// The staging file lives in the destination directory on purpose: os.Rename is
// only atomic within a single filesystem, and the system temp dir is often a
// different mount.
func downloadBinary(ctx context.Context, url, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s returned %s", url, resp.Status)
	}

	f, err := os.CreateTemp(dir, ".aiagentmemory-update-*")
	if err != nil {
		return "", fmt.Errorf("cannot stage the download in %s: %w — re-run with sudo, or use --bin <path> to update a copy you own", dir, err)
	}
	tmp := f.Name()
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("download failed: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// verifyBinary makes the staged file executable and runs it with --version. A
// truncated download or a wrong-architecture asset fails here, while the working
// binary is still in place — which is the whole point of checking before the
// swap rather than after it.
func verifyBinary(ctx context.Context, p string) error {
	_, err := verifyBinaryOutput(ctx, p)
	return err
}

// verifyBinaryOutput is verifyBinary keeping what --version printed, for a
// caller that judges IDENTITY as well as liveness: the Desktop bridge fetch
// requires the output to name the release tag, and reading it here means one
// exec rather than two — and no second exec whose failure could skip the check.
func verifyBinaryOutput(ctx context.Context, p string) (string, error) {
	if err := os.Chmod(p, 0o755); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, p, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("the downloaded binary does not run (%w): %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// replaceBinary swaps the staged file over the target. Renaming within a
// filesystem is atomic, so an interrupted update can never leave a half-written
// binary behind; replacing the file of a *running* process is safe on Unix
// because the running image keeps the old inode until it exits.
func replaceBinary(tmp, target string) error {
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("cannot replace %s: %w — re-run with sudo, or use --bin <path> to update a copy you own", target, err)
	}
	return nil
}
