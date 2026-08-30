package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// End-to-end across the whole ADR-042 chain, with a fake OpenCollective:
// upgrade click -> attributed checkout URL -> contribution appears in the API ->
// reconcile -> workspace is on Pro. Nothing is stubbed except the provider itself.
func TestEndToEndOpenCollectiveActivation(t *testing.T) {
	svc, _, _, gdb, teamID := newTestEnv(t)
	oc := newOpenCollectiveProvider(Config{
		PriceByPlanCode: map[string]string{"pro_monthly": testOCMonthlyURL},
	})
	svc.checkout, svc.webhook, svc.portal = oc, oc, oc
	svc.intents = NewIntentRepo(gdb)

	// 1. The user clicks Upgrade.
	raw, err := svc.StartCheckout(context.Background(), CheckoutRequest{
		TeamID: teamID, PlanCode: "pro_monthly", CustomerEmail: "buyer@example.com",
		SuccessURL: "https://app.example/ok",
	})
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	u, _ := url.Parse(raw)
	tag := u.Query().Get("tags")
	if tag == "" {
		t.Fatal("checkout carried no attribution tag")
	}

	// 2. They pay. OpenCollective now reports the order, echoing the tag back.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"data": map[string]any{"account": map[string]any{"orders": map[string]any{
			"totalCount": 1,
			"nodes": []any{map[string]any{
				"publicId": "or_e2eTestOrder001", "status": "ACTIVE", "frequency": "MONTHLY",
				"createdAt": "2026-08-28T10:00:00Z", "nextChargeDate": "2026-09-28T10:00:00Z",
				"amount":      map[string]any{"value": 50, "currency": "EUR"},
				"tier":        map[string]any{"legacyId": 104934, "slug": "pro-monthly"},
				"fromAccount": map[string]any{"slug": "buyer", "name": "B", "type": "INDIVIDUAL", "email": "buyer@example.com"},
				"tags":        []string{tag},
			}},
		}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// 3. One reconcile pass.
	rec := NewReconciler(svc, NewOCOrderSource(srv.Client(), srv.URL, "tok", "ai-agents-memory"),
		NewIntentRepo(gdb), map[string]string{"pro-monthly": "pro_monthly"}).WithLedger(NewAppliedOrderRepo(gdb))
	rep, err := rec.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 1 {
		t.Fatalf("want 1 activated, got %+v", rep)
	}

	// 4. The workspace is on Pro, with a real subscription row and a period end.
	if got := teamPlanID(t, svc.subs.db, teamID); got == tenant.FreePlanID {
		t.Fatal("workspace is still on Free after a paid, attributed contribution")
	}
	sub, err := svc.subs.ByTeam(context.Background(), teamID)
	if err != nil {
		t.Fatalf("ByTeam: %v", err)
	}
	if sub.StripeSubscriptionID != "or_e2eTestOrder001" {
		t.Fatalf("order id not recorded: %+v", sub)
	}
	if sub.CurrentPeriodEnd != "2026-09-28T10:00:00Z" {
		t.Fatalf("period end not recorded: %q", sub.CurrentPeriodEnd)
	}

	// 5. And the Manage button now has something to manage (T1's gate).
	if !svc.HasRelationship(context.Background(), teamID) {
		t.Fatal("HasRelationship is false after activation: the Manage card would still be hidden")
	}
	if !strings.HasPrefix(tag, "am-") {
		t.Fatalf("unexpected tag shape %q", tag)
	}
}
