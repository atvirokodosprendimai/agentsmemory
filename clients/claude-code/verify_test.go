package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/anchorcontract"
	"github.com/atvirokodosprendimai/agentsmemory/internal/testexec"
)

// TestFindSurvivesReformatting is the line between a useful flag and noise: a
// re-indent or a gofmt must NOT read as drift, or every commit marks half the
// palace stale and nobody looks at the flag again.
func TestFindSurvivesReformatting(t *testing.T) {
	src := readSourceFrom("package main\n\nfunc pin() {\n\t\tenv :=  []string{\n\t\t\tconfigEnv + \"=\" + dir,\n\t\t}\n}\n")

	for name, snippet := range map[string]string{
		"exact":                      "env := []string{",
		"re-indented":                "        env :=    []string{",
		"multi-line":                 "env := []string{\n  configEnv + \"=\" + dir,",
		"one distinctive expression": "configEnv + \"=\" + dir",
	} {
		t.Run(name, func(t *testing.T) {
			if line, ok := src.find(snippet); !ok {
				t.Errorf("snippet not found; formatting must not read as drift")
			} else if line < 1 {
				t.Errorf("line = %d, want the 1-based position", line)
			}
		})
	}
}

// TestFindReportsRealDrift: code that is genuinely gone must be reported, which
// is the whole point.
func TestFindReportsRealDrift(t *testing.T) {
	src := readSourceFrom("func pin() {\n\tvar env []string\n}\n")
	if _, ok := src.find("env := []string{configEnv + \"=\" + dir}"); ok {
		t.Error("removed code reported as still present")
	}
}

// TestFindLocatesTheCurrentLine: the line number is the ANSWER, never part of the
// question — an anchor holds no line, and verification reports where the code is
// now.
func TestFindLocatesTheCurrentLine(t *testing.T) {
	src := readSourceFrom("// a\n// b\n// c\nfunc target() {}\n")
	line, ok := src.find("func target() {}")
	if !ok || line != 4 {
		t.Errorf("line = %d, ok = %v; want line 4", line, ok)
	}

	// Insert two lines above it: the same anchor must still verify, at the new
	// position. Anchoring to "line 4" would have failed here — which is exactly
	// why anchors hold snippets.
	moved := readSourceFrom("// new\n// new\n// a\n// b\n// c\nfunc target() {}\n")
	if line, ok := moved.find("func target() {}"); !ok || line != 6 {
		t.Errorf("after inserting lines: line = %d, ok = %v; want line 6", line, ok)
	}
}

// TestMissingFileIsNotAnError: a deleted file is a verdict the report should
// carry, not a failure that stops the run.
func TestMissingFileIsNotAnError(t *testing.T) {
	src := readSource("/nonexistent/definitely/not/here.go")
	if src.exists {
		t.Fatal("a missing file must not report as existing")
	}
	if _, ok := src.find("anything"); ok {
		t.Error("a missing file cannot contain a snippet")
	}
}

// readSourceFrom builds a sourceFile from literal content, so the matcher is
// tested without touching disk.
func readSourceFrom(content string) *sourceFile {
	lines := strings.Split(content, "\n")
	norm := make([]string, len(lines))
	for i, l := range lines {
		norm[i] = anchorcontract.NormalizeSnippet(l)
	}
	return &sourceFile{exists: true, lines: lines, normalized: norm}
}

// TestCurrentRepoLabelPrefersTheRemote pins the rule anchors are labelled with,
// because the skip decision is only as good as the two labels agreeing.
func TestCurrentRepoLabelPrefersTheRemote(t *testing.T) {
	dir := t.TempDir()
	// No git remote: the label is UNKNOWN, not the directory name. Using the
	// folder name made every anchor from a named repository look foreign, so the
	// verifier reported success having checked nothing. See
	// TestCurrentRepoLabelIsEmptyWhenTheRepoIsUnknown.
	if got := currentRepoLabel(dir); got != "" {
		t.Errorf("without a remote the label must be empty (unknown), got %q", got)
	}
}

// TestAnchorsFromAnotherRepoAreNotMissing is the regression this behaviour
// exists for. A memory pinned to a file in a sibling repository used to report
// "file is gone" from every other checkout — and since the honest response to
// that is deleting the memory, the check destroyed what it was built to protect.
// A live session deleted three chunks that way.
func TestAnchorsFromAnotherRepoAreNotMissing(t *testing.T) {
	root := t.TempDir()
	here := currentRepoLabel(root)

	// Same shape as the loop in runVerify: a foreign label is skipped before the
	// file is ever looked for, so a path that does not exist here is not a
	// verdict about the memory.
	foreign := anchor{Path: "infra/docker/base/Dockerfile", Repo: "some-other-repo"}
	if foreign.Repo != "" && here != "" && !strings.EqualFold(foreign.Repo, here) {
		return // named tree, foreign label: skipped before the file is looked for
	}
	// Unknown tree (no remote): the label check cannot decide, so the file is
	// looked for — and when it is not found, runVerify must still refuse to call
	// it MISSING, because it cannot distinguish "deleted" from "lives elsewhere".
	// That second guard is what keeps the regression fixed now that an unknown
	// tree no longer skips everything.
	if here != "" {
		t.Fatalf("an anchor labelled %q must be skipped in a tree labelled %q, not reported missing", foreign.Repo, here)
	}
	src := readSource(filepath.Join(root, foreign.Path))
	if src.exists {
		t.Fatal("fixture: the foreign path should not exist in this temp tree")
	}
	if !(here == "" && foreign.Repo != "") {
		t.Fatal("the unknown-tree guard would not fire for this anchor")
	}
}

// TestCurrentRepoLabelIsEmptyWhenTheRepoIsUnknown pins the safety valve the doc
// comment describes and the code did not have.
//
// The comment on the skip says an empty result means "unknown", and that an
// unknown repository checks every anchor rather than skipping them — because a
// verifier that silently checked nothing would be worse than one that
// occasionally checks too much. That path was unreachable: the fallback was
// filepath.Base(root), which is non-empty for any real path.
//
// The failure it was supposed to prevent was therefore live. In a tree with no
// origin remote — a tarball, a vendored copy, a clone whose remote is named
// something else, a worktree in a differently-named directory — `here` became the
// DIRECTORY name, every anchor's Repo mismatched, and runVerify reported
// "N anchor(s): 0 verified, 0 drifted, 0 missing, N elsewhere". A clean-looking
// report from a verifier that checked nothing.
func TestCurrentRepoLabelIsEmptyWhenTheRepoIsUnknown(t *testing.T) {
	dir := t.TempDir()
	if got := currentRepoLabel(dir); got != "" {
		t.Errorf("currentRepoLabel on a non-git directory = %q, want \"\" — a non-empty label "+
			"makes every anchor from a named repository look like it belongs elsewhere, and the "+
			"verifier reports success while checking nothing", got)
	}
}

// TestCurrentRepoLabelReadsTheRemote is the other half: when there IS a remote,
// the label must come from it and not from the directory, or two clones of one
// repository in differently-named folders disagree about their own identity.
func TestCurrentRepoLabelReadsTheRemote(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:someone/expected-name.git"},
	} {
		cmd := testexec.Command(t, "git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable in this environment: %v (%s)", err, out)
		}
	}
	if got := currentRepoLabel(dir); got != "expected-name" {
		t.Errorf("currentRepoLabel = %q, want %q — the label must come from the remote, not the "+
			"directory, or the same repo cloned into two folders disagrees with itself", got, "expected-name")
	}
}

// TestUnknownTreeRecordsNoVerdictEndToEnd drives runVerify itself.
//
// The existing regression test recomputes a hand-copied duplicate of the guard's
// condition and asserts on that, so deleting the guard from verify.go left the
// suite green — a test of the code's shape rather than of the code. This one
// reads the report runVerify writes, which is the behaviour that matters: the
// defect it guards already deleted three memories once.
//
// Both destructive verdicts are covered, because both write a verdict that leads
// to a memory being removed. A file that is ABSENT here might live in the other
// repository; a file that is PRESENT here might be an unrelated file at the same
// path — README.md, main.go and go.mod collide across repositories constantly —
// so a snippet that fails to match is not evidence of staleness either.
func TestUnknownTreeRecordsNoVerdictEndToEnd(t *testing.T) {
	root := t.TempDir() // no git, so currentRepoLabel is "" — an unknown tree
	// A file that EXISTS here, whose content does not contain the anchor snippet.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("unrelated content\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	anchors := []anchor{
		{ID: "a1", Path: "missing/from/here.go", Snippet: "func Gone() {}", Repo: "some-other-repo", DrawerID: "d1"},
		{ID: "a2", Path: "README.md", Snippet: "a line that is not in this README", Repo: "some-other-repo", DrawerID: "d2"},
	}

	var buf strings.Builder
	verdicts, counts := verifyAnchors(root, anchors, &buf)
	report := buf.String()

	if len(verdicts) != 0 {
		t.Errorf("an unknown tree recorded %d verdict(s); it cannot tell this repository from the "+
			"one the anchors name, and a verdict here is what deletes a memory:\n%s", len(verdicts), report)
	}
	if counts.missing != 0 || counts.drifted != 0 {
		t.Errorf("missing=%d drifted=%d, want 0 and 0 — both are destructive verdicts and neither "+
			"is supportable without knowing which repository this is:\n%s", counts.missing, counts.drifted, report)
	}
	if counts.elsewhere != 2 {
		t.Errorf("elsewhere=%d, want 2 — the anchors must be accounted for, not silently dropped", counts.elsewhere)
	}
}

// gitTreeLabelled makes a temp tree that currentRepoLabel resolves to `label`,
// so a test can exercise the KNOWN-tree half of the rule. The unknown-tree half
// needs no git at all, which is why the older tests above do not have this.
func gitTreeLabelled(t *testing.T, label string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:someone/" + label + ".git"},
	} {
		if out, err := testexec.Command(t, "git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable in this environment: %v (%s)", err, out)
		}
	}
	if got := currentRepoLabel(dir); got != label {
		t.Fatalf("fixture: currentRepoLabel = %q, want %q", got, label)
	}
	return dir
}

// TestAnUnattributableAnchorIsNeverMissing is the KNOWN-tree half of "unknown is
// not absent", and it was the half nobody had.
//
// Every guard protecting an unknown from being read as an absence was conditioned
// on the anchor carrying a repo label. So an anchor with an EMPTY label, in a tree
// we CAN name, passed all of them and landed on MISSING — checked against whatever
// repository the session happened to be sitting in.
//
// Measured 2026-08-29: five sessions in five unrelated repositories were each told
// that seven of this project's Go and TypeScript files were "gone", from trees that
// have never contained a .go file. The verdicts are RECORDED, search then flags
// those memories STALE, and the session-start hook says "re-read the code and
// re-file whichever are wrong" — so a session that COMPLIES rewrites correct
// records on evidence from a repository that could not have produced it. One
// session supplied the decisive pair: the same file, in one working tree,
// verdict=missing with repo="" and verdict=verified with repo="agentsmemory".
//
// THE ASYMMETRY IS THE FIX, and it is the same one the unknown-tree branch already
// makes: a snippet that MATCHES is strong evidence wherever it is found, so it
// stays `verified`. A non-match is not evidence of anything without knowing which
// repository this is, so it records nothing.
func TestAnUnattributableAnchorIsNeverMissing(t *testing.T) {
	root := gitTreeLabelled(t, "some-known-tree")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("unrelated content\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	anchors := []anchor{
		// Absent here, and we cannot say whose it is.
		{ID: "a1", Path: "internal/palace/service.go", Snippet: "func (s *Service) Add(", Repo: "", DrawerID: "d1"},
		// PRESENT here at a path that collides across repositories, with a snippet
		// that does not match — the drift verdict, equally destructive.
		{ID: "a2", Path: "README.md", Snippet: "a line that is not in this README", Repo: "", DrawerID: "d2"},
	}

	var buf strings.Builder
	verdicts, counts := verifyAnchors(root, anchors, &buf)
	report := buf.String()

	if counts.missing != 0 || counts.drifted != 0 {
		t.Errorf("missing=%d drifted=%d, want 0 and 0. An anchor carrying no repo label cannot be "+
			"attributed to this tree, so neither verdict is supportable — and both are the "+
			"destructive reading of an unknown:\n%s", counts.missing, counts.drifted, report)
	}
	for _, v := range verdicts {
		if v.Status == statusMissing || v.Status == statusDrifted {
			t.Errorf("recorded a %q verdict for an unattributable anchor; a recorded verdict is "+
				"durable and is what flags the memory STALE:\n%s", v.Status, report)
		}
	}

	t.Run("a matching snippet is still verified", func(t *testing.T) {
		// The rule must not become "ignore unlabelled anchors". A match is strong
		// evidence wherever it is found: an unrelated file at the same path is
		// vanishingly unlikely to contain the same verbatim snippet.
		root := gitTreeLabelled(t, "some-known-tree")
		const snippet = "func VeryDistinctivelyNamedThing() error {"
		if err := os.WriteFile(filepath.Join(root, "main.go"),
			[]byte("package main\n\n"+snippet+"\n\treturn nil\n}\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		verdicts, counts := verifyAnchors(root,
			[]anchor{{ID: "a3", Path: "main.go", Snippet: snippet, Repo: "", DrawerID: "d3"}}, io.Discard)
		if counts.verified != 1 {
			t.Errorf("verified=%d, want 1 — an unlabelled anchor whose snippet is found is "+
				"confirmed, and refusing to confirm it would make the fix a silent no-op check",
				counts.verified)
		}
		if len(verdicts) != 1 || verdicts[0].Status != statusVerified {
			t.Errorf("verdicts=%+v, want one verified", verdicts)
		}
	})

	t.Run("the unattributable ones are reported, not silently dropped", func(t *testing.T) {
		// A cost paid silently is a cost nobody fixes. Labelling the anchors is
		// what restores drift detection for them, so the report has to say how
		// many are in this state and what to do about it.
		if !strings.Contains(report, "no repo label") {
			t.Errorf("the report does not tell the reader that anchors went unchecked for want "+
				"of a label, so the remedy is invisible:\n%s", report)
		}
	})
}
