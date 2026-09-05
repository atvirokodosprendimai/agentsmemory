package web

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/billing"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newBillingGateEnv builds a Server over a migrated in-memory database with one
// admin user on one workspace, so the billing controls can be exercised through
// the real projectsForUser path rather than through a reimplementation of its
// logic. ADR-042-T1: the defect being fixed is that CanManage was computed from
// the plan alone, and only the real path proves the subscription lookup is
// actually consulted.
func newBillingGateEnv(t *testing.T, planID string) (*Server, *gorm.DB, string, string) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	// One shared connection: SQLite drops an in-memory schema when the last
	// connection closes, so the pool is pinned for the test's lifetime.
	sqlDB.SetMaxOpenConns(1)
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Closed because the handle is the TEST's, not the process's. This palace is
	// :memory:, so there is no file to unlink and no TempDir cleanup waiting —
	// the sibling helpers' reason (#162) does not apply here and saying it would
	// be false. What makes it right instead: an in-memory database lives on
	// its CONNECTION, so the handle is the schema, and the helper that opened it
	// owns its lifetime.
	t.Cleanup(func() { _ = sqlDB.Close() })

	now := time.Now().UTC().Format(time.RFC3339)
	teamID, userID := uuid.NewString(), uuid.NewString()
	if err := gdb.Exec(
		"INSERT INTO teams (id, name, slug, kind, plan_id, created_at) VALUES (?,?,?,?,?,?)",
		teamID, "Acme", "acme-"+teamID[:6], "personal", planID, now,
	).Error; err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if err := gdb.Exec(
		"INSERT INTO users (id, email, password_hash, display_name, created_at) VALUES (?,?,?,?,?)",
		userID, "a@b.co", "x", "A", now,
	).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := gdb.Exec(
		"INSERT INTO memberships (id, team_id, user_id, role, created_at) VALUES (?,?,?,?,?)",
		uuid.NewString(), teamID, userID, string(tenant.RoleAdmin), now,
	).Error; err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	tenants := tenant.NewRepo(gdb)
	subs := billing.NewRepo(gdb)
	// A provider must be configured or Enabled() is false and both controls are
	// hidden for a reason unrelated to what this test is about.
	billingSvc := billing.NewService(billing.Config{
		Provider:                 billing.ProviderOpenCollective,
		PriceByPlanCode:          map[string]string{"pro_monthly": "https://opencollective.example/checkout"},
		OpenCollectiveProjectURL: "https://opencollective.example/project",
	}, tenants, subs)

	srv := &Server{
		tenants: tenants,
		usage:   usage.NewService(usage.NewRepo(gdb), tenants),
		billing: billingSvc,
	}
	return srv, gdb, userID, teamID
}

// TestCanManageRequiresARecordedSubscription pins ADR-042's central defect: under
// OpenCollective nothing can write a subscriptions row (parseWebhook fails closed
// and set-plan touches only teams.plan_id), so gating the Manage card on the plan
// alone renders a button whose handler can only ever return ErrNoSubscription.
// The card must appear only when a provider relationship is actually recorded.
func TestCanManageRequiresARecordedSubscription(t *testing.T) {
	srv, gdb, userID, teamID := newBillingGateEnv(t, "plan_pro_monthly")

	projects, err := srv.projectsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("projectsForUser: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("want 1 project, got %d", len(projects))
	}
	if projects[0].CanManage {
		t.Fatal("CanManage is true with no subscriptions row: this renders a Manage button whose handler can only fail")
	}

	// With a recorded relationship — what a reconciled or Stripe-backed workspace
	// has — the control must come back.
	if err := billing.NewRepo(gdb).Upsert(context.Background(), billing.Subscription{
		TeamID: teamID, PlanID: "plan_pro_monthly", Status: "active",
		StripeSubscriptionID: "order_oc_1",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	projects, err = srv.projectsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("projectsForUser: %v", err)
	}
	if !projects[0].CanManage {
		t.Fatal("CanManage is false for a paid workspace WITH a recorded subscription")
	}
}

// TestCanUpgradeIsUnaffectedByTheRelationshipGate keeps the fix narrow: the
// upgrade path must still be offered on the free plan, where by definition no
// subscription exists. Without this, tightening CanManage could plausibly be
// "fixed" by a change that also suppresses the upgrade button.
func TestCanUpgradeIsUnaffectedByTheRelationshipGate(t *testing.T) {
	srv, _, userID, _ := newBillingGateEnv(t, tenant.FreePlanID)

	projects, err := srv.projectsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("projectsForUser: %v", err)
	}
	if !projects[0].CanUpgrade {
		t.Fatal("CanUpgrade is false on the free plan with billing configured")
	}
	if projects[0].CanManage {
		t.Fatal("CanManage is true on the free plan")
	}
}

// TestAFixedDeploymentCapSuppressesTheUpgradeControl covers the seam a purchase
// actually goes through, which is where the harm is.
//
// capLookupFor returns usage.FixedCap for every nonzero --monthly-request-cap, so
// under an override teams.plan_id no longer decides the enforced cap. The upgrade
// card was computed from the plan row alone: a user could pay, the plan could flip
// successfully, and the cap they bought the lift for would not move. With a
// NEGATIVE override the same control offers a paid lift from a cap that is already
// unlimited.
//
// The startup check in run refuses that combination outright, so this is the
// second of two independent guards rather than the only one. It is worth having
// separately because the two fail differently: the startup check protects a
// process an operator configures, and this protects the model a template renders
// — and only this one is exercised by the path a browser takes.
func TestAFixedDeploymentCapSuppressesTheUpgradeControl(t *testing.T) {
	srv, gdb, userID, _ := newBillingGateEnv(t, tenant.FreePlanID)

	// Baseline: the control is offered, so a later absence means something.
	projects, err := srv.projectsForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("projectsForUser: %v", err)
	}
	if !projects[0].CanUpgrade {
		t.Fatal("CanUpgrade is false on the free plan with billing configured — the probe below would prove nothing")
	}

	for _, cap := range []usage.FixedCap{50, -1} {
		t.Run(strconv.Itoa(int(cap)), func(t *testing.T) {
			srv.usage = usage.NewService(usage.NewRepo(gdb), cap)
			projects, err := srv.projectsForUser(context.Background(), userID)
			if err != nil {
				t.Fatalf("projectsForUser: %v", err)
			}
			if projects[0].CanUpgrade {
				t.Errorf("CanUpgrade is true under a fixed deployment cap of %d: the checkout would "+
					"take money, flip teams.plan_id, and leave the enforced cap exactly where it was",
					int(cap))
			}
		})
	}
}
