package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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
	if distant.EverInSession != distant.Assertions || proximate.EverInSession != proximate.Assertions {
		t.Fatalf("the premise no longer holds — v2's latched reading is meant to score BOTH "+
			"at 100%%, which is the defect it exists to show: distant %d/%d, proximate %d/%d",
			distant.EverInSession, distant.Assertions, proximate.EverInSession, proximate.Assertions)
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

// TestABoundaryIsWhereNewWorkStarts covers the two definitions of "preceded" that
// are not a tool-call count: a recall since the last user turn, and one since the
// last compaction. Both are candidates only — nothing here decides which wins.
func TestABoundaryIsWhereNewWorkStarts(t *testing.T) {
	// ⚠ THE REPORTED RATE MUST *BE* THE CHOSEN DEFINITION, not merely be stamped v3.
	// Reverting `Preceded` to v2's latch while the stamp still read v3 passed the whole
	// suite — the version pin ties the stamp to the record, and nothing tied the field
	// to its own meaning. That is the same defect one layer over: a name asserting a
	// property nothing drives.
	sameAsChosenDefinition := func(t *testing.T, name string, o Observation) {
		t.Helper()
		if o.Preceded != o.PrecededSinceUserMessage {
			t.Errorf("%s: preceded_by_recall = %d but preceded_since_user_message = %d. "+
				"v3 defines the reported rate AS the user-turn reading; a divergence means "+
				"the published number is measuring something the record does not describe.",
				name, o.Preceded, o.PrecededSinceUserMessage)
		}
	}

	obs := func(name string) Observation {
		t.Helper()
		o, ok := Observe(filepath.Join(fixtures, name))
		if !ok {
			t.Fatalf("%s unreadable", name)
		}
		if o.Assertions != 1 {
			t.Fatalf("%s holds %d assertions, want 1 — the fixture stopped demonstrating "+
				"anything and every assertion below would be vacuous", name, o.Assertions)
		}
		sameAsChosenDefinition(t, name, o)
		return o
	}

	t.Run("a recall after the user turn counts", func(t *testing.T) {
		if o := obs("recall-after-user-turn.jsonl"); o.PrecededSinceUserMessage != 1 {
			t.Errorf("preceded_since_user_message = %d, want 1 — the agent was asked for "+
				"something and then asked the palace about it", o.PrecededSinceUserMessage)
		}
	})

	t.Run("a recall before the user turn does not", func(t *testing.T) {
		o := obs("recall-before-user-turn.jsonl")
		if o.PrecededSinceUserMessage != 0 {
			t.Errorf("preceded_since_user_message = %d, want 0 — the recall happened before "+
				"this work was asked for, which is the whole distinction", o.PrecededSinceUserMessage)
		}
		// v2's latched number still scores this session, and the divergence between the
		// two fields is the finding: `recall_anywhere_earlier` says the palace was
		// touched, `preceded_by_recall` says not for THIS work.
		if o.EverInSession != 1 {
			t.Errorf("recall_anywhere_earlier = %d, want 1 — v2's reading scores this "+
				"session, and the gap to preceded_by_recall is what v3 exists to show",
				o.EverInSession)
		}
	})

	// ⚠ THE CANARY, AND IT ALREADY FIRED ONCE. Claude Code records every TOOL RESULT
	// as a `"type": "user"` line — 11,055 of 11,704 in one real transcript. Taking
	// those for user turns reset the boundary after nearly every tool call, so a
	// recall could almost never be after one, and the whole corpus measured a clean
	// 0.0%. A rate of exactly zero over a corpus that yields 52.8% by another reading
	// is an instrument fault until proven otherwise.
	t.Run("a tool result is not a user turn", func(t *testing.T) {
		if o := obs("tool-results-are-not-user-turns.jsonl"); o.PrecededSinceUserMessage != 1 {
			t.Errorf("preceded_since_user_message = %d, want 1. Two tool results sit between "+
				"the recall and the claim; if those count as user turns this reads 0 and the "+
				"metric silently reports that nobody ever recalls", o.PrecededSinceUserMessage)
		}
	})

	t.Run("a recall before a compaction does not survive it", func(t *testing.T) {
		o := obs("recall-before-compaction.jsonl")
		if o.PrecededSinceCompaction != 0 {
			t.Errorf("preceded_since_compaction = %d, want 0 — a fresh context inherits a task "+
				"queue and no palace, which is the failure ADR-041 was opened for",
				o.PrecededSinceCompaction)
		}
		if o.EverInSession != 1 {
			t.Errorf("recall_anywhere_earlier = %d, want 1 — v2's latch survives the "+
				"compaction it is meant to be measuring the cost of", o.EverInSession)
		}
	})
}

// TestAStringContentLineIsNotSkipped pins the parsing fix the boundary work needed.
//
// Content arrives as an array of blocks OR as a bare string on a plain user turn.
// Decoding straight into the array made every string-content line fail to unmarshal
// and be dropped — 600 of them in one real transcript, all genuine user turns, and
// the drop was silent because a malformed line is skipped by design (F-5).
func TestAStringContentLineIsNotSkipped(t *testing.T) {
	o, ok := Observe(filepath.Join(fixtures, "recall-after-user-turn.jsonl"))
	if !ok {
		t.Fatal("fixture unreadable")
	}
	// The user turn in that fixture carries a bare string. If it were skipped, the
	// boundary would never be set and the recall would count against a boundary of
	// -1 — passing for the wrong reason.
	if o.PrecededSinceUserMessage != 1 {
		t.Errorf("preceded_since_user_message = %d, want 1", o.PrecededSinceUserMessage)
	}
	if o.SessionID == "" {
		t.Error("no session id: the fixture's lines are not being parsed at all")
	}
}

// baselineRecordPath is where the recorded baseline lives.
const baselineRecordPath = "../../docs/adr/BACKLOG.md"

// recordedBaselineVersion reads the classifier version the recorded baseline was
// taken under.
var recordedBaselineVersion = regexp.MustCompile(`(?m)^\| classifier \| (v\d+) \|`)

// TestTheRecordedBaselineNamesTheVersionTheCodeStamps is the pin the version stamp
// did not have.
//
// ⚠ F-16 GUARDS THE NUMERATOR'S CLASSIFIER AND NOTHING GUARDED ITS DEFINITION. That
// is the gap this test closes, and it is not hypothetical: `preceded_by_recall`
// meant "this session touched the palace at some earlier point" — a latch with no
// reset that nobody chose — and it could have been redefined under an unchanged v2
// stamp, leaving every rate before and after looking comparable. Over one corpus the
// two readings are 52.8% and 7.6%.
//
// So the stamp now covers the counting rule, both halves, and this fails when the
// code's version and the recorded baseline's disagree. A redefinition then has to
// re-take the baseline — which is F-16's actual requirement — rather than being a
// constant nobody remembered to change.
func TestTheRecordedBaselineNamesTheVersionTheCodeStamps(t *testing.T) {
	record, err := os.ReadFile(baselineRecordPath)
	if err != nil {
		t.Fatalf("read %s: %v", baselineRecordPath, err)
	}
	m := recordedBaselineVersion.FindSubmatch(record)
	if m == nil {
		t.Fatalf("%s records no baseline classifier version. The row `| classifier | vN |` is "+
			"what ties a published rate to the rule that produced it; without it a reader "+
			"cannot tell whether two numbers are comparable", baselineRecordPath)
	}
	if got := string(m[1]); got != classifierVersion {
		t.Errorf("the code stamps %q and the recorded baseline was taken under %q.\n"+
			"  Rates under different counting rules are not comparable (F-16). Either the\n"+
			"  baseline must be re-taken under %s and re-recorded, or this version bump was\n"+
			"  not one — a redefinition of what `preceded` means is exactly what the stamp\n"+
			"  exists to make visible.", classifierVersion, got, classifierVersion)
	}
}

// TestAnObservationCanBePlacedInAMeasurementWindow pins the field the whole
// before/after design rests on.
//
// ⚠ THE STORE COULD NOT SEPARATE ITS OWN WINDOWS. ADR-041's design is: record a
// baseline, ship exactly one mechanism (F-9), measure the delta whichever way it
// falls (F-10). Every observation was undated, so a store holding both windows can
// answer "the rate over everything ever recorded" and nothing else — the delta F-10
// requires is not computable from it. Found 2026-08-28 at the moment T6 shipped and
// the first window opened, by asking how the after-measurement would know which
// rows were after.
func TestAnObservationCanBePlacedInAMeasurementWindow(t *testing.T) {
	o, ok := Observe(filepath.Join(fixtures, "recalled.jsonl"))
	if !ok {
		t.Fatal("fixture unreadable")
	}
	if o.ObservedAt == "" {
		t.Fatal("the observation carries no time, so it cannot be placed before or after any " +
			"mechanism: a store of undated rows makes every measurement window the same window")
	}
	ts, err := time.Parse(time.RFC3339, o.ObservedAt)
	if err != nil {
		t.Fatalf("observed_at %q is not RFC3339: %v — a timestamp nothing can parse is a "+
			"string, and the window boundary has to be comparable", o.ObservedAt, err)
	}
	// A clock read from the wrong place is worse than none: it would silently sort
	// every observation into one window.
	if d := time.Since(ts); d < 0 || d > time.Hour {
		t.Errorf("observed_at is %s away from now (%s); it is meant to be the moment the "+
			"observation was taken", d, o.ObservedAt)
	}

	// Round-trips through the store, because that is where a window is read from.
	store := filepath.Join(t.TempDir(), "obs.jsonl")
	if err := AppendObservation(store, o); err != nil {
		t.Fatalf("append: %v", err)
	}
	body, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"observed_at"`) {
		t.Errorf("the stored row carries no observed_at key:\n  %s", body)
	}
}
