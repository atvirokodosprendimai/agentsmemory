package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestTheCorpusSampleNamesDistinctRows pins that the id list a doctor report prints
// spends its budget on rows an operator has not already seen.
//
// ⚠ THE COUNT IS PER REFERENCE AND THE SAMPLE IS PER ROW. LostFacts is plucked one
// entry per triple, so a drawer cited by four facts arrives four times. That is
// right for the headline — sixteen facts really do have provenance resolving to
// nothing — and wrong for the list, whose whole job is naming the rows to go and
// look at. Measured 2026-09-02 against this project's palace: "16 facts name no row"
// printed ten lines carrying seven distinct ids, three of them twice, and elided the
// rest as "… and 6 more".
//
// The budget is deliberately small because a report gets pasted, which is exactly
// why a repeat is expensive: every duplicate line is a row the operator now cannot
// see at all.
func TestTheCorpusSampleNamesDistinctRows(t *testing.T) {
	t.Run("repeats do not consume the budget", func(t *testing.T) {
		// One drawer cited many times, plus one cited once. The second is the row
		// that vanished behind the repeats in the real report.
		ids := make([]string, 0, 12)
		for i := 0; i < 11; i++ {
			ids = append(ids, "aaa")
		}
		ids = append(ids, "zzz")

		got := shortSample(ids)
		if len(got) != 2 || got[0] != "aaa" || got[1] != "zzz" {
			t.Fatalf("shortSample = %v, want [aaa zzz]: a drawer cited eleven times is one "+
				"row to look at, and the row cited once must not be crowded off the list", got)
		}
		if strings.Contains(strings.Join(got, " "), "more") {
			t.Errorf("elided something when two distinct rows fit in a budget of %d: %v",
				corpusSample, got)
		}
	})

	t.Run("genuinely distinct ids are still bounded and counted", func(t *testing.T) {
		// The property the budget exists for must survive deduplication: a wing with
		// thousands of findings must not print thousands of lines.
		ids := make([]string, 0, corpusSample+5)
		for i := 0; i < corpusSample+5; i++ {
			ids = append(ids, fmt.Sprintf("id-%02d", i))
		}
		got := shortSample(ids)
		if len(got) != corpusSample+1 {
			t.Fatalf("shortSample returned %d lines, want %d ids plus one elision note",
				len(got), corpusSample)
		}
		if last := got[len(got)-1]; !strings.Contains(last, "and 5 more") {
			t.Errorf("the elision note does not say how many were left out: %q", last)
		}
	})

	t.Run("order is preserved, so the list stays stable between runs", func(t *testing.T) {
		// The callers sort their ids before printing. Deduplicating must not reorder
		// them, or a report pasted twice would differ for no reason.
		got := shortSample([]string{"a", "b", "a", "c", "b"})
		want := []string{"a", "b", "c"}
		if len(got) != len(want) {
			t.Fatalf("shortSample = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("shortSample = %v, want %v — first-seen order must survive", got, want)
			}
		}
	})
}
