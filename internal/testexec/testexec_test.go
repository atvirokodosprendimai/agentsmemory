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
	// What this asserts is that the grandchild STOPPED RUNNING, which is the
	// promise the package makes — the incident behind it was two orphans
	// burning a core each for fifteen and a half hours, and a dead process
	// burns nothing. It deliberately does NOT require the pid entry to vanish.
	//
	// ⚠ ESRCH ALONE CANNOT EXPRESS THAT, AND ASSERTING IT FAILED THIS TEST WHERE
	// THE MECHANISM HAD WORKED. `Kill(pid, 0)` succeeds for a ZOMBIE exactly as
	// it does for a running process — a zombie is a dead process whose exit
	// status nobody has collected — so an ESRCH-only poll cannot tell "the kill
	// missed" from "the kill worked and nobody reaped". Reaping an orphan is the
	// init system's job, and a container started without one has no init: PID 1
	// is the shell from `docker run … sh -c`, which never reaps, so the zombie
	// persists for the life of the container and no length of deadline helps.
	//
	// That is the shape of issue #338, whose two candidate explanations were
	// "the deadline is too short" and "reapGroup has a race" — both of which
	// assume the survivor is ALIVE, which the old assertion could not check.
	// Load decides whether the direct child is still alive to reap its own child
	// before dying; a missing reaper decides whether losing that race is
	// permanent. TestAnUnreapedChildIsNotAlive pins the premise.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return // reaped: gone entirely
		}
		if !isRunning(pid) {
			return // dead, awaiting a reaper this environment may not have
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d is still RUNNING (state %q) after the deadline killed its "+
				"parent; the process group was not killed", pid, procState(pid))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAnUnreapedChildIsNotAlive pins the distinction the deadline test rests
// on, because getting it wrong is invisible in the worst way: it turns a
// healthy run red while reporting "still alive" about a process that is dead.
//
// It BUILDS the state rather than waiting for one — a child that is killed and
// not waited for is a zombie by definition — and asserts both halves: that
// `Kill(pid, 0)` still succeeds on it, so ESRCH is genuinely unreachable, and
// that isRunning reports it as not running regardless.
func TestAnUnreapedChildIsNotAlive(t *testing.T) {
	if procState(os.Getpid()) == "" {
		t.Skip("no readable /proc on this platform: process state is unavailable and the " +
			"deadline test keeps its ESRCH-only reading here")
	}
	cmd := command(t, 30*time.Second, "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the child: %v", err)
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill the child: %v", err)
	}
	// Deliberately NOT waiting yet: an unwaited dead child is the state under
	// test. The deferred Wait reaps it, so this leaves nothing behind.
	defer func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(3 * time.Second)
	for procState(pid) != "Z" {
		if time.Now().After(deadline) {
			t.Fatalf("the killed child never became a zombie; state %q", procState(pid))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Errorf("Kill(pid, 0) on a zombie returned %v, want nil — were it ESRCH, the deadline "+
			"test's original poll would have sufficed and isRunning would be redundant", err)
	}
	if isRunning(pid) {
		t.Errorf("isRunning(%d) reports a zombie as running; the deadline test would then fail "+
			"over a process group it had successfully killed", pid)
	}
}

// isRunning reports whether pid still holds the CPU — what this package
// promises to end — as distinct from whether its pid entry still exists.
//
// A pid whose state cannot be read is reported as RUNNING on purpose: the
// caller then falls back to the ESRCH reading it had before, so an unreadable
// state can never turn a failure into a pass.
func isRunning(pid int) bool {
	// Only a zombie is dead-but-present. Every other state — R, S, D, T, I —
	// is a process that exists, and "" means we could not tell.
	return procState(pid) != "Z"
}

// procState returns the single-letter state from /proc/<pid>/stat, or "" when
// it cannot be read — no /proc on this platform, or the process is gone.
//
// It scans to the LAST ')' rather than splitting the line on spaces: field two
// is the executable name, unquoted and free to contain both spaces and
// parentheses, so a plain field split mis-parses any process whose name has one.
func procState(pid int) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	line := string(raw)
	end := strings.LastIndexByte(line, ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(line[end+1:])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
