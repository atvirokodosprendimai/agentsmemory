package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ADR-041 T4. The mechanism ADR-017 named in 2026-08 and left unbuilt pending a
// measurement — which ADR-041 T2 now supplies.

// TestRecallHookIsRegistered is rung 2. The script is inert without this one line
// in the installer, and a hook that is written but never registered is this
// repository's characteristic defect wearing a shell script.
//
// ⚠ IT ASSERTS THE EVENT, not merely that some entry exists. The first version
// asked only whether a PreCompact plan carried the script, which is exactly the
// question that stayed green while the recall was being written to a debug log
// nothing reads. Which event a hook is registered on IS the mechanism;
// TestEveryInjectingHookIsOnAnInjectingEvent generalises this to every script.
func TestRecallHookIsRegistered(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, recallHookFile)); err != nil {
		t.Fatalf("the recall hook was not written: %v", err)
	}

	var events []string
	for _, p := range inst.hookPlans() {
		if strings.Contains(p.cmd, recallHookFile) {
			events = append(events, p.event)
		}
	}
	if len(events) == 0 {
		t.Fatal("no hook plan invokes the recall hook — the script is on disk and nothing " +
			"runs it, so a fresh context still starts blind")
	}
	for _, ev := range events {
		if !injectingEvents[ev] {
			t.Errorf("the recall hook is registered on %q, whose stdout goes to the debug log; "+
				"the recall would run and never reach the model", ev)
		}
	}
}

// TestF6AHookIsSilentInTheCommonCase drives the SCRIPT, not a description of it.
//
// F-6 is the constraint every pushed-recall mechanism lives inside: the SessionStart
// verify hook states the reasoning in its own header — "a hook that reports 'all
// good' at every session start is a hook people stop reading, and its output is
// spent context". A recall hook that speaks at every session start is that
// mistake at a higher frequency.
//
// ⚠ IT STUBS THE BINARY, AND THE FIRST VERSION DID NOT — which made it a test that
// could not fail. Without a stub the hook was silent because the REAL aiagentmemory
// found nothing in a temp directory, not because the guard under test worked, and
// two mutants survived: removing the empty-query guard and removing the off-switch
// both left the output empty for that downstream reason. The stub records that it
// ran, so "silent" and "never got that far" stop being the same observation.
//
// The speaks-when-it-has-something case is not decoration either. A test that only
// asserts silence is satisfied by `exit 0` on line one.
func TestF6AHookIsSilentInTheCommonCase(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	script, err := assets.ReadFile(recallHookAsset)
	if err != nil {
		t.Fatalf("read embedded hook: %v", err)
	}

	// A stub on PATH that records the fact it was called and returns one hit.
	stubDir := t.TempDir()
	marker := filepath.Join(stubDir, "was-called")
	// ⚠ THE STUB ASSERTS THE INVOCATION, not just its own existence. The flags that
	// keep this hook useful — a room scope and a distance floor — are arguments, not
	// branches, so no assertion on the hook's OUTPUT can see them: a stub that
	// ignores its arguments returns a hit either way and the mutant survives.
	// Measured 2026-08-28: without a room scope the top three hits for a real
	// mid-work query were this session's own transcript chunks, so dropping these
	// flags makes the hook actively harmful rather than merely quiet.
	//
	// ⚠ THE ROOM IS `diary`, AND THIS ASSERTION IS WHY THE WRONG ONE SURVIVED SO
	// LONG. Matching on the flag proves the hook PASSES a scope; it cannot show that
	// the scope returns anything, because the stub answers whatever it is asked.
	// The hook shipped with `room=decisions` and was mute on most real branches —
	// measured across three, `decisions` returned hits on one and `diary` on all
	// three — while this test stayed green throughout. What closes that gap is not
	// a stronger assertion here but `aiagentmemory doctor`, which runs the installed
	// hook against a live palace and fails when it produces nothing.
	stub := "#!/usr/bin/env bash\n" +
		"touch " + marker + "\n" +
		"case \"$*\" in\n" +
		"  *room=diary*max_distance*) echo '{\"count\":1,\"hits\":[{\"id\":\"x\"}]}' ;;\n" +
		"  *) echo '{\"count\":0,\"hits\":[]}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(stubDir, "aiagentmemory"), []byte(stub), 0o755); err != nil {
		t.Fatalf("stub: %v", err)
	}

	run := func(t *testing.T, extraEnv []string, workdir string) (string, bool) {
		t.Helper()
		_ = os.Remove(marker)
		cmd := exec.Command("bash", filepath.Join(workdir, "recall.sh"))
		cmd.Dir = workdir
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","source":"compact"}`)
		cmd.Env = append(os.Environ(),
			append([]string{"PATH=" + stubDir + ":" + os.Getenv("PATH")}, extraEnv...)...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("the hook failed the session (%v) — it must never do that", err)
		}
		_, statErr := os.Stat(marker)
		return string(out), statErr == nil
	}

	place := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "recall.sh"), script, 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
		return dir
	}

	// Not a repository: no branch, no diff, so no query. A query built from
	// nothing recalls whatever is most popular, which is worse than silence.
	quiet := place(t)
	if out, called := run(t, []string{"CLAUDE_PROJECT_DIR=" + quiet}, quiet); out != "" || called {
		t.Errorf("with nothing to go on the hook spoke (%q) or searched anyway (called=%v)", out, called)
	}

	// And it SPEAKS when it has something. Without this the assertions around it
	// are satisfied by a script that does nothing at all.
	live := place(t)
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@example.test"},
		{"config", "user.name", "t"}, {"commit", "--allow-empty", "-m", "base"}} {
		c := exec.Command("git", args...)
		c.Dir = live
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git unavailable for the positive case: %v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(live, "recallrate.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed a change: %v", err)
	}
	c := exec.Command("git", "add", "-A")
	c.Dir = live
	_, _ = c.CombinedOutput()
	out, called := run(t, []string{"CLAUDE_PROJECT_DIR=" + live}, live)
	if !called {
		t.Error("on a branch with changed files the hook never searched — it cannot inject a " +
			"recall it did not perform")
	}
	if !strings.Contains(out, "Memory recalled") {
		t.Errorf("the hook had a hit and said nothing: %q", out)
	}

	// ⚠ A FAILING RECALL IS NOT SILENCE. The stub exits non-zero; the hook must say
	// so. Without this the hook is silent whether the palace had nothing or the call
	// never worked, and on a --local install — where `mcp` demands a token that does
	// not exist — it would have been permanently the second while looking like the
	// first. That is how this was found: 25 measurement queries returned clean
	// zeroes that were 25 swallowed errors.
	broken := place(t)
	brokenBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(brokenBin, "aiagentmemory"),
		// ⚠ THE MESSAGE IS LOAD-BEARING NOW. It used to read "no workspace token
		// found", which is the one failure the hook deliberately keeps quiet about:
		// a Claude hosted install has no credential, and saying so at every session
		// start is noise nobody can act on. A genuine fault must still speak, and
		// that is what this case is for — TestNoCredentialIsSilentButABadOneSpeaks
		// owns the other half.
		[]byte("#!/usr/bin/env bash\necho 'transport error: connection refused' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("stub: %v", err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@example.test"},
		{"config", "user.name", "t"}, {"commit", "--allow-empty", "-m", "base"}} {
		c := exec.Command("git", args...)
		c.Dir = broken
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(broken, "recallrate.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stage := exec.Command("git", "add", "-A")
	stage.Dir = broken
	_, _ = stage.CombinedOutput()
	cmd := exec.Command("bash", filepath.Join(broken, "recall.sh"))
	cmd.Dir = broken
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","source":"compact"}`)
	cmd.Env = append(os.Environ(), "PATH="+brokenBin+":"+os.Getenv("PATH"), "CLAUDE_PROJECT_DIR="+broken)
	bout, err := cmd.Output()
	if err != nil {
		t.Fatalf("the hook failed the session on a failed recall (%v) — it must never do that", err)
	}
	if !strings.Contains(string(bout), "could not run") {
		t.Errorf("a failed recall produced %q — silence here is indistinguishable from having "+
			"nothing to say, which is how a hook that can never speak looks healthy", string(bout))
	}

	// ⚠ THE OFF-SWITCH IS TESTED HERE, IN THE LIVE TREE, and the placement is the
	// point. Run in an empty directory it passes whatever the switch does, because
	// the empty-query guard produces the silence — which is exactly how its first
	// mutant survived. Only a working tree, where a query exists and the hook would
	// otherwise speak, leaves the switch as the one thing that can keep it quiet.
	if out, called := run(t, []string{"AGENTSMEMORY_RECALL=off", "CLAUDE_PROJECT_DIR=" + live}, live); out != "" || called {
		t.Errorf("AGENTSMEMORY_RECALL=off produced output (%q) or still searched (called=%v) "+
			"on a tree where the hook otherwise speaks", out, called)
	}
}

// TestTheQueryCarriesTheBranchWorkOnACleanTree pins the second reason this hook
// could not work when it shipped.
//
// ⚠ THE FIRST VERSION ASKED `git diff --name-only HEAD` — uncommitted changes
// only, which is EMPTY on the clean tree a session sits on right after a commit.
// The query collapsed to the bare branch name, and measured 2026-08-28 against
// the live palace, bare branch names land at 0.450-0.509 while the hook's floor
// is 0.42: silent on every one of three real branches. The hook looked like F-6
// working perfectly and was simply mute.
//
// No assertion on the hook's OUTPUT can catch that — the stub returns a hit
// whatever it is asked. The query is an argument, so the stub has to record it.
// This is the same shape as the room scope flags above: a mechanism that is
// an argument rather than a branch is invisible to a test that only reads stdout.
func TestTheQueryCarriesTheBranchWorkOnACleanTree(t *testing.T) {
	for _, bin := range []string{"bash", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not available; the acceptance fence installs it", bin)
		}
	}
	script, err := assets.ReadFile(recallHookAsset)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}

	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-b", "main", "-q")
	write("base.txt")
	git("add", "-A")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "fix/a-distinctive-branch")
	// The distinctive part: COMMITTED, so the tree is clean afterwards. That is
	// exactly the state the broken query saw as empty.
	write("theonlyfilethatnames.go")
	git("add", "-A")
	git("commit", "-qm", "work")

	stubDir := t.TempDir()
	queryFile := filepath.Join(stubDir, "query")
	stub := "#!/usr/bin/env bash\nprintf '%s' \"$*\" > " + queryFile + "\necho '{\"count\":0,\"hits\":[]}'\n"
	if err := os.WriteFile(filepath.Join(stubDir, "aiagentmemory"), []byte(stub), 0o755); err != nil {
		t.Fatalf("stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "recall.sh"), script, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	ask := func(t *testing.T, env ...string) string {
		t.Helper()
		_ = os.Remove(queryFile)
		cmd := exec.Command("bash", filepath.Join(repo, "recall.sh"))
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","source":"compact"}`)
		// The token variables are CLEARED rather than inherited: an ambient token in
		// the developer's shell would make the no-token assertion below pass or fail
		// for a reason that has nothing to do with the hook.
		cmd.Env = append(os.Environ(), append([]string{
			"PATH=" + stubDir + ":" + os.Getenv("PATH"), "CLAUDE_PROJECT_DIR=" + repo,
			"AGENTSMEMORY_LOCAL_TOKEN=", "AGENTSMEMORY_TOKEN=",
		}, env...)...)
		if out, err := cmd.Output(); err != nil {
			t.Fatalf("the hook failed the session (%v, out=%q) — it must never do that", err, out)
		}
		b, err := os.ReadFile(queryFile)
		if err != nil {
			t.Fatalf("the hook never searched: %v", err)
		}
		return string(b)
	}

	// ⚠ --token IS PASSED ONLY WHEN THE ENVIRONMENT HAS ONE, and this is an
	// assertion about an ARGUMENT, which no assertion on the hook's output can
	// reach. The first version always passed a token, defaulting to the placeholder
	// `local`; --token overrides the CLI's own resolution, so an install keeping its
	// token in agentsmemory.env authenticated as "local" and was refused. That
	// mutant SURVIVED every output-based test in this file.
	if got := ask(t); strings.Contains(got, "--token") {
		t.Errorf("with no token in the environment the hook passed one anyway: %q\n"+
			"--token overrides the CLI's own lookup, so a placeholder silently breaks "+
			"every install that resolves its credential elsewhere.", got)
	}
	if got := ask(t, "AGENTSMEMORY_TOKEN=sk-from-the-environment"); !strings.Contains(got, "--token sk-from-the-environment") {
		t.Errorf("a token in the environment did not reach the CLI: %q", got)
	}

	asked, err := os.ReadFile(queryFile)
	if err != nil {
		t.Fatalf("the hook never searched on a clean branch with committed work: %v", err)
	}
	query := string(asked)
	if !strings.Contains(query, "theonlyfilethatnames.go") {
		t.Errorf("the query does not name the branch's committed work: %q\n"+
			"On a clean tree an uncommitted-only diff is empty, so the query collapses to the "+
			"bare branch name — which measures below this hook's own relevance floor and makes "+
			"it permanently silent.", query)
	}
	if !strings.Contains(query, "fix/a-distinctive-branch") {
		t.Errorf("the query does not name the branch: %q", query)
	}
}

// TestNoCredentialIsSilentButABadOneSpeaks pins the difference the previous
// version of this hook erased.
//
// ⚠ WITHOUT THIS TEST THE NO-CREDENTIAL BRANCH IS `2>/dev/null` WEARING A BETTER
// COMMENT. That is the defect this hook already shipped once: every failure —
// missing token, unreachable server, renamed flag — reported as a clean empty
// recall, so a hook that could never speak looked exactly like F-6 working.
//
// The distinction is deliberate and narrow. "No workspace token is configured" is
// a STATE: a Claude hosted install keeps its token in the MCP registration header,
// which the CLI does not read, so the hook cannot ask and the operator cannot act
// on being told so at every session start. Any OTHER failure is a fault and the
// hook must say it.
func TestNoCredentialIsSilentButABadOneSpeaks(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available; the acceptance fence installs it")
	}
	script, err := assets.ReadFile(recallHookAsset)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}

	// A real repository, so the hook gets far enough to search. Without this the
	// test passes for the wrong reason: silence because there was no query.
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available; the acceptance fence installs it")
	}
	git("init", "-b", "main", "-q")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "fix/some-real-branch")
	if err := os.WriteFile(filepath.Join(repo, "subject.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "work")
	if err := os.WriteFile(filepath.Join(repo, "recall.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}

	runWith := func(t *testing.T, stderrLine string) string {
		t.Helper()
		stubDir := t.TempDir()
		stub := "#!/usr/bin/env bash\nprintf '%s\\n' " +
			"'" + stderrLine + "' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(stubDir, "aiagentmemory"), []byte(stub), 0o755); err != nil {
			t.Fatalf("stub: %v", err)
		}
		cmd := exec.Command("bash", filepath.Join(repo, "recall.sh"))
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","source":"compact"}`)
		cmd.Env = append(os.Environ(),
			"PATH="+stubDir+":"+os.Getenv("PATH"), "CLAUDE_PROJECT_DIR="+repo,
			"AGENTSMEMORY_LOCAL_TOKEN=", "AGENTSMEMORY_TOKEN=")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("the hook failed the session (%v) — it must never do that", err)
		}
		return string(out)
	}

	if out := runWith(t, "aiagentmemory: no workspace token found: pass --token"); out != "" {
		t.Errorf("with no credential configured the hook spoke: %q\n"+
			"That is a state the operator cannot act on, reported at every session start.", out)
	}

	// The other half, and the one that makes the check above mean something: a
	// test asserting only silence is satisfied by `exit 0` on line one.
	out := runWith(t, "aiagentmemory: initialize: transport error: authorization required")
	if !strings.Contains(out, "could not run") {
		t.Errorf("a real failure was swallowed: %q\n"+
			"Every failure looking like a clean empty recall is the defect this hook shipped once.", out)
	}
}

// t4RecordPath is the record that owns the recall hook's shipped configuration.
const t4RecordPath = "../../docs/adr/ADR-041-the-recall-that-does-not-depend-on-remembering/" +
	"tasks/T4-recall-injection.md"

// shippedRoomRE reads the ONE sentence in that record that names the room in a
// machine-readable way. Prose around it may discuss any number of rooms — this is
// the statement of what ships.
var shippedRoomRE = regexp.MustCompile("The shipped room is now `([a-z_]+)`")

// hookRoomRE reads the room the installed script actually asks for.
var hookRoomRE = regexp.MustCompile(`-a room=([a-z_]+)`)

// TestTheRecallHookAsksTheRoomItsRecordShips pins the room to the decision.
//
// ⚠ THIS IS THE RUNG THE STUB CANNOT REACH. TestF6AHookIsSilentInTheCommonCase
// drives the hook through a stub whose matcher carries the same literal the hook
// passes, so changing the room in both places keeps the suite green — verified by
// an independent reviewer, who renamed `diary` to a room that does not exist in
// both files and watched `go test ./...` exit 0. The room was wrong in production
// for two repairs precisely because nothing outside the hook had an opinion about
// it; the record does, so make the record the other end of the pin.
//
// Changing the room deliberately means changing the record's sentence too, which is
// the change being reviewed rather than a line nobody reads.
func TestTheRecallHookAsksTheRoomItsRecordShips(t *testing.T) {
	script, err := assets.ReadFile("hooks/agentsmemory-recall-hook.sh")
	if err != nil {
		t.Fatalf("read embedded recall hook: %v", err)
	}
	asked := hookRoomRE.FindSubmatch(script)
	if asked == nil {
		t.Fatal("the recall hook passes no `-a room=` at all: unscoped, it recalls this " +
			"session's own transcript chunks back into the context compaction just cleared")
	}

	record, err := os.ReadFile(t4RecordPath)
	if err != nil {
		t.Fatalf("read %s: %v", t4RecordPath, err)
	}
	shipped := shippedRoomRE.FindSubmatch(record)
	if shipped == nil {
		t.Fatalf("%s names no shipped room. The sentence \"The shipped room is now `<room>`\" is "+
			"what pins the hook's room to a decision someone made; without it the hook's room is "+
			"again a literal only the hook has an opinion about", t4RecordPath)
	}

	if string(asked[1]) != string(shipped[1]) {
		t.Errorf("the recall hook asks room %q; %s says the shipped room is %q.\n"+
			"  One of the two moved without the other. The room decides whether this hook can "+
			"speak at all: `decisions` shipped for two repairs and was mute on every branch whose "+
			"work was not filed there.", asked[1], t4RecordPath, shipped[1])
	}
}
