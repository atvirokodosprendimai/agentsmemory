package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestPoolDiagnosisCountsOnlyPooledArms.
//
// The diagnosis answers one question: how many golds were never in the shared
// candidate pool, so no reordering could reach them. It reads the worst NotFound
// across arms — but NotFound does not mean the same thing in every arm.
// supersessionScope already says so: ArmProduction is ScopePage, because it goes
// through Service.Search and is scored over the PAGE that returns, not over the
// pool. A gold at pool rank 12 is "not found" for production and found for every
// pooled arm, and it is not a retrieval failure at all.
//
// Folding it in inflates the count and prints advice that cannot work: raising
// --pool does not widen production's page, so following it changes nothing and
// the reader concludes the embedding is at fault. The same mistake was already
// fixed once for ArmContextual by name; production has the identical property
// and was never excluded. This pins the classification instead of the name.
func TestPoolDiagnosisCountsOnlyPooledArms(t *testing.T) {
	report := palace.EvalReport{Arms: []palace.EvalMetrics{
		{Arm: palace.ArmVector, Cases: 30, NotFound: 2},
		{Arm: palace.ArmRRFReranked, Cases: 30, NotFound: 2},
		// Scored over the page, not the pool: six golds sat below the page cut.
		{Arm: palace.ArmProduction, Cases: 30, NotFound: 8},
	}}

	var buf bytes.Buffer
	printPoolDiagnosis(&buf, report)
	got := buf.String()

	if strings.Contains(got, "8 of 30") {
		t.Errorf("reported production's page misses as pool misses:\n%s", got)
	}
	if !strings.Contains(got, "2 of 30") {
		t.Errorf("did not report the pooled arms' 2 misses:\n%s", got)
	}
	// The page miss is real information — it must not simply vanish — but it has
	// its own knob, and --pool is not it.
	if !strings.Contains(got, "page") {
		t.Errorf("production's page misses were dropped entirely; they are a real "+
			"finding with a different remedy:\n%s", got)
	}
}

// TestPoolDiagnosisStaysSilentWhenEveryPooledArmFoundEverything: a run where only
// the page-scoped arm missed anything has no retrieval failure to report, and
// printing one sends the reader after the embedding.
func TestPoolDiagnosisStaysSilentWhenEveryPooledArmFoundEverything(t *testing.T) {
	report := palace.EvalReport{Arms: []palace.EvalMetrics{
		{Arm: palace.ArmVector, Cases: 30, NotFound: 0},
		{Arm: palace.ArmProduction, Cases: 30, NotFound: 5},
	}}
	var buf bytes.Buffer
	printPoolDiagnosis(&buf, report)
	if strings.Contains(buf.String(), "OUTSIDE the candidate pool") {
		t.Errorf("claimed a retrieval failure when every pooled arm found every gold:\n%s", buf.String())
	}
}

// TestNoAggregateMixesScopes: the arm table's `vs best` column, and the ceiling
// block's closing sentence, must both stay inside one population.
//
// `printEvalTable` picked its baseline with a single argmax over every arm and
// then paired every other arm against it. Three of the arms are not measuring
// the same thing: ArmProduction is scored over the PAGE Search returns,
// ArmContextual retrieves from its own capped index, and a run-time arm nobody
// has classified yet has no population at all. Comparing their MRR with a pooled
// arm's prints "worse by 0.40" for a difference that is a change of question,
// and the reader goes tuning the ranking of a pipeline that was never in the
// race. It is the same defect ADR-007 records against printPoolDiagnosis, one
// column over, and it survived that fix because the fix was made at the call
// site rather than as a rule.
//
// The unclassified arm is the half that matters. An exclusion list keyed by arm
// name passes every assertion an existing arm can make and folds the NEXT one
// back in — which is exactly how production inherited the contextual arm's bug —
// so the fixture uses a name `ArmScope` deliberately does not know, and the
// assertion is that it is excluded anyway.
func TestNoAggregateMixesScopes(t *testing.T) {
	// Not a real arm: ArmScope must return the empty scope for it, or this test
	// is asserting something weaker than it claims.
	const unclassified = palace.EvalArm("experimental-two-stage")
	if s := palace.ArmScope(unclassified); s != "" {
		t.Fatalf("fixture arm %q is classified as %q — pick a name ArmScope does not know, "+
			"otherwise the no-list half of this test proves nothing", unclassified, s)
	}

	report := palace.EvalReport{
		PoolRanks: []int{1, 1, 2, 3},
		Arms: []palace.EvalMetrics{
			{Arm: palace.ArmVector, Cases: 4, Recall1: 1, Recall5: 3, MRR: 0.40, Ranks: []int{1, 3, 5, 0}},
			{Arm: palace.ArmHybrid, Cases: 4, Recall1: 3, Recall5: 4, MRR: 0.90, Ranks: []int{1, 1, 2, 1}},
			{Arm: palace.ArmProduction, Cases: 4, Recall1: 1, Recall5: 2, MRR: 0.50, Ranks: []int{1, 4, 0, 0}},
			{Arm: palace.ArmContextual, Cases: 4, Recall1: 1, Recall5: 1, MRR: 0.30, Ranks: []int{1, 0, 0, 0}},
			{Arm: unclassified, Cases: 4, Recall1: 1, Recall5: 2, MRR: 0.35, Ranks: []int{2, 3, 0, 0}},
		},
	}

	var buf bytes.Buffer
	printEvalTable(&buf, report)
	got := buf.String()

	row := func(arm palace.EvalArm) string {
		for _, ln := range strings.Split(got, "\n") {
			if strings.HasPrefix(ln, string(arm)+" ") {
				return ln
			}
		}
		t.Fatalf("no table row for arm %q:\n%s", arm, got)
		return ""
	}

	// Each of the three non-pool arms is the only member of its own population,
	// so each is the best of it. Before the partition they were all ranked
	// against the pooled winner and reported as worse.
	for _, arm := range []palace.EvalArm{palace.ArmProduction, palace.ArmContextual, unclassified} {
		if r := row(arm); !strings.Contains(r, "BEST") {
			t.Errorf("arm %q measures a different population from the pooled winner but was "+
				"compared against it:\n  %s", arm, r)
		}
	}

	// The partition must not disable the comparison it exists to make: two pooled
	// arms are still ranked against each other.
	if r := row(palace.ArmHybrid); !strings.Contains(r, "BEST") {
		t.Errorf("the pooled winner is no longer marked BEST:\n  %s", r)
	}
	if r := row(palace.ArmVector); strings.Contains(r, "BEST") {
		t.Errorf("a losing pooled arm was marked BEST — the partition swallowed the "+
			"comparison instead of narrowing it:\n  %s", r)
	}

	// Step 3 of the task: an aggregate that spans populations says so in its own
	// output rather than being exempted in a list. Four scopes are in this table,
	// so each BEST must name which population it won.
	if r := row(palace.ArmHybrid); !strings.Contains(r, string(palace.ScopePool)) {
		t.Errorf("the pooled BEST does not name its population, so a reader cannot tell "+
			"which arms it beat:\n  %s", r)
	}
	if r := row(palace.ArmProduction); !strings.Contains(r, string(palace.ScopePage)) {
		t.Errorf("production's BEST does not name its population:\n  %s", r)
	}

	// The ceiling block's closing sentence is a claim about the table above it,
	// and it is false the moment a non-pool arm is in that table: production does
	// not re-order this pool, it is scored over a page cut from it.
	const claim = "every arm above re-orders this same pool"
	if strings.Contains(got, claim) {
		t.Errorf("the ceiling block told the reader every arm re-orders the shared pool, "+
			"with three arms in the table that do not:\n%s", got)
	}
}
