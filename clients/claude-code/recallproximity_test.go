package main

import (
	"path/filepath"
	"testing"
)

// TestPrecededCannotSeeProximityAndTheObservationCan is the check ADR-041's
// instrument was missing, and it is written as the pair of sessions that motivated
// it rather than as an assertion about one.
//
// ⚠ THE POINT IS THAT BOTH SESSIONS SCORE 100% ON `preceded`. `Observe` latches at
// the first recall and never resets, so a session that searched once and then made
// every claim forty calls later is indistinguishable from one that recalled before
// each claim. Measured 2026-08-28 on the session that BUILT the instrument: 109
// assertions, 109 preceded, the latching call at tool_use #172 of 8,277. A perfect
// rate against a 27.6% baseline, and an artifact.
//
// That matters because T4, T5 and T6 all aim to make recall PROXIMATE and RELEVANT,
// and a number that saturates on the first call moves by zero for every one of
// them. An instrument insensitive to the improvement it exists to measure will
// report each mechanism as no effect, and F-10 will faithfully record the null.
//
// ⚠ IT ASSERTS NO WINDOW. Which distance counts as "preceded" is a spec decision
// that changes what the rate means, and the honest order is to see the distribution
// first. This test pins that the DATA distinguishing them exists — not which
// bucket wins.
func TestPrecededCannotSeeProximityAndTheObservationCan(t *testing.T) {
	distant, ok := Observe(filepath.Join(fixtures, "distant-recall.jsonl"))
	if !ok {
		t.Fatal("distant-recall.jsonl unreadable")
	}
	proximate, ok := Observe(filepath.Join(fixtures, "proximate-recall.jsonl"))
	if !ok {
		t.Fatal("proximate-recall.jsonl unreadable")
	}

	// The premise. If this ever stops holding, the fixtures no longer demonstrate
	// the defect and everything below is asserting something else.
	if distant.Assertions == 0 || proximate.Assertions == 0 {
		t.Fatal("a fixture holds no assertions, so this test is vacuous: the classifier " +
			"stopped matching the fixture sentences and nothing else here means anything")
	}
	if distant.Preceded != distant.Assertions || proximate.Preceded != proximate.Assertions {
		t.Fatalf("the premise no longer holds — `preceded` is meant to score BOTH at 100%%, "+
			"which is the defect: distant %d/%d, proximate %d/%d",
			distant.Preceded, distant.Assertions, proximate.Preceded, proximate.Assertions)
	}

	// One recall then a wall of claims, versus a recall before each claim. `preceded`
	// says they are identical; the session that only searched once is not the session
	// that searched five times.
	if distant.Recalls >= proximate.Recalls {
		t.Errorf("recalls: distant %d, proximate %d — the count of recalls does not separate "+
			"a session that asked once from one that asked before every claim",
			distant.Recalls, proximate.Recalls)
	}

	// The field that actually answers ADR-041's question. At the tightest window the
	// distant session must score nothing and the proximate one everything.
	tight := windowKey(recallWindows[0])
	if distant.PrecededWithin[tight] != 0 {
		t.Errorf("distant session scored %d within %s calls; its only recall is far behind "+
			"every claim, so a proximity window must credit it with none",
			distant.PrecededWithin[tight], tight)
	}
	if proximate.PrecededWithin[tight] != proximate.Assertions {
		t.Errorf("proximate session scored %d of %d within %s calls; each claim is immediately "+
			"preceded by a recall, so the tightest window must credit all of them",
			proximate.PrecededWithin[tight], proximate.Assertions, tight)
	}

	// Cumulative, so a reader can see where the rate falls off rather than reading one
	// number. A non-monotonic set of buckets would be a bug in the bucketing, not a
	// finding about the session.
	for _, o := range []Observation{distant, proximate} {
		prev := 0
		for _, w := range recallWindows {
			got := o.PrecededWithin[windowKey(w)]
			if got < prev {
				t.Errorf("session %s: window %d credits %d, narrower window credited %d — the "+
					"buckets are cumulative, so they can never decrease as the window widens",
					o.SessionID, w, got, prev)
			}
			prev = got
		}
		if prev > o.Assertions {
			t.Errorf("session %s: widest window credits %d of %d assertions",
				o.SessionID, prev, o.Assertions)
		}
	}
}

// TestAnAssertionBeforeAnyRecallIsCountedAsSuch pins the one miss `preceded` can
// still see, so the additive fields do not quietly become the only honest ones
// while the old numerator stops being checked at all.
func TestAnAssertionBeforeAnyRecallIsCountedAsSuch(t *testing.T) {
	o, ok := Observe(filepath.Join(fixtures, "unrecalled.jsonl"))
	if !ok {
		t.Fatal("unrecalled.jsonl unreadable")
	}
	if o.BeforeFirstRecall != o.Assertions {
		t.Errorf("assertions_before_first_recall = %d, want %d — every claim in this fixture "+
			"is made with no recall anywhere before it", o.BeforeFirstRecall, o.Assertions)
	}
	if o.Recalls != 0 {
		t.Errorf("recalls = %d in a fixture with no recall call", o.Recalls)
	}
	if len(o.PrecededWithin) != 0 {
		t.Errorf("a session with no recall credited %v to a proximity window", o.PrecededWithin)
	}
}
