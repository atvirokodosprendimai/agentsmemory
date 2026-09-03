package main

import (
	"context"
	"strings"
	"testing"
)

// TestTheTwoChannelSetsAreDisjointAndComplete keeps the classification honest as
// a classification, before anything relies on it.
//
// An event in both sets makes hookEventChannel's answer depend on the order of
// its switch, which is a coin flip wearing a lookup. And a set that is empty on
// either side would make every event look like the other kind.
func TestTheTwoChannelSetsAreDisjointAndComplete(t *testing.T) {
	if len(injectingEvents) == 0 {
		t.Fatal("no injecting events classified; every hook would read as discarded")
	}
	if len(debugLogEvents) == 0 {
		t.Fatal("no debug-log events classified; every documented event would read as unknown")
	}
	for e := range injectingEvents {
		if debugLogEvents[e] {
			t.Errorf("%s is in BOTH sets; hookEventChannel's answer then depends on switch order", e)
		}
	}
	// The events this kit actually uses must resolve to a definite channel.
	for _, e := range []string{"SessionStart", "UserPromptSubmit", "Stop", "SubagentStop", "SessionEnd"} {
		if hookEventChannel(e) == channelUnknown {
			t.Errorf("%s is an event this kit registers on and it is unclassified", e)
		}
	}
}

// TestEveryPlannedEventIsClassified is the gate that makes the third answer worth
// having.
//
// Registering a hook on an event nobody has classified is how a hook ends up
// writing to the debug log while every test passes — the PreCompact defect
// ADR-041 records. Previously that could only be caught if someone happened to
// know the event did not inject; now an event missing from BOTH sets fails here,
// which is a question a reviewer cannot forget to ask.
func TestEveryPlannedEventIsClassified(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	plans := inst.hookPlans()
	if len(plans) == 0 {
		t.Fatal("no hook plans; this gate is looking at nothing")
	}
	for _, p := range plans {
		if hookEventChannel(p.event) == channelUnknown {
			t.Errorf("the installer registers %s, which is in neither injectingEvents nor debugLogEvents — classify it against the hooks reference before shipping it", p.event)
		}
	}
}

// TestDoctorSaysUnknownRatherThanDiscarded pins the distinction at the place an
// operator reads it.
//
// The old shape had two answers, so an event this build had never heard of was
// reported as "DISCARDED — its stdout goes to the debug log". That is a confident
// verdict about something unclassified, and it is wrong in the direction that
// gets acted on: an operator would move a working hook off a working event.
func TestDoctorSaysUnknownRatherThanDiscarded(t *testing.T) {
	dir := t.TempDir()

	unknown := judgeHook(context.Background(), nil, dir, "x.sh",
		hookRegistration{events: []string{"SomeEventClaudeShipsNextMonth"}}, dir)
	if !strings.Contains(unknown.label, "UNKNOWN") {
		t.Errorf("an unclassified event reported %q; it must say it does not know", unknown.label)
	}
	if !strings.Contains(unknown.detail, "cannot classify") {
		t.Errorf("the detail does not say the build cannot classify it: %q", unknown.detail)
	}

	// A documented debug-log event is still DISCARDED — the definite answer must
	// survive, or this change traded one wrong verdict for another.
	discarded := judgeHook(context.Background(), nil, dir, "x.sh",
		hookRegistration{events: []string{"PreCompact"}}, dir)
	if !strings.Contains(discarded.label, "DISCARDED") {
		t.Errorf("PreCompact is documented as debug-log and reported %q; it must stay a definite verdict", discarded.label)
	}

	// And PostCompact specifically, because it is the event a reader reaches for
	// when they want to act after a compaction — and its stdout does not inject.
	post := judgeHook(context.Background(), nil, dir, "x.sh",
		hookRegistration{events: []string{"PostCompact"}}, dir)
	if !strings.Contains(post.label, "DISCARDED") {
		t.Errorf("PostCompact reported %q; its stdout goes to the debug log, which is why the recall hook uses SessionStart's compact source instead", post.label)
	}
}

// TestUserPromptExpansionIsNotTreatedAsInjecting records a correction with a date
// on it, because this is the entry that went stale.
//
// It read as injecting from 2026-08-28 until 2026-09-03, quoting the hooks
// reference. That page now lists it on the debug-log side. Nothing here noticed,
// because a set of three strings cannot tell "correct" from "was correct" — so
// the fact is pinned where a future edit has to argue with it.
func TestUserPromptExpansionIsNotTreatedAsInjecting(t *testing.T) {
	if hookEventChannel("UserPromptExpansion") == channelInjected {
		t.Error("UserPromptExpansion is classified as injecting; the hooks reference lists it under debug-log-only as of 2026-09-03 — if that changed back, update the date on injectingEvents rather than only this line")
	}
	if hookEventChannel("PostModelSwitch") != channelInjected {
		t.Error("PostModelSwitch is not classified as injecting; the hooks reference lists it with UserPromptSubmit and SessionStart as of 2026-09-03")
	}
}
