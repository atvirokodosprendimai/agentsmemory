package main

import (
	"strings"
	"testing"
)

// writerHookBody is a hook that speaks to nobody and writes a file — the
// PreCompact note writer's shape, and the shape doctor could not see.
const writerHookBody = "#!/usr/bin/env bash\n# hook-output: none — it writes a file and says nothing to the model.\nexit 0\n"

// TestDoctorReportsAnUnregisteredHookWhateverItsChannel is the rung the channel
// declaration cannot reach.
//
// `injectingScriptsIn` skips every script whose declaration is not
// `stdout-injected`, so seven of the ten shipped hooks — the PreCompact note
// writer and the PostToolUse touched recorder among them — were outside the
// command's universe entirely. A note writer installed and registered on NO
// event produced exactly the operator-visible silence doctor exists to explain:
// no note, no re-ground marker, a monitor waiting on a file nobody writes, and
// `doctor` exiting 0. That is UNREGISTERED, the verdict this command was built
// to produce (§Reachability records it catching precisely this for three
// ADR-051 hooks), and it could not produce it here.
//
// The channel decides whether a hook can SPEAK. It must not decide whether a
// hook is WIRED: registration is checkable for every hook regardless of where
// its output goes. Found 2026-09-05 while a PreCompact note was missing from a
// session's filesystem entirely and doctor reported health.
func TestDoctorReportsAnUnregisteredHookWhateverItsChannel(t *testing.T) {
	t.Run("a non-injecting hook registered nowhere is reported", func(t *testing.T) {
		dir := doctorEnv(t, map[string]string{
			"agentsmemory-recall-hook.sh":     injectingHookBody,
			"agentsmemory-precompact-hook.sh": writerHookBody,
		}, map[string][]string{"SessionStart": {"agentsmemory-recall-hook.sh"}})
		report, err := runDoctor(t, dir)
		if err == nil {
			t.Fatalf("an installed hook registered by no event passed:\n%s", report)
		}
		if !strings.Contains(report, "agentsmemory-precompact-hook.sh") {
			t.Errorf("the report never names the unwired hook:\n%s", report)
		}
		if !strings.Contains(report, "UNREGISTERED") {
			t.Errorf("the report does not carry the UNREGISTERED verdict:\n%s", report)
		}
	})

	t.Run("a registered non-injecting hook is healthy and does not fail the run", func(t *testing.T) {
		// The other half, and the one that keeps this from being a noise machine:
		// a writer hook that IS wired is fine, and must not be judged by whether
		// it printed — it has no channel to print on. TestDoctorDoesNotFailOnSilence
		// records why silence is not a verdict.
		dir := doctorEnv(t, map[string]string{
			"agentsmemory-recall-hook.sh":     injectingHookBody,
			"agentsmemory-precompact-hook.sh": writerHookBody,
		}, map[string][]string{
			"SessionStart": {"agentsmemory-recall-hook.sh"},
			"PreCompact":   {"agentsmemory-precompact-hook.sh"},
		})
		report, err := runDoctor(t, dir)
		if err != nil {
			t.Fatalf("a correctly wired install failed:\n%s", report)
		}
		if strings.Contains(report, "UNREGISTERED") {
			t.Errorf("a wired hook was reported unregistered:\n%s", report)
		}
	})
}
