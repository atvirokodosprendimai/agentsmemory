package palace

import (
	"math"
	"strings"
	"testing"
)

// TestBootstrapSeparatesSignalFromNoise: a large real difference must exclude
// zero, and pure noise must not — otherwise the intervals are decoration.
func TestBootstrapSeparatesSignalFromNoise(t *testing.T) {
	// Arm A finds everything at rank 1; arm B misses half outright.
	a := []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	b := []int{1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0}
	if d := PairedDelta(a, b); d.Contains(0) {
		t.Errorf("a decisive difference produced an interval containing zero: %v", d)
	}

	// Identical arms: the delta must be exactly zero-width around zero.
	if d := PairedDelta(a, a); !d.Contains(0) || d.Lo != 0 || d.Hi != 0 {
		t.Errorf("identical arms produced a nonzero delta: %v", d)
	}

	// One flipped case out of twelve is the kind of gap this repo previously
	// ranked arms on. The interval must refuse to.
	c := []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2}
	if d := PairedDelta(a, c); !d.Contains(0) {
		t.Errorf("a one-case gap at n=12 excluded zero: %v — this is the over-reading the stats exist to stop", d)
	}
}

// TestBootstrapIsReproducible: same inputs, same interval — a report that
// changes between runs of identical cases reads as broken.
func TestBootstrapIsReproducible(t *testing.T) {
	ranks := []int{1, 2, 0, 3, 1, 1, 0, 2, 1, 5, 1, 1}
	first, second := BootstrapMRR(ranks), BootstrapMRR(ranks)
	if first != second {
		t.Errorf("two runs over identical ranks: %v then %v", first, second)
	}
	if first.Lo >= first.Hi {
		t.Errorf("degenerate interval %v for varied ranks", first)
	}
}

// TestEvaluateFailsLoudOnStaleGold pins the adversarial-review finding: a case
// whose drawer was purged by a re-mine must stop the run and say why, not score
// as an all-arm retrieval miss that the pool diagnosis then misattributes.
func TestEvaluateFailsLoudOnStaleGold(t *testing.T) {
	svc := newTestService(t)
	const team = "team-stale"
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a real memory so the corpus is not empty"})

	_, err := svc.Evaluate(t.Context(), team,
		[]EvalCase{{Query: "anything", Expect: "purged-drawer-id-that-no-longer-exists"}}, 10, nil)
	if err == nil {
		t.Fatal("a stale gold id must fail the run, not silently score as a miss")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("the error must name the cause: %v", err)
	}
}

// TestClosetDeltaExcludesUnreachableAndAbsentCases pins the admission rules, and
// pins that every exclusion is counted rather than quietly dropped.
//
// The comparison this ADR is decided on has to be preselected — hybrid+closet
// against hybrid, named before the run — because every "vs best" verdict the
// table prints picks its own baseline from the same data it is judging. Two
// kinds of case cannot contribute: one whose gold never entered the pool, since
// no arm could have ranked it and the delta would be zero for a retrieval reason
// rather than a ranking one; and an absent case, which has no gold at all.
func TestClosetDeltaExcludesUnreachableAndAbsentCases(t *testing.T) {
	report := EvalReport{Details: []EvalCaseResult{
		{Query: "reachable, closet wins", Category: CatSingle, PoolRank: 3,
			Ranks: map[EvalArm]int{ArmHybrid: 4, ArmHybridCloset: 1}},
		{Query: "reachable, closet loses", Category: CatSingle, PoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 1, ArmHybridCloset: 3}},
		{Query: "unreachable — gold never made the pool", Category: CatSingle, PoolRank: 0,
			Ranks: map[EvalArm]int{ArmHybrid: 0, ArmHybridCloset: 0}},
		// ⚠ PoolRank 3, NOT 0, and both arms carry a rank. An absent case with
		// PoolRank 0 is excluded by the UNREACHABLE check before the absent guard
		// is reached, so it cannot distinguish the two rules — that is exactly why
		// the mutant survived the first attempt at this test. This case is
		// retrievable and ranked, so the ONLY thing that can exclude it is its
		// category.
		{Query: "absent — retrieved and ranked, but there is no gold to be right about",
			Category: CatAbsent, PoolRank: 3,
			Ranks: map[EvalArm]int{ArmHybrid: 2, ArmHybridCloset: 1}},
	}}

	cell := ClosetDelta(report, CatSingle)

	if cell.Admitted != 2 {
		t.Errorf("admitted %d cases, want the 2 reachable single-hop ones", cell.Admitted)
	}
	if cell.Unreachable != 1 {
		t.Errorf("counted %d unreachable, want 1 — an exclusion nobody can see is an exclusion nobody can check", cell.Unreachable)
	}
	if cell.Moved != 2 {
		t.Errorf("counted %d moved, want 2: both admitted cases were ranked differently by the two arms", cell.Moved)
	}
	// Δ = closet minus no-closet. Case one: 1/1 − 1/4 = +0.75. Case two:
	// 1/3 − 1/1 = −0.667. Mean = +0.0417.
	if math.Abs(cell.DeltaMRR-0.0416666) > 1e-4 {
		t.Errorf("ΔMRR = %.6f, want ≈ +0.041667 (closet minus no-closet over the two admitted cases)", cell.DeltaMRR)
	}

	// ⚠ THE ABSENT HALF OF THE NAME WAS UNDRIVEN UNTIL THIS SUBTEST. Everything
	// above asks for CatSingle, and ClosetDelta's first check is
	// `if d.Category != category { continue }` — so the absent fixture is filtered
	// on CATEGORY before the `if category == CatAbsent` guard can run. Deleting
	// that guard changed nothing any assertion above observes, and the mutant
	// SURVIVED. A test whose name claims two exclusions and drives one is the
	// defect this repository keeps finding in its own gates.
	//
	// Asking for CatAbsent is the only call that reaches the guard.
	t.Run("asked for the absent category, the delta is undefined rather than zero", func(t *testing.T) {
		absent := ClosetDelta(report, CatAbsent)
		if absent.Admitted != 0 {
			t.Errorf("admitted %d absent cases, want 0 — an absent question has no gold to rank, "+
				"so its delta is UNDEFINED, and admitting it would average a zero that means "+
				"'nothing to measure' together with zeros that mean 'the arms agreed'", absent.Admitted)
		}
		if absent.DeltaMRR != 0 {
			t.Errorf("ΔMRR = %v over absent cases, want exactly 0 from an empty population", absent.DeltaMRR)
		}
	})
}

// TestClosetDeltaIsScopedToOneCategory pins that the statistic never pools
// categories. A paraphrase question and a real recorded query are different
// populations, and a delta averaged over both describes neither.
func TestClosetDeltaIsScopedToOneCategory(t *testing.T) {
	report := EvalReport{Details: []EvalCaseResult{
		{Query: "single", Category: CatSingle, PoolRank: 1,
			Ranks: map[EvalArm]int{ArmHybrid: 2, ArmHybridCloset: 1}},
		{Query: "real one", Category: CatReal, PoolRank: 1,
			Ranks: map[EvalArm]int{ArmHybrid: 1, ArmHybridCloset: 5}},
		{Query: "real two", Category: CatReal, PoolRank: 1,
			Ranks: map[EvalArm]int{ArmHybrid: 1, ArmHybridCloset: 5}},
	}}

	single := ClosetDelta(report, CatSingle)
	real := ClosetDelta(report, CatReal)

	if single.Admitted != 1 || real.Admitted != 2 {
		t.Fatalf("admitted single=%d real=%d, want 1 and 2 — the categories are leaking into each other", single.Admitted, real.Admitted)
	}
	if !(single.DeltaMRR > 0) {
		t.Errorf("single-hop ΔMRR = %.4f, want positive; the closet arm ranked that case better", single.DeltaMRR)
	}
	if !(real.DeltaMRR < 0) {
		t.Errorf("real ΔMRR = %.4f, want negative; the closet arm ranked both those cases worse", real.DeltaMRR)
	}
}

// TestClosetDeltaCountsCasesNeitherArmScored pins the third exclusion: a case
// present in the category and reachable, but which one of the two arms never
// scored, cannot be paired and is reported rather than dropped.
func TestClosetDeltaCountsCasesNeitherArmScored(t *testing.T) {
	report := EvalReport{Details: []EvalCaseResult{
		{Query: "only one arm ran", Category: CatSingle, PoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 3}},
		{Query: "both ran", Category: CatSingle, PoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 3, ArmHybridCloset: 2}},
	}}

	cell := ClosetDelta(report, CatSingle)

	if cell.Admitted != 1 {
		t.Errorf("admitted %d, want 1 — a case only one arm scored cannot be paired", cell.Admitted)
	}
	if cell.NoGold != 1 {
		t.Errorf("counted %d unpairable, want 1", cell.NoGold)
	}
}

// TestStaleAboveRateExcludesVacuous pins the denominator.
//
// A case is vacuous when the superseded version never entered the pool at all:
// no arm could have ranked it above anything, so counting it as a success would
// credit every arm for a retrieval accident. Vacuity is a property of the CASE —
// two arms may order a distractor differently, but they cannot disagree about
// whether it was retrievable — which is why it is read from DistractorPoolRank
// and not from each arm's own zero.
func TestStaleAboveRateExcludesVacuous(t *testing.T) {
	cases := []EvalCaseResult{
		// stale above current: distractor at 1, gold at 3 → counts, and is a hit
		{Category: CatTemporal, PoolRank: 3, DistractorPoolRank: 1,
			Ranks: map[EvalArm]int{ArmHybrid: 3}, DistractorRanks: map[EvalArm]int{ArmHybrid: 1}},
		// current above stale → counts, not a hit
		{Category: CatTemporal, PoolRank: 1, DistractorPoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 1}, DistractorRanks: map[EvalArm]int{ArmHybrid: 2}},
		// vacuous: the superseded version never made the pool
		{Category: CatTemporal, PoolRank: 1, DistractorPoolRank: 0,
			Ranks: map[EvalArm]int{ArmHybrid: 1}, DistractorRanks: map[EvalArm]int{ArmHybrid: 0}},
		// NOT vacuous, though this arm ranked it 0: the superseded version was in
		// the pool and simply did not make this arm's page. Only a page-scoped
		// arm can produce this, and it is the case that separates "read vacuity
		// from the case" from "read it from the arm" — the two agree everywhere
		// else, so without this row the distinction is untested and reading the
		// arm's own zero silently drops a case the arm should be answerable for.
		{Category: CatTemporal, PoolRank: 1, DistractorPoolRank: 7,
			Ranks: map[EvalArm]int{ArmHybrid: 1}, DistractorRanks: map[EvalArm]int{ArmHybrid: 0}},
	}

	got := StaleAboveRate(cases, ArmHybrid)
	if got.Vacuous != 1 {
		t.Errorf("counted %d vacuous, want 1 — an exclusion nobody can see is one nobody can check", got.Vacuous)
	}
	if got.Cases != 3 {
		t.Errorf("denominator %d, want 3 — the non-vacuous cases, including the one this arm "+
			"ranked 0 because it fell outside its page rather than outside the pool", got.Cases)
	}
	if got.StaleAbove != 1 {
		t.Errorf("counted %d stale-above, want 1", got.StaleAbove)
	}
	if math.Abs(got.Rate()-1.0/3.0) > 1e-9 {
		t.Errorf("rate %.4f, want 1/3 (one stale-above out of three non-vacuous)", got.Rate())
	}
}

// TestStaleAboveRateCountsUnreachableCurrent pins the sentinel that a bare `<`
// would get backwards.
//
// A gold rank of 0 means the CURRENT version was never retrieved. The distractor
// being retrieved while the correction is missing is the worst outcome this
// metric exists to measure, and `distractor < gold` scores it as a success
// because 0 sorts first. It counts as stale-above, and separately as
// CurrentUnreachable so the two are distinguishable.
func TestStaleAboveRateCountsUnreachableCurrent(t *testing.T) {
	cases := []EvalCaseResult{
		{Category: CatTemporal, PoolRank: 0, DistractorPoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 0}, DistractorRanks: map[EvalArm]int{ArmHybrid: 2}},
	}
	got := StaleAboveRate(cases, ArmHybrid)
	if got.StaleAbove != 1 {
		t.Errorf("stale retrieved with the correction missing must count as stale-above, got %d", got.StaleAbove)
	}
	if got.CurrentUnreachable != 1 {
		t.Errorf("counted %d unreachable-current, want 1", got.CurrentUnreachable)
	}
	if got.StaleAboveReachable != 0 {
		t.Errorf("the reachable-only rate must exclude it, got %d", got.StaleAboveReachable)
	}
}

// TestStaleAboveRateWilsonNotBootstrap pins the interval's shape.
//
// This is a proportion, not a mean of reciprocal ranks, and resampling a
// proportion by percentile returns [0,0] at a rate of 0 and [1,1] at 1 — which
// are exactly the values a small corpus produces most often, and exactly where a
// zero-width interval is a lie. The Wilson score interval stays open at both
// ends.
func TestStaleAboveRateWilsonNotBootstrap(t *testing.T) {
	zero := WilsonInterval(0, 20)
	if zero.Lo != 0 {
		t.Errorf("Wilson lower bound at 0/20 = %.4f, want 0", zero.Lo)
	}
	if zero.Hi <= 0 {
		t.Errorf("Wilson upper bound at 0/20 = %.4f — a zero-width interval at zero successes "+
			"claims certainty 20 samples cannot support", zero.Hi)
	}
	one := WilsonInterval(20, 20)
	if one.Hi != 1 {
		t.Errorf("Wilson upper bound at 20/20 = %.4f, want 1", one.Hi)
	}
	if one.Lo >= 1 {
		t.Errorf("Wilson lower bound at 20/20 = %.4f — same lie at the other end", one.Lo)
	}
	// A wider interval for less data is the property that makes it worth having.
	if WilsonInterval(1, 5).Hi-WilsonInterval(1, 5).Lo <= WilsonInterval(20, 100).Hi-WilsonInterval(20, 100).Lo {
		t.Error("1/5 must yield a wider interval than 20/100 at the same point estimate")
	}
	if WilsonInterval(0, 0).Hi != 0 {
		t.Error("no samples must not produce an interval claiming anything")
	}
}

// TestSupersessionGateThreeOutcomes pins that the verdict is read off the
// INTERVAL, not the point estimate, and that straddling the bar is its own
// answer rather than being rounded to one side.
//
// On a corpus with a few dozen verified pairs the point estimate is mostly
// noise. A gate that compares it to a bar answers confidently either way and is
// wrong about half the time it matters; the honest third outcome is "this corpus
// cannot tell", which is a finding about the evidence rather than about the
// ranker.
func TestSupersessionGateThreeOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		successes  int
		n          int
		wantStatus string
	}{
		// Interval entirely above the bar: the failure is real and common.
		{"clearly above the bar", 30, 40, VerdictJustified},
		// Interval entirely below: the failure is rare enough not to justify work.
		{"clearly below the bar", 0, 60, VerdictNotJustified},
		// Straddling: the corpus cannot separate the two.
		{"straddles the bar", 8, 40, VerdictUnresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cell := SupersessionCell{Scope: ScopePool, Cases: tc.n, StaleAbove: tc.successes}
			got := SupersessionVerdict(cell, supersessionBar)
			if got.Status != tc.wantStatus {
				t.Errorf("%d/%d against a bar of %.2f gave %q (interval %s), want %q",
					tc.successes, tc.n, supersessionBar, got.Status, got.Interval, tc.wantStatus)
			}
		})
	}
}

// TestSupersessionGateDisagreeingTreatmentsAreUnresolved pins the rule that a
// verdict depending on which defensible treatment you pick is not a verdict.
//
// A case whose CORRECTION was never retrieved can be counted as a failure — the
// stale one came back and its replacement did not — or excluded, on the grounds
// that no ranking could have fixed a retrieval miss. Both are defensible. When
// they disagree about the outcome, the answer is unresolved and both rates are
// named, rather than the report quietly shipping whichever one the author
// happened to code first.
func TestSupersessionGateDisagreeingTreatmentsAreUnresolved(t *testing.T) {
	// Counting unreachable as failures puts the rate well above the bar; excluding
	// them puts it well below.
	cell := SupersessionCell{Scope: ScopePool, Cases: 40, StaleAbove: 30,
		StaleAboveReachable: 0, CurrentUnreachable: 30}
	got := SupersessionVerdict(cell, supersessionBar)
	if got.Status != VerdictUnresolved {
		t.Errorf("status %q, want %q — counting unreachable cases as failures and excluding them "+
			"give opposite answers here, and a verdict that depends on that choice is not one",
			got.Status, VerdictUnresolved)
	}
	if got.Reason == "" {
		t.Error("an unresolved verdict must say which two treatments disagreed")
	}
}

// TestSupersessionGateVetoNeedsNonInferiority pins both halves of the recency
// veto, and the argument order that makes it honest.
//
// A cheap fix may only close the case if it is cheap: a band that fixes
// supersession while costing general ranking has not closed anything, it has
// moved the loss. And PairedDelta(a, b) is MRR(a) − MRR(b), so an inverted pair
// would let a band veto by being WORSE than the arm it replaces.
func TestSupersessionGateVetoNeedsNonInferiority(t *testing.T) {
	base := SupersessionVerdict(SupersessionCell{Scope: ScopePool, Cases: 40, StaleAbove: 30}, supersessionBar)
	if base.Status != VerdictJustified {
		t.Fatalf("fixture: the base verdict must be %q to have anything to veto, got %q", VerdictJustified, base.Status)
	}

	// A band that closes the failure AND costs nothing vetoes.
	cheap := SupersessionCell{Scope: ScopePool, Cases: 40, StaleAbove: 0}
	if got := ApplyRecencyVeto(base, cheap, Interval{Lo: -0.01, Hi: 0.03}, len(recencySweep)); got.Status != VerdictNotJustified {
		t.Errorf("a band that closes the failure at no ranking cost must veto, got %q", got.Status)
	}
	// A band that closes the failure but costs general ranking must NOT veto.
	if got := ApplyRecencyVeto(base, cheap, Interval{Lo: -0.20, Hi: -0.10}, len(recencySweep)); got.Status == VerdictNotJustified {
		t.Error("a band that closes supersession while costing general ranking vetoed anyway — " +
			"that is not a cheap fix, it is the loss moved somewhere the gate was not looking")
	}
	// A band whose own rate does not clear the bar cannot veto however cheap it is.
	weak := SupersessionCell{Scope: ScopePool, Cases: 40, StaleAbove: 20}
	if got := ApplyRecencyVeto(base, weak, Interval{Lo: -0.01, Hi: 0.03}, len(recencySweep)); got.Status == VerdictNotJustified {
		t.Error("a band that does not close the failure vetoed on cost alone")
	}

	// The family-wise correction has to bite. 2/40 clears the bar at a nominal
	// 95% (upper bound 0.165) and does NOT clear it corrected over three bands
	// (0.202), so a band selected as the best of the sweep must not veto on the
	// uncorrected interval. Without this the correction could be deleted and
	// every other assertion here would still pass.
	borderline := SupersessionCell{Scope: ScopePool, Cases: 40, StaleAbove: 2}
	if got := ApplyRecencyVeto(base, borderline, Interval{Lo: -0.01, Hi: 0.03}, len(recencySweep)); got.Status == VerdictNotJustified {
		t.Error("a band vetoed on an interval that only clears the bar UNCORRECTED — the best of k " +
			"bands chosen after the fact is not a 95% claim about any one of them")
	}
	// And it must still veto when only one band was ever in play.
	if got := ApplyRecencyVeto(base, borderline, Interval{Lo: -0.01, Hi: 0.03}, 1); got.Status != VerdictNotJustified {
		t.Errorf("with a single pre-registered band there is nothing to correct for, so the same "+
			"counts must veto; got %q", got.Status)
	}
}

// TestRecencyVetoExplainsACostRejection pins the explanation, not just the
// verdict.
//
// The middle branch of ApplyRecencyVeto is the interesting one: a band that DOES
// close the supersession failure but whose ranking cost is not bounded. It
// returns the base status unchanged with a Reason saying why — and every other
// test here asserts only on Status, so replacing that whole branch with
// `return base` left the suite green while the explanation vanished.
//
// "A band nearly closed this and was rejected on cost" is the sentence that
// stops someone re-running the sweep next month, and the gate's own doc comment
// promises that each refusal names its cause.
func TestRecencyVetoExplainsACostRejection(t *testing.T) {
	base := SupersessionVerdict(SupersessionCell{Scope: ScopePool, Cases: 40, StaleAbove: 30}, supersessionBar)
	closes := SupersessionCell{Scope: ScopePool, Cases: 40, StaleAbove: 0}

	got := ApplyRecencyVeto(base, closes, Interval{Lo: -0.20, Hi: -0.10}, len(recencySweep))
	if got.Status != base.Status {
		t.Fatalf("a band whose cost is unbounded must not change the verdict, got %q", got.Status)
	}
	if got.Reason == "" {
		t.Fatal("the rejection produced no explanation — the operator sees a bare verdict and " +
			"cannot tell that a cheap fix nearly closed it")
	}
	for _, want := range []string{"cost", "-0.05"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("the explanation never mentions %q, so it does not say why the band was rejected: %s", want, got.Reason)
		}
	}
}

// rowsFor builds calibration rows: answerable scores that were reachable, and
// verified-absent scores. Unreachable rows are added separately by the tests that
// care, because their whole point is that they must NOT be scored.
func rowsFor(answerable, absent []float64) []CalibrationRow {
	var rows []CalibrationRow
	for _, s := range answerable {
		rows = append(rows, CalibrationRow{Population: PopReachable, Score: s, Scored: true})
	}
	for _, s := range absent {
		rows = append(rows, CalibrationRow{Population: PopAbsent, Score: s, Scored: true})
	}
	return rows
}

// TestRiskCoverageRecommendsThresholds pins both boundaries against their DECLARED
// rules rather than against whatever maximises accuracy.
//
// The two rules answer different questions and must not be collapsed into one.
// answer_at is "how high can the bar go while still answering what we can answer";
// refuse_below is "how low must it go before we are throwing away answers we had".
// A single accuracy-maximising threshold answers neither, and on an overlapping
// distribution it lands wherever the class sizes happen to put it.
func TestRiskCoverageRecommendsThresholds(t *testing.T) {
	t.Run("separable, with a band", func(t *testing.T) {
		// A band exists only while the allowance is STRICTLY tighter than the
		// recall target permits — allowance < (1-recall)*n. Here n=20 and recall
		// 0.90 permits 2 answerable below the bar while the allowance permits 1,
		// so the two boundaries land on different thresholds and the middle
		// verdict has somewhere to live.
		var answerable []float64
		for i := 5; i < 25; i++ {
			answerable = append(answerable, float64(i))
		}
		got := RecommendThresholds(rowsFor(answerable, []float64{0, 1, 2}), 0.90, 1)
		if got.AnswerAt == nil || got.RefuseBelow == nil {
			t.Fatalf("separable data produced no boundaries: %+v", got)
		}
		if *got.AnswerAt < 5 {
			t.Errorf("answer_at %v is below the lowest answerable score — it answers more than "+
				"it needs to and admits absent cases for nothing", *got.AnswerAt)
		}
		if got.BandEmpty {
			t.Errorf("reported an empty band where the allowance (1) is tighter than the recall "+
				"target permits (2 of 20): %+v", got)
		}
		if !(*got.RefuseBelow < *got.AnswerAt) {
			t.Errorf("refuse_below %v is not below answer_at %v, so there is no band",
				*got.RefuseBelow, *got.AnswerAt)
		}
	})

	t.Run("band collapses when the allowance matches the target", func(t *testing.T) {
		// The case ADR-001 T2 predicted: at n=20 a 0.95 recall target permits
		// exactly one answerable below the bar, and so does an allowance of 1 —
		// so both rules select the SAME threshold and there is no middle verdict.
		// An empty band is a real outcome to report, not a failure to compute one.
		var answerable []float64
		for i := 5; i < 25; i++ {
			answerable = append(answerable, float64(i))
		}
		got := RecommendThresholds(rowsFor(answerable, []float64{0, 1, 2}), 0.95, 1)
		if got.AnswerAt == nil || got.RefuseBelow == nil {
			t.Fatalf("no boundaries: %+v", got)
		}
		if !got.BandEmpty {
			t.Errorf("the two rules selected %v and %v and the band was not reported empty",
				*got.RefuseBelow, *got.AnswerAt)
		}
	})

	t.Run("the band never inverts", func(t *testing.T) {
		// At small n the allowance can be LOOSER than the recall target permits:
		// 4 rows, a 0.95 target pins answer_at to the minimum, and an allowance of
		// 1 (25% of the sample) lifts refuse_below above it. T2 states the
		// ordering "follows from recall being non-increasing"; it does not, it
		// follows from allowance <= (1-recall)*n, and nothing enforced that.
		//
		// An inverted band would put the "not sure" verdict where the system
		// should simply answer.
		got := RecommendThresholds(rowsFor([]float64{1, 2, 3, 4}, []float64{0}), 0.95, 1)
		if got.AnswerAt == nil || got.RefuseBelow == nil {
			t.Fatalf("no boundaries: %+v", got)
		}
		if *got.RefuseBelow > *got.AnswerAt {
			t.Errorf("refuse_below %v sits ABOVE answer_at %v", *got.RefuseBelow, *got.AnswerAt)
		}
		if !got.BandEmpty {
			t.Error("an incoherent allowance/target pair produced a band rather than collapsing")
		}
	})

	t.Run("overlapping honours the recall target", func(t *testing.T) {
		// one answerable sits down among the absent scores. At a 1.00 recall
		// target the bar must drop to include it; the rule is the target, not
		// whatever separates best.
		rows := rowsFor([]float64{-6.5, 5, 6, 7}, []float64{-6.3, 0, 1})
		strict := RecommendThresholds(rows, 1.00, 1)
		if strict.AnswerAt == nil {
			t.Fatal("no answer_at at a 1.00 recall target")
		}
		if *strict.AnswerAt > -6.5 {
			t.Errorf("answer_at %v excludes an answerable case at -6.5 while the target demands "+
				"100%% recall", *strict.AnswerAt)
		}
		relaxed := RecommendThresholds(rows, 0.75, 1)
		if relaxed.AnswerAt == nil {
			t.Fatal("no answer_at at a 0.75 recall target")
		}
		if !(*relaxed.AnswerAt > *strict.AnswerAt) {
			t.Errorf("a looser recall target (%v) did not raise the bar above the strict one (%v) — "+
				"the target is not driving the choice", *relaxed.AnswerAt, *strict.AnswerAt)
		}
		if relaxed.AchievedRecall < 0.75 {
			t.Errorf("achieved recall %v is below the target it was asked for", relaxed.AchievedRecall)
		}
	})

	t.Run("unreachable rows are never scored as answerable", func(t *testing.T) {
		base := rowsFor([]float64{5, 6, 7, 8}, []float64{0, 1, 2})
		withUnreachable := append(append([]CalibrationRow(nil), base...),
			// a gold that never entered the pool, sitting at a terrible score:
			// counting it as answerable would drag the bar down to rescue a case
			// no threshold could ever have rescued
			CalibrationRow{Population: PopUnreachable, Score: -99, Scored: true},
		)
		a := RecommendThresholds(base, 0.95, 1)
		b := RecommendThresholds(withUnreachable, 0.95, 1)
		if a.AnswerAt == nil || b.AnswerAt == nil {
			t.Fatal("missing boundary")
		}
		if *a.AnswerAt != *b.AnswerAt {
			t.Errorf("an UNREACHABLE case moved answer_at from %v to %v — its gold never entered "+
				"the pool, so no threshold could have surfaced it and it is a retrieval fact "+
				"wearing a ranking result's clothes", *a.AnswerAt, *b.AnswerAt)
		}
		if b.Unreachable != 1 {
			t.Errorf("unreachable count %d, want 1 — excluded rows must still be REPORTED, or the "+
				"reader cannot tell a clean sample from one that dropped a third of itself", b.Unreachable)
		}
	})

	t.Run("refuse_below honours its allowance", func(t *testing.T) {
		// This subtest exists because a mutant found its absence: ignoring
		// refuseAllowance entirely broke nothing, so the second boundary — half
		// the recommendation — was derived by code no test drove.
		//
		// The allowance is a COUNT of answerable rows permitted to fall below the
		// bar. At 0 the bar cannot sit above the lowest answerable score; at 1 it
		// may rise past exactly one of them.
		// n=20 so the recall grid is fine enough that a 0.95 target and an
		// allowance of 1 are coherent with each other; at n=4 they are not,
		// and the guard below would (correctly) collapse the band instead.
		var answerable []float64
		for i := 1; i <= 20; i++ {
			answerable = append(answerable, float64(i))
		}
		rows := rowsFor(answerable, []float64{0})
		strict := RecommendThresholds(rows, 0.95, 0)
		loose := RecommendThresholds(rows, 0.95, 1)
		if strict.RefuseBelow == nil || loose.RefuseBelow == nil {
			t.Fatalf("missing refuse_below: strict=%+v loose=%+v", strict, loose)
		}
		if *strict.RefuseBelow > 1 {
			t.Errorf("refuse_below %v at allowance 0 sits above the lowest answerable score (1), "+
				"so an answerable case falls below a bar that permits none", *strict.RefuseBelow)
		}
		if !(*loose.RefuseBelow > *strict.RefuseBelow) {
			t.Errorf("allowance 1 gave refuse_below %v, the same as allowance 0 (%v) — the "+
				"allowance is not driving the boundary", *loose.RefuseBelow, *strict.RefuseBelow)
		}
		// The ordering is a consequence of recall being non-increasing in the
		// threshold, so it is asserted rather than assumed.
		if loose.AnswerAt != nil && *loose.RefuseBelow > *loose.AnswerAt {
			t.Errorf("refuse_below %v sits ABOVE answer_at %v — the band is inverted and the "+
				"middle verdict would fire where the system should be answering",
				*loose.RefuseBelow, *loose.AnswerAt)
		}
	})

	t.Run("unscored rows are excluded", func(t *testing.T) {
		rows := rowsFor([]float64{5, 6, 7}, []float64{0, 1})
		rows = append(rows, CalibrationRow{Population: PopReachable, Score: 0, Scored: false})
		got := RecommendThresholds(rows, 0.95, 1)
		if got.Reachable != 3 {
			t.Errorf("reachable count %d, want 3 — a row nothing scored has no score, and a "+
				"zero there is a value the reranker never produced", got.Reachable)
		}
	})
}

// TestGateFailsBelowDeclaredBar pins that the go/no-go is the Wilson LOWER BOUND
// against the declared bar, not the point estimate.
//
// At the sample sizes this ADR works with, the point estimate carries roughly
// ±0.11 at one standard error. A gate comparing the estimate passes on noise
// roughly half the time it is near the bar, which is the failure mode that makes
// a gate worse than no gate: it produces a verdict with the authority of a
// measurement and the reliability of a coin.
func TestGateFailsBelowDeclaredBar(t *testing.T) {
	// 7 of 20 refused = 0.35 observed, comfortably above a 0.30 bar on the point
	// estimate — and its 90% lower bound is not.
	pass, rate, lower, n := RefusalGate(20, 7, 0.30)
	if n != 20 {
		t.Fatalf("n reported as %d", n)
	}
	if rate < 0.34 || rate > 0.36 {
		t.Errorf("observed rate %v, want ~0.35", rate)
	}
	if lower >= rate {
		t.Errorf("the lower bound %v is not below the point estimate %v — it is not a bound", lower, rate)
	}
	if pass {
		t.Errorf("gate PASSED on an observed 0.35 whose 90%% lower bound is %.3f, below the 0.30 "+
			"bar — this is the case the bound exists for", lower)
	}

	// A large, clearly-clearing sample must pass, or the gate can never say yes.
	if pass, _, lower, _ := RefusalGate(400, 200, 0.30); !pass {
		t.Errorf("gate FAILED on 200/400 with lower bound %.3f — a gate that cannot pass is not "+
			"a gate, it is a refusal", lower)
	}

	// Zero cases is undefined, not failing-with-confidence.
	if pass, _, _, n := RefusalGate(0, 0, 0.30); pass || n != 0 {
		t.Error("an empty sample produced a verdict; there is nothing to be confident about")
	}
}
