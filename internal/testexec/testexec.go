// Package testexec spawns child processes from tests with a deadline and a
// reaper, so a test that fails or is killed cannot leave its children behind.
//
// It exists because of a measured failure in a sibling project on 2026-09-05:
// two hook children from a mutation run hung in catastrophic regex
// backtracking AFTER their test had already recorded FAILED. `go test` killed
// the test binary at its package timeout; the children were reparented to
// launchd and burned most of a core each for fifteen and a half hours. The
// owner found out from the laptop's fans, not from any test output. This tree
// had the same shape: 41 `exec.Command` sites in tests, most of them
// `bash <hook>`, none with a deadline (docs/adr/BACKLOG.md, "Tests spawn
// children with no deadline"). A deadline alone does not close it — killing
// `bash` leaves whatever bash started — so the child is put in its own
// process group and the whole group is killed when the deadline fires.
//
// The rule it enforces is the owner's, relayed the same day: every child a
// test, hook or script spawns carries a timeout, and the runner reaps its
// children before it exits. `TestEveryTestChildCarriesADeadline` in
// internal/repohygiene refuses a direct `exec.Command` in any test file, so
// this package is the one door.
package testexec

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// Deadline bounds every child started through Command. Five minutes is
// longer than any child this suite starts (the longest, the plugin tests that
// run the `claude` CLI, take seconds) and shorter than `go test`'s default
// package timeout of ten, so the child is killed and reported by the test
// that owns it rather than by the runner killing the test binary — which is
// exactly the path that orphans the child.
const Deadline = 5 * time.Minute

// Command is exec.Command for tests: the child is bound to the test's context
// with Deadline, runs in its own process group, and the whole group is killed
// when the deadline fires, the test ends, or the test binary is interrupted.
//
// A timeout surfaces as the command's error (`signal: killed`) and so fails
// whatever assertion read the result, which is the treatment the rule asks
// for: a child that outlived its deadline is a failed test, never a slow one.
// The name and args are exactly exec.Command's, so a call site changes only
// by the leading tb.
func Command(tb testing.TB, name string, args ...string) *exec.Cmd {
	tb.Helper()
	return command(tb, Deadline, name, args...)
}

// command is Command with the deadline as a parameter, so the package's own
// test can prove the reaping with a deadline short enough to wait for.
func command(tb testing.TB, deadline time.Duration, name string, args ...string) *exec.Cmd {
	tb.Helper()
	ctx, cancel := context.WithTimeout(tb.Context(), deadline)
	tb.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, name, args...)
	// WaitDelay is what lets Wait return when a grandchild still holds the
	// stdout pipe open after the child was killed; without it a killed bash
	// whose backgrounded child inherited the pipe hangs Wait forever.
	cmd.WaitDelay = 5 * time.Second
	reapGroup(cmd)
	return cmd
}
