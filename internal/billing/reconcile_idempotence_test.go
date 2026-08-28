package billing

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestReconcileDoesNotRevertAnOperatorDowngrade reproduces B1 from the PR #96
// review: `set-plan` writes only teams.plan_id, and applyActivated's only
// re-delivery guard is "the subscription row says canceled". After an operator
// downgrades, the row still reads active and the provider order is still PAID, so
// the next pass re-applies it — forever, with a routine "1 activated" in the log.
//
// A webhook fires once; a poll fires every interval. That difference is why the
// stateless-per-pass design was safe for Stripe and is not safe here.
func TestReconcileDoesNotRevertAnOperatorDowngrade(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_b1", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly",
			Tags: []string{intentTag(teamID)}, FromAccountSlug: "jane",
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")
	ctx := context.Background()

	if _, err := r.ReconcileOnce(ctx); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got == tenant.FreePlanID {
		t.Fatal("first pass did not activate")
	}

	// The operator downgrades, exactly as the ADR's rollback and the reconciler's
	// own "left for manual attribution with set-plan" log line instruct.
	if err := svc.plans.SetTeamPlan(ctx, teamID, tenant.FreePlanID); err != nil {
		t.Fatalf("set-plan: %v", err)
	}

	if _, err := r.ReconcileOnce(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("the operator's downgrade was reverted by the next reconcile pass: plan = %q", got)
	}
}

// TestReconcileDoesNotGrantARecurringPlanForAOneOff reproduces B1's corollary: a
// ONETIME contribution to a recurring tier is PAID forever on the provider, and
// providerOrder.Frequency is decoded and read by nothing — so a single payment
// grants Pro for as long as the collective exists.
func TestReconcileDoesNotGrantARecurringPlanForAOneOff(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_b1b", Status: "PAID", Frequency: "ONETIME", TierSlug: "pro-monthly",
			Tags: []string{intentTag(teamID)},
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")

	rep, err := r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 0 {
		t.Errorf("a ONETIME contribution activated a recurring plan: %+v", rep)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("a one-off payment granted a recurring plan: %q", got)
	}
}

// TestLedgerRecordsOnlyDecisionsActuallyTaken pins N1 from the self-review. The
// ledger is a record of what THIS SERVER DID; an entry for something it declined to
// do is a false record, and the next person to debug from it would be reading a
// decision that never happened.
//
// The case: a stale ACTIVE arrives for an order already recorded canceled.
// applyActivated's guard correctly declines it — a success that changed nothing —
// and that must leave no ledger row.
func TestLedgerRecordsOnlyDecisionsActuallyTaken(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_declined", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly",
			Tags: []string{intentTag(teamID)},
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")
	ctx := context.Background()

	// The workspace already cancelled this exact subscription.
	if err := svc.subs.Upsert(ctx, Subscription{
		TeamID: teamID, PlanID: tenant.FreePlanID, Status: "canceled",
		StripeSubscriptionID: "or_declined",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rep, err := r.ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 0 {
		t.Fatalf("a stale ACTIVE for a canceled subscription was counted as an activation: %+v", rep)
	}

	var rows int64
	if err := gdb.Raw("SELECT COUNT(*) FROM billing_applied_orders WHERE order_id = ?", "or_declined").
		Scan(&rows).Error; err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if rows != 0 {
		t.Fatal("the ledger recorded an activation the stale-re-delivery guard declined: it claims a decision this server refused to take")
	}
}

// TestApplyIsIdempotentWithoutTheLedger restores coverage the ledger silently took
// away, found by self-review of PR #96 (F2).
//
// `TestReconcileIsIdempotent` runs three passes and asserts one subscription row.
// Since the ledger landed, passes 2 and 3 return `Ignored:1` and never reach
// `applyActivated` at all — so that test now measures the ledger's skip, not the
// convergence of the apply. Proven by mutation: breaking `Repo.Upsert`'s ON CONFLICT
// left its row-count assertion GREEN, and it went red only via an incidental
// CurrentPeriodEnd check on a different mechanism.
//
// The underlying property still matters and is now unheld anywhere else: the plan
// flip and the subscription upsert must converge when the SAME event is applied
// twice. The webhook path still does exactly that on a provider re-delivery, where
// there is no ledger in front of it. So this exercises the reconciler with the
// ledger deliberately absent — which `WithLedger` being optional already allows.
func TestApplyIsIdempotentWithoutTheLedger(t *testing.T) {
	svc, _, _, gdb, teamID := newTestEnv(t)
	intents := NewIntentRepo(gdb)
	svc.intents = intents
	// No WithLedger: every pass reaches applyActivated, as a webhook re-delivery does.
	r := NewReconciler(svc, stubOrders{orders: []providerOrder{{
		ID: "or_noledger", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly",
		Tags: []string{intentTag(teamID)}, NextChargeDate: "2026-09-20T09:11:02Z",
	}}}, intents, ocTierMap)
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		rep, err := r.ReconcileOnce(ctx)
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		// Every pass must genuinely apply — otherwise something is skipping and this
		// test would be measuring a short-circuit again, exactly like the one it
		// replaces.
		if rep.Activated != 1 {
			t.Fatalf("pass %d did not reach the apply (%+v); this test must exercise it, not a skip", i, rep)
		}
	}

	var count int64
	if err := gdb.Raw("SELECT COUNT(*) FROM subscriptions WHERE team_id = ?", teamID).Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("three applies of the same event produced %d subscription rows, want 1: the upsert is not converging", count)
	}
	sub, err := svc.subs.ByTeam(ctx, teamID)
	if err != nil {
		t.Fatalf("ByTeam: %v", err)
	}
	if sub.Status != "active" || sub.CurrentPeriodEnd != "2026-09-20T09:11:02Z" {
		t.Fatalf("repeated applies did not converge on one state: %+v", sub)
	}
}

// TestUnknownFrequencyIsNotTreatedAsRecurring pins the tightened recurrence guard.
//
// `Order.frequency` is NULLABLE in Open Collective's published schema, so "we cannot
// tell whether this recurs" is a state the API can genuinely produce. An earlier
// version admitted it — `o.Frequency != "" && …` — which defeated the guard in
// exactly the case it exists for. That escape was not a decision: it was nine test
// fixtures that omitted the field, and the production code had been shaped to keep
// them green.
//
// Refusing costs a log line and one `set-plan`. Admitting costs a plan granted for
// as long as the collective exists, that nobody is billed for and nobody notices.
func TestUnknownFrequencyIsNotTreatedAsRecurring(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			// No Frequency: a null from the provider, not a one-off.
			ID: "or_nofreq", Status: "ACTIVE", TierSlug: "pro-monthly",
			Tags: []string{intentTag(teamID)},
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")

	rep, err := r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 0 {
		t.Errorf("an order with no stated recurrence activated a recurring plan: %+v", rep)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("unknown recurrence granted a plan: %q", got)
	}
}

// TestEmailFallbackRefusesAnAmbiguousMatch reproduces B2: MatchByEmail is scoped to
// (email, plan) across EVERY workspace and ordered created_at DESC, so with one
// email and two workspaces the payment lands on whichever clicked Upgrade last.
//
// This needs no attacker to be a real defect — one person with a personal and a team
// workspace, clicking both and paying once, is an ordinary support ticket. It is
// worse with one, because tenant.CreateUserWithPassword performs no email
// verification, so an address can be registered by someone who does not own it.
//
// And it is the PRIMARY channel in practice, not the fallback: if the tag does not
// survive the hosted checkout — still unproven — this is the only path that resolves.
func TestEmailFallbackRefusesAnAmbiguousMatch(t *testing.T) {
	_, _, _, gdb, _ := newTestEnv(t)
	intents := NewIntentRepo(gdb)
	ctx := context.Background()

	// Two workspaces, one email, both with an open intent for the same plan.
	for _, team := range []string{"victim-team", "attacker-team"} {
		if err := intents.Record(ctx, CheckoutIntent{
			TeamID: team, PlanCode: "pro_monthly",
			Tag: intentTag(team), Email: "shared@example.com",
		}); err != nil {
			t.Fatalf("record intent for %s: %v", team, err)
		}
	}

	// The contribution carries no tag, so the email channel decides. It must refuse:
	// "when neither resolves, the answer is we do not know" is the file's own stated
	// principle, and this is a case it currently resolves by guessing.
	if got, err := intents.MatchByEmail(ctx, "shared@example.com", "pro_monthly"); err == nil {
		t.Fatalf("an ambiguous email matched workspace %q; two workspaces share that address and nothing ties the intent to the payer", got.TeamID)
	}
}
