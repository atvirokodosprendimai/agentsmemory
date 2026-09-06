package palace

import (
	"context"
	"testing"
)

// Issue #34 asks for ADR-004's pre-registered supersession measurement to be RUN
// and its verdict recorded. It could not be: the gate was structurally unable to
// see a single case, on any corpus, ever.
//
// `evalCaseResult` computes `DistractorRanks` and `DistractorPoolRank` for every
// case carrying a superseded version. The `EvalCaseResult` appended to
// `report.Details` did not copy either. `StaleAboveRate` opens with
// `if c.DistractorRanks == nil { continue }` and reads `report.Details` — so the
// cell was always empty, `supersessionGateReady` always took its
// "this run scored none" branch, and the refusal named a cause that sends the
// operator to regenerate a case file that was never the problem.
//
// ⚠ MEASURED, NOT INFERRED. On this project's own palace, 40 sampled drawers
// produced 37 pair candidates and 1 judge-verified pair; before the wiring the
// gate said "records 1 verified pair(s) but this run scored none", and after it
// said "only 1 verified pair(s) are non-vacuous at --pool 50 … below 30 the
// interval straddles almost any bar" — the documented refusal, for the real
// reason, from the same case file.
//
// The class is §Reachability's: two fields that are computed, correct, and
// unreachable from the only consumer that needs them. Every unit test of
// StaleAboveRate passes, because they build their own cases.

// TestTheSupersessionCellSeesACaseThroughTheRealReport drives Evaluate and asks
// the gate's own accounting function what it can see — the composition, not the
// component.
func TestTheSupersessionCellSeesACaseThroughTheRealReport(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-supersession", "w"

	old, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "r", SourceFile: "policy-v1",
		Content:     "retention window policy retention window policy thirty days",
		ContentDate: "2024-01-01"})
	if err != nil {
		t.Fatalf("add old: %v", err)
	}
	newer, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "r", SourceFile: "policy-v2",
		Content: "retention window policy ninety days", ContentDate: "2026-01-01"})
	if err != nil {
		t.Fatalf("add new: %v", err)
	}

	cases := []EvalCase{{
		Query: "retention window policy", Expect: newer.Drawers[0].ID,
		Distractor: old.Drawers[0].ID, Wing: wing, Category: CatTemporal,
	}}
	report, err := svc.EvaluateWith(ctx, team, cases, 20, EvalOptions{Arms: []string{string(ArmHybrid)}}, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	cell := StaleAboveRate(report.Details, ArmHybrid)
	if cell.Cases+cell.Vacuous == 0 {
		t.Fatalf("the supersession cell saw no case from a report whose only case carries a "+
			"verified distractor (details: %d).\n"+
			"  evalCaseResult computes DistractorRanks and DistractorPoolRank; if the record the "+
			"gate reads does not carry them, the gate refuses on EVERY corpus with "+
			"\"this run scored none\" — and ADR-004's pre-registered measurement can never speak, "+
			"which is what issue #34 was waiting on.", len(report.Details))
	}
	if cell.Cases != 1 {
		t.Errorf("cell.Cases = %d, want 1 (vacuous %d): the distractor is in this pool, so the "+
			"case is scoreable rather than vacuous", cell.Cases, cell.Vacuous)
	}
}

// TestTheReportCarriesTheDistractorFieldsItComputed is the narrower half, and it
// is what fails first when somebody adds a field to caseOutcome and forgets the
// literal that copies it — the mistake this test exists for, which cost the
// measurement its entire ability to answer.
func TestTheReportCarriesTheDistractorFieldsItComputed(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team, wing = "team-supersession-fields", "w"

	old, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "r", SourceFile: "v1",
		Content: "cache eviction policy cache eviction policy least recently used", ContentDate: "2024-01-01"})
	if err != nil {
		t.Fatalf("add old: %v", err)
	}
	newer, err := svc.Add(ctx, team, AddInput{Wing: wing, Room: "r", SourceFile: "v2",
		Content: "cache eviction policy least frequently used", ContentDate: "2026-01-01"})
	if err != nil {
		t.Fatalf("add new: %v", err)
	}

	report, err := svc.EvaluateWith(ctx, team, []EvalCase{{
		Query: "cache eviction policy", Expect: newer.Drawers[0].ID,
		Distractor: old.Drawers[0].ID, Wing: wing, Category: CatTemporal,
	}}, 20, EvalOptions{Arms: []string{string(ArmHybrid)}}, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(report.Details) != 1 {
		t.Fatalf("report carries %d case detail(s), want 1", len(report.Details))
	}
	d := report.Details[0]
	if d.DistractorRanks == nil {
		t.Error("the case detail carries no DistractorRanks, so every consumer that reads the " +
			"report — the gate included — is told this case names no superseded version")
	}
	if d.DistractorPoolRank == 0 {
		t.Error("the case detail reports the distractor outside the pool while the run placed it " +
			"inside; a zero here is read as VACUOUS, which is a different finding from a case " +
			"that was measured")
	}
}
