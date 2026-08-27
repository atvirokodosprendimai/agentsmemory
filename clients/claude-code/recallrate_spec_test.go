package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestF8AddedProtocolTextIsNotAMechanism(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-8 (T6, and UC2-S2): a paragraph added to a document the agent already "+
		"receives in full is rejected as a mechanism, citing ADR-017")
}

func TestF9OneMechanismPerMeasurementWindow(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-9 (T3, and UC3-S1): ship one at a time, lowest compliance-dependence "+
		"first")
}

func TestF10EveryResultIsRecordedEitherWay(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-10 (T3, and UC3-S2): a delta inside the instrument's resolution is "+
		"recorded as NOT SHOWN TO WORK, never as directionally correct")
}

func TestF12EachMechanismNamesTheFailureItAddresses(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-12 (T3-T5): a mechanism that cannot name the distinct failure it "+
		"addresses is not a candidate")
}

func TestF13MechanismsAreOrderedByComplianceDependence(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-13 (T3): the ordering is recorded BEFORE any of them ships")
}

// notYetBuilt is the shape of a spec binding that has not been executed.
const notYetBuilt = "not built yet — %s"
