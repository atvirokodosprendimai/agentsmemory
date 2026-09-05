package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/urfave/cli/v3"
)

// The whole package's doctor tests run against the stub below, never a real
// endpoint: the real probe dials --mcp-url, which defaults to localhost:8080,
// and in CI nothing listens there. Without this every existing doctor test
// would report UNREACHABLE and the exit-0 assertions would go red for a reason
// unrelated to what they test. Individual tests swap the stub for the case
// they are about and restore it.
func init() {
	probeServerVersion = func(context.Context, string, string, time.Duration) (string, error) {
		return "v0.0.0-test-stub", nil
	}
}

// stubServerVersion points the probe at a fixed answer for one test.
func stubServerVersion(t *testing.T, version string, err error) {
	t.Helper()
	prev := probeServerVersion
	probeServerVersion = func(context.Context, string, string, time.Duration) (string, error) {
		return version, err
	}
	t.Cleanup(func() { probeServerVersion = prev })
}

// TestDoctorReportsAnUnstampedServer is issue #210: a server built without
// AGENTSMEMORY_VERSION reports `dev`, every other check is green, and the one
// comparison that tells a stale server from a current one — checkout against the
// served version — is gone. redeploy.sh refuses to produce that artifact; doctor
// is the route for an operator who did not deploy through the script.
func TestDoctorReportsAnUnstampedServer(t *testing.T) {
	healthy := func(t *testing.T) string {
		return doctorEnv(t,
			map[string]string{"agentsmemory-recall-hook.sh": injectingHookBody},
			map[string][]string{"SessionStart": {"agentsmemory-recall-hook.sh"}})
	}

	t.Run("bare dev is a finding", func(t *testing.T) {
		stubServerVersion(t, "dev", nil)
		report, err := runDoctor(t, healthy(t))
		if err == nil {
			t.Fatalf("a server reporting version dev passed doctor:\n%s", report)
		}
		if !strings.Contains(report, "UNSTAMPED") {
			t.Errorf("the report does not name the finding:\n%s", report)
		}
		if !strings.Contains(err.Error(), "UNSTAMPED") {
			t.Errorf("the summary legend does not explain UNSTAMPED:\n%v", err)
		}
	})

	t.Run("an empty version is the same finding", func(t *testing.T) {
		stubServerVersion(t, "", nil)
		report, err := runDoctor(t, healthy(t))
		if err == nil || !strings.Contains(report, "UNSTAMPED") {
			t.Fatalf("an empty served version passed doctor (err=%v):\n%s", err, report)
		}
	})

	t.Run("a dev-<commit> build is named, not failed", func(t *testing.T) {
		// buildinfo.Effective turns an unstamped build made inside a repository into
		// dev-<commit>; that names a build an operator can look up, which is the
		// whole distinction #210 draws.
		stubServerVersion(t, "dev-0123456789ab+dirty", nil)
		report, err := runDoctor(t, healthy(t))
		if err != nil {
			t.Fatalf("a dev-<commit> build failed doctor:\n%s\n%v", report, err)
		}
		if !strings.Contains(report, "unreleased") || !strings.Contains(report, "dev-0123456789ab+dirty") {
			t.Errorf("the report does not show the commit-named build:\n%s", report)
		}
	})

	t.Run("a release tag is reported and passes", func(t *testing.T) {
		stubServerVersion(t, "v0.0.115", nil)
		report, err := runDoctor(t, healthy(t))
		if err != nil {
			t.Fatalf("a stamped server failed doctor:\n%s\n%v", report, err)
		}
		if !strings.Contains(report, "v0.0.115") {
			t.Errorf("the served version is not in the report, so an operator still cannot compare it to the checkout:\n%s", report)
		}
	})

	t.Run("a server that does not answer is a finding", func(t *testing.T) {
		stubServerVersion(t, "", errors.New("connect http://localhost:8080/mcp: connection refused"))
		report, err := runDoctor(t, healthy(t))
		if err == nil || !strings.Contains(report, "UNREACHABLE") {
			t.Fatalf("an unreachable server passed doctor (err=%v):\n%s", err, report)
		}
		if !strings.Contains(report, "connection refused") {
			t.Errorf("the dial error is not printed, so the operator cannot see why:\n%s", report)
		}
	})
}

// TestTheServerProbeReadsTheHandshakeVersion drives the REAL probe against a real
// Streamable-HTTP MCP server, because every case above substitutes it: a stub
// proves the rung is selected, and only this proves the default reads the
// version the server puts in its initialize result — the same string am_status
// and --version carry (issue #70).
func TestTheServerProbeReadsTheHandshakeVersion(t *testing.T) {
	for _, want := range []string{"dev", "v9.9.9"} {
		srv := server.NewTestStreamableHTTPServer(server.NewMCPServer("agentsmemory", want))
		t.Cleanup(srv.Close)
		got, err := dialServerVersion(context.Background(), srv.URL, "", 5*time.Second)
		if err != nil {
			t.Fatalf("probe %s: %v", srv.URL, err)
		}
		if got != want {
			t.Errorf("probe read %q from a server whose handshake says %q", got, want)
		}
	}
	// An endpoint that answers nothing is an error, not an empty version — an
	// empty version is UNSTAMPED, and conflating the two would report a dead
	// server as an unstamped one.
	if _, err := dialServerVersion(context.Background(), "http://127.0.0.1:1/mcp", "", 2*time.Second); err == nil {
		t.Fatal("a closed port produced a version instead of an error")
	}
}

// TestJudgeServerNamesEveryState pins the pure judgement so the labels cannot
// drift apart from the legend the summary prints.
func TestJudgeServerNamesEveryState(t *testing.T) {
	cases := []struct {
		version string
		err     error
		label   string
		bad     bool
	}{
		{"v0.0.115", nil, "ok", false},
		{"dev-0123456789ab", nil, "unreleased", false},
		{"dev", nil, "UNSTAMPED", true},
		{"", nil, "UNSTAMPED", true},
		{"", errors.New("boom"), "UNREACHABLE", true},
	}
	for _, c := range cases {
		v := judgeServer("http://x/mcp", c.version, c.err)
		if v.label != c.label || v.bad != c.bad {
			t.Errorf("judgeServer(%q, %v) = %s/bad=%v, want %s/bad=%v", c.version, c.err, v.label, v.bad, c.label, c.bad)
		}
	}
}

// TestInstallEndpointLetsTheRegistrationWin pins the decision the server rung is
// built on: the install's own registration names the endpoint, and the --mcp-url
// flag — which defaults to the HOSTED palace — is only the fallback. Reversed,
// every self-hosted install would be judged against a palace its operator does
// not use and the resulting 401 reported as the install's condition, with the
// whole suite green, because the doctor tests stub the probe. Raised in review
// of #242: the doc comment was carrying this alone.
func TestInstallEndpointLetsTheRegistrationWin(t *testing.T) {
	const flagURL = "http://127.0.0.1:8/mcp"
	const regURL = "http://127.0.0.1:9/mcp"
	for _, tc := range []struct {
		name      string
		command   string
		wantURL   string
		wantToken string
	}{
		{"the registration's endpoint wins over the flag",
			hookCommand(regURL, "/tmp/hook.sh"), regURL, "t-flag"},
		{"an unprefixed registration falls back to the flag",
			"bash -- '/tmp/hook.sh'", flagURL, "t-flag"},
		{"an empty assignment does not blank the flag",
			mcpURLEnvVar + "='' bash -- '/tmp/hook.sh'", flagURL, "t-flag"},
		{"a registered token wins over the flag's",
			tokenEnvVar + "='t-reg' " + hookCommand(regURL, "/tmp/hook.sh"), regURL, "t-reg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			body, err := json.Marshal(map[string]any{"hooks": map[string]any{
				"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{
					"type": "command", "command": tc.command,
				}}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, claudeKit.hooksFile), body, 0o600); err != nil {
				t.Fatal(err)
			}
			var gotURL, gotToken string
			d := doctorCommand()
			d.Action = func(_ context.Context, c *cli.Command) error {
				gotURL, gotToken = installEndpoint(c, claudeKit, dir)
				return nil
			}
			if err := d.Run(context.Background(), []string{"doctor", "--mcp-url", flagURL, "--token", "t-flag", "--target-dir", dir}); err != nil {
				t.Fatal(err)
			}
			if gotURL != tc.wantURL {
				t.Errorf("endpoint = %q, want %q", gotURL, tc.wantURL)
			}
			if gotToken != tc.wantToken {
				t.Errorf("token = %q, want %q", gotToken, tc.wantToken)
			}
		})
	}
}
