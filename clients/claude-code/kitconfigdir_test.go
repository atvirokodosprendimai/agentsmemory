package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEveryKitConfigDirResolvesForThisPlatform derives its universe from the same
// list resolveAgentKits serves, so a sixth agent joins the check on the commit
// that adds it rather than repeating this bug for whichever platform its author
// did not have.
//
// ⚠ ONE KIT SHIPPED A macOS LITERAL AND REPORTED SUCCESS EVERYWHERE ELSE.
// `install --agent claude-desktop` on Windows resolved
// C:\Users\<user>\Library\Application Support\Claude — a path that means nothing
// there — created it beside the real config, registered the MCP into it, printed
// [ok] and told the operator to restart Desktop for tools that were never
// registered. Linux was broken identically. Reported 2026-08-31. It is
// §Reachability's failure mode with a success message attached.
func TestEveryKitConfigDirResolvesForThisPlatform(t *testing.T) {
	kits, err := resolveAgentKits(agentAll)
	if err != nil {
		t.Fatalf("resolve every kit: %v", err)
	}
	if len(kits) < 2 {
		t.Fatalf("only %d kit(s) in the universe, so this test would pass over almost nothing", len(kits))
	}
	// ⚠ EVERY PLATFORM, NOT JUST THIS ONE. The literal that caused this was
	// correct on macOS, so a host-only check passed on the machine that shipped
	// it — measured: restoring the bug left the first draft of this test green.
	const home = "/home/u"
	for _, kit := range kits {
		for _, goos := range []string{"darwin", "windows", "linux"} {
			t.Run(kit.name+"/"+goos, func(t *testing.T) {
				// base "" is the fallback path, which is where a kit that ignores the
				// platform gives itself away.
				dir := kit.globalConfigDirOn(goos, home, "")
				if dir == "" {
					t.Fatalf("%s resolved no config dir at all", kit.name)
				}
				if reason := foreignPlatformPath(goos, dir); reason != "" {
					t.Errorf("on %s, %s resolves %q, which %s. An install writes there, registers "+
						"the MCP there, and reports success — the operator restarts and the tools "+
						"are absent with nothing saying why", goos, kit.name, dir, reason)
				}
			})
		}
	}
}

// TestAKitCarryingAForeignLiteralIsCaught is the falsifiability half: a corpus in
// which every kit is already correct cannot exercise the branch that reports one,
// so this supplies kits that are wrong on purpose and drives the SAME resolution
// and the SAME judgement the gate uses rather than a copy of either.
func TestAKitCarryingAForeignLiteralIsCaught(t *testing.T) {
	const home = "/home/u"
	// The literal the real kit used to carry, plus the two it would have needed.
	// Exactly one of the three is right on any given platform, so on every
	// platform at least one of these must be judged foreign — that is what proves
	// the check discriminates rather than accepting everything.
	for _, globalDir := range []string{
		"Library/Application Support/Claude",
		"AppData/Roaming/Claude",
		".config/Claude",
	} {
		fixture := agentKit{name: "fixture", globalDir: globalDir}
		if reason := foreignPlatformPath(runtime.GOOS, fixture.globalConfigDirOn(runtime.GOOS, home, "")); reason != "" {
			return // caught one, which is all this needs to prove
		}
	}
	t.Errorf("no foreign literal was caught on %s, so the check accepts any path and the gate "+
		"above reports agreement over nothing", runtime.GOOS)
}

// foreignPlatformPath names why a resolved config dir belongs to another
// platform, or returns "" when it is plausible here. It is the judgement both
// tests share.
func foreignPlatformPath(goos, dir string) string {
	slashed := filepath.ToSlash(dir)
	switch goos {
	case "darwin":
		if strings.Contains(slashed, "AppData/Roaming") {
			return "is a Windows application-data path"
		}
		if strings.Contains(slashed, "/.config/") {
			return "is an XDG path, and macOS applications use Library/Application Support"
		}
	case "windows":
		if strings.Contains(slashed, "Library/Application Support") {
			return "is a macOS path"
		}
		if strings.Contains(slashed, "/.config/") {
			return "is an XDG path"
		}
	default:
		if strings.Contains(slashed, "Library/Application Support") {
			return "is a macOS path"
		}
		if strings.Contains(slashed, "AppData/Roaming") {
			return "is a Windows application-data path"
		}
	}
	return ""
}

// TestTheDesktopKitResolvesEachPlatformsRealLocation pins all three locations
// from one machine, which is the property the gate above cannot have.
//
// ⚠ RUNNING ONLY THE HOST'S PLATFORM IS HOW THIS BUG SHIPPED. On macOS the old
// literal is correct, so a check that asks only about the running OS stays green
// on the author's machine while two of three platforms are broken. Driving the
// decision function with each goos is what makes the defect reproducible here.
func TestTheDesktopKitResolvesEachPlatformsRealLocation(t *testing.T) {
	const home = "/home/u"
	for _, tc := range []struct {
		goos, base, want string
	}{
		// With the OS's own config base, which is the healthy path.
		{"darwin", "/home/u/Library/Application Support", "/home/u/Library/Application Support/Claude"},
		{"windows", `C:\Users\u\AppData\Roaming`, `C:\Users\u\AppData\Roaming/Claude`},
		{"linux", "/home/u/.config", "/home/u/.config/Claude"},
		// And with it missing, where the fallback must stay on the right platform
		// rather than reaching for a literal that is right on one of them.
		{"darwin", "", "/home/u/Library/Application Support/Claude"},
		{"windows", "", "/home/u/AppData/Roaming/Claude"},
		{"linux", "", "/home/u/.config/Claude"},
	} {
		name := tc.goos
		if tc.base == "" {
			name += "/no-config-base"
		}
		t.Run(name, func(t *testing.T) {
			got := filepath.ToSlash(desktopConfigDirOn(tc.goos, home, tc.base))
			if want := filepath.ToSlash(tc.want); got != want {
				t.Errorf("desktopConfigDirOn(%q, base=%q) = %q, want %q — Claude Desktop reads "+
					"exactly one location per platform, and writing to another one registers "+
					"the MCP where nothing will look for it", tc.goos, tc.base, got, want)
			}
			if reason := foreignPlatformPath(tc.goos, got); reason != "" {
				t.Errorf("on %s the kit resolves %q, which %s", tc.goos, got, reason)
			}
		})
	}
}
