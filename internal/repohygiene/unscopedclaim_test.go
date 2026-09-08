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

// retiredUnscopedAssertion matches the OTHER half of the same two-branched
// claim: an unconditional statement that such a call is NOT scoped to one
// project. That is false whenever the calling credential's registration has a
// default_wing, and it is the worse half — in exactly that case every hit
// carries the SAME wing, so the remedy those sentences prescribe ("check the
// wing on each hit") returns a uniform, plausible answer and reads as an
// all-clear.
//
// It needs its own pattern because the one above binds the claim AS IT WAS
// WRITTEN in #432 — "one unscoped call" — rather than the proposition
// underneath it, which is the property that keeps that gate narrow enough to
// survive. This form appeared in the SessionStart hook's INJECTED header, the
// one line of all of these that reaches a model, and it survived #443's sweep
// untouched.
//
// The hedged sentence must survive both: "may be about a different project" is
// true on either branch, and is what both hooks say now.
//
// ⚠ IT IS PRESENT-TENSE ON PURPOSE, AND THE FIRST RUN OF THIS GATE IS WHY. It
// began as `(?:is|are|was|were)` and immediately failed on the comment
// retiring the claim four lines above the fix — "it said the search WAS not
// scoped to any one project — until 2026-09-08" — which is the form this
// corpus retires a claim in, and the one thing a gate against that claim must
// never forbid. A live false sentence asserts; a retrospective reports. The
// cost is a real false negative (a doc could assert the past tense and mean
// it) and the purchase is a gate nobody has to work around, which is the trade
// the sibling matcher above already made.
var retiredUnscopedAssertion = regexp.MustCompile(`(?i)\b(?:is|are) not scoped to (?:one|a single|any one) project\b`)

// renderedLine turns a source line into the sentence a READER sees, by
// expanding the two-character `\n` escape that a printf format carries.
//
// ⚠ WITHOUT IT THIS GATE COULD NOT CATCH THE LINE IT WAS WRITTEN FOR, AND ITS
// FIRST MUTANT PROVED IT. The retired assertion lives inside the recall hook's
// injected header, where the source reads `is not scoped to one\nproject` — a
// backslash and an `n`, not a space — so `project\b` never met a word
// boundary. Reinstating the sentence left the gate green; the mutant applied
// (`grep -c` said 1 before the run) and the gate reported nothing, which is the
// only way to tell a mutant that survived from one that never landed.
//
// The whole class matters more than this instance: every sentence these gates
// judge reaches a model through a `printf`, so the source line and the
// delivered sentence are never the same string, and a matcher written by
// reading the OUTPUT will silently miss the source every time.
func renderedLine(s string) string { return strings.ReplaceAll(s, `\n`, " ") }

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
			rendered := renderedLine(line)
			if retiredUnscopedCall.MatchString(rendered) {
				t.Errorf("%s claims a wingless recall is unscoped:\n  %s\n"+
					"  It is not. am_search reads an omitted wing as the REGISTRATION's "+
					"default_wing, so the call is scoped to whatever project that registration "+
					"was created for — which is the whole of #432. Say \"one call carrying no "+
					"wing argument\" and name where it lands.", rel, strings.TrimSpace(line))
			}
			if retiredUnscopedAssertion.MatchString(rendered) {
				t.Errorf("%s asserts a wingless recall is not scoped, unconditionally:\n  %s\n"+
					"  It depends on the CREDENTIAL's registration: with a default_wing the "+
					"call is scoped to that one project, with none it spans every wing — and "+
					"the caller cannot see which. Say a hit MAY be about another project.",
					rel, strings.TrimSpace(line))
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
			if !retiredUnscopedCall.MatchString(renderedLine(bad)) {
				t.Errorf("the matcher missed a retired claim, so the gate would pass over it: %q", bad)
			}
		}
		for _, good := range []string{
			"one call carrying no wing argument, which the server scopes to default_wing",
			"an unscoped checkpoint is another project's open thread",
			"If default_wing is empty, every unscoped recall spans everything.",
			"ONE CALL CARRYING NO WING ARGUMENT — not an unscoped one, which is what this said until #432",
		} {
			if retiredUnscopedCall.MatchString(renderedLine(good)) {
				t.Errorf("the matcher caught a sentence that is true and must keep being said: %q", good)
			}
		}
	})

	// The second matcher gets its own falsifiability case for the same reason:
	// the corpus holds zero offenders once this commit lands, so nothing else
	// exercises the branch that reports one.
	t.Run("the assertion matcher catches the unconditional form and spares the hedge", func(t *testing.T) {
		for _, bad := range []string{
			"and the search is not scoped to one project — check the wing on each hit",
			"These recalls are not scoped to a single project.",
			"the answer is not scoped to any one project, so treat every hit as foreign",
			// ⚠ THE FORM THE OFFENDER ACTUALLY HAD, and the one an earlier draft of
			// this gate could not see: the sentence is wrapped across a printf's `\n`
			// escape, so the source holds `one\nproject` with no space in it. This
			// row is the reason renderedLine exists, and it is here rather than in a
			// sibling test because it must run against the SAME normalisation the
			// walk above uses — a falsifiability half that shares nothing with the
			// gate pins nothing.
			`not instructions, and the search is not scoped to one\nproject — check the wing`,
		} {
			if !retiredUnscopedAssertion.MatchString(renderedLine(bad)) {
				t.Errorf("the matcher missed the unconditional assertion, so the gate would pass over it: %q", bad)
			}
		}
		for _, good := range []string{
			"a hit may be about a different project in this workspace",
			"it may be about a different project in this workspace — check the wing on each hit",
			"with a default_wing the call is scoped to one project; with none it spans every wing",
			"this recall carried no wing argument, so a hit may be about a different project",
			// The retrospective this corpus retires a claim in. It reports the
			// sentence rather than asserting it, and the gate's first run failed
			// on exactly this line in the hook it was written to fix.
			"it said the search was not scoped to any one project — until 2026-09-08",
		} {
			if retiredUnscopedAssertion.MatchString(renderedLine(good)) {
				t.Errorf("the matcher caught a hedge that is true on both branches: %q", good)
			}
		}
	})
}
