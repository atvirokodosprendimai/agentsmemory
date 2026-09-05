package main

import (
	"strings"
	"testing"
)

// TestARetirePlanIsNeverReportedAsRegistered holds the one thing an operator has
// to go on when a hook is deliberately NOT installed.
//
// hookPlansOn puts a retire plan in the list on purpose — the event is present so
// that an earlier install's registration can be REMOVED rather than skipped. The
// reporting loop then had no notion of a plan's kind and called every unchanged
// plan "already registered", so a run could say both things about one event six
// lines apart (#184, measured on Windows 11):
//
//	[!!] SessionEnd hook NOT registered on Windows: … Any registration from an
//	     earlier install is retired.
//	[ok] SessionEnd hook already registered
//
// The state was right and only the report was wrong, which is the worst version:
// the operator's only evidence that a deliberate retirement worked is a line
// telling them it did not.
//
// This drives the real hookPlansOn with goos "windows" rather than a fabricated
// plan, so the test is exercising the retirement this project actually ships. It
// is skipped rather than passed if no retire plan is found — a check that silently
// asserts nothing is the failure this repository keeps recording.
func TestARetirePlanIsNeverReportedAsRegistered(t *testing.T) {
	i := &Installer{targetDir: "/cfg", kit: agentKit{name: agentClaude}}

	plans := i.hookPlansOn("windows")
	var retire, register *hookPlan
	for idx := range plans {
		if plans[idx].retire && retire == nil {
			retire = &plans[idx]
		}
		if !plans[idx].retire && register == nil {
			register = &plans[idx]
		}
	}
	if retire == nil {
		t.Skip("no retire plan on windows — nothing for this gate to check; it is not asserting a pass")
	}
	if register == nil {
		t.Fatal("no registration plan on windows: the install registers nothing at all, " +
			"which would make the contrast below meaningless")
	}

	got := unchangedNote(*retire)
	if strings.Contains(got, "already registered") {
		t.Errorf("an unchanged RETIRE plan for %s reports %q.\n"+
			"  That is the inverse of the state: nothing is registered, and the same run has "+
			"already told the operator the hook is deliberately not installed here. A report "+
			"that contradicts the run's own warning is worse than no report.", retire.event, got)
	}
	if !strings.Contains(got, "absent") {
		t.Errorf("an unchanged retire plan for %s reports %q, which does not say the hook is "+
			"gone — the one fact the operator needs to know the retirement worked", retire.event, got)
	}

	// The other half, so a fix that simply stopped saying "registered" everywhere
	// cannot pass: a real registration must still report itself as registered.
	if gotReg := unchangedNote(*register); !strings.Contains(gotReg, "already registered") {
		t.Errorf("an unchanged REGISTRATION plan for %s reports %q, want it to say the hook is "+
			"already registered — silencing both kinds is not a fix, it is the same defect "+
			"pointing the other way", register.event, gotReg)
	}
}
