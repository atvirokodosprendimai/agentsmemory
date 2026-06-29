package billing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	glebarez "github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testWebhookSecret = "whsec_test_secret"

// newTestEnv builds a billing Service over a throwaway migrated SQLite DB, so the
// real plans catalog (seeded by migrations 00002 + 00016) and subscriptions
// schema are exercised. The checkout API is faked — these tests never touch
// Stripe's network. A real tenant.Repo serves as the PlanStore, and a team row is
// inserted so SetTeamPlan has something to flip.
func newTestEnv(t *testing.T) (*Service, *fakeCheckout, *tenant.Repo, *gorm.DB, string) {
	t.Helper()
	gdb, err := gorm.Open(glebarez.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	// A single shared in-memory connection: SQLite drops the schema when the last
	// connection closes, so pin the pool to one conn for the test's lifetime.
	sqlDB.SetMaxOpenConns(1)
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	teamID := uuid.NewString()
	if err := gdb.Exec(
		"INSERT INTO teams (id, name, slug, kind, plan_id, created_at) VALUES (?,?,?,?,?,?)",
		teamID, "Acme", "acme-"+teamID[:6], "personal", tenant.FreePlanID,
		time.Now().UTC().Format(time.RFC3339),
	).Error; err != nil {
		t.Fatalf("seed team: %v", err)
	}

	tenants := tenant.NewRepo(gdb)
	fake := &fakeCheckout{url: "https://checkout.stripe.test/c/sess_123"}
	svc := &Service{
		cfg: Config{
			WebhookSecret:   testWebhookSecret,
			PriceByPlanCode: map[string]string{"pro_monthly": "price_m", "pro_annual": "price_y"},
		},
		plans:    tenants,
		subs:     NewRepo(gdb),
		checkout: fake,
	}
	return svc, fake, tenants, gdb, teamID
}

// fakeCheckout records the last checkoutInput and returns a canned URL.
type fakeCheckout struct {
	url  string
	last checkoutInput
}

func (f *fakeCheckout) createCheckout(_ context.Context, in checkoutInput) (string, error) {
	f.last = in
	return f.url, nil
}

// signedHeader builds a valid Stripe-Signature header for payload using secret,
// exactly as Stripe does (t=<unix>,v1=<hex hmac>), so ConstructEvent accepts it.
func signedHeader(payload []byte, secret string) string {
	now := time.Now()
	sig := webhook.ComputeSignature(now, payload, secret)
	return fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(sig))
}

// eventPayload marshals a minimal Stripe event envelope around object. The
// api_version is stamped to match the SDK constant, which ConstructEvent checks.
func eventPayload(eventType string, object map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{
		"id":          "evt_test",
		"type":        eventType,
		"api_version": stripe.APIVersion,
		"data":        map[string]any{"object": object},
	})
	return b
}

func planID(t *testing.T, tenants *tenant.Repo, code string) string {
	t.Helper()
	p, err := tenants.PlanByCode(context.Background(), code)
	if err != nil {
		t.Fatalf("plan %q: %v", code, err)
	}
	return p.ID
}

func teamPlanID(t *testing.T, gdb *gorm.DB, teamID string) string {
	t.Helper()
	var pid string
	if err := gdb.Raw("SELECT plan_id FROM teams WHERE id = ?", teamID).Scan(&pid).Error; err != nil {
		t.Fatalf("read team plan: %v", err)
	}
	return pid
}

func TestStartCheckout_UnknownPlan(t *testing.T) {
	svc, _, _, _, teamID := newTestEnv(t)
	// A plan code with no configured price is refused before any Stripe call.
	if _, err := svc.StartCheckout(context.Background(), CheckoutRequest{
		TeamID: teamID, PlanCode: "enterprise",
	}); err == nil {
		t.Fatal("expected ErrUnknownPlan for unpriced plan code")
	}
}

func TestStartCheckout_BuildsInput(t *testing.T) {
	svc, fake, _, _, teamID := newTestEnv(t)
	url, err := svc.StartCheckout(context.Background(), CheckoutRequest{
		TeamID: teamID, PlanCode: "pro_monthly", CustomerEmail: "a@b.co",
		SuccessURL: "https://app/ok", CancelURL: "https://app/no",
	})
	if err != nil {
		t.Fatalf("StartCheckout: %v", err)
	}
	if url != fake.url {
		t.Fatalf("url = %q, want %q", url, fake.url)
	}
	if fake.last.PriceID != "price_m" || fake.last.TeamID != teamID || fake.last.PlanCode != "pro_monthly" {
		t.Fatalf("checkout input not wired through: %+v", fake.last)
	}
}

func TestHandleWebhook_BadSignature(t *testing.T) {
	svc, _, _, _, teamID := newTestEnv(t)
	payload := eventPayload("checkout.session.completed", map[string]any{
		"client_reference_id": teamID,
		"metadata":            map[string]string{"plan_code": "pro_monthly"},
	})
	// A forged/empty signature must be rejected before any state changes.
	if err := svc.HandleWebhook(context.Background(), payload, "t=1,v1=deadbeef"); err == nil {
		t.Fatal("expected signature verification to fail")
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("plan changed on bad signature: %q", got)
	}
}

func TestHandleWebhook_CheckoutCompleted_Upgrades(t *testing.T) {
	svc, _, tenants, gdb, teamID := newTestEnv(t)
	want := planID(t, tenants, "pro_monthly")

	payload := eventPayload("checkout.session.completed", map[string]any{
		"client_reference_id": teamID,
		"metadata":            map[string]string{"plan_code": "pro_monthly"},
		"customer":            "cus_abc",
		"subscription":        "sub_abc",
	})
	if err := svc.HandleWebhook(context.Background(), payload, signedHeader(payload, testWebhookSecret)); err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}

	if got := teamPlanID(t, gdb, teamID); got != want {
		t.Fatalf("team plan = %q, want %q", got, want)
	}
	sub, err := svc.subs.ByTeam(context.Background(), teamID)
	if err != nil {
		t.Fatalf("ByTeam: %v", err)
	}
	if sub.Status != "active" || sub.StripeCustomerID != "cus_abc" || sub.StripeSubscriptionID != "sub_abc" {
		t.Fatalf("subscription not recorded correctly: %+v", sub)
	}
}

func TestHandleWebhook_Idempotent(t *testing.T) {
	svc, _, tenants, gdb, teamID := newTestEnv(t)
	want := planID(t, tenants, "pro_monthly")
	payload := eventPayload("checkout.session.completed", map[string]any{
		"client_reference_id": teamID,
		"metadata":            map[string]string{"plan_code": "pro_monthly"},
		"customer":            "cus_abc",
		"subscription":        "sub_abc",
	})
	// Stripe re-delivers; the second apply must converge, not duplicate.
	for i := 0; i < 2; i++ {
		if err := svc.HandleWebhook(context.Background(), payload, signedHeader(payload, testWebhookSecret)); err != nil {
			t.Fatalf("HandleWebhook #%d: %v", i, err)
		}
	}
	if got := teamPlanID(t, gdb, teamID); got != want {
		t.Fatalf("team plan = %q, want %q", got, want)
	}
	var n int64
	if err := gdb.Raw("SELECT COUNT(*) FROM subscriptions WHERE team_id = ?", teamID).Scan(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 subscription row, got %d", n)
	}
}

func TestHandleWebhook_SubscriptionDeleted_Downgrades(t *testing.T) {
	svc, _, _, gdb, teamID := newTestEnv(t)
	// First upgrade so there's a subscription to cancel.
	up := eventPayload("checkout.session.completed", map[string]any{
		"client_reference_id": teamID,
		"metadata":            map[string]string{"plan_code": "pro_annual"},
		"customer":            "cus_abc",
		"subscription":        "sub_xyz",
	})
	if err := svc.HandleWebhook(context.Background(), up, signedHeader(up, testWebhookSecret)); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	// Now Stripe says the subscription ended.
	del := eventPayload("customer.subscription.deleted", map[string]any{"id": "sub_xyz"})
	if err := svc.HandleWebhook(context.Background(), del, signedHeader(del, testWebhookSecret)); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := teamPlanID(t, gdb, teamID); got != tenant.FreePlanID {
		t.Fatalf("team not downgraded: plan = %q", got)
	}
	sub, err := svc.subs.ByTeam(context.Background(), teamID)
	if err != nil {
		t.Fatalf("ByTeam: %v", err)
	}
	if sub.Status != "canceled" {
		t.Fatalf("subscription status = %q, want canceled", sub.Status)
	}
}

func TestHandleWebhook_NoSecret_FailsClosed(t *testing.T) {
	svc, _, _, _, teamID := newTestEnv(t)
	svc.cfg.WebhookSecret = "" // simulate STRIPE_WEBHOOK_SECRET unset
	payload := eventPayload("checkout.session.completed", map[string]any{
		"client_reference_id": teamID,
		"metadata":            map[string]string{"plan_code": "pro_monthly"},
	})
	// Even a payload "signed" with the empty secret must be rejected — an
	// unconfigured secret fails closed, it never verifies.
	if err := svc.HandleWebhook(context.Background(), payload, signedHeader(payload, "")); err == nil {
		t.Fatal("expected fail-closed rejection when webhook secret is unset")
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("plan changed with no webhook secret configured: %q", got)
	}
}

func TestHandleWebhook_StaleCompletedAfterCancel_NoResurrect(t *testing.T) {
	svc, _, _, gdb, teamID := newTestEnv(t)
	ctx := context.Background()
	deliver := func(p []byte) {
		t.Helper()
		if err := svc.HandleWebhook(ctx, p, signedHeader(p, testWebhookSecret)); err != nil {
			t.Fatalf("HandleWebhook: %v", err)
		}
	}
	completed := eventPayload("checkout.session.completed", map[string]any{
		"client_reference_id": teamID,
		"metadata":            map[string]string{"plan_code": "pro_monthly"},
		"customer":            "cus_abc",
		"subscription":        "sub_x",
	})
	deleted := eventPayload("customer.subscription.deleted", map[string]any{"id": "sub_x"})

	deliver(completed) // upgrade
	deliver(deleted)   // cancel -> back to Free
	deliver(completed) // STALE re-delivery of the original completed event

	// The stale completed event must NOT resurrect the canceled subscription.
	if got := teamPlanID(t, gdb, teamID); got != tenant.FreePlanID {
		t.Fatalf("stale completed event resurrected Pro: plan = %q", got)
	}
	sub, err := svc.subs.ByTeam(ctx, teamID)
	if err != nil {
		t.Fatalf("ByTeam: %v", err)
	}
	if sub.Status != "canceled" {
		t.Fatalf("subscription status = %q, want canceled", sub.Status)
	}
}

func TestHandleWebhook_UnknownEvent_NoOp(t *testing.T) {
	svc, _, _, gdb, teamID := newTestEnv(t)
	payload := eventPayload("invoice.paid", map[string]any{"id": "in_1"})
	if err := svc.HandleWebhook(context.Background(), payload, signedHeader(payload, testWebhookSecret)); err != nil {
		t.Fatalf("unknown event should be a no-op, got: %v", err)
	}
	if got := teamPlanID(t, gdb, teamID); got != tenant.FreePlanID {
		t.Fatalf("plan changed on unrelated event: %q", got)
	}
}
