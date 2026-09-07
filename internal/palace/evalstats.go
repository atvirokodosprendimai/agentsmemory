package palace

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"sort"
)

// Statistical honesty for the eval.
//
// Every mistake this file exists to prevent has already been made in this repo:
// arms were ranked on MRR gaps of 0.05 at n=12, conclusions were drawn from
// tables whose differences were sampling noise, and nothing in the output said
// so. A number without an interval invites exactly that reading.
//
// The design is a PAIRED bootstrap: every arm answered the same questions, so
// resampling cases (not scores) preserves the pairing, and the interval on a
// DIFFERENCE between arms is far tighter than the intervals on the arms
// themselves. "Is A better than B?" is answered by the delta's interval
// containing zero, not by eyeballing two overlapping ranges.

// bootstrapIters is how many resamples build an interval. 2000 puts the Monte
// Carlo error well under the widths we print at these sample sizes.
const bootstrapIters = 2000

// bootstrapSeed fixes the resampling so two runs of the same case file print the
// same intervals — an eval that gives different intervals on identical inputs
// reads as broken, and reproducibility is the whole point of saved cases.
const bootstrapSeed = 42

// Interval is a 95% bootstrap confidence interval.
type Interval struct {
	Lo, Hi float64
}

// Contains reports whether v lies inside the interval.
func (i Interval) Contains(v float64) bool { return v >= i.Lo && v <= i.Hi }

// String renders the interval compactly for the report table.
func (i Interval) String() string { return fmt.Sprintf("[%.2f–%.2f]", i.Lo, i.Hi) }

// reciprocal maps a 1-based rank (0 = miss) to its reciprocal rank.
func reciprocal(rank int) float64 {
	if rank <= 0 {
		return 0
	}
	return 1 / float64(rank)
}

// BootstrapMRR builds the 95% interval for one arm's MRR over its per-case
// ranks.
func BootstrapMRR(ranks []int) Interval {
	n := len(ranks)
	if n == 0 {
		return Interval{}
	}
	rng := rand.New(rand.NewSource(bootstrapSeed))
	means := make([]float64, bootstrapIters)
	for b := range means {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += reciprocal(ranks[rng.Intn(n)])
		}
		means[b] = sum / float64(n)
	}
	sort.Float64s(means)
	return Interval{Lo: means[int(0.025*bootstrapIters)], Hi: means[int(0.975*bootstrapIters)-1]}
}

// PairedDelta builds the 95% interval for MRR(a) − MRR(b) over the SAME cases.
//
// Pairing is what makes small samples usable at all: the per-case difference
// cancels the difficulty of the question, so the interval measures the arms'
// disagreement rather than the questions' variance. An interval containing zero
// means the data cannot tell the arms apart — and the report says that instead
// of ranking them.
func PairedDelta(a, b []int) Interval {
	n := len(a)
	if n == 0 || n != len(b) {
		return Interval{}
	}
	diffs := make([]float64, n)
	for i := range diffs {
		diffs[i] = reciprocal(a[i]) - reciprocal(b[i])
	}
	rng := rand.New(rand.NewSource(bootstrapSeed))
	means := make([]float64, bootstrapIters)
	for bi := range means {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += diffs[rng.Intn(n)]
		}
		means[bi] = sum / float64(n)
	}
	sort.Float64s(means)
	return Interval{Lo: means[int(0.025*bootstrapIters)], Hi: means[int(0.975*bootstrapIters)-1]}
}

// ClosetCell is one category's preselected closet-versus-no-closet comparison,
// with every case it declined to use accounted for.
//
// The exclusion counts are not diagnostics. A delta computed over an unstated
// subset is a number nobody can check, and the whole point of preselecting this
// comparison is that the reader can see exactly what went into it.
//
// ⚠ A DELTA OF ZERO IS TWO OPPOSITE FACTS WEARING ONE NUMBER, which is why
// Status exists. Seven tables printed `Δ +0.000` with `moved 0` and were read as
// evidence that the closet prior does nothing. With no closets in the corpus the
// boost has no input, so ArmHybridCloset and ArmHybrid are the SAME pipeline and
// the zero is arithmetic. ADR-003's decision reads this cell, so the difference
// between a null and an unrun experiment is the difference between retiring a
// feature on evidence and retiring it on an artefact.

// ClosetStatus says what a closet cell's number is worth.
//
// The three values are not severities. `no effect` and `not measured` both print
// a delta of zero and mean opposite things: the first is a result, the second is
// the absence of one.
type ClosetStatus string

const (
	// ClosetMeasured: the two arms ordered at least one admitted case differently,
	// so the delta is a measurement of the prior.
	ClosetMeasured ClosetStatus = "measured"
	// ClosetNoEffect: the corpus held closets and the two arms still ordered every
	// admitted case identically. A real null, and it keeps its interval.
	ClosetNoEffect ClosetStatus = "no effect"
	// ClosetNotMeasured: the experiment had no input — no closets in the corpus, or
	// no case the two arms could be paired on. The zero is arithmetic.
	ClosetNotMeasured ClosetStatus = "not measured"
)

type ClosetCell struct {
	Category string
	// Admitted is how many cases the delta is computed over.
	Admitted int
	// Unreachable is cases whose gold never entered the pool. No arm could have
	// ranked it, so the pair would contribute a zero for a retrieval reason.
	Unreachable int
	// NoGold is cases one of the two arms did not score, so they cannot be paired.
	NoGold int
	// DeltaMRR is the mean of (closet − no-closet) reciprocal ranks. Negative
	// means the prior costs.
	DeltaMRR float64
	// Interval is the 95% paired bootstrap interval on DeltaMRR.
	Interval Interval
	// DeltaRecall1 is closet's recall@1 minus no-closet's, over admitted cases.
	DeltaRecall1 float64
	// Moved is how many admitted cases the two arms ranked differently at all.
	// A delta near zero means something different when nothing moved than when
	// many cases moved and cancelled out.
	Moved int
	// Status says whether this cell is a finding at all: `measured`, `no effect`,
	// or `not measured` when the experiment had no input to run on.
	Status ClosetStatus
	// Missing names the absent input when Status is ClosetNotMeasured, and is
	// empty otherwise. A status that says only THAT something is wrong sends the
	// reader to the ranking code, which is where the number came from and not
	// where the problem is.
	Missing string
}

// ClosetDelta computes the comparison ADR-003 is decided on: ArmHybridCloset
// against ArmHybrid, over one category, with the exclusions fixed in advance.
//
// It is deliberately dull. There is no threshold in it, no minimum case count,
// and no branch that depends on the sign of its own output — because the table's
// existing "vs best" verdicts compare each arm against a baseline chosen from
// the same table, which flatters whichever arm happened to win. A statistic that
// decides a default has to be named before the run and computed the same way
// whatever it returns.
//
// Sign convention: Δ = closet minus no-closet. Negative means the prior costs.
func ClosetDelta(report EvalReport, category string) ClosetCell {
	cell := ClosetCell{Category: category}
	var withCloset, without []int

	for _, d := range report.Details {
		if d.Category != category {
			continue
		}
		if category == CatAbsent {
			continue // no gold to rank; the delta is undefined, not zero
		}
		if d.PoolRank <= 0 {
			cell.Unreachable++
			continue
		}
		a, okA := d.Ranks[ArmHybridCloset]
		b, okB := d.Ranks[ArmHybrid]
		if !okA || !okB {
			cell.NoGold++
			continue
		}
		cell.Admitted++
		withCloset = append(withCloset, a)
		without = append(without, b)
		if a != b {
			cell.Moved++
		}
		if a == 1 {
			cell.DeltaRecall1++
		}
		if b == 1 {
			cell.DeltaRecall1--
		}
	}

	// The status is decided on the CORPUS, not on `moved`. A rule reading `moved
	// == 0` alone would report a genuine null — closets present, none inside
	// closetDistanceCap, so the boost had an input and declined to fire — as a
	// non-answer, which the task pre-registers as grounds to withdraw the rule
	// rather than ship it. TestGenuineNullIsStillReported is that check.
	switch {
	case cell.Admitted == 0:
		cell.Status, cell.Missing = ClosetNotMeasured, "no case both arms could be paired on"
		return cell
	case report.Closets == 0:
		cell.Status, cell.Missing = ClosetNotMeasured, "no closets in the corpus, so the boost had no input"
	case cell.Moved == 0:
		cell.Status = ClosetNoEffect
	default:
		cell.Status = ClosetMeasured
	}
	var sum float64
	for i := range withCloset {
		sum += reciprocal(withCloset[i]) - reciprocal(without[i])
	}
	cell.DeltaMRR = sum / float64(cell.Admitted)
	cell.DeltaRecall1 /= float64(cell.Admitted)
	cell.Interval = PairedDelta(withCloset, without)
	return cell
}

// SupersessionCell is one arm's stale-above measurement over one population,
// with every case it declined to use accounted for.
type SupersessionCell struct {
	// Scope names the population this arm's number was measured over. Without it
	// a pool-scoped arm and a page-scoped one print side by side as if they had
	// answered the same question.
	Scope SupersessionScope
	// Cases is the denominator: non-vacuous cases only.
	Cases int
	// StaleAbove is how many of those put the superseded version above the
	// correction — including the cases where the correction was never retrieved
	// at all, which is the worst outcome and the one a bare `<` scores as a win.
	StaleAbove int
	// StaleAboveReachable is the same count restricted to cases whose correction
	// WAS retrieved, so a ranking failure can be told from a retrieval one.
	StaleAboveReachable int
	// CurrentUnreachable is how many cases never retrieved the correction.
	CurrentUnreachable int
	// StaleInPage is how many put the superseded version at rank 5 or better —
	// the cutoff Recall5 uses, and the page an agent actually sees.
	StaleInPage int
	// Vacuous is how many cases the superseded version never entered the pool
	// in, so no arm could have ranked it above anything.
	Vacuous int
}

// Rate is StaleAbove over the non-vacuous cases, or 0 when there are none.
func (c SupersessionCell) Rate() float64 {
	if c.Cases == 0 {
		return 0
	}
	return float64(c.StaleAbove) / float64(c.Cases)
}

// StaleAboveRate measures how often one arm put a superseded memory above the
// correction that replaced it.
//
// Two things it deliberately does not do. It does not read vacuity from the
// arm's own zero — that is a case-level property, taken from DistractorPoolRank,
// because an arm's 0 means "not in this ordering" and a vacuous case would then
// score as a success for every arm at once. And it does not treat a gold rank of
// 0 as a good position: 0 is the miss sentinel, so `distractor < gold` would
// score "the stale one came back and the correction did not" as a win, which is
// the exact failure the metric exists to catch.
func StaleAboveRate(cases []EvalCaseResult, arm EvalArm) SupersessionCell {
	var cell SupersessionCell
	for _, c := range cases {
		if c.DistractorRanks == nil {
			continue // the case names no superseded version
		}
		if c.DistractorPoolRank <= 0 {
			cell.Vacuous++
			continue
		}
		cell.Cases++
		gold, stale := c.Ranks[arm], c.DistractorRanks[arm]
		if gold == 0 {
			cell.CurrentUnreachable++
		}
		if stale > 0 && (gold == 0 || stale < gold) {
			cell.StaleAbove++
			if gold != 0 {
				cell.StaleAboveReachable++
			}
		}
		if stale > 0 && stale <= 5 {
			cell.StaleInPage++
		}
	}
	return cell
}

// WilsonInterval is the 95% score interval for a proportion.
//
// Not a bootstrap. Resampling a proportion by percentile returns [0,0] at zero
// successes and [1,1] at all of them — the two values a small corpus produces
// most often, and the two places a zero-width interval claims a certainty the
// sample cannot support. Wilson stays open at both ends and widens as n falls,
// which is the only reason to report an interval at all.
func WilsonInterval(successes, n int) Interval {
	if n <= 0 {
		return Interval{}
	}
	const z = 1.959963984540054 // 95%
	p := float64(successes) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	centre := (p + z*z/(2*nf)) / denom
	spread := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	lo, hi := centre-spread, centre+spread
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return Interval{Lo: lo, Hi: hi}
}

// fillSupersession attaches each arm's stale-above measurement, scoped, once the
// per-case details are complete.
//
// It runs after the per-case loop rather than inside it because the metric's
// denominator is a property of the whole population — the non-vacuous cases —
// and a running total cannot exclude a case it has not seen yet.
func fillSupersession(report *EvalReport) {
	for i := range report.Arms {
		arm := report.Arms[i].Arm
		cell := StaleAboveRate(report.Details, arm)
		cell.Scope = ArmScope(arm)
		report.Arms[i].Supersession = cell
	}
}

// PrintSupersessionTable renders the stale-above measurement, grouped by scope.
//
// Grouped because the three scopes answer different questions and a reader who
// sums the column has added them together: a pool-scoped arm re-orders every
// candidate, ArmProduction sees at most a page after the distance gate, and the
// contextual arm retrieves from its own namespace. The denominator differs with
// the scope, so it is printed rather than assumed.
//
// The interval is beside every rate for the same reason it exists: on a corpus
// with a handful of verified pairs the point estimate is mostly noise, and a
// rate printed alone invites a conclusion the sample cannot support.
func PrintSupersessionTable(out io.Writer, report EvalReport) {
	printSupersessionTable(out, report)
}

// printSupersessionTable is the unexported body, kept so the tests in this
// package can render a table without going through the exported name.
func printSupersessionTable(out io.Writer, report EvalReport) {
	byScope := map[SupersessionScope][]EvalMetrics{}
	var order []SupersessionScope
	for _, m := range report.Arms {
		sc := m.Supersession.Scope
		if sc == "" {
			continue
		}
		if _, seen := byScope[sc]; !seen {
			order = append(order, sc)
		}
		byScope[sc] = append(byScope[sc], m)
	}
	if len(order) == 0 {
		return
	}
	// A run with no temporal cases would otherwise print one all-zero row per
	// arm — thirty-odd lines saying nothing, which trains a reader to skip the
	// block entirely on the runs where it does say something.
	measured := false
	for _, arms := range byScope {
		for _, m := range arms {
			if m.Supersession.Cases > 0 || m.Supersession.Vacuous > 0 {
				measured = true
			}
		}
	}
	if !measured {
		// "No temporal cases ran" and "temporal cases ran but none carries a
		// verified pair" are opposite findings, and this printed one sentence for
		// both. The first is fixed by generating temporal cases; the second by
		// regenerating a file whose pairs were never recorded, and telling THAT
		// operator to run --style temporal sends them to repeat the run that just
		// failed. One report printed "no temporal cases in this run" while its
		// closet block counted `temporal · admitted 5` two blocks above.
		temporal := 0
		for _, d := range report.Details {
			if d.Category == CatTemporal {
				temporal++
			}
		}
		if temporal == 0 {
			fmt.Fprintf(out, "\nsupersession — no temporal cases in this run, so nothing to measure "+
				"(generate some with --style temporal)\n")
			return
		}
		fmt.Fprintf(out, "\nsupersession — %d temporal case(s) ran but none carries a verified "+
			"distractor pair, so there is nothing to measure. The pair is discovered and judged at "+
			"GENERATION, so a case file written before that existed measures nothing here: regenerate "+
			"it with --style temporal\n", temporal)
		return
	}

	fmt.Fprintf(out, "\nsupersession — how often the arm put the SUPERSEDED memory above the correction:\n")
	for _, sc := range order {
		switch sc {
		case ScopePool:
			fmt.Fprintf(out, "  scope %s — re-orders the shared candidate pool; denominator is the non-vacuous cases\n", sc)
		case ScopePage:
			fmt.Fprintf(out, "  scope %s — scored over the page Search returns after the distance gate, so a distractor\n"+
				"    absent from the page is not the same finding as one ranked below the gold\n", sc)
		default:
			fmt.Fprintf(out, "  scope %s — retrieves from its own namespace, so its pool is not the shared one\n", sc)
		}
		fmt.Fprintf(out, "    %-40s %6s %10s %16s %10s %12s %8s\n",
			"arm", "cases", "stale@1+", "95% Wilson", "in page", "unreachable", "vacuous")
		for _, m := range byScope[sc] {
			c := m.Supersession
			fmt.Fprintf(out, "    %-40s %6d %9.1f%% %16s %10d %12d %8d\n",
				m.Arm, c.Cases, 100*c.Rate(), WilsonInterval(c.StaleAbove, c.Cases),
				c.StaleInPage, c.CurrentUnreachable, c.Vacuous)
		}
	}
	fmt.Fprintln(out, "  'vacuous' cases never retrieved the superseded version at all, so no arm could have")
	fmt.Fprintln(out, "  ranked it above anything; 'unreachable' never retrieved the CORRECTION, which counts as")
	fmt.Fprintln(out, "  a stale-above because the stale one came back and its replacement did not.")
}

// The supersession gate's pre-registered constants. Each is a decision, not a
// tuning knob: changing one is an ADR amendment, because a threshold moved after
// seeing the data is not a threshold.
const (
	// supersessionBar is the stale-above rate above which the failure is worth
	// building a mechanism for.
	//
	// Too low and every corpus justifies work that a date preference would have
	// closed; too high and a real, common failure reads as noise and ships. 0.20
	// says: one temporal question in five returning the superseded memory above
	// its correction is not something to leave alone.
	supersessionBar = 0.20

	// supersessionMinCases is the floor below which the gate refuses to answer.
	//
	// It is a floor on VERIFIED, NON-VACUOUS pairs in this run, not on how many
	// the generator once wrote: vacuity is defined against the pool a run used,
	// so the same case file yields a different count at a different --pool. Below
	// it the Wilson interval is wide enough to straddle almost any bar, and a
	// gate that always answers "unresolved" teaches people to skip it.
	supersessionMinCases = 30

	// supersessionNonInferiority is the MRR loss a cheap fix may cost general
	// ranking before it stops counting as cheap.
	//
	// Honest about its own provenance: this margin is set by what n=40 can
	// resolve, not by the loss we would actually accept. It is the weakest number
	// in this ADR and should be re-derived once the non-temporal case set can
	// resolve less than it.
	supersessionNonInferiority = 0.05

	// The shipped ranking shape, mirrored here because evalstats cannot import the
	// config package. TestGatedArmMatchesTheShippedDefaults keeps both honest.
	defaultFusionIsRRF = true
	defaultClosetIsOn  = false
)

// defaultShapeService is a Service configured exactly as the SHIPPED defaults
// configure one, so the gate's pre-registered arm is read off the same mapping a
// running service is. The mirrors it reads are kept honest by
// TestGatedArmMatchesTheShippedDefaults in cmd/server, which compares them
// against config.Default().
func defaultShapeService() *Service {
	s := NewService(nil, nil, nil, 0)
	if defaultFusionIsRRF {
		s = s.WithFusion("rrf")
	}
	if !defaultClosetIsOn {
		s = s.WithClosetBoost(0)
	}
	return s
}

var supersessionGatedArm = defaultShapeService().gatedArm(true)

// The three outcomes a supersession verdict can take.
const (
	VerdictJustified    = "justified"
	VerdictNotJustified = "not justified"
	VerdictUnresolved   = "unresolved"
)

// SupersessionOutcome is one verdict with the evidence it was read from.
type SupersessionOutcome struct {
	Status string
	// Interval is the Wilson interval on the counted-as-failure rate.
	Interval Interval
	// Rate counts unreachable-correction cases as failures; RateReachable
	// excludes them. When the two disagree about the outcome the verdict is
	// unresolved and Reason says so.
	Rate, RateReachable float64
	Reason              string
}

// SupersessionVerdict decides whether the supersession failure is common enough
// to justify building a mechanism against it.
//
// The verdict comes from the INTERVAL, not the point estimate: on a corpus with a
// few dozen verified pairs the estimate is mostly noise, and a gate that compares
// it to a bar answers confidently either way while being wrong about half the
// time it matters. An interval that straddles the bar means this corpus cannot
// separate the two, which is a finding about the evidence and not about the
// ranker.
//
// The rate is computed under both defensible treatments of a case whose
// correction was never retrieved — counted as a failure, since the stale one came
// back and its replacement did not; or excluded, since no ranking fixes a
// retrieval miss. When they disagree about the outcome the verdict is unresolved
// naming both, because a verdict that depends on which treatment the author coded
// first is not a verdict.
func SupersessionVerdict(cell SupersessionCell, bar float64) SupersessionOutcome {
	out := SupersessionOutcome{Rate: cell.Rate()}
	out.Interval = WilsonInterval(cell.StaleAbove, cell.Cases)

	reachableCases := cell.Cases - cell.CurrentUnreachable
	if reachableCases > 0 {
		out.RateReachable = float64(cell.StaleAboveReachable) / float64(reachableCases)
	}

	call := func(iv Interval) string {
		switch {
		case iv.Lo > bar:
			return VerdictJustified
		case iv.Hi < bar:
			return VerdictNotJustified
		default:
			return VerdictUnresolved
		}
	}
	counted := call(out.Interval)
	out.Status = counted

	if reachableCases > 0 && cell.CurrentUnreachable > 0 {
		excluded := call(WilsonInterval(cell.StaleAboveReachable, reachableCases))
		if excluded != counted {
			out.Status = VerdictUnresolved
			out.Reason = fmt.Sprintf(
				"counting the %d case(s) whose correction was never retrieved as failures gives %q (rate %.3f); "+
					"excluding them gives %q (rate %.3f) — both treatments are defensible and they disagree",
				cell.CurrentUnreachable, counted, out.Rate, excluded, out.RateReachable)
		}
	}
	return out
}

// ApplyRecencyVeto downgrades a justified verdict when a swept recency band
// already closes the failure at no cost to general ranking.
//
// Two conditions, and both are necessary. The band's own interval must clear the
// bar — corrected at α/k over the k pre-registered bands, because the best of k
// bands chosen after the fact is not a 95% claim about any of them. And the
// band's ranking cost must be bounded: nonInferiority is PairedDelta(band,
// gatedArm), so MRR(band) − MRR(gatedArm), and its lower bound must sit above
// −supersessionNonInferiority. A band that closes supersession while costing
// general ranking has moved the loss rather than removed it, and an inverted
// argument order would let a band veto by being worse than the arm it replaces.
func ApplyRecencyVeto(base SupersessionOutcome, band SupersessionCell, nonInferiority Interval, k int) SupersessionOutcome {
	if base.Status != VerdictJustified || k < 1 {
		return base
	}
	// Bonferroni over the pre-registered bands: the interval that must clear the
	// bar is the corrected one, not the nominal 95%.
	iv := wilsonAt(band.StaleAbove, band.Cases, 0.05/float64(k))
	if !(iv.Hi < supersessionBar) {
		return base // the band does not close the failure
	}
	if !(nonInferiority.Lo > -supersessionNonInferiority) {
		out := base
		out.Reason = fmt.Sprintf(
			"a recency band closes the failure (interval %s at alpha/%d) but its ranking cost is not bounded: "+
				"PairedDelta(band, %s) = %s, whose lower bound is not above -%.2f — unresolved on cost, so it does not veto",
			iv, k, supersessionGatedArm, nonInferiority, supersessionNonInferiority)
		return out
	}
	return SupersessionOutcome{
		Status: VerdictNotJustified, Interval: base.Interval,
		Rate: base.Rate, RateReachable: base.RateReachable,
		Reason: fmt.Sprintf("a date preference already closes it: a recency band's interval %s at alpha/%d "+
			"is below the %.2f bar and it costs no general ranking (%s)", iv, k, supersessionBar, nonInferiority),
	}
}

// wilsonAt is WilsonInterval at an arbitrary alpha, for family-wise correction.
func wilsonAt(successes, n int, alpha float64) Interval {
	if n <= 0 {
		return Interval{}
	}
	z := math.Sqrt2 * math.Erfinv(1-alpha)
	p := float64(successes) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	centre := (p + z*z/(2*nf)) / denom
	spread := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	lo, hi := centre-spread, centre+spread
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return Interval{Lo: lo, Hi: hi}
}

// SupersessionGatedArm is the arm the gate is pre-registered against. Exported
// so the command can refuse a report that does not contain it, by name.
func SupersessionGatedArm() EvalArm { return supersessionGatedArm }

// SupersessionGatedArmFor reports the arm that reconstructs THIS service's
// ranking exactly, or "" when no arm does.
//
// "" is the honest answer more often than it looks. An eval arm is a FIXED
// pipeline: ArmHybrid is the 0.4 weight at the page-max normaliser, and the RRF
// arms carry no closet prior because armBoosts hands boosts only to arms whose
// NAME says closet — while Service.Search passes its boosts into whichever
// ranker it chose. A configuration no arm reproduces has no faithful number in
// the table, and naming the nearest one is exactly how "the gate judged a
// pipeline nobody runs" happened in the first place. ADR-007's rule governs this
// as much as any other printed number: a measurement whose mechanism was not the
// served one reports that it was not measured.
//
// TestGatedArmReconstructsTheServedRanking is what keeps this honest — it
// compares the ORDER production produces against the order the named arm
// produces, so a mapping that drifts fails on the ranking rather than on a name.
func (s *Service) SupersessionGatedArmFor() EvalArm { return s.gatedArm(s.rerank != nil) }

// gatedArm is SupersessionGatedArmFor with the reranker half passed in, so the
// SHIPPED default's arm — which is computed before any service exists — comes
// out of this same mapping rather than a second one beside it. Two mappings is
// how the constant it replaced went stale.
func (s *Service) gatedArm(reranked bool) EvalArm {
	closetOn := s.closetBoostScale > 0

	if s.fusionRRF {
		switch {
		case closetOn:
			return "" // no RRF arm carries the closet prior production applies
		case reranked:
			// The arm must reconstruct THIS service's ranking exactly, and the
			// normaliser is part of the ranking: the B1 reset made rrf+rerank the
			// min-max arm, so a service served at sigmoid or rank is represented
			// only by the arm that says so. Naming rrf+rerank for a sigmoid
			// service would report a supersession number from a pipeline nobody
			// serves — the same defect the reset fixed in the arms, one call site
			// over.
			switch s.RerankNormName() {
			case RerankNormSigmoid:
				return ArmBlendSigmoid
			case RerankNormRank:
				return ArmBlendRank
			default:
				return ArmRRFReranked // min-max
			}
		default:
			return ArmRRF
		}
	}

	// Linear fusion. Every closet-bearing arm and every reranked arm is built on
	// rankHybrid — rankHybridWeightedNorm at the FIXED hybridBM25Weight and the
	// page-max normaliser — so those arms are faithful to that exact shape only.
	plain := !s.bm25Auto && s.bm25Base == hybridBM25Weight && s.lexNormName == DefaultLexNorm
	switch {
	case closetOn && reranked:
		if plain && s.RerankNormName() == RerankNormMinMax {
			return ArmReranked
		}
		return ""
	case closetOn:
		if plain {
			return ArmHybridCloset
		}
		return ""
	case reranked:
		if plain && s.RerankNormName() == RerankNormMinMax {
			return ArmHybridRerank
		}
		return ""
	}

	// Closet off and no reranker: the swept fusion arms cover more shapes. Each
	// is named through the same helper the sweep registers it under, so an arm
	// that stops being swept stops being nameable here in the same edit.
	norm := s.lexNormName
	anchored := false
	for _, n := range anchoredNorms {
		if n.name == norm {
			anchored = true
		}
	}
	switch {
	case s.bm25Auto && s.bm25IDF:
		if norm == DefaultLexNorm {
			return ArmAdaptiveIDF
		}
		if anchored {
			return anchoredArm(ArmAdaptiveIDF, norm)
		}
		return ""
	case s.bm25Auto:
		if norm == DefaultLexNorm {
			return ArmAdaptive
		}
		if anchored {
			return anchoredArm(ArmAdaptive, norm)
		}
		return ""
	}
	for _, w := range bm25Sweep {
		if w != s.bm25Base {
			continue
		}
		if norm == DefaultLexNorm {
			if w == hybridBM25Weight {
				return ArmHybrid
			}
			return bm25Arm(w)
		}
		// The anchored sweep skips weight zero — the lexical term is multiplied
		// away, so the divisor cannot matter and no such arm is registered.
		if w != 0 && anchored {
			return anchoredArm(bm25Arm(w), norm)
		}
	}
	return ""
}

// SupersessionMinCases is the floor on verified, non-vacuous pairs. Exported for
// the command's refusal message.
func SupersessionMinCases() int { return supersessionMinCases }

// SupersessionBar is the pre-registered rate the verdict is read against.
func SupersessionBar() float64 { return supersessionBar }

// RecencyBandCount is k, the number of pre-registered bands the veto corrects
// over. The correction is what stops the best of k, chosen after the fact, from
// being reported as a 95% claim about any one of them.
func RecencyBandCount() int { return len(recencySweep) }

// RecencyBandCell reports an arm's supersession cell when the arm is a swept
// recency band, so the veto can consider it without the caller matching names.
func RecencyBandCell(m EvalMetrics) (SupersessionCell, bool) {
	if _, ok := recencyBandOf(m.Arm); !ok {
		return SupersessionCell{}, false
	}
	return m.Supersession, true
}
