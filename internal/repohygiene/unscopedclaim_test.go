package repohygiene

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// retiredUnscopedCall matches the claim that a recall made with no wing argument
// is UNSCOPED. It is false: the server reads an omitted wing as the
// registration's own default_wing, so such a call is scoped to whatever project
// that registration was created for (#432).
//
// It matches the retired CLAIM rather than the topic, and that distinction is
// what makes the gate survivable. "unscoped" is a correct and necessary word
// elsewhere in this tree — an unscoped checkpoint is another project's open
// thread, an unscoped anchor listing spans every repository, and an EMPTY
// default_wing really does make recall span every wing. A gate that forbade the
// true sentences alongside the false one is a gate somebody deletes.
var retiredUnscopedCall = regexp.MustCompile(`(?i)\b(?:one|a single) unscoped (?:call|search)\b`)

// TestNoDocOrHookClaimsTheNoWingRecallIsUnscoped fails when the retired claim
// comes back anywhere in the tree.
//
// It is a gate rather than a review note because the claim was written five
// times, in two hooks, one test's failure message and both shipped copies of the
// protocol, and a reviewer raised it three times on one pull request. Correcting
// one copy leaves the others indistinguishable from it — worse than before,
// because the tree then carries both a false sentence and an emphatic correction
// of it with nothing marking which is current, and a reader who greps finds both.
//
// The universe is the whole tracked corpus rather than a list of files, so a
// sixth copy written tomorrow joins the check on the same commit.
func TestNoDocOrHookClaimsTheNoWingRecallIsUnscoped(t *testing.T) {
	root := repoRoot(t)
	ignored := gitignoreMatcher(t, root)
	self := filepath.Join("internal", "repohygiene", "unscopedclaim_test.go")

	for _, path := range walk(t, root, ignored) {
		rel, _ := filepath.Rel(root, path)
		if rel == self {
			continue // this file necessarily contains the pattern it forbids
		}
		src, err := os.ReadFile(path)
		if err != nil || !looksTextual(src) {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			if retiredUnscopedCall.MatchString(line) {
				t.Errorf("%s claims a wingless recall is unscoped:\n  %s\n"+
					"  It is not. am_search reads an omitted wing as the REGISTRATION's "+
					"default_wing, so the call is scoped to whatever project that registration "+
					"was created for — which is the whole of #432. Say \"one call carrying no "+
					"wing argument\" and name where it lands.", rel, strings.TrimSpace(line))
			}
		}
	}

	// A corpus with zero offenders cannot exercise the branch that reports one, so
	// the falsifiability case drives the SAME regexp over text that is an offender
	// — and asserts both directions, because a matcher that caught the correction
	// along with the claim would make the gate unusable rather than strict.
	t.Run("the matcher catches the claim and spares the correction", func(t *testing.T) {
		for _, bad := range []string{
			"# Without a wing: one unscoped call, as before the record.",
			"the hook must make one unscoped search",
			"With none it makes a single unscoped search and no craft call at all",
		} {
			if !retiredUnscopedCall.MatchString(bad) {
				t.Errorf("the matcher missed a retired claim, so the gate would pass over it: %q", bad)
			}
		}
		for _, good := range []string{
			"one call carrying no wing argument, which the server scopes to default_wing",
			"an unscoped checkpoint is another project's open thread",
			"If default_wing is empty, every unscoped recall spans everything.",
			"ONE CALL CARRYING NO WING ARGUMENT — not an unscoped one, which is what this said until #432",
		} {
			if retiredUnscopedCall.MatchString(good) {
				t.Errorf("the matcher caught a sentence that is true and must keep being said: %q", good)
			}
		}
	})
}
