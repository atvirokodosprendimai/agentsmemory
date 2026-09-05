package repohygiene

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// TestComposeFilesReadsTheIndexNotTheDirectory pins BOTH directions of the rule,
// because each one alone is satisfied by a broken implementation.
//
// Returning the tracked file alone is satisfied by a function that returns
// everything — the directory glob this replaced passed that half. Skipping the
// untracked file alone is satisfied by a function that returns nothing, which is
// the silent shape that would make every gate downstream vacuous. Only the pair
// says the index is what was read.
//
// The failure it exists to prevent is not a red suite, it is a red suite nobody
// can clear: scripts/redeploy.sh runs the tests before it builds, so on a host
// carrying a local overlay the documented deploy procedure had no working path at
// all (#178, #296).
func TestComposeFilesReadsTheIndexNotTheDirectory(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := testexec.Command(t, "git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	// Committing needs an identity, and a developer's global config is not
	// something a test may depend on or disturb.
	git("config", "user.email", "gate@example.invalid")
	git("config", "user.name", "gate")

	write := func(name string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("services: {}\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	tracked := write("docker-compose.yml")
	write("docker-compose.local.yml") // the operator's overlay: present, never committed
	git("add", "docker-compose.yml")
	git("commit", "-qm", "compose")

	files, err := ComposeFiles(dir)
	if err != nil {
		t.Fatalf("ComposeFiles: %v", err)
	}

	// Resolved on both sides: macOS hands a TempDir under /var, which is a symlink
	// to /private/var, and git reports the path it was given.
	wantDir, _ := filepath.EvalSymlinks(filepath.Dir(tracked))
	var got []string
	for _, f := range files {
		d, _ := filepath.EvalSymlinks(filepath.Dir(f))
		got = append(got, filepath.Join(d, filepath.Base(f)))
	}
	want := filepath.Join(wantDir, "docker-compose.yml")

	if len(got) != 1 || got[0] != want {
		t.Fatalf("ComposeFiles returned %v, want exactly [%s].\n"+
			"  Too many means the directory is still being read, and an operator's untracked "+
			"overlay rejoins every gate's universe — the defect this replaced.\n"+
			"  Too few means the enumeration answers nothing, which makes every gate "+
			"downstream vacuously green, and that is the worse of the two.", got, want)
	}
}

// TestComposeFilesFailsRatherThanFallingBackToTheDirectory holds the choice that
// makes the fix trustworthy on the hosts it was written for.
//
// A fallback to filepath.Glob when git cannot answer would restore the original
// defect exactly where nobody would look for it — a machine with an overlay and a
// git that does not work there — and would restore it silently, which is this
// repository's recorded failure shape. A directory that is not a repository is the
// cheapest way to ask git a question it must refuse.
func TestComposeFilesFailsRatherThanFallingBackToTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := ComposeFiles(dir)
	if err == nil {
		t.Fatalf("ComposeFiles returned %v and no error over a directory that is not a git "+
			"repository — it fell back to reading the directory, which is the behaviour this "+
			"function exists to remove", files)
	}
}
