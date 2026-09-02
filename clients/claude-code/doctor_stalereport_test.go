package main

import (
	"strings"
	"testing"
)

// TestDoctorReportsDriftWhenTheHooksStillDeclareTheirChannel pins the SECOND
// call site of staleHooksIn — the per-hook STALE rows at the end of the report —
// which nothing else reaches.
//
// ⚠ THE OTHER CALL SITE HID THIS ONE. staleHooksIn is called twice: once inside
// the `len(scripts) == 0` branch, where it turns "nothing declares a channel"
// into "an OLDER install is on disk", and once in the reporting loop that runs
// when scripts were found. TestDoctorNamesAStaleHookWhenNothingDeclaresItsChannel
// covers the first. Found by mutation while reviewing PR #166: replacing the
// reporting loop's range with an empty slice left the whole package green in
// 8.6s, all three tests of that PR included, because every one of them drives
// the empty-universe branch.
//
// The state covered here is the one the fourth-empty-state message cannot
// describe: an install new enough that its hooks DO carry `# hook-output:`, whose
// bytes have since drifted from the embedded copies. That is what `update`
// produces on any machine installed after the declaration line landed — the
// binary is refreshed, the hooks are not — so it is the state this check will
// spend most of its life in, and it was the one nothing asserted.
//
// Drift alone must not fail the run, for the reason judgeServerBin gives about
// STALE-PATH: an operator may be running a hand-edited hook deliberately, and a
// check that fails a legitimate choice is one that gets switched off. So the
// error is asserted nil and the REPORT is what carries the finding.
func TestDoctorReportsDriftWhenTheHooksStillDeclareTheirChannel(t *testing.T) {
	current, err := assets.ReadFile(verifyHookAsset)
	if err != nil {
		t.Fatalf("read embedded %s: %v", verifyHookAsset, err)
	}
	drifted, err := assets.ReadFile(recallHookAsset)
	if err != nil {
		t.Fatalf("read embedded %s: %v", recallHookAsset, err)
	}

	// Both declare `# hook-output: stdout-injected`, so the scan finds scripts and
	// the empty-universe branch — the one the existing tests exercise — is not
	// reached. Prepending a comment keeps the declaration intact while changing
	// the bytes, which is precisely the shape of an old hook: still recognisable,
	// no longer current.
	dir := doctorEnv(t, map[string]string{
		verifyHookFile: string(current),
		recallHookFile: "# edited by hand\n" + string(drifted),
	}, map[string][]string{"SessionStart": {verifyHookFile, recallHookFile}})

	report, err := runDoctor(t, dir)
	if err != nil {
		t.Fatalf("drift alone failed the run, which is what makes a check get switched "+
			"off: %v\n%s", err, report)
	}
	if !strings.Contains(report, recallHookFile) {
		t.Errorf("the report does not name the drifted hook, so an operator cannot act "+
			"on it:\n%s", report)
	}
	if !strings.Contains(report, "STALE") {
		t.Errorf("the report does not mark the drifted hook STALE:\n%s", report)
	}
	// The negative half, in the same fixture rather than a separate one: a
	// byte-identical hook reported as drifted is the false alarm that costs this
	// check its credibility, and only a fixture holding BOTH can catch it.
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, verifyHookFile) && strings.Contains(line, "STALE") {
			t.Errorf("a byte-identical hook was reported STALE:\n%s", line)
		}
	}
}
