package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStaleHooksInComparesAgainstTheEmbeddedCopy pins the three answers the
// check has to keep apart: byte-identical is current, different is stale, and
// ABSENT is neither.
//
// The absent case is the one worth a test rather than a reading. Kits install
// different subsets of the hook set, so treating "not there" as drift would
// report every codex install as stale — a false alarm on a healthy machine,
// which is how a check earns being switched off.
func TestStaleHooksInComparesAgainstTheEmbeddedCopy(t *testing.T) {
	dir := t.TempDir()

	current, err := assets.ReadFile(hookAsset)
	if err != nil {
		t.Fatalf("read embedded %s: %v", hookAsset, err)
	}
	// Byte-identical to what install would write.
	if err := os.WriteFile(filepath.Join(dir, hookFile), current, 0o755); err != nil {
		t.Fatal(err)
	}
	// One byte different is enough: the check is equality, not a heuristic about
	// how MUCH a file drifted, because a one-line stat probe is exactly the size
	// of change that caused the incident this exists for.
	if err := os.WriteFile(filepath.Join(dir, verifyHookFile), append([]byte("# edited\n"), current...), 0o755); err != nil {
		t.Fatal(err)
	}
	// recallHookFile is deliberately NOT written.

	stale := staleHooksIn(dir)
	if len(stale) != 1 || stale[0] != verifyHookFile {
		t.Fatalf("staleHooksIn = %v, want exactly [%s]: the identical copy must not be "+
			"reported, and the absent one must not be either", stale, verifyHookFile)
	}
}

// TestDoctorNamesAStaleHookWhenNothingDeclaresItsChannel pins the fourth empty
// state, which is the one an operator cannot reach from the other three.
//
// ⚠ THE SYMPTOM IS IDENTICAL TO "NOTHING IS INSTALLED" AND THE FIX IS THE
// OPPOSITE. A hook old enough to predate the `# hook-output:` line makes the
// scan find zero injecting scripts, so doctor said the directory looked empty
// and advised checking whether anything was installed — while a full, working,
// merely OLD install sat in it. Reported 2026-09-02: a sandbox whose Stop hook
// still carried a `stat` probe fixed eight days earlier, feeding a multiline
// filesystem block into an integer comparison on every Linux session. The
// binary was current, because `update` refreshes the binary and leaves configs
// alone — so the documented upgrade path produces exactly this state.
func TestDoctorNamesAStaleHookWhenNothingDeclaresItsChannel(t *testing.T) {
	// An old hook: no declaration line at all, which is what makes the scan come
	// back empty, and different bytes, which is what makes it drift.
	dir := doctorEnv(t, map[string]string{
		hookFile: "#!/usr/bin/env bash\necho an old hook from before the declaration line\n",
	}, map[string][]string{"Stop": {hookFile}})

	report, err := runDoctor(t, dir)
	if err == nil {
		t.Fatalf("a stale install reported success:\n%s", report)
	}
	msg := err.Error()
	if !strings.Contains(msg, hookFile) {
		t.Errorf("the error does not name the drifted file, so the operator cannot act on it: %v", err)
	}
	if !strings.Contains(msg, "aiagentmemory install") {
		t.Errorf("the error does not name the command that fixes it: %v", err)
	}
	// The distinguishing half. Without this the message is the old one, which
	// sends the operator to look for an empty directory that is not empty.
	if !strings.Contains(msg, "OLDER install") {
		t.Errorf("the error does not say the install is OLD, which is the whole finding: %v", err)
	}
}

// TestDoctorStaysQuietWhenTheHooksAreCurrent is the negative control: the same
// path must say nothing when the installed bytes match, or the check above
// would pass for a reason unrelated to drift.
func TestDoctorStaysQuietWhenTheHooksAreCurrent(t *testing.T) {
	current, err := assets.ReadFile(recallHookAsset)
	if err != nil {
		t.Fatalf("read embedded %s: %v", recallHookAsset, err)
	}
	dir := doctorEnv(t, map[string]string{recallHookFile: string(current)},
		map[string][]string{"SessionStart": {recallHookFile}})

	report, err := runDoctor(t, dir)
	if err != nil {
		t.Fatalf("a current install failed: %v\n%s", err, report)
	}
	if strings.Contains(report, "STALE") {
		t.Errorf("a byte-identical hook was reported as drifted:\n%s", report)
	}
}
