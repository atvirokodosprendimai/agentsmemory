package main

import "testing"

// Bindings for docs/specs/2026-08-27-recall-before-asserting.md.
//
// ⚠ THESE ARE DELIBERATELY RED. They are the TDD-red state of a spec that is not
// yet an ADR: each names one Fact, fails with the assertion it will prove, and
// turns green during execution. A spec whose bound tests are absent from the tree
// fails its own gate, so they travel with the document rather than after it.
//
// This branch carries nothing else, which is the condition for committing red
// tests at all — red tests on a branch that also carries something shippable
// block the thing that needed to ship.
//
// The instrument they describe reads a session transcript and counts one thing:
// how often a no-change assertion was preceded by a recall. Not whether the agent
// says it would have recalled — ADR-017 measured what that question selects for.

const notYetBuilt = "not built yet — %s"

func TestF1RecallRateIsCountedFromTranscripts(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-1: the rate is counted from session transcripts, never from an "+
		"agent's self-report. ADR-017: asked whether it would have complied, the probe said "+
		"'likely yes', which is what the question selects for")
}

func TestF2TheCountableUnitIsANoChangeAssertion(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-2 (and UC1-S1): a claim that something STILL behaves a given way, "+
		"does NOT do something, or has NOT been decided. A transcript containing one, with no "+
		"am_search before it, counts as one assertion and zero preceded")
}

func TestF3NoMechanismShipsBeforeABaseline(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-3: a mechanism intended to raise the rate cannot ship until a "+
		"baseline exists. Otherwise its effect is unfalsifiable in both directions")
}

func TestF4AClassifierThatMatchesNothingFailsLoudly(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-4 (and UC1-S3): over a fixture corpus that CONTAINS no-change "+
		"assertions, a classifier matching none must fail. A 100%% rate and a broken regex are "+
		"the same number, and this is the gate on the gate")
}

func TestF5AnUnreadableTranscriptRecordsNothing(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-5 (and UC1-S2): a missing or unreadable transcript records no "+
		"observation and never fails the session. An unread transcript is not a compliant one")
}

func TestF6AHookIsSilentInTheCommonCase(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-6: a hook adds no output unless it has something the session would "+
		"otherwise get wrong. The verify hook's own comment: one that reports 'all good' every "+
		"time is one people stop reading, and its output is spent context")
}

func TestF8AddedProtocolTextIsNotAMechanism(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-8 (and UC2-S2): a paragraph added to a document the agent already "+
		"receives in full is rejected as a mechanism, citing ADR-017 — injecting one more "+
		"paragraph into a context that already holds the whole gate is the least promising thing "+
		"to try")
}

func TestF9OneMechanismPerMeasurementWindow(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-9 (and UC3-S1): ship one at a time, lowest compliance-dependence "+
		"first. Two together and neither is attributable")
}

func TestF10EveryResultIsRecordedEitherWay(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-10 (and UC3-S2): a delta inside the instrument's resolution is "+
		"recorded as NOT SHOWN TO WORK, never as directionally correct")
}

func TestF12EachMechanismNamesTheFailureItAddresses(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-12: a mechanism that cannot name the distinct failure it addresses "+
		"is not a candidate. Four are in scope and they fix different things")
}

func TestF13MechanismsAreOrderedByComplianceDependence(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-13: the ordering is recorded BEFORE any of them ships, so it cannot "+
		"be rearranged afterwards to fit whichever one happened to work")
}

func TestF15AnObservationCarriesCountsNotContent(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-15: counts and identifiers only. No transcript text leaves the "+
		"machine that produced it")
}

func TestF16AnObservationCarriesItsClassifierVersion(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-16: rates from different classifier versions are never compared. "+
		"Without this, tightening the regex reads as a behaviour change")
}

func TestF17AMissIsRepresentable(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-17: the store records sessions where NO recall preceded an "+
		"assertion. This is why search_events cannot hold it — its rows are searches, and the "+
		"absence of one is the whole measurement")
}
