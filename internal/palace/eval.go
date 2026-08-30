package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

// Retrieval evaluation: does recall actually return the memory that answers the
// question, and does each ranking stage earn its place?
//
// It lives inside this package because an honest ablation has to run the REAL
// ranking code — rankRetrieved, the closet boost, the cross-encoder — rather
// than a reimplementation that agrees with itself. Nothing here is exposed over
// MCP: an eval knob on the production search API is a knob that eventually gets
// set in production.
//
// Every arm scores the SAME candidate pool from one vector search per query, so
// the table measures ordering rather than the noise of re-running retrieval.

// EvalArm is one ranking configuration under test.
type EvalArm string

const (
	// ArmVector is nearest-neighbour order alone — the baseline everything else
	// has to beat to justify existing.
	ArmVector EvalArm = "vector"
	// ArmHybrid adds the lexical BM25 half of the fusion.
	ArmHybrid EvalArm = "hybrid"
	// ArmHybridCloset adds the closet boost.
	ArmHybridCloset EvalArm = "hybrid+closet"
	// ArmHybridRerank is the closet-OFF reranked arm: fusion without the
	// curation prior, then the cross-encoder. After ADR-003's flip this is the
	// shape production serves, so without it the only reranked row in the table
	// would be named after a configuration nobody runs.
	//
	// It blends at the SERVED rerank weight, and rerankSweep contains 0.5, which
	// is also DefaultRerankWeight — so at the default configuration this row and
	// `rerank blend w=0.50` are computed identically and will agree exactly.
	// That is not a bug and it is not removed by dropping 0.5 from the sweep:
	// the sweep has to keep a fixed grid for runs to stay comparable across
	// configurations, and this arm has to track whatever weight is served. Two
	// rows agreeing is the correct reading of "the served weight happens to be a
	// swept point"; they diverge the moment RERANK_WEIGHT moves.
	ArmHybridRerank EvalArm = "hybrid+rerank"
	// ArmReranked is fusion WITH the closet prior, then the cross-encoder over
	// the top K, blended at the configured weight.
	ArmReranked EvalArm = "hybrid+closet+rerank"
	// ArmRRF fuses the same two retrievers by RANK instead of by weighted score —
	// the candidate for replacing an inherited 0.6/0.4 split with something that
	// needs no tuning at all.
	ArmRRF EvalArm = "rrf"
	// ArmBlendSigmoid is the served arm with the rerank axis normalised by
	// sigmoid instead of min-max, so a cross-encoder that is indifferent
	// contributes almost nothing rather than being stretched to the full range.
	ArmBlendSigmoid EvalArm = "rrf+rerank norm=sigmoid"
	// ArmBlendRank is the served arm with the rerank axis normalised by POSITION.
	// It cannot amplify a rounding difference into a decisive one, and it still
	// forces the extremes to {0,1} — so it separates the two halves of the defect:
	// if sigmoid wins and rank does not, the magnitude information is what mattered.
	ArmBlendRank EvalArm = "rrf+rerank norm=rank"
	// ArmRRFReranked is RRF with the cross-encoder on top, so the fusion choice
	// and the rerank choice can be read independently.
	ArmRRFReranked EvalArm = "rrf+rerank"
	// ArmContextual retrieves from an index built with each chunk carrying a
	// little of its parent's context, then ranks it exactly like ArmHybridCloset —
	// so the delta is the EMBEDDING, not the ranking.
	ArmContextual EvalArm = "contextual chunks"
	// ArmProduction goes through Service.Search itself — the code agents actually
	// call — rather than this file's reconstruction of it. It exists because an
	// eval that reimplements the pipeline can score well while production is
	// broken, and has: a mis-set rerank URL made every real search silently fall
	// back to hybrid while the eval's own arms looked fine.

	ArmProduction EvalArm = "production (Search)"

	// ArmFactRetrieval scores whether a question reaches the FACT that answers
	// it, rather than a drawer that mentions it. It is the instrument ADR-036
	// exists to build first: kg_triples and kg_entities are consulted nowhere on
	// the search path, so fact retrieval has never been measured and therefore
	// could not be improved.
	//
	// Its baseline is 0% by construction — nothing returns facts yet — which is
	// what exempts it from the ~0.01 MRR noise floor measured 2026-08-26 between
	// two provably identical arms. A non-zero result cannot be noise when the
	// only alternative is zero.
	//
	// It is NOT a fusion arm: it re-scores the same retrieved pool against a
	// different gold (a triple, not a drawer), so fusionRankerFor returns nil for
	// it and it may sit after the production arms without displacing them.
	ArmFactRetrieval EvalArm = "fact retrieval"
)

// ProductionRetrieveK is the retrieve-width floor the production retrieve-k arm
// asks Search for. It matches cmd/server's defaultEvalPool so the table can
// compare "same retrieve as the ablation pool, default page" without the two
// 50s drifting. The page Limit stays DefaultSearchLimit.
const ProductionRetrieveK = 50

// ArmProductionRetrieve is Service.Search at DefaultSearchLimit with a
// retrieve-k floor of ProductionRetrieveK. The page-cut row could not tell a
// ranking cut from a retrieve that never fetched the gold; this arm asks for
// the same page agents get and the fetch width the ablation already uses.
var ArmProductionRetrieve = EvalArm(fmt.Sprintf("production (Search) retrieve-k=%d", ProductionRetrieveK))

// productionRetrieveFloor is the retrieve-k an arm asks Search for.
//
// Zero means "leave candidateKFor in charge". Only ArmProductionRetrieve
// returns ProductionRetrieveK; the default and deep page arms stay formula-only
// so the table still measures the served fetch.
func productionRetrieveFloor(arm EvalArm) int {
	if arm == ArmProductionRetrieve {
		return ProductionRetrieveK
	}
	return 0
}

// isProductionSearchArm reports whether evalCase scores arm through
// Service.Search (page-scoped production) rather than a Clone of rankRetrieved.
func isProductionSearchArm(arm EvalArm) bool {
	switch arm {
	case ArmProduction, ArmProductionDeep, ArmProductionRetrieve:
		return true
	default:
		return false
	}
}

// productionDeepLimit is the second page size the production path is measured at.
//
// ArmProduction asks for DefaultSearchLimit, which is what a caller that passes no
// limit gets. That is one page size out of the range agents actually use, and it
// is the small end: an agent that wants more context asks for ten. Whether the
// answer is THERE at ten is a production question the table could not answer,
// because every production number in it was a page of five.
//
// It is deliberately a second arm rather than a change to the first. The two
// measure different things and both are real, and the abstention gate calibrates
// on the default page — moving that would recalibrate the gate as a side effect
// of adding a row.
const productionDeepLimit = 10

// ArmProductionDeep is Service.Search at productionDeepLimit. The name carries the
// number so a row can never claim a depth it did not request.
var ArmProductionDeep = EvalArm(fmt.Sprintf("production (Search) limit=%d", productionDeepLimit))

// productionLimit is the page size an arm asks Search for.
//
// It is a function rather than a literal at each call site because that is the
// whole content of the difference between these two arms: an arm named limit=10
// that asks for five is a duplicate row wearing a misleading name, and nothing
// about the table would look wrong. TestProductionArmsAskForDifferentDepths
// drives this directly.
func productionLimit(arm EvalArm) int {
	if arm == ArmProductionDeep {
		return productionDeepLimit
	}
	return DefaultSearchLimit
}

// rerankSweep are the blend weights the eval tries alongside production, so how
// much the cross-encoder should decide is answered by measurement rather than by
// whoever last had an opinion. 1.0 is the old behaviour: the cross-encoder
// overwrites the fused order completely.
var rerankSweep = []float64{0.25, 0.5, 0.75, 1.0}

// bm25Sweep is how much the LEXICAL half counts. 0.0 is vector-only, 0.4 is the
// inherited default. It is swept because a real corpus has already shown the
// default can be worse than not fusing at all: BM25 rewards shared vocabulary,
// and in a large palace many memories share a query's words without answering it.
var bm25Sweep = []float64{0.0, 0.2, 0.4, 0.6}

// ArmAdaptive picks the lexical weight per query from how much lexical signal
// that query actually has — the alternative to a constant nobody can set right
// for every palace.
const ArmAdaptive EvalArm = "fusion bm25=auto"

// ArmAdaptiveIDF is ArmAdaptive with each query term weighted by its IDF
// instead of counted once. The candidate to replace the binary coverage: a
// term in N-1 candidates reads as signal to the binary count and as ~nothing
// to this one.
const ArmAdaptiveIDF EvalArm = "fusion bm25=auto-idf"

// recencySweep is the band widths the recency arm is measured at, as a fraction
// of fused score. A FIXED list declared here and never derived from a run: T5
// corrects its interval family-wise over the number of bands, and a k that
// depends on the data is not a k anyone can pre-register.
//
// Swept rather than picked because picking one band by hand is the
// constant-nobody-measured mistake this repo has already swept its way out of
// twice — the lexical weight and the rerank blend.
var recencySweep = []float64{0.02, 0.05, 0.10}

// recencyBandOf reports the band a recency arm was registered at, and whether
// the arm is one at all. It is the counterpart of recencyArm, kept beside it so
// the two cannot drift.
func recencyBandOf(arm EvalArm) (float64, bool) {
	for _, band := range recencySweep {
		if arm == recencyArm(band) {
			return band, true
		}
	}
	return 0, false
}

// recencyArm names a swept recency arm.
func recencyArm(band float64) EvalArm {
	return EvalArm(fmt.Sprintf("fusion+recency band=%.2f", band))
}

// bm25Arm names a swept fusion arm.
func bm25Arm(w float64) EvalArm { return EvalArm(fmt.Sprintf("fusion bm25=%.2f", w)) }

// rerankArm names a swept arm.
func rerankArm(w float64) EvalArm { return EvalArm(fmt.Sprintf("rerank blend w=%.2f", w)) }

// Eval categories, borrowed from the agent-memory benchmarks (LoCoMo and its
// descendants) because the axes they separate are the ones a single-category
// eval silently averages over.
//
// The point of the split is that a system can be excellent at one and useless at
// another: finding the note that states a fact is a different problem from
// finding the CURRENT version of a fact that was later corrected, and both are
// different from knowing when the palace simply does not hold the answer.
const (
	// CatSingle: one memory answers the question outright.
	CatSingle = "single"
	// CatCrossLingual: the question is in one language, the memory in another.
	// This palace is bilingual, and the embedder's multilingual claim has never
	// been tested on it.
	CatCrossLingual = "crosslingual"
	// CatTemporal: a fact was later corrected or superseded, and recall must
	// prefer the version that is still true.
	CatTemporal = "temporal"
	// CatReal: the query is one an agent actually ran against this palace,
	// replayed from the search_events telemetry. The gold is not a generator's
	// seed note but a judged set — every pooled candidate an LLM judge marked
	// relevant — which is what breaks the circularity of generated questions:
	// nothing about a real query was manufactured to suit any arm's feature.
	CatReal = "real"
	// CatAbsent: the palace does NOT hold the answer, and the right behaviour is
	// to return nothing. Untested until now, which means max_distance was folklore.
	CatAbsent = "absent"
	// CatFact marks a case whose gold is a kg_triple rather than a drawer. It is
	// a distinct category rather than a flag because the eval already reports per
	// category, and an average that mixed fact cases into single-hop would hide
	// exactly the thing ADR-036 is trying to see.
	CatFact = "fact"
)

// CaseSetOrigin values: whether a run replayed questions somebody saved, or
// wrote its own that nobody else will ever see.
const (
	CaseSetGenerated = "generated"
	CaseSetReplayed  = "replayed"
)

// CaseSetID is the identity of a set of questions.
//
// It is derived from the CONTENT of the cases and from nothing else — not the
// file they came from, not its path, not the order they happen to sit in.
// Reordering one set must not change it, or replaying a saved file produces a
// different id from the run that wrote it, and an id that changes on a cosmetic
// difference trains people to ignore it.
//
// It is a one-way hash, which is why it is admissible in the committed run
// record where the queries themselves are not: it identifies a case set to
// anyone who already holds it, and discloses nothing to anyone who does not.
func CaseSetID(cases []EvalCase) string {
	lines := make([]string, 0, len(cases))
	for _, c := range cases {
		// A copy, sorted: two orderings of one case's accepted answers are one
		// case. nil and an empty slice must canonicalise identically, because
		// ExpectAny is `json:",omitempty"` — an empty slice is written as absent
		// and read back nil, so any other treatment makes every replay disagree
		// with the run that saved it.
		alts := append([]string(nil), c.ExpectAny...)
		sort.Strings(alts)
		lines = append(lines, strings.Join([]string{
			c.Query, c.Expect, strings.Join(alts, "|"), c.Wing, c.category(), c.Distractor,
		}, "\x1f"))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\x1e")))
	return "cs-" + hex.EncodeToString(sum[:6])
}

// EvalCase is one labelled question: the query, the drawer that should come back
// for it, and what kind of question it is.
type EvalCase struct {
	Query  string
	Expect string // drawer id; empty for CatAbsent, where any hit is a false positive
	// ExpectAny lists drawer ids that each count as a correct answer — the
	// judged qrels of a CatReal case. A generated case has exactly one gold by
	// construction; a real query can be answered by several memories, and
	// scoring only one of them turns valid answers into retrieval errors.
	ExpectAny []string `json:",omitempty"`
	// ExpectTriple is the fact that answers this case, written canonically as
	// "subject|predicate|object" and NOT as a triple id.
	//
	// The id was the obvious choice and it is wrong: tripleID hashes
	// validFrom+recordedAt, so re-adding the same fact at a different moment
	// yields a different id. A corpus keyed on ids goes stale the first time the
	// palace is rebuilt, and it goes stale SILENTLY — every case simply starts
	// missing, which reads as the retrieval getting worse.
	//
	// Additive and `omitempty`: case files written before ADR-036 carry no such
	// key and keep loading unchanged.
	ExpectTriple string `json:",omitempty"`
	Wing         string // optional scope, mirroring how the query would really be run
	Category     string // one of the Cat* values; empty is treated as CatSingle
	// AbsentVerification records that this case's absence was CHECKED, and how.
	// Nil means it was not — which is a different fact from "checked and nothing
	// answered it", and the two are indistinguishable once written to a file
	// unless the provenance travels with the case.
	//
	// It matters because a case file merges: two runs at different depths, or one
	// before the check existed and one after, land in the same file and are then
	// read as one population. A threshold fitted across that mixture is fitted to
	// cases some of which may be answerable by a memory nobody looked for.
	AbsentVerification *AbsentVerification `json:",omitempty"`
	// Distractor is the drawer id of the version this case's gold SUPERSEDES —
	// the older, now-wrong memory that a temporal question must not surface
	// above the correction. Empty when the case has no superseded version.
	Distractor string `json:",omitempty"`
}

// AbsentVerification is the provenance of one absence check: which model
// answered, how deep it looked, and when.
//
// The DEPTH is the load-bearing field. "Nothing answers this" is only as strong
// as the search that failed to find an answer, and a case checked at depth 3 and
// one checked at depth 20 are different claims wearing the same label.
type AbsentVerification struct {
	Checker string `json:"checker"`
	Depth   int    `json:"depth"`
	At      string `json:"at"`
}

// category returns the case's category, defaulting to single-hop.
func (c EvalCase) category() string {
	if c.Category == "" {
		return CatSingle
	}
	return c.Category
}

// EvalMetrics is one arm's score over a case set.
//
// MRR is the headline: recall@1 is brittle on a small corpus and recall@5 saturates,
// while the reciprocal rank moves whenever an arm shifts the right answer up or
// down at all — which is exactly what a ranking change does.
type EvalMetrics struct {
	Arm EvalArm
	// Supersession is this arm's stale-above measurement, with the scope naming
	// the population it was taken over.
	Supersession SupersessionCell
	Cases        int
	Recall1      int
	Recall5      int
	MRR          float64
	NotFound     int // the expected drawer was not in the candidate pool at all

	// Ranks are the per-case 1-based ranks (0 = miss), aligned with the case
	// order, so intervals and paired comparisons can be computed after the fact —
	// including by a reader of the results file, not only by this process.
	Ranks []int

	// ByCategory holds the same counts per question kind, because an average over
	// categories hides the failure that matters: a system can be perfect on
	// single-hop and blind on temporal, and the mean looks fine.
	ByCategory map[string]*CategoryMetrics
}

// CategoryMetrics is one arm's record within one category.
type CategoryMetrics struct {
	Cases    int
	Recall1  int
	Recall5  int
	MRR      float64
	NotFound int
}

// Recall1Pct / Recall5Pct render the counts as percentages of cases.
func (m EvalMetrics) Recall1Pct() float64 { return pct(m.Recall1, m.Cases) }
func (m EvalMetrics) Recall5Pct() float64 { return pct(m.Recall5, m.Cases) }

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// EvalReport is the full table plus the per-case detail a human needs to tell a
// bad query from a bad ranking.
type EvalReport struct {
	Arms    []EvalMetrics
	Details []EvalCaseResult

	// CaseSetID and CaseSetOrigin identify the questions this report scores.
	// Without them a BEST label is a claim about one sample that reads as a claim
	// about the system: four runs were compared across four different question
	// sets before these existed, and nothing in any of the four tables said so.
	CaseSetID     string
	CaseSetOrigin string

	// Warnings are conditions that changed what was measured — a degraded
	// reranker, a skipped arm. They are part of the result, because a table whose
	// caveats live only in scrollback gets quoted without them.
	Warnings []string

	// PoolRanks is, per answerable case, the position of the gold memory in the
	// pool ORDERED BY VECTOR DISTANCE — the retrieval channel itself, before any
	// fusion, boost or cross-encoder touches it. Zero means the dense channel
	// never surfaced the answer at all.
	//
	// It is the ceiling every other number in the table sits under. Each arm here
	// re-orders one shared pool that only the dense channel nominates: BM25 can
	// move a candidate up, never bring one in, because there is no independent
	// lexical retrieval. So a gold that is not in this pool is unreachable for
	// every arm, and an arm's loss against another arm is a REORDERING result,
	// not a retrieval one. Published "hybrid improves recall" findings are about
	// widening the pool, which our architecture cannot do — reporting this makes
	// the difference visible instead of leaving it to be assumed either way.
	PoolRanks []int

	// GoldRerank and AbsentRerank are the top-1 CROSS-ENCODER scores for the two
	// kinds of question. They exist because the distance distributions overlap —
	// so cosine cannot answer "do I know this?" — and a cross-encoder score is
	// the one number in the pipeline that was trained on exactly that question
	// rather than on similarity.
	GoldRerank   []float64
	AbsentRerank []float64

	// GoldDistances and AbsentDistances are the top-1 cosine distances for
	// answerable and unanswerable questions. They exist because max_distance —
	// the gate that decides when the palace should admit it knows nothing — was
	// inherited folklore, and the only way to set it honestly is to look at where
	// the two distributions actually sit.
	GoldDistances   []float64
	AbsentDistances []float64
}

// EvalCaseResult is where each arm put the expected drawer for one query. Rank 0
// means "not in the pool"; a case that is 0 everywhere is a retrieval miss, not a
// ranking one, and usually means the generated question shares no vocabulary with
// its own source.
type EvalCaseResult struct {
	Query    string
	Category string
	Ranks    map[EvalArm]int

	// DistractorRanks is where the SUPERSEDED version landed, per arm, in the
	// same ordering that produced Ranks. Nil when the case names no distractor.
	DistractorRanks map[EvalArm]int
	// DistractorPoolRank is where the superseded version sat by vector distance,
	// or 0 when it never entered the pool.
	//
	// It is per CASE and not per arm on purpose: two arms may order a distractor
	// differently, but they cannot disagree about whether it was retrievable, and
	// reading each arm's own 0 as "outside the pool" is the mistake that makes a
	// vacuous case look like a success for every arm at once.
	DistractorPoolRank int
	// Population is which of the three calibration populations this case belongs
	// to: PopReachable, PopUnreachable or PopAbsent.
	//
	// It exists because PoolRank == 0 is TWO opposite facts wearing one zero. A
	// gold outside the pool is a retrieval failure no ranking arm could have
	// fixed; counting it beside cases whose gold was retrievable makes a
	// retrieval fact look like a ranking result, and a paired statistic over the
	// mixture reports a zero delta that means "nobody could have won here"
	// rather than "the arms agree".
	Population string
	// TopGap and ScoreSpread are the CONTRASTIVE shape of the served page, and
	// they exist because an absolute score is the weak family. Measured on this
	// palace: an absolute rerank score separates a wrong page from a right one at
	// 0.841 AUC and an absolute centroid distance at 0.728, while a contrastive
	// margin reaches 0.985. A similarity score has no anchored zero and its scale
	// moves with the query, which is why the query-performance-prediction family
	// is defined as gaps and spreads rather than levels.
	//
	// TopGap is the WIG shape: the top document's score minus the mean of the
	// rest of the page. ScoreSpread is the NQC shape: the spread of the page's
	// scores. Both are 0 when the page is too short to have a shape, or when no
	// reranker scored it — RerankScored says which.
	//
	// Added BESIDE TopRerank, never replacing it, so a calibration curve can be
	// fitted on each and the better one chosen by measurement.
	TopGap      float64
	ScoreSpread float64
	// DistGap and DistSpread are the same shape read from cosine DISTANCES, which
	// exist on every page. TopGap and ScoreSpread need a cross-encoder and are
	// therefore zero in the default configuration, where a report built only on
	// them prints nothing — and "prints nothing" is indistinguishable from "there
	// is no signal here".
	//
	// Named separately rather than folded into TopGap because a gap over
	// cross-encoder logits and a gap over cosine distances are different
	// quantities on different scales. One name for two facts is the defect this
	// file has already carried twice.
	//
	// Polarity is normalised: distance is lower-is-better, so a decisive page
	// yields a POSITIVE gap, the same direction as TopGap — the two never disagree
	// about what "good" means while sharing a report.
	DistGap    float64
	DistSpread float64
	// TopRerank is the production arm's cross-encoder score for the top document,
	// and RerankScored says whether a reranker actually produced it. Carried per
	// case rather than into the two flat GoldRerank/AbsentRerank arrays, which
	// lose the label the calibration curve has to group by.
	TopRerank    float64
	RerankScored bool
	// PoolRank is where the gold sat in the pool ordered by vector distance, or
	// 0 when the dense channel never surfaced it. It duplicates what
	// EvalReport.PoolRanks carries and it has to: PoolRanks skips absent cases
	// while Details holds every case, so the two cannot be aligned by index. A
	// paired statistic needs to exclude a case whose gold was never reachable —
	// a zero delta there is a retrieval fact wearing a ranking result's clothes —
	// and that exclusion is only expressible per case.
	PoolRank int
}

// The three calibration populations. A curve fitted across them without the
// label is fitted across three different questions at once.
const (
	// PopReachable: an answerable case whose gold entered the retrieved pool, so
	// every arm had the chance to rank it. These are the only cases a ranking
	// comparison may be drawn from.
	PopReachable = "reachable"
	// PopUnreachable: an answerable case whose gold never entered the pool. No
	// arm could have surfaced it; its zero is a retrieval fact, not a ranking one.
	PopUnreachable = "unreachable"
	// PopAbsent: the palace holds no answer and any hit is a false positive.
	PopAbsent = "absent"
)

// pageShape reduces a served page's scores to the two reference-free statistics
// a calibration curve can be fitted on without knowing the right answer.
//
// gap is the top score minus the mean of the REST of the page (the WIG shape):
// how far the winner stands above the field. spread is the population standard
// deviation of the whole page (the NQC shape): whether the page discriminates at
// all or the scores are flat.
//
// Both are differences rather than levels, which is the point. A page of five
// mediocre-but-similar scores and a page with one clear winner can share a top
// score, and only the shape tells them apart.
//
// A page shorter than two has no shape and returns zeros — reported honestly
// rather than as a small number, because a fabricated gap would be a confident
// value on no evidence.
func pageShape(scores []float64) (gap, spread float64) {
	if len(scores) < 2 {
		return 0, 0
	}
	var restSum float64
	for _, v := range scores[1:] {
		restSum += v
	}
	gap = scores[0] - restSum/float64(len(scores)-1)

	var sum float64
	for _, v := range scores {
		sum += v
	}
	mean := sum / float64(len(scores))
	var sq float64
	for _, v := range scores {
		d := v - mean
		sq += d * d
	}
	return gap, math.Sqrt(sq / float64(len(scores)))
}

// populationOf labels a case with the calibration population it belongs to.
//
// The decision lives in a function rather than inline so a test can drive it,
// and it is deliberately total: every case gets exactly one label, because a
// case with no label silently drops out of whichever group a later grouping
// forgets to handle.
//
// An absent case has no gold, so its zero PoolRank says nothing about retrieval
// and the category alone decides. For an answerable case the zero is the whole
// question: gold in the pool means every arm had its chance, gold outside means
// none of them did.
func populationOf(cat string, poolRank int) string {
	if cat == CatAbsent {
		return PopAbsent
	}
	if poolRank <= 0 {
		return PopUnreachable
	}
	return PopReachable
}

// Progress reports how far a run has got. An eval that prints nothing for
// several minutes is indistinguishable from one that has hung — which is exactly
// how the first one read — so Evaluate reports each case as it lands.
type Progress func(done, total int, query string, elapsed time.Duration)

// EvalOptions turn on arms that need something built first.
type EvalOptions struct {
	// Contextual adds the contextual-chunk arm. The index must already exist
	// (BuildContextualIndex); the arm is skipped rather than silently empty when
	// it does not.
	Contextual bool
	// AllowDegraded lets the run continue without the reranked arms when the
	// configured reranker fails its preflight probe. The default is to REFUSE:
	// this eval has already once produced a full table of "reranked" numbers that
	// were silently the hybrid order, and a loud stop is the only reliable cure.
	AllowDegraded bool
	// Arms restricts the run to the named arms, matched by exact name or prefix.
	// Empty runs every arm, which is the default and the right thing for a tuning
	// sweep.
	//
	// It exists because COST, not corpus size, is what stops a question being
	// asked twice. Measured 2026-08-26: 54 cases against 36 arms took ~50 minutes
	// at the shipped RERANK_POOL, and ~110 minutes at pool 20 — two runs that day
	// were abandoned unfinished, and an unfinished run yields nothing at all. A
	// question worth answering is usually a question about three or four arms, and
	// paying for the other thirty-two is what makes it unaffordable to re-ask.
	//
	// It does NOT change the retrieved pool: every arm still re-orders the same
	// candidates, so a filtered run is comparable with a full one arm-for-arm.
	// What it does change is the "vs best" baseline, which is computed among the
	// arms present — the report says so rather than leaving a reader to assume the
	// winner beat arms that never ran.
	Arms []string
	// CaseSetOrigin says whether the caller replayed saved questions or generated
	// its own. Only the caller knows; the report carries it so the table and the
	// run record cannot disagree about it.
	CaseSetOrigin string
}

// Evaluate scores every arm over the cases. poolSize is how many neighbours the
// vector search fetches per query; it bounds every arm equally, so a memory
// outside the pool is unreachable for all of them (counted as NotFound).
func (s *Service) Evaluate(ctx context.Context, teamID string, cases []EvalCase, poolSize int, progress Progress) (EvalReport, error) {
	return s.EvaluateWith(ctx, teamID, cases, poolSize, EvalOptions{}, progress)
}

// EvaluateWith is Evaluate with the optional arms.
func (s *Service) EvaluateWith(ctx context.Context, teamID string, cases []EvalCase, poolSize int, opts EvalOptions, progress Progress) (EvalReport, error) {
	if poolSize <= 0 {
		poolSize = 50
	}
	report := EvalReport{CaseSetID: CaseSetID(cases), CaseSetOrigin: opts.CaseSetOrigin}

	// Preflight the reranker with ONE probe before scoring hundreds of cases
	// against it. A dead reranker degrades every reranked arm to the hybrid order
	// SILENTLY — this exact table has been published with that failure in it — so
	// the default is to stop and say what is wrong.
	if s.rerank != nil {
		// The probe mirrors a real call — pool-sized batch, chunk-sized documents —
		// because a token-sized probe has already passed while every real call
		// failed on the batch and sequence limits it never touched.
		probeDocs := make([]string, s.rerankPool)
		probeText := strings.Repeat("preflight document text sized like a real drawer chunk. ", 30)
		for i := range probeDocs {
			probeDocs[i] = probeText
		}
		if _, err := s.rerank.Rerank(ctx, "preflight probe", probeDocs); err != nil {
			if !opts.AllowDegraded {
				return EvalReport{}, fmt.Errorf(
					"the configured reranker failed its preflight (%w) — every reranked arm would silently score as plain hybrid, "+
						"which has already produced one wrong table too many. Fix it, or pass --allow-degraded to run without those arms", err)
			}
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("reranker preflight failed (%v): reranked arms were DROPPED, not silently degraded", err))
			s = s.withoutReranker()
		}
	}

	arms := evalArms(opts, s.rerank != nil)
	if len(opts.Arms) > 0 {
		kept, dropped := selectArms(arms, opts.Arms)
		// REFUSE a filter that matched nothing rather than running zero arms and
		// reporting an empty table. This repository's oldest lesson is that a
		// filter matching nothing exits 0 with a cheerful summary, which is how
		// every TDD task once passed its own gate with none of the work done.
		if len(kept) == 0 {
			return EvalReport{}, fmt.Errorf("--arms %q matched none of the %d available arms; "+
				"a filter that selects nothing must refuse rather than report an empty table",
				strings.Join(opts.Arms, ","), len(arms))
		}
		// Say what was dropped. Silent truncation reads as "covered everything".
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"--arms selected %d of %d arms (%d not run); the 'vs best' baseline is the best of "+
				"those %d, not of every arm", len(kept), len(arms), dropped, len(kept)))
		arms = kept
	}
	byArm := map[EvalArm]*EvalMetrics{}
	for _, a := range arms {
		byArm[a] = &EvalMetrics{Arm: a, ByCategory: map[string]*CategoryMetrics{}}
	}

	degradedCases := 0
	for i, c := range cases {
		started := time.Now()
		oc, err := s.evalCaseResult(ctx, teamID, c, arms, poolSize)
		if err != nil {
			return EvalReport{}, err
		}
		ranks, topDistance, topRerank := oc.Ranks, oc.TopDistance, oc.TopRerank
		scored, poolRank, degraded := oc.RerankScored, oc.PoolRank, oc.Degraded
		if degraded {
			degradedCases++
		}
		if progress != nil {
			progress(i+1, len(cases), c.Query, time.Since(started))
		}
		cat := c.category()
		report.Details = append(report.Details, EvalCaseResult{
			Query: c.Query, Category: cat, Ranks: ranks, PoolRank: poolRank,
			Population: populationOf(cat, poolRank),
			TopRerank:  topRerank, RerankScored: scored,
			TopGap: oc.TopGap, ScoreSpread: oc.ScoreSpread,
			DistGap: oc.DistGap, DistSpread: oc.DistSpread,
		})
		s.accumulate(byArm, &report, EvalCaseResult{Category: cat, Ranks: ranks}, arms)
		if cat != CatAbsent {
			report.PoolRanks = append(report.PoolRanks, poolRank)
		}
		if cat == CatAbsent {
			if topDistance >= 0 {
				report.AbsentDistances = append(report.AbsentDistances, topDistance)
			}
			if scored {
				report.AbsentRerank = append(report.AbsentRerank, topRerank)
			}
		} else {
			if topDistance >= 0 {
				report.GoldDistances = append(report.GoldDistances, topDistance)
			}
			if scored {
				report.GoldRerank = append(report.GoldRerank, topRerank)
			}
		}
	}
	if degradedCases > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"the reranker failed mid-run on %d of %d case(s): reranked arms scored the HYBRID order there — treat their numbers as a floor, not a measurement", degradedCases, len(cases)))
	}
	for _, a := range arms {
		m := byArm[a]
		if m.Cases > 0 {
			m.MRR /= float64(m.Cases)
		}
		for _, cm := range m.ByCategory {
			if cm.Cases > 0 {
				cm.MRR /= float64(cm.Cases)
			}
		}
		report.Arms = append(report.Arms, *m)
	}
	fillSupersession(&report)
	return report, nil
}

// anchoredNorms are the lexical normalisers the eval compares against page-max,
// each paired with the label that appears in the arm's name.
//
// It is a slice rather than a map so the table's column order is stable: a run
// taken today has to be comparable with one taken next month, and map iteration
// order is not.
var anchoredNorms = []struct {
	name string
	norm lexNorm
}{
	{"ceiling", lexNormCeiling},
	{"saturating", lexNormSaturating},
}

// anchoredArm names the anchored counterpart of a fusion arm. The suffix keeps
// the pair adjacent when the table is read top to bottom, because the comparison
// a reader wants is always base against anchored, never anchored against
// anchored.
func anchoredArm(base EvalArm, norm string) EvalArm {
	return EvalArm(string(base) + " anchored:" + norm)
}

func parseAnchored(arm EvalArm) (EvalArm, string, bool) {
	const sep = " anchored:"
	name := string(arm)
	i := strings.Index(name, sep)
	if i < 0 {
		return "", "", false
	}
	return EvalArm(name[:i]), name[i+len(sep):], true
}

// serviceForArm returns a Clone whose ranking knobs reconstruct arm, so the
// arm is scored by rankRetrieved rather than a parallel ranker. Production
// and contextual arms retrieve on a different path and return nil.
func (s *Service) serviceForArm(arm EvalArm) *Service {
	if isProductionSearchArm(arm) || arm == ArmContextual || arm == ArmFactRetrieval {
		return nil
	}
	c := s.Clone()
	c.fusionRRF = false
	c.bm25Auto = false
	c.bm25IDF = false
	c.bm25Base = hybridBM25Weight
	c.lexNorm = lexNormPageMax
	c.lexNormName = DefaultLexNorm
	c.closetBoostScale = 0
	c.rerankWeight = 0
	c.recencyBand = 0
	// rerankNorm is deliberately NOT reset here. The first fix for the min-max
	// control (e20890e) set it to min-max for EVERY arm, which cured `rrf+rerank`
	// and broke the production-shaped ones in the same stroke: `hybrid+rerank` is
	// documented as the closet-OFF reranked arm production actually serves, and
	// `rerank blend w=*` exists to sweep the WEIGHT — forcing min-max on them
	// measured a normaliser production does not run, which is the very defect the
	// fix was for, one arm family over.
	//
	// So the rule is per-arm and stated at each case: an arm that NAMES a
	// normaliser sets it, `rrf+rerank` sets min-max because every table in this
	// corpus reads it as the min-max control, and a production-shaped arm inherits
	// the served value BECAUSE that is what makes it production-shaped.

	if band, ok := recencyBandOf(arm); ok {
		return c.WithBM25Weight(false, hybridBM25Weight).WithRecencyBand(band)
	}
	if base, norm, ok := parseAnchored(arm); ok {
		inner := c.serviceForArm(base)
		if inner == nil {
			return nil
		}
		return inner.WithLexNorm(norm)
	}

	weight := s.servedRerankWeight()
	switch arm {
	case ArmVector:
		return c.WithBM25Weight(false, 0)
	case ArmHybrid:
		return c.WithBM25Weight(false, hybridBM25Weight)
	case ArmHybridCloset:
		return c.WithBM25Weight(false, hybridBM25Weight).WithClosetBoost(1)
	case ArmHybridRerank:
		return c.WithBM25Weight(false, hybridBM25Weight).WithRerankWeight(weight)
	case ArmReranked:
		return c.WithBM25Weight(false, hybridBM25Weight).WithClosetBoost(1).WithRerankWeight(weight)
	case ArmRRF:
		return c.WithFusion("rrf")
	case ArmBlendSigmoid:
		return c.WithFusion("rrf").WithRerankWeight(weight).WithRerankNorm(RerankNormSigmoid)
	case ArmBlendRank:
		return c.WithFusion("rrf").WithRerankWeight(weight).WithRerankNorm(RerankNormRank)
	case ArmRRFReranked:
		// The min-max control, named explicitly. Left to inherit it became a second
		// sigmoid arm whenever the server ran sigmoid — two bit-identical rows that
		// were written up as "sigmoid scores identically to min-max" from a
		// comparison in which min-max never ran.
		return c.WithFusion("rrf").WithRerankWeight(weight).WithRerankNorm(RerankNormMinMax)
	case ArmAdaptive:
		return c.WithBM25Weight(true, hybridBM25Weight)
	case ArmAdaptiveIDF:
		return c.WithBM25Weight(true, hybridBM25Weight).WithLexicalIDF(true)
	}
	for _, w := range bm25Sweep {
		if arm == bm25Arm(w) {
			return c.WithBM25Weight(false, w)
		}
	}
	for _, w := range rerankSweep {
		if arm == rerankArm(w) {
			return c.WithBM25Weight(false, hybridBM25Weight).WithRerankWeight(w)
		}
	}
	return nil
}

func (s *Service) servedRerankWeight() float64 {
	if s.rerankWeight > 0 {
		return s.rerankWeight
	}
	return DefaultRerankWeight
}

// fusionRankerFor turns an arm name into the ranker that scores it, or nil for
// arms that are not score fusion at all — vector, RRF, contextual, production
// and the reranked family, each of which evalCase handles on its own.
//
// It is a test seam over serviceForArm, not a second mapping: an arm's ranking
// is whatever fusionRanker the reconstructed service runs.
func fusionRankerFor(arm EvalArm, base float64) func(query string, docs []string, dists, boosts []float64) []HybridScore {
	if !armIsScoreFusion(arm) {
		return nil
	}
	seed := NewService(nil, nil, nil, 0)
	seed.bm25Base = base
	svc := seed.serviceForArm(arm)
	if svc == nil {
		return nil
	}
	return svc.fusionRanker()
}

func armIsScoreFusion(arm EvalArm) bool {
	switch arm {
	case ArmVector, ArmRRF, ArmRRFReranked, ArmContextual, ArmProduction, ArmProductionDeep,
		ArmProductionRetrieve, ArmHybridRerank, ArmReranked:
		return false
	}
	for _, w := range rerankSweep {
		if arm == rerankArm(w) {
			return false
		}
	}
	if _, ok := recencyBandOf(arm); ok {
		return false
	}
	return true
}

// evalArms is the registry: every arm a run can score, in table order.
//
// It is a function rather than an expression inside EvaluateWith so a test can
// enumerate the same list the run uses. TestEveryDeclaredArmIsRegistered parses
// this function looking for the appends, so an arm added anywhere else is
// reported as unreachable — which is the whole point, since an arm that is
// declared and never appended appears in no table and nothing else notices.
func evalArms(opts EvalOptions, rerank bool) []EvalArm {
	arms := []EvalArm{ArmVector, ArmHybrid, ArmHybridCloset, ArmRRF}
	for _, w := range bm25Sweep {
		arms = append(arms, bm25Arm(w))
	}
	arms = append(arms, ArmAdaptive, ArmAdaptiveIDF)
	// The recency arms: the cheap fix the knowledge graph has to beat. If a
	// stable tie-break on content date already fixes supersession, a graph is a
	// large answer to a small question.
	for _, band := range recencySweep {
		arms = append(arms, recencyArm(band))
	}
	// The anchored counterparts, one family and unboosted. ADR-002 T2 originally
	// called for a boosted family plus a no-closet control; ADR-003 T1 made the
	// closet prior opt-in by name and put closet variants of the sweep arms
	// permanently out of scope, which removes the confound the control existed
	// for. Weight zero is skipped: the lexical term is multiplied away there, so
	// the divisor cannot change the order and the row would duplicate its own
	// counterpart while reading as a finding.
	for _, n := range anchoredNorms {
		for _, w := range bm25Sweep {
			if w == 0 {
				continue
			}
			arms = append(arms, anchoredArm(bm25Arm(w), n.name))
		}
		arms = append(arms, anchoredArm(ArmAdaptive, n.name), anchoredArm(ArmAdaptiveIDF, n.name))
	}
	// The reality check goes after every FUSION arm: this is the arm that
	// exercises the code agents actually call, so it reads as the verdict on the
	// rows above it. (It is not literally last — the contextual and reranked
	// families follow — and the comment used to claim it was, which
	// TestEvalArmsKeepProductionLast turned up while pinning the order.) It went
	// missing once already — built, documented, and never appended — which an
	// adversarial review caught and no table did.
	// The blend-normalisation arms go in with the other pool arms, BEFORE the
	// production arms: TestEvalArmsKeepProductionLast requires the arm that scores
	// the served path to be last in the table, so a reader comparing rows always
	// finds production at the bottom.
	if rerank {
		arms = append(arms, ArmBlendSigmoid, ArmBlendRank)
	}
	arms = append(arms, ArmProduction, ArmProductionDeep, ArmProductionRetrieve)
	// The fact arm goes after production and does not disturb it: it is not a
	// fusion arm, so TestEvalArmsKeepProductionLast — which forbids a FUSION arm
	// after production — is unaffected. This append is the line that makes the
	// arm reachable; TestEveryDeclaredArmIsRegistered fails if it is deleted.
	arms = append(arms, ArmFactRetrieval)
	if opts.Contextual {
		arms = append(arms, ArmContextual)
	}
	if rerank {
		arms = append(arms, ArmRRFReranked)
		arms = append(arms, ArmHybridRerank)
		arms = append(arms, ArmReranked)
		for _, w := range rerankSweep {
			arms = append(arms, rerankArm(w))
		}
	}
	return arms
}

// armBoosts decides whether an arm carries the closet curation prior, and the
// rule is simply its name: an arm that does not say "closet" must not have one.
//
// The alternative is what this replaced. One boosts slice was built per case and
// handed to every arm, so twelve arms whose names promise a pure ranking
// comparison were quietly measuring a curation prior as well — and a conclusion
// about the lexical weight was then read off that table. An arm's name is the
// only thing a reader of the table has; it must be the whole truth about what
// went into the number.
func armBoosts(arm EvalArm, closet []float64) []float64 {
	switch arm {
	case ArmHybridCloset, ArmReranked:
		return closet
	default:
		return nil
	}
}

// SupersessionScope names the population an arm's supersession number was
// measured over. Three arms answer different questions by construction, and
// printing their numbers in one column would be the same error as reading an
// arm's zero as "outside the pool".
type SupersessionScope string

const (
	// ScopePool: the arm re-orders the shared candidate set, so every pooled
	// candidate is in its ordering.
	ScopePool SupersessionScope = "pool"
	// ScopePage: the arm is scored over the page Search actually returns —
	// at most DefaultSearchLimit long, after the distance gate — so "the
	// distractor was not above the gold" can mean "it was not on the page".
	ScopePage SupersessionScope = "page"
	// ScopeOwnIndex: the arm retrieves from its own namespace, so its pool is
	// not the shared one at all.
	ScopeOwnIndex SupersessionScope = "own-index"
)

// ArmScope classifies an arm by the population its ranks — and therefore its
// NotFound count — are taken over. An arm this switch does not name returns the
// EMPTY scope, which TestSupersessionRanksScopePerArm rejects.
//
// That default is the whole point and it used to be `ScopePool`. The doc comment
// then claimed the function was "exhaustive by construction: a new arm with no
// scope fails TestSupersessionRanksScopePerArm" — and the test's check is
// `if ArmScope(arm) == ""`, which the default made unreachable for every possible
// input. A new arm silently inherited ScopePool, so a page-scoped one would have
// had its NotFound count summed with pool-scoped arms: exactly the
// across-populations aggregation ADR-007 exists to stop. The claim was false, the
// gate behind it had never been able to fail, and a different-lineage reviewer
// found it by reading the default rather than the promise.
//
// Every arm is named explicitly below for the same reason — a reader can see the
// classification without inferring it from what is missing.
//
// Exported because the scope is not only a supersession concern. Any reader that
// aggregates NotFound across arms is summing different populations unless it
// filters on this: a gold at pool rank 12 is a miss for a ScopePage arm and a hit
// for every ScopePool one, and calling that a retrieval failure sends the reader
// after the embedding when nothing was ever missing from the pool.
func ArmScope(arm EvalArm) SupersessionScope {
	if isProductionSearchArm(arm) {
		return ScopePage
	}
	switch arm {
	case ArmContextual, ArmFactRetrieval:
		// ArmFactRetrieval scores kg_triples, not drawers, so its population is
		// disjoint from every pool arm's. Printing it in the pool column would
		// compare a fact miss with a drawer miss as if they were the same event.
		return ScopeOwnIndex
	case ArmVector, ArmHybrid, ArmHybridCloset, ArmHybridRerank, ArmReranked,
		ArmRRF, ArmRRFReranked, ArmAdaptive, ArmAdaptiveIDF,
		ArmBlendSigmoid, ArmBlendRank:
		return ScopePool
	}
	// The swept families are minted at run time — bm25Arm, rerankArm and
	// recencyArm build their names with fmt.Sprintf — so a case list cannot name
	// them. They all re-rank the SHARED pool, which is what the scope is about.
	//
	// This branch is why the empty default matters. While the fallback was
	// ScopePool these arms were never classified at all; they merely landed on the
	// right answer, and so would a page-scoped arm added tomorrow.
	for _, family := range sweptArmPrefixes {
		if strings.HasPrefix(string(arm), family) {
			return ScopePool
		}
	}
	// Unclassified. Not a scope — the absence of one, so it is visible rather than
	// absorbed into whichever value happened to be the fallback.
	return ""
}

// sweptArmPrefixes are the run-time-generated arm families, each pinned to the
// function that mints it so a renamed format string is caught by
// TestSweptArmPrefixesMatchWhatMintsThem rather than silently unclassifying a
// whole family.
var sweptArmPrefixes = []string{"fusion bm25=", "rerank blend w=", "fusion+recency band="}

// caseOutcome is everything one case produced. It is a struct because evalCase
// returned seven values including two bools and this task needed two more; a
// review asked for the struct on the grounds that it makes the next return value
// free, which it now is.
type caseOutcome struct {
	Ranks              map[EvalArm]int
	DistractorRanks    map[EvalArm]int
	TopDistance        float64
	TopRerank          float64
	TopGap             float64
	ScoreSpread        float64
	DistGap            float64
	DistSpread         float64
	RerankScored       bool
	PoolRank           int
	DistractorPoolRank int
	Degraded           bool
}

// evalCase runs one query through every arm and returns the 1-based rank of the
// expected drawer per arm (0 = absent).
//
// The rerankScored return says whether a cross-encoder actually scored the top
// candidate. It is a separate boolean rather than a test on the score, because
// the score's zero is only "unscored" by convention: a sigmoid backend never
// emits exactly 0, but a logit backend can, and the abstention data must not
// quietly drop the case that lands there.
func (s *Service) evalCaseResult(ctx context.Context, teamID string, c EvalCase, arms []EvalArm, poolSize int) (caseOutcome, error) {
	caseCtx, caseSpan := telemetry.Start(ctx, telemetry.StageEvalCase, attribute.Int("am.arms", len(arms)))
	caseOut := telemetry.Ran
	defer func() { caseSpan.End(caseOut) }()

	embedCtx, embedSpan := telemetry.Start(caseCtx, telemetry.StageEmbed)
	vec, err := s.embed.EmbedOne(embedCtx, c.Query)
	if err != nil {
		embedSpan.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
		caseOut = telemetry.FailedClosed
		return caseOutcome{TopDistance: -1}, fmt.Errorf("embed eval query: %w", err)
	}
	embedSpan.End(telemetry.Ran, attribute.Int("am.dim", len(vec)))
	hits, rows, stale, err := s.searchCandidates(caseCtx, teamID, SearchQuery{Wing: c.Wing}, vec, poolSize)
	if err != nil {
		caseOut = telemetry.FailedClosed
		return caseOutcome{TopDistance: -1}, fmt.Errorf("eval candidate pool: %w", err)
	}

	// The gold is a MEMORY, not a chunk of one.
	//
	// A long memory is stored as several chunks and any of them answers the
	// question as far as the agent is concerned — it reads the memory, not the
	// slice. Scoring only the exact chunk marks a correct retrieval as a miss, and
	// unevenly: an arm that changes WHICH chunk surfaces (contextual chunking, for
	// one) is penalised precisely for doing its job. In this corpus every sampled
	// gold was a chunk of a multi-chunk memory, so the bias applied to every
	// number measured before this.
	// memoryOf resolves a drawer id to the id the POOL is keyed by. The pool
	// stores p.memory, which is the parent for a chunk of a multi-chunk memory,
	// so an unresolved drawer id matches nothing and scores as never-retrieved.
	// The gold has always gone through this; the distractor must too, or every
	// multi-chunk distractor looks unreachable, Vacuous inflates, and every
	// stale-above rate comes out better than it is — with nothing failing.
	// Resolves an id to the memory it belongs to. The FOLDING itself lives in
	// memoryOf (palace.go) — it was written out by hand in four places in this
	// file and nowhere in the pipeline, which is how the eval came to score
	// memories while Search returned chunks.
	memoryOfID := func(id string) (string, bool) {
		if id == "" {
			return "", false
		}
		switch d, err := s.repo.Get(ctx, teamID, id); {
		case err == nil:
			return memoryOf(d), true
		default:
			return "", false
		}
	}

	distractorSet := map[string]bool{}
	if m, ok := memoryOfID(c.Distractor); ok {
		distractorSet[m] = true
	}

	goldSet := make(map[string]bool, 1+len(c.ExpectAny))
	for _, id := range append([]string{c.Expect}, c.ExpectAny...) {
		if id == "" { // CatAbsent cases have no gold to resolve
			continue
		}
		switch gold, err := s.repo.Get(ctx, teamID, id); {
		case err == nil:
			goldSet[memoryOf(gold)] = true
		case errors.Is(err, gorm.ErrRecordNotFound):
			// A saved case can outlive its drawer: re-mining a source purges the
			// old ids and mints new ones. Swallowing that scored the dead case as
			// an all-arm retrieval miss — and the pool diagnosis then told the
			// operator to raise --pool, misdiagnosing stale case data as a
			// retrieval failure. Found by adversarial review, minutes after a
			// full re-mine had made it live.
			caseOut = telemetry.FailedClosed
			return caseOutcome{TopDistance: -1}, fmt.Errorf(
				"eval case %q: its expected drawer %s no longer exists — the corpus was re-mined since this case file was generated; regenerate the cases", c.Query, id)
		default:
			caseOut = telemetry.FailedClosed
			return caseOutcome{TopDistance: -1}, fmt.Errorf("load eval gold %s: %w", id, err)
		}
	}

	// One candidate list, ordered by vector distance — the input every arm
	// re-orders. Building it once is what makes the comparison fair.
	type candidate struct {
		memory   string
		distance float64
	}
	var pool []candidate
	for _, h := range hits {
		d, ok := rows[h.ID]
		if !ok {
			continue
		}
		pool = append(pool, candidate{memory: memoryOf(d), distance: distanceFromScore(h.Score)})
	}

	// The nearest candidate's distance, whatever the arm: it is what a
	// max_distance gate would see, and the absent-case measurement needs it.
	topDistance := -1.0
	for _, p := range pool {
		if topDistance < 0 || p.distance < topDistance {
			topDistance = p.distance
		}
	}

	out := map[EvalArm]int{}
	distractorOut := map[EvalArm]int{}
	var distractorPoolRank int
	rerankFailed := false
	// The abstention gate's calibration data, taken from the production arm and
	// nowhere else.
	// Keyed by arm, deliberately, rather than a pair of variables the loop
	// overwrites. Two arms now run Service.Search at different page sizes, and
	// with last-write-wins the abstention gate would calibrate on whichever ran
	// last — a value that changes if someone reorders the arms list, with nothing
	// to notice. Keying it means the deeper arm CANNOT overwrite the default
	// page's number: there is no guard to forget, because there is no shared slot.
	type prodTop struct {
		rerank     float64
		gap        float64
		spread     float64
		distGap    float64
		distSpread float64
		scored     bool
	}
	prodTops := map[EvalArm]prodTop{}

	scorePage := func(page []SearchHit, expect, distract map[string]bool) (int, int) {
		ids := make([]string, len(page))
		order := make([]int, len(page))
		for i, h := range page {
			ids[i] = memoryOf(h.Drawer)
			order[i] = i
		}
		goldRank := rankOf(ids, order, expect)
		distRank := 0
		if len(distract) > 0 {
			distRank = rankOf(ids, order, distract)
		}
		return goldRank, distRank
	}

	// Where the gold sits in the RETRIEVAL channel's own ordering, before any
	// arm re-orders it. This is the ceiling every arm plays under.
	poolRank := 0
	{
		byDistance := make([]int, len(pool))
		for i := range byDistance {
			byDistance[i] = i
		}
		sort.SliceStable(byDistance, func(a, b int) bool { return pool[byDistance[a]].distance < pool[byDistance[b]].distance })
		poolIDs := make([]string, len(pool))
		for i, p := range pool {
			poolIDs[i] = p.memory
		}
		poolRank = rankOf(poolIDs, byDistance, goldSet)
		if len(distractorSet) > 0 {
			// Once per CASE, from the same dense ordering: whether the superseded
			// version was retrievable at all is not something two arms can disagree
			// about, and reading it per arm makes a vacuous case look like a
			// success for every arm at once.
			distractorPoolRank = rankOf(poolIDs, byDistance, distractorSet)
		}
	}
	poolQuery := SearchQuery{
		Query: c.Query, Wing: c.Wing, Limit: len(hits), SkipTelemetry: true,
	}
	for _, arm := range arms {
		armCtx, armSpan := telemetry.Start(caseCtx, telemetry.StageEvalArm, attribute.String("am.arm", string(arm)))
		switch {
		case isProductionSearchArm(arm):
			page, err := s.Search(armCtx, teamID, SearchQuery{
				Query: c.Query, Wing: c.Wing, Limit: productionLimit(arm),
				RetrieveK:   productionRetrieveFloor(arm),
				MaxDistance: DefaultMaxDistance, SkipTelemetry: true,
			})
			if err != nil {
				armSpan.End(telemetry.FailedOpen, telemetry.AttrReason(telemetry.ReasonError))
				break
			}
			if len(page) > 0 {
				scores := make([]float64, len(page))
				dists := make([]float64, len(page))
				for i, h := range page {
					scores[i] = h.RerankScore
					dists[i] = -h.Distance
				}
				gap, spread := pageShape(scores)
				dGap, dSpread := pageShape(dists)
				prodTops[arm] = prodTop{page[0].RerankScore, gap, spread, dGap, dSpread, page[0].Reranked}
			}
			out[arm], distractorOut[arm] = scorePage(page, goldSet, distractorSet)
			armSpan.End(telemetry.Ran, attribute.Int("am.count", len(page)))
		case arm == ArmFactRetrieval:
			// Without this branch the arm falls to `default`, where serviceForArm
			// returns nil and the case is BYPASSED — so the arm appears in every
			// table, passes every registration gate, and can never produce a
			// number. That is this repository's characteristic defect and T1
			// shipped with it until a cross-check ran the arm rather than
			// reading it.
			// `rows` is the candidate pool this case already hydrated, so the arm
			// sees BOTH vocabularies exactly as production does. Passing nil
			// scored the vector vocabulary alone: T4's on/off comparison could
			// not run through the arm at all, and the first real answerable-rate
			// would have understated the served path — a measurement biased
			// against the very feature it exists to judge.
			//
			// It is the POOL rather than the page because this arm produces no
			// drawer page of its own; production reads the ranked page, which is
			// a subset, so if the two ever diverge the arm is the more generous
			// of the pair and the direction of that bias is recorded here.
			block, err := s.factsFor(armCtx, teamID, c.Wing, vec, rows)
			if err != nil {
				armSpan.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
				caseOut = telemetry.FailedClosed
				return caseOutcome{TopDistance: -1}, fmt.Errorf("fact retrieval: %w", err)
			}
			out[arm] = rankOfFact(block.Facts, c.ExpectTriple)
			armSpan.End(telemetry.Ran, attribute.Int("am.count", len(block.Facts)))
		case arm == ArmContextual:
			ctxRes, err := s.vectors.Search(armCtx, contextualNamespace(teamID), vec, poolSize, searchFilter(SearchQuery{Wing: c.Wing}))
			if err != nil {
				armSpan.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
				caseOut = telemetry.FailedClosed
				return caseOutcome{TopDistance: -1}, fmt.Errorf("contextual index search: %w", err)
			}
			ctxHits := ctxRes.H
			if len(ctxHits) == 0 {
				armSpan.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonEmpty))
				break
			}
			ctxIDs := make([]string, len(ctxHits))
			for i, h := range ctxHits {
				ctxIDs[i] = h.ID
			}
			ctxRows, err := s.repo.GetMany(armCtx, teamID, ctxIDs)
			if err != nil {
				armSpan.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
				caseOut = telemetry.FailedClosed
				return caseOutcome{TopDistance: -1}, fmt.Errorf("load contextual candidates: %w", err)
			}
			hybrid := s.serviceForArm(ArmHybrid)
			page, _, _, err := hybrid.rankRetrieved(armCtx, teamID, c.Query, SearchQuery{
				Query: c.Query, Wing: c.Wing, Limit: len(ctxHits), SkipTelemetry: true,
			}, vec, ctxHits, ctxRows, ctxRes.StaleIndex)
			if err != nil {
				armSpan.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
				caseOut = telemetry.FailedClosed
				return caseOutcome{TopDistance: -1}, err
			}
			out[arm], _ = scorePage(page, goldSet, nil)
			armSpan.End(telemetry.Ran, attribute.Int("am.count", len(page)))
		default:
			svc := s.serviceForArm(arm)
			if svc == nil {
				armSpan.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonOff))
				break
			}
			page, reranked, _, err := svc.rankRetrieved(armCtx, teamID, c.Query, poolQuery, vec, hits, rows, stale)
			if err != nil {
				armSpan.End(telemetry.FailedOpen, telemetry.AttrReason(telemetry.ReasonError))
				break
			}
			if svc.rerank != nil && svc.rerankWeight > 0 && !reranked {
				rerankFailed = true
			}
			out[arm], distractorOut[arm] = scorePage(page, goldSet, distractorSet)
			armSpan.End(telemetry.Ran, attribute.Int("am.count", len(page)))
		}
	}
	// Production's score, or nothing. There is deliberately no fallback to the
	// fixed-weight reranked arm: substituting it would refill the distribution
	// with the very mismatch this measurement exists to avoid — that arm blends
	// at a constant weight and can top out on a different document. A case where
	// production returned no scored hit contributes nothing, which is honest.
	return caseOutcome{
		Ranks: out, DistractorRanks: distractorOut,
		// The DEFAULT page is what the abstention gate will run on in production,
		// so it is what the gate's calibration data has to come from. The deeper
		// arm sees a wider candidate pool and its top-1 can be a document the
		// default page never had.
		TopDistance: topDistance,
		TopRerank:   prodTops[ArmProduction].rerank, RerankScored: prodTops[ArmProduction].scored,
		TopGap: prodTops[ArmProduction].gap, ScoreSpread: prodTops[ArmProduction].spread,
		DistGap: prodTops[ArmProduction].distGap, DistSpread: prodTops[ArmProduction].distSpread,
		PoolRank: poolRank, DistractorPoolRank: distractorPoolRank, Degraded: rerankFailed,
	}, nil
}

// rankOf returns the 1-based position of the expected id in an ordering, or 0
// when it is absent — which is the signal that the memory never made the pool,
// a retrieval miss rather than a ranking one.
func rankOf(ids []string, ordered []int, expect map[string]bool) int {
	if len(expect) == 0 {
		return 0
	}
	// ids are MEMORY ids, so several candidates can carry the same one (sibling
	// chunks). The rank that matters is the position the agent SEES the answer at,
	// and since ADR-013 the served page collapses sibling chunks — so a memory
	// occupies one slot however many of its chunks matched.
	//
	// Counting raw positions therefore overstated the rank of everything below a
	// chunked memory: two chunks of an irrelevant memory above the gold pushed the
	// gold to "rank 3" while the page put it in slot 2. The eval folded onto
	// memories BEFORE ranking and then counted chunk positions, which is the same
	// unit mismatch one level down from the one ADR-013 removed. Found by a
	// different-lineage reviewer reading the two folds against each other.
	seen := make(map[string]bool, len(ordered))
	rank := 0
	for _, idx := range ordered {
		if idx < 0 || idx >= len(ids) {
			continue
		}
		if seen[ids[idx]] {
			continue // a sibling chunk of a memory already counted: one slot, not two
		}
		seen[ids[idx]] = true
		rank++
		// Generated cases carry a single-member set; judged real-query cases carry
		// every memory the judge accepted.
		if expect[ids[idx]] {
			return rank
		}
	}
	return 0
}

// SampleDrawers returns a random sample of a team's drawers for eval question
// generation. It is a thin pass-through to the repo, exposed because the eval
// command lives outside this package and must not reach into the repository.
func (s *Service) SampleDrawers(ctx context.Context, teamID, wing string, n int) ([]Drawer, error) {
	return s.repo.ListRandom(ctx, teamID, wing, n)
}

// CandidateUnion returns the union of what several rankers would surface for one
// query: the top perArm of the vector order, the fused order, the rank-fused
// order, and the cross-encoder order when one is configured.
//
// It exists because judging only what PRODUCTION returns bakes the current
// ranker's blind spots into the labels. A document today's ranking never
// surfaces can never be marked relevant, so a better ranker that does surface it
// earns no credit — the evidence is structurally incapable of selecting an
// improvement over the ranker that generated it, and more traffic only produces
// more of the same bias. Pooling the candidates of competing systems and judging
// the union blind is the standard answer (it is how TREC has built qrels for
// thirty years), and it is the difference between a case set that can rank our
// arms and one that can only confirm them.
//
// The returned drawers carry no indication of which ranker proposed them: the
// judge must not be able to infer an arm from the order, so the union is sorted
// by id rather than by anybody's score.
func (s *Service) CandidateUnion(ctx context.Context, teamID, query, wing string, perArm, poolSize int) ([]Drawer, error) {
	if perArm <= 0 {
		perArm = 5
	}
	if poolSize <= 0 {
		poolSize = DefaultRerankPool
	}
	vec, err := s.embed.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query for pooling: %w", err)
	}
	hits, rows, stale, err := s.searchCandidates(ctx, teamID, SearchQuery{Wing: wing}, vec, poolSize)
	if err != nil {
		return nil, fmt.Errorf("pool candidate search: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	picked := map[string]Drawer{}
	take := func(arm EvalArm) {
		svc := s.serviceForArm(arm)
		if svc == nil {
			return
		}
		page, _, _, err := svc.rankRetrieved(ctx, teamID, query, SearchQuery{
			Query: query, Wing: wing, Limit: perArm, SkipTelemetry: true,
		}, vec, hits, rows, stale)
		if err != nil {
			return
		}
		for _, h := range page {
			picked[h.Drawer.ID] = h.Drawer
		}
	}
	take(ArmVector)
	take(ArmHybrid)
	take(ArmRRF)
	take(ArmHybridCloset)
	if s.rerank != nil {
		take(ArmHybridRerank)
	}

	out := make([]Drawer, 0, len(picked))
	for _, d := range picked {
		out = append(out, d)
	}
	// Sorted by id: any score order would leak which ranker liked what.
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

// SampleSearchQueries exposes the telemetry sampler to the eval command, which
// lives outside this package and must not reach into the repository.
func (s *Service) SampleSearchQueries(ctx context.Context, teamID, wing string, n int) ([]string, error) {
	return s.repo.SampleSearchQueries(ctx, teamID, wing, n)
}

// DatedDrawers lists a team's drawers that carry a content date, optionally
// scoped to a wing — the population the temporal eval samples from, since a
// "later corrected" fact needs a chronology to be later than.
func (s *Service) DatedDrawers(ctx context.Context, teamID, wing string, limit int) ([]Drawer, error) {
	return s.repo.DatedDrawers(ctx, teamID, wing, limit)
}

// OlderNeighbor finds the distractor half of a temporal eval pair: the drawer
// semantically closest to d whose content date is non-empty and strictly older.
// Such a neighbour is the corpus's own record of the superseded version of d's
// fact, so a question about the fact's CURRENT state is answered correctly only
// when ranking puts d above it — which is exactly what CatTemporal measures.
//
// Hits from d's own source are skipped: chunks of one source are the same
// session split for embedding, not a fact that was later corrected, and pairing
// them would test chunk ordering rather than temporal preference. The skip
// requires a non-empty source, because a source-less drawer is a standalone
// memory (see Add) and two of them sharing "" proves nothing about their origin.
//
// Dates are normalized through findDate before comparing: Add stores
// content_date as the caller sent it, so a hand-filed drawer can carry
// "26 June 2026" — comparing that lexicographically against "2026-08-01" would
// order by spelling, not chronology. A date that does not normalize disqualifies
// the candidate; on the target it disqualifies the pair (ok=false), because the
// question "what is newer" cannot be answered about an unparseable date.
// ok=false means the corpus holds no such neighbour — the caller skips the
// drawer rather than fabricating a pair.
func (s *Service) OlderNeighbor(ctx context.Context, teamID string, d Drawer, poolSize int, maxDistance float64) (Drawer, bool, error) {
	if d.ContentDate == "" {
		// "Older than nothing" has no answer; the caller sampled the wrong
		// population (DatedDrawers is the right one), so say so rather than
		// silently returning no pair.
		return Drawer{}, false, fmt.Errorf("older neighbour of drawer %s: it has no content date, so \"older\" is undefined — sample dated drawers only", d.ID)
	}
	dDate := findDate(d.ContentDate)
	if dDate == "" {
		return Drawer{}, false, nil
	}
	if poolSize <= 0 {
		poolSize = 50
	}
	vec, err := s.embed.EmbedOne(ctx, d.Content)
	if err != nil {
		return Drawer{}, false, fmt.Errorf("embed drawer for temporal pairing: %w", err)
	}
	// The same retrieval seam evalCase uses, scoped to the drawer's own wing: a
	// superseded fact and its correction belong to one project, and a cross-wing
	// "pair" would be two projects coincidentally near in embedding space.
	res, err := s.vectors.Search(ctx, teamID, vec, poolSize, searchFilter(SearchQuery{Wing: d.Wing}))
	if err != nil {
		return Drawer{}, false, fmt.Errorf("temporal pair search: %w", err)
	}
	hits := res.H
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.GetMany(ctx, teamID, ids)
	if err != nil {
		return Drawer{}, false, fmt.Errorf("load temporal pair candidates: %w", err)
	}
	// Hits arrive best-first, so the first survivor of the filters is the
	// closest eligible neighbour.
	for _, h := range hits {
		cand, ok := rows[h.ID]
		if !ok {
			continue // orphan vector (row deleted) — skip, as search does
		}
		if cand.ID == d.ID {
			continue
		}
		if d.SourceFile != "" && cand.SourceFile == d.SourceFile {
			continue
		}
		candDate := findDate(cand.ContentDate)
		if candDate == "" || candDate >= dDate {
			continue
		}
		// The ceiling, and the only filter here that is a claim about the two
		// memories rather than about what they are not. Without it a sparse wing
		// hands the judge its least unrelated older memory and calls it a
		// supersession. 0 disables it, which is what the callers that predate it
		// pass.
		if maxDistance > 0 && distanceFromScore(h.Score) > maxDistance {
			continue
		}
		return cand, true, nil
	}
	return Drawer{}, false, nil
}

// withoutReranker returns a shallow copy of the service with the reranker
// removed, for a degraded eval run — the palace itself is untouched.
func (s *Service) withoutReranker() *Service {
	clone := *s
	clone.rerank = nil
	return &clone
}

// accumulate folds one case's ranks into the per-arm totals.
//
// Extracted so the population rule can be tested without a corpus, and because
// the rule is easy to get wrong in a way no arm's number reveals: a temporal
// case must reach ByCategory and the supersession counts but NOT the headline.
// It asks a different question and is scored against a deliberately hard
// distractor, so folding it in makes every arm look worse as more temporal cases
// are generated, with no ranking having changed — and the headline then depends
// on how many were generated rather than on how well anything ranks.
//
// Ranks follows the headline for the same reason: BootstrapMRR and PairedDelta
// resample it, and an interval taken over a different population than the point
// estimate describes neither of them.
func (s *Service) accumulate(byArm map[EvalArm]*EvalMetrics, report *EvalReport, res EvalCaseResult, arms []EvalArm) {
	cat := res.Category
	if cat == "" {
		cat = CatSingle
	}
	for _, a := range arms {
		m := byArm[a]
		if m == nil {
			continue
		}
		if m.ByCategory == nil {
			m.ByCategory = map[string]*CategoryMetrics{}
		}
		cm := m.ByCategory[cat]
		if cm == nil {
			cm = &CategoryMetrics{}
			m.ByCategory[cat] = cm
		}
		cm.Cases++

		// An absent case has no gold to rank; its distance evidence is taken once
		// per CASE by the caller, not here — appending inside this loop copied it
		// once per arm and silently weighted the separation medians.
		if cat == CatAbsent {
			continue
		}

		r := res.Ranks[a]
		if r == 0 {
			cm.NotFound++
		} else {
			cm.MRR += 1 / float64(r)
			if r == 1 {
				cm.Recall1++
			}
			if r <= 5 {
				cm.Recall5++
			}
		}

		// The headline population excludes temporal cases. They keep their
		// category row above and their supersession numbers in their own table.
		if cat == CatTemporal {
			continue
		}
		m.Cases++
		m.Ranks = append(m.Ranks, r)
		if r == 0 {
			m.NotFound++
		} else {
			m.MRR += 1 / float64(r)
			if r == 1 {
				m.Recall1++
			}
			if r <= 5 {
				m.Recall5++
			}
		}
	}
}

// selectArms keeps the arms matching any of the patterns, by exact name first
// and prefix second, preserving the declared order.
//
// Order is preserved rather than following the caller's patterns because
// TestEvalArmsKeepProductionLast requires the arm scoring the served path to be
// last in the table, and a reader comparing rows finds production at the bottom.
// Reordering here would break that quietly, in a report rather than in a test.
//
// Prefix matching is deliberate: the swept families are minted at run time
// (rerankArm, bm25Arm, recencyArm build their names with fmt.Sprintf), so
// "rerank blend" selects the whole weight sweep without naming four values that
// change whenever the sweep does.
func selectArms(all []EvalArm, patterns []string) (kept []EvalArm, dropped int) {
	want := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" {
			want[p] = true
		}
	}
	for _, a := range all {
		name := string(a)
		match := want[name]
		if !match {
			for p := range want {
				if strings.HasPrefix(name, p) {
					match = true
					break
				}
			}
		}
		if match {
			kept = append(kept, a)
		} else {
			dropped++
		}
	}
	return kept, dropped
}
