package usage

import (
	"encoding/json"
	"testing"
)

// TestAnUnlimitedCapReportsNoRemainderRatherThanZero pins the one thing an agent
// reading am_status cannot work out for itself.
//
// The wire carried `monthly_cap: -1` with `remaining: 0` — reported by two
// sessions on 2026-08-31, dismissed once as a display quirk, and still
// reproducing on 2026-09-01 across a version bump (issue #153). Those two fields
// contradict each other: the cap says the plan is unlimited and the count beside
// it reads as exhausted. The conservative reading of "0 left" is to stop writing,
// so the misleading field costs exactly the sessions that are being careful.
//
// It asserts the JSON rather than the Go value because the ambiguity is a
// PROPERTY OF THE WIRE — Remaining()'s 0 is correct arithmetic for a cap that
// bounds nothing, and a test on the int would pass while the JSON went on lying.
func TestAnUnlimitedCapReportsNoRemainderRatherThanZero(t *testing.T) {
	for _, cap := range []int{-1, 0} {
		st := Status{Used: 478, Cap: cap, Allowed: true}
		raw, err := json.Marshal(map[string]any{"remaining": st.RemainingReported()})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if got := string(raw); got != `{"remaining":null}` {
			t.Errorf("cap %d marshalled as %s, want {\"remaining\":null} — a caller cannot tell "+
				"an unlimited plan from an exhausted one when the two fields disagree", cap, got)
		}
	}
}

// TestARealCapStillReportsItsRemainder is the other half: an absence must mean
// "no limit", never "we stopped counting". A guard that returned nil for every
// cap would satisfy the test above and make the field useless.
func TestARealCapStillReportsItsRemainder(t *testing.T) {
	st := Status{Used: 3, Cap: 10, Allowed: true}
	got := st.RemainingReported()
	if got == nil {
		t.Fatal("a cap of 10 with 3 used reports no remainder, so the field says nothing on the " +
			"only plans where it has something to say")
	}
	if *got != 7 {
		t.Errorf("remaining = %d, want 7", *got)
	}
	exhausted := Status{Used: 10, Cap: 10}
	if r := exhausted.RemainingReported(); r == nil || *r != 0 {
		t.Errorf("an EXHAUSTED cap must still report 0 rather than null: a null there would hide "+
			"the state this field exists to show; got %v", r)
	}
}
