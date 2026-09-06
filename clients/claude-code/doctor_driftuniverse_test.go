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

// TestEveryVerbatimAssetIsCheckedForDrift is the same gate over the widened
// universe: not only the hooks, but every file a real install writes byte for
// byte — the protocol and the commands included.
//
// ⚠ THE FILES THE HOOK-ONLY CHECK MISSED ARE THE ONES A SESSION READS. A stale
// hook eventually misbehaves and somebody debugs it. A stale
// agentsmemory-bootstrap.md keeps teaching a rule the project retired, to a
// model with no way to know — and `update` refreshes the BINARY in place, so a
// current binary beside a year-old protocol is what the documented upgrade
// produces. PR #334 corrected wording in commands/am.md because the old
// sentence was wrong; every kit installed before it still serves the retired
// one, and nothing said so. Issue #349.
//
// The universe is what the install WROTE, discovered by walking it, so an asset
// added tomorrow joins on the commit that adds it rather than when somebody
// remembers this list.
func TestEveryVerbatimAssetIsCheckedForDrift(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	kit := inst.kit

	var want []string
	for name := range verbatimAssetFiles(kit) {
		p := filepath.Join(dir, name)
		body, err := os.ReadFile(p)
		if err != nil {
			continue // this kit did not install it; not this gate's finding
		}
		if err := os.WriteFile(p, append([]byte("drifted\n"), body...), 0o644); err != nil {
			t.Fatal(err)
		}
		want = append(want, name)
	}
	sort.Strings(want)

	// ⚠ The protocol and at least one command must be IN the universe, asserted
	// by name. Without this the test passes over a universe that quietly shrank
	// back to hooks — which is the state it was written to end.
	var sawProtocol, sawCommand bool
	for _, n := range want {
		if n == bootstrapFile {
			sawProtocol = true
		}
		if kit.commandsDir != "" && strings.HasPrefix(n, kit.commandsDir+string(filepath.Separator)) {
			sawCommand = true
		}
	}
	if !sawProtocol {
		t.Errorf("the drift universe does not include %s, the file a session reads first; it wrote %v",
			bootstrapFile, want)
	}
	if kit.commandsDir != "" && !sawCommand {
		t.Errorf("the drift universe includes no command under %s; it wrote %v", kit.commandsDir, want)
	}

	got := staleAssetsIn(dir, kit)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("doctor's drift check sees %v\nbut the install wrote and this test edited %v\n"+
			"— every file it cannot see is one whose drift an operator is never told about", got, want)
	}
}
