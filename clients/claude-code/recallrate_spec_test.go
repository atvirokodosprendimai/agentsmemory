package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Bindings for docs/specs/2026-08-27-recall-before-asserting.md, turned green by
// ADR-041 T1.
//
// ⚠ THE FIXTURES ARE REAL TEXT. Every assertion sentence below was lifted verbatim
// from a session transcript written before this classifier existed, then checked
// for paths and names. T1's Stop Condition names the alternative as the way to make
// F-4 unfalsifiable: a corpus assembled from text the regex was written against
// cannot fail it, and would report a perfect classifier over sentences chosen to
// match.

const fixtures = "testdata/transcripts"

// knownAssertions are the fixture sentences, verbatim. If the classifier stops
// matching these it has stopped working, whatever rate it reports.
var knownAssertions = []string{
	"`closetDistanceCap = 1.5` is still live in main, with the flat +0.40.",
	"Root cause: `repo.Get` returns `gorm.ErrRecordNotFound`, eval checks `palace.ErrNotFound` — loud path never fires.",
	"`Stop` is not session end — that's my sloppy wording, twice.",
}

func TestF1RecallRateIsCountedFromTranscripts(t *testing.T) {
	// F-1: the rate comes from the transcript, not from what an agent says about
	// itself. ADR-017: asked whether it would have complied, the probe said
	// "likely yes", which is what the question selects for.
	obs, ok := Observe(filepath.Join(fixtures, "recalled.jsonl"))
	if !ok {
		t.Fatal("a readable transcript produced no observation")
	}
	if obs.SessionID == "" {
		t.Error("the observation carries no session id, so two runs cannot be told apart")
	}
	if obs.Assertions == 0 {
		t.Error("counted nothing in a transcript that contains assertions")
	}
}

func TestF2TheCountableUnitIsANoChangeAssertion(t *testing.T) {
	// F-2 / UC1-S1: an assertion with no preceding recall is one miss.
	obs, ok := Observe(filepath.Join(fixtures, "unrecalled.jsonl"))
	if !ok {
		t.Fatal("fixture unreadable")
	}
	if obs.Assertions != 1 {
		t.Errorf("assertions = %d, want 1", obs.Assertions)
	}
	if obs.Preceded != 0 {
		t.Errorf("preceded = %d, want 0 — no recall appears before the claim", obs.Preceded)
	}

	// The other half of the unit: a recall before the claim.
	rec, _ := Observe(filepath.Join(fixtures, "recalled.jsonl"))
	if rec.Assertions != 2 || rec.Preceded != 2 {
		t.Errorf("recalled fixture = %d assertions / %d preceded, want 2/2", rec.Assertions, rec.Preceded)
	}

	// Ordinary prose must not count, or the rate measures the regex.
	noise, _ := Observe(filepath.Join(fixtures, "noise.jsonl"))
	if noise.Assertions != 0 {
		t.Errorf("noise fixture matched %d assertions, want 0 — the subject rule is not filtering",
			noise.Assertions)
	}

	// v2's two rejections, driven by real text that v1 counted. Judged complete
	// rather than sampled: all 20 sentences v2 removed from a 46-transcript corpus
	// were noise, and none were genuine.
	quoted, _ := Observe(filepath.Join(fixtures, "quoted.jsonl"))
	if quoted.Assertions != 0 {
		t.Errorf("quoted fixture matched %d, want 0 — a markdown table row is cells rather than "+
			"a sentence, and a shape word inside backticks is an error string being pasted. "+
			"Quoting a claim is not making one.", quoted.Assertions)
	}

	// A subagent transcript counts. Dropping isSidechain the way mineclaude does
	// would exclude the population most likely to skip a recall.
	sub, _ := Observe(filepath.Join(fixtures, "subagent.jsonl"))
	if sub.Assertions != 1 {
		t.Errorf("subagent fixture = %d assertions, want 1 — an isSidechain filter has crept in, "+
			"and it silently excludes subagents from a measurement about subagents", sub.Assertions)
	}
}

func TestF4AClassifierThatMatchesNothingFailsLoudly(t *testing.T) {
	// F-4 / UC1-S3: the gate on the gate. A rate of 100% and a broken regex are
	// the same number, and the broken one is silent.
	if err := CheckClassifierMatches(knownAssertions); err != nil {
		t.Errorf("the classifier matched none of the real sentences it was built for: %v", err)
	}
	if err := CheckClassifierMatches(nil); !errors.Is(err, ErrClassifierMatchedNothing) {
		t.Errorf("an empty corpus must be refused, not passed: %v", err)
	}
	if err := CheckClassifierMatches([]string{"nothing here resembles a claim about behaviour at all"}); !errors.Is(err, ErrClassifierMatchedNothing) {
		t.Errorf("a corpus with no assertion must report it: %v", err)
	}
}

func TestF5AnUnreadableTranscriptRecordsNothing(t *testing.T) {
	// F-5 / UC1-S2: no observation, and no failure. An unread transcript is not a
	// compliant session, and recording it as one would report a clean rate for a
	// session nobody looked at.
	if _, ok := Observe(filepath.Join(t.TempDir(), "does-not-exist.jsonl")); ok {
		t.Error("an absent transcript produced an observation")
	}
}

func TestF15AnObservationCarriesCountsNotContent(t *testing.T) {
	// F-15: counts and identifiers only. The instrument runs over a developer's
	// own sessions; the measurement never needs the words.
	obs, _ := Observe(filepath.Join(fixtures, "unrecalled.jsonl"))
	store := filepath.Join(t.TempDir(), "observations.jsonl")
	if err := AppendObservation(store, obs); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(store)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	for _, sentence := range knownAssertions {
		if strings.Contains(string(raw), sentence) {
			t.Fatal("the store contains transcript text")
		}
	}
	if strings.Contains(string(raw), "closetDistanceCap") {
		t.Error("the store contains a fragment of the transcript")
	}
}

func TestF16AnObservationCarriesItsClassifierVersion(t *testing.T) {
	// F-16: rates from different classifier versions are never compared. Without
	// the stamp, tightening the regex reads as a behaviour change.
	obs, _ := Observe(filepath.Join(fixtures, "unrecalled.jsonl"))
	if obs.Classifier == "" {
		t.Fatal("no classifier version on the observation")
	}
	store := filepath.Join(t.TempDir(), "observations.jsonl")
	if err := AppendObservation(store, obs); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, _ := os.ReadFile(store)
	var back Observation
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &back); err != nil {
		t.Fatalf("decode stored row: %v", err)
	}
	if back.Classifier != classifierVersion {
		t.Errorf("stored classifier = %q, want %q", back.Classifier, classifierVersion)
	}
}

func TestF17AMissIsRepresentable(t *testing.T) {
	// F-17: a session where NO recall preceded an assertion is a ROW, not an
	// absence. This is the reason search_events could not hold the store — its
	// rows are searches, and a miss has no search to hang on.
	obs, _ := Observe(filepath.Join(fixtures, "unrecalled.jsonl"))
	store := filepath.Join(t.TempDir(), "observations.jsonl")
	if err := AppendObservation(store, obs); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, _ := os.ReadFile(store)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d rows, want exactly 1", len(lines))
	}
	var back Observation
	if err := json.Unmarshal([]byte(lines[0]), &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Assertions != 1 || back.Preceded != 0 {
		t.Errorf("stored %d/%d, want 1 assertion and 0 preceded — the miss must survive the "+
			"round trip, or the store can only represent success", back.Assertions, back.Preceded)
	}
}

// ---------------------------------------------------------------------------
// Still red, and deliberately so: these bind facts owned by T2-T6, which have not
// run. They were briefly deleted while T1's file was rewritten, which is the exact
// failure the task warns about — a suite goes green by losing the tests that were
// supposed to stay red, and the spec silently loses its coverage.
// ---------------------------------------------------------------------------

func TestF3NoMechanismShipsBeforeABaseline(t *testing.T) {
	// F-3: a rate exists before any mechanism ships, or the mechanism's effect is
	// unfalsifiable in both directions.
	obs := make([]Observation, minBaselineSessions)
	for i := range obs {
		obs[i] = Observation{Assertions: 4, Preceded: 1, Classifier: classifierVersion}
	}
	r, err := ComputeRate(obs, 48)
	if err != nil {
		t.Fatalf("a sufficient sample with a precision figure must produce a rate: %v", err)
	}
	if r.Percent() != 25 {
		t.Errorf("rate = %.1f%%, want 25%%", r.Percent())
	}
	if r.PrecisionPct != 48 || r.Classifier != classifierVersion {
		t.Errorf("the rate does not carry what it means: precision=%d classifier=%q",
			r.PrecisionPct, r.Classifier)
	}

	// ⚠ A rate without precision is refused rather than reported. Half the
	// denominator is not the class at v2, so a bare number would be quoted as
	// meaning one thing while meaning another.
	if _, err := ComputeRate(obs, 0); !errors.Is(err, ErrPrecisionUnknown) {
		t.Errorf("a rate was produced with no precision figure: %v", err)
	}

	// Rates from different classifiers are not comparable (F-16).
	mixed := append(append([]Observation{}, obs...), Observation{Assertions: 1, Classifier: "v1"})
	if _, err := ComputeRate(mixed, 48); err == nil {
		t.Error("observations spanning two classifier versions were averaged into one rate")
	}
}

func TestTheBaselineRefusesAnUndersizedSample(t *testing.T) {
	// The floor is fixed BEFORE collection. T2's Stop Condition names choosing it
	// afterwards as the way to make this criterion impossible to fail.
	obs := make([]Observation, minBaselineSessions-1)
	for i := range obs {
		obs[i] = Observation{Assertions: 3, Preceded: 3, Classifier: classifierVersion}
	}
	if _, err := ComputeRate(obs, 48); !errors.Is(err, ErrUndersized) {
		t.Errorf("a rate was reported from %d sessions: %v", len(obs), err)
	}

	// And a round trip through the store, so the reader is exercised rather than
	// only the arithmetic.
	store := filepath.Join(t.TempDir(), "observations.jsonl")
	for _, o := range obs {
		if err := AppendObservation(store, o); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	back, err := ReadObservations(store)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(back) != len(obs) {
		t.Errorf("read %d observations, wrote %d", len(back), len(obs))
	}
}

// adr041TasksDir is the record these bindings read. F-9, F-10, F-12 and F-13 are
// facts about how mechanisms are CHOSEN, ORDERED and REPORTED — process, not
// code — so the artifact that can be wrong is the record, and that is what these
// drive. The alternative was to leave them as `t.Fatalf("not built yet")` stubs,
// which is what they were: they assert nothing, fail unconditionally, and made
// two PRs permanently un-mergeable.
const adr041TasksDir = "../../docs/adr/ADR-041-the-recall-that-does-not-depend-on-remembering/tasks"

// mechanism is one row of the compliance-dependence table in tasks/README.md.
type mechanism struct {
	order      int
	task       string
	name       string
	compliance string
}

// complianceRank orders the "Depends on compliance" column. F-13's whole claim is
// that the sequence runs from asking least of the agent to asking most, so the
// column has to be comparable rather than merely present.
func complianceRank(t *testing.T, s string) int {
	t.Helper()
	clean := strings.ToLower(strings.TrimLeft(strings.TrimSpace(s), "*"))
	for rank, word := range []string{"none", "low", "moderate", "high", "highest"} {
		if strings.HasPrefix(clean, word) {
			if word == "high" && strings.HasPrefix(clean, "highest") {
				return 4
			}
			return rank
		}
	}
	t.Errorf("compliance column %q names no known level; F-13's ordering cannot be checked "+
		"against a value nothing can compare", s)
	return -1
}

var mechanismRow = regexp.MustCompile(`(?m)^\|\s*(\d+)\s*\|\s*(T\d+)\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*$`)

// mechanismsInOrder reads the ordering table. ⚠ THE UNIVERSE IS THE TABLE: a
// mechanism added to the record joins every check below on the same commit, which
// a list repeated in this file would not.
func mechanismsInOrder(t *testing.T) []mechanism {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(adr041TasksDir, "README.md"))
	if err != nil {
		t.Fatalf("read the task index: %v", err)
	}
	body := string(raw)
	i := strings.Index(body, "The mechanism ordering, recorded before any of them ships")
	if i < 0 {
		t.Fatal("tasks/README.md carries no mechanism-ordering section — F-13 requires the order " +
			"to be recorded BEFORE any mechanism ships, so that it cannot be rearranged " +
			"afterwards to fit whichever one happened to work")
	}
	rest := body[i:]
	if j := strings.Index(rest, "\n## "); j > 0 {
		rest = rest[:j]
	}
	var out []mechanism
	for _, m := range mechanismRow.FindAllStringSubmatch(rest, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		out = append(out, mechanism{order: n, task: m[2], name: m[3], compliance: m[4]})
	}
	if len(out) < 2 {
		t.Fatalf("found %d mechanisms in the ordering table; an ordering of fewer than two is "+
			"not an ordering and every check below would pass vacuously", len(out))
	}
	return out
}

var indexRow = regexp.MustCompile(`(?m)^\|\s*(T\d+)\s*\|\s*([^|]+?)\s*\|\s*([a-z]+)\s*\|`)

// taskStatus reads the Task Index table: task id → recorded status.
func taskStatus(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(adr041TasksDir, "README.md"))
	if err != nil {
		t.Fatalf("read the task index: %v", err)
	}
	out := map[string]string{}
	for _, m := range indexRow.FindAllStringSubmatch(string(raw), -1) {
		out[m[1]] = m[3]
	}
	if len(out) == 0 {
		t.Fatal("no task rows parsed from the index — these checks would pass vacuously")
	}
	return out
}

// taskFile returns a mechanism task's record.
func taskFile(t *testing.T, id string) (string, string) {
	t.Helper()
	entries, err := os.ReadDir(adr041TasksDir)
	if err != nil {
		t.Fatalf("read tasks dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+"-") {
			raw, err := os.ReadFile(filepath.Join(adr041TasksDir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			return e.Name(), string(raw)
		}
	}
	t.Fatalf("the ordering table names %s but no task file starts with %s- — the record points "+
		"at a plan that does not exist", id, id)
	return "", ""
}

func TestF8AddedProtocolTextIsNotAMechanism(t *testing.T) {
	name, body := taskFile(t, "T6")
	// T6 is the one candidate that IS added protocol text. F-8 says such a
	// paragraph is not a mechanism, and the reason is measured rather than
	// asserted — ADR-017 delivered the whole protocol to a subagent and got 0
	// recalls in 5 dispatches, while one short paragraph got 5. So the record has
	// to carry that citation, or the next reader re-argues it from taste.
	if !strings.Contains(body, "ADR-017") {
		t.Errorf("%s does not cite ADR-017. F-8 rejects added protocol text as a mechanism on "+
			"MEASURED grounds; without the citation the rejection reads as an opinion", name)
	}
	if !strings.Contains(body, "F-8") {
		t.Errorf("%s does not name F-8, so nothing records that the constraint was applied to "+
			"the one task it constrains", name)
	}
}

func TestF9OneMechanismPerMeasurementWindow(t *testing.T) {
	status := taskStatus(t)
	var shipped []string
	for _, m := range mechanismsInOrder(t) {
		if status[m.task] == "done" {
			shipped = append(shipped, m.task)
		}
	}
	if len(shipped) > 1 {
		t.Errorf("%v are all recorded done. F-9 allows ONE mechanism per measurement window: "+
			"two shipped into the same window make the delta unattributable, which is the "+
			"whole reason the window exists", shipped)
	}
}

func TestF10EveryResultIsRecordedEitherWay(t *testing.T) {
	status := taskStatus(t)
	dated := regexp.MustCompile(`(?m)^## .*\b20\d\d-\d\d-\d\d\b`)
	for _, m := range mechanismsInOrder(t) {
		st := status[m.task]
		if st == "" || st == "pending" {
			continue
		}
		name, body := taskFile(t, m.task)
		if !dated.MatchString(body) {
			t.Errorf("%s is recorded %q but its file carries no dated outcome section. F-10 "+
				"requires the result to be written whichever way it fell — a mechanism that "+
				"was tried and abandoned teaches the next one, and an unrecorded attempt "+
				"gets made again", name, st)
		}
	}
}

func TestF12EachMechanismNamesTheFailureItAddresses(t *testing.T) {
	for _, m := range mechanismsInOrder(t) {
		name, body := taskFile(t, m.task)
		if !strings.Contains(body, "distinct failure this addresses (F-12)") {
			t.Errorf("%s does not name the distinct failure it addresses. F-12 makes that the "+
				"entry condition for being a candidate at all: a mechanism that cannot say "+
				"which failure it fixes cannot be judged to have fixed it", name)
		}
	}
}

func TestF13MechanismsAreOrderedByComplianceDependence(t *testing.T) {
	ms := mechanismsInOrder(t)
	prev := -1
	for i, m := range ms {
		if m.order != i+1 {
			t.Errorf("mechanism %s sits at order %d, expected %d — the ordering table has gaps "+
				"or repeats, so it does not record an order", m.task, m.order, i+1)
		}
		rank := complianceRank(t, m.compliance)
		if rank < prev {
			t.Errorf("%s (%s) depends on compliance MORE than the mechanism before it. F-13 "+
				"orders them least-dependent first, so that a failure early in the sequence "+
				"cannot be explained away as the agent declining to comply", m.task, m.compliance)
		}
		if rank >= 0 {
			prev = rank
		}
	}
}

// notYetBuilt is the shape of a spec binding that has not been executed.
const notYetBuilt = "not built yet — %s"
