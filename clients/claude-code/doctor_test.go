package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorIsRegistered is the rung the command's own tests cannot reach.
//
// ⚠ IT READS rootCommand's LIST, NOT THE SOURCE. Every other test here builds its
// own root, so all of them pass with `doctorCommand(),` deleted from main.go —
// verified, and that is the whole reason this exists. It is the same check
// cmd/server/doctor_test.go makes for the server's doctor, for the same reason.
func TestDoctorIsRegistered(t *testing.T) {
	for _, c := range rootCommand().Commands {
		if c.Name == "doctor" {
			return
		}
	}
	t.Fatal("rootCommand() lists no `doctor` command: the command is written, tested and " +
		"selected by nothing, which is exactly the defect it exists to report about hooks")
}

// doctorEnv writes a config dir holding hook scripts and a settings.json, and
// returns the dir. Each script is `name -> body`; each registration is
// `event -> script name`.
func doctorEnv(t *testing.T, scripts map[string]string, regs map[string][]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if regs == nil {
		return dir
	}
	hooks := map[string]any{}
	for event, names := range regs {
		entries := make([]any, 0, len(names))
		for _, n := range names {
			entries = append(entries, map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": bashHookCommand(filepath.Join(dir, n)),
				}},
			})
		}
		hooks[event] = entries
	}
	body, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, claudeKit.hooksFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runDoctor drives the real CLI against a config dir and returns its report.
func runDoctor(t *testing.T, dir string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := rootCommand()
	root.Writer = &buf
	err := root.Run(context.Background(), []string{
		"aiagentmemory", "doctor", "--target-dir", dir, "--project-dir", t.TempDir(),
	})
	return buf.String(), err
}

const injectingHookBody = "#!/usr/bin/env bash\n# hook-output: stdout-injected\necho hello\n"

// TestDoctorReportsAHookNoEventCanReach is the command's purpose, in the three
// shapes it can actually see. Silence is deliberately NOT one of them.
func TestDoctorReportsAHookNoEventCanReach(t *testing.T) {
	t.Run("installed and registered nowhere", func(t *testing.T) {
		// The shape that motivated the rewrite: the script is present, correct, and
		// the settings file registers it for no event. The previous version read
		// only the directory, so it ran this hook, saw output, and reported health.
		dir := doctorEnv(t, map[string]string{"agentsmemory-recall-hook.sh": injectingHookBody}, nil)
		report, err := runDoctor(t, dir)
		if err == nil {
			t.Fatalf("a hook registered by NO event passed:\n%s", report)
		}
		if !strings.Contains(report, "UNREGISTERED") {
			t.Errorf("the report does not name the finding:\n%s", report)
		}
	})

	t.Run("registered on an event that discards stdout", func(t *testing.T) {
		// Attempt 1's exact defect: the recall hook shipped on PreCompact, whose
		// stdout Claude Code writes to the debug log. It printed every compaction
		// and nothing read a character of it.
		dir := doctorEnv(t,
			map[string]string{"agentsmemory-recall-hook.sh": injectingHookBody},
			map[string][]string{"PreCompact": {"agentsmemory-recall-hook.sh"}})
		report, err := runDoctor(t, dir)
		if err == nil {
			t.Fatalf("a hook on a non-injecting event passed:\n%s", report)
		}
		if !strings.Contains(report, "DISCARDED") {
			t.Errorf("the report does not name the finding:\n%s", report)
		}
	})

	t.Run("registered correctly but unable to run", func(t *testing.T) {
		// Attempt 2's defect: the hook could not authenticate. It must not read as
		// a hook with nothing to say.
		dir := doctorEnv(t,
			map[string]string{"agentsmemory-recall-hook.sh": "#!/usr/bin/env bash\n# hook-output: stdout-injected\necho 'cannot reach the server' >&2\nexit 3\n"},
			map[string][]string{"SessionStart": {"agentsmemory-recall-hook.sh"}})
		report, err := runDoctor(t, dir)
		if err == nil {
			t.Fatalf("a hook that exited 3 passed:\n%s", report)
		}
		if !strings.Contains(report, "FAILED") {
			t.Errorf("the report does not name the finding:\n%s", report)
		}
		// The stderr it already captured, printed rather than described. The old
		// version collected this and then told the operator to go and read it.
		if !strings.Contains(report, "cannot reach the server") {
			t.Errorf("the hook's own stderr is missing from the report:\n%s", report)
		}
	})
}

// TestDoctorDoesNotFailOnSilence is the finding that killed the previous design,
// kept as a test so it cannot come back.
//
// ⚠ SILENCE IS THE HEALTHY STATE OF BOTH SHIPPED INJECTING HOOKS. The verify hook
// prints only when a memory drifted; the recall hook only when the palace has
// something for this branch. An earlier `doctor` called zero bytes MUTE and exited
// non-zero, so it failed on a correct install — and no single run can tell that
// apart from a hook that cannot speak, so resolving it in the exit code was not a
// check, it was a guess wearing one.
func TestDoctorDoesNotFailOnSilence(t *testing.T) {
	dir := doctorEnv(t,
		map[string]string{"agentsmemory-verify-hook.sh": "#!/usr/bin/env bash\n# hook-output: stdout-injected\necho 'asked: nothing drifted' >&2\n"},
		map[string][]string{"SessionStart": {"agentsmemory-verify-hook.sh"}})
	report, err := runDoctor(t, dir)
	if err != nil {
		t.Fatalf("a hook whose healthy state is silence was reported as a failure: %v\n%s", err, report)
	}
	if !strings.Contains(report, "silent") {
		t.Errorf("silence must be REPORTED even though it does not fail:\n%s", report)
	}
	// What the operator judges the silence with, since the exit code deliberately
	// does not.
	if !strings.Contains(report, "asked: nothing drifted") {
		t.Errorf("the hook's stderr is what makes a silence readable, and it is missing:\n%s", report)
	}
}

// TestDoctorRefusesAnEmptyUniverse covers the level above every finding: a check
// that examined nothing must not report success. It is the same shape as the defect
// `doctor` looks for, one layer out — and the message distinguishes "nothing is
// installed" from "the declaration changed", because they have different fixes.
func TestDoctorRefusesAnEmptyUniverse(t *testing.T) {
	dir := doctorEnv(t, map[string]string{
		"agentsmemory-stop-hook.sh": "#!/usr/bin/env bash\n# hook-output: transcript-only\necho hi\n",
	}, map[string][]string{"Stop": {"agentsmemory-stop-hook.sh"}})
	report, err := runDoctor(t, dir)
	if err == nil {
		t.Fatalf("a config dir with no injecting hook reported success:\n%s", report)
	}
	if !strings.Contains(err.Error(), channelStdoutInjected) {
		t.Errorf("the error does not name the declaration it looked for: %v", err)
	}
}

// TestDoctorReadsTheRegistrationNotJustTheDirectory pins the half the previous
// version never opened, by holding the directory constant and moving ONLY the
// settings file.
func TestDoctorReadsTheRegistrationNotJustTheDirectory(t *testing.T) {
	scripts := map[string]string{"agentsmemory-recall-hook.sh": injectingHookBody}
	good := doctorEnv(t, scripts, map[string][]string{"SessionStart": {"agentsmemory-recall-hook.sh"}})
	if _, err := runDoctor(t, good); err != nil {
		t.Fatalf("a correctly registered hook failed: %v", err)
	}
	bad := doctorEnv(t, scripts, map[string][]string{"Stop": {"agentsmemory-recall-hook.sh"}})
	if _, err := runDoctor(t, bad); err == nil {
		t.Fatal("the same script and the same output passed on a discarding event: the verdict " +
			"is being read off the directory, not the registration")
	}
}

// TestDoctorRefusesAnUnparseableSettingsFile guards the one false alarm this design
// could produce: a settings file it cannot read must not be reported as "registered
// nowhere", which would condemn a working install.
func TestDoctorRefusesAnUnparseableSettingsFile(t *testing.T) {
	dir := doctorEnv(t, map[string]string{"agentsmemory-recall-hook.sh": injectingHookBody}, nil)
	if err := os.WriteFile(filepath.Join(dir, claudeKit.hooksFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := runDoctor(t, dir)
	if err == nil {
		t.Fatalf("an unparseable settings file passed:\n%s", report)
	}
	if strings.Contains(report, "UNREGISTERED") {
		t.Errorf("an unreadable settings file was reported as a registration finding, which "+
			"is a false alarm on an install that may be fine:\n%s", report)
	}
}

// TestDoctorRefusesAnUnreadableSettingsFile is the other half of the parse
// refusal, and it was missing.
//
// ⚠ A MISSING FILE IS THE FINDING; AN UNREADABLE ONE IS NOT. The first version
// swallowed EVERY os.ReadFile error and returned an empty registration map, so an
// operator whose settings.json was correct but root-owned, or a directory, was told
// every hook was registered nowhere and advised to re-run `install` — the exact
// false alarm the parse branch refuses to produce, four lines away in the same
// function.
//
// ⚠ THE FIXTURE IS A DIRECTORY, NOT chmod 000. Tests here also run as root inside
// the acceptance container, where mode 000 is still readable — the check would have
// passed vacuously in exactly the environment that gates the merge. EISDIR fails for
// every user.
func TestDoctorRefusesAnUnreadableSettingsFile(t *testing.T) {
	dir := doctorEnv(t, map[string]string{"agentsmemory-recall-hook.sh": injectingHookBody}, nil)
	settings := filepath.Join(dir, claudeKit.hooksFile)
	if err := os.Remove(settings); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(settings, 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := runDoctor(t, dir)
	if err == nil {
		t.Fatalf("an unreadable settings file passed:\n%s", report)
	}
	if strings.Contains(report, "UNREGISTERED") {
		t.Errorf("an unreadable settings file was reported as a registration finding. The hooks "+
			"may be registered perfectly; this command cannot tell, and saying so is the only "+
			"honest answer:\n%s", report)
	}
	if !strings.Contains(err.Error(), "refuses to guess") {
		t.Errorf("the error does not say why it declined: %v", err)
	}
}

// TestDoctorSaysWhenAKitShipsNoInjectingHook covers the fourth empty state.
//
// ⚠ THE COMMENT ON THE EMPTY-UNIVERSE GUARD CLAIMED THERE WERE THREE. A kit that
// ships no injecting hook has an empty universe BY DESIGN — the companion hooks are
// Claude-only because codex's execution contract for those events was never
// captured — so `doctor --agent codex` fired that guard on a healthy install and
// advised re-running `install`, which could not have changed the outcome. The path
// is advertised in this command's own `--agent` usage and in the README.
func TestDoctorSaysWhenAKitShipsNoInjectingHook(t *testing.T) {
	if !claudeKit.shipsCompanionHooks {
		t.Fatal("the claude kit no longer ships companion hooks; this test's premise is gone " +
			"and doctor's whole universe with it")
	}
	if codexKit.shipsCompanionHooks {
		t.Skip("codex now ships companion hooks; this state no longer exists")
	}

	// An empty dir is right: the point is that nothing is expected there.
	dir := t.TempDir()
	var buf bytes.Buffer
	root := rootCommand()
	root.Writer = &buf
	err := root.Run(context.Background(), []string{
		"aiagentmemory", "doctor", "--agent", "codex", "--target-dir", dir,
	})
	if err == nil {
		t.Fatal("doctor reported success for a kit whose universe is empty by design; it should " +
			"say which state it is in")
	}
	if strings.Contains(err.Error(), "nothing is installed") ||
		strings.Contains(err.Error(), "the declaration changed") {
		t.Errorf("doctor reported a broken install for a kit that ships no injecting hook. "+
			"Neither disjunct is true and the advised fix cannot change the outcome: %v", err)
	}
	if !strings.Contains(err.Error(), "designed state") {
		t.Errorf("the message does not say this is the designed state: %v", err)
	}
}
