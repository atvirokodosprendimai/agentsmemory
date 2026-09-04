package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheHookProbePathReachesBashWithoutBackslashes pins the path doctor hands to
// bash on Windows.
//
// Measured 2026-09-04 (issue #224): the path reached MSYS bash with every
// backslash stripped — `C:Userszy.claudeagentsmemory-verify-hook.sh` — and doctor
// reported all three injecting hooks FAILED / exit status 127 on an install whose
// hooks ran to exit 0 by hand and in every session. The platform is a PARAMETER
// because filepath.ToSlash on a POSIX host is the identity: a test calling it
// there would pass over a function that converts nothing.
func TestTheHookProbePathReachesBashWithoutBackslashes(t *testing.T) {
	cases := []struct{ goos, joined, want string }{
		{"windows", `C:\Users\zy\.claude\agentsmemory-verify-hook.sh`, "C:/Users/zy/.claude/agentsmemory-verify-hook.sh"},
		{"windows", `C:\Users\zy tam\.claude\h.sh`, "C:/Users/zy tam/.claude/h.sh"},
		{"linux", "/home/zy/.claude/agentsmemory-verify-hook.sh", "/home/zy/.claude/agentsmemory-verify-hook.sh"},
		// A backslash is a legal byte in a POSIX filename; only Windows rewrites it.
		{"darwin", `/tmp/odd\name/h.sh`, `/tmp/odd\name/h.sh`},
	}
	for _, c := range cases {
		if got := hookProbePathOn(c.goos, c.joined); got != c.want {
			t.Errorf("%s: hookProbePathOn(%q) = %q, want %q", c.goos, c.joined, got, c.want)
		}
	}
}

// TestABridgeBinaryIsJudgedByExtensionOnWindows pins the executability test
// behind the NOT-EXECUTABLE verdict.
//
// Go never sets an execute bit on Windows — os.Stat reports 0666 for every regular
// file — so `Mode()&0o111 == 0` called EVERY bridge binary NOT-EXECUTABLE there,
// including one that served a full 41-tool handshake seconds later (issue #224,
// second comment). What the Windows loader honours is the extension.
func TestABridgeBinaryIsJudgedByExtensionOnWindows(t *testing.T) {
	cases := []struct {
		goos, pathext, file string
		mode                fs.FileMode
		want                bool
	}{
		{"windows", "", `C:\Users\zy\AppData\Roaming\Claude\bin\aiagentmemory-server.exe`, 0o666, true},
		{"windows", "", `C:\Users\zy\AppData\Roaming\Claude\bin\aiagentmemory-server`, 0o666, false},
		{"windows", ".EXE;.PS1", `C:\bin\bridge.ps1`, 0o666, true},
		{"windows", "", `C:\odd.dir\bridge`, 0o666, false},
		{"linux", "", "/home/zy/.local/bin/aiagentmemory-server", 0o755, true},
		{"linux", "", "/home/zy/.local/bin/aiagentmemory-server", 0o644, false},
		{"darwin", "", "/Users/zy/bin/aiagentmemory-server.exe", 0o644, false},
	}
	for _, c := range cases {
		if got := spawnableOn(c.goos, c.pathext, c.mode, c.file); got != c.want {
			t.Errorf("%s PATHEXT=%q %s mode=%o: spawnable=%v, want %v",
				c.goos, c.pathext, c.file, c.mode, got, c.want)
		}
	}
}

// TestDoctorSpawnsTheHookFromAPathWithThisPlatformsSeparators drives the REAL
// spawn against a hook under a path the platform itself produced.
//
// On Windows a temp dir carries backslashes, so this is the reproduction #224
// asked for and the two table tests above cannot supply: before the fix the probe
// reached bash mangled and doctor reported the hook FAILED with exit 127, which
// fails this test. There is no Windows runner in CI, so on this project's own
// hosts it is an ordinary healthy-install run — still the assertion that matters,
// that doctor reports the hook it installed as having run.
func TestDoctorSpawnsTheHookFromAPathWithThisPlatformsSeparators(t *testing.T) {
	const speaks = "#!/usr/bin/env bash\n# hook-output: stdout-injected\necho \"probed=$0\" >&2\n"
	dir := t.TempDir()
	script := filepath.Join(dir, "agentsmemory-recall-hook.sh")
	if err := os.WriteFile(script, []byte(speaks), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": hookCommand("http://127.0.0.1:9/mcp", script),
		}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, claudeKit.hooksFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runDoctor(t, dir)
	if err != nil {
		t.Fatalf("doctor failed on a healthy install whose hook path is %q: %v\n%s", script, err, out)
	}
	if !strings.Contains(out, "probed=") {
		t.Errorf("the hook did not run — its stderr never reached the report:\n%s", out)
	}
}
