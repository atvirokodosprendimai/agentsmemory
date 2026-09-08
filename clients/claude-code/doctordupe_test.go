package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two shapes measured in a real ~/.claude/settings.json on 2026-09-08
// (issue #416). They differ only in how AGENTSMEMORY_WING is written, and that
// difference is the whole defect: the first is what the installer emits and the
// second is what a hand-written per-project wing resolution looks like.
//
// ⚠ COPIED FROM A REAL FILE. Every other registration fixture in this package is
// built by hookCommand/hookCommandWithWing, which emit single-quoted values by
// construction — so the shape that blinded doctor CANNOT appear in a fixture
// derived from the code under test, and a test built that way is green over the
// bug.
const (
	dupePinned  = `AGENTSMEMORY_MCP_URL='http://x/mcp' AGENTSMEMORY_WING='wing_a' bash -- '/h/.claude/agentsmemory-recall-hook.sh'`
	dupeDerived = `AGENTSMEMORY_MCP_URL='http://x/mcp' AGENTSMEMORY_WING="$(bash '/h/.claude/wing.sh')" bash -- '/h/.claude/agentsmemory-recall-hook.sh'`
)

// writeRawSettings writes a settings.json body VERBATIM.
//
// ⚠ NOT writeSettings, which is the sibling helper in doctorpeer_test.go: that
// one builds registrations from kit script names, so every command it produces
// is the shape the installer emits. It cannot express the hand-written
// registration this file is about, which is part of why the blind spot survived
// — the only fixture builder in the package could not construct the offender.
func writeRawSettings(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDoctorSeesADuplicateItCannotParse is the case that shipped silent.
//
// doctor's duplicate detection was already correct — registeredHookEvents fills
// `duplicated`, judgeHook reports DUPLICATED first and marks it a finding. It
// never fired, because the same function SKIPPED any command installerHookPath
// could not read, and one of the two registrations was always that. A script
// seen once is not a duplicate, so the detector reported nothing over a config
// where every hook ran twice, and `doctor` exited 0.
func TestDoctorSeesADuplicateItCannotParse(t *testing.T) {
	p := writeRawSettings(t, `{"hooks":{"SessionStart":[{"hooks":[
	  {"type":"command","command":`+jsonQuote(dupePinned)+`},
	  {"type":"command","command":`+jsonQuote(dupeDerived)+`}]}]}}`)

	regs, err := registeredHookEvents(p)
	if err != nil {
		t.Fatal(err)
	}
	reg, ok := regs["agentsmemory-recall-hook.sh"]
	if !ok {
		t.Fatal("the recall hook is registered twice and doctor read neither")
	}
	if len(reg.duplicated) == 0 {
		t.Fatalf("read the registrations but reported no duplicate (events=%v) — one of the two "+
			"carries an assignment the installer's parser refuses, and skipping it makes a doubled "+
			"hook look singly registered", reg.events)
	}
	// ⚠ envPartial is deliberately NOT asserted here, and the first draft of this
	// test got it wrong. `env` is taken from the FIRST registration that supplies
	// one — pinned, in this fixture — and that one IS reproducible, so doctor runs
	// it faithfully and the flag is correctly false. The flag is about the env
	// doctor actually uses, not about every registration it can see; the ordering
	// case is the subtest below.
}

// TestDoctorSaysWhenItCannotReproduceTheEnvironment covers the ordering that
// makes the difference: when the FIRST registration is the one carrying a
// command substitution, the environment doctor would run the hook with is SHORT
// of what the agent uses, and a run under it is a reconstruction rather than the
// registration. hookCommandEnv's own comment records what that cost before.
func TestDoctorSaysWhenItCannotReproduceTheEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   string
		second  string
		partial bool
	}{
		{"derived first", dupeDerived, dupePinned, true},
		{"installer first", dupePinned, dupeDerived, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeRawSettings(t, `{"hooks":{"SessionStart":[{"hooks":[
			  {"type":"command","command":`+jsonQuote(tc.first)+`},
			  {"type":"command","command":`+jsonQuote(tc.second)+`}]}]}}`)
			regs, err := registeredHookEvents(p)
			if err != nil {
				t.Fatal(err)
			}
			reg := regs["agentsmemory-recall-hook.sh"]
			if reg.envPartial != tc.partial {
				t.Errorf("envPartial = %v, want %v — the environment doctor would run this hook "+
					"with comes from the FIRST registration, and saying nothing when that one "+
					"cannot be reproduced is how a reconstruction gets reported as the registration",
					reg.envPartial, tc.partial)
			}
		})
	}
}

// TestDoctorIsQuietOnASinglyRegisteredHook is the other direction, and it is the
// one whose absence let #393 ship: every test asserted a defect was caught and
// none asserted that a correct install is quiet. Reading tolerantly must not
// turn one registration into two.
func TestDoctorIsQuietOnASinglyRegisteredHook(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"installer shape", dupePinned},
		{"hand-written shape", dupeDerived},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeRawSettings(t, `{"hooks":{"SessionStart":[{"hooks":[
			  {"type":"command","command":`+jsonQuote(tc.cmd)+`}]}]}}`)
			regs, err := registeredHookEvents(p)
			if err != nil {
				t.Fatal(err)
			}
			reg, ok := regs["agentsmemory-recall-hook.sh"]
			if !ok {
				t.Fatal("a single valid registration was not read at all")
			}
			if len(reg.duplicated) != 0 {
				t.Errorf("reported %v as duplicated over ONE registration; a checker that cannot "+
					"pass on a healthy install is not a checker", reg.duplicated)
			}
			if len(reg.events) != 1 || reg.events[0] != "SessionStart" {
				t.Errorf("events = %v, want [SessionStart]", reg.events)
			}
		})
	}
}

// TestInstallCollapsesIdenticalRegistrationsAndNotTheseOnes is the fact doctor's
// new warning rests on, pinned so the warning cannot outlive it.
//
// Measured 2026-09-08 against a real --config-dir install: identical entries
// 2 -> 1, entries differing only in an assignment prefix 2 -> 2. The second is
// why the DUPLICATED verdict now says re-running install will not help — the
// installer drops the copy it can parse, appends its own, and foreignHookPredicate
// spares the one it cannot read, so the count is unchanged.
//
// ⚠ Driven through ensureHooksReporting rather than the binary. Three attempts to
// measure this by running `install` produced three wrong answers, the last because
// --local resolves to the REAL config dir and CLAUDE_CONFIG_DIR is never read.
// Reading resolveInstallTarget settled in one pass what probing could not.
func TestInstallCollapsesIdenticalRegistrationsAndNotTheseOnes(t *testing.T) {
	ours := `AGENTSMEMORY_MCP_URL='http://x/mcp' bash -- '/h/.claude/agentsmemory-recall-hook.sh'`

	t.Run("identical entries collapse", func(t *testing.T) {
		p := writeRawSettings(t, `{"hooks":{"SessionStart":[{"hooks":[
		  {"type":"command","command":`+jsonQuote(ours)+`,"timeout":75},
		  {"type":"command","command":`+jsonQuote(ours)+`,"timeout":75}]}]}}`)
		if _, _, err := ensureHooksReporting(p, []hookReg{{
			event: "SessionStart", cmd: ours, obsolete: foreignHookPredicate(ours),
		}}, ""); err != nil {
			t.Fatal(err)
		}
		if n := countCommand(t, p, ours); n != 1 {
			t.Errorf("identical entries left %d registrations, want 1 — the remedy doctor "+
				"prescribes has to work for the case it CAN reach", n)
		}
	})

	t.Run("an unparseable sibling survives", func(t *testing.T) {
		p := writeRawSettings(t, `{"hooks":{"SessionStart":[{"hooks":[
		  {"type":"command","command":`+jsonQuote(ours)+`,"timeout":75},
		  {"type":"command","command":`+jsonQuote(dupeDerived)+`,"timeout":75}]}]}}`)
		if _, _, err := ensureHooksReporting(p, []hookReg{{
			event: "SessionStart", cmd: ours, obsolete: foreignHookPredicate(ours),
		}}, ""); err != nil {
			t.Fatal(err)
		}
		total := countCommand(t, p, ours) + countCommand(t, p, dupeDerived)
		if total != 2 {
			t.Fatalf("the unparseable registration was collapsed after all (%d left). If the "+
				"installer can now reach it, doctor's warning that re-running install will not "+
				"help is false and must be removed with this test", total)
		}
	})
}

func countCommand(t *testing.T, path, cmd string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, gs := range doc.Hooks {
		for _, g := range gs {
			for _, h := range g.Hooks {
				if h.Command == cmd {
					n++
				}
			}
		}
	}
	return n
}

// TestTheRemedyWarningPrintsExactlyWhenInstallCannotHelp pins the SENTENCE an
// operator reads, in both directions.
//
// ⚠ WITHOUT THIS, DELETING THE WARNING IS GREEN. The sibling test pins the FACT
// the warning rests on — install collapses identical entries and not these — and
// passes identically whether the sentence is printed or not. §Reachability's rule
// is that a test for "X is now available" must fail when X is removed, and the
// review of this change proved the mutant survived: six lines deleted,
// `grep -c "will NOT collapse"` → 0, package still ok.
//
// The second direction is the one that caught the real defect. The warning was
// first keyed on envPartial, which reports whether the environment doctor would
// RUN the hook with is short — a fact about the first entry supplying an env, not
// about whether any entry is unreadable. On installer-first ordering, which is
// what the duplicate fixture uses, envPartial is false and the warning stayed
// silent over exactly the file it was written for.
func TestTheRemedyWarningPrintsExactlyWhenInstallCannotHelp(t *testing.T) {
	const remedy = "will NOT collapse these"

	for _, tc := range []struct {
		name  string
		first string
		want  bool
	}{
		{"unreadable sibling, installer entry read first", dupePinned, true},
		{"unreadable sibling, derived entry read first", dupeDerived, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			second := dupeDerived
			if tc.first == dupeDerived {
				second = dupePinned
			}
			p := writeRawSettings(t, `{"hooks":{"SessionStart":[{"hooks":[
			  {"type":"command","command":`+jsonQuote(tc.first)+`},
			  {"type":"command","command":`+jsonQuote(second)+`}]}]}}`)
			regs, err := registeredHookEvents(p)
			if err != nil {
				t.Fatal(err)
			}
			v := judgeHook(t.Context(), nil, t.TempDir(), "agentsmemory-recall-hook.sh",
				regs["agentsmemory-recall-hook.sh"], t.TempDir())
			if v.label != "DUPLICATED" {
				t.Fatalf("verdict %q, want DUPLICATED", v.label)
			}
			if got := strings.Contains(v.detail, remedy); got != tc.want {
				t.Errorf("remedy warning present = %v, want %v — whichever entry is read first, "+
					"one of these is invisible to the installer, so re-running it cannot collapse "+
					"them.\ndetail: %s", got, tc.want, v.detail)
			}
		})
	}

	// The other direction: an ordinary duplicate, both entries readable, is one
	// install CAN collapse — so the warning must not appear and send an operator
	// away from the remedy that works.
	t.Run("both entries readable: no warning", func(t *testing.T) {
		p := writeRawSettings(t, `{"hooks":{"SessionStart":[{"hooks":[
		  {"type":"command","command":`+jsonQuote(dupePinned)+`},
		  {"type":"command","command":`+jsonQuote(dupePinned)+`}]}]}}`)
		regs, err := registeredHookEvents(p)
		if err != nil {
			t.Fatal(err)
		}
		reg := regs["agentsmemory-recall-hook.sh"]
		if len(reg.duplicated) == 0 {
			t.Skip("identical entries are collapsed before this point; nothing to judge")
		}
		v := judgeHook(t.Context(), nil, t.TempDir(), "agentsmemory-recall-hook.sh", reg, t.TempDir())
		if strings.Contains(v.detail, remedy) {
			t.Errorf("the warning fired on a duplicate install CAN collapse, sending an operator "+
				"away from the remedy that works:\n%s", v.detail)
		}
	})
}
