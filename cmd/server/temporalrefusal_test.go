package main

import (
	"strings"
	"testing"
)

// A REFUSAL THAT NAMES ONE CAUSE WHILE HOLDING THE NUMBERS FOR THREE SENDS THE
// OPERATOR TO REPAIR SOMETHING THAT IS FINE.
//
// `temporalRefusal`'s predecessor was a single fmt.Errorf saying "none has a
// dated older semantic neighbour … file corrections with dates". pairCandidates
// and verifiedPairs were incremented on every iteration of the loop above it and
// read only on the SUCCESS path, where they ride caseFileMeta — so on the one
// path that had to explain a failure, both were in scope and ignored.
//
// Measured 2026-09-06 against this project's own palace at `--n 40`: 5 drawers
// had no dated older neighbour and 35 did, and the judge declined all 35. The
// refusal blamed the dates. An operator following it would file dated
// corrections into a corpus that already had chronology, and the pair bar — the
// actual limiter — would be exactly where it was.
//
// That is issue #34's defect one function over: #332 fixed a value computed
// correctly and unreachable from its only consumer, and this is a value computed
// correctly and unread by the branch that needed it.
//
// The three causes are different work, which is why they are three messages and
// not one with numbers appended: no neighbour is a corpus without chronology, no
// CONFIRMED pair is the judge's bar against this corpus, and confirmed pairs with
// no question is a broken generator.
func TestTheTemporalRefusalNamesTheCauseItsCountersCanSee(t *testing.T) {
	t.Run("no neighbour blames the corpus", func(t *testing.T) {
		err := temporalRefusal(40, 0, 0)
		if err == nil {
			t.Fatal("no refusal for a sample that produced no case")
		}
		if !strings.Contains(err.Error(), "none has a dated older semantic neighbour") {
			t.Errorf("a corpus with no dated neighbours must still be told so: %v", err)
		}
	})

	// The case the real palace produces, and the one the old message got wrong.
	t.Run("neighbours the judge declined blames the pair bar, not the dates", func(t *testing.T) {
		err := temporalRefusal(40, 35, 0)
		if err == nil {
			t.Fatal("no refusal for a sample that produced no case")
		}
		got := err.Error()
		if !strings.Contains(got, "35") {
			t.Errorf("the refusal does not report how many pairs were offered to the judge, which "+
				"is the number that distinguishes this from an undated corpus: %v", err)
		}
		if !strings.Contains(got, "--pair-max-distance") {
			t.Errorf("the refusal does not name the knob that governs what reaches the judge, so it "+
				"tells the operator a cause without an action: %v", err)
		}
		// ⚠ THE REGRESSION IS THE ADVICE, NOT THE WORDING. The old message's
		// remedy was "file corrections with dates", and repeating it here would
		// cost a corpus-wide fix for a judge threshold.
		if strings.Contains(got, "file corrections with dates") {
			t.Errorf("the refusal still sends the operator to file dated corrections into a corpus "+
				"that demonstrably has dated neighbours — the exact misdirection this splits: %v", err)
		}
	})

	t.Run("confirmed pairs with no question blames the generator", func(t *testing.T) {
		err := temporalRefusal(40, 35, 4)
		if err == nil {
			t.Fatal("no refusal for a sample that produced no case")
		}
		got := err.Error()
		if !strings.Contains(got, "--gen-model") {
			t.Errorf("pairs were confirmed and no question came back, which is a generator failure; "+
				"the refusal must name the generator rather than the corpus: %v", err)
		}
		if strings.Contains(got, "none has a dated older semantic neighbour") {
			t.Errorf("the refusal reports no neighbours over a run that confirmed 4 pairs: %v", err)
		}
	})

	// ⚠ THE THREE MUST BE DISTINGUISHABLE BY A READER, not merely produced by
	// different branches. Three branches returning the same sentence would pass
	// every assertion above that is phrased as "contains", because each string it
	// looks for could sit in one shared paragraph.
	t.Run("the three causes read differently", func(t *testing.T) {
		seen := map[string]bool{}
		for _, in := range [][3]int{{40, 0, 0}, {40, 35, 0}, {40, 35, 4}} {
			seen[temporalRefusal(in[0], in[1], in[2]).Error()] = true
		}
		if len(seen) != 3 {
			t.Errorf("the three causes produced %d distinct message(s); a refusal an operator "+
				"cannot tell apart is the single message this replaced", len(seen))
		}
	})
}
