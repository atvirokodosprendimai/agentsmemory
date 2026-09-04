package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runAnchorCue drives the shipped script with a fake `aiagentmemory` on PATH, so
// the hook's own behaviour is measured without a server.
//
// The stub is what makes the silence assertions meaningful: a hook that stayed
// quiet because the binary was missing would pass a naive "no output" test while
// being broken, so the stub always succeeds and the script's own branches decide.
func runAnchorCue(t *testing.T, input, stubStdout string) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "aiagentmemory")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncat <<'JSON'\n"+stubStdout+"\nJSON\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("hooks", "agentsmemory-anchor-cue-hook.sh"))
	cmd.Stdin = strings.NewReader(input)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	// CLAUDE_PROJECT_DIR is what makes an absolute tool path repo-relative. Anchors
	// are stored repo-relative, so without it every lookup asks about a path no
	// anchor can match and the cue is silent for the wrong reason.
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CLAUDE_PROJECT_DIR=/repo")
	if err := cmd.Run(); err != nil {
		t.Fatalf("the cue exited non-zero (%v); a PreToolUse hook that fails BLOCKS the tool call", err)
	}
	return out.String(), errb.String()
}

const anchorHit = `{"anchors":[{"path":"internal/palace/chunk.go","repo":"agentsmemory","snippet":"ChunkOverlap = 320","status":"verified","drawer_id":"abc"}],"count":1}`

// TestTheAnchorCueIsSilentWithoutAnAnchor is the assertion the whole design rests
// on, and the one a careless implementation fails.
//
// Nothing pins most files. If the cue spoke on those it would fire on nearly every
// tool call, and a channel that talks when it has nothing to say is one a reader
// learns to skip — which is worse than never shipping it. ADR-041's T5 measured
// its own cue at 3.4% of turns and still recorded frequency as the thing to check
// BEFORE relevance.
func TestTheAnchorCueIsSilentWithoutAnAnchor(t *testing.T) {
	out, _ := runAnchorCue(t, `{"tool_name":"Read","tool_input":{"file_path":"/repo/internal/nothing.go"}}`,
		`{"anchors":[],"count":0}`)
	if out != "" {
		t.Errorf("the cue emitted %d bytes for a path nothing pins:\n%s", len(out), out)
	}
}

// TestTheAnchorCueIsSilentWithoutAFilePath covers every tool that names no file.
//
// PreToolUse fires for tools this kit has never heard of, which is why the script
// filters rather than the registration: a matcher would be a second copy of a
// guard that has to exist anyway.
func TestTheAnchorCueIsSilentWithoutAFilePath(t *testing.T) {
	out, errs := runAnchorCue(t, `{"tool_name":"Bash","tool_input":{"command":"ls"}}`, anchorHit)
	if out != "" {
		t.Errorf("the cue spoke for a tool call carrying no file_path:\n%s", out)
	}
	if !strings.Contains(errs, "no file_path") {
		t.Errorf("it should say on stderr why it stayed quiet; got %q", errs)
	}
}

// TestTheAnchorCueEmitsAParseableEnvelope is the delivery half.
//
// PreToolUse does not inject plain stdout, so the payload has to be a JSON
// envelope carrying additionalContext. Hand-assembled JSON is how an envelope
// becomes unparseable and is then dropped in SILENCE by the harness — the failure
// mode that looks identical to a hook that chose not to speak.
func TestTheAnchorCueEmitsAParseableEnvelope(t *testing.T) {
	out, _ := runAnchorCue(t, `{"tool_name":"Read","tool_input":{"file_path":"/repo/internal/palace/chunk.go"}}`, anchorHit)
	if out == "" {
		t.Fatal("the cue said nothing for a path an anchor pins")
	}
	var env struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("the envelope does not parse (%v); the harness would drop it silently:\n%s", err, out)
	}
	if env.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want PreToolUse", env.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(env.HookSpecificOutput.AdditionalContext, "chunk.go") {
		t.Errorf("the context does not name the file it is about:\n%s", env.HookSpecificOutput.AdditionalContext)
	}
}

// TestTheAnchorCueRefusesAnUnfilteredAnswer is the hardening a live run demanded.
//
// ⚠ MEASURED 2026-09-04: an MCP server that does not recognise an argument IGNORES
// it. Against a container one commit behind this hook, `path=` was dropped and the
// call returned five anchors from THREE DIFFERENT REPOSITORIES for a file nothing
// pinned. A cue that fires with another project's memories attached is worse than
// one that never fires, and during any rollout the server is briefly older than
// the kit — so the hook confirms the path it asked about is in the answer instead
// of trusting that filtering happened.
func TestTheAnchorCueRefusesAnUnfilteredAnswer(t *testing.T) {
	unfiltered := `{"anchors":[{"path":"docs/other/thing.md","repo":"some_other_project","snippet":"x","status":"unchecked","drawer_id":"z"}],"count":1}`
	out, errs := runAnchorCue(t, `{"tool_name":"Read","tool_input":{"file_path":"/repo/internal/palace/chunk.go"}}`, unfiltered)
	if out != "" {
		t.Errorf("the cue passed on anchors that do not match the path it asked about:\n%s", out)
	}
	if !strings.Contains(errs, "ignored the path filter") {
		t.Errorf("it should name why it declined; got %q", errs)
	}
}

// TestThePreToolUseHookIsRegistered covers the rung the script's own tests cannot
// see.
//
// Every test above drives the FILE. A hook script can be perfect, embedded and
// installed while nothing registers it — the defect AGENTS.md §Reachability
// records repeatedly, and the one that made ADR-041's PreCompact recall run and be
// thrown away. This reads the installer's own plan.
func TestThePreToolUseHookIsRegistered(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	var found *hookPlan
	for _, p := range inst.hookPlans() {
		if p.event == "PreToolUse" && !p.retire {
			pp := p
			found = &pp
		}
	}
	if found == nil {
		t.Fatal("the installer registers no PreToolUse hook; the anchor cue ships and is never selected")
	}
	if !strings.Contains(found.cmd, anchorCueHookFile) {
		t.Errorf("the PreToolUse registration runs %q, not the anchor cue", found.cmd)
	}
}

// TestNoHookPlanIsRegisteredTwice was earned by nearly shipping one.
//
// ⚠ While landing the PreToolUse cue the plan was inserted twice: an earlier grep
// checking whether it existed was truncated by `head -3`, so the first insert was
// read as a no-op and repeated. The build stayed green and every other test
// passed, because a duplicate registration is not a compile error and not a
// behaviour change any single test observes — it just runs the hook twice, which
// doubles the injected context and is invisible in a transcript that already
// contains the text once.
//
// The pair is (event, command). The same SCRIPT on two different events is
// deliberate here — Stop and SubagentStop share one, branching on the event name —
// so keying on the command alone would fail the design rather than the defect.
func TestNoHookPlanIsRegisteredTwice(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	plans := inst.hookPlans()
	if len(plans) == 0 {
		t.Fatal("no hook plans — this check would pass vacuously")
	}
	seen := map[string]bool{}
	for _, p := range plans {
		if p.retire {
			continue
		}
		key := p.event + "\x00" + p.cmd
		if seen[key] {
			t.Errorf("%s is registered twice with the same command; the hook runs twice and injects twice", p.event)
		}
		seen[key] = true
	}
}

// runTaskRecall drives the shipped task-recall script with a stubbed binary.
func runTaskRecall(t *testing.T, input string) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "aiagentmemory")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho '{\"hits\":[{\"wing\":\"wing_acme\",\"room\":\"decisions\",\"content\":\"a recalled memory\"}],\"count\":1}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("hooks", "agentsmemory-task-recall-hook.sh"))
	cmd.Stdin = strings.NewReader(input)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_ = cmd.Run()
	return out.String(), errb.String()
}

// TestTheExpansionBranchRecallsWhereTheSubmitBranchRefuses is the gap T4 fills,
// stated as the pair that proves it is a gap rather than a duplicate.
//
// The UserPromptSubmit hook refuses a slash command deliberately: "/am" is a
// command NAME, and recalling against it retrieves whatever is nearest to one. So
// until this branch existed, every slash-command turn got no task recall at all —
// the turns most likely to be substantive work.
func TestTheExpansionBranchRecallsWhereTheSubmitBranchRefuses(t *testing.T) {
	task := "how does the rebind guard decide this machine is the boundary"

	out, errs := runTaskRecall(t, `{"hook_event_name":"UserPromptSubmit","prompt":"/am `+task+`"}`)
	if out != "" {
		t.Errorf("the submit branch recalled against a slash command; it must refuse:\n%s", out)
	}
	if !strings.Contains(errs, "slash command") {
		t.Errorf("the submit branch should say why it refused; got %q", errs)
	}

	out, _ = runTaskRecall(t, `{"hook_event_name":"UserPromptExpansion","prompt":"/am","expanded_prompt":"`+task+`"}`)
	if out == "" {
		t.Fatal("the expansion branch said nothing; the slash-command turn still gets no recall, which is the whole gap T4 closes")
	}
	if !strings.Contains(out, "a recalled memory") {
		t.Errorf("the expansion branch did not inject what it recalled:\n%s", out)
	}
}

// TestTheUserPromptExpansionHookIsRegistered covers the wiring, and it is the
// half T1 had to land first.
//
// Before T1 this registration could not pass the install gate at all:
// hookchannel.go filed UserPromptExpansion under the debug log, so
// TestEveryInjectingHookIsOnAnInjectingEvent rejected a stdout-injecting hook
// registered there. The dependency is real rather than bookkeeping.
func TestTheUserPromptExpansionHookIsRegistered(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	var found bool
	for _, p := range inst.hookPlans() {
		if p.event == "UserPromptExpansion" && !p.retire && strings.Contains(p.cmd, taskRecallHookFile) {
			found = true
		}
	}
	if !found {
		t.Fatal("no UserPromptExpansion registration for the task-recall hook; a slash command's real task still gets no recall")
	}
	if hookEventChannel("UserPromptExpansion") != channelInjected {
		t.Error("UserPromptExpansion is not classified as injecting, so the install gate would refuse this registration")
	}
}

// TestTheExpansionBranchStillRefusesAnUnexpandedCommand is the case a surviving
// mutant exposed.
//
// The expansion field name is NOT documented — the hooks reference truncates
// before its payload table — so the script tries several spellings. If none
// matches, PROMPT is still the literal "/am", and recalling against a command name
// retrieves whatever is nearest to one. An earlier version exempted this branch
// from the slash-command refusal; the exemption was dead code on the happy path
// and removed a safety check on the unhappy one.
func TestTheExpansionBranchStillRefusesAnUnexpandedCommand(t *testing.T) {
	out, errs := runTaskRecall(t, `{"hook_event_name":"UserPromptExpansion","prompt":"/am","unknown_field_name":"the real task text"}`)
	if out != "" {
		t.Errorf("with no recognised expansion field the hook recalled against the command name:\n%s", out)
	}
	if !strings.Contains(errs, "slash command") {
		t.Errorf("it should refuse and say why; got %q", errs)
	}
}

// touchedDir runs the recorder and returns the session's list.
func touchedDir(t *testing.T, stateDir string, events ...string) string {
	t.Helper()
	for _, ev := range events {
		cmd := exec.Command("bash", filepath.Join("hooks", "agentsmemory-touched-hook.sh"))
		cmd.Stdin = strings.NewReader(ev)
		cmd.Env = append(os.Environ(), "AGENTSMEMORY_STATE_DIR="+stateDir, "CLAUDE_PROJECT_DIR=/repo")
		if err := cmd.Run(); err != nil {
			t.Fatalf("recorder exited non-zero: %v", err)
		}
	}
	return filepath.Join(stateDir, "agentsmemory-touched")
}

// TestTouchedPathsAreRecordedOncePerPath: a file edited fifteen times is one file.
//
// A list that grows with every keystroke is a list nobody reads, and the Stop
// nudge that quotes it becomes a wall of repeats — which is how a nudge gets
// skimmed instead of answered.
func TestTouchedPathsAreRecordedOncePerPath(t *testing.T) {
	dir := t.TempDir()
	edit := `{"session_id":"s1","tool_name":"Edit","tool_input":{"file_path":"/repo/a.go"}}`
	out := touchedDir(t, dir, edit, edit, edit,
		`{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"/repo/b.go"}}`,
		`{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"/repo/c.go"}}`)
	b, err := os.ReadFile(filepath.Join(out, "s1"))
	if err != nil {
		t.Fatalf("no record written: %v", err)
	}
	got := strings.Fields(string(b))
	if len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Errorf("recorded %v, want [a.go b.go] — deduplicated, and READS excluded", got)
	}
}

// TestTouchedRecordIsScopedToTheSession: one session must never report another's
// work at its own end of turn.
func TestTouchedRecordIsScopedToTheSession(t *testing.T) {
	dir := t.TempDir()
	out := touchedDir(t, dir,
		`{"session_id":"one","tool_name":"Edit","tool_input":{"file_path":"/repo/one.go"}}`,
		`{"session_id":"two","tool_name":"Edit","tool_input":{"file_path":"/repo/two.go"}}`)
	for name, want := range map[string]string{"one": "one.go", "two": "two.go"} {
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatalf("session %s has no record: %v", name, err)
		}
		if strings.TrimSpace(string(b)) != want {
			t.Errorf("session %s recorded %q, want %q", name, strings.TrimSpace(string(b)), want)
		}
	}
}

// TestTheStopHookNamesTouchedPaths is what makes the WRITE reachable.
//
// A recorder nothing consumes is a file that grows — the reachability defect this
// repository keeps recording, in a shell script. This is the only test that fails
// if the Stop hook stops reading the list, and it is the reason T3 is a pair
// rather than a single hook.
func TestTheStopHookNamesTouchedPaths(t *testing.T) {
	dir := t.TempDir()
	touchedDir(t, dir,
		`{"session_id":"s9","tool_name":"Edit","tool_input":{"file_path":"/repo/alpha.go"}}`,
		`{"session_id":"s9","tool_name":"Edit","tool_input":{"file_path":"/repo/beta.go"}}`)

	cmd := exec.Command("bash", filepath.Join("hooks", "agentsmemory-stop-hook.sh"))
	cmd.Stdin = strings.NewReader(`{"session_id":"s9","hook_event_name":"Stop"}`)
	var errb strings.Builder
	cmd.Stderr = &errb
	cmd.Env = append(os.Environ(), "AGENTSMEMORY_STATE_DIR="+dir, "AGENTSMEMORY_STOP_HOOK=on")
	_ = cmd.Run()
	got := errb.String()
	for _, want := range []string{"alpha.go", "beta.go", "edited 2 file(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the Stop nudge does not name %q; the record is written and never read:\n%s", want, got)
		}
	}
}

// TestTheStopHookIsQuietWhenNothingWasTouched: a read-only session ends without
// being told what it changed, because it changed nothing.
func TestTheStopHookIsQuietWhenNothingWasTouched(t *testing.T) {
	cmd := exec.Command("bash", filepath.Join("hooks", "agentsmemory-stop-hook.sh"))
	cmd.Stdin = strings.NewReader(`{"session_id":"empty-session","hook_event_name":"Stop"}`)
	var errb strings.Builder
	cmd.Stderr = &errb
	cmd.Env = append(os.Environ(), "AGENTSMEMORY_STATE_DIR="+t.TempDir(), "AGENTSMEMORY_STOP_HOOK=on")
	_ = cmd.Run()
	if strings.Contains(errb.String(), "file(s)") {
		t.Errorf("the nudge claimed edits in a session that made none:\n%s", errb.String())
	}
}

// TestThePostToolUseHookIsRegistered: the wiring rung.
func TestThePostToolUseHookIsRegistered(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	var found bool
	for _, p := range inst.hookPlans() {
		if p.event == "PostToolUse" && !p.retire && strings.Contains(p.cmd, touchedHookFile) {
			found = true
		}
	}
	if !found {
		t.Fatal("no PostToolUse registration for the touched recorder; the Stop nudge can never name a file")
	}
}

// TestTheStatusLineMakesNoNetworkCall is the property that lets it render at all.
//
// A status line runs constantly. A command that waits on a server freezes the
// prompt for as long as the server takes, and a frozen prompt is the fastest way
// to have a status line removed. Every number it shows is read from a cache the
// SessionStart verify hook writes — a second READER of an answer that already
// exists rather than a second asker.
func TestTheStatusLineMakesNoNetworkCall(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("hooks", "agentsmemory-statusline.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"curl", "wget", "aiagentmemory ", "nc "} {
		if strings.Contains(string(b), bad) {
			t.Errorf("the status line invokes %q; it must read the cache and nothing else", strings.TrimSpace(bad))
		}
	}
}

// TestTheStatusLineIsSilentWithoutACache: no cache, no output, exit 0.
//
// An error string in a status line is PERMANENT noise — it is the one surface a
// user cannot dismiss — and no cache is the ordinary state before the first
// session-start hook has run.
func TestTheStatusLineIsSilentWithoutACache(t *testing.T) {
	cmd := exec.Command("bash", filepath.Join("hooks", "agentsmemory-statusline.sh"))
	cmd.Env = append(os.Environ(), "AGENTSMEMORY_STATE_DIR="+t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the status line exited non-zero with no cache: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("it printed %q with no cache; silence is the only acceptable answer", out)
	}
}

// TestTheStatusLineShowsWhatTheCacheHolds, including that a zero drift count is
// not rendered.
//
// A status line that always shows "0 drifted" spends the user's attention on the
// absence of a problem, every second, forever.
func TestTheStatusLineShowsWhatTheCacheHolds(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		if err := os.WriteFile(filepath.Join(dir, "agentsmemory-status.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", filepath.Join("hooks", "agentsmemory-statusline.sh"))
		cmd.Env = append(os.Environ(), "AGENTSMEMORY_STATE_DIR="+dir)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("status line failed: %v", err)
		}
		return string(out)
	}
	got := write("AM_WING=wing_acme\nAM_DRIFTED=11\nAM_INBOX=0\n")
	if !strings.Contains(got, "wing_acme") || !strings.Contains(got, "11 drifted") {
		t.Errorf("the status line does not show the wing and the drift: %q", got)
	}
	got = write("AM_WING=wing_acme\nAM_DRIFTED=0\nAM_INBOX=0\n")
	if strings.Contains(got, "drifted") {
		t.Errorf("it renders a zero drift count (%q); that spends attention on the absence of a problem", got)
	}
}

// TestTheStatusLineDoesNotReplaceOneTheUserSet.
//
// ⚠ THE REFUSAL IS THE DESIGN. A status line is the one surface a user cannot
// dismiss, and many have already put something they care about there. Overwriting
// it would be the most visible thing this installer does and the least invited.
func TestTheStatusLineDoesNotReplaceOneTheUserSet(t *testing.T) {
	mine := map[string]any{"statusLine": map[string]any{"type": "command", "command": "my-own-thing"}}
	if applyStatusLine(mine, "/cfg/agentsmemory-statusline.sh") {
		t.Error("it replaced a statusLine the user had already set")
	}
	got, _ := mine["statusLine"].(map[string]any)
	if got["command"] != "my-own-thing" {
		t.Errorf("the user's status line is gone: %v", mine["statusLine"])
	}

	fresh := map[string]any{}
	if !applyStatusLine(fresh, "/cfg/agentsmemory-statusline.sh") {
		t.Fatal("it did not fill an absent key")
	}
	set, _ := fresh["statusLine"].(map[string]any)
	if set["command"] != "/cfg/agentsmemory-statusline.sh" {
		t.Errorf("wrong command written: %v", fresh["statusLine"])
	}
}

// TestTheStatusLineIsRegistered covers the rung the script's own tests cannot see:
// a status line written to disk and registered by nothing renders never.
func TestTheStatusLineIsRegistered(t *testing.T) {
	b, err := os.ReadFile("installer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "statusLineCmd = i.hookCommand(i.statusLinePath())") {
		t.Fatal("the installer never passes the status-line command to ensureHooks; it ships and is never registered")
	}
}

// TestTheSkillIsInstalled covers the placement rung.
//
// A SKILL.md in the wrong directory is invisible and errors NOWHERE — Claude Code
// simply never discovers it, and every test that reads the file still passes. Only
// the install plan can see the difference.
func TestTheSkillIsInstalled(t *testing.T) {
	i := &Installer{targetDir: "/cfg", kit: agentKit{name: agentClaude}}
	got := i.skillPath("recall")
	want := filepath.Join("/cfg", "skills", "recall", "SKILL.md")
	if got != want {
		t.Errorf("skill lands at %s, want %s — Claude Code discovers skills/<name>/SKILL.md", got, want)
	}
	if len(nativeSkillAssets) == 0 {
		t.Fatal("no native skills declared; writeSkills would install nothing")
	}
	b, err := os.ReadFile("installer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "i.writeSkills()") {
		t.Error("writeSkills is never called; the skill ships embedded and is never written")
	}
}

// TestTheSkillPointsAtTheCatalogueRatherThanCopyingIt is the drift gate.
//
// The team's conventions live on the server and change there. A skill that
// restated one would be a second copy of a convention — a second thing to get
// wrong, and the copy nobody maintains is the one that stays wrong. This
// repository has recorded that against its own protocol documents more than once,
// which is why it is asserted here rather than trusted.
func TestTheSkillPointsAtTheCatalogueRatherThanCopyingIt(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("skills", "recall", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{"am_list_skills", "am_load_skill", "am_search"} {
		if !strings.Contains(body, want) {
			t.Errorf("the skill never names %s, so it cannot reach the centralised catalogue", want)
		}
	}
	// Front matter must declare a description and READ tools only. A skill that
	// can write is a second write path with none of the protocol's gates.
	if !strings.Contains(body, "description:") || !strings.Contains(body, "allowed-tools:") {
		t.Error("the skill lacks description or allowed-tools front matter")
	}
	for _, forbidden := range []string{"am_add_drawer", "am_kg_add", "am_diary_write", "am_update_drawer"} {
		if strings.Contains(body, "allowed-tools:") && strings.Contains(strings.SplitN(body, "---", 3)[1], forbidden) {
			t.Errorf("allowed-tools grants %s; a recall skill that can write is a write path with none of the protocol's gates", forbidden)
		}
	}
}
