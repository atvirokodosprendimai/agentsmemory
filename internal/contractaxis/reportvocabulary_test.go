package contractaxis

import (
	"bytes"
	"strings"
	"testing"
)

// TestTheReportNeverNamesADeclaredMarkerAsAFailure pins the one word that cost a
// session.
//
// `expectedFailure` is what a mutation DECLARES it must break — read out of the
// spec before anything runs. The report printed it as `failure=%q`, and on a
// mutant that never executed that is the ONLY populated field on the line: a
// dirty working tree makes RunMutation refuse at the clean precondition, so the
// patch digest, the paths, the compile and the assertion all come back empty and
// the declaration is left standing alone, dressed as an observation.
//
// Measured in #400. A reviewer on `main` whose only dirt was one uncommitted
// `.gitignore` edit read
//
//	MUTANT mcp-readonly-hint-wire-cut INVALID … failure="live tools/list lost registration policy"
//
// as a live regression and chased it until a clean clone came back green. The
// true cause was filed BELOW it as a residual — `repository must be clean before
// mutation` — so the misleading line arrived first and the correct one read as
// detail. The verdict was right the whole time and the vocabulary was wrong.
//
// ⚠ AND THE ISSUE'S OWN INFERENCE ABOUT THE MECHANISM IS WRONG, WHICH IS WHY THE
// FIX IS A RENAME AND NOT A GUARD. It reads the empty `patch=` as the harness
// proceeding with a half-applied mutant and running the assertion against it.
// Nothing ran: RunMutation returns at the status check (mutation.go), before the
// worktree is created, before the patch is applied. There is no half-applied
// state to scope — only a field name asserting something the run never observed.
func TestTheReportNeverNamesADeclaredMarkerAsAFailure(t *testing.T) {
	// A mutant that never got past the clean precondition: the declaration is
	// present because it came from the spec, and every field a run would have
	// filled is empty.
	neverRan := MutantEvidence{
		id: "wire-cut", axis: "a", item: "*", caseID: "*", target: testMutationTarget,
		expectedFailure: "live tools/list lost registration policy",
	}
	report := Report{Status: Fail, Axes: []AxisReport{{
		Axis: "a", Status: Fail, Maturity: Enforced,
		MutationTarget: testMutationTarget,
		Mutants:        []MutantEvidence{neverRan},
	}}}

	var out bytes.Buffer
	if err := WriteReport(&out, report); err != nil {
		t.Fatalf("write report: %v", err)
	}
	text := out.String()

	if strings.Contains(text, "failure=") {
		t.Errorf("the report labels a declared marker `failure=`, which reads as something the run "+
			"observed. On a mutant that never executed it is the only populated field on the line, so "+
			"it is what a reader chases:\n%s", text)
	}
	if !strings.Contains(text, `expects="live tools/list lost registration policy"`) {
		t.Errorf("the declared marker is not reported at all; it is real provenance — which mutation "+
			"this evidence belongs to — and dropping it trades a misleading line for a blank one:\n%s", text)
	}

	// The verdict itself was never the problem and must not move: this line is
	// INVALID because nothing verified it, which is correct.
	if !strings.Contains(text, "MUTANT wire-cut INVALID") {
		t.Errorf("a mutant that never ran is no longer reported INVALID:\n%s", text)
	}

	// A mutant that DID run renders through the same format string, so the rename
	// must not have cost the successful line its marker either.
	var ran bytes.Buffer
	if err := WriteReport(&ran, Report{Status: Fail, Axes: []AxisReport{{
		Axis: "a", Status: Pass, Maturity: Enforced,
		MutationTarget: testMutationTarget,
		Mutants:        []MutantEvidence{goodMutant("a")},
	}}}); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !strings.Contains(ran.String(), `expects="production selector disconnected"`) {
		t.Errorf("a VERIFIED mutant lost its declared marker:\n%s", ran.String())
	}
}
