package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// pluginHookEvents reads the events the shipped manifest declares.
func pluginHookEvents(t *testing.T) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".claude-plugin", "hooks.json"))
	if err != nil {
		t.Fatalf("no plugin hooks manifest: %v", err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("hooks.json does not parse: %v", err)
	}
	out := map[string][]string{}
	for ev, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out[ev] = append(out[ev], filepath.Base(h.Command))
			}
		}
		sort.Strings(out[ev])
	}
	return out
}

// TestThePluginDeclaresEveryHookTheInstallerRegisters is the gate that stops the
// two install paths diverging.
//
// ⚠ BOTH SIDES ARE DERIVED FROM SOURCE, WHICH IS THE ONLY SHAPE THAT SURVIVES.
// The installer's plan is read from hookPlans(); the manifest is read from the
// shipped JSON. Neither is a hand-kept list beside a truth, so a hook added
// tomorrow joins this check on the same commit rather than when somebody
// remembers. This repository has recorded repeatedly that a list kept beside the
// thing it describes goes stale — and a plugin migration that silently drops a
// registration is exactly that failure, in the one place nobody looks until a
// hook has been dead for a week.
func TestThePluginDeclaresEveryHookTheInstallerRegisters(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	plans := inst.hookPlans()
	if len(plans) == 0 {
		t.Fatal("no hook plans — this check would pass vacuously")
	}
	manifest := pluginHookEvents(t)
	if len(manifest) == 0 {
		t.Fatal("the manifest declares no hooks — this check would pass vacuously")
	}

	planned := map[string][]string{}
	for _, p := range plans {
		if p.retire {
			continue
		}
		// The plan's command is a shell line; the script is its last path element.
		script := p.cmd
		if i := strings.LastIndex(script, "/"); i >= 0 {
			script = script[i+1:]
		}
		script = strings.Trim(script, "'\"")
		planned[p.event] = append(planned[p.event], script)
	}
	for ev := range planned {
		sort.Strings(planned[ev])
	}

	for ev, want := range planned {
		got, ok := manifest[ev]
		if !ok {
			t.Errorf("the installer registers %s and the plugin manifest does not — a user installing the plugin loses that hook", ev)
			continue
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: manifest runs %v, installer runs %v", ev, got, want)
		}
	}
	for ev := range manifest {
		if _, ok := planned[ev]; !ok {
			t.Errorf("the plugin manifest declares %s and the installer does not — the two paths install different things", ev)
		}
	}
}

// TestThePluginManifestIsValid: the fields a marketplace needs to list it.
func TestThePluginManifestIsValid(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("no plugin manifest: %v", err)
	}
	var m struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("plugin.json does not parse: %v", err)
	}
	if m.Name == "" || m.Description == "" || m.Version == "" {
		t.Errorf("plugin.json is missing name, description or version: %+v", m)
	}
}

// TestThePluginManifestHardcodesNoHomePath.
//
// ⚠ A PATH A PLUGIN HARDCODES IS A PATH IT DEPENDS ON. A plugin is unpacked into a
// cache directory whose location the plugin does not choose, so every command must
// resolve through ${CLAUDE_PLUGIN_ROOT}. An absolute home path in a shipped file is
// also a personal path published to whoever installs it — reported by an adopter
// of the same tooling, who shipped one for two days.
func TestThePluginManifestHardcodesNoHomePath(t *testing.T) {
	for _, f := range []string{"plugin.json", "hooks.json"} {
		b, err := os.ReadFile(filepath.Join(".claude-plugin", f))
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		for _, bad := range []string{"/Users/", "/home/", "$HOME", "~/.claude"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s hardcodes %q; commands must resolve through ${CLAUDE_PLUGIN_ROOT}", f, bad)
			}
		}
		if f == "hooks.json" && !strings.Contains(body, "${CLAUDE_PLUGIN_ROOT}") {
			t.Errorf("%s never uses ${CLAUDE_PLUGIN_ROOT}; its commands cannot resolve where the plugin is unpacked", f)
		}
	}
}

// TestDoctorFailsOnADuplicateRegistration is the check no test of the install PLAN
// can make.
//
// The plan has ONE writer. A duplicate arises when there are two — the installer
// wrote a registration and a plugin declared another, or a settings.json was
// hand-edited — and the result is a hook that runs once per registration and
// therefore injects twice. It is silent: no error, and nothing a reader spots in a
// transcript that already contained the text once. Only a command that reads the
// file in front of the operator can see it.
func TestDoctorFailsOnADuplicateRegistration(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	cmd := "bash -- '" + filepath.Join(dir, "agentsmemory-recall-hook.sh") + "'"
	doc := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"` + cmd + `"}]},{"hooks":[{"type":"command","command":"` + cmd + `"}]}]}}`
	if err := os.WriteFile(settings, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	regs, err := registeredHookEvents(settings)
	if err != nil {
		t.Fatalf("read registrations: %v", err)
	}
	reg, ok := regs["agentsmemory-recall-hook.sh"]
	if !ok {
		t.Fatal("the registration was not read back at all")
	}
	if len(reg.duplicated) == 0 {
		t.Fatal("a script registered twice on SessionStart is not reported as duplicated; the hook injects twice and nothing says so")
	}
	if !containsString(reg.duplicated, "SessionStart") {
		t.Errorf("duplicated = %v, want it to name SessionStart", reg.duplicated)
	}
}

// unattendedRules reads the permission rules the plugin ships.
func unattendedRules(t *testing.T) (allow, deny []string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".claude-plugin", "settings.json"))
	if err != nil {
		t.Fatalf("no plugin settings: %v", err)
	}
	var doc struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("plugin settings do not parse: %v", err)
	}
	return doc.Permissions.Allow, doc.Permissions.Deny
}

// TestTheDenyListNamesTheIrreversibleActions is where the one line this record
// draws is written down as a rule rather than left to judgement.
//
// Where a session would stop to ask a human about RECALL, it consults the palace
// instead — that is the whole thesis, and the allow list is it. But the palace
// records what was DECIDED; it cannot consent on a human's behalf to something
// nobody has decided yet. So the irreversible and outward-facing set gates, and it
// gates HERE, in a file one review can read, because a rule can be reviewed and a
// mid-run judgement cannot.
func TestTheDenyListNamesTheIrreversibleActions(t *testing.T) {
	_, deny := unattendedRules(t)
	if len(deny) == 0 {
		t.Fatal("the deny list is empty; an allow-everything rule with a comment is not a gate")
	}
	for _, must := range []string{"push --force", "release create", "pr merge", "am_merge_wing"} {
		var found bool
		for _, d := range deny {
			if strings.Contains(d, must) {
				found = true
			}
		}
		if !found {
			t.Errorf("nothing in the deny list covers %q — an irreversible action the palace cannot consent to", must)
		}
	}
}

// TestTheAllowListDoesNotAllowEverything is the half that matters, and the shape
// this repository has learned to insist on.
//
// ⚠ A PERMISSION RULE IS THE EASIEST THING HERE TO SHIP INERT. It parses, it
// installs, and nothing proves it ever refused anything — the green-suite-over-a-
// dead-mechanism failure §Reachability records repeatedly. A test that only
// asserts the deny list CONTAINS some strings passes happily beside a wildcard
// that readmits every one of them, so this asserts the allow list cannot.
func TestTheAllowListDoesNotAllowEverything(t *testing.T) {
	allow, deny := unattendedRules(t)
	if len(allow) == 0 {
		t.Fatal("the allow list is empty; the unattended path grants nothing and every recall prompts")
	}
	for _, a := range allow {
		if a == "*" || a == "Bash" || strings.HasPrefix(a, "Bash(*") {
			t.Errorf("allow contains %q, which readmits everything the deny list names", a)
		}
		// A blanket Bash grant is the specific wildcard that would swallow the
		// force-push and release rules while every assertion above still passed.
		if strings.HasPrefix(a, "Bash(") && strings.Contains(a, ":*)") && !strings.Contains(a, " ") {
			t.Errorf("allow contains the broad grant %q", a)
		}
	}
	// Recall is granted; writing memory is granted; DESTRUCTIVE memory operations
	// are not. The palace may record, and may not un-record, without a human.
	for _, a := range allow {
		for _, d := range deny {
			if a == d {
				t.Errorf("%q is both allowed and denied; the resolution is then a coin flip wearing a rule", a)
			}
		}
	}
}

// TestRecallIsGrantedSoNoTurnStopsToAskForIt.
//
// The goal is a session that grounds itself. If a recall prompts, an unattended
// run stalls on the one call the whole protocol is built around, and a watched run
// trains its human to click through prompts — which is worse than not prompting.
func TestRecallIsGrantedSoNoTurnStopsToAskForIt(t *testing.T) {
	allow, _ := unattendedRules(t)
	for _, must := range []string{"am_search", "am_status", "am_get_drawer", "am_add_drawer", "am_kg_add", "am_diary_write"} {
		var found bool
		for _, a := range allow {
			if strings.Contains(a, must) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not granted; a session cannot ground or persist itself without stopping to ask", must)
		}
	}
}
