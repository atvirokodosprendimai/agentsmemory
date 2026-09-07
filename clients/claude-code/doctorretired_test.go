package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDoctorDoesNotFlagAHookTheInstallerRetires holds the two commands to one
// answer about one file.
//
// `install` writes the SessionEnd hook to disk and, on Windows, deliberately
// registers it for no event: the hook needs ~3.2s, process creation there costs
// ~1s, and it loses the teardown race, reporting "Hook cancelled" on every exit
// (#150). `doctor` then read that intended state as UNREGISTERED, exited 1 on a
// clean install, and prescribed `aiagentmemory install` — the command that had
// just produced it. Reported as #393, alongside a doctor run whose every other
// check was green, including the MCP handshake.
//
// The interesting half is the second subtest: the exemption has to be NARROW.
// An exemption keyed on the filename alone would silence a genuinely unwired
// SessionEnd hook everywhere, which is the finding this check exists to make.
func TestDoctorDoesNotFlagAHookTheInstallerRetires(t *testing.T) {
	dir := t.TempDir()
	// The declaration is what puts a script in unwiredHooksIn's universe at all;
	// a file with no `# hook-output:` line is staleHooksIn's business.
	script := filepath.Join(dir, sessionEndHookFile)
	if err := os.WriteFile(script, []byte("#!/bin/sh\n# hook-output: none\n"), 0o755); err != nil {
		t.Fatalf("write the hook: %v", err)
	}
	// Registered for nothing, which is exactly what install leaves behind on the
	// platform where it retires the hook.
	registered := map[string]hookRegistration{}

	t.Run("retired on windows is reported and not counted", func(t *testing.T) {
		got := unwiredHooksIn(dir, map[string]string{}, registered, claudeKit, "windows")
		if len(got) != 1 {
			t.Fatalf("got %d verdict(s), want exactly 1: %+v", len(got), got)
		}
		if got[0].bad {
			t.Errorf("the retired hook counts toward a non-zero exit; doctor then fails a clean "+
				"install and prescribes the command that produced the state: %+v", got[0])
		}
		if got[0].label != "RETIRED" {
			t.Errorf("label = %q, want RETIRED — an operator wondering where the session report "+
				"went needs the file named, not hidden", got[0].label)
		}
	})

	t.Run("unwired anywhere else is still a finding", func(t *testing.T) {
		got := unwiredHooksIn(dir, map[string]string{}, registered, claudeKit, "linux")
		if len(got) != 1 {
			t.Fatalf("got %d verdict(s), want exactly 1: %+v", len(got), got)
		}
		if !got[0].bad || got[0].label != "UNREGISTERED" {
			t.Errorf("a SessionEnd hook that no event runs on linux is a real finding; got %+v", got[0])
		}
	})
}

// TestTheRetirementDoctorRecognisesIsTheOneTheInstallerPlans is the coupling
// itself, and the reason there is no second list to keep in step.
//
// It reads the installer's OWN plan for each platform and requires doctor's
// verdict to agree: a platform whose plan retires the SessionEnd registration
// must not have that state counted against it, and a platform that registers it
// must still be told when nothing runs it. Change the rule in one place and this
// fails until the other follows.
//
// ⚠ IT PINS AGREEMENT, NOT THE RULE, AND ITS NAME OVERSTATES WHAT IT COVERS.
// Both sides read one predicate, so severing that predicate — `return false` —
// leaves them agreeing trivially and this test GREEN: `hookPlansOn` stops
// retiring anywhere, doctor stops recognising retirement anywhere, and every
// subtest lands consistently in the `!retires && bad` branch. It can see a
// DIVERGENCE, which is the shape #393 actually reported, and it cannot see the
// rule disappearing. Measured by a reviewer on this PR rather than argued.
//
// What holds the rule is three other tests — TestSessionEndIsNotRegisteredOnWindows
// and TestAnUpgradeOnWindowsRetiresTheHookAnOlderInstallWrote in
// sessionendplatform_test.go, and TestDoctorDoesNotFlagAHookTheInstallerRetires
// above. All three go red on a constant predicate. Delete them believing this
// test covers them and a constant ships green, which is the inference this
// paragraph exists to prevent.
//
// The platform list below is hand-written, which is honest while the predicate
// names one kit and one GOOS. It would go silent if a second kit ever retired
// something; deriving the universe is not worth it today and is worth it then.
func TestTheRetirementDoctorRecognisesIsTheOneTheInstallerPlans(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, sessionEndHookFile)
	if err := os.WriteFile(script, []byte("#!/bin/sh\n# hook-output: none\n"), 0o755); err != nil {
		t.Fatalf("write the hook: %v", err)
	}

	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			inst := &Installer{kit: claudeKit, targetDir: dir}
			var planned, retires bool
			for _, p := range inst.hookPlansOn(goos) {
				if p.event != "SessionEnd" {
					continue
				}
				planned = true
				retires = p.retire
			}
			if !planned {
				t.Fatalf("the installer has no SessionEnd plan at all on %s; this test can no "+
					"longer see the decision it exists to couple", goos)
			}

			got := unwiredHooksIn(dir, map[string]string{}, map[string]hookRegistration{}, claudeKit, goos)
			if len(got) != 1 {
				t.Fatalf("got %d verdict(s), want exactly 1: %+v", len(got), got)
			}
			if retires && got[0].bad {
				t.Errorf("install RETIRES the SessionEnd registration on %s and doctor counts its "+
					"absence as a finding: the two commands disagree about one file", goos)
			}
			if !retires && !got[0].bad {
				t.Errorf("install REGISTERS SessionEnd on %s, so a hook no event runs is a real "+
					"finding and doctor stayed quiet: %+v", goos, got[0])
			}
		})
	}
}
