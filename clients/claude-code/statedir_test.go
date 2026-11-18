package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points every hook this package's tests run at a throwaway state
// directory, so a test run cannot overwrite the developer's live hook state.
//
// The hooks write to ${AGENTSMEMORY_STATE_DIR:-${TMPDIR:-/tmp}}, which is the
// directory the developer's own sessions use, and many tests inherit the real
// environment through os.Environ(). Found 2026-11-18: the `doctor` tests run
// every installed hook with the real environment, so each `go test` rewrote the
// status-line cache with an empty wing, and every open session's status line
// went blank — the same symptom as the wrong-wing report it was being fixed for.
// One TestMain covers every current and future test in this package, where a
// per-test t.Setenv would have to be remembered at each new call site.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agentsmemory-kit-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "state dir:", err)
		os.Exit(1)
	}
	os.Setenv("TMPDIR", dir)
	os.Setenv("AGENTSMEMORY_STATE_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
