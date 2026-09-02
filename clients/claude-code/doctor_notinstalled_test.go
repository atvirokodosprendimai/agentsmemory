package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorNamesARegistrationWithNoFileBehindIt pins the reverse arrow: a hook
// settings.json selects that is not on disk.
//
// ⚠ EVERY OTHER STATE THIS COMMAND REPORTS IS "INSTALLED, AND THE REGISTRATION IS
// WRONG". This is the other direction, and nothing saw it, because the scan's
// universe is the DIRECTORY: judgeHook runs once per script found by scanning for a
// `# hook-output:` declaration, and a file that is not there declares nothing. So a
// deleted hook did not produce a bad verdict — it silently lowered the count, and
// the closing line then said "all N injecting hook(s) are registered on an injecting
// event and ran" over a config selecting a script the agent cannot run.
//
// Measured 2026-09-02 on a fresh install: deleting agentsmemory-recall-hook.sh while
// leaving its SessionStart registration produced exactly that sentence and exit 0.
//
// Fatal, unlike STALE. Drift is not fatal because an operator may be running a
// hand-edited hook deliberately, and a check that fails a legitimate choice gets
// switched off. Nothing registers a file it means to be absent.
func TestDoctorNamesARegistrationWithNoFileBehindIt(t *testing.T) {
	dir := doctorEnv(t, map[string]string{
		hookFile:       injectingHookBody,
		recallHookFile: injectingHookBody,
	}, map[string][]string{"SessionStart": {hookFile, recallHookFile}})

	// Registered, and then gone -- the shape an operator reaches by tidying a
	// config dir, or by a partial install.
	if err := os.Remove(filepath.Join(dir, recallHookFile)); err != nil {
		t.Fatal(err)
	}

	report, err := runDoctor(t, dir)
	if err == nil {
		t.Fatalf("a config selecting a script the agent cannot run reported success:\n%s", report)
	}
	if !strings.Contains(report, recallHookFile) {
		t.Errorf("the report does not name the missing hook, so the operator cannot act "+
			"on it:\n%s", report)
	}
	if !strings.Contains(report, "NOT-INSTALLED") {
		t.Errorf("the report does not distinguish this from the states that mean the file "+
			"IS there:\n%s", report)
	}
	if !strings.Contains(report, "aiagentmemory install") {
		t.Errorf("the report does not name the command that fixes it:\n%s", report)
	}
}

// TestDoctorStaysQuietAboutHooksItDoesNotOwn is the half that keeps the check
// honest, and it covers the two ways this could become a false alarm.
//
// A checker that reports every registration it cannot find a file for would fire on
// any config holding third-party hooks, which is most real ones. It does not,
// because `registered` is keyed off installerHookPath -- the installer's own parser
// for the command shapes it writes -- so a foreign command contributes no entry at
// all. And a hook that IS on disk must never be reported missing, whatever the scan
// made of it.
func TestDoctorStaysQuietAboutHooksItDoesNotOwn(t *testing.T) {
	dir := doctorEnv(t, map[string]string{
		hookFile: injectingHookBody,
	}, map[string][]string{"SessionStart": {hookFile}})

	// A third-party hook registered by absolute path, naming a file that does not
	// exist anywhere. This is not ours and must not be reported. Added by editing
	// the parsed document rather than splicing text, so the fixture cannot fail for
	// being malformed instead of for the reason under test.
	settings := filepath.Join(dir, "settings.json")
	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the fixture doctorEnv wrote does not parse: %v", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		t.Fatalf("no hooks block in the fixture:\n%s", body)
	}
	hooks["PostToolUse"] = []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": "/nowhere/some-other-tool.sh"}},
	}}
	patched, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, patched, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := runDoctor(t, dir)
	if err != nil {
		t.Fatalf("a healthy install with a foreign hook failed: %v\n%s", err, report)
	}
	if strings.Contains(report, "NOT-INSTALLED") {
		t.Errorf("reported a hook this command does not own, which is the false alarm "+
			"that gets a check switched off:\n%s", report)
	}
	if strings.Contains(report, "some-other-tool") {
		t.Errorf("named a third-party hook:\n%s", report)
	}
}

// TestAnInstalledHookThatDeclaresNothingIsNotReportedMissing pins the guard the two
// tests above cannot reach.
//
// ⚠ "NOT IN THE SCAN" AND "NOT ON DISK" ARE DIFFERENT THINGS, and only the second
// is this check's finding. The scan's universe is scripts that declare
// `# hook-output:`, so a registered hook which is present but carries no
// declaration is absent from it while sitting right there in the directory. Calling
// that NOT-INSTALLED would be false, and its advice -- run install to write the file
// back -- would be advice about a file that exists.
//
// That state is not hypothetical: it is exactly the OLDER install the fourth
// empty-state message exists to describe, a hook predating the declaration line.
// Reporting it as missing would replace a message that names the real cause with one
// that sends the operator looking for a file they already have.
//
// Found by mutation 2026-09-02: deleting the os.Stat guard left both tests above
// green, because in their fixtures every installed hook also declares a channel, so
// the scan lookup short-circuits before the guard is ever reached.
func TestAnInstalledHookThatDeclaresNothingIsNotReportedMissing(t *testing.T) {
	// Registered, present, and carrying no `# hook-output:` line at all.
	dir := doctorEnv(t, map[string]string{
		hookFile:       injectingHookBody,
		recallHookFile: "#!/usr/bin/env bash\necho an old hook from before the declaration line\n",
	}, map[string][]string{"SessionStart": {hookFile, recallHookFile}})

	report, _ := runDoctor(t, dir)
	if strings.Contains(report, "NOT-INSTALLED") {
		t.Errorf("a hook that is on disk was reported as not installed, so the advice "+
			"names a file the operator already has:\n%s", report)
	}
}
