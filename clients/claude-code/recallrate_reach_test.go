package main

import (
	"strings"
	"testing"
)

// TestTheInstrumentIsCalledByTheHook is rung 2: the classifier existing proves
// nothing while no hook runs it.
//
// It reads the EMBEDDED script rather than the file on disk, because those are two
// different artifacts: an edited hook that was never re-embedded ships the old one,
// and every unit test stays green while the instrument never runs.
func TestTheInstrumentIsCalledByTheHook(t *testing.T) {
	// ⚠ THE CALLER, NOT THE DEFINITION. The first version of this test looked for
	// the function in the file that DEFINES it, which is satisfied by a helper
	// nothing invokes — the exact defect it exists to catch, one file over.
	stop, err := assets.ReadFile("hooks/agentsmemory-stop-hook.sh")
	if err != nil {
		t.Fatalf("read embedded stop hook: %v", err)
	}
	if !strings.Contains(string(stop), "\nagentsmemory_recall_observe") {
		t.Error("the embedded Stop hook never calls agentsmemory_recall_observe — the instrument " +
			"is a function nothing runs, which is this repository's characteristic defect. " +
			"If the script was edited but not re-embedded, the two artifacts have diverged.")
	}

	// The definition has to be reachable from the caller too: the Stop hook sources
	// the stats helper, and the function lives there.
	helper, err := assets.ReadFile("hooks/agentsmemory-stats.sh")
	if err != nil {
		t.Fatalf("read embedded stats helper: %v", err)
	}
	if !strings.Contains(string(helper), "agentsmemory_recall_observe()") {
		t.Error("the embedded stats helper does not define agentsmemory_recall_observe, so the " +
			"Stop hook calls a name that does not exist")
	}
}
