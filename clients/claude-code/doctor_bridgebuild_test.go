package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestABridgeBehindItsServerIsReported is issue #207, and it is the state every
// other check in this file agrees is healthy.
//
// judgeServerBin compares the REGISTRATION with whatever is on PATH, and the
// installer copies the Desktop bridge from the first `aiagentmemory-server` PATH
// offers — so the ordinary way an install goes stale leaves those two identical
// and the comparison silent. Measured on the reference machine 2026-09-04: a
// day-old bridge against a current server, `doctor` at exit 0. Measured again in
// this checkout 2026-09-06, where the kit and the Desktop bridge sat at 8f485ab
// together while the server served 1e25405 — the same shape, found by
// redeploy.sh's kit check rather than by doctor.
//
// A bridge behind its server speaks an older wire contract INSIDE Claude Desktop,
// where the error reads as ours.
func TestABridgeBehindItsServerIsReported(t *testing.T) {
	for _, tc := range []struct {
		name, bridge string
		readErr      error
		srv          serverVerdict
		wantLabel    string
		wantBad      bool
	}{
		{
			name: "agreement is the healthy case", bridge: "v0.0.121",
			srv:       serverVerdict{label: "ok", version: "v0.0.121"},
			wantLabel: "ok",
		},
		{
			name: "a bridge behind its server", bridge: "v0.0.120",
			srv:       serverVerdict{label: "ok", version: "v0.0.121"},
			wantLabel: "STALE-BRIDGE", wantBad: true,
		},
		{
			name: "a bridge AHEAD of its server is equally a finding", bridge: "v0.0.121",
			srv:       serverVerdict{label: "ok", version: "v0.0.120"},
			wantLabel: "STALE-BRIDGE", wantBad: true,
		},
		{
			name: "a bridge that names no build", bridge: "dev",
			srv:       serverVerdict{label: "ok", version: "v0.0.121"},
			wantLabel: "UNSTAMPED", wantBad: true,
		},
		{
			name: "a bridge that will not answer", readErr: errors.New("exec format error"),
			srv:       serverVerdict{label: "ok", version: "v0.0.121"},
			wantLabel: "UNREADABLE", wantBad: true,
		},
		{
			// ⚠ THE STATE THAT MUST NOT READ AS AGREEMENT. There is no right-hand
			// side, so "ok" would report a healthy install on evidence that never
			// arrived. It is not counted as a finding either, because judgeServer
			// has already failed on the server row and one cause must not be
			// reported as two problems.
			name: "an unreachable server leaves nothing to compare", bridge: "v0.0.121",
			srv:       serverVerdict{label: "UNREACHABLE", version: ""},
			wantLabel: "no-comparison",
		},
		{
			name: "an unstamped server leaves nothing to compare", bridge: "v0.0.121",
			srv:       serverVerdict{label: "UNSTAMPED", version: "dev"},
			wantLabel: "no-comparison",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			label, detail, bad := judgeBridgeAgainstServer(tc.bridge, tc.readErr, tc.srv)
			if label != tc.wantLabel {
				t.Errorf("label = %q, want %q (detail: %s)", label, tc.wantLabel, detail)
			}
			if bad != tc.wantBad {
				t.Errorf("bad = %v, want %v — the exit code is the half an operator cannot "+
					"ignore, so which states carry it is the decision this test pins", bad, tc.wantBad)
			}
			if tc.wantLabel != "ok" && detail == "" {
				t.Error("a non-ok verdict with no detail tells an operator a word and not what to do")
			}
		})
	}
}

// TestDoctorPrintsTheBridgeBuildRow covers the rung the table above cannot see:
// a verdict function that is correct and called by nothing.
//
// That is this repository's own recorded defect class, and the bridge verdict has
// already been shipped in it once — every test in serverbin_test.go passed while
// runHookDoctor's hook guards returned early for the only kit that HAS a bridge,
// so the row was printed for nobody. This drives the real CLI for the same reason.
func TestDoctorPrintsTheBridgeBuildRow(t *testing.T) {
	// The package-wide stub answers v0.0.0-test-stub for the server, so a fixture
	// bridge printing the same string is the healthy case and any other string is
	// the stale one.
	run := func(t *testing.T, bridgeVersion string) (string, error) {
		t.Helper()
		kit := desktopKit(t)
		dir := t.TempDir()
		bin := filepath.Join(dir, "server")
		body := "#!/bin/sh\necho 'agentsmemory version " + bridgeVersion + "'\n"
		if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		writeMCPConfig(t, dir, kit.mcpConfigFile, bin)

		var buf bytes.Buffer
		root := rootCommand()
		root.Writer = &buf
		err := root.Run(context.Background(), []string{
			"aiagentmemory", "doctor", "--agent", agentClaudeDesktop,
			"--target-dir", dir, "--project-dir", t.TempDir(),
		})
		return buf.String(), err
	}

	t.Run("a current bridge passes and says so", func(t *testing.T) {
		report, err := run(t, "v0.0.0-test-stub")
		if err != nil {
			t.Fatalf("a bridge matching its server failed doctor: %v\n%s", err, report)
		}
		if !strings.Contains(report, "mcp bridge build") {
			t.Errorf("no bridge-build row, so the comparison is unreachable from the command "+
				"an operator actually runs:\n%s", report)
		}
	})

	t.Run("a stale bridge fails, names both versions, and exits non-zero", func(t *testing.T) {
		report, err := run(t, "v0.0.100")
		if err == nil {
			t.Fatalf("a bridge a release behind its server passed doctor:\n%s", report)
		}
		if !strings.Contains(report, "STALE-BRIDGE") {
			t.Errorf("the report does not name the finding:\n%s", report)
		}
		// Both sides, because "your bridge is stale" without the two versions
		// leaves an operator with nothing to check against.
		if !strings.Contains(report, "v0.0.100") || !strings.Contains(report, "v0.0.0-test-stub") {
			t.Errorf("the report does not name both builds it compared:\n%s", report)
		}
		if !strings.Contains(err.Error(), "STALE-BRIDGE") {
			t.Errorf("the exit error does not name the finding, so a caller reading only the "+
				"error learns there is a problem and not which: %v", err)
		}
	})
}
