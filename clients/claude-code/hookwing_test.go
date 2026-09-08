package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// TestTheRecallHookPrefersTheProjectsPinOverTheInstalledWing drives the shipped
// script, because the defect lives in the script rather than in anything Go.
//
// The installer bakes AGENTSMEMORY_WING onto the hook's command line, and a
// leading assignment overrides the inherited environment — so the baked value won
// against every other source, including a wing the sandbox launcher had already
// resolved correctly. Hook registrations land in the user-level settings.json
// whatever --scope says, so one project's wing was recalled in every project on
// the machine (#305). Observed, not theorised: a session in another repository was
// handed this project's diary and llm_open_threads checkpoint.
//
// Both directions are asserted, and the pair is the point. "The pin is used" alone
// is satisfied by a hook that ignores the environment entirely, which would break
// every install that has no pin — that is the population the fallback protects, and
// it is most of them today.
func TestTheRecallHookPrefersTheProjectsPinOverTheInstalledWing(t *testing.T) {
	script := filepath.Join("hooks", "agentsmemory-recall-hook.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the shipped hook is not where this test looks: %v", err)
	}

	// ⚠ A STUB ON PATH, BECAUSE THE HOOK EXITS BEFORE THE WING WITHOUT ONE.
	// Line 211 is `command -v aiagentmemory … || exit 0`, so on a machine without
	// the CLI installed this test would pass by never reaching the code it checks.
	// It did exactly that: it was green locally, where the CLI is installed, and
	// red in CI, where it is not — the green-here/red-there shape this session has
	// already recorded twice. The stub makes the run hermetic and identical on
	// both, and it costs nothing: the assertions read the trace the hook writes
	// before it would ever call out.
	stubBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubBin, "aiagentmemory"),
		[]byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, dir, bakedWing string) string {
		t.Helper()
		// The hook's own recall switch is left ON: the run stops at the credential
		// check before any network call, which is far enough to have resolved the
		// wing and traced it. The trace goes to stderr, and that is what carries the
		// verdict here — naming the switch as NAME=value would read as an operator
		// promise to TestDocumentedEnvVarsAreRead, which scans Go and cannot see
		// that a bash hook is what reads it.
		cmd := testexec.Command(t, "bash", script)
		cmd.Stdin = strings.NewReader(`{"session_id":"wingprobe","source":"startup"}`)
		cmd.Env = append(os.Environ(),
			"PATH="+stubBin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"CLAUDE_PROJECT_DIR="+dir,
			"AGENTSMEMORY_WING="+bakedWing,
		)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}

	write := func(t *testing.T, dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// ⚠ THE FIXTURE MUST BE A REPOSITORY WITH WORK IN IT, or this test proves
	// nothing. The hook builds its query from the branch name and the changed
	// files, and exits early with "no query: empty branch name and no changed
	// files" in a bare TempDir — before the wing is resolved at all. The first
	// version of this test asserted three things about a run that never reached
	// the code under test, which is the shape #294 recorded: a fixture one layer
	// short of the line it is written to protect.
	repo := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		git := func(args ...string) {
			t.Helper()
			if out, err := testexec.Command(t, "git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		git("init", "-q", "-b", "fix/the-recall-hook-follows-the-project-pin")
		git("config", "user.email", "probe@example.invalid")
		git("config", "user.name", "probe")
		write(t, dir, "recall_hook_wing_resolution.md", "seed\n")
		git("add", "recall_hook_wing_resolution.md")
		git("commit", "-qm", "the recall hook resolves its wing from the project pin rather than the installed default")
		// ⚠ FILES is the BRANCH'S WORK (a diff against the merge-base), not the
		// uncommitted tree — so an untracked file contributes nothing and the hook
		// falls back to `git log --format=%s`. The commit SUBJECT is therefore what
		// has to be substantive: a short one ("seed") trips the hook's own
		// "query too short to ask with" floor and the run never reaches the wing.
		// Two fixture revisions were spent learning that by guessing instead of
		// reading how QUERY is built.
		return dir
	}

	t.Run("a pinned project beats the baked wing", func(t *testing.T) {
		dir := repo(t)
		write(t, dir, ".aiagentmemory", "# a comment\n\nwing=wing_alpha\n")
		got := run(t, dir, "wing_acme")
		if !strings.Contains(got, "wing_alpha") {
			t.Errorf("the hook did not take the project's pin.\n"+
				"  The baked value is written into the hook's command line and overrides the "+
				"inherited environment, so without this precedence one project's wing is "+
				"recalled in every project on the machine (#305).\ngot:\n%s", got)
		}
		if strings.Contains(got, "wing_acme") {
			t.Errorf("the baked wing still reached the search despite a project pin:\n%s", got)
		}
	})

	t.Run("an unpinned project keeps the baked wing", func(t *testing.T) {
		dir := repo(t) // no .aiagentmemory at all
		got := run(t, dir, "wing_acme")
		if !strings.Contains(got, "wing_acme") {
			t.Errorf("an unpinned project lost the installed wing.\n"+
				"  That fallback is what keeps every existing install working — most projects "+
				"carry no pin today, and breaking them is a wider regression than the defect "+
				"being fixed.\ngot:\n%s", got)
		}
	})

	t.Run("local overrides shared", func(t *testing.T) {
		dir := repo(t)
		write(t, dir, ".aiagentmemory", "wing=wing_beta\n")
		write(t, dir, ".aiagentmemory.local", "wing=wing_delta\n")
		got := run(t, dir, "")
		if !strings.Contains(got, "wing_delta") || strings.Contains(got, "wing_beta") {
			t.Errorf("precedence between the two pin files disagrees with readProjectConfig, "+
				"so the hook and the Go path would resolve the same project differently:\n%s", got)
		}
	})

	// The third rung, and the only one whose source is not in the registration:
	// with no pin and no baked wing the search goes out with no wing argument,
	// which am_search scopes to the registration's own default_wing — this
	// project's memories surfacing in an unrelated repository, #305's outcome by a
	// quieter route. It traced NOTHING, so an operator reading settings.json saw no
	// wing and reasonably concluded the recall was unscoped (#432). The assertion
	// is on the trace rather than on the recall, because the recall is correct
	// server-side and the silence was the defect.
	t.Run("no pin and no baked wing says so", func(t *testing.T) {
		dir := repo(t) // no .aiagentmemory, and the baked value below is empty
		got := run(t, dir, "")
		if !strings.Contains(got, "no wing from either rung") {
			t.Errorf("the hook resolved no wing and said nothing about it, so the one rung "+
				"an operator cannot see in the registration is also the one the hook does "+
				"not name:\ngot:\n%s", got)
		}
		if !strings.Contains(got, "default_wing") {
			t.Errorf("the trace does not say where an omitted wing actually lands; without "+
				"that the reader concludes the recall is unscoped, which is the belief "+
				"#432 is about:\ngot:\n%s", got)
		}
	})
}

// pinnedProjectDir writes a git repository whose .aiagentmemory pins one wing.
//
// It is a real repository because the SessionStart hook builds its query from
// the branch name and the changed files, and refuses with "no query" outside
// one — so a bare directory would fail that hook before it ever reached the
// rung this test is about. The remote deliberately names a DIFFERENT project,
// so a hook that stopped reading the pin and derived from the remote instead
// would resolve a visibly wrong wing rather than the right one by accident.
func pinnedProjectDir(t *testing.T, wing string) string {
	t.Helper()
	dir := t.TempDir()
	body := "# a project that pins its own wing\nwing=" + wing + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".aiagentmemory"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := testexec.Command(t, "git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "fix/a-project-that-pins-its-own-wing")
	git("config", "user.email", "probe@example.invalid")
	git("config", "user.name", "probe")
	git("remote", "add", "origin", "https://example.invalid/some-other-project.git")
	git("add", "-A")
	git("commit", "-qm", "a project that pins its wing, so neither hook has to derive one")
	return dir
}

// TestBothRecallHooksResolveTheProjectsPin is the anti-drift gate for the ONE
// piece of logic the two recall hooks each carry their own copy of.
//
// The asymmetry it exists to catch already shipped. The SessionStart hook read
// the .aiagentmemory pin; the task-recall hook read $AGENTSMEMORY_WING alone. So
// removing --wing from the install line (#432/#435 — correct on its own terms:
// it baked one project's wing onto every hook command on the machine) left the
// task hook with no wing at all, and it fell through to its no-wing branch in
// silence. Measured 2026-09-08 over 120 real task queries at that hook's own
// parameters: 75% of everything it injected came from ANOTHER PROJECT'S WING.
//
// Nothing caught it. Both hooks were driven by tests, both passed, and the
// existing scope test uses an UNPINNED fixture — so the rung that differs
// between them was never exercised. That is the shape §Reachability keeps
// recording: a mutant proves a test notices a change, never that the thing
// under test reaches anything.
//
// It compares BEHAVIOUR — the wing each script actually searched — rather than
// the two resolver texts, because a tidy-up that aligned the texts while
// breaking one hook's call site is exactly the failure a text comparison waves
// through.
func TestBothRecallHooksResolveTheProjectsPin(t *testing.T) {
	const wing = "wing_alpha"
	pinned := pinnedProjectDir(t, wing)

	searched := map[string]string{}
	for _, hookName := range []string{"agentsmemory-task-recall-hook.sh", "agentsmemory-recall-hook.sh"} {
		t.Run(hookName, func(t *testing.T) {
			// extraEnv is appended after recallHookRun's own CLAUDE_PROJECT_DIR, and
			// the last assignment wins, so this replaces the unpinned fixture.
			out, errOut, calls := recallHookRun(t, hookName,
				[]string{"CLAUDE_PROJECT_DIR=" + pinned},
				"A PROJECT MEMORY\n  "+wing+"/decisions\n", 0, "")
			if len(calls) == 0 {
				t.Fatalf("the hook made no search at all with a pinned wing:\nstdout: %s\nstderr: %s", out, errOut)
			}
			if !strings.Contains(calls[0], "wing="+wing) {
				t.Fatalf("the hook did not search the wing its project pins. This is #438: the "+
					"task hook resolved $AGENTSMEMORY_WING alone, so with --wing gone from the "+
					"install line it searched no wing and the server answered from the "+
					"REGISTRATION's default_wing — 75%% of what it injected was another "+
					"project's memories.\ncall: %q\nstderr: %s", calls[0], errOut)
			}
			if !strings.Contains(errOut, "wing from the project's pin") {
				t.Errorf("the hook resolved the pin but did not SAY so. #305 was diagnosed only "+
					"by grepping settings.json, because nothing a hook emitted named the source "+
					"of its wing; doctor prints this stderr verbatim:\n%s", errOut)
			}
			// THE CRAFT CALL MUST SURVIVE THE FIX. Resolving a wing is what makes
			// either hook ask wing_craft at all — with no wing it makes one call and
			// silently reads no craft. So a fix that scoped the project call but lost
			// the craft call would trade foreign noise for missing craft, and the
			// wing assertion above would pass either way.
			if len(calls) != 2 {
				t.Fatalf("with a wing resolved the hook must make TWO searches — the project's "+
					"wing, then wing_craft — and made %d: %q", len(calls), calls)
			}
			if !strings.Contains(calls[1], "wing=wing_craft") {
				t.Errorf("the second call is not wing_craft, so the pin bought project scoping "+
					"and paid for it in craft: %q", calls[1])
			}
			searched[hookName] = calls[0]
		})

		// ⚠ THE COMBINATION NOTHING TESTED: a pin AND a baked AGENTSMEMORY_WING.
		// That is not a corner — it is every machine that ran the install line this
		// repo documented until #432, which wrote `--wing` onto every hook command.
		// The two rungs disagree there, and only the ORDER decides; the sibling has
		// a test for this and the task hook did not, which is the same asymmetry
		// #438 was.
		t.Run(hookName+" with a pin AND a baked wing", func(t *testing.T) {
			out, errOut, calls := recallHookRun(t, hookName,
				[]string{"CLAUDE_PROJECT_DIR=" + pinned, "AGENTSMEMORY_WING=wing_beta"},
				"A PROJECT MEMORY\n  "+wing+"/decisions\n", 0, "")
			if len(calls) == 0 {
				t.Fatalf("no search at all:\nstdout: %s\nstderr: %s", out, errOut)
			}
			if strings.Contains(calls[0], "wing=wing_beta") {
				t.Fatalf("the baked default beat the project's pin. A hook registration is "+
					"user-scope whatever --scope says, so the baked value is one project's "+
					"wing in front of every repository on the machine — #305 exactly: %q", calls[0])
			}
			if !strings.Contains(calls[0], "wing="+wing) {
				t.Fatalf("neither rung produced the pinned wing: %q", calls[0])
			}
			if len(calls) != 2 || !strings.Contains(calls[1], "wing=wing_craft") {
				t.Errorf("the craft call did not survive the two rungs disagreeing: %q", calls)
			}
		})
	}
	// The copies may be worded differently; what may never differ is which wing
	// they land on for one repository. A reader of either script must be able to
	// predict the other.
	if len(searched) == 2 {
		for name, call := range searched {
			if !strings.Contains(call, "wing="+wing) {
				t.Errorf("%s resolved a different wing from its sibling for the same pinned "+
					"project; the two resolvers have drifted: %q", name, call)
			}
		}
	}
}
