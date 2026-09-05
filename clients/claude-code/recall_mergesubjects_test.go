package main

import (
	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheWidenedQuerySkipsMergeSubjects pins that the commit-subject fallback
// carries what was DONE rather than what was merged.
//
// ⚠ THE FALLBACK'S OWN PREMISE FAILS ON THE BRANCH IT EXISTS FOR. It widens a thin
// query with recent commit subjects because those are sentence-shaped, and it fires
// when the branch-work diff is empty — which is the default branch, where the
// merge-base is HEAD. On a default branch fed by pull requests, most of `git log` is
// `Merge pull request #N from org/some/branch-slug`: boilerplate plus a slug, nearly
// identical to every other merge, and about nothing anyone wrote a memory about. So
// the case the widening was written for was the case it served worst.
//
// Measured 2026-09-02 against this project's palace, room diary, at the hook's own
// 0.42 floor. The query the hook actually built on `main` — two merge subjects and
// one real one, truncated mid-word by the 200-char cut — returned COUNT 0. The same
// three commits with merges skipped returned the session's own diary entry at 0.393.
// That is not the thin-query failure the widening already handles: the query was
// long, and long enough to push the real subject off the end.
//
// TestAThinQueryIsWidenedOnEveryBranch cannot see this. Its fixture is three linear
// commits, a history shape a default branch does not have, so every subject it picks
// is a real one whether or not merges are skipped.
func TestTheWidenedQuerySkipsMergeSubjects(t *testing.T) {
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
		cmd := testexec.Command(t, "git", args...)
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
		git("add", "-A")
	}

	// A default branch shaped the way one actually is: real work arrives through
	// merges, so the newest subjects are merge subjects. Two merges and one real
	// commit is the exact ratio `git log -n 3` returned on this repository when the
	// defect was found.
	git("init", "-b", "main", "-q")
	write("seed.txt")
	git("commit", "-qm", "aardvark the groundwork")

	for _, tc := range []struct{ branch, subject, slug string }{
		{"task/basilisk", "basilisk the second", "task/basilisk"},
		{"task/chimaera", "chimaera the third", "task/chimaera"},
	} {
		git("checkout", "-qb", tc.branch)
		write(strings.ReplaceAll(tc.branch, "/", "-") + ".txt")
		git("commit", "-qm", tc.subject)
		git("checkout", "-q", "main")
		// --no-ff so the merge is a commit with its own subject, which is what a
		// pull request produces and what the hook then picks up.
		git("merge", "--no-ff", "-m", "Merge pull request #1 from org/"+tc.slug, tc.branch)
	}

	stubDir := t.TempDir()
	queryFile := filepath.Join(stubDir, "query")
	stub := "#!/usr/bin/env bash\nprintf '%s' \"$*\" > " + queryFile + "\necho '{\"count\":0,\"hits\":[]}'\n"
	if err := os.WriteFile(filepath.Join(stubDir, "aiagentmemory"), []byte(stub), 0o755); err != nil {
		t.Fatalf("stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "recall.sh"), script, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd := testexec.Command(t, "bash", filepath.Join(repo, "recall.sh"))
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","source":"startup"}`)
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+":"+os.Getenv("PATH"), "CLAUDE_PROJECT_DIR="+repo,
		"AGENTSMEMORY_LOCAL_TOKEN=", "AGENTSMEMORY_TOKEN=")
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("the hook failed the session (%v, out=%q) — it must never do that", err, out)
	}

	asked, err := os.ReadFile(queryFile)
	if err != nil {
		t.Fatalf("the hook never called the server: %v", err)
	}
	got := string(asked)

	// The half that fails today: a merge subject in the query is a query about
	// nothing, and it crowds out the subjects that describe real work.
	if strings.Contains(got, "Merge pull request") {
		t.Errorf("the query carries merge boilerplate, which is about nothing anyone "+
			"filed a memory about:\n  asked: %s", got)
	}
	// The half that keeps the fix honest: skipping merges must not skip the work.
	// Without this, deleting the fallback entirely would satisfy the check above.
	for _, s := range []string{"aardvark the groundwork", "basilisk the second", "chimaera the third"} {
		if !strings.Contains(got, s) {
			t.Errorf("the query does not carry the real commit subject %q, so skipping "+
				"merges dropped the work instead of the noise.\n  asked: %s", s, got)
		}
	}
}
