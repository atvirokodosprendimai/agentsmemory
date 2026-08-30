package usage

import (
	"context"
	"testing"
)

// TestFixedCapAnswersTheSameForEveryWorkspace pins the property that makes this
// a DEPLOYMENT policy rather than a per-workspace grant: it does not look at the
// workspace, so it cannot drift apart per tenant and it edits no plan row.
func TestFixedCapAnswersTheSameForEveryWorkspace(t *testing.T) {
	const want = 25_000
	cap := FixedCap(want)

	for _, team := range []string{"team-a", "team-b", ""} {
		got, err := cap.MonthlyCap(context.Background(), team)
		if err != nil {
			t.Fatalf("MonthlyCap(%q): %v", team, err)
		}
		if got != want {
			t.Errorf("MonthlyCap(%q) = %d, want %d", team, got, want)
		}
	}
}

// TestFixedCapEnforcesThroughTheService is the half that matters: a cap that is
// returned and not enforced is not a cap. It drives the real Service, so the
// decorator is exercised through the same path a metered request takes rather
// than being asserted on in isolation.
func TestFixedCapEnforcesThroughTheService(t *testing.T) {
	svc := NewService(NewRepo(newTestDB(t)), FixedCap(2))
	ctx, team := context.Background(), "team-capped"

	for i := 1; i <= 2; i++ {
		st, err := svc.Allow(ctx, team)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !st.Allowed {
			t.Fatalf("request %d refused under a cap of 2 (used %d)", i, st.Used)
		}
	}

	st, err := svc.Allow(ctx, team)
	if err != nil {
		t.Fatalf("request 3: %v", err)
	}
	if st.Allowed {
		t.Error("the third request was allowed under a cap of 2 — the override is not enforced")
	}
	if st.Used != 2 {
		t.Errorf("a refused request was counted: used = %d, want 2 — a blocked caller must not "+
			"be able to inflate the tally", st.Used)
	}
}

// TestANegativeFixedCapIsUnlimited pins the spelling of "no limit".
//
// Negative rather than zero because zero already had to mean "operator set
// nothing", and -1 is what the Unlimited plan row carries and what am_status
// already reports — so a self-hosted operator reading either sees the same
// number for the same meaning.
func TestANegativeFixedCapIsUnlimited(t *testing.T) {
	svc := NewService(NewRepo(newTestDB(t)), FixedCap(-1))
	ctx, team := context.Background(), "team-uncapped"

	for i := 1; i <= 50; i++ {
		st, err := svc.Allow(ctx, team)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !st.Allowed {
			t.Fatalf("request %d refused under a negative (unlimited) cap", i)
		}
	}

	// Still counted, because analytics do not stop when enforcement does.
	st, err := svc.Snapshot(ctx, team)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if st.Used != 50 {
		t.Errorf("used = %d, want 50 — an uncapped workspace must still be metered", st.Used)
	}
	if st.Remaining() != 0 {
		t.Errorf("Remaining() = %d on an unlimited cap, want 0 (the documented meaning)", st.Remaining())
	}
}

// TestSnapshotAgreesWithAllowUnderAnOverride. Allow enforces and Snapshot is
// what the dashboard and am_status read; if the override reached only one of
// them, an operator would be shown a limit that is not the one being applied.
// Both consult the CapLookup, which is why the override is a decorator rather
// than a branch inside Allow.
func TestSnapshotAgreesWithAllowUnderAnOverride(t *testing.T) {
	svc := NewService(NewRepo(newTestDB(t)), FixedCap(7))
	ctx, team := context.Background(), "team-agree"

	allowed, err := svc.Allow(ctx, team)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	shown, err := svc.Snapshot(ctx, team)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if allowed.Cap != shown.Cap {
		t.Errorf("Allow reports cap %d and Snapshot reports %d — the enforced limit and the "+
			"displayed one disagree", allowed.Cap, shown.Cap)
	}
}
