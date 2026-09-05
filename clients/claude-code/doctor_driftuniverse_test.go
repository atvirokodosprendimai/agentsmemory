package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryShippedHookIsCheckedForDrift derives its universe from what a real
// install WRITES, then edits every one of those files and requires doctor's
// drift check to name each.
//
// The map this replaces was hand-kept and listed six of ten scripts: touched,
// anchor-cue, task-recall and precompact were installed, registered, judged for
// their channel — and never compared against the binary's embedded copy, so an
// operator whose `update` refreshed the binary and left those four behind was
// told nothing had drifted. A list kept beside the truth goes stale one script
// at a time; this test reads the truth (the install) and fails on the first
// script the check cannot see. Found 2026-09-05 while adding the tenth hook.
func TestEveryShippedHookIsCheckedForDrift(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "agentsmemory-") || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append([]byte("# drifted\n"), body...), 0o755); err != nil {
			t.Fatal(err)
		}
		want = append(want, e.Name())
	}
	if len(want) < 8 {
		t.Fatalf("the install wrote only %d scripts (%v); this universe is too small to be the real kit", len(want), want)
	}
	sort.Strings(want)
	got := staleHooksIn(dir)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("doctor's drift check sees %v\nbut the install wrote and this test edited %v\n— every script it cannot see is one whose drift an operator is never told about", got, want)
	}
}
