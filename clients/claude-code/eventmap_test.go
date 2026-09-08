package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEventMapIsRegistered covers the rung this command's own tests cannot see.
//
// Every other test here builds its own pieces and calls them directly, so all of
// them pass with `eventMapCommand(),` deleted from main.go — the command written,
// tested, and reachable by nobody. That is the defect the command itself reports
// about hooks, and §Reachability records it against `doctor` and `playbook`
// before this.
func TestEventMapIsRegistered(t *testing.T) {
	for _, c := range rootCommand().Commands {
		if c.Name == "eventmap" {
			return
		}
	}
	t.Fatal("rootCommand() lists no `eventmap` command: it maps what is reachable and is itself " +
		"reachable from nothing")
}

// realRegistrations are the two shapes measured in ~/.claude/settings.json on
// 2026-09-07 (issue #416), verbatim apart from the home directory.
//
// ⚠ THEY ARE COPIED FROM A REAL FILE, NOT BUILT BY THE INSTALLER, AND THAT IS
// THE POINT. Every existing fixture in this package is constructed from
// `hookCommand`/`hookCommandWithWing`, which emit single-quoted values by
// construction — so the shape that broke everything CANNOT appear in a fixture
// derived from the code under test. A test built that way is green over the bug.
const (
	realDerived = `AGENTSMEMORY_MCP_URL='http://localhost:8080/mcp' AGENTSMEMORY_WING="$(bash '/home/u/.claude/agentsmemory-wing.sh')" bash -- '/home/u/.claude/agentsmemory-recall-hook.sh'`
	realPinned  = `AGENTSMEMORY_MCP_URL='http://localhost:8080/mcp' AGENTSMEMORY_WING='wing_agentmemories' bash -- '/home/u/.claude/agentsmemory-recall-hook.sh'`
)

// TestTheMapperSeesARegistrationTheInstallerCannot is the reason this command
// exists, asserted in both directions at once.
//
// The installer's parser must keep refusing the derived shape — it is strict on
// purpose, because a command it parses is one it may drop or reproduce the
// environment of — and the mapper must read it anyway, because a registration
// nothing can parse is a registration nothing can report.
func TestTheMapperSeesARegistrationTheInstallerCannot(t *testing.T) {
	if _, ok := installerHookPath(realDerived); ok {
		t.Error("installerHookPath now parses the derived shape. If that is deliberate, this " +
			"mapper's tolerant parser is redundant and the duplicate it was written to find is " +
			"one the installer can clean by itself — delete one of the two, do not keep both")
	}
	if _, ok := installerHookPath(realPinned); !ok {
		t.Fatal("installerHookPath cannot parse the shape the installer itself writes")
	}

	for _, tc := range []struct{ name, cmd string }{
		{"derived", realDerived},
		{"pinned", realPinned},
	} {
		script, env, ok := tolerantHookPath(tc.cmd)
		if !ok {
			t.Fatalf("tolerantHookPath(%s) failed; it cannot report what it cannot read", tc.name)
		}
		if filepath.Base(script) != "agentsmemory-recall-hook.sh" {
			t.Errorf("tolerantHookPath(%s) = %q, want the recall hook", tc.name, script)
		}
		if !strings.Contains(env, "AGENTSMEMORY_WING") {
			t.Errorf("tolerantHookPath(%s) dropped the wing assignment from %q; the prefix is what "+
				"tells two registrations of one hook apart", tc.name, env)
		}
	}
}

// TestOneHookRegisteredTwiceIsOneFinding holds the identity rule: the pair
// (event, script) is the hook, and the environment prefix is that hook's
// configuration rather than a second hook.
func TestOneHookRegisteredTwiceIsOneFinding(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	body := `{"hooks":{"SessionStart":[{"hooks":[
	  {"type":"command","command":` + jsonQuote(realDerived) + `},
	  {"type":"command","command":` + jsonQuote(realPinned) + `}]}]}}`
	if err := os.WriteFile(settings, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	regs, err := scanSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 2 {
		t.Fatalf("read %d registration(s), want 2 — the mapper skipped one it could not parse, "+
			"which is the blindness it exists to remove", len(regs))
	}
	m := &eventMap{Registrations: regs}
	m.Findings = judge(m)

	var dup, unparseable int
	for _, f := range m.Findings {
		switch f.Class {
		case "duplicate":
			dup++
		case "unparseable":
			unparseable++
		}
	}
	if dup != 1 {
		t.Errorf("reported %d duplicate finding(s), want 1: two entries naming one script on one "+
			"event are one hook running twice, however they are parameterised", dup)
	}
	if unparseable != 1 {
		t.Errorf("reported %d unparseable finding(s), want 1 — only the derived entry is one", unparseable)
	}
}

// TestAStateFileWrittenAndNeverReadIsReported is the orphan check, and its
// fixture is deliberately shaped like the real defect: the path is bound to a
// variable on one line and redirected into many lines later.
//
// ⚠ A LINE-LOCAL RULE GETS THIS BACKWARDS. The first version of stateDirection
// judged each line that mentioned the family name, so it read
// `REGROUND_DIR="$STATE/agentsmemory-reground"` as a mention and never saw the
// redirection below it — classifying the only write as a read, which suppressed
// this finding entirely on the real kit.
func TestAStateFileWrittenAndNeverReadIsReported(t *testing.T) {
	writer := `#!/usr/bin/env bash
# hook-output: none
MARKER="${TMPDIR}/agentsmemory-reground/$SESSION"
mkdir -p "$(dirname "$MARKER")"
printf '%s\n' "$TASK" > "$MARKER"
`
	reader := `#!/usr/bin/env bash
# hook-output: none
NOTE="${TMPDIR}/agentsmemory-touched/$SESSION"
cat "$NOTE"
`
	writerAndReader := `#!/usr/bin/env bash
# hook-output: none
NOTE="${TMPDIR}/agentsmemory-touched/$SESSION"
printf '%s\n' "$PATHNAME" >> "$NOTE"
`
	kit := t.TempDir()
	for name, body := range map[string]string{
		"a-writes-reground.sh": writer,
		"b-reads-touched.sh":   reader,
		"c-writes-touched.sh":  writerAndReader,
	} {
		if err := os.WriteFile(filepath.Join(kit, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	scripts, err := scanKit(kit)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]hookScript{}
	for _, s := range scripts {
		byName[s.Name] = s
	}
	if got := byName["a-writes-reground.sh"].StateWrite; len(got) != 1 || got[0] != "agentsmemory-reground" {
		t.Fatalf("the re-ground marker is written on a line that names only the variable; "+
			"stateDirection reported writes=%v reads=%v", got, byName["a-writes-reground.sh"].StateRead)
	}

	m := &eventMap{Scripts: scripts}
	m.Findings = judge(m)
	var orphans []string
	for _, f := range m.Findings {
		if f.Class == "orphan-state" {
			orphans = append(orphans, f.Detail)
		}
	}
	if len(orphans) != 1 {
		t.Fatalf("want exactly one orphan (reground: written, never read); touched is written AND "+
			"read, so it is not one. Got %d: %v", len(orphans), orphans)
	}
	if !strings.Contains(orphans[0], "agentsmemory-reground") {
		t.Errorf("the orphan reported is not the re-ground marker: %s", orphans[0])
	}
}

// TestAnAssignmentIsNotAWrite pins the false positive that shipped in the first
// draft: `CACHE="$D/agentsmemory-status-$(id -u 2>/dev/null).txt"` mentions a
// family AND contains ">", and an "any redirection on the line" rule called the
// cache's READER its writer.
func TestAnAssignmentIsNotAWrite(t *testing.T) {
	body := `#!/usr/bin/env bash
CACHE="${TMPDIR}/agentsmemory-status-$(id -u 2>/dev/null || echo 0).txt"
cat "$CACHE" 2>/dev/null
`
	writes, reads := stateDirection(body)
	if len(writes) != 0 {
		t.Errorf("classified %v as written; the only redirection on that line is `2>/dev/null` "+
			"inside the assignment's own command substitution", writes)
	}
	if len(reads) != 1 || reads[0] != "agentsmemory-status" {
		t.Errorf("reads = %v, want [agentsmemory-status]", reads)
	}
}

func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TestTheShippedKitHasNoStructuralFindings is the half the first version of this
// command was missing, and the review of PR #417 is what named it.
//
// ⚠ EVERY OTHER TEST HERE ASSERTS THAT A DEFECT IS CAUGHT. None asserted that a
// healthy kit is quiet, so nothing said whether this command can PASS — and it
// could not: the re-ground marker made `eventmap` exit non-zero on every correct
// install, which makes it useless as a gate. That is exactly how #393 shipped,
// where `doctor` exited 1 on a freshly installed v0.0.125 and an operator found
// it rather than the suite; this repository's answer there was
// TestDoctorDoesNotFailOnSilence, and this is its sibling.
//
// It runs over the REAL shipped hooks with an empty registration set, so it
// judges the artifact rather than a fixture built to agree with it. A finding
// here means the kit itself is misshapen, which is the only thing this command
// should ever say about a clean install.
func TestTheShippedKitHasNoStructuralFindings(t *testing.T) {
	kit := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks")
	scripts, err := scanKit(kit)
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("scanned no script at all; an empty universe is not a clean bill of health")
	}
	m := &eventMap{KitDir: kit, Scripts: scripts}
	m.Findings = judge(m)
	for _, f := range m.Findings {
		t.Errorf("the shipped kit reports [%s] %s\n"+
			"  A finding with no settings file to blame is a finding about the kit. If the state "+
			"file's reader is outside this kit — a Monitor the session arms, say — the script that "+
			"writes it must declare `# state-consumer: <family> <reason>`; if it is a real orphan, "+
			"the consumer was never written.", f.Class, f.Detail)
	}
}

// TestAnExternalConsumerNeedsAReason keeps the declaration from becoming the
// dodge, the same way TestANonInjectedChannelIsJustified does for a quieter
// output channel: the reason is the review.
func TestAnExternalConsumerNeedsAReason(t *testing.T) {
	kit := t.TempDir()
	body := `#!/usr/bin/env bash
# hook-output: none
# state-consumer: agentsmemory-reground
MARKER="${TMPDIR}/agentsmemory-reground/$SESSION"
printf '%s\n' "$TASK" > "$MARKER"
`
	if err := os.WriteFile(filepath.Join(kit, "writer.sh"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	scripts, err := scanKit(kit)
	if err != nil {
		t.Fatal(err)
	}
	m := &eventMap{Scripts: scripts}
	if len(judge(m)) == 0 {
		t.Fatal("a `# state-consumer:` line with no reason was accepted; the declaration is an " +
			"escape hatch, and an escape hatch nobody has to justify is just a way to silence the gate")
	}
}

// TestADoubledWriterIsReportedAsARace is the decidable half of "is there a race".
//
// No shell analysis is attempted. What IS decidable from the two scans: a state
// file whose writing script is registered twice has two copies running for the
// same trigger, so any check-then-act inside it is racing itself. The touched
// hook is exactly that shape — `grep -qxF "$REL" "$LIST"` then `>> "$LIST"` — and
// it is safe only while one copy runs.
//
// ⚠ IT REPORTS THE SHAPE, NOT AN INTERLEAVING. A duplicate writer with no
// check-then-act is harmless and this cannot tell the difference, which is why
// the detail says what to look for rather than asserting a defect.
func TestADoubledWriterIsReportedAsARace(t *testing.T) {
	kit := t.TempDir()
	body := `#!/usr/bin/env bash
# hook-output: none
LIST="${TMPDIR}/agentsmemory-touched/$SESSION"
if [ -f "$LIST" ] && grep -qxF "$REL" "$LIST"; then exit 0; fi
printf '%s\n' "$REL" >> "$LIST"
`
	if err := os.WriteFile(filepath.Join(kit, "agentsmemory-touched-hook.sh"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	scripts, err := scanKit(kit)
	if err != nil {
		t.Fatal(err)
	}
	cmd := `bash -- '/h/.claude/agentsmemory-touched-hook.sh'`

	t.Run("registered twice", func(t *testing.T) {
		m := &eventMap{Scripts: scripts, Registrations: []registration{
			{Event: "PostToolUse", Script: "agentsmemory-touched-hook.sh", Raw: cmd, Parsed: true},
			{Event: "PostToolUse", Script: "agentsmemory-touched-hook.sh", Raw: cmd, Parsed: true},
		}}
		if !hasClass(judge(m), "duplicate-writer") {
			t.Error("a state file written by a script registered twice was not reported; two copies " +
				"run for one trigger, so the read-then-append dedupe inside it is racing itself")
		}
	})

	// The healthy direction, whose absence is what let #393 ship: one registration
	// is not a race, and a checker that cannot be quiet cannot gate.
	//
	// ⚠ THE SECOND CASE IS THE ONE THAT CAUGHT A FALSE FINDING, and the first
	// version of this test could not express it. A script registered once on each
	// of TWO events is registered twice and runs ONCE PER TRIGGER — nothing is
	// concurrent — but the predicate keyed on the script alone, so it reported a
	// race. The fixture put both registrations on one event, which is a fixture
	// that cannot express the case rather than one that is neutral about it.
	// Caught in review of #431.
	for _, tc := range []struct {
		name string
		regs []registration
	}{
		{"one registration", []registration{
			{Event: "PostToolUse", Script: "agentsmemory-touched-hook.sh", Raw: cmd, Parsed: true},
		}},
		{"one registration on each of two events", []registration{
			{Event: "PostToolUse", Script: "agentsmemory-touched-hook.sh", Raw: cmd, Parsed: true},
			{Event: "SessionStart", Script: "agentsmemory-touched-hook.sh", Raw: cmd, Parsed: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &eventMap{Scripts: scripts, Registrations: tc.regs}
			if hasClass(judge(m), "duplicate-writer") {
				t.Errorf("reported a race over %s: a trigger fires one of them, so nothing runs "+
					"concurrently with anything", tc.name)
			}
		})
	}
}

// TestTheInvocationCountIncludesHooksThisKitDidNotWrite pins the claim the
// section makes about itself.
//
// ⚠ THE FIRST VERSION PRINTED "the event's cost is the whole list" WHILE COUNTING
// ONLY OUR OWN. Other products do not register `bash -- '<path>'`: one writes a
// bare quoted path, another an inline pipeline, and tolerantHookPath returned
// !ok for both — so they were dropped and the total was our share, under a
// sentence claiming otherwise. A count that silently excludes most of what runs
// is worse than no count, because it reads as the answer.
func TestTheInvocationCountIncludesHooksThisKitDidNotWrite(t *testing.T) {
	p := writeRawSettings(t, `{"hooks":{"PreToolUse":[{"hooks":[
	  {"type":"command","command":"\"$HOME/.claude/hooks/some-other-tool-gate\""},
	  {"type":"command","command":`+jsonQuote(`bash -- '/h/.claude/agentsmemory-anchor-cue-hook.sh'`)+`},
	  {"type":"command","command":`+jsonQuote(`bash -- '/h/.claude/agentsmemory-anchor-cue-hook.sh'`)+`}]}]}}`)
	regs, err := scanSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 3 {
		t.Fatalf("read %d registrations, want 3 — a command this kit cannot parse is still a hook "+
			"that runs, and dropping it makes the invocation count a different number than the "+
			"one the report claims", len(regs))
	}
	var foreign, ours int
	for _, r := range regs {
		if r.Foreign {
			foreign++
		} else {
			ours++
		}
	}
	if foreign != 1 || ours != 2 {
		t.Errorf("foreign=%d ours=%d, want 1 and 2", foreign, ours)
	}
	// Foreign entries are counted and never judged: this command has no standing
	// to report a duplicate in software it does not own.
	m := &eventMap{Registrations: regs}
	for _, f := range judge(m) {
		if strings.Contains(f.Detail, "some-other-tool-gate") {
			t.Errorf("judged another tool's hook: %s", f.Detail)
		}
	}
}

func hasClass(fs []finding, class string) bool {
	for _, f := range fs {
		if f.Class == class {
			return true
		}
	}
	return false
}
