package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pluginHookEvents reads the events the shipped manifest declares.
func pluginHookEvents(t *testing.T) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("hooks", "hooks.json"))
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
	for _, f := range []string{filepath.Join(".claude-plugin", "plugin.json"), filepath.Join("hooks", "hooks.json"), ".mcp.json"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		for _, bad := range []string{"/Users/", "/home/", "$HOME", "~/.claude"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s hardcodes %q; commands must resolve through ${CLAUDE_PLUGIN_ROOT}", f, bad)
			}
		}
		if strings.HasSuffix(f, "hooks.json") && !strings.Contains(body, "${CLAUDE_PLUGIN_ROOT}") {
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
	b, err := os.ReadFile("unattended-settings.json")
	if err != nil {
		t.Fatalf("no unattended settings: %v", err)
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

// TestEveryRegisteredHookIsAlsoWritten is the rung that was missing, and the one
// `doctor` found in production.
//
// ⚠ MEASURED 2026-09-04: three scripts were added to the embed and to hookPlans()
// and the installer never wrote them. settings.json then named files that did not
// exist — the event fired, the agent ran nothing, and EVERY test passed, because
// each checks one rung: the script parses, the plan registers it, the manifest
// declares it. Nothing asked whether the install produces the file the
// registration points at.
//
// It derives both sides from source: the plan's script names, and the paths the
// installer writes. A hook added tomorrow joins this check on the same commit.
func TestEveryRegisteredHookIsAlsoWritten(t *testing.T) {
	dir := t.TempDir()
	inst, _, _ := newTestInstaller(t, false)
	inst.targetDir = dir
	if err := inst.writeAssets(); err != nil {
		t.Fatalf("writeAssets: %v", err)
	}
	plans := inst.hookPlans()
	if len(plans) == 0 {
		t.Fatal("no plans — this check would pass vacuously")
	}
	for _, p := range plans {
		if p.retire {
			continue
		}
		script := p.cmd
		if i := strings.LastIndex(script, "/"); i >= 0 {
			script = script[i+1:]
		}
		script = strings.Trim(script, "'\"")
		if _, err := os.Stat(filepath.Join(dir, script)); err != nil {
			t.Errorf("%s is registered on %s but the install writes no such file: the agent runs nothing for that event", script, p.event)
		}
	}
}

// TestNoShippedHookTriesBSDStatFirst is a portability gate earned by a bug that
// macOS testing structurally cannot find.
//
// ⚠ MEASURED 2026-09-04 in the Linux suite. The status line ran `stat -f %m`
// first. On BSD/macOS that is the mtime; on GNU/busybox `-f` means FILESYSTEM and
// SUCCEEDS, printing a block containing "File:". That string then reached
// arithmetic expansion, which resolves a bare word as a variable name, so under
// `set -u` the hook died with "File: unbound variable" and exited 1 on EVERY
// render — on the platform most installs run.
//
// The lesson generalises past this one call: a portability bug whose wrong branch
// SUCCEEDS is invisible to the developer's machine, and only the container suite
// sees it. So the order is pinned rather than left to whoever edits next.
func TestNoShippedHookTriesBSDStatFirst(t *testing.T) {
	entries, err := os.ReadDir("hooks")
	if err != nil {
		t.Fatal(err)
	}
	// ⚠ PER LINE, AND COMMENTS STRIPPED. The first version compared the file-wide
	// index of "stat -c" against "stat -f" and immediately reported a false
	// positive on agentsmemory-stats.sh — whose code is correct, and which carries
	// a COMMENT explaining this exact hazard above it. A gate that matches prose is
	// not checking code, and a gate whose first run is a false alarm is one nobody
	// trusts afterwards.
	var checked int
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("hooks", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(b), "\n") {
			code := line
			if i := strings.Index(code, "#"); i >= 0 {
				code = code[:i]
			}
			if !strings.Contains(code, "stat -") {
				continue
			}
			checked++
			gnu := strings.Index(code, "stat -c")
			bsd := strings.Index(code, "stat -f")
			if bsd >= 0 && (gnu < 0 || bsd < gnu) {
				t.Errorf("%s:%d tries `stat -f` before `stat -c`: on GNU/busybox `-f` means filesystem, SUCCEEDS, and prints prose where a number is expected\n    %s",
					e.Name(), n+1, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Skip("no shipped hook calls stat; nothing to order")
	}
}

// TestClaudeCodeActuallyLoadsThePlugin is the only test here that asks the HARNESS
// rather than the filesystem, and it exists because every other one passed over a
// plugin Claude Code could not load.
//
// ⚠ THE MANIFEST WAS AT .claude-plugin/hooks.json AND LOADED NOTHING. Claude Code
// reads plugin hooks from hooks/hooks.json; `.claude-plugin/` holds plugin.json
// alone. Every JSON-reading test was green — they read the same file the code
// wrote — and `claude plugin validate` passed without ever mentioning hooks. Only
// `claude plugin details` reports the component inventory the harness actually
// built. Reported by review 2026-09-04.
//
// Measured both ways before this was written: with the manifest misplaced the
// inventory says `Hooks (0)`, and with it at hooks/hooks.json it says `Hooks (9)`.
// A check that cannot fail is not a check, and this one demonstrably can.
//
// It SKIPS when the CLI is absent rather than failing: a developer without
// `claude` on PATH is not a broken plugin, and a gate that red-lights on a missing
// tool is one people learn to ignore.
func TestClaudeCodeActuallyLoadsThePlugin(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH; the harness cannot be asked what it loaded")
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("claude", "--plugin-dir", root, "plugin", "details", "agentsmemory")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude plugin details failed: %v\n%s", err, out)
	}
	got := string(out)

	// Every event the installer registers must appear in the harness's inventory.
	inst, _, _ := newTestInstaller(t, false)
	for _, p := range inst.hookPlans() {
		if p.retire {
			continue
		}
		if !strings.Contains(got, p.event) {
			t.Errorf("Claude Code's plugin inventory does not list %s; a plugin install would not register it\n%s", p.event, got)
		}
	}
	if strings.Contains(got, "Hooks (0)") {
		t.Errorf("the harness loaded ZERO hooks from this plugin — the manifest is not where Claude Code reads it\n%s", got)
	}
	if !strings.Contains(got, "MCP servers (1)") {
		t.Errorf("the harness loaded no MCP server; .mcp.json is missing or malformed\n%s", got)
	}
	if !strings.Contains(got, "recall") {
		t.Errorf("the recall skill is not in the inventory\n%s", got)
	}
}

// TestADeniedActionIsActuallyRefused is the only test here that proves a rule
// FIRES, and it was written because every other permission test passed over a
// file nothing loaded.
//
// ⚠ THE FIRST VERSION OF T9 PUT THESE RULES IN A PLUGIN settings.json, WHICH HAS
// NO `permissions` KEY. Nothing read it. The tests parsed it, asserted its
// contents — including a wildcard check written specifically to stop a permissive
// rule slipping through — and every one passed against an inert document.
//
// ⚠ AND "THE HARNESS ACCEPTED THE FILE" PROVES NOTHING EITHER: measured, `claude
// --settings` also accepts `{"permissions":{"deny":"not-an-array"}}` without
// complaint. So acceptance was rejected as evidence and this runs the real thing.
//
// Two arms, one harmless command. Deny echo and the model answers BLOCKED; allow
// it and the model answers with the echoed token. The allow arm is what makes the
// deny arm mean anything — without it a model that simply refused everything, or a
// probe that never ran, would look identical to a working rule.
func TestADeniedActionIsActuallyRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("spends a model call")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}
	if os.Getenv("AGENTSMEMORY_PERMISSION_PROBE") == "" {
		t.Skip("set AGENTSMEMORY_PERMISSION_PROBE=1 to run the live permission probe (it spends a model call)")
	}
	dir := t.TempDir()
	const prompt = "Run exactly this shell command: echo AGENTSMEMORY_PROBE_OK — then reply with only the command output, or the single word BLOCKED if you could not run it."

	run := func(rules string) string {
		f := filepath.Join(dir, "s.json")
		if err := os.WriteFile(f, []byte(rules), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("claude", "-p", "--output-format", "json", "--settings", f,
			"--permission-mode", "dontAsk", "--allowedTools", "Bash")
		cmd.Stdin = strings.NewReader(prompt)
		// Our own Stop hook speaks on exit 2, and in headless mode that text
		// REPLACES the result — measured while writing this. The probe reads the
		// model's answer, so the hook is off for it.
		cmd.Env = append(os.Environ(), "AGENTSMEMORY_STOP_HOOK=off", "AGENTSMEMORY_SUBAGENT_HOOK=off")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("claude -p failed: %v", err)
		}
		var d struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(out, &d); err != nil {
			t.Fatalf("result is not json: %v", err)
		}
		return d.Result
	}

	if got := run(`{"permissions":{"deny":["Bash(echo:*)"],"allow":[]}}`); !strings.Contains(got, "BLOCKED") {
		t.Errorf("a DENIED command was not refused; the rules do not fire. result=%q", got)
	}
	if got := run(`{"permissions":{"allow":["Bash(echo:*)"],"deny":[]}}`); !strings.Contains(got, "AGENTSMEMORY_PROBE_OK") {
		t.Errorf("an ALLOWED command did not run, so the deny arm above proves nothing. result=%q", got)
	}
}

// TestEveryRegisteredPluginHookIsExecutable is a blocker found in review, and it
// is invisible to every other check here.
//
// ⚠ MEASURED: agentsmemory-verify-hook.sh and agentsmemory-session-end-hook.sh
// were committed 100644. The INSTALLER path never noticed, because writeFile
// chmods to 0755 on the way out — but the PLUGIN path executes them in place from
// the plugin directory, where what ships is the mode in git's index. Both returned
// exit 126, "Permission denied". The manifest registered them, `claude plugin
// details` counted them, and every JSON-reading test passed.
//
// ⚠ AND `statSync` ON THE WORKING TREE IS NOT THE ANSWER. A Git-for-Windows
// checkout has no POSIX permission bits and reports 0644 for everything, so a test
// that reads the filesystem is a false alarm on Windows and a false pass wherever
// someone has chmod'ed locally without staging it. What SHIPS is the index mode,
// so that is what this asks git for.
func TestEveryRegisteredPluginHookIsExecutable(t *testing.T) {
	manifest := pluginHookEvents(t)
	if len(manifest) == 0 {
		t.Fatal("the manifest declares no hooks — this check would pass vacuously")
	}
	// ⚠ FATAL, NOT SKIP. A skip reads as a pass in a CI summary, so in a container
	// without git this gate would go silently inert — which is the shape of failure
	// it exists to catch, applied to itself. Every other check here already assumes
	// a git checkout. Reported by review.
	out, err := exec.Command("git", "ls-files", "-s", "hooks/").Output()
	if err != nil {
		t.Fatalf("git ls-files failed (%v); this gate reads the INDEX mode and cannot fall back to the working tree, which reports 0644 for everything on a Git-for-Windows checkout", err)
	}
	mode := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		mode[filepath.Base(f[3])] = f[0]
	}
	var checked int
	for ev, scripts := range manifest {
		for _, s := range scripts {
			m, ok := mode[s]
			if !ok {
				t.Errorf("%s registers %s and git does not track it", ev, s)
				continue
			}
			checked++
			if m != "100755" {
				t.Errorf("%s registers %s, which ships as %s — a plugin runs it in place and gets exit 126, Permission denied", ev, s, m)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no registered script was checked")
	}
}

// TestDoctorPrintsTheDuplicatedVerdict drives the branch, which the sibling test
// does not.
//
// ⚠ `TestDoctorFailsOnADuplicateRegistration` asserts `reg.duplicated` out of
// `registeredHookEvents` — so deleting the verdict block in `judgeHook` leaves the
// suite green and the verdict simply never printed. Reported by review. This
// drives `judgeHook` itself and asserts both the label and that it is a finding.
func TestDoctorPrintsTheDuplicatedVerdict(t *testing.T) {
	reg := hookRegistration{
		path:       "/cfg/agentsmemory-recall-hook.sh",
		events:     []string{"SessionStart"},
		duplicated: []string{"SessionStart"},
	}
	v := judgeHook(t.Context(), nil, "/cfg", "agentsmemory-recall-hook.sh", reg, "/repo")
	if v.label != "DUPLICATED" {
		t.Errorf("judgeHook returned %q for a doubled registration, want DUPLICATED", v.label)
	}
	if !v.bad {
		t.Error("DUPLICATED is not marked as a finding, so doctor would exit 0 over a hook that injects twice")
	}
	// The message must not claim a cause this command cannot observe.
	if strings.Contains(v.detail, "a plugin declared another") {
		t.Errorf("the verdict explains itself with a case doctor cannot see:\n%s", v.detail)
	}
}

// TestThePluginShipsNoStrayCommand is the second instance in two PRs of one class:
// a second distribution path that ENUMERATES DIFFERENTLY.
//
// ⚠ The installer ships an allowlist — `commandAssets` names am.md and
// load-skill.md. The plugin loader reads the DIRECTORY, so every .md in commands/
// becomes a slash command. claude-mem writes an empty <claude-mem-context>
// CLAUDE.md into directories it touches, one landed in commands/, and an installed
// user got a slash command named /CLAUDE whose body was generated activity notes —
// counted against the always-on token budget of every session.
//
// The executable-bit finding on the previous PR was the same shape: the installer
// chmods on the way out, the plugin path uses the file as committed. Whenever two
// paths ship the same directory, the one that enumerates rather than allowlists is
// the one that ships the surprise.
func TestThePluginShipsNoStrayCommand(t *testing.T) {
	entries, err := os.ReadDir("commands")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, a := range commandAssets {
		allowed[a] = true
	}
	if len(allowed) == 0 {
		t.Fatal("commandAssets is empty — this check would pass vacuously")
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if !allowed[e.Name()] {
			t.Errorf("commands/%s is not in commandAssets: the installer will not ship it and the plugin WILL, as a slash command", e.Name())
		}
	}
}

// TestThePluginVersionMatchesTheNewestChangelogHeading stops a tag containing a
// manifest that names the release before it.
//
// The release procedure puts the tag on the changelog PR's merge commit, so the
// manifest and the newest heading have to move together or the shipped plugin
// advertises the previous version. Nothing read that version against anything
// before this; `claude plugin details` prints it, so a user sees it.
func TestThePluginVersionMatchesTheNewestChangelogHeading(t *testing.T) {
	ch, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	headings := regexp.MustCompile(`(?m)^## v([0-9]+\.[0-9]+\.[0-9]+) —`).FindAllStringSubmatch(string(ch), -1)
	if len(headings) == 0 {
		t.Fatal("no version heading found in CHANGELOG.md — this check would pass vacuously")
	}
	newest := headings[len(headings)-1][1]

	b, err := os.ReadFile(filepath.Join(".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != newest {
		t.Errorf("plugin.json says %q and the newest CHANGELOG heading is v%s — the tag would ship a manifest naming the release before it",
			m.Version, newest)
	}
}
