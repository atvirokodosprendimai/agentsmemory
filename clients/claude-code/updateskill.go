package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

// rawBaseURL is where the kit's markdown is fetched from. It is raw.github
// rather than a release asset because the release workflow publishes binaries
// only (see .github/workflows/release.yml) — the protocol and commands live in
// the repository tree, so that tree is what we read them from. It is a var
// purely so tests can point the fetch at a local httptest server.
var rawBaseURL = "https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory"

const (
	// kitAssetDir is the directory inside the repository that holds the kit
	// assets, so an asset's embed-relative name doubles as its path in the tree.
	kitAssetDir = "clients/claude-code"

	// maxAssetBytes caps a single downloaded asset. The largest thing we fetch is
	// the protocol, tens of kilobytes; a megabyte is far above any legitimate
	// value and stops a wrong URL (or a hostile mirror) from streaming without
	// end into memory.
	maxAssetBytes = 1 << 20

	// fetchTimeout bounds the whole download set, not one request — half a dozen
	// small files over one connection should never take this long, and a stalled
	// transfer must fail rather than hang the command.
	fetchTimeout = 60 * time.Second
)

// updateSkillCommand builds `update-skill` — refresh the installed memory
// protocol and slash commands from GitHub.
//
// It is the counterpart to `update`, which replaces the binary and nothing else:
// after upgrading the binary the config dir still carries whatever markdown was
// installed the day the kit went in. This command closes that gap from the other
// side, and just as narrowly — it rewrites the protocol and the commands, and
// deliberately does not re-register the MCP, re-prompt for a token, or touch the
// Stop hook. Re-running `install` is what does all of that.
//
// The Stop hook and the pi bridge extension are excluded on purpose even though
// they are kit assets too: both are executable code, and quietly downloading a
// shell script or a TypeScript extension over an existing one is a materially
// bigger act than refreshing documentation. They come from `install`, which the
// user runs deliberately.
func updateSkillCommand() *cli.Command {
	return &cli.Command{
		Name:  "update-skill",
		Usage: "refresh the installed memory protocol and slash commands from GitHub (binary, MCP registration and token are untouched)",
		Description: "Refresh the global install:      aiagentmemory update-skill\n" +
			"Refresh a sandbox:               aiagentmemory update-skill --sandbox aks\n" +
			"Every agent in that sandbox:     aiagentmemory update-skill --sandbox aks --agent all\n" +
			"See what would change:           aiagentmemory update-skill --check\n" +
			"Track a branch or older tag:     aiagentmemory update-skill --ref main\n\n" +
			"Fetches " + bootstrapFile + " and the slash commands from the release tag\n" +
			"named by --ref (default: the latest release) and writes them into the target\n" +
			"config dir. Your MCP registration, workspace token, Stop hook and the\n" +
			"aiagentmemory binary itself are left alone — use 'aiagentmemory update' for\n" +
			"the binary and 'aiagentmemory install' to re-wire an agent.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "agent",
				Value: agentClaude,
				Usage: "agent CLI to refresh: claude | codex | pi | cursor | claude-desktop | both (claude+codex) | all (every one)",
			},
			&cli.StringFlag{
				Name:  "sandbox",
				Usage: "refresh the isolated config at ~/.sandboxes/<name> instead of the global config dir",
			},
			&cli.StringFlag{
				Name:    "claude-dir",
				Aliases: []string{"config-dir"},
				Usage:   "override the target agent config dir (ignored when --sandbox is set)",
			},
			&cli.BoolFlag{
				Name:  "global",
				Usage: "refresh the agent's global config dir; mutually exclusive with --sandbox/--config-dir",
			},
			&cli.StringFlag{
				Name:  "ref",
				Usage: "release tag or branch to fetch the kit from (default: the latest release)",
			},
			&cli.BoolFlag{
				Name:  "check",
				Usage: "report which files are out of date without writing anything",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			kits, err := resolveAgentKits(c.String("agent"))
			if err != nil {
				return err
			}
			return updateSkills(ctx, os.Stdout, kits, skillUpdate{
				global:    c.Bool("global"),
				sandbox:   c.String("sandbox"),
				configDir: c.String("claude-dir"),
				ref:       c.String("ref"),
				check:     c.Bool("check"),
			})
		},
	}
}

// skillUpdate carries one `update-skill` invocation's options, kept as a struct
// so the work below is callable from a test without building a cli.Command.
type skillUpdate struct {
	global    bool   // pin the kit's global config dir
	sandbox   string // isolated config under ~/.sandboxes
	configDir string // explicit config dir
	ref       string // tag or branch to fetch (empty ⇒ latest release)
	check     bool   // report drift, write nothing
}

// skillAssets are the asset names `update-skill` refreshes: the always-on memory
// protocol plus every slash command. Executable assets (the Stop hook, the pi
// bridge extension) are excluded — see updateSkillCommand.
func skillAssets() []string {
	names := []string{bootstrapAsset}
	for _, c := range commandAssets {
		names = append(names, "commands/"+c)
	}
	return names
}

// updateSkills resolves the ref, downloads the kit once, and applies it to every
// requested agent. The download happens before any target is touched so a
// multi-agent refresh either has the whole kit in hand or has written nothing.
func updateSkills(ctx context.Context, out io.Writer, kits []agentKit, opts skillUpdate) error {
	ref := opts.ref
	if ref == "" {
		var err error
		if ref, err = latestTag(ctx); err != nil {
			return err
		}
	}
	if err := validRef(ref); err != nil {
		return err
	}

	src, err := fetchAssets(ctx, ref, skillAssets())
	if err != nil {
		return err
	}

	for _, kit := range kits {
		targetDir, sandboxName, _, err := resolveInstallTarget(
			kit, opts.global, false, opts.sandbox, opts.configDir, homeDir())
		if err != nil {
			return err
		}
		// A config dir that was never installed into is almost always a typo'd
		// --sandbox. Writing commands into a fresh directory would "succeed" while
		// leaving an agent that has no MCP registered and no hook, so say what is
		// actually wrong instead.
		if !dirExists(targetDir) {
			return fmt.Errorf("no %s config at %s — install it first with `aiagentmemory install --agent %s%s`",
				kit.name, targetDir, kit.name, sandboxFlagHint(sandboxName))
		}

		fmt.Fprintf(out, "\n> %s  %s\n", kit.name, targetDir)
		if opts.check {
			reportSkillDrift(out, kit, targetDir, src)
			continue
		}
		if err := applySkillUpdate(out, kit, targetDir, src); err != nil {
			return err
		}
	}

	if opts.check {
		fmt.Fprintf(out, "\nchecked against %s — run without --check to apply\n", ref)
		return nil
	}
	fmt.Fprintf(out, "\nkit refreshed from %s. Restart the agent to pick it up.\n", ref)
	fmt.Fprintln(out, "the binary, MCP registration and workspace token were left untouched")
	return nil
}

// sandboxFlagHint renders the --sandbox part of an install hint, so the error
// above names the sandbox the user actually asked for rather than a generic
// command they would have to adapt.
func sandboxFlagHint(name string) string {
	if name == "" {
		return ""
	}
	return " --sandbox " + name
}

// applySkillUpdate writes the fetched kit into one agent's config dir, reusing
// the installer's own write paths so the protocol lands the way that agent needs
// it — imported on Claude, inlined into AGENTS.md on codex and pi.
func applySkillUpdate(out io.Writer, kit agentKit, targetDir string, src assetSource) error {
	inst := &Installer{kit: kit, targetDir: targetDir, out: out, src: src}
	if err := inst.writeCommands(); err != nil {
		return fmt.Errorf("writing %s commands: %w", kit.name, err)
	}
	if err := inst.registerMemoryBootstrap(); err != nil {
		return fmt.Errorf("installing %s memory protocol: %w", kit.name, err)
	}
	return nil
}

// reportSkillDrift prints, per asset, whether the fetched copy differs from what
// is installed. It backs --check, so it writes nothing.
//
// Only the files we own are compared. On codex and pi the protocol is also
// inlined into the agent's own memory file, but that block is regenerated from
// this sibling copy, so the sibling is a faithful proxy for it.
func reportSkillDrift(out io.Writer, kit agentKit, targetDir string, src assetSource) {
	for _, name := range skillAssets() {
		path := installedPath(kit, targetDir, name)
		want, err := src.ReadFile(name)
		if err != nil {
			fmt.Fprintf(out, "  [!!] %s: %v\n", filepath.Base(path), err)
			continue
		}
		have, err := os.ReadFile(path)
		switch {
		case err != nil:
			fmt.Fprintf(out, "  [->] %s (missing — would be added)\n", filepath.Base(path))
		case bytes.Equal(have, want):
			fmt.Fprintf(out, "  [ok] %s (up to date)\n", filepath.Base(path))
		default:
			fmt.Fprintf(out, "  [->] %s (would be updated)\n", filepath.Base(path))
		}
	}
}

// installedPath maps an asset name to where that asset lives inside a target
// config dir. The protocol is installed under its own name beside the agent's
// memory file; commands go into the kit's commands dir (commands/ on Claude,
// prompts/ on codex and pi).
func installedPath(kit agentKit, targetDir, asset string) string {
	if name, ok := strings.CutPrefix(asset, "commands/"); ok {
		return filepath.Join(targetDir, kit.commandsDir, name)
	}
	return filepath.Join(targetDir, bootstrapFile)
}

// validRef rejects a ref that could escape the repository path in the fetch URL.
// A tag or branch name never needs "..", and letting one through would let
// --ref rewrite which file is downloaded rather than which version of it.
func validRef(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, ".") {
		return fmt.Errorf("invalid --ref %q: name a release tag or branch, e.g. v0.0.72 or main", ref)
	}
	for _, seg := range strings.Split(ref, "/") {
		if seg == "" || seg == ".." {
			return fmt.Errorf("invalid --ref %q: name a release tag or branch, e.g. v0.0.72 or main", ref)
		}
	}
	if strings.ContainsAny(ref, " \t\r\n?#%\\") {
		return fmt.Errorf("invalid --ref %q: name a release tag or branch, e.g. v0.0.72 or main", ref)
	}
	return nil
}

// remoteAssets is an assetSource backed by files already downloaded from the
// repository tree. Everything is fetched up front (see fetchAssets), so ReadFile
// never touches the network — which is what lets the installer's write paths run
// unchanged against it, and what guarantees a failed download cannot leave a
// config dir holding two new commands and one stale one.
type remoteAssets struct {
	ref   string            // the tag or branch these came from, for messages
	files map[string][]byte // asset name → contents
}

// ReadFile returns a previously fetched asset. A name that was not fetched is a
// programming error rather than a network condition, so it reads as one.
func (r *remoteAssets) ReadFile(name string) ([]byte, error) {
	data, ok := r.files[name]
	if !ok {
		return nil, fmt.Errorf("asset %q was not fetched from %s", name, r.ref)
	}
	return data, nil
}

// fetchAssets downloads every named asset at ref and returns them as a source.
// All-or-nothing on purpose: the caller writes into a live config dir, and a
// half-applied kit (new protocol, old commands) is harder to notice and harder
// to recover from than an update that plainly failed.
func fetchAssets(ctx context.Context, ref string, names []string) (*remoteAssets, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	files := make(map[string][]byte, len(names))
	for _, name := range names {
		url := fmt.Sprintf("%s/%s/%s/%s", rawBaseURL, ref, kitAssetDir, name)
		data, err := fetchAsset(ctx, url)
		if err != nil {
			return nil, err
		}
		files[name] = data
	}
	return &remoteAssets{ref: ref, files: files}, nil
}

// fetchAsset downloads one asset, bounding both the status it accepts and the
// size it will hold in memory.
func fetchAsset(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%s not found — check --ref names a real tag or branch", url)
		}
		return nil, fmt.Errorf("cannot fetch %s: %s", url, resp.Status)
	}

	// Read one byte past the cap so an oversized body is detected rather than
	// silently truncated into a plausible-looking file.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", url, err)
	}
	if len(data) > maxAssetBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes — refusing it", url, maxAssetBytes)
	}
	// An empty file would install cleanly and silently disable the command or the
	// protocol it replaced, so it is treated as a failed fetch.
	if len(data) == 0 {
		return nil, fmt.Errorf("%s is empty — refusing to install it over your kit", url)
	}
	return data, nil
}
