package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

// doctorCommand builds `doctor`.
//
// ⚠ READ WHAT IT DOES AND DOES NOT REACH BEFORE TRUSTING ITS EXIT CODE. An earlier
// version of this command claimed to tell "a hook whose healthy state is silence"
// from "a hook that cannot speak". It cannot, and neither can anything else that
// runs a hook once: both produce zero bytes. Declaring silence to be muteness does
// not resolve that ambiguity, it relocates it into an exit code — and it made
// `doctor` fail on a healthy install, because the SessionStart verify hook prints
// only when something drifted.
//
// What it DOES reach, and what nothing else in this tree does:
//
//   - REGISTRATION. A script can be installed, correct, and selected by no event.
//     The installer's plan is gated by TestEveryInjectingHookIsOnAnInjectingEvent;
//     the FILE IN FRONT OF AN OPERATOR is gated by nothing, and a settings.json
//     that was hand-edited, copied from another config dir, or written by an older
//     install is the ordinary way this happens.
//   - THE EVENT. Claude Code injects plain stdout for three events only. This hook
//     shipped once on PreCompact, where the recall ran and was written to the debug
//     log. Every test passed, because they all asserted what the script printed.
//   - THE EXIT STATUS. A hook that fails to run is not a hook with nothing to say.
//
// What it does NOT reach: a hook that runs, exits 0, and returns nothing because
// its QUERY is wrong. That is the third recall-hook defect, and no single run can
// see it. What replaced the attempt is cheaper and honest — the hook writes what it
// asked and what came back to stderr, which no event injects, and `doctor` prints
// that verbatim so the operator judges the silence instead of the exit code
// pretending to.
//
// Residual, stated rather than hidden: a hook that prints its own error to stdout
// and exits 0 still reads as `speaks` here. Closing that needs the hook to put the
// fault in its exit status; until it does, the stderr this command prints is where
// that shows up.
func doctorCommand() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "check that the installed hooks are registered, on an injecting event, and able to run, and that the server names its build",
		Description: "Reads the agent's own settings file, finds every registration pointing at a\n" +
			"hook that declares `# hook-output: stdout-injected`, and reports whether each\n" +
			"is selected by an event whose stdout reaches the model — then runs it.\n\n" +
			"Exits non-zero when a hook is installed but registered nowhere, registered on\n" +
			"an event that discards its output, or fails to run; when the bridge binary a\n" +
			"registration names cannot be spawned; and when the server the install points\n" +
			"at reports version `dev` (built unstamped, so nothing can tell it from a stale\n" +
			"one) or does not complete a handshake. Silence is REPORTED and\n" +
			"never failed on: a hook that has nothing to say and a hook that cannot speak\n" +
			"look identical in one run, so the stderr each hook wrote is printed instead.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "agent", Value: "claude", Usage: "which agent's install to check: claude | codex | pi | cursor | claude-desktop (one at a time)"},
			&cli.StringFlag{Name: "target-dir", Usage: "the agent's config directory (default: the installed one)"},
			&cli.StringFlag{Name: "project-dir", Usage: "the repository a hook should look at (default: the working directory)"},
			&cli.StringFlag{Name: "mcp-url", Sources: cli.EnvVars(mcpURLEnvVar), Value: defaultMCPURL, Usage: "agentsmemory MCP endpoint"},
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars(tokenEnvVar), Usage: "workspace token (a --local server needs none)"},
			&cli.DurationFlag{Name: "timeout", Value: 45 * time.Second, Usage: "how long to wait for one hook"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			// The command's own writer, not os.Stdout: a report nothing can capture
			// is a report no test can read, which is how `doctor` would end up
			// reachable by nobody — the defect this command exists to find.
			out := io.Writer(os.Stdout)
			if w := c.Root().Writer; w != nil {
				out = w
			}
			return runHookDoctor(ctx, c, out)
		},
	}
}

// binVerdict is what `doctor` concluded about a stdio bridge's recorded binary.
type binVerdict struct {
	kit      string
	recorded string // the path written into the agent's MCP config
	onPath   string // what resolveServerBin finds today, if anything
	label    string // ok | MISSING | UNREADABLE | NOT-EXECUTABLE | NOT-REGISTERED | STALE-PATH
	detail   string
	bad      bool
}

// judgeServerBin checks the binary an agent's MCP config actually spawns.
//
// onPath is what the operator has on $PATH today, or "" when nothing is there or
// the caller chose not to look. It is a PARAMETER rather than a lookup so this
// judgement is a pure function of its inputs — see reportServerBin for why.
//
// ⚠ THE RECORDED PATH IS FROZEN AT INSTALL TIME AND NOTHING RE-RESOLVES IT. A
// stdio registration stores an absolute path — correctly, because the agent
// launches it from a working directory and PATH that need not match the
// installer's — but "correct at the time" becomes "silently stale" the moment the
// operator builds a newer binary somewhere else. Measured 2026-08-30: Claude
// Desktop had been spawning a five-day-old server from one directory while a
// current one sat on PATH in another. It connected fine and served old code, and
// the only way it was found was listing processes.
//
// Three states are reported, and only the first two are failures the operator can
// act on. STALE-PATH is deliberately NOT fatal on its own: an operator may have
// pointed a kit at a deliberate build, and a check that fails on a legitimate
// choice is one that gets switched off.
//
// ⚠ IT SEES ONLY THE REGISTRATIONS WE WRITE AS FILES, and that limit is worth
// stating because a clean run otherwise reads as full coverage. A kit registered
// by shelling out to the agent's own CLI (`claude mcp add`, `codex mcp add` —
// which is how socket mode registers) stores its command wherever that CLI keeps
// it, in a format this project does not own and must not start parsing. Those
// registrations are kept honest at WRITE time instead, by
// TestEveryStdioRegistrationNamesThePlacedBinary: every one of them names the
// binary the installer placed, so there is no build path to drift away from.
func judgeServerBin(kit agentKit, targetDir, onPath string) *binVerdict {
	if !kitNeedsServerBin(kit) {
		return nil // this kit hands over a URL; there is no binary to drift
	}
	path := filepath.Join(targetDir, kit.mcpConfigFile)
	recorded, err := recordedMCPCommand(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// ⚠ NOT SILENCE. An earlier version returned nil for every unreadable case
		// and explained it away with "the hook report covers the rest" — which is
		// false for exactly the kits this check serves, because they ship no hooks
		// and therefore have no other report. A config that is absent, unparseable,
		// or carries no entry of ours is a finding an operator can act on, and
		// swallowing it made a broken install look like a clean one.
		return &binVerdict{kit: kit.name, label: "NOT-REGISTERED", bad: true, recorded: path,
			detail: "no MCP config at this path — run `aiagentmemory install`"}
	case err != nil:
		return &binVerdict{kit: kit.name, label: "UNREADABLE", bad: true, recorded: path,
			detail: "the MCP config cannot be read or parsed: " + err.Error()}
	case recorded == "":
		return &binVerdict{kit: kit.name, label: "NOT-REGISTERED", bad: true, recorded: path,
			detail: "the MCP config carries no " + mcpName + " entry — run `aiagentmemory install`"}
	}

	v := &binVerdict{kit: kit.name, recorded: recorded, label: "ok", onPath: onPath}

	info, err := os.Stat(recorded)
	switch {
	case errors.Is(err, os.ErrNotExist):
		v.label, v.bad = "MISSING", true
		v.detail = "the registration names a binary that does not exist; the MCP will never connect"
	case err != nil:
		// ⚠ NOT "MISSING". A permission error, a broken mount or a bad symlink is a
		// different problem from a path that names nothing, and reporting them as
		// the same thing sends the operator to re-run install, which will not help.
		v.label, v.bad = "UNREADABLE", true
		v.detail = err.Error()
	case info.IsDir() || !info.Mode().IsRegular():
		// os.Stat on a directory reports the execute bit set, so the mode check
		// below would have called a directory a healthy binary.
		v.label, v.bad = "NOT-EXECUTABLE", true
		v.detail = "the registration names something that is not a regular file"
	case !spawnable(info.Mode(), recorded):
		v.label, v.bad = "NOT-EXECUTABLE", true
		v.detail = "the registration names a file the agent cannot execute"
	default:
		v.label, v.detail = compareWithPath(recorded, v.onPath)
	}
	return v
}

// spawnable reports whether a regular file at path is something the agent can
// execute, and the answer depends on the platform rather than on the mode alone.
//
// The POSIX answer is the execute bit. Go never sets one on Windows — os.Stat
// reports 0666 for every regular file there — so the earlier `Mode()&0o111 == 0`
// test called EVERY bridge binary NOT-EXECUTABLE on that platform, including one
// that answered `--version` and served a full 41-tool MCP handshake seconds later
// (issue #224, second comment). What the Windows loader honours is the extension,
// so that is what is judged there: PATHEXT when set, its documented default when
// not. The platform is a parameter so the Windows branch can be pinned from any
// host, the way serverBinLookupCandidatesOn already is.
func spawnable(mode fs.FileMode, path string) bool {
	return spawnableOn(runtime.GOOS, os.Getenv("PATHEXT"), mode, path)
}

func spawnableOn(goos, pathext string, mode fs.FileMode, file string) bool {
	if goos != "windows" {
		return mode&0o111 != 0
	}
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	// path.Ext, not filepath.Ext: on a POSIX host filepath does not know that a
	// backslash separates, so `C:\odd.dir\bridge` would read as extension `.dir\bridge`.
	ext := strings.ToUpper(path.Ext(strings.ReplaceAll(file, `\`, "/")))
	if ext == "" {
		return false
	}
	for _, e := range strings.Split(pathext, ";") {
		if strings.ToUpper(e) == ext {
			return true
		}
	}
	return false
}

// hookProbePath is the path doctor hands to bash for the hook it probes.
//
// It exists because on Windows the path reached bash with every backslash gone —
// `C:Userszy.claudeagentsmemory-verify-hook.sh` — and doctor reported all three
// injecting hooks FAILED / exit status 127 on an install whose hooks ran to exit 0
// by hand and every session (issue #224). Go serialises argv into one Windows
// command line; MSYS bash, spawned by a non-MSYS parent, parses that line under
// its own rules, where a backslash outside quotes is an escape. The registrations
// Claude Code runs are single-quoted in settings.json, which is why they survive
// and doctor's bare argument did not. Forward slashes are not escapes and MSYS
// bash resolves `C:/Users/...` natively, so the path is converted rather than
// quoted — a quote is one more thing for the same parser to eat. The platform is
// a parameter because filepath.ToSlash on a POSIX host is the identity, and a test
// that called it there would pass over a function that converts nothing.
func hookProbePath(joined string) string { return hookProbePathOn(runtime.GOOS, joined) }

func hookProbePathOn(goos, joined string) string {
	if goos != "windows" {
		return joined
	}
	return strings.ReplaceAll(joined, `\`, "/")
}

// compareWithPath decides whether the registered binary differs from the one the
// operator has on PATH, and it compares CONTENT rather than path strings.
//
// ⚠ COMPARING PATHS REPORTED EVERY HEALTHY INSTALL AS STALE. Since the installer
// began placing the binary it registers, the recorded path is ALWAYS
// <targetDir>/bin/aiagentmemory-server while PATH holds wherever the operator
// built it — so the two differ by construction, on a correct install, every time.
// A check that fires on the normal case is one an operator learns to ignore, and
// it would have shipped that way: the test asserting the healthy case never
// required the verdict to be "ok", so it passed on any label at all. Found in
// review 2026-08-30.
//
// os.SameFile comes first because a hardlink or symlink alias IS the same binary
// however differently the two paths are spelled, and hashing it would be work
// spent to reach the same answer.
func compareWithPath(recorded, onPath string) (label, detail string) {
	if onPath == "" || onPath == recorded {
		return "ok", ""
	}
	ri, rerr := os.Stat(recorded)
	pi, perr := os.Stat(onPath)
	if rerr == nil && perr == nil && os.SameFile(ri, pi) {
		return "ok", ""
	}
	rsum, rerr := sha256Of(recorded)
	psum, perr := sha256Of(onPath)
	if rerr != nil || perr != nil {
		// Cannot tell. Say so rather than guessing in either direction — a wrong
		// "ok" hides drift and a wrong "STALE" is the noise this function exists
		// to remove.
		return "ok", "could not compare with the binary on PATH at " + onPath
	}
	if rsum == psum {
		return "ok", ""
	}
	return "STALE-PATH", "a DIFFERENT build is on PATH at " + onPath +
		" — the registration is frozen at install time, so re-run `aiagentmemory install` if that one is newer"
}

// sha256Of hashes a file by streaming it, so comparing two ~40 MiB binaries does
// not hold both in memory at once.
func sha256Of(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// recordedMCPCommand reads the `command` this project's MCP entry spawns, from an
// agent config file that is shared with every other MCP server the operator runs.
//
// It returns an empty string with a nil error when the file parses but holds no
// entry of ours, because that is a different state from a missing or malformed
// file and the caller reports the three differently. Errors are returned
// unwrapped so the caller can tell os.ErrNotExist from a parse failure.
func recordedMCPCommand(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	return cfg.MCPServers[mcpName].Command, nil
}

// serverVerdict is what `doctor` concluded about the server the install points at.
type serverVerdict struct {
	url     string
	version string
	label   string // ok | unreleased | UNSTAMPED | UNREACHABLE
	detail  string
	bad     bool
}

// probeServerVersion reads the version the endpoint reports in its initialize
// handshake. It is a variable so the doctor tests — which drive the real CLI
// against a --mcp-url that defaults to localhost:8080 — can answer it without a
// server; the default is the real dial, and TestTheServerProbeReadsTheHandshakeVersion
// drives that one against a real Streamable-HTTP server.
var probeServerVersion = dialServerVersion

// dialServerVersion is the default probe: one handshake, the version out of it.
func dialServerVersion(ctx context.Context, url, token string, timeout time.Duration) (string, error) {
	c, res, err := dialMCPInit(ctx, url, token, timeout)
	if err != nil {
		return "", err
	}
	c.Close()
	return res.ServerInfo.Version, nil
}

// judgeServer is issue #210's rung, and it judges the server's IDENTITY rather
// than its health: `/healthz` already answers whether it is up.
//
// An image built without AGENTSMEMORY_VERSION reports the bare word `dev`, and
// every other check an operator has is green for it — the container is healthy,
// the digest matches the image, the needles are present — while the one
// comparison that tells a stale server from a current one (the checkout against
// the served version) is gone. redeploy.sh refuses to BUILD that artifact, but a
// bare `docker compose build` does not, and an operator who deployed that way
// had no route to the fact. `dev-<commit>` is a different case: buildinfo turns
// an unstamped build made inside a repository into a string naming its commit,
// which an operator CAN compare against a checkout, so it is reported and not
// failed. A pure function of its inputs for the reason judgeServerBin is: a
// verdict that depends on ambient environment cannot be pinned by a test.
func judgeServer(url, version string, dialErr error) serverVerdict {
	v := serverVerdict{url: url, version: version}
	switch {
	case dialErr != nil:
		v.label, v.bad = "UNREACHABLE", true
		v.detail = "the install points at an endpoint that did not complete a handshake: " + dialErr.Error()
	case version == "" || version == "dev":
		v.label, v.bad = "UNSTAMPED", true
		v.detail = "the server reports version " + strconv.Quote(version) + ", which names no build: it was " +
			"built without AGENTSMEMORY_VERSION, so nothing can tell it from a stale one (issue #210). " +
			"Rebuild through scripts/redeploy.sh, which stamps it, or pass " +
			"AGENTSMEMORY_VERSION=$(git describe --tags) to BOTH `docker compose build` and `up`"
	case strings.HasPrefix(version, "dev-"):
		v.label = "unreleased"
		v.detail = "a build from a checkout rather than a release tag; the string names its commit, " +
			"so compare it against yours"
	default:
		v.label = "ok"
	}
	return v
}

// installEndpoint resolves the endpoint and credential the INSTALL uses, the same
// way runOneHook does for a hook: the registration's own environment first, the
// --mcp-url/--token flags as the fallback.
//
// ⚠ THE FLAG DEFAULTS TO THE HOSTED ENDPOINT. Judging the server from the flag
// alone would point every self-hosted install's check at a palace its operator
// does not use and report the resulting 401 as the install's condition — the same
// reversal runOneHook's comment records for hooks. The hook registrations are
// what this installer writes, so they are read; the MCP registration itself is
// kept by the agent's own CLI in a format this project does not parse (see
// judgeServerBin). A token that resolves nowhere is passed as empty rather than
// refused here: a loopback server needs none, and a hosted one answers 401, which
// judgeServer reports as UNREACHABLE with the reason attached.
func installEndpoint(c *cli.Command, kit agentKit, dir string) (url, token string) {
	url, token = c.String("mcp-url"), c.String("token")
	if kit.hooksFile != "" {
		if registered, err := registeredHookEvents(filepath.Join(dir, kit.hooksFile)); err == nil {
			names := make([]string, 0, len(registered))
			for name := range registered {
				names = append(names, name)
			}
			sort.Strings(names) // a deterministic winner when two hooks disagree
			for _, name := range names {
				for _, kv := range registered[name].env {
					if v, ok := strings.CutPrefix(kv, mcpURLEnvVar+"="); ok && v != "" {
						url = v
					}
					if v, ok := strings.CutPrefix(kv, tokenEnvVar+"="); ok && v != "" {
						token = v
					}
				}
			}
		}
	}
	if token == "" {
		if t, _, err := resolveWorkspaceToken(c); err == nil {
			token = t
		}
	}
	return url, token
}

// hookVerdict is what `doctor` concluded about one installed hook.
type hookVerdict struct {
	name   string
	events []string // every event this script is registered for, in the settings file
	label  string   // UNREGISTERED | DISCARDED | FAILED | NOT-INSTALLED | silent | speaks
	detail string
	stderr string
	bad    bool // counts toward a non-zero exit
}

// runHookDoctor is the whole command. It is a function rather than a closure so a
// test can drive it, and it takes its writer so the report is capturable.
func runHookDoctor(ctx context.Context, c *cli.Command, out io.Writer) error {
	kits, err := resolveAgentKits(c.String("agent"))
	if err != nil {
		return err
	}
	if len(kits) != 1 {
		return fmt.Errorf("--agent %q names %d agents; check one at a time so a finding "+
			"names the install it belongs to", c.String("agent"), len(kits))
	}
	kit := kits[0]

	// ⚠ THE HOOK GUARDS USED TO ABORT THE WHOLE COMMAND, AND THAT MADE THE BINARY
	// CHECK UNREACHABLE FOR EVERY KIT. judgeServerBin applies exactly when
	// kitNeedsServerBin is true, which today is claude-desktop alone — and
	// claude-desktop ships no hooks file, so `return` here fired before the binary
	// was ever judged. Every kit that DID reach the call has its own CLI binary,
	// so kitNeedsServerBin was false and the call returned nil. The verdict could
	// therefore never be printed by any invocation: finished, tested, and
	// unreachable, which is this repository's signature defect, shipped inside the
	// commit that claimed to report drift. Found in review 2026-08-30; the commit
	// message asserting doctor reports it was false when written.
	//
	// The two checks are independent — one needs a hooks file, the other a stdio
	// registration — so each is skipped on its own terms and only a kit with
	// NEITHER is an error.
	hooksApplicable := kit.hooksFile != "" && kit.shipsCompanionHooks
	binApplicable := kitNeedsServerBin(kit)
	//
	// ⚠ AND THE SECOND BRANCH IS THE FOURTH EMPTY STATE. A kit that ships no
	// injecting hook has an empty universe BY DESIGN, so the guard further down
	// fired on a perfectly healthy codex install and advised re-running `install`,
	// which could not have changed the outcome. `--agent` is advertised as
	// claude | codex | pi in this command's own usage string, so the path was
	// reachable and documented.
	if !hooksApplicable && !binApplicable {
		if kit.hooksFile == "" {
			return fmt.Errorf("%s has no hooks file and spawns no bridge binary, so there is "+
				"no registration to check", kit.name)
		}
		return fmt.Errorf("the %s kit ships no hook that declares `# hook-output: %s`, so there "+
			"is nothing here for this check: %s receives the Stop hook and nothing else, because "+
			"its execution contract for the other events was never captured. This is the designed "+
			"state, not a broken install", kit.name, channelStdoutInjected, kit.name)
	}

	dir := c.String("target-dir")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home: %w", err)
		}
		d, _, _, err := resolveInstallTarget(kit, true, false, "", "", home)
		if err != nil {
			return err
		}
		dir = d
	}

	var verdicts []hookVerdict
	projectDir := c.String("project-dir")
	if projectDir == "" {
		if wd, err := os.Getwd(); err == nil {
			projectDir = wd
		}
	}

	if hooksApplicable {
		verdicts, err = hookVerdictsIn(ctx, c, kit, dir, projectDir)
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "config:  %s\n", dir)
	fmt.Fprintf(out, "project: %s\n\n", projectDir)
	bad := 0
	for _, v := range verdicts {
		if v.bad {
			bad++
		}
		where := "—"
		if len(v.events) > 0 {
			where = strings.Join(v.events, ",")
		}
		fmt.Fprintf(out, "  %-38s %-14s %-12s %s\n", v.name, where, v.label, v.detail)
		// The hook's own account of what it asked and what came back. This is the
		// only thing that separates a healthy silence from a broken one, and this
		// command deliberately prints it rather than guessing between them.
		for _, line := range strings.Split(strings.TrimSpace(v.stderr), "\n") {
			if line != "" {
				fmt.Fprintf(out, "      | %s\n", line)
			}
		}
	}
	// Drift is reported AFTER the per-hook verdicts and never fails the run on
	// its own, for the reason judgeServerBin gives about STALE-PATH: an operator
	// may be running a hand-edited hook deliberately, and a check that fails a
	// legitimate choice is one that gets switched off.
	for _, name := range staleHooksIn(dir) {
		fmt.Fprintf(out, "  %-38s %-14s %-12s %s\n", name, "—", "STALE",
			"differs from this binary's embedded copy — `aiagentmemory install` rewrites it")
	}
	// The server the install points at, judged by its handshake. Asked here,
	// past no guard, for the reason reportServerBin is: a rung behind the hook
	// guards is unreachable for exactly the kit with no hooks.
	url, token := installEndpoint(c, kit, dir)
	ver, perr := probeServerVersion(ctx, url, token, c.Duration("timeout"))
	srv := judgeServer(url, ver, perr)
	if srv.bad {
		bad++
	}
	fmt.Fprintf(out, "  %-38s %-14s %-12s %s\n", "mcp server", "handshake", srv.label, srv.version+" @ "+srv.url)
	if srv.detail != "" {
		fmt.Fprintf(out, "      | %s\n", srv.detail)
	}
	// ADR-057: the peer the protocol depends on, judged from the files above.
	// This call is what SELECTS the rung; TestDoctorReportsTheCodebaseMemoryPeer
	// drives the real CLI, so deleting it leaves no row and the test goes red.
	if reportCodebaseMemory(out, kit, dir) {
		bad++
	}
	return reportServerBin(ctx, out, kit, dir, bad, len(verdicts), srv, c.Duration("timeout"))
}

// hookAssetFiles pairs every embedded hook script with the filename install
// writes it to — every script under the embedded `hooks/` directory, keyed by
// its basename, because install writes each one flat into the config dir under
// exactly that name.
//
// ⚠ IT IS DERIVED, NOT LISTED, AND THE LIST IT REPLACES WAS WRONG BY FOUR. The
// previous version named six scripts by hand; the touched, anchor-cue,
// task-recall and precompact hooks were installed, registered and judged for
// their channel, and never compared against the embedded copy — so an operator
// whose `update` refreshed the binary and left those four behind was told
// nothing had drifted. A list kept beside the embed goes stale one script at a
// time, and each new hook is added by someone who does not know this map
// exists. Reading the embed makes the tenth hook join on the same commit as the
// first. TestEveryShippedHookIsCheckedForDrift edits every script a real
// install writes and requires each to be named. Found 2026-09-05.
func hookAssetFiles() map[string]string {
	out := map[string]string{}
	entries, err := assets.ReadDir("hooks")
	if err != nil {
		return out // no embedded hooks in this build; nothing to compare against
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		out[e.Name()] = "hooks/" + e.Name()
	}
	return out
}

// staleHooksIn reports installed hooks whose bytes differ from this binary's
// embedded copy, sorted so the output is stable.
//
// ⚠ THE BINARY IS HASHED FOR DRIFT AND THE HOOKS WERE NOT, THOUGH THE HOOKS ARE
// WHAT ACTUALLY CARRY THE BEHAVIOUR. judgeServerBin exists because a frozen path
// keeps serving old code; an install directory is the same problem one layer
// out, and worse in one respect — `update` refreshes the BINARY in place and
// says so in its own help text ("configs, sandboxes and MCP registration are
// untouched"), so the supported upgrade path leaves every hook exactly as it
// was. A current binary beside year-old hooks is not an edge case here, it is
// what following the documented instructions produces.
//
// Reported 2026-09-02 from a sandbox install: the Stop hook still carried the
// BSD-first `stat` probe fixed on 2026-08-25, so `stat -f %B` fed a multiline
// filesystem block into an integer comparison on every Linux session. The
// operator's binary was current. `doctor` said only that no hook declared
// `# hook-output:` — true, because that hook predated the declaration line
// itself — which named a symptom three refactors downstream of the cause.
//
// A file that is ABSENT is not drift: kits install different subsets, and an
// unreadable one is reported by the checks that try to run it rather than
// guessed at here.
func staleHooksIn(dir string) []string {
	var stale []string
	for name, asset := range hookAssetFiles() {
		want, err := assets.ReadFile(asset)
		if err != nil {
			continue // not embedded in this build; nothing to compare against
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // absent or unreadable — not this check's finding
		}
		if string(got) != string(want) {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	return stale
}

// hookVerdictsIn scans one install directory and judges every injecting hook in
// it. It is split out of runHookDoctor so the binary check below can run for a
// kit that ships no hooks at all — the conflation that made judgeServerBin
// unreachable.
func hookVerdictsIn(ctx context.Context, c *cli.Command, kit agentKit, dir, projectDir string) ([]hookVerdict, error) {
	scripts, err := injectingScriptsIn(dir)
	if err != nil {
		return nil, err
	}
	// ⚠ FOUR EMPTY STATES, NOT ONE. "nothing is installed", "the declaration
	// changed so this command now examines nothing", "installed but registered
	// nowhere" and "an OLD hook is installed that predates the declaration" are
	// different problems with different fixes, and earlier versions reported the
	// first three as the same alarm and the fourth not at all.
	//
	// The fourth is the one an operator cannot diagnose from the other three,
	// because its symptom is indistinguishable from "nothing is installed" while
	// its fix is the opposite of investigating an empty directory. Naming the
	// drifted files turns it into one instruction.
	if len(scripts) == 0 {
		if stale := staleHooksIn(dir); len(stale) > 0 {
			return nil, fmt.Errorf("no hook in %s declares `# hook-output: %s`, and %d installed "+
				"hook(s) differ from this binary's embedded copies: %s.\n"+
				"  That combination means an OLDER install is on disk — old enough to predate\n"+
				"  the declaration line this check looks for. The binary being current does not\n"+
				"  refresh them: `update` replaces the binary and leaves configs alone.\n"+
				"  Run `aiagentmemory install` to rewrite the hooks",
				dir, channelStdoutInjected, len(stale), strings.Join(stale, ", "))
		}
		return nil, fmt.Errorf("no hook in %s declares `# hook-output: %s`.\n"+
			"  Either nothing is installed there — run `aiagentmemory install` — or the\n"+
			"  declaration line changed, in which case this check now examines nothing\n"+
			"  while reporting success, which is the failure it exists to catch", dir, channelStdoutInjected)
	}

	registered, err := registeredHookEvents(filepath.Join(dir, kit.hooksFile))
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	verdicts := make([]hookVerdict, 0, len(names))
	for _, name := range names {
		verdicts = append(verdicts, judgeHook(ctx, c, dir, name, registered[name], projectDir))
	}
	// ⚠ WIRING IS CHECKED FOR EVERY INSTALLED HOOK, NOT ONLY THE ONES THAT CAN
	// SPEAK. The loop above judges the scripts that declare
	// `stdout-injected` — three of the NINE hooks this kit ships (eleven files,
	// two of which declare `not-a-hook`) — because judging
	// what a hook SAID needs a channel to say it on. Whether a hook is WIRED
	// needs no channel at all, and keying both questions on the declaration put
	// six hooks outside this command's universe: the PreCompact note writer
	// and the PostToolUse touched recorder among them, both `hook-output: none`.
	//
	// The cost was measured on 2026-09-05: a session compacted, no note was
	// written anywhere on the filesystem, the re-ground marker was therefore
	// never written either, the monitor armed by `/am` Step 1d waited on a file
	// nobody would write — and `doctor` exited 0 through all of it. UNREGISTERED
	// is the verdict this command exists to produce (§Reachability records it
	// catching exactly that for three ADR-051 hooks) and it could not reach the
	// hook that needed it.
	//
	// Only the UNREGISTERED case is reported here. A wired non-injecting hook is
	// not run and not judged on its output: it has no channel, so silence is its
	// healthy state, which is the distinction TestDoctorDoesNotFailOnSilence
	// already keeps.
	verdicts = append(verdicts, unwiredHooksIn(dir, scripts, registered)...)
	verdicts = append(verdicts, uninstalledRegistrations(dir, scripts, registered)...)
	return verdicts, nil
}

// uninstalledRegistrations reports hooks settings.json selects that are not on
// disk: the arrow this command was missing.
//
// ⚠ THE SCAN'S UNIVERSE IS THE DIRECTORY, SO A DELETED HOOK LEAVES IT SILENTLY.
// judgeHook is called once per script FOUND by scanning dir for a
// `# hook-output:` declaration. A registration naming a file that is not there
// declares nothing, so it never enters `names`, never gets a verdict, and simply
// lowers the count in the closing line — which then reports "all N injecting
// hook(s) are registered on an injecting event and ran" over a config that selects
// a script the agent cannot run. Measured 2026-09-02: deleting an installed recall
// hook while leaving its SessionStart registration gave exit 0 and that sentence.
//
// Every other state this command reports is "installed, and something about the
// registration is wrong". This is the reverse arrow, and it is unambiguous in a way
// STALE and STALE-PATH are not: an operator may run a hand-edited hook or a
// deliberate build on purpose, but nothing selects a file it means to be absent.
// So it is fatal, where drift is not.
//
// It cannot cry wolf on a foreign hook. `registered` is keyed off
// installerHookPath, the installer's own parser for the command shapes it writes,
// so a config full of unrelated hooks contributes no entries here at all.
func uninstalledRegistrations(dir string, scripts map[string]string, registered map[string]hookRegistration) []hookVerdict {
	names := make([]string, 0, len(registered))
	for name := range registered {
		if _, found := scripts[name]; found {
			continue // installed and already judged
		}
		// ⚠ STAT WHERE THE REGISTRATION POINTS, NOT WHERE WE EXPECT IT. The command
		// carries an absolute path and an operator may legitimately keep hooks in a
		// different config directory; asking about dir/<base> answers a question
		// nobody registered.
		at := registered[name].path
		if at == "" {
			at = filepath.Join(dir, name)
		}
		// A file that exists but declares no channel is a DIFFERENT finding, and
		// the empty-universe branch above already names it. Only absence is ours.
		//
		// ⚠ ONLY ErrNotExist IS ABSENCE. Reading every Stat error that way says "no
		// such file — the agent runs nothing for this event" over an EACCES on a
		// parent, a dangling symlink, or a path on an unmounted volume, and exits
		// non-zero for it. That was minor while this always asked about dir/<base>;
		// it is not, now that the path comes from someone else's config directory,
		// where those states are considerably more likely. Raised in review of #176,
		// and the file already uses this form twice for the bridge binary.
		if _, err := os.Stat(at); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]hookVerdict, 0, len(names))
	for _, name := range names {
		out = append(out, hookVerdict{
			name:   name,
			events: registered[name].events,
			label:  "NOT-INSTALLED",
			detail: "registered as " + registeredPathOf(registered, dir, name) + " but no such file — " +
				"the agent runs nothing for this event; `aiagentmemory install` writes it back",
			bad: true,
		})
	}
	return out
}

// bridgeBuild asks an installed bridge binary what build it is, by running it.
//
// ⚠ THE ISSUE ASKED FOR `vcs.revision` AND THAT PAIR DOES NOT EXIST. Measured on
// a correct install 2026-09-06: the bridge's buildinfo carries vcs.revision (a
// 40-character commit) and no recoverable main.version, while the server exposes
// only the tag string its handshake reports. There is nothing to compare a commit
// with, so the comparable pair is the one both sides stamp the same way — the
// `-X main.version` string, which `--version` prints. It reads the ARTIFACT, which
// is the property that mattered; it is the binary's self-report only in the sense
// that every version string is.
//
// It never chmods: doctor judges an operator's install and must not modify it.
func bridgeBuild(ctx context.Context, path string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := binaryVersionOutput(ctx, path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "agentsmemory version ")), nil
}

// judgeBridgeAgainstServer decides whether an installed bridge is the build the
// server it points at is running, and it reports THREE outcomes rather than two.
//
// ⚠ "COULD NOT COMPARE" IS NOT AGREEMENT, and conflating them is how this whole
// class of defect survives. When the server is UNREACHABLE or UNSTAMPED there is
// no right-hand side, and answering "ok" would report a healthy install on the
// strength of evidence that never arrived — the same reading of absence as a pass
// that TestDoctorDoesNotFailOnSilence exists to refuse. It is reported and not
// failed, because judgeServer has already failed on those states and counting one
// cause twice tells an operator there are two problems.
//
// A pure function of its inputs, for the reason judgeServerBin's comment gives: a
// verdict that depends on ambient environment cannot be pinned by a test.
func judgeBridgeAgainstServer(bridge string, readErr error, srv serverVerdict) (label, detail string, bad bool) {
	switch {
	case readErr != nil:
		return "UNREADABLE", "the registered bridge did not answer --version, so its build is unknown: " +
			readErr.Error(), true
	case bridge == "" || bridge == "dev":
		return "UNSTAMPED", "the bridge reports version " + strconv.Quote(bridge) + ", which names no build, " +
			"so nothing can tell it from a stale one — rebuild it with " +
			"-ldflags \"-X main.version=$(git describe --tags)\" (issue #210 for the server half)", true
	case srv.version == "" || srv.label == "UNREACHABLE" || srv.label == "UNSTAMPED":
		return "no-comparison", "the bridge is " + bridge + "; the server row above says why there is " +
			"nothing to compare it with. This is NOT a clean bill of health for the bridge", false
	case bridge != srv.version:
		return "STALE-BRIDGE", "the bridge is " + bridge + " and the server it points at is " + srv.version +
			". A bridge behind its server speaks an older wire contract inside the agent, where the error " +
			"reads as ours — rebuild and re-run `aiagentmemory install` (issue #207)", true
	default:
		return "ok", "", false
	}
}

// reportServerBin prints the bridge-binary verdict, then the command's summary.
//
// ⚠ IT IS CALLED UNCONDITIONALLY, and that is the whole point. The verdict used to
// be printed from inside runHookDoctor, AFTER two guards that return early for any
// kit without hooks — and the only kit that HAS a bridge binary is exactly a kit
// without hooks. Reporting it from here, past no guard, is what makes it reachable
// at all.
func reportServerBin(ctx context.Context, out io.Writer, kit agentKit, dir string, bad, hooks int, srv serverVerdict, timeout time.Duration) error {
	// ⚠ RESOLVED HERE, NOT INSIDE THE JUDGEMENT. judgeServerBin used to call
	// resolveServerBin itself, which made its verdict depend on the $PATH of
	// whoever ran it — including the developer running the tests, where a real
	// binary in ~/.local/bin turned a controlled fixture's verdict from ok into
	// STALE-PATH. A check whose answer varies with ambient environment cannot be
	// pinned by any test, so the lookup is the caller's and the judgement takes
	// what it is given.
	onPath, err := resolveServerBin("", false)
	if err != nil {
		onPath = "" // nothing on PATH to compare against; not a finding on its own
	}
	bv := judgeServerBin(kit, dir, onPath)
	if bv != nil {
		if bv.bad {
			bad++
		}
		fmt.Fprintf(out, "  %-38s %-14s %-12s %s\n", "mcp bridge binary", bv.kit, bv.label, bv.recorded)
		if bv.detail != "" {
			fmt.Fprintf(out, "      | %s\n", bv.detail)
		}
	}
	// The bridge judged against the SERVER it bridges to, which is the finding
	// nothing else here can reach. judgeServerBin compares the registration with
	// whatever is on PATH, and #204's own case is two copies that are stale
	// TOGETHER — the ordinary outcome, since the installer copies the bridge from
	// the first aiagentmemory-server it finds — where that comparison agrees and
	// reports ok over a bridge a full release behind the server (issue #207).
	if bv != nil && !bv.bad {
		got, verr := bridgeBuild(ctx, bv.recorded, timeout)
		lbl, detail, isBad := judgeBridgeAgainstServer(got, verr, srv)
		if isBad {
			bad++
		}
		fmt.Fprintf(out, "  %-38s %-14s %-12s %s\n", "mcp bridge build", bv.kit, lbl, got)
		if detail != "" {
			fmt.Fprintf(out, "      | %s\n", detail)
		}
	}
	fmt.Fprintln(out)

	if bad > 0 {
		return fmt.Errorf("%d finding(s) across %d injecting hook(s), the bridge binary and the server.\n"+
			"  UNREGISTERED:   the script is installed and %s registers it for no event.\n"+
			"  NOT-INSTALLED:  the reverse — %s registers it and the file is not there, so\n"+
			"                  the agent runs nothing for that event.\n"+
			"  DUPLICATED:     it is registered more than once on one event, so it runs and\n"+
			"                  injects twice — usually a half-finished migration\n"+
			"  DISCARDED:      it is registered on an event whose stdout goes to the debug\n"+
			"                  log; only %s inject.\n"+
			"  FAILED:         it exited non-zero. The stderr above says why.\n"+
			"  MISSING /\n"+
			"  NOT-EXECUTABLE: the MCP registration names a binary that cannot be spawned.\n"+
			"  UNSTAMPED:      the server reports version dev, so nothing can tell it from a\n"+
			"                  stale one; rebuild it stamped (issue #210).\n"+
			"  STALE-BRIDGE:   the bridge binary is a different build from the server it points\n"+
			"                  at; it speaks an older wire contract inside the agent (issue #207).\n"+
			"  UNREACHABLE:    the endpoint this install points at did not complete a handshake.\n"+
			"  Re-running `aiagentmemory install` rewrites the registrations",
			bad, hooks, kit.hooksFile, kit.hooksFile, strings.Join(sortedInjectingEvents(), ", "))
	}
	switch {
	case hooks > 0 && bv != nil:
		fmt.Fprintf(out, "  all %d injecting hook(s) are registered on an injecting event and ran, "+
			"and the bridge binary is %s\n", hooks, bv.label)
	case bv != nil:
		fmt.Fprintf(out, "  this kit ships no injecting hooks; the bridge binary is %s\n", bv.label)
	default:
		fmt.Fprintf(out, "  all %d injecting hook(s) are registered on an injecting event and ran\n", hooks)
	}
	fmt.Fprintf(out, "  the server at %s reports %s (%s)\n", srv.url, srv.version, srv.label)
	return nil
}

// sortedInjectingEvents renders the injecting-event set for a message, in a stable
// order so the text does not churn between runs.
func sortedInjectingEvents() []string {
	out := make([]string, 0, len(injectingEvents))
	for e := range injectingEvents {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// injectingScriptsIn returns the installed scripts that declare the injecting
// channel, keyed by filename.
//
// ⚠ THE UNIVERSE IS THE DIRECTORY AND THE DECLARATION, never a list of filenames
// kept beside it. A hook shipped tomorrow joins this check on the same commit, and
// a hook renamed does not silently shrink it.
func injectingScriptsIn(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w — is anything installed there?", dir, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		m := hookOutputDecl.FindSubmatch(body)
		if m == nil || string(m[1]) != channelStdoutInjected {
			continue
		}
		out[e.Name()] = path
	}
	return out, nil
}

// registeredHookEvents reads the agent's settings file and returns, per script
// filename, every event it is registered for.
//
// ⚠ THIS IS THE HALF THE OLD VERSION NEVER OPENED. It read the hooks DIRECTORY and
// nothing else, so a script present on disk and registered by nothing was reported
// as healthy — "finished, tested, unselected", inside the command written to catch
// exactly that.
//
// A missing settings file is not an error: it is the strongest possible finding,
// namely that every installed hook is registered nowhere. It returns an empty map
// and lets the per-hook verdict say so.
func registeredHookEvents(settingsPath string) (map[string]hookRegistration, error) {
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		// ⚠ ONLY A MISSING FILE IS THE FINDING. Swallowing EVERY read error told an
		// operator with a correct, root-owned or unreadable settings.json that their
		// hooks were registered nowhere — the exact false alarm the parse branch
		// below refuses to produce, four lines away, in the same function.
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]hookRegistration{}, nil
		}
		return nil, fmt.Errorf("read %s: %w — this command refuses to guess at a file it cannot "+
			"read, because reporting \"registered nowhere\" over an unreadable file would be a "+
			"false alarm on an install that may be fine", settingsPath, err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w — this command refuses to guess at a file it "+
			"cannot read, because reporting 'registered nowhere' over a parse failure would "+
			"be a false alarm on a working install", settingsPath, err)
	}
	out := map[string]hookRegistration{}
	// ⚠ SORTED, BECAUSE "THE FIRST REGISTRATION" HAS TO MEAN SOMETHING. doc.Hooks
	// is a map and Go randomises map iteration, so the environment doctor ran a
	// hook with — and therefore its verdict — varied between invocations whenever
	// one script was registered on two events with different prefixes. The shipped
	// installer registers each script on exactly one event, so this only reaches a
	// hand-edited, --copy-ed or older config: precisely the population doctor
	// exists for, and a checker whose answer changes run to run is the one thing it
	// cannot be. Reported by review 2026-08-31.
	events := make([]string, 0, len(doc.Hooks))
	for event := range doc.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	for _, event := range events {
		matchers := doc.Hooks[event]
		for _, m := range matchers {
			for _, h := range m.Hooks {
				// installerHookPath is the installer's own parser for the command
				// shapes it emits, so this stays in step with what install writes.
				path, ok := installerHookPath(h.Command)
				if !ok {
					continue
				}
				name := filepath.Base(path)
				reg := out[name]
				// First registration wins, for the same reason env does below: a
				// script registered twice with different paths is a hand edit this
				// command must not silently average away.
				if reg.path == "" {
					reg.path = path
				}
				if containsString(reg.events, event) {
					// ⚠ THE SAME SCRIPT REGISTERED TWICE ON ONE EVENT (ADR-051 T6).
					// This is what a half-finished migration looks like: the
					// installer wrote a registration and the plugin declared one,
					// so the hook RUNS TWICE and injects twice. It is silent — no
					// error, no duplicate output a reader can spot, because the
					// text was already there once — and no test of the install PLAN
					// can see it, since the plan is only one of the two writers.
					reg.duplicated = append(reg.duplicated, event)
				} else {
					reg.events = append(reg.events, event)
				}
				// The environment the registration carries, so the run below is the
				// registration rather than a reconstruction of it. Taken from the
				// FIRST registration that supplies one: a script registered on
				// several events is written by one install with one prefix, and a
				// later differing one is a hand edit this command must not silently
				// average away.
				if len(reg.env) == 0 {
					reg.env = hookCommandEnv(h.Command)
				}
				out[name] = reg
			}
		}
	}
	for name := range out {
		sort.Strings(out[name].events)
	}
	return out, nil
}

// hookRegistration is what settings.json says about one hook script: the events
// that select it, and the environment its command carries.
//
// ⚠ THE ENV IS HALF THE REGISTRATION, and doctor used to drop it. See
// hookCommandEnv for what that cost on a self-hosted install.
type hookRegistration struct {
	// duplicated lists events on which this script is registered MORE THAN ONCE.
	//
	// A duplicate is not a style problem: the hook runs once per registration, so
	// an injecting hook contributes its context twice. It is invisible in a
	// transcript that already contains the text once, which is why it needs a
	// command to report it rather than a reader to notice.
	duplicated []string

	events []string
	env    []string
	// path is the command's own path, kept whole.
	//
	// ⚠ THE MAP IS KEYED BY BASENAME AND THAT DISCARDS WHERE THE HOOK ACTUALLY IS.
	// Keying by base is right for matching a registration against the scripts found
	// by scanning dir, but uninstalledRegistrations then asked whether dir/<base>
	// exists — so a hook installed in ANOTHER config directory and registered by its
	// real absolute path was reported NOT-INSTALLED, with a message saying the agent
	// runs nothing for that event. The agent runs it fine. Found in review of #171
	// and reproduced: a declaring recall hook in a second directory, registered
	// absolutely, reported missing and exited 1 on a healthy install.
	path string
}

// registeredPathOf names the path the registration actually points at, so the
// report tells an operator where to look rather than where this command guessed.
func registeredPathOf(registered map[string]hookRegistration, dir, name string) string {
	if p := registered[name].path; p != "" {
		return p
	}
	return filepath.Join(dir, name)
}

// containsString reports whether haystack already holds needle.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// judgeHook decides one hook's verdict: registered at all, on an event that
// injects, and able to run.
func judgeHook(ctx context.Context, c *cli.Command, dir, name string, reg hookRegistration, projectDir string) hookVerdict {
	events := reg.events
	v := hookVerdict{name: name, events: events}
	// ⚠ DUPLICATED FIRST (ADR-051 T6). A hook registered twice on one event RUNS
	// TWICE, so an injecting hook contributes its context twice — silently, because
	// the text was already there once. It is reported ahead of every other verdict
	// because those describe a hook that does too little, and this one describes a
	// hook doing too much; an operator who sees "speaks" beside a doubled
	// registration has been told the reassuring half.
	if len(reg.duplicated) > 0 {
		v.label, v.bad = "DUPLICATED", true
		// ⚠ THE MESSAGE NAMES ONLY WHAT THIS COMMAND CAN SEE. It used to say "usually
		// a half-finished migration: the installer wrote one and a plugin declared
		// another" — and that is precisely the state doctor is BLIND to, because
		// registeredHookEvents reads settings.json alone and never opens a plugin's
		// hooks.json. A verdict whose explanation describes a case it cannot reach
		// reads as enforcement over ground it does not cover. Reported by review.
		v.detail = "registered more than once on " + strings.Join(reg.duplicated, ", ") +
			" — it runs once per registration, so it injects twice. This is duplication WITHIN " +
			"settings.json; doctor does not read plugin manifests, so a hook declared by both a " +
			"plugin and this installer is a separate case it cannot see."
		return v
	}
	if len(events) == 0 {
		v.label, v.bad = "UNREGISTERED", true
		v.detail = "installed, and no event runs it"
		return v
	}
	// ⚠ THREE ANSWERS, NOT TWO. A hook on an event this build has never heard of
	// is not the same as one on a documented debug-log event, and saying
	// "DISCARDED" about it is a confident verdict on something unclassified —
	// which is how a wrong answer gets acted on. Claude Code adds events; the
	// honest report for one we do not know is that we do not know.
	if unknown := unknownEvents(events); len(unknown) > 0 && !anyInjecting(events) {
		v.label, v.bad = "UNKNOWN EVENT", true
		v.detail = "registered on " + strings.Join(unknown, ",") +
			", which this build cannot classify — it may or may not reach the model. Check the hooks reference and update injectingEvents/debugLogEvents."
		return v
	}
	if !anyInjecting(events) {
		v.label, v.bad = "DISCARDED", true
		v.detail = "its stdout goes to the debug log on " + strings.Join(events, ",")
		return v
	}
	return runOneHook(ctx, c, dir, name, reg, projectDir)
}

// unknownEvents returns the events this build cannot classify at all — in neither
// the injecting set nor the documented debug-log set.
//
// It exists so an unclassified event is REPORTED rather than silently folded into
// "does not inject". That fold is the same shape as an unlabelled code anchor
// checked against whatever tree was open: an unknown that reads as an answer.
func unknownEvents(events []string) []string {
	var out []string
	for _, e := range events {
		if hookEventChannel(e) == channelUnknown {
			out = append(out, e)
		}
	}
	return out
}

// anyInjecting reports whether at least one of these events puts stdout in front of
// the model. One is enough: a hook registered on both SessionStart and PreCompact
// still reaches a session.
func anyInjecting(events []string) bool {
	for _, e := range events {
		if injectingEvents[e] {
			return true
		}
	}
	return false
}

// runOneHook feeds a hook a synthetic SessionStart payload and reports what it did.
//
// ⚠ IT SETS CLAUDE_PROJECT_DIR. Without it a hook falls back to the process's
// working directory, so the same install reported `speaks` from inside a repository
// and `MUTE` from /tmp, with nothing in the output saying the answer depended on
// where the operator stood.
func runOneHook(ctx context.Context, c *cli.Command, dir, name string, reg hookRegistration, projectDir string) hookVerdict {
	v := hookVerdict{name: name, events: reg.events}
	ctx, cancel := context.WithTimeout(ctx, c.Duration("timeout"))
	defer cancel()

	payload, _ := json.Marshal(map[string]any{
		"session_id": "doctor", "transcript_path": os.DevNull, "cwd": projectDir,
		"hook_event_name": "SessionStart", "source": "startup",
	})

	cmd := exec.CommandContext(ctx, "bash", "--", hookProbePath(filepath.Join(dir, name)))
	cmd.Dir = projectDir
	cmd.Stdin = strings.NewReader(string(payload))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// ⚠ THE REGISTRATION'S OWN ENVIRONMENT WINS, and the flag is the fallback for
	// a hook registered without one. Reversing this is the defect: the flag
	// defaults to the HOSTED endpoint, so doctor pointed every self-hosted
	// install's hook at a palace its operator does not use and then reported the
	// resulting no-credential exit as the install's condition. Appended AFTER the
	// flag value because exec takes the last assignment of a name.
	cmd.Env = append(os.Environ(),
		mcpURLEnvVar+"="+c.String("mcp-url"),
		"CLAUDE_PROJECT_DIR="+projectDir,
	)
	cmd.Env = append(cmd.Env, reg.env...)
	if t := c.String("token"); t != "" {
		cmd.Env = append(cmd.Env, tokenEnvVar+"="+t)
	}

	err := cmd.Run()
	// The stderr is captured and printed whatever the outcome. An earlier version
	// collected it, discarded it, and then told the operator to "run the hook by
	// hand to read its stderr" — which it already had.
	v.stderr = stderr.String()
	n := len(strings.TrimSpace(stdout.String()))
	switch {
	case err != nil:
		v.label, v.bad = "FAILED", true
		v.detail = err.Error()
	case n == 0:
		// NOT a failure. Silence is the designed state of both shipped injecting
		// hooks — the verify hook prints only on drift, the recall hook only when
		// the palace has something for this branch.
		v.label = "silent"
		v.detail = "no output; see its stderr for what it asked"
	default:
		v.label = "speaks"
		v.detail = fmt.Sprintf("%d bytes", n)
	}
	return v
}

// unwiredHooksIn reports every hook INSTALLED in dir that no event runs,
// whatever channel it declares — the half of the wiring question that does not
// need a channel to answer.
//
// judged holds the scripts hookVerdictsIn already reported on, so a hook is
// named once. A file that declares no `# hook-output:` line at all is skipped:
// it predates the declaration and staleHooksIn is what names those, with the
// instruction that actually fixes them.
func unwiredHooksIn(dir string, judged map[string]string, registered map[string]hookRegistration) []hookVerdict {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []hookVerdict
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || judged[name] != "" || !strings.HasSuffix(name, ".sh") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		m := hookOutputDecl.FindSubmatch(body)
		if m == nil {
			continue // predates the declaration; staleHooksIn owns that finding
		}
		channel := string(m[1])
		// A sourced helper and the statusLine command are run by no event by
		// design, and the declaration is where they say so.
		if channel == channelNotAHook {
			continue
		}
		if len(registered[name].events) > 0 {
			continue
		}
		out = append(out, hookVerdict{
			name:  name,
			label: "UNREGISTERED",
			bad:   true,
			detail: "installed, declares `# hook-output: " + channel + "`, and no event runs it — " +
				"it cannot fire, and a hook that never fires is indistinguishable from one with nothing to do. " +
				"Run `aiagentmemory install` to register it",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
