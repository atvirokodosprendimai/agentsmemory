package palace

import (
	"context"
	"testing"
)

// TestCoverageFormula is the base serving number: rows that should hold a point
// in the index half, minus the ones that do not, over the rows that should.
func TestCoverageFormula(t *testing.T) {
	r := DriftReport{
		Checked:          NamespaceSplit{Drawers: 10},
		IndexMissing:     NamespaceSplit{Drawers: 2},
		IndexMislabelled: NamespaceSplit{Drawers: 1},
	}
	if got := r.Coverage(); got != 0.7 {
		t.Fatalf("coverage = %v, want 0.7", got)
	}
}

// TestCoveragePendingOnlyReadsOne: nothing embedded yet is vacuously healthy —
// the number must be 1.0, never a division error.
func TestCoveragePendingOnlyReadsOne(t *testing.T) {
	r := DriftReport{} // no checked rows at all
	if got := r.Coverage(); got != 1.0 {
		t.Fatalf("pending-only coverage = %v, want 1.0", got)
	}
}

// TestCoverageExpectedIsEmbeddedCounts: the formula reads the per-namespace
// embedded counts (pending excluded once, by construction — DrawerWings and
// ClosetWings return embedded rows only) as its denominator, and the per-
// namespace view exposes those expected counts.
func TestCoverageExpectedIsEmbeddedCounts(t *testing.T) {
	r := DriftReport{
		Checked:      NamespaceSplit{Drawers: 5, Closets: 3},
		IndexMissing: NamespaceSplit{Drawers: 1, Closets: 1},
	}
	if got := r.Coverage(); got != 0.75 {
		t.Fatalf("blended coverage = %v, want 0.75", got)
	}
	v := r.CoverageView()
	if v["drawers"].Expected != 5 || v["closets"].Expected != 3 {
		t.Fatalf("per-namespace expected = %+v, want drawers 5 / closets 3", v)
	}
	if v["drawers"].Coverage != 0.8 || v["closets"].Coverage != 2.0/3.0 {
		t.Fatalf("per-namespace coverage = %+v, want 0.8 / 0.6667", v)
	}
}

// TestCoverageUsesIndexHalfOnly: the source-of-truth half does not serve, so its
// drift must not depress the serving number — and a point drifted in BOTH halves
// depresses it exactly once (per-half counters, not the conflated Total).
func TestCoverageUsesIndexHalfOnly(t *testing.T) {
	r := DriftReport{
		Checked:        NamespaceSplit{Drawers: 10},
		SotMissing:     NamespaceSplit{Drawers: 9}, // invisible to serving
		IndexMissing:   NamespaceSplit{Drawers: 1},
		SotMislabelled: NamespaceSplit{Drawers: 9}, // invisible to serving
	}
	if got := r.Coverage(); got != 0.9 {
		t.Fatalf("coverage = %v, want 0.9 (index half only, once)", got)
	}
}

// TestCoverageViewIndexedIsRealPopulation is the JD-003 gate on the view
// itself: Indexed is the index half's REAL point population (carried in
// IndexCount), not expected−missing−mislabelled, so an over-count displays
// indexed > expected instead of saturating at expected. Fails when the view
// derives Indexed from the checked rows again.
func TestCoverageViewIndexedIsRealPopulation(t *testing.T) {
	r := DriftReport{
		Checked:      NamespaceSplit{Drawers: 10},
		IndexMissing: NamespaceSplit{Drawers: 2},
		IndexCount:   NamespaceSplit{Drawers: 13}, // 10 expected + 3 orphans
	}
	v := r.CoverageView()["drawers"]
	if v.Indexed != 13 {
		t.Fatalf("indexed = %d, want 13 (the real index population, not the derived 8)", v.Indexed)
	}
	if v.Indexed <= v.Expected {
		t.Fatal("an over-count index must display indexed > expected — the raw fields are how it stays visible")
	}
	if v.Coverage != 0.8 {
		t.Fatalf("coverage = %v, want 0.8 — the formula still reads from missing/mislabelled", v.Coverage)
	}
}

// TestCoverageClampsToUnitInterval: a corrupted report (missing + mislabelled >
// expected, unreachable from the pinned accounting but possible from a counting
// bug) reads 0, never a negative number — and the raw fields still expose the
// corrupted inputs. An over-count-shaped report (nothing missing or mislabelled)
// reads 1.0 while the raw fields carry the counts.
func TestCoverageClampsToUnitInterval(t *testing.T) {
	corrupt := DriftReport{
		Checked:          NamespaceSplit{Drawers: 10},
		IndexMissing:     NamespaceSplit{Drawers: 10},
		IndexMislabelled: NamespaceSplit{Drawers: 5},
	}
	if got := corrupt.Coverage(); got != 0 {
		t.Fatalf("corrupted coverage = %v, want 0 (clamped, not negative)", got)
	}
	v := corrupt.CoverageView()["drawers"]
	if v.Expected != 10 || v.Missing != 10 || v.Mislabelled != 5 {
		t.Fatalf("corrupted raw fields = %+v; the clamp must not hide the inputs", v)
	}

	over := DriftReport{Checked: NamespaceSplit{Drawers: 10}}
	if got := over.Coverage(); got != 1.0 {
		t.Fatalf("over-count-shaped coverage = %v, want 1.0 (formula saturates; raw fields carry the counts)", got)
	}
}

// TestIndexDriftCountsPendingClosets: a closet awaiting its first embedding is a
// queue, not a fault — it must count as pending, be excluded from the checked
// rows, and never appear as a missing point.
func TestIndexDriftCountsPendingClosets(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-r3"

	pending := Closet{TeamID: team, ID: "c-pending", Wing: "alpha", Room: "db",
		SourceFile: "s.md", Document: "packed doc", Entities: []string{"E"}, FiledAt: "2026-01-01T00:00:00Z"}
	if err := svc.repo.SaveClosetsUnembedded(ctx, []Closet{pending}); err != nil {
		t.Fatalf("seed pending closet: %v", err)
	}
	embedded := pending
	embedded.ID = "c-embedded"
	if err := svc.repo.SaveClosets(ctx, []Closet{embedded}); err != nil {
		t.Fatalf("seed embedded closet: %v", err)
	}

	report, err := svc.IndexDrift(ctx, team)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if report.Checked.Closets != 1 {
		t.Fatalf("Checked.Closets = %d, want 1 (the embedded closet only — pending excluded)", report.Checked.Closets)
	}
	if report.Pending.Closets != 1 {
		t.Fatalf("Pending.Closets = %d, want 1 (the pending closet counted as a queue)", report.Pending.Closets)
	}
	for _, d := range report.Drifted {
		if d.DrawerID == "c-pending" {
			t.Fatalf("the pending closet was reported as a drifted/missing point: %+v", d)
		}
	}
	// The embedded closet has a stamped row but no vector point in this test's
	// store — a genuine population gap, so it IS missing. The point of the test
	// is the separation: pending is a queue, embedded-without-a-point is a fault.
	found := false
	for _, d := range report.Drifted {
		if d.DrawerID == "c-embedded" && d.Missing {
			found = true
		}
	}
	if !found {
		t.Fatalf("the embedded closet (stamped, no point) must be Missing; drifted = %+v", report.Drifted)
	}
}
