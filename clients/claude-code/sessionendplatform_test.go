package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planFor returns the events an install would REGISTER on one platform, and the
// ones it would RETIRE. The two are different answers and the first version of
// this file conflated them: once Windows started retiring the SessionEnd hook
// rather than merely omitting it, "the event appears in the plan" stopped meaning
// "the hook is installed".
func planFor(t *testing.T, goos string) (registered, retired map[string]bool) {
	t.Helper()
	inst := &Installer{kit: claudeKit, goos: goos}
	registered, retired = map[string]bool{}, map[string]bool{}
	plans := inst.hookPlans()
	if len(plans) == 0 {
		t.Fatalf("no hooks planned on %s, so this check is examining nothing", goos)
	}
	for _, p := range plans {
		if p.retire {
			retired[p.event] = true
			continue
		}
		registered[p.event] = true
	}
	return registered, retired
}

// TestSessionEndIsNotRegisteredOnWindows pins an omission, which is the harder
// direction to keep: a registration that comes back looks like a fix.
//
// The hook needs ~3.2s on Windows 11 and loses the teardown race, printing "Hook
// cancelled" on essentially every exit (#150, medians of five). Almost none of
// that is its own work — a bare `bash -c exit </dev/null` is 1,032ms there and
// `curl` another 708ms, because process creation costs ~1s each. The stdin fix in
// fa918e1 was correct and contributes ~0 in the healthy case, which is why this
// needed a second answer rather than another edit to the script.
func TestSessionEndIsNotRegisteredOnWindows(t *testing.T) {
	registered, retired := planFor(t, "windows")
	if registered["SessionEnd"] {
		t.Error("SessionEnd is registered on Windows, where it cannot finish before teardown — " +
			"every exit reports a cancelled hook, which is how hook errors stop being read")
	}
	if !retired["SessionEnd"] {
		t.Error("Windows neither registers nor RETIRES SessionEnd, so an install that once wrote " +
			"it leaves it there: the hook keeps failing on every exit while the installer says " +
			"it is not registered")
	}
}

// TestSessionEndIsStillRegisteredElsewhere is the half that stops the fix from
// becoming a deletion. Skipping it everywhere would satisfy the test above and
// silently take the end-of-session report away from macOS and Linux, where it
// costs a fraction of the same time and works.
func TestSessionEndIsStillRegisteredElsewhere(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		registered, retired := planFor(t, goos)
		if !registered["SessionEnd"] {
			t.Errorf("SessionEnd is not registered on %s, where the hook completes well inside "+
				"teardown — the Windows exception has become a removal", goos)
		}
		if retired["SessionEnd"] {
			t.Errorf("%s retires the SessionEnd hook it also installs", goos)
		}
	}
}

// TestAnUpgradeOnWindowsRetiresTheHookAnOlderInstallWrote drives the REAL
// registration path over a settings.json an older install left behind.
//
// ⚠ THE PLAN IS NOT THE OUTCOME, and the first version of this file only checked
// the plan. ensureHooks walks the events it is handed, so dropping SessionEnd
// from the plan changed nothing on an upgraded machine: the old registration
// stayed, the hook went on failing every exit, and the installer's own output
// said it was not registered. Reported by review before this shipped.
func TestAnUpgradeOnWindowsRetiresTheHookAnOlderInstallWrote(t *testing.T) {
	dir := t.TempDir()
	inst := &Installer{
		kit: claudeKit, goos: "windows", targetDir: dir,
		mcpURL: "http://127.0.0.1:8080/mcp", out: &strings.Builder{},
	}
	stale := inst.hookCommand(inst.sessionEndHookPath())
	settings := map[string]any{"hooks": map[string]any{
		"SessionEnd": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": stale}},
		}},
		// A hook this installer did not write must survive: an install may stop
		// shipping its own hook and may never delete somebody else's.
		"Stop": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": "bash /opt/theirs.sh"}},
		}},
	}}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	path := filepath.Join(dir, claudeKit.hooksFile)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	if err := inst.registerStopHook(); err != nil {
		t.Fatalf("registerStopHook: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if strings.Contains(string(after), "session-end-hook") {
		t.Errorf("the upgrade left the SessionEnd registration in place, so the hook goes on "+
			"failing on every exit:\n%s", after)
	}
	if !strings.Contains(string(after), "/opt/theirs.sh") {
		t.Errorf("the retirement removed a hook this installer did not write:\n%s", after)
	}
}

// TestTheWindowsOmissionIsAnnounced covers what the plan alone cannot: an absence
// nobody mentions is indistinguishable from a broken install. It also pins the
// remedy command, because a suggestion that does not run is worse than none — the
// first version printed $AGENTSMEMORY_URL, the PROXY variable, which an ordinary
// install never sets, so the line expanded to a bare path.
func TestTheWindowsOmissionIsAnnounced(t *testing.T) {
	var windows strings.Builder
	(&Installer{kit: claudeKit, out: &windows, mcpURL: "http://127.0.0.1:8080/mcp"}).
		noteSessionEndSkippedOn("windows")
	got := windows.String()
	if !strings.Contains(got, "SessionEnd") {
		t.Errorf("a Windows install says nothing about the hook it skipped:\n%s", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:8080/stats?hours=2") {
		t.Errorf("the notice does not carry a runnable stats URL built from this install's own "+
			"endpoint; an operator is told the data exists and not how to reach it:\n%s", got)
	}
	if strings.Contains(got, "$AGENTSMEMORY_URL") {
		t.Errorf("the notice suggests an environment variable an ordinary install does not set:\n%s", got)
	}

	var linux strings.Builder
	(&Installer{kit: claudeKit, out: &linux}).noteSessionEndSkippedOn("linux")
	if got := linux.String(); got != "" {
		t.Errorf("a linux install announced a Windows-only omission:\n%s", got)
	}
}
