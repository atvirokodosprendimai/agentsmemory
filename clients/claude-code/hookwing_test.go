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
}
