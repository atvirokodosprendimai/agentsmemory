package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// writeHook drops a fixture hook with the given channel declaration and body.
func writeHook(t *testing.T, dir, name, channel, body string) {
	t.Helper()
	src := "#!/bin/sh\n# hook-output: " + channel + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorReportsAMuteInjectingHook pins the rung no other gate in this tree
// reaches: whether an installed hook actually SAYS anything.
//
// ⚠ EVERY OTHER CHECK PASSED WHILE THE RECALL HOOK WAS MUTE, TWICE. The tree
// proves a hook is registered, that its event injects stdout, and that it declares
// its channel honestly — and all three were green across two separate repair
// attempts while the hook emitted nothing, because none of them RUNS it. A hook
// whose healthy state is silence is indistinguishable from a hook that cannot
// speak, which is why this is a command an operator runs rather than a claim
// anybody makes.
func TestDoctorReportsAMuteInjectingHook(t *testing.T) {
	dir := t.TempDir()
	writeHook(t, dir, "agentsmemory-loud-hook.sh", "stdout-injected", `echo "recalled something"`)
	writeHook(t, dir, "agentsmemory-mute-hook.sh", "stdout-injected", "exit 0")

	out := &bytes.Buffer{}
	err := runDoctorFor(t, dir, out)
	if err == nil {
		t.Fatalf("a mute injecting hook was not reported; output:\n%s", out)
	}
	if !strings.Contains(out.String(), "MUTE") || !strings.Contains(out.String(), "agentsmemory-mute-hook.sh") {
		t.Errorf("the report does not name the mute hook:\n%s", out)
	}
	if !strings.Contains(out.String(), "speaks") {
		t.Errorf("the report does not distinguish the hook that DID speak — a check that "+
			"condemns everything is one people switch off:\n%s", out)
	}

	// The negative half, without which "reports a mute hook" is satisfied by
	// reporting always.
	t.Run("a hook that speaks is not reported", func(t *testing.T) {
		clean := t.TempDir()
		writeHook(t, clean, "agentsmemory-loud-hook.sh", "stdout-injected", `echo "recalled something"`)
		out := &bytes.Buffer{}
		if err := runDoctorFor(t, clean, out); err != nil {
			t.Errorf("a hook that produced output was reported anyway: %v\n%s", err, out)
		}
	})

	// ⚠ AN EMPTY UNIVERSE MUST NOT READ AS ALL-CLEAR. If the declaration is renamed,
	// this command would examine nothing and exit 0 — the exact failure it exists to
	// catch, one level up.
	t.Run("a directory with no injecting hook is an error, not a pass", func(t *testing.T) {
		none := t.TempDir()
		writeHook(t, none, "agentsmemory-quiet.sh", "blocking", "exit 0")
		out := &bytes.Buffer{}
		err := runDoctorFor(t, none, out)
		if err == nil {
			t.Errorf("a directory declaring no injecting hook passed:\n%s", out)
		} else if !strings.Contains(err.Error(), "examines nothing") {
			t.Errorf("the error does not say the check examined nothing: %v", err)
		}
	})
}

// runDoctorFor drives the real command through its own CLI wiring, so the flag
// parsing and the action are exercised rather than the helper alone. Calling
// runHookDoctor directly would leave `doctor` reachable-by-nothing — this package's
// characteristic defect, and the one the command itself is about.
func runDoctorFor(t *testing.T, dir string, out *bytes.Buffer) error {
	t.Helper()
	root := &cli.Command{
		Name:     "aiagentmemory",
		Commands: []*cli.Command{doctorCommand()},
		Writer:   out,
	}
	return root.Run(context.Background(), []string{"aiagentmemory", "doctor", "--target-dir", dir})
}
