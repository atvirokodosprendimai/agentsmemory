package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
		Usage: "check that the installed hooks are registered, on an injecting event, and able to run",
		Description: "Reads the agent's own settings file, finds every registration pointing at a\n" +
			"hook that declares `# hook-output: stdout-injected`, and reports whether each\n" +
			"is selected by an event whose stdout reaches the model — then runs it.\n\n" +
			"Exits non-zero when a hook is installed but registered nowhere, registered on\n" +
			"an event that discards its output, or fails to run. Silence is REPORTED and\n" +
			"never failed on: a hook that has nothing to say and a hook that cannot speak\n" +
			"look identical in one run, so the stderr each hook wrote is printed instead.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "agent", Value: "claude", Usage: "which agent's install to check: claude | codex | pi"},
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

// hookVerdict is what `doctor` concluded about one installed hook.
type hookVerdict struct {
	name   string
	events []string // every event this script is registered for, in the settings file
	label  string   // UNREGISTERED | DISCARDED | FAILED | silent | speaks
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
	if kit.hooksFile == "" {
		return fmt.Errorf("%s has no hooks file, so there is no registration to check", kit.name)
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

	scripts, err := injectingScriptsIn(dir)
	if err != nil {
		return err
	}
	// ⚠ THREE EMPTY STATES, NOT ONE. "nothing is installed", "the declaration
	// changed so this command now examines nothing", and "installed but registered
	// nowhere" are different problems with different fixes, and an earlier version
	// reported all three as the same alarm.
	if len(scripts) == 0 {
		return fmt.Errorf("no hook in %s declares `# hook-output: %s`.\n"+
			"  Either nothing is installed there — run `aiagentmemory install` — or the\n"+
			"  declaration line changed, in which case this check now examines nothing\n"+
			"  while reporting success, which is the failure it exists to catch", dir, channelStdoutInjected)
	}

	registered, err := registeredHookEvents(filepath.Join(dir, kit.hooksFile))
	if err != nil {
		return err
	}

	projectDir := c.String("project-dir")
	if projectDir == "" {
		if wd, err := os.Getwd(); err == nil {
			projectDir = wd
		}
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
	fmt.Fprintln(out)

	if bad > 0 {
		return fmt.Errorf("%d of %d injecting hook(s) cannot reach a session.\n"+
			"  UNREGISTERED: the script is installed and %s registers it for no event.\n"+
			"  DISCARDED:    it is registered on an event whose stdout goes to the debug\n"+
			"                log; only %s inject.\n"+
			"  FAILED:       it exited non-zero. The stderr above says why.\n"+
			"  Re-running `aiagentmemory install` rewrites the registrations",
			bad, len(verdicts), kit.hooksFile, strings.Join(sortedInjectingEvents(), ", "))
	}
	fmt.Fprintf(out, "  all %d injecting hook(s) are registered on an injecting event and ran\n", len(verdicts))
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
func registeredHookEvents(settingsPath string) (map[string][]string, error) {
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return map[string][]string{}, nil
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
	out := map[string][]string{}
	for event, matchers := range doc.Hooks {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				// installerHookPath is the installer's own parser for the command
				// shapes it emits, so this stays in step with what install writes.
				path, ok := installerHookPath(h.Command)
				if !ok {
					continue
				}
				name := filepath.Base(path)
				if !containsString(out[name], event) {
					out[name] = append(out[name], event)
				}
			}
		}
	}
	for name := range out {
		sort.Strings(out[name])
	}
	return out, nil
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
func judgeHook(ctx context.Context, c *cli.Command, dir, name string, events []string, projectDir string) hookVerdict {
	v := hookVerdict{name: name, events: events}
	if len(events) == 0 {
		v.label, v.bad = "UNREGISTERED", true
		v.detail = "installed, and no event runs it"
		return v
	}
	if !anyInjecting(events) {
		v.label, v.bad = "DISCARDED", true
		v.detail = "its stdout goes to the debug log on " + strings.Join(events, ",")
		return v
	}
	return runOneHook(ctx, c, dir, name, events, projectDir)
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
func runOneHook(ctx context.Context, c *cli.Command, dir, name string, events []string, projectDir string) hookVerdict {
	v := hookVerdict{name: name, events: events}
	ctx, cancel := context.WithTimeout(ctx, c.Duration("timeout"))
	defer cancel()

	payload, _ := json.Marshal(map[string]any{
		"session_id": "doctor", "transcript_path": os.DevNull, "cwd": projectDir,
		"hook_event_name": "SessionStart", "source": "startup",
	})

	cmd := exec.CommandContext(ctx, "bash", "--", filepath.Join(dir, name))
	cmd.Dir = projectDir
	cmd.Stdin = strings.NewReader(string(payload))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Env = append(os.Environ(),
		mcpURLEnvVar+"="+c.String("mcp-url"),
		"CLAUDE_PROJECT_DIR="+projectDir,
	)
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
