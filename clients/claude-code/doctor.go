package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

// hookOutputDeclRE reads the `# hook-output:` line every shipped hook carries.
var hookOutputDeclRE = regexp.MustCompile(`(?m)^#\s*hook-output:\s*([a-z-]+)`)

// doctorCommand builds `doctor`.
//
// ⚠ IT CHECKS THE RUNG NO OTHER GATE COVERS: whether an installed hook actually
// SAYS ANYTHING. The tree already proves a hook is registered
// (TestReadmeNamesEveryHookEventTheInstallerRegisters), that it sits on an event
// whose stdout reaches the model (TestEveryInjectingHookIsOnAnInjectingEvent), and
// that it declares its channel honestly (TestEveryHookScriptDeclaresItsOutputChannel).
// All three passed while the recall hook emitted NOTHING for months, across two
// separate repair attempts, because none of them runs the hook against a real
// palace — and a hook whose healthy state is silence looks identical to a hook that
// cannot speak.
//
// That is the same shape as `doctor --corpus`: a check an operator can run, whose
// exit code is the claim, over state no hermetic test can reach.
func doctorCommand() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "check that the installed hooks can actually speak",
		Description: "Runs every installed hook that declares `# hook-output: stdout-injected`\n" +
			"against the live server and reports whether it produced anything.\n\n" +
			"A hook of that kind is the only way memory reaches a session unbidden, and\n" +
			"its healthy state is silence — so a hook that CANNOT speak is invisible\n" +
			"until someone runs it by hand. Exits non-zero when one is mute.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "agent", Value: "claude", Usage: "which agent's install to check: claude | codex | pi"},
			&cli.StringFlag{Name: "target-dir", Usage: "where the hooks are installed (default: the agent's config directory)"},
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

// hookVerdict is one hook's result.
type hookVerdict struct {
	name, channel string
	bytes         int
	err           error
}

// runHookDoctor runs each injecting hook and reports what it emitted.
func runHookDoctor(ctx context.Context, c *cli.Command, out io.Writer) error {
	dir := c.String("target-dir")
	if dir == "" {
		kits, err := resolveAgentKits(c.String("agent"))
		if err != nil {
			return err
		}
		if len(kits) != 1 {
			return fmt.Errorf("--agent %q names %d agents; check one at a time so a mute hook "+
				"names the install it belongs to", c.String("agent"), len(kits))
		}
		kit := kits[0]
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

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w — is anything installed there?", dir, err)
	}

	var verdicts []hookVerdict
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "agentsmemory-") || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		m := hookOutputDeclRE.FindSubmatch(body)
		if m == nil || string(m[1]) != "stdout-injected" {
			continue // only an injecting hook's silence is invisible; the rest report elsewhere
		}
		verdicts = append(verdicts, runOneHook(ctx, dir, e.Name(), c))
	}

	// A check that examined nothing is not a passing check. Guarding on this
	// because it is the exact failure the whole command exists to catch, one level
	// up: an empty universe reporting all-clear.
	if len(verdicts) == 0 {
		return fmt.Errorf("no hook in %s declares `# hook-output: stdout-injected` — either "+
			"nothing is installed there, or the declaration changed and this check now examines "+
			"nothing while reporting success", dir)
	}

	mute := 0
	fmt.Fprintf(out, "hooks: %s\n\n", dir)
	for _, v := range verdicts {
		switch {
		case v.err != nil:
			mute++
			fmt.Fprintf(out, "  %-42s FAILED   %v\n", v.name, v.err)
		case v.bytes == 0:
			mute++
			fmt.Fprintf(out, "  %-42s MUTE     produced no output\n", v.name)
		default:
			fmt.Fprintf(out, "  %-42s speaks   %d bytes\n", v.name, v.bytes)
		}
	}
	fmt.Fprintln(out)

	if mute > 0 {
		return fmt.Errorf("%d of %d injecting hook(s) produced nothing.\n"+
			"  A hook declaring `stdout-injected` is the only way memory reaches a session\n"+
			"  unbidden, and its healthy state is silence — so this is the one condition no\n"+
			"  other check can see. Run the hook by hand to read its stderr, and check the\n"+
			"  query it builds and the room it searches: the recall hook was mute for every\n"+
			"  branch whose work was not filed in the ONE room it asked",
			mute, len(verdicts))
	}
	fmt.Fprintf(out, "  all %d injecting hook(s) produced output\n", len(verdicts))
	return nil
}

// runOneHook feeds a hook a SessionStart payload and reports what it wrote.
func runOneHook(ctx context.Context, dir, name string, c *cli.Command) hookVerdict {
	v := hookVerdict{name: name, channel: "stdout-injected"}
	ctx, cancel := context.WithTimeout(ctx, c.Duration("timeout"))
	defer cancel()

	cwd, _ := os.Getwd()
	payload, _ := json.Marshal(map[string]any{
		"session_id": "doctor", "transcript_path": os.DevNull, "cwd": cwd,
		"hook_event_name": "SessionStart", "source": "startup",
	})

	cmd := exec.CommandContext(ctx, "bash", "--", filepath.Join(dir, name))
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = append(os.Environ(), mcpURLEnvVar+"="+c.String("mcp-url"))
	if t := c.String("token"); t != "" {
		cmd.Env = append(cmd.Env, tokenEnvVar+"="+t)
	}
	stdout, err := cmd.Output()
	if err != nil && len(stdout) == 0 {
		v.err = err
	}
	v.bytes = len(strings.TrimSpace(string(stdout)))
	return v
}
