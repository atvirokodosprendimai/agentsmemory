//go:build unix

package testexec

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestADeadlineKillsTheChildAndItsChildren is the mechanism this package
// exists for: a bash child that started a grandchild is killed at the
// deadline, and so is the grandchild. Killing bash alone would pass a test
// that only watched the direct child, which is the reparenting the incident
// behind this package was about.
func TestADeadlineKillsTheChildAndItsChildren(t *testing.T) {
	pidFile := t.TempDir() + "/grandchild.pid"
	// bash starts a sleeping grandchild, writes its pid, then sleeps itself.
	cmd := command(t, 300*time.Millisecond, "bash", "-c",
		"sleep 30 & echo $! > "+pidFile+"; wait")
	start := time.Now()
	err := cmd.Run()
	if err == nil {
		t.Fatal("the child outlived a 300ms deadline and Run reported success")
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("Run returned after %v; the deadline did not cut the wait", took)
	}

	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("the grandchild never recorded its pid: %v", rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if perr != nil {
		t.Fatalf("pid file: %q", raw)
	}
	// A killed process may linger as a zombie for an instant; give the group
	// kill a moment, then require ESRCH — "no such process" — not merely a
	// signal that was accepted.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d is still alive after the deadline killed its parent; the process group was not reaped", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
