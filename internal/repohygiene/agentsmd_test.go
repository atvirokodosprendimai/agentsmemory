package repohygiene

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// agentsMdTestRef matches a Go test name cited anywhere in AGENTS.md.
var agentsMdTestRef = regexp.MustCompile(`\bTest[A-Za-z0-9_]+\b`)

// TestAgentsMdNamesGatesThatExist fails when AGENTS.md cites a test that is not in
// the tree.
//
// AGENTS.md's "Reachability" section is the closest thing this repo has to a
// statement of its own worst habit, and it works by NAMING the gates that enforce
// each rule. That makes the list a factual claim about the tree — and it was an
// unguarded one, in the section whose closing line is "anything that must stay true
// gets a command whose exit code says so". The file exempted itself from its own
// rule.
//
// It had already rotted. ADR-006 (Accepted) shipped TestReadEnvVarsAreDocumented,
// TestNotOperatorFacingIsJustified, TestEveryKnobIsSweptOrNamed and
// TestDiscoveredPairsAdmitTheirCondition — all four of its tasks done — while
// AGENTS.md went on describing the pre-ADR-006 world whose insufficiency is that
// ADR's entire Context section. A read-only session asked what this repo requires
// before a config option counts as done, answered from AGENTS.md, and got a list
// missing the mode-scoping gate entirely: a knob read but inert under the running
// configuration passes every test the file named and fails the ones it did not.
//
// This checks the direction that can be checked mechanically and cheaply: every
// name the file cites must resolve. A rename or deletion that leaves the prose
// behind now goes red on the same commit.
//
// It deliberately does NOT check the reverse — that every gate in the tree is cited
// here — because "which tests are gates" is a judgement no regex should make, and a
// test that guesses would either nag on ordinary tests or be silently satisfied by
// an allowlist somebody stopped maintaining. The reverse direction is a review's
// job, and ADR-006 is the precedent for how it gets noticed: by writing the ADR.
func TestAgentsMdNamesGatesThatExist(t *testing.T) {
	root := repoRoot(t)

	agentsMd, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	cited := map[string]bool{}
	for _, name := range agentsMdTestRef.FindAllString(string(agentsMd), -1) {
		cited[name] = true
	}
	if len(cited) == 0 {
		t.Fatal("AGENTS.md cites no test by name — the reachability section names the gates " +
			"that enforce each of its rules, so zero means either the section was gutted or " +
			"this matcher stopped matching. Both are worth a look before deleting this test.")
	}

	declared := declaredTestNames(t, root)

	missing := make([]string, 0, len(cited))
	for name := range cited {
		if !declared[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)

	for _, name := range missing {
		t.Errorf("AGENTS.md names %s, which no *_test.go in this tree declares.\n"+
			"  Either the test was renamed or removed and the prose was left behind, or the\n"+
			"  name is a typo. Both make the section a claim about gates that are not there —\n"+
			"  in the section that says anything which must stay true gets a command whose\n"+
			"  exit code says so. Fix the file, or fix the test name, but do not delete this\n"+
			"  check: the list rotting silently is exactly what it exists to catch.", name)
	}
}

// declaredTestNames returns every Go test function declared under root, found with
// git grep so the walk respects .gitignore and never descends into build output or
// vendored trees.
func declaredTestNames(t *testing.T, root string) map[string]bool {
	t.Helper()

	// --untracked matters and is not a nicety: without it this searches only what
	// git already tracks, so a gate test added in the same change as the AGENTS.md
	// line naming it fails until someone stages the file. It did exactly that on
	// first run, reporting "AGENTS.md names a test nobody declares" about THIS test
	// — a precondition of the harness, announced in the vocabulary of the thing
	// under check. .gitignore is still respected, so build output stays out.
	cmd := exec.Command("git", "grep", "--untracked", "-h", "-o", "-E", `^func (Test[A-Za-z0-9_]+)\(`, "--", "*_test.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git grep for test declarations: %v", err)
	}

	names := map[string]bool{}
	for line := range strings.SplitSeq(string(out), "\n") {
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "func "), "(")
		if name != "" {
			names[name] = true
		}
	}
	if len(names) == 0 {
		t.Fatal("git grep found no test declarations at all, so this check would pass vacuously")
	}
	return names
}
