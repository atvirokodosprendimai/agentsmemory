package billing

import (
	"context"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"gorm.io/gorm"
)

// stubOrders returns a fixed page, standing in for the GraphQL client.
type stubOrders struct {
	orders []providerOrder
	err    error
}

func (s stubOrders) listOrders(context.Context) ([]providerOrder, error) { return s.orders, s.err }

// ocTierMap is the tier->plan mapping used throughout these tests, matching the
// live tier ids read 2026-08-28.
var ocTierMap = map[string]string{"pro-monthly": "pro_monthly", "pro-yearly": "pro_annual"}

// newReconcileEnv wires a Reconciler over a migrated in-memory DB with one team.
// The orders are built by a callback because most of them need the team id in a
// tag, and the id only exists once the environment is up.
func newReconcileEnv(t *testing.T, build func(teamID string) []providerOrder) (*Reconciler, *Service, *gorm.DB, string) {
	t.Helper()
	svc, _, _, gdb, teamID := newTestEnv(t)
	intents := NewIntentRepo(gdb)
	svc.intents = intents
	r := NewReconciler(svc, stubOrders{orders: build(teamID)}, intents, ocTierMap).WithLedger(NewAppliedOrderRepo(gdb))
	return r, svc, gdb, teamID
}

// recordIntent stores the intent a real checkout would have written.
func recordIntent(t *testing.T, gdb *gorm.DB, teamID, planCode, email string) {
	t.Helper()
	if err := NewIntentRepo(gdb).Record(context.Background(), CheckoutIntent{
		TeamID: teamID, PlanCode: planCode, Tag: intentTag(teamID), Email: email,
	}); err != nil {
		t.Fatalf("record intent: %v", err)
	}
}

// TestReconcileMapsOrderStatusToEventKind covers every value of the OrderStatus
// enum as read from the live schema on 2026-08-28, plus a status that does not
// exist. The unknown case is the one that matters: a status Open Collective adds
// after this was written must never be read as a cancellation, because that would
// silently downgrade paying workspaces.
func TestReconcileMapsOrderStatusToEventKind(t *testing.T) {
	cases := map[string]eventKind{
		"ACTIVE": eventActivated, "PAID": eventActivated,
		"CANCELLED": eventCanceled, "EXPIRED": eventCanceled,
		"REFUNDED": eventCanceled, "REJECTED": eventCanceled,
		"NEW": eventIgnored, "PENDING": eventIgnored, "PROCESSING": eventIgnored,
		"REQUIRE_CLIENT_CONFIRMATION": eventIgnored, "DISPUTED": eventIgnored,
		"IN_REVIEW": eventIgnored, "PAUSED": eventIgnored, "ERROR": eventIgnored,
	}
	if len(cases) != 14 {
		t.Fatalf("the live OrderStatus enum has 14 values, this table has %d", len(cases))
	}
	for status, want := range cases {
		if got := kindForStatus(status); got != want {
			t.Errorf("kindForStatus(%q) = %v, want %v", status, got, want)
		}
	}
	if got := kindForStatus("SOME_STATUS_THAT_DOES_NOT_EXIST_YET"); got != eventIgnored {
		t.Errorf("an unknown status mapped to %v; it must be ignored, never a cancellation", got)
	}
}

// TestReconcileAttributesByTagOnlyWithAMatchingIntent is the security property: the
// tag rides in a user-controlled URL, so a tag naming a workspace this server never
// recorded an intent for must NOT upgrade that workspace.
func TestReconcileAttributesByTagOnlyWithAMatchingIntent(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_test9001", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly",
			Tags: []string{intentTag(teamID)}, FromAccountSlug: "jane",
		}}
	})

	// No intent recorded yet: a forged tag must buy nothing.
	rep, err := r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 0 || rep.Unattributed != 1 {
		t.Fatalf("a tag with no recorded intent activated something: %+v", rep)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("plan changed on an uncorroborated tag: %q", got)
	}

	// With the intent this server actually recorded, the same order activates.
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")
	rep, err = r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 1 {
		t.Fatalf("a corroborated tag did not activate: %+v", rep)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got == tenant.FreePlanID {
		t.Fatal("plan was not upgraded after a corroborated activation")
	}
}

// TestReconcileAttributesByEmailWhenTheTagIsAbsent covers the fallback the ADR
// names as the answer if `tags` turns out not to survive the hosted checkout.
func TestReconcileAttributesByEmailWhenTheTagIsAbsent(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_test9002", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly",
			Tags: nil, FromAccountEmail: "buyer@example.com",
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "buyer@example.com")

	rep, err := r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 1 {
		t.Fatalf("email fallback did not attribute: %+v", rep)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got == tenant.FreePlanID {
		t.Fatal("plan was not upgraded via the email fallback")
	}
}

// TestReconcileLeavesAnUnattributableOrderAlone: a contribution made outside our
// checkout carries no tag and an unknown email. It must change nothing at all.
func TestReconcileLeavesAnUnattributableOrderAlone(t *testing.T) {
	r, svc, _, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_test9003", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly",
			Tags: nil, FromAccountEmail: "stranger@example.com",
		}}
	})

	rep, err := r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Unattributed != 1 || rep.Activated != 0 {
		t.Fatalf("an unattributable order was acted on: %+v", rep)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("an unattributable order changed a plan: %q", got)
	}
}

// TestReconcileIgnoresAContributionOutsideOurTiers: an ordinary donation is not a
// purchase, and must not be treated as one.
func TestReconcileIgnoresAContributionOutsideOurTiers(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_test9004", Status: "PAID", Frequency: "ONETIME", TierSlug: "",
			Tags: []string{intentTag(teamID)},
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "buyer@example.com")

	rep, err := r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 0 || rep.Ignored != 1 {
		t.Fatalf("a tierless donation was treated as a purchase: %+v", rep)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("a tierless donation changed a plan: %q", got)
	}
}

// TestReconcileIsIdempotent: the loop re-reads the same orders every pass forever,
// so running twice must converge rather than double-apply.
func TestReconcileIsIdempotent(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_test9005", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly",
			Tags: []string{intentTag(teamID)}, FromAccountSlug: "jane",
			NextChargeDate: "2026-09-20T09:11:02Z",
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")

	for i := 0; i < 3; i++ {
		if _, err := r.ReconcileOnce(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	var count int64
	if err := gdb.Raw("SELECT COUNT(*) FROM subscriptions WHERE team_id = ?", teamID).Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("three passes produced %d subscription rows, want 1", count)
	}
	sub, err := svc.subs.ByTeam(context.Background(), teamID)
	if err != nil {
		t.Fatalf("ByTeam: %v", err)
	}
	if sub.CurrentPeriodEnd != "2026-09-20T09:11:02Z" {
		t.Fatalf("CurrentPeriodEnd = %q; nextChargeDate must populate the column nothing has ever written", sub.CurrentPeriodEnd)
	}
}

// TestReconcileDoesNotResurrectACanceledSubscription: a page carrying both the
// cancellation and a stale ACTIVE for the SAME order must not end with the
// workspace back on Pro. This is the existing applyActivated guard, exercised on
// the reconcile path rather than the webhook path.
func TestReconcileDoesNotResurrectACanceledSubscription(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{
			{ID: "or_test9006", Status: "CANCELLED", Frequency: "MONTHLY", TierSlug: "pro-monthly"},
			{ID: "or_test9006", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly", Tags: []string{intentTag(teamID)}},
		}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")

	// Seed the workspace as an active subscriber of that order.
	if err := svc.subs.Upsert(context.Background(), Subscription{
		TeamID: teamID, PlanID: "plan_pro_monthly", Status: "active", StripeSubscriptionID: "or_test9006",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := r.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("a stale ACTIVE resurrected a canceled subscription: plan = %q", got)
	}
}

// TestReconcileLoopStopsOnContextCancel: the loop is bound to the server's
// lifecycle, so cancelling that context must end the goroutine rather than leak it
// past the process's intent to run. Without this, shutdown would leave a poller
// hitting a third-party API.
func TestReconcileLoopStopsOnContextCancel(t *testing.T) {
	svc, _, _, gdb, _ := newTestEnv(t)
	r := NewReconciler(svc, stubOrders{}, NewIntentRepo(gdb), ocTierMap)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	// A long interval: if the loop only noticed cancellation on the next tick, this
	// would hang and the test would time out rather than pass slowly.
	go func() { r.Run(ctx, time.Hour); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile loop did not stop within 5s of its context being cancelled")
	}
}

// TestReconcileReturnsTheReadError: a failed read must not look like a quiet pass
// with nothing to do — the caller logs the report, and a silent zero would read as
// "nobody has paid".
func TestReconcileReturnsTheReadError(t *testing.T) {
	svc, _, _, gdb, _ := newTestEnv(t)
	r := NewReconciler(svc, stubOrders{err: context.DeadlineExceeded}, NewIntentRepo(gdb), ocTierMap)
	if _, err := r.ReconcileOnce(context.Background()); err == nil {
		t.Fatal("a failing order source produced no error")
	}
}
